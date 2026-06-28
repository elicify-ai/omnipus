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

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// ptrStatus returns a pointer to a task.Status value, for use with task.Patch.
func ptrStatus(s task.Status) *task.Status { return &s }

// newTestTaskExecutor builds a minimal TaskExecutor backed by a real task.Store
// in a temp directory. agentLoop is nil (tests must not call methods that need it).
func newTestTaskExecutor(t *testing.T) (*TaskExecutor, *task.Store) {
	t.Helper()
	dir := t.TempDir()
	store := task.New(filepath.Join(dir, "tasks"))
	te := &TaskExecutor{
		agentLoop:     nil, // not needed for orchestrator/store logic
		store:         store,
		running:       make(map[string]*taskSlot),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(4),
	}
	return te, store
}

// createTask creates a task.Task with the given status and blocked_by list.
// The new 7-state vocabulary maps old "queued"→next, "running"→in_progress,
// "completed"→done, "failed"→failed.
//
// readyBlockedCandidates looks for tasks in StatusNext that still carry the
// completed dep in their BlockedBy list, so downstream tasks are created with
// StatusNext (not StatusBlocked) to match the post-advance state that
// AdvanceBlockedDependents would produce.
func createTask(t *testing.T, store *task.Store, status string, blockedBy []string) *task.Task {
	t.Helper()
	// Map legacy status names to the 7-state vocab.
	var initialStatus task.Status
	switch status {
	case "completed", "done":
		initialStatus = task.StatusDone
	case "running", "in_progress":
		initialStatus = task.StatusInProgress
	case "failed":
		initialStatus = task.StatusFailed
	default:
		// "queued" → next (dispatchable); any blocked downstream task is also
		// seeded as next so readyBlockedCandidates can find it.
		initialStatus = task.StatusNext
	}

	e := &task.Task{
		Title:       "test",
		Prompt:      "do thing",
		AgentID:     "jim",
		Priority:    3,
		Action:      task.ActionLLM,
		Status:      initialStatus,
		WorkspaceID: "default",
		BlockedBy:   blockedBy,
	}
	if err := store.Create(e); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// If we need a terminal status that requires intermediate transitions:
	// The new store has NO transition state machine, so we can set any status
	// directly via Update.
	switch status {
	case "completed", "done":
		// Already set to Done via initial status above; just stamp timestamps.
		now := time.Now().UTC().Format(time.RFC3339)
		updated, err := store.Update(e.ID, task.Patch{
			Status:      ptrStatus(task.StatusDone),
			CompletedAt: &now,
		})
		if err != nil {
			t.Fatalf("stamp done timestamps: %v", err)
		}
		return updated
	case "running", "in_progress":
		now := time.Now().UTC().Format(time.RFC3339)
		updated, err := store.Update(e.ID, task.Patch{
			Status:    ptrStatus(task.StatusInProgress),
			StartedAt: &now,
		})
		if err != nil {
			t.Fatalf("stamp in_progress timestamps: %v", err)
		}
		return updated
	case "failed":
		now := time.Now().UTC().Format(time.RFC3339)
		updated, err := store.Update(e.ID, task.Patch{
			Status:      ptrStatus(task.StatusFailed),
			CompletedAt: &now,
		})
		if err != nil {
			t.Fatalf("stamp failed timestamps: %v", err)
		}
		return updated
	}
	// For "queued"/"next": task is already StatusNext with no extra stamps.
	got, err := store.Get(e.ID)
	if err != nil {
		t.Fatalf("get task after create: %v", err)
	}
	return got
}

// idSet returns ids as a set for order-independent comparison.
func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// TestOrchestratorAdvance_UnblockedTaskFoundAfterDep drives the REAL
// readyBlockedCandidates decision: when the sole dependency is done, the
// downstream task (StatusNext, still carries dep in BlockedBy) must be
// reported as ready to advance.
//
// Traces to: wave spec — DAG auto-advance: blocked→next on dep done.
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
// readyBlockedCandidates: a task gated on two deps where one is still next
// must NOT be reported ready, even though the OTHER dep just completed.
//
// Traces to: wave spec — DAG auto-advance: all deps must be done.
func TestOrchestratorAdvance_StillBlockedWhenDepNotComplete(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	depA := createTask(t, store, "completed", nil)
	depB := createTask(t, store, "queued", nil) // not yet completed

	_ = createTask(t, store, "queued", []string{depA.ID, depB.ID})

	// depA completed → advance check keyed on depA. depB is still next, so the
	// downstream task's FULL dependency set is not satisfied: nothing is ready.
	ready := te.readyBlockedCandidates(depA.ID)
	if len(ready) != 0 {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want [] (depB still next)", depA.ID, ready)
	}

	// Now complete depB and re-check keyed on depB — the downstream task is ready.
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := store.Update(depB.ID, task.Patch{Status: ptrStatus(task.StatusDone), CompletedAt: &now}); err != nil {
		t.Fatalf("update depB→done: %v", err)
	}
	ready = te.readyBlockedCandidates(depB.ID)
	if len(ready) != 1 {
		t.Fatalf("readyBlockedCandidates(%q) after depB completes = %v, want 1 ready", depB.ID, ready)
	}
}

// TestOrchestratorAdvance_FailedDepDoesNotUnblock drives the REAL
// readyBlockedCandidates: a FAILED dependency must NOT satisfy the gate — only
// "done" unblocks.
//
// Traces to: wave spec — DAG auto-advance: failed dep does not unblock.
func TestOrchestratorAdvance_FailedDepDoesNotUnblock(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	dep := createTask(t, store, "failed", nil)
	_ = createTask(t, store, "queued", []string{dep.ID})

	ready := te.readyBlockedCandidates(dep.ID)
	if len(ready) != 0 {
		t.Fatalf("readyBlockedCandidates(%q) = %v, want [] (dep failed, not done)", dep.ID, ready)
	}
}

// TestOrchestratorAdvance_MultipleDownstreamTasks drives the REAL
// readyBlockedCandidates: every task gated solely on a just-completed dep must
// be reported ready, not just the first one.
//
// Traces to: wave spec — DAG auto-advance: multiple dependents advance.
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
func createChild(t *testing.T, store *task.Store, parentID, status string) *task.Task {
	t.Helper()
	e := &task.Task{
		Title:        "child",
		Prompt:       "do thing",
		AgentID:      "jim",
		Priority:     3,
		Action:       task.ActionLLM,
		Status:       task.StatusNext,
		WorkspaceID:  "default",
		ParentTaskID: parentID,
	}
	if err := store.Create(e); err != nil {
		t.Fatalf("create child: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	switch status {
	case "running", "in_progress":
		got, err := store.Update(e.ID, task.Patch{Status: ptrStatus(task.StatusInProgress), StartedAt: &now})
		if err != nil {
			t.Fatalf("child→in_progress: %v", err)
		}
		return got
	case "completed", "done":
		// Transition: next → in_progress → done (no state machine, but use two
		// steps to stamp both StartedAt and CompletedAt realistically).
		if _, err := store.Update(
			e.ID,
			task.Patch{Status: ptrStatus(task.StatusInProgress), StartedAt: &now},
		); err != nil {
			t.Fatalf("child→in_progress: %v", err)
		}
		got, err := store.Update(e.ID, task.Patch{Status: ptrStatus(task.StatusDone), CompletedAt: &now})
		if err != nil {
			t.Fatalf("child→done: %v", err)
		}
		return got
	case "failed":
		got, err := store.Update(e.ID, task.Patch{Status: ptrStatus(task.StatusFailed), CompletedAt: &now})
		if err != nil {
			t.Fatalf("child→failed: %v", err)
		}
		return got
	}
	// "queued" / "next" / default — leave as StatusNext.
	got, err := store.Get(e.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	return got
}

// TestParentFollowUp_ConcurrentSiblings_ExactlyOne is the duplicate-parent-
// follow-up race regression test. N sibling-completion callbacks fire
// concurrently for the same in_progress parent with all children already done;
// the atomic ClaimParentFollowUp must ensure the parent follow-up fires exactly once.
//
// Traces to: wave spec — ClaimParentFollowUp exactly-once under concurrent siblings.
func TestParentFollowUp_ConcurrentSiblings_ExactlyOne(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	// Parent in "in_progress" state.
	parent := &task.Task{
		Title:       "parent",
		Prompt:      "p",
		AgentID:     "jim",
		Priority:    3,
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		WorkspaceID: "default",
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := store.Update(
		parent.ID,
		task.Patch{Status: ptrStatus(task.StatusInProgress), StartedAt: &now},
	); err != nil {
		t.Fatalf("parent→in_progress: %v", err)
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
// sibling is still in_progress.
//
// Traces to: wave spec — parent follow-up: only fires when ALL siblings are terminal.
func TestParentFollowUp_NotAllSiblingsDone(t *testing.T) {
	te, store := newTestTaskExecutor(t)

	parent := &task.Task{
		Title:       "parent",
		Prompt:      "p",
		AgentID:     "jim",
		Priority:    3,
		Action:      task.ActionLLM,
		Status:      task.StatusNext,
		WorkspaceID: "default",
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := store.Update(
		parent.ID,
		task.Patch{Status: ptrStatus(task.StatusInProgress), StartedAt: &now},
	); err != nil {
		t.Fatalf("parent→in_progress: %v", err)
	}

	createChild(t, store, parent.ID, "completed")
	createChild(t, store, parent.ID, "running") // still running → not all done

	var fires int64
	te.parentFollowUp = func(string) { atomic.AddInt64(&fires, 1) }

	te.notifyParentIfAllSiblingsDone(parent.ID)

	if got := atomic.LoadInt64(&fires); got != 0 {
		t.Fatalf("follow-up fired %d times while a sibling is still in_progress, want 0", got)
	}
}

// TestDispatchSema_TaskExecutor_ExecuteTask_SemaRejection verifies that
// ExecuteTask respects the dispatch semaphore cap.
//
// Traces to: wave spec — dispatch semaphore: global cap enforced.
func TestDispatchSema_TaskExecutor_ExecuteTask_SemaRejection(t *testing.T) {
	_, store := newTestTaskExecutor(t)

	// Create a task that is "next" with no blocked_by.
	tk := createTask(t, store, "queued", nil)

	// Build a minimal TaskExecutor with cap=1 — then fill the semaphore manually
	// so TryAcquire fails.
	te := &TaskExecutor{
		agentLoop:     nil,
		store:         store,
		running:       make(map[string]*taskSlot),
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
	err := te.ExecuteTask(context.Background(), tk.ID)
	if err == nil {
		t.Fatal("expected error when dispatch sema is full")
	}
	t.Logf("got expected error: %v", err)
}
