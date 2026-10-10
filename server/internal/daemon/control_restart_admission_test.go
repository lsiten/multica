package daemon

import (
	"testing"
)

func validRetainedRestart() RetainedRestartAdmission {
	return RetainedRestartAdmission{
		Service:                 "gateway",
		ServerSupportsReconcile: true,
		ConfirmedStopped:        true,
		Retained:                true,
		Admitted:                true,
	}
}

func TestRetainedRestartAdmissionValidate(t *testing.T) {
	full := validRetainedRestart()

	noService := full
	noService.Service = ""

	admitNoReconcile := full
	admitNoReconcile.ServerSupportsReconcile = false

	admitSuspect := full
	admitSuspect.ConfirmedStopped = false

	admitNotRetained := full
	admitNotRetained.Retained = false

	admitWithReason := full
	admitWithReason.ReasonCode = RetainedRestartReasonDrainOnly

	unknownReason := full
	unknownReason.Admitted = false
	unknownReason.ReasonCode = "made_up"

	contradictDrain := full
	contradictDrain.Admitted = false
	contradictDrain.ReasonCode = RetainedRestartReasonDrainOnly

	contradictSuspect := full
	contradictSuspect.Admitted = false
	contradictSuspect.ReasonCode = RetainedRestartReasonSuspect

	contradictRetained := full
	contradictRetained.Admitted = false
	contradictRetained.ReasonCode = RetainedRestartReasonActiveTasks

	properRefuse := full
	properRefuse.Admitted = false
	properRefuse.ServerSupportsReconcile = false
	properRefuse.ConfirmedStopped = false
	properRefuse.Retained = false
	properRefuse.ReasonCode = RetainedRestartReasonDrainOnly

	cases := []struct {
		name    string
		a       RetainedRestartAdmission
		wantErr bool
	}{
		{"fully admitted restart is valid", full, false},
		{"missing service id", noService, true},
		{"admitted on a drain-only (non-reconciling) server", admitNoReconcile, true},
		{"admitted without a confirmed-stopped old instance", admitSuspect, true},
		{"admitted without preserving the retained execution", admitNotRetained, true},
		{"admitted but carries a refusal reason", admitWithReason, true},
		{"refused with an unknown reason", unknownReason, true},
		{"refused drain-only though server reconciles is contradictory", contradictDrain, true},
		{"refused suspect though instance is confirmed stopped is contradictory", contradictSuspect, true},
		{"refused active-tasks though execution is retained is contradictory", contradictRetained, true},
		{"a refused drain-only with the missing precondition is valid", properRefuse, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestAdmitRetainedRestartMatrix(t *testing.T) {
	cases := []struct {
		name             string
		serverSupports   bool
		confirmedStopped bool
		retained         bool
		admitted         bool
		reason           string
	}{
		{"admits only with all preconditions", true, true, true, true, ""},
		{"refuses a drain-only (non-reconciling) server", false, true, true, false, RetainedRestartReasonDrainOnly},
		{"refuses a suspect (unconfirmed-stopped) instance", true, false, true, false, RetainedRestartReasonSuspect},
		{"refuses a restart that would lose the retained execution", true, true, false, false, RetainedRestartReasonActiveTasks},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := AdmitRetainedRestart("gateway", c.serverSupports, c.confirmedStopped, c.retained)
			if a.Admitted != c.admitted {
				t.Fatalf("Admitted = %v, want %v (reason %q)", a.Admitted, c.admitted, a.ReasonCode)
			}
			if c.reason != "" && a.ReasonCode != c.reason {
				t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, c.reason)
			}
			if err := a.Validate(); err != nil {
				t.Fatalf("decided admission does not validate: %v", err)
			}
		})
	}
}

// TestAdmitRetainedRestartSuspectNeverAdmits is the core G invariant: a suspect
// (unconfirmed-stopped) instance is never admitted as stopped even when the
// server reconciles and the execution would be retained.
func TestAdmitRetainedRestartSuspectNeverAdmits(t *testing.T) {
	a := AdmitRetainedRestart("gateway", true, false, true)
	if a.Admitted {
		t.Fatal("a suspect instance was admitted as stopped")
	}
	if a.ReasonCode != RetainedRestartReasonSuspect {
		t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, RetainedRestartReasonSuspect)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("suspect refusal must validate: %v", err)
	}
}

// TestAdmitRetainedRestartDrainOnlyOnOldServer is the old-server invariant: on
// a drain-only server (no reconciliation), even a confirmed-stopped instance
// with a retained execution is refused for a capability-replacing restart.
func TestAdmitRetainedRestartDrainOnlyOnOldServer(t *testing.T) {
	a := AdmitRetainedRestart("gateway", false, true, true)
	if a.Admitted {
		t.Fatal("an old/drain-only server admitted a capability-replacing restart")
	}
	if a.ReasonCode != RetainedRestartReasonDrainOnly {
		t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, RetainedRestartReasonDrainOnly)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("drain-only refusal must validate: %v", err)
	}
}
