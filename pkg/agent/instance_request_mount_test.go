// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestNewAgentInstance_RegistersRequestMount pins the leg that was missing.
//
// request_mount reached the metadata catalog (GeneralBuiltinMetadata) and the
// seeded tool policies, so it showed up in /api/v1/tools and in Settings and
// looked entirely present. It was never registered on any agent's execution
// registry, which meant two things at once: no agent could call it, and it
// could not be granted or revoked per agent, because GET /agents/{id}/tools
// lists that registry. Both surfaces reported a tool that did not exist where
// it mattered.
//
// The catalog cannot catch this — it is the surface that looked fine. Only an
// assertion against a REAL instance registry can, which is what this is.
func TestNewAgentInstance_RegistersRequestMount(t *testing.T) {
	outer, err := os.MkdirTemp("", "agent-request-mount-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(outer)
	home := filepath.Join(outer, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         256,
				MaxToolIterations: 3,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})

	for _, tool := range agent.Tools.GetAll() {
		if tool.Name() == "request_mount" {
			return
		}
	}
	t.Fatal("request_mount is absent from the agent's execution registry: " +
		"no agent can call it and it cannot be configured per agent, " +
		"however complete it looks in the catalog and in Settings")
}
