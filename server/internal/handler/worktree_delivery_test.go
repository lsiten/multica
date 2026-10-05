package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestSharedWorktreeDeliverySurvivesLateTerminalCallback(t *testing.T) {
	ctx := context.Background()
	runtime := handlerTestRuntimeID(t)
	agent := dbfx.Agent(t, "Shared delivery agent", runtime)
	issue := dbfx.Issue(t, "Shared delivery issue")
	taskID := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "running", "work_dir": "/shared-code"})
	commit := strings.Repeat("a", 40)
	request := func(body any) {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/daemon/tasks/"+taskID+"/worktree-delivery", body)
		req = withURLParam(req, "taskId", taskID)
		testutil.Call(t, testHandler.RecordWorktreeDelivery, req).Want(http.StatusOK)
	}
	request(map[string]string{"work_dir": "/shared-code", "branch_name": "agent/shared", "worktree_commit": commit})
	completed, changed, err := testHandler.TaskService.CompleteTaskWithTransition(ctx, parseUUID(taskID), []byte(`{"output":"done","worktree_delivery_pending":true}`), "", "/shared-code", "", false, "", "")
	if err != nil || !changed {
		t.Fatalf("late callback failed: %v", err)
	}
	var result struct {
		Pending bool   `json:"worktree_delivery_pending"`
		Commit  string `json:"worktree_commit"`
	}
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Pending || result.Commit != commit || completed.BranchName.String != "agent/shared" {
		t.Fatalf("late completion overwrote final receipt: %+v %+v", result, completed)
	}
	request(map[string]string{"work_dir": "/shared-code", "branch_name": "agent/shared", "worktree_commit": commit})
}

func TestSharedWorktreeDeliveryRejectsMalformedAndChangedScope(t *testing.T) {
	runtime := handlerTestRuntimeID(t)
	agent := dbfx.Agent(t, "Delivery validation agent", runtime)
	issue := dbfx.Issue(t, "Delivery validation issue")
	taskID := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "work_dir": "/shared-code"})
	for _, tc := range []struct {
		name   string
		body   any
		status int
	}{
		{"invalid commit", map[string]string{"work_dir": "/shared-code", "branch_name": "agent/shared", "worktree_commit": "not-a-commit"}, http.StatusBadRequest},
		{"missing branch", map[string]string{"work_dir": "/shared-code", "worktree_commit": strings.Repeat("a", 40)}, http.StatusBadRequest},
		{"changed workdir", map[string]string{"work_dir": "/other-code", "branch_name": "agent/shared", "worktree_commit": strings.Repeat("a", 40)}, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, "/delivery", tc.body)
			req = withURLParam(req, "taskId", taskID)
			testutil.Call(t, testHandler.RecordWorktreeDelivery, req).Want(tc.status)
		})
	}
}

func TestSharedWorktreeDeliveryDeniesForeignWorkspace(t *testing.T) {
	workspace := dbfx.Workspace(t, "Foreign delivery workspace", "foreign-delivery")
	runtime := dbfx.Runtime(t, "Foreign delivery runtime", testutil.Cols{"workspace_id": workspace})
	agent := dbfx.Agent(t, "Foreign delivery agent", runtime, testutil.Cols{"workspace_id": workspace})
	issue := dbfx.Issue(t, "Foreign delivery issue", testutil.Cols{"workspace_id": workspace})
	taskID := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed"})
	req := newRequest(http.MethodPost, "/delivery", map[string]string{"work_dir": "/shared-code", "branch_name": "agent/shared", "worktree_commit": strings.Repeat("a", 40)})
	req = withURLParam(req, "taskId", taskID)
	// Workspace access deliberately masks foreign resources as missing.
	testutil.Call(t, testHandler.RecordWorktreeDelivery, req).Want(http.StatusNotFound)
	task, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(taskID))
	if err != nil || task.BranchName.Valid {
		t.Fatalf("foreign delivery mutated task: %+v %v", task, err)
	}
}

func TestSharedWorktreeNoWorkSettlesBeforeLateTerminalCallback(t *testing.T) {
	runtime := handlerTestRuntimeID(t)
	agent := dbfx.Agent(t, "Empty shared delivery agent", runtime)
	issue := dbfx.Issue(t, "Empty shared delivery issue")
	taskID := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "running", "work_dir": "/shared-code"})
	req := newRequest(http.MethodPost, "/delivery", map[string]any{"work_dir": "/shared-code", "no_work": true})
	req = withURLParam(req, "taskId", taskID)
	testutil.Call(t, testHandler.RecordWorktreeDelivery, req).Want(http.StatusOK)
	completed, changed, err := testHandler.TaskService.CompleteTaskWithTransition(t.Context(), parseUUID(taskID), []byte(`{"output":"done","worktree_delivery_pending":true}`), "", "/shared-code", "", false, "", "")
	if err != nil || !changed {
		t.Fatalf("late callback failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["worktree_delivery_pending"] != false || result["worktree_commit"] != "" || completed.BranchName.Valid {
		t.Fatalf("empty delivery remained pending or named a deleted branch: %+v %+v", result, completed)
	}
}
