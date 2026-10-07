package applicationgateway

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/redis/go-redis/v9"
)

func applicationClusterRedis(t *testing.T) *redis.Client {
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
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, executable, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), DialTimeout: time.Second})
	t.Cleanup(func() { client.Close(); cancel(); command.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		if client.Ping(context.Background()).Err() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated Redis did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return client
}

func applicationClusterNode(t *testing.T, client redis.UniversalClient) (*Hub, *httptest.Server) {
	t.Helper()
	var hub *Hub
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PeerPath {
			hub.ServePeer(w, r)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/control" {
			if err := hub.Control(r.Context(), "runtime", connection); err != nil {
				return
			}
			return
		}
		var input struct {
			StreamID string `json:"stream_id"`
			Token    string `json:"token"`
		}
		if err := connection.ReadJSON(&input); err != nil {
			connection.Close()
			return
		}
		if err := hub.AttachStream(r.Context(), "runtime", "workspace", input.StreamID, input.Token, connection); err != nil {
			connection.Close()
		}
	}))
	var err error
	hub, err = NewClusterHub(client, server.URL, []byte("cluster-test-key-not-a-real-platform-secret"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := hub.Start(ctx); err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		hub.DisconnectRuntime("runtime")
		cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := hub.Wait(ctx); err != nil {
			t.Error(err)
		}
		server.Close()
	})
	return hub, server
}

func TestApplicationClusterRoutesOpenAndDaemonAttachmentAcrossDifferentNodes(t *testing.T) {
	client := applicationClusterRedis(t)
	first, firstServer := applicationClusterNode(t, client)
	second, secondServer := applicationClusterNode(t, client)
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(firstServer.URL, "http")+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !first.Connected("runtime") {
		if ctx.Err() != nil {
			t.Fatal("runtime did not register")
		}
		time.Sleep(time.Millisecond)
	}
	available, err := second.Available(ctx, "runtime")
	if err != nil || !available {
		t.Fatalf("remote runtime hidden: %v %v", available, err)
	}
	type opened struct {
		connection net.Conn
		err        error
	}
	result := make(chan opened, 1)
	go func() {
		connection, err := second.Open(ctx, protocol.ApplicationTunnelRequest{RuntimeID: "runtime", WorkspaceID: "workspace", EndpointID: "endpoint", Kind: "service", Port: 3000})
		result <- opened{connection, err}
	}()
	control.SetReadDeadline(time.Now().Add(3 * time.Second))
	var request protocol.ApplicationTunnelRequest
	if err := control.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	data, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(secondServer.URL, "http")+"/data", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.WriteJSON(map[string]string{"stream_id": request.StreamID, "token": request.Token}); err != nil {
		t.Fatal(err)
	}
	var connection net.Conn
	select {
	case out := <-result:
		if out.err != nil {
			t.Fatal(out.err)
		}
		connection = out.connection
	case <-ctx.Done():
		t.Fatal("cross-node stream did not attach")
	}
	defer connection.Close()
	if err := data.WriteMessage(websocket.BinaryMessage, []byte("remote service bytes")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("remote service bytes"))
	connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(connection, buffer); err != nil || string(buffer) != "remote service bytes" {
		t.Fatalf("cross-node payload=%q error=%v", buffer, err)
	}
	if _, err := connection.Write([]byte("browser request")); err != nil {
		t.Fatal(err)
	}
	data.SetReadDeadline(time.Now().Add(time.Second))
	_, reply, err := data.ReadMessage()
	if err != nil || string(reply) != "browser request" {
		t.Fatalf("cross-node response=%q error=%v", reply, err)
	}
	second.Revoke("endpoint")
	if _, err := connection.Read(buffer); err == nil {
		t.Fatal("revocation did not close remote stream")
	}
}

func TestApplicationClusterLeaseCannotBeRenewedOrReleasedByAnOlderOwner(t *testing.T) {
	client := applicationClusterRedis(t)
	first, _ := applicationClusterNode(t, client)
	second, _ := applicationClusterNode(t, client)
	ctx := context.Background()
	if err := first.cluster.register(ctx, "runtime", "old"); err != nil {
		t.Fatal(err)
	}
	if err := second.cluster.register(ctx, "runtime", "new"); err != nil {
		t.Fatal(err)
	}
	if err := first.cluster.renew(ctx, "runtime", "old"); err == nil {
		t.Fatal("superseded owner retained its lease")
	}
	if err := first.cluster.release(ctx, "runtime", "old"); err != nil {
		t.Fatal(err)
	}
	owner, err := second.cluster.runtimeOwner(ctx, "runtime")
	if err != nil || owner.Epoch != "new" || owner.Origin != second.cluster.origin {
		t.Fatalf("old disconnect removed new owner: %+v %v", owner, err)
	}
}

func TestApplicationClusterPeerRejectsPlatformCredentialsAndBrowserOrigins(t *testing.T) {
	client := applicationClusterRedis(t)
	_, server := applicationClusterNode(t, client)
	secret := []byte("cluster-test-key-not-a-real-platform-secret")
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"multica-application-peer-v1"}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []http.Header{{"Authorization": {"Bearer " + token}}, {"Origin": {"https://browser.example"}}, {"Authorization": {"Bearer not-a-peer-token"}}} {
		connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+PeerPath, headers)
		if connection != nil {
			connection.Close()
			t.Fatal("untrusted peer connection was upgraded")
		}
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("peer accepted a platform credential or browser origin")
		}
	}
}
