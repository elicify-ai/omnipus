// Package tools — session_stress_test.go
//
// HIGH-CONCURRENCY stress coverage for the just-landed background-bash
// cancellation fix (see session.go's KillAllForSession "canceled" relabel,
// SessionManager.KillAll(), and pkg/agent/cancel.go's RequestCancel ->
// hooks.KillBackgroundSessions -> KillAllForSession wiring, backing
// AgentLoop.Close()'s KillAll() shutdown reaper).
//
// The correctness tests (session_kill_all_for_session_test.go, session_test.go)
// exercise each method with a small, fixed, sequential set of sessions. This
// file instead races MANY concurrent KillAllForSession callers (mirroring
// several overlapping RequestCancel cancellations, including multiple
// callers targeting the SAME owner session — a double-kill race) against
// MANY concurrent KillAll callers (mirroring an overlapping AgentLoop.Close()
// shutdown reaper) over one shared SessionManager and its ProcessSessions,
// plus concurrent List()/Get() read traffic, to catch:
//   - a lost-cancel timing race: a session left "running" forever because
//     every concurrent caller lost the two-phase (sm.mu.RLock -> release ->
//     per-session s.mu.Lock) race against some other caller and none of them
//     actually applied the kill — caught by asserting every session reaches
//     IsDone() after the wave completes (see the loop near the end of the
//     test below);
//   - a deadlock in that same two-phase locking pattern, which
//     KillAllForSession/KillAll/cleanupOldSessions all share — caught by the
//     30s/10s watchdog timeouts below, which fail loud (t.Fatal) rather than
//     hang the whole test binary;
//   - a double-kill panic or an inconsistent terminal state left behind by
//     two callers racing to kill the same session — caught by recoverPanic
//     below (t.Errorf, not t.Fatal, so one goroutine's panic doesn't abort
//     the others mid-wave) and by the terminal-status/counter accounting at
//     the end.
//
// NOTE: this is NOT `go test -race` coverage, despite the name "stress" and
// the concurrency-bug list above sounding like it. The Go race detector
// needs cgo (CGO_ENABLED=1); this project builds with CGO_ENABLED=0
// everywhere (see CLAUDE.md's Tech Constraints), so `-race` is unavailable
// in this repo's CI and is never actually run against this file. What this
// test catches is purely Go-level and assertion-driven: lost updates/lost
// cancels under contention, deadlocks (via the watchdog), and panics (via
// recoverPanic) — NOT a race detector's happens-before analysis. A genuine
// data race on SessionManager.sessions or a ProcessSession's fields would
// still need `-race` (or a cgo-enabled build) to catch directly; this file
// is not a substitute for that, only a best-effort outcome-level check that
// runs in this project's actual constraints.
//
// Uses fake, deliberately out-of-range PIDs (fakeUnusedPIDBase, defined in
// session_kill_all_for_session_test.go, same package, no build tag) so
// killProcessGroup's underlying syscall.Kill(-pid, ...) reliably returns
// ESRCH (treated as success) rather than spawning real child processes — the
// timing race under test is in Go-level locking, not OS process management.

package tools

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStress_SessionManager_ConcurrentKillAllForSessionAndKillAll is Stress
// B: concurrent background-session kill (see file doc above).
func TestStress_SessionManager_ConcurrentKillAllForSessionAndKillAll(t *testing.T) {
	const numOwners = 20
	const sessionsPerOwner = 5
	const totalSessions = numOwners * sessionsPerOwner
	// Offset well clear of the PID ranges used by session_test.go /
	// session_kill_all_for_session_test.go (fakeUnusedPIDBase+1..~800) so a
	// future change that parallelizes those tests can never collide with
	// this test's PIDs.
	const pidOffset = 500000

	sm := NewSessionManager()

	owners := make([]string, numOwners)
	for i := 0; i < numOwners; i++ {
		owners[i] = fmt.Sprintf("stress-owner-%d", i)
	}

	sessionIDs := make([]string, 0, totalSessions)
	for oi, owner := range owners {
		for si := 0; si < sessionsPerOwner; si++ {
			id := fmt.Sprintf("stress-bg-%d-%d", oi, si)
			sessionIDs = append(sessionIDs, id)
			sm.Add(&ProcessSession{
				ID:             id,
				OwnerSessionID: owner,
				Command:        "sleep 300",
				Status:         "running",
				PID:            fakeUnusedPIDBase + pidOffset + oi*sessionsPerOwner + si,
				StartTime:      time.Now().Unix(),
			})
		}
	}

	var panics int32
	recoverPanic := func(label string) {
		if r := recover(); r != nil {
			atomic.AddInt32(&panics, 1)
			t.Errorf("panic in %s: %v", label, r)
		}
	}

	// killWG covers the goroutines whose completion defines "the kill wave
	// is done" — owner-scoped killers + global reapers. Reader goroutines
	// are tracked separately (readerWG) and are stopped AFTER the kill wave
	// finishes, to avoid a self-inflicted deadlock in the test itself
	// (waiting on a WaitGroup that includes goroutines which only exit once
	// a signal is sent AFTER that same WaitGroup completes).
	var killWG sync.WaitGroup

	// Owner-scoped killers: several goroutines PER owner call
	// KillAllForSession concurrently for the SAME owner, forcing the
	// double-kill race within one owner's session set.
	const killersPerOwner = 3
	for _, owner := range owners {
		for k := 0; k < killersPerOwner; k++ {
			killWG.Add(1)
			go func() {
				defer killWG.Done()
				defer recoverPanic("KillAllForSession")
				sm.KillAllForSession(owner)
			}()
		}
	}

	// Global reapers: several goroutines call KillAll() concurrently,
	// mirroring an overlapping AgentLoop.Close() shutdown reaper racing
	// against the owner-scoped cancels above.
	const reapers = 5
	for i := 0; i < reapers; i++ {
		killWG.Add(1)
		go func() {
			defer killWG.Done()
			defer recoverPanic("KillAll")
			sm.KillAll()
		}()
	}

	// Concurrent read traffic on the same manager (List/Get) to exercise the
	// map's read path while the killers/reapers mutate it.
	var readerWG sync.WaitGroup
	stopReaders := make(chan struct{})
	const readers = 4
	for i := 0; i < readers; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			defer recoverPanic("reader")
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				sm.List()
				for _, id := range sessionIDs {
					_, _ = sm.Get(id)
				}
			}
		}()
	}

	// Watchdog: fail loud, not hang, if the kill/reap wave itself deadlocks.
	killersDone := make(chan struct{})
	go func() {
		killWG.Wait()
		close(killersDone)
	}()
	select {
	case <-killersDone:
	case <-time.After(30 * time.Second):
		close(stopReaders)
		t.Fatal("TIMEOUT: concurrent KillAllForSession/KillAll wave did not complete — suspected deadlock")
	}

	// Only stop (and wait for) the readers once the kill wave itself is
	// confirmed done — closing this before killWG completes would be fine
	// for the readers, but doing it in this order keeps read traffic
	// present for the ENTIRE kill/reap wave, maximizing overlap.
	close(stopReaders)
	readersDone := make(chan struct{})
	go func() {
		readerWG.Wait()
		close(readersDone)
	}()
	select {
	case <-readersDone:
	case <-time.After(10 * time.Second):
		t.Fatal("reader goroutines did not exit after the stop signal")
	}

	if p := atomic.LoadInt32(&panics); p > 0 {
		t.Fatalf("%d goroutine(s) panicked during the concurrent kill wave", p)
	}

	// Every session must have reached a terminal state — killed by SOME
	// caller (an owner-scoped KillAllForSession or a global KillAll), since
	// every owner has multiple killers AND multiple global reapers running
	// for the whole wave duration.
	var canceled, doneCount, other int
	for _, id := range sessionIDs {
		s, err := sm.Get(id)
		if err != nil {
			t.Fatalf("session %s vanished from the manager: %v", id, err)
		}
		if !s.IsDone() {
			t.Errorf("session %s (owner %s) was never killed: status=%s", id, s.OwnerSessionID, s.GetStatus())
			continue
		}
		switch s.GetStatus() {
		case "canceled":
			canceled++
		case "done":
			doneCount++
		default:
			other++
		}
	}
	t.Logf("terminal status breakdown: canceled=%d done=%d other=%d (total=%d)",
		canceled, doneCount, other, totalSessions)

	// KillAllForSession's own counter increments ONLY on its own successful
	// Kill(), immediately followed by its "canceled" relabel (see
	// session.go) — so across the whole wave the counter must equal exactly
	// the number of sessions whose FINAL status is "canceled", regardless of
	// how many of the totalSessions were instead won by a racing KillAll()
	// call (which leaves status "done", uncounted here by design).
	if got, want := sm.KilledBackgroundSessionsCount(), int64(canceled); got != want {
		t.Errorf("KilledBackgroundSessionsCount()=%d, want %d (must equal the number of sessions relabeled 'canceled')",
			got, want)
	}
	if canceled+doneCount+other != totalSessions {
		t.Errorf("status breakdown does not account for all sessions: %d+%d+%d != %d",
			canceled, doneCount, other, totalSessions)
	}
}
