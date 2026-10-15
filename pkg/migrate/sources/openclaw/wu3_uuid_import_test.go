package openclaw_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/migrate/sources/openclaw"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder 2026-10-14T17:10Z: public OpenClaw import generates NEW random IDs,
// never retains the source ID or derives an ID from the display label.
func TestWU3OpenClawImportGeneratesUUIDsAndKeepsNamesVerbatim(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", dest)
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	store := agentstore.New(dest)
	for _, ac := range cfg.Agents.List {
		copyAgent := ac
		require.NoError(t, store.Create(ac.ID, &copyAgent))
	}
	sourcePath := filepath.Join(source, "openclaw.json")
	input := map[string]any{"agents": map[string]any{"list": []any{
		map[string]any{"id": "source-one", "name": "MiXeD Label", "workspace": source, "model": map[string]any{"primary": "openai/fixture"}},
		map[string]any{"id": "source-two", "name": "MiXeD Label", "workspace": source, "model": map[string]any{"primary": "openai/fixture"}},
	}}}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sourcePath, data, 0o600))
	handler, err := openclaw.NewOpenclawHandler(openclaw.Options{SourceHome: source})
	require.NoError(t, err)
	require.NoError(t, handler.ExecuteConfigMigration(sourcePath, filepath.Join(dest, "config.json")))
	agents, skipped, err := store.List()
	require.NoError(t, err)
	require.Empty(t, skipped)
	ids := map[string]bool{}
	for _, ac := range agents {
		if ac.Name != "MiXeD Label" {
			continue
		}
		parsed, parseErr := uuid.Parse(ac.ID)
		require.NoError(t, parseErr, "source identity must be replaced with random UUID")
		assert.Equal(t, uuid.Version(4), parsed.Version())
		assert.False(t, ids[ac.ID], "imported entries have distinct IDs even with identical names")
		ids[ac.ID] = true
		assert.NotEqual(t, "source-one", ac.ID)
		assert.NotEqual(t, "source-two", ac.ID)
	}
	assert.Len(t, ids, 2, "both original display names survive exactly")
	for _, id := range []string{"omnipus", "mia", "jim"} {
		ac, getErr := store.Get(id)
		require.NoError(t, getErr)
		assert.Equal(t, id, ac.ID, "built-in identity remains fixed during import")
	}
}
