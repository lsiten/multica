package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenContinuationReclaimsExactAcknowledgedHistory(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.NativeVscreenPreferencesPath = filepath.Join(t.TempDir(), "preferences")
	f, actor := newVscreenToolFixture(t)
	s := d.vscreenRuntime()
	config, results := reportTestConfig(t)
	config.AckTimeout = time.Second
	reporter := reportTestNew(t, config)
	d.vscreenReporter = reporter
	sender, reports := reportTestSender(t)
	binding := reporter.Bind("server", sender)
	var pending protocol.VscreenIntervention
	for cycle := 0; cycle < 43; cycle++ {
		report := reportTestRecord()
		report.WorkspaceID = "workspace"
		report.RuntimeID = "runtime"
		report.AgentID = "agent"
		report.Epoch = f.display.Epoch
		for version, state := range []protocol.VscreenInterventionState{protocol.VscreenInterventionAwaitingTakeover, protocol.VscreenInterventionHuman, protocol.VscreenInterventionReadyToContinue} {
			report.State = state
			report.RequestID = uuid.NewString()
			if state == protocol.VscreenInterventionReadyToContinue {
				report.ReturnReceiptID = uuid.NewString()
			}
			if err := reporter.Queue(context.Background(), report); err != nil {
				t.Fatalf("cycle %d state %s: %v", cycle, state, err)
			}
			sent := reportTestReceive(t, reports)
			if !reportTestAck(reporter, binding, sent, int64(version+1), "") {
				t.Fatal("ack rejected")
			}
			if result := reportTestReceive(t, results); result.reason != "" {
				t.Fatal(result.reason)
			}
		}
		if cycle == 42 {
			pending = reportTestRecord()
			pending.WorkspaceID = report.WorkspaceID
			pending.RuntimeID = report.RuntimeID
			pending.Epoch = report.Epoch
			if err := reporter.Queue(context.Background(), pending); err != nil {
				t.Fatal(err)
			}
			reportTestReceive(t, reports)
		}
		s.interventions.records = map[string]*vscreenInterventionRecord{"runtime": {Report: report, Stopped: true}}
		task := Task{ID: fmt.Sprintf("continuation-%d", cycle), AgentID: "agent", WorkspaceID: "workspace", RuntimeID: "runtime", VscreenContinuation: &protocol.VscreenContinuationContext{InterventionID: report.InterventionID, SourceTaskID: report.SourceTaskID, Epoch: report.Epoch, ReturnReceiptID: report.ReturnReceiptID}}
		if err := d.validateVscreenContinuation(context.Background(), s, task, actor); err != nil {
			t.Fatal(err)
		}
	}
	reporter.mu.Lock()
	entries := append([]vscreenReportEntry(nil), reporter.disk.Entries...)
	flight := reporter.flight
	reporter.mu.Unlock()
	if len(entries) != 1 || entries[0].Report.InterventionID != pending.InterventionID || entries[0].Version != 0 || flight == nil || flight.interventionID != pending.InterventionID {
		t.Fatalf("unacknowledged sibling changed or consumed history retained: entries=%d flight=%+v", len(entries), flight)
	}
	t.Log("43 same-epoch completed intervention cycles reclaimed; exact same-runtime pending intervention and in-flight report preserved")
}

func TestVscreenConsumedReportProofAndDurableScope(t *testing.T) {
	config, _ := reportTestConfig(t)
	reporter := reportTestNew(t, config)
	report := reportTestRecord()
	for _, state := range []protocol.VscreenInterventionState{protocol.VscreenInterventionAwaitingTakeover, protocol.VscreenInterventionHuman, protocol.VscreenInterventionReadyToContinue} {
		report.State = state
		report.RequestID = uuid.NewString()
		if state == protocol.VscreenInterventionReadyToContinue {
			report.ReturnReceiptID = "owned-receipt"
		}
		if err := reporter.Queue(context.Background(), report); err != nil {
			t.Fatal(err)
		}
	}
	proof := protocol.VscreenContinuationContext{InterventionID: report.InterventionID, SourceTaskID: report.SourceTaskID, Epoch: report.Epoch, ReturnReceiptID: report.ReturnReceiptID}
	forged := proof
	forged.ReturnReceiptID = "forged"
	if err := reporter.Consume(report.WorkspaceID, report.RuntimeID, forged); err == nil {
		t.Fatal("forged receipt consumed report history")
	}
	if err := reporter.Consume("foreign", report.RuntimeID, proof); err == nil {
		t.Fatal("foreign workspace consumed report history")
	}
	sibling := reportTestRecord()
	sibling.RuntimeID = report.RuntimeID
	sibling.WorkspaceID = report.WorkspaceID
	if err := reporter.Queue(context.Background(), sibling); err != nil {
		t.Fatal(err)
	}
	// The exact server-protected continuation is also sufficient when the last
	// socket lost its ack; unrelated unacknowledged reports remain untouched.
	if err := reporter.Consume(report.WorkspaceID, report.RuntimeID, proof); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Consume(report.WorkspaceID, report.RuntimeID, proof); err != nil {
		t.Fatal("consumption not idempotent", err)
	}
	if err := reporter.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reportTestNew(t, config)
	reopened.mu.Lock()
	entries := append([]vscreenReportEntry(nil), reopened.disk.Entries...)
	reopened.mu.Unlock()
	if len(entries) != 1 || entries[0].Report.InterventionID != sibling.InterventionID || entries[0].Version != 0 {
		t.Fatalf("durable consumption changed sibling: %+v", entries)
	}
}
