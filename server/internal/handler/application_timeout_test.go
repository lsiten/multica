package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func applicationTestExpireOperation(t *testing.T, operationID string) {
	t.Helper()
	if err := testHandler.Queries.SetApplicationOperationDeadline(context.Background(), db.SetApplicationOperationDeadlineParams{ID: parseUUID(operationID), WorkspaceID: parseUUID(testWorkspaceID), DeadlineAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	operations, err := testHandler.applicationService().ExpireOperations(context.Background(), time.Now(), 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if operation.ID == operationID {
			return
		}
	}
	t.Fatal("expired operation was not settled")
}

func TestApplicationTimeoutKeepsReadySharedMembersAndExpiresOnlyUnfinishedBranches(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application partial timeout")
	ready := applicationTestCreate(t, projectID, "shared API", "service")
	pending := applicationTestCreate(t, projectID, "pending API", "service")
	root := applicationTestCreate(t, projectID, "composition", "composition")
	for _, app := range []application.View{ready, pending, root} {
		applicationTestOperationCleanup(t, app.ID)
	}
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, ready, runtimeID, "start", "timeout-independent-start")
	initial := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, initial, "completed")
	relations := []application.Relation{{Type: "contains", TargetID: ready.ID, Required: true}, {Type: "contains", TargetID: pending.ID, Required: true}}
	testutil.Call(t, testHandler.UpdateApplication, squadScopeReq("", http.MethodPatch, "/applications/"+root.ID, application.UpdateInput{Revision: root.Revision, Relations: &relations}, map[string]string{"id": root.ID})).Want(http.StatusOK).JSON(&root)
	operation := applicationTestEnqueue(t, root, runtimeID, "start", "timeout-composition")
	claims := applicationTestClaim(t, runtimeID)
	if len(claims) != 2 {
		t.Fatalf("composition did not include both service checks: %d", len(claims))
	}
	for _, claim := range claims {
		if claim.Command.ApplicationID == ready.ID {
			if claim.Command.Generation != initial.Command.Generation {
				t.Fatal("ready shared member was assigned a replacement generation")
			}
			applicationTestFinish(t, runtimeID, claim, "completed")
		}
	}
	applicationTestExpireOperation(t, operation.ID)
	instance, err := testHandler.Queries.GetApplicationInstance(context.Background(), db.GetApplicationInstanceParams{ID: parseUUID(initial.Command.InstanceID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil || instance.DesiredState != "running" || instance.Generation != initial.Command.Generation || instance.ProcessState != "running" {
		t.Fatalf("composition timeout disrupted its ready shared member: %+v %v", instance, err)
	}
	consumers, err := testHandler.Queries.ListApplicationInstanceConsumers(context.Background(), db.ListApplicationInstanceConsumersParams{WorkspaceID: parseUUID(testWorkspaceID), InstanceID: instance.ID})
	if err != nil || len(consumers) != 2 {
		t.Fatalf("timeout discarded shared ownership: %+v %v", consumers, err)
	}
}

func TestApplicationCancellationTimeoutLeavesResumeForReconnectWithoutEnablingRestart(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application resume timeout"), "API", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "timeout-resume-start")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	operation := applicationTestEnqueue(t, app, runtimeID, "stop", "timeout-cancel-stop")
	cancelled := applicationTestCancel(t, app.ID, operation.ID)
	if !cancelled.DeadlineAt.After(time.Now().Add(14*time.Minute)) || cancelled.DeadlineAt.After(time.Now().Add(16*time.Minute)) {
		t.Fatal("cancellation did not receive its bounded reconciliation window")
	}
	applicationTestExpireOperation(t, operation.ID)
	final := applicationTestGetOperation(t, app.ID, operation.ID)
	instances, err := testHandler.applicationService().RuntimeInstances(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
	if err != nil || final.State != "failed" || len(instances) != 1 || instances[0].Command.Action != "resume" || instances[0].HasPendingOperation || instances[0].CanRestore {
		t.Fatalf("cancel timeout lost resume intent or enabled process replacement: %+v %+v %v", final, instances, err)
	}
}

func TestApplicationDeadlineRejectsClaimsLeasesAndAcknowledgementsBeforeSweep(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application deadline fence"), "API", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	operation := applicationTestEnqueue(t, app, runtimeID, "start", "deadline-fence")
	claim := applicationTestClaim(t, runtimeID)[0]
	ctx := context.Background()
	service := testHandler.applicationService()
	if err := testHandler.Queries.SetApplicationOperationDeadline(ctx, db.SetApplicationOperationDeadlineParams{ID: parseUUID(operation.ID), WorkspaceID: parseUUID(testWorkspaceID), DeadlineAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := service.RenewLease(ctx, parseUUID(testWorkspaceID), parseUUID(runtimeID), parseUUID(claim.StepID), parseUUID(claim.ClaimToken)); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("deadline allowed the stale execution lease: %v", err)
	}
	result := protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "completed", Observation: protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}}
	if _, err := service.Complete(ctx, parseUUID(testWorkspaceID), parseUUID(runtimeID), parseUUID(claim.StepID), result); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("late acknowledgement replaced timeout with success: %v", err)
	}
	if claims := applicationTestClaim(t, runtimeID); len(claims) != 0 {
		t.Fatal("expired operation was dispatched before the cleanup tick")
	}
	applicationTestExpireOperation(t, operation.ID)
}

func TestApplicationTimeoutFencesOfflineStartAndRetainsActualProcessUncertainty(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "never dispatched", true: "dispatched without confirmation"}[claimed], func(t *testing.T) {
			app := applicationTestCreate(t, dbfx.Project(t, "application timeout"), "API", "service")
			applicationTestOperationCleanup(t, app.ID)
			runtimeID := applicationTestRuntime(t)
			operation := applicationTestEnqueue(t, app, runtimeID, "start", "timeout-start")
			if !operation.DeadlineAt.After(time.Now().Add(3 * time.Hour)) {
				t.Fatal("startup deadline omitted source/readiness allowance")
			}
			var claim protocol.ApplicationClaim
			if claimed {
				claim = applicationTestClaim(t, runtimeID)[0]
			}
			applicationTestExpireOperation(t, operation.ID)
			final := applicationTestGetOperation(t, app.ID, operation.ID)
			if final.State != "failed" || final.Steps[0].State != "failed" || final.Error == "" {
				t.Fatalf("timeout remained pending or lost its reason: %+v", final)
			}
			instances, err := testHandler.applicationService().RuntimeInstances(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
			if err != nil || len(instances) != 1 || instances[0].DesiredState != "stopped" || instances[0].HasPendingOperation || instances[0].ConfirmedStopped == claimed {
				t.Fatalf("timeout invented process exit or blocked reconnect cleanup: %+v %v", instances, err)
			}
			if claimed {
				if err := testHandler.applicationService().RenewLease(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID), parseUUID(claim.StepID), parseUUID(claim.ClaimToken)); !errors.Is(err, application.ErrConflict) {
					t.Fatalf("expired lease remained valid: %v", err)
				}
			}
			if claims := applicationTestClaim(t, runtimeID); len(claims) != 0 {
				t.Fatal("expired operation dispatched again")
			}
		})
	}
}

func TestApplicationStopTimeoutPreservesStopIntentWithoutClaimingExit(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "application stop timeout"), "API", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "timeout-ready-start")
	applicationTestFinish(t, runtimeID, applicationTestClaim(t, runtimeID)[0], "completed")
	operation := applicationTestEnqueue(t, app, runtimeID, "stop", "timeout-stop")
	applicationTestExpireOperation(t, operation.ID)
	instances, err := testHandler.applicationService().RuntimeInstances(context.Background(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
	if err != nil || len(instances) != 1 || instances[0].DesiredState != "stopped" || instances[0].ConfirmedStopped || instances[0].HasPendingOperation {
		t.Fatalf("stop timeout withdrew the requested stop or invented exit: %+v %v", instances, err)
	}
}
