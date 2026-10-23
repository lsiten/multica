package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChatFilesRetainStartedCanonicalChildWorkspace(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Canonical child", []byte("[]"))
	session := createHandlerTestChatSession(t, agentID)
	project := dbfx.Project(t, "Canonical family")
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, project, session)
	root := dbfx.Issue(t, "Open root", testutil.Cols{"project_id": project, "status": "todo"})
	child := dbfx.Issue(t, "First child", testutil.Cols{"project_id": project, "parent_issue_id": root, "status": "in_progress"})
	generation := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	task := dbfx.Task(t, agentID, testutil.Cols{"issue_id": child, "runtime_id": testRuntimeID, "status": "dispatched", "dispatched_at": generation})
	selection := protocol.CodeWorkspaceSelection{IssueID: root, RuntimeID: testRuntimeID, WorkDir: "/managed/family"}
	request := withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+task+"/start", map[string]any{"runtime_id": testRuntimeID, "dispatched_at": generation.Format(time.RFC3339Nano), "code_workspace": selection}, testWorkspaceID, "start-claim-test"), "taskId", task)
	testutil.Call(t, testHandler.StartTask, request).Want(http.StatusOK)
	var startMarker []byte
	var startPath string
	dbfx.QueryRow(t, `SELECT result->'code_workspace',work_dir FROM agent_task_queue WHERE id=$1`, task).Scan(&startMarker, &startPath)
	var recorded protocol.CodeWorkspaceSelection
	if err := json.Unmarshal(startMarker, &recorded); err != nil || recorded != selection || startPath != selection.WorkDir {
		t.Fatalf("selection not recorded: %s %s", startMarker, startPath)
	}
	_, err := testHandler.Queries.CompleteAgentTask(context.Background(), db.CompleteAgentTaskParams{ID: parseUUID(task), Result: []byte(`{"code_workspace":{"issue_id":"forged"},"output":"done"}`), WorkDir: pgtype.Text{String: selection.WorkDir, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE issue SET status='done' WHERE id=$1`, child)
	read := func() ChatSessionFilesResponse {
		t.Helper()
		req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files?include_attachments=false", nil), "sessionId", session))
		var response ChatSessionFilesResponse
		testutil.Call(t, testHandler.ListChatSessionFiles, req).Want(http.StatusOK).JSON(&response)
		return response
	}
	if got := read(); len(got.ProjectTasks) != 1 || got.ProjectTasks[0].ID != task {
		t.Fatalf("open family lost canonical child: %+v", got.ProjectTasks)
	}
	dbfx.Exec(t, `UPDATE issue SET status='done' WHERE id=$1`, root)
	if got := read(); len(got.ProjectTasks) != 0 {
		t.Fatalf("settled family stayed visible: %+v", got.ProjectTasks)
	}
}

func TestStartTaskRejectsInvalidCodeWorkspaceSelection(t *testing.T) {
	for _, name := range []string{"wrong-root", "wrong-runtime", "empty-path", "long-path", "chat"} {
		t.Run(name, func(t *testing.T) {
			task, runtime, generation := startClaimFixture(t, "dispatched")
			var issue string
			dbfx.QueryRow(t, `SELECT issue_id FROM agent_task_queue WHERE id=$1`, task).Scan(&issue)
			selection := protocol.CodeWorkspaceSelection{IssueID: issue, RuntimeID: runtime, WorkDir: "/managed/family"}
			switch name {
			case "wrong-root":
				selection.IssueID = dbfx.Issue(t, "Other root")
			case "wrong-runtime":
				selection.RuntimeID = "00000000-0000-0000-0000-000000000001"
			case "empty-path":
				selection.WorkDir = " "
			case "long-path":
				selection.WorkDir = string(make([]byte, 4097))
			case "chat":
				var agent string
				dbfx.QueryRow(t, `SELECT agent_id FROM agent_task_queue WHERE id=$1`, task).Scan(&agent)
				session := createHandlerTestChatSession(t, agent)
				dbfx.Exec(t, `UPDATE agent_task_queue SET chat_session_id=$1 WHERE id=$2`, session, task)
			}
			req := withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+task+"/start", map[string]any{"runtime_id": runtime, "dispatched_at": generation.Format(time.RFC3339Nano), "code_workspace": selection}, testWorkspaceID, "start-claim-test"), "taskId", task)
			testutil.Call(t, testHandler.StartTask, req).Want(http.StatusBadRequest)
			var status string
			dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, task).Scan(&status)
			if status != "dispatched" {
				t.Fatalf("invalid selection changed status to %s", status)
			}
		})
	}
}

func TestChatFilesExcludeUnverifiedCanonicalCandidates(t *testing.T) {
	agent := createHandlerTestAgent(t, "Canonical candidates", []byte("[]"))
	session := createHandlerTestChatSession(t, agent)
	project := dbfx.Project(t, "Canonical candidates")
	dbfx.Exec(t, `UPDATE chat_session SET project_id=$1 WHERE id=$2`, project, session)
	root := dbfx.Issue(t, "Root pending", testutil.Cols{"project_id": project, "status": "todo"})
	child := dbfx.Issue(t, "Closed child", testutil.Cols{"project_id": project, "parent_issue_id": root, "status": "done"})
	valid := protocol.CodeWorkspaceSelection{IssueID: root, RuntimeID: testRuntimeID, WorkDir: "/managed/family"}
	foreign := valid
	foreign.IssueID = dbfx.Issue(t, "Other family")
	wrongRuntime := valid
	wrongRuntime.RuntimeID = "00000000-0000-0000-0000-000000000001"
	wrongPath := valid
	wrongPath.WorkDir = "/some-other-checkout"
	for _, marker := range []any{foreign, wrongRuntime, wrongPath, "malformed", nil} {
		result := map[string]any{}
		if marker != nil {
			result["code_workspace"] = marker
		}
		dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "issue_id": child, "status": "completed", "work_dir": valid.WorkDir, "result": result})
	}
	req := withChatTestWorkspaceCtx(t, withURLParam(newRequest(http.MethodGet, "/files?include_attachments=false", nil), "sessionId", session))
	var response ChatSessionFilesResponse
	testutil.Call(t, testHandler.ListChatSessionFiles, req).Want(http.StatusOK).JSON(&response)
	if len(response.ProjectTasks) != 0 {
		t.Fatalf("unverified history became canonical: %+v", response.ProjectTasks)
	}
}

func TestCodeWorkspaceMarkerSurvivesFailureAndCancellation(t *testing.T) {
	for _, terminal := range []string{"failed", "cancelled", "completed"} {
		t.Run(terminal, func(t *testing.T) {
			task, runtime, generation := startClaimFixture(t, "dispatched")
			var issue string
			dbfx.QueryRow(t, `SELECT issue_id FROM agent_task_queue WHERE id=$1`, task).Scan(&issue)
			selection := protocol.CodeWorkspaceSelection{IssueID: issue, RuntimeID: runtime, WorkDir: "/managed/retained"}
			start := func(value protocol.CodeWorkspaceSelection) *http.Request {
				return withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+task+"/start", map[string]any{"runtime_id": runtime, "dispatched_at": generation.Format(time.RFC3339Nano), "code_workspace": value}, testWorkspaceID, "start-claim-test"), "taskId", task)
			}
			testutil.Call(t, testHandler.StartTask, start(selection)).Want(http.StatusOK)
			testutil.Call(t, testHandler.StartTask, start(selection)).Want(http.StatusOK)
			changed := selection
			changed.WorkDir = "/different"
			testutil.Call(t, testHandler.StartTask, start(changed)).Want(http.StatusBadRequest)
			var err error
			if terminal == "failed" {
				_, err = testHandler.Queries.FailAgentTask(t.Context(), db.FailAgentTaskParams{ID: parseUUID(task), Error: pgtype.Text{String: "failure", Valid: true}})
			} else if terminal == "completed" {
				_, err = testHandler.Queries.CompleteAgentTask(t.Context(), db.CompleteAgentTaskParams{ID: parseUUID(task), Result: []byte("null")})
			} else {
				_, err = testHandler.Queries.CancelAgentTask(t.Context(), parseUUID(task))
			}
			if err != nil {
				t.Fatal(err)
			}
			var marker []byte
			var workDir string
			dbfx.QueryRow(t, `SELECT result->'code_workspace',work_dir FROM agent_task_queue WHERE id=$1`, task).Scan(&marker, &workDir)
			var actual protocol.CodeWorkspaceSelection
			if err := json.Unmarshal(marker, &actual); err != nil || actual != selection || workDir != selection.WorkDir {
				t.Fatalf("terminal callback lost selected workspace: %s %s", marker, workDir)
			}
		})
	}
}

func TestStartCodeWorkspaceRejectsForeignIssueWorkspace(t *testing.T) {
	task, runtime, _ := startClaimFixture(t, "dispatched")
	foreignWorkspace := dbfx.Workspace(t, "Foreign selected workspace", "foreign-selected-workspace")
	foreignIssue := dbfx.Issue(t, "Foreign issue", testutil.Cols{"workspace_id": foreignWorkspace})
	dbfx.Exec(t, `UPDATE agent_task_queue SET issue_id=$1 WHERE id=$2`, foreignIssue, task)
	_, err := testHandler.TaskService.StartTask(t.Context(), parseUUID(task), service.TaskStartOptions{CodeWorkspace: &protocol.CodeWorkspaceSelection{IssueID: foreignIssue, RuntimeID: runtime, WorkDir: "/managed/foreign"}})
	if !errors.Is(err, service.ErrInvalidCodeWorkspaceSelection) {
		t.Fatalf("foreign workspace accepted: %v", err)
	}
	var status string
	var markerPresent bool
	dbfx.QueryRow(t, `SELECT status,COALESCE(result ? 'code_workspace',false) FROM agent_task_queue WHERE id=$1`, task).Scan(&status, &markerPresent)
	if status != "dispatched" || markerPresent {
		t.Fatalf("foreign workspace mutated task: %s %v", status, markerPresent)
	}
}

func TestStartCodeWorkspaceRejectsForeignRuntimeWorkspace(t *testing.T) {
	task, _, _ := startClaimFixture(t, "dispatched")
	foreignWorkspace := dbfx.Workspace(t, "Foreign selected runtime", "foreign-selected-runtime")
	runtime := dbfx.Runtime(t, "Foreign runtime", testutil.Cols{"workspace_id": foreignWorkspace})
	dbfx.Exec(t, `UPDATE agent_task_queue SET runtime_id=$1 WHERE id=$2`, runtime, task)
	var issue string
	dbfx.QueryRow(t, `SELECT issue_id FROM agent_task_queue WHERE id=$1`, task).Scan(&issue)
	_, err := testHandler.TaskService.StartTask(t.Context(), parseUUID(task), service.TaskStartOptions{CodeWorkspace: &protocol.CodeWorkspaceSelection{IssueID: issue, RuntimeID: runtime, WorkDir: "/managed/foreign-runtime"}})
	if !errors.Is(err, service.ErrInvalidCodeWorkspaceSelection) {
		t.Fatalf("foreign runtime accepted: %v", err)
	}
	var status string
	var markerPresent bool
	dbfx.QueryRow(t, `SELECT status,COALESCE(result ? 'code_workspace',false) FROM agent_task_queue WHERE id=$1`, task).Scan(&status, &markerPresent)
	if status != "dispatched" || markerPresent {
		t.Fatalf("foreign runtime mutated task: %s %v", status, markerPresent)
	}
}
