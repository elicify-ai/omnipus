package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestAgentToolsCfgToPolicy_GlobalMerge verifies O7 (WS-G): the global sandbox
// tool policies (sandbox.tool_policies) are threaded into the runtime
// tools.ToolPolicyCfg so that FilterToolsByPolicy enforces global × agent
// merge with most-restrictive-wins (deny > ask > allow).
//
// Before the fix, agentToolsCfgToPolicy dropped GlobalPolicies, so an admin's
// global deny showed enforced in the REST view but did NOT block the tool at
// call time.
//
// There is no default-policy fallback (CLAUDE.md hard constraint 6): a tool
// with no exact entry on EITHER side is a coverage gap that
// config.ValidateToolPolicyCoverage makes structurally impossible at boot and
// at every agent write; reaching that state at resolution time fails closed
// to "deny" (see the "no policies" case below).
func TestAgentToolsCfgToPolicy_GlobalMerge(t *testing.T) {
	const tool = "exec"

	mkGlobalCfg := func(perTool string) *config.Config {
		c := &config.Config{}
		if perTool != "" {
			c.Sandbox.ToolPolicies = map[string]string{tool: perTool}
		}
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
			globalCfg:     mkGlobalCfg("deny"),
			agentCfg:      mkAgentCfg(config.ToolPolicyAllow),
			wantEffective: "deny",
		},
		{
			name:          "global-allow with agent-deny is denied (agent may tighten)",
			globalCfg:     mkGlobalCfg("allow"),
			agentCfg:      mkAgentCfg(config.ToolPolicyDeny),
			wantEffective: "deny",
		},
		{
			name:          "global-ask with agent-allow resolves to ask",
			globalCfg:     mkGlobalCfg("ask"),
			agentCfg:      mkAgentCfg(config.ToolPolicyAllow),
			wantEffective: "ask",
		},
		{
			// No explicit entry on either side: a coverage gap that boot/write-time
			// validation (config.ValidateToolPolicyCoverage) makes structurally
			// impossible in practice. Reaching it here must fail closed to "deny",
			// never the historical hardcoded "allow" (CLAUDE.md hard constraint 6).
			name:          "no policy entry on either side fails closed to deny",
			globalCfg:     mkGlobalCfg(""),
			agentCfg:      mkAgentCfg(""),
			wantEffective: "deny",
		},
		{
			name:          "global-deny applies even when agent has no per-agent tools config",
			globalCfg:     mkGlobalCfg("deny"),
			agentCfg:      nil,
			wantEffective: "deny",
		},
		{
			name:          "agent-ask tightens a global-allow to ask",
			globalCfg:     mkGlobalCfg("allow"),
			agentCfg:      mkAgentCfg(config.ToolPolicyAsk),
			wantEffective: "ask",
		},
		{
			// agent-deny is stricter than global-ask → deny wins.
			name:          "global-ask with agent-deny resolves to deny",
			globalCfg:     mkGlobalCfg("ask"),
			agentCfg:      mkAgentCfg(config.ToolPolicyDeny),
			wantEffective: "deny",
		},
		{
			// global-deny is stricter than agent-ask → deny wins (agent cannot
			// loosen a global deny back to ask).
			name:          "global-deny with agent-ask resolves to deny",
			globalCfg:     mkGlobalCfg("deny"),
			agentCfg:      mkAgentCfg(config.ToolPolicyAsk),
			wantEffective: "deny",
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
				t.Errorf("effective policy for %q = %q, want %q (globalPolicies=%v agentPolicies=%v)",
					tool, got, tc.wantEffective,
					policyCfg.GlobalPolicies, policyCfg.Policies)
			}
		})
	}
}

// TestAgentToolsCfgToPolicy_NilGlobalCfg verifies graceful degradation when no
// global config is supplied (legacy/test construction paths): an explicit
// per-agent policy entry is still honored and no panic occurs. There is no
// agent-level default-policy fallback (CLAUDE.md hard constraint 6), so an
// UNLISTED tool resolves to "deny" (fail-closed) rather than any agent-wide
// default.
func TestAgentToolsCfgToPolicy_NilGlobalCfg(t *testing.T) {
	ac := &config.AgentToolsCfg{}
	ac.Builtin.Policies = map[string]config.ToolPolicy{"read_file": "ask"}

	policyCfg := agentToolsCfgToPolicy(nil, ac)
	if policyCfg == nil {
		t.Fatal("agentToolsCfgToPolicy(nil, ac) returned nil")
	}
	if policyCfg.GlobalPolicies != nil {
		t.Errorf("GlobalPolicies = %v, want nil under nil global config", policyCfg.GlobalPolicies)
	}
	// The explicit per-agent entry for read_file is honored.
	if got := tools.ResolveEffectivePolicy(policyCfg, "read_file"); got != "ask" {
		t.Errorf("effective policy = %q, want ask (explicit per-agent entry)", got)
	}
	// An unlisted tool with no coverage on either side fails closed to deny.
	if got := tools.ResolveEffectivePolicy(policyCfg, "write_file"); got != "deny" {
		t.Errorf("effective policy for unlisted tool = %q, want deny (fail-closed, no default fallback)", got)
	}
}
