package heartbeat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldCycle is a poller callback (both CheckQueuedTasks and CycleAll) that
// stays in flight until the test releases it. It records whether it had
// finished and whether its context was cancelled while it ran.
type heldCycle struct {
	entered     chan struct{}
	enterOnce   sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	finished    chan struct{}
	cancelled   atomic.Bool
}

func newHeldCycle() *heldCycle {
	return &heldCycle{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
}

func (h *heldCycle) run(ctx context.Context) {
	h.enterOnce.Do(func() { close(h.entered) })
	<-h.release
	if ctx.Err() != nil {
		h.cancelled.Store(true)
	}
	close(h.finished)
}

func (h *heldCycle) CheckQueuedTasks(ctx context.Context) { h.run(ctx) }
func (h *heldCycle) CycleAll(ctx context.Context)         { h.run(ctx) }
func (h *heldCycle) unblock()                             { h.releaseOnce.Do(func() { close(h.release) }) }

func (h *heldCycle) isFinished() bool {
	select {
	case <-h.finished:
		return true
	default:
		return false
	}
}

type pollerUnderTest struct {
	name    string
	service string
	start   func(cycle *heldCycle) (stop func() error, running func() bool)
}

var pollersUnderTest = []pollerUnderTest{
	{name: "task drain", service: "task-drain", start: func(c *heldCycle) (func() error, func() bool) {
		ds := NewTaskDrainService(c, time.Hour)
		ds.Start()
		return ds.Stop, ds.IsRunning
	}},
	{name: "mail watch", service: "mail-watch", start: func(c *heldCycle) (func() error, func() bool) {
		svc := NewMailWatchService(c, time.Hour)
		svc.Start()
		return svc.Stop, svc.IsRunning
	}},
}

// startHeldPoller starts the poller, waits until its first cycle is in
// flight, then calls Stop on another goroutine and waits on the barrier that
// proves Stop has closed the loop's stop channel and entered its join.
func startHeldPoller(t *testing.T, p pollerUnderTest) (cycle *heldCycle, stopped chan error, running func() bool) {
	t.Helper()
	joining := make(chan struct{})
	var joinOnce sync.Once
	pollerJoinStartedTestHook = func(service string) {
		if service == p.service {
			joinOnce.Do(func() { close(joining) })
		}
	}
	t.Cleanup(func() { pollerJoinStartedTestHook = nil })

	cycle = newHeldCycle()
	t.Cleanup(cycle.unblock)
	stop, running := p.start(cycle)
	select {
	case <-cycle.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the first cycle never started")
	}
	stopped = make(chan error, 1)
	go func() { stopped <- stop() }()
	select {
	case <-joining:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never reached its wait for the loop: it does not join the cycle in flight")
	}
	return cycle, stopped, running
}

// TestPollerStop_WaitsForTheCycleInFlight: Stop, once at its join (barrier,
// not a timing guess), must not return while the cycle runs; released, the
// cycle finishes uncancelled and Stop returns nil. A queued-task check
// claims and starts tasks; a mail check reads mailboxes and saves state -
// neither may keep running after Stop returned, nor be cut off mid-cycle.
func TestPollerStop_WaitsForTheCycleInFlight(t *testing.T) {
	for _, p := range pollersUnderTest {
		t.Run(p.name, func(t *testing.T) {
			cycle, stopped, running := startHeldPoller(t, p)
			select {
			case err := <-stopped:
				t.Fatalf("Stop returned (err=%v) while a cycle was still running", err)
			default:
			}
			cycle.unblock()
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatalf("Stop returned %v after the cycle finished within its wait; want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Stop did not return after the cycle finished")
			}
			if !cycle.isFinished() {
				t.Fatal("Stop returned before the cycle finished")
			}
			if cycle.cancelled.Load() {
				t.Fatal("Stop cancelled the cycle mid-cycle; it must wait, not cancel")
			}
			if running() {
				t.Fatal("the poller reports running after Stop")
			}
		})
	}
}

// TestPollerStop_ReportsACycleThatOutlivesTheWait (review SF-2/T4): a cycle
// that outlives Stop's bounded wait is not cancelled; Stop returns
// ErrPollerCycleStillRunning naming the poller and what is still running, so
// the reload or shutdown caller can warn; the retained cycle then finishes
// on its own.
func TestPollerStop_ReportsACycleThatOutlivesTheWait(t *testing.T) {
	saved := pollerStopJoinBudget
	pollerStopJoinBudget = 200 * time.Millisecond
	t.Cleanup(func() { pollerStopJoinBudget = saved })

	for _, p := range pollersUnderTest {
		t.Run(p.name, func(t *testing.T) {
			cycle, stopped, _ := startHeldPoller(t, p)
			var err error
			select {
			case err = <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("Stop did not return after its bounded wait ran out: the wait is unbounded")
			}
			if !errors.Is(err, ErrPollerCycleStillRunning) {
				t.Fatalf("Stop returned %v; want ErrPollerCycleStillRunning for a cycle that outlived the wait", err)
			}
			if !strings.Contains(err.Error(), p.service) || !strings.Contains(err.Error(), "may still") {
				t.Fatalf("the error %q must name the poller and what it may still be doing", err)
			}
			if cycle.isFinished() {
				t.Fatal("INSTRUMENT: the held cycle finished before the wait ran out")
			}
			cycle.unblock()
			select {
			case <-cycle.finished:
			case <-time.After(5 * time.Second):
				t.Fatal("the retained cycle never finished after release")
			}
			if cycle.cancelled.Load() {
				t.Fatal("a timed-out Stop cancelled the cycle; it must be left to finish")
			}
		})
	}
}
