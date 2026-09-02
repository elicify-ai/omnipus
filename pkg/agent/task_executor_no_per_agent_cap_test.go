// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// newNoPerAgentCapExecutor builds a real store-backed TaskExecutor over a
// real AgentLoop (newTestAgentLoop), with a hand-picked dispatchSema capacity
// so each test below fully controls the ONE remaining concurrency gate.
// autoSyncDispatchCapacity is deliberately left false (Go zero value) so
// nothing resizes semaCap out from under the test — same convention as every
// other bespoke TaskExecutor{} literal in this package (see
// task_executor_concurrency_test.go).
func newNoPerAgentCapExecutor(t *testing.T, semaCap int) (*TaskExecutor, *task.Store) {
	t.Helper()
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al needed
	t.Cleanup(cleanup)

	dir := t.TempDir()
	store := task.New(dir + "/tasks")

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(semaCap),
	}
	return te, store
}

// createDispatchableTask persists a `next`-status task assigned to agentID,
// ready for ExecuteTask to claim (next -> in_progress via ClaimForRun).
func createDispatchableTask(t *testing.T, store *task.Store, agentID, title string) string {
	t.Helper()
	tk := &task.Task{
		Title:       title,
		Prompt:      "hold until released (test-only blocking hook)",
		Description: "ready",
		AgentID:     agentID,
		Action:      task.ActionLLM,
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
	}
	require.NoError(t, store.Create(tk))
	return tk.ID
}

// TestTaskExecutor_NoPerAgentConcurrencyCap is the regression guard for the
// removal of the hardcoded `defaultMaxConcurrentTasksPerAgent = 3` constant
// and its enforcement gate in TaskExecutor.executeTask (deleted alongside the
// TaskExecutor.maxConcurrent field — see task_executor.go's dispatchSema doc
// comment for the full history). The removed gate counted StatusInProgress
// tasks for the dispatching agent via te.store.List(...) BEFORE ClaimForRun,
// and refused dispatch once that count reached 3 — regardless of how large
// dispatchSema (the global capacity, resolved from
// config.PerformanceConfig.EffectiveMaxParallelAgents — whose two-valued
// answer's capped flag the executor deliberately discards, since a semaphore
// capacity is not a figure shown to anyone and the live memory gate on the
// admission path is what actually bounds work) was configured. A live
// UAT measured exactly 6 concurrent tasks (2 bash-capable agents x 3) while
// the configured/reported ceiling was ~1026: the per-agent gate silently
// pinned real behavior far below the operator-configured value, so the UI
// control reported success while changing nothing.
//
// This test proves the opposite of that bug: with the per-agent gate gone,
// ONE agent can have MORE THAN 3 tasks genuinely in flight at the same
// instant, bounded only by dispatchSema.
//
// Mechanism: dispatchSema is sized to 100 (far above the 8 tasks under test,
// so it is provably not the limiter). Each task's runTask goroutine is
// intercepted by goroutineCtxHook (extended in this change to also fire from
// runTask/ExecuteTask's path, not just runTaskFromInProgress/StartTaskNow's —
// see that field's doc comment) which blocks until the test releases it, so
// every dispatched task is held genuinely in-flight (not yet completed,
// still `in_progress` in the store) at the moment the test samples peak
// concurrency. ExecuteTask itself only performs the synchronous claim/
// dispatch decision and returns immediately — the actual work runs in the
// spawned goroutine — so dispatching all 8 tasks for the SAME agent one
// after another from the test's own goroutine is exactly the real-world
// pattern (a dispatcher loop calling ExecuteTask repeatedly while earlier
// tasks are still mid-run): no artificial launch barrier is needed to prove
// concurrency, because the blocking hook is what holds each task open until
// sampled, and ExecuteTask's claim (ClaimForRun) is synchronous so dispatch
// order is fully deterministic.
//
// BDD:
//
//	Given 8 dispatchable (`next`-status) tasks all assigned to the SAME agent,
//	     and a dispatchSema capacity of 100 (never exhausted by 8 tasks),
//	When ExecuteTask is called for each of the 8 tasks in turn,
//	Then every call succeeds (no per-agent refusal), all 8 goroutines are
//	     simultaneously blocked in-flight at once, and peak observed
//	     concurrency for the single agent is > 3 (in fact == 8).
//
// CLARIFY: there is no wave-spec BDD scenario for this internal
// implementation detail (a hardcoded concurrency-cap removal is not
// user-facing behavior a spec enumerates) — this test is written directly
// from this session's task instructions and the deleted code itself
// (task_executor.go, pre-removal executeTask), not from a wave-spec line
// number.
func TestTaskExecutor_NoPerAgentConcurrencyCap(t *testing.T) {
	const agentID = testDefaultAgentID // no "main" sentinel to fall back to anymore; must be a real registered agent
	const numTasks = 8

	te, store := newNoPerAgentCapExecutor(t, 100) // sema cap >> numTasks

	taskIDs := make([]string, numTasks)
	for i := range numTasks {
		taskIDs[i] = createDispatchableTask(t, store, agentID, fmt.Sprintf("fanout-%d", i))
	}

	var active, peak atomic.Int64
	entered := make(chan struct{}, numTasks)
	releaseAll := make(chan struct{})
	finished := make(chan struct{}, numTasks)

	te.goroutineCtxHook = func(_ context.Context, _ string) {
		n := active.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		entered <- struct{}{}
		<-releaseAll
		active.Add(-1)
		finished <- struct{}{}
	}

	// Dispatch all 8 tasks for the SAME agent, one after another. ExecuteTask
	// itself returns as soon as the claim succeeds and the goroutine is
	// launched — it does not wait for the goroutine to run — so this loop
	// completes almost instantly even though every goroutine it spawns is
	// about to block indefinitely on releaseAll.
	for i, id := range taskIDs {
		err := te.ExecuteTask(context.Background(), id, nil)
		require.NoError(t, err,
			"ExecuteTask(#%d) must succeed: dispatchSema has 100 free slots and "+
				"there is NO per-agent cap (defaultMaxConcurrentTasksPerAgent was removed)", i)

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("BLOCKED: task #%d's runTask goroutine never reached goroutineCtxHook "+
				"within 5s — dispatch did not actually launch a goroutine for task %q", i, id)
		}
	}

	// Positive lower bound (project rule: every count assertion needs a
	// positive lower bound, not just "no error"). If a per-agent cap of 3
	// were silently reintroduced in executeTask, task #3 onward would have
	// failed ExecuteTask above (we would already have require.NoError-failed
	// the test) — so reaching this point with all numTasks confirmed entered
	// makes the peak assertion below a direct, deterministic proof, not a
	// timing guess.
	gotPeak := peak.Load()
	assert.Greater(t, gotPeak, int64(3),
		"peak simultaneous in-flight tasks for ONE agent must exceed the removed "+
			"per-agent cap of 3; got peak=%d", gotPeak)
	assert.Equal(t, int64(numTasks), gotPeak,
		"with no per-agent cap and dispatchSema far from exhausted, all %d tasks "+
			"for the single agent should be simultaneously in flight", numTasks)
	assert.Equal(t, int64(numTasks), active.Load(),
		"all %d goroutines must still be blocked (in flight) at the moment of sampling", numTasks)

	// Confirm the store agrees: all numTasks tasks are StatusInProgress for
	// this agent RIGHT NOW — this is exactly the count the removed gate used
	// to read via te.store.List(task.Filter{Status: StatusInProgress, AgentID:
	// agentID}) BEFORE ClaimForRun. Reproducing that same read here proves
	// the store-visible state a reintroduced gate would actually observe.
	inProgress, err := store.List(task.Filter{Status: task.StatusInProgress, AgentID: agentID})
	require.NoError(t, err)
	assert.Len(t, inProgress, numTasks,
		"the store must show all %d tasks in_progress for agent %q simultaneously", numTasks, agentID)

	// Release every blocked goroutine and confirm a clean drain — no leaked
	// dispatch-sema slots or te.running entries.
	close(releaseAll)
	for i := 0; i < numTasks; i++ {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatalf("goroutine %d did not unblock within 5s after releaseAll was closed", i)
		}
	}

	require.Eventually(t, func() bool {
		te.mu.Lock()
		defer te.mu.Unlock()
		return len(te.running) == 0
	}, 2*time.Second, 10*time.Millisecond, "te.running must drain to empty after all goroutines finish")

	assert.Equal(t, 0, te.dispatchSema.InFlight(),
		"dispatchSema must be fully released after every goroutine's deferred release() ran")
}

// TestTaskExecutor_DispatchSemaphoreStillGatesSingleAgent is the positive
// control paired with TestTaskExecutor_NoPerAgentConcurrencyCap above: it
// proves dispatchSema — now the SOLE concurrency gate on task dispatch — is
// still genuinely enforced for a single agent, using the exact same
// store-backed harness and blocking hook. Without this pairing, a suite that
// only asserts "no per-agent cap" could pass vacuously even if dispatchSema
// itself were accidentally broken (e.g. never resized, or TryAcquire always
// returning true): the "per-agent gate gone, global gate alive" pair is what
// makes the concurrency-model regression coverage trustworthy.
//
// BDD:
//
//	Given a dispatchSema capacity of 3 and 5 dispatchable tasks for the SAME
//	     agent,
//	When ExecuteTask is called for each of the 5 tasks in turn,
//	Then exactly 3 succeed and reach in-flight, and the remaining 2 fail with
//	     the specific ErrDispatchCapReached sentinel (not just "any error").
//
// CLARIFY: same as the sibling test above — no wave-spec BDD scenario exists
// for this internal mechanism; [INFERRED] from the requirement that
// dispatchSema remain "the ONLY concurrency gate on task dispatch"
// (task_executor.go's own doc comment on the field).
func TestTaskExecutor_DispatchSemaphoreStillGatesSingleAgent(t *testing.T) {
	const agentID = testDefaultAgentID // no "main" sentinel to fall back to anymore; must be a real registered agent
	const semaCap = 3
	const numTasks = 5 // > semaCap, so the excess must be refused

	te, store := newNoPerAgentCapExecutor(t, semaCap)

	taskIDs := make([]string, numTasks)
	for i := range numTasks {
		taskIDs[i] = createDispatchableTask(t, store, agentID, fmt.Sprintf("sema-gate-%d", i))
	}

	var active atomic.Int64
	entered := make(chan struct{}, numTasks)
	releaseAll := make(chan struct{})

	te.goroutineCtxHook = func(_ context.Context, _ string) {
		active.Add(1)
		entered <- struct{}{}
		<-releaseAll
		active.Add(-1)
	}

	var successes, capRejections int
	for i, id := range taskIDs {
		err := te.ExecuteTask(context.Background(), id, nil)
		if err == nil {
			successes++
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatalf("task #%d succeeded dispatch but never reached goroutineCtxHook within 5s", i)
			}
			continue
		}
		require.ErrorIs(t, err, ErrDispatchCapReached,
			"task #%d's ExecuteTask failure must be the dispatch-cap sentinel, not some other error: %v", i, err)
		capRejections++
	}

	assert.Equal(t, semaCap, successes,
		"exactly dispatchSema's capacity (%d) of ExecuteTask calls must succeed", semaCap)
	assert.Equal(t, numTasks-semaCap, capRejections,
		"the remaining %d calls must be refused with ErrDispatchCapReached", numTasks-semaCap)
	assert.Equal(t, int64(semaCap), active.Load(),
		"exactly %d goroutines should be simultaneously in flight, matching dispatchSema's cap", semaCap)

	close(releaseAll)
	require.Eventually(t, func() bool {
		return te.dispatchSema.InFlight() == 0
	}, 2*time.Second, 10*time.Millisecond, "dispatchSema must fully drain after release")
}
