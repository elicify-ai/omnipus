package cron

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #1760. A config reload must not stop the cron service: Stop cancels the
// lane context, so every running heartbeat / scheduled-task turn dies and every
// queued job is dropped. The reload only needs the two settings the service
// reads from config (schedules.max_concurrent_runs, schedules.retry_backoff_ms),
// so the service gets one method that applies them to the live instance.
//
// Contract under test (oracle: the issue text and the Set* doc comments):
//   - ApplySchedulesConfig(maxConcurrentRuns, retryBackoffMs) works on a started service.
//   - It never cancels a running job and never drops a queued one.
//   - New dispatches obey the new concurrency cap.
//   - Invalid input (negative backoff entry) returns an error and changes nothing.

// gateRunner blocks every run until release is closed and counts concurrency.
type gateRunner struct {
	release  chan struct{}
	running  atomic.Int32
	peak     atomic.Int32
	started  atomic.Int32
	ctxError atomic.Value // error seen by a run when it finished
	once     sync.Once
}

func newGateRunner() *gateRunner { return &gateRunner{release: make(chan struct{})} }

func (g *gateRunner) open() { g.once.Do(func() { close(g.release) }) }

func (g *gateRunner) RunScheduled(ctx context.Context, _ *CronJob) (string, error) {
	g.started.Add(1)
	n := g.running.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	defer g.running.Add(-1)
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	if err := ctx.Err(); err != nil {
		g.ctxError.Store(err)
	}
	return "", nil
}

func newApplyService(t *testing.T, jobs int) *CronService {
	t.Helper()
	cs := NewCronService(filepath.Join(t.TempDir(), "cron", "jobs.json"))
	for i := 0; i < jobs; i++ {
		if _, err := cs.AddJobFull(JobSpec{
			Name:     "apply-test",
			Schedule: CronSchedule{Kind: "every", EveryMS: int64Ptr(20)},
			Message:  "m",
			AgentID:  "u6-test-owner",
		}); err != nil {
			t.Fatalf("SETUP: AddJobFull: %v", err)
		}
	}
	return cs
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A running job must keep its live context across an apply: no cancel.
func TestCronService_ApplySchedulesConfig_DoesNotCancelARunningJob(t *testing.T) {
	cs := newApplyService(t, 1)
	g := newGateRunner()
	cs.SetRunner(g)
	if err := cs.Start(); err != nil {
		t.Fatalf("SETUP: Start: %v", err)
	}
	t.Cleanup(func() { g.open(); cs.Stop() })
	waitUntil(t, "the job to start", func() bool { return g.started.Load() == 1 })

	if err := cs.ApplySchedulesConfig(3, []int64{1000, 2000}); err != nil {
		t.Fatalf("ApplySchedulesConfig: %v", err)
	}
	// Give a (buggy) cancel time to land, then let the run finish.
	time.Sleep(150 * time.Millisecond)
	g.open()
	waitUntil(t, "the run to finish", func() bool { return g.running.Load() == 0 })
	if err, _ := g.ctxError.Load().(error); err != nil {
		t.Fatalf("#1760: a settings apply cancelled the running job's context: %v", err)
	}
}

// New fires after the apply obey the new cap. Two jobs are due all the time; with
// the cap lowered to 1 the peak after the apply must never exceed 1.
func TestCronService_ApplySchedulesConfig_NewCapBoundsLaterRuns(t *testing.T) {
	cs := newApplyService(t, 2)
	g := newGateRunner()
	cs.SetRunner(g)
	cs.SetMaxConcurrentRuns(8)
	if err := cs.Start(); err != nil {
		t.Fatalf("SETUP: Start: %v", err)
	}
	t.Cleanup(func() { g.open(); cs.Stop(); cs.WaitForLane() })
	// Both jobs run at once under cap 8.
	waitUntil(t, "two concurrent runs under cap 8", func() bool { return g.running.Load() == 2 })

	if err := cs.ApplySchedulesConfig(1, nil); err != nil {
		t.Fatalf("ApplySchedulesConfig: %v", err)
	}
	g.open() // both in-flight runs finish; later fires start under the new cap
	waitUntil(t, "in-flight runs to settle", func() bool { return g.running.Load() == 0 })
	g.peak.Store(0)
	// New runs: gate them again by swapping in a fresh blocking runner.
	g2 := newGateRunner()
	cs.SetRunner(g2)
	t.Cleanup(g2.open)
	waitUntil(t, "a run under the new cap", func() bool { return g2.running.Load() >= 1 })
	time.Sleep(300 * time.Millisecond) // several 20 ms ticks, second job due the whole time
	if p := g2.peak.Load(); p != 1 {
		t.Fatalf("#1760: after ApplySchedulesConfig(1, …) the peak concurrency was %d, want 1", p)
	}
}

// Invalid input is an error and leaves BOTH settings exactly as they were.
func TestCronService_ApplySchedulesConfig_InvalidKeepsOldSettings(t *testing.T) {
	cs := newApplyService(t, 0)
	cs.SetMaxConcurrentRuns(4)
	cs.SetRetryBackoff([]int64{111, 222})

	if err := cs.ApplySchedulesConfig(9, []int64{500, -1}); err == nil {
		t.Fatal("#1760: a negative backoff entry must be rejected, got nil error")
	}
	cs.mu.RLock()
	gotMax, gotBackoff := cs.maxConcurrentRuns, append([]int64(nil), cs.retryBackoffMs...)
	cs.mu.RUnlock()
	if gotMax != 4 {
		t.Errorf("#1760: rejected apply changed max concurrent runs to %d, want 4", gotMax)
	}
	if len(gotBackoff) != 2 || gotBackoff[0] != 111 || gotBackoff[1] != 222 {
		t.Errorf("#1760: rejected apply changed retry backoff to %v, want [111 222]", gotBackoff)
	}
}

// A valid apply stores both values; max <= 0 means the default, like config load.
func TestCronService_ApplySchedulesConfig_StoresValidValues(t *testing.T) {
	cs := newApplyService(t, 0)
	if err := cs.ApplySchedulesConfig(0, []int64{7, 8, 9}); err != nil {
		t.Fatalf("ApplySchedulesConfig: %v", err)
	}
	cs.mu.RLock()
	gotMax, gotBackoff := cs.maxConcurrentRuns, append([]int64(nil), cs.retryBackoffMs...)
	cs.mu.RUnlock()
	if gotMax != defaultMaxConcurrentRuns {
		t.Errorf("max <= 0 must fall back to the default %d, got %d", defaultMaxConcurrentRuns, gotMax)
	}
	if len(gotBackoff) != 3 || gotBackoff[2] != 9 {
		t.Errorf("retry backoff = %v, want [7 8 9]", gotBackoff)
	}
}
