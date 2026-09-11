// Omnipus — browser_handover seed-assertion test (wave B6).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// core_test.go (wave E1) already proves BROWSER-FR-051's three Constraint
// #6 sites land together — TestSeed_BrowserHandoverAtAllThreeSites and
// TestSeed_MiaAndAvaResolveDenyForBrowserHandover — written BEFORE the tool
// itself existed (its own doc comment names this file as the later, B6
// half: "the tool BODY and its own registration test are wave B6's").
//
// This file is that B6 half: it re-derives the same three sites from this
// package's own exported seams (so a change to either side that breaks the
// pairing is caught from BOTH directions, not just E1's), and adds the one
// check E1's seed-only tests cannot make — that the catalogue name they
// assert actually names a REAL, registered tool. A seeded tool-policy entry
// for a tool that was never implemented is a silent dead entry; this proves
// it is not one.

package coreagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// noopHandoverManagerResolver satisfies browser.ManagerResolver with no real
// browser behind it. browser.RegisterTools never calls ManagerFor during
// registration itself (every tool struct only stores the resolver; it is
// invoked at Execute time) — so a resolver that always errors is sufficient
// to prove registration succeeded without needing a real BrowserManager.
type noopHandoverManagerResolver struct{}

func (noopHandoverManagerResolver) ManagerFor(context.Context) (*browser.BrowserManager, browser.BrowsingKey, browser.TabOwner, error) {
	return nil, browser.BrowsingKey{}, browser.TabOwner{}, errors.New("browser_handover_seed_test: ManagerFor is never called by registration or by this test")
}

// TestSeed_BrowserHandoverAtAllThreeConstraint6Sites re-derives
// BROWSER-FR-051's three Constraint #6 sites (also proven, seed-side only,
// by core_test.go's TestSeed_BrowserHandoverAtAllThreeSites) and adds a
// fourth: that the catalogue name resolves an actually-registered tool.
func TestSeed_BrowserHandoverAtAllThreeConstraint6Sites(t *testing.T) {
	// Site 1: the static catalogue.
	found := false
	for _, n := range coreagent.AllStaticToolNames() {
		if n == "browser_handover" {
			found = true
			break
		}
	}
	require.True(t, found, "allStaticToolNames must contain browser_handover")

	// Site 2: the global ceiling — allow (a decision, not an inheritance:
	// browser_upload_file in the same block is seeded "ask").
	defCfg := config.DefaultConfig()
	ceiling, ok := defCfg.Sandbox.ToolPolicies["browser_handover"]
	require.True(t, ok, "pkg/config/defaults.go must carry a browser_handover ceiling entry")
	assert.Equal(t, string(config.ToolPolicyAllow), ceiling, "browser_handover ceiling must be allow")

	// Site 3: the per-agent seeds for every browser-capable agent.
	cfg := &config.Config{}
	coreagent.SeedConfig(cfg)
	byID := make(map[string]config.AgentConfig, len(cfg.Agents.List))
	for _, ac := range cfg.Agents.List {
		byID[ac.ID] = ac
	}
	for _, id := range []coreagent.CoreAgentID{coreagent.IDJim, coreagent.IDRay, coreagent.IDExplorer, coreagent.IDResearcher} {
		ac, ok := byID[string(id)]
		require.True(t, ok, "agent %q must be seeded", id)
		p, present := ac.Tools.Builtin.Policies["browser_handover"]
		require.True(t, present, "browser-capable agent %q must have an explicit browser_handover policy", id)
		assert.Equal(t, config.ToolPolicyAllow, p, "browser-capable agent %q must resolve browser_handover allow", id)
	}

	// Site 4 (this wave's own addition, not part of FR-051's three sites,
	// but the whole point of a seed-assertion test written AFTER the tool
	// exists): the name above must name a real, registered tool, not a
	// dangling policy entry.
	registry := tools.NewToolRegistry()
	err := browser.RegisterTools(registry, noopHandoverManagerResolver{}, true, t.TempDir(), true)
	require.NoError(t, err, "browser.RegisterTools must succeed")
	tool, ok := registry.Get("browser_handover")
	require.True(t, ok, "browser.RegisterTools must register browser_handover — a seeded policy with no registered tool is a dead entry")
	assert.Equal(t, "browser_handover", tool.Name())
}

// TestSeed_BrowserHandoverToolIsInBrowserCategory pins the wire-facing side
// of the tool that exists once B6 lands: it must self-report
// tools.CategoryBrowser, the category every other browser.* tool uses, so
// it appears in the same tool-picker grouping rather than falling into the
// generic core bucket.
func TestSeed_BrowserHandoverToolIsInBrowserCategory(t *testing.T) {
	registry := tools.NewToolRegistry()
	require.NoError(t, browser.RegisterTools(registry, noopHandoverManagerResolver{}, true, t.TempDir(), true))
	tool, ok := registry.Get("browser_handover")
	require.True(t, ok)
	assert.Equal(t, tools.CategoryBrowser, tool.Category())
}
