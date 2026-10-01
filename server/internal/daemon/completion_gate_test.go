package daemon

import "testing"

func TestAutonomousCompletionFailure(t *testing.T) {
	if got := autonomousCompletionFailure(completionVerification{Verified: true}); got != "" {
		t.Fatalf("verified completion rejected: %q", got)
	}
	if got := autonomousCompletionFailure(completionVerification{Reason: "verification_not_satisfied"}); got != "completion_verification_required: verification_not_satisfied" {
		t.Fatalf("unexpected rejection reason: %q", got)
	}
}

func TestCompletionGateRequiresSatisfiedEvidence(t *testing.T) {
	gate := &completionGate{}
	gate.record("ship report", []string{"report exists", "tests pass"}, []string{"report.md", "go test ./..."}, "satisfied")
	if result := gate.status(); !result.Verified || result.Reason != "semantic_evidence_accepted" {
		t.Fatalf("expected verified evidence, got %+v", result)
	}
}

func TestCompletionGateRejectsMissingOrUncertainVerification(t *testing.T) {
	cases := []struct {
		name     string
		goal     string
		criteria []string
		evidence []string
		verdict  string
		reason   string
	}{
		{name: "uncertain", goal: "ship", criteria: []string{"artifact"}, evidence: []string{"artifact.zip"}, verdict: "uncertain", reason: "verification_not_satisfied"},
		{name: "missing evidence", goal: "ship", criteria: []string{"artifact"}, verdict: "satisfied", reason: "verification_request_missing_goal_criteria_or_evidence"},
		{name: "empty evidence", goal: "ship", criteria: []string{"artifact"}, evidence: []string{" "}, verdict: "satisfied", reason: "verification_request_contains_empty_evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := &completionGate{}
			gate.record(tc.goal, tc.criteria, tc.evidence, tc.verdict)
			result := gate.status()
			if result.Verified || result.Reason != tc.reason {
				t.Fatalf("expected rejected verification %q, got %+v", tc.reason, result)
			}
		})
	}
}
