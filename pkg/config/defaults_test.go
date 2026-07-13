// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk verifies that a fresh
// install's default config seeds sandbox.tool_policies as a FULLY-ENUMERATED
// global ceiling: every static builtin tool defaults to "allow" except
// irreversible delete_*/remove_* actions, which ask for confirmation.
// disable_channel is deliberately NOT in the destructive set (reversible, not
// a delete). This ceiling can only be tightened per-agent, never loosened —
// the runtime filter resolves global x agent as most-restrictive-wins
// (pkg/agent/instance.go:agentToolsCfgToPolicy) — so this map does not, by
// itself, change what any agent (all deny-by-default least-privilege,
// pkg/coreagent/core.go) can actually do; it only raises the ceiling an
// operator could grant up to.
//
// This is a genuine configuration value the operator can see/edit at any
// time (Settings -> Security -> Tool Policies, or by hand-editing
// config.json) — not resolution-time code logic.
func TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk(t *testing.T) {
	cfg := DefaultConfig()

	destructive := map[string]bool{
		"delete_agent":             true,
		"delete_workspace":         true,
		"delete_task":              true,
		"delete_task_in_workspace": true,
		"remove_mcp_server":        true,
		"remove_skill":             true,
	}

	for name := range destructive {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != "ask" {
			t.Errorf("expected seeded policy 'ask' for destructive tool %q, got %q", name, got)
		}
	}

	// The global map must be a full, wildcard-free enumeration (CLAUDE.md hard
	// constraint 6) — 78 static builtin tools (32 general + 11 browser + 35
	// sysagent), matching pkg/coreagent's allStaticToolNames literal-for-literal.
	// Browser gained browser_list_tabs / browser_switch_tab / browser_close_tab
	// (ADR-041 multi-tab), taking browser from 7 to 10, then browser_open_tab
	// (live-UAT finding, Alex — "no agent tool to open a new tab"), taking it to 11.
	// General gained recall_conversation — the 4th memory tool (remember /
	// recall_memory / run_retrospective / recall_conversation) that was
	// registered but omitted from the seed + catalog, so it was denied-by-default
	// (fail-closed) on every install — taking general from 31 to 32.
	const wantToolCount = 78
	if got := len(cfg.Sandbox.ToolPolicies); got != wantToolCount {
		t.Errorf("expected sandbox.tool_policies to enumerate all %d static builtin tools, got %d entries", wantToolCount, got)
	}

	// Every non-destructive entry must be "allow" — this is an allow-by-default
	// ceiling, not a narrow ask-list. disable_channel is explicitly checked as
	// the canonical "reversible, not destructive" example.
	for name, policy := range cfg.Sandbox.ToolPolicies {
		if destructive[name] {
			continue
		}
		if policy != "allow" {
			t.Errorf("expected non-destructive tool %q to be seeded 'allow', got %q", name, policy)
		}
	}
	if got := cfg.Sandbox.ToolPolicies["disable_channel"]; got != "allow" {
		t.Errorf("disable_channel is reversible, not a delete — expected 'allow', got %q", got)
	}
}
