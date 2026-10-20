package heartbeat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingCycle is a poller callback that stays in flight until its context
// is cancelled or the test releases it, recording whether it was still
// running when Stop returned.
type blockingCycle struct {
	entered     chan struct{}
	enterOnce   sync.Once
	release     chan struct{}
	finished    atomic.Bool
	sawCancel   atomic.Bool
	releaseOnce sync.Once
}

func newBlockingCycle() *blockingCycle {
	return &blockingCycle{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingCycle) run(ctx context.Context) {
	b.enterOnce.Do(func() { close(b.entered) })
	select {
	case <-ctx.Done():
		b.sawCancel.Store(true)
	case <-b.release:
	}
	b.finished.Store(true)
}

func (b *blockingCycle) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }

func (b *blockingCycle) CheckQueuedTasks(ctx context.Context) { b.run(ctx) }
func (b *blockingCycle) CycleAll(ctx context.Context)         { b.run(ctx) }

// requireStopJoinsCycle starts a poller whose first cycle blocks, stops it
// while that cycle is in flight, and requires that Stop returned only after
// the cycle ended — cancelled by Stop, not left running to write after it.
func requireStopJoinsCycle(t *testing.T, cycle *blockingCycle, start, stop func()) {
	t.Helper()
	t.Cleanup(cycle.unblock)
	start()
	select {
	case <-cycle.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the first cycle never started")
	}

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		cycle.unblock()
		t.Fatal("Stop did not return within 15s of a cancellable in-flight cycle")
	}
	if !cycle.finished.Load() {
		t.Fatal("Stop returned while a cycle was still running: that cycle keeps writing after the service was stopped")
	}
	if !cycle.sawCancel.Load() {
		t.Fatal("the in-flight cycle was not cancelled by Stop: it ran on a context Stop cannot reach")
	}
}

// TestTaskDrainService_StopCancelsAndJoinsTheInFlightCheck: a queued-task
// check (CheckQueuedTasks -> ExecuteTask, which writes the task store and
// starts task runs) in flight when Stop is called must be cancelled and
// finished before Stop returns.
func TestTaskDrainService_StopCancelsAndJoinsTheInFlightCheck(t *testing.T) {
	cycle := newBlockingCycle()
	ds := NewTaskDrainService(cycle, time.Hour)
	requireStopJoinsCycle(t, cycle, ds.Start, ds.Stop)
	if ds.IsRunning() {
		t.Fatal("drain service reports running after Stop")
	}
}

// TestMailWatchService_StopCancelsAndJoinsTheInFlightCycle: the same for a
// mail cycle (CycleAll: mailbox I/O and mailbox state writes).
func TestMailWatchService_StopCancelsAndJoinsTheInFlightCycle(t *testing.T) {
	cycle := newBlockingCycle()
	svc := NewMailWatchService(cycle, time.Hour)
	requireStopJoinsCycle(t, cycle, svc.Start, svc.Stop)
	if svc.IsRunning() {
		t.Fatal("mail-watch service reports running after Stop")
	}
}
