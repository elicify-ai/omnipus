package agent

// Fix round 4, part B row 8 (team-lead ruling, option 1): the unattended-refusal tests
// (TestSkillLoad_AskUnattendedRefused, TestTimedAllowance_Unattended and the gateway's
// TestTimedAllowance_ReachesScheduledRun) drive a REAL task run instead of a cron-job
// ProcessScheduled turn.
//
// Why: S-12 (spec line "Scenario: S-12"), S-109 and S-123 say a scheduled TASK, and C-12's
// "Unattended (task, plan, scheduled)" column gives the sentence "Not run: needs approval, and
// task and plan runs never ask." (ST-Q9 A keeps today's rule). pkg/agent/turn_attendance.go
// ::applyTurnAttendance keeps the headless sentence for a cron-job or heartbeat turn (#1263 R4),
// so a ProcessScheduled drive could never meet the spec's sentence without reversing #1263 R4.
// Spec-sync note for team-lead: the C-12 column should say that cron-job and heartbeat runs keep
// the headless sentence today.
//
// What this helper does NOT do: it never decides whether a tool may run, never supplies the
// refusal text and never writes a transcript. It creates one ordinary task, asks the loop's own
// TaskExecutor to run it (the entry SpawnTriggeredRun uses), waits for the run to end and hands
// back the run's session id; the caller reads the real persisted transcript and the real model
// requests exactly as before.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// runRealUnattendedTask runs one real task of agentID (prompt as its task prompt) through the
// loop's own TaskExecutor and returns the task run's session id once the run has ended.
//
// The caller must have installed the production session wiring (installTestSessionLauncher): a
// task run is classified unattended through its session's task id and lifecycle record
// (turn_attendance.go::classifySessionChain), and without a lifecycle store the turn is simply
// not classified and gets the headless sentence.
//
// The task is created with its paired goal record (createTaskWithGoal), the way production task
// creation does it. The first draft of this helper used the bare store.Create and the run then
// executed with "NO goal loop" ("task has no paired goal record", GOAL-FR-023): the turn finished
// but the task never reached a terminal status, so every caller timed out waiting (fix4 proof
// logs fix4-go-fix4b-row8-skillload.log and fix4-go-fix4b-row8-timed-agent.log). That was a defect
// of this fixture, not of the product.
//
// The run ends deterministically after the one scripted tool call: the scripted model answers
// with plain text and no goal_claim, so with one goal try (cfg.Planning.GoalMaxRounds = 1, set by
// the caller) and one task attempt the run spends its only try and the task ends Failed. No
// Judge, no second run. Whether the task ends Done or Failed is not what these tests observe;
// the oracle is the run's persisted transcript and the model requests.
//
// Two more defects of the first two drafts, both read from the first proof run
// (fix4-go-fix4d-agent-unattended.log, run against dev code at 901c7381f, not a product PASS):
//
//  1. The task carries one acceptance criterion. A task goal record with NO criteria is a goal
//     whose first move is still open: pkg/agent/goal_loop_forcing.go::goalTurnRecordState holds
//     and evaluateGoalForcing narrows the first request to set_goal (and AskUserQuestion), so the
//     scripted first call to the tool under test was refused as "not offered in this request"
//     and never reached the permission decision these tests are about. Every task-run test in
//     task_executor_goal_loop_test.go gives its task a criterion for the same reason.
//  2. The wait no longer uses t3WaitForTerminal. That helper also requires the run's session to
//     read as archived, and in the same proof run two rows with identical data differed (one
//     returned in 0.95 s, the other timed out after 25 s) although the product log of both shows
//     the same ending ("paired goal record terminated with its task", goal_state=exhausted).
//     The archive flag is not what these tests observe, so the wait here is for the terminal
//     task status alone. The final session status is logged (t.Logf), not asserted, so the
//     proof log shows what it was.
//
// A third defect, read from the second proof run (fix4-go-fix4e-gateway-sched.log, run against
// dev code at 186e99344, not a product PASS) and fixed here in both mirrors: the first drafts
// also called TaskExecutor.Drain after the terminal status "so the transcript is complete".
// Drain is a one-way shutdown gate: every later dispatch on the same loop is refused with
// ErrExecutorDraining ("executor is draining for shutdown — new dispatch refused"), so any row
// that runs two tasks on one loop (a run before and a run after the clock passes the end of an
// allowance) failed at its second run before anything was observed. Nothing observed here needs
// the drain: the tool call's denial text is persisted by the turn, which ends before the task's
// terminal status is written, and the loop's own Close drains the executor at the end of the
// test. Dispatching again while the previous run still holds its slot is the retryable
// ErrDispatchCapReached, which the dispatch below retries.
func runRealUnattendedTask(t *testing.T, al *AgentLoop, agentID, prompt string) string {
	t.Helper()
	executor := GetTaskExecutor(al)
	require.NotNil(t, GetTaskStore(al), "fixture: the loop owns the real task store")
	require.NotNil(t, executor, "fixture: the loop owns the real task executor")

	oneAttempt := 1
	runner := createTaskWithGoal(t, al, &task.Task{
		Title:       "Unattended run of " + agentID,
		Prompt:      prompt,
		Description: "The task whose real run is the unattended turn under test.",
		AgentID:     agentID,
		Action:      task.ActionLLM,
		Priority:    3,
		WorkspaceID: testHarnessWorkspaceMembershipID,
		Status:      task.StatusNext,
		MaxAttempts: &oneAttempt,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "The one scripted model turn of this task has run.")},
	}, "The one scripted model turn of this task has run.")
	// A dispatch whose predecessor on this loop has not yet released its dispatch slot gets
	// ErrDispatchCapReached ("retry later": the executor's documented, retryable refusal), so
	// that one error is retried and nothing else is: any other refusal fails the fixture.
	dispatchDeadline := time.Now().Add(30 * time.Second)
	for {
		dispatchErr := executor.ExecuteTask(context.Background(), runner.ID, nil)
		if dispatchErr == nil {
			break
		}
		require.ErrorIs(t, dispatchErr, ErrDispatchCapReached,
			"ExecuteTask accepts the dispatch synchronously; the run itself is asynchronous")
		require.True(t, time.Now().Before(dispatchDeadline),
			"fixture: the previous run never released its dispatch slot: %v", dispatchErr)
		time.Sleep(20 * time.Millisecond)
	}

	final := waitForUnattendedRunEnd(t, al, runner.ID)
	require.NotEmpty(t, final.SessionID,
		"fixture: the task run never reached a session, so nothing was observed (task result: %q)", final.Result)

	meta, err := al.GetSessionStore().GetMeta(final.SessionID)
	require.NoError(t, err)
	require.Equal(t, runner.ID, meta.TaskID,
		"instrument: the observed session is the real task run's session, the one the unattended classification reads")
	return final.SessionID
}

// waitForUnattendedRunEnd waits until the task reached a terminal status and returns it. It does
// not drain the executor (see runRealUnattendedTask: Drain is a one-way gate). A timeout names
// the task's state instead of a bare deadline.
func waitForUnattendedRunEnd(t *testing.T, al *AgentLoop, taskID string) *task.Task {
	t.Helper()
	store := GetTaskStore(al)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		final, err := store.Get(taskID)
		require.NoError(t, err)
		if task.IsTerminal(final.Status) {
			if final.SessionID != "" {
				if meta, merr := al.GetSessionStore().GetMeta(final.SessionID); merr == nil {
					t.Logf("observed: the run ended with task status %q, %d attempt(s), session status %q",
						final.Status, final.AttemptCount, meta.Status)
				}
			}
			return final
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, err := store.Get(taskID)
	require.NoError(t, err)
	t.Fatalf("the task run did not reach a terminal status within 30 s: status %q, %d attempt(s), result %q",
		got.Status, got.AttemptCount, got.Result)
	return nil
}
