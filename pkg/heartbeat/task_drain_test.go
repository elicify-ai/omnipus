package heartbeat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingChecker records how many times CheckQueuedTasks was invoked.
type countingChecker struct {
	calls atomic.Int64
	done  chan struct{}
	once  sync.Once
}

func (c *countingChecker) CheckQueuedTasks(_ context.Context) {
	c.calls.Add(1)
	c.once.Do(func() { close(c.done) })
}

// TestTaskDrainService_DispatchesIndependentOfHeartbeat proves the dedicated
// drain poll calls CheckQueuedTasks even with NO HeartbeatService running — i.e.
// `next` tasks still dispatch when a per-agent heartbeat owns the HEARTBEAT.md
// half and the legacy global service is skipped. This is the regression guard
// for the silent-failure where queued tasks never dispatched on per-agent-
// heartbeat installs.
func TestTaskDrainService_DispatchesIndependentOfHeartbeat(t *testing.T) {
	checker := &countingChecker{done: make(chan struct{})}

	// 10ms interval so the test is fast; no HeartbeatService is created at all.
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	if !ds.IsRunning() {
		t.Fatal("drain service should report running after Start")
	}

	select {
	case <-checker.done:
		// CheckQueuedTasks fired at least once with no heartbeat present.
	case <-time.After(2 * time.Second):
		t.Fatal("CheckQueuedTasks was never called — queued tasks would not dispatch")
	}

	ds.Stop()
	if ds.IsRunning() {
		t.Fatal("drain service should report stopped after Stop")
	}
}

// TestTaskDrainService_NilCheckerNoOp proves a nil checker makes Start a safe
// no-op (the drain is simply disabled, not a panic).
func TestTaskDrainService_NilCheckerNoOp(t *testing.T) {
	ds := NewTaskDrainService(nil, 10*time.Millisecond)
	ds.Start()
	if ds.IsRunning() {
		t.Fatal("nil-checker drain must not start a loop")
	}
	ds.Stop() // must not panic
}

// TestTaskDrainService_DefaultInterval proves a non-positive interval falls back
// to the default cadence rather than spinning.
func TestTaskDrainService_DefaultInterval(t *testing.T) {
	ds := NewTaskDrainService(&countingChecker{done: make(chan struct{})}, 0)
	if ds.interval != defaultTaskDrainInterval {
		t.Fatalf("expected default interval %v, got %v", defaultTaskDrainInterval, ds.interval)
	}
}
