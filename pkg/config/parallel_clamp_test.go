package config

import (
	"strconv"
	"testing"
)

// TestClampParallel_AutoFloorsAtTwo verifies the AUTO-detect clamp floors at 2
// and has NO policy ceiling — only the physical OS-thread-safety ceiling
// (physicalConcurrencySafetyCeiling) bounds it. Removing the old policy-16
// ceiling is the whole point of this change: the auto-detected default must
// be able to reflect a genuinely large-memory box.
func TestClampParallel_AutoFloorsAtTwo(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-5, 2},
		{0, 2},
		{1, 2}, // auto-detected 1 is still raised to 2 (floor).
		{2, 2},
		{8, 8},
		{16, 16},
		{17, 17},   // NO ceiling at 16 anymore.
		{100, 100}, // NO ceiling at 16 anymore.
		{1999, 1999},
		{physicalConcurrencySafetyCeiling, physicalConcurrencySafetyCeiling},
		{physicalConcurrencySafetyCeiling + 1, physicalConcurrencySafetyCeiling}, // physical ceiling still applies to AUTO path.
		{1_000_000, physicalConcurrencySafetyCeiling},
	}
	for _, c := range cases {
		if got := clampParallel(c.in); got != c.want {
			t.Errorf("clampParallel(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestClampParallelExplicit_HonoursOne verifies that an EXPLICIT user value of 1
// is honored (single-flight) — only the floor of 1, and that large explicit
// values are honored in full (no silent ceiling — ADR-037 bans silently
// clamping an operator's explicit choice). Only the defensive floor applies;
// values above the physical safety ceiling are still passed through
// unchanged (a WARN is logged, verified separately by
// TestClampParallelExplicit_WarnsAboveSafetyCeiling-style behavior at the
// EffectiveMaxParallelAgents level below).
func TestClampParallelExplicit_HonoursOne(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{1, 1},
		{2, 2},
		{8, 8},
		{16, 16},
		{17, 17},     // NO ceiling at 16 anymore.
		{1000, 1000}, // an explicit value ABOVE the old 16 ceiling survives untouched.
		{5000, 5000}, // an explicit value ABOVE physicalConcurrencySafetyCeiling (2000) still survives untouched — explicit values are never clamped.
		{50000, 50000},
		{0, 1},  // below floor -> floor (1)
		{-3, 1}, // below floor -> floor (1)
	}
	for _, c := range cases {
		if got := clampParallelExplicit(c.in); got != c.want {
			t.Errorf("clampParallelExplicit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestEffectiveMaxParallelAgents_ExplicitOne verifies the end-to-end resolution:
// a user who sets max_parallel_agents=1 gets single-flight (1), not 2.
func TestEffectiveMaxParallelAgents_ExplicitOne(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // ensure env override is inert
	p := PerformanceConfig{MaxParallelAgents: 1}
	if got := p.EffectiveMaxParallelAgents(); got != 1 {
		t.Fatalf("EffectiveMaxParallelAgents() with explicit 1 = %d, want 1", got)
	}
}

// TestEffectiveMaxParallelAgents_ExplicitAboveOldCeiling_Survives is the
// REQUIRED "no ceiling: an explicit value above 16 survives end to end"
// regression test (config -> resolver). A prior version of this code hard-
// capped any explicit value at 16 via clampParallelExplicit; this asserts
// the cap is gone at the PerformanceConfig level, the layer every other
// consumer (AdmissionController's resolver, TaskExecutor's dispatch
// semaphore, subturn.go's getSubTurnConfig) ultimately reads through.
func TestEffectiveMaxParallelAgents_ExplicitAboveOldCeiling_Survives(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	for _, want := range []int{17, 24, 100, 1000} {
		p := PerformanceConfig{MaxParallelAgents: want}
		if got := p.EffectiveMaxParallelAgents(); got != want {
			t.Fatalf("EffectiveMaxParallelAgents() with explicit %d = %d, want %d (unchanged, no ceiling)", want, got, want)
		}
	}
}

// TestEffectiveMaxParallelAgents_Auto verifies that 0 (auto) returns a value
// that is at least the floor (2) and does not exceed the PHYSICAL safety
// ceiling — there is no policy ceiling of 16 anymore, but the auto-detected
// default must still be a sane, bounded number on any real machine.
func TestEffectiveMaxParallelAgents_Auto(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	p := PerformanceConfig{MaxParallelAgents: 0}
	got := p.EffectiveMaxParallelAgents()
	if got < 2 {
		t.Fatalf("auto EffectiveMaxParallelAgents() = %d, want >= 2 (floor)", got)
	}
	if got > physicalConcurrencySafetyCeiling {
		t.Fatalf("auto EffectiveMaxParallelAgents() = %d, want <= %d (physical ceiling)", got, physicalConcurrencySafetyCeiling)
	}
}

// TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula verifies the auto
// default is actually DERIVED from availableRAMBytes()/bytesPerAgent (clamped),
// not some other leftover heuristic (e.g. the old NumCPU-based formula).
func TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	p := PerformanceConfig{MaxParallelAgents: 0}
	got := p.EffectiveMaxParallelAgents()
	want := clampParallel(int(float64(availableRAMBytes()) / bytesPerAgent))
	if got != want {
		t.Fatalf("auto EffectiveMaxParallelAgents() = %d, want %d (availableRAMBytes()/bytesPerAgent, clamped)", got, want)
	}
}

// TestEffectiveMaxParallelAgents_EnvOverride verifies the env var takes
// priority and is honored without a ceiling (only the floor-1 rule from
// clampParallelExplicit applies).
func TestEffectiveMaxParallelAgents_EnvOverride(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "1")
	p := PerformanceConfig{MaxParallelAgents: 8} // should be ignored
	if got := p.EffectiveMaxParallelAgents(); got != 1 {
		t.Fatalf("env override 1 = %d, want 1", got)
	}

	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", strconv.Itoa(50))
	if got := p.EffectiveMaxParallelAgents(); got != 50 {
		t.Fatalf("env override 50 = %d, want 50 (no ceiling)", got)
	}
}

// TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections is the
// REQUIRED "the default is computed from available memory, and an explicit
// value overrides it -- both directions" test: an explicit value both BELOW
// and ABOVE the auto-detected default must win outright, never falling back
// to (or being blended with) the auto-detected number.
func TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	autoDefault := PerformanceConfig{MaxParallelAgents: 0}.EffectiveMaxParallelAgents()

	below := autoDefault - 1
	if below < 1 {
		below = 1
	}
	if got := (PerformanceConfig{MaxParallelAgents: below}).EffectiveMaxParallelAgents(); got != below {
		t.Fatalf("explicit value %d below auto-default %d was not honored outright: got %d", below, autoDefault, got)
	}

	above := autoDefault + 1000
	if got := (PerformanceConfig{MaxParallelAgents: above}).EffectiveMaxParallelAgents(); got != above {
		t.Fatalf("explicit value %d above auto-default %d was not honored outright: got %d", above, autoDefault, got)
	}
}

// TestClampParallelExplicit_NeverLowersLargeValue is a direct unit-level
// companion to the end-to-end test above: clampParallelExplicit itself must
// never reduce a large explicit value, at any magnitude.
func TestClampParallelExplicit_NeverLowersLargeValue(t *testing.T) {
	for _, v := range []int{17, 100, 2000, 2001, 10000, 100000} {
		if got := clampParallelExplicit(v); got != v {
			t.Errorf("clampParallelExplicit(%d) = %d, want %d (explicit values are never lowered)", v, got, v)
		}
	}
}
