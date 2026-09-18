package daemonws

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenDisableAcknowledgementAfterCommitAndReplay(t *testing.T) {
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	entered := make(chan struct{})
	commit := make(chan struct{})
	var calls atomic.Int32
	hub.SetVscreenDisabledHandler(func(ctx context.Context, scope VscreenConnection, receipt protocol.VscreenCommandReceipt) error {
		calls.Add(1)
		close(entered)
		select {
		case <-commit:
			return scope.WithCurrent(ctx, func() error { return nil })
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	accepted, err := hub.SubmitVscreenCommand("ws", "rt", "daemon", "owner", "disable", protocol.VscreenCommandDisable)
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if conn.ReadJSON(&frame) != nil {
		t.Fatal("command missing")
	}
	var command protocol.VscreenCommand
	if json.Unmarshal(frame.Payload, &command) != nil {
		t.Fatal("command malformed")
	}
	native := accepted
	native.ReceiptID = "native-disposed"
	native.State = protocol.VscreenReceiptSucceeded
	if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(native)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup callback not entered")
	}
	current, err := hub.VscreenCommandStatus("ws", "rt", "daemon", "owner", "disable", true)
	if err != nil || current.State != protocol.VscreenReceiptRunning {
		t.Fatalf("published success before DB cleanup: %+v %v", current, err)
	}
	close(commit)
	if err = conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	var ack protocol.VscreenCommandReceipt
	if frame.Type != protocol.EventVscreenResult || json.Unmarshal(frame.Payload, &ack) != nil || ack.ReceiptID != native.ReceiptID || ack.State != protocol.VscreenReceiptSucceeded {
		t.Fatal("cleanup ACK mismatch")
	}
	if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(native)}); err != nil {
		t.Fatal(err)
	}
	if err = conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("lost-ACK replay repeated database operation")
	}
}

func TestVscreenLifecycleGateHonorsCancellation(t *testing.T) {
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	accepted, err := hub.SubmitVscreenCommand("ws", "rt", "daemon", "owner", "gate", protocol.VscreenCommandEnable)
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if conn.ReadJSON(&frame) != nil {
		t.Fatal("command missing")
	}
	scope, err := hub.VscreenConnection(accepted.VscreenEnvelope, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- scope.WithVscreenLifecycle(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = scope.WithVscreenLifecycle(ctx, func() error { t.Error("cancelled lifecycle operation executed"); return nil }); err == nil {
		t.Fatal("cancelled gate wait succeeded")
	}
	close(release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestVscreenCleanupPersistenceFailureHasNoSuccessAck(t *testing.T) {
	hub := NewHub()
	conn := dialVscreenTestConn(t, hub, ClientIdentity{DaemonID: "daemon", WorkspaceID: "ws", RuntimeIDs: []string{"rt"}})
	hub.SetVscreenDisabledHandler(func(context.Context, VscreenConnection, protocol.VscreenCommandReceipt) error {
		return ErrVscreenUnavailable
	})
	receipt, err := hub.SubmitVscreenCommand("ws", "rt", "daemon", "owner", "failed-store", protocol.VscreenCommandDisable)
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if err = conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	receipt.State = protocol.VscreenReceiptSucceeded
	receipt.ReceiptID = "native-disposed"
	if err = conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenResult, Payload: mustMarshalRaw(receipt)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		status, err := hub.VscreenCommandStatus("ws", "rt", "daemon", "owner", "failed-store", true)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == protocol.VscreenReceiptSucceeded {
			t.Fatal("failed persistence reported cleanup success")
		}
		if status.State == protocol.VscreenReceiptUnknown {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("cleanup callback did not settle")
		case <-tick.C:
		}
	}
	conn.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
	if err = conn.ReadJSON(&frame); err == nil {
		t.Fatal("failed persistence emitted cleanup ACK")
	}
}
