package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TestBudgetBreachFromSnapshot is the behaviour-preservation gate for the shared
// breach seam of the F3 provider-run migration: both the budget watcher and the
// final result computation must evaluate the same snapshot identically, so the
// legacy runner and the per-execution task worker enforce the same autonomy
// limits.
func TestBudgetBreachFromSnapshot(t *testing.T) {
	// No limits: a non-zero usage never breaches.
	if cost, token := budgetBreachFromSnapshot(agent.TokenUsage{CostUSDTicks: 100, InputTokens: 1000, OutputTokens: 1000}, agent.ExecOptions{}); cost || token {
		t.Fatalf("no limits should never breach, got cost=%v token=%v", cost, token)
	}

	// Cost limit: usage at or above the cost limit breaches cost; below does not.
	costOpts := agent.ExecOptions{CostLimitUSDTicks: 700}
	if cost, _ := budgetBreachFromSnapshot(agent.TokenUsage{CostUSDTicks: 700}, costOpts); !cost {
		t.Fatalf("cost at the limit should breach, got cost=%v", cost)
	}
	if cost, _ := budgetBreachFromSnapshot(agent.TokenUsage{CostUSDTicks: 699}, costOpts); cost {
		t.Fatalf("cost below the limit should not breach")
	}

	// Token limit: total usage at or above the token limit breaches token.
	tokOpts := agent.ExecOptions{TokenLimit: 1000}
	if _, token := budgetBreachFromSnapshot(agent.TokenUsage{InputTokens: 600, OutputTokens: 600}, tokOpts); !token {
		t.Fatalf("token total at the limit should breach token")
	}
	if _, token := budgetBreachFromSnapshot(agent.TokenUsage{InputTokens: 400, OutputTokens: 400}, tokOpts); token {
		t.Fatalf("token total below the limit should not breach")
	}

	// Both limits are independent.
	both := agent.ExecOptions{CostLimitUSDTicks: 100, TokenLimit: 1000}
	if cost, token := budgetBreachFromSnapshot(agent.TokenUsage{CostUSDTicks: 100, InputTokens: 500, OutputTokens: 600}, both); !cost || !token {
		t.Fatalf("both limits reached should breach both, got cost=%v token=%v", cost, token)
	}
}

// TestRunBudgetWatcherReachesLimit verifies the budget watcher (the shared
// budget-enforcement seam of the F3 provider-run migration) records the breach
// flags and cancels the agent context exactly when a provider usage snapshot
// reaches the task's autonomy limit. A nil budget is exercised so the test does
// not depend on daemon budget bookkeeping; the breach decision is driven purely
// by the snapshot and opts, the same facts the task worker will supply.
func TestRunBudgetWatcherReachesLimit(t *testing.T) {
	var cost, token atomic.Bool
	session := &agent.Session{
		UsageSnapshot: func() (agent.TokenUsage, bool) {
			return agent.TokenUsage{CostUSDTicks: 700, InputTokens: 1000, OutputTokens: 1000}, true
		},
	}
	agentCtx, agentCancel := context.WithCancel(context.Background())
	budgetCtx, stopBudget := context.WithCancel(agentCtx)
	defer stopBudget()
	cancelled := make(chan struct{})
	go func() {
		<-agentCtx.Done()
		close(cancelled)
	}()
	opts := agent.ExecOptions{CostLimitUSDTicks: 700, TokenLimit: 1000}
	done := make(chan struct{})
	go func() {
		runBudgetWatcher(budgetCtx, session, opts, nil, "attempt:1", &cost, &token, agentCancel, slog.Default())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not return after reaching the budget")
	}
	if !cost.Load() {
		t.Fatalf("cost flag should be set when the cost limit is reached")
	}
	if !token.Load() {
		t.Fatalf("token flag should be set when the token limit is reached")
	}
	select {
	case <-cancelled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("agent context was not cancelled when the budget was reached")
	}
}

// TestRunBudgetWatcherStaysSilentUnderLimit verifies the watcher does not cancel
// the agent context or set breach flags while a provider usage snapshot stays
// under the autonomy limit; it returns only when budgetCtx is done. This is the
// fail-closed half of the shared budget seam: an under-limit run must never be
// force-stopped.
func TestRunBudgetWatcherStaysSilentUnderLimit(t *testing.T) {
	var cost, token atomic.Bool
	session := &agent.Session{
		UsageSnapshot: func() (agent.TokenUsage, bool) {
			return agent.TokenUsage{CostUSDTicks: 10, InputTokens: 10, OutputTokens: 10}, true
		},
	}
	agentCtx, agentCancel := context.WithCancel(context.Background())
	budgetCtx, stopBudget := context.WithCancel(agentCtx)
	defer stopBudget()
	opts := agent.ExecOptions{CostLimitUSDTicks: 700, TokenLimit: 1000}
	done := make(chan struct{})
	go func() {
		runBudgetWatcher(budgetCtx, session, opts, nil, "attempt:1", &cost, &token, agentCancel, slog.Default())
		close(done)
	}()
	// Let several ticks pass; an under-limit watcher must not cancel agentCtx.
	time.Sleep(300 * time.Millisecond)
	if agentCtx.Err() != nil {
		t.Fatalf("agent context should not be cancelled while under the limit: %v", agentCtx.Err())
	}
	if cost.Load() || token.Load() {
		t.Fatalf("breach flags must stay false while under the limit")
	}
	stopBudget()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not return after budgetCtx was cancelled")
	}
}

// TestBudgetResultFor is the behaviour-preservation gate for the shared budget
// verdict seam (budgetResultFor), which the legacy runner and the per-execution
// task worker apply to the final provider result. It must mark the result
// budget-exceeded and set the breach flags exactly when a usage snapshot reaches
// the autonomy limit, and leave an under-limit or snapshot-less result untouched.
func TestBudgetResultFor(t *testing.T) {
	// Over-limit snapshot marks the result budget-exceeded and sets the flags.
	var cost, token atomic.Bool
	session := &agent.Session{
		UsageSnapshot: func() (agent.TokenUsage, bool) {
			return agent.TokenUsage{CostUSDTicks: 700, InputTokens: 1000, OutputTokens: 1000}, true
		},
	}
	opts := agent.ExecOptions{CostLimitUSDTicks: 700, TokenLimit: 1000}
	out := budgetResultFor(agent.Result{Status: "completed"}, session, opts, nil, "attempt:1", &cost, &token)
	if !out.BudgetExceeded {
		t.Fatalf("result should be budget-exceeded when the cost limit is reached")
	}
	if !cost.Load() {
		t.Fatalf("cost flag should be set when the cost limit is reached")
	}
	if !token.Load() {
		t.Fatalf("token flag should be set when the token total (2000) exceeds the limit (1000)")
	}

	// Under-limit snapshot leaves the result and flags untouched.
	var cost2, token2 atomic.Bool
	under := &agent.Session{
		UsageSnapshot: func() (agent.TokenUsage, bool) {
			return agent.TokenUsage{CostUSDTicks: 10, InputTokens: 10, OutputTokens: 10}, true
		},
	}
	out2 := budgetResultFor(agent.Result{Status: "completed"}, under, opts, nil, "attempt:1", &cost2, &token2)
	if out2.BudgetExceeded {
		t.Fatalf("result should not be budget-exceeded while under the limit")
	}
	if cost2.Load() || token2.Load() {
		t.Fatalf("breach flags must stay false while under the limit")
	}

	// No usage snapshot and no budget: the result passes through unchanged.
	var cost3, token3 atomic.Bool
	out3 := budgetResultFor(agent.Result{Status: "completed"}, &agent.Session{}, agent.ExecOptions{CostLimitUSDTicks: 500}, nil, "attempt:1", &cost3, &token3)
	if out3.BudgetExceeded {
		t.Fatalf("result with no snapshot and no budget should not be budget-exceeded")
	}
}
