// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfig_LegacyModelListMigration_NoDuplicateOnRoundTrip is a
// regression test for a bug where detectUnknownConfigFields was called with
// the pre-rename `data` map (which still has the legacy "model_list" key)
// instead of `compatData` (the map used for unmarshalling, which has the key
// renamed to "providers"). That mismatch caused "model_list" to be flagged as
// an unrecognized field on every load, stashed into cfg.UnknownFields, and
// written back out verbatim by SaveConfig — permanently duplicating the
// providers data under both "model_list" and "providers" on every
// load+save cycle.
func TestLoadConfig_LegacyModelListMigration_NoDuplicateOnRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	legacy := `{
		"version": 1,
		"model_list": [
			{"provider": "openai", "model": "gpt-4o", "name": "legacy-model"}
		]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// The legacy model_list entry must have been migrated into Providers.
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "legacy-model" {
		t.Fatalf("expected exactly 1 provider named 'legacy-model' after migration, got %+v", cfg.Providers)
	}

	// The bug under test: "model_list" would be flagged as an unknown field
	// even though it was renamed to "providers" (a known field) before
	// unmarshalling.
	if _, ok := cfg.UnknownFields["model_list"]; ok {
		t.Fatal(
			"cfg.UnknownFields must not contain 'model_list' — it was renamed to 'providers' before unmarshalling and is therefore a known field",
		)
	}

	if err = SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err = json.Unmarshal(saved, &raw); err != nil {
		t.Fatalf("Unmarshal(saved config) error: %v", err)
	}

	if _, ok := raw["model_list"]; ok {
		t.Fatalf("saved config must not contain a duplicated top-level 'model_list' key, got: %s", string(saved))
	}
	if _, ok := raw["providers"]; !ok {
		t.Fatalf("saved config must contain the renamed 'providers' key, got: %s", string(saved))
	}
	if got := strings.Count(string(saved), "legacy-model"); got != 1 {
		t.Fatalf(
			"saved config should contain 'legacy-model' exactly once (no duplication), got %d occurrences in: %s",
			got,
			string(saved),
		)
	}

	// Reload once more to prove the round-trip is stable and the bug doesn't
	// re-accumulate the unknown field on a second cycle.
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("second LoadConfig() error: %v", err)
	}
	if _, ok := reloaded.UnknownFields["model_list"]; ok {
		t.Fatal("second load must not re-flag 'model_list' as an unknown field")
	}
	if len(reloaded.Providers) != 1 || reloaded.Providers[0].Name != "legacy-model" {
		t.Fatalf("expected exactly 1 provider named 'legacy-model' after second load, got %+v", reloaded.Providers)
	}
}
