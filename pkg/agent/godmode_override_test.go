// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// withGodModeAvailable flips the process-level availability gate for the
// duration of a test and restores it afterwards.
func withGodModeAvailable(t *testing.T, v bool) {
	t.Helper()
	prev := godModeAvailable.Load()
	setGodModeAvailable(v)
	t.Cleanup(func() { setGodModeAvailable(prev) })
}

// TestGodModeActive_RequiresBothSwitchAndAvailability verifies the active
// predicate is the AND of the runtime switch (cfg.Sandbox.GodMode) and the
// process-level availability gate.
func TestGodModeActive_RequiresBothSwitchAndAvailability(t *testing.T) {
	on := &config.Config{}
	on.Sandbox.GodMode = true
	off := &config.Config{}

	withGodModeAvailable(t, false)
	if GodModeActive(on) {
		t.Fatal("switch on but unavailable: GodModeActive must be false")
	}

	withGodModeAvailable(t, true)
	if !GodModeActive(on) {
		t.Fatal("switch on and available: GodModeActive must be true")
	}
	if GodModeActive(off) {
		t.Fatal("switch off: GodModeActive must be false")
	}
	if GodModeActive(nil) {
		t.Fatal("nil config: GodModeActive must be false")
	}
}

// TestAgentToolsCfgToPolicy_GodMode_FloorsAndIsNonDestructive verifies that when
// god mode is active, the resolved ToolPolicyCfg has GodMode=true and the
// per-agent/global policy maps are left empty (the override is total). When god
// mode is OFF, the per-agent deny is preserved (non-destructive: the on-disk
// policy is honored exactly).
func TestAgentToolsCfgToPolicy_GodMode_FloorsAndIsNonDestructive(t *testing.T) {
	globalCfg := &config.Config{}
	globalCfg.Sandbox.GodMode = true
	globalCfg.Sandbox.ToolPolicies = map[string]string{"system.exec": "deny"}
	globalCfg.Sandbox.DefaultToolPolicy = "deny"

	agentCfg := &config.AgentToolsCfg{}
	agentCfg.Builtin.DefaultPolicy = "deny"
	agentCfg.Builtin.Policies = map[string]config.ToolPolicy{"fetch_url": "ask"}

	withGodModeAvailable(t, true)
	got := agentToolsCfgToPolicy(globalCfg, agentCfg)
	if !got.GodMode {
		t.Fatal("god mode active: ToolPolicyCfg.GodMode must be true")
	}
	if len(got.Policies) != 0 || len(got.GlobalPolicies) != 0 {
		t.Fatalf("god mode active: policy maps must be empty (override is total), got agent=%v global=%v",
			got.Policies, got.GlobalPolicies)
	}

	// Switch off and confirm the prior decisions are restored.
	globalCfg.Sandbox.GodMode = false
	got = agentToolsCfgToPolicy(globalCfg, agentCfg)
	if got.GodMode {
		t.Fatal("god mode off: ToolPolicyCfg.GodMode must be false")
	}
	if got.Policies["fetch_url"] != "ask" {
		t.Fatalf("god mode off: per-agent fetch_url policy must be restored to ask, got %q",
			got.Policies["fetch_url"])
	}
	if got.GlobalPolicies["system.exec"] != "deny" {
		t.Fatalf("god mode off: global system.exec policy must be restored to deny, got %q",
			got.GlobalPolicies["system.exec"])
	}
}
