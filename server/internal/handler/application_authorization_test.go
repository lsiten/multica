package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestApplicationQueuedStartRechecksMembershipRuntimeAndAgentProjectAccess(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, change := range []string{"membership", "runtime", "agent-project"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			userID := createPlainMember(t, "application-queued-"+uuid.NewString()+"@multica.test")
			runtimeID := applicationTestRuntime(t)
			if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "public"}); err != nil {
				t.Fatal(err)
			}
			projectID := dbfx.Project(t, "queued application authorization")
			app := applicationTestCreate(t, projectID, "API", "service")
			applicationTestOperationCleanup(t, app.ID)
			request := squadScopeReq(userID, http.MethodPost, "/operations", application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: runtimeID, IdempotencyKey: "queued-authorization"}, map[string]string{"id": app.ID})
			var agentID string
			if change == "agent-project" {
				agentID = dbfx.Agent(t, "application queued agent", runtimeID, testutil.Cols{"owner_id": userID})
				taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
				if _, err := testPool.Exec(ctx, "UPDATE project SET lead_type='agent',lead_id=$1 WHERE id=$2", agentID, projectID); err != nil {
					t.Fatal(err)
				}
				request.Header.Set("X-Actor-Source", "task_token")
				request.Header.Set("X-Agent-ID", agentID)
				request.Header.Set("X-Task-ID", taskID)
			}
			var operation application.OperationView
			testutil.Call(t, testHandler.EnqueueApplicationOperation, request).Want(http.StatusAccepted).JSON(&operation)
			switch change {
			case "membership":
				member, err := testHandler.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: parseUUID(userID), WorkspaceID: parseUUID(testWorkspaceID)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := testHandler.revokeAndRemoveMember(ctx, parseUUID(testWorkspaceID), parseUUID(userID), member.ID, parseUUID(testUserID)); err != nil {
					t.Fatal(err)
				}
				dbfx.Member(t, testWorkspaceID, userID, "member")
			case "runtime":
				if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "private"}); err != nil {
					t.Fatal(err)
				}
			case "agent-project":
				if _, err := testPool.Exec(ctx, "UPDATE project SET lead_type=NULL,lead_id=NULL WHERE id=$1", projectID); err != nil {
					t.Fatal(err)
				}
				dbfx.Project(t, "new queued agent scope", testutil.Cols{"lead_type": "agent", "lead_id": agentID})
			}
			if claims := applicationTestClaim(t, runtimeID); len(claims) != 0 {
				t.Fatal("revoked queued authorization dispatched application code")
			}
			final := applicationTestGetOperation(t, app.ID, operation.ID)
			if final.State != "failed" || final.Steps[0].State != "blocked" {
				t.Fatalf("revoked queued operation was not settled: state=%s", final.State)
			}
		})
	}
}

func TestApplicationPreparingClaimCannotRenewOrPublishAfterRuntimePermissionRevocation(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	ctx := context.Background()
	userID := createPlainMember(t, "application-preparing-"+uuid.NewString()+"@multica.test")
	runtimeID := applicationTestRuntime(t)
	if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	app := applicationTestCreate(t, dbfx.Project(t, "preparation permission"), "API", "service")
	applicationTestOperationCleanup(t, app.ID)
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq(userID, http.MethodPost, "/operations", application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: runtimeID, IdempotencyKey: "preparing-permission"}, map[string]string{"id": app.ID})).Want(http.StatusAccepted)
	claim := applicationTestClaim(t, runtimeID)[0]
	if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	service := testHandler.applicationService()
	if err := service.RenewLease(ctx, parseUUID(testWorkspaceID), parseUUID(runtimeID), parseUUID(claim.StepID), parseUUID(claim.ClaimToken)); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("preparing service renewed revoked runtime authorization: %v", err)
	}
	result := protocol.ApplicationStepResult{ClaimToken: claim.ClaimToken, State: "completed", Observation: protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}}
	if _, err := service.Complete(ctx, parseUUID(testWorkspaceID), parseUUID(runtimeID), parseUUID(claim.StepID), result); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("revoked preparation created a successful or published result: %v", err)
	}
}
