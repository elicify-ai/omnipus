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

// TestCISeedConfig_LoadsSuccessfully is a regression guard for the incident
// where every e2e shard failed gateway boot with
// "providers[0]: provider is required" for the entire life of a branch,
// invisible because the e2e gate never ran.
//
// C1 (ADR-067 FR-034) deliberately deleted the migrateProviderFields /
// migrateAgentPrimaryProvider "<protocol>/<model>" prefix-splitting
// migration: splitting Model on a stale table of "known" provider slugs
// silently rerouted models whose bare id happened to start with a live
// provider id to the WRONG vendor with no error. Its documented, intended
// consequence is that a providers[] row with no explicit `provider` field
// now fails ModelConfig.Validate() loudly instead.
//
// Several CI config seeders (deploy/ci-worker/runci.sh, .github/workflows/
// pr.yml, .github/workflows/sandbox-uat.yml) were never updated for that
// change and kept writing the old shape — so every gateway they booted
// failed at LoadConfig before a single e2e spec ran, and the e2e gate never
// caught it because it never got past boot.
//
// This test pins the CORRECT shape those three seeders now write — an
// explicit `provider` field alongside a bare (no protocol-prefix) `model`
// id, plus the modern agents.defaults.default_model (provider, model) pair —
// so a future edit that drops `provider` again, or reintroduces the old
// model_name/prefixed-model shape, fails a fast unit test instead of only
// ever failing ten-of-ten e2e shards at boot.
func TestCISeedConfig_LoadsSuccessfully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Byte-identical (modulo the templated port) to the config.json body
	// written by deploy/ci-worker/runci.sh's _e2e_run_shard and by
	// .github/workflows/pr.yml's "Seed gateway config" step.
	const body = `{
  "version": 1,
  "gateway": { "port": 19999, "dev_mode_bypass": true },
  "sandbox": { "audit_log": true, "tool_policies": { "spawn": "allow" } },
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" }, "auto_recap_enabled": true } },
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("the seeded CI config must load without error; got: %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d entries, want 1", len(cfg.Providers))
	}
	row := cfg.Providers[0]
	if row.Provider != "openrouter" {
		t.Errorf("providers[0].Provider = %q, want %q", row.Provider, "openrouter")
	}
	if row.Model != "z-ai/glm-5.2" {
		t.Errorf("providers[0].Model = %q, want %q", row.Model, "z-ai/glm-5.2")
	}
	if err := row.Validate(); err != nil {
		t.Errorf("providers[0].Validate() = %v, want nil", err)
	}

	want := DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5.2"}
	if cfg.Agents.Defaults.DefaultModel != want {
		t.Errorf("agents.defaults.default_model = %+v, want %+v", cfg.Agents.Defaults.DefaultModel, want)
	}
}

// TestCISeedConfig_MissingProviderFieldFailsLoudly is the negative control:
// it proves the fix above is load-bearing by reverting to the OLD, broken
// shape (no explicit `provider`, a "<protocol>/<model>" prefixed `model`)
// and asserting LoadConfig fails with the exact documented error rather than
// silently guessing a provider.
func TestCISeedConfig_MissingProviderFieldFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	const body = `{
  "version": 1,
  "gateway": { "port": 19998, "dev_mode_bypass": true },
  "providers": [
    {
      "model_name": "openrouter-glm",
      "model": "openrouter/z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig must reject a providers[] row with no explicit provider field (ADR-067 FR-034, C1 fix)")
	}
	const wantMsg = "providers[0]: provider is required"
	if err.Error() != wantMsg {
		t.Errorf("LoadConfig error = %q, want %q", err.Error(), wantMsg)
	}
}
