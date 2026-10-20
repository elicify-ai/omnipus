package heartbeat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingMailCycle is a mail cycle that stays in flight until the test
// releases it, recording whether it had finished when Stop returned and
// whether its context was cancelled mid-cycle.
type blockingMailCycle struct {
	entered     chan struct{}
	enterOnce   sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	finished    atomic.Bool
	cancelled   atomic.Bool
}

func (b *blockingMailCycle) CycleAll(ctx context.Context) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-b.release
	if ctx.Err() != nil {
		b.cancelled.Store(true)
	}
	b.finished.Store(true)
}

func (b *blockingMailCycle) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }

// TestMailWatchService_StopWaitsForTheInFlightCycle: a mail cycle (CycleAll:
// mailbox I/O and mailbox state writes) already in flight when Stop is
// called - here the first pass after start - must finish before Stop
// returns, and must not be cancelled mid-cycle.
func TestMailWatchService_StopWaitsForTheInFlightCycle(t *testing.T) {
	cycle := &blockingMailCycle{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(cycle.unblock)
	svc := NewMailWatchService(cycle, time.Hour)
	svc.Start()
	select {
	case <-cycle.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the first mail cycle never started")
	}

	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a mail cycle was still running: that cycle keeps writing mailbox state after the service was stopped")
	case <-time.After(500 * time.Millisecond):
	}

	cycle.unblock()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the in-flight cycle finished")
	}
	if !cycle.finished.Load() {
		t.Fatal("Stop returned before the in-flight cycle finished")
	}
	if cycle.cancelled.Load() {
		t.Fatal("Stop cancelled the mail cycle mid-cycle; it must wait, not cancel")
	}
	if svc.IsRunning() {
		t.Fatal("mail-watch service reports running after Stop")
	}
}
