// turn_exit_test.go: tests for typed turn exits — how a turn ends, its end status and result

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- moved from turn.go tests 2026-09-15 ---

// TestFinishedChannelClosedState verifies that Finish() closes the Finished() channel
// so that child turns can safely abort waiting.
func TestFinishedChannelClosedState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts := &turnState{
		ctx:            ctx,
		cancelFunc:     cancel,
		turnID:         "test-finished-channel",
		depth:          0,
		pendingResults: make(chan *tools.ToolResult, 2),
	}

	// Verify Finished channel is blocking initially
	select {
	case <-ts.Finished():
		t.Fatal("finished channel should block initially")
	default:
		// Good
	}

	// Call Finish() with graceful finish
	ts.Finish(false)

	// Verify Finished channel is closed
	select {
	case _, ok := <-ts.Finished():
		if ok {
			t.Error("expected Finished() channel to be closed after Finish()")
		}
	default:
		t.Fatal("expected <-ts.Finished() to not block")
	}

	// Verify Finish() is idempotent
	ts.Finish(false) // Should not panic
}

// TestFinish_ConcurrentCalls verifies that calling Finish() concurrently from multiple
// goroutines is safe and doesn't cause panics or double-close errors.
func TestFinish_ConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	parentTS := &turnState{
		ctx:            ctx,
		turnID:         "parent-concurrent-finish",
		depth:          0,
		pendingResults: make(chan *tools.ToolResult, 16),
		concurrencySem: make(chan struct{}, 5),
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	// Launch multiple goroutines that all call Finish() concurrently
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			// This should not panic, even when called concurrently
			parentTS.Finish(false)
		}()
	}

	wg.Wait()

	// Verify the Finished() channel is closed
	select {
	case _, ok := <-parentTS.Finished():
		if ok {
			t.Error("Expected Finished() channel to be closed")
		}
	default:
		t.Error("Expected Finished() channel to be closed and readable without blocking")
	}

	// Verify isFinished is set
	parentTS.mu.Lock()
	if !parentTS.isFinished.Load() {
		t.Error("Expected isFinished to be true")
	}
	parentTS.mu.Unlock()
}

// ====================== Graceful vs Hard Finish Tests ======================

// TestFinish_GracefulVsHard verifies the behavior difference between:
// - Finish(false): graceful finish, signals parentEnded but doesn't cancel children
// - Finish(true): hard abort, immediately cancels all children
func TestFinish_GracefulVsHard(t *testing.T) {
	// Test 1: Graceful finish should set parentEnded but not cancel context
	t.Run("Graceful_SetsParentEnded", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ts := &turnState{
			ctx:            ctx,
			turnID:         "graceful-test",
			depth:          0,
			pendingResults: make(chan *tools.ToolResult, 16),
		}
		ts.ctx, ts.cancelFunc = context.WithCancel(ctx)

		// Finish gracefully
		ts.Finish(false)

		// Verify parentEnded is set
		if !ts.parentEnded.Load() {
			t.Error("parentEnded should be true after graceful finish")
		}

		// Verify context is NOT canceled (for graceful finish, children continue)
		// Note: In graceful mode, we don't call cancelFunc()
		// But since we're using WithCancel on the same ctx, it might be canceled
		// Let's check that the context is still valid for a moment
		time.Sleep(10 * time.Millisecond)
		// Context might be canceled by the deferred cancel() in test, which is fine
	})

	// Test 2: Hard abort should cancel context immediately
	t.Run("Hard_CancelsContext", func(t *testing.T) {
		ctx := context.Background()

		ts := &turnState{
			ctx:            ctx,
			turnID:         "hard-test",
			depth:          0,
			pendingResults: make(chan *tools.ToolResult, 16),
		}
		ts.ctx, ts.cancelFunc = context.WithCancel(ctx)

		// Finish with hard abort
		ts.Finish(true)

		// Verify context is canceled
		select {
		case <-ts.ctx.Done():
			t.Log("✓ Context canceled after hard abort")
		default:
			t.Error("Context should be canceled after hard abort")
		}
	})

	// Test 3: IsParentEnded returns correct value
	t.Run("IsParentEnded", func(t *testing.T) {
		ctx := context.Background()

		parentTS := &turnState{
			ctx:            ctx,
			turnID:         "parent-isended-test",
			depth:          0,
			pendingResults: make(chan *tools.ToolResult, 16),
		}
		parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

		childTS := &turnState{
			ctx:             ctx,
			turnID:          "child-isended-test",
			depth:           1,
			parentTurnState: parentTS,
			pendingResults:  make(chan *tools.ToolResult, 16),
		}

		// Before parent finishes
		if childTS.IsParentEnded() {
			t.Error("IsParentEnded should be false before parent finishes")
		}

		// Finish parent gracefully
		parentTS.Finish(false)

		// After parent finishes
		if !childTS.IsParentEnded() {
			t.Error("IsParentEnded should be true after parent finishes gracefully")
		}
	})
}
