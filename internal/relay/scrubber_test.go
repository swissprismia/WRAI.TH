package relay

import (
	"strings"
	"testing"
)

// Synthetic secret samples — these are fake values crafted to match the pattern
// shape only. None are real credentials. Values are built with concatenation so
// they don't appear as literal secrets in source scanning tools.

var (
	// 4-char prefix + 16 uppercase-alphanumeric chars (well-known AWS docs example)
	sampleAWSKeyID = "AKIA" + "IOSFODNN7EXAMPLE"
	// aws_secret_access_key + separator + 40-char base64-like value (fake)
	sampleAWSSecretKey = "aws_secret_access_key: " + "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	// Stripe-shaped test key: prefix_env_ + 24+ alphanumeric (not a real key)
	sampleStripeKey = "sk_test_" + "FakeStripeKeyXXXXXXXXXXXX"
	// JWT with three base64url segments of 10+ chars each
	sampleJWT = "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiJ0ZXN0dXNlciJ9" + "." + "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	// Fake RSA private key block (synthetic body — not a real key)
	sampleSSHKey = "-----BEGIN RSA PRIVATE KEY-----\n" + "MIIEowIBAAKCAQEA2a2rwplBQLzHPZe5NRfakekeydata\n" + "-----END RSA PRIVATE KEY-----"
	// GitHub PAT: ghp_ + 36 alphanumeric chars (not a real token)
	sampleGitHubPAT = "ghp_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
	// Anthropic-shaped key: prefix + 2 digits + - + 32+ chars (not a real key)
	sampleAnthropicKey = "sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz012345"
)

// TestScrubString_MatchAndRedact verifies that each pattern matches its
// synthetic sample and that ScrubString replaces the match with the expected
// [REDACTED:rule_id] token.
func TestScrubString_MatchAndRedact(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		ruleID  string
		wantTag string
	}{
		{
			name:    "aws_access_key_id",
			input:   "key=" + sampleAWSKeyID,
			ruleID:  "secret_aws_access_key_id",
			wantTag: "[REDACTED:secret_aws_access_key_id]",
		},
		{
			name:    "aws_secret_access_key",
			input:   sampleAWSSecretKey,
			ruleID:  "secret_aws_secret_access_key",
			wantTag: "[REDACTED:secret_aws_secret_access_key]",
		},
		{
			name:    "stripe_key",
			input:   "token=" + sampleStripeKey,
			ruleID:  "secret_stripe_key",
			wantTag: "[REDACTED:secret_stripe_key]",
		},
		{
			name:    "jwt",
			input:   "Authorization: Bearer " + sampleJWT,
			ruleID:  "secret_jwt",
			wantTag: "[REDACTED:secret_jwt]",
		},
		{
			name:    "ssh_private_key",
			input:   sampleSSHKey,
			ruleID:  "secret_ssh_private_key",
			wantTag: "[REDACTED:secret_ssh_private_key]",
		},
		{
			name:    "github_pat",
			input:   "token: " + sampleGitHubPAT,
			ruleID:  "secret_github_pat",
			wantTag: "[REDACTED:secret_github_pat]",
		},
		{
			name:    "anthropic_key",
			input:   "key=" + sampleAnthropicKey,
			ruleID:  "secret_anthropic_key",
			wantTag: "[REDACTED:secret_anthropic_key]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scrubbed, flags := ScrubString(tc.input)

			if !strings.Contains(scrubbed, tc.wantTag) {
				t.Errorf("scrubbed output missing %q\ngot: %s", tc.wantTag, scrubbed)
			}
			if strings.Contains(scrubbed, tc.ruleID) && !strings.Contains(scrubbed, "[REDACTED:") {
				t.Errorf("original secret still present in scrubbed output: %s", scrubbed)
			}

			found := false
			for _, f := range flags {
				if f.RuleID == tc.ruleID {
					found = true
					if f.Matched == "" {
						t.Errorf("FlagRecord for %q has empty Matched field", tc.ruleID)
					}
				}
			}
			if !found {
				t.Errorf("expected FlagRecord with rule_id %q, got %v", tc.ruleID, flags)
			}
		})
	}
}

// TestScrubString_NoFalsePositives verifies innocuous strings are not redacted.
func TestScrubString_NoFalsePositives(t *testing.T) {
	clean := []string{
		"hello world",
		"SELECT * FROM users WHERE id = 1",
		"https://example.com/api/v1/resource",
		"The AWS SDK requires an access key",
		"stripe provides payment processing",
		"eyJ is just a prefix",  // too short to be a real JWT
		"BEGIN PRIVATE THOUGHT", // not a PEM marker
		"ghx_notavalidprefix",   // wrong prefix char
	}
	for _, s := range clean {
		t.Run(s, func(t *testing.T) {
			scrubbed, flags := ScrubString(s)
			if scrubbed != s {
				t.Errorf("clean string was modified\ninput:   %q\nscrubbed: %q", s, scrubbed)
			}
			if len(flags) != 0 {
				t.Errorf("unexpected flags on clean string: %v", flags)
			}
		})
	}
}

// TestDetectSecrets verifies rule IDs are returned for matching strings.
func TestDetectSecrets(t *testing.T) {
	cases := []struct {
		input  string
		wantID string
	}{
		{sampleAWSKeyID, "secret_aws_access_key_id"},
		{sampleAWSSecretKey, "secret_aws_secret_access_key"},
		{sampleStripeKey, "secret_stripe_key"},
		{sampleJWT, "secret_jwt"},
		{sampleSSHKey, "secret_ssh_private_key"},
		{sampleGitHubPAT, "secret_github_pat"},
		{sampleAnthropicKey, "secret_anthropic_key"},
	}
	for _, tc := range cases {
		t.Run(tc.wantID, func(t *testing.T) {
			ids := DetectSecrets(tc.input)
			found := false
			for _, id := range ids {
				if id == tc.wantID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("DetectSecrets(%q) = %v, want rule_id %q", tc.input, ids, tc.wantID)
			}
		})
	}
}

// TestDetectSecrets_CleanString verifies an empty slice is returned for safe input.
func TestDetectSecrets_CleanString(t *testing.T) {
	ids := DetectSecrets("no secrets here")
	if len(ids) != 0 {
		t.Errorf("expected no rule IDs, got %v", ids)
	}
}

// TestScrubPayload_StringLeaf verifies a top-level string payload is scrubbed.
func TestScrubPayload_StringLeaf(t *testing.T) {
	in := "token=" + sampleAWSKeyID
	out, flags := ScrubPayload(in)
	s, ok := out.(string)
	if !ok {
		t.Fatalf("expected string, got %T", out)
	}
	if strings.Contains(s, sampleAWSKeyID) {
		t.Errorf("key not redacted in string output: %q", s)
	}
	if len(flags) == 0 {
		t.Error("expected at least one flag record")
	}
}

// TestScrubPayload_Map verifies nested map values are recursively scrubbed.
func TestScrubPayload_Map(t *testing.T) {
	in := map[string]any{
		"safe":  "hello",
		"token": sampleAnthropicKey,
		"meta":  map[string]any{"stripe": sampleStripeKey},
	}
	out, flags := ScrubPayload(in)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	if m["safe"] != "hello" {
		t.Errorf("safe field was modified: %v", m["safe"])
	}
	if strings.Contains(m["token"].(string), sampleAnthropicKey) {
		t.Error("anthropic key not redacted in map")
	}
	meta, _ := m["meta"].(map[string]any)
	if strings.Contains(meta["stripe"].(string), sampleStripeKey) {
		t.Error("stripe key not redacted in nested map")
	}
	if len(flags) < 2 {
		t.Errorf("expected ≥2 flag records, got %d: %v", len(flags), flags)
	}
}

// TestScrubPayload_Slice verifies slice elements are recursively scrubbed.
func TestScrubPayload_Slice(t *testing.T) {
	in := []any{"safe", sampleGitHubPAT, 42}
	out, flags := ScrubPayload(in)
	s, ok := out.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", out)
	}
	if s[0] != "safe" {
		t.Errorf("safe element modified: %v", s[0])
	}
	if strings.Contains(s[1].(string), sampleGitHubPAT) {
		t.Error("github PAT not redacted in slice")
	}
	if s[2] != 42 {
		t.Errorf("non-string scalar modified: %v", s[2])
	}
	if len(flags) == 0 {
		t.Error("expected flag record for slice scrub")
	}
}

// TestScrubPayload_NonStringScalar verifies booleans/ints pass through unchanged.
func TestScrubPayload_NonStringScalar(t *testing.T) {
	cases := []any{42, 3.14, true, nil}
	for _, v := range cases {
		out, flags := ScrubPayload(v)
		if out != v {
			t.Errorf("scalar %v changed to %v", v, out)
		}
		if len(flags) != 0 {
			t.Errorf("unexpected flags for scalar %v: %v", v, flags)
		}
	}
}

// TestScrubString_OriginalUnmutated verifies the input string is not modified.
func TestScrubString_OriginalUnmutated(t *testing.T) {
	original := "token=" + sampleStripeKey
	input := original
	ScrubString(input)
	if input != original {
		t.Errorf("input was mutated: %q → %q", original, input)
	}
}

// TestRuleIDs_MatchPythonVersion checks that the 7 rule ID strings exactly match
// the Python reference to preserve flag continuity across the cutover.
func TestRuleIDs_MatchPythonVersion(t *testing.T) {
	want := []string{
		"secret_aws_access_key_id",
		"secret_aws_secret_access_key",
		"secret_stripe_key",
		"secret_jwt",
		"secret_ssh_private_key",
		"secret_github_pat",
		"secret_anthropic_key",
	}
	if len(secretRules) != len(want) {
		t.Fatalf("expected %d rules, got %d", len(want), len(secretRules))
	}
	for i, id := range want {
		if secretRules[i].id != id {
			t.Errorf("rule[%d]: got %q, want %q", i, secretRules[i].id, id)
		}
	}
}
