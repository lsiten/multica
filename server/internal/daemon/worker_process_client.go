package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workerProcessClient is the control-side owner of ONE per-execution task
// worker. Unlike the long-lived service owners (gateway/application/
// environment), a worker owns a single server-claimed execution: control
// launches the child, binds the exact execution identity + scoped grant + the
// execution context, drives one genuine provider run, then stops the child.
// The legacy in-process runner (executeAndDrain) is the default and is never
// affected here; a worker is launched only when an opt-in capability admits it.
//
// This is the control-parent half of the F3 move-out. It owns only the private
// runtime channel and the launch/bind/run lifecycle. The worker itself owns the
// provider run, the completion gate, the report, and the launch-authorization
// decision (DecideWorkerLaunch), so a control-parent bind can never fabricate a
// launch or a result.
type workerProcessClient struct {
	transport workerRuntimeTransport
	wait      func(ctx context.Context) error
	// instanceID is this worker's canonical 32-char hex instance id; the
	// control parent binds it server-side before the bind so the worker
	// receives the exact execution identity it is bound to.
	instanceID string

	gate      chan struct{}
	uncertain error
	closeOnce sync.Once
	closeErr  error
}

// workerRuntimeTransport is the private runtime channel the control parent uses
// to drive a worker. It is the subset of *runtimeproc.Client the worker client
// needs; a test can supply a fake without launching a real child, while the
// real launch path hands over a live *runtimeproc.Client.
type workerRuntimeTransport interface {
	Health(ctx context.Context) (runtimeproc.Status, error)
	Request(operation string, fence runtimeproc.Fence, payload json.RawMessage) (runtimeproc.Request, error)
	Call(ctx context.Context, request runtimeproc.Request) (runtimeproc.Response, error)
}

// workerRunInput is the exact facts control delivers at bind. It is the closed
// set the worker applies its launch-authorization contract to; it carries no
// secret beyond the scoped grant (the worker's own credential) and no *Daemon
// reference.
// workerRunInput is the control parent's projection of the worker bind request.
// Its JSON tags match workerBindRequest exactly so the marshaled payload unmarshals
// into the worker's closed bind contract without any key drift.
type workerRunInput struct {
	Grant               protocol.ExecutionGrantResponse `json:"grant"`
	CallbackURL         string                          `json:"callback_url"`
	WorkerIDMatchesBind bool                            `json:"worker_id_matches_bind"`
	LaunchAuthorized    bool                            `json:"launch_authorized"`
	Uncertain           bool                            `json:"uncertain"`
	Context             *workerContext                  `json:"context,omitempty"`
}

// ensureWorkerProcess starts a per-execution worker child and returns the
// control-side client. It mirrors the sibling owners (a scoped identity, a
// private per-execution root, executable pinning, an explicit environment
// allowlist, and a readiness handshake) but for a single execution rather than a
// long-lived service. A missing executable fails closed: control never runs the
// task in-process to recover an uncertain launch.
func (d *Daemon) ensureWorkerProcess(ctx context.Context, execID string) (*workerProcessClient, error) {
	if d.cfg.NativeHostExecutable == "" {
		return nil, errors.New("worker executable is not configured; worker launch disabled")
	}
	var account struct {
		ID string `json:"id"`
	}
	if err := d.client.getJSON(ctx, "/api/me", &account); err != nil {
		return nil, err
	}
	if account.ID == "" {
		return nil, errors.New("worker owner identity missing")
	}
	identity, err := runtimeproc.NewIdentity(runtimeproc.Scope{
		Backend:  d.cfg.ServerBaseURL,
		Account:  account.ID,
		Profile:  d.cfg.Profile,
		DaemonID: d.cfg.DaemonID,
		Service:  "worker",
	}, d.cfg.NativeHostBuild)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(d.cfg.NativeVscreenPreferencesPath))
	if err != nil {
		return nil, err
	}
	root := filepath.Join(parent, "worker-service", execID)
	if !filepath.IsAbs(root) {
		return nil, errors.New("worker profile root must be absolute")
	}
	if err = runtimeproc.PrepareRoot(root); err != nil {
		return nil, err
	}
	bootstrap, err := runtimeproc.NewBootstrap(root, identity)
	if err != nil {
		return nil, err
	}
	executable, err := filepath.EvalSymlinks(d.cfg.NativeHostExecutable)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(executable)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	for _, name := range []string{"PATH", "HOME", "USER", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			environment[name] = value
		}
	}
	process, err := runtimeproc.Start(context.Background(), runtimeproc.LaunchConfig{
		Executable:     executable,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		Environment:    environment,
		Bootstrap:      bootstrap,
		StartupTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &workerProcessClient{
		transport:  process.Client,
		wait:       process.Wait,
		instanceID: identity.InstanceID,
		gate:       make(chan struct{}, 1),
	}, nil
}

// bind delivers the exact execution facts the worker applies its closed
// launch-authorization contract to and returns the worker's recorded decision.
// A transport failure is uncertain (the bind may have completed); a domain-level
// result (launchable or a refusal reason) is the worker's recorded decision and
// is returned directly. The control parent must not relaunch on an uncertain
// bind or a refused bind.
func (c *workerProcessClient) bind(ctx context.Context, in workerRunInput) (*workerStatus, error) {
	var status workerStatus
	if err := c.mutation(ctx, "worker.bind", in, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// run drives the single genuine provider run and returns the exact result the
// worker produced (never a fabricated success). A domain-level refusal (not
// launchable, no context, launch failed) is the worker's recorded decision and
// is returned directly; only a transport failure is uncertain. The terminal
// result the server settles the execution from is owned by the worker's own
// report (client.Callback), so the control parent never relaunches to recover a
// lost report.
func (c *workerProcessClient) run(ctx context.Context) (*workerRunResult, error) {
	var result workerRunResult
	if err := c.mutation(ctx, "worker.run", json.RawMessage(`{}`), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// mutation submits one bounded worker operation and returns the domain result.
// It is serialized so bind/run keep an exact order, and a transport failure
// becomes an uncertain operation rather than a false close.
func (c *workerProcessClient) mutation(ctx context.Context, operation string, input, out any) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if c.uncertain != nil {
		return c.uncertain
	}
	status, err := c.transport.Health(ctx)
	if err != nil {
		return err
	}
	var payload json.RawMessage
	if input != nil {
		payload = marshalRaw(input)
	}
	request, err := c.transport.Request(operation, status.Fence, payload)
	if err != nil {
		return err
	}
	request.Deadline = time.Now().Add(30 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(request.Deadline) {
		request.Deadline = deadline
	}
	response, err := c.transport.Call(ctx, request)
	if err != nil {
		// A domain-level result (a *runtimeproc.Error) is the worker's recorded
		// decision: return it directly, never as an uncertain transport. Only a
		// genuine transport failure (no domain error) is uncertain, because the
		// worker may have processed the request.
		var domain *runtimeproc.Error
		if errors.As(err, &domain) {
			return err
		}
		c.uncertain = fmt.Errorf("worker %s outcome uncertain (request %s): %v", operation, request.RequestID, err)
		return c.uncertain
	}
	if len(response.Receipt.Result) > 0 {
		if out != nil {
			if err = json.Unmarshal(response.Receipt.Result, out); err != nil {
				return err
			}
		}
	}
	ack, err := c.transport.Request("acknowledge", response.Status.Fence, nil)
	if err != nil {
		return err
	}
	if _, err = c.transport.Call(ctx, ack); err != nil {
		// The domain operation already completed; a lost acknowledgement is an
		// uncertainty, not a reason to re-run a bind/run.
		c.uncertain = fmt.Errorf("worker %s acknowledgement uncertain (request %s): %v", operation, request.RequestID, err)
		return c.uncertain
	}
	return nil
}

// ready reports whether the worker is live. A health failure means the owner is
// suspect, not stopped, so readiness fails closed.
func (c *workerProcessClient) ready() bool {
	if c == nil || c.transport == nil {
		return false
	}
	status, err := c.transport.Health(context.Background())
	if err != nil {
		return false
	}
	return status.State == "ready" || status.State == "running"
}

// close stops the per-execution worker and joins it. A lost stop is an
// uncertainty, never a claim that domain descendants stopped.
func (c *workerProcessClient) close() {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if c.wait == nil {
			c.closeErr = errors.New("worker has no runtime wait")
			return
		}
		status, err := c.transport.Health(ctx)
		if err == nil {
			request, requestErr := c.transport.Request("stop", status.Fence, nil)
			err = requestErr
			if err == nil {
				request.Deadline = time.Now().Add(55 * time.Second)
				if _, callErr := c.transport.Call(ctx, request); callErr != nil {
					err = callErr
				}
			}
		}
		if err != nil {
			c.closeErr = err
			return
		}
		c.closeErr = c.wait(ctx)
	})
}

// buildWorkerContext assembles the execution context the control parent delivers to
// a per-execution task worker. It is the *Daemon-free foundation of the opt-in F3
// move-out: when the worker capability is admitted, runTask hands the worker the
// same facts the legacy in-process runner uses to prepare and run the provider —
// the daemon-wide config, the server-claimed task, the prepared environment, and
// the assembled agent environment — so the worker resolves the backend and
// ExecOptions identically through buildWorkerProviderRun. The legacy in-process
// runner (executeAndDrain) is the default and is never affected here.
//
// The agent environment is assembled by the caller (runTask) and passed in, so this
// function reads no daemon state and mutates nothing: it is a pure projection. A
// nil prepared environment yields an empty projection rather than a panic, so a
// worker that cannot resolve its environment fails closed through
// buildWorkerProviderRun instead of a nil dereference.
func buildWorkerContext(cfg Config, task Task, provider string, env *execenv.Environment, agentEnv map[string]string) workerContext {
	prepared := PreparedEnv{Env: agentEnv}
	if env != nil {
		prepared.WorkDir = env.WorkDir
		prepared.CodexHome = env.CodexHome
		prepared.ClaudeSettingsPath = env.ClaudeSettingsPath
		prepared.QwenpawWorkspace = env.QwenpawWorkspace
		prepared.ContextDir = env.ContextDir
	}
	return workerContext{
		Provider: provider,
		Config:   cfg,
		Task:     task,
		Env:      prepared,
	}
}

// workerProcessEnabled reports whether this profile runs the per-execution task
// worker (F3). It is the gate of the move-out: it is the capability-gated default
// (cfg.WorkerProcessEnabled is set at config load to native.Supported() when no
// env value is present, so the worker is the default on a supported platform while
// the legacy in-process runner (executeAndDrain) stays the fallback whenever the
// worker cannot be launched, is refused, or fails — see attemptWorkerRun), and it
// is also false when no managed executable is configured (a worker with no binary
// to launch would fail closed). The hot path is a separate execution path, not a
// drop-in swap of executeAndDrain.
func (d *Daemon) workerProcessEnabled() bool {
	return d.cfg.WorkerProcessEnabled && d.cfg.NativeHostExecutable != ""
}

// runWorkerExecution is the *Daemon-coupled orchestration of the opt-in F3
// move-out: given an already-launched per-execution task worker, it binds
// the exact execution facts + context, drives one genuine provider run, then
// stops the child. The legacy in-process runner (executeAndDrain) stays the
// default and is never touched here. The launch itself happens in the caller
// (runTaskInWorker / ensureWorkerProcess) so the server-side bind can use
// the worker's instance id before the bind.
//
// Settlement is deliberately the worker's own: the worker posts the terminal
// result through its scoped callback (client.Callback), so the control parent
// must never report a terminal result for this task. Doing so would
// double-settle the execution against the server's already-terminal
// idempotency and a conflicting-authority 409. A refused bind is a definitive
// decision (returned, not an error, so the caller can fall back to the in-process
// runner); a transport failure is uncertain (returned, never a fabricated
// result); a launchable-but-not-ready worker fails closed.
// workerExecutionOperations is the closed set of task-local callback operations
// a per-execution worker is granted: it produces the terminal result, usage,
// transcript messages, session pin, supplement claim/ack, prepare-lease renewal
// and a status query. It carries no control/gateway authority.
func workerExecutionOperations() []string {
	return []string{"status", "start", "prepare-lease", "progress", "session", "complete", "fail", "usage", "messages", "supplements/claim", "supplements/ack", "worktree-delivery"}
}

// assembleWorkerBindInput assembles the closed bind request the control parent
// hands a per-execution task worker, from the runTask locals plus the
// server-side execution bind and grant. It is the contract prerequisite of the
// opt-in F3 move-out: control binds the exact execution identity server-side
// (the worker's own instance id), issues the scoped credential, projects the
// execution context (buildWorkerContext), and hands the worker the exact facts
// it applies its closed launch-authorization contract to. The legacy in-process
// runner (executeAndDrain) stays the default and is never touched here.
//
// It fails closed on any server-side bind/grant error (a bind or grant that
// cannot be confirmed is never turned into a launch) and never fabricates an
// identity: the returned workerRunInput carries the exact server-returned grant
// and the worker's own instance id, so the worker's launch decision is applied
// to real, server-authorized facts. A nil worker (or a worker without an
// instance id) cannot be bound and fails closed rather than guessing.
func (d *Daemon) assembleWorkerBindInput(ctx context.Context, worker *workerProcessClient, task Task, provider string, agentEnv map[string]string, env *execenv.Environment, supervisorEpoch int64) (workerRunInput, error) {
	if worker == nil || worker.instanceID == "" {
		return workerRunInput{}, errors.New("worker process without an instance id cannot be bound")
	}
	// The bind is keyed on the exact dispatched-at the server recorded at
	// claim time; the task carries it as a string, so parse it (RFC3339,
	// the server's JSON time format) and fail closed on a malformed value.
	dispatchedAt, err := time.Parse(time.RFC3339, task.DispatchedAt)
	if err != nil {
		return workerRunInput{}, fmt.Errorf("parse task dispatched_at %q: %w", task.DispatchedAt, err)
	}
	bind := d.bindExecution
	if bind == nil {
		bind = d.client.BindTaskExecution
	}
	identity, err := bind(ctx, task.RuntimeID, task.ID, protocol.BindExecutionRequest{
		WorkerID:        worker.instanceID,
		DispatchedAt:    dispatchedAt,
		SupervisorEpoch: supervisorEpoch,
	})
	if err != nil {
		return workerRunInput{}, err
	}
	issue := d.issueGrant
	if issue == nil {
		issue = d.client.IssueExecutionGrant
	}
	grant, err := issue(ctx, task.RuntimeID, task.ID, protocol.ExecutionGrantRequest{
		ExecutionID:     identity.ExecutionID,
		SupervisorEpoch: supervisorEpoch,
		Operations:      workerExecutionOperations(),
	})
	if err != nil {
		return workerRunInput{}, err
	}
	// The grant must be for the exact execution the bind reserved; a mismatch is
	// a server inconsistency the control parent must not paper over.
	if grant.Identity.TaskID != task.ID || grant.Identity.RuntimeID != task.RuntimeID || grant.Identity.ExecutionID != identity.ExecutionID || !validExecutionGrant(*grant) {
		return workerRunInput{}, errors.New("execution grant does not match the bind identity")
	}
	wc := buildWorkerContext(d.cfg, task, provider, env, agentEnv)
	return workerRunInput{
		Grant:               *grant,
		CallbackURL:         d.cfg.ServerBaseURL,
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Context:             &wc,
	}, nil
}

// runTaskInWorker composes the full opt-in F3 move-out: it launches a
// per-execution task worker, assembles the exact server-authorized bind request
// (bind + grant + callback + context), and drives one genuine provider run. It is
// the method the hot-path branch will call when a profile opts into the per-
// execution worker; the legacy in-process runner (executeAndDrain) stays the
// default and is never touched here.
//
// The second return value, ran, tells the caller whether the worker actually
// drove a provider run: a refused bind (not launchable) returns ran=false with
// no error so the caller can fall back to the in-process runner; a transport
// failure is uncertain (an error, ran=false) so the caller neither fabricates a
// result nor relaunches. The terminal result the server settles the execution
// from is the worker's own callback (client.Callback), NOT this method: the
// control parent must never also settle, so the caller must not double-settle
// when ran=true.
func (d *Daemon) runTaskInWorker(ctx context.Context, execID string, task Task, provider string, agentEnv map[string]string, env *execenv.Environment, supervisorEpoch int64) (*workerRunResult, bool, error) {
	launch := d.workerProcessLaunch
	if launch == nil {
		launch = d.ensureWorkerProcess
	}
	worker, err := launch(ctx, execID)
	if err != nil {
		return nil, false, err
	}
	defer worker.close()
	in, err := d.assembleWorkerBindInput(ctx, worker, task, provider, agentEnv, env, supervisorEpoch)
	if err != nil {
		return nil, false, err
	}
	result, err := d.runWorkerExecution(ctx, worker, in)
	if err != nil {
		return nil, false, err
	}
	if result.Status == "refused" {
		// A refused bind never ran a provider; the caller may fall back to the
		// in-process runner (which then settles), so this is not an error.
		return result, false, nil
	}
	return result, result.Ran, nil
}

// workerRunDecision is the outcome of attempting the opt-in per-execution task
// worker on the hot path. UseWorkerResult reports that the worker drove a genuine
// provider run and settled the execution via its OWN callback (client.Callback),
// so the control parent must NOT run the provider in-process (executeAndDrain) and
// must NOT settle the task in-process: it returns the projected worker result and
// lets handleTask's final pre-completion check (shouldInterruptAgent) discard the
// redundant settle. A zero value (UseWorkerResult == false) means the control
// parent must fall back to the in-process runner, which settles the execution
// itself -- this is a refused bind (not launchable) or an uncertain transport.
type workerRunDecision struct {
	// UseWorkerResult is true only when the worker ran and settled the execution.
	UseWorkerResult bool
	// Result is the genuine worker result, valid only when UseWorkerResult is true.
	Result *workerRunResult
}

// attemptWorkerRun is the hot-path decision for the opt-in F3 move-out: it runs
// the per-execution worker only when a profile opts in (workerProcessEnabled) and
// the worker drives a genuine, settled provider run. It is the sole hot-path
// caller of runTaskInWorker, so the branch in runTask stays a single, testable
// decision. It never fabricates a result: an uncertain transport or a refused bind
// returns a zero decision (with the transport error, if any) so the caller falls
// back to the in-process runner. The default (gate OFF) path returns a zero
// decision without launching anything.
// acquireControlSupervisor obtains the real control-supervisor epoch the
// opt-in F3 move-out must bind the worker with. The server rejects a bind whose
// supervisor epoch is below 1, so the hot path must acquire a live-held epoch
// BEFORE it launches and binds a worker. One daemon holds one control
// incarnation for its lifetime: the first bind acquires (expected epoch 0)
// and caches the result; later binds reuse the cached epoch rather than churn
// the control generation. A nil seam falls back to d.client.AcquireExecutionSupervisor
// exactly like bindExecution/issueGrant. Any error is returned unwrapped so the
// caller can distinguish ErrExecutionUnsupported (a definitive old-server
// decision) from an uncertain transport failure.
func (d *Daemon) acquireControlSupervisor(ctx context.Context, runtimeID string) (int64, error) {
	d.supervisorMu.Lock()
	defer d.supervisorMu.Unlock()
	if d.supervisorEpoch > 0 {
		return d.supervisorEpoch, nil
	}
	if d.supervisorInstanceID == "" {
		d.supervisorInstanceID = uuid.NewString()
	}
	acquire := d.acquireSupervisor
	if acquire == nil {
		acquire = d.client.AcquireExecutionSupervisor
	}
	resp, err := acquire(ctx, runtimeID, protocol.SupervisorRequest{
		InstanceID:    d.supervisorInstanceID,
		ExpectedEpoch: 0,
	})
	if err != nil {
		return 0, err
	}
	d.supervisorEpoch = resp.Epoch
	return resp.Epoch, nil
}

func (d *Daemon) attemptWorkerRun(ctx context.Context, task Task, provider string, agentEnv map[string]string, env *execenv.Environment) (workerRunDecision, error) {
	if !d.workerProcessEnabled() {
		return workerRunDecision{}, nil
	}
	// The server-side bind rejects a supervisor epoch below 1, so the opt-in
	// move-out must acquire a real control-supervisor epoch BEFORE it binds the
	// worker. An old or drain-only server (one that does not advertise execution
	// reconciliation) returns ErrExecutionUnsupported: that is a DEFINITIVE
	// decision the server cannot bind an execution, so the control parent refuses
	// the worker move-out and falls back to the in-process runner (the plan's
	// "old server -> drain-only" contract). It is a clean fall-back (nil error),
	// never an uncertain one. Any other acquisition error is uncertain (the
	// outcome may already be settled by the worker's callback), so it is surfaced
	// and the pre-completion check reconciles a possible double-settle.
	epoch, err := d.acquireControlSupervisor(ctx, task.RuntimeID)
	if err != nil {
		if errors.Is(err, ErrExecutionUnsupported) {
			return workerRunDecision{}, nil
		}
		return workerRunDecision{}, err
	}
	wr, ran, werr := d.runTaskInWorker(ctx, task.ID, task, provider, agentEnv, env, epoch)
	if werr != nil || !ran {
		// Uncertain transport or a refused bind: the worker did not drive a settled
		// run, so the control parent falls back to the in-process runner (which
		// settles the execution itself). The error is surfaced only when the outcome
		// was genuinely uncertain, so a refused bind is a clean fall-back (nil).
		return workerRunDecision{}, werr
	}
	return workerRunDecision{UseWorkerResult: true, Result: wr}, nil
}

// projectWorkerResult projects a genuine worker run result onto the TaskResult the
// control parent returns. It is a pure, *Daemon-free projection so the hot-path
// branch and its opt-in test share one definition. A nil prepared environment
// yields empty WorkDir/EnvRoot rather than a panic (the branch always passes a
// prepared env; the guard keeps the projection safe to unit-test in isolation).
func projectWorkerResult(wr *workerRunResult, env *execenv.Environment) TaskResult {
	result := TaskResult{
		Status:    wr.Status,
		Comment:   wr.Output,
		SessionID: wr.SessionID,
	}
	if env != nil {
		result.WorkDir = env.WorkDir
		result.EnvRoot = env.RootDir
	}
	return result
}

func (d *Daemon) runWorkerExecution(ctx context.Context, worker *workerProcessClient, in workerRunInput) (*workerRunResult, error) {
	if worker == nil {
		return nil, errors.New("worker process is nil")
	}
	defer worker.close()
	status, err := worker.bind(ctx, in)
	if err != nil {
		return nil, err
	}
	if !status.Launchable {
		return &workerRunResult{Status: "refused", RunError: status.ReasonCode}, nil
	}
	if !worker.ready() {
		return nil, errors.New("worker not ready after a launchable bind")
	}
	return worker.run(ctx)
}
