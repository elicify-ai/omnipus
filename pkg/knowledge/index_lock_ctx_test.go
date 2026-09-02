// Omnipus — a single-path index update must answer to its context even while a
// sweep holds the index lock.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUpdatePath_RespectsContextWhileSyncHoldsTheLock is the guard for the
// review's finding 9.
//
// SyncWith holds ix.mu for its WHOLE walk — seconds to minutes on a large
// collection. UpdatePath used to check ctx.Err() and then call a plain Lock(),
// which cannot be interrupted, so an agent's write during a first reconcile
// blocked for the entire sync with no timeout and no message. The ctx checks
// around the lock looked like they bounded the wait and bounded nothing.
//
// THE TEST HOLDS THE REAL LOCK rather than simulating a sweep, because the
// defect is about the lock itself and a fake sweep would prove nothing about
// it. The context is given a deadline far shorter than the hold, so a correct
// implementation returns on the deadline and a blocking one waits out the hold.
//
// The margins are deliberately wide apart — a 100ms deadline against a 3s hold
// — so this measures "did it give up" and not "was it slow". An earlier test on
// this branch used a 150ms window against ~300ms of natural latency and passed
// with the lock removed entirely; that is the failure mode being avoided here.
func TestUpdatePath_RespectsContextWhileSyncHoldsTheLock(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n\nalpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := OpenIndex(t.TempDir(), root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer func() { _ = ix.Close() }()

	// PRECONDITION: the lock must actually be free before we take it, or the
	// hold below proves nothing about contention.
	if !ix.mu.TryLock() {
		t.Fatal("precondition failed: index lock was already held before the test took it")
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(3 * time.Second)
		ix.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = ix.UpdatePath(ctx, "a.md")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("UpdatePath succeeded while the lock was held — it cannot have acquired it legitimately")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not carry the deadline cause, so a caller cannot tell contention from a real failure: %v", err)
	}
	// The load-bearing assertion. A blocking implementation returns only after
	// the 3s hold; a correct one returns on its own 100ms deadline.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("BLOCKING REGRESSION: UpdatePath waited %v for a lock held 3s, despite a 100ms context deadline — the context does not bound the wait", elapsed)
	}
	<-released
}
