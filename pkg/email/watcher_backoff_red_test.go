package email

// RED (round 8, silent-failure-hunter F1 — HIGH). Oracle: MC-33 / FR-037 /
// B-43 — per-mailbox exponential backoff 60 s → 2 → 4 … cap 15 min with ±20%
// jitter. EVERY attempt from 1 upward must return a POSITIVE delay inside the
// jitter bounds; attempts deep into the schedule (the finding names 29+, i.e.
// shift 28+) must sit in the capped range [0.8×cap, 1.2×cap], never negative.
//
// HEAD's `watcherBackoffBase << shift` overflows time.Duration at shift ≥ 28:
// the delay goes NEGATIVE, the `d > cap` guard cannot catch it, and
// recordFailure persists a NextAttemptAt in the past — the mailbox then dials
// on every tick forever, exactly the hammering MC-33 exists to prevent, with
// no log line anywhere.
//
// Expected values below are derived from the spec ladder only (60 s → 2 → 4
// … cap 15 min, factor 0.8 + 0.4×randUnit), never from the implementation:
// attempt n at shift n-1 doubles 60 s up to shift 3 (8 min ≤ cap); shift 4
// (16 min) exceeds the 15 min cap, so attempt ≥ 5 is capped. randUnit 0.5 is
// the jitter midpoint (factor exactly 1.0 — exact-value oracle); 0 and 1 are
// the ±20% bounds.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// wantBackoffBase derives the spec ladder value for one attempt at the jitter
// midpoint: 60 s → 2 → 4 → 8 min, then the 15 min cap (MC-33).
func wantBackoffBase(attempt int) time.Duration {
	const capD = 15 * time.Minute
	d := 60 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > capD {
			return capD
		}
	}
	if d > capD {
		return capD
	}
	return d
}

func TestWatcherBackoff_AllAttemptsPositiveAndCapped(t *testing.T) {
	const capD = 15 * time.Minute
	t.Run("exact ladder at the jitter midpoint", func(t *testing.T) {
		for _, attempt := range []int{1, 2, 3, 4, 5, 6, 28, 29, 30, 200} {
			got := WatcherBackoff(attempt, "timeout", 0.5)
			require.Equal(t, wantBackoffBase(attempt), got,
				"attempt %d must sit exactly on the MC-33 ladder (60s→2→4→8min→15min cap) at the jitter midpoint", attempt)
		}
	})

	t.Run("jitter bounds at 0 and 1", func(t *testing.T) {
		for _, attempt := range []int{1, 2, 4, 5, 29, 200} {
			base := wantBackoffBase(attempt)
			require.Equal(t, base-base/5, WatcherBackoff(attempt, "timeout", 0),
				"attempt %d randUnit=0 must be the -20%% jitter bound", attempt)
			require.Equal(t, base+base/5, WatcherBackoff(attempt, "timeout", 1),
				"attempt %d randUnit=1 must be the +20%% jitter bound", attempt)
		}
	})

	t.Run("property sweep attempts 1..200: positive and within cap jitter bounds", func(t *testing.T) {
		for attempt := 1; attempt <= 200; attempt++ {
			d := WatcherBackoff(attempt, "timeout", 0.5)
			require.Greater(t, d, time.Duration(0),
				"attempt %d: backoff must never go negative (negative ⇒ NextAttemptAt in the past ⇒ dial every tick)", attempt)
			require.LessOrEqual(t, d, capD,
				"attempt %d: backoff must never exceed the 15 min cap", attempt)
		}
	})

	t.Run("auth_failed sits at the cap for every attempt", func(t *testing.T) {
		for _, attempt := range []int{1, 5, 29, 200} {
			got := WatcherBackoff(attempt, "auth_failed", 0.5)
			require.Equal(t, capD, got,
				"attempt %d auth_failed must wait at the 15 min cap", attempt)
		}
	})
}
