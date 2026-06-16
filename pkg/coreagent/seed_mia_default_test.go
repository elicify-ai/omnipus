// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestSeedConfig_MiaIsDefault verifies that SeedConfig marks Mia as Default=true
// on a fresh config, and that no other core agent is Default=true.
//
// BDD: Given an empty config (no agents),
//
//	When SeedConfig is called,
//	Then mia.Default == true,
//	And all other core agents have Default == false.
//
// Traces to: sprint/258-jun-2026 — "Seed Mia as default (#3)".
func TestSeedConfig_MiaIsDefault(t *testing.T) {
	cfg := &config.Config{}
	modified := SeedConfig(cfg)
	assert.True(t, modified, "SeedConfig on empty config must return modified=true")

	var mia *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDMia) {
			mia = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, mia, "SeedConfig must add Mia to cfg.Agents.List")
	assert.True(t, mia.Default, "Mia must be seeded with Default=true on fresh install")

	// All other core agents must not be default.
	for i := range cfg.Agents.List {
		a := cfg.Agents.List[i]
		if a.ID == string(IDMia) {
			continue
		}
		assert.False(t, a.Default, "agent %q must not be default (only Mia is)", a.ID)
	}
}

// TestSeedConfig_MiaDefaultNotOverriddenOnReEnforcement verifies that the
// re-enforcement pass (existing agent) does NOT touch the Default flag.
// If an operator has already cleared Mia's Default or set another agent as
// default, that choice must survive a call to SeedConfig.
//
// BDD: Given Mia already exists with Default=false (operator cleared it),
//
//	When SeedConfig is called (re-enforcement path),
//	Then mia.Default remains false (operator choice preserved).
func TestSeedConfig_MiaDefaultNotOverriddenOnReEnforcement(t *testing.T) {
	enabled := true
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:      string(IDMia),
					Name:    "Mia — Assistant",
					Locked:  true,
					Enabled: &enabled,
					Default: false, // operator explicitly cleared the default
				},
			},
		},
	}

	SeedConfig(cfg)

	var mia *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDMia) {
			mia = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, mia)
	assert.False(t, mia.Default,
		"re-enforcement must not override operator's Default=false on an already-present Mia")
}
