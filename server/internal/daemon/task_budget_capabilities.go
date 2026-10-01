package daemon

// Limits are enforced at provider usage-notification boundaries. Providers
// without that contract must not accept a budget they can only audit later.
func supportsLiveTaskBudget(provider string, policy *AutonomyPolicy) bool {
	if policy == nil || (policy.MaxTokenCount == 0 && policy.MaxCostUSDTicks == 0) {
		return true
	}
	switch provider {
	case "codex", "claude":
		return policy.MaxCostUSDTicks == 0
	case "grok", "hermes", "mcode", "qoder", "reasonix", "traecli", "zeroclaw":
		return true
	default:
		return false
	}
}
