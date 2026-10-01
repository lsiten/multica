package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	autonomyPolicyKey      = "multica_autonomy"
	autonomyModeAutonomous = "autonomous"
	autonomyMaxDuration    = 24 * time.Hour
)

var errInvalidAutonomyPolicy = errors.New("invalid multica autonomy policy")

// AutonomyPolicy is the task-level policy carried inside an agent's existing
// runtime_config JSON. It is additive: agents without this object retain the
// existing execution behavior.
type AutonomyPolicy struct {
	Mode                        string
	MaxDurationSeconds          int
	MaxCostUSDTicks             int64
	MaxTokenCount               int64
	AllowedDecisionCapabilities []string
	AllowedIdentityActions      []string
}

func parseAutonomyPolicy(raw json.RawMessage) (*AutonomyPolicy, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: malformed runtime_config", errInvalidAutonomyPolicy)
	}
	policyRaw, ok := envelope[autonomyPolicyKey]
	if !ok || string(policyRaw) == "null" {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(policyRaw, &values); err != nil {
		return nil, fmt.Errorf("%w: malformed policy", errInvalidAutonomyPolicy)
	}
	policy := &AutonomyPolicy{}
	if err := decodePolicyString(values, "mode", &policy.Mode); err != nil {
		return nil, err
	}
	if err := decodePolicyInt(values, "max_duration_seconds", &policy.MaxDurationSeconds); err != nil {
		return nil, err
	}
	if err := decodePolicyInt64(values, "max_cost_usd_ticks", &policy.MaxCostUSDTicks); err != nil {
		return nil, err
	}
	if err := decodePolicyInt64(values, "max_token_count", &policy.MaxTokenCount); err != nil {
		return nil, err
	}
	if err := decodePolicyStrings(values, "allowed_decision_capabilities", &policy.AllowedDecisionCapabilities); err != nil {
		return nil, err
	}
	if err := decodePolicyStrings(values, "allowed_identity_actions", &policy.AllowedIdentityActions); err != nil {
		return nil, err
	}
	for _, unsupported := range []string{"max_parallel_agents", "confirm_only"} {
		if _, exists := values[unsupported]; exists {
			return nil, fmt.Errorf("%w: %s is not enforced by this runtime", errInvalidAutonomyPolicy, unsupported)
		}
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return policy, nil
}

func decodePolicyString(values map[string]json.RawMessage, key string, out *string) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s must be a string", errInvalidAutonomyPolicy, key)
	}
	return nil
}

func decodePolicyInt(values map[string]json.RawMessage, key string, out *int) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s must be an integer", errInvalidAutonomyPolicy, key)
	}
	return nil
}

func decodePolicyInt64(values map[string]json.RawMessage, key string, out *int64) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s must be an integer", errInvalidAutonomyPolicy, key)
	}
	return nil
}

func decodePolicyStrings(values map[string]json.RawMessage, key string, out *[]string) error {
	raw, ok := values[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: %s must be an array of strings", errInvalidAutonomyPolicy, key)
	}
	return nil
}

func (policy *AutonomyPolicy) validate() error {
	if policy == nil {
		return nil
	}
	if policy.Mode != autonomyModeAutonomous && policy.Mode != "normal" {
		return fmt.Errorf("%w: mode must be %q", errInvalidAutonomyPolicy, autonomyModeAutonomous)
	}
	maxDurationSeconds := int(autonomyMaxDuration / time.Second)
	if policy.MaxDurationSeconds < 0 || policy.MaxDurationSeconds > maxDurationSeconds {
		return fmt.Errorf("%w: max_duration_seconds must be between 0 and %d", errInvalidAutonomyPolicy, maxDurationSeconds)
	}
	if policy.MaxCostUSDTicks < 0 {
		return fmt.Errorf("%w: max_cost_usd_ticks must not be negative", errInvalidAutonomyPolicy)
	}
	if policy.MaxTokenCount < 0 {
		return fmt.Errorf("%w: max_token_count must not be negative", errInvalidAutonomyPolicy)
	}
	for _, capability := range policy.AllowedDecisionCapabilities {
		if strings.TrimSpace(capability) == "" {
			return fmt.Errorf("%w: allowed_capabilities cannot contain empty values", errInvalidAutonomyPolicy)
		}
	}
	for _, action := range policy.AllowedIdentityActions {
		if action != "email" && action != "phone" {
			return fmt.Errorf("%w: unsupported identity action %q", errInvalidAutonomyPolicy, action)
		}
	}
	return nil
}

func (policy *AutonomyPolicy) allows(capability string) bool {
	if policy == nil || policy.AllowedDecisionCapabilities == nil {
		return true
	}
	for _, allowed := range policy.AllowedDecisionCapabilities {
		if allowed == capability {
			return true
		}
	}
	return false
}

func (policy *AutonomyPolicy) allowsIdentityAction(action string) bool {
	if policy == nil || len(policy.AllowedIdentityActions) == 0 {
		return false
	}
	for _, allowed := range policy.AllowedIdentityActions {
		if allowed == action {
			return true
		}
	}
	return false
}

func (policy *AutonomyPolicy) duration() time.Duration {
	if policy == nil || policy.MaxDurationSeconds == 0 {
		return 0
	}
	return time.Duration(policy.MaxDurationSeconds) * time.Second
}

// effectiveAutonomyCostLimit returns the provider-reported execution-cost
// policy. It is independent from the Agent identity manifest.
func effectiveAutonomyCostLimit(policy *AutonomyPolicy) int64 {
	limit := int64(0)
	if policy != nil {
		limit = policy.MaxCostUSDTicks
	}
	return limit
}

func autonomyPolicyTokenLimit(policy *AutonomyPolicy) int64 {
	if policy == nil {
		return 0
	}
	return policy.MaxTokenCount
}

func remainingAutonomyCostLimit(limit, spent int64) (int64, bool) {
	if limit <= 0 {
		return limit, true
	}
	if spent >= limit {
		return 0, false
	}
	return limit - spent, true
}

func reportedUsageCostExceeded(usage []TaskUsageEntry, limit int64) (int64, bool) {
	var total int64
	for _, entry := range usage {
		if entry.CostUSDTicks <= 0 {
			continue
		}
		if total > limit-entry.CostUSDTicks {
			return limit, true
		}
		total += entry.CostUSDTicks
	}
	return total, total > limit
}

func reportedUsageTokenCount(usage []TaskUsageEntry) (int64, bool) {
	var total int64
	for _, entry := range usage {
		for _, value := range []int64{entry.InputTokens, entry.OutputTokens, entry.CacheReadTokens, entry.CacheWriteTokens} {
			if value <= 0 || total > math.MaxInt64-value {
				if value > 0 {
					return math.MaxInt64, true
				}
				continue
			}
			total += value
		}
	}
	return total, false
}

func saturatingAddCostTicks(a, b int64) int64 {
	if a <= 0 {
		if b <= 0 {
			return 0
		}
		return b
	}
	if b <= 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func (policy *AutonomyPolicy) instructions() string {
	if policy == nil || policy.Mode == "normal" {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\nAutonomy policy: mode=autonomous. Continue routine work within the task scope without per-step authorization.")
	if policy.MaxDurationSeconds > 0 {
		fmt.Fprintf(&builder, " The task time limit is %d seconds.", policy.MaxDurationSeconds)
	}
	if policy.MaxCostUSDTicks > 0 {
		fmt.Fprintf(&builder, " The provider-reported execution cost limit is %d USD ticks.", policy.MaxCostUSDTicks)
	}
	if policy.MaxTokenCount > 0 {
		fmt.Fprintf(&builder, " The total token limit is %d tokens.", policy.MaxTokenCount)
	}
	if len(policy.AllowedDecisionCapabilities) > 0 {
		fmt.Fprintf(&builder, " Allowed decision capabilities: %s.", strings.Join(policy.AllowedDecisionCapabilities, ", "))
	}
	if len(policy.AllowedIdentityActions) > 0 {
		fmt.Fprintf(&builder, " Allowed identity actions: %s.", strings.Join(policy.AllowedIdentityActions, ", "))
	}
	builder.WriteString(" Pause for missing required information, policy or permission denial, security boundaries, or destructive actions.")
	return builder.String()
}
