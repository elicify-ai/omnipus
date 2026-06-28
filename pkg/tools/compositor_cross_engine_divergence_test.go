// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Cross-engine divergence characterization (#438).
//
// Omnipus has historically carried TWO glob-based policy resolvers. They are NOT
// redundant — they govern DIFFERENT live decisions and use DIFFERENT precedence:
//
//  1. pkg/tools.FilterToolsByPolicy ("the compositor")  — THE LIVE authority for
//     TOOL-NAME access. Called at tool-list filter time (pkg/agent/loop.go) and at
//     dispatch re-check. Among matching WILDCARDS it uses MOST-SPECIFIC-WINS
//     (longest matching prefix wins; exact name beats any wildcard). Across the
//     global × agent LAYERS it is strictest-wins (deny > ask > allow).
//
//  2. pkg/policy.SecurityConfig.ResolveToolPolicy / Evaluator.EvaluateTool — uses
//     STRICTEST-WINS across ALL matching globs (resolveStrictestPolicy: any deny
//     beats any allow, no specificity tiebreak). On the LIVE path this engine
//     governs only EXEC-COMMAND access (Evaluator.EvaluateExec via
//     pkg/tools/shell.go). Its TOOL-NAME entry points (EvaluateTool, Engine.Evaluate,
//     resolveGlobalToolPolicy, builtinToolPolicies) have NO live caller for tool
//     dispatch — they are exercised only by tests. The single builtin safety
//     default (browser.evaluate = deny) is independently enforced by the tool's own
//     executeEnabled flag gate (pkg/tools/browser), so the vestigial path leaves no
//     live safety gap TODAY. WARNING: a NEW entry added to builtinToolPolicies would
//     NOT be enforced on the live path — it needs its own runtime gate (like
//     executeEnabled) or a real entry in the live FilterToolsByPolicy config.
//
// These tests PIN the precedence difference so it cannot silently drift, and assert
// that the compositor (the live tool authority) honors a specific-allow carve-out
// from a broad deny. If a future change routes tool-name decisions through the
// pkg/policy engine, TestCrossEngine_SpecificAllowCarveOut_EnginesDiverge will
// start failing on the compositor assertion — forcing a deliberate reconciliation
// (and an ADR) rather than an accidental semantic change.
//
// Traces to: #438, FR-009, FR-071.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// toolNames extracts the Name() of each tool for set assertions.
func toolNames(ts []Tool) map[string]bool {
	out := make(map[string]bool, len(ts))
	for _, t := range ts {
		out[t.Name()] = true
	}
	return out
}

// TestCrossEngine_SpecificAllowCarveOut_EnginesDiverge documents the one case
// where the two resolvers intentionally disagree: a specific allow carved out of
// a broad wildcard deny. The compositor (live tool authority) honors the carve-out
// (most-specific-wins); the pkg/policy resolver denies it (strictest-wins).
func TestCrossEngine_SpecificAllowCarveOut_EnginesDiverge(t *testing.T) {
	// Identical intent expressed to both engines:
	//   broad:    deny every tool from server "foo"
	//   carveout: but explicitly allow the one safe tool
	const broad = "mcp_foo_*"
	const carveout = "mcp_foo_safe"
	const danger = "mcp_foo_danger"

	// --- Engine 2: pkg/policy (strictest-wins) — exec-domain / vestigial for tools.
	sc := &policy.SecurityConfig{
		ToolPolicies: map[string]policy.ToolPolicy{
			broad:    policy.ToolPolicyDeny,
			carveout: policy.ToolPolicyAllow,
		},
	}
	// Strictest-wins: the broad deny beats the specific allow.
	assert.Equal(t, policy.ToolPolicyDeny, sc.ResolveToolPolicy(carveout),
		"pkg/policy uses strictest-wins: broad deny must override the specific allow")
	assert.Equal(t, policy.ToolPolicyDeny, sc.ResolveToolPolicy(danger),
		"pkg/policy denies the dangerous tool")

	// --- Engine 1: compositor (most-specific-wins) — THE LIVE tool authority.
	filtered, polMap := FilterToolsByPolicy(
		makeMCPAdapters("foo", carveout, danger),
		"custom-agent",
		&ToolPolicyCfg{
			GlobalPolicies: map[string]string{
				broad:    "deny",
				carveout: "allow",
			},
			GlobalDefaultPolicy: "allow",
			DefaultPolicy:       "allow",
		},
	)
	names := toolNames(filtered)
	// Most-specific-wins: the exact carve-out survives, the broad deny removes danger.
	assert.True(t, names[carveout],
		"compositor (live tool authority) honors the specific-allow carve-out from a broad deny")
	assert.False(t, names[danger],
		"compositor still applies the broad deny to the non-carved tool")
	assert.Equal(t, "allow", polMap[carveout],
		"the carved-out tool resolves to allow on the live path")

	// The assertions above ENCODE the divergence: same input, opposite verdict on
	// `carveout`. This is intentional and bounded (see file header). Do not
	// "fix" it by aligning the two without an ADR (#438).
}

// TestCrossEngine_BroadDenyNoCarveOut_EnginesAgree confirms the engines AGREE in
// the common case (broad deny, no specific allow) — the divergence is narrow.
func TestCrossEngine_BroadDenyNoCarveOut_EnginesAgree(t *testing.T) {
	const broad = "mcp_bar_*"
	const tool = "mcp_bar_anything"

	sc := &policy.SecurityConfig{
		ToolPolicies: map[string]policy.ToolPolicy{broad: policy.ToolPolicyDeny},
	}
	assert.Equal(t, policy.ToolPolicyDeny, sc.ResolveToolPolicy(tool),
		"pkg/policy denies under the broad wildcard")

	filtered, _ := FilterToolsByPolicy(
		makeMCPAdapters("bar", tool),
		"custom-agent",
		&ToolPolicyCfg{
			GlobalPolicies:      map[string]string{broad: "deny"},
			GlobalDefaultPolicy: "allow",
			DefaultPolicy:       "allow",
		},
	)
	assert.False(t, toolNames(filtered)[tool],
		"compositor also denies under the broad wildcard — engines agree absent a carve-out")
}
