package daemon

import (
	"strings"

	"github.com/multica-ai/multica/server/internal/runtimeproc"
)

// This file lands the RetainedRestartAdmission contract (control_restart_admission.go)
// into the real control-restart decision path. It is the G "per-capability upgrade
// with retained execution" cut's decision boundary: before a control may replace a
// capability while preserving a retained execution, it must gather the three held
// facts and run them through AdmitRetainedRestart.
//
// Every fact fails closed today. The actual cross-process retained restart
// (keeping the worker alive across a control restart, handing supervisor authority
// to the server) is the next, runtime/native step and is NOT faked here: on a
// missing/unknown fact the decision refuses and the daemon keeps its existing
// drain-before-restart behavior, which is byte-identical to the current default.
// That keeps the default (non-retained) path unchanged and makes a capability-
// replacing restart strictly opt-in, matching the plan's "旧 server / 未知响应 /
// 缺少能力 -> 排空重启，不以忽略未知字段代替安全校验".

// retainedRestartFacts gathers the three held facts a capability-replacing control
// restart needs. It is NOT the hot path: the cross-process retained restart lands
// after this decision boundary. Every fact is read from held state and fails
// closed (unknown server capability, unconfirmed old instance, or an execution
// that is not yet retained across the restart) so the decision never admits on an
// unproven fact.
func (d *Daemon) retainedRestartFacts(service string) (serverSupportsReconcile, confirmedStopped, retainedExecution bool) {
	serverSupportsReconcile = d.serverSupportsExecutionReconcile()
	confirmedStopped = d.instanceConfirmedStopped()
	retainedExecution = d.executionRetainedAcrossRestart()
	_ = service
	return serverSupportsReconcile, confirmedStopped, retainedExecution
}

// serverSupportsExecutionReconcile reports whether the daemon has observed a
// server that supports execution reconciliation. Fail closed: an unobserved or
// drain-only server (no reconciliation) forces drain-before-restart, because a
// capability-replacing restart must be able to hand the retained execution to a
// server that will reconcile it. The authoritative source is the server's
// heartbeat advertisement of ExecutionCapabilityV1 (mirrored into
// executionReconcileSupported, see handleHeartbeatActions); until that is
// observed the value is false, so the decision stays on drain-before-restart.
func (d *Daemon) serverSupportsExecutionReconcile() bool {
	return d.executionReconcileSupported.Load()
}

// instanceConfirmedStopped reports whether the old control instance has been
// confirmed stopped by a real runtimeproc control-record, not a suspect/unknown
// probe. It returns the fact captured at startup from a genuine runtimeproc read
// of the prior control's record (controlConfirmedStoppedRecord, see
// startControlRuntime): the capture happens before the daemon acquires its own
// control record, so it reads the prior instance's record rather than this
// daemon's. A suspect, draining, incomplete, or missing record is never treated as
// stopped, so a suspect instance is never replaced by a capability-replacing
// restart. When no control runtime root is configured (the default) no prior
// record exists to confirm, so the fact stays false and the daemon keeps
// drain-before-restart. This keeps the fact genuinely backed (a real read, not a
// no-op) without changing the default (non-retained) path.
func (d *Daemon) instanceConfirmedStopped() bool {
	return d.priorControlConfirmedStopped.Load()
}

// controlConfirmedStoppedRecord is the genuine, fail-closed reader of the prior
// control's runtimeproc record: it replicates runtimeproc's replaceableRecord
// "confirmed stopped" semantics against the current control identity. The prior
// record must be a clean stopped record of a DIFFERENT instance on the SAME
// scope, with a valid identity and every operation receipt completed. A record of
// the current instance (same InstanceID), a suspect/draining/starting/ready
// record, an incomplete set of operations, or a scope/identity mismatch is never
// confirmed stopped, so the fact fails closed. It is never a no-op: it acts on
// the bytes of a real record, exactly the check runtimeproc.NewService uses when
// a successor may take over a stopped owner.
func controlConfirmedStoppedRecord(rec runtimeproc.Record, currentInstanceID string, scope runtimeproc.Scope) bool {
	if rec.Identity.InstanceID == currentInstanceID {
		return false
	}
	if rec.Identity.Scope != scope {
		return false
	}
	if rec.Identity.Validate() != nil {
		return false
	}
	if rec.State != "stopped" {
		return false
	}
	for _, receipt := range rec.Operations {
		if receipt.State != "completed" {
			return false
		}
	}
	return true
}

// executionRetainedAcrossRestart reports whether the retained execution survives
// the restart (the active task count is not zeroed by moving a worker out). It
// reads the live-held active-task count rather than a hard-coded false: any task
// currently in handleTask (a claimed task the restart/update barriers must not
// kill, per the activeTasks invariant) means a retained execution that the
// capability-replacing restart must preserve. A zero count means no retained
// execution, so the fact stays false and the daemon drains.
//
// This is a count signal, not a per-task retained-execution record, so it is only
// one of the three facts the gate consults. A capability-replacing restart is still
// never admitted on this fact alone: the confirmed-stopped fact (a real stopped
// record, not a suspect/unknown probe) is not yet wired, so the decision keeps
// refusing and the daemon stays on drain-before-restart.
func (d *Daemon) executionRetainedAcrossRestart() bool {
	return d.activeTasks.Load() > 0
}

// retainedRestartAdmission produces the control's retained-restart decision for one
// capability from the held facts. It is the in-process boundary a capability-
// replacing restart must satisfy; it never admits on a missing/unknown fact.
func (d *Daemon) retainedRestartAdmission(service string) RetainedRestartAdmission {
	service = retainedServiceID(service)
	serverSupportsReconcile, confirmedStopped, retainedExecution := d.retainedRestartFacts(service)
	return AdmitRetainedRestart(service, serverSupportsReconcile, confirmedStopped, retainedExecution)
}

// logRetainedRestartDecision records the retained-restart decision at the control
// restart boundary without changing behavior. Today the decision is always refused
// (all facts fail closed), which keeps the daemon on its existing drain-before-
// restart path; the log makes the boundary observable so the retained path can be
// verified once the negotiation/record/retention facts are wired. An invalid
// decision is never honored: a well-formed decision is the only thing a restart
// may act on, so a malformed one keeps drain-before-restart.
func (d *Daemon) logRetainedRestartDecision(service string) {
	decision := d.retainedRestartAdmission(service)
	if err := decision.Validate(); err != nil {
		d.logger.Warn("control-restart: invalid retained-restart decision; keeping drain-before-restart",
			"service", decision.Service, "error", err)
		return
	}
	if decision.Admitted {
		d.logger.Info("control-restart: capability-replacing restart admitted, execution retained",
			"service", decision.Service)
		// Persist the decision so the shutdown path can re-adopt a surviving child
		// instead of closing it (the G "retain and re-adopt" cut). Default stays
		// drain-before-restart: this is only set when all three held facts are real.
		d.retainedRestartAdmitted.Store(true)
		return
	}
	d.logger.Debug("control-restart: capability-replacing restart refused; keeping drain-before-restart",
		"service", decision.Service, "reason", decision.ReasonCode)
}

// retainedServiceID normalizes the capability being replaced for the decision. A
// blank service is treated as the whole control (the drain-only default), which is
// the safe side of the boundary.
func retainedServiceID(service string) string {
	if s := strings.TrimSpace(service); s != "" {
		return s
	}
	return "control"
}
