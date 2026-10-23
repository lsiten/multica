package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTaskGCProtectsSharedCodeUntilTheIssueTreeIsSettled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rootStatus string
		pending    bool
		active     bool
		wantBlock  bool
	}{
		{name: "open requirement", rootStatus: "in_progress", wantBlock: true},
		{name: "settled tree", rootStatus: "done"},
		{name: "pending member input", rootStatus: "done", pending: true, wantBlock: true},
		{name: "active sibling run", rootStatus: "done", active: true, wantBlock: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given a completed child run whose code directory is shared with its requirement.
			runtime := dbfx.Runtime(t, "family-gc-runtime", testutil.Cols{"runtime_mode": "local"})
			agent := dbfx.Agent(t, "family-gc-agent", runtime)
			root := dbfx.Issue(t, "Requirement", testutil.Cols{"kind": "requirement", "status": tc.rootStatus})
			child := dbfx.Issue(t, "Finished child", testutil.Cols{"status": "done", "parent_issue_id": root})
			sibling := dbfx.Issue(t, "Finished sibling", testutil.Cols{"status": "done", "parent_issue_id": root})
			run := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": child, "status": "completed", "completed_at": time.Now(), "work_dir": "/family/worktree"})
			if tc.pending {
				dbfx.Insert(t, "human_request", testutil.Cols{"id": run, "workspace_id": testWorkspaceID, "source_task_id": run, "agent_id": agent, "recipient_id": testUserID, "issue_id": root, "request_key": "family-input", "payload": "{}", "expires_at": time.Now().Add(time.Hour)})
			}
			if tc.active {
				dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": sibling, "status": "running", "work_dir": "/family/worktree"})
			}

			// When the daemon requests lifecycle facts for the finished child.
			r := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "family-gc-daemon"), "taskId", run)
			var response protocol.TaskGCStatus
			testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)

			// Then retention follows the entire tree, independently of this child's done state.
			if response.CodeWorkspaceIssueID != root || response.CodeWorkspaceRetentionBlocked != tc.wantBlock || !response.RetentionSupported {
				t.Fatalf("family retention facts = %+v, want root=%s blocked=%v", response, root, tc.wantBlock)
			}
		})
	}
}

func TestTaskGCRetainsCodeWhenTheIssueTreeContainsACycle(t *testing.T) {
	// Given a completed run with a corrupt cyclic issue ancestry.
	runtime := dbfx.Runtime(t, "cyclic-family-gc-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "cyclic-family-gc-agent", runtime)
	root := dbfx.Issue(t, "Cycle root", testutil.Cols{"status": "done"})
	child := dbfx.Issue(t, "Cycle child", testutil.Cols{"status": "done", "parent_issue_id": root})
	dbfx.Exec(t, "UPDATE issue SET parent_issue_id = $1 WHERE id = $2", child, root)
	run := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": child, "status": "completed", "work_dir": "/family/worktree"})

	// When the daemon requests lifecycle facts.
	r := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "family-gc-daemon"), "taskId", run)
	var response protocol.TaskGCStatus
	testutil.Call(t, testHandler.GetTaskGCCheck, r).Want(http.StatusOK).JSON(&response)

	// Then unknown family ownership cannot authorize cleanup.
	if response.RetentionSupported || response.CodeWorkspaceIssueID != "" {
		t.Fatalf("cyclic ancestry authorized retention decisions: %+v", response)
	}
}
