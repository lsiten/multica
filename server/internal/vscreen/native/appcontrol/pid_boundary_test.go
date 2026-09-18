package appcontrol

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPIDDecisionRejectsChangedOrMissingMetadata(t *testing.T) {
	for _, tc := range []string{"valid", "pid_reuse", "missing_signature", "metadata_error"} {
		t.Run(tc, func(t *testing.T) {
			c, b, _, r := controlFixture(t)
			policy, p, action := policyFixture(t)
			base := p
			base.CodeHash = ""
			base.SigningID = ""
			base.ExecutablePath = ""
			base.AppVersion = ""
			base.AppBuild = ""
			c.windows["owned"].window.Process = base
			c.config.CertifiedPIDInput = policy.Decide
			c.config.VerifyPIDCompletion = syntheticPIDCompletion
			r.Action = action
			b.run = func(_ context.Context, op string, in, out any) error {
				if op == "pid_identity" {
					live := p
					switch tc {
					case "pid_reuse":
						live.Start = "reused"
					case "missing_signature":
						live = base
					case "metadata_error":
						return refusal("native_unavailable")
					}
					*out.(*Process) = live
				}
				if op == "action" {
					v := in.(map[string]any)
					if v["CertifiedPID"].(bool) != (tc == "valid") {
						t.Fatalf("case %s decision=%v", tc, v["CertifiedPID"])
					}
					if !v["CertifiedPID"].(bool) {
						return refusal("needs_intervention")
					}
					*out.(*Result) = Result{Outcome: protocol.VscreenActionVerified}
				}
				return nil
			}
			if _, err := c.Act(t.Context(), r); (err == nil) != (tc == "valid") {
				t.Fatalf("case %s returned %v", tc, err)
			}
		})
	}
}

func TestPIDObservationReportsNoneWithConfiguredEmptyPolicy(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	policy := ProductionPIDInputPolicy()
	c.config.CertifiedPIDInput = policy.Decide
	c.config.PIDInputVerification = policy.Verification
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "observe" {
			*out.(*Observation) = Observation{Window: c.windows["owned"].window, Width: 10, Height: 10, PIDInputVerification: "verified_variants"}
		}
		return nil
	}
	observation, err := c.Observe(t.Context(), a, "owned", false)
	if err != nil || !observation.PIDInputCertificationConfigured || observation.PIDInputVerification != "none" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestPIDMetadataFailureDoesNotDisableSemanticAX(t *testing.T) {
	c, b, _, r := controlFixture(t)
	policy := ProductionPIDInputPolicy()
	c.config.CertifiedPIDInput = policy.Decide
	b.run = func(_ context.Context, op string, in, out any) error {
		if op == "pid_identity" {
			return refusal("native_unavailable")
		}
		if op == "action" {
			if in.(map[string]any)["CertifiedPID"].(bool) {
				t.Fatal("metadata failure certified PID")
			}
			*out.(*Result) = Result{Outcome: protocol.VscreenActionVerified}
		}
		return nil
	}
	if _, err := c.Act(t.Context(), r); err != nil {
		t.Fatalf("optional metadata blocked semantic AX dispatch: %v", err)
	}
}
