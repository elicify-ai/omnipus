package agent

// White-box tests for sandbox_profile=off coercion inside wireTier13DepsLocked.
//
// Verifies that when GodModeAvailable=false OR allowGodMode=false, an agent
// configured with sandbox_profile=off has its profile silently coerced to
// workspace before the workspace.shell / workspace.shell_bg tools are registered.
//
// Uses ProfileForTest() test accessors on WorkspaceShellTool / WorkspaceShellBgTool.
//
// Traces to: quizzical-marinating-frog.md PR 4 acceptance criteria — coercion.

import (
	"runtime"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// buildLoopWithOffProfile constructs a minimal AgentLoop whose config contains
// a single agent ("shell-agent") with sandbox_profile=off and workspace_shell_enabled=true.
// The caller sets allowGodMode before calling WireTier13Deps.
func buildLoopWithOffProfile(t *testing.T) (*AgentLoop, string) {
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
				{
					ID:             "shell-agent",
					Name:           "Shell Agent",
					SandboxProfile: config.SandboxProfileOff,
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: boolPtr(true)},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	return al, tmpDir
}

// TestGodModeCoercion_AllowGodModeFalse_CoercesToWorkspace verifies that
// when allowGodMode=false and sandbox.GodModeAvailable=true (default build),
// a sandbox_profile=off agent has its profile coerced to workspace before
// the workspace.shell tool is registered.
func TestGodModeCoercion_AllowGodModeFalse_CoercesToWorkspace(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test targets the GodModeAvailable=true (default) build path")
	}

	al, _ := buildLoopWithOffProfile(t)
	al.SetAllowGodMode(false)

	al.WireTier13Deps(Tier13Deps{}) // no registry/proxy needed for this test path

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	agent, ok := reg.GetAgent("shell-agent")
	if !ok || agent == nil {
		t.Fatal("shell-agent not found in registry")
	}

	rawTool, found := agent.Tools.Get("workspace.shell")
	if !found {
		t.Fatal("workspace.shell tool not registered")
	}
	shellTool, ok := rawTool.(*tools.WorkspaceShellTool)
	if !ok {
		t.Fatalf("workspace.shell is not *WorkspaceShellTool; got %T", rawTool)
	}

	if shellTool.ProfileForTest() == config.SandboxProfileOff {
		t.Errorf("expected sandbox_profile=off to be coerced to workspace, but got off")
	}
	if shellTool.ProfileForTest() != config.SandboxProfileWorkspace {
		t.Errorf("expected coerced profile=workspace, got %q", shellTool.ProfileForTest())
	}
}

// TestGodModeCoercion_AllowGodModeTrue_PreservesOff verifies that when both
// sandbox.GodModeAvailable=true AND allowGodMode=true, a sandbox_profile=off
// agent's profile is preserved as off.
func TestGodModeCoercion_AllowGodModeTrue_PreservesOff(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test requires GodModeAvailable=true (default build)")
	}

	al, _ := buildLoopWithOffProfile(t)
	al.SetAllowGodMode(true)

	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	agent, ok := reg.GetAgent("shell-agent")
	if !ok || agent == nil {
		t.Fatal("shell-agent not found in registry")
	}

	rawTool, found := agent.Tools.Get("workspace.shell")
	if !found {
		t.Fatal("workspace.shell tool not registered")
	}
	shellTool, ok := rawTool.(*tools.WorkspaceShellTool)
	if !ok {
		t.Fatalf("workspace.shell is not *WorkspaceShellTool; got %T", rawTool)
	}

	if shellTool.ProfileForTest() != config.SandboxProfileOff {
		t.Errorf("expected profile=off to be preserved, got %q", shellTool.ProfileForTest())
	}
}

// TestGodModeCoercion_ShellBg_AllowGodModeFalse_CoercesToWorkspace verifies
// that workspace.shell_bg has the same profile coercion as workspace.shell when
// allowGodMode=false and sandbox_profile=off.
//
// BDD: Given allowGodMode=false and sandbox_profile=off,
//
//	When WireTier13Deps is called with a DevServerRegistry,
//	Then workspace.shell_bg.ProfileForTest() == workspace (coerced, not off).
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-7.
func TestGodModeCoercion_ShellBg_AllowGodModeFalse_CoercesToWorkspace(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test targets the GodModeAvailable=true (default) build path")
	}
	if runtime.GOOS != "linux" {
		t.Skip("workspace.shell_bg only registered on Linux")
	}

	al, _ := buildLoopWithOffProfile(t)
	al.SetAllowGodMode(false)

	devReg := sandbox.NewDevServerRegistry()
	defer devReg.Close()

	al.WireTier13Deps(Tier13Deps{DevServerRegistry: devReg})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	agent, ok := reg.GetAgent("shell-agent")
	if !ok || agent == nil {
		t.Fatal("shell-agent not found in registry")
	}

	rawTool, found := agent.Tools.Get("workspace.shell_bg")
	if !found {
		t.Fatal("workspace.shell_bg tool not registered")
	}
	bgTool, ok := rawTool.(*tools.WorkspaceShellBgTool)
	if !ok {
		t.Fatalf("workspace.shell_bg is not *WorkspaceShellBgTool; got %T", rawTool)
	}

	if bgTool.ProfileForTest() == config.SandboxProfileOff {
		t.Errorf("expected sandbox_profile=off to be coerced to workspace for shell_bg, but got off")
	}
	if bgTool.ProfileForTest() != config.SandboxProfileWorkspace {
		t.Errorf("expected coerced profile=workspace for shell_bg, got %q", bgTool.ProfileForTest())
	}
}

// TestGodModeCoercion_ShellBg_AllowGodModeTrue_PreservesOff verifies that
// workspace.shell_bg's profile is preserved as off when both
// GodModeAvailable=true AND allowGodMode=true.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-7.
func TestGodModeCoercion_ShellBg_AllowGodModeTrue_PreservesOff(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: test requires GodModeAvailable=true (default build)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("workspace.shell_bg only registered on Linux")
	}

	al, _ := buildLoopWithOffProfile(t)
	al.SetAllowGodMode(true)

	devReg := sandbox.NewDevServerRegistry()
	defer devReg.Close()

	al.WireTier13Deps(Tier13Deps{DevServerRegistry: devReg})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	agent, ok := reg.GetAgent("shell-agent")
	if !ok || agent == nil {
		t.Fatal("shell-agent not found in registry")
	}

	rawTool, found := agent.Tools.Get("workspace.shell_bg")
	if !found {
		t.Fatal("workspace.shell_bg tool not registered")
	}
	bgTool, ok := rawTool.(*tools.WorkspaceShellBgTool)
	if !ok {
		t.Fatalf("workspace.shell_bg is not *WorkspaceShellBgTool; got %T", rawTool)
	}

	if bgTool.ProfileForTest() != config.SandboxProfileOff {
		t.Errorf("expected profile=off to be preserved for shell_bg, got %q", bgTool.ProfileForTest())
	}
}

// TestGodModeCoercion_ProfileWorkspace_NeverCoerced verifies that a
// sandbox_profile=workspace agent is never affected by the coercion logic.
func TestGodModeCoercion_ProfileWorkspace_NeverCoerced(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:             "ws-agent",
					Name:           "WS Agent",
					SandboxProfile: config.SandboxProfileWorkspace,
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: boolPtr(true)},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.SetAllowGodMode(false) // irrelevant for non-off profile

	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	agent, ok := reg.GetAgent("ws-agent")
	if !ok || agent == nil {
		t.Fatal("ws-agent not found in registry")
	}

	rawTool, found := agent.Tools.Get("workspace.shell")
	if !found {
		t.Fatal("workspace.shell tool not registered")
	}
	shellTool, ok := rawTool.(*tools.WorkspaceShellTool)
	if !ok {
		t.Fatalf("workspace.shell is not *WorkspaceShellTool; got %T", rawTool)
	}

	if shellTool.ProfileForTest() != config.SandboxProfileWorkspace {
		t.Errorf("expected profile=workspace to be preserved, got %q", shellTool.ProfileForTest())
	}
}
