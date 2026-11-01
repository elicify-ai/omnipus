package systools_test

// C2-01 / S-58 / SC-007: update_agent must map or refuse a model-only change
// just like REST. Expected values and the refusal sentence come from S-58,
// not from the current updater. Only the Given is seeded as canonical JSON:
// requiring the missing effort-write tool to seed it would hide this boundary.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tlInstallS58Catalog(t *testing.T) {
	t.Helper()
	rows := []any{}
	for _, row := range []struct {
		id         string
		levels     []string
		switchable bool
	}{
		{"s58-switch", []string{}, true},
		{"s58-with-none", []string{"none", "low", "high", "max"}, false},
		{"s58-without-off", []string{"low", "medium", "high"}, false},
	} {
		rows = append(rows, map[string]any{
			"id": row.id, "name": row.id, "context_window": 128000,
			"max_output_tokens": 8192, "input_modalities": []string{"text"},
			"tool_call": true, "status": "active",
			"reasoning": map[string]any{"levels": row.levels, "switch": row.switchable},
		})
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": "2.0.0", "version": "v2026.11.01", "updated_at": "2026-11-01T00:00:00Z", "source": "test",
		"default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760},
		"providers": []any{map[string]any{
			"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter", "api": "https://openrouter.ai/api/v1",
			"protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://openrouter.ai/api/v1"}},
			"tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": rows,
		}},
	})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err, "the S-58 catalog fixture uses the production parser")
	old := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}

func TestT_TL58_ModelChangeWithoutEffortPreservesOffOrRefusesAtomically(t *testing.T) {
	for _, row := range []struct {
		name, target string
		refuse       bool
	}{
		{"target_offered_none_preserves_off", "s58-with-none", false},
		{"target_without_off_refuses_whole_update", "s58-without-off", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			deps, cfg := tlPersistentDeps(t)
			tlInstallS58Catalog(t)
			reg := tlRegistry(t, deps)
			id := tlCreateMainAgent(t, reg, "S58 model change", "s58-switch", "openrouter", nil)
			entityPath := filepath.Join(deps.Home, "entities", "agents", id+".json")
			given := tlReadEntityJSON(t, deps.Home, id)
			given["reasoning_effort"] = "off"
			raw, err := json.Marshal(given)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(entityPath, raw, 0o600))
			require.Equal(t, "off", tlReadEntityJSON(t, deps.Home, id)["reasoning_effort"], "instrument: the durable Given contains Off")
			revision := currentAgentRevision(t, deps, id)
			beforeConfig, err := os.ReadFile(deps.ConfigPath)
			require.NoError(t, err)
			beforeRuntime, err := json.Marshal(cfg)
			require.NoError(t, err)
			soulPath := filepath.Join(deps.Home, "agents", id, "SOUL.md")
			beforeSoul, err := os.ReadFile(soulPath)
			require.NoError(t, err)
			args := map[string]any{
				"id": id, "revision": revision, "model": row.target,
				"provider": "openrouter", "description": "valid S58 sibling",
			}
			require.NotContains(t, args, "reasoning_effort", "S-58 exercises omission, not an explicit clear or replacement")
			res := tlExec(t, reg, "update_agent", args)
			after := tlReadEntityJSON(t, deps.Home, id)
			if !row.refuse {
				require.False(t, res.IsError, "target offering none accepts the omitted-effort model change: %s", res.ForLLM)
				// AgentModelConfig's canonical persisted pair is nested under model;
				// the tool input's provider sibling is not an entity top-level key.
				require.IsType(t, map[string]any{}, after["model"])
				assert.Equal(t, map[string]any{"primary": row.target, "provider": "openrouter"}, after["model"], "supported model/provider pair is durable")
				assert.Equal(t, "off", after["reasoning_effort"], "S-58 stores Off, not none or an absent setting")
				assert.Equal(t, "valid S58 sibling", after["description"])
				assert.NotEqual(t, revision, currentAgentRevision(t, deps, id), "a successful write changes the durable revision")
				return
			}
			// Nonfatal first assertions keep the atomicity checks observable even
			// when today's updater incorrectly reports success.
			assert.True(t, res.IsError, "S-58 must refuse when the target cannot turn reasoning off: %s", res.ForLLM)
			assert.Equal(t, "s58-without-off can't turn reasoning off. Send reasoning_effort with one of: provider_default, low, medium, high.", tlRefusalText(t, res), "S-58 fixes the full sentence and offered-option order")
			afterBytes, err := os.ReadFile(entityPath)
			require.NoError(t, err)
			assert.Equal(t, raw, afterBytes, "refusal saves nothing, including model/provider/Off/valid sibling")
			assert.Equal(t, revision, currentAgentRevision(t, deps, id), "refusal preserves the revision")
			afterConfig, err := os.ReadFile(deps.ConfigPath)
			require.NoError(t, err)
			assert.Equal(t, beforeConfig, afterConfig, "refusal leaves config bytes unchanged")
			afterRuntime, err := json.Marshal(cfg)
			require.NoError(t, err)
			assert.Equal(t, beforeRuntime, afterRuntime, "refusal leaves runtime config unchanged")
			afterSoul, err := os.ReadFile(soulPath)
			require.NoError(t, err)
			assert.Equal(t, beforeSoul, afterSoul, "refusal leaves the SOUL unchanged")
		})
	}
}
