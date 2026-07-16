// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — unit tests for ResolveInt (mirroring TestResolveBool_* in
// config_b1_test.go) and EffectiveOrphanedTurnGraceSeconds (ADR-045), added
// during the orphan-foreground-turn watchdog's reap-via-RequestCancel
// redesign review to close a test gap: neither had dedicated coverage.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// ResolveInt helper
// ---------------------------------------------------------------------------

func TestResolveInt_NilPointer_ReturnsDefault(t *testing.T) {
	assert.Equal(t, 300, ResolveInt(nil, 300), "nil *int must resolve to the caller-supplied default")
	assert.Equal(t, 0, ResolveInt(nil, 0), "nil *int with a zero default must resolve to zero")
}

func TestResolveInt_ExplicitZero_ReturnsZeroVerbatim(t *testing.T) {
	// Unlike ResolveBool, ResolveInt must return an explicit 0 verbatim rather
	// than falling back to the default — callers (the orphan-turn watchdog)
	// rely on this to distinguish "operator explicitly disabled" (0) from
	// "operator never set it" (nil).
	zero := 0
	assert.Equal(
		t,
		0,
		ResolveInt(&zero, 300),
		"an explicit *int=0 must be returned verbatim, not replaced by the default",
	)
}

func TestResolveInt_ExplicitNegative_ReturnsNegativeVerbatim(t *testing.T) {
	neg := -1
	assert.Equal(t, -1, ResolveInt(&neg, 300), "an explicit negative *int must be returned verbatim")
}

func TestResolveInt_ExplicitPositive_ReturnsPositiveVerbatim(t *testing.T) {
	v := 42
	assert.Equal(
		t,
		42,
		ResolveInt(&v, 300),
		"an explicit positive *int must be returned verbatim regardless of the default",
	)
}

// ---------------------------------------------------------------------------
// EffectiveOrphanedTurnGraceSeconds (ADR-045)
// ---------------------------------------------------------------------------

func TestEffectiveOrphanedTurnGraceSeconds_NilMeansDefault(t *testing.T) {
	cfg := &Config{}
	// The shipped default is now 0 = watchdog DISABLED: an unset config must
	// never auto-cancel an abandoned turn. Omnipus runs turns as background
	// work — closing a tab keeps the turn running to completion; only an
	// explicit user Stop cancels. Operators opt in with a positive value.
	assert.Equal(t, DefaultOrphanedTurnGraceSeconds, cfg.EffectiveOrphanedTurnGraceSeconds(),
		"unset OrphanedTurnGraceSeconds (nil) must resolve to the default")
	assert.Equal(t, 0, cfg.EffectiveOrphanedTurnGraceSeconds(),
		"the shipped default must be 0 (watchdog disabled — tab-close never cancels)")
}

func TestEffectiveOrphanedTurnGraceSeconds_ExplicitZero_Disabled(t *testing.T) {
	zero := 0
	cfg := &Config{Gateway: GatewayConfig{OrphanedTurnGraceSeconds: &zero}}
	assert.Equal(t, 0, cfg.EffectiveOrphanedTurnGraceSeconds(),
		"an explicit 0 must disable the watchdog (returned verbatim, not the default)")
}

func TestEffectiveOrphanedTurnGraceSeconds_Negative_Disabled(t *testing.T) {
	neg := -5
	cfg := &Config{Gateway: GatewayConfig{OrphanedTurnGraceSeconds: &neg}}
	assert.Equal(t, -5, cfg.EffectiveOrphanedTurnGraceSeconds(),
		"a negative value must be returned verbatim (disabled — callers check <= 0)")
}

func TestEffectiveOrphanedTurnGraceSeconds_ExplicitPositive_Verbatim(t *testing.T) {
	v := 20
	cfg := &Config{Gateway: GatewayConfig{OrphanedTurnGraceSeconds: &v}}
	assert.Equal(t, 20, cfg.EffectiveOrphanedTurnGraceSeconds(),
		"an explicit positive override (e.g. the e2e harness's 20s) must be honored verbatim")
}
