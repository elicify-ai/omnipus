// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk verifies that a fresh
// install's default config pre-populates sandbox.tool_policies with "ask" for
// irreversible/destructive tool actions (delete_agent, delete_workspace,
// remove_mcp_server, remove_skill), while leaving the general tool-policy
// default untouched (empty DefaultToolPolicy, which resolves to "allow" in
// pkg/tools/compositor.go) and NOT seeding delete_task_in_workspace or
// disable_channel — both deliberately excluded (task deletion and channel
// disabling are not treated as destructive enough to require confirmation).
//
// This is a genuine configuration value the operator can see/edit at any
// time (Settings -> Security -> Tool Policies, or by hand-editing
// config.json) — not resolution-time code logic.
func TestDefaultConfig_SeedsDestructiveToolPoliciesAsAsk(t *testing.T) {
	cfg := DefaultConfig()

	for _, name := range []string{"delete_agent", "delete_workspace", "remove_mcp_server", "remove_skill"} {
		got, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("expected sandbox.tool_policies to seed an entry for %q, found none", name)
			continue
		}
		if got != "ask" {
			t.Errorf("expected seeded policy 'ask' for %q, got %q", name, got)
		}
	}

	for _, name := range []string{"delete_task_in_workspace", "disable_channel"} {
		if got, ok := cfg.Sandbox.ToolPolicies[name]; ok {
			t.Errorf("%q must NOT be seeded with a destructive-tool policy, got %q", name, got)
		}
	}

	if cfg.Sandbox.DefaultToolPolicy != "" {
		t.Errorf(
			"expected DefaultToolPolicy to stay unset (resolves to 'allow'), got %q",
			cfg.Sandbox.DefaultToolPolicy,
		)
	}
}
