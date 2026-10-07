package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestApplicationOptionalMemberFailureReturnsPartialAndKeepsRequiredService(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "optional application outcome")
	required := applicationTestCreate(t, projectID, "required", "service")
	optional := applicationTestCreate(t, projectID, "optional", "service")
	root := applicationTestCreate(t, projectID, "composition", "composition")
	for _, app := range []application.View{required, optional, root} {
		applicationTestOperationCleanup(t, app.ID)
	}
	relations := []application.Relation{{Type: "contains", TargetID: required.ID, Required: true}, {Type: "contains", TargetID: optional.ID, Required: false}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+root.ID, application.UpdateInput{Revision: root.Revision, Relations: &relations}, map[string]string{"id": root.ID})).Want(http.StatusOK).JSON(&root)
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, root, runtimeID, "start", "optional-outcome")
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 2 {
		t.Fatalf("independent optional member did not start: count=%d", len(claims))
	}
	for _, claim := range claims {
		state := "completed"
		if claim.Command.ApplicationID == optional.ID {
			state = "failed"
		}
		applicationTestFinish(t, runtimeID, claim, state)
	}
	final := applicationTestGetOperation(t, root.ID, operation.ID)
	if final.State != "partial" {
		t.Fatalf("optional failure lost partial outcome: %s", final.State)
	}
	for _, step := range final.Steps {
		if step.ApplicationID == required.ID && step.State != "completed" {
			t.Fatal("optional failure disrupted required service")
		}
	}
}

func TestApplicationForcedStopReportsAndOverridesSharedUsage(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "forced application stop")
	service := applicationTestCreate(t, projectID, "shared service", "service")
	root := applicationTestCreate(t, projectID, "composition", "composition")
	for _, app := range []application.View{service, root} {
		applicationTestOperationCleanup(t, app.ID)
	}
	relations := []application.Relation{{Type: "contains", TargetID: service.ID, Required: true}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+root.ID, application.UpdateInput{Revision: root.Revision, Relations: &relations}, map[string]string{"id": root.ID})).Want(http.StatusOK).JSON(&root)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, service, runtimeID, "start", "force-independent")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	applicationTestEnqueue(t, root, runtimeID, "start", "force-composition")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	request := func(force bool, key string) *http.Request {
		return squadScopeReq("", http.MethodPost, "/operations", application.OperationInput{Action: "stop", Revision: service.Revision, RuntimeID: runtimeID, Force: force, IdempotencyKey: key}, map[string]string{"id": service.ID})
	}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, request(false, "protected-stop")).Want(http.StatusConflict)
	var operation application.OperationView
	testutil.Call(t, testHandler.EnqueueApplicationOperation, request(true, "forced-stop")).Want(http.StatusAccepted).JSON(&operation)
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 1 || claims[0].Command.Action != "stop" {
		t.Fatal("explicit forced stop did not dispatch its shared target")
	}
	applicationTestFinish(t, runtimeID, claims[0], "completed")
	if final := applicationTestGetOperation(t, service.ID, operation.ID); final.State != "completed" {
		t.Fatalf("forced stop did not confirm exit: %s", final.State)
	}
}
