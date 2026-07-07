// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveEffectivePolicy_GodMode_FloorsAtAllow verifies that with GodMode
// set, every tool resolves to "allow" regardless of deny/ask in the policy maps.
func TestResolveEffectivePolicy_GodMode_FloorsAtAllow(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"system.exec": "deny", "fetch_url": "ask"},
		GlobalPolicies: map[string]config.ToolPolicy{"system.*": "deny"},
		GodMode:        true,
	}
	for _, name := range []string{"system.exec", "fetch_url", "anything.else"} {
		if got := ResolveEffectivePolicy(cfg, name); got != "allow" {
			t.Fatalf("god-mode: ResolveEffectivePolicy(%q) = %q, want allow", name, got)
		}
	}
}

// TestResolveEffectivePolicy_GodModeOff_RestoresDecisions verifies the override
// is non-destructive: clearing GodMode restores the original deny/ask decisions.
func TestResolveEffectivePolicy_GodModeOff_RestoresDecisions(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"system.exec": "deny", "fetch_url": "ask"},
		GlobalPolicies: map[string]config.ToolPolicy{},
		GodMode:        false,
	}
	if got := ResolveEffectivePolicy(cfg, "system.exec"); got != "deny" {
		t.Fatalf("god-mode off: system.exec = %q, want deny", got)
	}
	if got := ResolveEffectivePolicy(cfg, "fetch_url"); got != "ask" {
		t.Fatalf("god-mode off: web_fetch = %q, want ask", got)
	}
}
