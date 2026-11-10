// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

// Backend-lead's own tests for the #904 review-gate fix to the env import
// (spec D6: copy once, never overwrite an admin value; WARNs once per boot).
// Expected values come from D6/D13, not from the implementation.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// bootLoadLogFile routes pkg/logger to a temp file for the test.
func bootLoadLogFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "boot-load.log")
	prev := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	if err := logger.EnableFileLogging(p); err != nil {
		t.Fatalf("enable file logging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prev)
	})
	return p
}

func countLogLinesContaining(t *testing.T, path, needle string) int {
	t.Helper()
	raw, _ := os.ReadFile(path)
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}

// A refresh load (a later load of the same path in this process — what runs
// right after every REST or tool config write) must not import the env var,
// even when the marker is missing on disk: the value on disk is the admin's.
func TestMaxToolIterationsEnvImport_RefreshNeverOverwritesAdminValue(t *testing.T) {
	t.Setenv(MaxToolIterationsEnvVar, "80")
	path, _ := writeMaxToolIterConfig(t, `"max_tool_iterations": 200`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("boot load: %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 80 {
		t.Fatalf("boot load global = %d, want 80 (imported once)", got)
	}

	// An admin value lands on disk without the marker (the shape an older
	// generic config write left behind).
	adminBytes := []byte(maxToolIterConfigJSON(`"max_tool_iterations": 50`))
	if writeErr := os.WriteFile(path, adminBytes, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("refresh load: %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 50 {
		t.Errorf("refresh global = %d, want 50 — the env var must not overwrite the admin value", got)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(adminBytes) {
		t.Errorf("refresh load rewrote config.json:\n%s", after)
	}
}

// D13 WARN and the "env ignored" WARN: once per boot, not on every refresh.
func TestMaxToolIterationsWarnings_OncePerBoot(t *testing.T) {
	logFile := bootLoadLogFile(t)
	t.Setenv(MaxToolIterationsEnvVar, "80")
	path, _ := writeMaxToolIterConfig(t,
		`"max_tool_iterations": 5000, "max_tool_iterations_env_imported": true`)
	for i := 0; i < 3; i++ {
		if _, err := LoadConfig(path); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}
	if n := countLogLinesContaining(t, logFile, "saved global limit is outside 1-1000"); n != 1 {
		t.Errorf("D13 WARN lines = %d over 3 loads, want 1 (boot only)", n)
	}
	if n := countLogLinesContaining(t, logFile, MaxToolIterationsEnvVar+" is ignored"); n != 1 {
		t.Errorf("env-ignored WARN lines = %d over 3 loads, want 1 (boot only)", n)
	}
}

// Fresh install: the env value and the marker are persisted together by the
// first struct save, so a later boot never re-imports.
func TestMaxToolIterationsEnvImport_FreshInstallPersistsMarker(t *testing.T) {
	t.Setenv(MaxToolIterationsEnvVar, "80")
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 80 || !cfg.Agents.Defaults.MaxToolIterationsEnvImported {
		t.Fatalf("fresh install in memory: global=%d marker=%v, want 80/true",
			cfg.Agents.Defaults.MaxToolIterations, cfg.Agents.Defaults.MaxToolIterationsEnvImported)
	}
	if saveErr := SaveConfig(path, cfg); saveErr != nil {
		t.Fatalf("first save: %v", saveErr)
	}
	d := readDefaultsMap(t, path)
	if d["max_tool_iterations"] != float64(80) || d["max_tool_iterations_env_imported"] != true {
		t.Fatalf("first save must persist the env value WITH the marker; defaults=%v", d)
	}

	t.Setenv(MaxToolIterationsEnvVar, "90")
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("next load: %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 80 {
		t.Errorf("global = %d after the marker was persisted, want 80 (never re-imported)", got)
	}
}
