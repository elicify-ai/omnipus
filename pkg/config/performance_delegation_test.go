// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 D9 — the config fold: performance.max_delegation_depth is the
// single source of truth for the onward-delegation depth ceiling (moved off
// agents.defaults.subturn.max_depth). The sibling delegation timeout was
// retired by issue #1306 (delegated work has no time limit).

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
