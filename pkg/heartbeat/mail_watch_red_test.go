package heartbeat

// N1 - MailWatchService lifecycle tests (zero tests existed; the old drainer
// pattern was lost with MailboxDrainService, deleted with #631 / D20).
// Oracle from the spec/ADR-082 follow-on: the service supplies the cadence,
// runs a first pass shortly after start (badge freshness), and is safe to
// stop twice and restart. The watcher owns backoff (MC-33 gate is tested at
// pkg/email level: TestWatcher_CycleIfDue_NeverDialsWhileBackingOff).

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countingMailWatcher struct {
	calls atomic.Int32
}

func (m *countingMailWatcher) CycleAll(ctx context.Context) {
	m.calls.Add(1)
}

func TestMailWatchService_Lifecycle(t *testing.T) {
	w := &countingMailWatcher{}
	svc := NewMailWatchService(w, 50*time.Millisecond)

	if svc.IsRunning() {
		t.Fatal("service reports running before Start")
	}
	if w.calls.Load() != 0 {
		t.Fatalf("cycles before Start = %d, want 0", w.calls.Load())
	}

	svc.Start()
	if !svc.IsRunning() {
		t.Fatal("IsRunning false after Start")
	}

	// First pass fires shortly after start (the badge-freshness pass).
	require.Eventually(t, func() bool { return w.calls.Load() >= 1 }, 5*time.Second, 20*time.Millisecond,
		"no cycle after Start - the first-pass timer never fired")
	// The ticker keeps cycling at the configured cadence.
	require.Eventually(t, func() bool { return w.calls.Load() >= 3 }, 5*time.Second, 20*time.Millisecond,
		"ticker did not keep cycling at the configured interval")

	svc.Stop()
	if svc.IsRunning() {
		t.Fatal("IsRunning true after Stop")
	}
	n := w.calls.Load()
	time.Sleep(150 * time.Millisecond)
	if got := w.calls.Load(); got != n {
		t.Fatalf("cycles kept incrementing after Stop: %d -> %d", n, got)
	}

	// Idempotent stop.
	svc.Stop()
	if svc.IsRunning() {
		t.Fatal("second Stop re-started the service")
	}

	// Restart works.
	svc.Start()
	if !svc.IsRunning() {
		t.Fatal("restart after Stop failed")
	}
	svc.Stop()
}

func TestMailWatchService_NilWatcherStartIsNoop(t *testing.T) {
	svc := NewMailWatchService(nil, time.Minute)
	svc.Start()
	if svc.IsRunning() {
		t.Fatal("nil watcher: Start started the loop - must be a no-op")
	}
	svc.Stop()
}

func TestMailWatchService_NonPositiveIntervalFallsBackToDefault(t *testing.T) {
	// Characterization of the constructor contract: a non-positive interval
	// is accepted and falls back to the default cadence (no panic; service
	// usable). The default value itself (1 min) is not directly observable -
	// the point is the constructor never panics or stores a broken cadence.
	w := &countingMailWatcher{}
	svc := NewMailWatchService(w, 0)
	svc.Start()
	if !svc.IsRunning() {
		t.Fatal("zero interval: Start did not start")
	}
	svc.Stop()
}
