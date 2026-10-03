package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAutomationClaimSharesWorkdirWithoutSharingProviderSession(t *testing.T) {
	ctx := t.Context()
	runtimeID := dbfx.Runtime(t, "workline-runtime", testutil.Cols{"runtime_mode": "local"})
	agentID := dbfx.Agent(t, "workline-agent", runtimeID)
	automation := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": testWorkspaceID, "title": "Workline", "assignee_id": agentID, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	firstRun := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": automation, "source": "manual", "status": "completed"})
	firstTask := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "autopilot_run_id": firstRun, "status": "completed", "work_dir": "/managed/automation/workdir", "session_id": "prior-session", "created_at": time.Now().Add(-time.Hour)})
	nextRun := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": automation, "source": "manual", "status": "running"})
	nextTask := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "autopilot_run_id": nextRun, "status": "dispatched"})
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(nextTask))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: parseUUID(runtimeID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	req := newDaemonTokenRequest(http.MethodPost, "/claim", nil, testWorkspaceID, "workline-daemon")
	response, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(req, &task, runtime, runtimeID, testWorkspaceID)
	if failure != nil {
		t.Fatalf("claim rejected: %+v", failure)
	}
	if response.PriorWorkDir != "/managed/automation/workdir" || response.PriorSessionID != "" {
		t.Fatalf("workline did not preserve code with a fresh per-run session: %q %q", response.PriorWorkDir, response.PriorSessionID)
	}
	otherRuntime := dbfx.Runtime(t, "other-workline-runtime")
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": otherRuntime, "autopilot_run_id": firstRun, "status": "completed", "work_dir": "/other-runtime/workdir"})
	otherAgent := dbfx.Agent(t, "other-workline-agent", runtimeID)
	dbfx.Task(t, otherAgent, testutil.Cols{"runtime_id": runtimeID, "autopilot_run_id": firstRun, "status": "completed", "work_dir": "/other-agent/workdir"})
	prior, err := testHandler.automationPriorWorkdir(ctx, task, parseUUID(testWorkspaceID), parseUUID(automation))
	if err != nil || prior.String != "/managed/automation/workdir" {
		t.Fatalf("decoy hijacked workline: %+v %v", prior, err)
	}
	task.RerunOfTaskID = parseUUID(firstTask)
	prior, err = testHandler.automationPriorWorkdir(ctx, task, parseUUID(testWorkspaceID), parseUUID(automation))
	if err != nil || prior.String != "/managed/automation/workdir" {
		t.Fatalf("manual retry lost its exact source: %+v %v", prior, err)
	}
	prior, err = testHandler.automationPriorWorkdir(ctx, task, parseUUID(testWorkspaceID), parseUUID("00000000-0000-0000-0000-000000000001"))
	if err != nil || prior.Valid {
		t.Fatalf("manual retry crossed automation: %+v %v", prior, err)
	}
}
