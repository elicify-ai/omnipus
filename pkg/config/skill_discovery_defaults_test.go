// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const clawHubDefaultURL = "https://clawhub.ai"

// writeTempConfig writes raw JSON to a temp config.json and returns its path.
func writeTempConfig(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

// TestRestoreSkillDiscoveryDefaults_OnboardedConfigWithZeroValues reproduces the
// bug: an onboarded config.json that persisted the skill-discovery tool fields
// as disabled/empty zero values. Before the fix the unmarshal overwrites the
// DefaultConfig() defaults; after the fix they are restored because the keys
// are absent from the raw JSON (no operator intent).
func TestRestoreSkillDiscoveryDefaults_OnboardedConfigWithZeroValues(t *testing.T) {
	// This mirrors a real onboarded config: providers + agents + gateway set,
	// and a tools section that carries explicit zero values for the skill
	// fields (as a config written from a partially-populated struct would).
	// Critically there is NO "enabled" key under clawhub/find_skills/install_skill
	// — only sibling keys — so intent detection treats them as absent.
	raw := `{
  "version": 1,
  "agents": {"defaults": {}, "list": []},
  "providers": [],
  "channels": {},
  "gateway": {"host": "localhost", "port": 5000},
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {"base_url": "", "search_path": "/api/v1/search"}
      }
    }
  }
}`
	p := writeTempConfig(t, raw)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ch := cfg.Tools.Skills.Registries.ClawHub
	if !ch.Enabled {
		t.Errorf("clawhub.Enabled = false; want true (default restored)")
	}
	if ch.BaseURL != clawHubDefaultURL {
		t.Errorf("clawhub.BaseURL = %q; want %q", ch.BaseURL, clawHubDefaultURL)
	}
	if !cfg.Tools.FindSkills.Enabled {
		t.Errorf("find_skills.Enabled = false; want true (default restored)")
	}
	if !cfg.Tools.InstallSkill.Enabled {
		t.Errorf("install_skill.Enabled = false; want true (default restored)")
	}
}

// TestRestoreSkillDiscoveryDefaults_DefaultRoundTrip proves a default config
// survives a full save+reload with the skill-discovery defaults intact. This is
// the round-trip the task requires.
func TestRestoreSkillDiscoveryDefaults_DefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")

	def := DefaultConfig()
	if err := SaveConfig(p, def); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ch := cfg.Tools.Skills.Registries.ClawHub
	if !ch.Enabled || ch.BaseURL != clawHubDefaultURL {
		t.Errorf("clawhub = {enabled:%v base_url:%q}; want {true %q}", ch.Enabled, ch.BaseURL, clawHubDefaultURL)
	}
	if !cfg.Tools.FindSkills.Enabled || !cfg.Tools.InstallSkill.Enabled {
		t.Errorf("find_skills=%v install_skill=%v; want both true",
			cfg.Tools.FindSkills.Enabled, cfg.Tools.InstallSkill.Enabled)
	}
}

// TestRestoreSkillDiscoveryDefaults_RespectsExplicitDisable confirms an operator
// who explicitly disabled ClawHub / the skill tools keeps that choice — the
// healing logic must NOT override an explicit `"enabled": false`.
func TestRestoreSkillDiscoveryDefaults_RespectsExplicitDisable(t *testing.T) {
	raw := `{
  "version": 1,
  "agents": {"defaults": {}, "list": []},
  "providers": [],
  "channels": {},
  "gateway": {"host": "localhost", "port": 5000},
  "tools": {
    "find_skills": {"enabled": false},
    "install_skill": {"enabled": false},
    "skills": {
      "registries": {
        "clawhub": {"enabled": false, "base_url": "https://example.test"}
      }
    }
  }
}`
	p := writeTempConfig(t, raw)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Tools.Skills.Registries.ClawHub.Enabled {
		t.Errorf("clawhub.Enabled = true; want false (explicit operator disable preserved)")
	}
	if got := cfg.Tools.Skills.Registries.ClawHub.BaseURL; got != "https://example.test" {
		t.Errorf("clawhub.BaseURL = %q; want %q (explicit URL preserved)", got, "https://example.test")
	}
	if cfg.Tools.FindSkills.Enabled {
		t.Errorf("find_skills.Enabled = true; want false (explicit disable preserved)")
	}
	if cfg.Tools.InstallSkill.Enabled {
		t.Errorf("install_skill.Enabled = true; want false (explicit disable preserved)")
	}
}

// TestRestoreSkillDiscoveryDefaults_EmptyBaseURLHealedEvenWhenPresent confirms
// an explicitly-present but empty base_url is healed to the default URL (UI
// parity: empty-as-default), while keeping an explicit enabled choice.
func TestRestoreSkillDiscoveryDefaults_EmptyBaseURLHealedEvenWhenPresent(t *testing.T) {
	raw := `{
  "version": 1,
  "agents": {"defaults": {}, "list": []},
  "providers": [],
  "channels": {},
  "gateway": {"host": "localhost", "port": 5000},
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {"enabled": true, "base_url": ""}
      }
    }
  }
}`
	p := writeTempConfig(t, raw)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	ch := cfg.Tools.Skills.Registries.ClawHub
	if ch.BaseURL != clawHubDefaultURL {
		t.Errorf("clawhub.BaseURL = %q; want %q (empty URL healed)", ch.BaseURL, clawHubDefaultURL)
	}
	if !ch.Enabled {
		t.Errorf("clawhub.Enabled = false; want true (explicit enable preserved)")
	}
}

// TestRestoreSkillDiscoveryDefaults_NoToolsSectionAtAll confirms a config with
// no tools section at all (the minimal datamodel.Init default) still resolves
// to the working skill-discovery defaults.
func TestRestoreSkillDiscoveryDefaults_NoToolsSectionAtAll(t *testing.T) {
	raw := `{
  "version": 1,
  "agents": {"defaults": {}, "list": []},
  "providers": [],
  "channels": {},
  "gateway": {"host": "localhost", "port": 5000}
}`
	p := writeTempConfig(t, raw)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	ch := cfg.Tools.Skills.Registries.ClawHub
	if !ch.Enabled || ch.BaseURL != clawHubDefaultURL {
		t.Errorf("clawhub = {enabled:%v base_url:%q}; want {true %q}", ch.Enabled, ch.BaseURL, clawHubDefaultURL)
	}
	if !cfg.Tools.FindSkills.Enabled || !cfg.Tools.InstallSkill.Enabled {
		t.Errorf("find_skills=%v install_skill=%v; want both true",
			cfg.Tools.FindSkills.Enabled, cfg.Tools.InstallSkill.Enabled)
	}
}

// sanity check that DefaultConfig itself is the source of truth we assert on.
func TestDefaultConfig_SkillDiscoveryDefaults(t *testing.T) {
	def := DefaultConfig()
	b, _ := json.Marshal(def.Tools.Skills.Registries.ClawHub)
	t.Logf("default clawhub: %s", b)
	if !def.Tools.Skills.Registries.ClawHub.Enabled ||
		def.Tools.Skills.Registries.ClawHub.BaseURL != clawHubDefaultURL ||
		!def.Tools.FindSkills.Enabled || !def.Tools.InstallSkill.Enabled {
		t.Fatalf("DefaultConfig skill-discovery defaults regressed")
	}
}
