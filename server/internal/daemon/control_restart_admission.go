package daemon

import (
	"fmt"
	"strings"
)

// RetainedRestartAdmission is control's decision of whether it may perform a
// capability-replacing restart while preserving the retained execution. It is
// the minimum typed boundary for the G "per-capability upgrade with retained
// execution" cut. A control restart must preserve legitimate executions: it
// never treats a merely suspect (unconfirmed-stopped) instance as stopped,
// never replaces an old/drain-only server's capability as if it were stopped,
// and never zeros the retained active_task_count by moving a worker out.
//
// This is NOT the hot path. The actual cross-process control restart lands in
// F3/G; this contract is the in-process boundary a restart must satisfy before
// it may replace a capability, mirroring budget_admission/replacement_admission
// /worker_authorization.
type RetainedRestartAdmission struct {
	// Service is the capability being replaced (for example "gateway",
	// "application", "environment"); it binds the decision to the exact
	// capability, never the whole daemon.
	Service string
	// ServerSupportsReconcile is true only when the server supports execution
	// reconciliation. On an old/drain-only server a capability-replacing restart
	// must be refused: the caller keeps drain-before-restart (ErrExecutionUnsupported).
	ServerSupportsReconcile bool
	// ConfirmedStopped is true only when the old instance has actually been
	// confirmed stopped (a real stopped receipt, not a suspect/unknown probe).
	// A suspect or unconfirmed instance must never be treated as stopped.
	ConfirmedStopped bool
	// Retained is true only when the retained execution is preserved (the
	// active task count is not zeroed by moving a worker out). A restart that
	// would lose a live execution is refused.
	Retained bool
	// Admitted is the decision: true permits the capability-replacing restart
	// while retaining the execution, false keeps the old instance draining.
	Admitted bool
	// ReasonCode explains a refusal (RetainedRestartReason* closed set); it is
	// empty when Admitted is true. It is evidence, never authorization.
	ReasonCode string
}

// Refusal reasons. They are the closed set control emits on refusal; a
// capability-replacing restart must not be admitted on any code outside it.
const (
	// RetainedRestartReasonDrainOnly: the server is old/drain-only (no execution
	// reconciliation); a replacing restart must drain before restart.
	RetainedRestartReasonDrainOnly = "server_drain_only"
	// RetainedRestartReasonSuspect: the old instance is suspect/unknown (not
	// confirmed stopped); it must not be treated as stopped or replaced.
	RetainedRestartReasonSuspect = "instance_suspect"
	// RetainedRestartReasonActiveTasks: the retained execution cannot be
	// preserved; the restart would zero the active task count, so it is refused.
	RetainedRestartReasonActiveTasks = "execution_not_retained"
)

// Validate enforces the G boundary. It fails closed: an empty service id, a
// restart admitted without all three preconditions (a reconciling server, a
// confirmed-stopped old instance and a preserved retained execution), an admitted
// restart that carries a refusal reason, a refusal with an unknown reason, or a
// refusal whose reason contradicts the held facts is rejected. A suspect
// instance can never be admitted as stopped.
func (a RetainedRestartAdmission) Validate() error {
	if strings.TrimSpace(a.Service) == "" {
		return fmt.Errorf("retained restart admission has no service")
	}
	if a.Admitted {
		if !a.ServerSupportsReconcile || !a.ConfirmedStopped || !a.Retained {
			return fmt.Errorf("retained restart admitted without all preconditions (reconcile, confirmed-stopped, retained execution)")
		}
		if strings.TrimSpace(a.ReasonCode) != "" {
			return fmt.Errorf("retained restart admitted but carries a refusal reason %q", a.ReasonCode)
		}
		return nil
	}
	if !knownRetainedRestartReason(a.ReasonCode) {
		return fmt.Errorf("retained restart is refused but carries an unknown reason %q", a.ReasonCode)
	}
	if a.ReasonCode == RetainedRestartReasonDrainOnly && a.ServerSupportsReconcile {
		return fmt.Errorf("retained restart is refused as %q but the server supports reconciliation", a.ReasonCode)
	}
	if a.ReasonCode == RetainedRestartReasonSuspect && a.ConfirmedStopped {
		return fmt.Errorf("retained restart is refused as %q but the old instance is confirmed stopped", a.ReasonCode)
	}
	if a.ReasonCode == RetainedRestartReasonActiveTasks && a.Retained {
		return fmt.Errorf("retained restart is refused as %q but the execution is retained", a.ReasonCode)
	}
	return nil
}

func knownRetainedRestartReason(code string) bool {
	switch code {
	case RetainedRestartReasonDrainOnly, RetainedRestartReasonSuspect, RetainedRestartReasonActiveTasks:
		return true
	default:
		return false
	}
}

// AdmitRetainedRestart produces one retained-restart admission from control's
// held facts. It is NOT the hot path: the cross-process restart lands in F3/G.
// It never admits on an old/drain-only server (drain-before-restart), never
// admits a suspect (unconfirmed-stopped) instance, and never admits a restart
// that would lose the retained execution (zero the active task count). A
// replacement is admitted only on a reconciling server, with a confirmed-stopped
// old instance and a preserved retained execution.
func AdmitRetainedRestart(service string, serverSupportsReconcile, confirmedStopped, retainedExecution bool) RetainedRestartAdmission {
	a := RetainedRestartAdmission{
		Service:                 service,
		ServerSupportsReconcile: serverSupportsReconcile,
		ConfirmedStopped:        confirmedStopped,
		Retained:                retainedExecution,
	}
	// An old/drain-only server (no execution reconciliation) must keep
	// drain-before-restart; a capability-replacing restart is refused.
	if !serverSupportsReconcile {
		a.ReasonCode = RetainedRestartReasonDrainOnly
		return a
	}
	// A suspect or unconfirmed-stopped instance must never be treated as stopped
	// or replaced; only a confirmed-stopped old instance may be replaced.
	if !confirmedStopped {
		a.ReasonCode = RetainedRestartReasonSuspect
		return a
	}
	// A restart that would lose the retained execution (zero the active task
	// count) is refused rather than silently dropping a live worker.
	if !retainedExecution {
		a.ReasonCode = RetainedRestartReasonActiveTasks
		return a
	}
	a.Admitted = true
	return a
}
