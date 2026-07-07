// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Compositor precedence characterization (#438, #70).
//
// pkg/tools.FilterToolsByPolicy ("the compositor") is THE LIVE authority for
// TOOL-NAME access. Called at tool-list filter time (pkg/agent/loop.go) and at
// dispatch re-check. Among matching WILDCARDS it uses MOST-SPECIFIC-WINS
// (longest matching prefix wins; exact name beats any wildcard). Across the
// global × agent LAYERS it is strictest-wins (deny > ask > allow).
//
// Omnipus used to also carry a second, pkg/policy-based glob resolver
// (SecurityConfig.ResolveToolPolicy / Evaluator.EvaluateTool, strictest-wins
// with no specificity tiebreak) that diverged from the compositor on exactly
// the case below. That resolver had no live caller for tool dispatch — it was
// exercised only by tests — and was removed (#70). These tests now pin the
// compositor's own precedence behavior on its own terms.
//
// Traces to: #438, FR-009, FR-071.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// toolNames extracts the Name() of each tool for set assertions.
func toolNames(ts []Tool) map[string]bool {
	out := make(map[string]bool, len(ts))
	for _, t := range ts {
		out[t.Name()] = true
	}
	return out
}

// TestCompositor_SpecificAllowCarveOut_HonorsMostSpecific verifies the
// compositor (live tool authority) honors a specific-allow carve-out from a
// broad wildcard deny (most-specific-wins).
func TestCompositor_SpecificAllowCarveOut_HonorsMostSpecific(t *testing.T) {
	// Intent: deny every tool from server "foo", but explicitly allow one safe tool.
	const broad = "mcp_foo_*"
	const carveout = "mcp_foo_safe"
	const danger = "mcp_foo_danger"

	filtered, polMap := FilterToolsByPolicy(
		makeMCPAdapters("foo", carveout, danger),
		"custom-agent",
		&ToolPolicyCfg{
			GlobalPolicies: map[string]string{
				broad:    "deny",
				carveout: "allow",
			},
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
}

// TestCompositor_BroadDenyNoCarveOut_DeniesAll confirms the common case:
// a broad wildcard deny with no specific-allow carve-out denies everything
// it matches.
func TestCompositor_BroadDenyNoCarveOut_DeniesAll(t *testing.T) {
	const broad = "mcp_bar_*"
	const tool = "mcp_bar_anything"

	filtered, _ := FilterToolsByPolicy(
		makeMCPAdapters("bar", tool),
		"custom-agent",
		&ToolPolicyCfg{
			GlobalPolicies: map[string]string{broad: "deny"},
		},
	)
	assert.False(t, toolNames(filtered)[tool],
		"compositor denies under the broad wildcard absent a carve-out")
}
