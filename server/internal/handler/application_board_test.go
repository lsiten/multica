package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplicationBoardProjectsRealStateAndProtectsPrivateRuntimeControl(t *testing.T) {
	if testPool == nil {
		t.Skip("database unavailable")
	}
	projectID := dbfx.Project(t, "application board")
	app := applicationTestCreate(t, projectID, "API", "service")
	applicationTestCreate(t, projectID, "composition", "composition")
	applicationTestOperationCleanup(t, app.ID)
	runtimeID := applicationTestRuntime(t)
	applicationTestEnqueue(t, app, runtimeID, "start", "board-start")
	claim := applicationTestClaim(t, runtimeID)[0]
	applicationTestFinish(t, runtimeID, claim, "completed")
	instanceID := parseUUID(claim.Command.InstanceID)
	if _, err := testHandler.Queries.UpsertApplicationEndpoint(context.Background(), db.UpsertApplicationEndpointParams{WorkspaceID: parseUUID(testWorkspaceID), ApplicationID: parseUUID(app.ID), InstanceID: instanceID, Port: 4100, EntryPath: "/", Visibility: "workspace", PublishedBy: parseUUID(testUserID), State: "published"}); err != nil {
		t.Fatal(err)
	}
	var board applicationBoardView
	testutil.Call(t, testHandler.GetApplicationBoard, squadScopeReq("", http.MethodGet, "/applications/board", nil, nil)).Want(http.StatusOK).JSON(&board)
	found := false
	for _, instance := range board.Instances {
		if instance.ID == claim.Command.InstanceID {
			found = true
			if instance.Status != "running" || !instance.CanManage {
				t.Fatalf("current instance: %+v", instance)
			}
		}
	}
	if !found || board.Summary.Compositions < 1 || board.Summary.Running < 1 {
		t.Fatalf("board projection: %+v", board.Summary)
	}
	memberID := createPlainMember(t, "application-board-reader@multica.test")
	testutil.Call(t, testHandler.GetApplicationBoard, squadScopeReq(memberID, http.MethodGet, "/applications/board", nil, nil)).Want(http.StatusOK).JSON(&board)
	for _, instance := range board.Instances {
		if instance.ID == claim.Command.InstanceID && instance.CanManage {
			t.Fatal("board granted control of another member's private runtime")
		}
	}
	testutil.Call(t, testHandler.EnqueueApplicationOperation, squadScopeReq("", http.MethodPost, "/operations", map[string]any{"action": "stop", "revision": 1, "runtime_id": runtimeID, "idempotency_key": "board-stop"}, map[string]string{"id": app.ID})).Want(http.StatusAccepted)
	testutil.Call(t, testHandler.GetApplicationBoard, squadScopeReq("", http.MethodGet, "/applications/board", nil, nil)).Want(http.StatusOK).JSON(&board)
	for _, instance := range board.Instances {
		if instance.ID == claim.Command.InstanceID && instance.Status != "stopping" {
			t.Fatalf("old healthy observation masked stop intent: %+v", instance)
		}
	}
}
