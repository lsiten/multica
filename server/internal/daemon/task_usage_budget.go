package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/multica-ai/multica/server/pkg/agent"
)

var errTaskTokenLimit = errors.New("task token budget exceeded")
var errTaskCostLimit = errors.New("task cost budget exceeded")
var errTaskUsageUnavailable = errors.New("task provider cannot report usage for the configured budget")

type taskBudgetContextKey struct{}
type taskUsageSource struct {
	provider string
	usage    map[string]agent.TokenUsage
}

// A source is one execution attempt or one managed provider. Replacing an
// attempt snapshot avoids charging both its live counters and final result.
type taskUsageBudget struct {
	mu              sync.Mutex
	primaryProvider string
	sources         map[string]taskUsageSource
	sequence        uint64
	tokens, cost    int64
	cancel          context.CancelCauseFunc
	err             error
}

func newTaskUsageBudget(ctx context.Context, policy *AutonomyPolicy) (context.Context, *taskUsageBudget, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	b := &taskUsageBudget{sources: make(map[string]taskUsageSource), tokens: autonomyPolicyTokenLimit(policy), cost: effectiveAutonomyCostLimit(policy), cancel: cancel}
	return context.WithValue(ctx, taskBudgetContextKey{}, b), b, cancel
}

func taskBudgetFromContext(ctx context.Context) *taskUsageBudget {
	b, _ := ctx.Value(taskBudgetContextKey{}).(*taskUsageBudget)
	return b
}

func (b *taskUsageBudget) nextAttempt() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sequence++
	return fmt.Sprintf("attempt:%d", b.sequence)
}

func (b *taskUsageBudget) RecordSnapshot(source, provider string, usage map[string]agent.TokenUsage) {
	if b == nil {
		return
	}
	copyUsage := make(map[string]agent.TokenUsage, len(usage))
	for model, u := range usage {
		copyUsage[model] = u
	}
	b.mu.Lock()
	b.sources[source] = taskUsageSource{provider: provider, usage: copyUsage}
	b.checkLocked()
	err := b.err
	b.mu.Unlock()
	if err != nil {
		b.cancel(err)
	}
}

// RecordUsage charges a single completed managed-model request exactly once.
// provider must distinguish managed usage (for example "jev") from the Agent.
func (b *taskUsageBudget) RecordUsage(provider, model string, usage agent.TokenUsage) {
	if b == nil {
		return
	}
	b.mu.Lock()
	key := "managed:" + provider
	source := b.sources[key]
	if source.usage == nil {
		source = taskUsageSource{provider: provider, usage: make(map[string]agent.TokenUsage)}
	}
	source.usage[model] = addTaskUsage(source.usage[model], usage)
	b.sources[key] = source
	b.checkLocked()
	err := b.err
	b.mu.Unlock()
	if err != nil {
		b.cancel(err)
	}
}

func (b *taskUsageBudget) Check() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Unknown chargeable usage must not silently turn a bounded task unbounded.
func (b *taskUsageBudget) RejectUnreportedUsage() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.err == nil && (b.tokens > 0 || b.cost > 0) {
		b.err = errTaskUsageUnavailable
	}
	err := b.err
	b.mu.Unlock()
	if err != nil {
		b.cancel(err)
	}
	return err
}

func (b *taskUsageBudget) ValidateReportedUsage(tokensKnown, costKnown bool) error {
	if b == nil {
		return nil
	}
	if (b.tokens > 0 && !tokensKnown) || (b.cost > 0 && !costKnown) {
		return b.RejectUnreportedUsage()
	}
	return b.Check()
}

func (b *taskUsageBudget) checkLocked() {
	if b.err != nil {
		return
	}
	var total agent.TokenUsage
	for _, source := range b.sources {
		for _, usage := range source.usage {
			total = addTaskUsage(total, usage)
		}
	}
	switch {
	case reportedUsageTokenLimitReached(total, b.tokens):
		b.err = errTaskTokenLimit
	case b.cost > 0 && total.CostUSDTicks >= b.cost:
		b.err = errTaskCostLimit
	}
}

func (b *taskUsageBudget) Entries() []TaskUsageEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	byProvider := make(map[string]map[string]agent.TokenUsage)
	for _, source := range b.sources {
		if byProvider[source.provider] == nil {
			byProvider[source.provider] = make(map[string]agent.TokenUsage)
		}
		for model, u := range source.usage {
			byProvider[source.provider][model] = addTaskUsage(byProvider[source.provider][model], u)
		}
	}
	var entries []TaskUsageEntry
	for provider, usage := range byProvider {
		entries = append(entries, usageEntriesForResult(provider, usage)...)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Provider != entries[j].Provider {
			return entries[i].Provider < entries[j].Provider
		}
		return entries[i].Model < entries[j].Model
	})
	return entries
}

func addTaskUsage(a, b agent.TokenUsage) agent.TokenUsage {
	return agent.TokenUsage{
		InputTokens: saturatingAddCostTicks(a.InputTokens, b.InputTokens), OutputTokens: saturatingAddCostTicks(a.OutputTokens, b.OutputTokens),
		CacheReadTokens: saturatingAddCostTicks(a.CacheReadTokens, b.CacheReadTokens), CacheWriteTokens: saturatingAddCostTicks(a.CacheWriteTokens, b.CacheWriteTokens),
		CostUSDTicks: saturatingAddCostTicks(a.CostUSDTicks, b.CostUSDTicks),
	}
}

func finalizeTaskBudget(ctx context.Context, policy *AutonomyPolicy, budget *taskUsageBudget, result *TaskResult, err *error) {
	result.Usage = budget.Entries()
	reason, comment := "", ""
	switch {
	case errors.Is(budget.Check(), errTaskTokenLimit):
		reason, comment = "token_budget_exceeded", "Task stopped at its reported token limit."
	case errors.Is(budget.Check(), errTaskCostLimit):
		reason, comment = "budget_exceeded", "Task stopped at its reported cost limit."
	case errors.Is(budget.Check(), errTaskUsageUnavailable):
		reason, comment = "budget_usage_unsupported", "The provider cannot report usage required for this task's limits."
	case policy != nil && policy.MaxDurationSeconds > 0 && errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		reason, comment = "duration_exceeded", "Task stopped at its autonomy time limit."
	}
	if reason != "" {
		result.Status, result.FailureReason, result.Comment = "blocked", reason, comment
		*err = nil
	}
}
