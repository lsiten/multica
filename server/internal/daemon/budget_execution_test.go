package daemon

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

type budgetTestBackend struct {
	cost atomic.Int64
}

func (b *budgetTestBackend) Execute(ctx context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	messages := make(chan agent.Message)
	results := make(chan agent.Result, 1)
	go func() {
		defer close(messages)
		<-ctx.Done()
		results <- agent.Result{Status: "aborted", Error: "context cancelled"}
	}()
	return &agent.Session{
		Messages: messages,
		Result:   results,
		UsageSnapshot: func() (agent.TokenUsage, bool) {
			cost := b.cost.Load()
			return agent.ReportedCostSnapshot(agent.TokenUsage{CostUSDTicks: cost})
		},
	}, nil
}

func TestExecuteAndDrainStopsAndPreservesReportedBudgetSnapshot(t *testing.T) {
	backend := &budgetTestBackend{}
	d := &Daemon{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	backend.cost.Store(10)

	result, _, err := d.executeAndDrain(context.Background(), backend, "test", agent.ExecOptions{
		CostLimitUSDTicks: 10,
	}, slog.Default(), "budget-test", "", new(atomic.Int32))
	if err != nil {
		t.Fatal(err)
	}
	if !result.BudgetExceeded {
		t.Fatalf("budget stop was not reported: %+v", result)
	}
	usage := result.Usage["unknown"]
	if usage.CostUSDTicks != 10 {
		t.Fatalf("reported usage = %+v, want cost 10", usage)
	}
}

func TestReconcileUsageSnapshotDoesNotDoubleCountFinalResult(t *testing.T) {
	usage := map[string]agent.TokenUsage{"provider-model": {
		InputTokens: 5, CostUSDTicks: 10,
	}}
	got := reconcileUsageSnapshot(usage, "provider-model", agent.TokenUsage{
		InputTokens: 5, CostUSDTicks: 10,
	})
	if got["provider-model"].CostUSDTicks != 10 {
		t.Fatalf("cost was double counted: %+v", got)
	}
	multi := reconcileUsageSnapshot(map[string]agent.TokenUsage{
		"first":  {CostUSDTicks: 4},
		"second": {CostUSDTicks: 6},
	}, "unknown", agent.TokenUsage{CostUSDTicks: 6})
	if len(multi) != 2 {
		t.Fatalf("snapshot introduced a duplicate model row: %+v", multi)
	}
}

func TestMergeUsageSaturatesProviderCost(t *testing.T) {
	got := mergeUsage(
		map[string]agent.TokenUsage{"model": {CostUSDTicks: int64(^uint64(0) >> 1)}},
		map[string]agent.TokenUsage{"model": {CostUSDTicks: 1}},
	)
	if got["model"].CostUSDTicks != int64(^uint64(0)>>1) {
		t.Fatalf("cost overflowed: %+v", got)
	}
}

func TestReconcileFreshRetryPreservesBudgetStopAndMergesAttempts(t *testing.T) {
	first := agent.Result{
		Status: "failed",
		Usage:  map[string]agent.TokenUsage{"model": {CostUSDTicks: 4}},
	}
	retry := agent.Result{
		Status:         "failed",
		BudgetExceeded: true,
		Usage:          map[string]agent.TokenUsage{"model": {CostUSDTicks: 6}},
	}
	got, _ := reconcileFreshRetryResult(first, first.Usage, 0, retry, 0, nil)
	if !got.BudgetExceeded {
		t.Fatal("retry budget stop was lost when the retry had no new session")
	}
	if got.Usage["model"].CostUSDTicks != 10 {
		t.Fatalf("merged retry cost = %+v, want 10", got.Usage)
	}
}

func TestRemainingAutonomyCostLimitUsesUnspentAllowance(t *testing.T) {
	if remaining, ok := remainingAutonomyCostLimit(10, 4); !ok || remaining != 6 {
		t.Fatalf("remaining=%d canRetry=%v, want 6,true", remaining, ok)
	}
	if remaining, ok := remainingAutonomyCostLimit(10, 10); ok || remaining != 0 {
		t.Fatalf("spent budget must stop retry: remaining=%d canRetry=%v", remaining, ok)
	}
}
