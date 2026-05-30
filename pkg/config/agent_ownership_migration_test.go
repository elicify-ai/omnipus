//go:build !cgo

// agent_ownership_migration_test.go — migration tests ( / / ).
//
// Covers migrateAgentOwnership scenarios from the spec dataset.
// BDD test IDs: #77b, #78, #78b
// Traces to: path-sandbox-and-capability-tiers-spec.md /

package config

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// slog record capture for WARN assertions
// ---------------------------------------------------------------------------

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// captureSlogHandler installs a capturing slog handler and returns it.
// It restores the previous default logger in t.Cleanup.
func captureSlogHandler(t *testing.T) *captureHandler {
	t.Helper()
	h := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func containsMessage(records []slog.Record, substr string) bool {
	for _, r := range records {
		if strings.Contains(r.Message, substr) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// buildMigrationFixture constructs a *Config for migration tests.
// - admins: list of (username, role) pairs
// - agents: list of AgentConfig to add
//
// Returns (cfg, tmpFilePath) where the config.json file has been written.
// ---------------------------------------------------------------------------

func buildMigrationFixture(t *testing.T, admins []UserConfig, agents []AgentConfig) (*Config, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	enabled := true
	for i := range agents {
		if agents[i].Enabled == nil {
			agents[i].Enabled = &enabled
		}
	}

	cfg := &Config{
		Version: CurrentVersion,
		Gateway: GatewayConfig{
			Users: admins,
		},
		Agents: AgentsConfig{
			List: agents,
		},
	}

	// Write initial config so SaveConfig can persist mutations.
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, data, 0o600))

	return cfg, cfgPath
}

// ---------------------------------------------------------------------------
// Dataset Row 1: TestMigration_AssignsAlphabeticallyFirstAdmin
// BDD: Given a custom agent with no OwnerUsername and two admins alice and bob,
// When migrateAgentOwnership runs,
// Then the agent's OwnerUsername is set to "alice" (alphabetically first).
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 1
// ---------------------------------------------------------------------------

func TestMigration_AssignsAlphabeticallyFirstAdmin(t *testing.T) {
	admins := []UserConfig{
		{Username: "bob", Role: UserRoleAdmin},
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "agent-orphan", Name: "Orphan", Type: AgentTypeCustom},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	require.Len(t, cfg.Agents.List, 1)
	assert.Equal(t, "alice", cfg.Agents.List[0].OwnerUsername,
		"alphabetically-first admin (alice) must be assigned as owner")
}

// ---------------------------------------------------------------------------
// Dataset Row 2: TestMigration_PreservesExistingOwner
// BDD: Given a custom agent that already has OwnerUsername="alice",
// When migrateAgentOwnership runs,
// Then the owner is unchanged.
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 2
// ---------------------------------------------------------------------------

func TestMigration_PreservesExistingOwner(t *testing.T) {
	h := captureSlogHandler(t)

	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "agent-owned", Name: "Owned", Type: AgentTypeCustom, OwnerUsername: "alice"},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	// Owner must remain alice.
	assert.Equal(t, "alice", cfg.Agents.List[0].OwnerUsername,
		"existing owner must be preserved")

	// No WARN log should be emitted for already-owned agents.
	assert.False(t, containsMessage(h.records, "assigning owner"),
		"no migration WARN should be emitted for agents that already have an owner")
}

// ---------------------------------------------------------------------------
// Dataset Row 3: TestMigration_NoAdminLeavesEmpty
// BDD: Given a custom agent with no OwnerUsername and ZERO admins,
// When migrateAgentOwnership runs,
// Then OwnerUsername stays "", and a WARN log is emitted.
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 3 /
// ---------------------------------------------------------------------------

func TestMigration_NoAdminLeavesEmpty(t *testing.T) {
	h := captureSlogHandler(t)

	admins := []UserConfig{} // no admins
	agents := []AgentConfig{
		{ID: "agent-no-admin", Name: "No Admin", Type: AgentTypeCustom},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	// Owner must remain empty.
	assert.Equal(t, "", cfg.Agents.List[0].OwnerUsername,
		"with no admins, OwnerUsername must stay empty")

	// A WARN log must be emitted.
	assert.True(t, containsMessage(h.records, "no admin available"),
		"WARN log must be emitted when no admin is available for migration")
}

// ---------------------------------------------------------------------------
// Dataset Row 4: TestMigration_SystemAgentSkipped
// BDD: Given an agent with Type=AgentTypeSystem and Default=false,
// When migrateAgentOwnership runs,
// Then OwnerUsername stays "" (system agents must not receive an owner).
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 4 /
// ---------------------------------------------------------------------------

func TestMigration_SystemAgentSkipped(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "omnipus-system", Name: "System", Type: AgentTypeSystem},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	assert.Equal(t, "", cfg.Agents.List[0].OwnerUsername,
		"system agents must never receive an OwnerUsername")
}

// ---------------------------------------------------------------------------
// Dataset Row 5: TestMigration_DefaultTrueCustomAgentMigrated ( regression)
// BDD: Given a custom agent with Default=true (UI-prominence flag only),
// When migrateAgentOwnership runs,
// Then the agent IS migrated (not skipped as a system agent).
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 5
// ---------------------------------------------------------------------------

func TestMigration_DefaultTrueCustomAgentMigrated(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{
			ID:      "custom-default-true",
			Name:    "Custom Default",
			Type:    AgentTypeCustom,
			Default: true,
		},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	assert.Equal(t, "alice", cfg.Agents.List[0].OwnerUsername,
		"custom agent with Default=true must be migrated (Default is UI-prominence, not system classification)")
}

// ---------------------------------------------------------------------------
// Dataset Row 6: TestMigration_CoreAgentSkipped
// BDD: Given an agent with Type=AgentTypeCore (e.g., jim),
// When migrateAgentOwnership runs,
// Then OwnerUsername stays "" (core agents must not receive an owner).
// Traces to: path-sandbox-and-capability-tiers-spec.md / Dataset Row 6 /
// ---------------------------------------------------------------------------

func TestMigration_CoreAgentSkipped(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "jim", Name: "Jim", Type: AgentTypeCore},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	assert.Equal(t, "", cfg.Agents.List[0].OwnerUsername,
		"core agents must never receive an OwnerUsername")
}

// ---------------------------------------------------------------------------
// TestMigration_PersistsToDisk
// BDD: Given a custom agent that gets an owner assigned by migration,
// When migrateAgentOwnership runs,
// Then SaveConfig writes the config; reload reveals the owner survives on disk.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestMigration_PersistsToDisk(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "agent-to-persist", Name: "Persist Me", Type: AgentTypeCustom},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	// In-memory: owner assigned.
	require.Equal(t, "alice", cfg.Agents.List[0].OwnerUsername)

	// On disk: owner must survive after reload.
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(data, &diskCfg))

	agentsSection, ok := diskCfg["agents"].(map[string]any)
	require.True(t, ok, "agents section must be present on disk")
	list, ok := agentsSection["list"].([]any)
	require.True(t, ok, "agents.list must be present on disk")

	// Find our agent.
	var diskAgent map[string]any
	for _, entry := range list {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if em["id"] == "agent-to-persist" {
			diskAgent = em
			break
		}
	}
	require.NotNil(t, diskAgent, "agent-to-persist must be present on disk")
	assert.Equal(t, "alice", diskAgent["owner_username"],
		"owner_username must survive SaveConfig round-trip")
}

// ---------------------------------------------------------------------------
// Differentiation test: two agents, two different outcomes
// ---------------------------------------------------------------------------

func TestMigration_MultipleCustomAgents_BothMigrated(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "agent-1", Name: "Agent One", Type: AgentTypeCustom},
		{ID: "agent-2", Name: "Agent Two", Type: AgentTypeCustom},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	// Both custom agents must receive alice as owner.
	assert.Equal(t, "alice", cfg.Agents.List[0].OwnerUsername,
		"agent-1 must be assigned alice")
	assert.Equal(t, "alice", cfg.Agents.List[1].OwnerUsername,
		"agent-2 must be assigned alice")
}

// ---------------------------------------------------------------------------
// TestMigration_MixedAgentTypes_OnlyCustomMigrated
// Confirms system/core agents are skipped while custom agents are migrated.
// Differentiation: same admin, different agent types produce different outcomes.
// ---------------------------------------------------------------------------

func TestMigration_MixedAgentTypes_OnlyCustomMigrated(t *testing.T) {
	admins := []UserConfig{
		{Username: "alice", Role: UserRoleAdmin},
	}
	agents := []AgentConfig{
		{ID: "system-1", Name: "System", Type: AgentTypeSystem},
		{ID: "core-1", Name: "Core", Type: AgentTypeCore},
		{ID: "custom-1", Name: "Custom", Type: AgentTypeCustom},
	}
	cfg, cfgPath := buildMigrationFixture(t, admins, agents)

	migrateAgentOwnership(cfg, cfgPath)

	// Find each by ID.
	byID := make(map[string]AgentConfig)
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a
	}

	assert.Equal(t, "", byID["system-1"].OwnerUsername, "system agent must not be migrated")
	assert.Equal(t, "", byID["core-1"].OwnerUsername, "core agent must not be migrated")
	assert.Equal(t, "alice", byID["custom-1"].OwnerUsername, "custom agent must be migrated")
}
