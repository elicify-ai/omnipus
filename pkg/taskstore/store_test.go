package taskstore

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// newTestStore creates a TaskStore rooted in a temp directory and registers
// cleanup. All tests share this helper so they remain isolated.
func newTestStore(t *testing.T) *TaskStore {
	t.Helper()
	dir := t.TempDir()
	return New(dir)
}

// seedTask is a convenience helper: creates a task with the given fields and
// returns its ID. Fails the test on error.
func seedTask(t *testing.T, s *TaskStore, title, agentID string, priority int, blockedBy []string) string {
	t.Helper()
	entity := &TaskEntity{
		Title:     title,
		AgentID:   agentID,
		Priority:  priority,
		BlockedBy: blockedBy,
		Prompt:    "test prompt",
	}
	if err := s.Create(entity); err != nil {
		t.Fatalf("seedTask: Create(%q): %v", title, err)
	}
	return entity.ID
}

// ─────────────────────────────────────────────────────────────────────────────
// MAJOR 2 — ClaimForRun: atomic queued→running transition
// ─────────────────────────────────────────────────────────────────────────────

// TestClaimForRun_TransitionsToRunning verifies that ClaimForRun atomically
// moves a queued task to running and persists StartedAt.
func TestClaimForRun_TransitionsToRunning(t *testing.T) {
	s := newTestStore(t)
	id := seedTask(t, s, "task-A", "agent-1", 3, nil)

	now := time.Now().UTC()
	got, err := s.ClaimForRun(id, now)
	if err != nil {
		t.Fatalf("ClaimForRun: unexpected error: %v", err)
	}
	if got.Status != "running" {
		t.Fatalf("ClaimForRun: status = %q, want %q", got.Status, "running")
	}
	if got.StartedAt == nil {
		t.Fatal("ClaimForRun: StartedAt must not be nil after claim")
	}

	// Verify the transition is durable (read back from disk).
	reloaded, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get after ClaimForRun: %v", err)
	}
	if reloaded.Status != "running" {
		t.Fatalf("Get after ClaimForRun: status = %q, want %q", reloaded.Status, "running")
	}
}

// TestClaimForRun_RejectsAlreadyRunning verifies that a second ClaimForRun on
// a task that is already running returns ErrAlreadyClaimed — preventing a
// duplicate runTask goroutine.
func TestClaimForRun_RejectsAlreadyRunning(t *testing.T) {
	s := newTestStore(t)
	id := seedTask(t, s, "task-B", "agent-1", 3, nil)

	now := time.Now().UTC()
	if _, err := s.ClaimForRun(id, now); err != nil {
		t.Fatalf("first ClaimForRun: %v", err)
	}

	// Second claim must fail with ErrAlreadyClaimed.
	_, err := s.ClaimForRun(id, now)
	if err == nil {
		t.Fatal("second ClaimForRun: expected ErrAlreadyClaimed, got nil")
	}
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second ClaimForRun: want ErrAlreadyClaimed, got %v", err)
	}
}

// TestClaimForRun_ConcurrentCallers_OnlyOneWins verifies that when N goroutines
// concurrently call ClaimForRun for the same task ID, exactly one succeeds and
// all others return ErrAlreadyClaimed. This is the TOCTOU double-dispatch proof.
func TestClaimForRun_ConcurrentCallers_OnlyOneWins(t *testing.T) {
	s := newTestStore(t)
	id := seedTask(t, s, "task-race", "agent-1", 3, nil)

	const N = 20
	successes := make([]int, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			now := time.Now().UTC()
			if _, err := s.ClaimForRun(id, now); err == nil {
				successes[i] = 1
			}
		}()
	}
	wg.Wait()

	total := 0
	for _, v := range successes {
		total += v
	}
	if total != 1 {
		t.Fatalf("ConcurrentCallers: expected exactly 1 success, got %d", total)
	}
}

// TestClaimForRun_NotFound verifies ErrNotFound for a missing task ID.
func TestClaimForRun_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ClaimForRun("does-not-exist", time.Now().UTC())
	if err == nil {
		t.Fatal("expected ErrNotFound for missing task, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestClaimForRun_InvalidID verifies that path-traversal IDs are rejected.
func TestClaimForRun_InvalidID(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ClaimForRun("../../etc/passwd", time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for invalid id, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MAJOR 1 — Starvation: dep-checking logic used by CheckQueuedTasks
// ─────────────────────────────────────────────────────────────────────────────
// CheckQueuedTasks iterates queued tasks per agent in priority order and calls
// te.store.Get(depID) to determine whether deps are satisfied. The starvation
// fix is that it skips blocked tasks and continues to the next one, so a
// lower-priority ready task is not starved by a higher-priority blocked head.
//
// We validate the dep-checking data model (the foundation of the fix) and the
// List ordering that makes iteration in priority order correct.

// TestList_PriorityOrder verifies that List returns tasks sorted by priority
// ascending (1=highest) then created_at ascending. This is the ordering that
// CheckQueuedTasks relies on to iterate in priority order.
func TestList_PriorityOrder(t *testing.T) {
	s := newTestStore(t)

	// Insert in reverse priority order to confirm sorting.
	id5 := seedTask(t, s, "low-prio", "agent-1", 5, nil)
	id1 := seedTask(t, s, "high-prio", "agent-1", 1, nil)
	id3 := seedTask(t, s, "mid-prio", "agent-1", 3, nil)

	tasks, err := s.List(TaskFilter{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("List: want 3 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != id1 {
		t.Errorf("List[0]: want %q (priority 1), got %q", id1, tasks[0].ID)
	}
	if tasks[1].ID != id3 {
		t.Errorf("List[1]: want %q (priority 3), got %q", id3, tasks[1].ID)
	}
	if tasks[2].ID != id5 {
		t.Errorf("List[2]: want %q (priority 5), got %q", id5, tasks[2].ID)
	}
}

// TestDepCheckLogic_BlockedHeadDoesNotStarveReadyTask simulates the scenario
// that triggered the starvation bug:
//
//   - Task A (priority 1, blocked by dep-X which is NOT completed) is the head
//   - Task B (priority 3, no deps) is lower priority but dispatchable
//
// The fix in CheckQueuedTasks iterates all queued tasks per agent, skips
// blocked ones, and dispatches the first ready one. We prove the underlying
// store correctly allows discovering B after A is found to be blocked.
func TestDepCheckLogic_BlockedHeadDoesNotStarveReadyTask(t *testing.T) {
	s := newTestStore(t)

	// Create dep-X (queued, not completed yet).
	depID := seedTask(t, s, "dep-task", "agent-dep", 3, nil)

	// Task A: high priority but blocked by dep-X.
	idA := seedTask(t, s, "blocked-head", "agent-1", 1, []string{depID})
	// Task B: lower priority, no deps — should be dispatchable.
	idB := seedTask(t, s, "ready-lower", "agent-1", 3, nil)

	// List returns [A, B] in priority order.
	tasks, err := s.List(TaskFilter{Status: "queued", AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("List: want 2, got %d", len(tasks))
	}
	if tasks[0].ID != idA {
		t.Errorf("List[0]: want blocked-head (%s), got %s", idA, tasks[0].ID)
	}
	if tasks[1].ID != idB {
		t.Errorf("List[1]: want ready-lower (%s), got %s", idB, tasks[1].ID)
	}

	// Simulate the fixed CheckQueuedTasks logic: iterate in order, skip blocked tasks.
	var dispatched string
	for i := range tasks {
		task := &tasks[i]
		allSatisfied := true
		for _, depID := range task.BlockedBy {
			dep, err := s.Get(depID)
			if err != nil || dep.Status != "completed" {
				allSatisfied = false
				break
			}
		}
		if !allSatisfied {
			continue // skip the blocked head
		}
		dispatched = task.ID
		break
	}

	if dispatched != idB {
		t.Errorf("starvation fix: expected task B (%s) to be dispatched, got %q", idB, dispatched)
	}

	// Confirm that after dep-X completes, task A becomes the dispatchable head.
	completedAt := time.Now().UTC()
	if _, err := s.Update(depID, TaskPatch{Status: ptrStr("running"), StartedAt: &completedAt}); err != nil {
		t.Fatalf("update dep to running: %v", err)
	}
	if _, err := s.Update(depID, TaskPatch{Status: ptrStr("completed"), CompletedAt: &completedAt}); err != nil {
		t.Fatalf("update dep to completed: %v", err)
	}

	tasks2, err := s.List(TaskFilter{Status: "queued", AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("List after dep complete: %v", err)
	}

	var dispatched2 string
	for i := range tasks2 {
		task := &tasks2[i]
		allSatisfied := true
		for _, dID := range task.BlockedBy {
			dep, err := s.Get(dID)
			if err != nil || dep.Status != "completed" {
				allSatisfied = false
				break
			}
		}
		if !allSatisfied {
			continue
		}
		dispatched2 = task.ID
		break
	}

	if dispatched2 != idA {
		t.Errorf("after dep complete: expected task A (%s) to be dispatched, got %q", idA, dispatched2)
	}
}

// ptrStr is a local copy of the helper used in the main package.
func ptrStr(s string) *string { return &s }
