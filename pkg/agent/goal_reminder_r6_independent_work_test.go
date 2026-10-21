package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type grIndependentWork struct {
	h                 *grHarness
	taskID, sessionID string
	call              *grCall
	planStore         *plan.Store
	engine            *PlanEngine
	planID            string
	stopped           bool
}

func grOtherActor(t *testing.T, h *grHarness) *grHarness {
	t.Helper()
	id := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	owner := "goal-reminder-owner"
	require.NoError(t, h.al.GetSessionStore().SetMeta(id, session.MetaPatch{Owner: &owner}))
	return &grHarness{al: h.al, id: id, root: h.root, reviewer: h.reviewer, ctx: h.ctx, cancel: h.cancel}
}

func grStartIndependentTask(t *testing.T, related bool) *grIndependentWork {
	t.Helper()
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t)) // real prior watermark; no stale wake is left queued
	actor := h
	if !related {
		actor = grOtherActor(t, h)
	}
	parentRun := actor.human(t, "Start the independent background task.")
	parentCall := grAwaitCall(t, h.root, parentRun)
	tk := mustCreateTask(t, h.al.taskStore, &task.Task{Title: "Independent R6 work", WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: grReviewerID, Status: task.StatusNext, RunIsolated: true})
	seedTaskGoal(t, tk.ID, "Independent R6 task supplies its result", "It provides a concrete result")
	te := GetTaskExecutor(h.al)
	sid, err := te.StartTaskNowAs(h.ctx, tk.ID, &task.Initiator{AgentID: testDefaultAgentID, SessionID: actor.id})
	require.NoError(t, err)
	waiting := &grPending{done: make(chan struct{})}
	call := grAwaitCall(t, h.reviewer, waiting)
	require.Equal(t, sid, call.sessionID)
	rec, err := h.al.GetSessionLifecycleStore().Load(sid)
	require.NoError(t, err)
	require.NotNil(t, rec.InitiatedBy)
	require.Equal(t, actor.id, rec.InitiatedBy.SessionID, "the real current-run starter is persisted by StartTaskNowAs")
	require.Nil(t, rec.SteeredBy, "the independent run must not pass through ordinary descendant suppression")
	require.NotEqual(t, h.id, sid)
	require.True(t, te.holdsRun(tk.ID))
	actor.finish(t, parentRun, parentCall)
	require.Nil(t, h.al.ownLiveTurn(h.id))
	work := &grIndependentWork{h: h, taskID: tk.ID, sessionID: sid, call: call}
	t.Cleanup(func() { work.stop(t) })
	return work
}

func (w *grIndependentWork) stop(t *testing.T) {
	t.Helper()
	if w.stopped {
		return
	}
	w.stopped = true
	if w.engine != nil {
		_, err := w.engine.StopPlan(context.Background(), w.planID, "goal-reminder-owner", "web")
		require.NoError(t, err)
	} else {
		result, err := w.h.al.StopSession(context.Background(), StopRequest{SessionID: w.sessionID, Channel: "web", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-reminder-owner"}})
		require.NoError(t, err)
		require.NoError(t, result.RootErr)
	}
	w.h.reviewer.release(w.call)
	grAwaitOwner(t, w.call)
	GetTaskExecutor(w.h.al).Drain(grWatchdog) // join the actual task completion writer, not a guessed sleep
	require.False(t, GetTaskExecutor(w.h.al).holdsRun(w.taskID))
	runs, err := w.h.al.taskStore.ListRuns(w.taskID)
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	require.NotNil(t, runs[len(runs)-1].EndedAt, "actual run completion must be durably written")
}

func grStartRelatedPlan(t *testing.T, related bool) *grIndependentWork {
	t.Helper()
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	ps := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	pe := NewPlanEngine(h.al, ps, h.al.taskStore, GetTaskExecutor(h.al))
	h.al.SetPlanEngine(pe)
	p := &plan.Plan{Title: "R6 independently initiated plan", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerAgentID: grReviewerID, State: plan.StateDraft, DoD: newFloorDoD()}
	mustCreatePlan(t, ps, p)
	tk := mustCreateTask(t, h.al.taskStore, &task.Task{Title: "R6 plan member", WorkspaceID: testHarnessWorkspaceMembershipID, PlanID: p.ID, AgentID: grReviewerID, Status: task.StatusNext, Criteria: recordedGoalCriteria("Member produces a result")})
	seedTaskGoal(t, tk.ID, "R6 member produces a result", "Member result is independently checked")
	actor := h
	if !related {
		actor = grOtherActor(t, h)
	}
	grActionRound(t, actor, actor.humanInput(t, "Execute the plan through the real tool."), "execute_plan", map[string]any{"plan_id": p.ID}, true)
	approved, err := ps.Get(p.ID)
	require.NoError(t, err)
	require.Equal(t, plan.StateApproved, approved.State)
	require.NotNil(t, approved.InitiatedBy)
	require.Equal(t, actor.id, approved.InitiatedBy.SessionID, "the real execute_plan call authors provenance; no test-injected InitiatedBy")
	require.NotEqual(t, h.id, approved.OwnerSessionID, "a coordinator is not the launching goal session")
	pe.tryStartApprovedPlan(h.ctx, p.ID)
	waiting := &grPending{done: make(chan struct{})}
	call := grAwaitCall(t, h.reviewer, waiting)
	member, err := h.al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	require.Equal(t, member.SessionID, call.sessionID)
	require.True(t, GetTaskExecutor(h.al).holdsRun(tk.ID))
	busy, why := pe.planWorkInFlight(p.ID)
	require.True(t, busy, "actual member must be in flight: %s", why)
	work := &grIndependentWork{h: h, taskID: tk.ID, sessionID: member.SessionID, call: call, planStore: ps, engine: pe, planID: p.ID}
	t.Cleanup(func() { work.stop(t) })
	return work
}

func TestGoalReminderIndependentWorkProvenance(t *testing.T) {
	for _, kind := range []string{"Task", "Plan"} {
		for _, related := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/InitiatorMatches=%v", kind, related), func(t *testing.T) {
				var work *grIndependentWork
				if kind == "Task" {
					work = grStartIndependentTask(t, related)
				} else {
					work = grStartRelatedPlan(t, related)
				}
				h := work.h
				grSetStreak(t, h, 1)
				h.age(t)
				if related {
					h.assertNoKeeperWake(t, "actual independent work initiated by this goal session")
					assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes)
				} else {
					next := h.reminder(t)
					assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "another same-agent session's work must neither suppress nor reset")
					grTextReminder(t, h, next)
				}
			})
		}
	}
}

func TestGoalReminderIndependentCompletionBetweenPolls(t *testing.T) {
	for _, kind := range []string{"Task", "Plan"} {
		for _, removed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/Removed=%v", kind, removed), func(t *testing.T) {
				var work *grIndependentWork
				if kind == "Task" {
					work = grStartIndependentTask(t, true)
				} else {
					work = grStartRelatedPlan(t, true)
				}
				h := work.h
				grSetStreak(t, h, 1)
				work.stop(t)
				if removed {
					if kind == "Plan" {
						require.NoError(t, work.planStore.Delete(work.planID))
					} else {
						require.NoError(t, h.al.taskStore.Delete(work.taskID))
					}
				}
				// No keeper poll ran while it was live: persisted actual completion,
				// not a remembered live-count decrease, must carry progress.
				h.age(t)
				next := h.reminder(t)
				assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
				grTextReminder(t, h, next)
			})
		}
	}
}
