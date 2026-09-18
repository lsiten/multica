package daemonws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenUpgradeHeaderMatchesQueryGeneration(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	}))
	defer server.Close()
	c, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	generation := response.Header.Get("X-Daemon-Generation")
	if generation == "" {
		t.Fatal("missing server generation header")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ready := time.NewTicker(time.Millisecond)
	defer ready.Stop()
	for {
		hub.mu.RLock()
		client := hub.vscreenClientLocked("ws", "rt", "daemon")
		hub.mu.RUnlock()
		if client != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("client not registered")
		case <-ready.C:
		}
	}
	result := make(chan error, 1)
	go func() { _, err := hub.QueryVscreen(ctx, "ws", "rt", "daemon", "state"); result <- err }()
	c.SetReadDeadline(time.Now().Add(time.Second))
	var message protocol.Message
	if err = c.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	var query protocol.VscreenQuery
	if err = json.Unmarshal(message.Payload, &query); err != nil {
		t.Fatal(err)
	}
	if query.DaemonGeneration != generation {
		t.Fatalf("query %s differs from header %s", query.DaemonGeneration, generation)
	}
	t.Logf("upgrade header generation=%s exact query envelope match", generation)
	cancel()
	<-result
}
