// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// fakeTaskLauncher is a steer.SessionLauncher whose Launch mints a unique
// session id per call and whose Dispatch routes the launched session into the
// REAL task_executor.go::dispatchLaunchedTask — the same executor entry the
// production dispatch chain (dispatchSteeredSessionWithReservation) reaches
// after its steer-level admission. Everything upstream of the executor
// (lifecycle records, admission, queueing) is deliberately faked: the unit
// under test is the executor's own launch-slot claim/adoption discipline.
type fakeTaskLauncher struct {
	te *TaskExecutor

	mu       sync.Mutex
	launches int
	taskID   string
}

func (f *fakeTaskLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches++
	f.taskID = req.Origin.TaskID
	return steer.LaunchResult{SessionID: fmt.Sprintf("sess-%d", f.launches), Generation: 1}, nil
}

func (f *fakeTaskLauncher) Dispatch(_ context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	f.mu.Lock()
	taskID := f.taskID
	f.mu.Unlock()
	rec := &session.LifecycleRecord{
		SessionID: sessionID,
		Origin: &session.Origin{
			Kind:   session.OriginKindTask,
			TaskID: taskID,
		},
	}
	if err := f.te.dispatchLaunchedTask(rec, func() {}); err != nil {
		return steer.DispatchResult{}, err
	}
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

// TestStartTaskNow_LauncherPath_NoDoubleLaunch is the launcher-path twin of
// TestStartTaskNow_NoConcurrentDoubleLaunch: the production gateway boot
// always wires a session launcher (gateway_boot.go), so every production
// StartTaskNow goes through startTaskNowViaLauncher — which, before this
// fix, held NO launch-slot claim at all. Two concurrent calls on a fresh task
// both reached SteerLauncher.Launch (which has no per-task dedup), minted two
// sessions, and the loser could persist its orphan session id over the
// winner's binding while the winner's run went live in the other session.
//
// BDD:
//
//	Given a fresh task (no session_id) on an executor WITH a launcher wired,
//	When two goroutines call StartTaskNow(sameTaskID) simultaneously,
//	Then exactly one Launch happens, exactly one caller succeeds, the loser
//	     fails with the typed already-running sentinel, and the task record is
//	     bound to the WINNER's session (never the loser's orphan).
func TestStartTaskNow_LauncherPath_NoDoubleLaunch(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al and cleanup needed
	defer cleanup()

	dir := t.TempDir()
	store := task.New(dir + "/tasks")

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(2), // cap>1 so sema is not the bottleneck
	}
	launcher := &fakeTaskLauncher{te: te}
	te.SetSessionLauncher(launcher)

	// newTestAgentLoop registers testDefaultAgentID — a real registered agent
	// (mirrors TestStartTaskNow_NoConcurrentDoubleLaunch's note).
	const agentID = testDefaultAgentID

	tk := &task.Task{
		Title:       "launcher-race-test",
		Prompt:      "do it",
		AgentID:     agentID,
		Action:      task.ActionLLM,
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusInbox,
	}
	require.NoError(t, store.Create(tk))
	nextStatus := task.StatusNext
	desc := "ready"
	_, err := store.Update(tk.ID, task.Patch{Status: &nextStatus, Description: &desc})
	require.NoError(t, err)
	inProg := task.StatusInProgress
	_, err = store.Update(tk.ID, task.Patch{Status: &inProg})
	require.NoError(t, err)

	// Hook seam: each runTaskFromInProgress goroutine signals and returns
	// without real execution, so the test observes entry counts, not LLM work.
	var goroutines atomic.Int64
	entered := make(chan struct{}, 2)
	te.goroutineCtxHook = func(_ context.Context, _ string) {
		goroutines.Add(1)
		entered <- struct{}{}
	}

	var (
		wg       sync.WaitGroup
		sessIDs  [2]string
		startErr [2]error
	)
	barrier := sync.WaitGroup{}
	barrier.Add(2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			barrier.Done()
			barrier.Wait() // release both goroutines simultaneously
			sessIDs[idx], startErr[idx] = te.StartTaskNow(context.Background(), tk.ID)
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("TestStartTaskNow_LauncherPath_NoDoubleLaunch: timed out waiting for callers")
	}

	// Drain the hook channel (≤2 signals).
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case <-entered:
		case <-timeout:
			break loop
		}
	}

	t.Cleanup(func() {
		te.Drain(10 * time.Second)
	})

	// Invariant 1: exactly one Launch — the whole point of the claim on the
	// launcher path. (Red on the pre-fix code: both callers launched.)
	launcher.mu.Lock()
	launches := launcher.launches
	launcher.mu.Unlock()
	assert.Equal(t, 1, launches,
		"exactly one Launch must happen per task, got %d", launches)

	// Invariant 2: exactly one caller succeeded and one failed.
	successes, failures := 0, 0
	for i := range 2 {
		if startErr[i] == nil {
			successes++
		} else {
			failures++
		}
	}
	assert.Equal(t, 1, successes, "exactly one StartTaskNow call must succeed")
	assert.Equal(t, 1, failures, "exactly one StartTaskNow call must fail (already-running)")

	// Invariant 3: the loser fails with the typed sentinel callers map to the
	// safe no-op (rest_tasks.go / run_task.go).
	loserErr := error(nil)
	for i := range 2 {
		if startErr[i] != nil {
			loserErr = startErr[i]
		}
	}
	require.Error(t, loserErr, "the losing caller must carry an error")
	assert.ErrorIs(t, loserErr, task.ErrAlreadyRunning,
		"loser error must satisfy errors.Is(err, task.ErrAlreadyRunning); got: %v", loserErr)

	// Invariant 4: the task record is bound to the WINNER's session — never
	// to a loser's orphan session. (Red on the pre-fix code: the second
	// Launch's persist could overwrite the binding with its orphan session.)
	launcher.mu.Lock()
	winnerSession := "sess-1"
	launcher.mu.Unlock()
	fresh, getErr := store.Get(tk.ID)
	require.NoError(t, getErr, "task re-read must succeed")
	assert.Equal(t, winnerSession, fresh.SessionID,
		"task must be bound to the winner's session, got %q", fresh.SessionID)

	// Invariant 5: exactly one run goroutine entered orchestration.
	assert.Equal(t, int64(1), goroutines.Load(),
		"exactly one run goroutine must enter orchestration, got %d", goroutines.Load())
}
