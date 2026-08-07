// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Bug 2 DoD proof: an OpenClaw migration must produce readable per-entity
// agent records — not merely populate the in-memory
// OmnipusConfig/config.Config.Agents.List, which (per Bug 1's fix,
// AgentsConfig.List json:"-") never reaches config.json's disk bytes, and
// which even before that fix was silently dropped on the very next config
// load by stripLegacyAgentsList. Before the persistMigratedAgents fix in
// openclaw_handler.go, ExecuteConfigMigration wrote nothing to
// $OMNIPUS_HOME/entities/agents/ at all, so every migrated agent evaporated
// on first boot.

package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
)

// TestExecuteConfigMigration_PersistsAgentsAsEntityRecords proves both bugs'
// fixes together, end to end through the real migration entrypoint: (1)
// config.json on disk never carries agents.list, and (2) every migrated
// agent is readable back out of the per-entity agent store.
func TestExecuteConfigMigration_PersistsAgentsAsEntityRecords(t *testing.T) {
	srcHome := t.TempDir()
	srcConfigPath := filepath.Join(srcHome, "openclaw.json")
	srcConfig := `{
		"agents": {
			"defaults": {
				"workspace": "~/.openclaw/workspace"
			},
			"list": [
				{
					"id": "sales",
					"name": "Sales Bot",
					"workspace": "~/.openclaw/workspace/sales",
					"model": {"primary": "openai/gpt-4o"}
				},
				{
					"id": "support",
					"name": "Support Bot",
					"workspace": "~/.openclaw/workspace/support",
					"model": {"primary": "anthropic/claude-opus", "fallbacks": ["haiku"]},
					"skills": ["triage"]
				}
			]
		}
	}`
	require.NoError(t, os.WriteFile(srcConfigPath, []byte(srcConfig), 0o644))

	handler, err := NewOpenclawHandler(Options{SourceHome: srcHome})
	require.NoError(t, err)

	dstHome := t.TempDir()
	dstConfigPath := filepath.Join(dstHome, "config.json")

	require.NoError(t, handler.ExecuteConfigMigration(srcConfigPath, dstConfigPath))

	// 1. config.json itself must never carry the roster (Bug 1's structural
	// guarantee — AgentsConfig.List is json:"-" — exercised end-to-end
	// through the migration write path, not just a direct SaveConfig call).
	raw, err := os.ReadFile(dstConfigPath)
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	agentsSection, ok := onDisk["agents"].(map[string]any)
	require.True(t, ok, "config.json must still have an agents section (defaults)")
	_, hasList := agentsSection["list"]
	assert.False(t, hasList, "config.json must NOT carry agents.list after migration")

	// 2. Every migrated agent must be readable as its own entity record
	// under $OMNIPUS_HOME/entities/agents/<id>.json (Bug 2's fix).
	store := agentstore.New(dstHome)

	sales, err := store.Get("sales")
	require.NoError(t, err, "sales agent must be readable from the entity store")
	assert.Equal(t, "sales", sales.ID)
	assert.Equal(t, "Sales Bot", sales.Name)
	assert.Equal(t, "~/.omnipus/workspace/sales", sales.Home)
	require.NotNil(t, sales.Model)
	assert.Equal(t, "openai/gpt-4o", sales.Model.Primary)

	support, err := store.Get("support")
	require.NoError(t, err, "support agent must be readable from the entity store")
	assert.Equal(t, "support", support.ID)
	assert.Equal(t, "Support Bot", support.Name)
	require.NotNil(t, support.Model)
	assert.Equal(t, "anthropic/claude-opus", support.Model.Primary)
	assert.Equal(t, []string{"haiku"}, support.Model.Fallbacks)
	assert.Equal(t, []string{"triage"}, support.Skills)

	agents, skipped, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, skipped, "no entity record should have failed to load")
	assert.Len(t, agents, 2, "both migrated agents must be listed from the entity store")
}

// TestExecuteConfigMigration_NoAgents_NoOp proves persistMigratedAgents is a
// clean no-op (no entities directory clutter, no error) when the source
// config has no agent roster at all.
func TestExecuteConfigMigration_NoAgents_NoOp(t *testing.T) {
	srcHome := t.TempDir()
	srcConfigPath := filepath.Join(srcHome, "openclaw.json")
	require.NoError(t, os.WriteFile(srcConfigPath, []byte(`{}`), 0o644))

	handler, err := NewOpenclawHandler(Options{SourceHome: srcHome})
	require.NoError(t, err)

	dstHome := t.TempDir()
	dstConfigPath := filepath.Join(dstHome, "config.json")

	require.NoError(t, handler.ExecuteConfigMigration(srcConfigPath, dstConfigPath))

	store := agentstore.New(dstHome)
	agents, skipped, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Empty(t, agents)
}

// TestExecuteConfigMigration_RerunUpdatesExistingEntityRecords proves
// re-running the migration (e.g. an operator re-invoking `omnipus migrate`
// after editing their openclaw config) does not hard-fail with
// entity.ErrAlreadyExists — it refreshes the existing record's fields while
// preserving its original ID/CreatedAt, mirroring the "always clobber on
// re-migrate" semantics config.json's own SaveConfig already has.
func TestExecuteConfigMigration_RerunUpdatesExistingEntityRecords(t *testing.T) {
	srcHome := t.TempDir()
	srcConfigPath := filepath.Join(srcHome, "openclaw.json")
	require.NoError(t, os.WriteFile(srcConfigPath, []byte(`{
		"agents": {"list": [{"id": "sales", "name": "Sales Bot"}]}
	}`), 0o644))

	handler, err := NewOpenclawHandler(Options{SourceHome: srcHome})
	require.NoError(t, err)

	dstHome := t.TempDir()
	dstConfigPath := filepath.Join(dstHome, "config.json")

	require.NoError(t, handler.ExecuteConfigMigration(srcConfigPath, dstConfigPath))

	store := agentstore.New(dstHome)
	first, err := store.Get("sales")
	require.NoError(t, err)
	require.NotNil(t, first.CreatedAt)
	firstCreatedAt := *first.CreatedAt

	// Edit the source and re-run.
	require.NoError(t, os.WriteFile(srcConfigPath, []byte(`{
		"agents": {"list": [{"id": "sales", "name": "Sales Bot Renamed"}]}
	}`), 0o644))
	require.NoError(t, handler.ExecuteConfigMigration(srcConfigPath, dstConfigPath))

	second, err := store.Get("sales")
	require.NoError(t, err)
	assert.Equal(t, "sales", second.ID, "re-migration must not change the agent's ID")
	assert.Equal(t, "Sales Bot Renamed", second.Name, "re-migration must refresh the agent's fields")
	require.NotNil(t, second.CreatedAt)
	assert.True(t, firstCreatedAt.Equal(*second.CreatedAt), "re-migration must preserve the original CreatedAt")
}
