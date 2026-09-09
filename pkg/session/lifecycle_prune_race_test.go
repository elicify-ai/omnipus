// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// errBox wraps an error so it can be stored in an atomic.Value, which
// requires every Store call to use the same concrete type — a bare `error`
// interface value would panic if two different underlying error types were
// ever stored across calls.
type errBox struct{ err error }

// TestLifecycleStore_PruneTerminal_ReopenDuringPruneWindow is a regression
// test for BUG 2 (concurrency review, commit 99d4e729): PruneTerminal's
// check-then-act was not atomic. The old implementation called s.Load(id) —
// which internally takes AND RELEASES the per-session striped lock — to
// decide a session was terminal and past the retention cutoff, then
// separately re-acquired the SAME lock just to remove the file. In the
// window between those two acquisitions no lock was held for that
// session_id at all: a follow_up/Play legitimately minting a fresh,
// non-terminal generation onto the terminal tail in that exact gap (the
// package doc explicitly allows this) would have that brand-new generation
// silently destroyed when the prune then deleted the whole .jsonl anyway —
// violating PruneTerminal's own stated invariant ("a NON-terminal record is
// NEVER pruned regardless of age, full stop").
//
// This test exercises the window directly rather than hoping independent
// goroutine scheduling lands inside a nanosecond-scale gap:
// pruneTerminalRaceHook (lifecycle.go, test-only, always nil in production)
// fires at exactly the "decided to remove, about to os.Remove" instant,
// deterministically, on every run. A pool of spinner goroutines — already
// running and busy-polling an atomic flag, not freshly scheduled from a
// parked/blocked state — race PruneTerminal's own very next statement for
// the SAME per-session striped mutex:
//
//   - BUGGY code: the hook fires OUTSIDE any lock (Load already released
//     it), so a spinner can win that race, successfully append a new
//     non-terminal generation via Mutate, and then have PruneTerminal
//     delete the file anyway, destroying it.
//   - FIXED code: the hook fires INSIDE pruneTerminalOne's single
//     continuously-held lock, so a spinner's Mutate call cannot even
//     attempt the append until AFTER the file is already gone — no data is
//     ever lost because none was ever at risk of being read stale.
//
// The assertion is the invariant that must hold either way: IF the reopen
// actually observed the live terminal tail and committed a new generation,
// THEN that generation must survive. The test only fails when that
// invariant is violated — i.e., only on runs where the race actually lands
// inside the (buggy) window. See the report's mutation-test evidence for the
// observed hit rate across repeated runs against the reverted code.
func TestLifecycleStore_PruneTerminal_ReopenDuringPruneWindow(t *testing.T) {
	s := newTestLifecycleStore(t)
	const id = "sess-reopen-race"

	if err := s.Persist(&LifecycleRecord{
		SessionID:      id,
		Generation:     0,
		State:          LifecycleCompleted,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        "agent-1",
	}); err != nil {
		t.Fatalf("seed Persist failed: %v", err)
	}

	var hookFired atomic.Bool
	var reopenAttempted atomic.Bool
	var reopenSawExistingTail atomic.Bool
	var reopenErr atomic.Value // holds errBox

	// fn simulates a follow_up/Play: mint a new generation onto whatever tail
	// currently exists. If the tail is already gone (next == nil), this
	// reopen attempt lost the race against an ALREADY-COMPLETED removal —
	// correct, non-lossy no-op, not a failure.
	fn := func(next *LifecycleRecord) error {
		if next == nil {
			return nil
		}
		reopenSawExistingTail.Store(true)
		next.Generation++
		next.ResumedFrom = fmt.Sprintf("gen%d", next.Generation-1)
		next.State = LifecycleQueued
		next.NeedsInput = nil
		return nil
	}

	stop := make(chan struct{})
	const spinners = 8
	var wg sync.WaitGroup
	for i := 0; i < spinners; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if !hookFired.Load() {
					continue
				}
				if reopenAttempted.CompareAndSwap(false, true) {
					err := s.Mutate(id, fn)
					reopenErr.Store(errBox{err})
				}
				return
			}
		}()
	}

	pruneTerminalRaceHook = func(hookID string) {
		if hookID == id {
			hookFired.Store(true)
		}
	}
	t.Cleanup(func() { pruneTerminalRaceHook = nil })

	// retentionDays=1, "now" pushed 48h into the future so the just-seeded
	// (real-time) UpdatedAt is comfortably past the resulting 24h-in-the-
	// future cutoff — eligible for pruning without needing to fake on-disk
	// timestamps (Persist always stamps UpdatedAt = time.Now()).
	removed, err := s.PruneTerminal(1, time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("PruneTerminal failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected PruneTerminal to report 1 removal, got %d", removed)
	}

	// hookFired is already guaranteed true at this point (PruneTerminal, and
	// therefore pruneTerminalOne's synchronous hook call, has already
	// returned) — but give the spinners a real window to actually notice it
	// and win the reopenAttempted CompareAndSwap BEFORE telling them to stop.
	// Closing `stop` immediately would race each spinner's own `select`
	// (which checks `<-stop` before re-checking hookFired on every loop):
	// if a spinner is scheduled again only after `stop` is already closed, it
	// takes the `<-stop` branch and returns WITHOUT ever re-observing
	// hookFired==true, even though the flag had flipped well before — a false
	// "reopen never attempted" that has nothing to do with BUG 2 itself, just
	// test-harness scheduling. Waiting here for reopenAttempted (or a
	// generous timeout) removes that self-inflicted race entirely.
	waitDeadline := time.Now().Add(3 * time.Second)
	for !reopenAttempted.Load() && time.Now().Before(waitDeadline) {
		runtime.Gosched()
	}
	close(stop)
	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("spinner goroutines never finished")
	}

	if !reopenAttempted.Load() {
		t.Fatal("test setup bug: the reopen was never attempted (hook never fired, or no spinner ran)")
	}
	if boxed, ok := reopenErr.Load().(errBox); ok && boxed.err != nil {
		t.Fatalf("reopen Mutate returned an unexpected error: %v", boxed.err)
	}

	finalTail, _, tailErr := s.tail(id)
	if tailErr != nil {
		t.Fatalf("final tail read failed: %v", tailErr)
	}

	if reopenSawExistingTail.Load() {
		// The reopen raced INTO the prune decision window, observed the
		// still-terminal tail, and successfully committed a new, non-terminal
		// generation. That generation MUST survive — losing it is exactly
		// BUG 2 (a lost update / silent data destruction).
		if finalTail == nil {
			t.Fatal("BUG 2 reproduced: the reopen committed a new non-terminal generation, but the " +
				"session file was deleted anyway — the reopened generation was silently destroyed by PruneTerminal")
		}
		if finalTail.Terminal() {
			t.Fatalf("BUG 2 reproduced: reopen committed a new non-terminal generation, but the final tail "+
				"is terminal state %q — the reopen was lost", finalTail.State)
		}
		if finalTail.Generation != 1 {
			t.Fatalf("expected the surviving reopened generation to be 1, got %d", finalTail.Generation)
		}
		t.Log("reopen raced into the prune decision window and its new generation correctly survived")
	} else {
		// The reopen only ran once the removal was already fully committed
		// (the file was gone by the time it got the lock) — there was
		// nothing left to lose, and no window to have lost it in.
		if finalTail != nil {
			t.Fatal("reopen observed no existing tail (file already removed) yet a tail record exists afterward — inconsistent")
		}
		t.Log("reopen ran strictly after prune's atomic removal — nothing was lost because nothing existed to lose at that point")
	}
}
