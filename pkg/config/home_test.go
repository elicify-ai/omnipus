// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg"
)

func TestOmnipusHomeDir_HonorsEnvOverride(t *testing.T) {
	t.Setenv(EnvHome, "/opt/custom-omnipus")

	got := OmnipusHomeDir()
	if got != "/opt/custom-omnipus" {
		t.Errorf("OmnipusHomeDir() with env override = %q, want %q", got, "/opt/custom-omnipus")
	}
}

func TestOmnipusHomeDir_FallsBackToHomeDirDotOmnipus(t *testing.T) {
	t.Setenv(EnvHome, "")

	got := OmnipusHomeDir()
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir failed on this host; skipping: %v", err)
	}
	want := filepath.Join(userHome, ".omnipus")
	if got != want {
		t.Errorf("OmnipusHomeDir() with empty env = %q, want %q", got, want)
	}
}

func TestOmnipusHomeDir_EmptyEnvIsTreatedAsUnset(t *testing.T) {
	// An empty OMNIPUS_HOME must NOT be returned verbatim — otherwise callers
	// that join ".omnipus" onto it would end up writing to the process cwd.
	t.Setenv(EnvHome, "")

	got := OmnipusHomeDir()
	if got == "" {
		t.Fatal("OmnipusHomeDir() returned empty string for empty env; expected fallback")
	}
	if !strings.Contains(got, ".omnipus") && !strings.Contains(got, "omnipus-") {
		t.Errorf("OmnipusHomeDir() fallback = %q, expected it to contain \".omnipus\" or a temp prefix", got)
	}
}

func TestOmnipusHomeDir_RelativeOverrideIsResolvedAgainstCWD(t *testing.T) {
	// A relative OMNIPUS_HOME used to be returned "trusted verbatim", which
	// meant runtime files (cost.json, state.json) would land inside the
	// source tree when the gateway was started from the repo root with
	// `OMNIPUS_HOME=pkg/gateway` (or any other relative override). The
	// resolution must always return an absolute path, resolved against the
	// process CWD at call time, so the home directory is stable across CWD
	// changes and the cost-tracker points at the right location.
	t.Setenv(EnvHome, "relative/override")

	got := OmnipusHomeDir()
	if !filepath.IsAbs(got) {
		t.Errorf("OmnipusHomeDir() with relative env = %q, want absolute path", got)
	}
	// The absolute path should be the CWD joined with the relative override,
	// cleaned of any double-separator or trailing-slash artifacts.
	cwd, err := os.Getwd()
	if err != nil {
		t.Skipf("Getwd failed on this host; skipping: %v", err)
	}
	want := filepath.Clean(filepath.Join(cwd, "relative", "override"))
	if got != want {
		t.Errorf("OmnipusHomeDir() with relative env = %q, want %q", got, want)
	}
}

func TestOmnipusHomeDir_RelativeOverrideIsCleaned(t *testing.T) {
	// Even an "absolute-looking" relative path with redundant separators
	// must be cleaned before returning, so downstream filepath.Join calls
	// don't produce paths with "//" or trailing slashes.
	t.Setenv(EnvHome, "rel/./with/../trailing/")

	got := OmnipusHomeDir()
	if !filepath.IsAbs(got) {
		t.Errorf("OmnipusHomeDir() with messy relative env = %q, want absolute path", got)
	}
	if strings.Contains(got, "//") || strings.Contains(got, "/./") {
		t.Errorf("OmnipusHomeDir() returned un-cleaned path %q", got)
	}
}

// The following tests prove that DefaultConfig() (pkg/config/defaults.go)
// and the workspace-defaulting logic in loadConfigInternal
// (pkg/config/config.go) — the two other home-directory-resolution call
// sites — now route through OmnipusHomeDir() instead of reimplementing home
// resolution themselves, and therefore agree with it (and each other) on the
// edge cases where the three used to diverge: a relative $OMNIPUS_HOME
// (previously trusted verbatim, unresolved, in both reimplementations) and a
// missing $HOME (previously silently produced an empty/relative workspace
// path in config.go's reimplementation, with no secure-temp-dir fallback).

func TestDefaultConfig_WorkspaceAgreesWithOmnipusHomeDir_RelativeOverride(t *testing.T) {
	t.Setenv(EnvHome, "relative/testhome-defaults")

	wantHome := OmnipusHomeDir()
	want := filepath.Join(wantHome, pkg.WorkspaceName)

	cfg := DefaultConfig()
	if !filepath.IsAbs(cfg.Agents.Defaults.Home) {
		t.Fatalf("DefaultConfig().Agents.Defaults.Home = %q, want an absolute path", cfg.Agents.Defaults.Home)
	}
	if cfg.Agents.Defaults.Home != want {
		t.Errorf(
			"DefaultConfig().Agents.Defaults.Home = %q, want %q (OmnipusHomeDir()+workspace)",
			cfg.Agents.Defaults.Home,
			want,
		)
	}
}

func TestLoadConfig_WorkspaceDefaultAgreesWithOmnipusHomeDir_RelativeOverride(t *testing.T) {
	t.Setenv(EnvHome, "relative/testhome-loadconfig")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// No agents.defaults.workspace set, so loadConfigInternal's
	// workspace-defaulting branch (config.go) computes it itself.
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	// Relative-override resolution is a pure CWD join with no side effects,
	// so calling OmnipusHomeDir() again yields the identical value used
	// internally by loadConfigInternal.
	wantHome := OmnipusHomeDir()
	want := filepath.Join(wantHome, pkg.WorkspaceName)

	if !filepath.IsAbs(cfg.Agents.Defaults.Home) {
		t.Fatalf("cfg.Agents.Defaults.Home = %q, want an absolute path", cfg.Agents.Defaults.Home)
	}
	if cfg.Agents.Defaults.Home != want {
		t.Errorf(
			"cfg.Agents.Defaults.Home = %q, want %q (OmnipusHomeDir()+workspace)",
			cfg.Agents.Defaults.Home,
			want,
		)
	}
}

func TestDefaultConfigAndLoadConfig_AgreeOnSecureTempFallback_WhenHomeDirFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME is not the home-dir env var on Windows (USERPROFILE); os.UserHomeDir failure path differs")
	}
	// Force os.UserHomeDir() to fail (on Unix it only reads $HOME) and ensure
	// no explicit override is set, so both call sites must fall through to
	// OmnipusHomeDir()'s secure-temp-dir fallback.
	t.Setenv("HOME", "")
	t.Setenv(EnvHome, "")

	// DefaultConfig() call site.
	cfg := DefaultConfig()
	defaultsWorkspace := cfg.Agents.Defaults.Home
	if !filepath.IsAbs(defaultsWorkspace) {
		t.Fatalf(
			"DefaultConfig().Agents.Defaults.Home = %q, want an absolute path even when $HOME is unset",
			defaultsWorkspace,
		)
	}
	if !strings.Contains(defaultsWorkspace, "omnipus-") {
		t.Errorf(
			"DefaultConfig().Agents.Defaults.Home = %q, want it under a secure omnipus- temp dir fallback",
			defaultsWorkspace,
		)
	}
	if filepath.Base(defaultsWorkspace) != pkg.WorkspaceName {
		t.Errorf(
			"DefaultConfig().Agents.Defaults.Home = %q, want basename %q",
			defaultsWorkspace,
			pkg.WorkspaceName,
		)
	}

	// loadConfigInternal's workspace-defaulting branch (config.go) call site.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	loadedWorkspace := loaded.Agents.Defaults.Home
	if !filepath.IsAbs(loadedWorkspace) {
		t.Fatalf(
			"cfg.Agents.Defaults.Home = %q, want an absolute path even when $HOME is unset (previously fell back to a bare relative %q)",
			loadedWorkspace,
			pkg.WorkspaceName,
		)
	}
	if !strings.Contains(loadedWorkspace, "omnipus-") {
		t.Errorf(
			"cfg.Agents.Defaults.Home = %q, want it under a secure omnipus- temp dir fallback",
			loadedWorkspace,
		)
	}
	if filepath.Base(loadedWorkspace) != pkg.WorkspaceName {
		t.Errorf("cfg.Agents.Defaults.Home = %q, want basename %q", loadedWorkspace, pkg.WorkspaceName)
	}
}
