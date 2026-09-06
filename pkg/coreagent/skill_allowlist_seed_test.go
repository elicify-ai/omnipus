package coreagent_test

import (
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeedConfig_SeedsSkillAllowlistMatrix verifies FR-9.4: SeedConfig seeds the
// per-agent skill allowlist matrix on a fresh install:
//
//	summarize       → Mia, Ray
//	plan            → Jim
//	skill-authoring → Ava
//	daily-briefing  → Mia
//	define-goal     → all of the above (ADR-074 D4; renamed from define-done
//	                  by ADR-080 D-SKILL)
func TestSeedConfig_SeedsSkillAllowlistMatrix(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil // fresh install — no agents yet

	coreagent.SeedConfig(cfg)

	want := map[string][]string{
		"mia": {"daily-briefing", "define-goal", "summarize"},
		"ray": {"define-goal", "summarize"},
		"jim": {"define-goal", "plan"},
		"ava": {"define-goal", "skill-authoring"},
	}

	byID := map[string][]string{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a.Skills
	}

	for id, wantSkills := range want {
		got := append([]string(nil), byID[id]...)
		sort.Strings(got)
		ws := append([]string(nil), wantSkills...)
		sort.Strings(ws)
		if len(got) != len(ws) {
			t.Errorf("agent %q: skills = %v; want %v", id, byID[id], wantSkills)
			continue
		}
		for i := range ws {
			if got[i] != ws[i] {
				t.Errorf("agent %q: skills = %v; want %v", id, byID[id], wantSkills)
				break
			}
		}
	}
}

// TestSeedConfig_SkillAllowlist_RespectsOperatorEdits verifies the idempotent
// migration: an existing core agent that already declares a skill allowlist
// keeps it (operator choice wins). The one sanctioned addition is the ADR-074
// D4 define-done migration: on the first boot without its marker, the
// never-before-existing "define-done" grant is APPENDED to a non-empty
// core-roster allowlist — the operator's own entries are never removed or
// reordered. ADR-080 D-SKILL then rewrites that same append, in the SAME
// boot, from "define-done" to its renamed form "define-goal" — the final
// on-disk token an operator ever sees.
func TestSeedConfig_SkillAllowlist_RespectsOperatorEdits(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:     "mia",
			Name:   "Mia",
			Skills: []string{"custom-skill"}, // operator-set
		},
	}

	coreagent.SeedConfig(cfg)

	var mia *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "mia" {
			mia = &cfg.Agents.List[i]
		}
	}
	if mia == nil {
		t.Fatal("mia missing after seed")
	}
	// Operator entry preserved, in place, first; define-goal appended once by
	// the ADR-074 D4 migration (marker was absent on this "boot") and
	// immediately rewritten from define-done to define-goal by the ADR-080
	// D-SKILL rename migration in the same pass.
	want := []string{"custom-skill", "define-goal"}
	if len(mia.Skills) != len(want) || mia.Skills[0] != want[0] || mia.Skills[1] != want[1] {
		t.Errorf("operator skill allowlist not preserved+appended: got %v; want %v", mia.Skills, want)
	}
}
