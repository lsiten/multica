package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenManagedViewerRenewAndMemberRevocation(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) { testVscreenManagedViewerRenewAndMemberRevocation(t, version) })
	}
}

func testVscreenManagedViewerRenewAndMemberRevocation(t *testing.T, version int) {
	// Given: a public runtime, signed viewer JWT, and real daemon WebSocket.
	const daemonID = "vscreen-grant-daemon"
	runtimeID := dbfx.Runtime(t, "Vscreen grant", testutil.Cols{"workspace_id": testWorkspaceID, "owner_id": testUserID, "daemon_id": daemonID, "provider": "vscreen-grant", "device_info": "test", "visibility": "public", "status": "online", "metadata": testutil.Raw(`'{"capabilities":["screen-mirror-v1","virtual-screen-v1","mirror-viewer-grant-v1","screen-mirror-video-v2"]}'::jsonb`)})
	userID := createSecondWorkspaceMember(t)
	h := *testHandler
	h.DaemonHub = daemonws.NewHub()
	h.MirrorGrants = mirror.NewViewerGrantStore()
	h.MirrorSessions = mirror.NewSessionStore(mirror.SessionStoreOptions{})
	conn := connectMirrorViewerDaemon(t, &h, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	frames := make(chan protocol.Message, 8)
	daemonErrors := make(chan error, 1)
	go func() {
		for {
			var frame protocol.Message
			if err := conn.ReadJSON(&frame); err != nil {
				daemonErrors <- err
				return
			}
			if frame.Type != protocol.EventVscreenQuery {
				frames <- frame
				continue
			}
			var query protocol.VscreenQuery
			if err := json.Unmarshal(frame.Payload, &query); err != nil {
				daemonErrors <- err
				return
			}
			source := protocol.VscreenSourceDescriptor{MirrorSourceBinding: protocol.MirrorSourceBinding{Resource: protocol.ResourceKey{BackendIdentity: "http://localhost:18234", WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, UID: 501}, Source: protocol.MirrorSource{Kind: protocol.MirrorSourceVirtual, SourceID: "virtual"}, NativeEpoch: "native", Generation: "display"}, Name: "Virtual screen", Width: 1920, Height: 1080, Scale: 1}
			if version == 1 {
				source.Source = protocol.MirrorSource{Kind: protocol.MirrorSourcePhysical, SourceID: "primary"}
				source.Primary = true
			}
			raw, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Sources: []protocol.VscreenSourceDescriptor{source}})
			if err != nil {
				daemonErrors <- err
				return
			}
			if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: raw}); err != nil {
				daemonErrors <- err
				return
			}
		}
	}()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": userID, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"viewer_id": "viewer", "offer": map[string]string{"type": "offer", "sdp": "offer-sdp"}, "protocol_version": 2, "transport": "video", "source": map[string]string{"kind": "virtual", "source_id": "virtual"}, "source_generation": "display"}
	if version == 1 {
		delete(body, "protocol_version")
		delete(body, "transport")
		delete(body, "source")
		delete(body, "source_generation")
	}
	req := withURLParam(newRequestAs(userID, http.MethodPost, "/mirror/sessions", body), "runtimeId", runtimeID)
	req.Header.Set("Authorization", "Bearer "+token)
	var created mirrorSessionResponse
	testutil.Call(t, middleware.Auth(h.Queries, nil, nil, nil)(http.HandlerFunc(h.CreateMirrorSession)).ServeHTTP, req).Want(http.StatusCreated).JSON(&created)
	readFrame := func(want string) protocol.Message {
		t.Helper()
		select {
		case frame := <-frames:
			if frame.Type != want {
				t.Fatalf("type=%s want %s", frame.Type, want)
			}
			t.Logf("wire %s: %s", frame.Type, frame.Payload)
			return frame
		case err := <-daemonErrors:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatal("missing daemon frame")
		}
		return protocol.Message{}
	}
	offerFrame := readFrame(protocol.EventMirrorOffer)
	var offer protocol.MirrorOfferPayload
	if err := json.Unmarshal(offerFrame.Payload, &offer); err != nil {
		t.Fatal(err)
	}
	if offer.ViewerGrant == nil || (version == 2 && (offer.Source == nil || offer.Source.Kind != protocol.MirrorSourceVirtual)) || (version == 1 && (offer.Source != nil || offer.ViewerGrant.Source.Kind != protocol.MirrorSourcePhysical)) {
		t.Fatalf("missing source grant: %+v", offer)
	}
	if err := h.HandleDaemonMirrorViewer(context.Background(), daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}}, protocol.MirrorViewerPayload{WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, DaemonID: daemonID, ViewerID: "viewer", Active: true}); err != nil {
		t.Fatal(err)
	}
	// When: a live viewer renews, then workspace membership is revoked.
	renew := withURLParams(newRequestAs(userID, http.MethodPost, "/renew", nil), "runtimeId", runtimeID, "sessionId", created.ID)
	renew.Header.Set("Authorization", "Bearer "+token)
	if version == 2 {
		testutil.Call(t, middleware.Auth(h.Queries, nil, nil, nil)(http.HandlerFunc(h.RenewMirrorSession)).ServeHTTP, renew).Want(http.StatusOK)
	} else {
		h.sweepViewerGrants(context.Background())
	}
	readFrame(protocol.EventMirrorViewerRenew)
	member, err := h.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(userID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Queries.DeleteMember(context.Background(), member.ID); err != nil {
		t.Fatal(err)
	}
	h.sweepViewerGrants(context.Background())
	// Then: the exact existing grant receives targeted revocation and cannot renew again.
	revokeFrame := readFrame(protocol.EventMirrorViewerRevoke)
	var revoke protocol.MirrorViewerRevokePayload
	if err := json.Unmarshal(revokeFrame.Payload, &revoke); err != nil {
		t.Fatal(err)
	}
	if revoke.GrantID != offer.ViewerGrant.GrantID || revoke.SessionID != created.ID {
		t.Fatal("revoked wrong viewer")
	}
	if _, err := h.MirrorGrants.Lookup(created.ID, userID, runtimeID, time.Now()); err == nil {
		t.Fatal("revoked grant retained")
	}
}
