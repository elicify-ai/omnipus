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
	defer cancel()

	startRetentionSweepLoop(ctx, store, enabledCfg(7), tick)

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
	defer cancel()

	startRetentionSweepLoop(ctx, store, disabledCfg(), tick)

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
	defer cancel()

	startRetentionSweepLoop(ctx, store, enabledCfg(7), tick)

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
	defer cancel()

	startRetentionSweepLoop(ctx, store, enabledCfg(7), tick)

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
	defer cancel()

	// Acquire the shared mutex before starting the loop so the first tick blocks.
	retentionSweepMu.Lock()

	startRetentionSweepLoop(ctx, store, enabledCfg(7), tick)

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
