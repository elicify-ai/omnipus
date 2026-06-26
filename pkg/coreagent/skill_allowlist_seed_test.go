package coreagent_test

import (
	"sort"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
)

// TestSeedConfig_SeedsSkillAllowlistMatrix verifies FR-9.4: SeedConfig seeds the
// per-agent skill allowlist matrix on a fresh install:
//
//	summarize       → Mia, Ray
//	plan            → Jim
//	skill-authoring → Ava
//	daily-briefing  → Mia
func TestSeedConfig_SeedsSkillAllowlistMatrix(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil // fresh install — no agents yet

	coreagent.SeedConfig(cfg)

	want := map[string][]string{
		"mia": {"daily-briefing", "summarize"},
		"ray": {"summarize"},
		"jim": {"plan"},
		"ava": {"skill-authoring"},
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
// keeps it (operator choice wins).
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
	if len(mia.Skills) != 1 || mia.Skills[0] != "custom-skill" {
		t.Errorf("operator skill allowlist was overwritten: got %v", mia.Skills)
	}
}
