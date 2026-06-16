package config

import (
	"strconv"
	"testing"
)

// TestClampParallel_AutoFloorsAtTwo verifies the AUTO-detect clamp floors at 2
// and caps at 16 — accidental single-flight on capable hardware is prevented.
func TestClampParallel_AutoFloorsAtTwo(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-5, 2},
		{0, 2},
		{1, 2}, // GAP 3: auto-detected 1 is still raised to 2.
		{2, 2},
		{8, 8},
		{16, 16},
		{17, 16},
		{100, 16},
	}
	for _, c := range cases {
		if got := clampParallel(c.in); got != c.want {
			t.Errorf("clampParallel(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestClampParallelExplicit_HonoursOne verifies that an EXPLICIT user value of 1
// is honoured (single-flight) — only the floor of 1, not 2 (GAP 3).
func TestClampParallelExplicit_HonoursOne(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{1, 1}, // GAP 3: explicit 1 must stay 1, NOT be raised to 2.
		{2, 2},
		{8, 8},
		{16, 16},
		{17, 16},
		{0, 1}, // below floor → floor (1)
		{-3, 1},
	}
	for _, c := range cases {
		if got := clampParallelExplicit(c.in); got != c.want {
			t.Errorf("clampParallelExplicit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestEffectiveMaxParallelAgents_ExplicitOne verifies the end-to-end resolution:
// a user who sets max_parallel_agents=1 gets single-flight (1), not 2 (GAP 3).
func TestEffectiveMaxParallelAgents_ExplicitOne(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // ensure env override is inert
	p := PerformanceConfig{MaxParallelAgents: 1}
	if got := p.EffectiveMaxParallelAgents(); got != 1 {
		t.Fatalf("EffectiveMaxParallelAgents() with explicit 1 = %d, want 1", got)
	}
}

// TestEffectiveMaxParallelAgents_ExplicitCapped verifies an explicit value above
// the ceiling is capped at 16.
func TestEffectiveMaxParallelAgents_ExplicitCapped(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	p := PerformanceConfig{MaxParallelAgents: 99}
	if got := p.EffectiveMaxParallelAgents(); got != 16 {
		t.Fatalf("EffectiveMaxParallelAgents() with explicit 99 = %d, want 16", got)
	}
}

// TestEffectiveMaxParallelAgents_Auto verifies that 0 (auto) returns a value in
// the auto-clamped range [2,16].
func TestEffectiveMaxParallelAgents_Auto(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	p := PerformanceConfig{MaxParallelAgents: 0}
	got := p.EffectiveMaxParallelAgents()
	if got < 2 || got > 16 {
		t.Fatalf("auto EffectiveMaxParallelAgents() = %d, want within [2,16]", got)
	}
}

// TestEffectiveMaxParallelAgents_EnvOverride verifies the env var takes priority
// and is clamped by the explicit (floor-1) rule — env "1" yields single-flight.
func TestEffectiveMaxParallelAgents_EnvOverride(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "1")
	p := PerformanceConfig{MaxParallelAgents: 8} // should be ignored
	if got := p.EffectiveMaxParallelAgents(); got != 1 {
		t.Fatalf("env override 1 = %d, want 1", got)
	}

	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", strconv.Itoa(50))
	if got := p.EffectiveMaxParallelAgents(); got != 16 {
		t.Fatalf("env override 50 = %d, want 16 (capped)", got)
	}
}
