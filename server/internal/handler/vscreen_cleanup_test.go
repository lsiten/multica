package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenDisableReceiptRetiresPendingRowsAndAllowsFreshScene(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		name := "stale_scene"
		if cancelFirst {
			name = "cancelled_scene"
		}
		t.Run(name, func(t *testing.T) {
			f := newInterventionFixture(t, "issue")
			old := f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, http.StatusOK)
			sibling := newInterventionFixture(t, "issue")
			siblingRow := sibling.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, http.StatusOK)
			if cancelFirst {
				request := testutil.WithURLParams(newRequest(http.MethodPost, "/cancel", nil), "taskId", f.report.SourceTaskID, "id", f.report.InterventionID)
				testutil.Call(t, f.h.CancelVscreenIntervention, request).Want(http.StatusOK)
				f.mu.Lock()
				control := f.snapshot.ControlState
				f.mu.Unlock()
				if control != protocol.VscreenControlAwaitingTakeover {
					t.Fatal("cancel unexpectedly resumed native authority")
				}
			}
			f.h.DaemonHub.SetVscreenDisabledHandler(f.h.DaemonVscreenDisabled)
			f.mu.Lock()
			f.cleanupResult = protocol.VscreenReceiptSucceeded
			f.cleanupAcks = make(chan protocol.VscreenCommandReceipt, 4)
			acks := f.cleanupAcks
			f.mu.Unlock()
			rt, err := f.h.Queries.GetAgentRuntime(context.Background(), old.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := f.h.DaemonHub.SubmitVscreenCommand(f.report.WorkspaceID, f.report.RuntimeID, rt.DaemonID.String, testUserID, uuid.NewString(), protocol.VscreenCommandDisable)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case ack := <-acks:
				if ack.CommandID != receipt.CommandID || ack.State != protocol.VscreenReceiptSucceeded {
					t.Fatal(ack)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cleanup acknowledgement did not arrive")
			}
			row, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: old.ID, WorkspaceID: old.WorkspaceID})
			if err != nil || row.State != "cancelled" {
				t.Fatalf("cleanup ACK preceded persisted retirement: %+v %v", row, err)
			}
			row, err = f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: siblingRow.ID, WorkspaceID: siblingRow.WorkspaceID})
			if err != nil || row.State != "awaiting_takeover" {
				t.Fatal("cleanup touched sibling", err)
			}
			source, err := f.h.Queries.GetAgentTask(context.Background(), old.SourceTaskID)
			if err != nil || source.Status != "failed" {
				t.Fatal("cleanup resumed source run", err)
			}
			newSource := dbfx.Task(t, f.report.AgentID, testutil.Cols{"runtime_id": f.report.RuntimeID, "issue_id": uuidToString(source.IssueID), "status": "failed", "completed_at": testutil.Raw("now()"), "failure_reason": "gui_human_intervention"})
			dbfx.Cleanup(t, "DELETE FROM runtime_vscreen_intervention WHERE source_task_id=$1", newSource)
			f.report.SourceTaskID = newSource
			f.report.InterventionID = uuid.NewString()
			f.report.Epoch.DisplayGeneration = "fresh-display"
			f.mu.Lock()
			f.snapshot.State = protocol.VscreenStateReady
			f.snapshot.ControlState = protocol.VscreenControlAwaitingTakeover
			f.snapshot.InterventionID = &f.report.InterventionID
			f.snapshot.DisplayGeneration = f.report.Epoch.DisplayGeneration
			f.mu.Unlock()
			f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, http.StatusOK)
			t.Log("native disabled query + current-socket command receipt committed cancellation before ACK; original failed task and sibling preserved; fresh scene persisted without pending_conflict")
		})
	}
}

func TestVscreenFailedDisableDoesNotRetirePendingRow(t *testing.T) {
	f := newInterventionFixture(t, "issue")
	old := f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, http.StatusOK)
	f.h.DaemonHub.SetVscreenDisabledHandler(f.h.DaemonVscreenDisabled)
	f.mu.Lock()
	f.cleanupResult = protocol.VscreenReceiptFailed
	f.mu.Unlock()
	rt, err := f.h.Queries.GetAgentRuntime(context.Background(), old.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.h.DaemonHub.SubmitVscreenCommand(f.report.WorkspaceID, f.report.RuntimeID, rt.DaemonID.String, testUserID, uuid.NewString(), protocol.VscreenCommandDisable)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		status, err := f.h.DaemonHub.VscreenCommandStatus(f.report.WorkspaceID, f.report.RuntimeID, rt.DaemonID.String, testUserID, receipt.CommandID, true)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == protocol.VscreenReceiptFailed {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("failed native receipt not observed")
		case <-tick.C:
		}
	}
	row, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: old.ID, WorkspaceID: old.WorkspaceID})
	if err != nil || row.State != "awaiting_takeover" {
		t.Fatal("failed disposal retired authority", err)
	}
}
