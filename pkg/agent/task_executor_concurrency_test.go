// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// isDispatchCapReached is a test-local helper to avoid importing errors in the
// assertion call sites. The exported ErrDispatchCapReached sentinel is what
// rest_tasks.go uses; this helper just wraps errors.Is for readability.
func isDispatchCapReached(err error) bool {
	return errors.Is(err, ErrDispatchCapReached)
}

// TestStartTaskNow_NoConcurrentDoubleLaunch verifies that two concurrent
// StartTaskNow calls for the same task produce exactly one goroutine and one
// session. The TOCTOU fix (atomic slot reservation) is the guard under test.
//
// BDD:
//
//	Given a dispatchable task with a cap-1 semaphore,
//	When two goroutines call StartTaskNow(sameTaskID) simultaneously,
//	Then exactly one goroutine enters the goroutineCtxHook (one entry in
//	     te.running) and only one session is written to the task.
func TestStartTaskNow_NoConcurrentDoubleLaunch(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al and cleanup needed
	defer cleanup()

	dir := t.TempDir()
	store := task.New(dir + "/tasks")

	te := &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]*taskSlot),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(2), // cap>1 so sema is not the bottleneck
	}

	// Register a fake agent so the registry look-up succeeds.
	const agentID = "main"

	// Create a task in inbox → advance to next so StartTaskNow can pick it up.
	tk := &task.Task{
		Title:       "concurrency-test",
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

	// Advance to in_progress so StartTaskNow's SessionID=="" guard is satisfied
	// (StartTaskNow checks task.SessionID == "" as the idempotency guard).
	// We do NOT use ClaimForRun so the task stays dispatchable; instead we seed
	// it as in_progress with no session_id, which is the state the REST handler
	// creates right before calling StartTaskNow.
	inProg := task.StatusInProgress
	_, err = store.Update(tk.ID, task.Patch{Status: &inProg})
	require.NoError(t, err)

	// Track how many times the goroutineCtxHook fires — each represents one
	// goroutine that entered runTaskFromInProgress.
	var goroutineCount atomic.Int64

	// A barrier ensures both StartTaskNow calls are in-flight simultaneously.
	var barrier sync.WaitGroup
	barrier.Add(2)

	// The hook signals when one goroutine has entered so the test can inspect state.
	entered := make(chan struct{}, 2)
	te.goroutineCtxHook = func(_ context.Context, _ string) {
		goroutineCount.Add(1)
		entered <- struct{}{}
	}

	var (
		wg       sync.WaitGroup
		sessIDs  [2]string
		startErr [2]error
	)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			barrier.Done()
			barrier.Wait() // release both goroutines simultaneously
			sessIDs[idx], startErr[idx] = te.StartTaskNow(context.Background(), tk.ID)
		}(i)
	}

	// Wait for both callers to return.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("TestStartTaskNow_NoConcurrentDoubleLaunch: timed out waiting for goroutines")
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

	// Invariant 1: exactly one goroutine entered runTaskFromInProgress.
	count := goroutineCount.Load()
	assert.Equal(t, int64(1), count,
		"exactly one goroutine must enter runTaskFromInProgress, got %d", count)

	// Invariant 2: exactly one call succeeded and one failed.
	successes := 0
	failures := 0
	for i := range 2 {
		if startErr[i] == nil {
			successes++
		} else {
			failures++
		}
	}
	assert.Equal(t, 1, successes, "exactly one StartTaskNow call must succeed")
	assert.Equal(t, 1, failures, "exactly one StartTaskNow call must fail (already-running)")

	// Invariant 3: at most one session_id was written to the task.
	unique := make(map[string]struct{})
	for i := range 2 {
		if sessIDs[i] != "" {
			unique[sessIDs[i]] = struct{}{}
		}
	}
	assert.LessOrEqual(t, len(unique), 1,
		"at most one session_id must be created, got %v", unique)

	// Invariant 4: te.running has exactly one live entry (the goroutine) OR zero
	// if the goroutineCtxHook returned and the defer already cleaned up.
	te.mu.Lock()
	runningCount := len(te.running)
	te.mu.Unlock()
	assert.LessOrEqual(t, runningCount, 1,
		"te.running must have at most one entry after both calls return, got %d", runningCount)
}

// TestErrDispatchCapReached_IsDetectable verifies that ErrDispatchCapReached is
// returned as a wrapped error from StartTaskNow and is detectable via errors.Is.
//
// BDD:
//
//	Given a TaskExecutor whose dispatch semaphore is fully exhausted,
//	When StartTaskNow is called,
//	Then the returned error satisfies errors.Is(err, ErrDispatchCapReached)
//	and te.running has no leaked sentinel.
func TestErrDispatchCapReached_IsDetectable(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al and cleanup needed
	defer cleanup()

	dir := t.TempDir()
	store := task.New(dir + "/tasks")

	te := &TaskExecutor{
		agentLoop:    al,
		store:        store,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(1), // cap=1
	}

	// Create and advance a task so StartTaskNow can find it.
	tk := &task.Task{
		Title:       "cap-test",
		Prompt:      "do it",
		AgentID:     "main",
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

	// Exhaust the single slot before calling StartTaskNow.
	ok, releaseSlot := te.dispatchSema.TryAcquire()
	require.True(t, ok, "must be able to acquire the only slot before exhaustion")
	defer releaseSlot()

	_, startErr := te.StartTaskNow(context.Background(), tk.ID)
	require.Error(t, startErr, "StartTaskNow with exhausted sema must return an error")

	// The caller must be able to distinguish this as ErrDispatchCapReached.
	assert.True(t, isDispatchCapReached(startErr),
		"error must satisfy errors.Is(err, ErrDispatchCapReached); got: %v", startErr)

	// The reservation sentinel must have been cleaned up.
	te.mu.Lock()
	remaining := len(te.running)
	te.mu.Unlock()
	assert.Equal(t, 0, remaining,
		"te.running must be empty after a failed StartTaskNow (slot leaked)")
}
