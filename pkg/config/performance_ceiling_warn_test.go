// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"testing"
	"time"
)

// TestShouldLogExplicitCeilingWarn_Throttles is the regression test for the
// 2026-08-04 code review MINOR at config.go:487-493: clampParallelExplicit's
// above-physical-ceiling WARN used to fire unconditionally on every call, and
// EffectiveMaxParallelAgents (which calls it) is invoked on every new-session
// admission check, every dispatch capacity sync, and every
// GET /api/v1/performance — a hot, repeated path that could flood
// gateway.log. shouldLogExplicitCeilingWarn is the throttle gate split out
// from clampParallelExplicit specifically so this can be tested with a fake
// clock, without depending on capturing real logger output.
//
// NOTE ON SCOPE, since the surrounding machinery changed underneath this test:
// the AUTO-DETECT path this warning used to sit beside is deleted (there is no
// longer a computed default). The EXPLICIT path is untouched —
// clampParallelExplicit still honours any operator value in full and still
// warns loudly above physicalConcurrencySafetyCeiling rather than lowering it,
// which is the ADR-037 rule this whole throttle exists to serve. This test and
// its subject are unchanged deliberately.
func TestShouldLogExplicitCeilingWarn_Throttles(t *testing.T) {
	old := lastExplicitCeilingWarnNano.Load()
	lastExplicitCeilingWarnNano.Store(0)
	t.Cleanup(func() { lastExplicitCeilingWarnNano.Store(old) })

	base := time.Now()

	if !shouldLogExplicitCeilingWarn(base) {
		t.Fatal("first call must log (nothing logged yet)")
	}
	if shouldLogExplicitCeilingWarn(base.Add(time.Second)) {
		t.Fatal("a call 1s later (well within explicitCeilingWarnInterval) must NOT log again")
	}
	if shouldLogExplicitCeilingWarn(base.Add(explicitCeilingWarnInterval - time.Second)) {
		t.Fatal("a call 1s before the interval elapses must still NOT log")
	}
	if !shouldLogExplicitCeilingWarn(base.Add(explicitCeilingWarnInterval + time.Second)) {
		t.Fatal("a call after explicitCeilingWarnInterval has elapsed must log again")
	}
}

// TestShouldLogExplicitCeilingWarn_ConcurrentCallsLogOnlyOnce verifies the
// CompareAndSwap claim prevents concurrent callers from all logging at once
// — clampParallelExplicit is reachable from concurrent session-admission
// checks (EffectiveMaxParallelAgents' own doc comment), so the throttle must
// be safe under real concurrency, not just single-goroutine sequencing.
func TestShouldLogExplicitCeilingWarn_ConcurrentCallsLogOnlyOnce(t *testing.T) {
	old := lastExplicitCeilingWarnNano.Load()
	lastExplicitCeilingWarnNano.Store(0)
	t.Cleanup(func() { lastExplicitCeilingWarnNano.Store(old) })

	now := time.Now()
	const goroutines = 50
	results := make(chan bool, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			results <- shouldLogExplicitCeilingWarn(now)
		}()
	}
	trueCount := 0
	for i := 0; i < goroutines; i++ {
		if <-results {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Fatalf("exactly one concurrent caller should win the throttle slot, got %d", trueCount)
	}
}

// TestClampParallelExplicit_AboveCeiling_StillHonorsValue verifies the
// throttle change didn't alter clampParallelExplicit's actual return value
// (only whether it logs) — an explicit value above the physical safety
// ceiling must still be honored exactly as configured (never silently
// clamped), independent of the WARN throttle's state.
func TestClampParallelExplicit_AboveCeiling_StillHonorsValue(t *testing.T) {
	old := lastExplicitCeilingWarnNano.Load()
	lastExplicitCeilingWarnNano.Store(0)
	t.Cleanup(func() { lastExplicitCeilingWarnNano.Store(old) })

	const aboveCeiling = physicalConcurrencySafetyCeiling + 500
	got := clampParallelExplicit(aboveCeiling)
	if got != aboveCeiling {
		t.Fatalf("clampParallelExplicit(%d) = %d, want %d (explicit values are never silently clamped, even when throttled)", aboveCeiling, got, aboveCeiling)
	}
}
