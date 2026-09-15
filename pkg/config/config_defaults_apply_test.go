// config_defaults_apply_test.go: tests for effective-value accessors, defaults and normalisation for loaded config sections (performance clamp, schedules, search-tool API keys, browser/delegate/judge caps)

package config

import (
	"testing"
	"time"
)

// --- moved from config.go tests 2026-09-15 ---

func TestMergeAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		apiKeys  []string
		expected []string
	}{
		{
			name:     "both empty",
			apiKey:   "",
			apiKeys:  nil,
			expected: nil,
		},
		{
			name:     "only ApiKey",
			apiKey:   "key1",
			apiKeys:  nil,
			expected: []string{"key1"},
		},
		{
			name:     "only ApiKeys",
			apiKey:   "",
			apiKeys:  []string{"key1", "key2"},
			expected: []string{"key1", "key2"},
		},
		{
			name:     "both with overlap",
			apiKey:   "key1",
			apiKeys:  []string{"key1", "key2", "key3"},
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "with whitespace",
			apiKey:   "  key1  ",
			apiKeys:  []string{"  key2  ", "  key1  "},
			expected: []string{"key1", "key2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeAPIKeys(tt.apiKey, tt.apiKeys)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d keys, got %d", len(tt.expected), len(result))
			}
			for i, k := range result {
				if k != tt.expected[i] {
					t.Errorf("expected key[%d] = %q, got %q", i, tt.expected[i], k)
				}
			}
		})
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
