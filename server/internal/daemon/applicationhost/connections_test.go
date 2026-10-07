package applicationhost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationConnectionShutdownClosesUpgradedStreams(t *testing.T) {
	for _, shutdown := range []string{"close", "cancel"} {
		t.Run(shutdown, func(t *testing.T) {
			backendClosed := make(chan struct{})
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				stream, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer close(backendClosed)
				defer stream.Close()
				_, _, _ = stream.ReadMessage()
			}))
			defer service.Close()
			resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewEncoder(w).Encode(connectionAccess{URL: service.URL, Token: "dependency-session", CookieName: "multica_app_session", ExpiresInSeconds: 28800}); err != nil {
					t.Error(err)
				}
			}))
			defer resolver.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			values, closeAll, err := startConnections(ctx, []protocol.ApplicationResolvedConnection{{URLVariable: "API_URL", ResolverURL: resolver.URL + "/api/application-connections/resolve", Grant: "resolver-grant"}})
			if err != nil {
				t.Fatal(err)
			}
			defer closeAll()
			stream, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(values["API_URL"], "http")+"/events", nil)
			if response != nil {
				response.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if shutdown == "close" {
				closeAll()
			} else {
				cancel()
			}
			if err := stream.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, _, err = stream.ReadMessage()
			if err == nil {
				t.Fatal("dependency stream remained open after shutdown")
			}
			if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
				t.Fatal("dependency shutdown left the upgraded stream open")
			}
			select {
			case <-backendClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("dependency shutdown did not close the target stream")
			}
		})
	}
}

func TestApplicationConnectionProxyInjectsOnlyDependencySessionAndRefreshesAuthorization(t *testing.T) {
	var calls atomic.Int64
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("multica_app_session")
		if err != nil || cookie.Value != "dependency-session" {
			t.Error("dependency session missing")
		}
		if r.Header.Get("Authorization") != "Bearer application-owned-token" {
			t.Error("application's own authorization changed")
		}
		if strings.Contains(r.Header.Get("Cookie"), "resolver-grant") {
			t.Error("resolver credential reached target service")
		}
		if _, err := io.WriteString(w, r.URL.Path); err != nil {
			t.Error(err)
		}
	}))
	defer service.Close()
	resolver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer resolver-grant" {
			t.Error("resolver did not use its narrow grant")
		}
		if err := json.NewEncoder(w).Encode(connectionAccess{URL: service.URL + "/api", Token: "dependency-session", CookieName: "multica_app_session", ExpiresInSeconds: 28800}); err != nil {
			t.Error(err)
		}
	}))
	defer resolver.Close()
	proxy := &connectionProxy{binding: protocol.ApplicationResolvedConnection{Grant: "resolver-grant", ResolverURL: resolver.URL}, client: &http.Client{Timeout: time.Second}}
	proxy.pathToken = "private-local-capability"
	local := httptest.NewServer(proxy)
	defer local.Close()
	unauthorized, err := http.Get(local.URL + "/resource")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusNotFound {
		t.Fatal("local proxy exposed the dependency without its capability path")
	}
	request, err := http.NewRequest(http.MethodGet, local.URL+"/private-local-capability/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer application-owned-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(actual) != "/api/resource" {
		t.Fatalf("dependency routing=%q error=%v", actual, err)
	}
	if _, err := proxy.resolve(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpired dependency session was unnecessarily reissued")
	}
	if _, err := proxy.resolve(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("dependency session did not refresh")
	}
}

func TestApplicationConnectionsKeepLocalBindingsDirectAndRejectMissingGrants(t *testing.T) {
	values, closeAll, err := startConnections(context.Background(), []protocol.ApplicationResolvedConnection{{URLVariable: "API_URL", LocalURL: "http://127.0.0.1:4100/api"}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	if values["API_URL"] != "http://127.0.0.1:4100/api" {
		t.Fatal("same-machine dependency did not retain its local address")
	}
	if _, _, err := startConnections(context.Background(), []protocol.ApplicationResolvedConnection{{URLVariable: "API_URL"}}); err == nil {
		t.Fatal("remote connection without grant became available")
	}
}
