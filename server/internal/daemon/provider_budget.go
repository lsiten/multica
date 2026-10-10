package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// budgetBreachFromSnapshot reports whether a provider usage snapshot has reached
// the task's cost or token autonomy limit. It is the shared breach seam of the
// F3 provider-run migration: both the budget watcher (runBudgetWatcher) and the
// final result computation evaluate the same snapshot through it, so the legacy
// in-process runner and the per-execution task worker enforce identical autonomy
// limits without a *Daemon reference. It is pure: it reads only the passed facts
// and mutates nothing.
func budgetBreachFromSnapshot(usage agent.TokenUsage, opts agent.ExecOptions) (costReached, tokenReached bool) {
	costReached = opts.CostLimitUSDTicks > 0 && usage.CostUSDTicks >= opts.CostLimitUSDTicks
	tokenReached = opts.TokenLimit > 0 && reportedUsageTokenLimitReached(usage, opts.TokenLimit)
	return costReached, tokenReached
}

// runBudgetWatcher is the shared budget-enforcement seam of the F3 provider-run
// migration. It polls a provider session's usage snapshot on a fixed interval and,
// once the task's cost or token autonomy limit is reached, records the breach on
// the passed flags and cancels the agent context so the run stops. The legacy
// in-process runner (executeAndDrain) and the per-execution task worker (F3) call
// the same function, so a worker that owns the provider run enforces identical
// autonomy limits without a *Daemon reference.
//
// It is side-effect-free with respect to daemon state: it reads only the passed
// facts and the session, and mutates only the passed breach flags and the passed
// cancel. Its behaviour is covered by provider_budget_test.go and by the full
// daemon suite.
func runBudgetWatcher(budgetCtx context.Context, session *agent.Session, opts agent.ExecOptions, budget *taskUsageBudget, source string, costReached, tokenReached *atomic.Bool, cancel context.CancelFunc, taskLog *slog.Logger) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			usage, ok := session.UsageSnapshot()
			if ok && budget != nil {
				model := opts.Model
				if model == "" {
					model = "unknown"
				}
				budget.RecordSnapshot(source, budget.primaryProvider, map[string]agent.TokenUsage{model: usage})
			}
			cost, tokens := budgetBreachFromSnapshot(usage, opts)
			if ok && (cost || tokens) {
				costReached.Store(true)
				if tokens {
					tokenReached.Store(true)
				}
				taskLog.Warn("provider-reported autonomy limit reached; stopping execution", "cost_usd_ticks", usage.CostUSDTicks, "max_cost_usd_ticks", opts.CostLimitUSDTicks, "max_token_count", opts.TokenLimit)
				cancel()
				return
			}
		case <-budgetCtx.Done():
			return
		}
	}
}

// budgetResultFor wraps a provider result with the task's budget-enforcement
// state, reconciling the usage snapshot and marking the cost/token breach. It is
// the completion of the shared budget-enforcement seam of the F3 provider-run
// migration (alongside budgetBreachFromSnapshot and runBudgetWatcher): the legacy
// in-process runner (executeAndDrain) and the per-execution task worker apply the
// same budget verdict to the final result without a *Daemon reference.
//
// It is side-effect-free with respect to daemon state: it reads only the passed
// facts and the session, records the snapshot into the passed budget (if any),
// and mutates only the passed breach flags. Its behaviour is covered by
// provider_budget_test.go and by the full daemon suite.
func budgetResultFor(result agent.Result, session *agent.Session, opts agent.ExecOptions, budget *taskUsageBudget, source string, costReached, tokenReached *atomic.Bool) agent.Result {
	reached := costReached.Load()
	var snapshot agent.TokenUsage
	var snapshotKnown bool
	if session.UsageSnapshot != nil {
		snapshot, snapshotKnown = session.UsageSnapshot()
		cost, tokens := budgetBreachFromSnapshot(snapshot, opts)
		if cost {
			reached = true
			costReached.Store(true)
		}
		if tokens {
			reached = true
			tokenReached.Store(true)
		}
	}

	if snapshotKnown {
		result.Usage = reconcileUsageSnapshot(result.Usage, opts.Model, snapshot)
		if budget != nil {
			budget.RecordSnapshot(source, budget.primaryProvider, result.Usage)
		}
	}
	if budget != nil {
		if errors.Is(budget.Check(), errTaskTokenLimit) {
			tokenReached.Store(true)
			reached = true
		}
		if errors.Is(budget.Check(), errTaskCostLimit) {
			reached = true
		}
	}
	if !reached {
		return result
	}
	result.BudgetExceeded = true
	result.TokenBudgetExceeded = tokenReached.Load()
	if snapshotKnown {
		result.Usage = reconcileUsageSnapshot(result.Usage, opts.Model, snapshot)
	}
	return result
}
