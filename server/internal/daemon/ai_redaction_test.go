package daemon

import (
	"fmt"
	"strings"
	"testing"
)

// TestAIRedactionRedactsAIownLocalSecret proves the AI-applied redaction
// redacts the AI's own local model/lease credential and endpoint, which the
// owner's policy never carries (the owner only ships lengths + digests). This
// is the core F2 seam: the AI holds its own raw secrets and redacts its own
// log copy with them.
func TestAIRedactionRedactsAIownLocalSecret(t *testing.T) {
	// Owner policy knows nothing about the AI's local secrets.
	ownerPolicy, err := resolveRedactionPolicy([]string{"owner-secret-value"})
	if err != nil {
		t.Fatalf("resolve owner policy: %v", err)
	}
	ar, err := NewAIRedaction(ownerPolicy, []string{"ai-model-lease-credential", "https://endpoint.example.test/v1"})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	copy := `{"model":"gpt-x","key":"ai-model-lease-credential","base":"https://endpoint.example.test/v1"}`
	got, err := ar.Redact(copy)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(got, "ai-model-lease-credential") {
		t.Fatalf("AI local credential not redacted: %q", got)
	}
	if strings.Contains(got, "https://endpoint.example.test/v1") {
		t.Fatalf("AI local endpoint not redacted: %q", got)
	}
}

// TestAIRedactionRedactsOwnerPolicySecret proves the AI-applied redaction
// still redacts the owner's policy secret (by length + truncated digest), so
// the owner-resolved rule and the AI's local secrets are both covered by one
// bounded pass.
func TestAIRedactionRedactsOwnerPolicySecret(t *testing.T) {
	ownerPolicy, err := resolveRedactionPolicy([]string{"owner-secret-value"})
	if err != nil {
		t.Fatalf("resolve owner policy: %v", err)
	}
	ar, err := NewAIRedaction(ownerPolicy, []string{})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	got, err := ar.Redact("the key is owner-secret-value done")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(got, "owner-secret-value") {
		t.Fatalf("owner policy secret not redacted: %q", got)
	}
	if !strings.Contains(got, redactionReplacement) {
		t.Fatalf("replacement missing: %q", got)
	}
}

// TestAIRedactionRedactsJSONKey proves the bounded JSON-key policy redacts a
// credential-bearing key's value even when the raw value was not in the
// by-value policy, so a credential that only appears as a JSON key is still
// suppressed. The key is kept and its value becomes redactionReplacement,
// matching the owner's decision-log redaction.
func TestAIRedactionRedactsJSONKey(t *testing.T) {
	ar, err := NewAIRedaction(redactionPolicy{}, []string{})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	got, err := ar.Redact(`{"api_key":"value-without-secret-rule","note":"ok"}`)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(got, "value-without-secret-rule") {
		t.Fatalf("credential JSON key value not redacted: %q", got)
	}
	if !strings.Contains(got, `"api_key":"`+redactionReplacement) {
		t.Fatalf("api_key value must be redacted by key: %q", got)
	}
	if !strings.Contains(got, `"note":"ok"`) {
		t.Fatalf("non-credential key must survive: %q", got)
	}
}

// TestAIRedactionRedactsNestedJSONKey proves the by-key policy recurses into
// nested objects and arrays, but only within the fixed, bounded key set, so
// nesting cannot turn key redaction into an unbounded scan.
func TestAIRedactionRedactsNestedJSONKey(t *testing.T) {
	ar, err := NewAIRedaction(redactionPolicy{}, []string{})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	got, err := ar.Redact(`{"a":{"Authorization":"tok"},"b":[{"secret":"s"}]}`)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(got, "tok") || strings.Contains(got, `"secret":"s"`) {
		t.Fatalf("nested credential not redacted: %q", got)
	}
}

// TestAIRedactionNeverCarriesRawSecret is the core invariant: the combined
// AI/owner policy is only lengths + truncated digests, so neither the owner's
// secret nor the AI's local secret can be recovered from the struct and
// cannot cross the owner/AI boundary.
func TestAIRedactionNeverCarriesRawSecret(t *testing.T) {
	ownerSecret := "owner-secret-value"
	aiSecret := "ai-model-lease-credential"
	ownerPolicy, err := resolveRedactionPolicy([]string{ownerSecret})
	if err != nil {
		t.Fatalf("resolve owner policy: %v", err)
	}
	ar, err := NewAIRedaction(ownerPolicy, []string{aiSecret})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	if len(ar.policy.rules) != 2 {
		t.Fatalf("want 2 rules (owner + ai local), got %d", len(ar.policy.rules))
	}
	for _, rule := range ar.policy.rules {
		if string(rule.digest[:]) == ownerSecret || string(rule.digest[:]) == aiSecret {
			t.Fatalf("digest must not equal a raw secret")
		}
	}
}

// TestAIRedactionFailsClosedOnOversize proves the AI-applied redaction is
// fail closed: an oversize copy is refused (error) rather than scanned or
// emitted under-redacted, so redaction cannot become an unbounded job or a
// raw-credential leak.
func TestAIRedactionFailsClosedOnOversize(t *testing.T) {
	ar, err := NewAIRedaction(redactionPolicy{}, []string{})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	if _, err := ar.Redact(strings.Repeat("b", redactionMaxCopyBytes+1)); err == nil {
		t.Fatal("oversize copy must fail closed")
	}
}

// TestAIRedactionFailsClosedOnOversizeSecret proves the constructor is fail
// closed: an AI local secret too large to redact by value returns an error
// instead of being shipped raw.
func TestAIRedactionFailsClosedOnOversizeSecret(t *testing.T) {
	if _, err := NewAIRedaction(redactionPolicy{}, []string{strings.Repeat("a", maxRedactionSecretBytes+1)}); err == nil {
		t.Fatal("oversize local secret must fail closed")
	}
}

// TestAIRedactionBoundsCombinedRules locks the combined owner/AI rule set to
// its bound: an over-limit set fails closed rather than silently dropping a
// rule (which would leave a secret unredeacted), and duplicate secrets
// collapse to one rule.
func TestAIRedactionBoundsCombinedRules(t *testing.T) {
	// Owner policy at the bound with distinct secrets, one AI local secret
	// over it -> fail closed.
	owner := make([]string, maxRedactionRules)
	for i := range owner {
		owner[i] = fmt.Sprintf("owner-secret-%d", i)
	}
	ownerPolicy, err := resolveRedactionPolicy(owner)
	if err != nil {
		t.Fatalf("resolve owner policy: %v", err)
	}
	if _, err := NewAIRedaction(ownerPolicy, []string{"extra-secret"}); err == nil {
		t.Fatal("over-limit combined rules must fail closed")
	}
	// Deduped combined: an AI local secret that matches an owner secret
	// collapses, so the combined count does not double-count it.
	onePolicy, err := resolveRedactionPolicy([]string{"owner-secret"})
	if err != nil {
		t.Fatalf("resolve one policy: %v", err)
	}
	ar, err := NewAIRedaction(onePolicy, []string{"owner-secret", "ai-secret"})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	if len(ar.policy.rules) != 2 {
		t.Fatalf("want 2 deduped rules, got %d", len(ar.policy.rules))
	}
}

// TestAIRedactionEmptyPolicyIsValid proves an empty owner policy with no local
// secrets is a valid "nothing to redact" state and does not touch the input.
func TestAIRedactionEmptyPolicyIsValid(t *testing.T) {
	ar, err := NewAIRedaction(redactionPolicy{}, []string{})
	if err != nil {
		t.Fatalf("NewAIRedaction: %v", err)
	}
	got, err := ar.Redact("no secrets here")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got != "no secrets here" {
		t.Fatalf("empty policy must leave the copy unchanged: %q", got)
	}
}
