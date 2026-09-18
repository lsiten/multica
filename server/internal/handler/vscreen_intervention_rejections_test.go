package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionRejectedContinuationPreservesProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reason string
	}{
		{"missing_rollout", 409, "resume_unavailable"}, {"missing_session", 409, "resume_unavailable"},
		{"pending_slot", 409, "pending_conflict"}, {"stale_native", 409, "return_unverified"},
		{"browser_proof", 400, "invalid_request"}, {"machine_actor", 403, "permission_denied"},
		{"source_succeeded", 409, "source_not_stopped"}, {"agent_moved", 403, "permission_denied"},
		{"cancelled", 409, "intervention_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a stopped source and durable, native-confirmed return receipt.
			f := newInterventionFixture(t, "issue")
			f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
			f.sendReport(t, protocol.VscreenInterventionHuman, 200)
			row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
			body := map[string]any{"human_summary": "summary"}
			switch tc.name {
			case "missing_rollout":
				dbfx.Exec(t, "UPDATE agent_task_queue SET session_rollout_missing=true WHERE id=$1", f.report.SourceTaskID)
			case "missing_session":
				dbfx.Exec(t, "UPDATE agent_task_queue SET session_id=NULL WHERE id=$1", f.report.SourceTaskID)
			case "pending_slot":
				source, err := f.h.Queries.GetAgentTask(context.Background(), parseUUID(f.report.SourceTaskID))
				if err != nil {
					t.Fatal(err)
				}
				dbfx.Task(t, f.report.AgentID, testutil.Cols{"runtime_id": f.report.RuntimeID, "issue_id": source.IssueID})
			case "stale_native":
				f.mu.Lock()
				f.snapshot.NativeEpoch = "restarted-native"
				f.mu.Unlock()
			case "browser_proof":
				body["return_receipt_id"] = "self-asserted-proof"
			case "source_succeeded":
				dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed' WHERE id=$1", f.report.SourceTaskID)
			case "agent_moved":
				dbfx.Exec(t, "UPDATE agent SET runtime_id=NULL WHERE id=$1", f.report.AgentID)
			case "cancelled":
				dbfx.Exec(t, "UPDATE runtime_vscreen_intervention SET state='cancelled',version=version+1 WHERE id=$1", f.report.InterventionID)
			}
			// When a human or forged machine request attempts continuation.
			req := f.continueRequest(body)
			if tc.name == "machine_actor" {
				req.Header.Set("X-Actor-Source", "task_token")
			}
			var out struct {
				Reason string `json:"reason"`
			}
			testutil.Call(t, f.h.ContinueVscreenIntervention, req).Want(tc.status).JSON(&out)
			// Then no new task exists and the return proof is unconsumed.
			if out.Reason != tc.reason {
				t.Fatalf("reason=%q want %q", out.Reason, tc.reason)
			}
			stored, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
			if err != nil {
				t.Fatal(err)
			}
			if stored.ContinuationTaskID.Valid || stored.ReturnReceiptID != row.ReturnReceiptID {
				t.Fatalf("proof consumed on rejection: %+v", stored)
			}
			if n := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id=$1", f.report.SourceTaskID); n != 0 {
				t.Fatalf("children=%d", n)
			}
			t.Logf("POST %s HTTP=%d JSON={\"reason\":%q}; DB child_count=0 receipt_unconsumed=true", req.URL.Path, tc.status, out.Reason)
		})
	}
}

func TestVscreenInterventionSourceMustHaveStopped(t *testing.T) {
	for _, status := range []string{"running", "completed"} {
		t.Run(status, func(t *testing.T) {
			// Given a runtime that has not confirmed a failed stopped source.
			f := newInterventionFixture(t, "issue")
			dbfx.Exec(t, "UPDATE agent_task_queue SET status=$2 WHERE id=$1", f.report.SourceTaskID, status)
			// When its daemon reports an intervention.
			f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, http.StatusConflict)
			// Then no persisted handoff can be continued.
			if n := dbfx.Count(t, "SELECT count(*) FROM runtime_vscreen_intervention WHERE source_task_id=$1", f.report.SourceTaskID); n != 0 {
				t.Fatalf("interventions=%d", n)
			}
		})
	}
}

func TestVscreenInterventionExplicitFreshSession(t *testing.T) {
	// Given missing source rollout and a native-confirmed return.
	f := newInterventionFixture(t, "chat")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	dbfx.Exec(t, "UPDATE agent_task_queue SET session_rollout_missing=true WHERE id=$1", f.report.SourceTaskID)
	// When the human explicitly chooses a new session.
	var out struct {
		TaskID string `json:"task_id"`
	}
	testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{"fresh_session": true, "human_summary": "completed local step"})).Want(200).JSON(&out)
	// Then claim keeps source workdir and human summary, but no provider session.
	child, err := f.h.Queries.GetAgentTask(context.Background(), parseUUID(out.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	resp := AgentTaskResponse{WorkspaceID: testWorkspaceID, PriorSessionID: "other-session"}
	if err = f.h.applyInterventionClaim(context.Background(), child, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.PriorSessionID != "" || resp.HandoffNote != "completed local step" || resp.PriorWorkDir != "/tmp/test-owned-workdir" {
		t.Fatalf("claim=%+v", resp)
	}
	t.Logf("HTTP task_id=%s claim.session_empty=true exact_workdir=true human_summary=true", out.TaskID)
}
