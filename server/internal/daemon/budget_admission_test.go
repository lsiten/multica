package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestBudgetAdmissionValidateMatrix(t *testing.T) {
	cases := []struct {
		name    string
		admit   BudgetAdmission
		wantErr bool
	}{
		{
			name:  "admitted valid",
			admit: BudgetAdmission{TaskID: "task-a", BudgetPolicyRevision: 3, UsageRequired: true, Admitted: true},
		},
		{
			name:  "refused token limit valid",
			admit: BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: BudgetReasonTokenExceeded},
		},
		{
			name:  "refused cost limit valid",
			admit: BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: BudgetReasonCostExceeded},
		},
		{
			name:  "refused usage unavailable valid",
			admit: BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: BudgetReasonUsageUnavailable},
		},
		{
			name:  "refused owner refused valid",
			admit: BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: BudgetReasonOwnerRefused},
		},
		{
			name:    "empty task id rejected",
			admit:   BudgetAdmission{Admitted: true},
			wantErr: true,
		},
		{
			name:    "whitespace task id rejected",
			admit:   BudgetAdmission{TaskID: "  ", Admitted: true},
			wantErr: true,
		},
		{
			name:    "negative policy revision rejected",
			admit:   BudgetAdmission{TaskID: "task-a", BudgetPolicyRevision: -1, Admitted: true},
			wantErr: true,
		},
		{
			name:    "admitted with reason rejected",
			admit:   BudgetAdmission{TaskID: "task-a", Admitted: true, ReasonCode: BudgetReasonTokenExceeded},
			wantErr: true,
		},
		{
			name:    "refused without reason rejected",
			admit:   BudgetAdmission{TaskID: "task-a", Admitted: false},
			wantErr: true,
		},
		{
			name:    "refused unknown reason rejected",
			admit:   BudgetAdmission{TaskID: "task-a", Admitted: false, ReasonCode: "made_up"},
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

func TestAdmitForAttemptAdmitsWithinAggregateBudget(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: 100})
	defer cancel(nil)
	// Primary attempts and JEV share the one ledger; record both, still inside.
	budget.RecordSnapshot("attempt:1", "codex", map[string]agent.TokenUsage{"main": {InputTokens: 40, OutputTokens: 10}})
	budget.RecordUsage("jev", "decision", agent.TokenUsage{InputTokens: 40})

	a := AdmitForAttempt("task-a", 7, true, budget)
	if !a.Admitted {
		t.Fatalf("within budget: admission refused %+v", a)
	}
	if a.ReasonCode != "" {
		t.Fatalf("admitted admission carried a reason %q", a.ReasonCode)
	}
	if a.TaskID != "task-a" || a.BudgetPolicyRevision != 7 || !a.UsageRequired {
		t.Fatalf("admission not bound to owner budget: %+v", a)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	// The owner aggregate budget is within limits, so its cancellation cause has
	// not fired: the admission is a decision, not a copy of a spent ledger.
	if ctx.Err() != nil {
		t.Fatalf("within-budget admission cancelled the owner context: %v", ctx.Err())
	}
}

func TestAdmitForAttemptRefusesWhenAggregateBudgetExceeded(t *testing.T) {
	cases := []struct {
		name   string
		policy AutonomyPolicy
		reason string
	}{
		{"token limit", AutonomyPolicy{MaxTokenCount: 5}, BudgetReasonTokenExceeded},
		{"cost limit", AutonomyPolicy{MaxCostUSDTicks: 5}, BudgetReasonCostExceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, budget, cancel := newTaskUsageBudget(context.Background(), &c.policy)
			defer cancel(nil)
			// A single JEV managed charge exhausts the owner's aggregate budget.
			budget.RecordUsage("jev", "decision", agent.TokenUsage{InputTokens: 100, OutputTokens: 100, CostUSDTicks: 100})
			a := AdmitForAttempt("task-a", 0, false, budget)
			if a.Admitted {
				t.Fatalf("exhausted budget: admission admitted %+v", a)
			}
			if a.ReasonCode != c.reason {
				t.Fatalf("refusal reason = %q, want %q", a.ReasonCode, c.reason)
			}
			if !errors.Is(budget.Check(), budgetErrForReason(c.reason)) {
				t.Fatalf("owner aggregate budget not in the refused state")
			}
			if ctx.Err() == nil {
				t.Fatalf("exhausted budget did not cancel the owner context")
			}
			if err := a.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestAdmitForAttemptRefusesUnknownUsage(t *testing.T) {
	// Unknown usage against a configured limit must refuse, not silently admit.
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: 10})
	defer cancel(nil)
	if err := budget.RejectUnreportedUsage(); err == nil {
		t.Fatalf("expected the owner aggregate budget to become unavailable")
	}
	a := AdmitForAttempt("task-a", 0, true, budget)
	if a.Admitted || a.ReasonCode != BudgetReasonUsageUnavailable {
		t.Fatalf("unknown usage admitted %+v, want refused with %q", a, BudgetReasonUsageUnavailable)
	}
	if ctx.Err() == nil {
		t.Fatalf("unknown usage did not cancel the owner context")
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestAdmitForAttemptNilBudgetFailsClosed(t *testing.T) {
	// A missing ledger admits nothing rather than silently unbinding the task.
	a := AdmitForAttempt("task-a", 0, false, nil)
	if a.Admitted {
		t.Fatalf("nil budget admitted a chargeable attempt")
	}
	if a.ReasonCode != BudgetReasonOwnerRefused {
		t.Fatalf("nil budget refusal reason = %q, want %q", a.ReasonCode, BudgetReasonOwnerRefused)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

// TestBudgetAdmissionIsNeverRemainingCount proves the F2 invariant directly: the
// admission is a decision, not a copy of remaining budget the AI could spend
// against. After the owner's aggregate ledger is exhausted, a second admission
// is refused; nothing in the admission carries a remaining token/cost count that
// could survive past the owner's ledger.
func TestBudgetAdmissionIsNeverRemainingCount(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: 10})
	defer cancel(nil)
	fresh := AdmitForAttempt("task-a", 0, true, budget)
	if !fresh.Admitted {
		t.Fatalf("fresh aggregate budget refused: %+v", fresh)
	}
	if ctx.Err() != nil {
		t.Fatalf("fresh admission cancelled the owner context: %v", ctx.Err())
	}
	// Exhaust the one ledger through the managed JEV charge.
	budget.RecordUsage("jev", "decision", agent.TokenUsage{InputTokens: 100})
	refused := AdmitForAttempt("task-a", 0, true, budget)
	if refused.Admitted {
		t.Fatalf("exhausted aggregate budget admitted a chargeable attempt")
	}
	if refused.ReasonCode != BudgetReasonTokenExceeded {
		t.Fatalf("exhausted refusal reason = %q, want %q", refused.ReasonCode, BudgetReasonTokenExceeded)
	}
	if err := refused.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func budgetErrForReason(reason string) error {
	switch reason {
	case BudgetReasonTokenExceeded:
		return errTaskTokenLimit
	case BudgetReasonCostExceeded:
		return errTaskCostLimit
	default:
		return errTaskUsageUnavailable
	}
}
