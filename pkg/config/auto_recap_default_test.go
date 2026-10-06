// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// loadWithAgentsDefaults writes a minimal valid version-1 config.json with the given
// agents.defaults JSON object body and loads it.
func loadWithAgentsDefaults(t *testing.T, defaultsJSON string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version": 1, "agents": {"defaults": ` + defaultsJSON + `}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// Founder decision 2026-10-06: the session-end recap is ON by default.
func TestAutoRecap_DefaultConfigEnabled(t *testing.T) {
	if !DefaultConfig().Agents.Defaults.AutoRecapEnabled {
		t.Fatal("DefaultConfig().Agents.Defaults.AutoRecapEnabled = false, want true (recap is on by default)")
	}
}

// A config.json that does not mention the key keeps the default (on).
func TestAutoRecap_AbsentKeyKeepsDefaultOn(t *testing.T) {
	cfg := loadWithAgentsDefaults(t, `{"idle_timeout_minutes": 45}`)
	if !cfg.Agents.Defaults.AutoRecapEnabled {
		t.Fatal("absent auto_recap_enabled loaded as false, want the default true")
	}
	if cfg.Agents.Defaults.IdleTimeoutMinutes != 45 {
		t.Fatalf("idle_timeout_minutes = %d, want 45 (the file was actually applied)", cfg.Agents.Defaults.IdleTimeoutMinutes)
	}
}

// An operator's explicit false must stay false.
func TestAutoRecap_ExplicitFalseStaysFalse(t *testing.T) {
	cfg := loadWithAgentsDefaults(t, `{"auto_recap_enabled": false}`)
	if cfg.Agents.Defaults.AutoRecapEnabled {
		t.Fatal("explicit auto_recap_enabled=false loaded as true; the operator's choice was overridden")
	}
}

// An explicit true loads as true (guards the false-case test against a loader
// that ignores the key altogether).
func TestAutoRecap_ExplicitTrueStaysTrue(t *testing.T) {
	cfg := loadWithAgentsDefaults(t, `{"auto_recap_enabled": true}`)
	if !cfg.Agents.Defaults.AutoRecapEnabled {
		t.Fatal("explicit auto_recap_enabled=true loaded as false")
	}
}
