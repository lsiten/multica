package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// hotPathFakes builds the Daemon seams the opt-in hot-path branch relies on: a
// launchable worker with a faithful transport, plus a server-authorized bind and
// grant. It mirrors TestRunTaskInWorkerLaunchesAssemblesAndRuns so the opt-in
// decision (attemptWorkerRun) is driven by real, not canned, worker behavior.
func hotPathFakes(t *testing.T, transport *fakeWorkerTransport) (*Daemon, *Task) {
	taskID := uuid.NewString()
	runtimeID := uuid.NewString()
	workerInstanceID := strings.Repeat("a", 32)
	dispatchedAt := time.Now().Truncate(time.Second)
	execID := uuid.NewString()
	task := &Task{ID: taskID, RuntimeID: runtimeID, DispatchedAt: dispatchedAt.Format(time.RFC3339)}

	worker := &workerProcessClient{
		transport:  transport,
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: workerInstanceID,
	}
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example", WorkerProcessEnabled: true, NativeHostExecutable: "/test-owned/exec"},
		workerProcessLaunch: func(_ context.Context, _ string) (*workerProcessClient, error) {
			return worker, nil
		},
		acquireSupervisor: func(_ context.Context, _ string, _ protocol.SupervisorRequest) (*protocol.SupervisorResponse, error) {
			return &protocol.SupervisorResponse{InstanceID: "instance", Epoch: 1, Capabilities: []string{protocol.ExecutionCapabilityV1}}, nil
		},
		bindExecution: func(_ context.Context, rid, tid string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			if req.WorkerID != workerInstanceID {
				t.Fatalf("bind worker id = %q, want %q", req.WorkerID, workerInstanceID)
			}
			// The server rejects a bind whose supervisor epoch is below 1; the opt-in
			// hot path must therefore bind with the real acquired epoch (never 0).
			if req.SupervisorEpoch != 1 {
				t.Fatalf("bind supervisor epoch = %d, want 1 (the acquired epoch, not 0)", req.SupervisorEpoch)
			}
			return &protocol.ExecutionIdentity{TaskID: tid, RuntimeID: rid, WorkerID: req.WorkerID, ExecutionID: execID, DispatchedAt: req.DispatchedAt}, nil
		},
		issueGrant: func(_ context.Context, rid, tid string, req protocol.ExecutionGrantRequest) (*protocol.ExecutionGrantResponse, error) {
			if req.ExecutionID != execID {
				t.Fatalf("grant execution id = %q, want %q", req.ExecutionID, execID)
			}
			return &protocol.ExecutionGrantResponse{
				Token:     "mwt_" + strings.Repeat("a", 64),
				ExpiresAt: time.Now().Add(time.Hour),
				Identity:  protocol.ExecutionIdentity{TaskID: tid, RuntimeID: rid, WorkerID: workerInstanceID, ExecutionID: execID, DispatchedAt: dispatchedAt},
			}, nil
		},
	}
	return d, task
}

// TestAttemptWorkerRunGateOffNeverLaunches proves the default (gate OFF) path is
// inert: attemptWorkerRun returns a zero decision and never launches a worker, so
// the control parent falls back to the in-process runner and the hot path is
// untouched.
func TestAttemptWorkerRunGateOffNeverLaunches(t *testing.T) {
	d := &Daemon{cfg: Config{}}
	launched := 0
	d.workerProcessLaunch = func(_ context.Context, _ string) (*workerProcessClient, error) {
		launched++
		return nil, nil
	}
	decision, err := d.attemptWorkerRun(context.Background(), Task{ID: "t"}, "codex", nil, nil)
	if err != nil {
		t.Fatalf("attemptWorkerRun: %v", err)
	}
	if decision.UseWorkerResult {
		t.Fatalf("gate OFF must not take the worker path")
	}
	if launched != 0 {
		t.Fatalf("gate OFF must not launch a worker, launched=%d", launched)
	}
}

// TestAttemptWorkerRunOptInTakesWorkerPath proves the ON path: with the gate ON
// and a launchable bind, attemptWorkerRun drives the worker (bind -> run ->
// stop) and returns UseWorkerResult with the genuine, un-fabricated result. In
// runTask this means the control parent returns the projected worker result
// BEFORE executeAndDrain, so the provider is not double-run, and handleTask's
// final pre-completion check (shouldInterruptAgent) discards the redundant
// in-process settle, so it is not double-settled.
func TestAttemptWorkerRunOptInTakesWorkerPath(t *testing.T) {
	transport := &fakeWorkerTransport{}
	d, task := hotPathFakes(t, transport)
	env := &execenv.Environment{WorkDir: "/w", RootDir: "/r"}

	if !d.workerProcessEnabled() {
		t.Fatalf("precondition: gate must be ON")
	}
	decision, err := d.attemptWorkerRun(context.Background(), *task, "codex", nil, env)
	if err != nil {
		t.Fatalf("attemptWorkerRun: %v", err)
	}
	if !decision.UseWorkerResult {
		t.Fatalf("opt-in + launchable bind must take the worker path")
	}
	if !decision.Result.Ran || decision.Result.Status != "completed" {
		t.Fatalf("expected a genuine completed run, got %+v", decision.Result)
	}
	// The worker was bound, run, and stopped: the provider ran in the worker,
	// not in-process.
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.ops) == 0 || transport.ops[0] != "worker.bind" || transport.ops[len(transport.ops)-1] != "stop" {
		t.Fatalf("expected bind..run..stop, got %v", transport.ops)
	}
}

// TestAttemptWorkerRunRefusedBindFallsBack proves a refused bind (not launchable)
// is a clean fall-back: no error and no worker result, so the control parent
// returns to the in-process runner and settles the execution itself.
func TestAttemptWorkerRunRefusedBindFallsBack(t *testing.T) {
	transport := &fakeWorkerTransport{refuseBind: true}
	d, task := hotPathFakes(t, transport)

	decision, err := d.attemptWorkerRun(context.Background(), *task, "codex", nil, nil)
	if err != nil {
		t.Fatalf("a refused bind must not be an error, got %v", err)
	}
	if decision.UseWorkerResult {
		t.Fatalf("a refused bind must fall back to the in-process runner, not take the worker path")
	}
}

// TestAttemptWorkerRunUncertainFallsBack proves an uncertain transport (a genuine
// transport failure, not a domain refusal) surfaces an error with a zero
// decision, so the control parent neither fabricates a result nor double-runs;
// it falls back and the pre-completion check reconciles any double-settle.
func TestAttemptWorkerRunUncertainFallsBack(t *testing.T) {
	transport := &fakeWorkerTransport{failNext: true}
	d, task := hotPathFakes(t, transport)

	decision, err := d.attemptWorkerRun(context.Background(), *task, "codex", nil, nil)
	if err == nil {
		t.Fatalf("an uncertain transport must surface an error so the caller falls back")
	}
	if decision.UseWorkerResult {
		t.Fatalf("an uncertain outcome must not take the worker path")
	}
}

// TestProjectWorkerResult proves the pure projection maps a genuine worker run
// onto the TaskResult the control parent returns, and is nil-safe for the
// prepared environment (the hot path always passes a prepared env; the guard
// keeps the projection safe to unit-test in isolation).
func TestProjectWorkerResult(t *testing.T) {
	wr := &workerRunResult{Status: "completed", Output: "done", SessionID: "s1", Ran: true}
	env := &execenv.Environment{WorkDir: "/w", RootDir: "/r"}
	got := projectWorkerResult(wr, env)
	if got.Status != "completed" || got.Comment != "done" || got.SessionID != "s1" || got.WorkDir != "/w" || got.EnvRoot != "/r" {
		t.Fatalf("projection mismatch: %+v", got)
	}
	gotNil := projectWorkerResult(wr, nil)
	if gotNil.Status != "completed" || gotNil.Comment != "done" || gotNil.SessionID != "s1" {
		t.Fatalf("nil-env projection mismatch: %+v", gotNil)
	}
}

// TestAttemptWorkerRunOldServerRefusesInProcessFallback proves the "old server
// -> drain-only" contract: when the control-supervisor acquisition returns
// ErrExecutionUnsupported (a server that does not advertise execution
// reconciliation), attemptWorkerRun refuses the worker move-out with a CLEAN
// fall-back (nil error, zero decision) and never launches or binds a worker, so
// the control parent falls back to the in-process runner and settles itself.
func TestAttemptWorkerRunOldServerRefusesInProcessFallback(t *testing.T) {
	dispatchedAt := time.Now().Truncate(time.Second)
	task := &Task{ID: uuid.NewString(), RuntimeID: uuid.NewString(), DispatchedAt: dispatchedAt.Format(time.RFC3339)}

	launched := 0
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example", WorkerProcessEnabled: true, NativeHostExecutable: "/test-owned/exec"},
		workerProcessLaunch: func(_ context.Context, _ string) (*workerProcessClient, error) {
			launched++
			return &workerProcessClient{}, nil
		},
		acquireSupervisor: func(_ context.Context, _ string, _ protocol.SupervisorRequest) (*protocol.SupervisorResponse, error) {
			return nil, ErrExecutionUnsupported
		},
	}

	decision, err := d.attemptWorkerRun(context.Background(), *task, "codex", nil, nil)
	if err != nil {
		t.Fatalf("an old-server (ErrExecutionUnsupported) acquisition must be a clean fall-back (nil), got %v", err)
	}
	if decision.UseWorkerResult {
		t.Fatalf("an old server must refuse the worker move-out, not take the worker path")
	}
	if launched != 0 {
		t.Fatalf("an old server must not launch or bind a worker, launched=%d", launched)
	}
}
