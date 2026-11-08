package config

// C1 authority: approved DESIGN-v2 and later independent-clock ruling.
// JSON boundaries intentionally compile before C1 fields exist. Unknown-key
// acceptance is not enough: valid input must survive exact save/reload.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanningGoalTimersC1(t *testing.T) {
	t.Run("ExistingBootValidationInstrument", func(t *testing.T) {
		cfg, err := c1PlanningLoad(t, map[string]any{"goal_max_rounds": 7, "check_timeout_seconds": 9})
		require.NoError(t, err)
		require.Equal(t, 7, cfg.Planning.EffectiveGoalMaxRounds())
		require.Equal(t, 9, cfg.Planning.CheckTimeoutSeconds)
		_, err = c1PlanningLoad(t, map[string]any{"check_timeout_seconds": 3601})
		require.ErrorContains(t, err, "CheckTimeoutSeconds")
	})
	t.Run("DefaultConfigResolvedValues", func(t *testing.T) {
		c1PlanningTuple(t, DefaultConfig(), 60, true, 30)
	})
	for _, name := range []string{"MissingPlanning", "EmptyPlanning", "NullPointers", "ZeroValuePlanning", "IdleExpiryOnlyPlanning"} {
		t.Run(name, func(t *testing.T) {
			var planning any = map[string]any{}
			if name == "MissingPlanning" {
				planning = nil
			}
			if name == "NullPointers" {
				planning = map[string]any{"goal_reminder_quiet_seconds": nil, "goal_stalled_checkin_enabled": nil, "goal_stalled_checkin_minutes": nil}
			}
			cfg, err := c1PlanningLoad(t, planning)
			require.NoError(t, err, "absence/null is not explicit numeric zero")
			require.Equal(t, 7, cfg.Performance.MaxParallelAgents)
			if name == "ZeroValuePlanning" {
				cfg.Planning = PlanningConfig{}
			}
			if name == "IdleExpiryOnlyPlanning" {
				cfg.Planning = PlanningConfig{IdleExpiryDays: 1}
			}
			// Nil pointers are omitted on save; a fresh normal load seeds the
			// defaults. Actual nil-effective GET/sweep coverage is in sibling roots.
			path := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, SaveConfig(path, cfg))
			fresh, err := LoadConfig(path)
			require.NoError(t, err)
			c1PlanningTuple(t, fresh, 60, true, 30)
		})
	}
	for _, spec := range []struct {
		key    string
		values []int
		lo, hi int
	}{
		{"goal_reminder_quiet_seconds", []int{-1, 0, 29, 30, 31, 3599, 3600, 3601}, 30, 3600},
		{"goal_stalled_checkin_minutes", []int{-1, 0, 4, 5, 6, 1439, 1440, 1441}, 5, 1440},
	} {
		states := []bool{true}
		if spec.key == "goal_stalled_checkin_minutes" {
			states = []bool{true, false}
		}
		for _, enabled := range states {
			for _, value := range spec.values {
				t.Run(fmt.Sprintf("Bounds/%s/%d/enabled_%t", spec.key, value, enabled), func(t *testing.T) {
					planning := map[string]any{"goal_max_rounds": 7, "goal_reminder_quiet_seconds": 60, "goal_stalled_checkin_enabled": enabled, "goal_stalled_checkin_minutes": 30}
					planning[spec.key] = value
					cfg, err := c1PlanningLoad(t, planning)
					if value < spec.lo || value > spec.hi {
						require.Error(t, err, "explicit out-of-range input must never default or clamp, including while OFF")
						require.Nil(t, cfg)
						c1PlanningErrorField(t, err, spec.key)
						return
					}
					require.NoError(t, err)
					require.Equal(t, 7, cfg.Planning.EffectiveGoalMaxRounds())
					path := filepath.Join(t.TempDir(), "config.json")
					require.NoError(t, SaveConfig(path, cfg))
					fresh, err := LoadConfig(path)
					require.NoError(t, err)
					quiet, minutes := 60, 30
					if spec.key == "goal_reminder_quiet_seconds" {
						quiet = value
					} else {
						minutes = value
					}
					c1PlanningTuple(t, fresh, quiet, enabled, minutes)
				})
			}
		}
	}
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("ExplicitBooleanSaveReload/%t", enabled), func(t *testing.T) {
			cfg, err := c1PlanningLoad(t, map[string]any{"goal_max_rounds": 7, "goal_reminder_quiet_seconds": 120, "goal_stalled_checkin_enabled": enabled, "goal_stalled_checkin_minutes": 45})
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "config.json")
			require.NoError(t, SaveConfig(path, cfg))
			fresh, err := LoadConfig(path)
			require.NoError(t, err)
			c1PlanningTuple(t, fresh, 120, enabled, 45)
		})
	}
	for _, key := range []string{"goal_reminder_quiet_seconds", "goal_stalled_checkin_minutes", "goal_stalled_checkin_enabled"} {
		bad := []any{"not-a-timer", 30.5, false}
		if key == "goal_stalled_checkin_enabled" {
			bad = []any{"false", 0}
		}
		for i, value := range bad {
			t.Run(fmt.Sprintf("Malformed/%s/%d", key, i), func(t *testing.T) {
				cfg, err := c1PlanningLoad(t, map[string]any{key: value})
				require.Error(t, err)
				require.Nil(t, cfg)
				var mismatch *json.UnmarshalTypeError
				require.ErrorAs(t, err, &mismatch, "typed planning field must reject wrong JSON type")
				c1PlanningErrorField(t, err, key)
			})
		}
	}
}

func c1PlanningLoad(t *testing.T, planning any) (*Config, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	input := map[string]any{"version": CurrentVersion, "performance": map[string]any{"max_parallel_agents": 7}}
	if planning != nil {
		input["planning"] = planning
	}
	if fields, ok := input["planning"].(map[string]any); ok {
		if _, exists := fields["goal_max_rounds"]; !exists {
			fields["goal_max_rounds"] = 7
		}
	}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	path := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return LoadConfig(path)
}

func c1PlanningTuple(t *testing.T, cfg *Config, quiet int, enabled bool, minutes int) {
	t.Helper()
	data, err := json.Marshal(cfg.Planning)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	got := map[string]any{}
	for _, key := range []string{"goal_reminder_quiet_seconds", "goal_stalled_checkin_enabled", "goal_stalled_checkin_minutes"} {
		got[key] = raw[key]
	}
	require.Equal(t, map[string]any{"goal_reminder_quiet_seconds": float64(quiet), "goal_stalled_checkin_enabled": enabled, "goal_stalled_checkin_minutes": float64(minutes)}, got, "C1 specified defaults/explicit values must survive the actual config boundary")
}

func c1PlanningErrorField(t *testing.T, err error, key string) {
	t.Helper()
	normalize := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "_", ""), ".", ""))
	}
	require.Contains(t, normalize(err.Error()), normalize(key), "refusal must identify the invalid C1 field; exact copy is not specified")
}
