package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// aiRedaction is the redaction the AI service applies to its OWN log path,
// before any log, persistence, or export of a JEV decision record. The AI
// holds its own raw model/lease credential and endpoint locally; those never
// cross the owner/AI boundary. It combines them with the owner-resolved
// redactionPolicy (bounded lengths + truncated digests, which carry no raw
// value) and a bounded JSON-key policy to redact a copy of a log or a record.
//
// It is fail closed: a copy it cannot fully redact is refused (Redact returns
// an error) rather than emitted under-redacted, so a redaction failure can
// never leak a credential or a partial one. A redacted value may only become
// redactionReplacement; a value can never be half-suppressed.
//
// This is the F2 redaction seam made AI-owned. Until the AI service runs in
// its own process (F3) it is exercised in isolation, exactly like the
// evaluation seam; it is not wired into the decision-log hot path, which
// remains owner-applied via configureDecisionLogging.
type aiRedaction struct {
	// policy is the combined by-value redaction policy: the owner's rules
	// (lengths + truncated digests) merged with rules derived from the AI's
	// own local secrets. Every rule carries no raw value, so the struct can
	// never leak a credential across the owner/AI boundary.
	policy redactionPolicy
	// keys is the bounded set of JSON keys the AI redacts by key, in
	// addition to the by-value pass.
	keys jsonKeyRedactionPolicy
}

// jsonKeyRedactionPolicy is the bounded set of JSON keys the AI redacts by
// key, independent of the by-value policy. It is bounded to a fixed,
// normalized set of credential-bearing keys so a single JSON value cannot
// turn key redaction into an unbounded scan, and it never re-exposes a
// value. An empty policy means "no key redaction", which is valid.
type jsonKeyRedactionPolicy struct {
	// keys is the normalized set of credential-bearing JSON keys (see
	// redactedKey). Empty means "no key redaction".
	keys map[string]struct{}
}

// NewAIRedaction builds the AI's redaction from the owner-resolved policy and
// the AI's own local secrets (its model/lease credential and endpoint, held
// locally and never shipped). It is fail closed: a local secret too large to
// redact by value, or a combined rule set over the bound, returns an error
// instead of shipping a raw secret or silently dropping a rule (which would
// leave a secret unredeacted). The AI's local secrets are merged into the
// policy as rules (lengths + truncated digests), so the returned struct
// carries no raw value and cannot leak across the owner/AI boundary.
func NewAIRedaction(policy redactionPolicy, localSecrets []string) (aiRedaction, error) {
	seen := make(map[[redactionDigestBytes]byte]struct{})
	combined := redactionPolicy{}
	for _, rule := range policy.rules {
		if _, ok := seen[rule.digest]; ok {
			continue
		}
		seen[rule.digest] = struct{}{}
		combined.rules = append(combined.rules, rule)
	}
	for _, secret := range localSecrets {
		if secret == "" {
			continue
		}
		rule, err := newRedactionRule(secret)
		if err != nil {
			return aiRedaction{}, fmt.Errorf("ai redaction: %w", err)
		}
		if _, ok := seen[rule.digest]; ok {
			continue
		}
		seen[rule.digest] = struct{}{}
		combined.rules = append(combined.rules, rule)
	}
	if len(combined.rules) > maxRedactionRules {
		return aiRedaction{}, fmt.Errorf("ai redaction: policy has more than %d rules", maxRedactionRules)
	}
	return aiRedaction{policy: combined, keys: newJSONKeyRedactionPolicy()}, nil
}

// Redact applies the AI's redaction to a copy of a log or a record. It
// combines the owner's by-value policy, the AI's own local secrets, and the
// bounded JSON-key policy. It is fail closed: a copy over the bounded payload
// is refused rather than scanned, so redaction cannot be forced into an
// unbounded job or a raw-credential leak. A JSON object has its
// credential-bearing keys redacted by key in addition to the by-value pass; a
// non-JSON copy is redacted by value only. It never re-emits a partial
// secret: a redacted value may only become redactionReplacement.
func (a aiRedaction) Redact(text string) (string, error) {
	if text == "" {
		return text, nil
	}
	redacted, err := a.policy.redactLogCopy(text)
	if err != nil {
		return "", err
	}
	return a.keys.redact(redacted), nil
}

// newJSONKeyRedactionPolicy builds the bounded set of credential-bearing JSON
// keys the AI redacts by key. The keys are normalized exactly as redactedKey
// normalizes them, so a single fixed set covers credential keys in any
// underscore/hyphen/case variant, and it is bounded so a JSON value cannot
// turn key redaction into an unbounded scan.
func newJSONKeyRedactionPolicy() jsonKeyRedactionPolicy {
	p := jsonKeyRedactionPolicy{keys: make(map[string]struct{})}
	for _, key := range []string{"authorization", "cookie", "setcookie", "credentials", "apikey", "token", "password", "secret", "credential"} {
		p.keys[key] = struct{}{}
	}
	return p
}

// redact applies the bounded JSON-key policy to a copy that the by-value pass
// already processed. It redacts only a JSON object's credential-bearing keys
// (replacing the key's value with redactionReplacement); a value that is not
// a JSON object is returned unchanged because the by-value pass already
// covered its raw text. It is bounded: only the fixed key set is matched, so
// a JSON value cannot turn key redaction into an unbounded scan.
func (p jsonKeyRedactionPolicy) redact(text string) string {
	if len(p.keys) == 0 {
		return text
	}
	trimmed := strings.TrimSpace(text)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return text
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		// Not JSON the by-value pass already covered; leave it untouched.
		return text
	}
	clean := p.redactValue(value)
	out, err := json.Marshal(clean)
	if err != nil {
		// Encoding failure is a refusal, not a partial redaction.
		return text
	}
	return string(out)
}

// redactValue walks a parsed JSON value and redacts credential-bearing keys.
// It keeps the key and replaces its value with redactionReplacement, matching
// the owner's decision-log redaction, so a redaction can never re-emit the
// value. It is bounded: only the fixed key set is matched.
func (p jsonKeyRedactionPolicy) redactValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(value))
		for key, entry := range value {
			if _, redact := p.keys[redactedKey(key)]; redact {
				clean[key] = redactionReplacement
				continue
			}
			clean[key] = p.redactValue(entry)
		}
		return clean
	case []any:
		for index, entry := range value {
			value[index] = p.redactValue(entry)
		}
	}
	return value
}

// redactedKey normalizes a JSON key the same way jevLogSecretKey does, so the
// AI's by-key policy matches the owner's credential keys regardless of case,
// underscores, or hyphens.
func redactedKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
}
