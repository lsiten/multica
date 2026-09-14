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

func TestVscreenQueryAuthenticatedRoundTrip(t *testing.T) {
	// Given: a real authenticated WebSocket connection.
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	result := make(chan error, 1)
	// When: the HTTP-side broker queries and the daemon returns its correlated snapshot.
	go func() { _, err := hub.QueryVscreen(context.Background(), "ws", "rt", "daemon", "state"); result <- err }()
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.VscreenQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	t.Logf("wire request: %s", frame.Payload)
	response := protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, State: &protocol.VscreenStateSnapshot{RuntimeID: "rt", State: protocol.VscreenStateDisabled, ControlState: protocol.VscreenControlIdle, StateRevision: 1, Permissions: protocol.VscreenPermissions{ScreenRecording: "unknown", Accessibility: "unknown"}}}
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	// Then: a validated result reaches the exact request waiter.
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no correlated result")
	}
}

func TestVscreenQueryNoAcknowledgementExpires(t *testing.T) {
	// Given
	hub := NewHub()
	dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	// When
	_, err := hub.QueryVscreen(ctx, "ws", "rt", "daemon", "state")
	// Then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestVscreenRejectsStaleAndForeignResults(t *testing.T) {
	// Given
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	foreign := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "other", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := hub.QueryVscreen(ctx, "ws", "rt", "daemon", "sources"); done <- err }()
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.VscreenQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	// When: a different authenticated daemon replays the correlation, then the right daemon uses a stale generation.
	response := protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, Sources: []protocol.VscreenSourceDescriptor{}}
	if err := foreign.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	response.DaemonGeneration = "stale"
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: mustMarshalRaw(response)}); err != nil {
		t.Fatal(err)
	}
	// Then
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forged result accepted: %v", err)
	}
}

func TestVscreenCommandReceiptAndReplay(t *testing.T) {
	// Given
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	// When
	receipt, err := hub.SubmitVscreenCommand("ws", "rt", "daemon", "owner", "command", protocol.VscreenCommandEnable)
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var command protocol.VscreenCommand
	if err := json.Unmarshal(frame.Payload, &command); err != nil {
		t.Fatal(err)
	}
	receipt.State = protocol.VscreenReceiptSucceeded
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(receipt)}); err != nil {
		t.Fatal(err)
	}
	// Then
	deadline := time.Now().Add(time.Second)
	for {
		got, err := hub.VscreenCommandStatus("ws", "rt", "daemon", "owner", "command", true)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == protocol.VscreenReceiptSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receipt not updated")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := hub.SubmitVscreenCommand("ws", "rt", "daemon", "owner", "command", protocol.VscreenCommandEnable); !errors.Is(err, ErrVscreenReplay) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := hub.VscreenCommandStatus("ws", "rt", "daemon", "foreign", "command", false); !errors.Is(err, ErrVscreenNotFound) {
		t.Fatalf("foreign receipt: %v", err)
	}
	t.Logf("command wire: %s; receipt: %+v", frame.Payload, receipt)
}

func TestVscreenSupersededConnectionCannotCompleteQuery(t *testing.T) {
	// Given: a pending request belongs to the original socket.
	hub := NewHub()
	identity := ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}}
	old := dialVscreenTestConn(t, hub, identity)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := hub.QueryVscreen(ctx, "ws", "rt", "daemon", "sources"); done <- err }()
	var frame protocol.Message
	if err := old.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var query protocol.VscreenQuery
	if err := json.Unmarshal(frame.Payload, &query); err != nil {
		t.Fatal(err)
	}
	// When: a new authenticated socket supersedes it before the old result arrives.
	dialVscreenTestConn(t, hub, identity)
	if err := old.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: mustMarshalRaw(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope})}); err != nil {
		t.Fatal(err)
	}
	// Then: even its otherwise exact envelope is stale.
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("superseded result accepted: %v", err)
	}
}

// A completed HTTP upgrade precedes Hub registration; wait for this exact socket.
func dialVscreenTestConn(t *testing.T, hub *Hub, identity ClientIdentity) *websocket.Conn {
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
