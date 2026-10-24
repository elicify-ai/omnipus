package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK R2-04 / code-reviewer R2-01 / architect M02: get_agent_tools reports
// effective_policies with the same resolver the runtime uses
// (tools.ResolveEffectivePolicy: wildcards and the God Mode floor), so one
// response never says allow in effective_policies and deny in clamped for the
// same tool. Expected values come from the compositor contract in
// pkg/tools/compositor.go::resolveEffectivePolicyWith.
func TestWU3EffectivePoliciesMatchTheRuntimeResolver(t *testing.T) {
	stored := map[string]config.ToolPolicy{
		"read_file":       config.ToolPolicyAllow, // global ask
		"write_file":      config.ToolPolicyDeny,  // per-agent deny survives God Mode
		"edit_file":       config.ToolPolicyAsk,   // per-agent ask survives God Mode
		"mcp_docs_search": config.ToolPolicyAllow, // global wildcard mcp_* deny
		"ToolSearch":      config.ToolPolicyDeny,  // SPEC.md compact effective-policy contract / CR-03: runtime discovery remains Allow.
	}
	for _, tc := range []struct {
		name    string
		godMode bool
		want    map[string]string
	}{
		{"god mode off", false, map[string]string{
			"read_file": "ask", "write_file": "deny", "edit_file": "ask", "mcp_docs_search": "deny", "ToolSearch": "allow",
		}},
		{"god mode on", true, map[string]string{
			"read_file": "allow", "write_file": "deny", "edit_file": "ask", "mcp_docs_search": "allow", "ToolSearch": "allow",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			d.GodModeActive = func() bool { return tc.godMode }
			cfg.Sandbox.ToolPolicies["read_file"] = "ask"
			cfg.Sandbox.ToolPolicies["write_file"] = "allow"
			cfg.Sandbox.ToolPolicies["edit_file"] = "allow"
			cfg.Sandbox.ToolPolicies["mcp_*"] = "deny"
			policies := make(map[string]config.ToolPolicy, len(stored))
			for k, v := range stored {
				policies[k] = v
			}
			agent := &config.AgentConfig{ID: "policy-subject", Name: "Policy subject", Type: config.AgentTypeCustom,
				Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}}}
			// SPEC.md compact output: live MCP tools are listed only after explicit assignment.
			agent.Tools.MCP.Servers = []config.AgentMCPServerBinding{{ID: "docs", Tools: []string{"search"}, ToolsSpecified: true}}
			search := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "search", Description: "Search documents."})
			require.Equal(t, "mcp_docs_search", search.Name(), "MCP name positive control")
			_, err := agentstore.New(d.Home).CreateState(agent.ID, agent, "Help.")
			require.NoError(t, err)

			// SPEC.md compact output: exact effective policy rows replace effective_policies/clamped JSON blocks.
			_, effective := defaultToolsExistingBody(t, d, agent.ID, map[string]any{"id": agent.ID}, search)
			for name, want := range tc.want {
				assert.Equal(t, want, effective[name], "compact policy[%s]", name)
			}
			storedAfter, err := agentstore.New(d.Home).Get(agent.ID)
			require.NoError(t, err)
			assert.Equal(t, policies, storedAfter.Tools.Builtin.Policies, "compact read must not change stored overrides")

			// The runtime resolver itself is the oracle for every non-infrastructure stored tool.
			resolver := &tools.ToolPolicyCfg{Policies: policies, GodMode: tc.godMode,
				GlobalPolicies: map[string]config.ToolPolicy{"read_file": "ask", "write_file": "allow", "edit_file": "allow", "mcp_*": "deny"}}
			for name := range stored {
				// SPEC.md compact effective-policy contract / CR-03: discovery is the explicit runtime exception, not a bare-map verdict.
				if name == "ToolSearch" {
					assert.Equal(t, "allow", effective[name], "registered discovery cannot be denied or approval-gated")
				} else {
					assert.Equal(t, tools.ResolveEffectivePolicy(resolver, name), effective[name], "runtime resolver for %s", name)
				}
			}
		})
	}
}
