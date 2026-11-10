package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const r6TailHoldFiller = "r6-tail-hold-filler"

// TestGoalReminderR6PendingOutcomeWaitsForOwnerTail holds the real post-turn
// outbound reply open after a natural reminder completion. The producing owner's
// settlement barrier is still open in that window, so a keeper tick must not
// count or pause. WRITTEN / NOT EXECUTED in the authoring dispatch.
func TestGoalReminderR6PendingOutcomeWaitsForOwnerTail(t *testing.T) {
	h := grNewRecordedBrake(t)
	for completed := 0; completed < 4; completed++ {
		if completed > 0 {
			h.age(t)
		}
		grTextReminder(t, h, h.reminder(t))
	}
	h.age(t)
	fifth := h.reminder(t)
	require.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes, "four settled unproductive reminders are assessed before the fifth runs")
	require.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))

	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State, "the fifth reminder must start from a settled predecessor")
	run := h.system(fifth)
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	require.NotNil(t, call.owner)
	r6TailFillOutbound(t, h)
	h.root.release(call)
	r6TailAwaitOpen(t, h, run, call)

	for tick := 1; tick <= 3; tick++ {
		h.al.goalQuietWindowSettle(time.Now())
		r6TailAssertStillOpen(t, h, run, call, tick)
	}

	r6TailReleaseOutbound(t, h, run)
	grAwaitOwner(t, call)
	settled := h.load(t)
	require.Equal(t, session.LifecycleCompleted, settled.State, "releasing the outbound tail must settle this producing execution")
	require.Equal(t, call.record.ExecutionID, settled.ExecutionID, "settlement must be this fifth execution, not an earlier one")
	assert.Nil(t, h.al.executionDispositionFor(call.claim), "a settled reminder releases its ordinary owner")
	assert.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes, "closing the owner is not itself the assessment")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))

	grAssertFifthPause(t, h)
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID), "the pause waits; it does not end the goal")
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "a second tick must not count the same settled completion again")
	assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "the pause notice is published once")
	grNoPublishedReminder(t, h.al)
}

// r6TailFillOutbound saturates the real outbound bus so the reminder's own
// post-turn reply cannot be published yet. That publish happens only after the
// natural outcome is recorded and before the ordinary owner is finished.
func r6TailFillOutbound(t *testing.T, h *grHarness) {
	t.Helper()
	outbound := h.al.bus.OutboundChan()
	capacity := cap(outbound)
	require.Positive(t, capacity, "the real outbound bus must be able to hold a reply")
	for len(outbound) > 0 {
		<-outbound
	}
	for len(outbound) < capacity {
		require.NoError(t, h.al.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel: "r6-tail-hold", ChatID: "r6-tail-hold", Content: r6TailHoldFiller,
		}))
	}
	require.Equal(t, capacity, len(outbound))
}

func r6TailAwaitOpen(t *testing.T, h *grHarness, run *grPending, call *grCall) {
	t.Helper()
	deadline := time.Now().Add(grWatchdog)
	for time.Now().Before(deadline) {
		select {
		case <-run.done:
			t.Fatalf("BLOCKED: producing reminder returned before its post-outcome outbound tail could be held (reply=%q err=%v)", run.result.reply, run.result.err)
		default:
		}
		select {
		case <-call.owner.done:
			t.Fatal("ordinary owner settled before the outbound tail was held")
		default:
		}
		work := goalReminderWorkFor(h.goalID)
		work.mu.Lock()
		pending := work.pending
		work.mu.Unlock()
		if pending != nil && pending.execution == call.owner && pending.turnID != "" {
			live := h.load(t)
			require.Equal(t, session.LifecycleRunning, live.State, "the producing execution stays running while its reply is unpublished")
			require.NotNil(t, live.ExecutionID)
			require.Equal(t, call.owner.claim.RunID, live.ExecutionID.RunID)
			require.Equal(t, call.owner, h.al.executionDispositionFor(call.claim), "the open barrier must be this reminder's admitted owner")
			require.Equal(t, cap(h.al.bus.OutboundChan()), len(h.al.bus.OutboundChan()), "the real outbound boundary must still be saturated")
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("natural outcome did not become pending while the producing owner's settlement barrier stayed open")
}

func r6TailAssertStillOpen(t *testing.T, h *grHarness, run *grPending, call *grCall, tick int) {
	t.Helper()
	select {
	case <-run.done:
		t.Fatalf("tick %d: producing receiver finished while its outbound tail was supposed to stay open", tick)
	default:
	}
	select {
	case <-call.owner.done:
		t.Fatalf("tick %d: owner settled during a tick that must observe the open tail", tick)
	default:
	}
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	pending := work.pending
	work.mu.Unlock()
	require.NotNil(t, pending, "tick %d: the natural outcome must stay pending", tick)
	require.Equal(t, call.owner, pending.execution, "tick %d: assessment must still be waiting on this producing owner", tick)
	assert.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes, "tick %d: an open producing tail is not a completed reminder", tick)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State), "tick %d: the goal stays active while the tail is open", tick)
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "tick %d: the pause must not arm before the owner settles", tick)
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine), "tick %d: no pause notice while the tail is open", tick)
	assert.Equal(t, session.LifecycleRunning, h.load(t).State)
	grNoPublishedReminder(t, h.al)
}

func r6TailReleaseOutbound(t *testing.T, h *grHarness, run *grPending) {
	t.Helper()
	select {
	case got := <-h.al.bus.OutboundChan():
		require.Equal(t, r6TailHoldFiller, got.Content, "the release must free the saturated outbound bus, not skip ahead to another message")
	case <-time.After(grWatchdog):
		t.Fatal("saturated outbound boundary had no message to release")
	}
	result := grJoin(t, run)
	require.NoError(t, result.err, "the producing reminder must finish once its real reply can be published")
	require.Equal(t, grReply, result.reply)
	replies := 0
	outbound := h.al.bus.OutboundChan()
	for len(outbound) > 0 {
		msg := <-outbound
		if msg.Content == grReply {
			replies++
		}
	}
	require.Equal(t, 1, replies, "the held boundary must be the one real outbound reply of this producing reminder")
	h.root.drainRequests()
}
