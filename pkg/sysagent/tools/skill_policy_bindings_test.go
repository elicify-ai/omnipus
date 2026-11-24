// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// GREEN bindings for the Skills & Tools (#1963) seams of this package: the
// C-2 global skill map (sandbox.skill_policies) and the C-4 per-agent
// override store (tools.skills.policies on the agent record).

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func init() {
	setGlobalSkillPolicyForTest = setLiveGlobalSkillPolicy
	seedSkillSettingsForAgent = seedS74SkillSettings
}

// setLiveGlobalSkillPolicy stores one global skill value in the live config.
func setLiveGlobalSkillPolicy(cfg *config.Config, key, value string) {
	if cfg.Sandbox.SkillPolicies == nil {
		cfg.Sandbox.SkillPolicies = map[string]string{}
	}
	cfg.Sandbox.SkillPolicies[config.NormalizeSkillKey(key)] = value
}

// seedS74SkillSettings writes S-74's Given through the production stores:
// config.json carries plan=ask and deep-research=allow, and the agent record
// carries the override plan=deny.
func seedS74SkillSettings(t testing.TB, home, agentID string) {
	t.Helper()
	path := filepath.Join(home, "config.json")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("seed: load config: %v", err)
	}
	setLiveGlobalSkillPolicy(cfg, "plan", "ask")
	setLiveGlobalSkillPolicy(cfg, "deep-research", "allow")
	if saveErr := config.SaveConfig(path, cfg); saveErr != nil {
		t.Fatalf("seed: save config: %v", saveErr)
	}
	_, err = agentstore.New(home).Update(agentID, func(a *config.AgentConfig) error {
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		a.Tools.Skills = &config.AgentSkillsCfg{Policies: map[string]config.ToolPolicy{"plan": config.ToolPolicyDeny}}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: agent override: %v", err)
	}
}
