// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests pin what boot-time tool-policy validation ACTUALLY does under
// the ADR-077 two-layer model: the global ceiling (cfg.Sandbox.ToolPolicies),
// kept complete for the whole static catalog by config.ReconcileToolPolicyCeiling
// (ADR-076), IS the default for every tool; sparse per-agent overrides only
// ever tighten below it. There is no third layer and no fail-closed per-agent
// deny backfill between them — config.RepairIncompleteToolPolicyCoverage and
// config.ValidateAgentOwnToolPolicyCoverage, which used to live in this
// package's call path, are retired (see their retirement comments in
// pkg/config/validate.go).
//
//  1. The validator (config.ValidateToolPolicyCoverage) DOES run and its
//     abort IS live code — see the caller in gateway.go's RunContextWithOptions
//     (boot) and executeReload (hot-reload), both of which return an error on
//     any remaining gap.
//
//  2. But config.ReconcileToolPolicyCeiling runs immediately BEFORE it and
//     keeps the GLOBAL ceiling complete for the whole static catalog, so a
//     both-sides gap for a catalog tool is impossible in practice — the abort
//     is a never-firing correctness tripwire, not a normal-operation path.
//
//  3. A tool an agent never mentions in its own map — e.g. bash for an agent
//     with no explicit bash entry — resolves from the reconciled ceiling's
//     shipped default. For bash that is "allow" (CLAUDE.md hard constraint 6:
//     bash is registered for every agent and the kernel sandbox is the
//     protective layer). That is the intended, ratified behaviour, not a gap
//     to paper over with a fail-closed backfill.
package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestRepairAndValidate_BothSidesGap_ResolvesFromReconciledCeiling_NoDenyBackfill
// documents the ADR-077 two-layer model with a live assertion rather than a
// comment: given an agent with an EMPTY per-agent policy map and a
// deliberately EMPTY global ceiling, the shared boot/reload helper reconciles
// the global ceiling to the shipped static-catalog defaults (never touching
// the agent's own map), returns ZERO remaining gaps, and bash resolves
// "allow" purely from that reconciled ceiling.
//
// This is the guard called for by ADR-077 D6 Guard 1: if a fail-closed
// per-agent deny backfill is ever reintroduced into the load path, this test
// fails loudly — either the agent's own policies map stops being empty, or
// bash stops resolving "allow" from the ceiling alone.
func TestRepairAndValidate_BothSidesGap_ResolvesFromReconciledCeiling_NoDenyBackfill(t *testing.T) {
	cfg := &config.Config{
		// A deliberately EMPTY global ceiling: without ReconcileToolPolicyCeiling
		// closing it, every static tool would be a genuine both-sides gap.
		Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{}},
		Agents: config.AgentsConfig{List: []config.AgentConfig{{
			ID:    "legacy-agent",
			Tools: &config.AgentToolsCfg{},
		}}},
	}

	gaps := repairAndValidateToolPolicyCoverage(cfg)
	assert.Empty(t, gaps,
		"ReconcileToolPolicyCeiling closes every gap by filling the GLOBAL ceiling with shipped "+
			"defaults, so the boot abort is unreachable in practice for a catalog tool")

	// The agent's OWN policy map must stay empty — no per-agent deny backfill.
	require.NotNil(t, cfg.Agents.List[0].Tools)
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	assert.Empty(t, policies,
		"ADR-077: there must be no fail-closed per-agent backfill; an agent that never mentions a "+
			"tool must ride the global ceiling, not gain a synthesized entry of its own")

	// bash resolves "allow" from the reconciled global ceiling alone.
	require.Contains(t, cfg.Sandbox.ToolPolicies, "bash", "ReconcileToolPolicyCeiling must have filled bash into the global ceiling")
	wantBash := config.DefaultConfig().Sandbox.ToolPolicies["bash"]
	require.Equal(t, "allow", wantBash, "fixture assumption: bash ships allow by default")
	assert.Equal(t, wantBash, cfg.Sandbox.ToolPolicies["bash"],
		"bash must resolve to its shipped default (allow) from the reconciled ceiling, not a deny backfill")

	// And the reconciled config is genuinely complete by the coverage definition.
	assert.Empty(t, config.ValidateToolPolicyCoverage(cfg, buildKnownBuiltinToolNames()))
}
