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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newDrainTestExecutorWithProvider mirrors newNoPerAgentCapExecutor
// (task_executor_no_per_agent_cap_test.go) but takes an explicit provider —
// needed here so TestTaskExecutor_Drain_GoalLoopRedispatchChainTerminatesAtNextHop
// can synchronously flip te.draining from INSIDE the worker's own Chat call,
// something goroutineCtxHook's short-circuit-before-finishTaskRun seam cannot
// reach (it returns before the goal-loop's redispatch decision is ever made).
func newDrainTestExecutorWithProvider(t *testing.T, provider providers.LLMProvider) (*TaskExecutor, *task.Store, *AgentLoop) {
	t.Helper()
	tmpDir := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	dir := t.TempDir()
	store := task.New(dir + "/tasks")

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(100),
	}
	return te, store, al
}

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

// drainMidChainProvider is the LLM provider for the goal-loop redispatch
// test below. Its FIRST Chat call fires onFirstCall SYNCHRONOUSLY, before
// returning the scripted "success + evidence" claim — letting the test flip
// te.draining from INSIDE the currently-executing attempt, deterministically
// (no sleeps): the redispatch this attempt's own trailing defer performs
// happens strictly after Chat returns (see runTask's doc comment on why the
// redispatch call is made from the outermost deferred closure), so by the
// time that redispatch call reaches ExecuteTask's entry gate, draining is
// already guaranteed to be true.
type drainMidChainProvider struct {
	mu           sync.Mutex
	calls        int
	onFirstCall  func()
	responseBody string
}

func (p *drainMidChainProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first && p.onFirstCall != nil {
		p.onFirstCall()
	}
	return &providers.LLMResponse{Content: p.responseBody}, nil
}

func (p *drainMidChainProvider) GetDefaultModel() string { return "drain-mid-chain-model" }

func (p *drainMidChainProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestTaskExecutor_Drain_GoalLoopRedispatchChainTerminatesAtNextHop is
// scenario (c) from the fix-wave finding: a goal-loop redispatch chain that
// would otherwise keep re-attempting the SAME task (the judge always reports
// "unmet", and MaxAttempts leaves plenty of budget) stops after exactly ONE
// real dispatch once draining is set DURING that first attempt's own
// execution — proving the entry-level wg.Add-then-check-draining ordering
// (finding #1) actually closes the window: the first attempt is allowed to
// run to completion (its own store writes are not caught mid-flight), but
// its trailing self-redispatch is refused, so the chain never reaches a
// second real LLM call.
func TestTaskExecutor_Drain_GoalLoopRedispatchChainTerminatesAtNextHop(t *testing.T) {
	worker := &drainMidChainProvider{
		responseBody: "did the work\n[goal:evidence] verified against the acceptance criterion\n" +
			"TASK_STATUS: success\nTASK_SUMMARY: I finished it.",
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = alwaysUnmetJudgeProvider()

	// Flip draining from INSIDE the first (and, if this fix regresses, only
	// the first of MANY) worker Chat call — see drainMidChainProvider's doc
	// comment for why this is deterministic rather than a timing guess.
	worker.onFirstCall = func() { al.taskExecutor.draining.Store(true) }

	maxAttempts := 5 // far more than the 1 attempt this test expects to see
	tk := &task.Task{
		Title: "redispatch-chain-vs-drain", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	require.NoError(t, al.taskStore.Create(tk))

	require.NoError(t, al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil))

	// The first (only) attempt runs synchronously to completion inside the
	// dispatched goroutine; wait for it to land back in the store as `next`
	// with AttemptCount==1 (consumeAttemptOrExhaust's unmet-with-budget-left
	// outcome) rather than sleeping a fixed duration.
	require.Eventually(t, func() bool {
		cur, err := al.taskStore.Get(tk.ID)
		return err == nil && cur.AttemptCount == 1 && cur.Status == task.StatusNext
	}, 5*time.Second, 10*time.Millisecond,
		"task must settle at AttemptCount=1, status=next (the first attempt's own outcome) within 5s")

	// Give any (incorrect, if the fix regressed) further redispatch a real
	// window to occur before asserting the chain stayed at exactly one call —
	// a bare Eventually success above only proves attempt #1 landed, not
	// that no attempt #2 ever started.
	require.Never(t, func() bool {
		return worker.callCount() > 1
	}, 500*time.Millisecond, 20*time.Millisecond,
		"the goal-loop redispatch chain must terminate at its next hop once draining — "+
			"a second real worker dispatch means ExecuteTask's entry-level draining check "+
			"is not actually gating the self-redispatch call runTask's trailing defer makes")

	final, err := al.taskStore.Get(tk.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, final.AttemptCount,
		"exactly one attempt must have been consumed — the refused redispatch must not double-increment it")
	assert.Equal(t, task.StatusNext, final.Status,
		"the task must be left at `next` (re-queued by the first attempt's own outcome), "+
			"never advanced to `in_progress` by a second dispatch that should have been refused")
}

// --- notifyParentIfAllSiblingsDone's follow-up goroutine is wg-tracked -----

// gatedParentFollowUpProvider blocks the parent's follow-up turn on a gate
// channel, signaling entry once — the same "hold it open, release on a
// timer" technique as TestTaskExecutor_Drain_WaitsForInFlightGoroutine,
// applied to notifyParentIfAllSiblingsDone's own goroutine (task_executor.go)
// rather than runTask's.
type gatedParentFollowUpProvider struct {
	enteredOnce sync.Once
	entered     chan struct{}
	gate        chan struct{}
}

func (g *gatedParentFollowUpProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	g.enteredOnce.Do(func() { close(g.entered) })
	<-g.gate
	return &providers.LLMResponse{Content: "ack"}, nil
}

func (g *gatedParentFollowUpProvider) GetDefaultModel() string { return "gated-parent-followup-model" }

// TestTaskExecutor_Drain_WaitsForParentFollowUpGoroutine proves the second
// half of finding #1: notifyParentIfAllSiblingsDone's own `go func(){...}()`
// (task_executor.go, calling processTaskDirect) is now wg-tracked, so Drain
// waits for it exactly like it waits for a runTask goroutine — before this
// fix, this goroutine had NO wg tracking at all and Drain could return while
// it was still writing through stores Close() was about to tear down.
func TestTaskExecutor_Drain_WaitsForParentFollowUpGoroutine(t *testing.T) {
	provider := &gatedParentFollowUpProvider{entered: make(chan struct{}), gate: make(chan struct{})}
	te, store, _ := newDrainTestExecutorWithProvider(t, provider)

	parent := &task.Task{
		Title: "parent", Prompt: "parent", Action: task.ActionLLM,
		AgentID: "mia", Priority: 3, WorkspaceID: "default", Status: task.StatusInProgress,
	}
	require.NoError(t, store.Create(parent))
	child := &task.Task{
		Title: "child", Prompt: "child", Action: task.ActionLLM,
		AgentID: "mia", Priority: 3, WorkspaceID: "default", Status: task.StatusDone,
		ParentTaskID: parent.ID,
	}
	require.NoError(t, store.Create(child))

	// Drives the exact same call onTaskComplete makes for a just-completed
	// child (task_executor.go) — every sibling is terminal, the parent is
	// in_progress and not yet followed-up, so this launches the tracked
	// goroutine under test.
	te.notifyParentIfAllSiblingsDone(parent.ID)

	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("parent follow-up goroutine never reached the provider within 5s")
	}

	const hold = 300 * time.Millisecond
	var released atomic.Bool
	go func() {
		time.Sleep(hold)
		released.Store(true)
		close(provider.gate)
	}()

	start := time.Now()
	te.Drain(5 * time.Second)
	elapsed := time.Since(start)

	if !released.Load() {
		t.Fatalf("Drain() returned in %v while the parent follow-up goroutine was still blocked in the "+
			"provider — it is not wg-tracked", elapsed)
	}
	if elapsed < hold {
		t.Fatalf("Drain() returned after %v but the follow-up goroutine was held for %v — Drain did not "+
			"actually wait for it", elapsed, hold)
	}
}
