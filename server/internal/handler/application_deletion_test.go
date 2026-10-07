package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationResourceDeleteProtectsCurrentAndRunningRevisions(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application resources")
	resource := func(label string) string {
		ref, err := json.Marshal(map[string]string{"url": "https://github.com/example/" + label})
		if err != nil {
			t.Fatal(err)
		}
		return dbfx.Insert(t, "project_resource", testutil.Cols{"workspace_id": testWorkspaceID, "project_id": projectID, "resource_type": "github_repo", "resource_ref": json.RawMessage(ref), "label": label})
	}
	first, second := resource("original"), resource("new")
	config := protocol.DefaultApplicationConfig()
	config.ResourceID = first
	config.Command = []string{"test-created-application-command"}
	config.Port = 4100
	var app application.View
	testutil.Call(t, testHandler.CreateApplication, squadScopeReq("", http.MethodPost, "/applications", map[string]any{"project_id": projectID, "name": "resource consumer", "kind": "service", "config": config}, nil)).Want(http.StatusCreated).JSON(&app)
	dbfx.Cleanup(t, "DELETE FROM application WHERE id=$1", app.ID)
	dbfx.Cleanup(t, "DELETE FROM application_revision WHERE application_id=$1", app.ID)
	request := func() *http.Request {
		return squadScopeReq("", http.MethodDelete, "/resources/"+first, nil, map[string]string{"id": projectID, "resourceId": first})
	}
	testutil.Call(t, testHandler.DeleteProjectResource, request()).Want(http.StatusConflict)
	runtimeID := applicationTestRuntime(t)
	instanceID := dbfx.Insert(t, "application_instance", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": app.ID, "runtime_id": runtimeID, "daemon_id": testUserID, "revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running"})
	config.ResourceID = second
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+app.ID, application.UpdateInput{Revision: 1, Config: &config}, map[string]string{"id": app.ID})).Want(http.StatusOK)
	testutil.Call(t, testHandler.DeleteProjectResource, request()).Want(http.StatusConflict)
	if _, err := testPool.Exec(context.Background(), "UPDATE application_instance SET desired_state='stopped',process_state='stopped',generation=2 WHERE id=$1", instanceID); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteProjectResource, request()).Want(http.StatusConflict)
	if _, err := testHandler.Queries.ReportApplicationInstance(context.Background(), db.ReportApplicationInstanceParams{ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID), ObservedGeneration: 2, ObservedRevision: 1, ProcessState: "stopped", HealthState: "unknown", Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteProjectResource, request()).Want(http.StatusNoContent)
}

func TestApplicationRuntimeDeleteKeepsUnconfirmedServices(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "runtime application retention")
	app := applicationTestCreate(t, projectID, "running API", "service")
	runtimeID := applicationTestRuntime(t)
	instanceID := dbfx.Insert(t, "application_instance", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": app.ID, "runtime_id": runtimeID, "daemon_id": testUserID, "revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running"})
	request := func() *http.Request {
		return squadScopeReq("", http.MethodDelete, "/runtimes/"+runtimeID, nil, map[string]string{"runtimeId": runtimeID})
	}
	testutil.Call(t, testHandler.DeleteAgentRuntime, request()).Want(http.StatusConflict)
	if _, err := testPool.Exec(context.Background(), "UPDATE application_instance SET desired_state='stopped',generation=2 WHERE id=$1", instanceID); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteAgentRuntime, request()).Want(http.StatusConflict)
	if _, err := testHandler.Queries.ReportApplicationInstance(context.Background(), db.ReportApplicationInstanceParams{ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID), ObservedGeneration: 2, ObservedRevision: 1, ProcessState: "stopped", HealthState: "unknown", Metrics: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteAgentRuntime, request()).Want(http.StatusOK)
	var instances int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM application_instance WHERE id=$1", instanceID).Scan(&instances); err != nil || instances != 0 {
		t.Fatalf("deleted runtime retained its instance: %d %v", instances, err)
	}
	if _, err := testHandler.applicationService().Get(context.Background(), parseUUID(testWorkspaceID), parseUUID(app.ID)); err != nil {
		t.Fatal("runtime deletion removed reusable application definition")
	}
}

func TestApplicationProjectDeleteCleansStoppedCatalogAndRetainsOtherProjects(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	endpoint, runtimeID := applicationAccessFixture(t)
	app, err := testHandler.applicationService().Get(context.Background(), parseUUID(testWorkspaceID), endpoint.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	otherProject := dbfx.Project(t, "unrelated application project")
	other := applicationTestCreate(t, otherProject, "keep definition", "service")
	ticket := strings.Repeat("b", 64)
	if err := testHandler.Queries.CreateApplicationAccessTicket(context.Background(), db.CreateApplicationAccessTicketParams{TokenHash: auth.HashToken(ticket), EndpointID: endpoint.ID, WorkspaceID: endpoint.WorkspaceID, UserID: parseUUID(testUserID), MemberID: parseUUID(applicationTestMemberID(t, testUserID)), EndpointRevision: endpoint.Revision}); err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		return squadScopeReq("", http.MethodDelete, "/projects/"+app.ProjectID, nil, map[string]string{"id": app.ProjectID})
	}
	testutil.Call(t, testHandler.DeleteProject, request()).Want(http.StatusConflict)
	applicationTestEnqueue(t, app, runtimeID, "stop", "project-delete-stop")
	claim := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, claim, "completed")
	testutil.Call(t, testHandler.DeleteProject, request()).Want(http.StatusNoContent)
	for _, table := range []string{"application", "application_revision", "application_relation", "application_instance", "application_endpoint", "application_operation", "application_operation_step"} {
		column := "application_id"
		if table == "application" {
			column = "id"
		}
		if table == "application_relation" {
			column = "source_id"
		}
		var count int
		if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE "+column+"=$1", app.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained deleted application: count=%d err=%v", table, count, err)
		}
	}
	var tickets int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM application_access_ticket WHERE endpoint_id=$1", uuidToString(endpoint.ID)).Scan(&tickets); err != nil || tickets != 0 {
		t.Fatalf("launch ticket survived deletion: %d %v", tickets, err)
	}
	if _, err := testHandler.applicationService().Get(context.Background(), parseUUID(testWorkspaceID), parseUUID(other.ID)); err != nil {
		t.Fatal("project deletion removed another project's application")
	}
}

func TestApplicationWorkspaceDeleteFencesRunningServicesAndCleansOnlyItsCatalog(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	workspaceID := dbfx.Workspace(t, "application workspace deletion", "application-delete-test")
	dbfx.Member(t, workspaceID, testUserID, "owner")
	projectID := dbfx.Project(t, "workspace application", testutil.Cols{"workspace_id": workspaceID})
	config := protocol.DefaultApplicationConfig()
	config.Mode = "external"
	config.Port = 4100
	actor := application.Actor{Type: "member", ID: parseUUID(testUserID), UserID: parseUUID(testUserID)}
	app, err := testHandler.applicationService().Create(context.Background(), parseUUID(workspaceID), application.CreateInput{ProjectID: parseUUID(projectID), Name: "workspace API", Kind: "service", Config: config}, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"application", "application_revision", "application_relation", "application_instance", "application_endpoint", "application_operation", "application_operation_step", "application_instance_consumer", "application_access_ticket"} {
		dbfx.Cleanup(t, "DELETE FROM "+table+" WHERE workspace_id=$1", workspaceID)
	}
	runtimeID := dbfx.Insert(t, "agent_runtime", testutil.Cols{"workspace_id": workspaceID, "daemon_id": testUserID, "name": "application runtime", "runtime_mode": "local", "provider": "codex", "owner_id": testUserID, "status": "online", "last_seen_at": time.Now()})
	instanceID := dbfx.Insert(t, "application_instance", testutil.Cols{"workspace_id": workspaceID, "application_id": app.ID, "runtime_id": runtimeID, "daemon_id": testUserID, "revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running"})
	otherProject := dbfx.Project(t, "application outside deleted workspace")
	other := applicationTestCreate(t, otherProject, "surviving application", "service")
	request := func() *http.Request {
		return squadScopeReq("", http.MethodDelete, "/workspaces/"+workspaceID, nil, map[string]string{"id": workspaceID})
	}
	testutil.Call(t, testHandler.DeleteWorkspace, request()).Want(http.StatusConflict)
	if _, err := testPool.Exec(context.Background(), "UPDATE application_instance SET desired_state='stopped',process_state='stopped' WHERE id=$1", instanceID); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.DeleteWorkspace, request()).Want(http.StatusNoContent)
	for _, table := range []string{"application", "application_revision", "application_relation", "application_instance", "application_endpoint", "application_operation", "application_operation_step", "application_instance_consumer", "application_access_ticket"} {
		var count int
		if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE workspace_id=$1", workspaceID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s survived workspace deletion: %d %v", table, count, err)
		}
	}
	if _, err := testHandler.applicationService().Get(context.Background(), parseUUID(testWorkspaceID), parseUUID(other.ID)); err != nil {
		t.Fatal("workspace deletion removed another workspace's application")
	}
}

func TestApplicationRuntimeDeleteProtectsCompositionAnchorOnAnotherMachine(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "composition runtime anchor")
	root := applicationTestCreate(t, projectID, "composition", "composition")
	service := applicationTestCreate(t, projectID, "remote member", "service")
	anchor := applicationTestRuntime(t)
	host := dbfx.Insert(t, "agent_runtime", testutil.Cols{"workspace_id": testWorkspaceID, "daemon_id": testUserID, "name": "remote application runtime", "runtime_mode": "local", "provider": "codex", "owner_id": testUserID, "status": "online", "last_seen_at": time.Now()})
	instance := dbfx.Insert(t, "application_instance", testutil.Cols{"workspace_id": testWorkspaceID, "application_id": service.ID, "runtime_id": host, "daemon_id": testUserID, "revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running"})
	dbfx.Insert(t, "application_instance_consumer", testutil.Cols{"workspace_id": testWorkspaceID, "instance_id": instance, "root_application_id": root.ID, "root_runtime_id": anchor, "actor_id": testUserID})
	testutil.Call(t, testHandler.DeleteAgentRuntime, squadScopeReq("", http.MethodDelete, "/runtimes/"+anchor, nil, map[string]string{"runtimeId": anchor})).Want(http.StatusConflict)
	if _, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(host)); err != nil {
		t.Fatal("anchor refusal removed the remote hosting runtime")
	}
}
