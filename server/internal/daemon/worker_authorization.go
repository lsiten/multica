package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WorkerLaunchAuthorization is the task worker's decision of whether it may
// launch a provider for its claimed execution. It is the minimum typed boundary
// for the F3 "one execution, one worker" cut. A worker may launch a provider
// only when it holds the exact server-returned execution identity, a valid
// scoped mwt_ grant and a current launch authorization. It fails closed: an
// uncertain bind/Start/launch ACK never authorizes a launch, and the worker must
// query the exact recorded request and worker (same-worker bind replay) rather
// than guess a new worker or relaunch a provider after an uncertain ACK.
//
// This is NOT the hot path. The cross-process worker launch lands in F3/G; this
// contract is the in-process boundary the runtime worker must satisfy before it
// may launch, mirroring budget_admission/replacement_admission.
type WorkerLaunchAuthorization struct {
	// TaskID and RuntimeID bind the authorization to the exact execution.
	TaskID    string
	RuntimeID string
	// WorkerID is the worker's own canonical UUID; it must equal the identity the
	// server bound. A guessed or re-chosen WorkerID is never this authorization.
	WorkerID string
	// ExecutionID is the server-returned ExecutionIdentity. It is validated as a
	// UUID so a worker can never launch on a fabricated or client-chosen id.
	ExecutionID string
	// DispatchedAt is the exact dispatch the bind carries; a zero time is not
	// evidence of a bound execution.
	DispatchedAt time.Time
	// Granted is true only when a valid scoped mwt_ grant for this execution is
	// held. It is evidence, never a remaining-budget or completion promise.
	Granted bool
	// LaunchAuthorized is true only when a current launch authorization is present
	// (never an uncertain bind/Start/launch ACK).
	LaunchAuthorized bool
	// Launchable is the decision: true only when the worker may launch a provider.
	Launchable bool
	// ReasonCode explains a refusal (WorkerAuthReason* closed set); it is empty
	// when Launchable is true. It is evidence, never authorization to launch.
	ReasonCode string
}

// Refusal reasons. They are the closed set the worker emits on refusal; a
// provider must not be launched on any code outside this set.
const (
	// WorkerAuthReasonNoIdentity: no server-returned execution identity.
	WorkerAuthReasonNoIdentity = "execution_identity_missing"
	// WorkerAuthReasonNoGrant: no valid scoped grant is held.
	WorkerAuthReasonNoGrant = "grant_missing"
	// WorkerAuthReasonNoLaunchAuth: no current launch authorization is present.
	WorkerAuthReasonNoLaunchAuth = "launch_authorization_missing"
	// WorkerAuthReasonUncertain: a bind/Start/launch ACK was uncertain; the worker
	// must query the exact recorded request, never launch or guess a new worker.
	WorkerAuthReasonUncertain = "acknowledgement_uncertain"
	// WorkerAuthReasonWorkerMismatch: the worker id does not match the bound
	// identity; guessing a new worker is not allowed.
	WorkerAuthReasonWorkerMismatch = "worker_id_mismatch"
)

// Validate enforces the F3 boundary. It fails closed: an empty task/runtime or
// worker id, an invalid or fabricated execution id, a zero dispatched time, a
// launch that is not fully granted and launch-authorized, a launch that carries
// a refusal reason, a refusal with an unknown reason, or a refusal that still
// holds a valid grant and launch authorization is rejected. An uncertain ACK can
// never authorize a launch.
func (a WorkerLaunchAuthorization) Validate() error {
	if strings.TrimSpace(a.TaskID) == "" {
		return fmt.Errorf("worker launch authorization has no task id")
	}
	if strings.TrimSpace(a.RuntimeID) == "" {
		return fmt.Errorf("worker launch authorization has no runtime id")
	}
	if strings.TrimSpace(a.WorkerID) == "" {
		return fmt.Errorf("worker launch authorization has no worker id")
	}
	if uuid.Validate(a.ExecutionID) != nil {
		return fmt.Errorf("worker launch authorization carries an invalid or fabricated execution id")
	}
	if a.DispatchedAt.IsZero() {
		return fmt.Errorf("worker launch authorization has no bound dispatch time")
	}
	if a.Launchable {
		if !a.Granted || !a.LaunchAuthorized {
			return fmt.Errorf("worker launch is authorized without a valid grant and launch authorization")
		}
		if strings.TrimSpace(a.ReasonCode) != "" {
			return fmt.Errorf("worker launch is authorized but carries a refusal reason %q", a.ReasonCode)
		}
		return nil
	}
	if !knownWorkerAuthReason(a.ReasonCode) {
		return fmt.Errorf("worker launch is refused but carries an unknown reason %q", a.ReasonCode)
	}
	// A reason that asserts a missing fact cannot coexist with that fact being
	// held. An uncertain ACK or a worker-id mismatch may refuse even while a
	// valid grant and launch authorization are held: they override them.
	if a.ReasonCode == WorkerAuthReasonNoGrant && a.Granted {
		return fmt.Errorf("worker launch is refused as %q but holds a valid grant", a.ReasonCode)
	}
	if a.ReasonCode == WorkerAuthReasonNoLaunchAuth && a.LaunchAuthorized {
		return fmt.Errorf("worker launch is refused as %q but holds a valid launch authorization", a.ReasonCode)
	}
	return nil
}

func knownWorkerAuthReason(code string) bool {
	switch code {
	case WorkerAuthReasonNoIdentity, WorkerAuthReasonNoGrant, WorkerAuthReasonNoLaunchAuth,
		WorkerAuthReasonUncertain, WorkerAuthReasonWorkerMismatch:
		return true
	default:
		return false
	}
}

// DecideWorkerLaunch produces one worker launch authorization from the worker's
// held facts. It is NOT the hot path: the cross-process launch lands in F3/G.
// It never launches on an uncertain ACK or on a missing/invalid identity; it sets
// a refusal reason instead so the worker queries the exact recorded request
// rather than guessing a new worker or relaunching. A launch is admitted only
// when the exact server-returned identity, a valid scoped grant and a current
// launch authorization are all present.
func DecideWorkerLaunch(taskID, runtimeID, workerID, executionID string, dispatchedAt time.Time,
	workerIDMatchesBind, granted, launchAuthorized, uncertain bool) WorkerLaunchAuthorization {
	a := WorkerLaunchAuthorization{
		TaskID:           taskID,
		RuntimeID:        runtimeID,
		WorkerID:         workerID,
		ExecutionID:      executionID,
		DispatchedAt:     dispatchedAt,
		Granted:          granted,
		LaunchAuthorized: launchAuthorized,
	}
	// A missing or invalid server-returned identity is the first refusal: the
	// worker may not launch on a client-chosen or fabricated id.
	if uuid.Validate(executionID) != nil || dispatchedAt.IsZero() {
		a.ReasonCode = WorkerAuthReasonNoIdentity
		return a
	}
	// The worker id must match the bound identity; guessing a new worker is not
	// allowed, and same-worker bind replay is the only retry.
	if !workerIDMatchesBind {
		a.ReasonCode = WorkerAuthReasonWorkerMismatch
		return a
	}
	// An uncertain bind/Start/launch ACK never authorizes a launch; the worker
	// must query the exact recorded request and worker instead.
	if uncertain {
		a.ReasonCode = WorkerAuthReasonUncertain
		return a
	}
	if !granted {
		a.ReasonCode = WorkerAuthReasonNoGrant
		return a
	}
	if !launchAuthorized {
		a.ReasonCode = WorkerAuthReasonNoLaunchAuth
		return a
	}
	a.Launchable = true
	return a
}
