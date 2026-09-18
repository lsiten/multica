//go:build darwin || linux

package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVscreenDesktopPrivateCredentialRotationAndCleanup(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := desktopVscreenCredential{Capability: strings.Repeat("a", 64)}
	cleanup, err := writeDesktopVscreenCredential(directory, old)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, ".desktop-vscreen", "credential.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential not private", err)
	}
	current := old
	current.Capability = strings.Repeat("b", 64)
	cleanupNew, err := writeDesktopVscreenCredential(directory, current)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), current.Capability) {
		t.Fatal("old daemon removed new credential")
	}
	cleanupNew()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("current exit failed to remove credential")
	}
	if err := os.Remove(filepath.Join(directory, ".desktop-vscreen")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(directory, ".desktop-vscreen")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeDesktopVscreenCredential(directory, old); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestVscreenDesktopHTTPRejectsForeignAndExpiredAuthority(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := desktopVscreenCredential{Capability: strings.Repeat("a", 64), Incarnation: "incarnation", Profile: "desktop-test"}
	verify := func(ctxCall context.Context, token string) bool {
		return ctx.Err() == nil && ctxCall.Err() == nil && subtle.ConstantTimeCompare([]byte(token), []byte(c.Capability)) == 1
	}
	handler := d.vscreenDesktopHandler(c, verify)
	for _, scenario := range []struct {
		name, token, incarnation, origin, runtime string
		want                                      int
	}{
		{"valid", c.Capability, c.Incarnation, "", "rt", 200}, {"wrong token", "foreign", c.Incarnation, "", "rt", 403}, {"old incarnation", c.Capability, "old", "", "rt", 403}, {"web origin", c.Capability, c.Incarnation, "https://fixture.invalid", "rt", 403}, {"foreign runtime", c.Capability, c.Incarnation, "", "foreign", 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			body := `{"action":"status","workspace_id":"ws","runtime_id":"` + scenario.runtime + `"}`
			r := httptest.NewRequest(http.MethodPost, "/vscreen/desktop", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+scenario.token)
			r.Header.Set("X-Multica-Profile", c.Profile)
			r.Header.Set("X-Vscreen-Incarnation", scenario.incarnation)
			r.Header.Set("Origin", scenario.origin)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != scenario.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), c.Capability) {
				t.Fatal("capability exposed")
			}
		})
	}
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/vscreen/desktop", strings.NewReader(`{"action":"status","workspace_id":"ws","runtime_id":"rt"}`))
	r.Header.Set("Authorization", "Bearer "+c.Capability)
	r.Header.Set("X-Multica-Profile", c.Profile)
	r.Header.Set("X-Vscreen-Incarnation", c.Incarnation)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("expired daemon capability accepted")
	}
}
func TestVscreenDesktopExclusionsPersistBeforeHostStarts(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	if err := d.updateVscreenExclusions(t.Context(), []uint32{41, 42}); err != nil {
		t.Fatal(err)
	}
	s := d.vscreenRuntime()
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(s.excludedWindows)
	if err != nil || string(b) != "[41,42]" || s.client != nil {
		t.Fatal("exclusion update launched host or lost registry")
	}
}

func TestVscreenDesktopContinuedStatusDoesNotWaitForConsumedReport(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	s := d.vscreenRuntime()
	s.interventions.records = map[string]*vscreenInterventionRecord{"rt": {Report: protocol.VscreenIntervention{InterventionID: "intervention", State: protocol.VscreenInterventionContinued}}}
	c := desktopVscreenCredential{Capability: "private", Profile: "desktop-test", Incarnation: "current"}
	handler := d.vscreenDesktopHandler(c, func(ctx context.Context, token string) bool { return token == c.Capability })
	r := httptest.NewRequest(http.MethodPost, "/vscreen/desktop", strings.NewReader(`{"action":"status","workspace_id":"ws","runtime_id":"rt"}`))
	r.Header.Set("Authorization", "Bearer private")
	r.Header.Set("X-Multica-Profile", c.Profile)
	r.Header.Set("X-Vscreen-Incarnation", c.Incarnation)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("continued permanently pending: %s", w.Body.String())
	}
	s.interventions.records["rt"].Report.State = protocol.VscreenInterventionAwaitingTakeover
	w = httptest.NewRecorder()
	r.Body = io.NopCloser(strings.NewReader(`{"action":"status","workspace_id":"ws","runtime_id":"rt"}`))
	handler.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "report_pending") {
		t.Fatalf("active unacknowledged stage accepted: %s", w.Body.String())
	}
}

func TestVscreenDesktopHTTPTransferNativeRoundtrip(t *testing.T) {
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
	_, broker, execution, err := d.startTaskVscreen(ctx, Task{ID: "source", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt"}, "codex", func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	defer broker.Close()
	if _, err = execution.acquire(ctx, vscreenToolArgs{RequestID: "request"}); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{"transaction_id": execution.lease.TransactionID, "bundle_id": "owned.fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = execution.invoke(ctx, "vscreen_launch_app", args); err != nil {
		t.Fatal(err)
	}
	execution.freeze(errVscreenIntervention)
	execution.Close()
	broker.Close()
	id := s.interventions.records["rt"].Report.InterventionID
	config, results := reportTestConfig(t)
	reporter := reportTestNew(t, config)
	d.vscreenReporter = reporter
	defer d.closeVscreenReporter()
	sender, reports := reportTestSender(t)
	binding := reporter.Bind("current", sender)
	if err = d.markVscreenInterventionStopped(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	awaiting := reportTestReceive(t, reports)
	credential := desktopVscreenCredential{Capability: strings.Repeat("a", 64), Profile: "desktop-fixture", Incarnation: "current"}
	verify := func(call context.Context, token string) bool {
		return call.Err() == nil && subtle.ConstantTimeCompare([]byte(token), []byte(credential.Capability)) == 1
	}
	d.SetVscreenLocalOwnerVerifier(verify)
	handler := d.vscreenDesktopHandler(credential, verify)
	call := func(action string, want int) {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"action": action, "workspace_id": "ws", "runtime_id": "rt", "intervention_id": id, "destination_source_id": "display:physical", "summary": "manual change"})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/vscreen/desktop", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+credential.Capability)
		r.Header.Set("X-Multica-Profile", credential.Profile)
		r.Header.Set("X-Vscreen-Incarnation", credential.Incarnation)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("action %s status %d: %s", action, w.Code, w.Body.String())
		}
	}
	call("takeover", 409)
	reportTestAck(reporter, binding, awaiting, 1, "")
	reportTestReceive(t, results)
	call("takeover", 200)
	human := reportTestReceive(t, reports)
	call("return", 409)
	reportTestAck(reporter, binding, human, 2, "")
	reportTestReceive(t, results)
	call("return", 200)
	ready := reportTestReceive(t, reports)
	if ready.State != protocol.VscreenInterventionReadyToContinue || ready.ReturnReceiptID == "" || !actor.Status().Frozen {
		t.Fatal("HTTP bridge fabricated return proof or resumed old input")
	}
}
