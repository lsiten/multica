package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionRuntimeDeletionCancelsDurableHandoff(t *testing.T) {
	// Given a persisted ready intervention owned by a runtime.
	f := newInterventionFixture(t, "issue")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	// When the production runtime teardown and deletion commit.
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err = service.TeardownRuntime(ctx, q, row.RuntimeID, service.RuntimeTeardownOptions{CancelNonTerminalTasks: true}); err != nil {
		t.Fatal(err)
	}
	if err = q.DeleteAgentRuntime(ctx, row.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// Then the durable handoff is cancelled and the human continuation route cannot enqueue.
	stored, err := f.h.Queries.GetVscreenIntervention(ctx, db.GetVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "cancelled" {
		t.Fatalf("state=%s", stored.State)
	}
	testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{})).Want(404)
	t.Log("runtime teardown committed; persisted intervention cancelled; continuation HTTP 404")
}

func TestVscreenInterventionRevokedInvokePermission(t *testing.T) {
	// Given a ready handoff after the actor loses the right to invoke its agent.
	f := newInterventionFixture(t, "issue")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	other := dbfx.User(t, "other owner", "intervention-other-"+f.report.InterventionID+"@test.invalid")
	dbfx.Exec(t, "UPDATE agent SET owner_id=$2,permission_mode='private' WHERE id=$1", f.report.AgentID, other)
	dbfx.Cleanup(t, "UPDATE agent SET owner_id=$2 WHERE id=$1", f.report.AgentID, testUserID)
	// When the old owner sends a continuation request.
	testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{})).Want(403)
	// Then transactional invocation authorization leaves the receipt unconsumed.
	stored, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContinuationTaskID.Valid {
		t.Fatal("unauthorized proof consumption")
	}
}
