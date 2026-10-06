package handler

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	if !response.RetentionSupported || response.CurrentWorkDir != "/retained/worktree" || response.CurrentTaskID != task {
		t.Fatalf("current workline missing: %+v", response)
	}
}

func TestTaskGCIdentifiesSupersededDirectoryWithoutLosingReviewTask(t *testing.T) {
	runtime := dbfx.Runtime(t, "gc-workline-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "gc-workline-agent", runtime)
	issue := dbfx.Issue(t, "Current delivery", testutil.Cols{"status": "in_review"})
	first := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": time.Now(), "work_dir": "/old/workdir", "created_at": time.Now().Add(-time.Hour)})
	latest := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": time.Now(), "work_dir": "/current/workdir"})
	r := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "gc-workline-daemon"), "taskId", first)
	var response protocol.TaskGCStatus
	testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)
	if response.WorkDir != "/old/workdir" || response.CurrentWorkDir != "/current/workdir" || response.CurrentTaskID != latest || response.IssueStatus != "in_review" {
		t.Fatalf("historical workdir confused with current delivery: %+v", response)
	}
}

func TestBatchTaskGCIsRuntimeScopedAndReportsPendingHumanRequest(t *testing.T) {
	runtime := dbfx.Runtime(t, "gc-batch-runtime", testutil.Cols{"runtime_mode": "local"})
	otherRuntime := dbfx.Runtime(t, "gc-other-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "gc-batch-agent", runtime)
	issue := dbfx.Issue(t, "Waiting for input", testutil.Cols{"status": "blocked"})
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": time.Now(), "work_dir": "/current/workdir"})
	foreign := dbfx.Task(t, agent, testutil.Cols{"runtime_id": otherRuntime, "status": "completed"})
	dbfx.Insert(t, "human_request", testutil.Cols{"id": task, "workspace_id": testWorkspaceID, "source_task_id": task, "agent_id": agent, "recipient_id": testUserID, "issue_id": issue, "request_key": "input", "payload": "{}", "expires_at": time.Now().Add(time.Hour)})
	body := map[string]any{"task_ids": []string{task, foreign}}
	r := newDaemonTokenRequest(http.MethodPost, "/gc-check", body, testWorkspaceID, "gc-batch-daemon")
	r = withURLParam(r, "runtimeId", runtime)
	chi.RouteContext(r.Context()).URLParams.Add("workspaceId", testWorkspaceID)
	var response protocol.TaskGCBatch
	testutil.Call(t, testHandler.BatchTaskGCCheck, r).Want(http.StatusOK).JSON(&response)
	if len(response.Tasks) != 1 || response.Tasks[0].TaskID != task || !response.Tasks[0].WaitingHuman || response.Tasks[0].IssueStatus != "blocked" {
		t.Fatalf("runtime isolation or human-request evidence lost: %+v", response)
	}
}

func TestBatchTaskGCConfirmsMissingTasksWithoutTreatingForeignRuntimeAsMissing(t *testing.T) {
	runtime := dbfx.Runtime(t, "gc-deleted-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "gc-deleted-agent", runtime)
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "completed"})
	foreign := dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "status": "completed"})
	dbfx.Exec(t, `DELETE FROM agent_task_queue WHERE id = $1`, task)
	r := newDaemonTokenRequest(http.MethodPost, "/gc-check", map[string]any{"task_ids": []string{task, foreign}}, testWorkspaceID, "gc-deleted-daemon")
	r = withURLParam(r, "workspaceId", testWorkspaceID)
	chi.RouteContext(r.Context()).URLParams.Add("runtimeId", runtime)
	var response protocol.TaskGCBatch
	testutil.Call(t, testHandler.BatchTaskGCCheck, r).Want(http.StatusOK).JSON(&response)
	if len(response.Tasks) != 1 || !response.Tasks[0].Missing || response.Tasks[0].TaskID != task || response.Tasks[0].WorkspaceID != testWorkspaceID {
		t.Fatalf("missing task confused with out-of-scope task: %+v", response)
	}
}

func TestTaskGCOldAgentDoesNotPinEveryHistoricalWorklineOfAnOpenIssue(t *testing.T) {
	runtime := dbfx.Runtime(t, "gc-current-agent-runtime", testutil.Cols{"runtime_mode": "local"})
	oldAgent := dbfx.Agent(t, "gc-previous-contributor", runtime)
	newAgent := dbfx.Agent(t, "gc-current-contributor", runtime)
	issue := dbfx.Issue(t, "Member-owned task", testutil.Cols{"status": "in_progress", "assignee_type": "member", "assignee_id": testUserID})
	oldTask := dbfx.Task(t, oldAgent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "work_dir": "/old/workdir", "created_at": time.Now().Add(-time.Hour)})
	dbfx.Task(t, newAgent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "work_dir": "/current/workdir"})
	r := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "gc-current-agent-daemon"), "taskId", oldTask)
	var response protocol.TaskGCStatus
	testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)
	if response.CurrentAgent || response.IssueStatusCategory != "started" || !response.RetentionSupported {
		t.Fatalf("old contributor pins the open task indefinitely: %+v", response)
	}
}

func TestTaskGCBatchRejectsMalformedInputBeforeDeletionFacts(t *testing.T) {
	for _, body := range []string{`{}`, `{"task_ids":[]}`, `{"task_ids":["not-a-uuid"]}`, `{"task_ids":[],"path":"/tmp"}`, `{"task_ids":["11111111-1111-4111-8111-111111111111"]} {}`} {
		r := newDaemonTokenRequest(http.MethodPost, "/gc-check", nil, testWorkspaceID, "gc-malformed-daemon")
		r.Body = io.NopCloser(bytes.NewBufferString(body))
		r = withURLParam(r, "workspaceId", testWorkspaceID)
		testutil.Call(t, testHandler.BatchTaskGCCheck, r).Want(http.StatusBadRequest)
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
