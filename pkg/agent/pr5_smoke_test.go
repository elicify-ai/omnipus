package agent

// Smoke tests for PR 5 (quizzical-marinating-frog.md):
//   - workspace_shell_enabled defaults to false (nil pointer → off, deny-by-default per Wave 1 B3)
//   - workspace_shell and workspace_shell_bg are registered only when the flag is explicitly true
//   - Jim's seeded policy forces workspace_shell_enabled=true via SeedConfig (kernel sandbox is the guard)

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestPR5_WorkspaceShellNilDefaultsFalse verifies that when
// experimental.workspace_shell_enabled is nil (absent in config), workspace_shell
// is NOT registered for a generic agent (deny-by-default, Wave 1 B3).
// Jim is the sole exception — SeedConfig forces WorkspaceShellEnabled=true for Jim
// because the kernel sandbox is the protective layer (see TestPR5_JimSeedPolicyAppliedInLoop).
//
// BDD: Given a config where WorkspaceShellEnabled is nil (pointer is nil),
//
//	When WireTier13Deps is called on a non-Jim AgentLoop,
//	Then workspace_shell is NOT registered for the agent.
//
// Traces to: quizzical-marinating-frog.md Wave 1 B3 — deny-by-default for
// experimental.workspace_shell_enabled; Hard Constraint #6.
func TestPR5_WorkspaceShellNilDefaultsFalse(t *testing.T) {
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
					ID:   "test-agent",
					Name: "Test Agent",
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// nil → resolveBoolWithDefault(..., false) → workspace_shell not registered.
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: nil},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("test-agent")
	if !ok || ag == nil {
		t.Fatal("test-agent not found in registry")
	}

	if _, found := ag.Tools.Get("workspace_shell"); found {
		t.Error("workspace_shell must NOT be registered when WorkspaceShellEnabled is nil (deny-by-default)")
	}
}

// TestPR5_WorkspaceShellDisabledWhenFlagFalse verifies that setting
// WorkspaceShellEnabled=false explicitly disables tool registration.
//
// BDD: Given a config with WorkspaceShellEnabled=false,
//
//	When WireTier13Deps is called,
//	Then workspace_shell is NOT registered.
func TestPR5_WorkspaceShellDisabledWhenFlagFalse(t *testing.T) {
	tmpDir := t.TempDir()
	disabled := false

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{
					ID:   "test-agent",
					Name: "Test Agent",
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: &disabled},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("test-agent")
	if !ok || ag == nil {
		t.Fatal("test-agent not found in registry")
	}

	if _, found := ag.Tools.Get("workspace_shell"); found {
		t.Error("workspace_shell must NOT be registered when WorkspaceShellEnabled=false")
	}
}

// TestPR5_JimSeedPolicyAppliedInLoop verifies that when SeedConfig is called
// before WireTier13Deps, Jim's seeded tool policy (workspace_shell=allow,
// run_in_workspace=deny) is correctly wired into the agent loop.
//
// BDD: Given a config seeded with SeedConfig (giving Jim workspace+net profile),
//
//	When the AgentLoop is set up and WireTier13Deps is called,
//	Then Jim's registry entry has the workspace_shell tool registered
//	(the tool policy allow is confirmed by the seed, registry wiring is confirmed here).
//
// Traces to: quizzical-marinating-frog.md PR 5 — "Jim's seed allows workspace_shell".
func TestPR5_JimSeedPolicyAppliedInLoop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// nil here — SeedConfig will explicitly set WorkspaceShellEnabled=true for Jim.
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: nil},
		},
	}

	// Apply core agent seeds (adds Jim with workspace+net profile and correct policies).
	coreagent.SeedConfig(cfg)

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	jimAgent, ok := reg.GetAgent("jim")
	if !ok || jimAgent == nil {
		t.Fatal("jim agent not found in registry after SeedConfig")
	}

	// workspace_shell must be registered for Jim.
	rawTool, found := jimAgent.Tools.Get("workspace_shell")
	if !found {
		t.Fatal("workspace_shell must be registered for Jim after PR 5 seed")
	}
	_, isShellTool := rawTool.(*tools.WorkspaceShellTool)
	if !isShellTool {
		t.Fatalf("workspace_shell tool for Jim is not *WorkspaceShellTool; got %T", rawTool)
	}

	// Verify Jim's sandbox profile was applied to the shell tool.
	shellTool := rawTool.(*tools.WorkspaceShellTool)
	if shellTool.ProfileForTest() != config.SandboxProfileWorkspaceNet {
		t.Errorf("Jim's workspace_shell profile must be workspace+net, got %q",
			shellTool.ProfileForTest())
	}
}
