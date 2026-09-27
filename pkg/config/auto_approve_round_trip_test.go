// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveConfig_ExplicitAutoApproveFalseSurvivesRoundTrip is the
// regression test for review finding #4 (HIGH): sandbox.AutoApprove ships
// `true` (DefaultConfig, defaults.go), and before this fix carried
// `json:"auto_approve,omitempty"`. encoding/json omits a field exactly when
// it holds its type's zero value, and false IS bool's zero value — so an
// operator who explicitly turned Auto off (AutoApprove = false), followed
// by a whole-config SaveConfig, produced a config.json with NO
// "auto_approve" key at all. The next LoadConfig unmarshals onto
// DefaultConfig()'s seeded true and finds nothing in the JSON to overwrite
// it with, silently turning Auto back ON — the opposite of "switching Auto
// off globally stays off". Mirrors the already-fixed, structurally
// identical trap on Sandbox.AuditLog (audit_log_default_test.go).
//
// Re-adding `omitempty` to AutoApprove's json tag makes this test fail.
func TestSaveConfig_ExplicitAutoApproveFalseSurvivesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := DefaultConfig()
	if !cfg.Sandbox.AutoApprove {
		t.Fatalf("DefaultConfig().Sandbox.AutoApprove = false, want true (fresh-install seed) — " +
			"the round-trip assertion below is meaningless unless the default really is true")
	}
	cfg.Sandbox.AutoApprove = false

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// The key must be physically present in the file. Asserting only on the
	// reloaded value would not distinguish "false was written" from "the
	// key vanished and DefaultConfig's true happened to survive anyway".
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(raw), `"auto_approve"`) {
		t.Fatal(`saved config.json has no "auto_approve" key — omitempty dropped the operator's false; ` +
			`the next load will resurrect the seeded true and silently re-enable Auto`)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig: %v", err)
	}
	if reloaded.Sandbox.AutoApprove {
		t.Error("auto_approve came back true after a save/load round-trip of an explicit false — " +
			"the operator's global Auto off-switch does not stick (finding #4 regression)")
	}
}

// TestLoadConfig_AutoApprove_ExplicitValuesAreRespected covers both
// explicit values the positive-control way — pinning that a bare load
// (independent of any save round-trip) still honours whatever the operator
// physically wrote, for both directions.
func TestLoadConfig_AutoApprove_ExplicitValuesAreRespected(t *testing.T) {
	body := `{
  "version": ` + currentVersionJSON() + `,
  "sandbox": { "auto_approve": %s },
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" } } },
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}`
	for _, tc := range []struct {
		name string
		val  string
		want bool
	}{
		{"explicit false", "false", false},
		{"explicit true", "true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			content := strings.Replace(body, "%s", tc.val, 1)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Sandbox.AutoApprove != tc.want {
				t.Errorf("auto_approve = %v, want %v", cfg.Sandbox.AutoApprove, tc.want)
			}
		})
	}
}
