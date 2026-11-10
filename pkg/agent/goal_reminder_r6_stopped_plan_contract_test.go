package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6StoppedPlanReleasesGoal is founder R6: only work that is
// running or actually queued to run suppresses the keeper. An approved plan's
// unstarted member is queued. After StopPlan the plan is failed, that member
// stays next, and the owner wake is joined. The pending text reminder must
// then count. The plan row is written through the store, not execute_plan,
// because execute_plan admits the plan before this stop can observe it.
func TestGoalReminderR6StoppedPlanReleasesGoal(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	engine, planID := r6GateApprovedPlan(t, h)

	grSetStreak(t, h, 1)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "an approved plan with a next member is still dispatchable and must suppress the pending reminder")

	stopped, err := engine.StopPlan(h.ctx, planID, "goal-reminder-owner", "web")
	require.NoError(t, err)
	require.Equal(t, plan.StateFailed, stopped.State)
	held := r6GateHoldOwnerWake(t, h, engine, planID)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	// Stop's failed transition writes CompletedAt, and the collector treats that
	// timestamp as progress, so the streak number is not proof of the live wake.
	// Suppression is the unpublished reminder and the still-pending outcome.
	require.True(t, r6GatePendingReminder(h), "the stopped plan's owner wake is a real tail and must keep the pending reminder unassessed until it settles")

	r6GateReleaseOwnerWake(t, h, engine, held)
	member := r6GateRequireStoppedMember(t, h, planID)
	require.Equal(t, task.StatusNext, member.Status, "StopPlan leaves an unstarted member next; that historical row is the case under test")
	require.Nil(t, h.al.ownLiveTurn(h.id), "SETUP: the owner wake must finish before the failed plan is judged")
	busy, why := engine.planWorkInFlight(planID)
	assert.False(t, busy, "a failed plan's unstarted member is not work that can run: %s", why)

	// The joined wake wrote a real reply. That reply is progress, so this settle
	// must assess it: clear the old pending outcome and dispatch the next
	// reminder. A failed plan's unstarted member must not block that.
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	fresh, published := r6GateTakeReminder(h)
	assert.False(t, r6GatePendingReminder(h), "the wake reply is progress and must be assessed, not left pending by a failed plan")
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the wake reply is progress and resets the streak")
	assert.True(t, published, "after the owner wake settles, the failed plan must not keep this goal busy")
	if !published {
		return
	}
	grTextReminder(t, h, fresh)
	grSetStreak(t, h, 1)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "a fresh text reminder with no new progress counts once")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
}

func r6GateTakeReminder(h *grHarness) (bus.InboundMessage, bool) {
	select {
	case msg := <-h.al.bus.InboundChan():
		return msg, true
	default:
		return bus.InboundMessage{}, false
	}
}

func r6GateApprovedPlan(t *testing.T, h *grHarness) (*PlanEngine, string) {
	t.Helper()
	store := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	h.al.SetPlanStore(store)
	GetTaskExecutor(h.al).SetPlanStore(store)
	engine := NewPlanEngine(h.al, store, h.al.taskStore, GetTaskExecutor(h.al))
	h.al.SetPlanEngine(engine)
	created := mustCreatePlan(t, store, &plan.Plan{
		Title: "R6 cap-queued plan", WorkspaceID: testHarnessWorkspaceMembershipID,
		OwnerAgentID: testDefaultAgentID, State: plan.StateApproved, DoD: newFloorDoD(),
		InitiatedBy: &task.Initiator{AgentID: testDefaultAgentID, SessionID: h.id, Depth: 1},
	})
	mustCreateTask(t, h.al.taskStore, &task.Task{
		Title: "R6 unstarted member", WorkspaceID: testHarnessWorkspaceMembershipID,
		PlanID: created.ID, AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	require.True(t, created.PermitsMemberDispatch(), "SETUP: the approved plan must still permit dispatch before Stop")
	return engine, created.ID
}

func r6GateHoldOwnerWake(t *testing.T, h *grHarness, engine *PlanEngine, planID string) *grCall {
	t.Helper()
	var call *grCall
	select {
	case call = <-h.root.requests:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: StopPlan did not reach the owner provider, so the wake was not a real tail")
	}
	require.NotNil(t, call)
	t.Cleanup(func() { h.root.release(call) })
	busy, why := engine.planWorkInFlight(planID)
	require.True(t, busy, "SETUP: the owner wake must be registered before it is released: %s", why)
	return call
}

func r6GateReleaseOwnerWake(t *testing.T, h *grHarness, engine *PlanEngine, call *grCall) {
	t.Helper()
	h.root.release(call)
	joined := make(chan struct{})
	go func() {
		engine.wakeWG.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: the owner wake did not settle after the provider was released")
	}
}

func r6GatePendingReminder(h *grHarness) bool {
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	return work.pending != nil
}

func r6GateRequireStoppedMember(t *testing.T, h *grHarness, planID string) task.Task {
	t.Helper()
	members, err := h.al.taskStore.List(task.Filter{PlanID: planID})
	require.NoError(t, err)
	require.Len(t, members, 1)
	return members[0]
}
