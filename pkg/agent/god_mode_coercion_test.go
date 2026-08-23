// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// White-box tests for the O14 global god-mode wiring inside
// wireExecToolDepsOn (pkg/agent/loop.go). Per
// docs/internal/architecture/ADR-035-remove-per-agent-sandbox-profile.md, the
// per-agent kernel-profile indirection that used to sit in front of this
// (workspace / workspace+net / host / off, with a runtime "off coerced to
// workspace when god mode is unavailable" fallback) has been removed
// entirely. ADR-036 then merged exec/workspace_shell/workspace_shell_bg into
// one universally-registered `bash` tool, which now runs under a single
// fixed sandbox boundary (see sandbox.ResolveLimits/BuildLimits) for every
// agent, with exactly one escape hatch: the global god-mode runtime switch
// (agent.GodModeActive(cfg)), which is threaded straight into bash's GodMode
// field via wireExecToolDepsOn.
//
// This file is the safety-critical coverage for that wiring — it must keep
// proving both invariants regardless of how the test names/shapes evolve:
//  1. When GodModeActive(cfg) is true, bash is constructed with GodMode=true
//     (Execute skips ApplyChildHardening/sandbox.Run entirely).
//  2. When it is false, bash is constructed with GodMode=false (Execute
//     always applies the fixed Limits + hardening).
//
// Uses the GodModeForTest() test accessor on ExecTool (the bash tool's Go
// type — see pkg/tools/shell.go's package doc for the naming rationale).
//
// Traces to: quizzical-marinating-frog.md PR 4 acceptance criteria — coercion
// (superseded by ADR-035's direct god-mode wiring, then ADR-036's tool merge).

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// buildLoopWithShellAgent constructs a minimal AgentLoop whose config contains
// a single agent ("shell-agent"). The caller sets al.SetAllowGodMode and
// cfg.Sandbox.GodMode before calling WireTier13Deps to control
// GodModeActive's resolution. bash is registered universally (ADR-036) —
// there is no more experimental.workspace_shell_enabled flag to set.
func buildLoopWithShellAgent(t *testing.T) (*AgentLoop, *config.Config) {
	t.Helper()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "shell-agent", Name: "Shell Agent"},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	return al, cfg
}

// shellGodModeForAgent rewires Tier13 deps (which re-runs wireExecToolDeps)
// and returns the resolved GodMode flag for the given agent's bash tool — the
// live wiring result after agent.GodModeActive is resolved at
// tool-construction time.
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
	rawTool, found := ag.Tools.Get("bash")
	if !found {
		t.Fatal("bash tool not registered")
	}
	shellTool, ok := rawTool.(*tools.ExecTool)
	if !ok {
		t.Fatalf("bash is not *ExecTool; got %T", rawTool)
	}
	return shellTool.GodModeForTest()
}

// TestGodMode_Inactive_ShellToolGodModeFalse verifies invariant (2): with no
// god-mode switch configured (the default), GodModeActive(cfg) is false and
// bash is wired with GodMode=false.
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
// bash is wired with GodMode=true.
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

// TestGodMode_GlobalSwitch_ForcesGodModeTrue_AndReverts proves the O14
// override end-to-end: when the global god-mode switch (sandbox.god_mode) is
// active AND available, every agent's bash tool is wired with GodMode=true
// regardless of anything else in its config. The override is non-destructive:
// clearing sandbox.god_mode and re-wiring restores GodMode=false — nothing is
// mutated on disk.
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
