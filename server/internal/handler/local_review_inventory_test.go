package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLocalReviewInventoryFiltersIssueBeforePagination(t *testing.T) {
	// Given two issues with separate retained worktrees in the same runtime.
	runtime := dbfx.Runtime(t, "issue-inventory-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "issue-inventory-agent", runtime)
	issue := dbfx.Issue(t, "Selected issue")
	otherIssue := dbfx.Issue(t, "Other issue")
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "work_dir": "/selected/worktree", "status": "completed"})
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "issue_id": otherIssue, "work_dir": "/other/worktree", "status": "completed"})
	handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))
	for _, tc := range []struct {
		query  string
		status int
		count  int
	}{
		{"?issue_id=" + issue, http.StatusOK, 1},
		{"?issue_id=" + issue + "&offset=1", http.StatusOK, 0},
		{"?issue_id=invalid", http.StatusBadRequest, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			// When an issue-scoped inventory request reaches the handler.
			response := testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees"+tc.query, nil)).Want(tc.status)
			if tc.status != http.StatusOK {
				return
			}
			var rows []db.ListLocalReviewWorktreesRow
			response.JSON(&rows)
			// Then filtering precedes offset and only the selected issue is returned.
			if len(rows) != tc.count || (len(rows) == 1 && uuidToString(rows[0].TaskID) != task) {
				t.Fatalf("unexpected issue-scoped inventory: %+v", rows)
			}
		})
	}
}

func TestLocalReviewInventoryExcludesConfirmedRemovedWorktrees(t *testing.T) {
	runtime := dbfx.Runtime(t, "inventory-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "inventory-agent", runtime)
	for _, tc := range []struct {
		name, status, durable string
		visible               bool
		nullDurable           bool
	}{
		{"completed retained", "completed", "", true, false},
		{"failed retained", "failed", "", true, false},
		{"cancelled retained", "cancelled", "", true, false},
		{"legacy null retained", "completed", "", true, true},
		{"completed removed", "completed", "/repo", false, false},
		{"failed removed", "failed", "/repo", false, false},
		{"cancelled removed", "cancelled", "/repo", false, false},
		{"running not finalized", "running", "/repo", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given task metadata that distinguishes retention from finalization.
			columns := testutil.Cols{
				"runtime_id": runtime, "status": tc.status,
				"work_dir": "/disposable/worktree", "durable_work_dir": tc.durable,
			}
			if tc.nullDurable {
				columns["durable_work_dir"] = nil
			}
			task := dbfx.Task(t, agent, columns)
			// When the MR entry inventory reads existing metadata.
			rows, err := testHandler.Queries.ListLocalReviewWorktrees(t.Context(), db.ListLocalReviewWorktreesParams{
				WorkspaceID: parseUUID(testWorkspaceID), ReaderID: parseUUID(testUserID), AgentID: parseUUID(agent),
			})
			if err != nil {
				t.Fatal(err)
			}
			visible := false
			for _, row := range rows {
				visible = visible || uuidToString(row.TaskID) == task
			}
			// Then terminal status alone does not hide a retained worktree.
			if visible != tc.visible {
				t.Fatalf("task visible = %v, want %v", visible, tc.visible)
			}
		})
	}
}
