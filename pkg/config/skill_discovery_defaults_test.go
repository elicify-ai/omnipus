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

	ch := clawHubMarketplace(cfg)
	if !ch.Enabled || ch.BaseURL != clawHubDefaultURL {
		t.Errorf("clawhub = {enabled:%v base_url:%q}; want {true %q}", ch.Enabled, ch.BaseURL, clawHubDefaultURL)
	}
	if !cfg.Tools.FindSkills.Enabled || !cfg.Tools.InstallSkill.Enabled {
		t.Errorf("find_skills=%v install_skill=%v; want both true",
			cfg.Tools.FindSkills.Enabled, cfg.Tools.InstallSkill.Enabled)
	}
}

// TestSkillDiscovery_RespectsExplicitDisable confirms an operator who
// explicitly disabled ClawHub / the skill tools via the current config shape
// keeps that choice — no self-heal exists to override it, so a plain
// unmarshal onto DefaultConfig()'s seed must preserve the explicit values.
func TestSkillDiscovery_RespectsExplicitDisable(t *testing.T) {
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
      "marketplaces": [
        {"name": "clawhub", "type": "clawhub", "enabled": false, "base_url": "https://example.test"}
      ]
    }
  }
}`
	p := writeTempConfig(t, raw)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ch := clawHubMarketplace(cfg)
	if ch.Enabled {
		t.Errorf("clawhub.Enabled = true; want false (explicit operator disable preserved)")
	}
	if got := ch.BaseURL; got != "https://example.test" {
		t.Errorf("clawhub.BaseURL = %q; want %q (explicit URL preserved)", got, "https://example.test")
	}
	if cfg.Tools.FindSkills.Enabled {
		t.Errorf("find_skills.Enabled = true; want false (explicit disable preserved)")
	}
	if cfg.Tools.InstallSkill.Enabled {
		t.Errorf("install_skill.Enabled = true; want false (explicit disable preserved)")
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
	ch := clawHubMarketplace(cfg)
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
	ch := clawHubMarketplace(def)
	b, _ := json.Marshal(ch)
	t.Logf("default clawhub marketplace: %s", b)
	if !ch.Enabled ||
		ch.BaseURL != clawHubDefaultURL ||
		!def.Tools.FindSkills.Enabled || !def.Tools.InstallSkill.Enabled {
		t.Fatalf("DefaultConfig skill-discovery defaults regressed")
	}
}

// clawHubMarketplace returns the ClawHub entry from cfg's marketplaces list
// (located by Type=="clawhub"). Returns a zero MarketplaceConfig if no such
// entry exists.
func clawHubMarketplace(cfg *Config) MarketplaceConfig {
	for _, m := range cfg.Tools.Skills.Marketplaces {
		if m.Type == "clawhub" {
			return m
		}
	}
	return MarketplaceConfig{}
}
