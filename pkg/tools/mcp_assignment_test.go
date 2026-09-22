package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestADR090_MCPAssignmentsFilter(t *testing.T) {
	tool := NewMCPTool(nil, "finance_primary", &mcp.Tool{Name: "read_balance"})
	for _, tc := range []struct {
		name     string
		bindings []config.AgentMCPServerBinding
		global   config.ToolPolicy
		god      bool
		allowed  bool
	}{
		{name: "wildcard all", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", Tools: []string{"*"}, ToolsSpecified: true}}, global: config.ToolPolicyAllow, allowed: true},
		{name: "unassigned", global: config.ToolPolicyAllow},
		{name: "assigned all", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary"}}, global: config.ToolPolicyAllow, allowed: true},
		{name: "explicit empty", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", Tools: []string{}, ToolsSpecified: true}}, global: config.ToolPolicyAllow},
		{name: "explicit nil", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", ToolsSpecified: true}}, global: config.ToolPolicyAllow},
		{name: "raw tool selection", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", Tools: []string{"read_balance"}, ToolsSpecified: true}}, global: config.ToolPolicyAllow, allowed: true},
		{name: "public tool selection", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", Tools: []string{tool.Name()}, ToolsSpecified: true}}, global: config.ToolPolicyAllow, allowed: true},
		{name: "other tool", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary", Tools: []string{"write_balance"}, ToolsSpecified: true}}, global: config.ToolPolicyAllow},
		{name: "other server", bindings: []config.AgentMCPServerBinding{{ID: "finance_secondary"}}, global: config.ToolPolicyAllow},
		{name: "global deny", bindings: []config.AgentMCPServerBinding{{ID: "finance_primary"}}, global: config.ToolPolicyDeny},
		{name: "unassigned god mode", global: config.ToolPolicyAllow, god: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ToolPolicyCfg{MCPServers: tc.bindings, GlobalPolicies: map[string]config.ToolPolicy{tool.Name(): tc.global}, GodMode: tc.god}
			got, policies := FilterToolsByPolicy([]Tool{tool}, "core", cfg)
			if tc.allowed {
				if len(got) != 1 || got[0] != tool || policies[tool.Name()] != "allow" {
					t.Fatalf("assigned allowed tool unavailable: tools=%v policies=%v", got, policies)
				}
			} else if len(got) != 0 || len(policies) != 0 {
				t.Fatalf("unassigned or denied tool escaped: tools=%v policies=%v", got, policies)
			}
		})
	}
}

func TestADR090_MCPUnbindingLoadedTool(t *testing.T) {
	tool := NewMCPTool(nil, "connector", &mcp.Tool{Name: "read"})
	policy := map[string]config.ToolPolicy{tool.Name(): config.ToolPolicyAllow}
	before := &ToolPolicyCfg{MCPServers: []config.AgentMCPServerBinding{{ID: "connector"}}, GlobalPolicies: policy}
	loaded, _ := FilterToolsByPolicy([]Tool{tool}, "core", before)
	if len(loaded) != 1 {
		t.Fatal("positive control failed to load assigned connector")
	}
	after := &ToolPolicyCfg{GlobalPolicies: policy}
	got, verdict := FilterToolsByPolicy(loaded, "core", after)
	if len(got) != 0 || len(verdict) != 0 {
		t.Fatal("previously loaded tool remained authorized after unbinding")
	}
}
