package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionReportIsIdempotentAndProofImmutable(t *testing.T) {
	// Given an authenticated report persisted under one intervention ID.
	f := newInterventionFixture(t, "issue")
	first := f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	// When the producer retries exactly the same report.
	again := f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	// Then retry creates neither a new row nor a new version.
	if first.ID != again.ID || first.Version != again.Version {
		t.Fatalf("idempotency failed: first=%+v again=%+v", first, again)
	}
	f.sendReport(t, protocol.VscreenInterventionHuman, 200)
	ready := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
	// A newly asserted receipt for the already-ready report is a different action, not a retry.
	f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 409)
	stored, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: ready.ID, WorkspaceID: ready.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReturnReceiptID != ready.ReturnReceiptID || stored.Version != ready.Version {
		t.Fatal("replayed report replaced accepted proof")
	}
	t.Log("identical report 200 unchanged version; altered ready receipt 409 unchanged persisted proof")
}

func TestVscreenInterventionReportRejectsForeignSourceAgent(t *testing.T) {
	// Given a real stopped task owned by another agent on the same daemon runtime.
	f := newInterventionFixture(t, "issue")
	other := dbfx.Agent(t, "foreign source", f.report.RuntimeID)
	foreignTask := dbfx.Task(t, other, testutil.Cols{"runtime_id": f.report.RuntimeID, "status": "failed", "completed_at": testutil.Raw("now()"), "failure_reason": "gui_human_intervention"})
	f.report.SourceTaskID = foreignTask
	// When the reporting agent claims that foreign task as its source.
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 409)
	// Then no intervention can be created from mismatched source ownership.
	if n := dbfx.Count(t, "SELECT count(*) FROM runtime_vscreen_intervention WHERE source_task_id=$1", foreignTask); n != 0 {
		t.Fatalf("interventions=%d", n)
	}
}
