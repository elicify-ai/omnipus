package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: SPEC.md Founder rulings Q22=B, inside the existing two-layer model.
func TestQ22SkillAuthoring_OrdinaryAskAndSystemExemption(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	for _, name := range []string{"create_skill", "edit_skill"} {
		t.Run(name, func(t *testing.T) {
			// SPEC.md Q22=B: the ceiling stays Allow; only ordinary per-agent defaults ask.
			assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies[name], "Q22 must not ask-gate exempt Omnipus globally")
			for _, row := range []struct{ id, want string }{
				{"mia", "ask"}, {"jim", "ask"}, {"planner", "ask"}, {"researcher", "ask"}, {"worker", "ask"},
				{"omnipus", "allow"}, {"judge", "deny"}, {"plansupervisor", "deny"},
			} {
				t.Run(row.id, func(t *testing.T) {
					agent := l1WU2SeededAgent(t, cfg, row.id)
					require.NotNil(t, agent.Tools)
					if row.id == "omnipus" {
						// SPEC.md Q22=B: Omnipus has no override and rides the Allow ceiling.
						assert.NotContains(t, agent.Tools.Builtin.Policies, name)
					} else {
						// SPEC.md Q22=B: ordinary roles store Ask; Judge/Plan Supervisor retain explicit Deny.
						assert.Equal(t, config.ToolPolicy(row.want), agent.Tools.Builtin.Policies[name])
					}
					pc, _ := tools.BuildFallbackPolicyCfg(cfg, row.id)
					assert.Equal(t, row.want, tools.ResolveEffectivePolicy(pc, name), "Q22 effective permission")
				})
			}
		})
	}
	for _, worker := range []bool{false, true} {
		name := "Main"
		if worker {
			name = "Worker"
		}
		t.Run("new_native_"+name, func(t *testing.T) {
			seed := coreagent.NewCustomAgentToolsCfg()
			if worker {
				coreagent.RestrictForWorkerTier(seed)
			}
			for _, tool := range []string{"create_skill", "edit_skill"} {
				// SPEC.md Q22=B: do not rely on a global Ask to manufacture the required native-agent Ask.
				assert.Equal(t, config.ToolPolicyAsk, seed.Builtin.Policies[tool])
				resolver := &tools.ToolPolicyCfg{Policies: seed.Builtin.Policies, GlobalPolicies: map[string]config.ToolPolicy{tool: config.ToolPolicyAllow}}
				assert.Equal(t, "ask", tools.ResolveEffectivePolicy(resolver, tool))
			}
		})
	}
}
