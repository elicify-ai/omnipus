package agent

import "testing"

// TestActiveSkillNames_DoesNotUnionGrantList is ADR-072 D1/D3's regression:
// before this fix, activeSkillNames unioned agent.SkillsFilter (the agent's
// ENTIRE per-agent grant list) into every turn's active skills, force-loading
// the whole grant list on every message — the exact mechanism the on-demand
// Skill tool replaces. A grant merely makes a skill LOADABLE (skillAllowed);
// it must not make it ACTIVE.
func TestActiveSkillNames_DoesNotUnionGrantList(t *testing.T) {
	agent := &AgentInstance{
		SkillsFilter: []string{"summarize", "plan", "daily-briefing"},
	}

	t.Run("a grant list with no ForcedSkills yields no active skills", func(t *testing.T) {
		got := activeSkillNames(agent, processOptions{})
		if len(got) != 0 {
			t.Errorf("activeSkillNames with only a grant list (no ForcedSkills) = %v, want empty — "+
				"a grant must not force-load the skill every turn (ADR-072 D1/D3)", got)
		}
	})

	t.Run("ForcedSkills outside the grant list still surfaces (resolution is independent of the grant here)", func(t *testing.T) {
		// activeSkillNames itself does not consult skillAllowed — the grant
		// gate is enforced at the point a name is ADDED to ForcedSkills
		// (applyExplicitSkillCommand's ResolveSkillName call, or delegate's
		// D9 gate). This test pins that activeSkillNames' own output is a
		// pure function of ForcedSkills, not of SkillsFilter, in either
		// direction — SkillsFilter neither adds to nor restricts the result.
		got := activeSkillNames(agent, processOptions{ForcedSkills: []string{"ad-hoc-skill"}})
		if len(got) != 1 || got[0] != "ad-hoc-skill" {
			t.Errorf("activeSkillNames = %v, want [ad-hoc-skill]", got)
		}
	})
}

// TestActiveSkillNames_ForcedSkillsStillHonored is the regression that the
// two legitimate one-shot activation channels — the /<slug> slash command
// (applyExplicitSkillCommand) and delegate's requested_skill (D9,
// spawnSubTurn's ForcedSkills append) — still work after the grant-list union
// was removed: both operate purely through opts.ForcedSkills.
func TestActiveSkillNames_ForcedSkillsStillHonored(t *testing.T) {
	agent := &AgentInstance{} // no grant list at all — must not matter

	got := activeSkillNames(agent, processOptions{
		ForcedSkills: []string{"plan-spec", "PLAN-SPEC", "  summarize  "},
	})

	if len(got) != 2 {
		t.Fatalf("activeSkillNames = %v, want 2 entries (dedup case-insensitively, trim whitespace)", got)
	}
	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
	}
	if !seen["plan-spec"] && !seen["PLAN-SPEC"] {
		t.Errorf("activeSkillNames = %v, want a plan-spec entry (deduped from the two case variants)", got)
	}
	if !seen["summarize"] {
		t.Errorf("activeSkillNames = %v, want a trimmed \"summarize\" entry", got)
	}
}

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
