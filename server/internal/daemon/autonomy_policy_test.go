package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestParseAutonomyPolicyAbsentKeepsLegacyBehavior(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, []byte("{}"), []byte("{\"provider\":\"claude\"}")} {
		policy, err := parseAutonomyPolicy(raw)
		if err != nil || policy != nil {
			t.Fatalf("raw=%s policy=%+v err=%v", raw, policy, err)
		}
	}
}

func TestParseAutonomyPolicyValidatesAndBuildsInstructions(t *testing.T) {
	policy, err := parseAutonomyPolicy(json.RawMessage("{\"multica_autonomy\":{\"mode\":\"autonomous\",\"max_duration_seconds\":120,\"max_cost_usd_ticks\":500,\"allowed_decision_capabilities\":[\"decision\"],\"allowed_identity_actions\":[\"email\",\"phone\"]}}"))
	if err != nil {
		t.Fatal(err)
	}
	if policy == nil || policy.duration() != 120*time.Second {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if !policy.allows("decision") || policy.allows("payment") {
		t.Fatalf("capability allowlist not enforced: %+v", policy.AllowedDecisionCapabilities)
	}
	if !policy.allowsIdentityAction("email") || !policy.allowsIdentityAction("phone") || policy.allowsIdentityAction("payment") {
		t.Fatalf("identity action allowlist not enforced: %+v", policy.AllowedIdentityActions)
	}
	instructions := policy.instructions()
	for _, want := range []string{"mode=autonomous", "120 seconds", "provider-reported execution cost limit", "500 USD ticks", "Allowed decision capabilities: decision"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q: %s", want, instructions)
		}
	}
}

func TestParseAutonomyPolicyRejectsUnsafeValues(t *testing.T) {
	cases := []json.RawMessage{
		json.RawMessage("{\"multica_autonomy\":{\"mode\":\"assisted\"}}"),
		json.RawMessage("{\"multica_autonomy\":{\"mode\":\"autonomous\",\"max_duration_seconds\":86401}}"),
		json.RawMessage(fmt.Sprintf("{\"multica_autonomy\":{\"mode\":\"autonomous\",\"max_duration_seconds\":%d}}", math.MaxInt)),
		json.RawMessage("{\"multica_autonomy\":{\"mode\":\"autonomous\",\"max_parallel_agents\":17}}"),
		json.RawMessage("{\"multica_autonomy\":{\"mode\":\"autonomous\",\"confirm_only\":[\"payment\"]}}"),
	}
	for _, raw := range cases {
		if _, err := parseAutonomyPolicy(raw); !errors.Is(err, errInvalidAutonomyPolicy) {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
}

func TestEffectiveAutonomyCostLimitExcludesPaymentBudget(t *testing.T) {
	if got := effectiveAutonomyCostLimit(nil); got != 0 {
		t.Fatalf("unbounded provider limit=%d, want 0", got)
	}
	if got := effectiveAutonomyCostLimit(&AutonomyPolicy{MaxCostUSDTicks: 42}); got != 42 {
		t.Fatalf("provider limit=%d, want 42", got)
	}
}

func TestReportedUsageCostExceededClampsInvalidAndOverflowingUsage(t *testing.T) {
	if total, exceeded := reportedUsageCostExceeded([]TaskUsageEntry{{CostUSDTicks: -10}, {CostUSDTicks: 4}}, 5); total != 4 || exceeded {
		t.Fatalf("negative usage handling: total=%d exceeded=%v", total, exceeded)
	}
	if total, exceeded := reportedUsageCostExceeded([]TaskUsageEntry{{CostUSDTicks: math.MaxInt64}, {CostUSDTicks: 1}}, math.MaxInt64); total != math.MaxInt64 || !exceeded {
		t.Fatalf("overflow handling: total=%d exceeded=%v", total, exceeded)
	}
}

func TestReportedUsageTokenCount(t *testing.T) {
	got, overflow := reportedUsageTokenCount([]TaskUsageEntry{{InputTokens: 10, OutputTokens: 5, CacheReadTokens: 2, CacheWriteTokens: 3}})
	if got != 20 || overflow {
		t.Fatalf("token total=%d overflow=%v, want 20,false", got, overflow)
	}
	got, overflow = reportedUsageTokenCount([]TaskUsageEntry{{InputTokens: math.MaxInt64}, {OutputTokens: 1}})
	if got != math.MaxInt64 || !overflow {
		t.Fatalf("overflow total=%d overflow=%v", got, overflow)
	}
}

func TestReportedUsageTokenLimitReached(t *testing.T) {
	if !reportedUsageTokenLimitReached(agent.TokenUsage{InputTokens: 8, OutputTokens: 3}, 11) {
		t.Fatal("expected token limit to trigger at equality")
	}
	if reportedUsageTokenLimitReached(agent.TokenUsage{InputTokens: 8}, 12) {
		t.Fatal("unexpected token limit trigger below threshold")
	}
}

func TestAutonomyMessageOverridePreservesGrants(t *testing.T) {
	base := json.RawMessage(`{"multica_autonomy":{"mode":"autonomous","max_token_count":100,"max_cost_usd_ticks":900,"allowed_identity_actions":["email","phone"],"allowed_decision_capabilities":[]}}`)
	for _, override := range []string{`{"multica_autonomy":{"mode":"autonomous","max_token_count":4,"max_cost_usd_ticks":50}}`, `{"multica_autonomy":null}`} {
		raw, err := mergeRuntimeConfigAutonomy(base, json.RawMessage(override))
		if err != nil {
			t.Fatal(err)
		}
		policy, err := parseAutonomyPolicy(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !policy.allowsIdentityAction("email") || !policy.allowsIdentityAction("phone") || policy.allows("decision") {
			t.Fatalf("grants changed: %+v", policy)
		}
		if override != `{"multica_autonomy":null}` && policy.MaxCostUSDTicks != 50 {
			t.Fatalf("cost limit was not overridden: %+v", policy)
		}
		if override == `{"multica_autonomy":null}` && (policy.MaxTokenCount != 0 || policy.MaxCostUSDTicks != 0 || policy.instructions() != "") {
			t.Fatalf("normal mode did not reset execution limits: %+v", policy)
		}
	}
	if _, err := mergeRuntimeConfigAutonomy(base, json.RawMessage(`{"multica_autonomy":{"allowed_identity_actions":["payment"]}}`)); err == nil {
		t.Fatal("message override must not grant identity actions")
	}
}
