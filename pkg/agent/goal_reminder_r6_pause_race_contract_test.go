package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6HumanReplyBeatsLatePause holds the fifth-outcome pause
// after it has read the paused goal and before it sets waiting. A real human
// turn on the same goal then resumes it. The stale pause must not put the
// waiting flag, overlay or notice back.
func TestGoalReminderR6HumanReplyBeatsLatePause(t *testing.T) {
	h := grNewRecordedBrake(t)
	fifth := h.reminder(t)
	grSetStreak(t, h, 4)
	grTextReminder(t, h, fifth)
	h.age(t)
	sub := h.al.SubscribeEvents(64)
	require.NotEqual(t, uint64(0), sub.ID)
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })

	reached := make(chan struct{})
	release := make(chan struct{})
	settled := make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(release) }) }
	var run *grPending
	settleStarted := false
	prev := goalReminderSeams.Swap(&goalReminderPassiveSeams{
		goalPauseBeforeWaiting: func(sessionID, goalID string) {
			if sessionID != h.id || goalID != h.goalID {
				return
			}
			reachedOnce.Do(func() { close(reached) })
			<-release
		},
	})
	t.Cleanup(func() {
		unhold()
		if run != nil {
			h.root.openAll()
			select {
			case <-run.done:
			case <-time.After(grWatchdog):
				t.Fatal("SETUP: the real human turn did not join during cleanup")
			}
		}
		if settleStarted {
			select {
			case <-settled:
			case <-time.After(grWatchdog):
				t.Fatal("SETUP: the real pauser did not join during cleanup")
			}
		}
		goalReminderSeams.Store(prev)
	})

	settleStarted = true
	go func() {
		h.al.goalQuietWindowSettle(time.Now())
		close(settled)
	}()
	select {
	case <-reached:
	case <-settled:
		t.Fatal("SETUP: the fifth outcome settled without holding the pause")
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: the pause did not stop after reading the goal")
	}
	require.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: the fifth completed outcome must already be recorded")

	run = h.human(t, "Continue working on the goal.")
	call := grAwaitCall(t, h.root, run)
	require.True(t, call.human, "SETUP: the reply must be the real human turn")
	require.Equal(t, h.id, call.sessionID, "SETUP: the human reply must run on the same goal's session")
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: the genuine joined human turn must have resumed the brake before the stale pause is released")
	require.False(t, h.al.goalIsWaitingOnUser(h.goalID), "SETUP: the human turn must not already be waiting at the barrier")
	unhold()
	select {
	case <-settled:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: the pause did not finish after the human reply")
	}

	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the human reply clears the streak and the late pause must not restore it")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "the late pause must not leave the goal waiting")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine), "the late pause must not write the pause notice")
	assert.False(t, r6GateSawWaitingOverlay(sub.C, h.id), "the late pause must not publish the waiting overlay")

	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	select {
	case <-h.al.bus.InboundChan():
	default:
		t.Error("after the human reply the same goal must be eligible for another reminder")
	}
}

func r6GateSawWaitingOverlay(events <-chan Event, sessionID string) bool {
	for _, event := range collectEventStream(events) {
		if event.Kind != EventKindGoalStatusChanged {
			continue
		}
		payload, ok := event.Payload.(GoalStatusChangedPayload)
		if ok && payload.SessionID == sessionID && payload.State == goalPillWaitingOnUser {
			return true
		}
	}
	return false
}
