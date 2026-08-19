// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"runtime"
	"strings"
	"testing"
)

// TestDefaultAllowedExecPaths_ShapeInvariants guards the seed against the two
// mistakes that would make it dangerous rather than useful.
func TestDefaultAllowedExecPaths_ShapeInvariants(t *testing.T) {
	seed := DefaultAllowedExecPaths()

	// The seed is macOS-only BY DESIGN, and this test used to assert
	// non-emptiness unconditionally — so it passed on the machine it was written
	// on and failed on Linux, where the implementation deliberately returns nil.
	// The invariant below is the real one: wherever a seed exists it must be
	// narrow. Whether one exists at all is a per-platform decision.
	//
	// Linux stays empty on purpose. Under ADR-062's open model execution is
	// unrestricted, so an exec-path seed has nothing left to fix there; under
	// confined it keeps today's behaviour, which means upgrading a Linux install
	// does not silently widen its Landlock posture.
	if runtime.GOOS == "darwin" && len(seed) == 0 {
		t.Fatal("macOS seed must not be empty; without it agents cannot run any " +
			"Homebrew- or version-manager-installed toolchain under the confined model")
	}

	for _, p := range seed {
		// A bare $HOME grant would expose ~/.ssh, ~/Documents and every other
		// private directory. Each version manager must be named individually.
		if p == "~" || p == "~/" {
			t.Errorf("seed grants execute on the whole home directory: %q", p)
		}
		if p == "/" {
			t.Errorf("seed grants execute on the filesystem root")
		}
		// /usr/local and /opt/homebrew must be narrowed to subdirectories:
		// their etc/ and var/ hold private keys and service state.
		if p == "/usr/local" || p == "/opt/homebrew" {
			t.Errorf("seed grants a whole Homebrew prefix %q; narrow it to bin/lib/Cellar/... so etc/ stays closed", p)
		}
		if strings.HasSuffix(p, "/etc") || strings.HasSuffix(p, "/var") {
			t.Errorf("seed exposes a configuration/state directory: %q", p)
		}
	}
}

// TestLoadConfig_SeededExecPathsSurviveExistingConfig pins the mechanism that
// makes this fix reach EXISTING installs rather than only fresh ones.
//
// loadConfig starts from DefaultConfig() and unmarshals the operator's JSON on
// top, so a key absent from an older config.json keeps the seeded value. If
// that ever changed, every machine upgrading into this version would silently
// keep an empty exec list and agents would still be unable to run node.
func TestLoadConfig_SeededExecPathsSurviveExistingConfig(t *testing.T) {
	// A config.json written before allowed_exec_paths existed.
	legacy := []byte(`{"version":1,"sandbox":{"mode":"enforce"}}`)

	cfg, err := loadConfig(legacy)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	// filesystem_model is seeded non-empty on EVERY platform, so it probes the
	// survival mechanism itself rather than a value that happens to exist on
	// the machine running the test. Asserting only on allowed_exec_paths made
	// this test silently platform-specific: it proved the mechanism on macOS
	// and merely failed on Linux, where the seed is intentionally empty.
	if cfg.Sandbox.FilesystemModel == "" {
		t.Error("a seeded key must survive a config.json that predates it; " +
			"if this breaks, every upgrading install silently loses its seeded defaults")
	}
	if runtime.GOOS == "darwin" && len(cfg.Sandbox.AllowedExecPaths) == 0 {
		t.Error("seeded exec paths must survive a config.json that predates the key")
	}
}

// The operator must remain able to turn the feature off entirely. An explicit
// empty list is a deliberate choice and must be honoured, not re-seeded.
func TestLoadConfig_ExplicitEmptyExecPathsIsHonoured(t *testing.T) {
	explicit := []byte(`{"version":1,"sandbox":{"allowed_exec_paths":[]}}`)

	cfg, err := loadConfig(explicit)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Sandbox.AllowedExecPaths) != 0 {
		t.Errorf("explicit empty list must be honoured, got %v", cfg.Sandbox.AllowedExecPaths)
	}
}

// An operator-supplied list must fully replace the seed, not merge with it.
func TestLoadConfig_OperatorExecPathsReplaceSeed(t *testing.T) {
	custom := []byte(`{"version":1,"sandbox":{"allowed_exec_paths":["/opt/mytools"]}}`)

	cfg, err := loadConfig(custom)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Sandbox.AllowedExecPaths) != 1 || cfg.Sandbox.AllowedExecPaths[0] != "/opt/mytools" {
		t.Errorf("operator list must replace the seed, got %v", cfg.Sandbox.AllowedExecPaths)
	}
}
