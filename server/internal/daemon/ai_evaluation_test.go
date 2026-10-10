package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// stubAIEvaluationService is a test double that satisfies the AI evaluation
// seam without touching a provider, a model lease, or the completion gate. It
// exists to prove the contract is implementable and to exercise the owner's
// acceptance path in isolation.
type stubAIEvaluationService struct {
	calledEval   int
	calledCancel int
	calledClose  int
	result       EvaluateResult
	err          error
}

func (s *stubAIEvaluationService) Evaluate(_ context.Context, attempt EvaluationAttempt) (EvaluateResult, error) {
	s.calledEval++
	if s.err != nil {
		return EvaluateResult{}, s.err
	}
	return s.result, nil
}

func (s *stubAIEvaluationService) Cancel(_ context.Context, _ EvaluationAttempt) error {
	s.calledCancel++
	return nil
}

func (s *stubAIEvaluationService) Close(_ context.Context, _ EvaluationAttempt) error {
	s.calledClose++
	return nil
}

// Compile-time proof the seam is a real, satisfiable interface.
var _ AIEvaluationService = (*stubAIEvaluationService)(nil)

func TestAIEvaluationAttemptValidateMatrix(t *testing.T) {
	remote := func() EvaluationContext {
		return EvaluationContext{
			Source:           "remote",
			TaskID:           "task-a",
			ResolvedEndpoint: "https://provider.example.test/v1",
		}
	}
	cases := []struct {
		name    string
		attempt EvaluationAttempt
		wantErr bool
	}{
		{
			name:    "decision valid",
			attempt: EvaluationAttempt{Context: remote(), Kind: EvaluationKindDecision, Payload: json.RawMessage(`{"question":"q"}`)},
		},
		{
			name:    "completion valid",
			attempt: EvaluationAttempt{Context: remote(), Kind: EvaluationKindCompletion, Payload: json.RawMessage(`{"goal":"g"}`)},
		},
		{
			name:    "unknown kind rejected",
			attempt: EvaluationAttempt{Context: remote(), Kind: "bogus", Payload: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "empty payload rejected",
			attempt: EvaluationAttempt{Context: remote(), Kind: EvaluationKindDecision},
			wantErr: true,
		},
		{
			name:    "empty task id rejected",
			attempt: EvaluationAttempt{Context: EvaluationContext{Source: "remote", ResolvedEndpoint: "https://p"}, Kind: EvaluationKindDecision, Payload: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "local with endpoint rejected",
			attempt: EvaluationAttempt{Context: EvaluationContext{Source: "local", TaskID: "task-a", ResolvedEndpoint: "https://p"}, Kind: EvaluationKindDecision, Payload: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "local with credential rejected",
			attempt: EvaluationAttempt{Context: EvaluationContext{Source: "local", TaskID: "task-a", ResolvedCredential: "key"}, Kind: EvaluationKindCompletion, Payload: json.RawMessage(`{}`)},
			wantErr: true,
		},
		{
			name:    "local valid carries neither",
			attempt: EvaluationAttempt{Context: EvaluationContext{Source: "local", TaskID: "task-a"}, Kind: EvaluationKindCompletion, Payload: json.RawMessage(`{"goal":"g"}`)},
		},
		{
			name:    "remote without endpoint rejected",
			attempt: EvaluationAttempt{Context: EvaluationContext{Source: "remote", TaskID: "task-a"}, Kind: EvaluationKindDecision, Payload: json.RawMessage(`{}`)},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.attempt.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestAIEvaluationServiceOwnerAcceptsTypedResult(t *testing.T) {
	// The owner delegates one attempt and receives typed evidence; it is the
	// sole authority that applies the result, never AI.
	svc := &stubAIEvaluationService{
		result: EvaluateResult{
			Attempt: 7,
			Kind:    EvaluationKindCompletion,
			Completion: &CompletionEvidence{
				Verdict:       "satisfied",
				Mode:          "system_one",
				Calibrated:    true,
				Probabilities: map[string]float64{"satisfied": 0.8, "incomplete": 0.2},
			},
		},
	}
	attempt := EvaluationAttempt{
		Context: EvaluationContext{Source: "remote", TaskID: "task-a", ResolvedEndpoint: "https://p"},
		Kind:    EvaluationKindCompletion,
		Payload: json.RawMessage(`{"goal":"g","criteria":["c"],"evidence":["e"]}`),
		Attempt: 7,
	}
	if err := attempt.Validate(); err != nil {
		t.Fatalf("attempt validate: %v", err)
	}
	got, err := svc.Evaluate(context.Background(), attempt)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got.Attempt != attempt.Attempt {
		t.Fatalf("result attempt = %d, want %d", got.Attempt, attempt.Attempt)
	}
	if got.Completion == nil || got.Completion.Verdict != "satisfied" {
		t.Fatalf("result = %+v, want satisfied completion evidence", got)
	}
	if svc.calledEval != 1 {
		t.Fatalf("evaluate calls = %d, want 1", svc.calledEval)
	}
}

func TestAIEvaluationServiceFailureSurfacesAsError(t *testing.T) {
	// A gateway/AI failure must surface a capability error; the owner never
	// silently accepts a verification on a failed attempt.
	svc := &stubAIEvaluationService{err: errors.New("ai unavailable")}
	attempt := EvaluationAttempt{
		Context: EvaluationContext{Source: "remote", TaskID: "task-a", ResolvedEndpoint: "https://p"},
		Kind:    EvaluationKindDecision,
		Payload: json.RawMessage(`{"question":"q"}`),
	}
	if _, err := svc.Evaluate(context.Background(), attempt); err == nil {
		t.Fatal("expected capability error, got nil")
	}
	if err := svc.Cancel(context.Background(), attempt); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := svc.Close(context.Background(), attempt); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestAIEvaluationAttemptEnforcesBudgetAdmission(t *testing.T) {
	// Base usage-required remote attempt; the owner must admit it before AI runs.
	base := func() EvaluationAttempt {
		return EvaluationAttempt{
			Context: EvaluationContext{
				Source:           "remote",
				TaskID:           "task-a",
				Provider:         "openai",
				ProtocolMode:     "semantic",
				MaxConcurrent:    1,
				MaxCalls:         1,
				CallTimeout:      1,
				UsageRequired:    true,
				ResolvedEndpoint: "https://provider.example.test/v1",
			},
			Kind:    EvaluationKindDecision,
			Payload: json.RawMessage(`{"question":"q"}`),
		}
	}
	admitted := func() *BudgetAdmission {
		a := BudgetAdmission{TaskID: "task-a", BudgetPolicyRevision: 1, UsageRequired: true, Admitted: true}
		return &a
	}
	refused := func() *BudgetAdmission {
		a := BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: BudgetReasonTokenExceeded}
		return &a
	}

	cases := []struct {
		name    string
		apply   func(a *EvaluationAttempt)
		wantErr bool
	}{
		{
			name:  "usage-required with admitted admission passes",
			apply: func(a *EvaluationAttempt) { a.Admission = admitted() },
		},
		{
			name:    "usage-required with refused admission fails closed",
			apply:   func(a *EvaluationAttempt) { a.Admission = refused() },
			wantErr: true,
		},
		{
			name:    "usage-required with no admission fails closed",
			apply:   func(a *EvaluationAttempt) { a.Admission = nil },
			wantErr: true,
		},
		{
			name:    "usage-required with invalid admission fails closed",
			apply:   func(a *EvaluationAttempt) { a.Admission = &BudgetAdmission{Admitted: true} },
			wantErr: true,
		},
		{
			name: "non-chargeable with no admission passes",
			apply: func(a *EvaluationAttempt) {
				a.Context.UsageRequired = false
				a.Admission = nil
			},
		},
		{
			name:    "non-chargeable with refused admission still fails",
			apply:   func(a *EvaluationAttempt) { a.Admission = refused() },
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := base()
			c.apply(&a)
			err := a.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestAIEvaluationAttemptBudgetAdmissionIsNotRemainingCount proves the F2
// invariant at the AI boundary: the owner's admission rides the attempt as a
// decision (admitted + reason), never a remaining-budget count the AI could
// spend against, and a refused admission never reaches Evaluate.
func TestAIEvaluationAttemptBudgetAdmissionIsNotRemainingCount(t *testing.T) {
	svc := &stubAIEvaluationService{
		result: EvaluateResult{Attempt: 1, Kind: EvaluationKindDecision, Decision: json.RawMessage(`{}`)},
	}
	attempt := EvaluationAttempt{
		Context: EvaluationContext{
			Source: "remote", TaskID: "task-a", Provider: "openai", ProtocolMode: "semantic",
			MaxConcurrent: 1, MaxCalls: 1, CallTimeout: 1, UsageRequired: true,
			ResolvedEndpoint: "https://provider.example.test/v1",
		},
		Kind:    EvaluationKindDecision,
		Payload: json.RawMessage(`{"question":"q"}`),
		Attempt: 1,
	}
	// Without an admitted admission a chargeable attempt is refused at the
	// boundary and never reaches the AI service.
	if err := attempt.Validate(); err == nil {
		t.Fatal("chargeable attempt without admission unexpectedly valid")
	}
	attempt.Admission = &BudgetAdmission{TaskID: "task-a", BudgetPolicyRevision: 1, UsageRequired: true, Admitted: true}
	if err := attempt.Validate(); err != nil {
		t.Fatalf("admitted chargeable attempt validate: %v", err)
	}
	if _, err := svc.Evaluate(context.Background(), attempt); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if svc.calledEval != 1 {
		t.Fatalf("evaluate calls = %d, want 1", svc.calledEval)
	}
}
