package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectSupervisionAPIContract(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database unavailable")
	}
	if err := testHandler.Queries.SeedIssueStatusEntries(context.Background(), parseUUID(dbfx.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	lead := dbfx.Agent(t, "supervision lead", handlerTestRuntimeID(t))
	project := dbfx.Project(t, "supervision API", testutil.Cols{"lead_type": "agent", "lead_id": lead, "status": "in_progress"})
	dbfx.Cleanup(t, "DELETE FROM project_supervision WHERE project_id=$1", project)
	url := "/api/projects/" + project + "/supervision"
	params := map[string]string{"id": project}
	var initial service.ProjectSupervisionView
	testutil.Call(t, testHandler.GetProjectSupervision, squadScopeReq("", http.MethodGet, url, nil, params)).Want(http.StatusOK).JSON(&initial)
	if initial.Enabled || initial.Revision != 0 || initial.Config.MaxInFlight != 3 {
		t.Fatalf("default contract: %+v", initial)
	}
	config := service.DefaultProjectSupervisionConfig()
	input := map[string]any{"enabled": true, "revision": 0, "config": config}
	var saved service.ProjectSupervisionView
	testutil.Call(t, testHandler.SaveProjectSupervision, squadScopeReq("", http.MethodPut, url, input, params)).Want(http.StatusOK).JSON(&saved)
	if !saved.Enabled || saved.Revision != 1 {
		t.Fatalf("save: %+v", saved)
	}
	testutil.Call(t, testHandler.SaveProjectSupervision, squadScopeReq("", http.MethodPut, url, input, params)).Want(http.StatusConflict)
	config.MaxInFlight = 0
	input["config"] = config
	input["revision"] = saved.Revision
	testutil.Call(t, testHandler.SaveProjectSupervision, squadScopeReq("", http.MethodPut, url, input, params)).Want(http.StatusBadRequest)
	request := squadScopeReq("", http.MethodPut, url, input, params)
	request.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.SaveProjectSupervision, request).Want(http.StatusForbidden)
	testutil.Call(t, testHandler.ApplyProjectSupervision, squadScopeReq("", http.MethodPost, url+"/actions", map[string]any{}, params)).Want(http.StatusForbidden)
	otherWS := dbfx.Workspace(t, "other supervision workspace", "supervision-api-other")
	other := dbfx.Project(t, "foreign supervision", testutil.Cols{"workspace_id": otherWS})
	testutil.Call(t, testHandler.GetProjectSupervision, squadScopeReq("", http.MethodGet, url, nil, map[string]string{"id": other})).Want(http.StatusNotFound)
}

func TestProjectSupervisionDaemonClaimUsesDedicatedContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	if err := testHandler.Queries.SeedIssueStatusEntries(ctx, parseUUID(testWorkspaceID)); err != nil {
		t.Fatal(err)
	}
	runtimeID := dbfx.Runtime(t, "supervision daemon", testutil.Cols{"metadata": testutil.Raw(`'{"capabilities":["project-supervision-v1"]}'::jsonb`)})
	lead := dbfx.Agent(t, "supervision coordinator", runtimeID)
	projectID := dbfx.Project(t, "coordination context", testutil.Cols{"lead_type": "agent", "lead_id": lead, "status": "in_progress"})
	project, err := testHandler.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: parseUUID(projectID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM project_supervision WHERE project_id=$1", projectID)
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE context->>'project_id'=$1", projectID)
	dbfx.Issue(t, "missing assignment", testutil.Cols{"project_id": projectID, "status": "todo"})
	if err = testHandler.ProjectSupervisionService.Save(ctx, project, parseUUID(testUserID), true, service.DefaultProjectSupervisionConfig(), 0); err != nil {
		t.Fatal(err)
	}
	if err = testHandler.ProjectSupervisionService.CheckNow(ctx, project); err != nil {
		t.Fatal(err)
	}
	task, err := testHandler.TaskService.ClaimTask(ctx, parseUUID(lead))
	if err != nil || task == nil {
		t.Fatalf("claim: %+v %v", task, err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	request := newDaemonTokenRequest(http.MethodPost, "/claim", nil, testWorkspaceID, "supervision")
	request.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityProjectSupervisionV1)
	response, _, _, _, _, failure := testHandler.buildClaimedTaskResponse(request, task, runtime, runtimeID, testWorkspaceID)
	if failure != nil {
		t.Fatalf("dedicated claim: %+v", failure)
	}
	if response.ProjectID != projectID || response.WorkspaceID != testWorkspaceID || response.IssueID != "" || response.QuickCreatePrompt != "" || !strings.Contains(response.ProjectSupervisionPrompt, "checked_version") {
		t.Fatalf("wrong coordination response: %+v", response)
	}
	request.Header.Del("X-Client-Capabilities")
	_, _, _, _, _, failure = testHandler.buildClaimedTaskResponse(request, task, runtime, runtimeID, testWorkspaceID)
	if failure == nil || failure.status != http.StatusConflict {
		t.Fatalf("old daemon admitted: %+v", failure)
	}
}
