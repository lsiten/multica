//go:build darwin || linux

package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/multica-ai/multica/server/pkg/agent"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/mirror"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func mirrorProcessFixture(t *testing.T) (*Daemon, *mirrorProcessClient, runtimeproc.Bootstrap) {
	return mirrorProcessFixtureEnv(t, nil)
}
func mirrorProcessFixtureEnv(t *testing.T, extra map[string]string) (*Daemon, *mirrorProcessClient, runtimeproc.Bootstrap) {
	t.Helper()
	profile, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{ProcessServices: []string{"mirror"}, ServerBaseURL: "https://example.com", DaemonID: "daemon", NativeHostBuild: "fixture/commit", NativeVscreenPreferencesPath: filepath.Join(profile, "vscreen-enabled.json")}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), workspaces: map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt", "other"}}}, runtimeIndex: map[string]Runtime{"rt": {ID: "rt"}, "other": {ID: "other"}}, runtimeMirrors: map[string]*mirror.RuntimeMirror{}}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "https://example.com", Account: "fixture-user", Profile: "", DaemonID: "daemon", Service: "mirror"}, "fixture/commit")
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := runtimeproc.NewBootstrap(filepath.Join(profile, "mirror-service"), identity)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	environment := map[string]string{"MIRROR_FIXTURE_NATIVE_PID": filepath.Join(profile, "native.pid")}
	for key, value := range extra {
		environment[key] = value
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{Executable: executable, SHA256: hex.EncodeToString(digest[:]), Bootstrap: bootstrap, Environment: environment, StartupTimeout: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client, err := newMirrorProcessClient(d, process, bootstrap)
	if err != nil {
		process.Close()
		t.Fatal(err)
	}
	d.mirrorProcess = client
	d.vscreenReporter, err = newVscreenReporter(vscreenReportConfig{Path: filepath.Join(profile, "reports", "reports.json"), BackendIdentity: identity.Scope.Backend, AccountID: identity.Scope.Account})
	if err != nil {
		client.close()
		t.Fatal(err)
	}
	d.vscreenServerGeneration = "server"
	d.mirrorControlGeneration = 1
	resources, err := d.mirrorRoster()
	if err != nil {
		t.Fatal(err)
	}
	if err = client.bind(t.Context(), mirrorControlBinding{Generation: 1, ServerGeneration: "server", Resources: resources}, func(frame []byte) (*wsOutbound, error) { return &wsOutbound{data: frame, sent: true}, nil }); err != nil {
		client.close()
		t.Fatal(err)
	}
	t.Cleanup(func() { d.closeRuntimeMirrors(); d.closeVscreens(); d.closeVscreenReporter() })
	return d, client, bootstrap
}
func waitMirrorProcess(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("mirror process state did not converge")
}
func enableProcessScreen(t *testing.T, d *Daemon) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", RequestID: "enable", DaemonGeneration: "server"}, CommandID: "enable", Kind: protocol.VscreenCommandEnable}
	if err := d.executeVscreenCommand(ctx, command, 1); err != nil {
		t.Fatal(err)
	}
}
func processScreenTask() Task {
	return Task{ID: "task", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt", DispatchedAt: "2026-10-08T00:00:00.123456Z"}
}
func TestMirrorProcessOwnsNativeActorAndAuthenticatedGUI(t *testing.T) {
	t.Setenv("MULTICA_TOKEN", "must-not-be-inherited")
	d, client, _ := mirrorProcessFixture(t)
	state, err := d.vscreenSnapshot(t.Context(), "ws", "rt")
	if err != nil || state.State != protocol.VscreenStateDisabled {
		t.Fatalf("query %+v %v", state, err)
	}
	if state.Permissions.ScreenRecording != "denied" {
		t.Fatal("fixture permission query did not deny capture")
	}
	if d.vscreen != nil || d.inputArbiter != nil || d.globalInjector() != nil {
		t.Fatal("control process created GUI authority")
	}
	if _, err = d.VscreenActor("ws", "rt"); err == nil {
		t.Fatal("parent exposed child actor")
	}
	enableProcessScreen(t, d)
	state, err = d.vscreenSnapshot(t.Context(), "ws", "rt")
	if err != nil || state.State != protocol.VscreenStateReady {
		t.Fatalf("native actor state %+v %v", state, err)
	}
	var stopped atomic.Int32
	_, broker, execution, err := d.startTaskVscreen(t.Context(), processScreenTask(), "codex", func(error) { stopped.Add(1) })
	if err != nil || broker == nil || execution == nil {
		t.Fatalf("GUI acquisition failed: %v", err)
	}
	content, err := execution.invoke(t.Context(), "vscreen_acquire", json.RawMessage(`{"request_id":"acquire","intent":"fixture"}`))
	if err != nil || len(content) == 0 {
		t.Fatalf("actual native wire acquire %v %v", content, err)
	}
	client.mu.Lock()
	dependent := client.executions[execution.remote.id].dependent
	client.mu.Unlock()
	if !dependent {
		t.Fatal("native action ran before parent GUI dependency acknowledged")
	}
	raw, err := client.process.Client.Read(t.Context(), "mirror.inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		PID        int  `json:"pid"`
		Native     bool `json:"native_alive"`
		Executions int  `json:"executions"`
	}
	if json.Unmarshal(raw, &inventory) != nil || inventory.PID == os.Getpid() || !inventory.Native || inventory.Executions != 1 {
		t.Fatalf("physical ownership: %s", raw)
	}
	broker.Close()
	execution.Close()
	if stopped.Load() != 0 {
		t.Fatal("normal release incorrectly stopped provider")
	}
}
func TestMirrorProcessFullClaimAndGrantValidation(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	task := processScreenTask()
	_, broker, execution, err := d.startTaskVscreen(t.Context(), task, "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	defer execution.Close()
	for _, field := range []string{"workspace", "runtime", "agent", "claim", "source", "continuation"} {
		bad := execution.remote.claim
		switch field {
		case "workspace":
			bad.WorkspaceID = "other"
		case "runtime":
			bad.RuntimeID = "other"
		case "agent":
			bad.AgentID = "other"
		case "claim":
			bad.DispatchedAt = "2026-10-08T00:00:00Z"
		case "source":
			bad.Source = &protocol.MirrorSourceBinding{}
		case "continuation":
			bad.Continuation = &protocol.VscreenContinuationContext{SourceTaskID: "other"}
		}
		if err = client.call(t.Context(), mirrorProcessRequest{Operation: "release", ExecutionID: execution.remote.id, Claim: &bad}, nil); err == nil {
			t.Fatalf("misbound %s claim released GUI execution", field)
		}
	}
	if _, err = execution.invoke(t.Context(), "vscreen_status", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("rejected claim poisoned healthy service: %v", err)
	}
	valid := mirrorExecutionGrant{Active: true, InstanceID: client.identity.InstanceID, Claim: execution.remote.claim, ExecutionID: execution.remote.id, MCP: bytes.Clone(execution.remote.config)}
	for _, field := range []string{"instance", "id", "claim", "url", "credential", "server"} {
		bad := valid
		bad.MCP = bytes.Clone(valid.MCP)
		switch field {
		case "instance":
			bad.InstanceID = "old"
		case "id":
			bad.ExecutionID = "guess"
		case "claim":
			bad.Claim.RuntimeID = "other"
		default:
			var doc map[string]any
			json.Unmarshal(bad.MCP, &doc)
			servers := doc["mcpServers"].(map[string]any)
			entry := servers[vscreenMCPName].(map[string]any)
			switch field {
			case "url":
				entry["url"] = "https://example.invalid/stolen"
			case "credential":
				entry["headers"] = map[string]string{"Authorization": "Bearer invalid", "X-Other": "unscoped"}
			case "server":
				servers["unexpected"] = entry
			}
			bad.MCP = marshalRaw(doc)
		}
		if err = client.validateExecutionGrant(bad, execution.remote.claim); err == nil {
			t.Fatalf("invalid %s grant accepted", field)
		}
	}
}
func TestMirrorProcessGUIActivationMustPrecedeNativeGrant(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	_, broker, execution, err := d.startTaskVscreen(t.Context(), processScreenTask(), "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	defer execution.Close()
	client.mu.Lock()
	delete(client.executions, execution.remote.id)
	client.mu.Unlock()
	if _, err = execution.invoke(t.Context(), "vscreen_acquire", json.RawMessage(`{"request_id":"acquire"}`)); err == nil {
		t.Fatal("native grant proceeded without parent dependency acknowledgement")
	}
	state, err := d.vscreenSnapshot(t.Context(), "ws", "rt")
	if err != nil || state.ActiveTaskID != nil {
		t.Fatalf("native authority created despite missing ACK: %+v %v", state, err)
	}
	client.mu.Lock()
	client.executions[execution.remote.id] = execution.remote
	client.mu.Unlock()
}
func TestMirrorProcessCrashStopsGUIButNotStatusOnlyTask(t *testing.T) {
	d, client, b := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	var guiStopped, codeStopped atomic.Int32
	gui := processScreenTask()
	_, guiBroker, guiExec, err := d.startTaskVscreen(t.Context(), gui, "codex", func(error) { guiStopped.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guiExec.invoke(t.Context(), "vscreen_acquire", json.RawMessage(`{"request_id":"acquire"}`)); err != nil {
		t.Fatal(err)
	}
	code := processScreenTask()
	code.ID = "status-only"
	_, codeBroker, codeExec, err := d.startTaskVscreen(t.Context(), code, "codex", func(error) { codeStopped.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = codeExec.invoke(t.Context(), "vscreen_status", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct{ name, body string }{{"unknown_tool", `{}`}, {"vscreen_acquire", `{"request_id":""}`}, {"vscreen_acquire", `{"unknown":true}`}} {
		if _, err = codeExec.invoke(t.Context(), call.name, json.RawMessage(call.body)); err == nil {
			t.Fatal("invalid GUI invocation accepted")
		}
	}
	client.mu.Lock()
	depends := client.executions[codeExec.remote.id].dependent
	client.mu.Unlock()
	if depends {
		t.Fatal("readonly/invalid GUI probes changed provider dependency")
	}
	if err = client.process.Close(); err != nil {
		t.Fatal(err)
	}
	waitMirrorProcess(t, func() bool { return guiStopped.Load() > 0 })
	if codeStopped.Load() != 0 {
		t.Fatal("mirror failure canceled pure-code provider")
	}
	// This test deliberately crashed and reaped the child; there is no clean stop claim.
	client.closeOnce.Do(func() { client.cancel(); <-client.done })
	guiBroker.Close()
	guiExec.Close()
	codeBroker.Close()
	codeExec.Close()
	nativePID, err := os.ReadFile(filepath.Join(filepath.Dir(b.Root), "native.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(nativePID))
	if err != nil {
		t.Fatal(err)
	}
	waitMirrorProcess(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
	if err = reconcileStoppedMirror(t.Context(), b.Root, b.Identity.Scope); err == nil {
		t.Fatal("crashed mirror owner replaced without cleanup proof")
	}
}
func TestMirrorOutboundCancellationFencesActualQueuedFrame(t *testing.T) {
	id, _ := runtimeproc.NewIdentity(runtimeproc.Scope{Backend: "https://example.com", Account: "fixture", Profile: "", DaemonID: "daemon", Service: "mirror"}, "fixture")
	actual := &wsOutbound{data: []byte("first")}
	owner := &Daemon{cfg: Config{ServerBaseURL: "https://example.com", DaemonID: "daemon"}, workspaces: map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt"}}}, runtimeIndex: map[string]Runtime{"rt": {ID: "rt"}}}
	client := &mirrorProcessClient{daemon: owner, identity: id, generation: 1, outbound: map[string]mirrorParentOutbound{}, enqueue: func([]byte) (*wsOutbound, error) { return actual, nil }}
	event := mirrorBridgeEvent{InstanceID: id.InstanceID, ID: "first-event", Generation: 1, Kind: "outbound", Payload: marshalRaw(protocol.Message{Type: protocol.EventMirrorViewer, Payload: marshalRaw(protocol.MirrorViewerPayload{WorkspaceID: "ws", RuntimeID: "rt", DaemonID: "daemon"})}), Deadline: time.Now().Add(time.Second)}
	if reply := client.handleEvent(t.Context(), event); reply.Error != "" {
		t.Fatal(reply.Error)
	}
	cancelEvent := mirrorBridgeEvent{InstanceID: id.InstanceID, ID: "cancel", Generation: 2, Kind: "cancel_outbound", Payload: marshalRaw(map[string]string{"id": "first-event"}), Deadline: time.Now().Add(time.Second)}
	if reply := client.handleEvent(t.Context(), cancelEvent); reply.Error == "" {
		t.Fatal("cross-generation cancel accepted")
	}
	if actual.canceled {
		t.Fatal("stale cancel changed parent queue")
	}
	cancelEvent.Generation = 1
	reply := client.handleEvent(t.Context(), cancelEvent)
	var outcome struct {
		Cancelled bool `json:"cancelled"`
	}
	json.Unmarshal(reply.Result, &outcome)
	if !outcome.Cancelled || actual.beginWrite() {
		t.Fatal("child cancellation did not cancel actual queued outbound")
	}
	replacement := &wsOutbound{data: []byte("new")}
	client.generation = 2
	client.enqueue = func([]byte) (*wsOutbound, error) { return replacement, nil }
	event.ID = "replacement"
	event.Generation = 2
	client.handleEvent(t.Context(), event)
	cancelEvent.Payload = marshalRaw(map[string]string{"id": "replacement"})
	client.handleEvent(t.Context(), cancelEvent)
	if replacement.canceled {
		t.Fatal("old binding canceled replacement frame")
	}
}
func TestMirrorEventBridgeRejectsStaleRepliesAndBoundsQueue(t *testing.T) {
	bridge, err := newMirrorEventBridge(strings.Repeat("a", 64), "instance", func(uint64, bool) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.close()
	bridge.setGeneration(1, true)
	send := func(path string, payload any) int {
		t.Helper()
		req, _ := http.NewRequest("POST", bridge.address+path, bytes.NewReader(marshalRaw(payload)))
		req.Header.Set("Authorization", "Bearer "+bridge.token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := bridge.emit(t.Context(), 1, "outbound", map[string]string{"fixture": "old"})
		done <- err
	}()
	waitMirrorProcess(t, func() bool { bridge.mu.Lock(); defer bridge.mu.Unlock(); return len(bridge.waiters) == 1 })
	bridge.mu.Lock()
	var eventID string
	for id := range bridge.waiters {
		eventID = id
	}
	bridge.mu.Unlock()
	bridge.setGeneration(2, true)
	if code := send("/reply", mirrorBridgeReply{InstanceID: "instance", ID: eventID, Generation: 1}); code != 409 {
		t.Fatalf("stale reply %d", code)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("retired event reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("retired waiter not released")
	}
	if code := send("/events", mirrorBridgePoll{InstanceID: "instance", Generation: 2, Renew: true}); code != 204 {
		t.Fatalf("stale queued event crossed replacement binding: %d", code)
	}
	var workers sync.WaitGroup
	for range mirrorEventLimit {
		workers.Add(1)
		go func() { defer workers.Done(); _, _, _ = bridge.emit(context.Background(), 2, "outbound", struct{}{}) }()
	}
	waitMirrorProcess(t, func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return len(bridge.waiters) == mirrorEventLimit
	})
	if _, _, err = bridge.emit(t.Context(), 2, "outbound", struct{}{}); err == nil {
		t.Fatal("event queue overflow admitted")
	}
	bridge.close()
	workers.Wait()
}
func TestMirrorProcessProviderStopAndReportACKGateHandoff(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var requested atomic.Bool
	task := processScreenTask()
	_, broker, execution, err := d.startTaskVscreen(ctx, task, "codex", func(error) { requested.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	content, err := execution.invoke(ctx, "vscreen_acquire", json.RawMessage(`{"request_id":"gui"}`))
	if err != nil {
		t.Fatal(err)
	}
	var lease struct {
		TransactionID string `json:"transaction_id"`
	}
	json.Unmarshal([]byte(content[0]["text"].(string)), &lease)
	if _, err = execution.invoke(ctx, "vscreen_launch_app", marshalRaw(map[string]string{"transaction_id": lease.TransactionID, "bundle_id": "owned.fixture"})); err != nil {
		t.Fatal(err)
	}
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "takeover"}, CommandID: "takeover", Kind: protocol.VscreenCommandRequestTakeover}
	if err = d.executeVscreenCommand(ctx, command, 1); err != nil {
		t.Fatal(err)
	}
	if !requested.Load() {
		t.Fatal("provider stop was not requested")
	}
	state, err := d.vscreenSnapshot(ctx, "ws", "rt")
	if err != nil || state.ControlState != protocol.VscreenControlStopping || state.InterventionID == nil {
		t.Fatalf("RPC acceptance falsely meant provider stopped: %+v %v", state, err)
	}
	id := *state.InterventionID
	d.SetVscreenLocalOwnerVerifier(func(_ context.Context, token string) bool { return token == "local-owner" })
	if err = d.TakeOverVscreenLocally(ctx, "local-owner", "ws", "rt", id, "display:physical"); err == nil {
		t.Fatal("takeover preceded actual provider stop")
	}
	sender, reports := reportTestSender(t)
	binding := d.vscreenReporter.Bind("server", sender)
	// The test controller now confirms the provider has exited and transcript drain finished.
	broker.Close()
	execution.Close()
	if err = d.markVscreenInterventionStopped(context.WithValue(ctx, mirrorReportedClaimKey{}, execution.remote.claim), task.ID); err != nil {
		t.Fatal(err)
	}
	awaiting := reportTestReceive(t, reports)
	if err = d.TakeOverVscreenLocally(ctx, "local-owner", "ws", "rt", id, "display:physical"); err == nil || !strings.Contains(err.Error(), "report_pending") {
		t.Fatalf("missing durable ACK gate: %v", err)
	}
	if !reportTestAck(d.vscreenReporter, binding, awaiting, 1, "") {
		t.Fatal("server ACK rejected")
	}
	waitMirrorProcess(t, func() bool { return d.vscreenReporter.Acknowledged(id, protocol.VscreenInterventionAwaitingTakeover) })
	if err = d.TakeOverVscreenLocally(ctx, "forged", "ws", "rt", id, "display:physical"); err == nil {
		t.Fatal("forged local owner accepted")
	}
	if err = d.TakeOverVscreenLocally(ctx, "local-owner", "ws", "rt", id, "display:physical"); err != nil {
		t.Fatal(err)
	}
	human := reportTestReceive(t, reports)
	if err = d.ReturnVscreenLocally(ctx, "local-owner", "ws", "rt", id, "fixture correction"); err == nil || !strings.Contains(err.Error(), "report_pending") {
		t.Fatal("return bypassed human-state ACK")
	}
	reportTestAck(d.vscreenReporter, binding, human, 2, "")
	waitMirrorProcess(t, func() bool { return d.vscreenReporter.Acknowledged(id, protocol.VscreenInterventionHuman) })
	if err = d.ReturnVscreenLocally(ctx, "local-owner", "ws", "rt", id, "fixture correction"); err != nil {
		t.Fatal(err)
	}
	ready := reportTestReceive(t, reports)
	if ready.State != protocol.VscreenInterventionReadyToContinue || ready.ReturnReceiptID == "" {
		t.Fatal("native return proof missing")
	}
	reportTestAck(d.vscreenReporter, binding, ready, 3, "")
	waitMirrorProcess(t, func() bool { return d.vscreenReporter.Acknowledged(id, protocol.VscreenInterventionReadyToContinue) })
	next := processScreenTask()
	next.ID = "continued"
	next.DispatchedAt = "2026-10-08T00:00:01.123456Z"
	next.VscreenContinuation = &protocol.VscreenContinuationContext{InterventionID: id, SourceTaskID: task.ID, Epoch: ready.Epoch, ReturnReceiptID: ready.ReturnReceiptID}
	_, nextBroker, nextExecution, err := d.startTaskVscreen(ctx, next, "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.vscreenReporter.Acknowledged(id, protocol.VscreenInterventionReadyToContinue) {
		t.Fatal("continuation did not durably consume old proof")
	}
	if _, err = nextExecution.invoke(ctx, "vscreen_acquire", json.RawMessage(`{"request_id":"continued"}`)); err != nil {
		t.Fatal(err)
	}
	nextBroker.Close()
	nextExecution.Close()
	frames := make(chan protocol.Message, 8)
	client.mu.Lock()
	client.enqueue = func(raw []byte) (*wsOutbound, error) {
		var frame protocol.Message
		if err := json.Unmarshal(raw, &frame); err != nil {
			return nil, err
		}
		frames <- frame
		return &wsOutbound{data: raw, sent: true}, nil
	}
	client.mu.Unlock()
	disable := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "disable"}, CommandID: "disable", Kind: protocol.VscreenCommandDisable}
	if err = client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventVscreenCommand, Payload: marshalRaw(disable)})}, nil); err != nil {
		t.Fatal(err)
	}
	var receipt protocol.VscreenCommandReceipt
	for receipt.State != protocol.VscreenReceiptSucceeded {
		select {
		case frame := <-frames:
			if frame.Type == protocol.EventVscreenResult {
				json.Unmarshal(frame.Payload, &receipt)
				if receipt.State == protocol.VscreenReceiptFailed {
					t.Fatal("native disable failed")
				}
			}
		case <-ctx.Done():
			t.Fatal("cleanup receipt missing")
		}
	}
	if err = client.call(ctx, mirrorProcessRequest{Operation: "control", Payload: marshalRaw(protocol.Message{Type: protocol.EventVscreenResult, Payload: marshalRaw(receipt)})}, nil); err != nil {
		t.Fatal(err)
	}
	local, err := d.verifiedMirrorLocal(ctx, "local-owner", mirrorLocalAction{Action: "status", WorkspaceID: "ws", RuntimeID: "rt"})
	if err != nil || local.InterventionID != "" {
		t.Fatal("cleanup ACK did not retire child intervention")
	}
}
func TestMirrorProcessPermissionQueriesDoNotPromptAndDeniedEnableFails(t *testing.T) {
	requests := filepath.Join(t.TempDir(), "permission-requests")
	d, client, _ := mirrorProcessFixtureEnv(t, map[string]string{"VSCREEN_FIXTURE_PERMISSION_REQUEST_COUNT": requests, "VSCREEN_FIXTURE_PERMISSION_REQUEST": "accessibility_denied"})
	if _, err := d.vscreenSnapshot(t.Context(), "ws", "rt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(requests); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("readonly permission query requested consent")
	}
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", RequestID: "enable", DaemonGeneration: "server"}, CommandID: "enable", Kind: protocol.VscreenCommandEnable}
	if err := d.executeVscreenCommand(t.Context(), command, 1); vscreenReason(err) != protocol.VscreenPermissionDenied {
		t.Fatalf("permission refusal lost: %v", err)
	}
	state, err := d.vscreenSnapshot(t.Context(), "ws", "rt")
	if err != nil || state.State != protocol.VscreenStateDisabled {
		t.Fatal("denied permission created a display")
	}
	raw, err := os.ReadFile(requests)
	if err != nil || len(raw) != 1 {
		t.Fatal("explicit permission request not bounded")
	}
	if d.vscreen != nil || d.globalInjector() != nil {
		t.Fatal("permission path created parent authority")
	}
	_ = client
}
func TestMirrorProcessLocalWindowSelectionAndRuntimeRemoval(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	task := processScreenTask()
	_, broker, execution, err := d.startTaskVscreen(ctx, task, "codex", func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = execution.invoke(ctx, "vscreen_acquire", json.RawMessage(`{"request_id":"empty-owner"}`)); err != nil {
		t.Fatal(err)
	}
	command := protocol.VscreenCommand{VscreenEnvelope: protocol.VscreenEnvelope{WorkspaceID: "ws", RuntimeID: "rt", DaemonGeneration: "server", RequestID: "takeover"}, CommandID: "takeover", Kind: protocol.VscreenCommandRequestTakeover}
	if err = d.executeVscreenCommand(ctx, command, 1); err != nil {
		t.Fatal(err)
	}
	state, err := d.vscreenSnapshot(ctx, "ws", "rt")
	if err != nil || state.InterventionID == nil {
		t.Fatal("intervention unavailable")
	}
	id := *state.InterventionID
	d.SetVscreenLocalOwnerVerifier(func(_ context.Context, token string) bool { return token == "local-owner" })
	sender, reports := reportTestSender(t)
	binding := d.vscreenReporter.Bind("server", sender)
	broker.Close()
	execution.Close()
	if err = d.markVscreenInterventionStopped(context.WithValue(ctx, mirrorReportedClaimKey{}, execution.remote.claim), task.ID); err != nil {
		t.Fatal(err)
	}
	awaiting := reportTestReceive(t, reports)
	if _, err = d.selectVscreenWindow(ctx, "local-owner", "ws", "rt", id, "", false); err == nil || !strings.Contains(err.Error(), "report_pending") {
		t.Fatal("window selection bypassed report ACK")
	}
	reportTestAck(d.vscreenReporter, binding, awaiting, 1, "")
	waitMirrorProcess(t, func() bool { return d.vscreenReporter.Acknowledged(id, protocol.VscreenInterventionAwaitingTakeover) })
	local, err := d.verifiedMirrorLocal(ctx, "local-owner", mirrorLocalAction{Action: "status", WorkspaceID: "ws", RuntimeID: "rt"})
	if err != nil || !local.SelectionRequired {
		t.Fatalf("local status was not forwarded: %+v %v", local, err)
	}
	candidates, err := d.selectVscreenWindow(ctx, "local-owner", "ws", "rt", id, "", false)
	if err != nil || len(candidates.Windows) != 1 {
		t.Fatalf("private candidate listing %v", err)
	}
	if _, err = d.selectVscreenWindow(ctx, "forged", "ws", "rt", id, candidates.Windows[0].Handle, true); err == nil {
		t.Fatal("foreign local owner adopted window")
	}
	if _, err = d.selectVscreenWindow(ctx, "local-owner", "ws", "rt", id, candidates.Windows[0].Handle, true); err != nil {
		t.Fatal(err)
	}
	adopted := reportTestReceive(t, reports)
	if adopted.State != protocol.VscreenInterventionHuman {
		t.Fatal("window selection did not reach native adoption")
	}
	d.mu.Lock()
	d.detachRuntimeMirrorsLocked([]string{"rt"})
	delete(d.runtimeIndex, "rt")
	d.workspaces["ws"].runtimeIDs = []string{"other"}
	d.mu.Unlock()
	d.closeDetachedRuntimeMirrors(nil)
	raw, err := client.process.Client.Read(ctx, "mirror.inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Mirrors    map[string]bool `json:"mirrors"`
		Executions int             `json:"executions"`
	}
	json.Unmarshal(raw, &inventory)
	if _, ok := inventory.Mirrors["rt"]; ok || inventory.Executions != 0 {
		t.Fatal("removed runtime retained child consumers")
	}
	if _, err = d.vscreenSnapshot(ctx, "ws", "rt"); err == nil {
		t.Fatal("removed runtime retained query authority")
	}
	if d.vscreen != nil || d.inputArbiter != nil || d.controlBackend != nil || d.globalInjector() != nil {
		t.Fatal("local/removal path created parent GUI authority")
	}
}
func TestMirrorProcessDisconnectAndStaleControlRemainFenced(t *testing.T) {
	d, client, _ := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	if err := client.mutation(t.Context(), "mirror.submit", mirrorProcessRequest{Generation: 0, Operation: "snapshot", WorkspaceID: "ws", RuntimeID: "rt"}, nil); err == nil {
		t.Fatal("stale control accepted")
	}
	if _, err := d.vscreenSnapshot(t.Context(), "ws", "rt"); err != nil {
		t.Fatal("safe stale rejection poisoned current binding")
	}
	client.unbind(1)
	raw, err := client.process.Client.Read(t.Context(), "mirror.inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Bound bool `json:"bound"`
	}
	json.Unmarshal(raw, &inventory)
	if inventory.Bound {
		t.Fatal("disconnect left child authority active")
	}
	resources, err := d.mirrorRoster()
	if err != nil {
		t.Fatal(err)
	}
	d.mirrorControlGeneration = 2
	if err = client.bind(t.Context(), mirrorControlBinding{Generation: 2, ServerGeneration: "server-2", Resources: resources}, func(raw []byte) (*wsOutbound, error) { return &wsOutbound{data: raw, sent: true}, nil }); err != nil {
		t.Fatal(err)
	}
	code, err := client.bridgeRequest(t.Context(), "/unbind", mirrorBridgePoll{InstanceID: client.identity.InstanceID, Generation: 1}, nil)
	if code != 409 || err == nil {
		t.Fatal("old disconnect changed replacement authority")
	}
	raw, err = client.process.Client.Read(t.Context(), "mirror.inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw, &inventory)
	if !inventory.Bound {
		t.Fatal("replacement binding was suspended by old event")
	}
}
func TestMirrorProcessLostAcquireResponseRetainsReceiptAndCleansJob(t *testing.T) {
	d, client, b := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	record, err := runtimeproc.ReadRecord(b.Root, b.Identity)
	if err != nil {
		t.Fatal(err)
	}
	var dropped atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request runtimeproc.Request
		json.Unmarshal(raw, &request)
		req, _ := http.NewRequestWithContext(r.Context(), "POST", record.Address+"/rpc", bytes.NewReader(raw))
		req.Header = r.Header.Clone()
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if request.Operation == "mirror.submit" && !dropped.Swap(true) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.WriteHeader(response.StatusCode)
		w.Write(body)
	}))
	defer proxy.Close()
	proxied := record
	proxied.Address = proxy.URL
	client.process.Client, err = runtimeproc.NewClient(proxied)
	if err != nil {
		t.Fatal(err)
	}
	claim := mirrorTaskClaim{TaskID: "lost", AgentID: "agent", WorkspaceID: "ws", RuntimeID: "rt", DispatchedAt: "2026-10-08T00:00:00.123456Z"}
	var grant mirrorExecutionGrant
	err = client.call(t.Context(), mirrorProcessRequest{Operation: "acquire", Provider: "codex", Claim: &claim}, &grant)
	var uncertain *mirrorUncertainOperation
	if !errors.As(err, &uncertain) || uncertain.RequestID == "" {
		t.Fatalf("lost request identity: %v", err)
	}
	receipt, err := client.process.Client.QueryOperation(t.Context(), uncertain.RequestID)
	if err != nil || receipt.State != "completed" {
		t.Fatal("accepted receipt not retained")
	}
	waitMirrorProcess(t, func() bool {
		raw, err := client.process.Client.Read(t.Context(), "mirror.inventory", nil)
		var inventory struct {
			Executions int `json:"executions"`
		}
		return err == nil && json.Unmarshal(raw, &inventory) == nil && inventory.Executions == 0
	})
	if err = client.call(t.Context(), mirrorProcessRequest{Operation: "snapshot", WorkspaceID: "ws", RuntimeID: "rt"}, nil); !errors.As(err, &uncertain) {
		t.Fatal("later mutation acknowledged earlier unknown result")
	}
	client.unbind(1)
	raw, err := client.process.Client.Read(t.Context(), "mirror.inventory", nil)
	var inventory struct {
		Bound bool `json:"bound"`
	}
	if err != nil || json.Unmarshal(raw, &inventory) != nil || inventory.Bound {
		t.Fatal("unknown receipt prevented fail-closed disconnect")
	}
	// Stop still operates on the owned process. The test proxy must remain alive through it.
	if err = client.close(); err != nil {
		t.Fatal(err)
	}
}
func TestMirrorProcessDesktopAndApprovalNegativePathsNeverCreateParentActor(t *testing.T) {
	d, _, _ := mirrorProcessFixture(t)
	credential := desktopVscreenCredential{Profile: "", Incarnation: "fixture"}
	verify := func(context.Context, string) bool { return true }
	handler := d.vscreenDesktopHandler(credential, verify)
	for _, action := range []string{"status", "list_windows", "adopt_window", "takeover", "return", "exclusions"} {
		body := marshalRaw(map[string]any{"action": action, "workspace_id": "ws", "runtime_id": "rt", "intervention_id": "missing"})
		request := httptest.NewRequest("POST", "/vscreen/desktop", bytes.NewReader(body))
		request.Header.Set("X-Vscreen-Incarnation", "fixture")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 && response.Code != 409 {
			t.Fatalf("local %s status=%d", action, response.Code)
		}
	}
	task := processScreenTask()
	task.InitiatorType = "member"
	task.InitiatorID = "member"
	if _, err := d.requestTaskApproval(task)(t.Context(), agent.ApprovalRequest{Method: "execCommandApproval", Params: json.RawMessage(`{"command":"fixture-no-op"}`)}); err == nil {
		t.Fatal("approval succeeded without reviewer")
	}
	d.pruneVscreens()
	d.closeVscreenRuntime("other")
	if d.vscreen != nil || d.inputArbiter != nil || d.controlBackend != nil || d.globalInjector() != nil {
		t.Fatal("negative facade path created control-process GUI authority")
	}
}
func TestMirrorProcessNativeFaultStopsOnlyGUIAndRevokesAuthority(t *testing.T) {
	d, client, b := mirrorProcessFixture(t)
	enableProcessScreen(t, d)
	var guiStopped, codeStopped atomic.Int32
	task := processScreenTask()
	_, guiBroker, gui, err := d.startTaskVscreen(t.Context(), task, "codex", func(error) { guiStopped.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gui.invoke(t.Context(), "vscreen_acquire", json.RawMessage(`{"request_id":"native-fault"}`)); err != nil {
		t.Fatal(err)
	}
	task.ID = "code"
	_, codeBroker, code, err := d.startTaskVscreen(t.Context(), task, "codex", func(error) { codeStopped.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = code.invoke(t.Context(), "vscreen_status", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(b.Root), "native.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitMirrorProcess(t, func() bool { return guiStopped.Load() > 0 })
	if codeStopped.Load() != 0 {
		t.Fatal("native fault canceled a status-only provider")
	}
	state, err := client.process.Client.Read(t.Context(), "mirror.inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Bound  bool `json:"bound"`
		Native bool `json:"native_alive"`
	}
	json.Unmarshal(state, &inventory)
	if inventory.Bound || inventory.Native {
		t.Fatal("native failure left child authority active")
	}
	guiBroker.Close()
	gui.Close()
	codeBroker.Close()
	code.Close()
	// Native disposal after a crash is unconfirmed; forcibly reap only this owned
	// mirror child and retain its unknown record, rather than claiming clean stop.
	if err = client.process.Close(); err != nil {
		t.Fatal(err)
	}
	client.closeOnce.Do(func() { client.cancel(); <-client.done })
	waitMirrorProcess(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
	if err = reconcileStoppedMirror(t.Context(), b.Root, b.Identity.Scope); err == nil {
		t.Fatal("native-fault owner was automatically replaced")
	}
}
func TestMirrorProcessLazyProfileLauncherUsesStableAccountWithoutPAT(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" || r.Header.Get("Authorization") != "Bearer fixture-pat" {
			w.WriteHeader(401)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
	}))
	defer backend.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := New(Config{ProcessServices: []string{"mirror"}, ServerBaseURL: backend.URL, DaemonID: "daemon", WorkspacesRoot: filepath.Join(root, "workspaces"), NativeHostExecutable: executable, NativeHostBuild: "fixture/commit", NativeVscreenPreferencesPath: filepath.Join(root, "vscreen-enabled.json")}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetToken("fixture-pat")
	d.workspaces = map[string]*workspaceState{"ws": {runtimeIDs: []string{"rt"}}}
	d.runtimeIndex = map[string]Runtime{"rt": {ID: "rt"}}
	d.vscreenServerGeneration = "server"
	d.mirrorControlGeneration = 1
	t.Cleanup(func() { d.closeRuntimeMirrors(); d.closeVscreens(); d.closeVscreenReporter() })
	if err = d.bindMirrorProcess(t.Context(), 1, func(raw []byte) (*wsOutbound, error) { return &wsOutbound{data: raw, sent: true}, nil }); err != nil {
		t.Fatal(err)
	}
	client, err := d.currentMirrorProcess()
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || client.identity.Scope.Account != "11111111-1111-4111-8111-111111111111" || client.identity.Scope.Profile != "" {
		t.Fatal("account/profile identity was fabricated or token-derived")
	}
	if d.inputArbiter != nil || d.vscreen != nil || d.globalInjector() != nil {
		t.Fatal("parent constructor created GUI authority")
	}
	d.client.SetToken("rotated-token-not-forwarded")
	same, err := d.ensureMirrorProcess(t.Context())
	if err != nil || same != client {
		t.Fatal("token rotation changed process owner")
	}
	if _, err = d.vscreenSnapshot(t.Context(), "ws", "rt"); err != nil {
		t.Fatal(err)
	}
	if err = client.close(); err != nil {
		t.Fatal(err)
	}
}
