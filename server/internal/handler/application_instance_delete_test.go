package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/application"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestApplicationInstanceDeleteRemovesInstanceAndKeepsDefinition enqueues a
// delete operation on a running instance and verifies the confirmed-stopped
// process removes the instance (and its runtime footprint) while preserving the
// reusable application definition so it can be started again.
func TestApplicationInstanceDeleteRemovesInstanceAndKeepsDefinition(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "instance deletion")
	app := applicationTestCreate(t, projectID, "deletable service", "service")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	instanceID := dbfx.Insert(t, "application_instance", testutil.Cols{
		"workspace_id": testWorkspaceID, "application_id": app.ID, "runtime_id": runtimeID, "daemon_id": applicationTestDaemonID,
		"revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running",
	})

	operation := applicationTestEnqueue(t, app, runtimeID, "delete", "delete-instance")
	if operation.State != "queued" || len(operation.Steps) != 1 {
		t.Fatalf("delete acceptance is not completion: state=%s steps=%d", operation.State, len(operation.Steps))
	}
	// The delete command sent to the daemon must be a stop: the daemon only
	// knows how to stop a process; the server performs the removal on confirm.
	claim := applicationTestClaim(t, runtimeID)[0]
	if claim.Command.Action != "stop" {
		t.Fatalf("delete enqueued command action %q, want stop", claim.Command.Action)
	}
	applicationTestFinish(t, runtimeID, claim, "completed")

	var count int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM application_instance WHERE id=$1", instanceID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("delete did not remove the instance: count=%d err=%v", count, err)
	}
	if _, err := testHandler.applicationService().Get(context.Background(), parseUUID(testWorkspaceID), parseUUID(app.ID)); err != nil {
		t.Fatalf("delete removed the reusable application definition: %v", err)
	}
	completed := applicationTestGetOperation(t, app.ID, operation.ID)
	if completed.State != "completed" {
		t.Fatalf("delete operation state = %s, want completed", completed.State)
	}
}

// TestApplicationInstanceDeleteRejectsSharedInstance guards the shared-service
// conflict: a delete without force must not remove an instance another
// application still consumes.
func TestApplicationInstanceDeleteRejectsSharedInstance(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "shared instance deletion")
	producer := applicationTestCreate(t, projectID, "shared producer", "service")
	consumer := applicationTestCreate(t, projectID, "consumer app", "service")
	applicationTestOperationCleanup(t, producer.ID)
	runtimeID := applicationTestRuntime(t)
	producerInstanceID := dbfx.Insert(t, "application_instance", testutil.Cols{
		"workspace_id": testWorkspaceID, "application_id": producer.ID, "runtime_id": runtimeID, "daemon_id": applicationTestDaemonID,
		"revision": 1, "generation": 1, "observed_generation": 1, "desired_state": "running", "process_state": "running",
	})
	if _, err := testHandler.Queries.AddApplicationInstanceConsumer(context.Background(), db.AddApplicationInstanceConsumerParams{WorkspaceID: parseUUID(testWorkspaceID), InstanceID: parseUUID(producerInstanceID), RootApplicationID: parseUUID(consumer.ID), RootRuntimeID: parseUUID(runtimeID), ActorID: parseUUID(testUserID), Wave: -1, OwnsLifecycle: false}); err != nil {
		t.Fatal(err)
	}
	input := application.OperationInput{Action: "delete", Revision: producer.Revision, RuntimeID: runtimeID, IdempotencyKey: "delete-shared"}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq("", http.MethodPost, "/applications/"+producer.ID+"/operations", input, map[string]string{"id": producer.ID})).Want(http.StatusConflict)

	var count int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM application_instance WHERE id=$1", producerInstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared delete changed the instance: count=%d err=%v", count, err)
	}
}
