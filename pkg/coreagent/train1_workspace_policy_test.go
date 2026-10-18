package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-18: "Create yes, delete asks". Expectations come from that
// decision, not from either production policy map. FR-022 forbids storing Ask
// on Omnipus itself; the two tools must inherit the global ceiling instead.
func TestTrain1OmnipusWorkspacePolicies_FreshInstallRidesCeiling(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	omnipus := l1WU2SeededAgent(t, cfg, "omnipus")
	require.NotNil(t, omnipus.Tools)
	inventory := coreagent.ADR090RolePolicyInventory(coreagent.IDOmnipus)

	for _, tc := range []struct {
		name string
		want config.ToolPolicy
	}{
		{"create_workspace", config.ToolPolicyAllow},
		{"delete_workspace", config.ToolPolicyAsk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, string(tc.want), cfg.Sandbox.ToolPolicies[tc.name], "shipped global policy")
			assert.NotContains(t, omnipus.Tools.Builtin.Policies, tc.name, "supported tools have no per-agent override")
			assert.Equal(t, tc.want, inventory[tc.name], "role inventory must match the founder's decision")
			pc, agentType := tools.BuildFallbackPolicyCfg(cfg, "omnipus")
			assert.Equal(t, "core", agentType)
			// This exported entry point calls resolveEffectivePolicyWith, the
			// same strictest-wins merge used by the runtime approval gate.
			assert.Equal(t, string(tc.want), tools.ResolveEffectivePolicy(pc, tc.name))
			pc.GlobalPolicies[tc.name] = config.ToolPolicyDeny
			assert.Equal(t, "deny", tools.ResolveEffectivePolicy(pc, tc.name), "operator tightening must still win")
		})
	}
}
