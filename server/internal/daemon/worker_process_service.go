package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// The worker owns the provider run and all task-scoped callbacks. The control
// parent only supplies the bound execution context and reconciles the durable
// outcome.
type workerProcessService struct {
	bootstrap runtimeproc.Bootstrap

	mu            sync.Mutex
	supplements   taskSupplementSignals
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
	runContext          *workerContext
	lifetime            context.Context
	workCancel          context.CancelFunc
	workDone            chan struct{}
	workResult          *workerRunResult
	workArtifact        *runtimeproc.ReviewArtifactReference
	workError           error
	workPendingResult   *workerRunResult
	workPendingRaw      []byte
	workPersistRetryAt  time.Time
	workPersistFailures int
	executeProvider     func(context.Context, *ExecutionClient, *workerContext) workerRunResult
}

// RunWorkerService starts the worker role and owns its private record until a
// durable stopped receipt. It runs only when a control parent launched it.
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
	s := &workerProcessService{bootstrap: b, state: "starting", lifetime: ctx}
	service, err := runtimeproc.NewService(runtimeproc.Config{
		Bootstrap:        b,
		Capabilities:     []string{"worker.bind", "worker.acquire", "worker.run", "worker.report", "worker.gate-record", "worker.honor-cancel", "worker.cancel", "worker.supplement-wakeup"},
		ReadCapabilities: []string{"worker.status", "worker.gate-status", "worker.result"},
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

// read reports the current decision without changing it. It is a read because a
// lost launch ACK must query the exact recorded decision, never relaunch.
func (s *workerProcessService) read(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Operation == "worker.result" {
		if s.workPendingResult != nil && !time.Now().Before(s.workPersistRetryAt) {
			s.persistWorkerResultLocked()
		}
		if s.workError != nil {
			return nil, &runtimeproc.Error{Code: "result_unavailable", Message: s.workError.Error()}
		}
		result := s.workResult
		if s.workArtifact != nil {
			result = nil
		}
		return marshalRaw(workerResultStatus{ExecutionID: s.identity.ExecutionID, Accepted: s.workDone != nil, Pending: s.workResult == nil, Result: result, Artifact: s.workArtifact}), nil
	}
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
	if err := s.stopProviderRun(ctx); err != nil {
		return err
	}
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
	if strings.TrimRight(callbackURL, "/") == strings.TrimRight(s.bootstrap.Identity.Scope.Backend, "/") {
		origin, err := url.Parse(callbackURL)
		if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Scheme != "http" && origin.Scheme != "https" {
			return nil
		}
	} else {
		if err := validateGatewayForwardTarget(callbackURL); err != nil {
			return nil
		}
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
	RetiredSessionID        string                      `json:"retired_session_id,omitempty"`
	ProviderError           string                      `json:"provider_error,omitempty"`
	InstanceID              string                      `json:"instance_id"`
	PID                     int                         `json:"pid"`
	Ran                     bool                        `json:"ran"`
	Output                  string                      `json:"output,omitempty"`
	SessionID               string                      `json:"session_id,omitempty"`
	Status                  string                      `json:"status,omitempty"`
	ToolCount               int32                       `json:"tool_count"`
	RunError                string                      `json:"run_error,omitempty"`
	BudgetExceeded          bool                        `json:"budget_exceeded"`
	TokenBudgetExceeded     bool                        `json:"token_budget_exceeded"`
	Usage                   []TaskUsageEntry            `json:"usage,omitempty"`
	Execution               *protocol.ExecutionIdentity `json:"execution,omitempty"`
	WorkDir                 string                      `json:"work_dir,omitempty"`
	WorktreeDeliveryPending bool                        `json:"worktree_delivery_pending,omitempty"`
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

func (s *workerProcessService) executeProviderRun(ctx context.Context, client *ExecutionClient, runCtx *workerContext) workerRunResult {
	run := *runCtx
	run.supplements = &s.supplements
	result, tools, retired, runErr := runWorkerProvider(ctx, client, &run)
	status := result.Status
	if runErr != nil {
		status = "failed"
	}
	// Persist the worker's genuine outcome before control adds directory-delivery metadata.
	return workerRunResult{
		InstanceID:       s.bootstrap.Identity.InstanceID,
		PID:              os.Getpid(),
		Ran:              true,
		Output:           result.Output,
		RetiredSessionID: retired, ProviderError: result.Error,
		SessionID:               result.SessionID,
		Status:                  status,
		ToolCount:               tools,
		RunError:                runErrMsg(runErr),
		BudgetExceeded:          result.BudgetExceeded || result.TokenBudgetExceeded,
		TokenBudgetExceeded:     result.TokenBudgetExceeded,
		Usage:                   taskUsageEntries(runCtx.Provider, result.Usage),
		Execution:               &client.identity,
		WorkDir:                 runCtx.Env.WorkDir,
		WorktreeDeliveryPending: runCtx.Env.LocalWorktree,
	}

}
