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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
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
	// under $OMNIPUS_HOME/entities/agents/<id>.json (Bug 2's fix). Founder
	// decision 2026-10-14: the ids are generated UUIDs (the OpenClaw ids
	// "sales"/"support" are not kept) and the names are kept verbatim.
	store := agentstore.New(dstHome)
	agents, skipped, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, skipped, "no entity record should have failed to load")
	require.Len(t, agents, 2, "both migrated agents must be listed from the entity store")
	byName := map[string]config.AgentConfig{}
	for _, a := range agents {
		_, parseErr := uuid.Parse(a.ID)
		assert.NoError(t, parseErr, "imported agent %q must have a generated UUID id, got %q", a.Name, a.ID)
		assert.NotContains(t, []string{"sales", "support"}, a.ID, "the OpenClaw id must not be kept")
		byName[a.Name] = a
	}

	sales, ok := byName["Sales Bot"]
	require.True(t, ok, "the Sales Bot name must be kept verbatim")
	got, err := store.Get(sales.ID)
	require.NoError(t, err, "sales agent must be readable from the entity store by its new id")
	assert.Equal(t, "~/.omnipus/workspace/sales", got.Home)
	require.NotNil(t, got.Model)
	assert.Equal(t, "openai/gpt-4o", got.Model.Primary)

	support, ok := byName["Support Bot"]
	require.True(t, ok, "the Support Bot name must be kept verbatim")
	require.NotNil(t, support.Model)
	assert.Equal(t, "anthropic/claude-opus", support.Model.Primary)
	assert.Equal(t, []string{"haiku"}, support.Model.Fallbacks)
	assert.Equal(t, []string{"triage"}, support.Skills)
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

// TestExecuteConfigMigration_RerunImportsAgain proves a re-run of the
// migration never overwrites an agent an earlier run created: every imported
// agent gets a freshly generated id (founder decision 2026-10-14 — old ids
// are not kept, so there is nothing to match a re-run against), so the second
// run adds a second agent and the first keeps its fields.
func TestExecuteConfigMigration_RerunImportsAgain(t *testing.T) {
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

	require.NoError(t, os.WriteFile(srcConfigPath, []byte(`{
		"agents": {"list": [{"id": "sales", "name": "Sales Bot Renamed"}]}
	}`), 0o644))
	require.NoError(t, handler.ExecuteConfigMigration(srcConfigPath, dstConfigPath))

	agents, _, err := agentstore.New(dstHome).List()
	require.NoError(t, err)
	require.Len(t, agents, 2, "a re-run imports the roster again as new agents")
	names := []string{agents[0].Name, agents[1].Name}
	assert.ElementsMatch(t, []string{"Sales Bot", "Sales Bot Renamed"}, names,
		"the first run's agent keeps its name; the re-run adds its own")
	assert.NotEqual(t, agents[0].ID, agents[1].ID, "each imported agent gets its own generated id")
}
