package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func applicationTestCancel(t *testing.T, appID, operationID string) application.OperationView {
	t.Helper()
	var operation application.OperationView
	testutil.Call(t, testHandler.CancelApplicationOperation, squadScopeReq("", http.MethodPost, "/applications/"+appID+"/operations/"+operationID+"/cancel", nil, map[string]string{"id": appID, "operationId": operationID})).Want(http.StatusAccepted).JSON(&operation)
	return operation
}

func TestApplicationCancelBeforeClaimConfirmsNoProcessWasStarted(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "cancel queued application"), "queued service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, app, runtimeID, "start", "cancel-unclaimed")
	cancelled := applicationTestCancel(t, app.ID, operation.ID)
	if cancelled.State != "cancelled" || cancelled.CancelRequestedAt == nil || cancelled.CancelActorID != testUserID || cancelled.Steps[0].State != "cancelled" {
		t.Fatalf("cancellation receipt=%+v", cancelled)
	}
	instance, err := testHandler.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: parseUUID(operation.Steps[0].InstanceID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil || instance.DesiredState != "stopped" || instance.Generation != instance.ObservedGeneration {
		t.Fatalf("undispatched start did not settle: %+v %v", instance, err)
	}
	if len(applicationTestClaim(t, runtimeID)) != 0 {
		t.Fatal("cancelled command was delivered")
	}
	registry, err := testHandler.applicationService().RuntimeInstances(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
	if err != nil || len(registry) != 1 || !registry[0].ConfirmedStopped {
		t.Fatalf("undispatched cancellation lost its stopped proof: %+v %v", registry, err)
	}
	if repeated := applicationTestCancel(t, app.ID, operation.ID); repeated.CancelRequestedAt == nil || !repeated.CancelRequestedAt.Equal(*cancelled.CancelRequestedAt) {
		t.Fatal("cancellation retry changed its audit identity")
	}
}

func TestApplicationCancelKeepsCompletedMembersAndStopsUnfinishedClaims(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	project := dbfx.Project(t, "cancel composed applications")
	first := applicationTestCreate(t, project, "ready member", "service")
	second := applicationTestCreate(t, project, "unfinished member", "service")
	root := applicationTestCreate(t, project, "composition", "composition")
	for _, app := range []application.View{first, second, root} {
		applicationTestOperationCleanup(t, app.ID)
	}
	relations := []application.Relation{{Type: "contains", TargetID: first.ID, Required: true}, {Type: "contains", TargetID: second.ID, Required: true}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+root.ID, application.UpdateInput{Revision: 1, Relations: &relations}, map[string]string{"id": root.ID})).Want(http.StatusOK).JSON(&root)
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, root, runtimeID, "start", "cancel-partial")
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 2 {
		t.Fatalf("expected parallel members: %+v", claims)
	}
	completed, pending := claims[0], claims[1]
	applicationTestFinish(t, runtimeID, completed, "completed")
	cancelled := applicationTestCancel(t, root.ID, operation.ID)
	if cancelled.State != "cancelling" || len(cancelled.Steps) != 3 {
		t.Fatalf("unfinished process was declared cancelled early: %+v", cancelled)
	}
	lease := squadScopeReq("", http.MethodPost, "/lease", map[string]any{"daemon_id": applicationTestDaemonID, "claim_token": pending.ClaimToken}, map[string]string{"runtimeId": runtimeID, "stepId": pending.StepID})
	testutil.Call(t, testHandler.RenewRuntimeApplicationLease, lease).Want(http.StatusConflict)
	remaining := applicationTestClaim(t, runtimeID)
	if len(remaining) != 1 || remaining[0].Command.Action != "stop" || remaining[0].Command.Generation <= pending.Command.Generation {
		t.Fatalf("missing fenced stop: %+v", remaining)
	}
	applicationTestFinish(t, runtimeID, remaining[0], "completed")
	final := applicationTestGetOperation(t, root.ID, operation.ID)
	if final.State != "cancelled" {
		t.Fatalf("cancellation did not settle after shutdown: %+v", final)
	}
	ready, err := testHandler.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: parseUUID(completed.Command.InstanceID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil || ready.DesiredState != "running" || ready.Generation != completed.Command.Generation {
		t.Fatalf("completed independent member was stopped: %+v %v", ready, err)
	}
}

func TestApplicationCancelStopResumesOnlyAnUnclaimedStop(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, claimed := range []bool{false, true} {
		name := "queued stop"
		if claimed {
			name = "executing stop"
		}
		t.Run(name, func(t *testing.T) {
			app := applicationTestCreate(t, dbfx.Project(t, "cancel stop"), "service", "service")
			applicationTestOperationCleanup(t, app.ID)
			runtimeID := applicationTestRuntime(t)
			applicationTestEnqueue(t, app, runtimeID, "start", "cancel-stop-start")
			applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
			operation := applicationTestEnqueue(t, app, runtimeID, "stop", "cancel-stop-operation")
			if claimed {
				if len(applicationTestClaim(t, runtimeID)) != 1 {
					t.Fatal("stop not claimed")
				}
			}
			cancelled := applicationTestCancel(t, app.ID, operation.ID)
			if cancelled.State != "cancelling" {
				t.Fatalf("stop reconciliation was skipped: %+v", cancelled)
			}
			claims := applicationTestClaim(t, runtimeID)
			want := "resume"
			if claimed {
				want = "stop"
			}
			if len(claims) != 1 || claims[0].Command.Action != want {
				t.Fatalf("stop cancellation command=%+v want=%s", claims, want)
			}
			applicationTestFinish(t, runtimeID, claims[0], "completed")
			if final := applicationTestGetOperation(t, app.ID, operation.ID); final.State != "cancelled" {
				t.Fatalf("stop cancellation did not finish: %+v", final)
			}
		})
	}
}

func TestApplicationCancelCannotUseAnotherApplicationsOperation(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	project := dbfx.Project(t, "cancellation isolation")
	first := applicationTestCreate(t, project, "first", "service")
	other := applicationTestCreate(t, project, "other", "service")
	applicationTestOperationCleanup(t, first.ID)
	operation := applicationTestEnqueue(t, first, applicationTestRuntime(t), "start", "cancel-isolation")
	testutil.Call(t, testHandler.CancelApplicationOperation, squadScopeReq("", http.MethodPost, "/cancel", nil, map[string]string{"id": other.ID, "operationId": operation.ID})).Want(http.StatusNotFound)
	if final := applicationTestGetOperation(t, first.ID, operation.ID); final.State != "queued" {
		t.Fatal("unrelated cancellation changed the operation")
	}
}
