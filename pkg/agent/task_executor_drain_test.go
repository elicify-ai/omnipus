// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_drain_test.go is the regression suite for fix-wave finding
// #1/#2: TaskExecutor.Drain and ErrExecutorDraining had ZERO test coverage
// before this file, despite Drain existing specifically to close a real
// exit-143-class CI OOM (see Drain's own doc comment, task_executor.go).
//
// Every assertion here is an OUTCOME, mirroring plan_engine_stop_drain_test.go's
// header discipline:
//
//   - "Drain waited for the in-flight goroutine" means DRAIN DID NOT RETURN
//     UNTIL THE GOROUTINE COULD HAVE FINISHED — observed by releasing it from
//     a separate goroutine on a timer and checking, after Drain returns, that
//     the release had already happened. Never a sleep used as synchronization.
//   - "refuses new dispatch" means ExecuteTask/StartTaskNow return the
//     ErrExecutorDraining sentinel specifically (errors.Is), not just any
//     error.
//   - "the redispatch chain terminates at its next hop" means a goal-loop
//     redispatch chain that would otherwise keep re-attempting the SAME task
//     stops after exactly one more attempt once draining is set mid-chain —
//     proven by a real dispatch-call counter, not an inference from log text.
package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newDrainTestExecutorWithProvider was deleted 2026-09-24 as an
// unreachable ADR-091 leftover (golangci unused): grep found no caller
// anywhere in the repo.

// --- (a) Drain waits for a genuinely in-flight task-dispatch goroutine -----

// TestTaskExecutor_Drain_WaitsForInFlightGoroutine proves Drain's core
// contract: it must not return while a task-dispatch goroutine it tracks via
// wg is still running. Mirrors
// TestPlanEngineStop_DrainsWakeTurnDispatchedByNeverStartedEngine's technique
// (plan_engine_stop_drain_test.go): hold the goroutine open on a channel,
// release it from a timer, and check Drain could only have returned after
// that release.
func TestTaskExecutor_Drain_WaitsForInFlightGoroutine(t *testing.T) {
	te, store := newNoPerAgentCapExecutor(t, 10)
	taskID := createDispatchableTask(t, store, "mia", "drain-inflight")

	entered := make(chan struct{})
	gate := make(chan struct{})
	te.goroutineCtxHook = func(_ context.Context, _ string) {
		close(entered)
		<-gate
	}

	require.NoError(t, te.ExecuteTask(context.Background(), taskID, nil))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runTask goroutine never reached goroutineCtxHook within 5s")
	}

	const hold = 300 * time.Millisecond
	var released atomic.Bool
	go func() {
		time.Sleep(hold)
		released.Store(true)
		close(gate)
	}()

	start := time.Now()
	te.Drain(5 * time.Second)
	elapsed := time.Since(start)

	if !released.Load() {
		t.Fatalf("Drain() returned in %v while the in-flight task goroutine it is supposed to wait for "+
			"was STILL blocked — wg is not tracking it", elapsed)
	}
	if elapsed < hold {
		t.Fatalf("Drain() returned after %v but the goroutine was held for %v — Drain did not actually wait for it",
			elapsed, hold)
	}
}

// TestTaskExecutor_Drain_BoundsOnWedgedGoroutine covers the other half: a
// goroutine that never releases still lets Drain return, on budget, rather
// than hanging teardown forever. Mirrors
// TestPlanEngineStop_BoundsDrainOnStartedEngineWithWedgedWakeTurn.
func TestTaskExecutor_Drain_BoundsOnWedgedGoroutine(t *testing.T) {
	te, store := newNoPerAgentCapExecutor(t, 10)
	taskID := createDispatchableTask(t, store, "mia", "drain-wedged")

	entered := make(chan struct{})
	gate := make(chan struct{}) // never closed — this goroutine is wedged for good
	t.Cleanup(func() { close(gate) })
	te.goroutineCtxHook = func(_ context.Context, _ string) {
		close(entered)
		<-gate
	}

	require.NoError(t, te.ExecuteTask(context.Background(), taskID, nil))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runTask goroutine never reached goroutineCtxHook within 5s")
	}

	const budget = 250 * time.Millisecond
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		te.Drain(budget)
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		if elapsed < budget {
			t.Fatalf("Drain() returned in %v, faster than its own %v budget, with a goroutine still "+
				"wedged — the bound is not being enforced", elapsed, budget)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Drain() never returned with a wedged goroutine in flight — shutdown is hostage to it")
	}
}

// --- (b) Once draining, ExecuteTask/StartTaskNow refuse new dispatch -------

// TestTaskExecutor_Drain_RefusesNewDispatch proves the second half of
// finding #1's fix: once Drain has set the flag (here with nothing in
// flight, so it returns immediately), BOTH ExecuteTask and StartTaskNow must
// refuse a brand-new, never-touched task with ErrExecutorDraining — the
// specific sentinel, not merely "some error" — and must do so WITHOUT ever
// claiming the task (it stays `next`, never `in_progress`).
func TestTaskExecutor_Drain_RefusesNewDispatch(t *testing.T) {
	te, store := newNoPerAgentCapExecutor(t, 10)

	// Nothing in flight: Drain must return immediately and leave draining set.
	start := time.Now()
	te.Drain(5 * time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Drain() with nothing in flight took %v — should return almost instantly", elapsed)
	}

	execTaskID := createDispatchableTask(t, store, "mia", "refused-execute-task")
	err := te.ExecuteTask(context.Background(), execTaskID, nil)
	require.ErrorIs(t, err, ErrExecutorDraining,
		"ExecuteTask must refuse a new dispatch once draining with the ErrExecutorDraining sentinel")
	unclaimed, getErr := store.Get(execTaskID)
	require.NoError(t, getErr)
	assert.Equal(t, task.StatusNext, unclaimed.Status,
		"a task refused by the draining gate must never be claimed (still `next`, not `in_progress`)")

	startNowTaskID := createDispatchableTask(t, store, "mia", "refused-start-task-now")
	sessionID, err := te.StartTaskNow(context.Background(), startNowTaskID)
	require.ErrorIs(t, err, ErrExecutorDraining,
		"StartTaskNow must refuse a new dispatch once draining with the ErrExecutorDraining sentinel")
	assert.Empty(t, sessionID, "StartTaskNow must not return a session id on refusal")
	unclaimed2, getErr := store.Get(startNowTaskID)
	require.NoError(t, getErr)
	assert.Empty(t, unclaimed2.SessionID,
		"a task refused by StartTaskNow's draining gate must never get a session created for it")
}

// --- (c) A goal-loop redispatch chain terminates at its next hop -----------

// drainMidChainProvider wraps a goal_claim worker for the restart test below.
// The FIRST turn it starts fires onFirstCall SYNCHRONOUSLY, before that turn's
// claim is returned — letting the test flip te.draining from INSIDE the
// currently-executing run, deterministically (no sleeps): the restart a failed
// run hands back is dispatched strictly after the run returns (runTask's
// outermost deferred closure), so by the time that restart reaches
// ExecuteTask's entry gate, draining is already guaranteed to be true.
type drainMidChainProvider struct {
	worker      *claimingWorker
	once        sync.Once
	onFirstCall func()
}

func (p *drainMidChainProvider) Chat(
	ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	resp, err := p.worker.Chat(ctx, msgs, defs, model, opts)
	if p.onFirstCall != nil && p.worker.turnsStarted() >= 1 {
		p.once.Do(p.onFirstCall)
	}
	return resp, err
}

func (p *drainMidChainProvider) GetDefaultModel() string { return "drain-mid-chain-model" }

// TestTaskExecutor_Drain_GoalLoopRedispatchChainTerminatesAtNextHop is
// scenario (c) from the fix-wave finding: a chain of task restarts that would
// otherwise keep re-running the SAME task (the judge always reports "unmet",
// each run has one goal try, and max_attempts leaves plenty of budget) stops
// after exactly ONE run once draining is set DURING that run's own execution —
// proving the entry-level wg.Add-then-check-draining ordering (finding #1)
// actually closes the window: the first run is allowed to finish (its own
// store writes are not caught mid-flight), but the restart it hands back is
// refused, so the chain never reaches a second worker turn.
func TestTaskExecutor_Drain_GoalLoopRedispatchChainTerminatesAtNextHop(t *testing.T) {
	worker := &drainMidChainProvider{worker: newClaimingWorker(turnClaimMet("verified against the acceptance criterion"))}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
	judgeInst.Provider = alwaysUnmetJudgeProvider()

	// Flip draining from INSIDE the first (and, if this fix regresses, only
	// the first of MANY) worker turn — see drainMidChainProvider's doc
	// comment for why this is deterministic rather than a timing guess.
	worker.onFirstCall = func() { al.taskExecutor.draining.Store(true) }

	maxAttempts := 5 // far more than the 1 run this test expects to see
	tk := &task.Task{
		Title: "redispatch-chain-vs-drain", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	require.NoError(t, al.taskStore.Create(tk))

	require.NoError(t, al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil))

	// The first run spends its one goal try, fails, and consumeTaskAttempt
	// re-queues the task as `next` with AttemptCount==1 before handing back
	// the restart; wait for that rather than sleeping a fixed duration.
	require.Eventually(t, func() bool {
		cur, err := al.taskStore.Get(tk.ID)
		return err == nil && cur.AttemptCount == 1 && cur.Status == task.StatusNext
	}, 10*time.Second, 10*time.Millisecond,
		"task must settle at AttemptCount=1, status=next (the failed first run's own outcome) within 10s")

	// Give any (incorrect, if the fix regressed) restart a real window to
	// occur before asserting the chain stayed at exactly one run — a bare
	// Eventually success above only proves run #1 landed, not that no run #2
	// ever started.
	require.Never(t, func() bool {
		return worker.worker.turnsStarted() > 1
	}, 500*time.Millisecond, 20*time.Millisecond,
		"the restart chain must terminate at its next hop once draining — "+
			"a second worker turn means ExecuteTask's entry-level draining check "+
			"is not actually gating the restart runTask's trailing defer dispatches")

	final, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, final.AttemptCount,
		"exactly one attempt must have been consumed — the refused restart must not double-increment it")
	assert.Equal(t, task.StatusNext, final.Status,
		"the task must be left at `next` (re-queued by the first run's own outcome), "+
			"never advanced to `in_progress` by a second dispatch that should have been refused")
}

// notifyParentIfAllSiblingsDone (and its dedicated wg-tracked follow-up
// goroutine) is DELETED by ADR-091 D3/FR-B-003: "the system MUST wake per
// child; task_executor_judge.go::notifyParentIfAllSiblingsDone is deleted."
// Its replacement, deliverTaskCompletionUpward (task_executor_judge.go),
// calls steer.UpwardDeliverer.Deliver synchronously — no goroutine, so the
// wg-tracking regression this test file's former
// TestTaskExecutor_Drain_WaitsForParentFollowUpGoroutine guarded no longer
// applies (there is nothing left to leak past Drain).
