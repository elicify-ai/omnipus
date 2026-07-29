package testutil

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file tests pollUntilReady — the readiness-polling decision logic
// extracted from StartTestGateway (see gateway_harness.go) — in complete
// isolation from any real gateway, real network, or real wall-clock time.
// All inputs (probe, bootErr, now, sleep) are injected fakes, so every test
// here runs in well under a millisecond regardless of the simulated
// scenario's "elapsed" duration.
//
// Traces to: the CI-blocking test-harness-defect fix task (2026-07-29) —
// "N consecutive failed probes, not wall-clock deadline" — for both
// pkg/agent/testutil/gateway_harness.go (this file) and its TypeScript twin,
// tests/e2e/setup.ts (see tests/e2e/setup.poll.test.ts).

// fakeClock is a deterministic, manually-advanced clock. Advancing it from
// inside a probe/sleep closure simulates variable probe latency, including a
// multi-second "host freeze" during which nothing runs at all.
type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// TestPollUntilReady_HostFreezeThenHealthy_Succeeds is CASE 1 from the task:
// "A simulated host freeze (no probes run for several seconds, then the
// gateway is healthy) passes where it previously failed."
//
// The freeze is modeled as: the first probe attempt only happens after 20s
// of wall-clock time has silently passed (nothing was polling — the host was
// unscheduled), and even that first attempt catches the gateway a moment
// before it starts listening (a transient, single failure — exactly the
// "unlucky" case a bare wall-clock deadline would treat as terminal, since
// 20s already exceeds the historical 15s deadline before a single real probe
// ran). The very next probe, 50ms later, succeeds.
//
// The OLD design (single wall-clock deadline, checked only on failure) would
// fail here: by the time the first post-freeze probe fails, elapsed time
// (20s) has already exceeded any 15s-class deadline, so it would report
// failure immediately without ever giving the gateway a second chance. The
// NEW design must instead succeed, because a freeze produces FEW probes, not
// FAILED ones, and only 1 consecutive failure has been observed — nowhere
// near the 300-consecutive-failure threshold.
func TestPollUntilReady_HostFreezeThenHealthy_Succeeds(t *testing.T) {
	clock := newFakeClock()
	var probeCalls int
	probe := func() probeOutcome {
		probeCalls++
		if probeCalls == 1 {
			clock.Advance(20 * time.Second) // the freeze: no probes ran during this window
			return probeOutcome{err: errors.New("dial tcp 127.0.0.1:12345: connect: connection refused")}
		}
		return probeOutcome{} // healthy on the very next attempt
	}

	result := pollUntilReady(pollConfig{
		probe:                    probe,
		bootErr:                  func() error { return nil },
		interval:                 50 * time.Millisecond,
		consecutiveFailThreshold: 300,
		hardBackstop:             30 * time.Second,
		now:                      clock.Now,
		sleep:                    func(d time.Duration) { clock.Advance(d) },
	})

	require.Equal(t, pollReady, result.kind, "a freeze followed by a healthy probe must succeed regardless of elapsed wall time")
	assert.Equal(t, 2, probeCalls)
	assert.Equal(t, 2, result.attempts)
	// Elapsed reflects the real 20s+ that passed — proving this is NOT a case
	// where the fix "succeeded" by secretly ignoring the freeze; it genuinely
	// tolerated it.
	assert.GreaterOrEqual(t, result.elapsed, 20*time.Second)
}

// TestPollUntilReady_FatalBootError_FailsFast is CASE 2 from the task:
// "A genuine boot failure fails fast and reports the actual boot error, not
// a timeout."
//
// bootErr reports an error on the very first check, before any HTTP probe is
// even attempted. The old design only consulted the boot error once the
// wall-clock deadline had already expired — so even a boot failure that
// happens in milliseconds still burned the full 15s before being reported.
// The new design must fail on the very first iteration, with zero elapsed
// simulated time and zero probe attempts, carrying the real error.
func TestPollUntilReady_FatalBootError_FailsFast(t *testing.T) {
	clock := newFakeClock()
	bootErr := errors.New("fatal: provider credential injection failed")
	var probeCalls int

	result := pollUntilReady(pollConfig{
		probe: func() probeOutcome {
			probeCalls++
			return probeOutcome{err: errors.New("probe should never be reached once boot has fatally errored")}
		},
		bootErr:                  func() error { return bootErr },
		interval:                 50 * time.Millisecond,
		consecutiveFailThreshold: 300,
		hardBackstop:             30 * time.Second,
		now:                      clock.Now,
		sleep: func(time.Duration) {
			t.Fatal("sleep must not be called on a fast-fail boot error")
		},
	})

	require.Equal(t, pollFatalBootError, result.kind)
	assert.Equal(t, 0, probeCalls, "a fatal boot error must short-circuit before ever attempting a health probe")
	assert.Equal(t, 0, result.attempts)
	assert.Equal(t, time.Duration(0), result.elapsed, "must fail before consuming any wall-clock budget")
	assert.Equal(t, bootErr, result.bootErr)
}

// TestPollUntilReady_ConsecutiveFailures_FailsAfterThreshold is the
// "genuine boot failure that never surfaces via bootErr" half of CASE 3
// ("a genuinely wedged gateway still fails, bounded, with a message that
// distinguishes it from case 1"): the health endpoint fails on every fast
// attempt (e.g. the process is up but /health never starts returning 200)
// and RunContext never returns an error. The consecutive-failure threshold —
// not the hard backstop — must be what trips, and it must trip well within
// the hard backstop (proving the two are genuinely distinct signals, not
// the same check wearing two names).
func TestPollUntilReady_ConsecutiveFailures_FailsAfterThreshold(t *testing.T) {
	clock := newFakeClock()
	probeErr := errors.New("health endpoint returned status 503")
	var probeCalls int

	result := pollUntilReady(pollConfig{
		probe: func() probeOutcome {
			probeCalls++
			return probeOutcome{err: probeErr}
		},
		bootErr:                  func() error { return nil },
		interval:                 50 * time.Millisecond,
		consecutiveFailThreshold: 300,
		hardBackstop:             30 * time.Second,
		now:                      clock.Now,
		sleep:                    func(d time.Duration) { clock.Advance(d) },
	})

	require.Equal(t, pollConsecutiveFailures, result.kind)
	assert.Equal(t, 300, probeCalls)
	assert.Equal(t, 300, result.attempts)
	assert.Equal(t, 300, result.consecutiveFails)
	assert.Equal(t, probeErr, result.lastProbeErr)
	// 300 attempts at a 50ms interval == 299 sleeps == 14.95s elapsed —
	// comfortably under the 30s hard backstop, proving the THRESHOLD (not
	// the backstop) is what tripped here.
	assert.Equal(t, 299*50*time.Millisecond, result.elapsed)
	assert.Less(t, result.elapsed, 30*time.Second)
}

// TestPollUntilReady_HardBackstop_FiresForSlowWedgedProbes is the other half
// of CASE 3: a gateway that accepts connections but hangs on every request
// (each probe itself takes ~1s to fail, e.g. a TCP-level or proxy timeout)
// rather than failing fast. At that latency, reaching the 300-consecutive-
// failure threshold would take roughly 5 minutes — far too long to make CI
// wait. The absolute hardBackstop ceiling must trip instead, and it must do
// so after far fewer attempts than the consecutive-failure threshold would
// require — proving it is a genuinely distinct (and reachable) ceiling, not
// a signal that only fires in theory.
func TestPollUntilReady_HardBackstop_FiresForSlowWedgedProbes(t *testing.T) {
	clock := newFakeClock()
	probeErr := errors.New("read tcp 127.0.0.1:54321: i/o timeout")
	var probeCalls int

	result := pollUntilReady(pollConfig{
		probe: func() probeOutcome {
			probeCalls++
			clock.Advance(1 * time.Second) // simulate a hanging request
			return probeOutcome{err: probeErr}
		},
		bootErr:                  func() error { return nil },
		interval:                 50 * time.Millisecond,
		consecutiveFailThreshold: 300, // would need ~300 * 1.05s ≈ 5.25 minutes to reach
		hardBackstop:             30 * time.Second,
		now:                      clock.Now,
		sleep:                    func(d time.Duration) { clock.Advance(d) },
	})

	require.Equal(t, pollHardBackstop, result.kind)
	assert.Less(t, result.attempts, 300, "the backstop, not the consecutive-failure threshold, must be what trips")
	assert.GreaterOrEqual(t, result.elapsed, 30*time.Second)
	assert.Equal(t, probeErr, result.lastProbeErr)
}

// TestPollUntilReady_EventualSuccessAfterTransientFailures_Succeeds is a
// sanity/regression check for the ordinary happy path: a handful of
// connection-refused probes while the listener is still binding, then
// success — the overwhelmingly common real-world case (measured 0.22s p50
// boot cost ≈ 4-5 failed probes at a 50ms interval). This must succeed
// quickly and report an accurate attempt count.
func TestPollUntilReady_EventualSuccessAfterTransientFailures_Succeeds(t *testing.T) {
	clock := newFakeClock()
	const failuresBeforeSuccess = 4
	var probeCalls int

	result := pollUntilReady(pollConfig{
		probe: func() probeOutcome {
			probeCalls++
			if probeCalls <= failuresBeforeSuccess {
				return probeOutcome{err: errors.New("dial tcp: connection refused")}
			}
			return probeOutcome{}
		},
		bootErr:                  func() error { return nil },
		interval:                 50 * time.Millisecond,
		consecutiveFailThreshold: 300,
		hardBackstop:             30 * time.Second,
		now:                      clock.Now,
		sleep:                    func(d time.Duration) { clock.Advance(d) },
	})

	require.Equal(t, pollReady, result.kind)
	assert.Equal(t, failuresBeforeSuccess+1, result.attempts)
	assert.Equal(t, failuresBeforeSuccess*50*time.Millisecond, result.elapsed)
}
