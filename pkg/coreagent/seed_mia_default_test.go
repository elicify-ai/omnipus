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

// TestSeedConfig_MiaHeartbeatSeeded verifies that SeedConfig seeds Mia with a
// per-agent heartbeat enabled at the default interval on a fresh install, and that
// worker/specialist agents have no seeded heartbeat. This preserves the fresh-install
// behavior previously delivered by the legacy global HeartbeatConfig (removed).
//
// BDD: Given an empty config,
//
//	When SeedConfig is called,
//	Then mia.HeartbeatEnabled == &true and mia.HeartbeatInterval == DefaultHeartbeatIntervalMinutes,
//	And all worker/specialist agents have HeartbeatEnabled == nil.
func TestSeedConfig_MiaHeartbeatSeeded(t *testing.T) {
	cfg := &config.Config{}
	SeedConfig(cfg)

	var mia *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDMia) {
			mia = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, mia, "SeedConfig must add Mia")
	require.NotNil(t, mia.HeartbeatEnabled, "Mia must be seeded with HeartbeatEnabled != nil")
	assert.True(t, *mia.HeartbeatEnabled, "Mia must be seeded with HeartbeatEnabled=true")
	assert.Equal(t, config.DefaultHeartbeatIntervalMinutes, mia.HeartbeatInterval,
		"Mia must be seeded with HeartbeatInterval == DefaultHeartbeatIntervalMinutes")

	// Workers and specialists must never carry a seeded heartbeat.
	workerIDs := map[string]bool{
		string(IDWorker): true, string(IDPlanner): true,
		string(IDExplorer): true, string(IDResearcher): true,
	}
	for i := range cfg.Agents.List {
		a := cfg.Agents.List[i]
		if !workerIDs[a.ID] {
			continue
		}
		assert.Nil(t, a.HeartbeatEnabled,
			"worker/specialist %q must have HeartbeatEnabled==nil (no heartbeat)", a.ID)
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
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:      string(IDMia),
					Name:    "Mia — Assistant",
					Locked:  true,
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

// TestSeedConfig_MiaHeartbeatUpgradeMigration verifies the one-time
// global→per-agent heartbeat upgrade migration (replacement for the deleted
// migrateGlobalHeartbeatToAgent). An existing Mia with HeartbeatEnabled==nil
// (pre-migration state from any release that had the global heartbeat)
// must get HeartbeatEnabled=true and HeartbeatInterval=30 after SeedConfig.
//
// BDD scenarios:
//
//  1. Given Mia exists with HeartbeatEnabled==nil (pre-upgrade state),
//     When SeedConfig is called,
//     Then mia.HeartbeatEnabled=true and mia.HeartbeatInterval=DefaultHeartbeatIntervalMinutes,
//     And modified=true.
//
//  2. Given the same config is passed to SeedConfig a second time,
//     When SeedConfig is called again,
//     Then modified=false (idempotent — the value is now non-nil, no re-write).
//
//  3. Given Mia exists with HeartbeatEnabled=&false (operator explicitly disabled),
//     When SeedConfig is called,
//     Then mia.HeartbeatEnabled remains false (operator choice preserved).
//
//  4. Given a worker/specialist agent with HeartbeatEnabled==nil,
//     When SeedConfig is called,
//     Then the worker's HeartbeatEnabled remains nil (migration never touches workers).
//
// Traces to: feat/0.1.0-uat-fixes — global heartbeat removal, per-agent migration.
func TestSeedConfig_MiaHeartbeatUpgradeMigration(t *testing.T) {
	t.Run("nil HeartbeatEnabled is migrated to true", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:     string(IDMia),
						Name:   "Mia — Assistant",
						Locked: true,
						// HeartbeatEnabled intentionally nil: simulates an existing
						// install that relied on the now-removed global heartbeat.
					},
				},
			},
		}

		modified := SeedConfig(cfg)
		assert.True(t, modified, "SeedConfig must return modified=true when migration fires")

		var mia *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(IDMia) {
				mia = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, mia)
		require.NotNil(t, mia.HeartbeatEnabled, "HeartbeatEnabled must be non-nil after migration")
		assert.True(t, *mia.HeartbeatEnabled, "HeartbeatEnabled must be true after migration")
		assert.Equal(t, config.DefaultHeartbeatIntervalMinutes, mia.HeartbeatInterval,
			"HeartbeatInterval must be set to DefaultHeartbeatIntervalMinutes when previously zero")
	})

	t.Run("migration does not re-fire once HeartbeatEnabled is non-nil", func(t *testing.T) {
		// Seed a full config so all core agents already exist; the second call
		// should make no changes at all (neither the migration block nor new-agent
		// additions), confirming the nil-guard is truly idempotent.
		cfg := &config.Config{}
		SeedConfig(cfg) // first call: seeds everything including the migration

		// Capture Mia's HeartbeatEnabled pointer value before the second call.
		var miaBefore *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(IDMia) {
				miaBefore = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, miaBefore)
		require.NotNil(t, miaBefore.HeartbeatEnabled,
			"HeartbeatEnabled must be non-nil after first SeedConfig call")
		assert.True(t, *miaBefore.HeartbeatEnabled)

		// Second call: migration guard (HeartbeatEnabled != nil) must prevent
		// any re-write; all agents already present means modified=false.
		modified2 := SeedConfig(cfg)
		assert.False(t, modified2,
			"SeedConfig must return modified=false on second call (all agents present, migration guard active)")

		var mia *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(IDMia) {
				mia = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, mia)
		require.NotNil(t, mia.HeartbeatEnabled)
		assert.True(t, *mia.HeartbeatEnabled,
			"HeartbeatEnabled must still be true after second SeedConfig call")
	})

	t.Run("explicit false is never overwritten", func(t *testing.T) {
		disabled := false
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:               string(IDMia),
						Name:             "Mia — Assistant",
						Locked:           true,
						HeartbeatEnabled: &disabled, // operator explicitly disabled
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
		require.NotNil(t, mia.HeartbeatEnabled, "HeartbeatEnabled must remain non-nil")
		assert.False(t, *mia.HeartbeatEnabled,
			"operator's explicit HeartbeatEnabled=false must survive SeedConfig migration")
	})

	t.Run("workers and specialists are never migrated", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{ID: string(IDWorker), Name: "Worker", Locked: true},
					{ID: string(IDPlanner), Name: "Planner", Locked: true},
					{ID: string(IDExplorer), Name: "Explorer", Locked: true},
					{ID: string(IDResearcher), Name: "Researcher", Locked: true},
				},
			},
		}

		SeedConfig(cfg)

		workerIDs := map[string]bool{
			string(IDWorker):     true,
			string(IDPlanner):    true,
			string(IDExplorer):   true,
			string(IDResearcher): true,
		}
		for i := range cfg.Agents.List {
			a := cfg.Agents.List[i]
			if !workerIDs[a.ID] {
				continue
			}
			assert.Nil(t, a.HeartbeatEnabled,
				"worker/specialist %q must have HeartbeatEnabled==nil (migration never touches them)", a.ID)
		}
	})
}
