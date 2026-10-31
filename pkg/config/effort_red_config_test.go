package config

// effort_red_config_test.go — RED tests for the configuration codec of the
// effort-levels feature (T-D1 / T-W2): the deleted legacy thinking_level key,
// and round trips that must retain the new effort keys instead of dropping
// them silently.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-69, FR-056, T-D1, T-W2 (AgentModelConfig.MarshalJSON /
// DefaultModel / FallbackModelSlice / recap keys round trips).
//
// RED reason: the effort fields do not exist on the config structs today
// (the loader drops unknown JSON keys), so every "retained after a round
// trip" assertion fails; the legacy thinking_level field still exists and
// round-trips, so its deletion assertion fails too.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadFromJSON(t *testing.T, raw string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	cfg, err := LoadConfig(path)
	require.NoError(t, err, "the saved configuration loads")
	return cfg
}

func remarshal(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestT_D1_S69_ThinkingLevelKeyDeleted pins FR-056: the legacy thinking_level
// key is deleted — an old configuration still containing it loads without
// error, and the key survives nowhere in the loaded model row.
// Traces to: S-69, FR-056, T-D1.
func TestT_D1_S69_ThinkingLevelKeyDeleted(t *testing.T) {
	cfg := loadFromJSON(t, `{
		"agents": {"defaults": {"default_model": {"model": "m1", "thinking_level": "high"}}}
	}`)
	require.NotNil(t, cfg)

	m := remarshal(t, cfg.Agents.Defaults.DefaultModel)
	assert.NotContains(t, m, "thinking_level",
		"the legacy thinking_level key is deleted from the model row (S-69/FR-056) — RED: the field still exists")
	assert.Equal(t, "m1", m["model"], "the model row itself still loads — PIN")
}

// TestT_W2_DefaultModelEffortRoundTrip pins T-W2: a configuration carrying
// the default-model effort round-trips through load → save without silent
// loss, for two different values (hardcode guard).
// Traces to: T-W2, S-51 (storage half), FR-039.
func TestT_W2_DefaultModelEffortRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effort string
	}{
		{name: "high persists", effort: "high"},
		{name: "low persists too (not a hardcoded value)", effort: "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadFromJSON(t, `{
				"agents": {"defaults": {"default_model": {"model": "m1", "reasoning_effort": "`+tc.effort+`"}}}
			}`)
			m := remarshal(t, cfg.Agents.Defaults.DefaultModel)
			assert.Equal(t, tc.effort, m["reasoning_effort"],
				"the default-model effort survives the config round trip (T-W2) — RED: dropped as an unknown key")
		})
	}
}

// TestT_W2_RecapEffortKeysRoundTrip pins T-W2's recap half: the recap effort
// key and the recap fallback list (each fallback with its own effort) round
// trip through the configuration codec.
// Traces to: T-W2, S-83 (storage half), FR-073, FR-021.
func TestT_W2_RecapEffortKeysRoundTrip(t *testing.T) {
	cfg := loadFromJSON(t, `{
		"agents": {"defaults": {
			"recap_reasoning_effort": "high",
			"recap_max_output_tokens": 4096,
			"recap_fallback_models": [{"model": "m2", "reasoning_effort": "max"}]
		}}
	}`)
	m := remarshal(t, cfg.Agents.Defaults)
	assert.Equal(t, "high", m["recap_reasoning_effort"],
		"recap_reasoning_effort survives the config round trip (S-83/T-W2) — RED")
	assert.Equal(t, float64(4096), m["recap_max_output_tokens"],
		"recap_max_output_tokens survives the config round trip (S-79/T-W2) — RED")
	fb, has := m["recap_fallback_models"]
	require.True(t, has, "recap_fallback_models survive the config round trip — PIN (the list exists)")
	list, ok := fb.([]any)
	require.True(t, ok)
	require.NotEmpty(t, list)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "max", first["reasoning_effort"],
		"each fallback keeps its own effort through the round trip (FR-021/S-53 storage half) — RED")
}
