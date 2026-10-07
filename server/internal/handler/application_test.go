package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func applicationTestCreate(t *testing.T, projectID, name, kind string) application.View {
	t.Helper()
	config := protocol.DefaultApplicationConfig()
	if kind == "service" {
		config.Mode = "external"
		config.Port = 4100
	}
	var view application.View
	testutil.Call(t, testHandler.CreateApplication, squadScopeReq("", http.MethodPost, "/api/applications", map[string]any{"project_id": projectID, "name": name, "kind": kind, "config": config}, nil)).Want(http.StatusCreated).JSON(&view)
	dbfx.Cleanup(t, "DELETE FROM application WHERE id=$1", view.ID)
	dbfx.Cleanup(t, "DELETE FROM application_revision WHERE application_id=$1", view.ID)
	dbfx.Cleanup(t, "DELETE FROM application_relation WHERE source_id=$1 OR target_id=$1", view.ID)
	dbfx.Cleanup(t, "DELETE FROM application_instance WHERE application_id=$1", view.ID)
	return view
}

func TestApplicationCatalogRevisionAndCompositionContract(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application catalog")
	web := applicationTestCreate(t, projectID, "前台", "service")
	api := applicationTestCreate(t, projectID, "API", "service")
	shop := applicationTestCreate(t, projectID, "商城", "composition")
	if web.Revision != 1 || web.Name != "前台" || web.Config.Mode != "external" {
		t.Fatalf("create contract: %+v", web)
	}
	relations := []application.Relation{{TargetID: web.ID, Type: "contains", Required: true}, {TargetID: api.ID, Type: "contains", Required: true}}
	params := map[string]string{"id": shop.ID}
	var saved application.View
	input := application.UpdateInput{Revision: shop.Revision, Relations: &relations}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+shop.ID, input, params)).Want(http.StatusOK).JSON(&saved)
	if saved.Revision != 2 || len(saved.Relations) != 2 || saved.Relations[0].SourceID != shop.ID {
		t.Fatalf("relation revision: %+v", saved)
	}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+shop.ID, input, params)).Want(http.StatusConflict)
	dependency := []application.Relation{{TargetID: api.ID, Type: "depends_on", Condition: "healthy"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+web.ID, application.UpdateInput{Revision: 1, Relations: &dependency}, map[string]string{"id": web.ID})).Want(http.StatusOK)
	var plan application.Plan
	testutil.Call(t, testHandler.PreviewApplicationPlan, squadScopeReq("", http.MethodGet, "/applications/"+shop.ID+"/plan", nil, params)).Want(http.StatusOK).JSON(&plan)
	if len(plan.Nodes) != 2 || len(plan.Waves) != 2 || plan.Waves[0][0] != api.ID || plan.Waves[1][0] != web.ID {
		t.Fatalf("persisted graph plan: %+v", plan)
	}
	var listed struct {
		Applications []application.View `json:"applications"`
		Total        int                `json:"total"`
	}
	testutil.Call(t, testHandler.ListApplications, squadScopeReq("", http.MethodGet, "/applications?project_id="+projectID, nil, nil)).Want(http.StatusOK).JSON(&listed)
	if listed.Total != 3 || len(listed.Applications) != 3 {
		t.Fatalf("project application list: %+v", listed)
	}
	initial, err := testHandler.Queries.GetApplicationRevision(context.Background(), db.GetApplicationRevisionParams{ApplicationID: parseUUID(shop.ID), WorkspaceID: parseUUID(testWorkspaceID), Revision: 1})
	if err != nil || string(initial.Relations) != "[]" {
		t.Fatalf("initial revision was overwritten: %s %v", initial.Relations, err)
	}
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+api.ID+"?revision=1", nil, map[string]string{"id": api.ID})).Want(http.StatusConflict)
}

func TestApplicationRelationshipsRejectCycleAndForeignProject(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application relationships")
	otherProject := dbfx.Project(t, "other application project")
	first := applicationTestCreate(t, projectID, "first", "service")
	second := applicationTestCreate(t, projectID, "second", "service")
	foreign := applicationTestCreate(t, otherProject, "foreign", "service")
	relations := []application.Relation{{TargetID: second.ID, Type: "depends_on", Condition: "healthy"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+first.ID, application.UpdateInput{Revision: 1, Relations: &relations}, map[string]string{"id": first.ID})).Want(http.StatusOK)
	relations = []application.Relation{{TargetID: first.ID, Type: "depends_on", Condition: "healthy"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+second.ID, application.UpdateInput{Revision: 1, Relations: &relations}, map[string]string{"id": second.ID})).Want(http.StatusBadRequest)
	relations = []application.Relation{{TargetID: foreign.ID, Type: "related"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+second.ID, application.UpdateInput{Revision: 1, Relations: &relations}, map[string]string{"id": second.ID})).Want(http.StatusBadRequest)
	var unchanged application.View
	testutil.Call(t, testHandler.GetApplication, squadScopeReq("", http.MethodGet, "/applications/"+second.ID, nil, map[string]string{"id": second.ID})).Want(http.StatusOK).JSON(&unchanged)
	if unchanged.Revision != 1 || len(unchanged.Relations) != 0 {
		t.Fatalf("invalid graph edit partially persisted: %+v", unchanged)
	}
}

func TestApplicationIsolationValidationAndDeletion(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application isolation")
	app := applicationTestCreate(t, projectID, "service", "service")
	otherWS := dbfx.Workspace(t, "foreign applications", "foreign-application-test")
	dbfx.Member(t, otherWS, testUserID, "member")
	otherProject := dbfx.Project(t, "foreign project", testutil.Cols{"workspace_id": otherWS})
	dbfx.Insert(t, "application", testutil.Cols{"workspace_id": otherWS, "project_id": otherProject, "name": "foreign", "kind": "service", "created_by": testUserID})
	request := squadScopeReq("", http.MethodGet, "/applications/"+app.ID, nil, map[string]string{"id": app.ID})
	request.Header.Set("X-Workspace-ID", otherWS)
	testutil.Call(t, testHandler.GetApplication, request).Want(http.StatusNotFound)
	testutil.Call(t, testHandler.CreateApplication, squadScopeReq("", http.MethodPost, "/applications", map[string]any{"project_id": "invalid", "name": "invalid", "kind": "composition"}, nil)).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.CreateApplication, squadScopeReq("", http.MethodPost, "/applications", map[string]any{"project_id": projectID, "name": "invalid", "actor_id": testUserID, "kind": "composition"}, nil)).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+app.ID, nil, map[string]string{"id": app.ID})).Want(http.StatusBadRequest)
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+app.ID+"?revision=2", nil, map[string]string{"id": app.ID})).Want(http.StatusConflict)
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+app.ID+"?revision=1", nil, map[string]string{"id": app.ID})).Want(http.StatusNoContent)
	testutil.Call(t, testHandler.GetApplication, squadScopeReq("", http.MethodGet, "/applications/"+app.ID, nil, map[string]string{"id": app.ID})).Want(http.StatusNotFound)
}

func TestApplicationAgentAttributionAndScope(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "agent application catalog")
	runtimeID := handlerTestRuntimeID(t)
	agentID := dbfx.Agent(t, "application manager", runtimeID)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
	request := squadScopeReq("", http.MethodPost, "/applications", map[string]any{"project_id": projectID, "name": "agent application", "kind": "composition"}, nil)
	request.Header.Set("X-Actor-Source", "task_token")
	request.Header.Set("X-Agent-ID", agentID)
	request.Header.Set("X-Task-ID", taskID)
	var view application.View
	testutil.Call(t, testHandler.CreateApplication, request).Want(http.StatusCreated).JSON(&view)
	dbfx.Cleanup(t, "DELETE FROM application WHERE id=$1", view.ID)
	dbfx.Cleanup(t, "DELETE FROM application_revision WHERE application_id=$1", view.ID)
	revision, err := testHandler.Queries.GetApplicationRevision(context.Background(), db.GetApplicationRevisionParams{ApplicationID: parseUUID(view.ID), WorkspaceID: parseUUID(testWorkspaceID), Revision: 1})
	if err != nil || revision.ActorType != "agent" || uuidToString(revision.ActorID) != agentID {
		t.Fatalf("agent attribution lost: %+v %v", revision, err)
	}
	request = squadScopeReq("", http.MethodPatch, "/applications/"+view.ID, application.UpdateInput{Revision: 1}, map[string]string{"id": view.ID})
	request.Header.Set("X-Actor-Source", "task_token")
	request.Header.Set("X-Agent-ID", agentID)
	testutil.Call(t, testHandler.UpdateApplication, request).Want(http.StatusBadRequest)
}

func TestApplicationDeleteProtectsCompositionConsumers(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application consumer retention")
	service := applicationTestCreate(t, projectID, "shared API", "service")
	composition := applicationTestCreate(t, projectID, "root", "composition")
	runtimeID := handlerTestRuntimeID(t)
	instanceID := dbfx.Insert(t, "application_instance", testutil.Cols{
		"workspace_id": testWorkspaceID, "application_id": service.ID,
		"runtime_id": runtimeID, "daemon_id": testUserID, "revision": 1,
		"desired_state": "running", "process_state": "running",
	})
	dbfx.Insert(t, "application_instance_consumer", testutil.Cols{
		"workspace_id": testWorkspaceID, "instance_id": instanceID,
		"root_application_id": composition.ID, "root_runtime_id": runtimeID,
		"actor_id": testUserID,
	})
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+composition.ID+"?revision=1", nil, map[string]string{"id": composition.ID})).Want(http.StatusConflict)
	testutil.Call(t, testHandler.DeleteApplication, squadScopeReq("", http.MethodDelete, "/applications/"+service.ID+"?revision=1", nil, map[string]string{"id": service.ID})).Want(http.StatusConflict)
}

func TestApplicationConcurrentEditsCommitOneRevision(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application concurrent edits")
	app := applicationTestCreate(t, projectID, "initial", "service")
	results := make(chan int, 2)
	for _, name := range []string{"human edit", "agent edit"} {
		request := squadScopeReq("", http.MethodPatch, "/applications/"+app.ID, application.UpdateInput{Revision: 1, Name: &name}, map[string]string{"id": app.ID})
		go func() {
			response := httptest.NewRecorder()
			testHandler.UpdateApplication(response, request)
			results <- response.Code
		}()
	}
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusConflict) || (second == http.StatusOK && first == http.StatusConflict)) {
		t.Fatalf("concurrent revision results: %d, %d", first, second)
	}
	count := dbfx.Count(t, "SELECT count(*) FROM application_revision WHERE application_id=$1", app.ID)
	if count != 2 {
		t.Fatalf("expected initial and one edit revision, got %d", count)
	}
}
