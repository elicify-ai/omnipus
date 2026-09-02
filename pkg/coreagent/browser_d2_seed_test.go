// Omnipus — the D2 browser seed, verified by RESOLUTION rather than by
// reading the seed map (capability spec §10 orders 1, 3, 4).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// READ THIS BEFORE ADDING A "COVERAGE HAS NO GAPS" ASSERTION ANYWHERE NEAR
// THIS FILE.
//
// Tool-policy COVERAGE is not the gate here and cannot be. At boot and at hot
// reload, pkg/gateway runs config.RepairIncompleteToolPolicyCoverage BEFORE
// config.ValidateToolPolicyCoverage: the repair backfills every (agent, tool)
// gap with an explicit `deny` and logs one WARN, and validation then reports
// zero gaps. Worse, if allStaticToolNames gains the six names while the
// per-agent maps do not, denyAllThenOverride stamps `deny` for all six on
// every seeded agent, coverage is COMPLETE, and there is no WARN at all.
//
// So coverage passes in BOTH failure directions. What can actually go red is
// the RESOLVED value, per seeded agent, through the real production
// compositor — pkg/tools.ResolveEffectivePolicy over
// resolveEffectivePolicyWith, the same merge the agent loop's tool filter and
// the gateway's approval hook both run. Every posture assertion below goes
// through it. None reads a seed map.

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// d2BrowserVerbs are the five D2 tools that resolve `allow` for a
// browser-capable agent. browser_upload_file is deliberately not among them —
// it is `ask`, and mixing it in would make an "everything allows" assertion
// that could never see the ask/allow distinction FR-021 turns on.
var d2BrowserVerbs = []string{
	"browser_select_option",
	"browser_press_key",
	"browser_hover",
	"browser_snapshot",
}

// browsingAgents are the seeded agents that hold the browser surface.
var browsingAgents = []coreagent.CoreAgentID{coreagent.IDJim, coreagent.IDRay, coreagent.IDExplorer, coreagent.IDResearcher}

// zeroBrowserAgents hold no browser tool at all and are expected to resolve
// deny for every one of the six by their own least-privilege default —
// denyAllThenOverride starts every catalog name at deny and neither names a
// browser tool.
var zeroBrowserAgents = []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDAva}

// d2Resolve resolves one (agent, tool) pair through the REAL compositor,
// starting from a real DefaultConfig() + SeedConfig() install: the agent's own
// seeded map against the real global ceiling, merged strictest-wins exactly as
// production does. It reads no seed literal.
func d2Resolve(t *testing.T, agent coreagent.CoreAgentID, toolName string) string {
	t.Helper()
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig reported no change on a fresh config")
	return resolveFor(t, cfg, string(agent), toolName, nil)
}

// TestCoreAgentSeed_BrowserD2Posture is the gate: every (agent, tool) cell of
// the spec's seeded-policy table, resolved through the real merge.
func TestCoreAgentSeed_BrowserD2Posture(t *testing.T) {
	for _, agent := range browsingAgents {
		for _, tool := range d2BrowserVerbs {
			if got := d2Resolve(t, agent, tool); got != "allow" {
				t.Errorf("(%s, %s) resolves %q, want \"allow\". The tool is registered and appears "+
					"in the catalog, so this is not a visible failure: %s would simply be refused "+
					"every time it called it", agent, tool, got, agent)
			}
		}
	}
	for _, agent := range zeroBrowserAgents {
		for _, tool := range append(append([]string{}, d2BrowserVerbs...), "browser_upload_file") {
			if got := d2Resolve(t, agent, tool); got != "deny" {
				t.Errorf("(%s, %s) resolves %q, want \"deny\". %s holds no browser tool at all; a "+
					"non-deny here means the global ceiling has reached an agent whose own seed "+
					"never granted it", agent, tool, got, agent)
			}
		}
	}
}

// TestCoreAgentSeed_UploadIsAskForEveryBrowsingAgent covers FR-021's
// operator ruling: `ask`, not `deny`, for every agent that holds the browser
// surface — the delegation tier included.
//
// The distinction matters and a table that only checked "not allow" would miss
// it: `deny` was the proposal the operator OVERRULED, and it is what the seed
// would silently fall back to if the per-agent entries were dropped.
func TestCoreAgentSeed_UploadIsAskForEveryBrowsingAgent(t *testing.T) {
	for _, agent := range browsingAgents {
		got := d2Resolve(t, agent, "browser_upload_file")
		switch got {
		case "ask":
		case "deny":
			t.Errorf("(%s, browser_upload_file) resolves \"deny\". That is the posture the operator "+
				"explicitly overruled (ADR D2.9): the concern about an unattended agent facing an "+
				"approval nobody can answer is met by FR-029 holding the tool unregistered, not by "+
				"denying it here", agent)
		default:
			t.Errorf("(%s, browser_upload_file) resolves %q, want \"ask\". Attaching a host file to "+
				"a page on the operator's signed-in session is the one browser action that moves "+
				"their data outward; it is consent-gated on every agent", agent, got)
		}
	}
}

// TestCoreAgentSeed_ExplorerResearcherBrowserParity is FR-024. The research
// tier gets the same browsing surface Jim and Ray get, minus browser_evaluate
// — and the minus is asserted too, because a parity test that only checked the
// grants would pass on a build that had quietly widened the carve-out.
func TestCoreAgentSeed_ExplorerResearcherBrowserParity(t *testing.T) {
	for _, agent := range []coreagent.CoreAgentID{coreagent.IDExplorer, coreagent.IDResearcher} {
		for _, tool := range d2BrowserVerbs {
			if got := d2Resolve(t, agent, tool); got != "allow" {
				t.Errorf("(%s, %s) resolves %q, want \"allow\" — FR-024 parity with Jim and Ray",
					agent, tool, got)
			}
		}
		if got := d2Resolve(t, agent, "browser_upload_file"); got != "ask" {
			t.Errorf("(%s, browser_upload_file) resolves %q, want \"ask\"", agent, got)
		}
		if got := d2Resolve(t, agent, "browser_evaluate"); got != "deny" {
			t.Errorf("(%s, browser_evaluate) resolves %q, want \"deny\". The research tier's "+
				"ten-allow/one-deny shape is a least-privilege judgement about arbitrary code, and "+
				"D2's verbs were granted precisely BECAUSE none of them is arbitrary-code-adjacent. "+
				"Widening it here would take the judgement with them", agent, got)
		}
	}
}
