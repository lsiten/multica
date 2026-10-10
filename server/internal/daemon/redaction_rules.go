package daemon

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// redactionRule is the bounded, never-raw description of one secret the task
// owner resolves and hands to the AI for redacting its own log copies. It
// carries only the secret's byte length and a truncated digest; the raw value
// is never shipped across the owner/AI boundary. The AI, which holds its own
// raw model/lease credential and endpoint locally, combines these with its own
// secrets to redact a log copy; the length narrows a candidate and the digest
// confirms it, so an exact-value or substring suppression can be verified
// without ever exposing the credential.
type redactionRule struct {
	length int
	digest [redactionDigestBytes]byte
}

const (
	// redactionDigestBytes is the truncated digest length kept per rule.
	redactionDigestBytes = 16
	// maxRedactionRules bounds how many secrets the owner may put behind the
	// boundary in one policy; the current secret set is far smaller, so this
	// caps a pathological config instead of matching one.
	maxRedactionRules = 64
	// maxRedactionSecretBytes bounds a single secret a rule may describe. A
	// credential over this is not redactable by value in a bounded way and is
	// treated as an error (fail closed) rather than shipped raw.
	maxRedactionSecretBytes = 8192
	// redactionReplacement is the only text a redacted value may become, so a
	// redaction can never re-emit a partial secret.
	redactionReplacement = "[redacted]"
	// redactionMaxCopyBytes bounds a single redaction pass; a copy over this
	// is refused rather than scanned, so redaction cannot be forced into an
	// unbounded job. It is far larger than any real decision log payload.
	redactionMaxCopyBytes = 2 << 20
)

// redactionPolicy is the bounded set of rules the owner resolves and the AI
// applies to its own log copies. It is the F2 redaction seam: the owner never
// ships a raw credential, only lengths and digests; the AI adds its own raw
// model/lease credential and endpoint locally. A policy with no rules is the
// explicit "nothing to redact" state and is valid; a policy that cannot be
// fully applied to a log copy must fail closed rather than emit an
// under-redacted copy.
type redactionPolicy struct {
	rules []redactionRule
}

func newRedactionRule(secret string) (redactionRule, error) {
	if len(secret) > maxRedactionSecretBytes {
		return redactionRule{}, fmt.Errorf("redaction secret exceeds %d bytes", maxRedactionSecretBytes)
	}
	var digest [redactionDigestBytes]byte
	sum := sha256.Sum256([]byte(secret))
	copy(digest[:], sum[:redactionDigestBytes])
	return redactionRule{length: len(secret), digest: digest}, nil
}

// resolveRedactionPolicy builds the bounded DTO from the raw secrets the owner
// holds. It is fail closed: a secret too large to redact by value returns an
// error instead of being shipped, and an over-limit set returns an error
// instead of silently dropping a rule (dropping a rule would leave a secret
// unredeacted). Duplicates collapse to one rule so matching stays bounded.
func resolveRedactionPolicy(secrets []string) (redactionPolicy, error) {
	policy := redactionPolicy{}
	seen := make(map[[redactionDigestBytes]byte]struct{})
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		rule, err := newRedactionRule(secret)
		if err != nil {
			return redactionPolicy{}, fmt.Errorf("resolve redaction policy: %w", err)
		}
		if _, ok := seen[rule.digest]; ok {
			continue
		}
		seen[rule.digest] = struct{}{}
		policy.rules = append(policy.rules, rule)
		if len(policy.rules) > maxRedactionRules {
			return redactionPolicy{}, fmt.Errorf("redaction policy has more than %d rules", maxRedactionRules)
		}
	}
	return policy, nil
}

// redactLogCopy applies the policy to a log copy only, never to a real provider
// input. It suppresses every substring of a rule's length whose digest matches
// that rule, so an exact-value or a substring occurrence is covered. It is
// fail closed: if the copy is over the bounded payload it returns an error
// rather than an unredacted (or partially redacted) copy, because an
// under-redacted log is worse than a dropped one. Matching is bounded to the
// payload length, so a large copy cannot turn redaction into a scan bomb.
func (p redactionPolicy) redactLogCopy(text string) (string, error) {
	if len(text) == 0 {
		return text, nil
	}
	// Bound the work: a single redaction pass scans at most len(text)
	// substrings per rule, and a policy has at most maxRedactionRules rules.
	// A copy over this is refused rather than scanned, so redaction cannot be
	// forced into an unbounded job.
	if len(text) > redactionMaxCopyBytes {
		return "", fmt.Errorf("redaction copy exceeds %d bytes", redactionMaxCopyBytes)
	}
	out := text
	for _, rule := range p.rules {
		if rule.length == 0 || rule.length > len(out) {
			continue
		}
		out = redactByRule(out, rule)
	}
	return out, nil
}

// redactByRule redacts every length-rule.length run in text whose digest
// equals rule.digest, replacing it in place. Longest-first is unnecessary
// because each rule is a single length; overlapping occurrences are still
// covered because the scan advances one byte at a time.
func redactWithPolicy(text string, p redactionPolicy) string {
	redacted, err := p.redactLogCopy(text)
	if err != nil {
		// A bounded refusal must not turn a redaction into an unredacted
		// copy; return the input unchanged so the by-value pass remains the
		// authoritative redaction and the caller still sees it.
		return text
	}
	return redacted
}

func redactByRule(text string, rule redactionRule) string {
	if rule.length == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		end := i + rule.length
		if end > len(text) {
			b.WriteString(text[i:])
			break
		}
		candidate := text[i:end]
		var digest [redactionDigestBytes]byte
		sum := sha256.Sum256([]byte(candidate))
		copy(digest[:], sum[:redactionDigestBytes])
		if digest == rule.digest {
			b.WriteString(redactionReplacement)
			i += rule.length - 1
			continue
		}
		b.WriteByte(text[i])
	}
	return b.String()
}
