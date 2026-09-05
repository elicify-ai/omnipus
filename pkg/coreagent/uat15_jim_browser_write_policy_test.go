// Omnipus — UAT-15 (agent half) root-cause probe.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file exists to KILL one hypothesis with a tool result rather than an
// argument: "UAT-15 (agent half) fails on the merge because Jim's browser
// write verbs resolve to deny/ask after the release branch's tool-policy
// work, so the agent narrates a type it never performed".
//
// It resolves the exact tools UAT-15 needs (browser_type, plus the ones the
// model actually reached for in the failing CI run: browser_click,
// browser_screenshot, browser_navigate) through the REAL compositor, on a
// fresh-install config, BOTH before and AFTER
// config.RepairIncompleteToolPolicyCoverage runs — because the repair is what
// silently stamps `deny` into an agent's own map for any catalog name the seed
// missed, and it runs at every boot and every hot reload.
package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// uat15Tools are the browser verbs the UAT-15 agent half depends on.
var uat15Tools = []string{
	"browser_type",
	"browser_click",
	"browser_navigate",
	"browser_screenshot",
}

func TestUAT15_JimResolvesAllowForBrowserWriteVerbs(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig reported no change on a fresh config")

	for _, tool := range uat15Tools {
		if got := resolveFor(t, cfg, string(coreagent.IDJim), tool, nil); got != "allow" {
			t.Errorf("BEFORE repair: (jim, %s) resolves %q, want \"allow\"", tool, got)
		}
	}

	// Now do what the gateway does at boot and at every hot reload.
	known := make(map[string]struct{})
	for _, ac := range cfg.Agents.List {
		if ac.Tools == nil {
			continue
		}
		for name := range ac.Tools.Builtin.Policies {
			known[name] = struct{}{}
		}
	}
	for name := range cfg.Sandbox.ToolPolicies {
		known[name] = struct{}{}
	}
	for _, tool := range uat15Tools {
		known[tool] = struct{}{}
	}
	repaired := config.RepairIncompleteToolPolicyCoverage(cfg, known)
	for _, gap := range repaired {
		if gap.AgentID == string(coreagent.IDJim) {
			t.Logf("repair backfilled deny: (jim, %s)", gap.ToolName)
		}
	}

	for _, tool := range uat15Tools {
		if got := resolveFor(t, cfg, string(coreagent.IDJim), tool, nil); got != "allow" {
			t.Errorf("AFTER RepairIncompleteToolPolicyCoverage: (jim, %s) resolves %q, want \"allow\"", tool, got)
		}
	}
}
