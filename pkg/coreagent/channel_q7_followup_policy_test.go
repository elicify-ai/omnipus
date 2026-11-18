package coreagent_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-11-03 Q4: Omnipus, Mia, Jim and custom chat agents always ask.
// Hidden Judge/Plan Supervisor are the explicit background-system exception.
func TestQ7FollowupQ4_ChatCapableAgentsAlwaysAskOnFreshInstall(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "q4-custom", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()})
	require.Equal(t, "ask", cfg.Sandbox.ToolPolicies["set_channel_outbound"], "Q4: shipped global ceiling always asks")
	for _, id := range []string{"omnipus", "mia", "jim", "q4-custom"} {
		t.Run(id, func(t *testing.T) {
			policy, _ := tools.BuildFallbackPolicyCfg(cfg, id)
			require.NotNil(t, policy)
			assert.Equal(t, "ask", tools.ResolveEffectivePolicy(policy, "set_channel_outbound"), "Q4: a new chat-capable agent is usable with consent, never Allow or default Deny")
			policy.GodMode = true
			assert.Equal(t, "ask", tools.ResolveEffectivePolicy(policy, "set_channel_outbound"), "T-10 R2 applies to every chat-capable agent")
		})
	}
}

func TestQ7FollowupQ4_ChatCapableUpgradeInheritsAskAndPreservesStoredDeny(t *testing.T) {
	for _, id := range []string{"omnipus", "mia", "jim", "q4-existing-custom"} {
		for _, stored := range []string{"missing", "deny"} {
			t.Run(id+"_"+stored, func(t *testing.T) {
				cfg := config.DefaultConfig()
				delete(cfg.Sandbox.ToolPolicies, "set_channel_outbound") // Existing install predates the tool.
				policies := map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow}
				if stored == "deny" {
					policies["set_channel_outbound"] = config.ToolPolicyDeny
				}
				kind := config.AgentTypeCore
				if id == "q4-existing-custom" {
					kind = config.AgentTypeCustom
				}
				cfg.Agents.List = []config.AgentConfig{{ID: id, Type: kind, Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}}}}
				known := map[string]struct{}{}
				for _, name := range coreagent.AllStaticToolNames() {
					known[name] = struct{}{}
				}
				config.ReconcileToolPolicyCeiling(cfg, known)
				coreagent.SeedConfig(cfg) // The real upgrade/boot seeder, not hand-built policy output.
				policy, _ := tools.BuildFallbackPolicyCfg(cfg, id)
				want := "ask"
				if stored == "deny" {
					want = "deny"
					ac := l1WU2SeededAgent(t, cfg, id)
					assert.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["set_channel_outbound"], "Q4: operator's stored Deny is not rewritten")
				}
				for _, god := range []bool{false, true} {
					policy.GodMode = god
					assert.Equal(t, want, tools.ResolveEffectivePolicy(policy, "set_channel_outbound"), "Q4 upgrade / T-10: missing inherits Ask; stored Deny survives all modes")
				}
			})
		}
	}
}

func TestQ7FollowupQ4_BackgroundSystemAgentsJudgeAndPlanSupervisorStayDenied(t *testing.T) {
	raw, err := os.ReadFile("../../tests/default-tools/system-policy-baseline-c90310f1f.json")
	require.NoError(t, err)
	var baseline map[string]map[string]string
	require.NoError(t, json.Unmarshal(raw, &baseline))
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	for _, id := range []string{"judge", "plansupervisor"} {
		t.Run(id, func(t *testing.T) {
			require.Equal(t, "deny", baseline[id]["set_channel_outbound"], "chief 2026-11-02 17:23Z: frozen baseline keeps this explicit Deny")
			ac := l1WU2SeededAgent(t, cfg, id)
			require.Equal(t, config.ToolPolicyDeny, ac.Tools.Builtin.Policies["set_channel_outbound"], "Q4: background system roles have nobody to ask")
			policy, _ := tools.BuildFallbackPolicyCfg(cfg, id)
			for _, god := range []bool{false, true} {
				policy.GodMode = god
				require.Equal(t, "deny", tools.ResolveEffectivePolicy(policy, "set_channel_outbound"))
			}
		})
	}
}
