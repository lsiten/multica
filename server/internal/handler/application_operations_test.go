package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const applicationTestDaemonID = "66666666-6666-4666-8666-666666666666"

func applicationTestRuntime(t *testing.T) string {
	t.Helper()
	return dbfx.Runtime(t, "application runtime", testutil.Cols{"daemon_id": applicationTestDaemonID, "metadata": testutil.Raw(`'{"capabilities":["applications-v1"]}'::jsonb`), "status": "online", "last_seen_at": testutil.Raw("now()")})
}

func TestApplicationRecoveryRequiresCompletedGeneration(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application recovery eligibility")
	app := applicationTestCreate(t, projectID, "recoverable API", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "recovery-start")
	registry := func() []protocol.ApplicationRuntimeInstance {
		instances, err := testHandler.applicationService().RuntimeInstances(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
		if err != nil {
			t.Fatal(err)
		}
		return instances
	}
	if instances := registry(); len(instances) != 1 || instances[0].CanRestore {
		t.Fatalf("queued start became recoverable: %+v", instances)
	}
	claim := applicationTestClaim(t, runtimeID)[0]
	if registry()[0].CanRestore {
		t.Fatal("claimed start became recoverable before completion")
	}
	applicationTestFinish(t, runtimeID, claim, "completed")
	if !registry()[0].CanRestore {
		t.Fatal("completed service was not recoverable")
	}
	applicationTestEnqueue(t, app, runtimeID, "stop", "recovery-stop")
	if registry()[0].CanRestore {
		t.Fatal("stop generation reused an old recovery approval")
	}
}

func applicationTestOperationCleanup(t *testing.T, id string) {
	t.Helper()
	dbfx.Cleanup(t, "DELETE FROM application_operation WHERE application_id=$1", id)
	dbfx.Cleanup(t, "DELETE FROM application_operation_step WHERE operation_id IN (SELECT id FROM application_operation WHERE application_id=$1)", id)
	dbfx.Cleanup(t, "DELETE FROM application_instance_consumer WHERE root_application_id=$1", id)
	dbfx.Cleanup(t, "DELETE FROM application_endpoint WHERE application_id=$1", id)
}

func applicationTestEnqueue(t *testing.T, app application.View, runtimeID, action, key string) application.OperationView {
	t.Helper()
	var view application.OperationView
	input := application.OperationInput{Action: action, Revision: app.Revision, RuntimeID: runtimeID, IdempotencyKey: key}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq("", http.MethodPost, "/applications/"+app.ID+"/operations", input, map[string]string{"id": app.ID})).Want(http.StatusAccepted).JSON(&view)
	return view
}

func applicationTestClaim(t *testing.T, runtimeID string) []protocol.ApplicationClaim {
	t.Helper()
	request := squadScopeReq("", http.MethodPost, "/claim", map[string]string{"daemon_id": applicationTestDaemonID}, map[string]string{"runtimeId": runtimeID})
	request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, applicationTestDaemonID))
	var claims []protocol.ApplicationClaim
	testutil.Call(t, testHandler.ClaimRuntimeApplications, request).Want(http.StatusOK).JSON(&claims)
	return claims
}

func applicationTestFinish(t *testing.T, runtimeID string, claim protocol.ApplicationClaim, state string) {
	t.Helper()
	observation := protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}
	if claim.Command.Action == "stop" {
		observation.ProcessState = "stopped"
		observation.HealthState = "unknown"
	}
	if state != "completed" {
		observation.ProcessState = "failed"
		observation.HealthState = "unhealthy"
	}
	request := squadScopeReq("", http.MethodPost, "/result", map[string]any{"daemon_id": applicationTestDaemonID, "result": protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: state, Error: map[string]string{"failed": "fixture startup failed"}[state], Observation: observation}}, map[string]string{"runtimeId": runtimeID, "stepId": claim.StepID})
	request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, applicationTestDaemonID))
	testutil.Call(t, testHandler.CompleteRuntimeApplication, request).Want(http.StatusOK)
}

func applicationTestGetOperation(t *testing.T, appID, operationID string) application.OperationView {
	t.Helper()
	var operation application.OperationView
	testutil.Call(t, testHandler.GetApplicationOperation, squadScopeReq("", http.MethodGet, "/operation", nil, map[string]string{"id": appID, "operationId": operationID})).Want(http.StatusOK).JSON(&operation)
	return operation
}

func TestApplicationOperationDurabilityIdempotencyAndFencing(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application operations")
	app := applicationTestCreate(t, projectID, "external service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, app, runtimeID, "start", "first-start")
	if operation.State != "queued" || len(operation.Steps) != 1 {
		t.Fatalf("acceptance is not completion: %+v", operation)
	}
	retry := applicationTestEnqueue(t, app, runtimeID, "start", "first-start")
	if retry.ID != operation.ID {
		t.Fatal("repeated request created another operation")
	}
	conflicting := application.OperationInput{Action: "stop", Revision: app.Revision, RuntimeID: runtimeID, IdempotencyKey: "first-start"}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq("", http.MethodPost, "/operations", conflicting, map[string]string{"id": app.ID})).Want(http.StatusConflict)
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 1 || claims[0].OperationID != operation.ID {
		t.Fatalf("claim: %+v", claims)
	}
	if len(applicationTestClaim(t, runtimeID)) != 0 {
		t.Fatal("unexpired command was claimed twice")
	}
	applicationTestFinish(t, runtimeID, claims[0], "completed")
	applicationTestFinish(t, runtimeID, claims[0], "completed")
	completed := applicationTestGetOperation(t, app.ID, operation.ID)
	if completed.State != "completed" || completed.CompletedAt == nil {
		t.Fatalf("completion receipt: %+v", completed)
	}
	applicationTestEnqueue(t, app, runtimeID, "stop", "first-stop")
	stale := protocol.ApplicationObservation{InstanceID: claims[0].Command.InstanceID, Generation: claims[0].Command.Generation, Revision: 1, ProcessState: "running", HealthState: "healthy"}
	request := squadScopeReq("", http.MethodPost, "/observe", map[string]any{"daemon_id": applicationTestDaemonID, "observation": stale}, map[string]string{"runtimeId": runtimeID})
	request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, applicationTestDaemonID))
	testutil.Call(t, testHandler.ReportRuntimeApplication, request).Want(http.StatusConflict)
	stops := applicationTestClaim(t, runtimeID)
	if len(stops) != 1 || stops[0].Command.Generation <= claims[0].Command.Generation {
		t.Fatalf("stop generation: %+v", stops)
	}
	applicationTestFinish(t, runtimeID, stops[0], "completed")
}

func TestApplicationOperationFailureBlocksOnlyDependents(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application orchestration")
	api := applicationTestCreate(t, projectID, "API", "service")
	web := applicationTestCreate(t, projectID, "web", "service")
	worker := applicationTestCreate(t, projectID, "independent", "service")
	root := applicationTestCreate(t, projectID, "system", "composition")
	applicationTestOperationCleanup(t, root.ID)
	set := func(app *application.View, relations []application.Relation) {
		t.Helper()
		var updated application.View
		testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/application", application.UpdateInput{Revision: app.Revision, Relations: &relations}, map[string]string{"id": app.ID})).Want(http.StatusOK).JSON(&updated)
		*app = updated
	}
	set(&web, []application.Relation{{Type: "depends_on", TargetID: api.ID, Condition: "healthy"}})
	set(&root, []application.Relation{{Type: "contains", TargetID: api.ID, Required: true}, {Type: "contains", TargetID: web.ID, Required: true}, {Type: "contains", TargetID: worker.ID, Required: true}})
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, root, runtimeID, "start", "composition-start")
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 2 {
		t.Fatalf("independent first wave: %+v", claims)
	}
	for _, claim := range claims {
		if claim.Command.ApplicationID == api.ID {
			applicationTestFinish(t, runtimeID, claim, "failed")
		} else if claim.Command.ApplicationID == worker.ID {
			applicationTestFinish(t, runtimeID, claim, "completed")
		} else {
			t.Fatalf("dependent started early: %+v", claim)
		}
	}
	if len(applicationTestClaim(t, runtimeID)) != 0 {
		t.Fatal("failed prerequisite did not block dependent")
	}
	final := applicationTestGetOperation(t, root.ID, operation.ID)
	if final.State != "failed" {
		t.Fatalf("composition outcome: %+v", final)
	}
	for _, step := range final.Steps {
		if step.ApplicationID == web.ID && step.State != "blocked" {
			t.Fatalf("dependent state: %+v", step)
		}
		if step.ApplicationID == worker.ID && step.State != "completed" {
			t.Fatalf("independent service was cancelled: %+v", step)
		}
	}
}

func TestApplicationSharedDependencyStopPreservesOtherConsumer(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application shared dependencies")
	api := applicationTestCreate(t, projectID, "API", "service")
	web := applicationTestCreate(t, projectID, "web", "service")
	applicationTestOperationCleanup(t, api.ID)
	applicationTestOperationCleanup(t, web.ID)
	relations := []application.Relation{{Type: "depends_on", TargetID: api.ID, Condition: "healthy"}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/application", application.UpdateInput{Revision: 1, Relations: &relations}, map[string]string{"id": web.ID})).Want(http.StatusOK).JSON(&web)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, api, runtimeID, "start", "api-start")
	apiClaims := applicationTestClaim(t, runtimeID)
	applicationTestFinish(t, runtimeID, apiClaims[0], "completed")
	applicationTestEnqueue(t, web, runtimeID, "start", "web-start")
	webClaims := applicationTestClaim(t, runtimeID)
	if len(webClaims) != 1 || webClaims[0].Command.ApplicationID != web.ID {
		t.Fatalf("external dependency was controlled: %+v", webClaims)
	}
	applicationTestFinish(t, runtimeID, webClaims[0], "completed")
	stop := application.OperationInput{Action: "stop", Revision: api.Revision, RuntimeID: runtimeID, IdempotencyKey: "unsafe-stop"}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq("", http.MethodPost, "/operation", stop, map[string]string{"id": api.ID})).Want(http.StatusConflict)
	applicationTestEnqueue(t, web, runtimeID, "stop", "web-stop")
	stopClaims := applicationTestClaim(t, runtimeID)
	if len(stopClaims) != 1 || stopClaims[0].Command.ApplicationID != web.ID {
		t.Fatalf("stopping web touched API: %+v", stopClaims)
	}
	applicationTestFinish(t, runtimeID, stopClaims[0], "completed")
	apiInstance, err := testHandler.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: parseUUID(apiClaims[0].Command.InstanceID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil || apiInstance.DesiredState != "running" || apiInstance.ProcessState != "running" {
		t.Fatalf("shared API stopped: %+v %v", apiInstance, err)
	}
	applicationTestEnqueue(t, api, runtimeID, "stop", "api-stop-after-release")
}

func TestApplicationOperationRuntimeOwnership(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application runtime ownership")
	app := applicationTestCreate(t, projectID, "service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	memberID := createPlainMember(t, "application-runtime-member@multica.test")
	input := application.OperationInput{Action: "start", Revision: 1, RuntimeID: runtimeID, IdempotencyKey: "unauthorized-start"}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq(memberID, http.MethodPost, "/operation", input, map[string]string{"id": app.ID})).Want(http.StatusForbidden)
	applicationTestEnqueue(t, app, runtimeID, "start", "authorized-start")
	request := squadScopeReq("", http.MethodPost, "/claim", map[string]string{"daemon_id": applicationTestDaemonID}, map[string]string{"runtimeId": runtimeID})
	request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, "77777777-7777-4777-8777-777777777777"))
	testutil.Call(t, testHandler.ClaimRuntimeApplications, request).Want(http.StatusForbidden)
	if len(applicationTestClaim(t, runtimeID)) != 1 {
		t.Fatal("unauthorized daemon consumed the command")
	}
}

func TestApplicationNewStartAfterConfirmedExitGetsNewGeneration(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application confirmed exit")
	app := applicationTestCreate(t, projectID, "service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "initial-start")
	initial := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, initial, "completed")
	observation := protocol.ApplicationObservation{InstanceID: initial.Command.InstanceID, Generation: initial.Command.Generation, Revision: 1, ProcessState: "stopped", HealthState: "unknown"}
	request := squadScopeReq("", http.MethodPost, "/observe", map[string]any{"daemon_id": applicationTestDaemonID, "observation": observation}, map[string]string{"runtimeId": runtimeID})
	request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, applicationTestDaemonID))
	testutil.Call(t, testHandler.ReportRuntimeApplication, request).Want(http.StatusNoContent)
	applicationTestEnqueue(t, app, runtimeID, "start", "start-after-exit")
	next := applicationTestClaim(t, runtimeID)
	if len(next) != 1 || next[0].Command.Generation <= initial.Command.Generation {
		t.Fatalf("start reused a completed generation: %+v", next)
	}
}
