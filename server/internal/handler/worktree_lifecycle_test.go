package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTaskGCReportsRunAndIssueLifecycleSeparately(t *testing.T) {
	runtime := dbfx.Runtime(t, "lifecycle-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "lifecycle-agent", runtime)
	issue := dbfx.Issue(t, "Delivery still awaiting review", testutil.Cols{"status": "in_review"})
	completed := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": completed, "work_dir": "/retained/worktree"})

	r := newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "lifecycle-daemon")
	r = withURLParam(r, "taskId", task)
	var response protocol.TaskGCStatus
	testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)

	if response.Status != "completed" || !response.CompletedAt.Equal(completed) || !response.LifecycleSupported {
		t.Fatalf("run facts lost: %+v", response)
	}
	if response.IssueID != issue || response.IssueStatus != "in_review" || response.IssueStatusCategory != "started" || response.LastActivityAt == nil {
		t.Fatalf("issue facts lost: %+v", response)
	}
}

func TestTaskGCReportsAuthoritativeChatAndAutomationParents(t *testing.T) {
	runtime := dbfx.Runtime(t, "gc-parent-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "gc-parent-agent", runtime)
	chat := dbfx.Insert(t, "chat_session", testutil.Cols{"workspace_id": testWorkspaceID, "agent_id": agent, "creator_id": testUserID, "status": "archived"})
	automation := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": testWorkspaceID, "title": "GC parent", "assignee_id": agent, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	run := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": automation, "source": "manual", "status": "completed", "completed_at": time.Now()})
	for _, parent := range []struct{ column, id string }{{"chat_session_id", chat}, {"autopilot_run_id", run}} {
		t.Run(parent.column, func(t *testing.T) {
			task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "completed", "completed_at": time.Now(), parent.column: parent.id})
			r := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "gc-parent-daemon"), "taskId", task)
			var response protocol.TaskGCStatus
			testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)
			if response.WorkspaceID != testWorkspaceID || response.RuntimeID != runtime || response.AgentID != agent || !response.LifecycleSupported {
				t.Fatalf("execution scope missing: %+v", response)
			}
			if parent.column == "chat_session_id" && response.ChatSessionID != chat || parent.column == "autopilot_run_id" && response.AutopilotRunID != run {
				t.Fatalf("authoritative parent missing: %+v", response)
			}
		})
	}
}

func TestCloudWorktreeInventoryDoesNotInferLocalApproval(t *testing.T) {
	runtime := dbfx.Runtime(t, "cloud-inventory-lifecycle", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "cloud-lifecycle-agent", runtime)
	issue := dbfx.Issue(t, "Reviewed business issue", testutil.Cols{"status": "done"})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": time.Now().Add(-10 * 24 * time.Hour), "work_dir": "/retained/worktree"})
	handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))

	var rows []localReviewWorktreeResponse
	testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees?issue_id="+issue, nil)).Want(http.StatusOK).JSON(&rows)

	if len(rows) != 1 || rows[0].RunStatus != "completed" || rows[0].IssueStatus != "done" || rows[0].IssueStatusCategory != "done" {
		t.Fatalf("missing lifecycle: %+v", rows)
	}
	if rows[0].NextAction != protocol.WorktreeUnknown || rows[0].Stale || len(rows[0].RepositoriesDetails) != 0 {
		t.Fatalf("cloud guessed local safety or ignored recent business activity: %+v", rows[0])
	}
}
