package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// S46 / CAP-A7 / CAP-B9. The retired key is tolerated on load, not migrated,
// validated, or reserialized. Values cover the old negative/zero/default classes.
func TestCapRED_RetiredSchedulesKeyIgnoredAndDropped(t *testing.T) {
	for _, n := range []int{-1, 0, 3, 8, 12} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(EnvHome, home)
			path := filepath.Join(home, "config.json")
			input := fmt.Sprintf(`{"version":1,"schedules":{"max_concurrent_runs":%d}}`, n)
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("retired schedule key must not prevent boot configuration load: %v", err)
			}
			if saveErr := SaveConfig(path, cfg); saveErr != nil {
				t.Fatalf("save after retired key: %v", saveErr)
			}
			bytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]json.RawMessage
			if err := json.Unmarshal(bytes, &saved); err != nil {
				t.Fatal(err)
			}
			if raw, present := saved["schedules"]; present {
				var schedules map[string]any
				if err := json.Unmarshal(raw, &schedules); err != nil {
					t.Fatal(err)
				}
				if value, present := schedules["max_concurrent_runs"]; present {
					t.Errorf("retired key was saved again with value %#v; want key absent", value)
				}
			}
		})
	}
}

func TestCapRED_SchedulesConfigHasNoFixedLaneCapacity(t *testing.T) {
	_, exists := reflect.TypeOf(SchedulesConfig{}).FieldByName("MaxConcurrentRuns")
	if exists {
		t.Error("R-6 RED: SchedulesConfig.MaxConcurrentRuns is still an operator-settable fixed lane limit")
	}
	raw, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if schedules, yes := cfg["schedules"].(map[string]any); yes {
		if _, exists := schedules["max_concurrent_runs"]; exists {
			t.Error("fresh config still seeds retired schedules.max_concurrent_runs")
		}
	}
}
