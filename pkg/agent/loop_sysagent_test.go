// Omnipus — Agent Loop System Tools Policy Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestCustomAgent_HasNoSystemToolsRegistered verifies that a custom agent's
// tool registry does not contain any system.* tools after agent-loop
// initialisation. Under the central tool registry redesign (FR-020) system.*
// tools are governed solely by per-agent policy; they are never auto-injected
// into custom agent registries.
//
// Traces to: central tool registry redesign spec — "ScopeSystem is retired;
// policy-only governance replaces WireSystemTools".
func TestCustomAgent_HasNoSystemToolsRegistered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sysagent-policy-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	// Add a custom agent.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "custom-bot", Name: "Custom Bot"},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// The custom agent must NOT have any system.* tools in its registry.
	customAgent, ok := al.GetRegistry().GetAgent("custom-bot")
	if !ok || customAgent == nil {
		t.Fatal("custom-bot agent not found in registry")
	}
	for _, name := range customAgent.Tools.List() {
		if strings.HasPrefix(name, "system.") {
			t.Errorf("custom-bot agent has unexpected system tool %q (policy gate violated)", name)
		}
	}
}
