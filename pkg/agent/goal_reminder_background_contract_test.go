package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R6 fixtures use the real process registry with no OS process, and the real
// task/plan stores and ownership stamps. A fake PID is never killed; all
// process records are removed before AgentLoop.Close's process cleanup.
func grBrakeBackgroundCommands(t *testing.T) {
	for _, ownerClass := range []string{"Root", "CompletedDescendant"} {
		t.Run(ownerClass, func(t *testing.T) {
			h := grNewRecordedBrake(t)
			owner := h.id
			if ownerClass == "CompletedDescendant" {
				run := h.human(t, "Review this work before continuing.")
				call := grAwaitCall(t, h.root, run)
				child := h.launchReviewer(t, "background-shell-owner")
				require.NotNil(t, child)
				owner = child.SessionID
				h.finish(t, run, call)
			}
			manager := tools.GetSharedSessionManager()
			id := "reminder-bg-" + owner
			manager.Add(&tools.ProcessSession{ID: id, OwnerSessionID: owner, Background: true, Status: tools.StatusRunning})
			t.Cleanup(func() { manager.Remove(id) })
			require.Equal(t, 1, manager.CountRunningBackgroundCommands(owner), "SETUP: exact owner has a real registered running background command")
			if ownerClass == "CompletedDescendant" {
				// This reviewer really completed AFTER the goal boundary. Its
				// positive progress may reset the streak while its process is
				// busy; it must not publish a reminder or start another execution.
				grSetStreak(t, h, 1)
				h.age(t)
				before := h.load(t)
				rootCalls, reviewerCalls := h.root.count(), h.reviewer.count()
				h.al.goalQuietWindowSettle(time.Now())
				grNoPublishedReminder(t, h.al)
				assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "fresh real completed helper activity resets progress even while its background command remains busy")
				assert.Equal(t, before, h.load(t), "observing helper progress must not start another root execution")
				assert.Equal(t, rootCalls, h.root.count())
				assert.Equal(t, reviewerCalls, h.reviewer.count())
				require.Equal(t, 1, manager.CountRunningBackgroundCommands(owner))
			}
			// For the descendant this seed is AFTER its completed activity was
			// observed. The same no-output busy work must not reset it again.
			grSetStreak(t, h, 1) // below both the old lifetime-two cap and the R6 brake: only genuine busy suppression can pass
			h.age(t)
			before := h.load(t)
			h.assertNoKeeperWake(t, "an owned running background command after its foreground turn returned")
			assert.Equal(t, before, h.load(t), "a busy goal must not acquire another execution")
		})
	}
}

func grBrakeUnrelatedBackground(t *testing.T) {
	for _, kind := range []string{"SameAgentOtherSession", "OtherWorkspace", "ForegroundNotBackground", "FinishedBeforeGoal"} {
		t.Run(kind, func(t *testing.T) {
			h := grNewR6Harness(t)
			h.completedRoot(t)
			owner := h.id
			if kind == "SameAgentOtherSession" {
				owner = newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
			}
			if kind == "OtherWorkspace" {
				provisionTestWorkspace(t, "r6-other-workspace", testDefaultAgentID, grReviewerID)
				owner = newTestSteeringSession(t, h.al, "r6-other-workspace")
			}
			ps := grRegisteredProcess(t, owner, kind != "ForegroundNotBackground")
			if kind == "FinishedBeforeGoal" {
				ps.SetStatus(tools.StatusDone)
				require.True(t, ps.IsDone())
			}
			h.arm(t)
			grTextReminder(t, h, h.reminder(t))
			grSetStreak(t, h, 1)
			h.age(t)
			msg := h.reminder(t)
			assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "unrelated/foreground/old-terminal work neither suppresses dispatch nor resets the actual completed text-only outcome")
			grTextReminder(t, h, msg)
		})
	}
}

func grBrakeLiveHelper(t *testing.T) {
	h := grNewRecordedBrake(t)
	run := h.human(t, "Keep an independent reviewer working.")
	parent := grAwaitCall(t, h.root, run)
	launcher := NewSteerLauncher(h.al)
	launched, err := launcher.Launch(h.ctx, steer.LaunchRequest{
		SteeringSessionID: h.id, TargetAgentID: grReviewerID, Task: "Continue independent review.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "live-helper-suppression"},
	})
	require.NoError(t, err)
	dispatched, err := launcher.Dispatch(h.ctx, launched.SessionID, launched.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	var child *grCall
	select {
	case child = <-h.reviewer.requests:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: dispatched helper did not execute")
	}
	require.Equal(t, launched.SessionID, child.sessionID)
	require.NotNil(t, child.turn)
	assert.Equal(t, h.id, string(child.turn.routingSessionID), "helper must be in the actual root's routing tree")
	h.finish(t, run, parent)
	require.Nil(t, h.al.ownLiveTurn(h.id), "SETUP: foreground root has returned; only its helper remains live")
	grSetStreak(t, h, 1) // below both the old lifetime-two cap and the R6 brake: only genuine busy suppression can pass
	h.age(t)
	h.assertNoKeeperWake(t, "a genuinely running delegated descendant")
	h.reviewer.release(child)
	grAwaitOwner(t, child)
}

func grBrakeQueuedHelper(t *testing.T) {
	h := grNewRecordedBrake(t)
	run := h.human(t, "Queue an independent reviewer.")
	parent := grAwaitCall(t, h.root, run)
	launched, err := NewSteerLauncher(h.al).Launch(h.ctx, steer.LaunchRequest{
		SteeringSessionID: h.id, TargetAgentID: grReviewerID, Task: "Queued review work.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "queued-helper-suppression"},
	})
	require.NoError(t, err)
	h.finish(t, run, parent)
	child, err := h.al.GetSessionLifecycleStore().Load(launched.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, child.State)
	require.Nil(t, h.al.ownLiveTurn(h.id))
	require.Nil(t, h.al.ownLiveTurn(child.SessionID), "queued work is deliberately not visible in the live-turn registry")
	grSetStreak(t, h, 1) // below both the old lifetime-two cap and the R6 brake: only genuine busy suppression can pass
	h.age(t)
	h.assertNoKeeperWake(t, "durably queued delegated work, without a registered turn")
}

func grBrakeIndependentTask(t *testing.T) {
	work := grStartIndependentTask(t, true)
	grSetStreak(t, work.h, 1)
	work.h.age(t)
	work.h.assertNoKeeperWake(t, "a real independent task initiated by this exact goal session")
}

func grBrakeRelatedPlan(t *testing.T) {
	for _, related := range []bool{true, false} {
		t.Run(fmt.Sprintf("InitiatingSessionMatches=%v", related), func(t *testing.T) {
			work := grStartRelatedPlan(t, related)
			h := work.h
			grSetStreak(t, h, 1)
			h.age(t)
			if related {
				grAssertOwnedPlanBusyProgressThenQuiet(t, work)
			} else {
				msg := h.reminder(t)
				assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "other-session plan work is not this goal's progress")
				grTextReminder(t, h, msg)
			}
		})
	}
}

func grRegisteredProcess(t *testing.T, owner string, background bool) *tools.ProcessSession {
	t.Helper()
	manager := tools.GetSharedSessionManager()
	ps := &tools.ProcessSession{ID: "r6-process-" + owner + "-" + fmt.Sprint(time.Now().UnixNano()), OwnerSessionID: owner, Background: background, Status: tools.StatusRunning}
	manager.Add(ps)
	t.Cleanup(func() { manager.Remove(ps.ID) })
	require.Equal(t, "running", ps.GetStatus())
	return ps
}
