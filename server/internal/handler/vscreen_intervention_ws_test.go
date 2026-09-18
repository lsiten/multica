package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type interventionWSTest struct {
	*interventionFixture
	server *httptest.Server
	token  string
}

func newInterventionWSTest(t *testing.T) *interventionWSTest {
	t.Helper()
	f := newInterventionFixture(t, "issue")
	f.h.DaemonHub = daemonws.NewHub()
	f.h.DaemonHub.SetVscreenInterventionHandler(f.h.DaemonVscreenIntervention)
	router := chi.NewRouter()
	router.Use(middleware.DaemonAuth(f.h.Queries, nil, nil, nil))
	router.Get("/api/daemon/ws", f.h.DaemonWebSocket)
	router.Post("/api/daemon/runtimes/{runtimeId}/vscreen/interventions", f.h.ReportVscreenIntervention)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	token, _ := insertTestPAT(t, time.Now().Add(time.Hour))
	return &interventionWSTest{interventionFixture: f, server: server, token: token}
}

func (f *interventionWSTest) dial(t *testing.T) (*websocket.Conn, string) {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.server.URL, "http")+"/api/daemon/ws?runtime_id="+f.report.RuntimeID, http.Header{"Authorization": []string{"Bearer " + f.token}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	generation := response.Header.Get(protocol.DaemonGenerationHeader)
	if generation == "" {
		t.Fatal("missing authenticated generation")
	}
	return conn, generation
}

func (f *interventionWSTest) exchange(t *testing.T, conn *websocket.Conn, report protocol.VscreenIntervention, answer bool) protocol.VscreenInterventionAck {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenIntervention, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var frame protocol.Message
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		switch frame.Type {
		case protocol.EventVscreenQuery:
			if !answer {
				continue
			}
			var query protocol.VscreenQuery
			if err := json.Unmarshal(frame.Payload, &query); err != nil {
				t.Fatal(err)
			}
			snapshot := f.snapshot
			payload, err := json.Marshal(protocol.VscreenQueryResult{VscreenEnvelope: query.VscreenEnvelope, State: &snapshot})
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.WriteJSON(protocol.Message{Type: protocol.EventVscreenQueryResult, Payload: payload}); err != nil {
				t.Fatal(err)
			}
		case protocol.EventVscreenInterventionAck:
			var ack protocol.VscreenInterventionAck
			if err := json.Unmarshal(frame.Payload, &ack); err != nil {
				t.Fatal(err)
			}
			if ack.VscreenEnvelope != report.VscreenEnvelope || ack.InterventionID != report.InterventionID {
				t.Fatalf("ack correlation mismatch: %+v", ack)
			}
			return ack
		default:
			t.Fatalf("unexpected frame: %s", frame.Type)
		}
	}
}

func TestVscreenInterventionWSAuthenticatedPATAndReconnect(t *testing.T) {
	f := newInterventionWSTest(t)
	conn, generation := f.dial(t)
	f.report.DaemonGeneration = generation
	ack := f.exchange(t, conn, f.report, true)
	row, err := f.h.Queries.GetVscreenIntervention(context.Background(), db.GetVscreenInterventionParams{ID: parseUUID(f.report.InterventionID), WorkspaceID: parseUUID(f.report.WorkspaceID)})
	if err != nil || !ack.Accepted || ack.Version != row.Version {
		t.Fatalf("ack=%+v row=%+v err=%v", ack, row, err)
	}
	retry := f.exchange(t, conn, f.report, true)
	if !retry.Accepted || retry.Version != ack.Version {
		t.Fatalf("retry=%+v", retry)
	}
	next, nextGeneration := f.dial(t)
	stale := f.exchange(t, conn, f.report, false)
	if stale.Accepted || stale.Reason != "stale_generation" {
		t.Fatalf("old socket ack=%+v", stale)
	}
	f.report.DaemonGeneration = nextGeneration
	retry = f.exchange(t, next, f.report, true)
	if !retry.Accepted || retry.Version != ack.Version {
		t.Fatalf("reconnect retry=%+v", retry)
	}
	if count := dbfx.Count(t, "SELECT count(*) FROM runtime_vscreen_intervention WHERE source_task_id=$1", f.report.SourceTaskID); count != 1 {
		t.Fatalf("rows=%d", count)
	}
	for _, state := range []protocol.VscreenInterventionState{protocol.VscreenInterventionHuman, protocol.VscreenInterventionReadyToContinue} {
		f.report.State, f.report.RequestID = state, uuid.NewString()
		f.snapshot.InterventionState = state
		if state == protocol.VscreenInterventionHuman {
			f.snapshot.ControlState = protocol.VscreenControlHuman
		} else {
			f.snapshot.ControlState = protocol.VscreenControlIdle
			f.report.ReturnReceiptID = uuid.NewString()
			f.snapshot.ReturnReceiptID = f.report.ReturnReceiptID
		}
		advanced := f.exchange(t, next, f.report, true)
		if !advanced.Accepted || advanced.Version != retry.Version+1 {
			t.Fatalf("state=%s ack=%+v previous=%+v", state, advanced, retry)
		}
		retry = advanced
	}
	confirmed := f.exchange(t, next, f.report, true)
	if !confirmed.Accepted || confirmed.Version != retry.Version {
		t.Fatalf("ready proof retry=%+v", confirmed)
	}
	req := testutil.JSONRequest(http.MethodPost, "/api/daemon/runtimes/"+f.report.RuntimeID+"/vscreen/interventions", f.report)
	req.Header.Set("Authorization", "Bearer "+f.token)
	testutil.Call(t, f.server.Config.Handler.ServeHTTP, req).Want(http.StatusForbidden)
	t.Log("PAT-authenticated actual WS: query/result processed during report, commit before ack, exact DB version; duplicate and reconnect same request ID one row; old socket fenced; PAT HTTP remains forbidden")
}

func TestVscreenInterventionWSRejections(t *testing.T) {
	for _, scenario := range []string{"foreign_runtime", "foreign_workspace", "foreign_daemon", "non_owner_pat", "not_stopped", "native_mismatch", "query_timeout"} {
		t.Run(scenario, func(t *testing.T) {
			f := newInterventionWSTest(t)
			conn, generation := f.dial(t)
			report := f.report
			report.DaemonGeneration = generation
			expected := "permission_denied"
			answer := true
			switch scenario {
			case "foreign_runtime":
				report.RuntimeID = uuid.NewString()
			case "foreign_workspace":
				report.WorkspaceID = uuid.NewString()
			case "foreign_daemon":
				dbfx.Exec(t, "UPDATE agent_runtime SET daemon_id=$2 WHERE id=$1", report.RuntimeID, uuid.NewString())
			case "non_owner_pat":
				dbfx.Exec(t, "UPDATE agent_runtime SET owner_id=$2 WHERE id=$1", report.RuntimeID, dbfx.User(t, "another owner", uuid.NewString()+"@example.test"))
			case "not_stopped":
				dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',completed_at=NULL WHERE id=$1", report.SourceTaskID)
				expected = "source_not_stopped"
			case "native_mismatch":
				f.snapshot.NativeEpoch = "another-native"
				expected = "stale_generation"
			case "query_timeout":
				answer = false
				expected = "daemon_timeout"
			}
			ack := f.exchange(t, conn, report, answer)
			if ack.Accepted || ack.Reason != expected || ack.Version != 0 {
				t.Fatalf("ack=%+v expected=%s", ack, expected)
			}
			if count := dbfx.Count(t, "SELECT count(*) FROM runtime_vscreen_intervention WHERE source_task_id=$1", report.SourceTaskID); count != 0 {
				t.Fatalf("rows=%d", count)
			}
			t.Logf("actual WS rejection %s: reason=%s zero rows", scenario, ack.Reason)
		})
	}
}
