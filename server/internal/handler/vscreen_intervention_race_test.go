package handler

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionCancelContinueRace(t *testing.T) {
	// Given a ready intervention with an unconsumed receipt.
	f := newInterventionFixture(t, "issue")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	// When cancel and continue race through their human HTTP handlers.
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for _, handler := range []http.HandlerFunc{f.h.CancelVscreenIntervention, f.h.ContinueVscreenIntervention} {
		wg.Add(1)
		go func(h http.HandlerFunc) {
			defer wg.Done()
			<-start
			response := testutil.Call(t, h, f.continueRequest(map[string]any{})).WantOneOf(200, 409)
			statuses <- response.Code
		}(handler)
	}
	close(start)
	wg.Wait()
	close(statuses)
	// Then exactly one operation wins, and no child exists when cancellation wins.
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful operations=%d", successes)
	}
	stored, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	count := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id=$1", f.report.SourceTaskID)
	if stored.State == "cancelled" && count != 0 || stored.State == "continued" && count != 1 {
		t.Fatalf("state=%s children=%d", stored.State, count)
	}
	t.Logf("HTTP successful_operations=1 DB state=%s child_count=%d", stored.State, count)
}

func TestVscreenInterventionStaleVersionCannotConsume(t *testing.T) {
	// Given a preflight row and a fresh native observation that precede cancellation.
	f := newInterventionFixture(t, "issue")
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	rt, err := f.h.Queries.GetAgentRuntime(context.Background(), row.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.h.DaemonHub.QueryVscreen(context.Background(), f.report.WorkspaceID, f.report.RuntimeID, rt.DaemonID.String, "state")
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, f.h.CancelVscreenIntervention, f.continueRequest(map[string]any{})).Want(200)
	// When the previously observed proof is submitted to the transactional service.
	_, err = f.h.TaskService.ContinueAfterIntervention(context.Background(), service.InterventionContinuation{Intervention: row, ActorID: parseUUID(testUserID), Observation: proof})
	// Then version/state checks reject it without creating a child.
	if err != service.InterventionError("intervention_changed") {
		t.Fatalf("error=%v", err)
	}
	if count := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id=$1", f.report.SourceTaskID); count != 0 {
		t.Fatalf("children=%d", count)
	}
}
