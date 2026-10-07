package applicationgateway

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationGatewayForwardsStreamingBytesAndRevokesConnections(t *testing.T) {
	hub := NewHub()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/control" {
			if err = hub.Control(r.Context(), "runtime", connection); err != nil {
				return
			}
			return
		}
		var request protocol.ApplicationTunnelRequest
		if err = connection.ReadJSON(&request); err != nil {
			connection.Close()
			return
		}
		if _, err = hub.Attach("runtime", "workspace", request.StreamID, request.Token, connection); err != nil {
			connection.Close()
		}
	}))
	defer server.Close()
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	deadline := time.Now().Add(time.Second)
	for !hub.Connected("runtime") {
		if time.Now().After(deadline) {
			t.Fatal("control registration timed out")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opened := make(chan net.Conn, 1)
	go func() {
		connection, openErr := hub.Open(ctx, protocol.ApplicationTunnelRequest{RuntimeID: "runtime", WorkspaceID: "workspace", EndpointID: "endpoint", Kind: "service", Port: 3000})
		if openErr != nil {
			t.Error(openErr)
			return
		}
		opened <- connection
	}()
	var request protocol.ApplicationTunnelRequest
	if err = control.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Port != 3000 || request.Token == "" {
		t.Fatalf("scope missing: %+v", request)
	}
	data, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err = data.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	connection := <-opened
	if err = data.WriteMessage(websocket.BinaryMessage, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err = data.WriteMessage(websocket.BinaryMessage, []byte("second")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 11)
	if _, err = io.ReadFull(connection, buffer); err != nil || string(buffer) != "firstsecond" {
		t.Fatalf("stream bytes=%s err=%v", buffer, err)
	}
	hub.Revoke("endpoint")
	if _, err = connection.Read(buffer); err == nil {
		t.Fatal("revoked stream remained open")
	}
}

func TestApplicationGatewayRevocationCancelsWaitingAttachment(t *testing.T) {
	hub := NewHub()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if err := hub.Control(r.Context(), "runtime", connection); err != nil {
			return
		}
	}))
	defer server.Close()
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for !hub.Connected("runtime") {
		if ctx.Err() != nil {
			t.Fatal("control registration timed out")
		}
		time.Sleep(time.Millisecond)
	}
	opened := make(chan error, 1)
	go func() {
		connection, err := hub.Open(ctx, protocol.ApplicationTunnelRequest{RuntimeID: "runtime", WorkspaceID: "workspace", EndpointID: "endpoint", Kind: "service", Port: 3000})
		if connection != nil {
			connection.Close()
		}
		opened <- err
	}()
	control.SetReadDeadline(time.Now().Add(time.Second))
	var request protocol.ApplicationTunnelRequest
	if err := control.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	hub.Revoke("endpoint")
	select {
	case err := <-opened:
		if err == nil {
			t.Fatal("revoked open succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("revoked open waited for attachment timeout")
	}
	if _, err := hub.Attach("runtime", "workspace", request.StreamID, request.Token, nil); err == nil {
		t.Fatal("revoked attachment was accepted")
	}
}

func TestApplicationGatewayTransportCarriesHTTPAndFlushesSSE(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: first\n\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		if _, err := io.WriteString(w, "data: second\n\n"); err != nil {
			return
		}
	}))
	defer service.Close()
	upgrader := websocket.Upgrader{}
	tunnel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocketConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		parsed, err := url.Parse(service.URL)
		if err != nil {
			t.Error(err)
			return
		}
		local, err := net.Dial("tcp", parsed.Host)
		if err != nil {
			t.Error(err)
			return
		}
		if err = Bridge(NewStream(websocketConn, nil), local); err != nil {
			return
		}
	}))
	defer tunnel.Close()
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(tunnel.URL, "http"), nil)
		if err != nil {
			return nil, err
		}
		return NewStream(connection, nil), nil
	}}
	proxy := httptest.NewServer(&httputil.ReverseProxy{Director: func(r *http.Request) { r.URL.Scheme = "http"; r.URL.Host = "application.local" }, Transport: transport, FlushInterval: -1})
	defer proxy.Close()
	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "data: first\n\ndata: second\n\n" {
		t.Fatalf("SSE body=%s error=%v", body, err)
	}
}

func TestApplicationGatewayRejectsExpiredAndWrongScopeStreamTokens(t *testing.T) {
	hub := NewHub()
	hub.controls["runtime"] = &control{epoch: "epoch"}
	hub.streams["stream"] = &pendingStream{runtimeID: "runtime", workspaceID: "workspace", token: "secret", epoch: "epoch", attached: make(chan *Stream, 1)}
	for _, request := range []struct{ runtime, workspace, token string }{{"other", "workspace", "secret"}, {"runtime", "other", "secret"}, {"runtime", "workspace", "wrong"}} {
		if _, err := hub.Attach(request.runtime, request.workspace, "stream", request.token, nil); err == nil {
			t.Fatal("invalid stream scope accepted")
		}
	}
	hub.Revoke("")
	if raw, err := json.Marshal(hub.Connected("runtime")); err != nil || string(raw) != "true" {
		t.Fatal("endpoint revocation unexpectedly dropped control connection")
	}
}
