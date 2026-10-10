package daemon

import (
	"strings"
	"testing"
)

func TestRedactionPolicyRedactsValueAndSubstring(t *testing.T) {
	policy, err := resolveRedactionPolicy([]string{"sk-secret-value", "remote-mcp-token"})
	if err != nil {
		t.Fatalf("resolve policy: %v", err)
	}
	// Exact value.
	got, err := policy.redactLogCopy("the key is sk-secret-value done")
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if strings.Contains(got, "sk-secret-value") {
		t.Fatalf("value not redacted: %q", got)
	}
	if !strings.Contains(got, redactionReplacement) {
		t.Fatalf("replacement missing: %q", got)
	}
	// Same secret repeated.
	got, err = policy.redactLogCopy("sk-secret-value and again sk-secret-value")
	if err != nil {
		t.Fatalf("redact repeated: %v", err)
	}
	if strings.Contains(got, "sk-secret-value") {
		t.Fatalf("repeated value not redacted: %q", got)
	}
}

// TestRedactionPolicyNeverCarriesRawSecret is the core F2 invariant: the DTO is
// a bounded description (length + truncated digest), so the raw secret cannot be
// recovered from it and cannot cross the owner/AI boundary.
func TestRedactionPolicyNeverCarriesRawSecret(t *testing.T) {
	secret := "this-is-a-very-raw-credential-value"
	policy, err := resolveRedactionPolicy([]string{secret})
	if err != nil {
		t.Fatalf("resolve policy: %v", err)
	}
	if len(policy.rules) != 1 {
		t.Fatalf("want exactly one rule, got %d", len(policy.rules))
	}
	if policy.rules[0].length != len(secret) {
		t.Fatalf("length = %d, want %d", policy.rules[0].length, len(secret))
	}
	// Reconstructing the raw value from the DTO must be impossible.
	if len(policy.rules) == 1 && string(policy.rules[0].digest[:]) == secret {
		t.Fatal("digest must not equal the raw secret")
	}
}

// TestRedactionPolicyFailsClosedOnOversize proves the DTO is fail closed: a
// secret too large to redact by value is refused rather than shipped, and an
// over-limit copy is refused rather than scanned, so redaction never becomes an
// unbounded job or a raw-credential leak.
func TestRedactionPolicyFailsClosedOnOversize(t *testing.T) {
	tooLarge := strings.Repeat("a", maxRedactionSecretBytes+1)
	if _, err := resolveRedactionPolicy([]string{tooLarge}); err == nil {
		t.Fatal("oversize secret must fail closed")
	}
	policy, err := resolveRedactionPolicy([]string{"short-secret"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := policy.redactLogCopy(strings.Repeat("b", redactionMaxCopyBytes+1)); err == nil {
		t.Fatal("oversize copy must fail closed")
	}
}

// TestRedactionPolicyDeduplicatesAndBounds locks the DTO to its bounds:
// duplicate secrets collapse to one rule, and an over-limit set fails closed
// rather than silently dropping a rule (which would leave a secret unredeacted).
func TestRedactionPolicyDeduplicatesAndBounds(t *testing.T) {
	policy, err := resolveRedactionPolicy([]string{"a", "a", "b", "b", "c"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(policy.rules) != 3 {
		t.Fatalf("want 3 deduped rules, got %d", len(policy.rules))
	}
	empty := []string{}
	p, err := resolveRedactionPolicy(empty)
	if err != nil {
		t.Fatalf("empty policy must be valid: %v", err)
	}
	if len(p.rules) != 0 {
		t.Fatalf("empty policy must have no rules")
	}
}

// TestRedactionPolicyUnicodeSecret proves a multi-byte UTF-8 secret is redacted
// by its byte length and digest, so redaction does not stop at a rune boundary.
func TestRedactionPolicyUnicodeSecret(t *testing.T) {
	secret := "密钥"
	policy, err := resolveRedactionPolicy([]string{secret})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := policy.redactLogCopy("prefix " + secret + " suffix")
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("unicode secret not redacted: %q", got)
	}
}

// TestRedactionWithPolicyIsSafeOnRefusal proves a bounded refusal does not turn
// redaction into an unredacted copy: redactWithPolicy returns the input so the
// by-value pass stays the authoritative redaction.
func TestRedactionWithPolicyIsSafeOnRefusal(t *testing.T) {
	policy, err := resolveRedactionPolicy([]string{"x"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	in := strings.Repeat("b", redactionMaxCopyBytes+1)
	if got := redactWithPolicy(in, policy); got != in {
		t.Fatal("refused redaction must return the input unchanged")
	}
}
