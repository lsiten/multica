package applicationgateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationScopedStreamCancellationAcrossGatewayNodes(t *testing.T) {
	redis := applicationClusterRedis(t)
	first, firstServer := applicationClusterNode(t, redis)
	second, _ := applicationClusterNode(t, redis)
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(firstServer.URL, "http")+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !first.Connected("runtime") {
		if ctx.Err() != nil {
			t.Fatal("control did not register")
		}
		time.Sleep(time.Millisecond)
	}
	scope, cancelScope := context.WithCancel(ctx)
	defer cancelScope()
	returned := make(chan struct{})
	ingress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var input protocol.ApplicationTunnelRequest
		if err = connection.ReadJSON(&input); err != nil {
			return
		}
		second.AttachStreamScoped(scope, "runtime", "workspace", input.StreamID, input.Token, connection)
		close(returned)
	}))
	defer ingress.Close()
	type opened struct {
		connection net.Conn
		err        error
	}
	result := make(chan opened, 1)
	go func() {
		connection, err := second.Open(ctx, protocol.ApplicationTunnelRequest{RuntimeID: "runtime", WorkspaceID: "workspace", EndpointID: "endpoint", Kind: "service", Port: 4100})
		result <- opened{connection, err}
	}()
	control.SetReadDeadline(time.Now().Add(3 * time.Second))
	var request protocol.ApplicationTunnelRequest
	if err = control.ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	data, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ingress.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err = data.WriteJSON(request); err != nil {
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
		t.Fatal("scoped remote stream did not attach")
	}
	defer connection.Close()
	if err = data.WriteMessage(websocket.BinaryMessage, []byte("scoped cross-node bytes")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("scoped cross-node bytes"))
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err = io.ReadFull(connection, buffer); err != nil || string(buffer) != "scoped cross-node bytes" {
		t.Fatalf("cross-node data: %v", err)
	}
	cancelScope()
	data.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err = data.ReadMessage(); err == nil {
		t.Fatal("cancelled scoped data websocket stayed open")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("scoped cancellation only timed out")
	}
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("scoped bridge goroutine outlived cancellation")
	}
}
