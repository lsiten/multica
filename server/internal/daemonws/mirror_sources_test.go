package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMirrorSourcesQueryAuthenticatedRoundTrip(t *testing.T) {
	hub := NewHub()
	conn := dialMirrorTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	result := make(chan error, 1)
	go func() {
		response, err := hub.QueryMirrorSources(context.Background(), "ws", "rt", "daemon")
		if err == nil && (len(response.Sources) != 1 || response.Sources[0].Source.Kind != protocol.MirrorSourcePhysical) {
			err = errors.New("unexpected source catalog")
		}
		result <- err
	}()
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.MirrorSourcesQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	response := protocol.MirrorSourcesResult{RuntimeEnvelope: query.RuntimeEnvelope}
	response.Sources = []protocol.MirrorSourceDescriptor{testMirrorSource(protocol.MirrorSourcePhysical)}
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventMirrorSourcesResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no correlated mirror source result")
	}
}

func TestMirrorSourcesQueryWithoutReplyExpires(t *testing.T) {
	hub := NewHub()
	dialMirrorTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := hub.QueryMirrorSources(ctx, "ws", "rt", "daemon")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestMirrorSourcesRejectsForeignAndStaleResults(t *testing.T) {
	hub := NewHub()
	conn := dialMirrorTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	foreign := dialMirrorTestConn(t, hub, ClientIdentity{DaemonID: "other", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := hub.QueryMirrorSources(ctx, "ws", "rt", "daemon")
		done <- err
	}()
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.MirrorSourcesQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	response := protocol.MirrorSourcesResult{RuntimeEnvelope: query.RuntimeEnvelope}
	response.Sources = []protocol.MirrorSourceDescriptor{testMirrorSource(protocol.MirrorSourcePhysical)}
	if err := foreign.WriteJSON(protocol.Message{Type: protocol.EventMirrorSourcesResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	response.DaemonGeneration = "stale"
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventMirrorSourcesResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forged mirror source result accepted: %v", err)
	}
}

func TestMirrorSourcesSupersededConnectionCannotCompleteQuery(t *testing.T) {
	hub := NewHub()
	identity := ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}}
	old := dialMirrorTestConn(t, hub, identity)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := hub.QueryMirrorSources(ctx, "ws", "rt", "daemon")
		done <- err
	}()
	var frame protocol.Message
	if err := old.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.MirrorSourcesQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	dialMirrorTestConn(t, hub, identity)
	response := protocol.MirrorSourcesResult{RuntimeEnvelope: query.RuntimeEnvelope}
	response.Sources = []protocol.MirrorSourceDescriptor{testMirrorSource(protocol.MirrorSourcePhysical)}
	if err := old.WriteJSON(protocol.Message{Type: protocol.EventMirrorSourcesResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("superseded mirror source result accepted: %v", err)
	}
}

func testMirrorSource(kind protocol.MirrorSourceKind) protocol.MirrorSourceDescriptor {
	binding := protocol.MirrorSourceBinding{
		Resource: protocol.ResourceKey{
			BackendIdentity: "https://multica.example",
			WorkspaceID:     "ws",
			RuntimeID:       "rt",
			UID:             501,
		},
		Source:      protocol.MirrorSource{Kind: kind, SourceID: string(kind)},
		NativeEpoch: "native",
		Generation:  "generation",
		Primary:     kind == protocol.MirrorSourcePhysical,
	}
	return protocol.MirrorSourceDescriptor{
		MirrorSourceBinding: binding,
		Name:                "Primary display",
		Width:               1920,
		Height:              1080,
		LogicalWidth:        1920,
		LogicalHeight:       1080,
		Scale:               1,
		GeometryRevision:    1,
	}
}

func dialMirrorTestConn(t *testing.T, hub *Hub, identity ClientIdentity) *websocket.Conn {
	t.Helper()
	conn := dialRPCTestConn(t, hub, identity)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		hub.mu.RLock()
		registered := false
		for client := range hub.clients {
			if client.conn.RemoteAddr().String() == conn.LocalAddr().String() {
				registered = true
				break
			}
		}
		hub.mu.RUnlock()
		if registered {
			return conn
		}
		select {
		case <-deadline.C:
			t.Fatal("daemon socket was not registered in Hub")
		case <-poll.C:
		}
	}
}
