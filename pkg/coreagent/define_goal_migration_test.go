package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

func TestSeedConfig_FreshInstall_DefineGoalAssignedOnlyToADR090Roles(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	require.True(t, coreagent.SeedConfig(cfg))
	want := map[string]bool{"mia": true, "jim": true, "planner": true, "plansupervisor": true}
	for _, agent := range cfg.Agents.List {
		assert.Equalf(t, want[agent.ID], containsString(agent.Skills, "define-goal"),
			"define-goal assignment for %q must match ADR-090", agent.ID)
		assert.NotContains(t, agent.Skills, "define-done", "retired skill ID must never be seeded")
	}
}

func TestSeedConfig_DoesNotMigrateExistingSkillSelections(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"custom-skill"}},
		{ID: "custom-agent", Name: "Custom", Type: config.AgentTypeCustom, Skills: []string{"define-done"}},
	}
	coreagent.SeedConfig(cfg)
	assert.Equal(t, []string{"custom-skill"}, findSeeded(t, cfg, "mia").Skills)
	assert.Equal(t, []string{"define-done"}, findSeeded(t, cfg, "custom-agent").Skills)
}

func TestSeedConfig_DefineGoalRemovalPersists(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	require.True(t, coreagent.SeedConfig(cfg))
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "mia" {
			cfg.Agents.List[i].Skills = []string{}
		}
	}
	coreagent.SeedConfig(cfg)
	assert.Empty(t, findSeeded(t, cfg, "mia").Skills)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
