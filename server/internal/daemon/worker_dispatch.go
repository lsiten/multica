package daemon

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"time"
)

// buildWorkerContext assembles the execution context the control parent delivers
// to a per-execution task worker. The parent hands over the daemon config,
// claimed task, prepared environment, and assembled agent environment; the child
// resolves the backend and ExecOptions through buildWorkerProviderRun.
//
// The agent environment is assembled by the caller (runTask) and passed in, so this
// function reads no daemon state and mutates nothing: it is a pure projection. A
// nil prepared environment yields an empty projection rather than a panic, so a
// worker that cannot resolve its environment fails closed through
// buildWorkerProviderRun instead of a nil dereference.
func buildWorkerContext(cfg Config, task Task, provider string, env *execenv.Environment, agentEnv map[string]string, launchFacts ...workerLaunchFacts) workerContext {
	prepared := PreparedEnv{Env: agentEnv}
	if env != nil {
		prepared.WorkDir = env.WorkDir
		prepared.PiSkillPaths = env.PiSkillPaths
		prepared.HermesHome = env.HermesHome
		prepared.Delivery = env.LocalWorktree.Inspection()
		prepared.RootDir = env.RootDir
		prepared.LocalWorktree = env.LocalWorktree != nil
		prepared.CodexHome = env.CodexHome
		prepared.ClaudeSettingsPath = env.ClaudeSettingsPath
		prepared.QwenpawWorkspace = env.QwenpawWorkspace
		prepared.ContextDir = env.ContextDir
		prepared.CursorMcpPluginDir = env.CursorMcpPluginDir
		prepared.OmpMcpExtensionDir = env.OmpMcpExtensionDir
	}
	context := workerContext{
		Provider: provider,
		Config:   cfg,
		Task:     task,
		Env:      prepared,
	}
	if len(launchFacts) > 0 {
		context.Launch = launchFacts[0]
	}
	return context
}

// workerProcessEnabled reports whether the mandatory per-execution worker can
// launch. The only remaining prerequisite is the pinned daemon executable;
// there is no opt-in flag or in-process execution mode.
func (d *Daemon) workerProcessEnabled() bool {
	return d.cfg.NativeHostExecutable != ""
}

// runWorkerExecution is the Daemon-coupled orchestration of one per-execution
// task worker: given an already-launched child, it binds
// the exact execution facts + context, drives one genuine provider run, then
// stops the child. The launch itself happens in the caller
// (runTaskInWorker / ensureWorkerProcess) so the server-side bind can use
// the worker's instance id before the bind.
//
// Settlement is deliberately the worker's own: the worker posts the terminal
// result through its scoped callback (client.Callback), so the control parent
// must never report a terminal result for this task. Doing so would
// double-settle the execution against the server's already-terminal
// idempotency and a conflicting-authority 409. A refused bind is a definitive
// admission failure; a transport failure is uncertain (returned, never a
// fabricated result); a launchable-but-not-ready worker fails closed.
// workerExecutionOperations is the closed set of task-local callback operations
// a per-execution worker is granted: it produces the terminal result, usage,
// transcript messages, session pin, supplement claim/ack, prepare-lease renewal
// and a status query. It carries no control/gateway authority.
func workerExecutionOperations() []string {
	return []string{"status", "start", "prepare-lease", "progress", "session", "complete", "fail", "usage", "context-receipt", "messages", "supplements/claim", "supplements/ack", "worktree-delivery"}
}

// assembleWorkerBindInput assembles the closed bind request the control parent
// hands a per-execution task worker, from the runTask locals plus the
// server-side execution bind and grant. It is the contract prerequisite of the
// control binds the exact execution identity server-side
// (the worker's own instance id), issues the scoped credential, projects the
// execution context (buildWorkerContext), and hands the worker the exact facts
// it applies its closed launch-authorization contract to. The child worker is
// the execution owner; the parent never runs a provider itself.
//
// It fails closed on any server-side bind/grant error (a bind or grant that
// cannot be confirmed is never turned into a launch) and never fabricates an
// identity: the returned workerRunInput carries the exact server-returned grant
// and the worker's own instance id, so the worker's launch decision is applied
// to real, server-authorized facts. A nil worker (or a worker without an
// instance id) cannot be bound and fails closed rather than guessing.
func (d *Daemon) assembleWorkerBindInput(ctx context.Context, worker *workerProcessClient, task Task, provider string, agentEnv map[string]string, env *execenv.Environment, supervisorEpoch int64, launchFacts ...workerLaunchFacts) (workerRunInput, error) {
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
	wc := buildWorkerContext(d.cfg, task, provider, env, agentEnv, launchFacts...)
	return workerRunInput{
		Grant:               *grant,
		CallbackURL:         d.cfg.ServerBaseURL,
		WorkerIDMatchesBind: true,
		LaunchAuthorized:    true,
		Context:             &wc,
	}, nil
}

// runTaskInWorker composes the full per-execution worker flow: it launches a
// per-execution task worker, assembles the exact server-authorized bind request
// (bind + grant + callback + context), and drives one genuine provider run. It is
// the method the task hot path calls; there is no in-process provider fallback.
//
// The second return value, ran, tells the caller whether the worker actually
// drove a provider run. A refused bind is a terminal worker admission failure;
// a transport failure is uncertain, so the caller neither fabricates a result
// nor relaunches. The terminal result the server settles the execution
// from is the worker's own callback (client.Callback), NOT this method: the
// control parent must never also settle, so the caller must not double-settle
// when ran=true.
func (d *Daemon) runTaskInWorker(ctx context.Context, execID string, task Task, provider string, agentEnv map[string]string, env *execenv.Environment, supervisorEpoch int64, launchFacts ...workerLaunchFacts) (*workerRunResult, bool, error) {
	launch := d.workerProcessLaunch
	if launch == nil {
		launch = d.ensureWorkerProcess
	}
	worker, err := launch(ctx, execID)
	if err != nil {
		return nil, false, err
	}
	d.registerWorkerProcess(worker, execID, task.ID, provider)
	defer worker.close()
	in, err := d.assembleWorkerBindInput(ctx, worker, task, provider, agentEnv, env, supervisorEpoch, launchFacts...)
	if err != nil {
		return nil, false, err
	}
	result, err := d.runWorkerExecution(ctx, worker, in)
	if err != nil {
		return nil, false, err
	}
	if result.Status == "refused" {
		return result, false, errors.New("task worker refused execution admission")
	}
	return result, true, nil
}

// workerRunDecision is the outcome of attempting the per-execution task worker on
// the hot path. UseWorkerResult reports that the worker drove a genuine
// provider run and settled the execution via its OWN callback (client.Callback),
// so the control parent must not run or settle the provider itself: it returns the
// projected worker result and
// lets handleTask's final pre-completion check (shouldInterruptAgent) discard the
// redundant settle. A zero value means the worker produced no result and the
// caller must fail the execution without retrying in another owner.
type workerRunDecision struct {
	// UseWorkerResult is true only when the worker ran and settled the execution.
	UseWorkerResult bool
	// Result is the genuine worker result, valid only when UseWorkerResult is true.
	Result *workerRunResult
}

// attemptWorkerRun is the hot-path decision for the per-execution worker: it
// launches the child, binds the execution, and
// the worker drives a genuine, settled provider run. It is the sole hot-path
// caller of runTaskInWorker, so the branch in runTask stays a single, testable
// decision. It never fabricates a result: an uncertain transport or a refused bind
// returns an error for any admission, transport, or execution failure. There is
// no gate-off path in the production lifecycle.
// acquireControlSupervisor obtains the real control-supervisor epoch the
// worker must bind with. The server rejects a bind whose
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

func (d *Daemon) attemptWorkerRun(ctx context.Context, task Task, provider string, agentEnv map[string]string, env *execenv.Environment, launchFacts ...workerLaunchFacts) (workerRunDecision, error) {
	if !d.workerProcessEnabled() {
		return workerRunDecision{}, errors.New("required task worker executable is not configured")
	}
	// The server-side bind requires a real control-supervisor epoch. A server
	// without execution reconciliation is incompatible with the mandatory
	// multi-process execution contract and must fail explicitly.
	epoch, err := d.acquireControlSupervisor(ctx, task.RuntimeID)
	if err != nil {
		if errors.Is(err, ErrExecutionUnsupported) {
			return workerRunDecision{}, fmt.Errorf("multi-process execution reconciliation is required: %w", err)
		}
		return workerRunDecision{}, err
	}
	wr, ran, werr := d.runTaskInWorker(ctx, task.ID, task, provider, agentEnv, env, epoch, launchFacts...)
	if werr != nil {
		return workerRunDecision{}, werr
	}
	if !ran {
		return workerRunDecision{}, errors.New("task worker did not execute")
	}
	return workerRunDecision{UseWorkerResult: true, Result: wr}, nil
}

// projectWorkerResult retains worker metadata on launch or transport errors.
func projectWorkerResult(wr *workerRunResult, env *execenv.Environment) TaskResult {
	result := TaskResult{
		Status:           wr.Status,
		RetiredSessionID: wr.RetiredSessionID,
		Comment:          wr.Output,
		SessionID:        wr.SessionID,
		Usage:            wr.Usage,
		Execution:        wr.Execution,
	}
	if wr.RunError != "" {
		result.Comment = wr.RunError
		result.FailureReason = taskRunFailureReason(errors.New(wr.RunError))
	}
	if wr.ProviderError != "" {
		result.Comment = wr.ProviderError
	}
	if wr.Status == "cleanup_unconfirmed" {
		result.Status = "failed"
		result.FailureReason = "cleanup_unconfirmed"
	}
	if wr.Status != "cleanup_unconfirmed" && (wr.BudgetExceeded || wr.TokenBudgetExceeded) {
		result.Status, result.FailureReason, result.Comment = "blocked", "budget_exceeded", "Task stopped after reported provider usage reached its autonomy cost limit."
		if wr.TokenBudgetExceeded {
			result.FailureReason, result.Comment = "token_budget_exceeded", "Task stopped after reported token usage reached its autonomy limit."
		}
	}
	if env != nil {
		result.WorkDir = env.WorkDir
		result.EnvRoot = env.RootDir
	}
	return result
}

func recordWorkerUsage(budget *taskUsageBudget, entries []TaskUsageEntry) {
	if budget == nil {
		return
	}
	providers := map[string]map[string]agent.TokenUsage{}
	for _, entry := range entries {
		usage := providers[entry.Provider]
		if usage == nil {
			usage = map[string]agent.TokenUsage{}
			providers[entry.Provider] = usage
		}
		usage[entry.Model] = addTaskUsage(usage[entry.Model], agent.TokenUsage{InputTokens: entry.InputTokens, OutputTokens: entry.OutputTokens, CacheReadTokens: entry.CacheReadTokens, CacheWriteTokens: entry.CacheWriteTokens, CostUSDTicks: entry.CostUSDTicks})
	}
	for provider, usage := range providers {
		budget.RecordSnapshot("worker:"+provider, provider, usage)
	}
}
