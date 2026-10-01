package agent

import "math"

func totalTokenUsage(usage map[string]TokenUsage) TokenUsage {
	var total TokenUsage
	for _, u := range usage {
		total = addTokenUsage(total, u)
	}
	return total
}

func addTokenUsage(a, b TokenUsage) TokenUsage {
	add := func(a, b int64) int64 {
		a, b = max(a, 0), max(b, 0)
		if a > math.MaxInt64-b {
			return math.MaxInt64
		}
		return a + b
	}
	return TokenUsage{InputTokens: add(a.InputTokens, b.InputTokens), OutputTokens: add(a.OutputTokens, b.OutputTokens), CacheReadTokens: add(a.CacheReadTokens, b.CacheReadTokens), CacheWriteTokens: add(a.CacheWriteTokens, b.CacheWriteTokens), CostUSDTicks: add(a.CostUSDTicks, b.CostUSDTicks)}
}
