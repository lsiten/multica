package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// fakeWorkerTransport simulates the per-execution worker's private runtime
// channel: Health/Request/Call. It records the operations it received and models
// the worker's bind/run decision (DecideWorkerLaunch) so the control parent is
// driven by a faithful, not a canned, worker.
type fakeWorkerTransport struct {
	mu       sync.Mutex
	ops      []string
	failNext bool
	revision uint64
	// healthState overrides the reported owner state (empty stays "ready"),
	// so a not-ready worker can be exercised without a real child.
	healthState string
	// refuseBind makes the bind return a not-launchable decision so a
	// refused bind (not launchable) can be exercised without a real child.
	refuseBind bool
}

func (f *fakeWorkerTransport) Health(_ context.Context) (runtimeproc.Status, error) {
	f.mu.Lock()
	f.revision++
	fence := runtimeproc.Fence{Revision: f.revision}
	state := f.healthState
	if state == "" {
		state = "ready"
	}
	f.mu.Unlock()
	return runtimeproc.Status{State: state, Fence: fence}, nil
}

func (f *fakeWorkerTransport) Request(operation string, fence runtimeproc.Fence, payload json.RawMessage) (runtimeproc.Request, error) {
	return runtimeproc.Request{Operation: operation, Fence: fence, Payload: payload, Deadline: time.Now().Add(30 * time.Second)}, nil
}

func (f *fakeWorkerTransport) Call(_ context.Context, request runtimeproc.Request) (runtimeproc.Response, error) {
	f.mu.Lock()
	f.ops = append(f.ops, request.Operation)
	fail := f.failNext
	f.failNext = false
	f.mu.Unlock()
	if fail {
		// A genuine transport failure (not a domain error) is what the client
		// must treat as uncertain.
		return runtimeproc.Response{}, errors.New("runtime control channel unavailable; owner is suspect")
	}
	var receipt *runtimeproc.Receipt
	switch request.Operation {
	case "worker.bind":
		var in workerBindRequest
		_ = json.Unmarshal(request.Payload, &in)
		var launchable bool
		var reason string
		if f.refuseBind {
			launchable = false
			reason = "not_launchable"
		} else {
			decision := DecideWorkerLaunch(
				in.Grant.Identity.TaskID, in.Grant.Identity.RuntimeID, in.Grant.Identity.WorkerID,
				in.Grant.Identity.ExecutionID, in.Grant.Identity.DispatchedAt,
				in.WorkerIDMatchesBind, validExecutionGrant(in.Grant), in.LaunchAuthorized, in.Uncertain,
			)
			launchable = decision.Launchable
			reason = decision.ReasonCode
		}
		result, _ := json.Marshal(workerStatus{
			InstanceID: "instance", PID: 1, State: "ready",
			Launchable: launchable, ReasonCode: reason,
			ExecutionID: in.Grant.Identity.ExecutionID, HasClient: validExecutionGrant(in.Grant),
		})
		receipt = &runtimeproc.Receipt{State: "completed", Result: result}
	case "worker.run":
		// The control parent only reaches a run after a launchable bind, so the
		// worker genuinely produces a result (not a fabricated success).
		result, _ := json.Marshal(workerRunResult{InstanceID: "instance", PID: 1, Ran: true, Status: "completed"})
		receipt = &runtimeproc.Receipt{State: "completed", Result: result}
	default:
		receipt = &runtimeproc.Receipt{State: "completed"}
	}
	return runtimeproc.Response{Status: runtimeproc.Status{Fence: request.Fence}, Receipt: receipt}, nil
}

// TestWorkerProcessClientBindThenRun is the control-parent F3 core: the control
// parent (here, a faithful transport) binds the exact execution facts, gets a
// launchable decision, then drives a genuine run and reads the exact result the
// worker produced.
func TestWorkerProcessClientBindThenRun(t *testing.T) {
	transport := &fakeWorkerTransport{}
	c := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	grant := validWorkerGrant()
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{Provider: "codex"}}
	status, err := c.bind(context.Background(), in)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !status.Launchable {
		t.Fatalf("expected launchable bind, got reason %q", status.ReasonCode)
	}
	if !c.ready() {
		t.Fatalf("expected ready after bind")
	}
	result, err := c.run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Ran || result.Status != "completed" {
		t.Fatalf("expected a genuine run result, got %+v", result)
	}
}

// TestWorkerProcessClientUncertainMatrix proves a transport failure is uncertain
// (never a false close or a fabricated result): the client records the
// uncertainty and every subsequent operation returns it rather than relaunching.
func TestWorkerProcessClientUncertainMatrix(t *testing.T) {
	transport := &fakeWorkerTransport{}
	c := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	grant := validWorkerGrant()
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{Provider: "codex"}}
	// First bind fails at the transport layer: it must become uncertain.
	transport.failNext = true
	if _, err := c.bind(context.Background(), in); err == nil {
		t.Fatalf("expected an uncertain bind, got nil")
	}
	// A second bind must return the recorded uncertainty, not re-drive the worker.
	transport.failNext = false
	if _, err := c.bind(context.Background(), in); err == nil {
		t.Fatalf("expected the recorded uncertainty to block the second bind")
	}
}

// TestWorkerProcessClientRefusedBindNeverRuns proves a refused (not-launchable)
// bind is a definitive refusal the control parent must not turn into a run.
func TestWorkerProcessClientRefusedBindNeverRuns(t *testing.T) {
	transport := &fakeWorkerTransport{}
	c := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	grant := validWorkerGrant()
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: false, LaunchAuthorized: true}
	status, err := c.bind(context.Background(), in)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if status.Launchable {
		t.Fatalf("a worker-id mismatch must refuse the launch")
	}
}

// TestBuildWorkerContextProjectsRunTaskLocals is the control-parent half of the
// opt-in F3 move-out: the *Daemon-free context assembly runTask would hand a
// per-execution worker. It proves the projected workerContext carries the config
// and task by reference and maps the prepared environment, so the worker resolves
// the same provider run the legacy in-process runner would — not a fabricated one.
func TestBuildWorkerContextProjectsRunTaskLocals(t *testing.T) {
	cfg := Config{
		CLIVersion: "0.1.13",
		Agents:     map[string]AgentEntry{"codex": {Model: "gpt-6-sol"}},
	}
	agent := &AgentData{
		ID:            "agent-1",
		Model:         "explicit-model",
		CustomArgs:    []string{"--custom"},
		McpConfig:     json.RawMessage(`{"mcpServers":{}}`),
		ThinkingLevel: "high",
		ServiceTier:   "priority",
	}
	task := Task{ID: "task-1", RuntimeID: "runtime-1", PriorSessionID: "sess-1", Agent: agent}
	env := &execenv.Environment{
		WorkDir:            "/work/dir",
		CodexHome:          "/work/codex",
		ClaudeSettingsPath: "/work/claude.json",
		QwenpawWorkspace:   "/work/qwenpaw",
		ContextDir:         "/work/context",
	}
	agentEnv := map[string]string{"CODEX_HOME": "/work/codex", "MULTICA_WORKSPACE_ID": "ws-1"}

	wc := buildWorkerContext(cfg, task, "codex", env, agentEnv)

	if wc.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", wc.Provider)
	}
	if wc.Config.CLIVersion != "0.1.13" {
		t.Fatalf("config not carried: CLIVersion = %q", wc.Config.CLIVersion)
	}
	if entry, ok := wc.Config.Agents["codex"]; !ok || entry.Model != "gpt-6-sol" {
		t.Fatalf("config not carried: Agents[codex] = %+v", entry)
	}
	if wc.Task.ID != "task-1" || wc.Task.RuntimeID != "runtime-1" || wc.Task.PriorSessionID != "sess-1" || wc.Task.Agent != agent {
		t.Fatalf("task not carried: %+v", wc.Task)
	}
	if got := wc.Env; got.WorkDir != "/work/dir" || got.CodexHome != "/work/codex" || got.ClaudeSettingsPath != "/work/claude.json" || got.QwenpawWorkspace != "/work/qwenpaw" || got.ContextDir != "/work/context" {
		t.Fatalf("prepared env projection = %+v", got)
	}
	if got := wc.Env.Env; got["CODEX_HOME"] != "/work/codex" || got["MULTICA_WORKSPACE_ID"] != "ws-1" {
		t.Fatalf("agent env not carried: %+v", got)
	}
	// An explicit agent model wins over the config entry, proving the worker
	// resolves the same model the legacy runner would.
	if got := resolveWorkerModel("codex", wc.Task, wc.Config); got != "explicit-model" {
		t.Fatalf("resolved model = %q, want explicit-model", got)
	}
	// A nil prepared environment must not panic and must leave an empty projection.
	if empty := buildWorkerContext(cfg, task, "codex", nil, agentEnv); empty.Env.WorkDir != "" {
		t.Fatalf("nil env must project empty, got %q", empty.Env.WorkDir)
	}
}

// TestWorkerProcessEnabledGateDefaultsOff proves the opt-in gate is closed by
// default: the legacy in-process runner stays the default and the hot path is
// unaffected unless a profile both opts in and has a managed executable.
func TestWorkerProcessEnabledGateDefaultsOff(t *testing.T) {
	d := &Daemon{cfg: Config{}}
	if d.workerProcessEnabled() {
		t.Fatalf("worker must be disabled by default")
	}
	d.cfg.WorkerProcessEnabled = true
	if d.workerProcessEnabled() {
		t.Fatalf("worker must be disabled without a managed executable")
	}
	d.cfg.NativeHostExecutable = "/usr/local/bin/multica"
	if !d.workerProcessEnabled() {
		t.Fatalf("worker must be enabled when opted in with an executable")
	}
}

// TestRunWorkerExecutionLaunchesBindsRuns is the *Daemon-coupled F3 core: the
// opt-in orchestration launches a worker through the (fake) transport, binds the
// exact execution facts, drives a genuine run, reads the exact result the
// worker produced, and stops the child. The worker's own callback — not the
// control parent — settles the execution, so the control parent never
// double-settles.
func TestRunWorkerExecutionLaunchesBindsRuns(t *testing.T) {
	transport := &fakeWorkerTransport{}
	client := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	d := &Daemon{}
	grant := validWorkerGrant()
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{Provider: "codex"}}

	result, err := d.runWorkerExecution(context.Background(), client, in)
	if err != nil {
		t.Fatalf("runWorkerExecution: %v", err)
	}
	if !result.Ran || result.Status != "completed" {
		t.Fatalf("expected a genuine run result, got %+v", result)
	}

	transport.mu.Lock()
	defer transport.mu.Unlock()
	// bind then run, then the close() stop the child; nothing is relaunch
	// or fabricated.
	ops := transport.ops
	if len(ops) == 0 || ops[0] != "worker.bind" || ops[len(ops)-1] != "stop" {
		t.Fatalf("expected bind..stop sequence, got %v", ops)
	}
}

// TestRunWorkerExecutionRefusedBindNeverRuns proves a refused (not-launchable)
// bind is a definitive decision the orchestration returns, not an error or a
// fabricated run: the child is still stopped, but no run is driven.
func TestRunWorkerExecutionRefusedBindNeverRuns(t *testing.T) {
	transport := &fakeWorkerTransport{}
	client := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	d := &Daemon{}
	grant := validWorkerGrant()
	// A worker-id mismatch refuses the launch in the worker's own decision.
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: false, LaunchAuthorized: true}

	result, err := d.runWorkerExecution(context.Background(), client, in)
	if err != nil {
		t.Fatalf("a refused bind must not be an error: %v", err)
	}
	if result.Ran {
		t.Fatalf("a refused bind must never run a provider, got %+v", result)
	}
	if result.Status != "refused" {
		t.Fatalf("expected a refused result, got %q", result.Status)
	}
	// The child was still stopped despite the refusal.
	transport.mu.Lock()
	defer transport.mu.Unlock()
	stopped := false
	for _, op := range transport.ops {
		if op == "stop" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("expected the child to be stopped after a refused bind, ops = %v", transport.ops)
	}
}

// TestRunWorkerExecutionNotReadyFailsClosed proves a launchable but not-ready
// worker fails closed: the control parent neither drives a run nor fabricates a
// result, and still stops the child.
func TestRunWorkerExecutionNotReadyFailsClosed(t *testing.T) {
	transport := &fakeWorkerTransport{healthState: "stopping"}
	client := &workerProcessClient{transport: transport, wait: func(context.Context) error { return nil }, gate: make(chan struct{}, 1)}
	d := &Daemon{}
	grant := validWorkerGrant()
	in := workerRunInput{Grant: grant, CallbackURL: "http://127.0.0.1:5555", WorkerIDMatchesBind: true, LaunchAuthorized: true, Context: &workerContext{Provider: "codex"}}

	result, err := d.runWorkerExecution(context.Background(), client, in)
	if err == nil {
		t.Fatalf("a not-ready worker must fail closed, got result %+v", result)
	}
	if result != nil {
		t.Fatalf("a not-ready worker must return no fabricated result, got %+v", result)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	stopped := false
	for _, op := range transport.ops {
		if op == "stop" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("expected the child to be stopped after a not-ready failure, ops = %v", transport.ops)
	}
}

// TestAssembleWorkerBindInputProducesValidBind is the F3 contract-prerequisite
// core: the control parent binds the exact execution identity server-side
// (the worker's own instance id), issues the scoped grant, and hands the worker
// the exact closed bind request (grant + callback + authorization + context).
// It proves the assembly is built from real, server-authorized facts, not a
// fabricated one.
func TestAssembleWorkerBindInputProducesValidBind(t *testing.T) {
	taskID := uuid.NewString()
	runtimeID := uuid.NewString()
	workerInstanceID := strings.Repeat("a", 32)
	dispatchedAt := time.Now().Truncate(time.Second)
	execID := uuid.NewString()
	task := Task{ID: taskID, RuntimeID: runtimeID, DispatchedAt: dispatchedAt.Format(time.RFC3339)}
	worker := &workerProcessClient{instanceID: workerInstanceID, gate: make(chan struct{}, 1)}
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example"},
		bindExecution: func(_ context.Context, rid, tid string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			if tid != taskID || rid != runtimeID {
				t.Fatalf("bind scoped to wrong runtime/task: %q/%q", rid, tid)
			}
			if req.WorkerID != workerInstanceID {
				t.Fatalf("bind worker id = %q, want %q", req.WorkerID, workerInstanceID)
			}
			if req.DispatchedAt.IsZero() {
				t.Fatalf("bind must carry the dispatched-at")
			}
			return &protocol.ExecutionIdentity{TaskID: taskID, RuntimeID: runtimeID, WorkerID: req.WorkerID, ExecutionID: execID, DispatchedAt: req.DispatchedAt}, nil
		},
		issueGrant: func(_ context.Context, rid, tid string, req protocol.ExecutionGrantRequest) (*protocol.ExecutionGrantResponse, error) {
			if req.ExecutionID != execID {
				t.Fatalf("grant execution id = %q, want %q", req.ExecutionID, execID)
			}
			return &protocol.ExecutionGrantResponse{
				Token:     "mwt_" + strings.Repeat("a", 64),
				ExpiresAt: time.Now().Add(time.Hour),
				Identity:  protocol.ExecutionIdentity{TaskID: taskID, RuntimeID: runtimeID, WorkerID: workerInstanceID, ExecutionID: execID, DispatchedAt: dispatchedAt},
			}, nil
		},
	}

	in, err := d.assembleWorkerBindInput(context.Background(), worker, task, "codex", nil, nil, 7)
	if err != nil {
		t.Fatalf("assembleWorkerBindInput: %v", err)
	}
	if in.CallbackURL != "https://server.example" {
		t.Fatalf("callback url = %q, want the server base", in.CallbackURL)
	}
	if !in.WorkerIDMatchesBind || !in.LaunchAuthorized {
		t.Fatalf("bind flags = matches=%v authorized=%v, want both true", in.WorkerIDMatchesBind, in.LaunchAuthorized)
	}
	if !validExecutionGrant(in.Grant) {
		t.Fatalf("assembled grant is not valid: %+v", in.Grant)
	}
	if in.Grant.Identity.ExecutionID != execID || in.Grant.Identity.WorkerID != workerInstanceID {
		t.Fatalf("grant identity = %+v", in.Grant.Identity)
	}
	if in.Context == nil || in.Context.Provider != "codex" || in.Context.Task.ID != taskID {
		t.Fatalf("context not projected: %+v", in.Context)
	}
}

// TestAssembleWorkerBindInputFailClosedWithoutWorker proves the assembly fails
// closed when there is no launched worker (or no instance id) rather than
// guessing an identity to bind.
func TestAssembleWorkerBindInputFailClosedWithoutWorker(t *testing.T) {
	d := &Daemon{cfg: Config{ServerBaseURL: "https://server.example"}}
	if _, err := d.assembleWorkerBindInput(context.Background(), nil, Task{}, "codex", nil, nil, 0); err == nil {
		t.Fatalf("expected an error for a nil worker")
	}
}

// TestAssembleWorkerBindInputFailClosedOnBindError proves a server-side bind
// failure is returned (never turned into a launch) rather than fabricated.
func TestAssembleWorkerBindInputFailClosedOnBindError(t *testing.T) {
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example"},
		bindExecution: func(ctx context.Context, runtimeID, taskID string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			return nil, errors.New("bind failed")
		},
	}
	worker := &workerProcessClient{instanceID: strings.Repeat("a", 32), gate: make(chan struct{}, 1)}
	task := Task{ID: uuid.NewString(), RuntimeID: uuid.NewString(), DispatchedAt: time.Now().Truncate(time.Second).Format(time.RFC3339)}
	if _, err := d.assembleWorkerBindInput(context.Background(), worker, task, "codex", nil, nil, 0); err == nil {
		t.Fatalf("expected an error on a bind failure")
	}
}

// TestAssembleWorkerBindInputRejectsGrantMismatch proves a grant for a different
// execution id (a server inconsistency) is rejected rather than handed to the
// worker, so the worker is never bound to an execution it did not reserve.
func TestAssembleWorkerBindInputRejectsGrantMismatch(t *testing.T) {
	taskID := uuid.NewString()
	runtimeID := uuid.NewString()
	dispatchedAt := time.Now().Truncate(time.Second)
	worker := &workerProcessClient{instanceID: strings.Repeat("a", 32), gate: make(chan struct{}, 1)}
	task := Task{ID: taskID, RuntimeID: runtimeID, DispatchedAt: dispatchedAt.Format(time.RFC3339)}
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example"},
		bindExecution: func(_ context.Context, rid, tid string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			return &protocol.ExecutionIdentity{TaskID: taskID, RuntimeID: runtimeID, WorkerID: req.WorkerID, ExecutionID: uuid.NewString(), DispatchedAt: req.DispatchedAt}, nil
		},
		issueGrant: func(_ context.Context, rid, tid string, req protocol.ExecutionGrantRequest) (*protocol.ExecutionGrantResponse, error) {
			// A grant for a DIFFERENT execution id than the bind reserved.
			return &protocol.ExecutionGrantResponse{
				Token:     "mwt_" + strings.Repeat("a", 64),
				ExpiresAt: time.Now().Add(time.Hour),
				Identity:  protocol.ExecutionIdentity{TaskID: taskID, RuntimeID: runtimeID, WorkerID: "00000000000000000000000000000000", ExecutionID: uuid.NewString(), DispatchedAt: dispatchedAt},
			}, nil
		},
	}
	if _, err := d.assembleWorkerBindInput(context.Background(), worker, task, "codex", nil, nil, 0); err == nil {
		t.Fatalf("expected an error for a grant that does not match the bind")
	}
}

// TestRunTaskInWorkerLaunchesAssemblesAndRuns is the F3 composition core: the
// full opt-in move-out — launch a worker (fake transport), assemble the exact
// server-authorized bind request (bind + grant + callback + context), and drive
// one genuine provider run. It proves the composition moves toward the real
// end state (the worker runs the task in a separate process) without a real
// child or a real server.
func TestRunTaskInWorkerLaunchesAssemblesAndRuns(t *testing.T) {
	taskID := uuid.NewString()
	runtimeID := uuid.NewString()
	workerInstanceID := strings.Repeat("a", 32)
	dispatchedAt := time.Now().Truncate(time.Second)
	execID := uuid.NewString()
	task := Task{ID: taskID, RuntimeID: runtimeID, DispatchedAt: dispatchedAt.Format(time.RFC3339)}

	transport := &fakeWorkerTransport{}
	worker := &workerProcessClient{
		transport:  transport,
		wait:       func(context.Context) error { return nil },
		gate:       make(chan struct{}, 1),
		instanceID: workerInstanceID,
	}
	d := &Daemon{
		cfg: Config{ServerBaseURL: "https://server.example"},
		workerProcessLaunch: func(_ context.Context, _ string) (*workerProcessClient, error) {
			return worker, nil
		},
		bindExecution: func(_ context.Context, rid, tid string, req protocol.BindExecutionRequest) (*protocol.ExecutionIdentity, error) {
			if req.WorkerID != workerInstanceID {
				t.Fatalf("bind worker id = %q, want %q", req.WorkerID, workerInstanceID)
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

	result, ran, err := d.runTaskInWorker(context.Background(), "exec-1", task, "codex", nil, nil, 3)
	if err != nil {
		t.Fatalf("runTaskInWorker: %v", err)
	}
	if !ran {
		t.Fatalf("expected ran=true after a launchable bind + genuine run")
	}
	if !result.Ran || result.Status != "completed" {
		t.Fatalf("expected a genuine run result, got %+v", result)
	}
	// The worker was bound, run, and stopped (close); nothing is fabricated.
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.ops) == 0 || transport.ops[0] != "worker.bind" || transport.ops[len(transport.ops)-1] != "stop" {
		t.Fatalf("expected bind..stop sequence, got %v", transport.ops)
	}
}
