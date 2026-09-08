// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// orphan_grace_key_ignored_test.go — T-14 (S-13, FR-002), ADR-082 D1/D7.
//
// The ADR-045 orphaned-foreground-turn watchdog and its
// gateway.orphaned_turn_grace_seconds config key are deleted in full
// (greenfield, ADR-082 §5) — not disabled, not defaulted to 0, REMOVED:
// GatewayConfig.OrphanedTurnGraceSeconds no longer exists as a Go field at
// all. This is the regression test the deletion inventory calls for: an
// operator's pre-existing config.json that still carries the retired key
// (an upgrade from a pre-ADR-082 install) must still boot cleanly — Go's
// standard json.Unmarshal silently ignores unknown object keys, so the
// retired key becomes ordinary dead JSON with zero effect, rather than a
// load failure — and the field itself must be gone from the struct so no
// future code can accidentally resurrect a live consumer of it.
package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestConfig_OrphanGraceKeyIgnored is T-14 (S-13): "Given a config.json
// carrying gateway.orphaned_turn_grace_seconds, When the gateway loads,
// Then it boots, the key has no effect, and no watchdog is armed on
// connection close." The "no watchdog is armed" half of S-13 is T-15
// (pkg/gateway); this test covers the config-load half plus the structural
// assertion that makes a future reintroduction impossible to do by
// accident — a re-added field would need its own new PR, not a config.json
// upgrade path silently reviving dead code.
func TestConfig_OrphanGraceKeyIgnored(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// A realistic pre-ADR-082 config.json: the retired key present under
	// "gateway", alongside an otherwise-minimal valid config (mirrors the
	// minimal fixture shape TestLoadConfig_ToolFeedbackDefaultsFalseWhenUnset
	// uses elsewhere in this package).
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}},
		"gateway": {
			"port": 5000,
			"orphaned_turn_grace_seconds": 20
		}
	}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() must succeed against a config.json carrying the retired "+
			"gateway.orphaned_turn_grace_seconds key (an upgrade from a pre-ADR-082 install "+
			"must still boot) — got error: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned a nil *Config with no error")
	}
	// The rest of the same "gateway" object loaded normally — proves the
	// retired key was silently ignored as ordinary unknown JSON, not that
	// the whole gateway section failed to parse and fell back to zero values.
	if cfg.Gateway.Port != 5000 {
		t.Errorf("cfg.Gateway.Port = %d, want 5000 — the gateway section around the retired "+
			"key must still parse correctly", cfg.Gateway.Port)
	}

	// Structural assertion: GatewayConfig must carry NO field for the
	// retired key, by either its former Go field name or its former JSON
	// tag. A field reappearing here — even unused — is exactly the kind of
	// accidental resurrection ADR-082's guard script cannot catch on its
	// own (the guard matches identifiers in source text; this asserts the
	// type itself, independent of naming drift).
	gwType := reflect.TypeOf(GatewayConfig{})
	for i := 0; i < gwType.NumField(); i++ {
		f := gwType.Field(i)
		if f.Name == "OrphanedTurnGraceSeconds" {
			t.Errorf("GatewayConfig still has a field named %q — ADR-082 D1 deleted this field", f.Name)
		}
		jsonTag := f.Tag.Get("json")
		jsonName, _, _ := strings.Cut(jsonTag, ",")
		if jsonName == "orphaned_turn_grace_seconds" {
			t.Errorf("GatewayConfig field %q carries the retired json tag %q — ADR-082 D1 deleted "+
				"the orphaned_turn_grace_seconds config key", f.Name, jsonTag)
		}
	}
}
