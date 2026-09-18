package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type interventionFixture struct {
	h        Handler
	report   protocol.VscreenIntervention
	mu       sync.Mutex
	snapshot protocol.VscreenStateSnapshot
}

func newInterventionFixture(t *testing.T, scope string) *interventionFixture {
	t.Helper()
	daemonID := uuid.NewString()
	runtimeID := dbfx.Runtime(t, "intervention", testutil.Cols{"daemon_id": daemonID})
	agentID := dbfx.Agent(t, "intervention", runtimeID)
	cols := testutil.Cols{"runtime_id": runtimeID, "status": "failed", "completed_at": testutil.Raw("now()"), "failure_reason": "gui_human_intervention", "session_id": "exact-source-session", "work_dir": "/tmp/test-owned-workdir"}
	switch scope {
	case "issue":
		cols["issue_id"] = dbfx.Issue(t, "intervention")
	case "chat":
		cols["chat_session_id"] = dbfx.ChatSession(t, agentID)
	case "quick":
		cols["context"] = testutil.Raw(`'{"type":"quick_create","prompt":"inspect","workspace_id":"` + testWorkspaceID + `","requester_id":"` + testUserID + `"}'::jsonb`)
	}
	sourceID := dbfx.Task(t, agentID, cols)
	if scope == "chat" {
		dbfx.Insert(t, "chat_message", testutil.Cols{"chat_session_id": cols["chat_session_id"], "role": "user", "content": "continue this exact chat turn", "task_id": sourceID})
		dbfx.Exec(t, "UPDATE agent_task_queue SET chat_input_task_id=id WHERE id=$1", sourceID)
	}
	f := &interventionFixture{h: *testHandler, report: protocol.VscreenIntervention{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: testWorkspaceID, RuntimeID: runtimeID, RequestID: uuid.NewString()}, InterventionID: uuid.NewString(), AgentID: agentID, SourceTaskID: sourceID, Reason: protocol.VscreenRejectionReason("background_unsupported"), State: protocol.VscreenInterventionAwaitingTakeover, Epoch: protocol.VscreenEpoch{NativeEpoch: "native-epoch", DisplayGeneration: "display-generation", GeometryRevision: 1}}}
	f.snapshot = protocol.VscreenStateSnapshot{RuntimeID: runtimeID, State: protocol.VscreenStateReady, NativeEpoch: f.report.Epoch.NativeEpoch, DisplayGeneration: f.report.Epoch.DisplayGeneration, GeometryRevision: 1, ControlState: protocol.VscreenControlAwaitingTakeover, InterventionID: &f.report.InterventionID, InterventionState: f.report.State, Permissions: protocol.VscreenPermissions{ScreenRecording: "granted", Accessibility: "granted"}, StateRevision: 1}
	hub := daemonws.NewHub()
	f.h.DaemonHub = hub
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, UserID: testUserID, WorkspaceID: testWorkspaceID, RuntimeIDs: []string{runtimeID}})
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var frame protocol.Message
			if conn.ReadJSON(&frame) != nil {
				return
			}
			if frame.Type != protocol.EventVscreenQuery {
				continue
			}
			var query protocol.VscreenQuery
			if json.Unmarshal(frame.Payload, &query) != nil {
				return
			}
			f.mu.Lock()
			snapshot := f.snapshot
			f.mu.Unlock()
			payload, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, State: &snapshot})
			if err != nil {
				return
			}
			if conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: payload}) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { conn.Close(); <-done; server.Close() })
	observed, err := hub.QueryVscreen(context.Background(), testWorkspaceID, runtimeID, daemonID, "state")
	if err != nil {
		t.Fatal(err)
	}
	f.report.DaemonGeneration = observed.DaemonGeneration
	dbfx.Cleanup(t, "DELETE FROM runtime_vscreen_intervention WHERE source_task_id=$1", sourceID)
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE rerun_of_task_id=$1", sourceID)
	return f
}

func (f *interventionFixture) sendReport(t *testing.T, state protocol.VscreenInterventionState, status int) db.RuntimeVscreenIntervention {
	t.Helper()
	f.report.State = state
	f.mu.Lock()
	f.snapshot.InterventionState = state
	if state == protocol.VscreenInterventionHuman {
		f.snapshot.ControlState = protocol.VscreenControlHuman
	}
	if state == protocol.VscreenInterventionReadyToContinue {
		f.report.ReturnReceiptID = uuid.NewString()
		f.snapshot.ReturnReceiptID = f.report.ReturnReceiptID
		f.snapshot.ControlState = protocol.VscreenControlIdle
	}
	f.mu.Unlock()
	rt, err := f.h.Queries.GetAgentRuntime(context.Background(), parseUUID(f.report.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	req := withURLParam(newRequest(http.MethodPost, "/api/daemon/runtimes/"+f.report.RuntimeID+"/vscreen/interventions", f.report), "runtimeId", f.report.RuntimeID)
	req = req.WithContext(middleware.WithDaemonContext(req.Context(), testWorkspaceID, rt.DaemonID.String))
	var row db.RuntimeVscreenIntervention
	call := testutil.Call(t, f.h.ReportVscreenIntervention, req).Want(status)
	if status == http.StatusOK {
		call.JSON(&row)
	}
	t.Logf("POST %s state=%s status=%d", req.URL.Path, state, status)
	return row
}

func (f *interventionFixture) continueRequest(body any) *http.Request {
	path := "/api/tasks/" + f.report.SourceTaskID + "/vscreen/interventions/" + f.report.InterventionID + "/continue"
	return testutil.WithURLParams(newRequest(http.MethodPost, path, body), "taskId", f.report.SourceTaskID, "id", f.report.InterventionID)
}

func TestVscreenInterventionHTTPConcurrentContinuation(t *testing.T) {
	for _, scope := range []string{"issue", "chat", "quick"} {
		t.Run(scope, func(t *testing.T) {
			// Given an authenticated stopped source and locally returned screen.
			f := newInterventionFixture(t, scope)
			observer, err := testPool.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Release()
			eventIDs := make(chan string, 2)
			svc := service.NewTaskService(f.h.Queries, f.h.TaskService.TxStarter, f.h.TaskService.Hub, events.New(), f.h.TaskService.Wakeup)
			f.h.TaskService = svc
			svc.Bus.Subscribe(protocol.EventTaskQueued, func(event events.Event) {
				var state string
				if err := observer.QueryRow(context.Background(), "SELECT state FROM runtime_vscreen_intervention WHERE continuation_task_id=$1", event.TaskID).Scan(&state); err != nil {
					t.Errorf("event before commit: %v", err)
				}
				if state != "continued" {
					t.Errorf("event before proof consumption: %s", state)
				}
				eventIDs <- event.TaskID
			})

			f.sendReport(t, protocol.VscreenInterventionAwaitingTakeover, 200)
			f.sendReport(t, protocol.VscreenInterventionHuman, 200)
			f.sendReport(t, protocol.VscreenInterventionReadyToContinue, 200)
			// When simultaneous human HTTP requests continue the same intervention.
			start := make(chan struct{})
			ids := make(chan string, 2)
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					var out struct {
						TaskID string `json:"task_id"`
					}
					testutil.Call(t, f.h.ContinueVscreenIntervention, f.continueRequest(map[string]any{"human_summary": "human completed local step", "fresh_session": false})).Want(200).JSON(&out)
					ids <- out.TaskID
				}()
			}
			close(start)
			wg.Wait()
			close(ids)
			// Then exactly one new linked task exists, the source remains terminal, and claim uses its exact session.
			var childID string
			for id := range ids {
				if childID != "" && childID != id {
					t.Fatalf("different tasks %s %s", childID, id)
				}
				childID = id
			}
			if len(eventIDs) != 1 {
				t.Fatalf("queued events=%d", len(eventIDs))
			}
			t.Log("task:queued emitted once after consumed intervention visible from independent DB connection")
			if n := dbfx.Count(t, "SELECT count(*) FROM agent_task_queue WHERE rerun_of_task_id=$1", f.report.SourceTaskID); n != 1 {
				t.Fatalf("continuation count=%d", n)
			}
			child, err := f.h.Queries.GetAgentTask(context.Background(), parseUUID(childID))
			if err != nil {
				t.Fatal(err)
			}
			resp := AgentTaskResponse{WorkspaceID: testWorkspaceID, PriorSessionID: "unrelated-latest-session"}
			if err := f.h.applyInterventionClaim(context.Background(), child, &resp); err != nil {
				t.Fatal(err)
			}
			rt, err := f.h.Queries.GetAgentRuntime(context.Background(), child.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			claimReq := newRequest(http.MethodPost, "/api/daemon/runtimes/"+f.report.RuntimeID+"/tasks/claim", nil)
			built, _, _, _, _, failure := f.h.buildClaimedTaskResponse(claimReq, &child, rt, f.report.RuntimeID, testWorkspaceID)
			if failure != nil {
				t.Fatalf("claim builder rejected: %+v", failure)
			}
			if built.PriorSessionID != "exact-source-session" {
				t.Fatalf("full claim session=%q", built.PriorSessionID)
			}

			if resp.PriorSessionID != "exact-source-session" || resp.HandoffNote != "human completed local step" {
				t.Fatalf("claim=%+v", resp)
			}
			source, err := f.h.Queries.GetAgentTask(context.Background(), parseUUID(f.report.SourceTaskID))
			if err != nil {
				t.Fatal(err)
			}
			if source.Status != "failed" {
				t.Fatalf("source status=%s", source.Status)
			}
			t.Logf("HTTP JSON {\"task_id\":%q}; DB continuation_count=1 source_status=failed exact_source_session=%q", childID, resp.PriorSessionID)
		})
	}
}

func TestVscreenInterventionResumeSafety(t *testing.T) {
	// Given the stop cause is canonical and does not poison saved provider history.
	// When resume safety is evaluated.
	// Then explicit intervention continuation may reuse the exact source.
	if service.ResumeUnsafeFailure("gui_human_intervention", "") {
		t.Fatal("intervention falsely poisons source")
	}
}
