package daemon

import (
	"context"
	"encoding/json"
	"github.com/multica-ai/multica/server/internal/runtimeproc"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"os"
)

// mutate binds a single execution and cancels it. bind constructs the scoped
// execution client (only from a valid grant) and applies DecideWorkerLaunch so
// the launch decision is made exactly once, fail-closed, in the worker process.
func (s *workerProcessService) mutate(ctx context.Context, r runtimeproc.Request) (json.RawMessage, *runtimeproc.Error) {
	switch r.Operation {
	case "worker.supplement-wakeup":
		return s.wakeSupplement(r.Payload)
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
		if client != nil && in.Context != nil && in.Context.Config.WorkspacesRoot != "" {
			store := newTerminalReportStore(in.Context.Config)
			store.bindAccount(s.bootstrap.Identity.Scope.Account)
			identity := in.Grant.Identity
			client.client.taskReportQueue = func(report terminalTaskReport) error {
				report.execution = &identity
				report.accountID = s.bootstrap.Identity.Scope.Account
				return store.enqueue(report)
			}
		}
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
		if (s.bound || s.workDone != nil) && s.identity != in.Grant.Identity {
			s.mu.Unlock()
			return nil, &runtimeproc.Error{Code: "execution_changed", Message: "worker cannot bind a different execution"}
		}
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
		if err := s.stopProviderRun(ctx); err != nil {
			return nil, &runtimeproc.Error{Code: "stop_unconfirmed", Message: err.Error()}
		}
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
		s.startProviderRun(client, runCtx)
		return json.RawMessage(`{"accepted":true}`), nil

	default:
		return nil, &runtimeproc.Error{Code: "unknown_operation", Message: "unknown worker operation"}
	}
}
