// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package heartbeat — additional coverage tests.
// Targets: MailboxDrainService and TaskDrainService (idempotent start/stop,
// drain-called-on-tick, zero-return scanner, etc.).
// Build tags: goolm,stdjson
package heartbeat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Shared test helpers
// ---------------------------------------------------------------------------

// countingMailScanner records Drain call counts and signals once on the first call.
type countingMailScanner struct {
	calls atomic.Int64
	done  chan struct{}
	once  atomicOnce
}

type atomicOnce struct {
	done atomic.Uint32
}

func (ao *atomicOnce) Do(f func()) {
	if ao.done.CompareAndSwap(0, 1) {
		f()
	}
}

func (s *countingMailScanner) Drain(_ context.Context) int {
	n := int(s.calls.Add(1))
	s.once.Do(func() { close(s.done) })
	return n
}

// failingMailScanner always panics (tests loop resilience via recover or just returns error).
// Actually simpler: a scanner that always returns 0 but we can count calls.
type zeroMailScanner struct {
	calls atomic.Int64
	done  chan struct{}
	once  atomicOnce
}

func (z *zeroMailScanner) Drain(_ context.Context) int {
	z.calls.Add(1)
	z.once.Do(func() { close(z.done) })
	return 0
}

// ---------------------------------------------------------------------------
// MailboxDrainService tests
// ---------------------------------------------------------------------------

// Traces to: mailbox_drain.go — NewMailboxDrainService, Start, IsRunning, Stop
func TestMailboxDrainService_NilScannerNoOp(t *testing.T) {
	// BDD: Given a nil scanner
	// When Start is called
	// Then the loop must not start (IsRunning == false) and Stop must not panic
	ds := NewMailboxDrainService(nil, 10*time.Millisecond)
	ds.Start()
	assert.False(t, ds.IsRunning(), "nil-scanner mailbox drain must not start a loop")
	ds.Stop() // must not panic
}

// Traces to: mailbox_drain.go — NewMailboxDrainService default interval
func TestMailboxDrainService_DefaultInterval(t *testing.T) {
	// BDD: Given a zero interval
	// When NewMailboxDrainService is called
	// Then the interval falls back to defaultMailboxDrainInterval (not zero / not spin)
	ds := NewMailboxDrainService(&countingMailScanner{done: make(chan struct{})}, 0)
	assert.Equal(t, defaultMailboxDrainInterval, ds.interval,
		"zero interval must fall back to defaultMailboxDrainInterval")
}

// Traces to: mailbox_drain.go — Start begins drain; IsRunning; Stop halts
func TestMailboxDrainService_StartStopLifecycle(t *testing.T) {
	// BDD: Given a valid scanner and short interval
	// When Start is called → IsRunning must be true
	// When Stop is called → IsRunning must be false
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)

	assert.False(t, ds.IsRunning(), "must not be running before Start")

	ds.Start()
	assert.True(t, ds.IsRunning(), "must be running after Start")

	ds.Stop()
	assert.False(t, ds.IsRunning(), "must not be running after Stop")
}

// Traces to: mailbox_drain.go — Drain is actually called on tick
func TestMailboxDrainService_DrainCalledOnTick(t *testing.T) {
	// BDD: Given a running MailboxDrainService with a 10ms interval
	// When at least one tick fires
	// Then scanner.Drain is called at least once (proves the loop dispatches, not a no-op)
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	select {
	case <-scanner.done:
		// Drain was called at least once.
	case <-time.After(3 * time.Second):
		t.Fatal("MailboxDrainService.Drain was never called — mailbox draining is broken")
	}

	// Differentiation: calls must be > 0 (not hardcoded zero)
	assert.Greater(t, scanner.calls.Load(), int64(0),
		"Drain call count must be greater than zero")
}

// Traces to: mailbox_drain.go — idempotent Start (double-Start must not spawn two goroutines)
func TestMailboxDrainService_IdempotentStart(t *testing.T) {
	// BDD: Given a running MailboxDrainService
	// When Start is called again
	// Then only one loop goroutine exists (IsRunning stays true, no panic)
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	ds.Start() // second call — must be a no-op, not a panic
	assert.True(t, ds.IsRunning(), "must still be running after double Start")
}

// Traces to: mailbox_drain.go — idempotent Stop
func TestMailboxDrainService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped MailboxDrainService
	// When Stop is called again
	// Then no panic occurs
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	ds.Stop()
	ds.Stop() // second Stop — must not panic
	assert.False(t, ds.IsRunning(), "must still be stopped after double Stop")
}

// Traces to: mailbox_drain.go — Stop halts polling (no calls after stop)
func TestMailboxDrainService_StopHaltsPolling(t *testing.T) {
	// BDD: Given a MailboxDrainService that has accumulated at least one Drain call
	// When Stop is called and a full interval elapses
	// Then no additional Drain calls are made
	scanner := &countingMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()

	// Wait for at least one call
	select {
	case <-scanner.done:
	case <-time.After(3 * time.Second):
		t.Fatal("Drain was never called before Stop test")
	}

	ds.Stop()
	countAfterStop := scanner.calls.Load()

	// Allow one more tick window for the already-queued AfterFunc
	time.Sleep(50 * time.Millisecond)

	finalCount := scanner.calls.Load()
	// Allow a small grace of 1 extra call (the early AfterFunc may fire right at stop boundary)
	assert.LessOrEqual(t, finalCount-countAfterStop, int64(2),
		"Drain must not keep being called after Stop — got %d extra calls", finalCount-countAfterStop)
}

// Traces to: mailbox_drain.go — zero-return scanner (no crash when Drain returns 0)
func TestMailboxDrainService_ZeroReturnScanner(t *testing.T) {
	// BDD: Given a scanner that always returns 0 tasks created
	// When the drain loop runs
	// Then the loop continues without crashing
	scanner := &zeroMailScanner{done: make(chan struct{})}
	ds := NewMailboxDrainService(scanner, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	select {
	case <-scanner.done:
	case <-time.After(3 * time.Second):
		t.Fatal("zeroMailScanner.Drain was never called")
	}
	assert.True(t, ds.IsRunning(), "loop must survive a scanner returning 0")
}

// ---------------------------------------------------------------------------
// TaskDrainService — idempotent stop and coverage gaps
// ---------------------------------------------------------------------------

// Traces to: task_drain.go — idempotent Stop
func TestTaskDrainService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped TaskDrainService
	// When Stop is called a second time
	// Then no panic occurs
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	ds.Stop()
	ds.Stop() // must not panic
	assert.False(t, ds.IsRunning(), "must remain stopped after double Stop")
}

// Traces to: task_drain.go — idempotent Start
func TestTaskDrainService_IdempotentStart(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Start is called again
	// Then no second goroutine is spawned, IsRunning stays true
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	ds.Start() // second call
	assert.True(t, ds.IsRunning())
}

// Traces to: task_drain.go — stop actually halts ticker (runLoop stopChan path)
func TestTaskDrainService_StopHaltsTicker(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Stop is called after at least one tick
	// Then CheckQueuedTasks is not called further
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()

	// Wait for at least one call so the ticker has fired at least once
	select {
	case <-checker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("checker never called before stop test")
	}

	ds.Stop()
	countAtStop := checker.calls.Load()
	time.Sleep(50 * time.Millisecond)

	// Allow for the in-flight AfterFunc
	assert.LessOrEqual(t, checker.calls.Load()-countAtStop, int64(2),
		"CheckQueuedTasks must not keep firing after Stop")
}
