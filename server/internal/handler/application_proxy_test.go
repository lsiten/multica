package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func applicationProxyFixture(t *testing.T, service http.Handler) (*Handler, string, string, string) {
	t.Helper()
	endpoint, runtimeID := applicationAccessFixture(t)
	h := *testHandler
	h.cfg.ApplicationOrigin = "http://apps.localhost"
	h.ApplicationGateway = applicationgateway.NewHub()
	local := httptest.NewServer(service)
	localURL, err := url.Parse(local.URL)
	if err != nil {
		t.Fatal(err)
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(h.ApplicationHostBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/control" {
			if err := h.ApplicationGateway.Control(r.Context(), runtimeID, connection); err != nil {
				return
			}
			return
		}
		var request protocol.ApplicationTunnelRequest
		if err := connection.ReadJSON(&request); err != nil {
			connection.Close()
			return
		}
		if _, err := h.ApplicationGateway.Attach(runtimeID, testWorkspaceID, request.StreamID, request.Token, connection); err != nil {
			connection.Close()
		}
	})))
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/control", nil)
	if err != nil {
		server.Close()
		local.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer workers.Wait()
		for {
			var request protocol.ApplicationTunnelRequest
			if err := control.ReadJSON(&request); err != nil {
				return
			}
			workers.Go(func() {
				data, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/data", nil)
				if err != nil {
					return
				}
				defer data.Close()
				if err := data.WriteJSON(request); err != nil {
					return
				}
				connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", localURL.Host)
				if err != nil {
					return
				}
				defer connection.Close()
				closed := make(chan struct{})
				go func() {
					select {
					case <-ctx.Done():
						connection.Close()
						data.Close()
					case <-closed:
					}
				}()
				defer close(closed)
				if err := applicationgateway.Bridge(applicationgateway.NewStream(data, nil), connection); err != nil {
					return
				}
			})
		}
	}()
	t.Cleanup(func() { cancel(); control.Close(); <-done; server.Close(); local.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for !h.ApplicationGateway.Connected(runtimeID) {
		if time.Now().After(deadline) {
			t.Fatal("runtime channel did not connect")
		}
		time.Sleep(5 * time.Millisecond)
	}
	token, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), testWorkspaceID, testUserID, applicationTestMemberID(t, testUserID), endpoint.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return &h, server.URL, uuidToString(endpoint.ID) + ".apps.localhost", token
}

func TestApplicationProxyDoesNotForwardManagementCredentials(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	_, address, host, session := applicationProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(r.Header); err != nil {
			return
		}
	}))
	platformJWT, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": testUserID, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(auth.JWTSecret())
	if err != nil {
		t.Fatal(err)
	}
	for _, authorization := range []string{"Bearer mul_secret", "Bearer mat_secret", "Bearer mdt_secret", "Bearer mcn_secret", "Bearer " + platformJWT, "Basic YXBwOnNlY3JldA==", "Bearer application-owned-token"} {
		t.Run(strings.Split(authorization, " ")[1][:8], func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, address+"/headers", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = host
			request.Header.Set("Authorization", authorization)
			request.Header.Set("X-User-ID", testUserID)
			request.Header.Set("X-Workspace-ID", testWorkspaceID)
			request.Header.Set("X-Multica-Test", "internal")
			request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
			request.AddCookie(&http.Cookie{Name: "multica_auth", Value: "platform-secret"})
			request.AddCookie(&http.Cookie{Name: "app_cookie", Value: "application-value"})
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				raw, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s", response.StatusCode, raw)
			}
			var headers http.Header
			if err := json.NewDecoder(response.Body).Decode(&headers); err != nil {
				t.Fatal(err)
			}
			wantAuthorization := ""
			if strings.HasPrefix(authorization, "Basic ") || authorization == "Bearer application-owned-token" {
				wantAuthorization = authorization
			}
			if headers.Get("Authorization") != wantAuthorization {
				t.Fatalf("authorization reached service: %q", headers.Get("Authorization"))
			}
			if headers.Get("X-User-ID") != "" || headers.Get("X-Workspace-ID") != "" || headers.Get("X-Multica-Test") != "" {
				t.Fatalf("platform identity reached service: %v", headers)
			}
			if headers.Get("Cookie") != "app_cookie=application-value" {
				t.Fatalf("cookies reached service: %q", headers.Get("Cookie"))
			}
		})
	}
}

func TestApplicationProxyRevokesRemovedMembersWithoutDisconnectingOtherMembers(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	_, address, host, ownerSession := applicationProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	userID := dbfx.User(t, "application viewer", "application-viewer@example.com")
	dbfx.Member(t, testWorkspaceID, userID, "member")
	endpointID := strings.TrimSuffix(host, ".apps.localhost")
	viewerSession, err := applicationgateway.SignAccess(auth.JWTSecret(), endpointID, testWorkspaceID, userID, applicationTestMemberID(t, userID), 1)
	if err != nil {
		t.Fatal(err)
	}
	open := func(session string) *http.Response {
		request, err := http.NewRequest(http.MethodGet, address+"/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = host
		request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
		response, err := (&http.Client{Timeout: 9 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream denied: %d", response.StatusCode)
		}
		ready := make([]byte, len("data: ready\n\n"))
		if _, err := io.ReadFull(response.Body, ready); err != nil || string(ready) != "data: ready\n\n" {
			t.Fatalf("stream not flushed: %q %v", ready, err)
		}
		return response
	}
	owner := open(ownerSession)
	viewer := open(viewerSession)
	if _, err := testPool.Exec(context.Background(), "DELETE FROM member WHERE user_id=$1 AND workspace_id=$2", userID, testWorkspaceID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := viewer.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("removed member stream remained accessible")
	}
	if time.Since(started) > 6*time.Second {
		t.Fatal("membership revocation was not observed before client timeout")
	}
	ownerRead := make(chan error, 1)
	go func() { _, err := owner.Body.Read(make([]byte, 1)); ownerRead <- err }()
	select {
	case err := <-ownerRead:
		t.Fatalf("other member was disconnected: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	owner.Body.Close()
	<-ownerRead
}

func TestApplicationProxyStreamsResponseBeforeUploadCompletes(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	_, address, host, session := applicationProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
			return
		}
		if _, err := io.WriteString(w, "ready\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		if _, err := io.Copy(w, r.Body); err != nil {
			return
		}
	}))
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/upload", reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
	type reply struct {
		response *http.Response
		err      error
	}
	replied := make(chan reply, 1)
	go func() { response, err := http.DefaultClient.Do(request); replied <- reply{response, err} }()
	var result reply
	select {
	case result = <-replied:
	case <-ctx.Done():
		t.Fatal("application response waited for upload completion")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.response.Body.Close()
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(result.response.Body, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("streamed response missing: %q %v", ready, err)
	}
	payload := bytes.Repeat([]byte("streamed upload\n"), 10000)
	writeDone := make(chan error, 1)
	go func() { _, err := writer.Write(payload); writeDone <- errors.Join(err, writer.Close()) }()
	actual, err := io.ReadAll(result.response.Body)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("streamed upload/download lost bytes: size=%d err=%v", len(actual), err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}
