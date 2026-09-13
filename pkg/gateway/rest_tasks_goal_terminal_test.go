// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_tasks_goal_terminal_test.go is review finding C1's REST-side oracle.
//
// The first fix for "a terminated task must end its paired goal record"
// (GOAL-FR-015/FR-027/FR-028) wired three call sites, all of them inside
// pkg/agent, and its own doc comment asserted those three were all the
// terminal writers there were. The two in THIS file were missed:
//
//   - handleTaskPatch. task.Store's validateTransition permits in_progress->
//     done and in_progress->failed, so dragging a card onto Done or Failed on
//     the board is an ordinary, legal PATCH. It answered 200 and left the goal
//     record `state: active`.
//   - reconcileStuckTasks. Every task left in_progress by a crashed gateway is
//     reset to `failed` on the next boot — a real terminal write, for every
//     in-flight task at once, with no goal hook at all.
//
// Why an `active` leftover is a live functional bug and not untidiness:
// activateTaskGoal switches on the record's phase (IsDefining -> Activate,
// IsTerminal -> Reactivate, default -> no-op). An `active` record matches
// neither arm, so the NEXT run of that task silently skips Goal.Reactivate
// entirely. It inherits run 1's attempts_used, rounds_used, latest_reason,
// both keeper budgets, an active_session_id pointing at an archived session,
// writes no TerminalHistory entry — and, because Reactivate is what resets the
// per-criterion statuses, serves a DoD item marked `met` in run 1 as `met` for
// work run 2 never did.

package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// runOneSessionID is the session a task's first run is bound to. Its only
// property that matters is that it differs from the re-run's.
const (
	runOneSessionID = "session-run-1"
	runTwoSessionID = "session-run-2"
)

// startTaskWithActiveGoal creates a task through the REST API (so it gets a
// paired goal record carrying real criteria and a real Definition of Done),
// walks it inbox -> next -> in_progress through the REST API, and activates
// its goal record against runOneSessionID — the state a task that is actually
// RUNNING is in, and the only state from which the defect under test is
// reachable.
//
// It then stamps run-1 state worth losing onto the record: counters, a reason,
// the two keeper budgets, and — the one that matters most — both criterion
// lists marked `met`. Without that, "the statuses were reset" would be a
// comparison of two `pending`s and would pass with the reset deleted.
func startTaskWithActiveGoal(t *testing.T, api *restAPI, wsID, title string) (taskID string, goalID string) {
	t.Helper()
	taskID = createTaskWithGoal(t, api, wsID, title)

	wNext := patchTaskJSON(t, api, taskID, `{"status":"next","description":"task ready"}`)
	require.Equal(t, http.StatusOK, wNext.Code, "inbox->next; body=%s", wNext.Body.String())
	wIP := patchTaskJSON(t, api, taskID, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, wIP.Code, "next->in_progress; body=%s", wIP.Body.String())

	gs := tools.GoalStoreForTasks(api.taskStore)
	rec, err := gs.GetByOwner(gen.GoalOwnerKindTask, taskID)
	require.NoError(t, err, "fixture: the task must have a paired goal record")

	now := time.Now().UTC()
	_, err = gs.Update(rec.GoalID, func(g *goal.Goal) error {
		if aErr := g.Activate(runOneSessionID, now); aErr != nil {
			return aErr
		}
		g.AttemptsUsed = 2
		g.Round = 3
		g.LatestReason = "run 1 fell short"
		g.ZeroOutputPushes = 1
		g.QuestionRoundsUsed = 1
		for i := range g.Criteria {
			g.Criteria[i].Status = task.CritMet
		}
		for i := range g.DoD {
			g.DoD[i].Status = task.CritMet
		}
		return nil
	})
	require.NoError(t, err, "fixture: activating and seeding the run-1 goal record")

	seeded, err := gs.GetByOwner(gen.GoalOwnerKindTask, taskID)
	require.NoError(t, err)
	require.Equal(t, gen.GoalStateActive, seeded.State,
		"fixture: the record must be ACTIVE before the terminal PATCH, or nothing is being proven")
	require.NotEmpty(t, seeded.DoD, "fixture: the record must carry a Definition of Done")
	return taskID, seeded.GoalID
}

// TestPatchTaskToTerminalEndsItsGoalRecord_GOALFR015 drives the REST PATCH
// that a board drag sends and asserts the paired goal record ends with it.
//
// The two rows differ only in the terminal status, and they pin the outcome
// VOCABULARY as well as the fact of termination: a task that succeeded ends
// its goal `met`, a task that failed without a user Stop ends it `exhausted`.
// A fix that terminated every record to one state would pass "it is no longer
// active" and fail here, which is the point.
func TestPatchTaskToTerminalEndsItsGoalRecord_GOALFR015(t *testing.T) {
	cases := []struct {
		name          string
		patchStatus   string
		wantTask      task.Status
		wantGoalState gen.GoalState
	}{
		{
			name:          "done_ends_the_goal_as_met",
			patchStatus:   "done",
			wantTask:      task.StatusDone,
			wantGoalState: gen.GoalStateMet,
		},
		{
			name:          "failed_ends_the_goal_as_exhausted",
			patchStatus:   "failed",
			wantTask:      task.StatusFailed,
			wantGoalState: gen.GoalStateExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			wsID := ensureTestWorkspace(t, api)
			taskID, _ := startTaskWithActiveGoal(t, api, wsID, "board-driven "+tc.name)

			w := patchTaskJSON(t, api, taskID, `{"status":"`+tc.patchStatus+`"}`)
			require.Equal(t, http.StatusOK, w.Code,
				"in_progress->%s is a legal transition and must answer 200; body=%s",
				tc.patchStatus, w.Body.String())

			stored, err := api.taskStore.Get(taskID)
			require.NoError(t, err)
			require.Equal(t, tc.wantTask, stored.Status,
				"fixture broken: the PATCH did not actually move the task, so nothing about its "+
					"goal record is being proven")

			rec, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, taskID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantGoalState, rec.State,
				"PATCH /api/v1/tasks/{id} is a real terminal writer — validateTransition permits "+
					"in_progress->done and ->failed, which is what a Kanban drag sends — and it must "+
					"end the task's paired goal record. Left `active`, the record makes every later "+
					"run of this task skip Goal.Reactivate in silence (activateTaskGoal's `default:` "+
					"branch matches neither IsDefining nor IsTerminal).")
			assert.NotEmpty(t, rec.TerminalReason,
				"GOAL-FR-027: a terminal record must say what ended it, without a reader having to "+
					"go and find the task")
		})
	}
}

// TestPatchTaskToFailedLeavesTheGoalReactivatable_R04 is the assertion that
// makes the one above matter. It proves the CONSEQUENCE, on the real stored
// record, rather than restating the state name.
//
// R-04's re-entry contract lives entirely inside Goal.Reactivate, and
// Reactivate REFUSES a non-terminal record outright. So "the PATCH ended the
// record" and "a re-run resets this task's run state" are the same claim, and
// this test states it in the second form: after the terminal PATCH, the real
// re-entry succeeds and every per-run field is genuinely back to zero.
//
// The negative control at the end is what stops this passing for the wrong
// reason: on a record the PATCH left ACTIVE, the identical call FAILS — which
// in production is not an error anybody sees, because activateTaskGoal's
// `default:` arm never makes the call at all.
func TestPatchTaskToFailedLeavesTheGoalReactivatable_R04(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	taskID, goalID := startTaskWithActiveGoal(t, api, wsID, "re-run after a board Fail")

	w := patchTaskJSON(t, api, taskID, `{"status":"failed"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	gs := tools.GoalStoreForTasks(api.taskStore)
	afterPatch, err := gs.GetByOwner(gen.GoalOwnerKindTask, taskID)
	require.NoError(t, err)
	require.True(t, afterPatch.IsTerminal(),
		"the goal record is %q after the terminal PATCH, not terminal — Goal.Reactivate refuses a "+
			"non-terminal record outright, so the re-run below cannot reset anything and run 2 would "+
			"inherit run 1 wholesale", afterPatch.State)

	// --- the re-run: the exact transition activateTaskGoal's IsTerminal arm
	// performs when the task is dispatched again -------------------------------
	reRun, err := gs.Update(goalID, func(g *goal.Goal) error {
		return g.Reactivate(runTwoSessionID, time.Now().UTC())
	})
	require.NoError(t, err, "R-04: a terminated task's goal must be able to re-enter active on re-run")

	assert.Equal(t, gen.GoalStateActive, reRun.State)
	assert.Equal(t, runTwoSessionID, reRun.ActiveSessionID,
		"active_session_id must point at the NEW run's session — a record left active by run 1 keeps "+
			"pointing at run 1's session, so every keeper push and cancel for run 2 is routed to a "+
			"session that is already archived")
	assert.Zero(t, reRun.AttemptsUsed, "attempts_used must reset to 0 on re-entry (R-04)")
	assert.Zero(t, reRun.Round, "rounds_used must reset to 0 on re-entry (R-04)")
	assert.Empty(t, reRun.LatestReason, "latest_reason must be cleared on re-entry (R-04)")
	assert.Zero(t, reRun.ZeroOutputPushes, "the keeper's zero-output budget is per-RUN and must reset")
	assert.Zero(t, reRun.QuestionRoundsUsed, "the keeper's question budget is per-RUN and must reset")

	// The sharpest of the resets: run 1 judged these `met`. Run 2 has done no
	// work at all, so serving them as `met` would hand the Judge — and the
	// operator reading the record — a verdict for work that never happened.
	require.NotEmpty(t, reRun.Criteria)
	require.NotEmpty(t, reRun.DoD)
	for _, c := range reRun.Criteria {
		assert.Equal(t, task.CritPending, c.Status,
			"acceptance criterion %q is still %q after the re-run — Goal.Reactivate resets every "+
				"criterion status precisely so run 2 is not credited with run 1's verdict",
			c.Text, c.Status)
	}
	for _, d := range reRun.DoD {
		assert.Equal(t, task.CritPending, d.Status,
			"Definition-of-Done item %q is still %q after the re-run — this is the failure mode in "+
				"full: a DoD item marked met in run 1, served as met for work run 2 never did",
			d.Text, d.Status)
	}
	require.Len(t, reRun.TerminalHistory, 1,
		"Goal.Reactivate appends the prior run's outcome BEFORE resetting the counters (R-04); with "+
			"no history entry there is no record that run 1 ever happened")
	assert.Equal(t, 3, reRun.TerminalHistory[0].Round,
		"the archived entry must carry run 1's REAL counters, taken before the reset")
	assert.Equal(t, 2, reRun.TerminalHistory[0].AttemptsUsed)

	// --- negative control: the state the defect actually leaves behind --------
	stillActiveTaskID, stillActiveGoalID := startTaskWithActiveGoal(t, api, wsID, "never terminated")
	_, err = gs.Update(stillActiveGoalID, func(g *goal.Goal) error {
		return g.Reactivate(runTwoSessionID, time.Now().UTC())
	})
	require.Error(t, err,
		"control: Goal.Reactivate must REFUSE a record still ACTIVE (task %q). This is why leaving "+
			"the record active is not a cosmetic leak — in production activateTaskGoal never even "+
			"makes this call for an active record, so the whole re-entry contract is skipped with no "+
			"error anywhere", stillActiveTaskID)
}

// TestReconcileStuckTasksEndsItsGoalRecord_GOALFR015 covers the second missed
// writer, and it is the one with the widest blast radius: a single crash and
// restart reset EVERY in-flight task to `failed`, so every one of their goal
// records was left permanently active in one pass.
func TestReconcileStuckTasksEndsItsGoalRecord_GOALFR015(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	taskID, _ := startTaskWithActiveGoal(t, api, wsID, "stranded by a crash")

	api.reconcileStuckTasks()

	stored, err := api.taskStore.Get(taskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, stored.Status,
		"fixture broken: boot reconciliation did not reset the stuck task, so nothing about its "+
			"goal record is being proven")

	rec, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, taskID)
	require.NoError(t, err)
	assert.Equal(t, gen.GoalStateExhausted, rec.State,
		"reconcileStuckTasks writes a terminal status to every task a crash left in_progress, so it "+
			"must end their goal records too. CancelReason is empty here (a crash is not a user "+
			"Stop), which is what makes the ending `exhausted` rather than `cleared`.")
	assert.Contains(t, rec.TerminalReason, "interrupted",
		"the record must retain WHY it ended — the reconciler's own reason text, not a generic one")
}

// TestPatchTaskToNextLeavesItsGoalRecordAlone is the differentiation control
// for the whole file. It is what stops every assertion above being satisfiable
// by "terminate the record on any PATCH".
//
// A task moving in_progress -> next is mid-flight: its goal is still being
// worked on, and ending the record here would both lie about the outcome and
// break the keeper, which selects ACTIVE records.
func TestPatchTaskToNextLeavesItsGoalRecordAlone(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)
	taskID, _ := startTaskWithActiveGoal(t, api, wsID, "back to the queue")

	w := patchTaskJSON(t, api, taskID, `{"status":"next"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	rec, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, taskID)
	require.NoError(t, err)
	assert.Equal(t, gen.GoalStateActive, rec.State,
		"a non-terminal PATCH must not touch the goal record — the task is still being worked on")
	assert.Equal(t, 2, rec.AttemptsUsed,
		"and it must not reset the in-flight run's counters either")
}
