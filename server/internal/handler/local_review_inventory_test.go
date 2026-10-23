package handler

import (
	"github.com/multica-ai/multica/server/pkg/protocol"
	"net/http"
	"testing"
	"time"

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

func TestLocalReviewInventoryAttributesSystemRunsWithoutInventingChat(t *testing.T) {
	// Given an issue-less supervision run and an unrelated project reference.
	runtime := dbfx.Runtime(t, "system-inventory-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "system-inventory-agent", runtime)
	project := dbfx.Project(t, "Inventory project")
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "failed", "work_dir": "/retained/worktree", "context": `{"type":"project_supervision","project_id":"` + project + `"}`})
	handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))
	// When the real HTTP inventory route reads the stored association.
	var rows []localReviewWorktreeResponse
	testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees?agent_id="+agent, nil)).Want(http.StatusOK).JSON(&rows)
	// Then failure remains visible, its source is explicit, and the directory is not claimed live.
	if len(rows) != 1 || uuidToString(rows[0].TaskID) != task || rows[0].RunKind != "project_supervision" || uuidToString(rows[0].ProjectID) != project || rows[0].ChatSessionID.Valid || rows[0].DirectoryState != "unverified" {
		t.Fatalf("incorrect system-run attribution: %+v", rows)
	}
}

func TestLocalReviewInventoryDoesNotResolveMalformedProjectContext(t *testing.T) {
	runtime := dbfx.Runtime(t, "unknown-project-inventory", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "unknown-project-agent", runtime)
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "failed", "work_dir": "/retained", "context": `{"type":"project_supervision","project_id":"invalid"}`})
	handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))
	var rows []localReviewWorktreeResponse
	testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees?agent_id="+agent, nil)).Want(http.StatusOK).JSON(&rows)
	if len(rows) != 1 || rows[0].ProjectID.Valid {
		t.Fatalf("invented project association: %+v", rows)
	}
}

func TestAutomationInventoryAndGCPreserveAuthoritativeProjectWithoutContext(t *testing.T) {
	// Given an automation project binding absent from (or different from) run context.
	runtime := dbfx.Runtime(t, "automation-project-inventory", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "automation-project-agent", runtime)
	project := dbfx.Project(t, "Automation authoritative project")
	other := dbfx.Project(t, "Stale context project")
	automation := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": project, "title": "Project inventory automation", "assignee_id": agent, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	run := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": automation, "source": "manual", "status": "completed", "completed_at": time.Now()})
	for _, raw := range []string{`{}`, `{"project_id":"` + other + `"}`} {
		t.Run(raw, func(t *testing.T) {
			task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "failed", "work_dir": "/retained/automation", "completed_at": time.Now(), "autopilot_run_id": run, "context": raw})
			// When both authorized projections read the automation's source of truth.
			handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))
			var inventory []localReviewWorktreeResponse
			testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees?agent_id="+agent, nil)).Want(http.StatusOK).JSON(&inventory)
			found := false
			for _, row := range inventory {
				if uuidToString(row.TaskID) == task {
					found = true
					if uuidToString(row.ProjectID) != project || row.RunKind != "autopilot_run" {
						t.Fatalf("automation inventory lost project: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("automation absent from inventory")
			}
			request := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "automation-project-daemon"), "taskId", task)
			var status protocol.TaskGCStatus
			testutil.Call(t, testHandler.GetTaskGCCheck, request).Want(http.StatusOK).JSON(&status)
			// Then neither missing nor stale context can erase the actual project.
			if status.ProjectID != project || status.AutopilotID != automation || status.RunKind != "autopilot_run" {
				t.Fatalf("automation GC facts lost project: %+v", status)
			}
		})
	}
}

func TestAutomationProjectAttributionRejectsForeignWorkspaceBinding(t *testing.T) {
	// Given a malformed cross-workspace automation binding pointing at a local project.
	runtime := dbfx.Runtime(t, "foreign-automation-runtime", testutil.Cols{"runtime_mode": "local"})
	agent := dbfx.Agent(t, "foreign-automation-agent", runtime)
	project := dbfx.Project(t, "Do not infer this project")
	foreign := dbfx.Workspace(t, "Foreign automation", "foreign-automation-project")
	automation := dbfx.Insert(t, "autopilot", testutil.Cols{"workspace_id": foreign, "project_id": project, "title": "Foreign binding", "assignee_id": agent, "execution_mode": "run_only", "created_by_type": "member", "created_by_id": testUserID})
	run := dbfx.Insert(t, "autopilot_run", testutil.Cols{"autopilot_id": automation, "source": "manual", "status": "completed", "completed_at": time.Now()})
	task := dbfx.Task(t, agent, testutil.Cols{"runtime_id": runtime, "status": "failed", "work_dir": "/retained/foreign", "completed_at": time.Now(), "autopilot_run_id": run, "context": `{}`})
	// When authorized inventory and GC facts resolve their project association.
	handler := middleware.RequireWorkspaceMember(testHandler.Queries)(http.HandlerFunc(testHandler.ListLocalReviewWorktrees))
	var rows []localReviewWorktreeResponse
	testutil.Call(t, handler.ServeHTTP, newRequest(http.MethodGet, "/local-reviews/worktrees?agent_id="+agent, nil)).Want(http.StatusOK).JSON(&rows)
	var status protocol.TaskGCStatus
	request := withURLParam(newDaemonTokenRequest(http.MethodGet, "/gc-check", nil, testWorkspaceID, "foreign-auto-daemon"), "taskId", task)
	testutil.Call(t, testHandler.GetTaskGCCheck, request).Want(http.StatusOK).JSON(&status)
	// Then the foreign automation contributes no project, even though that project is local.
	if len(rows) != 1 || rows[0].ProjectID.Valid || status.ProjectID != "" {
		t.Fatalf("cross-workspace association escaped guard: %+v %+v", rows, status)
	}
}
