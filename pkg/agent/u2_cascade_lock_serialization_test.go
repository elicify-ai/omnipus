// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// u2_cascade_lock_serialization_test.go — CHECK round 2 finding 2 (MEDIUM,
// coordination/logs/fix890-opus/u2-check2/37-U2-CHECK-AUDIT.md): the
// committed U2 pack's TestU2CancelFrameScope
// (pkg/gateway/adr093_web_stop_record_test.go:341) sends one synchronous
// Stop per subtest — it never overlaps two Stop/cancel operations on the
// same session, so it cannot tell whether SteerCanceller.cascade's per-session
// lock (pkg/agent/steer_cancel.go:439, `lock := c.cascadeLock(sessionID);
// lock.Lock(); defer lock.Unlock()`) is load-bearing.
//
// StopTurns is the exact production entry pkg/gateway/websocket_stop_scope.go
// ::requestTurnStop uses for a real web Stop
// (agent.CancelScope{TurnOnly: true}) — this test calls it directly (same
// package, no new mock beyond the cancelTurn substitution every existing
// cascade test in steer_cancel_test.go already uses, e.g.
// TestCascade_StampsAndCancelsReenteredChild), with two overlapping calls on
// the SAME session id, and proves the second call's live-cancellation
// callback never runs concurrently with the first's — i.e. the lock actually
// serializes cascade.cancelStamped's cancelTurn invocations, not just the
// stampStop record mutation.
package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestCascade_LockSerializesOverlappingStopTurns proves CHECK round 2 finding
// 2: two StopTurns calls on the same session, overlapped so the first is
// still inside its live-cancellation callback when the second starts, must
// never run their cancelTurn callbacks concurrently.
func TestCascade_LockSerializesOverlappingStopTurns(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	persistSteerLifecycle(t, store, testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1))
	canceller := NewSteerCanceller(store)
	principal := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"}

	var active int32    // callbacks currently inside cancelTurn, across both calls
	var maxActive int32 // high-water mark of `active`
	bumpMax := func(n int32) {
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				return
			}
		}
	}

	aEntered := make(chan struct{})
	aRelease := make(chan struct{})
	bEntered := make(chan struct{}, 1)

	cancelTurnA := func(context.Context, string, int) (GenerationCancelResult, error) {
		bumpMax(atomic.AddInt32(&active, 1))
		close(aEntered)
		<-aRelease // hold the cascade lock (if it exists) until the test releases it
		atomic.AddInt32(&active, -1)
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}
	cancelTurnB := func(context.Context, string, int) (GenerationCancelResult, error) {
		bumpMax(atomic.AddInt32(&active, 1))
		select {
		case bEntered <- struct{}{}:
		default:
		}
		atomic.AddInt32(&active, -1)
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		if _, err := canceller.StopTurns(context.Background(), "root", principal, false, cancelTurnA); err != nil {
			t.Errorf("StopTurns A: %v", err)
		}
	}()

	select {
	case <-aEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: call A never reached its live-cancellation callback")
	}

	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		if _, err := canceller.StopTurns(context.Background(), "root", principal, false, cancelTurnB); err != nil {
			t.Errorf("StopTurns B: %v", err)
		}
	}()

	// While A is still holding its callback open, B must NOT have entered
	// its own callback — that would mean the cascade lock let two calls into
	// live cancellation on the same session at once. A bounded, generous
	// window: this assertion is a negative (B did not yet act), so a false
	// PASS here would require B to lose a race against the wall clock, not
	// against the lock — the failure mode this test exists to catch is B
	// entering almost immediately (no lock at all), which this window catches
	// deterministically.
	select {
	case <-bEntered:
		t.Fatal("cascade B entered live cancellation while cascade A's own callback was still in progress on the same session — the cascade lock did not serialize them")
	case <-time.After(300 * time.Millisecond):
	}

	close(aRelease)
	select {
	case <-aDone:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: call A never finished after release")
	}
	select {
	case <-bDone:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: call B never finished after A released the lock")
	}

	if got := atomic.LoadInt32(&maxActive); got > 1 {
		t.Fatalf("at most 1 cancelTurn callback may be active at once for the same session under the cascade lock; observed %d concurrently", got)
	}
}
