// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"
)

// godModeAskTool is a minimal Tool whose RequiresAdminAsk() is true and whose
// agent-level policy would normally resolve to "ask"/"deny". Used to prove the
// O14 god-mode override floors it at "allow" and skips the admin-ask fence.
type godModeAskTool struct{ name string }

func (g godModeAskTool) Name() string               { return g.name }
func (g godModeAskTool) Description() string        { return "test tool" }
func (g godModeAskTool) Parameters() map[string]any { return map[string]any{} }
func (g godModeAskTool) Scope() ToolScope           { return ScopeGeneral }
func (g godModeAskTool) RequiresAdminAsk() bool     { return true }
func (g godModeAskTool) Category() ToolCategory     { return CategoryCore }
func (g godModeAskTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("")
}

// TestResolveEffectivePolicy_GodMode_FloorsAtAllow verifies that with GodMode
// set, every tool resolves to "allow" regardless of deny/ask in the policy maps.
func TestResolveEffectivePolicy_GodMode_FloorsAtAllow(t *testing.T) {
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "deny",
		GlobalDefaultPolicy: "deny",
		Policies:            map[string]string{"system.exec": "deny", "fetch_url": "ask"},
		GlobalPolicies:      map[string]string{"system.*": "deny"},
		GodMode:             true,
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
		DefaultPolicy:  "allow",
		Policies:       map[string]string{"system.exec": "deny", "fetch_url": "ask"},
		GlobalPolicies: map[string]string{},
		GodMode:        false,
	}
	if got := ResolveEffectivePolicy(cfg, "system.exec"); got != "deny" {
		t.Fatalf("god-mode off: system.exec = %q, want deny", got)
	}
	if got := ResolveEffectivePolicy(cfg, "fetch_url"); got != "ask" {
		t.Fatalf("god-mode off: web_fetch = %q, want ask", got)
	}
}

// TestFilterToolsByPolicy_GodMode_SkipsAdminAskFence proves that under god mode a
// RequiresAdminAsk tool on a CUSTOM agent (which would normally be downgraded to
// "ask" by the admin-ask fence) resolves to "allow" and is included.
func TestFilterToolsByPolicy_GodMode_SkipsAdminAskFence(t *testing.T) {
	tool := godModeAskTool{name: "system.dangerous"}
	cfg := &ToolPolicyCfg{DefaultPolicy: "allow", IsCoreAgent: false, GodMode: true}

	filtered, policyMap := FilterToolsByPolicy([]Tool{tool}, "custom", cfg)
	if len(filtered) != 1 {
		t.Fatalf("god-mode: expected the tool to pass the filter, got %d tools", len(filtered))
	}
	if p := policyMap["system.dangerous"]; p != "allow" {
		t.Fatalf("god-mode: admin-ask tool resolved to %q, want allow (fence must be skipped)", p)
	}
}

// TestFilterToolsByPolicy_NoGodMode_AdminAskFenceApplies is the control: with
// GodMode off, the same RequiresAdminAsk tool on a custom agent is downgraded to
// "ask" by the fence.
func TestFilterToolsByPolicy_NoGodMode_AdminAskFenceApplies(t *testing.T) {
	tool := godModeAskTool{name: "system.dangerous"}
	cfg := &ToolPolicyCfg{DefaultPolicy: "allow", IsCoreAgent: false, GodMode: false}

	_, policyMap := FilterToolsByPolicy([]Tool{tool}, "custom", cfg)
	if p := policyMap["system.dangerous"]; p != "ask" {
		t.Fatalf("control: admin-ask tool resolved to %q, want ask", p)
	}
}
