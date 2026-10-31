package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: 2026-10-23 bounded cold-identity dispatch and guarded-cold addendum.
// No expected notice ID is assembled from the implementation's key format.
// This is RAM-cold keeper bookkeeping, not a process restart or crash proof.
func TestGoalReminderR3ColdIdentityGuard(t *testing.T) {
	t.Run("ColdRunningReplacementRefuses", r3ColdGuardRunningReplacement)
	t.Run("WarmProducerCapturedBeforeReplacementAccepted", r3ColdGuardWarmProducer)
	t.Run("CompletedFailedProducerColdAllowed", r3ColdGuardCompletedProducer)
}

func r3ColdGuardRunningReplacement(t *testing.T) {
	h, provider, producer, events := r3ColdGuardPaused(t, r2ReplyProviderError)
	r3RealPausedStatus(t, h)
	owner, completeHuman := r3ColdGuardAdmitHuman(t, h, provider)
	replacement := h.load(t)
	require.NotEqual(t, producer.claim.RunID, replacement.ExecutionID.RunID)
	goalBefore := goalRecordForSession(t, h.id)

	resetGoalTriggerStateForTest()
	h.al.SetGoalRouteSessionStore()
	require.Equal(t, goalBefore, goalRecordForSession(t, h.id))
	require.Equal(t, replacement, h.load(t))
	require.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	t.Logf("cold replacement premise: session=%s producer=%s replacement=%s state=%s body_unrun=true", h.id, producer.claim.RunID, owner.claim.RunID, replacement.State)
	historyBefore := h.visibleHistory(t)
	replaysBefore := events.pauseNotices()
	callsBefore := h.root.count()

	err := h.al.pauseGoalReminders(h.id, goalBefore)
	assert.Error(t, err, "cold identity must refuse actual Running H before H resets the old R pause; an older notice is not authority")
	assert.NotErrorIs(t, err, context.Canceled, "this is identity refusal, not a cancelled fixture")
	assert.Equal(t, historyBefore, h.visibleHistory(t), "refused cold publication must neither append under H nor reuse/rewrite an older notice")
	assert.Equal(t, replaysBefore, events.pauseNotices(), "refusal must emit no appended or replayed pause")
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "refused publication cannot accept waiting status")
	assert.Equal(t, goalBefore, goalRecordForSession(t, h.id), "refusal must not count or reset the saved pause")
	assert.Equal(t, replacement, h.load(t), "refusal must not alter the genuine Running replacement")
	assert.Equal(t, callsBefore, h.root.count(), "refusal must not run another provider turn")
	r3ColdGuardEmptyAccounting(t, h)
	grNoPublishedReminder(t, h.al)
	r3ColdGuardUnrunOwner(t, h, owner)
	completeHuman() // SAME admitted H now runs and settles through the normal shell.
}

func r3ColdGuardWarmProducer(t *testing.T) {
	h, provider, producer, events := r3ColdGuardPaused(t, r2ReplyProviderError)
	goalBefore := goalRecordForSession(t, h.id)
	replaysBefore := events.pauseNotices()
	reached, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(release) }) }
	previous := goalReminderSeams.Swap(&goalReminderPassiveSeams{
		goalPauseBeforeWaiting: func(sessionID, goalID string) {
			if sessionID == h.id && goalID == h.goalID {
				reachedOnce.Do(func() { close(reached) })
				<-release
			}
		},
	})
	var pauseErr error
	t.Cleanup(func() {
		unhold()
		r2AwaitPauser(t, done)
		goalReminderSeams.Store(previous)
	})
	go func() {
		defer close(done)
		pauseErr = h.al.pauseGoalReminders(h.id, goalBefore)
	}()
	select {
	case <-reached:
	case <-done:
		t.Fatal("SETUP: warm publication returned before the existing pre-acceptance passive point")
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: warm publication did not reach the existing passive point")
	}

	owner, completeHuman := r3ColdGuardAdmitHuman(t, h, provider)
	replacement := h.load(t)
	require.NotEqual(t, producer.claim.RunID, replacement.ExecutionID.RunID)
	t.Logf("warm PRE ordering: session=%s producer=%s replacement=%s state=%s body_unrun=true", h.id, producer.claim.RunID, owner.claim.RunID, replacement.State)
	historyBefore := h.visibleHistory(t)
	unhold()
	r2AwaitPauser(t, done)
	require.NoError(t, pauseErr, "warm captured producer R must not be rejected by later Running H")
	assert.Equal(t, historyBefore, h.visibleHistory(t), "warm publication must retain the original observed R notice ID/time, never choose H")
	assert.Equal(t, replaysBefore, events.pauseNotices(), "accepting the same warm R pause must not replay it again")
	assert.Equal(t, goalBefore, goalRecordForSession(t, h.id), "admission alone has not executed the human reset")
	assert.Equal(t, replacement, h.load(t))
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID))
	r3ColdGuardUnrunOwner(t, h, owner)
	grNoPublishedReminder(t, h.al)
	completeHuman()
}

func r3ColdGuardCompletedProducer(t *testing.T) {
	h, _, producer, _ := r3ColdGuardPaused(t, r2ReplyEmpty)
	producer.turn.mu.RLock()
	failed := producer.turn.turnFailed
	producer.turn.mu.RUnlock()
	require.True(t, failed, "SETUP: exhausted empty replies must produce a real failed automatic turn")
	producer.owner.mu.Lock()
	ran, turnErr, settlementErr := producer.owner.turnRan, producer.owner.turnErr, producer.owner.result
	producer.owner.mu.Unlock()
	require.True(t, ran)
	require.NoError(t, turnErr, "SETUP: this real failure has nil returned turn error")
	require.NoError(t, settlementErr)
	completed := h.load(t)
	require.Equal(t, session.LifecycleCompleted, completed.State, "SETUP: the actual failed producer really settled Completed")
	require.True(t, completed.Terminal())
	require.False(t, session.LifecycleRecordIsRestartInterrupted(completed))
	require.Equal(t, producer.record.ExecutionID, completed.ExecutionID)
	r3RealPausedStatus(t, h)
	goalBefore := goalRecordForSession(t, h.id)
	callsBefore := h.root.count()
	resetGoalTriggerStateForTest()
	h.al.SetGoalRouteSessionStore()
	require.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	require.Equal(t, completed, h.load(t))
	require.Equal(t, goalBefore, goalRecordForSession(t, h.id))
	t.Logf("cold terminal premise: session=%s producer=%s state=%s turn_failed=true turn_error=nil owner_joined=true", h.id, producer.claim.RunID, completed.State)

	err := h.al.pauseGoalReminders(h.id, goalBefore)
	assert.NoError(t, err, "cold terminal eligibility includes Completed when the actual producer failed; failed-only is wrong")
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID), "eligible cold publication must restore waiting through the actual acceptance path")
	assert.Equal(t, completed, h.load(t))
	assert.Equal(t, goalBefore, goalRecordForSession(t, h.id))
	assert.Equal(t, callsBefore, h.root.count())
	r3ColdGuardEmptyAccounting(t, h)
	grNoPublishedReminder(t, h.al)
	// Exact cold dedup/new-cycle assertions remain in the unchanged 9fabec pack.
	// This control specifically rejects an incorrect Failed-only state filter.
}

func r3ColdGuardPaused(t *testing.T, finalMode int32) (*grHarness, *r2FailureProvider, *grCall, *r3ReviewEvents) {
	t.Helper()
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	events := r3CaptureReviewEvents(t, h)
	grSetStreak(t, h, 2) // Existing separate completed-unproductive boundary, not failure accounting.
	var producer *grCall
	for _, mode := range []int32{r2ReplyProviderError, r2ReplyProviderError, finalMode} {
		producer = r2FailedReminder(t, h, provider, mode)
	}
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	r2AssertFailurePause(t, h, events.pauseNotices)
	require.Len(t, r2FailurePauseRows(t, h), 1)
	require.Len(t, events.pauseNotices(), 1)
	require.Equal(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason)
	return h, provider, producer, events
}

// Admit the real fixture human, stopping at the actual admission/body boundary.
// The returned completion runs the SAME admitted owner, never a replacement or
// a fabricated turn outcome. All owner/result/done writes remain production-owned.
func r3ColdGuardAdmitHuman(t *testing.T, h *grHarness, provider *r2FailureProvider) (*executionDisposition, func()) {
	t.Helper()
	before := h.load(t)
	goalBefore := goalRecordForSession(t, h.id)
	callsBefore := h.root.count()
	msg := h.humanInput(t, "Continue this same goal after the paused reminder cycle.")
	pm := &agentLoopProcessMessage{al: h.al, ctx: h.ctx, msg: msg,
		sessionKey: msg.SessionKey, transcriptSessionID: h.id, transcriptStore: h.al.ResolveSessionStore(h.id)}
	pm.resolveWorkspace()
	pm.prepareTurn() // Production derives human options from the actual inbound fixture.
	ctx, entry := ordinaryExecutionContext(h.ctx)
	preparation, err := h.al.prepareOrdinaryExecution(ctx, msg, pm.opts)
	require.NoError(t, err)
	owner := preparation.execution
	require.NotNil(t, owner)
	entry.disposition = owner
	opts := pm.opts
	opts.executionDisposition = owner
	var once sync.Once
	complete := func() {
		t.Helper()
		once.Do(func() {
			provider.mode.Store(r2ReplySuccess)
			agent, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			run := h.start(func() (reply string, runErr error) {
				// Same direct-producer completion shell as runInboundTurnWithRevival.
				defer func() { runErr = errors.Join(runErr, h.al.finishExecutionDisposition(owner)) }()
				reply, runErr = h.al.runAgentLoop(ctx, agent, opts)
				owner.recordTurnOutcome(runErr)
				return reply, runErr
			})
			call := grAwaitCall(t, h.root, run)
			require.Same(t, owner, call.owner, "cleanup must run the already admitted H owner")
			require.Equal(t, owner.claim, call.claim)
			require.True(t, call.human)
			require.Equal(t, msg.GatewayUserID, call.userID)
			h.root.release(call)
			result := grJoin(t, run)
			grAwaitOwner(t, call)
			require.NoError(t, result.err)
			require.Equal(t, grReply, result.reply)
			owner.mu.Lock()
			settlementErr := owner.result
			owner.mu.Unlock()
			require.NoError(t, settlementErr)
			require.Nil(t, h.al.executionDispositionFor(owner.claim))
			require.Nil(t, h.al.ownLiveTurn(h.id))
			settled := h.load(t)
			require.Equal(t, session.LifecycleCompleted, settled.State)
			require.True(t, owner.claim.matches(settled))
			require.Equal(t, "", goalRecordForSession(t, h.id).LatestReason, "only the actual human body performs reset")
			require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
			require.False(t, h.al.goalIsWaitingOnUser(h.goalID))
			h.root.drainRequests()
			t.Logf("admitted human cleanup: session=%s run=%s receiver_joined=true owner_joined=true retired=true state=%s reset=true", h.id, owner.claim.RunID, settled.State)
		})
	}
	t.Cleanup(complete) // Registered before any subsequent fixture assertion.
	running := h.load(t)
	require.Equal(t, session.LifecycleRunning, running.State)
	require.NotNil(t, running.ExecutionID)
	require.NotEqual(t, before.ExecutionID.RunID, running.ExecutionID.RunID)
	require.Equal(t, before.Generation+1, running.Generation)
	require.True(t, owner.claim.matches(running))
	require.Equal(t, goalBefore, goalRecordForSession(t, h.id), "actual admission precedes the human brake reset")
	require.Equal(t, callsBefore, h.root.count())
	r3ColdGuardUnrunOwner(t, h, owner)
	return owner, complete
}

func r3ColdGuardUnrunOwner(t *testing.T, h *grHarness, owner *executionDisposition) {
	t.Helper()
	require.Same(t, owner, h.al.executionDispositionFor(owner.claim))
	require.Nil(t, h.al.ownLiveTurn(h.id), "H is admitted but its body has not started")
	owner.mu.Lock()
	ran, turnErr := owner.turnRan, owner.turnErr
	owner.mu.Unlock()
	require.False(t, ran, "SETUP: do not disguise a completed/replacement body as the pre-reset admission")
	require.NoError(t, turnErr)
	select {
	case <-owner.done:
		t.Fatal("SETUP: the genuine admitted H owner was already disposed")
	default:
	}
}

func r3ColdGuardEmptyAccounting(t *testing.T, h *grHarness) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	failures, assessed, pending := work.failedReminders, work.lastAssessedTurn, work.pending
	work.mu.Unlock()
	assert.Equal(t, 0, failures, "cold publication is not another failure assessment")
	assert.Equal(t, "", assessed, "cold publication must not fabricate an assessed producing turn")
	assert.Nil(t, pending, "cold publication must not dispatch or invent a pending reminder")
}
