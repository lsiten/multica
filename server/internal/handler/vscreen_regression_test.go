package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenRegressionCommandRejectsTrailingBody(t *testing.T) {
	const daemonID = "review-command-daemon"
	runtimeID := dbfx.Runtime(t, "Review command", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "review", "device_info": "test", "visibility": "private", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["virtual-screen-v1","mirror-viewer-grant-v1"]}'::jsonb`)})
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	conn := connectMirrorViewerDaemon(t, &h, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	for _, tc := range []struct{ name, trailing string }{{"second_json", ` {"kind":"disable"}`}, {"invalid_suffix", " garbage"}, {"oversized_trailing_data", strings.Repeat(" ", 8192)}} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"command_id":%q,"kind":"enable"}`, tc.name) + tc.trailing
			req := withURLParam(newRequest(http.MethodPost, "/commands", nil), "runtimeId", runtimeID)
			req.Body = io.NopCloser(strings.NewReader(body))
			req.ContentLength = int64(len(body))
			res := testutil.Call(t, h.CreateVscreenCommand, req).Want(http.StatusBadRequest)
			t.Logf("body_bytes=%d HTTP=%d response=%s", len(body), res.Code, res.Body.String())
			if !strings.Contains(res.Body.String(), "invalid_command") {
				t.Fatal("missing invalid_command reason")
			}
		})
	}
	assertVscreenNoFrame(t, conn)
}

type vscreenRegressionClock struct{ now time.Time }

func (c *vscreenRegressionClock) Now() time.Time { return c.now }

func TestVscreenRegressionManagedCloseRejectsLateAnswer(t *testing.T) {
	for _, signaling := range []string{"live", "expired", "purged"} {
		t.Run(signaling, func(t *testing.T) { testVscreenRegressionManagedClose(t, signaling) })
	}
}

func testVscreenRegressionManagedClose(t *testing.T, signaling string) {
	// Given: a managed viewer with independent signaling lifetime.
	const daemonID = "review-close-daemon"
	runtimeID := dbfx.Runtime(t, "Review close", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "review", "device_info": "test", "visibility": "private", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["screen-mirror-v1","mirror-viewer-grant-v1"]}'::jsonb`)})
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	h.MirrorGrants = mirror.NewViewerGrantStore()
	clock := &vscreenRegressionClock{now: time.Now()}
	h.MirrorSessions = mirror.NewSessionStore(mirror.SessionStoreOptions{Clock: clock})
	identity := mirror.SessionIdentity{WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UserID: testUserID, DaemonID: daemonID, ViewerID: "review-viewer"}
	session, err := h.MirrorSessions.Create(context.Background(), mirror.CreateSessionInput{Identity: identity, Offer: protocol.MirrorSessionDescription{Type: "offer", SDP: "offer-sdp"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.MirrorSessions.ConsumeOffer(context.Background(), session.ID, identity); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	grant := protocol.MirrorViewerGrant{GrantID: "review-grant", SessionID: session.ID, WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UserID: testUserID, ViewerID: identity.ViewerID, NativeEpoch: "native", Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "primary"}, SourceGeneration: "display", ExpiresAt: now.Add(30 * time.Second)}
	if err := h.MirrorGrants.Add(mirror.ViewerGrantRecord{Grant: grant, DaemonID: daemonID, DaemonGeneration: "connection", Active: true}, now); err != nil {
		t.Fatal(err)
	}
	if signaling != "live" {
		clock.now = clock.now.Add(time.Minute)
	}
	if signaling == "purged" {
		if count, err := h.MirrorSessions.PurgeExpired(context.Background()); err != nil || count != 1 {
			t.Fatalf("purge count=%d error=%v", count, err)
		}
	}
	// When: the viewer closes after offer consumption.
	req := withURLParams(newRequest(http.MethodDelete, "/mirror/sessions/"+session.ID+"?viewer_id="+identity.ViewerID, nil), "runtimeId", runtimeID, "sessionId", session.ID)
	res := testutil.Call(t, h.CloseMirrorSession, req).Want(http.StatusNoContent)
	if res.Code != http.StatusNoContent {
		t.Fatalf("close HTTP=%d body=%s", res.Code, res.Body.String())
	}
	// Then: late signaling is rejected even if the SDP lifetime ended first.
	err = h.HandleDaemonMirrorAnswer(context.Background(), daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}, protocol.MirrorAnswerPayload{SessionID: session.ID, WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UserID: testUserID, DaemonID: daemonID, ViewerID: identity.ViewerID, Answer: protocol.MirrorSessionDescription{Type: "answer", SDP: "late-answer-sdp"}})
	t.Logf("DELETE HTTP=%d; late daemon answer error=%v", res.Code, err)
	if len(h.MirrorGrants.Records()) != 0 {
		t.Fatal("closed grant retained")
	}
	if err == nil {
		t.Fatal("closed session accepted late answer")
	}
	get := withURLParams(newRequest(http.MethodGet, "/mirror/sessions/"+session.ID, nil), "runtimeId", runtimeID, "sessionId", session.ID)
	getRes := testutil.Call(t, h.GetMirrorSession, get)
	t.Logf("GET after DELETE HTTP=%d response=%s", getRes.Code, getRes.Body.String())
	var response mirrorSessionResponse
	if err := json.Unmarshal(getRes.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Answer != nil {
		t.Errorf("closed managed session exposes a late SDP answer")
	}
}

func TestVscreenRegressionPATGrantCreationRevalidatesCache(t *testing.T) {
	for _, revoked := range []bool{true, false} {
		t.Run(fmt.Sprintf("revoked=%v", revoked), func(t *testing.T) {
			const daemonID = "review-pat-daemon"
			runtimeID := dbfx.Runtime(t, "Review PAT", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "review", "device_info": "test", "visibility": "private", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["screen-mirror-v1","mirror-viewer-grant-v1"]}'::jsonb`)})
			h := *testHandler
			h.DaemonHub = daemonws.NewHub()
			h.MirrorGrants = mirror.NewViewerGrantStore()
			h.MirrorSessions = mirror.NewSessionStore(mirror.SessionStoreOptions{})
			conn := connectMirrorViewerDaemon(t, &h, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			respondToQuery := func() {
				var frame protocol.Message
				if err := conn.ReadJSON(&frame); err != nil {
					done <- err
					return
				}
				var query protocol.VscreenQuery
				if err := json.Unmarshal(frame.Payload, &query); err != nil {
					done <- err
					return
				}
				source := protocol.VscreenSourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "http://localhost:18234", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UID: 501}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "primary"}, NativeEpoch: "native", Generation: "display", Primary: true}, Scale: 1, GeometryRevision: 1}
				raw, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Sources: []protocol.VscreenSourceDescriptor{source}})
				if err != nil {
					done <- err
					return
				}
				done <- conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: raw})
			}
			go respondToQuery()
			rdb := newRedisTestClient(t)
			if err := rdb.Ping(context.Background()).Err(); err != nil {
				t.Fatal(err)
			}
			cache := auth.NewPATCache(rdb)
			patExpiry := time.Now().Add(5 * time.Second)
			if revoked {
				patExpiry = time.Now().Add(time.Hour)
			}
			rawToken, patID := insertTestPAT(t, patExpiry)
			warm := newRequest(http.MethodGet, "/warm-cache", nil)
			warm.Header.Set("Authorization", "Bearer "+rawToken)
			testutil.Call(t, middleware.Auth(h.Queries, cache, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP, warm).Want(http.StatusNoContent)
			if revoked {
				dbfx.Exec(t, "UPDATE personal_access_token SET revoked = TRUE WHERE id = $1", patID)
			}
			body := map[string]any{"viewer_id": "pat-viewer", "offer": map[string]string{"type": "offer", "sdp": "offer-sdp"}}
			req := withURLParam(newRequest(http.MethodPost, "/mirror/sessions", body), "runtimeId", runtimeID)
			req.Header.Set("Authorization", "Bearer "+rawToken)
			res := testutil.Call(t, middleware.Auth(h.Queries, cache, nil)(http.HandlerFunc(h.CreateMirrorSession)).ServeHTTP, req)
			t.Logf("cached PAT revoked=%v HTTP=%d response=%s", revoked, res.Code, res.Body.String())
			if revoked {
				if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "viewer_revoked") {
					t.Fatalf("revoked PAT HTTP=%d body=%s", res.Code, res.Body.String())
				}
				if len(h.MirrorGrants.Records()) != 0 {
					t.Fatal("revoked PAT stored a grant")
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				assertVscreenNoFrame(t, conn)
				return
			}
			if res.Code != http.StatusCreated {
				t.Fatalf("valid PAT HTTP=%d body=%s", res.Code, res.Body.String())
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var offer protocol.Message
			if err := conn.ReadJSON(&offer); err != nil {
				t.Fatal(err)
			}
			t.Logf("actual daemon WS type=%s", offer.Type)
			var response mirrorSessionResponse
			if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			record, err := h.MirrorGrants.Lookup(response.ID, testUserID, runtimeID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			validationErr := h.validateViewerCredential(context.Background(), record)
			t.Logf("fresh credential validation error=%v; grant expiry beyond PAT=%v", validationErr, record.Grant.ExpiresAt.Sub(patExpiry))
			if revoked {
				t.Error("cached revoked PAT created a managed viewer and dispatched its offer")
			}
			if !revoked && record.Grant.ExpiresAt.After(patExpiry) {
				t.Error("cached PAT loses expiry: grant outlives the credential")
			}
			// When: the PAT expiry is shortened before renewal.
			patExpiry = time.Now().Add(2 * time.Second)
			dbfx.Exec(t, "UPDATE personal_access_token SET expires_at = $1 WHERE id = $2", patExpiry, patID)
			h.MirrorGrants.SetActive(daemonID, runtimeID, "pat-viewer", true)
			go respondToQuery()
			renewReq := withURLParams(newRequest(http.MethodPost, "/renew", nil), "runtimeId", runtimeID, "sessionId", response.ID)
			renewReq.Header.Set("Authorization", "Bearer "+rawToken)
			renewedResponse := testutil.Call(t, middleware.Auth(h.Queries, cache, nil)(http.HandlerFunc(h.RenewMirrorSession)).ServeHTTP, renewReq).Want(http.StatusOK)
			var renewed protocol.MirrorViewerGrant
			renewedResponse.JSON(&renewed)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var renewalFrame protocol.Message
			if err := conn.ReadJSON(&renewalFrame); err != nil {
				t.Fatal(err)
			}
			t.Logf("PAT renewal HTTP=%d body=%s WS=%s payload=%s", renewedResponse.Code, renewedResponse.Body.String(), renewalFrame.Type, renewalFrame.Payload)
			// Then: the fresh database expiry bounds the wire grant.
			if renewed.ExpiresAt.After(patExpiry) {
				t.Fatal("renewal outlives current PAT expiry")
			}

		})
	}
}

func TestVscreenRegressionConcurrentRenewKeepsViewer(t *testing.T) {
	const daemonID = "review-renew-daemon"
	runtimeID := dbfx.Runtime(t, "Review concurrent renew", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "review", "device_info": "test", "visibility": "private", "status": "online"})
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	h.MirrorGrants = mirror.NewViewerGrantStore()
	conn := connectMirrorViewerDaemon(t, &h, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	bootstrap := make(chan error, 1)
	go func() {
		_, err := h.DaemonHub.QueryVscreen(context.Background(), testWorkspaceID, runtimeID, daemonID, "sources")
		bootstrap <- err
	}()
	readQuery := func() protocol.VscreenQuery {
		var frame protocol.Message
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != protocol.EventVscreenQuery {
			t.Fatalf("wanted query, got %s", frame.Type)
		}
		var query protocol.VscreenQuery
		if err := json.Unmarshal(frame.Payload, &query); err != nil {
			t.Fatal(err)
		}
		return query
	}
	respond := func(query protocol.VscreenQuery) {
		source := protocol.VscreenSourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "http://localhost:18234", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UID: 501}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "primary"}, NativeEpoch: "native", Generation: "display", Primary: true}, Scale: 1, GeometryRevision: 1}
		raw, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Sources: []protocol.VscreenSourceDescriptor{source}})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: raw}); err != nil {
			t.Fatal(err)
		}
	}
	query := readQuery()
	respond(query)
	if err := <-bootstrap; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	expiry := now.Add(time.Hour)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": testUserID, "exp": expiry.Unix()}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatal(err)
	}
	grant := protocol.MirrorViewerGrant{GrantID: "concurrent-grant", SessionID: "concurrent-session", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UserID: testUserID, ViewerID: "concurrent-viewer", NativeEpoch: "native", Source: protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "primary"}, SourceGeneration: "display", ExpiresAt: now.Add(30 * time.Second)}
	record := mirror.ViewerGrantRecord{Grant: grant, DaemonID: daemonID, DaemonGeneration: query.DaemonGeneration, CredentialHash: auth.HashToken(token), CredentialKind: "jwt", CredentialExpiry: expiry, Active: true}
	if err := h.MirrorGrants.Add(record, now); err != nil {
		t.Fatal(err)
	}
	done := make(chan *testutil.Response, 2)
	for i := 0; i < 2; i++ {
		req := withURLParams(newRequest(http.MethodPost, "/mirror/sessions/concurrent-session/renew", nil), "runtimeId", runtimeID, "sessionId", grant.SessionID)
		req.Header.Set("Authorization", "Bearer "+token)
		go func() {
			res := testutil.Call(t, middleware.Auth(h.Queries, nil, nil)(http.HandlerFunc(h.RenewMirrorSession)).ServeHTTP, req)
			done <- res
		}()
	}
	// Both requests have captured the same grant before either can complete its native query.
	first, second := readQuery(), readQuery()
	respond(first)
	respond(second)
	for i := 0; i < 2; i++ {
		res := <-done
		t.Logf("concurrent renew HTTP=%d body=%s", res.Code, res.Body.String())
		if res.Code != http.StatusOK {
			t.Errorf("valid renewal failed: %d", res.Code)
		}
	}
	for i := 0; i < 2; i++ {
		var frame protocol.Message
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		t.Logf("actual daemon WS type=%s payload=%s", frame.Type, frame.Payload)
		if frame.Type != protocol.EventMirrorViewerRenew {
			t.Errorf("wanted renewal, got %s", frame.Type)
		}
	}
	if _, err := h.MirrorGrants.Lookup(grant.SessionID, testUserID, runtimeID, time.Now()); err != nil {
		t.Errorf("overlapping valid renewals revoked their viewer: %v", err)
	}
}

func assertVscreenNoFrame(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	err := conn.ReadJSON(&frame)
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("unexpected daemon frame=%s payload=%s error=%v", frame.Type, frame.Payload, err)
	}
	t.Log("daemon WS: no command or offer dispatched during observation window")
}
