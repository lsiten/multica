package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/vscreen"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenContinuationRequiresCurrentProofAndFreshObservation(t *testing.T) {
	f, a := newVscreenToolFixture(t)
	d := newTestDaemon(t)
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(t.TempDir(), "preferences")
	s := d.vscreenRuntime()
	epoch := f.display.Epoch
	lease, err := a.Acquire(context.Background(), vscreen.Transaction{TaskID: "source", ID: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.RegisterObservation(vscreen.Observation{Lease: lease, Epoch: epoch, WindowHandle: "window", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = a.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := &vscreenInterventionRecord{Stopped: true, Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "workspace", RuntimeID: "runtime"}, InterventionID: "intervention", SourceTaskID: "source", AgentID: "agent", State: protocol.VscreenInterventionReadyToContinue, Epoch: epoch, ReturnReceiptID: "receipt"}}
	s.interventions.records = map[string]*vscreenInterventionRecord{"runtime": record}
	task := Task{ID: "new", AgentID: "agent", RuntimeID: "runtime", WorkspaceID: "workspace", VscreenContinuation: &protocol.VscreenContinuationContext{InterventionID: "intervention", SourceTaskID: "source", Epoch: epoch, ReturnReceiptID: "forged"}}
	if err = d.validateVscreenContinuation(context.Background(), s, task, a); err == nil {
		t.Fatal("forged receipt accepted")
	}
	if !a.Status().Frozen {
		t.Fatal("forged receipt opened gate")
	}
	task.VscreenContinuation.ReturnReceiptID = "receipt"
	if err = d.validateVscreenContinuation(context.Background(), s, task, a); err != nil {
		t.Fatal(err)
	}
	current, err := a.Acquire(context.Background(), vscreen.Transaction{TaskID: "new", ID: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	action := protocol.VscreenActionRequest{Target: protocol.VscreenActionTarget{Resource: current.Resource, TaskID: "new", TransactionID: "fresh", LeaseEpoch: current.LeaseEpoch, Epoch: epoch, WindowHandle: "window", SnapshotRevision: 1}, ActionID: "old-click", Sequence: 1, Action: protocol.VscreenAction{Kind: protocol.VscreenActionClick, Click: &protocol.VscreenClickAction{ElementHandle: "button"}}}
	if _, err = a.Execute(context.Background(), action); err == nil {
		t.Fatal("continued task reused old snapshot")
	}
	if err = a.RegisterObservation(vscreen.Observation{Lease: current, Epoch: epoch, WindowHandle: "window", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	action.Target.SnapshotRevision = 2
	if _, err = a.Execute(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if err = d.validateVscreenContinuation(context.Background(), s, task, a); err == nil {
		t.Fatal("consumed proof reused")
	}
}
func TestVscreenContinuationClaimWireName(t *testing.T) {
	var task Task
	if err := json.Unmarshal([]byte(`{"id":"new","vscreen_intervention":{"intervention_id":"proof","source_task_id":"source","return_receipt_id":"receipt"}}`), &task); err != nil {
		t.Fatal(err)
	}
	if task.VscreenContinuation == nil || task.VscreenContinuation.InterventionID != "proof" {
		t.Fatal("server protected continuation lost at claim boundary")
	}
}
func TestVscreenInterventionRestartNeverRestoresAuthority(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(t.TempDir(), "preferences")
	s := d.vscreenRuntime()
	s.interventions.records = map[string]*vscreenInterventionRecord{"runtime": {Stopped: true, Report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{RuntimeID: "runtime"}, State: protocol.VscreenInterventionReadyToContinue, ReturnReceiptID: "old-proof"}}}
	if err := d.persistVscreenInterventionsLocked(s); err != nil {
		t.Fatal(err)
	}
	restored := &vscreenRuntime{}
	d.loadVscreenInterventions(restored)
	record := restored.interventions.records["runtime"]
	if record == nil || record.Report.State != protocol.VscreenInterventionStale || record.Report.ReturnReceiptID != "" || record.Authority.TaskID != "" {
		t.Fatal("restart revived native authority")
	}
}
