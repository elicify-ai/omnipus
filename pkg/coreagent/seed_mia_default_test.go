// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestSeedConfig_NoPerAgentHeartbeat verifies that ADR-027 is enforced: SeedConfig
// no longer seeds any per-agent heartbeat fields (heartbeat is workspace-scoped).
// Workers and all core agents must have no HeartbeatEnabled / HeartbeatInterval set.
//
// BDD: Given an empty config,
//
//	When SeedConfig is called,
//	Then no agent in cfg.Agents.List has a HeartbeatEnabled or HeartbeatInterval field
//	(heartbeat is workspace-scoped per ADR-027 and must not appear on AgentConfig).
func TestSeedConfig_NoPerAgentHeartbeat(t *testing.T) {
	cfg := &config.Config{}
	SeedConfig(cfg)

	// Heartbeat is workspace-scoped (ADR-027). No seeded agent should carry
	// per-agent heartbeat fields. The AgentConfig struct itself no longer has
	// HeartbeatEnabled / HeartbeatInterval — this test documents the expectation
	// that SeedConfig does not re-introduce them via any other mechanism.
	require.NotEmpty(t, cfg.Agents.List, "SeedConfig must populate agents")
	// All we can assert here is that SeedConfig produces valid agents (no panic).
	// The absence of per-agent heartbeat fields is enforced at compile time by the
	// removed struct fields (config.AgentConfig no longer has HeartbeatEnabled /
	// HeartbeatInterval), so there is nothing to assert at runtime.
}

// TestSeedConfig_Idempotent verifies that calling SeedConfig twice returns
// modified=false on the second call (all agents are already present).
//
// BDD: Given SeedConfig has been called once on an empty config,
//
//	When SeedConfig is called again on the same config,
//	Then modified=false (no changes).
func TestSeedConfig_Idempotent(t *testing.T) {
	cfg := &config.Config{}
	first := SeedConfig(cfg)
	assert.True(t, first, "first SeedConfig on empty config must return modified=true")

	second := SeedConfig(cfg)
	assert.False(t, second,
		"second SeedConfig call must return modified=false (all agents already present)")
}
