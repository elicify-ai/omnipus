//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unit tests for Sprint-J sandbox-apply wiring — mode resolution, legacy
// Enabled mapping, CLI > config precedence, and production-off nag banner.
// Kernel-level Apply/Install coverage lives in pkg/sandbox (subprocess
// test) and tests/security (E2E harness).

package gateway

import (
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestResolveMode_CLIBeatsConfig verifies FR-J-006: --sandbox=off with
// config.Sandbox.Mode="enforce" resolves to off. CLI always wins.
func TestResolveMode_CLIBeatsConfig(t *testing.T) {
	mode, src, err := resolveMode("off", "enforce", true, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if mode != sandbox.ModeOff {
		t.Errorf("got mode=%q, want off", mode)
	}
	if src != "cli_flag" {
		t.Errorf("got src=%q, want cli_flag", src)
	}
}

// TestResolveMode_ConfigDefault verifies fallback to config value when
// CLI flag is not set.
func TestResolveMode_ConfigDefault(t *testing.T) {
	mode, src, err := resolveMode("", "off", true, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if mode != sandbox.ModeOff {
		t.Errorf("got mode=%q, want off", mode)
	}
	if src != "config" {
		t.Errorf("got src=%q, want config", src)
	}
}

// TestResolveMode_FreshConfigDefaultsToEnforce verifies that a truly
// empty config (Mode unset, no AllowedPaths) defaults to enforce on
// capable kernels. configTouched=false means the operator has not
// written anything to the sandbox section.
func TestResolveMode_FreshConfigDefaultsToEnforce(t *testing.T) {
	mode, src, err := resolveMode("", "", false, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveMode: %v", err)
	}
	if mode != sandbox.ModeEnforce {
		t.Errorf("got mode=%q, want enforce", mode)
	}
	if src != "" {
		t.Errorf("got src=%q, want empty (default)", src)
	}
}

// TestResolveMode_InvalidCLIReturnsError verifies FR-J-006 second
// sentence: invalid --sandbox values surface as errors so cmd/omnipus
// can exit 2 (usage error) before any boot logic.
func TestResolveMode_InvalidCLIReturnsError(t *testing.T) {
	_, _, err := resolveMode("of", "", false, func(string) string { return "" })
	if err == nil {
		t.Fatal("resolveMode: expected error for --sandbox=of")
	}
	if !strings.Contains(err.Error(), "invalid sandbox mode") {
		t.Errorf("error message must identify the bad value; got %q", err.Error())
	}
}

// TestResolveMode_InvalidConfigReturnsError verifies that a malformed
// gateway.sandbox.mode in the config file is also rejected, not
// silently treated as default.
func TestResolveMode_InvalidConfigReturnsError(t *testing.T) {
	_, _, err := resolveMode("", "bogus", true, func(string) string { return "" })
	if err == nil {
		t.Fatal("resolveMode: expected error for config Mode=bogus")
	}
	if !strings.Contains(err.Error(), "gateway.sandbox.mode") {
		t.Errorf("error message must include the config path prefix; got %q", err.Error())
	}
}

// TestApplySandbox_OffModeDoesNotCallApply verifies that when mode is
// resolved to off, applySandbox does not invoke Apply/Install — just
// logs the decision and returns a no-enforcement result. This is the
// dev-override path (FR-J-006).
func TestApplySandbox_OffModeDoesNotCallApply(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "off"

	tmp, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create temp stderr: %v", err)
	}
	defer tmp.Close()

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "",
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(),
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}
	if result.Mode != sandbox.ModeOff {
		t.Errorf("got mode=%q, want off", result.Mode)
	}
	if result.ApplyState.LandlockEnforced {
		t.Error("mode=off must not report LandlockEnforced=true")
	}
	if result.ApplyState.SeccompEnforced {
		t.Error("mode=off must not report SeccompEnforced=true")
	}
}

// TestApplySandbox_ProductionOffBanner verifies FR-J-011: mode=off +
// OMNIPUS_ENV=production fires the multi-line warning banner to stderr.
// Uses a temp file in lieu of stderr so the Fprint output can be read
// back after applySandbox returns.
func TestApplySandbox_ProductionOffBanner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "off"

	tmp, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create temp stderr: %v", err)
	}
	defer tmp.Close()

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(),
		GetEnv: func(k string) string {
			if k == "OMNIPUS_ENV" {
				return "production"
			}
			return ""
		},
		Stderr: tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}
	if result.NagReason != "production_off" {
		t.Errorf("NagReason: got %q, want production_off", result.NagReason)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		t.Fatalf("sync stderr temp: %v", syncErr)
	}
	body, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("read stderr temp: %v", err)
	}
	if !strings.Contains(string(body), "SANDBOX DISABLED IN PRODUCTION") {
		t.Errorf("stderr must contain production banner; got %q", string(body))
	}
}

// TestApplySandbox_FreshInstall_Boots verifies CRIT-1 fix: a fresh install
// with no sandbox section in config.json (Mode=="", configTouched==false)
// resolves to enforce and boots without error. This exercises the
// CLI > config > default precedence rule end-to-end with a real fresh-install
// config snapshot and a FallbackBackend (so the test runs on any kernel).
//
// Before the CRIT-1 fix, this path triggered the boot-abort:
//
//	applied=enforce, config-reported=off → mismatch → SandboxBootError
func TestApplySandbox_FreshInstall_Boots(t *testing.T) {
	// cfg with zero-value Sandbox section — simulates a first-run config
	// that has no sandbox stanza at all. Mode is empty, AllowedPaths is nil,
	// so configTouched=false and resolveMode returns (enforce, "", nil).
	cfg := &config.Config{}
	// Confirm both conditions that trigger the old mismatch.
	if cfg.Sandbox.Mode != "" {
		t.Fatalf("precondition: cfg.Sandbox.Mode must be empty; got %q", cfg.Sandbox.Mode)
	}
	if len(cfg.Sandbox.AllowedPaths) != 0 {
		t.Fatalf("precondition: cfg.Sandbox.AllowedPaths must be nil; got %v", cfg.Sandbox.AllowedPaths)
	}

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "", // no CLI override
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(), // avoids kernel dependency
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox on fresh install must not return an error; got: %v", err)
	}
	// Fresh install with capable default resolves to enforce.
	if result.Mode != sandbox.ModeEnforce {
		t.Errorf("fresh install must resolve to enforce; got %q", result.Mode)
	}
}

// TestApplySandbox_CLIEnforceEmptyConfig_Boots verifies the second broken
// scenario from CRIT-1: --sandbox=enforce with an empty config must boot.
// Before the fix: applied=enforce, config-reported=off → mismatch → abort.
func TestApplySandbox_CLIEnforceEmptyConfig_Boots(t *testing.T) {
	cfg := &config.Config{} // empty sandbox section

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "enforce", // explicit CLI flag
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(),
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox with --sandbox=enforce and empty config must not error; got: %v", err)
	}
	if result.Mode != sandbox.ModeEnforce {
		t.Errorf("mode: got %q, want enforce", result.Mode)
	}
}

// TestApplySandbox_CLIOffEnforceConfig_Boots verifies the third broken
// scenario from CRIT-1: --sandbox=off with config.Mode=enforce must boot
// (e.g. operator debugging). Before the fix: applied=off,
// config-reported=enforce → mismatch → abort.
func TestApplySandbox_CLIOffEnforceConfig_Boots(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	tmp, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatalf("create temp stderr: %v", err)
	}
	defer tmp.Close()

	result, err := applySandbox(SandboxApplyOptions{
		CLIMode:  "off", // CLI override: operator debugging
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(),
		GetEnv:   func(string) string { return "" },
		Stderr:   tmp,
	})
	if err != nil {
		t.Fatalf("applySandbox with --sandbox=off over enforce config must not error; got: %v", err)
	}
	if result.Mode != sandbox.ModeOff {
		t.Errorf("mode: got %q, want off", result.Mode)
	}
}

// TestIsRunningInDocker_DockerenvPresent verifies that isRunningInDocker returns
// true when /.dockerenv (overridden via dockerenvPath) exists on the filesystem.
//
// Regression for fix #3: previously os.Stat errors other than ENOENT were
// silently treated as "not in Docker"; this test ensures the positive detection
// path works when the file is present.
func TestIsRunningInDocker_DockerenvPresent(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), ".dockerenv-*")
	if err != nil {
		t.Fatalf("create temp dockerenv: %v", err)
	}
	tmp.Close()

	// Override the probe path for this test, restore after.
	orig := dockerenvPath
	dockerenvPath = tmp.Name()
	t.Cleanup(func() { dockerenvPath = orig })

	got := isRunningInDocker(func(string) string { return "" })
	if !got {
		t.Errorf("isRunningInDocker: got false, want true (/.dockerenv-equiv exists at %q)", tmp.Name())
	}
}

// TestIsRunningInDocker_DockerenvAbsent verifies that isRunningInDocker returns
// false when neither OMNIPUS_IN_DOCKER nor /.dockerenv are present.
//
// Regression for fix #3: the positive ENOENT path must not log a warning and
// must return false cleanly.
func TestIsRunningInDocker_DockerenvAbsent(t *testing.T) {
	// Point dockerenvPath at a path that definitely does not exist.
	orig := dockerenvPath
	dockerenvPath = t.TempDir() + "/.dockerenv-nonexistent"
	t.Cleanup(func() { dockerenvPath = orig })

	got := isRunningInDocker(func(string) string { return "" })
	if got {
		t.Errorf("isRunningInDocker: got true, want false (file does not exist)")
	}
}

// TestApplySandbox_FallbackBackendNoSeccompInstall verifies FR-J-014:
// when the selected backend is not a LinuxBackend, seccomp Install is
// NOT invoked even with mode=enforce. Seccomp is strictly gated on
// LinuxBackend selection — both-or-neither, never seccomp-alone.
func TestApplySandbox_FallbackBackendNoSeccompInstall(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	result, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(), // explicit fallback injection
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}
	// With fallback backend, applySandbox takes the degraded path.
	if result.ApplyState.LandlockEnforced {
		t.Error("FallbackBackend must not report LandlockEnforced=true")
	}
	if result.ApplyState.SeccompEnforced {
		t.Error("FallbackBackend must not report SeccompEnforced=true")
	}
	// The extra notes must mention graceful degradation so operators
	// know why the requested enforce mode didn't actually install.
	found := false
	for _, note := range result.ApplyState.ExtraNotes {
		if strings.Contains(note, "kernel does not support Landlock") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected degradation note in ExtraNotes; got %v", result.ApplyState.ExtraNotes)
	}
}
