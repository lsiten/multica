package handler

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/redis/go-redis/v9"
)

func applicationDistributedProxyFixture(t *testing.T, service http.Handler) (*Handler, string, string, string) {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("isolated Redis executable unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	redisCtx, stopRedis := context.WithCancel(context.Background())
	command := exec.CommandContext(redisCtx, executable, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		stopRedis()
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), DialTimeout: time.Second})
	t.Cleanup(func() { client.Close(); stopRedis(); command.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	for client.Ping(ctx).Err() != nil {
		if ctx.Err() != nil {
			t.Fatal("isolated gateway Redis did not start")
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	endpoint, runtimeID := applicationAccessFixture(t)
	local := httptest.NewServer(service)
	t.Cleanup(local.Close)
	makeNode := func() (*Handler, *httptest.Server) {
		h := *testHandler
		h.cfg.ApplicationOrigin = "http://apps.localhost"
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(h.ApplicationHostBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == applicationgateway.PeerPath {
				h.ApplicationGateway.ServePeer(w, r)
				return
			}
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
			var input protocol.ApplicationTunnelRequest
			if err := connection.ReadJSON(&input); err != nil {
				connection.Close()
				return
			}
			if err := h.ApplicationGateway.AttachStream(r.Context(), runtimeID, testWorkspaceID, input.StreamID, input.Token, connection); err != nil {
				connection.Close()
			}
		})))
		h.ApplicationGateway, err = applicationgateway.NewClusterHub(client, server.URL, auth.JWTSecret())
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		lifetime, stop := context.WithCancel(context.Background())
		if err := h.ApplicationGateway.Start(lifetime); err != nil {
			stop()
			server.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			h.ApplicationGateway.Close()
			stop()
			waitCtx, stopWait := context.WithTimeout(context.Background(), time.Second)
			defer stopWait()
			if err := h.ApplicationGateway.Wait(waitCtx); err != nil {
				t.Error(err)
			}
			server.Close()
		})
		return &h, server
	}
	owner, first := makeNode()
	ingress, second := makeNode()
	control, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(first.URL, "http")+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	for !owner.ApplicationGateway.Connected(runtimeID) {
		if ctx.Err() != nil {
			t.Fatal("distributed runtime control did not register")
		}
		time.Sleep(time.Millisecond)
	}
	bridgeCtx, stopBridges := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer workers.Wait()
		for {
			var input protocol.ApplicationTunnelRequest
			if err := control.ReadJSON(&input); err != nil {
				return
			}
			workers.Go(func() {
				data, _, err := websocket.DefaultDialer.DialContext(bridgeCtx, "ws"+strings.TrimPrefix(second.URL, "http")+"/data", nil)
				if err != nil {
					return
				}
				defer data.Close()
				if err := data.WriteJSON(input); err != nil {
					return
				}
				connection, err := (&net.Dialer{}).DialContext(bridgeCtx, "tcp", strings.TrimPrefix(local.URL, "http://"))
				if err != nil {
					return
				}
				defer connection.Close()
				closed := make(chan struct{})
				go func() {
					select {
					case <-bridgeCtx.Done():
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
	t.Cleanup(func() { stopBridges(); control.Close(); <-done })
	session, err := applicationgateway.SignAccess(auth.JWTSecret(), uuidToString(endpoint.ID), testWorkspaceID, testUserID, applicationTestMemberID(t, testUserID), endpoint.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return ingress, second.URL, uuidToString(endpoint.ID) + ".apps.localhost", session
}

func TestApplicationDistributedProxyStreamsUploadsAndDownloads(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	_, address, host, session := applicationDistributedProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+"/upload", reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(response.Body, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("distributed response waited for upload completion: %q %v", ready, err)
	}
	payload := bytes.Repeat([]byte("distributed streaming payload\n"), 40000)
	written := make(chan error, 1)
	go func() {
		_, err := writer.Write(payload)
		if err == nil {
			err = writer.Close()
		}
		written <- err
	}()
	actual, err := io.ReadAll(response.Body)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("distributed upload/download lost bytes: received=%d error=%v", len(actual), err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestApplicationDistributedProxySSECancellationReachesTheService(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	cancelled := make(chan struct{})
	_, address, host, session := applicationDistributedProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	request.AddCookie(&http.Cookie{Name: "multica_app_session", Value: session})
	response, err := (&http.Client{Timeout: 8 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	ready := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(response.Body, ready); err != nil || string(ready) != "data: ready\n\n" {
		t.Fatalf("distributed SSE did not flush: %q %v", ready, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("browser cancellation did not reach the distributed service")
	}
}

func TestApplicationDistributedProxyWebSocketRevocationClosesTheService(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	closed := make(chan struct{})
	h, address, host, session := applicationDistributedProxyFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		defer close(closed)
		for {
			kind, payload, err := connection.ReadMessage()
			if err != nil {
				return
			}
			if err := connection.WriteMessage(kind, payload); err != nil {
				return
			}
		}
	}))
	connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(address, "http")+"/socket", http.Header{"Host": {host}, "Cookie": {"multica_app_session=" + session}})
	if response != nil {
		response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.WriteMessage(websocket.TextMessage, []byte("across replicas")); err != nil {
		t.Fatal(err)
	}
	connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, reply, err := connection.ReadMessage()
	if err != nil || string(reply) != "across replicas" {
		t.Fatalf("distributed WebSocket did not echo: %q %v", reply, err)
	}
	h.ApplicationGateway.Revoke(strings.TrimSuffix(host, ".apps.localhost"))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("revoked distributed WebSocket remained open")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not close the target WebSocket")
	}
}
