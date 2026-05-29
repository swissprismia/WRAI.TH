package relay

import "regexp"

// FlagRecord describes a single secret-pattern match found in an original string
// before redaction. rule_id strings are kept identical to the Python scrubber in
// agt-geonosis so flag continuity is preserved across the Python→Go cutover.
type FlagRecord struct {
	RuleID  string `json:"rule_id"`
	Matched string `json:"matched"` // original matched text, before redaction
}

type secretRule struct {
	id      string
	pattern *regexp.Regexp
}

// secretRules lists the 7 patterns in evaluation order. All patterns are RE2-safe
// (no PCRE-only features: no lookahead/lookbehind, no atomic groups, no possessive
// quantifiers). The lazy quantifier +? used in the SSH pattern is valid RE2.
var secretRules = []secretRule{
	{
		id:      "secret_aws_access_key_id",
		pattern: regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|ANPA|AROA|AIDA)[0-9A-Z]{16}\b`),
	},
	{
		id:      "secret_aws_secret_access_key",
		pattern: regexp.MustCompile(`(?i)aws[_\-]?secret[_\-]?access[_\-]?key.{0,5}["': ]+([A-Za-z0-9/+=]{40})`),
	},
	{
		id:      "secret_stripe_key",
		pattern: regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{24,}\b`),
	},
	{
		id:      "secret_jwt",
		pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`),
	},
	{
		// [\s\S]+? matches any character including newlines (RE2-safe lazy quantifier).
		id:      "secret_ssh_private_key",
		pattern: regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |DSA |EC |PGP )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |OPENSSH |DSA |EC |PGP )?PRIVATE KEY-----`),
	},
	{
		id:      "secret_github_pat",
		pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`),
	},
	{
		id:      "secret_anthropic_key",
		pattern: regexp.MustCompile(`\bsk-ant-(?:api|admin)\d{2}-[A-Za-z0-9_\-]{32,}\b`),
	},
}

// ScrubString replaces every secret match in s with [REDACTED:rule_id] and
// returns the scrubbed string plus one FlagRecord per match found in the
// original s. Flags are collected before any substitution so positions reflect
// the unmodified input; the order follows rule evaluation order.
func ScrubString(s string) (string, []FlagRecord) {
	var flags []FlagRecord
	for _, rule := range secretRules {
		for _, m := range rule.pattern.FindAllString(s, -1) {
			flags = append(flags, FlagRecord{RuleID: rule.id, Matched: m})
		}
	}
	out := s
	for _, rule := range secretRules {
		out = rule.pattern.ReplaceAllString(out, "[REDACTED:"+rule.id+"]")
	}
	return out, flags
}

// ScrubPayload walks an arbitrary JSON-shaped value (string, map[string]any,
// []any, or any other scalar) and returns a scrubbed deep copy plus the union
// of all FlagRecords found across every string leaf. Non-string scalars are
// returned unchanged. Maps and slices are reconstructed; the original is not
// mutated so callers may detect and then scrub independently.
func ScrubPayload(v any) (any, []FlagRecord) {
	switch val := v.(type) {
	case string:
		return ScrubString(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		var flags []FlagRecord
		for k, child := range val {
			scrubbed, f := ScrubPayload(child)
			out[k] = scrubbed
			flags = append(flags, f...)
		}
		return out, flags
	case []any:
		out := make([]any, len(val))
		var flags []FlagRecord
		for i, child := range val {
			scrubbed, f := ScrubPayload(child)
			out[i] = scrubbed
			flags = append(flags, f...)
		}
		return out, flags
	default:
		return v, nil
	}
}

// DetectSecrets returns the rule IDs that match s, deduplicated and in
// evaluation order. API-equivalent to detect_secrets() in the Python version.
func DetectSecrets(s string) []string {
	seen := make(map[string]struct{}, len(secretRules))
	var ids []string
	for _, rule := range secretRules {
		if _, already := seen[rule.id]; !already && rule.pattern.MatchString(s) {
			ids = append(ids, rule.id)
			seen[rule.id] = struct{}{}
		}
	}
	return ids
}
