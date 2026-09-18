//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/pkg/agent"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVscreenMCPActualNativeWire(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	cfg, broker, execution, err := d.startTaskVscreen(ctx, Task{ID: "wire-task", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt"}, "codex", func(error) { t.Error("unexpected provider stop") })
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	defer broker.Close()
	provider := vscreenWireProvider{call: func(config json.RawMessage) {
		url := mcpFixtureURL(t, config)
		lease := mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_acquire", map[string]any{"request_id": "wire-ticket"}))
		tx := lease["transaction_id"]
		before := mcpFixtureCall(t, url, "vscreen_launch_app", map[string]any{"transaction_id": tx, "bundle_id": "owned.fixture"})
		meta := mcpFixtureText(t, before)
		mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_type", map[string]any{"transaction_id": tx, "window_handle": "wire-window", "snapshot_revision": meta["snapshot_revision"], "action_id": "write", "sequence": 1, "action": map[string]any{"kind": "type", "type": map[string]any{"element_handle": "entry", "text": "wire 你好"}}}))
		after := mcpFixtureCall(t, url, "vscreen_observe", map[string]any{"transaction_id": tx, "window_handle": "wire-window"})
		next := mcpFixtureText(t, after)
		if next["elements"].([]any)[0].(map[string]any)["Value"] != "wire 你好" {
			t.Fatal("native FD6 did not apply text")
		}
		if before["content"].([]any)[1].(map[string]any)["data"] == after["content"].([]any)[1].(map[string]any)["data"] {
			t.Fatal("FD5 verified PNG did not change")
		}
		mcpFixtureText(t, mcpFixtureCall(t, url, "vscreen_release", map[string]any{"transaction_id": tx}))
		if actor.Status().Lease.TaskID != "" {
			t.Fatal("native transaction not released")
		}
	}}
	merged, err := mergeVscreenMCP(json.RawMessage(`{"mcpServers":{"existing":{"command":"test-owned-unused"}}}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := d.executeAndDrain(ctx, provider, "owned fixture", agent.ExecOptions{McpConfig: merged}, slog.Default(), "wire-task", "", new(atomic.Int32))
	if err != nil || result.Status != "completed" {
		t.Fatalf("fake provider config execution: %+v %v", result, err)
	}

}
func TestVscreenPermissionsProbeDoesNotCreateDisplay(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	state, err := d.vscreenSnapshot(context.Background(), "ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	if state.State != protocol.VscreenStateDisabled || state.Permissions.Accessibility != "denied" || state.Permissions.ScreenRecording != "denied" {
		t.Fatalf("nonprompt fixture permissions: %+v", state)
	}
	actor, err := d.VscreenActor("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	if actor.Status().Ready || actor.Status().Display.DisplayID != 0 {
		t.Fatal("permission preflight created display")
	}
}
func TestVscreenMCPNamespacedActionSchemas(t *testing.T) {
	for _, tool := range vscreenToolDescriptors() {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 {
			t.Fatal("empty tool descriptor")
		}
	}
}

func TestVscreenLocalHandoffNativeWireAndReportGate(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	ctx := context.Background()
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
	args, _ := json.Marshal(map[string]any{"transaction_id": execution.lease.TransactionID, "bundle_id": "owned.fixture"})
	if _, err = execution.invoke(ctx, "vscreen_launch_app", args); err != nil {
		t.Fatal(err)
	}
	execution.freeze(errVscreenIntervention)
	execution.Close()
	broker.Close()
	s.interventions.mu.Lock()
	id := s.interventions.records["rt"].Report.InterventionID
	s.interventions.mu.Unlock()
	config, results := reportTestConfig(t)
	config.AckTimeout = 2 * time.Second
	reporter := reportTestNew(t, config)
	d.vscreenReporter = reporter
	defer d.closeVscreenReporter()
	sender, reports := reportTestSender(t)
	binding := reporter.Bind("current", sender)
	// This test begins at the producer's confirmed terminal boundary; provider drain has a separate canonical test.
	if err = d.markVscreenInterventionStopped(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	awaiting := reportTestReceive(t, reports)
	d.SetVscreenLocalOwnerVerifier(func(_ context.Context, token string) bool { return token == "owned-main-process" })
	if err = d.TakeOverVscreenLocally(ctx, "forged", "ws", "rt", id, "display:physical"); err == nil {
		t.Fatal("forged local owner accepted")
	}
	if err = d.TakeOverVscreenLocally(ctx, "owned-main-process", "ws", "rt", id, "display:physical"); err == nil || err.Error() != "report_pending" {
		t.Fatalf("unacknowledged awaiting state advanced: %v", err)
	}
	reportTestAck(reporter, binding, awaiting, 1, "")
	reportTestReceive(t, results)
	if err = d.TakeOverVscreenLocally(ctx, "owned-main-process", "ws", "rt", id, "display:physical"); err != nil {
		t.Fatal(err)
	}
	human := reportTestReceive(t, reports)
	if err = d.ReturnVscreenLocally(ctx, "owned-main-process", "ws", "rt", id, "manual change"); err == nil || err.Error() != "report_pending" {
		t.Fatalf("unacknowledged human state advanced: %v", err)
	}
	reportTestAck(reporter, binding, human, 2, "")
	reportTestReceive(t, results)
	if err = d.ReturnVscreenLocally(ctx, "owned-main-process", "ws", "rt", id, "manual change"); err != nil {
		t.Fatal(err)
	}
	ready := reportTestReceive(t, reports)
	if ready.State != protocol.VscreenInterventionReadyToContinue || ready.ReturnReceiptID == "" || ready.HumanSummary != "" {
		t.Fatalf("invalid native return report: %+v", ready)
	}
	s.interventions.mu.Lock()
	summary := s.interventions.records["rt"].Report.HumanSummary
	s.interventions.mu.Unlock()
	if summary != "manual change" {
		t.Fatal("local human summary lost")
	}
	if !actor.Status().Frozen {
		t.Fatal("return proof unexpectedly restored AI input")
	}
}

type vscreenWireProvider struct{ call func(json.RawMessage) }

func (p vscreenWireProvider) Execute(ctx context.Context, _ string, options agent.ExecOptions) (*agent.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.call(options.McpConfig)
	messages := make(chan agent.Message)
	close(messages)
	result := make(chan agent.Result, 1)
	result <- agent.Result{Status: "completed"}
	close(result)
	return &agent.Session{Messages: messages, Result: result}, nil
}

func TestVscreenMCPUnsupportedProviderDoesNotStartNative(t *testing.T) {
	d := vscreenFixtureDaemon(t)
	key, err := d.vscreenResource("ws", "rt")
	if err != nil {
		t.Fatal(err)
	}
	s := d.vscreenRuntime()
	s.enabled[key] = true
	cfg, broker, execution, err := d.startTaskVscreen(context.Background(), Task{ID: "task", WorkspaceID: "ws", RuntimeID: "rt"}, "unsupported-fixture", nil)
	if err != errVscreenProviderUnavailable || len(cfg) != 0 || broker != nil || execution != nil || s.client != nil {
		t.Fatal("unsupported provider received managed tools or started native")
	}
}
