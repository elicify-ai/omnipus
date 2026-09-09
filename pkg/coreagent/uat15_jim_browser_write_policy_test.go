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
// config.ReconcileToolPolicyCeiling runs — that reconcile is what the gateway
// runs at every boot and every hot reload (ADR-076/ADR-077's two-layer
// model); it only ever ADDS a shipped-default entry to the GLOBAL ceiling,
// never touches a per-agent map, so it must never be able to change Jim's
// resolved posture for these verbs.
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
	config.ReconcileToolPolicyCeiling(cfg, known)

	for _, tool := range uat15Tools {
		if got := resolveFor(t, cfg, string(coreagent.IDJim), tool, nil); got != "allow" {
			t.Errorf("AFTER ReconcileToolPolicyCeiling: (jim, %s) resolves %q, want \"allow\"", tool, got)
		}
	}
}
