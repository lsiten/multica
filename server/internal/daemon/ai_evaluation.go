package daemon

import (
	"context"
	"encoding/json"
	"fmt"
)

// EvaluationKind names the kind of evaluation the AI service performs for one
// attempt. A decision is the semantic JEV question; a completion is the
// completion verification. The owner chooses the kind; AI never infers it.
type EvaluationKind string

const (
	EvaluationKindDecision   EvaluationKind = "decision"
	EvaluationKindCompletion EvaluationKind = "completion"
)

// EvaluationAttempt is one narrow evaluation the task owner (control until F3,
// the task worker afterwards) hands to the AI service. It carries the resolved
// EvaluationContext (the owner namespace, the resolved source/model identity and
// limits from the minimum typed boundary) plus the raw request payload and the
// attempt sequence from the completion gate. It never carries the full
// Task/Agent/Config, CustomEnv, an account PAT, or the model lease: local
// selection obtains its lease inside AI, remote/agent_context carries only the
// resolved credential, and unrelated MCP credentials stay with the gateway.
type EvaluationAttempt struct {
	Context EvaluationContext
	Kind    EvaluationKind
	// Payload is the raw MCP request body for this attempt (a decision or a
	// completion request). AI validates and normalizes it; the owner does not.
	Payload json.RawMessage
	// Attempt is the completion-gate sequence this answer belongs to. The owner
	// ignores a result whose attempt is stale, so a slow provider response can
	// never restore a superseded verification.
	Attempt uint64
	// Admission is the task owner's budget admission for a chargeable attempt.
	// It is optional: a non-chargeable attempt carries nil. When UsageRequired
	// is set the owner must supply a valid, admitted admission, so a chargeable
	// attempt is never delegated to the AI service without the owner's admission.
	// A refused admission fails the attempt closed. This is the F2 budget cut
	// bound to the AI evaluation boundary: the owner's aggregate budget stays in
	// the task owner and AI never receives an isolated copy of remaining budget.
	Admission *BudgetAdmission
}

// EvaluateResult is the typed evidence the AI service returns for one attempt.
// It never authorizes completion by itself: the task owner applies a completion
// result to the completion gate and is the sole authority for acceptance,
// budget and terminal reports. A decision result carries the normalized
// decision; a completion result carries the typed verification evidence.
type EvaluateResult struct {
	// Attempt echoes the attempt the result answers; the owner drops stale ones.
	Attempt uint64
	Kind    EvaluationKind
	// Decision carries the normalized decision for a decision attempt.
	Decision json.RawMessage
	// Completion carries the typed verification evidence for a completion
	// attempt; it is nil for a decision attempt.
	Completion *CompletionEvidence
}

// CompletionEvidence is the typed completion verification the AI service
// returns. It mirrors completionVerification so the owner can apply it to the
// gate unchanged; it is evidence, never authorization. An uncertain verdict
// carries no probabilities; a calibrated path carries confidence/probabilities.
type CompletionEvidence struct {
	Verdict       string
	Mode          string
	Calibrated    bool
	Confidence    *float64
	Probabilities map[string]float64
	Missing       []string
	ReasonCode    string
}

// AIEvaluationService is the seam the AI service implements. The task owner
// delegates provider HTTP evaluation, semantic/SystemOne normalization and
// model leases to it; it owns neither the completion gate nor the task budget.
// A gateway/AI failure must surface a capability error, never silently accept a
// verification. Results bind to the attempt sequence and to the server-issued
// execution identity, never to an invented epoch.
type AIEvaluationService interface {
	// Evaluate runs one attempt and returns typed evidence. It must not write
	// the completion gate; the owner applies the result.
	Evaluate(ctx context.Context, attempt EvaluationAttempt) (EvaluateResult, error)
	// Cancel stops an in-flight attempt so the owner can begin a new one.
	Cancel(ctx context.Context, attempt EvaluationAttempt) error
	// Close releases the evaluation context (the model lease and resources) when
	// the owner is done with it.
	Close(ctx context.Context, attempt EvaluationAttempt) error
}

// Validate rejects an attempt that would be unsafe to hand to the AI service. It
// is the boundary guard the owner runs before delegating; a refusal fails the
// attempt closed rather than shipping a malformed or cross-boundary payload.
func (a EvaluationAttempt) Validate() error {
	if a.Kind != EvaluationKindDecision && a.Kind != EvaluationKindCompletion {
		return fmt.Errorf("ai evaluation: unknown kind %q", a.Kind)
	}
	if len(a.Payload) == 0 {
		return fmt.Errorf("ai evaluation: attempt has no payload")
	}
	if a.Context.TaskID == "" {
		return fmt.Errorf("ai evaluation: attempt has no task id")
	}
	// Local selection obtains its lease inside AI, so it carries neither a
	// resolved endpoint nor a credential. remote/agent_context use the resolved
	// endpoint and, only for remote, the resolved credential.
	if a.Context.Source == "local" {
		if a.Context.ResolvedEndpoint != "" || a.Context.ResolvedCredential != "" {
			return fmt.Errorf("ai evaluation: local source must carry neither endpoint nor credential")
		}
		return nil
	}
	if a.Context.ResolvedEndpoint == "" {
		return fmt.Errorf("ai evaluation: %s source requires a resolved endpoint", a.Context.Source)
	}
	// Budget boundary: a chargeable attempt (UsageRequired) must carry the owner's
	// admission; any admission present must be valid and admitted. A refused or
	// invalid admission fails the attempt closed, so a chargeable attempt can
	// never reach the AI service without the owner's admission and AI never gets
	// an isolated copy of remaining budget.
	if a.Context.UsageRequired && a.Admission == nil {
		return fmt.Errorf("ai evaluation: usage-required attempt has no budget admission")
	}
	if a.Admission != nil {
		if err := a.Admission.Validate(); err != nil {
			return fmt.Errorf("ai evaluation: budget admission invalid: %w", err)
		}
		if !a.Admission.Admitted {
			return fmt.Errorf("ai evaluation: budget admission refused (%s)", a.Admission.ReasonCode)
		}
	}
	return nil
}
