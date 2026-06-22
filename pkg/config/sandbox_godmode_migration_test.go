// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestMigrateAgentSandboxOff_OffToHost verifies O13: a per-agent
// sandbox_profile=off is migrated to "host" at load time, since per-agent "off"
// is retired ("no sandbox" is reachable only via the global god-mode switch).
func TestMigrateAgentSandboxOff_OffToHost(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "a", SandboxProfile: SandboxProfileOff},
				{ID: "b", SandboxProfile: SandboxProfileWorkspace},
				{ID: "c", SandboxProfile: SandboxProfileHost},
				{ID: "d"}, // empty — inherit default
			},
		},
	}
	migrateAgentSandboxOff(cfg)

	if got := cfg.Agents.List[0].SandboxProfile; got != SandboxProfileHost {
		t.Fatalf("agent a: off must migrate to host, got %q", got)
	}
	if got := cfg.Agents.List[1].SandboxProfile; got != SandboxProfileWorkspace {
		t.Fatalf("agent b: workspace must be untouched, got %q", got)
	}
	if got := cfg.Agents.List[2].SandboxProfile; got != SandboxProfileHost {
		t.Fatalf("agent c: host must be untouched, got %q", got)
	}
	if got := cfg.Agents.List[3].SandboxProfile; got != "" {
		t.Fatalf("agent d: empty must be untouched, got %q", got)
	}
}

// TestMigrateAgentSandboxOff_Idempotent verifies a second pass is a no-op.
func TestMigrateAgentSandboxOff_Idempotent(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "a", SandboxProfile: SandboxProfileOff}},
		},
	}
	migrateAgentSandboxOff(cfg)
	migrateAgentSandboxOff(cfg)
	if got := cfg.Agents.List[0].SandboxProfile; got != SandboxProfileHost {
		t.Fatalf("idempotent: want host, got %q", got)
	}
}
