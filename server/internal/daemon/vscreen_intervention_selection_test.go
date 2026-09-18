//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/vscreen/native/appcontrol"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenPrivateEmptyWindowSelectionAdoptionAndReturn(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "adopt-and-return", true: "expired-candidate-no-record"}[failure], func(t *testing.T) {
			if failure {
				t.Setenv("VSCREEN_FIXTURE_ADOPT_ERROR", "expired")
			}
			d := vscreenFixtureDaemon(t)
			ctx := t.Context()
			key, err := d.vscreenResource("ws", "rt")
			if err != nil {
				t.Fatal(err)
			}
			s := d.vscreenRuntime()
			s.mu.Lock()
			err = d.startVscreenHost(ctx, s)
			s.enabled[key] = true
			s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			actor, err := s.manager.For(key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = actor.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			_, broker, execution, err := d.startTaskVscreen(ctx, Task{ID: "empty-source", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt"}, "codex", func(error) {})
			if err != nil {
				t.Fatal(err)
			}
			defer execution.Close()
			defer broker.Close()
			if _, err = execution.acquire(ctx, vscreenToolArgs{RequestID: "request"}); err != nil {
				t.Fatal(err)
			}
			// The launch never produced an owned window; the source still stops through production fencing.
			execution.freeze(errVscreenIntervention)
			execution.Close()
			broker.Close()
			record := s.interventions.records["rt"]
			id := record.Report.InterventionID
			config, results := reportTestConfig(t)
			reporter := reportTestNew(t, config)
			d.vscreenReporter = reporter
			defer d.closeVscreenReporter()
			sender, reports := reportTestSender(t)
			binding := reporter.Bind("current", sender)
			credential := desktopVscreenCredential{Capability: "private-owner", Profile: "desktop-test", Incarnation: "current"}
			verify := func(call context.Context, token string) bool {
				return call.Err() == nil && token == credential.Capability
			}
			d.SetVscreenLocalOwnerVerifier(verify)
			handler := d.vscreenDesktopHandler(credential, verify)
			call := func(action, intervention, handle string, want int) map[string]any {
				t.Helper()
				raw, _ := json.Marshal(map[string]string{"action": action, "workspace_id": "ws", "runtime_id": "rt", "intervention_id": intervention, "window_handle": handle})
				r := httptest.NewRequest(http.MethodPost, "/vscreen/desktop", strings.NewReader(string(raw)))
				r.Header.Set("Authorization", "Bearer "+credential.Capability)
				r.Header.Set("X-Multica-Profile", credential.Profile)
				r.Header.Set("X-Vscreen-Incarnation", credential.Incarnation)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s status %d: %s", action, w.Code, w.Body.String())
				}
				var out map[string]any
				if json.Unmarshal(w.Body.Bytes(), &out) != nil {
					t.Fatal("invalid local response")
				}
				return out
			}
			call("list_windows", id, "", 409)
			if err = d.markVscreenInterventionStopped(ctx, "empty-source"); err != nil {
				t.Fatal(err)
			}
			awaiting := reportTestReceive(t, reports)
			if call("list_windows", id, "", 409)["reason"] != "report_pending" {
				t.Fatal("list before ACK")
			}
			reportTestAck(reporter, binding, awaiting, 1, "")
			reportTestReceive(t, results)
			if _, err = d.selectVscreenWindow(ctx, "forged", "ws", "rt", id, "", false); err == nil {
				t.Fatal("wrong owner listed private windows")
			}
			call("list_windows", "old-intervention", "", 409)
			status := call("status", id, "", 200)
			if status["selection_required"] != true {
				t.Fatal("empty launch is still trapped behind takeover")
			}
			listed := call("list_windows", id, "", 200)
			raw, _ := json.Marshal(listed["candidates"])
			var candidates appcontrol.WindowCandidates
			if json.Unmarshal(raw, &candidates) != nil || len(candidates.Windows) != 1 {
				t.Fatal("missing private candidates")
			}
			if len(record.Windows) != 0 || record.Report.State != protocol.VscreenInterventionAwaitingTakeover {
				t.Fatal("listing adopted automatically")
			}
			if failure {
				if call("adopt_window", id, candidates.Windows[0].Handle, 409)["reason"] != "selection_expired" {
					t.Fatal("expiry not actionable")
				}
				if len(record.Windows) != 0 || record.Report.State != protocol.VscreenInterventionAwaitingTakeover {
					t.Fatal("failed adoption recorded a window")
				}
				return
			}
			call("adopt_window", id, candidates.Windows[0].Handle, 200)
			human := reportTestReceive(t, reports)
			if human.State != protocol.VscreenInterventionHuman || len(record.Windows) != 1 || record.Windows[0] != candidates.Windows[0].Handle {
				t.Fatal("successful handle not registered as human")
			}
			reportJSON, _ := json.Marshal(human)
			persisted, err := os.ReadFile(d.cfg.NativeVscreenPreferencesPath + ".intervention-state")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(reportJSON), "Private document") || strings.Contains(string(persisted), "Private document") || strings.Contains(string(persisted), "org.example.Editor") {
				t.Fatal("candidate metadata persisted or reported")
			}
			call("adopt_window", id, candidates.Windows[0].Handle, 409)
			if !actor.Status().Frozen || s.client.Renew(ctx, record.Authority, time.Second) == nil {
				t.Fatal("adoption thawed old lease")
			}
			call("return", id, "", 409)
			reportTestAck(reporter, binding, human, 2, "")
			reportTestReceive(t, results)
			call("return", id, "", 200)
			ready := reportTestReceive(t, reports)
			if ready.State != protocol.VscreenInterventionReadyToContinue || ready.ReturnReceiptID == "" || !actor.Status().Frozen {
				t.Fatal("missing real return proof or old lease resumed")
			}
			reportTestAck(reporter, binding, ready, 3, "")
			reportTestReceive(t, results)
			if call("status", id, "", 200)["selection_required"] == true {
				t.Fatal("returned window reopened selector")
			}
			continuation := Task{ID: "continued-selection", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt", VscreenContinuation: &protocol.VscreenContinuationContext{InterventionID: id, SourceTaskID: "empty-source", ReturnReceiptID: ready.ReturnReceiptID, Epoch: ready.Epoch}}
			_, nextBroker, next, err := d.startTaskVscreen(ctx, continuation, "codex", func(error) {})
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			defer nextBroker.Close()
			acquired, err := next.acquire(ctx, vscreenToolArgs{RequestID: "continued-acquire"})
			if err != nil {
				t.Fatal(err)
			}
			directory := managedReply(t, acquired)["managed_windows"].([]any)
			if len(directory) != 1 || directory[0].(map[string]any)["bundle_id"] != "org.example.Editor" {
				t.Fatal("continuation cannot discover returned adopted app")
			}
			discovered := directory[0].(map[string]any)["window_handle"].(string)
			if len(s.interventions.windows[continuation.ID]) != 0 {
				t.Fatal("catalog marked every app as used")
			}
			typed := map[string]any{"transaction_id": next.lease.TransactionID, "window_handle": discovered, "snapshot_revision": 1, "action_id": "continued-type", "sequence": 1, "action": protocol.VscreenAction{Kind: protocol.VscreenActionType, Type: &protocol.VscreenTypeAction{ElementHandle: "entry", Text: "continued"}}}
			if _, err := managedInvoke(t, next, "vscreen_type", typed); err == nil {
				t.Fatal("continued task used return-stage snapshot")
			}
			observed, err := managedInvoke(t, next, "vscreen_observe", map[string]any{"transaction_id": next.lease.TransactionID, "window_handle": discovered})
			if err != nil {
				t.Fatal(err)
			}
			typed["snapshot_revision"] = managedReply(t, observed)["snapshot_revision"]
			if _, err := managedInvoke(t, next, "vscreen_type", typed); err != nil {
				t.Fatal(err)
			}

		})
	}
}
