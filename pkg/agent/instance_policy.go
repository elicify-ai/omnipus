// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agent — tool-policy resolution helpers.
//
// resolveToolPolicy centralizes the strictest-wins merge of:
//   (a) the global sandbox tool_policies + default_tool_policy from config.Config, and
//   (b) the per-agent builtin tool policies from config.AgentConfig.Tools.
//
// This mirrors pkg/policy.SecurityConfig.ResolveToolPolicy but operates on the
// config.Config representation used at agent-instance construction time, where the
// policy.SecurityConfig bridge may not yet be available.

package agent

import (
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// builtinAgentToolPolicies lists tools that are deny-by-default regardless of
// operator configuration (SEC-04/SEC-06 safety floor). Operators may override
// in config.json under sandbox.tool_policies.
var builtinAgentToolPolicies = map[string]policy.ToolPolicy{
	"browser.evaluate": policy.ToolPolicyDeny,
}

// resolveToolPolicy returns the effective policy for toolName by merging:
//  1. The per-agent policy (from agentCfg.Tools.Builtin.Policies[toolName]).
//  2. The global sandbox policy (from cfg.Sandbox.ToolPolicies[toolName]).
//  3. The baked-in safety defaults (builtinAgentToolPolicies).
//  4. The global default policy (cfg.Sandbox.DefaultToolPolicy).
//  5. ToolPolicyAllow as the final fallback.
//
// The strictest value across (1), (2), and (3) wins. This ensures that a
// global deny cannot be overridden by a per-agent allow.
func resolveToolPolicy(cfg *config.Config, agentCfg *config.AgentConfig, toolName string) policy.ToolPolicy {
	effective := policy.ToolPolicyAllow

	// Apply baked-in safety defaults first.
	if p, ok := builtinAgentToolPolicies[toolName]; ok {
		effective = strictestPolicy(effective, p)
	}

	// Apply global sandbox default.
	if cfg != nil && cfg.Sandbox.DefaultToolPolicy != "" {
		global := policy.ToolPolicy(cfg.Sandbox.DefaultToolPolicy)
		effective = strictestPolicy(effective, global)
	}

	// Apply global per-tool override (wins over default, loses to per-agent if
	// per-agent is stricter — strictestPolicy handles the merge).
	if cfg != nil {
		if raw, ok := cfg.Sandbox.ToolPolicies[toolName]; ok {
			effective = strictestPolicy(effective, policy.ToolPolicy(raw))
		}
	}

	// Apply per-agent override.
	if agentCfg != nil && agentCfg.Tools != nil {
		if raw := agentCfg.Tools.Builtin.ResolvePolicy(toolName); raw != "" {
			effective = strictestPolicy(effective, policy.ToolPolicy(raw))
		}
	}

	return effective
}

// strictestPolicy returns the stricter of the two policies.
// Ordering: deny > ask > allow (> empty, treated as allow).
// When both inputs are at the "allow" tier (including the empty string),
// the canonical non-empty "allow" value is returned so callers can compare
// against policy.ToolPolicyAllow without special-casing the empty string.
func strictestPolicy(a, b policy.ToolPolicy) policy.ToolPolicy {
	rank := func(p policy.ToolPolicy) int {
		switch p {
		case policy.ToolPolicyDeny:
			return 2
		case policy.ToolPolicyAsk:
			return 1
		default: // allow or empty
			return 0
		}
	}
	ra, rb := rank(a), rank(b)
	if rb > ra {
		return b
	}
	if ra > 0 {
		return a
	}
	// Both are at rank 0 (allow or empty). Return the canonical "allow" value
	// so callers don't need to handle empty-string as a special case.
	return policy.ToolPolicyAllow
}

// toolAllowed reports whether toolName should be registered in the tool
// catalog for the given config and agent. A denied tool is invisible to the
// LLM (not registered). An "ask" tool is registered but gated at dispatch.
func toolAllowed(cfg *config.Config, agentCfg *config.AgentConfig, toolName string) bool {
	return resolveToolPolicy(cfg, agentCfg, toolName) != policy.ToolPolicyDeny
}
