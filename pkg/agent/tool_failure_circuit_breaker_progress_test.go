package agent

// Codex review 2026-09-14 finding #9: the oscillation breaker (D-23) must
// not refuse an agent that is productively monitoring a background job —
// alternating bash poll / bash read where every read returns new output —
// while still refusing a loop whose results never change.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pollReadSigs() (poll, read string) {
	poll = toolCallSignature("bash", map[string]any{"action": "poll", "session_id": "bg-42"})
	read = toolCallSignature("bash", map[string]any{"action": "read", "session_id": "bg-42"})
	return poll, read
}

// A background build that makes progress: the poll says "running" every
// time, the read returns fresh output every time. Twelve full cycles — far
// past oscillationBreakCycles — and the breaker never fires.
func TestToolCircuitBreaker_ProgressingBackgroundJobDoesNotTrip(t *testing.T) {
	ts := &turnState{}
	poll, read := pollReadSigs()
	for cycle := 1; cycle <= 12; cycle++ {
		_, tripped := ts.toolCircuitBreakerTripped(poll)
		require.False(t, tripped, "poll in cycle %d must dispatch", cycle)
		ts.recordToolSuccess(poll)
		assert.Empty(t, ts.recordToolCallOutcomeForLoopDetection(poll, "status: running (pid 4242)"))

		_, tripped = ts.toolCircuitBreakerTripped(read)
		require.False(t, tripped, "read in cycle %d must dispatch — the job is producing new output", cycle)
		ts.recordToolSuccess(read)
		assert.Empty(t, ts.recordToolCallOutcomeForLoopDetection(read, fmt.Sprintf("[build] compiling unit %d of 12", cycle)),
			"no loop notice while every read returns something new")
	}
}

// The same shape with NOTHING changing — poll always "running", read always
// the same stale buffer — is a stalled loop and trips exactly where the
// D-23 oracle says: notice from the third full cycle, refusal of the call
// that would complete the fifth.
func TestToolCircuitBreaker_StalledPollReadLoopStillTrips(t *testing.T) {
	ts := &turnState{}
	poll, read := pollReadSigs()
	notices := 0
	for cycle := 1; cycle <= oscillationBreakCycles-1; cycle++ {
		for _, step := range []struct{ sig, out string }{{poll, "status: running"}, {read, "(no new output)"}} {
			_, tripped := ts.toolCircuitBreakerTripped(step.sig)
			require.False(t, tripped, "cycle %d must still dispatch", cycle)
			ts.recordToolSuccess(step.sig)
			if n := ts.recordToolCallOutcomeForLoopDetection(step.sig, step.out); n != "" {
				notices++
				assert.Contains(t, n, "repeat the same 2-call cycle")
			}
		}
		if cycle < oscillationWarnCycles {
			assert.Equal(t, 0, notices, "no notice before %d identical cycles", oscillationWarnCycles)
		}
	}
	assert.Greater(t, notices, 0, "identical cycles must earn the notice")

	_, tripped := ts.toolCircuitBreakerTripped(poll)
	require.False(t, tripped, "the poll that only STARTS the fifth cycle still runs")
	ts.recordToolSuccess(poll)
	_ = ts.recordToolCallOutcomeForLoopDetection(poll, "status: running")

	reason, tripped := ts.toolCircuitBreakerTripped(read)
	require.True(t, tripped, "the read completing the %dth identical cycle must be refused", oscillationBreakCycles)
	assert.Contains(t, reason, "5th repetition of the same 2-call cycle")
}

// Progress on ONE side of the cycle is progress: the poll never changes but
// the read does. Not a loop.
func TestToolCircuitBreaker_ProgressOnOneSideOfTheCycleIsEnough(t *testing.T) {
	ts := &turnState{}
	poll, read := pollReadSigs()
	for cycle := 1; cycle <= 8; cycle++ {
		_, tripped := ts.toolCircuitBreakerTripped(poll)
		require.False(t, tripped)
		_ = ts.recordToolCallOutcomeForLoopDetection(poll, "status: running")
		_, tripped = ts.toolCircuitBreakerTripped(read)
		require.False(t, tripped, "cycle %d: a read that returns new output is not a repetition", cycle)
		_ = ts.recordToolCallOutcomeForLoopDetection(read, fmt.Sprintf("line %d", cycle))
	}
}

// The result-blind entry point keeps the original semantics (every unknown
// result counts as identical), so the existing loop.go call site is
// neither broken nor silently loosened until it passes real results.
func TestToolCircuitBreaker_ResultBlindRecordingStillTrips(t *testing.T) {
	ts := &turnState{}
	poll, read := pollReadSigs()
	for cycle := 1; cycle <= oscillationBreakCycles-1; cycle++ {
		for _, sig := range []string{poll, read} {
			_, tripped := ts.toolCircuitBreakerTripped(sig)
			require.False(t, tripped)
			_ = ts.recordToolCallForLoopDetection(sig)
		}
	}
	_, tripped := ts.toolCircuitBreakerTripped(poll)
	require.False(t, tripped)
	_ = ts.recordToolCallForLoopDetection(poll)
	_, tripped = ts.toolCircuitBreakerTripped(read)
	assert.True(t, tripped, "without results the detector must behave as before")
}

func TestDetectOscillationAhead(t *testing.T) {
	e := func(sig, res string) string { return loopHistoryEntry(sig, res) }
	same := []string{e("A", "1"), e("B", "1"), e("A", "1"), e("B", "1"), e("A", "1"), e("B", "1"), e("A", "1")}
	p, c := detectOscillationAhead(same, "B")
	assert.Equal(t, 2, p)
	assert.Equal(t, 4, c, "identical results: B would complete the 4th cycle")

	progressing := []string{e("A", "1"), e("B", "1"), e("A", "2"), e("B", "2"), e("A", "3"), e("B", "3"), e("A", "4")}
	_, c = detectOscillationAhead(progressing, "B")
	assert.Less(t, c, oscillationWarnCycles, "changing results never accumulate into a loop")

	_, c = detectOscillationAhead(same, "C")
	assert.Equal(t, 0, c, "a call that breaks the pattern is no repetition at all")
	_, c = detectOscillationAhead(nil, "A")
	assert.Equal(t, 0, c)
}
