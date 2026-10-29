package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Diagnostic-only, not-wire-format: no context values, IDs, paths or raw errors.
// Keep time.Time values intact so Sub uses their monotonic clock readings.
// Recording is race-safe; t.Logf is deferred to cleanup to avoid log I/O in the
// watched interval or a worker logging after a failed cleanup join.
type healFixCancellationTiming struct { // not-wire-format: private test recorder.
	mu       sync.Mutex
	started  time.Time
	released time.Time
	request  context.Context
	detached context.Context
	events   []healFixCancellationTimingEvent
}

type healFixCancellationTimingEvent struct { // not-wire-format: test diagnostics only.
	phase          string
	elapsedNS      int64
	recordDelayNS  int64
	released       bool
	afterReleaseNS int64
	request        healFixCancellationContextState
	detached       healFixCancellationContextState
}

type healFixCancellationContextState struct { // not-wire-format: test diagnostics only.
	state       string
	hasDeadline bool
	remainingNS int64
}

func healFixCancellationContext(ctx context.Context, at time.Time) healFixCancellationContextState {
	if ctx == nil {
		return healFixCancellationContextState{state: "unobserved"}
	}
	state := "other_error"
	switch ctx.Err() {
	case nil:
		state = "live"
	case context.Canceled:
		state = "cancelled"
	case context.DeadlineExceeded:
		state = "deadline_exceeded"
	}
	deadline, hasDeadline := ctx.Deadline()
	remainingNS := int64(0)
	if hasDeadline {
		remainingNS = deadline.Sub(at).Nanoseconds()
	}
	return healFixCancellationContextState{state, hasDeadline, remainingNS}
}

func (trace *healFixCancellationTiming) mark(phase string, request, detached context.Context) {
	at := time.Now() // phase timestamp precedes any diagnostic-lock delay
	trace.mu.Lock()
	defer trace.mu.Unlock()
	sampledAt := time.Now()
	if request != nil {
		trace.request = request
	}
	if detached != nil {
		trace.detached = detached
	}
	if phase == "release" {
		trace.released = at // called once, immediately before the real channel close
	}
	released := !trace.released.IsZero() && !at.Before(trace.released)
	afterReleaseNS := int64(0)
	if released {
		afterReleaseNS = at.Sub(trace.released).Nanoseconds()
	}
	trace.events = append(trace.events, healFixCancellationTimingEvent{
		phase: phase, elapsedNS: at.Sub(trace.started).Nanoseconds(), recordDelayNS: sampledAt.Sub(at).Nanoseconds(),
		released: released, afterReleaseNS: afterReleaseNS,
		request: healFixCancellationContext(trace.request, sampledAt), detached: healFixCancellationContext(trace.detached, sampledAt),
	})
}

func (trace *healFixCancellationTiming) log(t *testing.T) {
	t.Helper()
	// Only diagnostic emission is gated; all original assertions still run.
	if !testing.Verbose() {
		return
	}
	trace.mu.Lock()
	events := append([]healFixCancellationTimingEvent(nil), trace.events...)
	trace.mu.Unlock()
	for _, event := range events {
		// Negative remaining time means the deadline is past, not necessarily
		// that the timer has run. The explicit context state is authoritative.
		// A completed heal also cancels its own internal context normally.
		t.Logf("heal_cancel_timing phase=%s elapsed_ns=%d record_delay_ns=%d released=%t after_release_ns=%d request_state=%s request_has_deadline=%t request_remaining_ns=%d detached_state=%s detached_has_deadline=%t detached_remaining_ns=%d",
			event.phase, event.elapsedNS, event.recordDelayNS, event.released, event.afterReleaseNS,
			event.request.state, event.request.hasDeadline, event.request.remainingNS,
			event.detached.state, event.detached.hasDeadline, event.detached.remainingNS)
	}
}
