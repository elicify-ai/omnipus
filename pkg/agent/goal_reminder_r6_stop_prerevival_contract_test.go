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

// TestGoalReminderR6StopBeforeRevivalDoesNotRun holds a real reminder after
// its precheck and before revival. An authenticated one-session Stop returns
// while that root is still terminal. The reminder must not reach the provider.
func TestGoalReminderR6StopBeforeRevivalDoesNotRun(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	before := h.root.count()
	beforeRec := h.load(t)
	require.Equal(t, session.LifecycleCompleted, beforeRec.State)

	reached := make(chan struct{})
	release := make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(release) }) }
	var worker *sessionWorker
	workerJoined := false
	stopStarted := false
	stopped := make(chan struct{})
	var got stopResult
	prev := goalReminderSeams.Swap(&goalReminderPassiveSeams{
		ordinaryPreRevival: func(sessionID string) {
			if sessionID != h.id {
				return
			}
			reachedOnce.Do(func() { close(reached) })
			<-release
		},
	})
	t.Cleanup(func() {
		unhold()
		if worker != nil && !workerJoined {
			r6GateJoinWorker(t, h, worker)
		}
		if stopStarted {
			select {
			case <-stopped:
			case <-time.After(grWatchdog):
				t.Fatal("SETUP: the real Stop did not join during cleanup")
			}
		}
		goalReminderSeams.Store(prev)
	})

	require.True(t, h.al.dispatchSystemMessageToSessionWorker(msg, "webchat"), "the reminder must enter the real session worker")
	worker = h.worker(t)
	select {
	case <-reached:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: the reminder did not stop between precheck and revival")
	}
	require.True(t, worker.inTurn.Load(), "SETUP: positively observe the receiver processing this reminder before releasing it")
	require.Equal(t, beforeRec.ExecutionID, h.load(t).ExecutionID, "SETUP: the hold must precede any new execution stamp")

	stopStarted = true
	go func() {
		defer close(stopped)
		got.result, got.err = h.al.StopSession(context.Background(), StopRequest{
			SessionID: h.id, Channel: "web", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-reminder-owner"},
		})
	}()
	select {
	case <-stopped:
		require.NoError(t, got.err, "SETUP: Stop must return while the reminder is held before revival")
		require.NoError(t, got.result.RootErr)
		require.Empty(t, got.result.Report.Unreachable)
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: Stop did not return while the reminder was held before revival")
	}
	require.Equal(t, session.LifecycleCompleted, h.load(t).State, "SETUP: Stop returned on the still-terminal root")
	require.True(t, h.al.goalKeeperPausedByStop(h.id), "SETUP: the authenticated Stop must have paused this goal's keeper")
	unhold()

	calls := r6GateJoinWorker(t, h, worker)
	workerJoined = true
	assert.Len(t, calls, 0, "a Stop that lands before revival must keep the reminder from reaching the provider or a tool")
	assert.Equal(t, before, h.root.count(), "the provider call count must stay at the pre-reminder count")
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "that Stop must not change the reminder streak")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State), "the same goal stays active")
	assert.NotEqual(t, session.LifecycleRunning, h.load(t).State, "the reminder must not be left running past the Stop")
	assert.Equal(t, beforeRec.Generation, h.load(t).Generation, "the held reminder must not revive into a new generation past the accepted Stop")
	assert.Equal(t, beforeRec.ExecutionID, h.load(t).ExecutionID, "the held reminder must not replace the terminal execution after Stop")
	assert.True(t, h.al.goalKeeperPausedByStop(h.id), "the late reminder must not erase Stop's keeper pause")
	assert.True(t, r6GateWorkerIdle(h, worker), "the joined reminder must leave no active owner or turn")
}

type stopResult struct {
	result StopResult
	err    error
}
