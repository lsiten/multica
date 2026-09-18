package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestDeleteWorkspaceRemovesAllInterventionStatesOnlyInItsWorkspace(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	lockRollupSingleton(t)
	setWorkspaceDeleteLockTimeoutForTest(t, 500*time.Millisecond)
	workspaceID := dbfx.Workspace(t, "Intervention deletion", "intervention-delete-"+uuid.NewString())
	dbfx.Member(t, workspaceID, testUserID, "owner")
	insert := func(workspaceID, state string) string {
		return dbfx.Insert(t, "runtime_vscreen_intervention", testutil.Cols{
			"id": uuid.NewString(), "workspace_id": workspaceID, "runtime_id": uuid.NewString(), "agent_id": uuid.NewString(), "source_task_id": uuid.NewString(),
			"reason": "background_unsupported", "state": state, "native_epoch": "native", "display_generation": "display", "geometry_revision": 1, "created_by_user_id": testUserID,
		})
	}
	// History can outlive its run/runtime because there are no cascading foreign keys.
	for _, state := range []string{"awaiting_takeover", "human", "ready_to_continue", "continued", "cancelled", "stale"} {
		insert(workspaceID, state)
	}
	otherID := insert(testWorkspaceID, "human")
	request := withURLParam(newRequest("DELETE", "/api/workspaces/"+workspaceID, nil), "id", workspaceID)
	testutil.Call(t, testHandler.DeleteWorkspace, request).Want(http.StatusNoContent)
	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM runtime_vscreen_intervention WHERE workspace_id=$1`, workspaceID).Scan(&count)
	if count != 0 {
		t.Fatalf("deleted workspace retains %d intervention rows", count)
	}
	dbfx.QueryRow(t, `SELECT count(*) FROM runtime_vscreen_intervention WHERE id=$1 AND workspace_id=$2`, otherID, testWorkspaceID).Scan(&count)
	if count != 1 {
		t.Fatal("another workspace's intervention was deleted")
	}
}
