package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
		"ToolSearch":      config.ToolPolicyDeny,  // infrastructure: always callable
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
			_, err := agentstore.New(d.Home).CreateState(agent.ID, agent, "Help.")
			require.NoError(t, err)

			read := wu3Success(t, systools.NewAgentGetToolsTool(d).Execute(context.Background(), map[string]any{"id": agent.ID}))
			effective := wu3Object(t, read["effective_policies"])
			for name, want := range tc.want {
				assert.Equal(t, want, effective[name], "effective_policies[%s]", name)
			}
			clamped, ok := read["clamped"].([]any)
			require.True(t, ok)
			for _, row := range clamped {
				r := wu3Object(t, row)
				tool, _ := r["tool"].(string)
				assert.Equal(t, effective[tool], r["effective"], "clamped and effective_policies agree on %s", tool)
				assert.NotEqual(t, "ToolSearch", tool, "an always-callable tool is never reported clamped")
			}
			// The runtime resolver itself is the oracle for every non-infrastructure stored tool.
			resolver := &tools.ToolPolicyCfg{Policies: policies, GodMode: tc.godMode,
				GlobalPolicies: map[string]config.ToolPolicy{"read_file": "ask", "write_file": "allow", "edit_file": "allow", "mcp_*": "deny"}}
			for name := range stored {
				if name == "ToolSearch" {
					continue
				}
				assert.Equal(t, tools.ResolveEffectivePolicy(resolver, name), effective[name], "runtime resolver for %s", name)
			}
		})
	}
}
