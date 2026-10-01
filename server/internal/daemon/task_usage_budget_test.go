package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestTaskBudgetIncludesPrimaryRetryAndConcurrentJev(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: 20})
	defer cancel(nil)
	first := budget.nextAttempt()
	budget.RecordSnapshot(first, "codex", map[string]agent.TokenUsage{"main": {InputTokens: 3}})
	budget.RecordSnapshot(first, "codex", map[string]agent.TokenUsage{"main": {InputTokens: 3, OutputTokens: 1}})
	budget.RecordSnapshot(budget.nextAttempt(), "codex", map[string]agent.TokenUsage{"main": {InputTokens: 4, OutputTokens: 2}})
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() { budget.RecordUsage("jev", "decision", agent.TokenUsage{InputTokens: 1}) })
	}
	group.Wait()
	if !errors.Is(context.Cause(ctx), errTaskTokenLimit) {
		t.Fatalf("task not cancelled: %v", context.Cause(ctx))
	}
	if !errors.Is(budget.Check(), errTaskTokenLimit) {
		t.Fatal("further calls permitted")
	}
	entries := budget.Entries()
	if len(entries) != 2 || entries[0].InputTokens != 7 || entries[0].OutputTokens != 3 || entries[1].InputTokens != 10 {
		t.Fatalf("double charged or lost usage: %+v", entries)
	}
}

func TestTaskBudgetRejectsUnknownLimitedUsage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		policy       AutonomyPolicy
		tokens, cost bool
		blocked      bool
	}{
		{"no limits", AutonomyPolicy{}, false, false, false},
		{"tokens only", AutonomyPolicy{MaxTokenCount: 10}, true, false, false},
		{"missing tokens", AutonomyPolicy{MaxTokenCount: 10}, false, true, true},
		{"missing cost", AutonomyPolicy{MaxCostUSDTicks: 10}, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, budget, cancel := newTaskUsageBudget(context.Background(), &tc.policy)
			defer cancel(nil)
			err := budget.ValidateReportedUsage(tc.tokens, tc.cost)
			if errors.Is(err, errTaskUsageUnavailable) != tc.blocked || (ctx.Err() != nil) != tc.blocked {
				t.Fatalf("err=%v context=%v", err, ctx.Err())
			}
		})
	}
}

func TestTaskBudgetOverflowCannotResetAllowance(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: math.MaxInt64})
	defer cancel(nil)
	budget.RecordUsage("jev", "m", agent.TokenUsage{InputTokens: math.MaxInt64 - 1})
	budget.RecordUsage("jev", "m", agent.TokenUsage{InputTokens: 9})
	if !errors.Is(context.Cause(ctx), errTaskTokenLimit) || budget.Entries()[0].InputTokens != math.MaxInt64 {
		t.Fatal("usage overflow reset budget")
	}
}

func TestAutonomyDeadlineWinsAfterFreshRetryFailure(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxDurationSeconds: 1})
	defer cancel(nil)
	budget.RecordSnapshot("first", "codex", map[string]agent.TokenUsage{"m": {InputTokens: 3}})
	budget.RecordSnapshot("retry", "codex", map[string]agent.TokenUsage{"m": {InputTokens: 2}})
	first := agent.Result{Status: "failed", SessionID: "poisoned"}
	retry := agent.Result{Status: "timeout", SessionID: "fresh"}
	result, _ := reconcileFreshRetryResult(first, nil, 0, retry, 0, nil)
	final := TaskResult{Status: result.Status, SessionID: result.SessionID, FailureReason: "agent_error"}
	deadlineCtx, expired := context.WithCancelCause(ctx)
	expired(context.DeadlineExceeded)
	err := errors.New("old failure")
	finalizeTaskBudget(deadlineCtx, &AutonomyPolicy{MaxDurationSeconds: 1}, budget, &final, &err)
	if final.FailureReason != "duration_exceeded" || final.Status != "blocked" || final.SessionID != "fresh" || err != nil || final.Usage[0].InputTokens != 5 {
		t.Fatalf("retry deadline lost: %+v err=%v", final, err)
	}
}

func TestJevUsageCancelsTaskAndDoesNotRetryChargeableResponse(t *testing.T) {
	ctx, budget, cancel := newTaskUsageBudget(context.Background(), &AutonomyPolicy{MaxTokenCount: 10})
	defer cancel(nil)
	budget.RecordSnapshot("primary", "codex", map[string]agent.TokenUsage{"m": {InputTokens: 7}})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":1}}}`)
	}))
	defer server.Close()
	s := llm2jevMCPServer{model: "decision", endpoint: server.URL, client: server.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := s.requestCompletion(ctx, "candidate")
	if !errors.Is(err, errTaskTokenLimit) || requests != 1 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
	if !errors.Is(context.Cause(ctx), errTaskTokenLimit) {
		t.Fatal("primary task context still live")
	}
	_, err = s.requestCompletion(ctx, "second")
	if !errors.Is(err, errTaskTokenLimit) || requests != 1 {
		t.Fatalf("request after limit reached: %v calls=%d", err, requests)
	}
	entries := budget.Entries()
	if len(entries) != 2 || entries[1].InputTokens != 1 || entries[1].CacheReadTokens != 1 || entries[1].OutputTokens != 1 {
		t.Fatalf("usage=%+v", entries)
	}
}

func TestTaskBudgetRejectsUnobservableProvider(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "hermes", "grok"} {
		if !supportsLiveTaskBudget(provider, &AutonomyPolicy{MaxTokenCount: 1}) {
			t.Fatalf("missing live tokens for %s", provider)
		}
	}
	for _, provider := range []string{"opencode", "cursor", "pi"} {
		if supportsLiveTaskBudget(provider, &AutonomyPolicy{MaxTokenCount: 1}) {
			t.Fatalf("silently bounded unsupported %s", provider)
		}
		if !supportsLiveTaskBudget(provider, &AutonomyPolicy{MaxDurationSeconds: 1}) {
			t.Fatalf("unnecessarily rejected duration for %s", provider)
		}
	}
	if supportsLiveTaskBudget("codex", &AutonomyPolicy{MaxCostUSDTicks: 1}) {
		t.Fatal("Codex cannot enforce live dollar limits")
	}
}
