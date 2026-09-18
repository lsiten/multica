//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func recoveryCommand(t *testing.T, d *Daemon, g mirrorControlGeneration, kind protocol.VscreenCommandKind, id string) protocol.VscreenCommandReceipt {
	t.Helper()
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: d.vscreenServerGeneration, RequestID: id}, CommandID: id, Kind: kind}
	var receipts []protocol.VscreenCommandReceipt
	d.handleVscreenCommand(context.Background(), mirrorOfferMessage{raw: marshalRaw(command), controlGeneration: g, enqueue: func(raw []byte) (*wsOutbound, error) {
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}
		var receipt protocol.VscreenCommandReceipt
		if err := json.Unmarshal(msg.Payload, &receipt); err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
		return &wsOutbound{}, nil
	}})
	if len(receipts) != 2 || receipts[0].State != protocol.VscreenReceiptPending {
		t.Fatalf("cleanup receipts: %+v", receipts)
	}
	return receipts[1]
}
func recoveryAck(d *Daemon, g mirrorControlGeneration, r protocol.VscreenCommandReceipt) {
	d.handleVscreenCleanupAck(mirrorOfferMessage{raw: marshalRaw(r), controlGeneration: g})
}

func TestVscreenExplicitDisableRetiresOldInterventionGate(t *testing.T) {
	for _, state := range []protocol.VscreenInterventionState{protocol.VscreenInterventionCancelled, protocol.VscreenInterventionStale, protocol.VscreenInterventionAwaitingTakeover} {
		t.Run(string(state), func(t *testing.T) {
			d := vscreenFixtureDaemon(t)
			g, ctx, cancel := d.beginMirrorControlConnection(context.Background())
			defer cancel()
			d.vscreenServerGeneration = "server"
			if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "enable"); r.State != protocol.VscreenReceiptSucceeded {
				t.Fatal(r)
			}
			actor, err := d.VscreenActor("ws", "rt")
			if err != nil {
				t.Fatal(err)
			}
			s := d.vscreenRuntime()
			s.interventions.records = map[string]*vscreenInterventionRecord{"rt": {Stopped: true, Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt"}, InterventionID: "old", SourceTaskID: "old-task", State: state, Epoch: actor.Status().Display.Epoch}}}
			if state == protocol.VscreenInterventionStale {
				s.interventions.records["rt"].Report.Epoch.NativeEpoch = "previous-native"
			}
			old := s.interventions.records["rt"].Report
			if err = d.validateVscreenContinuation(ctx, s, Task{ID: "forged-resume", WorkspaceID: "ws", RuntimeID: "rt", VscreenContinuation: &protocol.VscreenContinuationContext{InterventionID: old.InterventionID, SourceTaskID: old.SourceTaskID, Epoch: old.Epoch, ReturnReceiptID: "old-proof"}}, actor); err == nil {
				t.Fatal("obsolete intervention proof resumed")
			}
			if err = actor.Suspend(ctx); err != nil {
				t.Fatal(err)
			}
			// Empty Windows models a rejected launch before any ownership registration.
			if _, err = actor.Acquire(ctx, vscreen.Transaction{TaskID: "old-task", ID: "old"}); err == nil {
				t.Fatal("cancel/stale gate automatically resumed")
			}
			receipt := recoveryCommand(t, d, g, protocol.VscreenCommandDisable, "disable")
			if receipt.State != protocol.VscreenReceiptSucceeded {
				t.Fatal(receipt)
			}
			if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "early-enable"); r.State == protocol.VscreenReceiptSucceeded {
				t.Fatal("enable bypassed cleanup ACK")
			}
			forged := receipt
			forged.ReceiptID = "forged"
			recoveryAck(d, g, forged)
			if s.interventions.records["rt"] == nil {
				t.Fatal("forged cleanup receipt retired gate")
			}
			_, broker, execution, proofErr := d.startTaskVscreen(ctx, Task{ID: "old-continuation", WorkspaceID: "ws", RuntimeID: "rt", VscreenContinuation: &protocol.VscreenContinuationContext{InterventionID: old.InterventionID, SourceTaskID: old.SourceTaskID, Epoch: old.Epoch, ReturnReceiptID: "old-proof"}}, "claude", nil)
			if proofErr == nil || broker != nil || execution != nil {
				t.Fatal("disabled runtime accepted old continuation without native proof")
			}
			recoveryAck(d, g, receipt)
			if s.interventions.records["rt"] != nil {
				t.Fatal("acknowledged cleanup retained old intervention")
			}
			restored := &vscreenRuntime{}
			d.loadVscreenInterventions(restored)
			if restored.interventions.records["rt"] != nil {
				t.Fatal("retired gate restored from disk")
			}
			if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "fresh-enable"); r.State != protocol.VscreenReceiptSucceeded {
				t.Fatal(r)
			}
			if err = d.validateVscreenContinuation(ctx, s, Task{ID: "new-task", WorkspaceID: "ws", RuntimeID: "rt"}, actor); err != nil {
				t.Fatal(err)
			}
			lease, err := actor.Acquire(ctx, vscreen.Transaction{TaskID: "new-task", ID: "new-transaction"})
			if err != nil {
				t.Fatalf("safe explicit recovery remains blocked: %v", err)
			}
			request := protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: lease.Resource, TaskID: lease.TaskID, TransactionID: lease.TransactionID, LeaseEpoch: lease.LeaseEpoch, Epoch: actor.Status().Display.Epoch, WindowHandle: "old-window", SnapshotRevision: 1}, ActionID: "old-action", Sequence: 1, Action: protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{ElementHandle: "old-element"}}}
			if _, err = actor.Execute(ctx, request); err == nil {
				t.Fatal("old action replayed without fresh observation")
			}
			if err = actor.Release(ctx, lease); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVscreenCleanupLostAckAndRestartRequireFreshDisable(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	g, _, cancel := d.beginMirrorControlConnection(context.Background())
	defer cancel()
	d.vscreenServerGeneration = "old-server"
	if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "enable"); r.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(r)
	}
	old := recoveryCommand(t, d, g, protocol.VscreenCommandDisable, "lost-ack")
	s := d.vscreenRuntime()
	d.loadVscreenInterventions(s)
	s.commands = map[string]vscreenCachedCommand{}
	newer, _, stop := d.beginMirrorControlConnection(context.Background())
	defer stop()
	d.vscreenServerGeneration = "new-server"
	recoveryAck(d, newer, old)
	if r := recoveryCommand(t, d, newer, protocol.VscreenCommandEnable, "early"); r.State == protocol.VscreenReceiptSucceeded {
		t.Fatal("restart resurrected cleanup authority")
	}
	receipt := recoveryCommand(t, d, newer, protocol.VscreenCommandDisable, "retry-explicit-disable")
	if receipt.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(receipt)
	}
	recoveryAck(d, newer, receipt)
	if r := recoveryCommand(t, d, newer, protocol.VscreenCommandEnable, "fresh"); r.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(r)
	}
}

type recoveryFailInput struct{}

func (recoveryFailInput) Act(context.Context, vscreen.Action) (vscreen.ActionResult, error) {
	return vscreen.ActionResult{}, errors.New("owned fixture")
}
func (recoveryFailInput) Quiesce(context.Context, vscreen.ResourceKey) error {
	return errors.New("owned quiescence refused")
}
func (recoveryFailInput) Dispose(context.Context, vscreen.ResourceKey) error {
	return errors.New("unexpected disposal")
}
func TestVscreenDisableFailureAndRuntimeRemovalPreserveSibling(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	g, ctx, cancel := d.beginMirrorControlConnection(context.Background())
	defer cancel()
	d.vscreenServerGeneration = "server"
	if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "enable"); r.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(r)
	}
	s := d.vscreenRuntime()
	key, _ := d.vscreenResource("ws", "rt")
	s.interventions.records = map[string]*vscreenInterventionRecord{"rt": {Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt"}, InterventionID: "target"}}, "other": {Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "other"}, InterventionID: "sibling"}}}
	config, _ := reportTestConfig(t)
	reporter := reportTestNew(t, config)
	d.vscreenReporter = reporter
	target := reportTestRecord()
	target.WorkspaceID = "ws"
	target.RuntimeID = "rt"
	actor, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	target.Epoch = actor.Status().Display.Epoch
	sibling := reportTestRecord()
	sibling.WorkspaceID = "ws"
	sibling.RuntimeID = "other"
	for _, report := range []protocol.VscreenIntervention{target, sibling} {
		if err := reporter.Queue(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	d.SetVscreenInputHandler(recoveryFailInput{})
	if r := recoveryCommand(t, d, g, protocol.VscreenCommandDisable, "failed"); r.State != protocol.VscreenReceiptFailed {
		t.Fatal("failed native cleanup reported success", r)
	}
	if err := d.removeVscreenRuntime(ctx, s, "rt"); err == nil {
		t.Fatal("runtime removal ignored native quiescence failure")
	}
	if !s.enabled[key] || s.interventions.records["rt"] == nil || s.interventions.records["other"] == nil {
		t.Fatal("failed cleanup discarded authority or sibling")
	}
	reporter.mu.Lock()
	retained := len(reporter.disk.Entries)
	reporter.mu.Unlock()
	if retained != 2 {
		t.Fatal("failed disposal purged pending reports")
	}
	d.SetVscreenInputHandler(nil)
	if err := d.removeVscreenRuntime(ctx, s, "rt"); err != nil {
		t.Fatal(err)
	}
	if s.enabled[key] || s.interventions.records["rt"] != nil || s.interventions.records["other"] == nil {
		t.Fatal("runtime removal crossed scope")
	}
	reporter.mu.Lock()
	entries := append([]vscreenReportEntry(nil), reporter.disk.Entries...)
	reporter.mu.Unlock()
	if len(entries) != 1 || entries[0].Report.RuntimeID != "other" {
		t.Fatal("runtime removal discarded sibling outbox or retained target")
	}
	restored := &vscreenRuntime{}
	d.loadVscreenInterventions(restored)
	if restored.interventions.records["rt"] != nil || restored.interventions.records["other"] == nil {
		t.Fatal("runtime cleanup not durable or touched sibling")
	}
}

func TestVscreenDisableJoinsOldToolExecutionBeforeReplacement(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	g, _, stop := d.beginMirrorControlConnection(context.Background())
	defer stop()
	d.vscreenServerGeneration = "server"
	if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "enable"); r.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	task := Task{ID: "old-owner", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt"}
	_, broker, execution, err := d.startTaskVscreen(ctx, task, "claude", cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	defer broker.Close()
	if _, err = execution.acquire(ctx, vscreenToolArgs{RequestID: "old-owner-request"}); err != nil {
		t.Fatal(err)
	}
	receipt := recoveryCommand(t, d, g, protocol.VscreenCommandDisable, "dispose-owner")
	if receipt.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(receipt)
	}
	select {
	case <-execution.done:
	default:
		t.Fatal("successful disable left old tool lifecycle alive")
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) || errors.Is(context.Cause(ctx), errVscreenIntervention) {
		t.Fatal("explicit disable was reported as a new intervention")
	}
	recoveryAck(d, g, receipt)
	if r := recoveryCommand(t, d, g, protocol.VscreenCommandEnable, "replace"); r.State != protocol.VscreenReceiptSucceeded {
		t.Fatal(r)
	}
	actor, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.beginVscreenIntervention(task, actor, errVscreenIntervention); err == nil {
		t.Fatal("late old task recreated intervention on replacement scene")
	}
	if actor.Status().Frozen {
		t.Fatal("replacement remained frozen after safe cleanup")
	}
}
