package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A1/R1: a REAL failed automatic turn can finish compute while its full
// producing owner is still blocked on the actual outbound reply tail.
func TestGoalReminderFailedOutcomeWaitsForFullProducerTail(t *testing.T) {
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	live := r2FailureNotices(t, h)
	grSetStreak(t, h, 2)
	for failure := 0; failure < 2; failure++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
	}
	provider.mode.Store(r2ReplyEmpty)
	h.age(t)
	run := h.system(h.reminder(t))
	var call *grCall
	t.Cleanup(func() {
		h.root.openAll()
		r2DrainAndJoinFailedTail(t, h, run)
		if call != nil {
			grAwaitOwner(t, call)
		}
	})
	call = grAwaitCall(t, h.root, run)
	require.False(t, call.human)
	require.NotNil(t, call.owner)
	require.NotNil(t, call.turn)
	r6TailFillOutbound(t, h)
	h.root.release(call)
	r6TailAwaitOpen(t, h, run, call)
	pending := r6WarnPendingOutcome(h.goalID)
	require.NotNil(t, pending)
	require.True(t, pending.failed, "SETUP: this must enter the failed-outcome done branch, not the success helper")
	require.Same(t, call.owner, pending.execution)
	require.Equal(t, call.turn.turnID, pending.turnID)
	call.turn.mu.RLock()
	failed := call.turn.turnFailed
	call.turn.mu.RUnlock()
	require.True(t, failed, "SETUP: exhausted empty replies really failed the actual reminder turn")
	require.Nil(t, h.al.ownLiveTurn(h.id), "SETUP: compute has retired; only the REAL producer tail remains open")
	beforeCalls := h.root.count()
	for tick := 0; tick < 3; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2AssertFailedTailStillOpen(t, h, run, call)
		assert.Same(t, pending, r6WarnPendingOutcome(h.goalID), "an open failed producer must retain the SAME natural pending outcome")
		assert.NotEqual(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason, "the third failure cannot be assessed before its owner is done")
		assert.False(t, h.al.goalIsWaitingOnUser(h.goalID))
		assert.Empty(t, r2FailurePauseRows(t, h))
		assert.Empty(t, live())
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes)
		assert.Equal(t, beforeCalls, h.root.count())
		grNoPublishedReminder(t, h.al)
	}
	select {
	case filler := <-h.al.bus.OutboundChan():
		require.Equal(t, r6TailHoldFiller, filler.Content)
	default:
		t.Fatal("SETUP: actual full open outbound queue lost its held reply boundary")
	}
	result := grJoin(t, run)
	require.NoError(t, result.err, "SETUP: exhausted empty fallback must reach its natural real outbound tail")
	grAwaitOwner(t, call)
	require.NotEmpty(t, result.reply, "SETUP: the released boundary must really be an outbound reply")
	replies := 0
	for _, message := range r2DrainOutbound(t, h.al.bus) {
		if message.Content == result.reply {
			replies++
			assert.Equal(t, h.id, message.SessionID)
		}
	}
	assert.Equal(t, 1, replies, "exactly the held producer's real reply must be released")
	h.root.drainRequests()
	assert.Equal(t, call.record.ExecutionID, h.load(t).ExecutionID)
	assert.Nil(t, h.al.executionDispositionFor(call.claim))
	assert.Same(t, pending, r6WarnPendingOutcome(h.goalID), "owner completion alone is not assessment")
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2NoFourthWake(t, h)
		r2AssertFailurePause(t, h, live)
	}
	assert.Nil(t, r6WarnPendingOutcome(h.goalID))
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes)
}

func r2AssertFailedTailStillOpen(t *testing.T, h *grHarness, run *grPending, call *grCall) {
	t.Helper()
	select {
	case <-run.done:
		t.Fatal("SETUP: producing receiver unexpectedly closed the supposedly held real tail")
	default:
	}
	select {
	case <-call.owner.done:
		t.Fatal("SETUP: actual failed producer owner unexpectedly settled during the held tail")
	default:
	}
	require.Same(t, call.owner, h.al.executionDispositionFor(call.claim))
	require.Equal(t, session.LifecycleRunning, h.load(t).State)
	require.Equal(t, cap(h.al.bus.OutboundChan()), len(h.al.bus.OutboundChan()))
}

func r2DrainAndJoinFailedTail(t *testing.T, h *grHarness, run *grPending) {
	t.Helper()
	watchdog := time.NewTimer(grWatchdog)
	defer watchdog.Stop()
	for {
		select {
		case <-run.done:
			r2DrainOutbound(t, h.al.bus)
			return
		case <-h.al.bus.OutboundChan():
		case call := <-h.root.requests:
			h.root.release(call)
		case <-watchdog.C:
			t.Fatal("SETUP: released actual failed tail did not join during cleanup")
		}
	}
}
