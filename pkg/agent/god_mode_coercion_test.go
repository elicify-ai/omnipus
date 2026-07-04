// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// White-box tests for the O14 global god-mode wiring inside
// wireTier13DepsLocked (pkg/agent/loop.go). Per
// docs/internal/architecture/ADR-035-remove-per-agent-sandbox-profile.md, the
// per-agent kernel-profile indirection that used to sit in front of this
// (workspace / workspace+net / host / off, with a runtime "off coerced to
// workspace when god mode is unavailable" fallback) has been removed
// entirely. workspace_shell and workspace_shell_bg now run under a single
// fixed sandbox boundary (see sandbox.BuildLimits) for every agent, with
// exactly one escape hatch: the global god-mode runtime switch
// (agent.GodModeActive(cfg)), which is threaded straight into each tool's
// GodMode field.
//
// This file is the safety-critical coverage for that wiring — it must keep
// proving both invariants regardless of how the test names/shapes evolve:
//  1. When GodModeActive(cfg) is true, workspace_shell / workspace_shell_bg
//     are constructed with GodMode=true (Execute skips ApplyChildHardening
//     entirely — see the dedicated behavioral tests in
//     pkg/tools/workspace_shell_test.go / workspace_shell_bg_test.go).
//  2. When it is false, they are constructed with GodMode=false (Execute
//     always applies the fixed Limits + hardening).
//
// Uses GodModeForTest() test accessors on WorkspaceShellTool / WorkspaceShellBgTool.
//
// Traces to: quizzical-marinating-frog.md PR 4 acceptance criteria — coercion
// (superseded by ADR-035's direct god-mode wiring).

import (
	"runtime"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// buildLoopWithShellAgent constructs a minimal AgentLoop whose config contains
// a single agent ("shell-agent") with workspace_shell_enabled=true. The
// caller sets al.SetAllowGodMode and cfg.Sandbox.GodMode before calling
// WireTier13Deps to control GodModeActive's resolution.
func buildLoopWithShellAgent(t *testing.T) (*AgentLoop, *config.Config) {
	t.Helper()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "shell-agent", Name: "Shell Agent"},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: boolPtr(true)},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	return al, cfg
}

// shellGodModeForAgent rewires Tier13 deps and returns the resolved GodMode
// flag for the given agent's workspace_shell tool — the live wiring result
// after agent.GodModeActive is resolved at tool-construction time.
func shellGodModeForAgent(t *testing.T, al *AgentLoop, agentID string) bool {
	t.Helper()
	al.WireTier13Deps(Tier13Deps{})
	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent(agentID)
	if !ok || ag == nil {
		t.Fatalf("%s not found in registry", agentID)
	}
	rawTool, found := ag.Tools.Get("workspace_shell")
	if !found {
		t.Fatal("workspace_shell tool not registered")
	}
	shellTool, ok := rawTool.(*tools.WorkspaceShellTool)
	if !ok {
		t.Fatalf("workspace_shell is not *WorkspaceShellTool; got %T", rawTool)
	}
	return shellTool.GodModeForTest()
}

// TestGodMode_Inactive_ShellToolGodModeFalse verifies invariant (2): with no
// god-mode switch configured (the default), GodModeActive(cfg) is false and
// workspace_shell is wired with GodMode=false.
func TestGodMode_Inactive_ShellToolGodModeFalse(t *testing.T) {
	al, _ := buildLoopWithShellAgent(t)
	al.SetAllowGodMode(false)

	if got := shellGodModeForAgent(t, al, "shell-agent"); got {
		t.Errorf("expected GodMode=false when sandbox.god_mode is unset, got true")
	}
}

// TestGodMode_Active_ShellToolGodModeTrue verifies invariant (1): when the
// global god-mode switch is on AND available (allowGodMode=true,
// sandbox.GodModeAvailable=true at build time), GodModeActive(cfg) is true and
// workspace_shell is wired with GodMode=true.
func TestGodMode_Active_ShellToolGodModeTrue(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test requires GodModeAvailable=true (default build)")
	}
	al, cfg := buildLoopWithShellAgent(t)
	cfg.Sandbox.GodMode = true
	al.SetAllowGodMode(true)

	if got := shellGodModeForAgent(t, al, "shell-agent"); !got {
		t.Errorf("expected GodMode=true when sandbox.god_mode is active and available, got false")
	}
}

// TestGodMode_ShellBg_Inactive_GodModeFalse mirrors
// TestGodMode_Inactive_ShellToolGodModeFalse for workspace_shell_bg.
func TestGodMode_ShellBg_Inactive_GodModeFalse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("workspace_shell_bg only registered on Linux")
	}
	al, _ := buildLoopWithShellAgent(t)
	al.SetAllowGodMode(false)

	devReg := sandbox.NewDevServerRegistry()
	defer devReg.Close()
	al.WireTier13Deps(Tier13Deps{DevServerRegistry: devReg})

	reg := al.GetRegistry()
	ag, ok := reg.GetAgent("shell-agent")
	if !ok || ag == nil {
		t.Fatal("shell-agent not found in registry")
	}
	rawTool, found := ag.Tools.Get("workspace_shell_bg")
	if !found {
		t.Fatal("workspace_shell_bg tool not registered")
	}
	bgTool, ok := rawTool.(*tools.WorkspaceShellBgTool)
	if !ok {
		t.Fatalf("workspace_shell_bg is not *WorkspaceShellBgTool; got %T", rawTool)
	}
	if bgTool.GodModeForTest() {
		t.Errorf("expected GodMode=false for shell_bg when sandbox.god_mode is unset, got true")
	}
}

// TestGodMode_ShellBg_Active_GodModeTrue mirrors
// TestGodMode_Active_ShellToolGodModeTrue for workspace_shell_bg.
func TestGodMode_ShellBg_Active_GodModeTrue(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test requires GodModeAvailable=true (default build)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("workspace_shell_bg only registered on Linux")
	}
	al, cfg := buildLoopWithShellAgent(t)
	cfg.Sandbox.GodMode = true
	al.SetAllowGodMode(true)

	devReg := sandbox.NewDevServerRegistry()
	defer devReg.Close()
	al.WireTier13Deps(Tier13Deps{DevServerRegistry: devReg})

	reg := al.GetRegistry()
	ag, ok := reg.GetAgent("shell-agent")
	if !ok || ag == nil {
		t.Fatal("shell-agent not found in registry")
	}
	rawTool, found := ag.Tools.Get("workspace_shell_bg")
	if !found {
		t.Fatal("workspace_shell_bg tool not registered")
	}
	bgTool, ok := rawTool.(*tools.WorkspaceShellBgTool)
	if !ok {
		t.Fatalf("workspace_shell_bg is not *WorkspaceShellBgTool; got %T", rawTool)
	}
	if !bgTool.GodModeForTest() {
		t.Errorf("expected GodMode=true for shell_bg when sandbox.god_mode is active and available, got false")
	}
}

// TestGodMode_GlobalSwitch_ForcesGodModeTrue_AndReverts proves the O14
// override end-to-end: when the global god-mode switch (sandbox.god_mode) is
// active AND available, every agent's workspace_shell tool is wired with
// GodMode=true regardless of anything else in its config. The override is
// non-destructive: clearing sandbox.god_mode and re-wiring restores
// GodMode=false — nothing is mutated on disk.
func TestGodMode_GlobalSwitch_ForcesGodModeTrue_AndReverts(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: requires GodModeAvailable=true (default build)")
	}
	al, cfg := buildLoopWithShellAgent(t)
	cfg.Sandbox.GodMode = true // global switch ON
	al.SetAllowGodMode(true)   // availability granted

	// God mode active → GodMode=true regardless of the agent's own config.
	if got := shellGodModeForAgent(t, al, "shell-agent"); !got {
		t.Fatalf("god mode active: expected GodMode=true, got false")
	}

	// Switch the global god-mode OFF and re-wire — GodMode must revert to
	// false (the override is non-destructive; nothing was mutated on disk).
	cfg.Sandbox.GodMode = false
	if got := shellGodModeForAgent(t, al, "shell-agent"); got {
		t.Fatalf("god mode off: expected GodMode=false restored, got true")
	}
}

// TestGodMode_GlobalSwitch_Unavailable_NoForce proves the override is inert
// when availability is not granted (allowGodMode=false): GodMode stays false
// even with sandbox.god_mode=true (fail-closed — the switch is a no-op
// without the boot-time authorization).
func TestGodMode_GlobalSwitch_Unavailable_NoForce(t *testing.T) {
	al, cfg := buildLoopWithShellAgent(t)
	cfg.Sandbox.GodMode = true // switch on, but...
	al.SetAllowGodMode(false)  // ...availability NOT granted

	if got := shellGodModeForAgent(t, al, "shell-agent"); got {
		t.Fatalf("unavailable: expected GodMode=false (switch inert), got true")
	}
}
