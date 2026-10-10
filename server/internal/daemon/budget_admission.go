package daemon

import (
	"errors"
	"fmt"
	"strings"
)

// BudgetAdmission is the task owner's (control until F3, the task worker
// afterwards) admission for one chargeable JEV attempt the AI service wants to
// make. It is the minimum typed boundary for the F2 budget cut: the aggregate
// task budget stays in the task owner, which owns the one ledger and the one
// cancellation cause shared by primary attempts and JEV. The AI service must
// never replace it with an isolated copy of remaining budget: it asks the owner
// for admission and receives a decision bound to the owner's aggregate budget,
// not a number it could spend against on its own.
//
// The decision is admitted or refused, never a remaining token/cost count.
// Unknown usage is a refusal, not an admission: the owner must not let unknown
// usage turn a bounded task unbounded. A refusal reason is evidence, never
// authorization to spend, and the admission never authorizes completion.
type BudgetAdmission struct {
	// TaskID and BudgetPolicyRevision bind the admission to the task owner's
	// aggregate budget. The owner, not the AI service, is the sole authority
	// for the token/cost ledger and its cancellation cause.
	TaskID               string
	BudgetPolicyRevision int64
	// UsageRequired mirrors the owner's usage-required flag. When true and the
	// owner cannot report usage for the configured budget, admission is refused
	// rather than silently accepted.
	UsageRequired bool
	// Admitted is the owner's decision: true allows the chargeable attempt,
	// false refuses it. It is never a remaining-budget number.
	Admitted bool
	// ReasonCode explains a refusal (BudgetReasonTokenExceeded,
	// BudgetReasonCostExceeded, BudgetReasonUsageUnavailable or
	// BudgetReasonOwnerRefused); it is empty when Admitted is true. It is
	// evidence, never authorization.
	ReasonCode string
}

// Refusal reasons. They are the closed set the owner emits on refusal; AI must
// not infer spending authority from any code outside it.
const (
	BudgetReasonTokenExceeded    = "token_budget_exceeded"
	BudgetReasonCostExceeded     = "budget_exceeded"
	BudgetReasonUsageUnavailable = "budget_usage_unsupported"
	BudgetReasonOwnerRefused     = "budget_refused"
)

// Validate enforces the F2 budget boundary. It fails closed: an empty task id, a
// negative policy revision, a refusal without a known reason, or an admitted
// admission that carries a reason is rejected. The admission is a decision,
// never a remaining-budget count, so it cannot carry the isolated copy of
// remaining budget the F2 contract forbids.
func (a BudgetAdmission) Validate() error {
	if strings.TrimSpace(a.TaskID) == "" {
		return fmt.Errorf("budget admission has no task id")
	}
	if a.BudgetPolicyRevision < 0 {
		return fmt.Errorf("budget admission has a negative policy revision")
	}
	if a.Admitted {
		if strings.TrimSpace(a.ReasonCode) != "" {
			return fmt.Errorf("budget admission is admitted but carries a refusal reason %q", a.ReasonCode)
		}
		return nil
	}
	if !knownBudgetReason(a.ReasonCode) {
		return fmt.Errorf("budget admission is refused but carries an unknown reason %q", a.ReasonCode)
	}
	return nil
}

func knownBudgetReason(code string) bool {
	switch code {
	case BudgetReasonTokenExceeded, BudgetReasonCostExceeded, BudgetReasonUsageUnavailable, BudgetReasonOwnerRefused:
		return true
	default:
		return false
	}
}

// AdmitForAttempt is the owner-side boundary that produces one admission from
// the task owner's aggregate budget. It is NOT the hot path: the cross-process
// admission lands in F3. It shows how the owner decides admitted/refused from
// the one ledger and one cancellation cause, so the AI service receives only the
// decision, never an isolated copy of remaining budget. A nil budget admits
// nothing (fail closed), so a missing ledger cannot silently unbound the task.
// It never mutates the budget: the aggregate ledger and its cancellation cause
// stay in the task owner.
func AdmitForAttempt(taskID string, budgetPolicyRevision int64, usageRequired bool, budget *taskUsageBudget) BudgetAdmission {
	a := BudgetAdmission{
		TaskID:               taskID,
		BudgetPolicyRevision: budgetPolicyRevision,
		UsageRequired:        usageRequired,
	}
	if budget == nil {
		a.Admitted = false
		a.ReasonCode = BudgetReasonOwnerRefused
		return a
	}
	if err := budget.Check(); err != nil {
		a.Admitted = false
		a.ReasonCode = reasonCodeForBudgetError(err)
		return a
	}
	a.Admitted = true
	return a
}

func reasonCodeForBudgetError(err error) string {
	switch {
	case errors.Is(err, errTaskTokenLimit):
		return BudgetReasonTokenExceeded
	case errors.Is(err, errTaskCostLimit):
		return BudgetReasonCostExceeded
	case errors.Is(err, errTaskUsageUnavailable):
		return BudgetReasonUsageUnavailable
	default:
		return BudgetReasonOwnerRefused
	}
}
