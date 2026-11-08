package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W1/M11: this checkpoint is AFTER final validation, not the old PRE control.
// TryLock chooses a schedule only. Actual selection/control/identity and full
// settlement are the oracle; a watchdog or mutex bit is not a mutation kill.
func TestGoalReminderStopCutSelectsNewAdmission(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State)
	require.NotNil(t, prior.ExecutionID)
	beforeCalls := h.root.count()
	reached, release := make(chan struct{}), make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(release) }) }
	stopped := make(chan struct{})
	stopStarted := false
	var stop StopResult
	var stopErr error
	prev := goalReminderSeams.Swap(&goalReminderPassiveSeams{
		ordinaryAfterGoalValidation: func(sessionID string) {
			if sessionID == h.id {
				reachedOnce.Do(func() { close(reached) })
				<-release
			}
		},
	})
	run := h.system(msg)
	t.Cleanup(func() {
		unhold()
		h.root.openAll()
		grJoin(t, run)
		if stopStarted {
			select {
			case <-stopped:
			case <-time.After(grWatchdog):
				t.Fatal("SETUP: actual Stop did not join before restoring the passive seam")
			}
		}
		goalReminderSeams.Store(prev)
	})
	select {
	case <-reached:
	case <-run.done:
		t.Fatal("SETUP: real admission exited without the approved post-validation point")
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: real admission did not reach the approved post-validation point")
	}
	require.Equal(t, prior.Generation, h.load(t).Generation)
	require.Equal(t, prior.ExecutionID, h.load(t).ExecutionID, "SETUP: boundary must still precede identity mint/revival")
	cut := h.al.steerCanceller().cascadeLock(h.id)
	unprotected := cut.TryLock()
	if unprotected {
		cut.Unlock() // only this test's successful probe, never another owner
	}
	stopStarted = true
	go func() {
		defer close(stopped)
		stop, stopErr = h.al.StopSession(context.Background(), StopRequest{
			SessionID: h.id, Channel: "web", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-reminder-owner"},
		})
	}()
	if unprotected {
		// Lock-only mutant: positively complete Stop on the old terminal tuple
		// before the already-validated admission continues. No elapsed oracle.
		select {
		case <-stopped:
		case <-time.After(grWatchdog):
			t.Fatal("SETUP: unprotected terminal Stop did not finish at the held boundary")
		}
	} else {
		require.Eventually(t, func() bool { return h.al.goalKeeperPausedByStop(h.id) }, grWatchdog, 10*time.Millisecond,
			"SETUP: the genuine Stop request must enter before releasing protected admission")
	}
	unhold()
	r2JoinAdmissionAndStop(t, h, run, stopped)
	assert.NoError(t, stopErr)
	assert.NoError(t, stop.RootErr)
	assert.Empty(t, stop.Report.Unreachable)
	assert.Contains(t, stop.Report.Reached, h.id, "Stop after the protected final validation must reach the newly admitted execution")
	assert.NotContains(t, stop.Report.SkippedTerminal, h.id, "Stop must not be lost against the old terminal predecessor")
	current := h.load(t)
	assert.Equal(t, prior.Generation+1, current.Generation)
	require.NotNil(t, current.ExecutionID)
	assert.NotEqual(t, prior.ExecutionID.RunID, current.ExecutionID.RunID)
	assert.Equal(t, session.LifecycleStopped, current.State, "the real selected admission must fully settle its Stop")
	actualClaim := h.al.executionClaimFor(current)
	selected, ok := stop.Selected[h.id]
	assert.True(t, ok, "Stop must carry an actual selection for the new owner, not a terminal no-op")
	if ok {
		assert.NotEmpty(t, selected.Effect.ControlID)
		assert.Equal(t, actualClaim, claimForStopEffect(h.id, selected.Effect))
	}
	r2AssertStoppedProviderOwners(t, h, beforeCalls, actualClaim, selected, ok)
	assert.Nil(t, h.al.executionDispositionFor(actualClaim))
	assert.Nil(t, h.al.ownLiveTurn(h.id))
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes)
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r2JoinAdmissionAndStop(t *testing.T, h *grHarness, run *grPending, stopped <-chan struct{}) {
	t.Helper()
	done := run.done
	var calls []*grCall
	stopDone := false
	watchdog := time.NewTimer(grWatchdog)
	defer watchdog.Stop()
	for done != nil || stopped != nil {
		select {
		case call := <-h.root.requests:
			calls = append(calls, call)
			if stopDone {
				h.root.release(call)
			}
		case <-stopped:
			stopDone = true
			stopped = nil
			for _, call := range calls {
				h.root.release(call)
			}
		case <-done:
			done = nil
		case <-watchdog.C:
			t.Fatal("SETUP: actual Stop/receiver did not both join after release")
		}
	}
}

func r2AssertStoppedProviderOwners(t *testing.T, h *grHarness, before int, actual executionClaim, selected session.StopSelection, selectedOK bool) {
	t.Helper()
	h.root.mu.Lock()
	calls := append([]*grCall(nil), h.root.calls[before:]...)
	h.root.mu.Unlock()
	// Admission-first permits provider entry before cancellation. Every actual
	// entry must still belong to the selected real owner and be fully settled.
	for _, call := range calls {
		require.NotNil(t, call.owner)
		assert.Equal(t, actual, call.claim)
		assert.Equal(t, actual, call.owner.claim)
		grAwaitOwner(t, call)
		call.owner.mu.Lock()
		binding := call.owner.stop
		call.owner.mu.Unlock()
		assert.NotNil(t, binding, "an entered actual provider owner must retain its accepted Stop")
		if binding != nil && selectedOK {
			assert.Equal(t, selected, binding.selected)
		}
	}
	t.Logf("actual admission provider entries=%d; full receiver/Stop joins observed", len(calls))
}
