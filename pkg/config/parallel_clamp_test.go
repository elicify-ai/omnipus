package config

import (
	"strconv"
	"testing"
)

// TestEffectiveMaxParallelAgents_ExplicitOne verifies the end-to-end resolution:
// a user who sets max_parallel_agents=1 gets single-flight (1), not 2.
func TestEffectiveMaxParallelAgents_ExplicitOne(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // ensure env override is inert
	p := PerformanceConfig{MaxParallelAgents: 1}
	if got, _ := p.EffectiveMaxParallelAgents(); got != 1 {
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
		if got, _ := p.EffectiveMaxParallelAgents(); got != want {
			t.Fatalf("EffectiveMaxParallelAgents() with explicit %d = %d, want %d (unchanged, no ceiling)", want, got, want)
		}
	}
}

// TestEffectiveMaxParallelAgents_UnsetIsBackstopNotCapacity is the FR-067
// shape assertion, and it replaces a test that asserted the unset case lands
// between a floor of 2 and the physical ceiling.
//
// It asserts the SECOND return value, which is the whole change. The old
// assertion would still pass today — the backstop is inside that bracket —
// while the field's MEANING changed underneath it from "a capacity the system
// computed for you" to "a physical bound on what the Go runtime survives".
// A test that cannot tell those two apart is not testing this.
//
// capped=false is the load-bearing half: it is what tells every caller
// (notably the Settings panel) that the integer alongside it is not a
// recommendation.
func TestEffectiveMaxParallelAgents_UnsetIsBackstopNotCapacity(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	p := PerformanceConfig{MaxParallelAgents: 0}

	got, capped := p.EffectiveMaxParallelAgents()
	if capped {
		t.Fatalf("EffectiveMaxParallelAgents() with nothing configured returned capped=true — capped must be false so callers know %d is a physical backstop, not a capacity claim", got)
	}
	if got != physicalConcurrencySafetyCeiling {
		t.Fatalf("EffectiveMaxParallelAgents() with nothing configured = %d, want %d (the physical OS-thread backstop)", got, physicalConcurrencySafetyCeiling)
	}
	if got == 0 {
		t.Fatal("EffectiveMaxParallelAgents() returned a bare 0 for the unset case — a 0 fed into newTaskExecutor's semaphore capacity deadlocks every dispatch in the process; the two-valued shape exists precisely so 0 is never the answer")
	}
}

// TestEffectiveMaxParallelAgents_ExplicitPathUnchanged pins the OTHER half of
// FR-067: nothing about an explicitly configured value moved. Both the config
// and env routes must report capped=true with the operator's own number.
func TestEffectiveMaxParallelAgents_ExplicitPathUnchanged(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	if got, capped := (PerformanceConfig{MaxParallelAgents: 40}).EffectiveMaxParallelAgents(); got != 40 || !capped {
		t.Fatalf("config 40: EffectiveMaxParallelAgents() = (%d, %v), want (40, true)", got, capped)
	}

	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "50")
	if got, capped := (PerformanceConfig{MaxParallelAgents: 8}).EffectiveMaxParallelAgents(); got != 50 || !capped {
		t.Fatalf("env 50 over config 8: EffectiveMaxParallelAgents() = (%d, %v), want (50, true) — the env override wins and is still an operator's explicit choice", got, capped)
	}
}

// TestEffectiveMaxParallelAgents_EnvOverride verifies the env var takes
// priority and is honored without a ceiling (only the floor-1 rule from
// clampParallelExplicit applies).
func TestEffectiveMaxParallelAgents_EnvOverride(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "1")
	p := PerformanceConfig{MaxParallelAgents: 8} // should be ignored
	if got, _ := p.EffectiveMaxParallelAgents(); got != 1 {
		t.Fatalf("env override 1 = %d, want 1", got)
	}

	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", strconv.Itoa(50))
	if got, _ := p.EffectiveMaxParallelAgents(); got != 50 {
		t.Fatalf("env override 50 = %d, want 50 (no ceiling)", got)
	}
}

// TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections is the
// REQUIRED "an explicit value wins outright -- both directions" test: an
// explicit value both BELOW and ABOVE the unset case's answer must win, never
// falling back to (or being blended with) it.
//
// RE-DERIVED for FR-067. The bracket is now computed against the physical
// OS-thread BACKSTOP rather than against a memory-derived default, because
// there is no longer a computed default to bracket. The property under test
// is unchanged and the assertions are not weakened: both directions are still
// exercised, and both now additionally assert capped=true, which is what
// distinguishes "the operator set this" from "this is the backstop".
func TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")
	backstop, capped := PerformanceConfig{MaxParallelAgents: 0}.EffectiveMaxParallelAgents()
	if capped {
		t.Fatalf("test precondition broken: the unset case reported capped=true (%d)", backstop)
	}

	below := backstop - 1
	if below < 1 {
		below = 1
	}
	if got, gotCapped := (PerformanceConfig{MaxParallelAgents: below}).EffectiveMaxParallelAgents(); got != below || !gotCapped {
		t.Fatalf("explicit value %d below the backstop %d was not honored outright: got (%d, %v), want (%d, true)", below, backstop, got, gotCapped, below)
	}

	above := backstop + 1000
	if got, gotCapped := (PerformanceConfig{MaxParallelAgents: above}).EffectiveMaxParallelAgents(); got != above || !gotCapped {
		t.Fatalf("explicit value %d above the backstop %d was not honored outright: got (%d, %v), want (%d, true)", above, backstop, got, gotCapped, above)
	}
}
