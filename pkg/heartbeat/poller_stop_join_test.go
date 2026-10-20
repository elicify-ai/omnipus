package heartbeat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingCheck is a queued-task check that stays in flight until the test
// releases it, recording whether it had finished when Stop returned.
type blockingCheck struct {
	entered     chan struct{}
	enterOnce   sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	finished    atomic.Bool
}

func (b *blockingCheck) CheckQueuedTasks(context.Context) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-b.release
	b.finished.Store(true)
}

func (b *blockingCheck) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }

// TestTaskDrainService_StopWaitsForTheInFlightCheck: a queued-task check
// (CheckQueuedTasks -> ExecuteTask: it claims a task in the task store and
// starts its run) already in flight when Stop is called must finish before
// Stop returns - otherwise it claims or dispatches after the drain was
// stopped, in the failed-boot teardown and in a normal shutdown alike.
func TestTaskDrainService_StopWaitsForTheInFlightCheck(t *testing.T) {
	check := &blockingCheck{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(check.unblock)
	ds := NewTaskDrainService(check, time.Hour)
	ds.Start()
	select {
	case <-check.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the first queued-task check never started")
	}

	stopped := make(chan struct{})
	go func() { ds.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a queued-task check was still running: that check can claim and dispatch a task after the drain was stopped")
	case <-time.After(500 * time.Millisecond):
	}

	check.unblock()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the in-flight check finished")
	}
	if !check.finished.Load() {
		t.Fatal("Stop returned before the in-flight check finished")
	}
	if ds.IsRunning() {
		t.Fatal("drain service reports running after Stop")
	}
}
