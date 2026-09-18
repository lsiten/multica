//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func vscreenFixtureDaemon(t *testing.T) *Daemon {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{DaemonID: "daemon", ServerBaseURL: "https://example.com", NativeHostExecutable: executable, NativeHostBuild: "fixture/commit", NativeVscreenPreferencesPath: filepath.Join(t.TempDir(), "enabled.json")}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt", "other"}}}, runtimeIndex: map[string]Runtime{"rt": {ID: "rt"}, "other": {ID: "other"}}, runtimeMirrors: make(map[string]*mirror.RuntimeMirror)}
	t.Cleanup(func() { d.closeRuntimeMirrors(); d.closeVscreens() })
	return d
}

func TestVscreenAuthenticatedControlNativeLifecycle(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	peer := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-only" {
			http.Error(w, "unauthorized", 401)
			return
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Daemon-Generation": []string{"server-epoch"}})
		if err != nil {
			return
		}
		peer <- c
	}))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": []string{"Bearer fixture-only"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := <-peer
	defer remote.Close()
	remote.SetReadDeadline(time.Now().Add(10 * time.Second))
	generation, ctx, cancel := d.beginMirrorControlConnection(context.Background())
	defer cancel()
	d.mu.Lock()
	d.vscreenServerGeneration = response.Header.Get("X-Daemon-Generation")
	d.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		done <- d.readTaskWakeupMessagesForConnectionAndWriter(ctx, taskWakeupReader{conn: conn, mirrorControlGeneration: generation, enqueue: func(raw []byte) (*wsOutbound, error) {
			return &wsOutbound{}, conn.WriteMessage(websocket.TextMessage, raw)
		}})
	}()
	send := func(event string, value any) {
		t.Helper()
		if err := remote.WriteJSON(protocol.Message{Type: event, Payload: marshalRaw(value)}); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(event string, target any) {
		t.Helper()
		var msg protocol.Message
		if err := remote.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != event {
			t.Fatalf("event=%s want %s", msg.Type, event)
		}
		if err := json.Unmarshal(msg.Payload, target); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s %s", event, msg.Payload)
	}
	e := protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server-epoch", RequestID: "query-1"}
	send(protocol.EventVscreenQuery, protocol.VscreenQuery{VscreenEnvelope: e, Kind: "state"})
	var result protocol.VscreenQueryResult
	receive(protocol.EventVscreenQueryResult, &result)
	if result.Validate("state") != nil || result.State.State != protocol.VscreenStateDisabled {
		t.Fatalf("initial state: %+v", result)
	}
	send(protocol.EventVscreenCommand, protocol.VscreenCommand{VscreenEnvelope: e, CommandID: "enable-1", Kind: protocol.VscreenCommandEnable})
	var receipt protocol.VscreenCommandReceipt
	receive(protocol.EventVscreenResult, &receipt)
	if receipt.State != protocol.VscreenReceiptPending {
		t.Fatal(receipt)
	}
	receive(protocol.EventVscreenResult, &receipt)
	if receipt.Validate() != nil || receipt.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(receipt)
	}
	send(protocol.EventVscreenQuery, protocol.VscreenQuery{VscreenEnvelope: e, Kind: "state"})
	receive(protocol.EventVscreenQueryResult, &result)
	if result.State.State != protocol.VscreenStateReady || result.State.Permissions.ScreenRecording != "denied" {
		t.Fatalf("permission readback: %+v", result.State)
	}
	original := *receipt.Epoch
	send(protocol.EventVscreenQuery, protocol.VscreenQuery{VscreenEnvelope: e, Kind: "sources"})
	result = protocol.VscreenQueryResult{}
	receive(protocol.EventVscreenQueryResult, &result)
	if result.Validate("sources") != nil || len(result.Sources) != 2 {
		t.Fatalf("source catalog: %+v", result)
	}
	sources, err := d.vscreenSources(ctx, "ws", "other")
	if err != nil || len(sources) != 1 {
		t.Fatalf("foreign virtual source leaked: %+v %v", sources, err)
	}
	stale := e
	stale.DaemonGeneration = "old-epoch"
	if err := d.executeVscreenCommand(ctx, protocol.VscreenCommand{VscreenEnvelope: stale, CommandID: "stale", Kind: protocol.VscreenCommandDisable}, generation); err == nil {
		t.Fatal("stale command accepted")
	}
	d.handleVscreenViewerRevoke(mirrorOfferMessage{raw: marshalRaw(protocol.MirrorViewerRevokePayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: e.DaemonGeneration, SessionID: "session", ViewerID: "viewer", GrantID: "grant"}), controlGeneration: generation})
	a, err := d.VscreenActor("ws", "rt")
	if err != nil || a.Status().Display.Epoch != original {
		t.Fatal("viewer revoke changed display", err)
	}
	send(protocol.EventVscreenCommand, protocol.VscreenCommand{VscreenEnvelope: e, CommandID: "disable-1", Kind: protocol.VscreenCommandDisable})
	receive(protocol.EventVscreenResult, &receipt)
	receive(protocol.EventVscreenResult, &receipt)
	if receipt.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(receipt)
	}
	send(protocol.EventVscreenQuery, protocol.VscreenQuery{VscreenEnvelope: e, Kind: "sources"})
	result = protocol.VscreenQueryResult{}
	receive(protocol.EventVscreenQueryResult, &result)
	if len(result.Sources) != 1 {
		t.Fatal("virtual source retained after disable")
	}
	remote.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader leaked")
	}
}

func TestVscreenDisconnectFreezesAuthorityWithoutDisposingDisplay(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	ctx := t.Context()
	g, _, cancel := d.beginMirrorControlConnection(ctx)
	defer cancel()
	d.vscreenServerGeneration = "server"
	e := protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "request"}
	if err := d.executeVscreenCommand(ctx, protocol.VscreenCommand{VscreenEnvelope: e, CommandID: "enable", Kind: protocol.VscreenCommandEnable}, g); err != nil {
		t.Fatal(err)
	}
	a, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	display := a.Status().Display
	lease, err := a.Acquire(ctx, vscreen.Transaction{TaskID: "task", ID: "transaction"})
	if err != nil {
		t.Fatal(err)
	}
	d.suspendVscreens(g)
	if _, err = a.Heartbeat(lease); err == nil {
		t.Fatal("old input lease remained valid after detach")
	}
	if !a.Status().Frozen || a.Status().Display != display || !a.Status().Ready {
		t.Fatalf("detach state: %+v", a.Status())
	}
	_, _, nextCancel := d.beginMirrorControlConnection(ctx)
	defer nextCancel()
	if !a.Status().Frozen || a.Status().Display != display {
		t.Fatal("reconnect thawed input or changed display")
	}
	t.Log("WS detach quiesced native authority, retained display; reconnect did not thaw")
}
