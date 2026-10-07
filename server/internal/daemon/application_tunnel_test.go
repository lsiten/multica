package daemon

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/applicationgateway"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationDaemonTunnelTransfersHTTPWebSocketAndCancellation(t *testing.T) {
	upgrader := websocket.Upgrader{}
	cancelled := make(chan struct{}, 1)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload":
			if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
				t.Error(err)
				return
			}
			if _, err := io.Copy(w, r.Body); err != nil {
				return
			}
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled <- struct{}{}
		case "/socket":
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			for {
				kind, message, err := connection.ReadMessage()
				if err != nil {
					return
				}
				if err := connection.WriteMessage(kind, message); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer service.Close()
	serviceURL, err := url.Parse(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(serviceURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	hub := applicationgateway.NewHub()
	var command protocol.ApplicationControlCommand
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-daemon-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/control") {
			if err := hub.Control(r.Context(), command.RuntimeID, connection); err != nil {
				return
			}
			return
		}
		var attachment struct {
			StreamID string `json:"stream_id"`
			Token    string `json:"token"`
		}
		if err := connection.ReadJSON(&attachment); err != nil {
			connection.Close()
			return
		}
		if _, err := hub.Attach(command.RuntimeID, command.WorkspaceID, attachment.StreamID, attachment.Token, connection); err != nil {
			connection.Close()
		}
	}))
	defer broker.Close()
	d, configured := applicationManagerFixture(t, broker.URL)
	command = configured
	command.Config.Mode = "external"
	command.Config.Port = port
	d.applicationCommands.Store(command.InstanceID, command)
	d.client.token = "test-daemon-token"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	control, err := d.client.connectApplicationTunnel(ctx, command.RuntimeID, "control", d.cfg.DaemonID)
	if err != nil {
		t.Fatal(err)
	}
	readerDone := make(chan error, 1)
	go func() { readerDone <- d.readApplicationTunnel(ctx, command.RuntimeID, control) }()
	defer func() {
		cancel()
		control.Close()
		select {
		case <-readerDone:
		case <-time.After(2 * time.Second):
			t.Error("daemon tunnel workers did not drain")
		}
	}()
	waitApplicationManager(t, func() bool { return hub.Connected(command.RuntimeID) })
	descriptor := protocol.ApplicationTunnelRequest{EndpointID: "endpoint", InstanceID: command.InstanceID, WorkspaceID: command.WorkspaceID, RuntimeID: command.RuntimeID, Generation: command.Generation, Kind: "service", Port: port}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) { return hub.Open(ctx, descriptor) }}
	defer transport.CloseIdleConnections()
	reverseProxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.Out.URL.Scheme = "http"; r.Out.URL.Host = "service.local" }, Transport: transport, FlushInterval: -1}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
				t.Error(err)
				return
			}
		}
		reverseProxy.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	payload := bytes.Repeat([]byte("application upload/download\n"), 12000)
	response, err := client.Post(proxy.URL+"/upload", "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("upload/download lost bytes: size=%d want=%d error=%v", len(actual), len(payload), err)
	}
	events, err := client.Get(proxy.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	first := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(events.Body, first); err != nil || string(first) != "data: ready\n\n" {
		t.Fatalf("event stream was buffered: %q %v", first, err)
	}
	events.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("browser cancellation did not reach local service")
	}
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(proxy.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := socket.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	socket.SetReadDeadline(time.Now().Add(3 * time.Second))
	kind, echo, err := socket.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(echo, payload) {
		t.Fatalf("websocket lost message: kind=%d size=%d err=%v", kind, len(echo), err)
	}
	hub.Revoke("endpoint")
	if _, _, err := socket.ReadMessage(); err == nil {
		t.Fatal("revoked websocket remained open")
	}
	for _, change := range []func(*protocol.ApplicationTunnelRequest){
		func(r *protocol.ApplicationTunnelRequest) { r.Port++ },
		func(r *protocol.ApplicationTunnelRequest) { r.Generation++ },
		func(r *protocol.ApplicationTunnelRequest) { r.WorkspaceID = "other" },
	} {
		invalid := descriptor
		change(&invalid)
		if err := d.forwardApplicationTunnel(ctx, invalid); err == nil {
			t.Fatalf("invalid stream scope accepted: %+v", invalid)
		}
	}
}
