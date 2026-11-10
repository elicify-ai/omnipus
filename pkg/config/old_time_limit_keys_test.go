// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOldTimeLimitKeysAreIgnoredOnLoad is issue #1306's backward-compatibility
// guarantee at the config boundary. An install written before the time limits
// were removed still has agents.defaults.timeout_seconds,
// performance.delegation_timeout_minutes and schedules.run_timeout_seconds in
// its config.json. The new binary must LOAD that file — never refuse it (a
// config the runtime refused made the gateway unbootable in #1293) — keep
// every other value, and drop the retired keys on the next save.
func TestOldTimeLimitKeysAreIgnoredOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "version": 1,
  "agents": {"defaults": {"timeout_seconds": 900, "max_tool_iterations": 42}},
  "performance": {"max_delegation_depth": 4, "delegation_timeout_minutes": 30},
  "schedules": {"max_concurrent_runs": 3, "run_timeout_seconds": 300}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(config with retired time-limit keys) = %v; the new binary must boot with them", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 42 || cfg.Performance.MaxDelegationDepth != 4 || cfg.Schedules.MaxConcurrentRuns != 3 {
		t.Fatalf("sibling values lost while ignoring the retired keys: max_tool_iterations=%d depth=%d runs=%d",
			cfg.Agents.Defaults.MaxToolIterations, cfg.Performance.MaxDelegationDepth, cfg.Schedules.MaxConcurrentRuns)
	}

	if serr := SaveConfig(path, cfg); serr != nil {
		t.Fatalf("SaveConfig: %v", serr)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if uerr := json.Unmarshal(saved, &doc); uerr != nil {
		t.Fatalf("saved config is not JSON: %v", uerr)
	}
	section := func(keys ...string) map[string]any {
		cur := doc
		for _, k := range keys {
			next, _ := cur[k].(map[string]any)
			cur = next
		}
		return cur
	}
	for _, c := range []struct {
		path []string
		key  string
	}{
		{[]string{"agents", "defaults"}, "timeout_seconds"},
		{[]string{"performance"}, "delegation_timeout_minutes"},
		{[]string{"schedules"}, "run_timeout_seconds"},
	} {
		if _, present := section(c.path...)[c.key]; present {
			t.Errorf("saved config still carries retired key %s.%s", strings.Join(c.path, "."), c.key)
		}
	}
}
