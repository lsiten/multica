package daemon

import (
	"strings"
	"sync"
)

// completionVerification is evidence that this task's managed verifier
// accepted explicit criteria during the current run. It is intentionally a
// narrow evidence gate, not a claim that semantic output proves business
// correctness.
type completionVerification struct {
	Verified      bool
	Reason        string
	Mode          string
	Calibrated    bool
	Verdict       string
	Confidence    *float64
	Probabilities map[string]float64
}

func autonomousCompletionFailure(verification completionVerification) string {
	if verification.Verified {
		return ""
	}
	reason := "completion_verification_required"
	if verification.Reason != "" {
		reason += ": " + verification.Reason
	}
	return reason
}

type completionGate struct {
	mu       sync.Mutex
	taskID   string
	sequence uint64
	result   completionVerification
}

func newCompletionGate(taskID string) *completionGate {
	return &completionGate{taskID: strings.TrimSpace(taskID), result: completionVerification{Reason: "completion_verification_required"}}
}

// begin starts a new verification attempt and invalidates any previous result.
// The sequence token prevents a slow, older provider response from restoring a
// successful gate after a newer request has failed or become uncertain.
func (g *completionGate) begin() uint64 {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sequence++
	g.result = completionVerification{Reason: "verification_in_progress"}
	return g.sequence
}

func (g *completionGate) fail(sequence uint64, reason string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if sequence != 0 && sequence != g.sequence {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "verification_failed"
	}
	g.result = completionVerification{Reason: reason}
}

func (g *completionGate) record(goal string, criteria, evidence []string, verdict string) {
	sequence := g.begin()
	g.recordForAttempt(sequence, "", goal, criteria, evidence, verdict)
}

func (g *completionGate) recordForAttempt(sequence uint64, taskID, goal string, criteria, evidence []string, verdict string) {
	g.recordForAttemptWithDetails(sequence, taskID, goal, criteria, evidence, verdict, "", false, nil, nil)
}

func (g *completionGate) recordForAttemptWithDetails(sequence uint64, taskID, goal string, criteria, evidence []string, verdict, mode string, calibrated bool, confidence *float64, probabilities map[string]float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if sequence != 0 && sequence != g.sequence {
		return
	}
	if g.taskID != "" && strings.TrimSpace(taskID) != g.taskID {
		g.result = completionVerification{Reason: "verification_task_mismatch"}
		return
	}
	g.result = completionVerification{Reason: "verification_not_satisfied", Mode: mode, Calibrated: calibrated, Verdict: verdict, Confidence: confidence, Probabilities: probabilities}
	if strings.TrimSpace(goal) == "" || len(criteria) == 0 || len(evidence) == 0 {
		g.result.Reason = "verification_request_missing_goal_criteria_or_evidence"
		return
	}
	for _, value := range append(append([]string(nil), criteria...), evidence...) {
		if strings.TrimSpace(value) == "" {
			g.result.Reason = "verification_request_contains_empty_evidence"
			return
		}
	}
	if verdict != "satisfied" {
		return
	}
	g.result = completionVerification{Verified: true, Reason: "semantic_evidence_accepted", Mode: mode, Calibrated: calibrated, Verdict: verdict, Confidence: confidence, Probabilities: probabilities}
}

func (g *completionGate) status() completionVerification {
	if g == nil {
		return completionVerification{Reason: "completion_verifier_unavailable"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.result
}

func (s *llm2jevMCPSet) completionVerification() completionVerification {
	if s == nil || s.gate == nil {
		return completionVerification{Reason: "completion_verifier_unavailable"}
	}
	return s.gate.status()
}

func (s *configuredJevMCPSet) completionVerification() completionVerification {
	if s == nil {
		return completionVerification{Reason: "completion_verifier_unavailable"}
	}
	if verifier, ok := s.inner.(interface{ completionVerification() completionVerification }); ok {
		return verifier.completionVerification()
	}
	return completionVerification{Reason: "completion_verifier_unavailable"}
}
