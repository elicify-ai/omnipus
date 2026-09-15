// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_loop_test.go pins the founder decisions of 2026-09-14 for task runs
// (task_run_loop.go), each through the real executor, the real goal_claim tool
// and a scripted provider:
//
//   - INNER goal tries: a "not met" ruling keeps the worker in the SAME run and
//     session, spending a goal try (the goal record's round); AttemptCount is
//     untouched.
//   - OUTER task attempts: a run whose goal ends not met after all its tries
//     fails as a whole and the task restarts in a fresh run (new session, goal
//     reactivated with a fresh try budget), up to the attempt limit (default 3).
//   - blocked ends the task Failed with "Blocked: …": no attempt, no Judge, no
//     restart.
//   - two reasoning-only tries in a row fail the run with a plain reason.
//   - a Judge only an operator can fix ends the task with a plain reason; a
//     transient outage is retried a bounded number of times, visibly.
//   - a run that never started the work consumes no attempt.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const runLoopJudgeReason = "report.md lists two supplier quotes, not the three the task requires"

func newRunLoopTask(t *testing.T, al *AgentLoop, maxAttempts *int) *task.Task {
	t.Helper()
	return createTaskWithGoal(t, al, &task.Task{
		Title: "supplier quote report", Prompt: "Write report.md summarising the three supplier quotes.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status: task.StatusNext, MaxAttempts: maxAttempts,
		Criteria: []task.AcceptanceCriterion{proseCriterion("", "report.md lists all three supplier quotes")},
	}, "no credentials appear in report.md")
}

func runTaskUntilTerminal(t *testing.T, al *AgentLoop, taskID string, expectedTurns int) *task.Task {
	t.Helper()
	if err := al.taskExecutor.ExecuteTask(context.Background(), taskID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	return t3WaitForTerminal(t, al, taskID, expectedTurns)
}

// waitForGoalState polls the task's goal record until it is in want: the goal
// ends with completeTaskWithResult's trailing write, after the session archive
// t3WaitForTerminal returns on.
func waitForGoalState(t *testing.T, taskID string, want generated.GoalState) *goal.Goal {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	rec := taskGoalRecordOf(t, taskID)
	for rec.State != want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		rec = taskGoalRecordOf(t, taskID)
	}
	if rec.State != want {
		t.Fatalf("goal state = %q, want %q", rec.State, want)
	}
	return rec
}

// Given a task run whose first claim the Judge overturns
// When the worker keeps working and claims again
// Then the second claim is judged in the SAME run and session, the goal spent
// two tries, the task is done, and no task attempt was used.
func TestTaskRun_UnmetVerdictKeepsWorkingInTheSameRun(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"), turnClaimMet("added the third quote and re-read report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 2, reason: runLoopJudgeReason}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 2)

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — an overturned claim spends a goal try, not a task attempt", final.AttemptCount)
	}
	if worker.turnsStarted() != 2 || judge.callCount() != 2 {
		t.Fatalf("worker turns=%d Judge calls=%d, want 2/2", worker.turnsStarted(), judge.callCount())
	}
	reqs := worker.requestSnapshot()
	if strings.Contains(reqs[0], runLoopJudgeReason) || !strings.Contains(reqs[1], runLoopJudgeReason) {
		t.Errorf("the second turn must carry the Judge's reason and the first must not")
	}
	runs, err := al.taskStore.ListRuns(tk.ID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].SessionID != final.SessionID {
		t.Errorf("runs=%+v final session=%q — both tries must happen in the run's one session", runs, final.SessionID)
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateMet)
	if rec.Round != 2 {
		t.Errorf("goal round = %d, want 2 (one overturned try, one upheld)", rec.Round)
	}
	if len(rec.TerminalHistory) != 0 {
		t.Errorf("the goal was ended and reactivated mid-run (%d history entries) — tries must stay in one run", len(rec.TerminalHistory))
	}
}

// Given a goal try limit of 2 and a Judge that upholds only the third claim
// When run 1 spends both tries
// Then run 1 fails, one task attempt is used, the task restarts in a fresh
// session with the goal reactivated (fresh tries, no ACTIVE re-entry warning),
// and run 2's claim is upheld.
func TestTaskRun_GoalTriesExhaustedRestartsInAFreshRun(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 2 })
	judge := &b6ScriptedJudge{metFromCall: 3, reason: runLoopJudgeReason}
	judgeInst.Provider = judge
	readLog := captureLogFile(t, logger.WARN)
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 3)

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 — exactly one run failed", final.AttemptCount)
	}
	if worker.turnsStarted() != 3 || judge.callCount() != 3 {
		t.Fatalf("worker turns=%d Judge calls=%d, want 3/3", worker.turnsStarted(), judge.callCount())
	}
	reqs := worker.requestSnapshot()
	if !strings.Contains(reqs[2], "The previous run of this task failed (attempt 1 of 3)") {
		t.Errorf("the restarted run's first prompt must say why the previous run failed:\n%s", reqs[2])
	}
	runs, err := al.taskStore.ListRuns(tk.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %+v, %v", runs, err)
	}
	if runs[0].SessionID == final.SessionID {
		t.Errorf("the restart must run in a NEW session; both runs used %q", final.SessionID)
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateMet)
	if len(rec.TerminalHistory) != 1 || rec.TerminalHistory[0].Round != 2 {
		t.Errorf("terminal history = %+v, want one entry recording run 1's two tries", rec.TerminalHistory)
	}
	if rec.Round != 1 {
		t.Errorf("goal round after the restart = %d, want 1 — the restart gets a fresh try budget", rec.Round)
	}
	if rec.ActiveSessionID != final.SessionID {
		t.Errorf("goal bound to %q, want the restarted run's session %q", rec.ActiveSessionID, final.SessionID)
	}
	if strings.Contains(readLog(), "already ACTIVE at run start") {
		t.Error("the restart logged the ACTIVE re-entry warning — the goal was not ended before the restart")
	}
}

// Given the default task attempt limit (3) and a Judge that never upholds
// When every run spends its one try
// Then the third failed run ends the task Failed with the reason
// And the task's one ending leaves exactly one goal outcome line, in the final
// run's session: the restarts in between end each run's goal only so the next
// run can reactivate it, and are not the task ending.
func TestTaskRun_ThirdFailedAttemptEndsFailedWithReason(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: runLoopJudgeReason}
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 3)

	if final.Status != task.StatusFailed || final.AttemptCount != 3 {
		t.Fatalf("status=%q attempt_count=%d, want failed after 3 attempts (result: %s)", final.Status, final.AttemptCount, final.Result)
	}
	if worker.turnsStarted() != 3 {
		t.Errorf("worker turns = %d, want 3", worker.turnsStarted())
	}
	if !strings.Contains(final.Result, "Task failed after 3 attempt(s) (max 3)") || !strings.Contains(final.Result, runLoopJudgeReason) {
		t.Errorf("result = %q, want the attempt count and the last Judge reason", final.Result)
	}

	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	store := al.GetAgentStore(tk.AgentID)
	waitForGoalOutcomeEntry(t, store, final.SessionID)
	requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
	runs, err := al.taskStore.ListRuns(tk.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns = %+v, %v; want the task's one run", runs, err)
	}
	if runs[0].SessionID == final.SessionID {
		t.Fatalf("the first run and the final run share session %q — the restarts did not start fresh runs", final.SessionID)
	}
	if n := countGoalOutcomeEntries(t, store, runs[0].SessionID); n != 0 {
		t.Errorf("the first run's session carries %d goal outcome line(s), want 0 — a restart is not the task ending", n)
	}
}

// countGoalOutcomeEntries counts the goal outcome entries saved in sid.
func countGoalOutcomeEntries(t *testing.T, store *session.UnifiedStore, sid string) int {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("read transcript %q: %v", sid, err)
	}
	n := 0
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeGoalOutcome {
			n++
		}
	}
	return n
}

// Given a goal try limit of 2 and a Judge that upholds only the third claim
// When the task's first run spends both tries and the task restarts in a fresh
// run
// Then every adjudication writes exactly one judge_verdict transcript entry and
// emits exactly one live Judge verdict event, each carrying the session of the
// run it belongs to: two for the first run, one for the restarted run.
func TestTaskRun_EachAdjudicationEmitsOneVerdictForItsRunSession(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 2 })
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 3, reason: runLoopJudgeReason}
	events, stop := recordAgentEvents(t, al)
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 3)
	stop()

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	runs, err := al.taskStore.ListRuns(tk.ID)
	if err != nil || len(runs) != 1 || runs[0].SessionID == final.SessionID {
		t.Fatalf("runs = %+v (err %v), final session %q — want one run whose restart used a fresh session",
			runs, err, final.SessionID)
	}
	firstRun, restarted := runs[0].SessionID, final.SessionID

	perSession := map[string]int{}
	events.mu.Lock()
	for _, e := range events.events {
		if e.Kind != EventKindJudgeVerdict {
			continue
		}
		p, ok := e.Payload.(JudgeVerdictPayload)
		if !ok {
			events.mu.Unlock()
			t.Fatalf("judge verdict event payload is %T, want JudgeVerdictPayload", e.Payload)
		}
		perSession[p.SessionID]++
	}
	events.mu.Unlock()
	if perSession[firstRun] != 2 || perSession[restarted] != 1 || len(perSession) != 2 {
		t.Errorf("live Judge verdict events per session = %v, want 2 for the first run (%s) and 1 for the restarted run (%s)",
			perSession, firstRun, restarted)
	}

	store := al.GetAgentStore(tk.AgentID)
	for sid, want := range map[string]int{firstRun: 2, restarted: 1} {
		entries, rerr := store.ReadTranscript(sid)
		if rerr != nil {
			t.Fatalf("read transcript %q: %v", sid, rerr)
		}
		got := 0
		for _, e := range entries {
			if e.Type == session.EntryTypeJudgeVerdict {
				got++
			}
		}
		if got != want {
			t.Errorf("session %s holds %d judge_verdict transcript entries, want %d — one per adjudication", sid, got, want)
		}
	}
}

// A per-task max_attempts wins over the global task attempt limit.
func TestTaskRun_PerTaskMaxAttemptsOverrideIsHonoured(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 1
		cfg.Planning.TaskMaxAttempts = 3
	})
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: runLoopJudgeReason}
	one := 1
	tk := newRunLoopTask(t, al, &one)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusFailed || final.AttemptCount != 1 || worker.turnsStarted() != 1 {
		t.Fatalf("status=%q attempts=%d turns=%d, want failed after the task's own limit of 1",
			final.Status, final.AttemptCount, worker.turnsStarted())
	}
}

// Given a worker that reports it cannot proceed
// Then the task ends Failed "Blocked: <reason>", with no attempt, no Judge call
// and no restart; its goal records the blocked claim and ends through the
// shared task-to-goal writer, leaving exactly one outcome line in the run's
// session — an "other" ending, never a user stop.
func TestTaskRun_BlockedEndsFailedWithoutAttemptJudgeOrRestart(t *testing.T) {
	const reason = "the supplier portal requires a login I do not have"
	worker := newClaimingWorker(turnClaimBlocked(reason))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusFailed || final.Result != "Blocked: "+reason {
		t.Fatalf("status=%q result=%q, want failed with \"Blocked: %s\"", final.Status, final.Result, reason)
	}
	if final.AttemptCount != 0 || judge.callCount() != 0 || worker.turnsStarted() != 1 {
		t.Errorf("attempts=%d Judge calls=%d turns=%d, want 0/0/1", final.AttemptCount, judge.callCount(), worker.turnsStarted())
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	if rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusBlocked ||
		rec.LatestClaim.Evidence != reason {
		t.Errorf("latest claim = %+v, want blocked with the worker's own reason %q as evidence", rec.LatestClaim, reason)
	}
	store := al.GetAgentStore(tk.AgentID)
	waitForGoalOutcomeEntry(t, store, final.SessionID)
	e := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
	if e.GoalOutcome.Ending != generated.GoalOutcomeEndingOther {
		t.Errorf("outcome ending = %q, want other — a blocked task is not a user stop", e.GoalOutcome.Ending)
	}
	if !strings.Contains(e.Content, reason) {
		t.Errorf("outcome line %q does not carry the blocked reason", e.Content)
	}
}

// Given a worker that needs an answer from the operator
// Then the task ends Failed "Needs the operator: <reason>", with no attempt, no
// Judge call and no restart — a task run has no reply channel to park on — and
// its goal's ending leaves exactly one outcome line in the run's session.
func TestTaskRun_WaitingOnUserEndsFailedWithOneOutcomeLine(t *testing.T) {
	const reason = "which of the two supplier price lists is current?"
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusWaitingOnUser, evidence: reason, content: "I need an answer before I can go on.",
	})
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusFailed || final.Result != "Needs the operator: "+reason {
		t.Fatalf("status=%q result=%q, want failed with \"Needs the operator: %s\"", final.Status, final.Result, reason)
	}
	if final.AttemptCount != 0 || judge.callCount() != 0 || worker.turnsStarted() != 1 {
		t.Errorf("attempts=%d Judge calls=%d turns=%d, want 0/0/1", final.AttemptCount, judge.callCount(), worker.turnsStarted())
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	if rec.LatestClaim == nil || rec.LatestClaim.Status != generated.GoalLatestClaimStatusWaitingOnUser ||
		rec.LatestClaim.Evidence != reason {
		t.Errorf("latest claim = %+v, want waiting_on_user with the worker's own question %q as evidence", rec.LatestClaim, reason)
	}
	store := al.GetAgentStore(tk.AgentID)
	waitForGoalOutcomeEntry(t, store, final.SessionID)
	e := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
	if !strings.Contains(e.Content, reason) {
		t.Errorf("outcome line %q does not carry the operator question", e.Content)
	}
}

// waitForGoalOutcomeEntry polls sid until a goal outcome entry is saved: the
// hook writes it just after the goal's terminal transition, a moment after the
// goal state the caller already waited on.
func waitForGoalOutcomeEntry(t *testing.T, store *session.UnifiedStore, sid string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := store.ReadTranscript(sid)
		if err != nil {
			t.Fatalf("read transcript %q: %v", sid, err)
		}
		for _, e := range entries {
			if e.SystemSubtype == session.SystemSubtypeGoalOutcome {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no goal outcome entry was saved in session %q within the deadline", sid)
}

// midRunLimitJudge changes the Settings goal try limit on its first call — i.e.
// while the run is already going — and never upholds the claim.
type midRunLimitJudge struct {
	inner   *b6ScriptedJudge
	once    sync.Once
	onFirst func()
}

func (j *midRunLimitJudge) Chat(
	ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	j.once.Do(j.onFirst)
	return j.inner.Chat(ctx, msgs, defs, model, opts)
}

func (j *midRunLimitJudge) GetDefaultModel() string { return "fake-judge-model" }

// Given a run that started with 2 tries per goal
// When Settings changes the limit to 5 mid-run
// Then the running goal still stops after 2 tries.
func TestTaskRun_TriesPerGoalChangeMidRunDoesNotMoveARunningGoal(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 2 })
	judgeInst.Provider = &midRunLimitJudge{
		inner: &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: runLoopJudgeReason},
		onFirst: func() {
			if err := al.MutateConfig(func(cfg *config.Config) error { cfg.Planning.GoalMaxRounds = 5; return nil }); err != nil {
				t.Errorf("change the limit mid-run: %v", err)
			}
		},
	}
	one := 1
	tk := newRunLoopTask(t, al, &one)

	final := runTaskUntilTerminal(t, al, tk.ID, 5)

	if goalTryLimit(al) != 5 {
		t.Fatalf("the mid-run Settings change did not land")
	}
	if final.Status != task.StatusFailed || worker.turnsStarted() != 2 {
		t.Fatalf("status=%q turns=%d, want failed after the 2 tries the run started with", final.Status, worker.turnsStarted())
	}
}

// Given two reasoning-only tries in a row
// Then the run fails with the founder's plain reason and the goal ends carrying it.
func TestTaskRun_TwoReasoningOnlyTriesFailTheRun(t *testing.T) {
	worker := newClaimingWorker(turnReasoningOnly())
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
	judgeInst.Provider = judge
	one := 1
	tk := newRunLoopTask(t, al, &one)

	final := runTaskUntilTerminal(t, al, tk.ID, 2)

	if final.Status != task.StatusFailed || !strings.Contains(final.Result, reasoningOnlyFailureReason) {
		t.Fatalf("status=%q result=%q, want failed with the reasoning-only reason", final.Status, final.Result)
	}
	if worker.turnsStarted() != 2 || judge.callCount() != 0 {
		t.Errorf("turns=%d Judge calls=%d, want 2/0", worker.turnsStarted(), judge.callCount())
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	if !strings.Contains(rec.TerminalReason, reasoningOnlyFailureReason) {
		t.Errorf("goal terminal reason = %q, want it to carry the reasoning-only reason", rec.TerminalReason)
	}
}

// Given one reasoning-only try followed by a real claim
// Then the run continues after the first, and the claim completes the task.
func TestTaskRun_OneReasoningOnlyTryThenAClaimContinues(t *testing.T) {
	worker := newClaimingWorker(turnReasoningOnly(), turnClaimMet("wrote report.md with all three quotes"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "ok"}
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 2)

	if final.Status != task.StatusDone || final.AttemptCount != 0 {
		t.Fatalf("status=%q attempts=%d, want done with no attempt used (result: %s)", final.Status, final.AttemptCount, final.Result)
	}
	if reqs := worker.requestSnapshot(); len(reqs) < 2 || !strings.Contains(reqs[1], "no answer at all") {
		t.Errorf("the turn after a reasoning-only try must be told it produced no answer")
	}
}

// Given a Judge whose provider does not exist (only an operator can fix it)
// Then the task ends Failed "The Judge could not run: …" on the first claim,
// with no attempt used and no backoff wait.
func TestTaskRun_JudgeMisconfiguredEndsFailedWithPlainReason(t *testing.T) {
	waits := e7RecordJudgeBackoffWaits(t)
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, _ := newGoalLoopTestLoop(t, worker, e7BreakJudgeModel)
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusFailed || !strings.HasPrefix(final.Result, "The Judge could not run: ") {
		t.Fatalf("status=%q result=%q, want failed with the Judge's plain reason", final.Status, final.Result)
	}
	if strings.Contains(final.Result, JudgeMisconfiguredReasonPrefix) {
		t.Errorf("result = %q carries the machine prefix", final.Result)
	}
	if final.AttemptCount != 0 || worker.turnsStarted() != 1 {
		t.Errorf("attempts=%d turns=%d, want 0/1 — restarting cannot fix the Judge", final.AttemptCount, worker.turnsStarted())
	}
	if waits.count() != 0 {
		t.Errorf("backoff waits = %d, want 0", waits.count())
	}
}

// Given a Judge that keeps failing for a transient reason
// Then the same claim is retried a bounded number of times with the reason
// visible on the task, and the task then ends Failed with no attempt used.
func TestTaskRun_TransientJudgeOutageIsBoundedAndVisible(t *testing.T) {
	origTimeout, origSleep := goalJudgeRoundTimeout, judgeSleepFn
	t.Cleanup(func() { goalJudgeRoundTimeout, judgeSleepFn = origTimeout, origSleep })
	goalJudgeRoundTimeout = 300 * time.Millisecond
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return nil
		}
	}

	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	var calls int
	var mu sync.Mutex
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, errors.New("upstream returned 503 service unavailable")
	}}
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	want := fmt.Sprintf("The Judge could not check this task's work after %d tries", judgeUnavailableRetryBound)
	if final.Status != task.StatusFailed || !strings.Contains(final.Result, want) {
		t.Fatalf("status=%q result=%q, want failed with %q", final.Status, final.Result, want)
	}
	if final.AttemptCount != 0 || worker.turnsStarted() != 1 {
		t.Errorf("attempts=%d turns=%d, want 0/1", final.AttemptCount, worker.turnsStarted())
	}
}

// Given a run whose dispatch is refused before any work starts
// Then the task ends without its AttemptCount moving and is not restarted.
func TestTaskRun_NotDispatchedConsumesNoAttempt(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk := newRunLoopTask(t, al, nil)
	claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
	if err != nil {
		t.Fatalf("ClaimForRun: %v", err)
	}
	sid, serr := al.taskExecutor.createTaskSessionSync(claimed)
	if serr != nil {
		t.Fatalf("createTaskSessionSync: %v", serr)
	}

	var turns int
	redispatch := al.taskExecutor.executeTaskRun(context.Background(), claimed, sid, "", nil, func(string) (string, error) {
		turns++
		return "", fmt.Errorf("isolated workspace is dirty: %w", ErrTaskRunNotDispatched)
	})

	final, _ := al.taskStore.Get(tk.ID)
	if redispatch != "" || turns != 1 {
		t.Fatalf("redispatch=%q turns=%d, want no restart after one refused dispatch", redispatch, turns)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a run that never started is not a failed attempt", final.AttemptCount)
	}
	if final.Status != task.StatusFailed || !strings.Contains(final.Result, "could not be started") {
		t.Errorf("status=%q result=%q, want failed with the not-started reason", final.Status, final.Result)
	}
}

// Every agent-created task carries a delegation generation >= 1 on its run's
// context; its worker must still be able to claim.
func TestTaskRun_AgentCreatedTaskAtDepthCanClaim(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "ok"}
	one := 1
	tk := createTaskWithGoal(t, al, &task.Task{
		Title: "delegated report", Prompt: "Write report.md.", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &one, DelegationDepth: 1,
		Criteria: []task.AcceptanceCriterion{proseCriterion("", "report.md exists")},
	}, "no credentials appear in report.md")

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done — a task run at delegation depth 1 must be able to claim (result: %s)",
			final.Status, final.Result)
	}
}

// A legacy task with no goal record gets one at its first run, so its worker
// can claim through goal_claim.
func TestTaskRun_LegacyTaskWithoutGoalRecordCanClaim(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("painted the widget green"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "ok"}
	tk := &task.Task{
		Title: "paint the widget", Prompt: "the widget must end up green", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateMet)
	if len(rec.DoD) == 0 {
		t.Error("the minted goal record carries no Definition of Done")
	}
}

// The idle keeper stands down while the executor holds a task's run, and
// resumes once it does not.
func TestKeeperStandsDownWhileTheExecutorHoldsTheRun(t *testing.T) {
	h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
	h.al.taskExecutor.mu.Lock()
	h.al.taskExecutor.running[h.taskID] = &taskSlot{}
	h.al.taskExecutor.mu.Unlock()

	h.al.goalQuietWindowSettle(time.Now())
	if n := len(h.dispatch.all()); n != 0 {
		t.Fatalf("the keeper dispatched %d turn(s) into a run the executor holds, want 0", n)
	}

	h.al.taskExecutor.mu.Lock()
	delete(h.al.taskExecutor.running, h.taskID)
	h.al.taskExecutor.mu.Unlock()
	h.al.goalQuietWindowSettle(time.Now())
	if n := len(h.dispatch.all()); n != 1 {
		t.Fatalf("with no run held the keeper must push the quiet goal once, got %d", n)
	}
}

// resolveRunClaim for a subagent_3p worker: its marker feeds the same claim
// shapes goal_claim produces.
func TestTaskRun_ExternalCLIMarkerFeedsTheSameClaimPath(t *testing.T) {
	cases := []struct {
		name       string
		resp       string
		wantStep   runStep
		wantJudge  int
		wantStatus task.Status
		wantResult string
	}{
		{"success_with_evidence_is_judged", "done\n[goal:evidence] ran the export and diffed it\nTASK_STATUS: success",
			runStepEnded, 1, task.StatusDone, "ran the export and diffed it"},
		{"success_without_evidence_spends_a_try", "done\nTASK_STATUS: success", runStepContinue, 0, task.StatusInProgress, ""},
		{"failure_is_a_blocked_claim", "stuck\n[goal:evidence] tried twice\nTASK_STATUS: failure\nTASK_SUMMARY: no write access",
			runStepEnded, 0, task.StatusFailed, "Blocked: no write access"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newExternalCLITaskTestLoop(t, &countingProvider{})
			judge := bindMetSoftTierJudge(t, al)
			tk := &task.Task{
				Title: "external task", Prompt: "do the external task", Action: task.ActionLLM,
				AgentID: "ext-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
			}
			if err := al.taskStore.Create(tk); err != nil {
				t.Fatalf("create task: %v", err)
			}
			claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
			if err != nil {
				t.Fatalf("ClaimForRun: %v", err)
			}
			sid, serr := al.taskExecutor.createTaskSessionSync(claimed)
			if serr != nil {
				t.Fatalf("createTaskSessionSync: %v", serr)
			}

			step, _, _ := al.taskExecutor.finishRunTurn(context.Background(), claimed, sid, tc.resp, nil, "", nil,
				&taskRunState{claimWatermark: time.Now().UTC()})

			final, _ := al.taskStore.Get(tk.ID)
			if step != tc.wantStep || judge.callCount() != tc.wantJudge || final.Status != tc.wantStatus {
				t.Fatalf("step=%v Judge calls=%d status=%q, want %v/%d/%q (result %q)",
					step, judge.callCount(), final.Status, tc.wantStep, tc.wantJudge, tc.wantStatus, final.Result)
			}
			if tc.wantResult != "" && final.Result != tc.wantResult {
				t.Errorf("result = %q, want %q", final.Result, tc.wantResult)
			}
		})
	}
}
