package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenInterventionPrivateSourceAccess(t *testing.T) {
	for _, scope := range []string{"chat", "quick"} {
		for _, action := range []string{"list", "get", "continue", "cancel", "service_continue", "service_cancel"} {
			t.Run(scope+"/"+action, func(t *testing.T) {
				f := newInterventionFixture(t, scope)
				other := dbfx.User(t, "another member", uuid.NewString()+"@example.test")
				dbfx.Member(t, testWorkspaceID, other, "member")
				dbfx.Exec(t, "UPDATE agent_runtime SET visibility='public' WHERE id=$1", f.report.RuntimeID)
				dbfx.Exec(t, "UPDATE agent SET permission_mode='public_to' WHERE id=$1", f.report.AgentID)
				dbfx.Insert(t, "agent_invocation_target", testutil.Cols{"agent_id": f.report.AgentID, "target_type": "workspace", "target_id": testWorkspaceID})
				f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
				f.sendReport(t, protocol.VscreenInterventionHuman, 200)
				row := f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
				dbfx.Exec(t, "UPDATE runtime_vscreen_intervention SET human_summary='private human note' WHERE id=$1", f.report.InterventionID)
				path := "/api/tasks/" + f.report.SourceTaskID + "/vscreen/interventions/" + f.report.InterventionID
				request := func(method, path string, body any) *http.Request {
					return testutil.WithURLParams(newRequestAs(other, method, path, body), "runtimeId", f.report.RuntimeID, "taskId", f.report.SourceTaskID, "id", f.report.InterventionID)
				}
				switch action {
				case "list":
					var rows []db.RuntimeVscreenIntervention
					testutil.Call(t, f.h.ListVscreenInterventions, request(http.MethodGet, "/api/runtimes/"+f.report.RuntimeID+"/vscreen/interventions", nil)).Want(200).JSON(&rows)
					if len(rows) != 0 {
						t.Fatalf("private source leaked %d intervention rows including summary", len(rows))
					}
				case "get":
					testutil.Call(t, f.h.GetVscreenIntervention, request(http.MethodGet, path, nil)).Want(403)
				case "continue":
					testutil.Call(t, f.h.ContinueVscreenIntervention, request(http.MethodPost, path+"/continue", map[string]any{})).Want(403)
				case "cancel":
					testutil.Call(t, f.h.CancelVscreenIntervention, request(http.MethodPost, path+"/cancel", nil)).Want(403)
				case "service_continue":
					_, err := f.h.TaskService.ContinueAfterIntervention(context.Background(), service.InterventionContinuation{Intervention: row, ActorID: parseUUID(other), Observation: protocol.VscreenQueryResult{VscreenEnvelope: f.report.VscreenEnvelope, State: &f.snapshot}})
					if !errors.Is(err, service.InterventionError("permission_denied")) {
						t.Fatalf("foreign source continuation err=%v", err)
					}
				case "service_cancel":
					_, err := f.h.TaskService.CancelVscreenIntervention(context.Background(), row, parseUUID(other))
					if !errors.Is(err, service.InterventionError("permission_denied")) {
						t.Fatalf("foreign source cancellation err=%v", err)
					}
				}
				stored, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
				if err != nil || stored.State != "ready_to_continue" || stored.ReturnReceiptID != row.ReturnReceiptID || stored.ContinuationTaskID.Valid {
					t.Fatalf("foreign actor changed proof: %+v err=%v", stored, err)
				}
				if n := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id=$1", f.report.SourceTaskID); n != 0 {
					t.Fatalf("children=%d", n)
				}
				t.Log("public runtime and invocable agent do not grant private source access; proof unchanged; zero children")
			})
		}
	}
}

func TestVscreenInterventionIssueRemainsWorkspaceReadable(t *testing.T) {
	f := newInterventionFixture(t, "issue")
	other := dbfx.User(t, "issue member", uuid.NewString()+"@example.test")
	dbfx.Member(t, testWorkspaceID, other, "member")
	dbfx.Exec(t, "UPDATE agent_runtime SET visibility='public' WHERE id=$1", f.report.RuntimeID)
	f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
	request := testutil.WithURLParams(newRequestAs(other, http.MethodGet, "/api/runtimes/"+f.report.RuntimeID+"/vscreen/interventions", nil), "runtimeId", f.report.RuntimeID)
	var rows []db.RuntimeVscreenIntervention
	testutil.Call(t, f.h.ListVscreenInterventions, request).Want(200).JSON(&rows)
	if len(rows) != 1 || uuidToString(rows[0].ID) != f.report.InterventionID {
		t.Fatalf("workspace issue missing: %+v", rows)
	}
	request = testutil.WithURLParams(newRequestAs(other, http.MethodGet, "/api/tasks/"+f.report.SourceTaskID+"/vscreen/interventions/"+f.report.InterventionID, nil), "taskId", f.report.SourceTaskID, "id", f.report.InterventionID)
	testutil.Call(t, f.h.GetVscreenIntervention, request).Want(200)
	t.Log("issue intervention follows existing workspace issue read gate; public runtime alone does not expose private sources")
}
