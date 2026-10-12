package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type applicationRegistryQueryCounter struct {
	db.DBTX
	queries int
}

func TestApplicationHeartbeatRefreshesFactsWithoutRepeatedWorkspaceEvents(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	app := applicationTestCreate(t, dbfx.Project(t, "heartbeat observation"), "API", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "heartbeat-start")
	claim := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, claim, "completed")
	h := *testHandler
	h.Bus = events.New()
	changed := 0
	h.Bus.Subscribe(protocol.EventApplicationChanged, func(events.Event) { changed++ })
	observation := protocol.ApplicationObservation{InstanceID: claim.Command.InstanceID, Generation: claim.Command.Generation, Revision: claim.Command.Revision, ProcessState: "running", HealthState: "healthy"}
	send := func(desired string) {
		t.Helper()
		request := squadScopeReq("", http.MethodPost, "/observe", map[string]any{"daemon_id": applicationTestDaemonID, "observation": observation, "desired_state": desired}, map[string]string{"runtimeId": runtimeID})
		request = request.WithContext(middleware.WithDaemonContext(request.Context(), testWorkspaceID, applicationTestDaemonID))
		testutil.Call(t, h.ReportRuntimeApplication, request).Want(http.StatusNoContent)
	}
	var before, after string
	if err := testPool.QueryRow(t.Context(), "SELECT xmin::text FROM application_instance WHERE id=$1", observation.InstanceID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	send("")
	send("")
	observation.Metrics = map[string]float64{"rss_bytes": 512}
	send("")
	if err := testPool.QueryRow(t.Context(), "SELECT xmin::text FROM application_instance WHERE id=$1", observation.InstanceID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == after || changed != 0 {
		t.Fatalf("heartbeat not persisted or repeated state broadcast: row=%s/%s events=%d", before, after, changed)
	}
	observation.ProcessState, observation.HealthState = "stopped", "unknown"
	send("stopped")
	send("stopped")
	if changed != 1 {
		t.Fatalf("real state change did not produce exactly one event: %d", changed)
	}
}

func (q *applicationRegistryQueryCounter) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.queries++
	return q.DBTX.Query(ctx, sql, args...)
}
func (q *applicationRegistryQueryCounter) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.queries++
	return q.DBTX.QueryRow(ctx, sql, args...)
}

func TestApplicationRegistryBatchesMultipleInstancesInOneQuery(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	runtimeID := applicationTestRuntime(t)
	projectID := dbfx.Project(t, "batched registry")
	for _, name := range []string{"API", "worker"} {
		app := applicationTestCreate(t, projectID, name, "service")
		applicationTestOperationCleanup(t, app.ID)
		applicationTestEnqueue(t, app, runtimeID, "start", "batch-"+name)
		claims := applicationTestClaim(t, runtimeID)
		if len(claims) != 1 {
			t.Fatalf("claim=%+v", claims)
		}
		applicationTestFinish(t, runtimeID, claims[0], "completed")
	}
	counted := &applicationRegistryQueryCounter{DBTX: testPool}
	service := &application.Service{Queries: db.New(counted), Transactions: testHandler.TxStarter}
	instances, err := service.RuntimeInstances(t.Context(), parseUUID(testWorkspaceID), parseUUID(runtimeID))
	if err != nil || len(instances) != 2 || counted.queries != 1 {
		t.Fatalf("instances=%d SQL calls=%d err=%v", len(instances), counted.queries, err)
	}
	for _, instance := range instances {
		if !instance.CanRestore || instance.HasPendingOperation {
			t.Fatalf("completed instance lost restore eligibility: %+v", instance)
		}
	}
}

func TestApplicationRegistryBatchRechecksCurrentAuthorization(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	for _, change := range []string{"membership", "runtime", "agent-project", "agent-archived"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			userID := createPlainMember(t, "registry-"+uuid.NewString()+"@multica.test")
			runtimeID := applicationTestRuntime(t)
			if _, err := testHandler.Queries.UpdateAgentRuntimeVisibility(ctx, db.UpdateAgentRuntimeVisibilityParams{ID: parseUUID(runtimeID), Visibility: "public"}); err != nil {
				t.Fatal(err)
			}
			projectID := dbfx.Project(t, "registry permissions")
			app := applicationTestCreate(t, projectID, "API", "service")
			applicationTestOperationCleanup(t, app.ID)
			request := squadScopeReq(userID, http.MethodPost, "/operations", application.OperationInput{Action: "start", Revision: app.Revision, RuntimeID: runtimeID, IdempotencyKey: "completed-authorization"}, map[string]string{"id": app.ID})
			agentID := ""
			if change == "agent-project" || change == "agent-archived" {
				agentID = dbfx.Agent(t, "registry agent", runtimeID, testutil.Cols{"owner_id": userID})
				taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID})
				if _, err := testPool.Exec(ctx, "UPDATE project SET lead_type='agent',lead_id=$1 WHERE id=$2", agentID, projectID); err != nil {
					t.Fatal(err)
				}
				request.Header.Set("X-Actor-Source", "task_token")
				request.Header.Set("X-Agent-ID", agentID)
				request.Header.Set("X-Task-ID", taskID)
			}
			testutil.Call(t, testHandler.EnqueueApplicationOperation, request).Want(http.StatusAccepted)
			claim := applicationTestClaim(t, runtimeID)[0]
			applicationTestFinish(t, runtimeID, claim, "completed")
			read := func() bool {
				t.Helper()
				rows, err := testHandler.applicationService().RuntimeInstances(ctx, parseUUID(testWorkspaceID), parseUUID(runtimeID))
				if err != nil || len(rows) != 1 {
					t.Fatalf("registry=%+v %v", rows, err)
				}
				return rows[0].CanRestore
			}
			if !read() {
				t.Fatal("authorized completed service cannot restore")
			}
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
				dbfx.Project(t, "narrower registry scope", testutil.Cols{"lead_type": "agent", "lead_id": agentID})
			case "agent-archived":
				if _, err := testPool.Exec(ctx, "UPDATE agent SET archived_at=now() WHERE id=$1", agentID); err != nil {
					t.Fatal(err)
				}
			}
			if read() {
				t.Fatal("batch registry reused revoked authorization")
			}
		})
	}
}
