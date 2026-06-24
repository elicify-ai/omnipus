//go:build !cgo

// Package agent — ToolPolicyCfg governance tests.
//
// Verifies that the central tool registry governance contract holds:
// when ToolPolicyCfg maps workspace_shell or workspace_shell_bg to "deny",
// FilterToolsByPolicy excludes those tools from the set presented to the LLM.
//
// Note on architecture: tools are always registered in ag.Tools regardless of
// policy — the registry is a storage layer. FilterToolsByPolicy is the
// governance gate applied at LLM-call assembly time (loop.go:3212). These
// tests verify that gate directly, not the storage layer.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-3 —
// "ToolPolicyCfg deny means tool not registered".

package agent

import (
	"runtime"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestToolPolicyCfg_DenyWorkspaceShell_ExcludedFromLLMView verifies that when
// an agent's ToolPolicyCfg maps workspace_shell to "deny", FilterToolsByPolicy
// removes it from the set of tools visible to the LLM.
//
// BDD: Given an agent with ToolPolicyCfg{"workspace_shell": "deny"},
//
//	When FilterToolsByPolicy is applied,
//	Then workspace_shell is absent from the filtered tool list.
func TestToolPolicyCfg_DenyWorkspaceShell_ExcludedFromLLMView(t *testing.T) {
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
					ID:   "deny-shell-agent",
					Name: "Deny Shell Agent",
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: boolPtr(true)},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	al.WireTier13Deps(Tier13Deps{}) // no registry — workspace_shell_bg not registered

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("deny-shell-agent")
	if !ok || ag == nil {
		t.Fatal("deny-shell-agent not found in registry")
	}

	// Confirm workspace_shell IS in the raw registry (governance is at filter time).
	if _, found := ag.Tools.Get("workspace_shell"); !found {
		t.Fatal("workspace_shell must be registered in ag.Tools — policy applies at filter time, not register time")
	}

	// Apply a deny policy for workspace_shell and verify FilterToolsByPolicy excludes it.
	policyCfg := &tools.ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"workspace_shell": "deny",
		},
	}

	allTools := ag.Tools.GetAll()
	filtered, _ := tools.FilterToolsByPolicy(allTools, "custom", policyCfg)

	for _, tool := range filtered {
		if tool.Name() == "workspace_shell" {
			t.Errorf("workspace_shell must be excluded from filtered tools when policy=deny; found in filtered list")
		}
	}
}

// TestToolPolicyCfg_DenyWorkspaceShellBg_ExcludedFromLLMView verifies the same
// governance contract for workspace_shell_bg.
//
// BDD: Given an agent with ToolPolicyCfg{"workspace_shell_bg": "deny"},
//
//	When FilterToolsByPolicy is applied,
//	Then workspace_shell_bg is absent from the filtered tool list.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-3.
func TestToolPolicyCfg_DenyWorkspaceShellBg_ExcludedFromLLMView(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("workspace_shell_bg only registered on Linux")
	}

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
					ID:   "deny-shellbg-agent",
					Name: "Deny Shell BG Agent",
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Experimental: config.ExperimentalConfig{WorkspaceShellEnabled: boolPtr(true)},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// Wire with a DevServerRegistry so workspace_shell_bg is registered.
	devReg := sandbox.NewDevServerRegistry()
	defer devReg.Close()

	al.WireTier13Deps(Tier13Deps{DevServerRegistry: devReg})

	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent("deny-shellbg-agent")
	if !ok || ag == nil {
		t.Fatal("deny-shellbg-agent not found in registry")
	}

	// Confirm workspace_shell_bg is in the raw registry.
	if _, found := ag.Tools.Get("workspace_shell_bg"); !found {
		t.Fatal("workspace_shell_bg must be registered when DevServerRegistry is wired")
	}

	// Apply deny policy for workspace_shell_bg.
	policyCfg := &tools.ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"workspace_shell_bg": "deny",
		},
	}

	allTools := ag.Tools.GetAll()
	filtered, _ := tools.FilterToolsByPolicy(allTools, "custom", policyCfg)

	for _, tool := range filtered {
		if tool.Name() == "workspace_shell_bg" {
			t.Errorf("workspace_shell_bg must be excluded from filtered tools when policy=deny; found in filtered list")
		}
	}
}
