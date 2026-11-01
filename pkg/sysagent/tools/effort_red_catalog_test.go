package systools_test

// CHECK-11: D8's expected options come from the spec, while the given catalog
// is parsed by the production parser. No capability/get_agent mapper is mocked.
import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/require"
)

func tlInstallEffortCatalog(t *testing.T) {
	t.Helper()
	rows := []struct {
		id            string
		levels        []string
		sw, reasoning bool
	}{
		{tlPrimaryModel, []string{"none", "low", "high", "max"}, false, true},
		{tlFallbackSwitch, []string{}, true, true},
		{"levels-and-switch", []string{"low", "high"}, true, true},
		{"default-levels", []string{"low", "high"}, false, true},
		{"muted", []string{}, false, true},
		{"no-reasoning", nil, false, false},
	}
	models := []any{}
	for _, row := range rows {
		m := map[string]any{"id": row.id, "name": row.id, "context_window": 128000, "max_output_tokens": 8192, "input_modalities": []string{"text"}, "tool_call": true, "status": "active"}
		if row.reasoning {
			m["reasoning"] = map[string]any{"levels": row.levels, "switch": row.sw}
		}
		models = append(models, m)
	}
	raw, err := json.Marshal(map[string]any{"schema_version": "2.0.0", "version": "v2026.10.31", "updated_at": "2026-10-31T00:00:00Z", "source": "test", "default_resize_limits": map[string]any{"long_edge_px": 2048, "max_bytes": 10485760}, "providers": []any{map[string]any{"id": "openrouter", "name": "OpenRouter", "company": "OpenRouter", "api": "https://openrouter.ai/api/v1", "protocol": "openai-compatible", "protocols": []any{map[string]any{"protocol": "openai-compatible", "api": "https://openrouter.ai/api/v1"}}, "tier": "popular", "auth_methods": []string{"api_key"}, "aliases": []string{}, "models": models}}})
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	old := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(old) })
}

func TestT_TL67_D8_AllCapabilityAndInheritedSlots(t *testing.T) {
	rows := []struct {
		name, model string
		rideDefault bool
		want        []string
	}{
		{"primary_levels", tlPrimaryModel, false, []string{"provider_default", "none", "low", "high", "max"}},
		{"fallback_switch", tlFallbackSwitch, false, []string{"provider_default", "off", "on"}},
		{"fallback_levels_and_switch", "levels-and-switch", false, []string{"provider_default", "low", "high", "off"}},
		{"primary_rides_default", "default-levels", true, []string{"", "provider_default", "low", "high"}},
		{"nothing_sendable", "muted", false, []string{"provider_default"}},
		{"does_not_reason", "no-reasoning", false, nil},
		{"no_catalog_row", "custom-not-in-catalog", false, nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			deps, cfg := tlPersistentDeps(t)
			reg := tlRegistry(t, deps)
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openrouter", Model: row.model}
			model := row.model
			if row.rideDefault {
				model = ""
			}
			fallbacks := any(nil)
			if row.name == "fallback_switch" || row.name == "fallback_levels_and_switch" {
				model = tlPrimaryModel
				fallbacks = []any{map[string]any{"provider": "openrouter", "model": row.model}}
			}
			var id string
			if row.rideDefault {
				// create_agent currently requires an explicit model; that is not
				// the read-out behavior under test. Seed a valid ordinary entity
				// without a model using its real durable store.
				id = "d8-rides-default"
				_, err := agentstore.New(deps.Home).CreateState(id, &config.AgentConfig{ID: id, Name: "D8 Options"}, "D8 fixture")
				require.NoError(t, err)
			} else {
				id = tlCreateMainAgent(t, reg, "D8 Options", model, "openrouter", fallbacks)
			}
			res := tlExec(t, reg, "get_agent", map[string]any{"id": id, "fields": []any{"model_chain"}})
			require.False(t, res.IsError, "BLOCKED: get_agent fields/model_chain not implemented — required by S-67 D8: %s", res.ForLLM)
			chain, ok := parseSuccess(t, res.ForLLM)["model_chain"].([]any)
			require.True(t, ok)
			index := 0
			if fallbacks != nil {
				index = 1
			}
			require.Len(t, chain, index+1)
			slot, ok := chain[index].(map[string]any)
			require.True(t, ok)
			opts, present := slot["effort_options"]
			require.True(t, present, "each slot returns the field even when null")
			if row.want == nil {
				require.Nil(t, opts)
			} else {
				tlWantStringSlice(t, "effort_options", opts, row.want...)
			}
		})
	}
}
