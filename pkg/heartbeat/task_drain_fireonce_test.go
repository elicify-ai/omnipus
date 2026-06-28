// task_drain_fireonce_test.go — deterministic tests for the FireOnce test seam
// added to TaskDrainService. FireOnce performs exactly one drain tick without
// requiring a real ticker interval, so tests are fast and deterministic.

package heartbeat

import (
	"context"
	"testing"
)

// TestTaskDrainService_FireOnce_InvokesCheckerExactlyOnce verifies that a
// single FireOnce call produces exactly one CheckQueuedTasks invocation — no
// more, no fewer. This is the canonical behavior contract for the seam.
func TestTaskDrainService_FireOnce_InvokesCheckerExactlyOnce(t *testing.T) {
	checker := &countingChecker{done: make(chan struct{})}

	// Interval is irrelevant: we never Start the loop, so no ticker fires.
	ds := NewTaskDrainService(checker, 0)

	ds.FireOnce(context.Background())

	got := checker.calls.Load()
	if got != 1 {
		t.Errorf("FireOnce: CheckQueuedTasks called %d times, want 1", got)
	}
}

// TestTaskDrainService_FireOnce_MultipleCallsAccumulate verifies that N calls
// to FireOnce produce exactly N checker invocations — the method has no
// internal deduplication or throttle.
func TestTaskDrainService_FireOnce_MultipleCallsAccumulate(t *testing.T) {
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 0)

	const n = 3
	for i := 0; i < n; i++ {
		ds.FireOnce(context.Background())
	}

	got := checker.calls.Load()
	if got != n {
		t.Errorf("FireOnce x%d: CheckQueuedTasks called %d times, want %d", n, got, n)
	}
}

// TestTaskDrainService_FireOnce_NilCheckerSafe verifies that FireOnce is safe
// to call on a nil-checker service (no panic, no invocation).
func TestTaskDrainService_FireOnce_NilCheckerSafe(t *testing.T) {
	ds := NewTaskDrainService(nil, 0)
	// Must not panic.
	ds.FireOnce(context.Background())
}

// TestTaskDrainService_FireOnce_IndependentOfRunLoop verifies that FireOnce
// works correctly whether or not the background loop has been started. Here we
// start the loop AND call FireOnce, then assert the checker count is at least
// the FireOnce contribution (the loop may add more ticks concurrently, so we
// check ≥1 from FireOnce and that the service reports running).
func TestTaskDrainService_FireOnce_IndependentOfRunLoop(t *testing.T) {
	checker := &countingChecker{done: make(chan struct{})}
	// Use a long interval so the background loop almost certainly doesn't fire
	// during the test window — we want FireOnce to be the primary driver.
	ds := NewTaskDrainService(checker, 10*60*1000*1000*1000) // 10 min in nanoseconds
	ds.Start()
	defer ds.Stop()

	if !ds.IsRunning() {
		t.Fatal("service should be running after Start")
	}

	ds.FireOnce(context.Background())

	got := checker.calls.Load()
	// The runLoop's time.AfterFunc fires after 1s (startup drain); with a 10-min
	// ticker it won't contribute within this synchronous call. After FireOnce we
	// expect at least 1.
	if got < 1 {
		t.Errorf("FireOnce with loop running: CheckQueuedTasks called %d times, want ≥1", got)
	}
}
