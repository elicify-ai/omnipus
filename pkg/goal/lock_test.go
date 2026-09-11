// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// withLockObserver swaps goalLockAcquireFn/goalLockReleaseFn for the
// duration of fn, recording every (event, id) pair in call order, then
// restores the original (no-op) hooks. Not safe to run in parallel with
// another test using the same hooks — none of this file's tests call
// t.Parallel for that reason.
func withLockObserver(t *testing.T, fn func()) []string {
	t.Helper()
	var mu sync.Mutex
	var events []string

	origAcquire, origRelease := goalLockAcquireFn, goalLockReleaseFn
	goalLockAcquireFn = func(id string) {
		mu.Lock()
		events = append(events, "acquire:"+id)
		mu.Unlock()
	}
	goalLockReleaseFn = func(id string) {
		mu.Lock()
		events = append(events, "release:"+id)
		mu.Unlock()
	}
	t.Cleanup(func() {
		goalLockAcquireFn, goalLockReleaseFn = origAcquire, origRelease
	})

	fn()

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), events...)
}

// TestGoalSessionLockOrder is GOAL-FR-008's named test (S-47): it proves the
// exported acquire/release observation seam fires, in the correct
// acquire-before-release order, around each of Store's three mutating
// operations (Create/Update/Delete) — the seam a later wave (S3's retention
// sweep, which also holds ADR-057 session shards) needs to make the total
// lock order `goalLock -> taskFileLock -> sessionLock -> cacheMu`
// observable in a test, since go test -race reports nothing for a
// lock-order inversion that does not happen to deadlock in the run under
// test.
//
// This package does not itself import pkg/session (S1's write-set does not
// include pkg/session), so it cannot assert the cross-package "no goal lock
// held across a session lock" half of that order directly — that assertion
// belongs to the wave that holds both lock classes. What THIS test proves
// is the half S1 owns: the seam exists, is exported (package-level, not
// unexported-and-untestable), fires exactly once per acquire and once per
// release for every Create/Update/Delete call, and never fires out of
// order (a release before its matching acquire, or two acquires for the
// same id with no release between them, would both be exactly the kind of
// bug this seam exists to catch).
func TestGoalSessionLockOrder(t *testing.T) {
	s := newTestStore(t)

	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	var createID string
	events := withLockObserver(t, func() {
		if err := s.Create(g); err != nil {
			t.Fatalf("Create: %v", err)
		}
		createID = g.GoalID
	})
	assertAcquireThenRelease(t, events, createID)

	events = withLockObserver(t, func() {
		if _, err := s.Update(createID, func(gg *Goal) error { return nil }); err != nil {
			t.Fatalf("Update: %v", err)
		}
	})
	assertAcquireThenRelease(t, events, createID)

	events = withLockObserver(t, func() {
		if err := s.Delete(createID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
	assertAcquireThenRelease(t, events, createID)
}

// assertAcquireThenRelease asserts events is exactly
// ["acquire:<id>", "release:<id>"] — proving the seam fired exactly once on
// each side, for the right id, in the right order. A mutation that removed
// the acquire/release calls from Store.Create/Update/Delete (or reordered
// them) would fail this assertion; a mutation that swapped in a no-op
// hook-firing scheme would leave events empty and fail it too.
func assertAcquireThenRelease(t *testing.T, events []string, id string) {
	t.Helper()
	want := []string{"acquire:" + id, "release:" + id}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// TestGoalSessionLockOrder_ConcurrentDifferentIDsDoNotDeadlock is a basic
// concurrency sanity check on the observation seam itself: swapping in
// hooks that also acquire an unrelated mutex must not deadlock when several
// goroutines call Store.Update on DIFFERENT goal ids concurrently (proving
// the hooks are not accidentally serialising unrelated goals against a
// single shared lock of their own).
func TestGoalSessionLockOrder_ConcurrentDifferentIDsDoNotDeadlock(t *testing.T) {
	s := newTestStore(t)
	const n = 8
	ids := make([]string, n)
	for i := range ids {
		g := newTestGoal(t, generated.GoalOwnerKindSession, "owner")
		if err := s.Create(g); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids[i] = g.GoalID
	}

	withLockObserver(t, func() {
		var wg sync.WaitGroup
		for _, id := range ids {
			id := id
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.Update(id, func(gg *Goal) error {
					gg.RecordAttempt(gg.LastActivityAt)
					return nil
				}); err != nil {
					t.Errorf("Update(%s): %v", id, err)
				}
			}()
		}
		wg.Wait()
	})
}
