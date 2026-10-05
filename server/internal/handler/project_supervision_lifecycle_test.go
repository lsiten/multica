package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTaskContextDaemonLifecycle(t *testing.T) {
	for _, tc := range []struct{ contextType, outcome string }{
		{service.ProjectSupervisionContextType, "complete"},
		{service.ProjectSupervisionContextType, "fail"},
		{service.HumanFollowupContextType, "complete"},
		{service.HumanFollowupContextType, "fail"},
	} {
		t.Run(tc.contextType+"/"+tc.outcome, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, "coordination lifecycle")
			agentID := dbfx.Agent(t, "coordination lead", runtimeID)
			projectID := dbfx.Project(t, "coordination lifecycle", testutil.Cols{"lead_type": "agent", "lead_id": agentID})
			var taskContext any = service.ProjectCoordinationContext{
				Type: service.ProjectSupervisionContextType, WorkspaceID: testWorkspaceID, ProjectID: projectID,
			}
			if tc.contextType == service.HumanFollowupContextType {
				sourceTaskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "status": "completed"})
				taskContext = service.HumanFollowupContext{Type: service.HumanFollowupContextType, WorkspaceID: testWorkspaceID, ProjectID: projectID, SourceTaskID: sourceTaskID}
			}
			contextBytes, err := json.Marshal(taskContext)
			if err != nil {
				t.Fatal(err)
			}
			taskID := dbfx.Task(t, agentID, testutil.Cols{
				"runtime_id": runtimeID, "status": "dispatched", "context": contextBytes,
			})
			request := func(method, action, workspaceID string, body any) *http.Request {
				return withURLParam(newDaemonTokenRequest(method, "/api/daemon/tasks/"+taskID+"/"+action, body, workspaceID, "coordination"), "taskId", taskID)
			}

			foreignWorkspace := dbfx.Workspace(t, "foreign coordinator", "foreign-coordinator-"+tc.outcome)
			testutil.Call(t, testHandler.StartTask, request(http.MethodPost, "start", foreignWorkspace, nil)).Want(http.StatusNotFound)
			testutil.Call(t, testHandler.ExtendTaskPrepareLease, withURLParams(request(http.MethodPost, "prepare-lease", testWorkspaceID, nil), "taskId", taskID, "runtimeId", runtimeID)).Want(http.StatusOK)
			var started AgentTaskResponse
			testutil.Call(t, testHandler.StartTask, request(http.MethodPost, "start", testWorkspaceID, nil)).Want(http.StatusOK).JSON(&started)
			if started.Status != "running" || started.WorkspaceID != testWorkspaceID {
				t.Fatalf("started task = %+v, want running in %s", started, testWorkspaceID)
			}
			bus := events.New()
			progressService := &service.TaskService{Queries: testHandler.Queries, Bus: bus}
			progressHandler := *testHandler
			progressHandler.TaskService = progressService
			var progressWorkspace string
			bus.Subscribe(protocol.EventTaskProgress, func(event events.Event) { progressWorkspace = event.WorkspaceID })
			testutil.Call(t, progressHandler.ReportTaskProgress, request(http.MethodPost, "progress", testWorkspaceID, map[string]any{"summary": "checking project", "step": 1, "total": 2})).Want(http.StatusOK)
			if progressWorkspace != testWorkspaceID {
				t.Fatalf("progress event workspace = %q, want %s", progressWorkspace, testWorkspaceID)
			}
			testutil.Call(t, testHandler.ReportTaskMessages, request(http.MethodPost, "messages", testWorkspaceID, map[string]any{"messages": []map[string]any{{"seq": 1, "type": "text", "content": "project checked"}}})).Want(http.StatusOK)
			testutil.Call(t, testHandler.ListTaskMessages, request(http.MethodGet, "messages", foreignWorkspace, nil)).Want(http.StatusNotFound)
			testutil.Call(t, testHandler.ListTaskMessages, request(http.MethodGet, "messages", testWorkspaceID, nil)).Want(http.StatusOK)
			if tc.outcome == "complete" {
				testutil.Call(t, testHandler.CompleteTask, request(http.MethodPost, tc.outcome, testWorkspaceID, map[string]any{"output": "project checked"})).Want(http.StatusOK)
			} else {
				testutil.Call(t, testHandler.FailTask, request(http.MethodPost, tc.outcome, testWorkspaceID, map[string]any{"error": "review needs follow-up", "failure_reason": "agent_error.unknown"})).Want(http.StatusOK)
			}
			finished, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(taskID))
			if err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if tc.outcome == "fail" {
				want = "failed"
			}
			if finished.Status != want {
				t.Fatalf("terminal status = %s, want %s", finished.Status, want)
			}
		})
	}
}
