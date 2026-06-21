// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// newTestTaskExecutor builds a minimal TaskExecutor backed by a real TaskStore
// in a temp directory.  agentLoop is nil (tests must not call methods that
// need it).
func newTestTaskExecutor(t *testing.T) (*TaskExecutor, *taskstore.TaskStore) {
	t.Helper()
	dir := t.TempDir()
	store := taskstore.New(filepath.Join(dir, "tasks"))
	te := &TaskExecutor{
		agentLoop:     nil, // not needed for orchestrator/store logic
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}
	return te, store
}

func createTask(t *testing.T, store *taskstore.TaskStore, status string, blockedBy []string) *taskstore.TaskEntity {
	t.Helper()
	e := &taskstore.TaskEntity{
		Title:       "test",
		Prompt:      "do thing",
		AgentID:     "jim",
		Priority:    3,
		TriggerType: "manual",
		Status:      "queued",
		BlockedBy:   blockedBy,
	}
	if err := store.Create(e); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if status != "queued" {
		now := time.Now().UTC()
		var patch taskstore.TaskPatch
		switch status {
		case "running":
			patch = taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}
		case "completed":
			// queued → running → completed requires two transitions.
			patch = taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}
			updated, err := store.Update(e.ID, patch)
			if err != nil {
				t.Fatalf("update to running: %v", err)
			}
			e = updated
			patch = taskstore.TaskPatch{Status: ptrStr("completed"), CompletedAt: &now}
		case "failed":
			patch = taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}
			updated, err := store.Update(e.ID, patch)
			if err != nil {
				t.Fatalf("update to running: %v", err)
			}
			e = updated
			patch = taskstore.TaskPatch{Status: ptrStr("failed"), CompletedAt: &now}
		}
		updated, err := store.Update(e.ID, patch)
		if err != nil {
			t.Fatalf("update to %q: %v", status, err)
		}
		e = updated
	}
	return e
}

// sortedSet returns ids as a set for order-independent comparison.
func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// TestOrchestratorAdvance_UnblockedTaskFoundAfterDep drives the REAL
// readyBlockedCandidates decision: when the sole dependency is completed, the
// downstream task must be reported as ready to advance.
func TestOrchestratorAdvance_UnblockedTaskFoundAfterDep(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	dep := createTask(t, store, "completed", nil)
	downstream := createTask(t, store, "queued", []string{dep.ID})

	ready := te.readyBlockedCandidates(dep.ID)
	if len(ready) != 1 || ready[0] != downstream.ID {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want [%q]", dep.ID, ready, downstream.ID)
	}
}

// TestOrchestratorAdvance_StillBlockedWhenDepNotComplete drives the REAL
// readyBlockedCandidates: a task gated on two deps where one is still queued
// must NOT be reported ready, even though the OTHER dep just completed.
func TestOrchestratorAdvance_StillBlockedWhenDepNotComplete(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	depA := createTask(t, store, "completed", nil)
	depB := createTask(t, store, "queued", nil) // not yet completed

	_ = createTask(t, store, "queued", []string{depA.ID, depB.ID})

	// depA completed → advance check keyed on depA. depB is still queued, so the
	// downstream task's FULL dependency set is not satisfied: nothing is ready.
	ready := te.readyBlockedCandidates(depA.ID)
	if len(ready) != 0 {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want [] (depB still queued)", depA.ID, ready)
	}

	// Now complete depB and re-check keyed on depB — the downstream task is ready.
	now := time.Now().UTC()
	if _, err := store.Update(depB.ID, taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}); err != nil {
		t.Fatalf("update depB→running: %v", err)
	}
	if _, err := store.Update(
		depB.ID,
		taskstore.TaskPatch{Status: ptrStr("completed"), CompletedAt: &now},
	); err != nil {
		t.Fatalf("update depB→completed: %v", err)
	}
	ready = te.readyBlockedCandidates(depB.ID)
	if len(ready) != 1 {
		t.Fatalf("readyBlockedCandidates(%q) after depB completes = %v, want 1 ready", depB.ID, ready)
	}
}

// TestOrchestratorAdvance_FailedDepDoesNotUnblock drives the REAL
// readyBlockedCandidates: a FAILED dependency must NOT satisfy the gate — only
// "completed" unblocks.
func TestOrchestratorAdvance_FailedDepDoesNotUnblock(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	dep := createTask(t, store, "failed", nil)
	_ = createTask(t, store, "queued", []string{dep.ID})

	ready := te.readyBlockedCandidates(dep.ID)
	if len(ready) != 0 {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want [] (dep failed, not completed)", dep.ID, ready)
	}
}

// TestOrchestratorAdvance_MultipleDownstreamTasks drives the REAL
// readyBlockedCandidates: every task gated solely on a just-completed dep must be
// reported ready, not just the first one.
func TestOrchestratorAdvance_MultipleDownstreamTasks(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	dep := createTask(t, store, "completed", nil)

	d1 := createTask(t, store, "queued", []string{dep.ID})
	d2 := createTask(t, store, "queued", []string{dep.ID})
	d3 := createTask(t, store, "queued", []string{dep.ID})

	ready := te.readyBlockedCandidates(dep.ID)
	if len(ready) != 3 {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want 3 ready", dep.ID, ready)
	}
	got := idSet(ready)
	for _, want := range []string{d1.ID, d2.ID, d3.ID} {
		if !got[want] {
			t.Errorf("expected ready candidate %q not found in %v", want, ready)
		}
	}
}

// createChild creates a child task with the given parent and status.
func createChild(t *testing.T, store *taskstore.TaskStore, parentID, status string) *taskstore.TaskEntity {
	t.Helper()
	e := &taskstore.TaskEntity{
		Title:        "child",
		Prompt:       "do thing",
		AgentID:      "jim",
		Priority:     3,
		TriggerType:  "manual",
		Status:       "queued",
		ParentTaskID: parentID,
	}
	if err := store.Create(e); err != nil {
		t.Fatalf("create child: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.Update(e.ID, taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}); err != nil {
		t.Fatalf("child→running: %v", err)
	}
	final := status
	if final == "queued" || final == "running" {
		got, err := store.Get(e.ID)
		if err != nil {
			t.Fatalf("get child: %v", err)
		}
		return got
	}
	updated, err := store.Update(e.ID, taskstore.TaskPatch{Status: ptrStr(final), CompletedAt: &now})
	if err != nil {
		t.Fatalf("child→%s: %v", final, err)
	}
	return updated
}

// TestParentFollowUp_ConcurrentSiblings_ExactlyOne is the duplicate-parent-
// follow-up race regression test. N sibling-completion callbacks fire
// concurrently for the same running parent with all children already done; the
// atomic ClaimParentFollowUp must ensure the parent follow-up fires exactly once.
func TestParentFollowUp_ConcurrentSiblings_ExactlyOne(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	// Parent in "running" state.
	parent := &taskstore.TaskEntity{
		Title: "parent", Prompt: "p", AgentID: "jim", Priority: 3,
		TriggerType: "manual", Status: "queued",
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.Update(parent.ID, taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}); err != nil {
		t.Fatalf("parent→running: %v", err)
	}

	// All children already completed (so every concurrent caller observes allDone).
	const numChildren = 8
	for i := 0; i < numChildren; i++ {
		createChild(t, store, parent.ID, "completed")
	}

	// Wire the test seam to count follow-up fires.
	var fires int64
	te.parentFollowUp = func(parentID string) {
		atomic.AddInt64(&fires, 1)
	}

	// Fire N concurrent sibling-completion notifications for the same parent.
	const N = 16
	var wg sync.WaitGroup
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start
			te.notifyParentIfAllSiblingsDone(parent.ID)
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&fires); got != 1 {
		t.Fatalf("parent follow-up fired %d times, want exactly 1", got)
	}

	// The parent must be flagged FollowedUp on disk.
	reloaded, err := store.Get(parent.ID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if !reloaded.FollowedUp {
		t.Fatal("parent FollowedUp must be true after the follow-up fired")
	}
}

// TestParentFollowUp_NotAllSiblingsDone verifies no follow-up fires while a
// sibling is still running.
func TestParentFollowUp_NotAllSiblingsDone(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	parent := &taskstore.TaskEntity{
		Title: "parent", Prompt: "p", AgentID: "jim", Priority: 3,
		TriggerType: "manual", Status: "queued",
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.Update(parent.ID, taskstore.TaskPatch{Status: ptrStr("running"), StartedAt: &now}); err != nil {
		t.Fatalf("parent→running: %v", err)
	}

	createChild(t, store, parent.ID, "completed")
	createChild(t, store, parent.ID, "running") // still running → not all done

	var fires int64
	te.parentFollowUp = func(string) { atomic.AddInt64(&fires, 1) }

	te.notifyParentIfAllSiblingsDone(parent.ID)

	if got := atomic.LoadInt64(&fires); got != 0 {
		t.Fatalf("follow-up fired %d times while a sibling is still running, want 0", got)
	}
}

// TestDispatchSema_TaskExecutor_ExecuteTask_SemaRejection verifies that
// ExecuteTask respects the dispatch semaphore cap.
func TestDispatchSema_TaskExecutor_ExecuteTask_SemaRejection(t *testing.T) {
	_, store := newTestTaskExecutor(t)

	// Create a task that is "queued" with no blocked_by.
	task := createTask(t, store, "queued", nil)

	// Build a minimal TaskExecutor with cap=0 (which clamps to 1) — but then
	// fill the semaphore manually so TryAcquire fails.
	te := &TaskExecutor{
		agentLoop:     nil,
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: 5,
		dispatchSema:  newDispatchSemaphore(1),
	}

	// Pre-fill the single slot.
	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		t.Fatal("pre-fill should succeed")
	}
	defer release()

	// ExecuteTask should fail with a concurrency error, not a nil-deref.
	err := te.ExecuteTask(context.Background(), task.ID)
	if err == nil {
		t.Fatal("expected error when dispatch sema is full")
	}
	t.Logf("got expected error: %v", err)
}
