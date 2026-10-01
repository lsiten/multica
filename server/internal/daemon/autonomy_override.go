package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Message overrides tune execution limits, never the Agent's capability grants.
func mergeRuntimeConfigAutonomy(base, override json.RawMessage) (json.RawMessage, error) {
	values := map[string]json.RawMessage{}
	if raw := strings.TrimSpace(string(base)); raw != "" && raw != "null" {
		if err := json.Unmarshal(base, &values); err != nil || values == nil {
			return nil, fmt.Errorf("%w: malformed runtime_config", errInvalidAutonomyPolicy)
		}
	}
	var overlay map[string]json.RawMessage
	if err := json.Unmarshal(override, &overlay); err != nil || overlay == nil {
		return nil, fmt.Errorf("%w: malformed runtime_config_override", errInvalidAutonomyPolicy)
	}
	if raw, exists := overlay[autonomyPolicyKey]; exists {
		policy := map[string]json.RawMessage{}
		if saved := values[autonomyPolicyKey]; len(saved) > 0 && string(saved) != "null" {
			if err := json.Unmarshal(saved, &policy); err != nil || policy == nil {
				return nil, fmt.Errorf("%w: malformed saved policy", errInvalidAutonomyPolicy)
			}
		}
		var message map[string]json.RawMessage
		if string(raw) == "null" {
			message = map[string]json.RawMessage{"mode": json.RawMessage(`"normal"`)}
		} else if err := json.Unmarshal(raw, &message); err != nil || message == nil {
			return nil, fmt.Errorf("%w: malformed message policy", errInvalidAutonomyPolicy)
		}
		for key, value := range message {
			switch key {
			case "mode", "max_duration_seconds", "max_token_count", "max_cost_usd_ticks":
				policy[key] = value
			default:
				return nil, fmt.Errorf("%w: message cannot override %s", errInvalidAutonomyPolicy, key)
			}
		}
		if string(policy["mode"]) == `"normal"` {
			policy["max_duration_seconds"] = json.RawMessage("0")
			policy["max_token_count"] = json.RawMessage("0")
			policy["max_cost_usd_ticks"] = json.RawMessage("0")
		}
		encoded, err := json.Marshal(policy)
		if err != nil {
			return nil, err
		}
		values[autonomyPolicyKey] = encoded
	}
	return json.Marshal(values)
}
