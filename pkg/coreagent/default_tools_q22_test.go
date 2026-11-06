package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r15 §10 supersedes Q22's global-Allow and worker-Ask authoring defaults.
// Preserve both layers: fresh edit Ask, operator Allow retained, per-agent
// tightening still decisive, and the new read verb allowed for every tier.
func TestSkillsR15_PermissionDefaultsAndRetiredTools(t *testing.T) {
	for _, ceiling := range []string{"ask", "allow"} {
		t.Run("ceiling_"+ceiling, func(t *testing.T) {
			cfg := config.DefaultConfig()
			if ceiling == "allow" {
				cfg.Sandbox.ToolPolicies["edit_skill"] = "allow" // explicit operator value
			}
			coreagent.SeedConfig(cfg)
			assert.Equal(t, ceiling, cfg.Sandbox.ToolPolicies["edit_skill"], "§10: fresh Ask; never overwrite operator Allow")
			assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["get_skill"], "§10: reads available everywhere")
			catalog := coreagent.AllStaticToolNames()
			assert.Contains(t, catalog, "edit_skill")
			assert.Contains(t, catalog, "get_skill")
			for _, retired := range []string{"create_skill", "remove_skill"} {
				assert.NotContains(t, catalog, retired, "§15: retired tool is not selectable")
				assert.NotContains(t, cfg.Sandbox.ToolPolicies, retired, "§10: fresh ceiling has no retired entry")
			}
			for _, row := range []struct{ id, edit string }{
				{"mia", "ask"}, {"jim", "ask"}, {"planner", "deny"}, {"researcher", "deny"}, {"worker", "deny"},
				{"omnipus", ceiling}, {"judge", "deny"}, {"plansupervisor", "deny"},
			} {
				t.Run(row.id, func(t *testing.T) {
					agent := l1WU2SeededAgent(t, cfg, row.id)
					require.NotNil(t, agent.Tools)
					if row.id == "omnipus" {
						assert.NotContains(t, agent.Tools.Builtin.Policies, "edit_skill", "Omnipus rides the ceiling")
					} else {
						assert.Equal(t, config.ToolPolicy(row.edit), agent.Tools.Builtin.Policies["edit_skill"], "stored tier restriction")
					}
					for _, retired := range []string{"create_skill", "remove_skill"} {
						assert.NotContains(t, agent.Tools.Builtin.Policies, retired)
					}
					pc, _ := tools.BuildFallbackPolicyCfg(cfg, row.id)
					assert.Equal(t, row.edit, tools.ResolveEffectivePolicy(pc, "edit_skill"), "two-layer editing permission")
					assert.Equal(t, "allow", tools.ResolveEffectivePolicy(pc, "get_skill"), "read permission survives all tier restrictions")
				})
			}
		})
	}
	for _, worker := range []bool{false, true} {
		name, edit := "Main", config.ToolPolicyAsk
		if worker {
			name, edit = "Worker", config.ToolPolicyDeny
		}
		t.Run("new_native_"+name, func(t *testing.T) {
			seed := coreagent.NewCustomAgentToolsCfg()
			if worker {
				coreagent.RestrictForWorkerTier(seed)
			}
			assert.Equal(t, edit, seed.Builtin.Policies["edit_skill"], "Main Ask, Subagent Deny")
			assert.Equal(t, config.ToolPolicyAllow, seed.Builtin.Policies["get_skill"])
			for _, retired := range []string{"create_skill", "remove_skill"} {
				assert.NotContains(t, seed.Builtin.Policies, retired)
			}
			resolver := &tools.ToolPolicyCfg{Policies: seed.Builtin.Policies, GlobalPolicies: map[string]config.ToolPolicy{
				"edit_skill": config.ToolPolicyAllow, "get_skill": config.ToolPolicyAllow,
			}}
			assert.Equal(t, string(edit), tools.ResolveEffectivePolicy(resolver, "edit_skill"), "per-agent restriction tightens even an Allow ceiling")
			assert.Equal(t, "allow", tools.ResolveEffectivePolicy(resolver, "get_skill"))
		})
	}
}
