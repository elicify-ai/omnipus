// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// resetRetentionLoop resets the sync.Once so tests can each launch a fresh
// goroutine instance. This is valid because retentionLoopStarted is
// package-level and tests run sequentially in a single binary.
func resetRetentionLoop() {
	retentionLoopStarted = sync.Once{}
}

// startRetentionSweepLoopAndWaitForExit launches the retention-sweep
// goroutine exactly like production's startRetentionSweepLoop, but returns a
// cleanup function that cancels ctx AND WAITS (with a 1s deadline) for the
// goroutine to actually observe the cancellation and return, instead of the
// fire-and-forget `defer cancel()` every test here used to do.
//
// Why this matters: cancel() only closes ctx.Done() — it does not block
// until a goroutine selecting on that channel has woken up, checked it, and
// returned. Every test in this file mutates the PACKAGE-LEVEL
// retentionSweepFn/retentionTaskRunSweepFn vars for the duration of its own
// run and restores them via t.Cleanup. A `defer cancel()` that does not wait
// leaves a real window, however short, where THIS test's goroutine is still
// alive (and can still fire one more tick, reading whatever
// retentionSweepFn/retentionTaskRunSweepFn happen to be set to AT THAT
// MOMENT) after this test function has returned — including during a LATER,
// unrelated test's own run, corrupting its call-count/cutoff assertions.
// This is exactly what caused go-race's real, reproducible failure in
// TestRetentionSweep_InvokesTaskRunPruneWithSessionCutoff (a different test
// in this same package, which touches retentionTaskRunSweepFn directly and
// has no goroutine of its own to race against — the leak came from here).
//
// resetRetentionLoop() must be called by the caller BEFORE this, exactly as
// before — this helper only changes how the loop's exit is confirmed.
func startRetentionSweepLoopAndWaitForExit(
	t *testing.T,
	ctx context.Context,
	cancel context.CancelFunc,
	store *session.UnifiedStore,
	getCfg func() *config.Config,
	tickInterval time.Duration,
) {
	t.Helper()
	done := make(chan struct{})
	retentionLoopStarted.Do(func() {
		go func() {
			defer close(done)
			runRetentionSweepLoop(ctx, store, getCfg, tickInterval)
		}()
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("retention-sweep goroutine did not exit within 1s of context cancellation")
		}
	})
}

// newTestStore builds a *session.UnifiedStore backed by a temp directory.
func newTestStore(t *testing.T) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	return store
}

// enabledCfg returns a config with retention enabled (not disabled).
func enabledCfg(days int) func() *config.Config {
	return func() *config.Config {
		cfg := &config.Config{}
		cfg.Storage.Retention = config.OmnipusRetentionConfig{
			SessionDays: days,
			Disabled:    false,
		}
		return cfg
	}
}

// disabledCfg returns a config with retention explicitly disabled.
func disabledCfg() func() *config.Config {
	return func() *config.Config {
		cfg := &config.Config{}
		cfg.Storage.Retention = config.OmnipusRetentionConfig{
			Disabled: true,
		}
		return cfg
	}
}

// TestRetentionSweep_NightlyGoroutineTicks verifies that the goroutine fires
// the sweep function at least N times within N*tickInterval.
func TestRetentionSweep_NightlyGoroutineTicks(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 20 * time.Millisecond
	want := 3

	var count atomic.Int64
	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) {
		count.Add(1)
		return 0, nil
	}
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())
	startRetentionSweepLoopAndWaitForExit(t, ctx, cancel, store, enabledCfg(7), tick)

	deadline := time.After(time.Duration(want+2) * tick * 2)
	for {
		select {
		case <-deadline:
			t.Fatalf("sweep called %d times, want >= %d within deadline", count.Load(), want)
		default:
			if count.Load() >= int64(want) {
				return
			}
			time.Sleep(tick / 4)
		}
	}
}

// TestRetentionSweep_GracefulShutdown verifies the goroutine exits within 1s
// of context cancellation.
func TestRetentionSweep_GracefulShutdown(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 500 * time.Millisecond

	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) { return 0, nil }
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	retentionLoopStarted = sync.Once{}
	retentionLoopStarted.Do(func() {
		go func() {
			defer close(done)
			runRetentionSweepLoop(ctx, store, enabledCfg(7), tick)
		}()
	})

	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("goroutine did not exit within 1s of context cancellation")
	}
}

// TestRetentionSweep_SkipsWhenDisabled verifies that when retention is
// disabled the sweep function is never called.
func TestRetentionSweep_SkipsWhenDisabled(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 20 * time.Millisecond

	var count atomic.Int64
	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) {
		count.Add(1)
		return 0, nil
	}
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())
	startRetentionSweepLoopAndWaitForExit(t, ctx, cancel, store, disabledCfg(), tick)

	time.Sleep(tick * 5)

	if count.Load() != 0 {
		t.Fatalf("sweep called %d times, expected 0 when disabled", count.Load())
	}
}

// TestRetentionSweep_ContinuesAfterError verifies the goroutine does not exit
// when the sweep function returns an error on the first tick.
func TestRetentionSweep_ContinuesAfterError(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 20 * time.Millisecond

	var count atomic.Int64
	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) {
		n := count.Add(1)
		if n == 1 {
			return 0, &testSweepError{"simulated sweep error"}
		}
		return 0, nil
	}
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())
	startRetentionSweepLoopAndWaitForExit(t, ctx, cancel, store, enabledCfg(7), tick)

	deadline := time.After(tick * 10)
	for {
		select {
		case <-deadline:
			t.Fatalf("sweep called %d times, want >= 2 (error then success)", count.Load())
		default:
			if count.Load() >= 2 {
				return
			}
			time.Sleep(tick / 4)
		}
	}
}

type testSweepError struct{ msg string }

func (e *testSweepError) Error() string { return e.msg }

// TestRetentionSweep_PanicRecovery verifies that a panic in the sweep function
// on the first tick is recovered and the goroutine continues to fire on the
// second tick.
func TestRetentionSweep_PanicRecovery(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 20 * time.Millisecond

	var count atomic.Int64
	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) {
		n := count.Add(1)
		if n == 1 {
			panic("simulated sweep panic")
		}
		return 0, nil
	}
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())
	startRetentionSweepLoopAndWaitForExit(t, ctx, cancel, store, enabledCfg(7), tick)

	deadline := time.After(tick * 10)
	for {
		select {
		case <-deadline:
			t.Fatalf("sweep called %d times, want >= 2 (panic then success)", count.Load())
		default:
			if count.Load() >= 2 {
				return
			}
			time.Sleep(tick / 4)
		}
	}
}

// TestRetentionSweep_MutexSharedWithOnDemand verifies that when a caller holds
// retentionSweepMu the nightly tick blocks and does not invoke the sweep
// function until the mutex is released.
func TestRetentionSweep_MutexSharedWithOnDemand(t *testing.T) {
	resetRetentionLoop()

	store := newTestStore(t)
	tick := 30 * time.Millisecond

	var count atomic.Int64
	orig := retentionSweepFn
	retentionSweepFn = func(_ *session.UnifiedStore, _ int) (int, error) {
		count.Add(1)
		return 0, nil
	}
	t.Cleanup(func() { retentionSweepFn = orig })

	ctx, cancel := context.WithCancel(context.Background())

	// Acquire the shared mutex before starting the loop so the first tick blocks.
	retentionSweepMu.Lock()

	startRetentionSweepLoopAndWaitForExit(t, ctx, cancel, store, enabledCfg(7), tick)

	// Wait for at least two tick intervals; the goroutine should be blocked on
	// the mutex and must not have called retentionSweepFn.
	time.Sleep(tick * 3)

	if count.Load() != 0 {
		retentionSweepMu.Unlock()
		t.Fatalf("sweep called %d times while mutex was held, expected 0", count.Load())
	}

	// Release the mutex and assert the sweep now proceeds.
	retentionSweepMu.Unlock()

	deadline := time.After(tick * 5)
	for {
		select {
		case <-deadline:
			t.Fatalf("sweep not called after mutex released within deadline")
		default:
			if count.Load() >= 1 {
				return
			}
			time.Sleep(tick / 4)
		}
	}
}
