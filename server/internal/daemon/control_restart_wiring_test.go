package daemon

import (
	"testing"
)

// TestRetainedRestartAdmissionRefusedByDefault is the default-path invariant: with
// no negotiation/record/retention wired, every fact fails closed, so the decision
// gate refuses and the daemon keeps drain-before-restart. This is the behavior the
// full suite must never regress.
func TestRetainedRestartAdmissionRefusedByDefault(t *testing.T) {
	d := &Daemon{}
	a := d.retainedRestartAdmission("")
	if a.Admitted {
		t.Fatal("a default (no retained facts) control restart was admitted")
	}
	if a.Service != "control" {
		t.Fatalf("service = %q, want normalized control", a.Service)
	}
	if a.ReasonCode != RetainedRestartReasonDrainOnly {
		t.Fatalf("reason = %q, want %q (first fail-closed fact is the server capability)", a.ReasonCode, RetainedRestartReasonDrainOnly)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("refused decision must validate: %v", err)
	}
}

// TestRetainedRestartFactsFailClosed proves the three facts are read from held
// state and each fails closed on its own, so a single missing fact never admits.
func TestRetainedRestartFactsFailClosed(t *testing.T) {
	d := &Daemon{}
	if d.serverSupportsExecutionReconcile() {
		t.Fatal("serverSupportsExecutionReconcile must fail closed (drain-only default)")
	}
	if d.instanceConfirmedStopped() {
		t.Fatal("instanceConfirmedStopped must fail closed (a suspect instance is never stopped)")
	}
	if d.executionRetainedAcrossRestart() {
		t.Fatal("executionRetainedAcrossRestart must fail closed (worker retention is not implemented)")
	}
	server, stopped, retained := d.retainedRestartFacts("gateway")
	if server || stopped || retained {
		t.Fatalf("facts = (%v,%v,%v), want all false", server, stopped, retained)
	}
}

// TestRetainedServiceID checks the capability normalization a blank service maps to
// the whole control (the safe drain-only default).
func TestRetainedServiceID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "control"},
		{"   ", "control"},
		{"gateway", "gateway"},
		{"  environment  ", "environment"},
	}
	for _, c := range cases {
		if got := retainedServiceID(c.in); got != c.want {
			t.Fatalf("retainedServiceID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestServerSupportsExecutionReconcileReflectsFlag proves the first fact is
// read from the real, live-held source: the server capability observed on a
// heartbeat ack, not a hard-coded false.
func TestServerSupportsExecutionReconcileReflectsFlag(t *testing.T) {
	d := &Daemon{}
	if d.serverSupportsExecutionReconcile() {
		t.Fatal("unobserved server must fail closed (drain-only default)")
	}
	d.executionReconcileSupported.Store(true)
	if !d.serverSupportsExecutionReconcile() {
		t.Fatal("an observed reconciling server must be reported as supported")
	}
}

// TestRetainedRestartAdmissionUsesServerReconcileFact proves the decision gate
// advances as the first fact becomes real: with the server observed reconciling
// but the old instance not yet confirmed stopped, the decision refuses on the
// suspect fact (not drain-only) and stays refused — never admitting without all
// three facts. This is the "one real fact, still drain-only" guarantee.
func TestRetainedRestartAdmissionUsesServerReconcileFact(t *testing.T) {
	d := &Daemon{}
	d.executionReconcileSupported.Store(true)
	a := d.retainedRestartAdmission("")
	if a.Admitted {
		t.Fatal("a restart was admitted without a confirmed-stopped instance or retained execution")
	}
	if a.ReasonCode != RetainedRestartReasonSuspect {
		t.Fatalf("reason = %q, want %q (server reconciles, but old instance is not confirmed stopped)", a.ReasonCode, RetainedRestartReasonSuspect)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("decided admission must validate: %v", err)
	}
}

// TestRetainedRestartAdmissionUsesActiveTaskCountFact proves the retained-execution
// fact is read from the live-held active-task count (not a hard-coded false): a
// task in handleTask makes the fact true, but the gate still refuses (the
// confirmed-stopped fact is not yet real), so a capability-replacing restart is
// never admitted on this fact alone. This is the "two real facts, still drain-only"
// guarantee that keeps the default path byte-identical.
func TestRetainedRestartAdmissionUsesActiveTaskCountFact(t *testing.T) {
	d := &Daemon{}
	// With no active task the retained-execution fact fails closed.
	if d.executionRetainedAcrossRestart() {
		t.Fatal("no active task must fail closed (no retained execution)")
	}
	// A task in handleTask makes the retained-execution fact real.
	d.activeTasks.Store(1)
	if !d.executionRetainedAcrossRestart() {
		t.Fatal("an active task must be reported as a retained execution")
	}
	// But the gate still refuses: the confirmed-stopped fact is not yet real, so a
	// capability-replacing restart is never admitted on the retained fact alone.
	d.executionReconcileSupported.Store(true)
	a := d.retainedRestartAdmission("gateway")
	if a.Admitted {
		t.Fatal("a restart was admitted without a confirmed-stopped instance")
	}
	if a.ReasonCode != RetainedRestartReasonSuspect {
		t.Fatalf("reason = %q, want %q (server reconciles + retained execution, but old instance is not confirmed stopped)", a.ReasonCode, RetainedRestartReasonSuspect)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("decided admission must validate: %v", err)
	}
}
