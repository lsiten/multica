package daemon

import (
	"context"
	"encoding/json"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func recordJevCompletionUsage(ctx context.Context, model string, body []byte, chargeable bool) error {
	budget := taskBudgetFromContext(ctx)
	var response struct {
		Usage *struct {
			InputTokens      *int64 `json:"input_tokens"`
			OutputTokens     *int64 `json:"output_tokens"`
			CachedTokens     *int64 `json:"cached_tokens"`
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			CostUSDTicks     *int64 `json:"cost_usd_ticks"`
			PromptDetails    struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || response.Usage == nil {
		if chargeable {
			return budget.RejectUnreportedUsage()
		}
		return budget.Check()
	}
	u := response.Usage
	// Native SystemOne counts uncached input separately from cached tokens.
	if u.PromptTokens == nil && u.InputTokens != nil && u.OutputTokens != nil {
		cached := int64(0)
		if u.CachedTokens != nil {
			cached = *u.CachedTokens
		}
		known := *u.InputTokens >= 0 && *u.OutputTokens >= 0 && cached >= 0
		if known {
			budget.RecordUsage("jev", model, agent.TokenUsage{InputTokens: *u.InputTokens, OutputTokens: *u.OutputTokens, CacheReadTokens: cached})
		}
		return budget.ValidateReportedUsage(known, false)
	}
	tokensKnown := u.PromptTokens != nil && u.CompletionTokens != nil && *u.PromptTokens >= 0 && *u.CompletionTokens >= 0 && u.PromptDetails.CachedTokens >= 0 && u.PromptDetails.CachedTokens <= *u.PromptTokens
	costKnown := u.CostUSDTicks != nil && *u.CostUSDTicks >= 0
	var usage agent.TokenUsage
	if tokensKnown {
		usage.InputTokens, usage.CacheReadTokens, usage.OutputTokens = *u.PromptTokens-u.PromptDetails.CachedTokens, u.PromptDetails.CachedTokens, *u.CompletionTokens
	}
	if costKnown {
		usage.CostUSDTicks = *u.CostUSDTicks
	}
	budget.RecordUsage("jev", model, usage)
	return budget.ValidateReportedUsage(tokensKnown, costKnown)
}
