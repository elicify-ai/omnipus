// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 D9 — the config fold: performance.max_delegation_depth /
// performance.delegation_timeout_minutes are the single source of truth
// for the onward-delegation depth ceiling and default timeout (moved off
// agents.defaults.subturn.max_depth / default_timeout_minutes).

package config

import (
	"errors"
	"testing"
)

func TestEffectiveMaxDelegationDepth_ZeroIsUnsetNotError(t *testing.T) {
	p := PerformanceConfig{MaxDelegationDepth: 0}
	got, err := p.EffectiveMaxDelegationDepth()
	if err != nil {
		t.Fatalf("EffectiveMaxDelegationDepth(0): %v, want nil error", err)
	}
	if got != 0 {
		t.Fatalf("EffectiveMaxDelegationDepth(0) = %d, want 0 (caller applies its own backstop)", got)
	}
}

func TestEffectiveMaxDelegationDepth_PositiveIsHonored(t *testing.T) {
	p := PerformanceConfig{MaxDelegationDepth: 7}
	got, err := p.EffectiveMaxDelegationDepth()
	if err != nil {
		t.Fatalf("EffectiveMaxDelegationDepth(7): %v", err)
	}
	if got != 7 {
		t.Fatalf("EffectiveMaxDelegationDepth(7) = %d, want 7", got)
	}
}

func TestEffectiveMaxDelegationDepth_NegativeIsRejected(t *testing.T) {
	p := PerformanceConfig{MaxDelegationDepth: -1}
	_, err := p.EffectiveMaxDelegationDepth()
	if !errors.Is(err, ErrPerformanceLimitMisconfigured) {
		t.Fatalf("EffectiveMaxDelegationDepth(-1) error = %v, want ErrPerformanceLimitMisconfigured", err)
	}
}

func TestEffectiveDelegationTimeoutMinutes_ZeroIsUnsetNotError(t *testing.T) {
	p := PerformanceConfig{DelegationTimeoutMinutes: 0}
	got, err := p.EffectiveDelegationTimeoutMinutes()
	if err != nil {
		t.Fatalf("EffectiveDelegationTimeoutMinutes(0): %v, want nil error", err)
	}
	if got != 0 {
		t.Fatalf("EffectiveDelegationTimeoutMinutes(0) = %d, want 0", got)
	}
}

func TestEffectiveDelegationTimeoutMinutes_NegativeIsRejected(t *testing.T) {
	p := PerformanceConfig{DelegationTimeoutMinutes: -5}
	_, err := p.EffectiveDelegationTimeoutMinutes()
	if !errors.Is(err, ErrPerformanceLimitMisconfigured) {
		t.Fatalf("EffectiveDelegationTimeoutMinutes(-5) error = %v, want ErrPerformanceLimitMisconfigured", err)
	}
}
