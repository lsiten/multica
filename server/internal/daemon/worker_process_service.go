package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// workerProcessService is the per-execution task worker role: one runtimeproc
// service per server-claimed execution. It is the F3 move-out — the process
// boundary is the worker, not the in-process handleTask. It holds only the
// server-returned execution identity, a scoped mwt_ grant, and the launch
// authorization decision; it owns no task business database and no account
// authority. A provider may be launched only when the worker launch-authorization
// contract admits it (DecideWorkerLaunch), so an uncertain bind/Start/launch ACK
// or a worker-id mismatch can never launch a second provider for the same claim.
//
// This is the first F3 cut: the worker process role and its launch-authorization
// gate are real and driven by a control parent over the private runtime channel.
// The provider run (the legacy handleTask/runTask lifecycle) is the next slice and
// stays legacy/opt-in until it has its own runtime acceptance; the legacy
// in-process runner is the default and is never affected here.
type workerProcessService struct {
	bootstrap runtimeproc.Bootstrap

	mu            sync.Mutex
	client        *ExecutionClient // nil until a valid callback url is bound
	identity      protocol.ExecutionIdentity
	authorization WorkerLaunchAuthorization
	gate          *completionGate // the worker-owned completion gate (F3 保留)
	bound         bool
	state         string

	// runContext is the execution context the control parent delivers at bind so
	// the worker can build and launch the provider run without a *Daemon. It is
	// nil until a bind carries the context; a nil runContext means the worker
	// has no context to run a provider (fail-closed: it must not fabricate one).
	runContext *workerContext
}

// RunWorkerService starts the worker role and owns its private record until a
// durable stopped receipt. It runs only when a control parent launched it; the
// legacy in-process runner is the default and is never affected here.
func RunWorkerService(ctx context.Context, b runtimeproc.Bootstrap) error {
	if b.Identity.Scope.Service != "worker" {
		return errors.New("unsupported worker role")
	}
	if err := runtimeproc.PrepareRoot(b.Root); err != nil {
		return err
	}
	lock, err := lockMirrorProcessDomain(filepath.Join(b.Root, "worker-manager.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	s := &workerProcessService{bootstrap: b, state: "starting"}
	service, err := runtimeproc.NewService(runtimeproc.Config{
		Bootstrap:        b,
		Capabilities:     []string{"worker.bind", "worker.acquire", "worker.run", "worker.report", "worker.gate-record", "worker.honor-cancel", "worker.cancel"},
		ReadCapabilities: []string{"worker.status", "worker.gate-status"},
		Handler:          s.mutate,
		ReadHandler:      s.read,
		Ready:            s.ready,
		Shutdown:         s.shutdown,
	})
	if err != nil {
		return err
	}
	return service.Serve(ctx)
}

// ready has no external dependency: a worker is ready as soon as it owns its
// record and can accept a bind. A failed owner lock is reported by NewService.
func (s *workerProcessService) ready(ctx context.Context) error {
	s.mu.Lock()
	if s.state == "starting" {
		s.state = "ready"
	}
	s.mu.Unlock()
	slog.Default().Info("worker ready", "worker", s.bootstrap.Identity.InstanceID)
	return nil
}

// workerBindRequest is the exact facts control delivers before a provider launch.
// The grant is the scoped mwt_ credential; the remaining flags are control's
// observation of the bind/Start/launch acknowledgement. The worker applies the
// closed launch-authorization contract to them and never launches on its own.
type workerBindRequest struct {
	Grant               protocol.ExecutionGrantResponse `json:"grant"`
	CallbackURL         string                          `json:"callback_url"`
	WorkerIDMatchesBind bool                            `json:"worker_id_matches_bind"`
	LaunchAuthorized    bool                            `json:"launch_authorized"`
	Uncertain           bool                            `json:"uncertain"`
	// Context is the execution context the control parent delivers so the worker
	// can build and launch the provider run without a *Daemon. Absent until the
	// control parent supports context delivery; the worker stays launchable for
	// the authorization gate but cannot run a provider without it (fail-closed).
	Context *workerContext `json:"context,omitempty"`
}

// workerContext is the exact facts the control parent delivers so a worker can
// build the provider run (ExecOptions, backend, prompt) and drive
// runProviderExecution. It carries no *Daemon reference and no secret: the
// scoped mwt_ grant is delivered separately as the worker's own credential.
type workerContext struct {
	// Provider is the runtime identity (e.g. "codex", "opencode", "kimi") the
	// control parent delivers so the worker resolves the exact provider it is
	// authorized to run. It is a separate fact from AgentData: the legacy runner
	// receives it as a runTask parameter, and the worker needs the same.
	Provider string
	// Config is the daemon-wide configuration the worker reuses (timeouts,
	// budgets, agent entries) so the launch is assembled identically to the
	// legacy in-process runner.
	Config Config
	// Task is the server-claimed task; it supplies the resume session, thread
	// name, resume continuity, and the prompt brief.
	Task Task
	// Env is the prepared execution environment (workdir, codex home, mcp, and
	// the resolved agent environment) so the backend resolves identically.
	Env PreparedEnv
}

// PreparedEnv is the prepared execution environment delivered to a worker. It is
// the JSON-safe projection of the environment the legacy runner prepares in
// runTask: the workdir, codex home, and the resolved agent environment map.
type PreparedEnv struct {
	WorkDir            string            `json:"work_dir"`
	CodexHome          string            `json:"codex_home"`
	ClaudeSettingsPath string            `json:"claude_settings_path,omitempty"`
	QwenpawWorkspace   string            `json:"qwenpaw_workspace,omitempty"`
	ContextDir         string            `json:"context_dir,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
}

// workerGateRecord is one verification result the worker records into its gate.
type workerGateRecord struct {
	Goal          string             `json:"goal"`
	Criteria      []string           `json:"criteria"`
	Evidence      []string           `json:"evidence"`
	Verdict       string             `json:"verdict"`
	Mode          string             `json:"mode"`
	Calibrated    bool               `json:"calibrated"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// workerCancelState records the worker's cancellation decision: whether control
// has moved the execution to a terminal state and the worker must stop.
type workerCancelState struct {
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid"`
	Cancelled  bool   `json:"cancelled"`
	TaskState  string `json:"task_state"`
	ReasonCode string `json:"reason_code"`
}

// workerReportRequest is a terminal result the worker produces. The operation is
// validated against the closed execution registry by the scoped client; the
// payload is the exact result the server records.
type workerReportRequest struct {
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

// workerStatus is the read-only view of the worker's current decision.
type workerStatus struct {
	InstanceID  string `json:"instance_id"`
	PID         int    `json:"pid"`
	State       string `json:"state"`
	Launchable  bool   `json:"launchable"`
	ReasonCode  string `json:"reason_code"`
	ExecutionID string `json:"execution_id"`
	HasClient   bool   `json:"has_client"`
}

// mutate binds a single execution and cancels it. bind constructs the scoped
// execution client (only from a valid grant) and applies DecideWorkerLaunch so
// the launch decision is made exactly once, fail-closed, in the worker process.
func (s *workerProcessService) mutate(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	switch r.Operation {
	case "worker.bind":
		var in workerBindRequest
		if json.Unmarshal(r.Payload, &in) != nil {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid worker bind"}
		}
		// The grant is evidence, not authorization: a missing/invalid grant is
		// never a remaining-budget or completion promise.
		granted := validExecutionGrant(in.Grant)
		// A callback url must be a plain loopback http origin; a worker that
		// would call a non-loopback host is not bound and cannot launch.
		client := s.executionClient(in.Grant, in.CallbackURL, granted)
		decision := DecideWorkerLaunch(
			in.Grant.Identity.TaskID,
			in.Grant.Identity.RuntimeID,
			in.Grant.Identity.WorkerID,
			in.Grant.Identity.ExecutionID,
			in.Grant.Identity.DispatchedAt,
			in.WorkerIDMatchesBind,
			granted,
			in.LaunchAuthorized,
			in.Uncertain,
		)
		if err := decision.Validate(); err != nil {
			return nil, &runtimeproc.Error{Code: "invalid_authorization", Message: err.Error()}
		}
		s.mu.Lock()
		s.client = client
		s.identity = in.Grant.Identity
		s.authorization = decision
		s.gate = newCompletionGate(in.Grant.Identity.TaskID)
		s.runContext = in.Context
		s.bound = true
		if s.state == "starting" {
			s.state = "ready"
		}
		s.mu.Unlock()
		return marshalRaw(workerStatus{
			InstanceID:  s.bootstrap.Identity.InstanceID,
			PID:         os.Getpid(),
			State:       s.state,
			Launchable:  decision.Launchable,
			ReasonCode:  decision.ReasonCode,
			ExecutionID: in.Grant.Identity.ExecutionID,
			HasClient:   client != nil,
		}), nil
	case "worker.acquire":
		s.mu.Lock()
		client := s.client
		s.mu.Unlock()
		if client == nil {
			return nil, &runtimeproc.Error{Code: "no_client", Message: "worker has no bound execution client; bind first"}
		}
		// A live callback to control is real work: the worker confirms the
		// execution is still current before it would launch a provider. An
		// uncertain/failed query is never an acquisition.
		var live struct {
			State string `json:"state"`
		}
		if err := client.Status(ctx, &live); err != nil {
			return nil, &runtimeproc.Error{Code: "acquire_failed", Message: "execution status query failed: " + err.Error()}
		}
		return marshalRaw(map[string]any{
			"acquired":     true,
			"execution_id": s.identity.ExecutionID,
			"state":        live.State,
		}), nil
	case "worker.report":
		s.mu.Lock()
		client := s.client
		launchable := s.authorization.Launchable
		s.mu.Unlock()
		if client == nil {
			return nil, &runtimeproc.Error{Code: "no_client", Message: "worker has no bound execution client; bind first"}
		}
		// A worker that is not launchable never ran a provider, so it must not
		// produce a result the server would record as real work.
		if !launchable {
			return nil, &runtimeproc.Error{Code: "not_launchable", Message: "worker is not launchable; it produced no result"}
		}
		var in workerReportRequest
		if json.Unmarshal(r.Payload, &in) != nil || in.Operation == "" {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid worker report"}
		}
		if err := client.Callback(ctx, in.Operation, in.Payload, nil); err != nil {
			return nil, &runtimeproc.Error{Code: "report_failed", Message: "result report failed: " + err.Error()}
		}
		return json.RawMessage(`{"reported":true}`), nil
	case "worker.gate-record":
		var in workerGateRecord
		if json.Unmarshal(r.Payload, &in) != nil {
			return nil, &runtimeproc.Error{Code: "malformed", Message: "invalid worker gate record"}
		}
		s.mu.Lock()
		gate := s.gate
		s.mu.Unlock()
		if gate == nil {
			return nil, &runtimeproc.Error{Code: "no_gate", Message: "worker has no bound completion gate; bind first"}
		}
		// The worker owns the completion gate: a verification result is recorded
		// only for the bound task and only advances the current attempt, so a
		// stale provider response cannot restore a successful gate (F3 保留).
		gate.recordForAttemptWithDetails(0, s.identity.TaskID, in.Goal, in.Criteria, in.Evidence, in.Verdict, in.Mode, in.Calibrated, in.Confidence, in.Probabilities)
		return marshalRaw(gate.status()), nil
	case "worker.honor-cancel":
		// A provider that is not launchable never started, so there is nothing
		// to cancel; honor-cancel must not fabricate a cancellation.
		s.mu.Lock()
		client := s.client
		launchable := s.authorization.Launchable
		s.mu.Unlock()
		if client == nil {
			return nil, &runtimeproc.Error{Code: "no_client", Message: "worker has no bound execution client; bind first"}
		}
		if !launchable {
			return marshalRaw(workerCancelState{
				InstanceID: s.bootstrap.Identity.InstanceID,
				PID:        os.Getpid(),
				Cancelled:  false,
				ReasonCode: "not_launchable",
			}), nil
		}
		// Query the execution's authoritative state from control. A terminal state
		// means the execution is cancelled/failed/completed server-side; the
		// worker must stop and may not relaunch or report a result it did not run.
		var live struct {
			State string `json:"state"`
		}
		if err := client.Status(ctx, &live); err != nil {
			// An uncertain query is never a cancellation: the worker keeps the
			// execution and must not treat a failed query as a stop signal.
			return nil, &runtimeproc.Error{Code: "cancel_query_failed", Message: "cancellation query uncertain: " + err.Error()}
		}
		cancelled := isAgentTaskTerminal(live.State)
		if cancelled {
			s.mu.Lock()
			s.client = nil
			s.authorization = WorkerLaunchAuthorization{}
			s.gate = nil
			s.bound = false
			s.state = "cancelled"
			s.mu.Unlock()
		}
		return marshalRaw(workerCancelState{
			InstanceID: s.bootstrap.Identity.InstanceID,
			PID:        os.Getpid(),
			Cancelled:  cancelled,
			TaskState:  live.State,
			ReasonCode: live.State,
		}), nil
	case "worker.cancel":
		s.mu.Lock()
		s.client = nil
		s.identity = protocol.ExecutionIdentity{}
		s.authorization = WorkerLaunchAuthorization{}
		s.gate = nil
		s.bound = false
		s.mu.Unlock()
		return json.RawMessage(`{}`), nil
	case "worker.run":
		// A worker runs the provider only when control admitted the launch,
		// delivered the execution context, and bound a scoped client. A missing
		// context or client fails closed: the worker must never fabricate a run.
		s.mu.Lock()
		client := s.client
		launchable := s.authorization.Launchable
		runCtx := s.runContext
		s.mu.Unlock()
		if !launchable {
			return nil, &runtimeproc.Error{Code: "not_launchable", Message: "worker is not launchable; run refused"}
		}
		if client == nil {
			return nil, &runtimeproc.Error{Code: "no_client", Message: "worker has no bound execution client; bind first"}
		}
		if runCtx == nil {
			return nil, &runtimeproc.Error{Code: "no_context", Message: "worker has no execution context; run refused"}
		}
		// Assemble the exact provider run the legacy in-process runner would:
		// backend + ExecOptions + prompt, resolved identically through the shared
		// launch seam. A missing provider/agent or an unresolved backend refuses
		// the run instead of fabricating one.
		run, err := buildWorkerProviderRun(*runCtx, runCtx.Provider, slog.Default(), workerApproveAll, agent.LocalProcessLauncher{})
		if err != nil {
			return nil, &runtimeproc.Error{Code: "launch_failed", Message: "worker could not build provider run: " + err.Error()}
		}
		// Genuinely run the provider: create the phase recorder (runProviderExecution
		// dereferences it, so it must be non-nil), wire the *Daemon-free seams from
		// the scoped client, and drive the shared provider run the legacy in-process
		// runner calls. Nil budget is tolerated by runProviderExecution.
		var msgSeq atomic.Int32
		phaseRecorder := newTaskPhaseRecorder(slog.Default(), time.Now)
		result, tools, runErr := executePreparedProvider(
			ctx, run.Backend, run.Prompt, run.Options, slog.Default(), runCtx.Task.ID, runCtx.Env.CodexHome, &msgSeq, nil, phaseRecorder,
			func(c context.Context, taskID string, messages []TaskMessageData) error {
				return client.ReportTaskMessages(c, messages)
			},
			func(c context.Context, taskID, sessionID, workDir string) error {
				return client.PinTaskSession(c, sessionID, workDir)
			},
			workerSupplementSubscribe(),
			func(c context.Context, taskID string) (*TaskSupplement, error) { return client.ClaimSupplement(c) },
			func(c context.Context, taskID, commentID string, delivered bool, reason string) error {
				return client.AckSupplement(c, commentID, delivered, reason)
			},
			defaultTaskSupplementReadyInterval, defaultTaskSupplementPollInterval,
			runCtx.Config.AgentIdleWatchdog, runCtx.Config.AgentToolWatchdog, runCtx.Config.AgentStartupTimeout,
		)
		// Report the terminal result through the scoped client so the server
		// settles the execution. A lost report is owned by the server's terminal
		// report outbox: the worker never relaunches or fabricates a result to
		// recover it, so a report failure is logged, never faked as success.
		if reportErr := s.reportWorkerRun(ctx, client, result, runErr); reportErr != nil {
			slog.Default().Warn("worker terminal result report failed", "worker", s.bootstrap.Identity.InstanceID, "error", reportErr.Error())
		}
		return marshalRaw(workerRunResult{
			InstanceID:     s.bootstrap.Identity.InstanceID,
			PID:            os.Getpid(),
			Ran:            true,
			Output:         result.Output,
			SessionID:      result.SessionID,
			Status:         result.Status,
			ToolCount:      tools,
			RunError:       runErrMsg(runErr),
			BudgetExceeded: result.BudgetExceeded || result.TokenBudgetExceeded,
		}), nil

	default:
		return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown worker operation"}
	}
}

// read reports the current decision without changing it. It is a read because a
// lost launch ACK must query the exact recorded decision, never relaunch.
func (s *workerProcessService) read(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Operation == "worker.gate-status" {
		if s.gate == nil {
			return marshalRaw(completionVerification{Reason: "completion_verifier_unavailable"}), nil
		}
		return marshalRaw(s.gate.status()), nil
	}
	return marshalRaw(workerStatus{
		InstanceID:  s.bootstrap.Identity.InstanceID,
		PID:         os.Getpid(),
		State:       s.state,
		Launchable:  s.authorization.Launchable,
		ReasonCode:  s.authorization.ReasonCode,
		ExecutionID: s.identity.ExecutionID,
		HasClient:   s.client != nil,
	}), nil
}

// shutdown releases the bound client and grant so a stopped worker holds no
// live credential; it is a domain cleanup, never a provider launch.
func (s *workerProcessService) shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.client = nil
	s.identity = protocol.ExecutionIdentity{}
	s.authorization = WorkerLaunchAuthorization{}
	s.bound = false
	s.state = "stopped"
	s.mu.Unlock()
	return nil
}

// executionClient builds the scoped transport only from a valid grant. A missing
// or invalid grant, or a non-loopback callback origin, yields a nil client so a
// worker that cannot reach control cannot claim to be ready to launch.
func (s *workerProcessService) executionClient(grant protocol.ExecutionGrantResponse, callbackURL string, granted bool) *ExecutionClient {
	if !granted || callbackURL == "" {
		return nil
	}
	if err := validateGatewayForwardTarget(callbackURL); err != nil {
		return nil
	}
	client, err := NewExecutionClient(callbackURL, grant)
	if err != nil {
		return nil
	}
	return client
}

// workerRunResult is the outcome of a genuine worker provider run. Ran records
// that the worker actually drove the shared provider run (not a fabricated
// success); the terminal result the server settles the execution from is the
// one executePreparedProvider produced through runProviderExecution.
type workerRunResult struct {
	InstanceID     string `json:"instance_id"`
	PID            int    `json:"pid"`
	Ran            bool   `json:"ran"`
	Output         string `json:"output,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Status         string `json:"status,omitempty"`
	ToolCount      int32  `json:"tool_count"`
	RunError       string `json:"run_error,omitempty"`
	BudgetExceeded bool   `json:"budget_exceeded"`
}

// runErrMsg renders a run error as a string for the result payload; an empty
// run error yields an empty string so a clean run reports no error.
func runErrMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// workerApproveAll is the *Daemon-free approval the worker supplies to the
// resolved backend. The worker is launched only after control admitted the
// launch (DecideWorkerLaunch), so it has no in-process mirror reviewer to defer
// to; it approves the exact requested operation rather than blocking a run
// control already authorized. A later slice may route it to control.
func workerApproveAll(ctx context.Context, request agent.ApprovalRequest) (bool, error) {
	return true, nil
}

// reportWorkerRun reports the terminal result of a genuine worker provider run
// through the scoped client. It is best-effort: the server's terminal report
// outbox owns retry, so a lost report is never faked as success and the worker
// never relaunches to recover it. A run that errored before a usable result
// posts the fail boundary; otherwise the complete boundary.
func (s *workerProcessService) reportWorkerRun(ctx context.Context, client *ExecutionClient, result agent.Result, runErr error) error {
	if runErr != nil {
		return client.Callback(ctx, "fail", map[string]any{"error": runErr.Error()}, nil)
	}
	return client.Callback(ctx, "complete", map[string]any{"output": result.Output}, nil)
}
