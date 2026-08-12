// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestLoadConfig_UpgradingInstallPicksUpOpenModel pins the mechanism that makes
// the ADR-060 default reach EXISTING installs, which is the part with the real
// consequence.
//
// loadConfig unmarshals the operator's JSON over DefaultConfig(), so a
// config.json written before this key existed inherits "open" on the next boot.
// That is intended: leaving existing installs on confined would mean the bug
// stays unfixed for exactly the people already running the product. But it IS a
// security-posture change on upgrade, so it is asserted deliberately rather than
// left as an emergent property of the loader that nobody is watching.
//
// These live in pkg/config rather than pkg/gateway because loadConfig is
// unexported; exporting it purely to let a gateway test reach it would widen the
// package API for a test's convenience.
func TestLoadConfig_UpgradingInstallPicksUpOpenModel(t *testing.T) {
	legacy := []byte(`{"version":1,"sandbox":{"mode":"enforce"}}`)

	cfg, err := loadConfig(legacy)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := cfg.Sandbox.FilesystemModel; got != string(FilesystemModelOpen) {
		t.Errorf("a config.json predating filesystem_model got %q, want %q — "+
			"without this the fix never reaches anyone who already runs Omnipus", got, FilesystemModelOpen)
	}
}

// TestLoadConfig_ExplicitConfinedIsHonoured: the seed is DATA, never a fallback
// branch in the binary. An operator who wants the old posture must be able to
// keep it, and must not find it silently re-seeded on the next boot.
func TestLoadConfig_ExplicitConfinedIsHonoured(t *testing.T) {
	explicit := []byte(`{"version":1,"sandbox":{"filesystem_model":"confined"}}`)

	cfg, err := loadConfig(explicit)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := cfg.Sandbox.FilesystemModel; got != string(FilesystemModelConfined) {
		t.Errorf("explicit confined must be honoured, got %q", got)
	}
}

// TestLoadConfig_UnknownModelSurvivesLoadAndFailsAtBoot documents WHERE a typo is
// caught, because the answer is not obvious and the wrong assumption is unsafe.
//
// loadConfig does not validate this key — the value is a plain string in the
// config struct. Rejection happens at applySandbox via
// sandbox.ParseFilesystemModel, which aborts boot. That is deliberate: config
// loading is used in contexts that must tolerate odd files, while the sandbox
// decision must never resolve a typo to a posture the operator did not choose.
func TestLoadConfig_UnknownModelSurvivesLoadAndFailsAtBoot(t *testing.T) {
	typo := []byte(`{"version":1,"sandbox":{"filesystem_model":"opne"}}`)

	cfg, err := loadConfig(typo)
	if err != nil {
		t.Fatalf("loadConfig must not reject the value itself: %v", err)
	}
	if cfg.Sandbox.FilesystemModel != "opne" {
		t.Errorf("loadConfig should preserve the raw value for the sandbox layer to reject, got %q",
			cfg.Sandbox.FilesystemModel)
	}
}
