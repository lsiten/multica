package service

import "context"

// ChatAutonomyPolicy is a per-message execution override. It is carried in the
// task context and never mutates the Agent's saved runtime configuration.
type ChatAutonomyPolicy struct {
	Mode               string `json:"mode"`
	MaxDurationSeconds int    `json:"max_duration_seconds"`
	MaxTokenCount      int64  `json:"max_token_count"`
	MaxCostUSDTicks    int64  `json:"max_cost_usd_ticks"`
}

type chatAutonomyPolicyContextKey struct{}

func WithChatAutonomyPolicy(ctx context.Context, policy *ChatAutonomyPolicy) context.Context {
	if policy == nil {
		return ctx
	}
	return context.WithValue(ctx, chatAutonomyPolicyContextKey{}, policy)
}

func chatAutonomyPolicyFromContext(ctx context.Context) *ChatAutonomyPolicy {
	policy, _ := ctx.Value(chatAutonomyPolicyContextKey{}).(*ChatAutonomyPolicy)
	return policy
}
