package agent

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestAgentToolsCfgToPolicy_GlobalMerge verifies O7 (WS-G): the global sandbox
// tool policies (sandbox.tool_policies / sandbox.default_tool_policy) are
// threaded into the runtime tools.ToolPolicyCfg so that FilterToolsByPolicy
// enforces global × agent merge with most-restrictive-wins (deny > ask > allow).
//
// Before the fix, agentToolsCfgToPolicy dropped GlobalPolicies, so an admin's
// global deny showed enforced in the REST view but did NOT block the tool at
// call time.
func TestAgentToolsCfgToPolicy_GlobalMerge(t *testing.T) {
	const tool = "exec"

	mkGlobalCfg := func(perTool, defPolicy string) *config.Config {
		c := &config.Config{}
		if perTool != "" {
			c.Sandbox.ToolPolicies = map[string]string{tool: perTool}
		}
		c.Sandbox.DefaultToolPolicy = defPolicy
		return c
	}

	mkAgentCfg := func(perTool config.ToolPolicy) *config.AgentToolsCfg {
		ac := &config.AgentToolsCfg{}
		if perTool != "" {
			ac.Builtin.Policies = map[string]config.ToolPolicy{tool: perTool}
		}
		return ac
	}

	tests := []struct {
		name          string
		globalCfg     *config.Config
		agentCfg      *config.AgentToolsCfg
		wantEffective string // resolved effective policy via FilterToolsByPolicy
	}{
		{
			name:          "global-deny beats agent-allow",
			globalCfg:     mkGlobalCfg("deny", ""),
			agentCfg:      mkAgentCfg(config.ToolPolicyAllow),
			wantEffective: "deny",
		},
		{
			name:          "global-allow with agent-deny is denied (agent may tighten)",
			globalCfg:     mkGlobalCfg("allow", ""),
			agentCfg:      mkAgentCfg(config.ToolPolicyDeny),
			wantEffective: "deny",
		},
		{
			name:          "global-ask with agent-allow resolves to ask",
			globalCfg:     mkGlobalCfg("ask", ""),
			agentCfg:      mkAgentCfg(config.ToolPolicyAllow),
			wantEffective: "ask",
		},
		{
			name:          "no policies resolves to allow",
			globalCfg:     mkGlobalCfg("", ""),
			agentCfg:      mkAgentCfg(""),
			wantEffective: "allow",
		},
		{
			name:          "global-deny applies even when agent has no per-agent tools config",
			globalCfg:     mkGlobalCfg("deny", ""),
			agentCfg:      nil,
			wantEffective: "deny",
		},
		{
			name:          "global default-policy deny blocks an otherwise-unlisted tool",
			globalCfg:     mkGlobalCfg("", "deny"),
			agentCfg:      mkAgentCfg(config.ToolPolicyAllow),
			wantEffective: "deny",
		},
		{
			name:          "agent-ask tightens a global-allow to ask",
			globalCfg:     mkGlobalCfg("allow", ""),
			agentCfg:      mkAgentCfg(config.ToolPolicyAsk),
			wantEffective: "ask",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policyCfg := agentToolsCfgToPolicy(tc.globalCfg, tc.agentCfg)
			if policyCfg == nil {
				t.Fatal("agentToolsCfgToPolicy returned nil")
			}
			got := tools.ResolveEffectivePolicy(policyCfg, tool)
			if got != tc.wantEffective {
				t.Errorf("effective policy for %q = %q, want %q (globalPolicies=%v globalDefault=%q agentPolicies=%v)",
					tool, got, tc.wantEffective,
					policyCfg.GlobalPolicies, policyCfg.GlobalDefaultPolicy, policyCfg.Policies)
			}
		})
	}
}

// TestAgentToolsCfgToPolicy_NilGlobalCfg verifies graceful degradation when no
// global config is supplied (legacy/test construction paths): the per-agent
// policy is still honored and no panic occurs.
func TestAgentToolsCfgToPolicy_NilGlobalCfg(t *testing.T) {
	ac := &config.AgentToolsCfg{}
	ac.Builtin.DefaultPolicy = config.ToolPolicyAsk

	policyCfg := agentToolsCfgToPolicy(nil, ac)
	if policyCfg == nil {
		t.Fatal("agentToolsCfgToPolicy(nil, ac) returned nil")
	}
	if policyCfg.DefaultPolicy != "ask" {
		t.Errorf("DefaultPolicy = %q, want ask", policyCfg.DefaultPolicy)
	}
	if policyCfg.GlobalPolicies != nil {
		t.Errorf("GlobalPolicies = %v, want nil under nil global config", policyCfg.GlobalPolicies)
	}
	// An unlisted tool falls back to the agent default (ask).
	if got := tools.ResolveEffectivePolicy(policyCfg, "read_file"); got != "ask" {
		t.Errorf("effective policy = %q, want ask (agent default)", got)
	}
}
