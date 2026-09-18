package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func interventionTransportReport(c *client) protocol.VscreenIntervention {
	return protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: c.vscreenGeneration, RequestID: "request"}, InterventionID: "intervention", AgentID: "agent", SourceTaskID: "task", Reason: protocol.VscreenRejectionReason("background_unsupported"), State: protocol.VscreenInterventionAwaitingTakeover, Epoch: protocol.VscreenEpoch{NativeEpoch: "native", DisplayGeneration: "display", GeometryRevision: 1}}
}

func TestVscreenInterventionTransportBackpressureAndDisconnect(t *testing.T) {
	hub := NewHub()
	identity := ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}}
	conn := dialVscreenTestConn(t, hub, identity)
	hub.mu.RLock()
	c := hub.vscreenClientLocked("ws", "rt", "daemon")
	hub.mu.RUnlock()
	entered := make(chan struct{}, maxInFlightRPCPerClient)
	exited := make(chan struct{}, maxInFlightRPCPerClient)
	hub.SetVscreenInterventionHandler(func(ctx context.Context, _ VscreenConnection, _ protocol.VscreenIntervention) (int64, string) {
		entered <- struct{}{}
		<-ctx.Done()
		exited <- struct{}{}
		return 0, "daemon_unavailable"
	})
	report := interventionTransportReport(c)
	for range maxInFlightRPCPerClient {
		c.handleVscreenIntervention(mustMarshalRaw(report))
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("handler not entered")
		}
	}
	c.handleVscreenIntervention(mustMarshalRaw(report))
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var ack protocol.VscreenInterventionAck
	if err := json.Unmarshal(frame.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Accepted || ack.Reason != "request_capacity" {
		t.Fatalf("ack=%+v", ack)
	}
	conn.Close()
	for range maxInFlightRPCPerClient {
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Fatal("handler outlived disconnected socket")
		}
	}
	t.Log("eight owned RPC slots; ninth rejected; all eight cancel on connection close")
}

func TestVscreenInterventionTransportReplacementFencesPendingCommit(t *testing.T) {
	hub := NewHub()
	identity := ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}}
	conn := dialVscreenTestConn(t, hub, identity)
	hub.mu.RLock()
	c := hub.vscreenClientLocked("ws", "rt", "daemon")
	hub.mu.RUnlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var persisted atomic.Bool
	hub.SetVscreenInterventionHandler(func(ctx context.Context, scope VscreenConnection, _ protocol.VscreenIntervention) (int64, string) {
		close(entered)
		<-release
		err := scope.WithCurrent(ctx, func() error { persisted.Store(true); return nil })
		if !errors.Is(err, ErrVscreenStale) {
			t.Errorf("commit fence=%v", err)
		}
		return 0, "stale_generation"
	})
	c.handleVscreenIntervention(mustMarshalRaw(interventionTransportReport(c)))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not entered")
	}
	dialVscreenTestConn(t, hub, identity)
	close(release)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var frame protocol.Message
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var ack protocol.VscreenInterventionAck
	if err := json.Unmarshal(frame.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if persisted.Load() || ack.Accepted || ack.Reason != "stale_generation" {
		t.Fatalf("persisted=%v ack=%+v", persisted.Load(), ack)
	}
	t.Log("replacement after handler admission prevents old socket persistence")
}
