package agent

import (
	"testing"
)

// TestSkillAllowed_NilAllowlistDeniesEverything is ADR-072 D5's T1a: a
// ContextBuilder on which WithSkillAllowlist was never called (skillAllowlist
// stays at its zero value, nil) must deny every skill name — the flipped nil
// semantics, asserted directly against skillAllowed rather than only via a
// caller like ResolveSkillName.
func TestSkillAllowed_NilAllowlistDeniesEverything(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	if cb.skillAllowlist != nil {
		t.Fatalf("test setup bug: skillAllowlist = %v, want nil (WithSkillAllowlist not called)", cb.skillAllowlist)
	}
	for _, name := range []string{"summarize", "plan", "daily-briefing", "anything"} {
		if cb.skillAllowed(name) {
			t.Errorf("skillAllowed(%q) = true with a nil allowlist, want false (ADR-072 D5: no list ⇒ no skills)", name)
		}
	}
}

// TestSkillAllowed_EmptySliceMatchesNilSemantics is ADR-072 D5's T1b: a
// non-nil empty slice must deny exactly what a nil allowlist denies —
// absence of the field and an empty array are semantically identical, per
// contracts/components/schemas/Agent.yaml.
func TestSkillAllowed_EmptySliceMatchesNilSemantics(t *testing.T) {
	nilCB := NewContextBuilder(t.TempDir())
	emptyCB := NewContextBuilder(t.TempDir()).WithSkillAllowlist([]string{})

	for _, name := range []string{"summarize", "plan", "daily-briefing", "anything"} {
		nilResult := nilCB.skillAllowed(name)
		emptyResult := emptyCB.skillAllowed(name)
		if nilResult != emptyResult {
			t.Errorf("skillAllowed(%q): nil allowlist = %v, empty slice = %v — must match", name, nilResult, emptyResult)
		}
		if emptyResult {
			t.Errorf("skillAllowed(%q) = true with an empty allowlist, want false", name)
		}
	}
}

// TestSkillAllowed_GrantedSlugCaseInsensitive is the regression pinning that
// D5's flip did not disturb the pre-existing case-insensitive/trimmed
// matching for slugs actually present in the allowlist.
func TestSkillAllowed_GrantedSlugCaseInsensitive(t *testing.T) {
	cb := NewContextBuilder(t.TempDir()).WithSkillAllowlist([]string{"  Summarize  ", "Daily-Briefing"})

	for _, name := range []string{
		"summarize", "SUMMARIZE", "Summarize", "  summarize  ",
		"daily-briefing", "DAILY-BRIEFING", "Daily-Briefing",
	} {
		if !cb.skillAllowed(name) {
			t.Errorf("skillAllowed(%q) = false, want true — granted slugs must match case-insensitively and trimmed", name)
		}
	}
	for _, name := range []string{"plan", "not-granted", ""} {
		if cb.skillAllowed(name) {
			t.Errorf("skillAllowed(%q) = true, want false — not in the allowlist", name)
		}
	}
}
