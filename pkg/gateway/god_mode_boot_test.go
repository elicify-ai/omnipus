//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unit tests for resolveAllowGodMode (O14): the single boot-time computation
// that combines the legacy --allow-god-mode CLI flag with the
// config-persisted sandbox.god_mode_allowed grant into one availability
// decision. Pure function, no gateway boot required.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveAllowGodMode_ConfigGrant_TrueEvenWithFlagOff proves the core
// claim of the UI-driven enablement design: a config-persisted
// sandbox.god_mode_allowed=true grant resolves to allowGodMode=true on the
// NEXT boot, even though the legacy --allow-god-mode CLI flag was not passed.
func TestResolveAllowGodMode_ConfigGrant_TrueEvenWithFlagOff(t *testing.T) {
	cfg := &config.Config{
		Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: true},
	}
	assert.True(t, resolveAllowGodMode(false /* cliFlag */, cfg),
		"sandbox.god_mode_allowed=true must grant availability even when --allow-god-mode was not passed")
}

// TestResolveAllowGodMode_FlagOnly proves the legacy CLI flag alone still
// grants availability, with no config authorization at all — back-compat for
// headless/CI boots.
func TestResolveAllowGodMode_FlagOnly(t *testing.T) {
	cfg := &config.Config{}
	assert.True(t, resolveAllowGodMode(true /* cliFlag */, cfg),
		"--allow-god-mode must grant availability regardless of config")
}

// TestResolveAllowGodMode_NeitherGrant proves the fail-closed default: no
// flag and no config grant resolves to false.
func TestResolveAllowGodMode_NeitherGrant(t *testing.T) {
	cfg := &config.Config{Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: false}}
	assert.False(t, resolveAllowGodMode(false, cfg),
		"neither --allow-god-mode nor sandbox.god_mode_allowed must resolve to unavailable")
}

// TestResolveAllowGodMode_NilConfig proves the defensive nil-cfg path only
// consults the CLI flag and never panics.
func TestResolveAllowGodMode_NilConfig(t *testing.T) {
	assert.False(t, resolveAllowGodMode(false, nil))
	assert.True(t, resolveAllowGodMode(true, nil))
}

// TestResolveAllowGodMode_BothGrants proves the OR is inclusive — both
// sources present still resolves to true (no double-negative surprise).
func TestResolveAllowGodMode_BothGrants(t *testing.T) {
	cfg := &config.Config{Sandbox: config.OmnipusSandboxConfig{GodModeAllowed: true}}
	assert.True(t, resolveAllowGodMode(true, cfg))
}
