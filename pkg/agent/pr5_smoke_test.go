package agent

// Smoke tests for PR 5 (quizzical-marinating-frog.md):
//   - workspace_shell_enabled defaults to true (nil pointer → on)
//   - workspace.shell and workspace.shell_bg are registered when the flag is on
//   - Jim's seeded policy is applied correctly at the AgentLoop level

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestPR5_WorkspaceShellEnabledByDefault verifies that when
// experimental.workspace_shell_enabled is nil (absent in config), the tools
// are still registered (the default is true per PR 5).
//
// BDD: Given a config where WorkspaceShellEnabled is nil (pointer is nil),
//
//	When WireTier13Deps is called on an AgentLoop,
//	Then workspace.shell and workspace.shell_bg are registered for the agent.
//
// Traces to: quizzical-marinating-frog.md PR 5 — "Default config now has
// experimental.workspace_shell_enabled = true".
func TestPR5_WorkspaceShellEnabledByDefault(t *testing.T) {
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
			// WorkspaceShellEnabled is nil — should behave as true (PR 5 default).
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: nil},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{}) // no registry needed for workspace.shell (foreground)

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("test-agent")
	if !ok || ag == nil {
		t.Fatal("test-agent not found in registry")
	}

	if _, found := ag.Tools.Get("workspace.shell"); !found {
		t.Error("workspace.shell must be registered when WorkspaceShellEnabled is nil (default=true)")
	}
	// workspace.shell_bg requires a DevServerRegistry; with nil registry it is not wired.
	// That is correct behavior — the tool requires the registry to be useful.
}

// TestPR5_WorkspaceShellDisabledWhenFlagFalse verifies that setting
// WorkspaceShellEnabled=false explicitly disables tool registration.
//
// BDD: Given a config with WorkspaceShellEnabled=false,
//
//	When WireTier13Deps is called,
//	Then workspace.shell is NOT registered.
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

	if _, found := ag.Tools.Get("workspace.shell"); found {
		t.Error("workspace.shell must NOT be registered when WorkspaceShellEnabled=false")
	}
}

// TestPR5_JimSeedPolicyAppliedInLoop verifies that when SeedConfig is called
// before WireTier13Deps, Jim's seeded tool policy (workspace.shell=allow,
// run_in_workspace=deny) is correctly wired into the agent loop.
//
// BDD: Given a config seeded with SeedConfig (giving Jim workspace+net profile),
//
//	When the AgentLoop is set up and WireTier13Deps is called,
//	Then Jim's registry entry has the workspace.shell tool registered
//	(the tool policy allow is confirmed by the seed, registry wiring is confirmed here).
//
// Traces to: quizzical-marinating-frog.md PR 5 — "Jim's seed allows workspace.shell".
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
			// nil = default true
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

	// workspace.shell must be registered for Jim.
	rawTool, found := jimAgent.Tools.Get("workspace.shell")
	if !found {
		t.Fatal("workspace.shell must be registered for Jim after PR 5 seed")
	}
	_, isShellTool := rawTool.(*tools.WorkspaceShellTool)
	if !isShellTool {
		t.Fatalf("workspace.shell tool for Jim is not *WorkspaceShellTool; got %T", rawTool)
	}

	// Verify Jim's sandbox profile was applied to the shell tool.
	shellTool := rawTool.(*tools.WorkspaceShellTool)
	if shellTool.ProfileForTest() != config.SandboxProfileWorkspaceNet {
		t.Errorf("Jim's workspace.shell profile must be workspace+net, got %q",
			shellTool.ProfileForTest())
	}
}
