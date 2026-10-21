package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-18: "delete later, track" (#1363) supersedes "delete asks".
// Creation rides the allow ceiling; deletion is an explicit per-agent Deny,
// including under God Mode. FR-022 still forbids a per-agent Ask for Omnipus.
func TestTrain1OmnipusWorkspacePolicies_CreateAllowedDeleteDenied(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	omnipus := l1WU2SeededAgent(t, cfg, "omnipus")
	require.NotNil(t, omnipus.Tools)
	inventory := coreagent.ADR090RolePolicyInventory(coreagent.IDOmnipus)
	pc, agentType := tools.BuildFallbackPolicyCfg(cfg, "omnipus")
	require.Equal(t, "core", agentType)

	assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["create_workspace"])
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["delete_workspace"], "the shared global ceiling is unchanged")
	assert.NotContains(t, omnipus.Tools.Builtin.Policies, "create_workspace")
	assert.Equal(t, config.ToolPolicyDeny, omnipus.Tools.Builtin.Policies["delete_workspace"], "#1363: stored Omnipus Deny, not an approval")
	assert.Equal(t, config.ToolPolicyAllow, inventory["create_workspace"])
	assert.Equal(t, config.ToolPolicyDeny, inventory["delete_workspace"])
	assert.Equal(t, "allow", tools.ResolveEffectivePolicy(pc, "create_workspace"))
	assert.Equal(t, "deny", tools.ResolveEffectivePolicy(pc, "delete_workspace"))
	pc.GodMode = true
	assert.Equal(t, "allow", tools.ResolveEffectivePolicy(pc, "create_workspace"))
	assert.Equal(t, "deny", tools.ResolveEffectivePolicy(pc, "delete_workspace"), "God Mode cannot lift Omnipus's own Deny")
	pc.GodMode = false
	pc.GlobalPolicies["create_workspace"] = config.ToolPolicyDeny
	assert.Equal(t, "deny", tools.ResolveEffectivePolicy(pc, "create_workspace"), "operator tightening still wins")
	for name, policy := range omnipus.Tools.Builtin.Policies {
		assert.NotEqual(t, config.ToolPolicyAsk, policy, "FR-022: no per-agent Ask on %s", name)
	}
}
