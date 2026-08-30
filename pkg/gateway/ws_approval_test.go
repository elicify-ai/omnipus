package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveApprovalToolPolicy_InfraForceAllow proves the fix for the two-gate
// infra bug: the tool-approval exec gate must force-allow infra tools (ToolSearch) for a
// deny-by-default agent (e.g. Ava), or every lazy/load-on-demand tool becomes
// unreachable at execution time. The force-allow must stay NARROW — it must not
// widen any non-infra tool the agent's policy denies.
func TestResolveApprovalToolPolicy_InfraForceAllow(t *testing.T) {
	// Ava: deny-by-default; create_agent/list_models allowed; ToolSearch NOT listed.
	// There is no DefaultPolicy field any more (CLAUDE.md hard constraint 6) —
	// "exec" gets an explicit "deny" entry below (replacing the removed
	// default-deny fallback) so the "must not widen a denied tool" assertion
	// below exercises an explicit policy decision rather than incidentally
	// relying on the fail-closed "no entry on either side" path.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID: "ava",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								"create_agent": config.ToolPolicyAllow,
								"list_models":  config.ToolPolicyAllow,
								"exec":         config.ToolPolicyDeny,
							},
						},
					},
				},
			},
		},
	}

	// THE FIX: ToolSearch (infra) is always "allow" even for a deny-default agent.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "ToolSearch", "ava"),
		"ToolSearch (infra) must be force-allowed for a deny-default agent")
	// An allowed lazy tool stays allowed.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "create_agent", "ava"))
	// A tool the agent denies is STILL denied — the force-allow must not widen.
	assert.Equal(t, "deny", resolveApprovalToolPolicy(cfg, "exec", "ava"))
	// Infra force-allow holds even with a nil config; non-infra falls back to "ask".
	assert.Equal(t, "allow", resolveApprovalToolPolicy(nil, "ToolSearch", "ava"))
	assert.Equal(t, "ask", resolveApprovalToolPolicy(nil, "exec", "ava"))
}
