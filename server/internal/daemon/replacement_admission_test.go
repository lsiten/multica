package daemon

import "testing"

func TestReplacementAdmissionValidateMatrix(t *testing.T) {
	fullExit := &OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: true}
	cases := []struct {
		name    string
		admit   ReplacementAdmission
		wantErr bool
	}{
		{
			name:  "admitted with full exit proof valid",
			admit: ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: true, Exit: fullExit},
		},
		{
			name:  "refused old_tree_unknown valid",
			admit: ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false, ReasonCode: ReplacementReasonOldTreeUnknown},
		},
		{
			name:  "refused old_tree_running valid",
			admit: ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false, ReasonCode: ReplacementReasonOldTreeRunning},
		},
		{
			name:  "refused participant_not_released valid",
			admit: ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false, ReasonCode: ReplacementReasonNotReleased},
		},
		{
			name:    "empty task id rejected",
			admit:   ReplacementAdmission{RuntimeID: "rt-a", Admitted: true, Exit: fullExit},
			wantErr: true,
		},
		{
			name:    "empty runtime id rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", Admitted: true, Exit: fullExit},
			wantErr: true,
		},
		{
			name:    "admitted without exit proof rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: true},
			wantErr: true,
		},
		{
			name:    "admitted with provider exit but not released rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: true, Exit: &OldTreeExitProof{ProviderTreeExited: true}},
			wantErr: true,
		},
		{
			name:    "admitted with reason rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: true, Exit: fullExit, ReasonCode: ReplacementReasonOldTreeUnknown},
			wantErr: true,
		},
		{
			name:    "refused with exit proof rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false, Exit: fullExit},
			wantErr: true,
		},
		{
			name:    "refused without reason rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false},
			wantErr: true,
		},
		{
			name:    "refused unknown reason rejected",
			admit:   ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: false, ReasonCode: "policy_expired"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.admit.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestAdmitReplacementRequiresActualExitProof(t *testing.T) {
	cases := []struct {
		name   string
		exit   *OldTreeExitProof
		admit  bool
		reason string
	}{
		{"full exit admits", &OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: true}, true, ""},
		{"unknown tree refuses", nil, false, ReplacementReasonOldTreeUnknown},
		{"running tree refuses", &OldTreeExitProof{}, false, ReplacementReasonOldTreeUnknown},
		{"provider exited but not released refuses", &OldTreeExitProof{ProviderTreeExited: true, ParticipantReleased: false}, false, ReplacementReasonNotReleased},
		{"released but not exited refuses", &OldTreeExitProof{ProviderTreeExited: false, ParticipantReleased: true}, false, ReplacementReasonOldTreeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := AdmitReplacement("task-a", "rt-a", c.exit)
			if a.Admitted != c.admit {
				t.Fatalf("Admitted = %v, want %v (%+v)", a.Admitted, c.admit, a)
			}
			if a.ReasonCode != c.reason {
				t.Fatalf("ReasonCode = %q, want %q", a.ReasonCode, c.reason)
			}
			if err := a.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestPolicyStateIsNeverExitProof proves the task14 invariant directly: a
// policy/permission deadline, an expired grant, an offline runtime, a failed
// status or a cancellation ACK is never old-tree exit evidence and must never
// admit a replacement or "adopt" a policy-failed old worker as a new claim.
func TestPolicyStateIsNeverExitProof(t *testing.T) {
	// An owner-side state (policy expiry / cancellation ACK) must never set the
	// actual exit-proof fields, so it can never admit a replacement.
	ownerState := OldTreeExitProof{ProviderTreeExited: false, ParticipantReleased: false}
	if a := AdmitReplacement("task-a", "rt-a", &ownerState); a.Admitted {
		t.Fatalf("policy/owner state admitted a replacement")
	}
	if a := AdmitReplacement("task-a", "rt-a", nil); a.Admitted {
		t.Fatalf("missing exit proof admitted a replacement")
	}
	// Even an admitted admission must carry the real exit proof, not a policy
	// state, so the boundary cannot be tricked into adopting a policy-failed old
	// worker.
	fake := ReplacementAdmission{TaskID: "task-a", RuntimeID: "rt-a", Admitted: true, ReasonCode: ReplacementReasonOldTreeRunning}
	if err := fake.Validate(); err == nil {
		t.Fatalf("admission without exit proof unexpectedly valid")
	}
}
