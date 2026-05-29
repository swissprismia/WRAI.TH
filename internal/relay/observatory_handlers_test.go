package relay

import (
	"testing"
)

// ---- obsDeriveRole ----

func TestObsDeriveRole(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"agt-kuat-backend", "backend"},
		{"agt-kuat-tech-lead", "tech-lead"},
		{"backend", "backend"},
		{"architect-transversal", "architect"},
		{"adf-worker-3", ""},
		{"", ""},
	}
	for _, tc := range cases {
		s := tc.slug
		got := obsDeriveRole(&s)
		gotStr := ""
		if got != nil {
			gotStr = *got
		}
		if gotStr != tc.want {
			t.Errorf("obsDeriveRole(%q) = %q; want %q", tc.slug, gotStr, tc.want)
		}
	}
}

// ---- obsDeriveProjSlug ----

func TestObsDeriveProjSlug(t *testing.T) {
	cases := []struct {
		slug string
		want string
	}{
		{"agt-kuat-backend", "agt-kuat"},
		{"agt-kuat-tech-lead", "agt-kuat"},
		{"my-proj-qa", "my-proj"},
		{"backend", ""},          // single word = no project
		{"architect-transversal", ""},
		{"adf-worker-3", ""},
		{"", ""},
	}
	for _, tc := range cases {
		s := tc.slug
		got := obsDeriveProjSlug(&s)
		gotStr := ""
		if got != nil {
			gotStr = *got
		}
		if gotStr != tc.want {
			t.Errorf("obsDeriveProjSlug(%q) = %q; want %q", tc.slug, gotStr, tc.want)
		}
	}
}

// ---- obsSeverityFor ----

func TestObsSeverityFor(t *testing.T) {
	for ruleID := range secretRuleSeverity {
		if got := obsSeverityFor(ruleID); got != "high" {
			t.Errorf("obsSeverityFor(%q) = %q; want \"high\"", ruleID, got)
		}
	}
	if got := obsSeverityFor("unknown_rule"); got != "medium" {
		t.Errorf("obsSeverityFor(unknown) = %q; want \"medium\"", got)
	}
}

// ---- obsJSONB ----

func TestObsJSONB(t *testing.T) {
	if obsJSONB(nil) != nil {
		t.Error("obsJSONB(nil) should return nil")
	}
	got := obsJSONB(map[string]any{"k": "v"})
	if got == nil {
		t.Fatal("obsJSONB(map) should return non-nil")
	}
	if *got != `{"k":"v"}` {
		t.Errorf("obsJSONB(map) = %q; want {\"k\":\"v\"}", *got)
	}
	got2 := obsJSONB([]string{"a", "b"})
	if got2 == nil || *got2 != `["a","b"]` {
		t.Errorf("obsJSONB(slice) = %v; want [\"a\",\"b\"]", got2)
	}
}

// ---- Scrubber integration: obsInsertEvent helper logic ----

// TestObsInsertEvent_ScrubAndDedupe verifies that the dedup-by-rule-id logic
// used inside obsInsertEvent fires only one flag per rule across input+output.
func TestObsInsertEvent_ScrubAndDedupe(t *testing.T) {
	// Build a fake event whose input and output both contain the same GitHub PAT.
	pat := "ghp_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"

	input := map[string]any{"command": "echo " + pat}
	output := map[string]any{"result": "token=" + pat}

	_, inFlags := ScrubPayload(input)
	_, outFlags := ScrubPayload(output)

	firedRules := make(map[string]string)
	for _, f := range inFlags {
		if _, seen := firedRules[f.RuleID]; !seen {
			firedRules[f.RuleID] = obsSeverityFor(f.RuleID)
		}
	}
	for _, f := range outFlags {
		if _, seen := firedRules[f.RuleID]; !seen {
			firedRules[f.RuleID] = obsSeverityFor(f.RuleID)
		}
	}

	if len(firedRules) != 1 {
		t.Errorf("expected 1 distinct fired rule; got %d: %v", len(firedRules), firedRules)
	}
	if sev, ok := firedRules["secret_github_pat"]; !ok || sev != "high" {
		t.Errorf("expected secret_github_pat=high; got map %v", firedRules)
	}
}
