// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

// Package entity — isolates the sidecar flock's OWN contribution to
// Store.Update's concurrency contract, independent of the in-process striped
// mutex (lock.go).
//
// This closes review finding #1 against ADR-054 D3: in-process, the striped
// mutex ALONE already satisfies every existing test in this package —
// TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate
// (store_test.go) passes just the same whether or not fileutil.WithFlock is
// present in Store.Update, because that test only ever observes ONE
// process's in-memory view, which the mutex already fully serializes. No
// test isolated the flock's contribution, so a silent regression deleting it
// from Store.Update would never be caught by anything in this package.
//
// SCOPE — this file covers Update ONLY. An earlier revision of this comment
// claimed the gap was closed for "the Update/Create/Delete call sites", which
// was an overclaim: this technique (holding one call open mid-critical-section
// and probing the sidecar path from an independent os.File handle) is only
// implemented here for Update. Cross-process coverage for Create (a same-ID
// creation race — exactly one winner, never two, never a corrupted record)
// and for Delete (racing against concurrent Updates — never a resurrected
// entity after a successful delete) lives in
// pkg/entity/store_crossprocess_test.go's
// TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner and
// TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity,
// respectively — both proven non-vacuous the same stub/restore way as this
// file's own test. See ADR-054 §5.1 for the full accounting of what is
// covered where.
//
// TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection closes the
// Update gap directly: it holds one Update call open mid-critical-section
// (via a mutate callback blocked on a channel) and, from the SAME process but
// a wholly independent os.File handle, attempts a non-blocking flock on the
// exact sidecar path (Store.lockPath) Update is supposed to be holding.
// flock(2) locks bind to the open file description, not the process, so two
// separate os.OpenFile calls on the same path — even within one process —
// genuinely contend (pkg/tools/browser/coordinator_lock_unix.go documents
// and relies on this same property: "two separate open() calls contend even
// within one process"). This bypasses the Go-level mutex entirely: the probe
// goroutine never touches lock.go's stripedLock, so a pass here can ONLY be
// explained by Store.Update actually holding the OS-level flock.
//
// PORTABILITY CAVEAT: the "two open file descriptions on the same path
// genuinely contend" property this test relies on holds on local
// Linux/macOS/BSD filesystems, but NOT universally — Linux emulates flock(2)
// over NFS using POSIX record locks scoped per-PROCESS, not per open file
// description, so on an NFS-mounted test directory this same-process probe
// would NOT block and the test would fail closed (a spurious failure, not a
// missed regression), which is an acceptable failure mode for a test whose
// job is to catch a real regression, not to pass silently on every possible
// filesystem.
package entity

import (
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection isolates the
// sidecar flock's contribution to Store.Update, independent of the
// in-process striped mutex.
//
// Traces to: docs/internal/architecture/ADR-054-entity-config-separation.md
// §5.1 ("Testing-gap closure and Windows posture") — closes finding #1
// ("the striped mutex ALONE satisfies every existing test").
//
// PROVEN NOT VACUOUS (ADR-054 D3 review finding #1): with
// fileutil.WithFlock's call removed from Store.Update (so the load/mutate/
// write cycle runs with no OS-level lock at all — only the striped mutex),
// this test FAILS: the probe's LOCK_EX|LOCK_NB attempt succeeds immediately
// while the blocked Update goroutine is still mid-mutate, because nothing is
// holding the sidecar lock. Restoring fileutil.WithFlock makes it pass again.
// See the qa-lead session's before/after run transcript for the recorded
// evidence of both states.
func TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection(t *testing.T) {
	s := newTestStore(t)
	e := &testEntity{Name: "held"}
	if err := s.Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		_, updErr := s.Update(e.ID, func(inner *testEntity) error {
			close(entered)
			<-release
			inner.Name = "updated"
			return nil
		})
		done <- updErr
	}()

	// GAP 2 fix: register a once-guarded release RIGHT AFTER launching the
	// goroutine, before any t.Fatal in this function can fire. Without this,
	// a t.Fatal below (both the "never entered" timeout and the probe-open
	// failure fired BEFORE the explicit close(release) further down) calls
	// runtime.Goexit while the Update goroutine is still blocked on
	// <-release, holding BOTH the process-global striped-mutex shard
	// (lock.go — 64 shards shared by EVERY Store[T] in this binary) and the
	// sidecar flock forever. Any later test in this package whose id happens
	// to hash to that same shard would then block forever too, turning one
	// legible failure into a package-wide timeout panic. sync.Once makes the
	// eventual explicit release below safe to call a second time.
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer closeRelease()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Update never entered its critical section (mutate callback never ran)")
	}

	// Update is now blocked inside its (supposedly) flocked critical section.
	// Probe the exact sidecar path it should be holding, via an independent
	// open file description.
	probePath := s.lockPath(e.ID)
	probe, err := os.OpenFile(probePath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open sidecar lock for probe: %v", err)
	}
	defer probe.Close()

	lockErr := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)

	// Let Update finish regardless of what the probe observed, so the
	// goroutine never leaks past this test. Idempotent — see the
	// once-guarded defer registered right after the goroutine was launched.
	closeRelease()
	if updErr := <-done; updErr != nil {
		t.Fatalf("Update returned error: %v", updErr)
	}

	if lockErr == nil {
		// We successfully grabbed the lock WHILE Update claimed to be
		// mid-critical-section. Release what we took so we don't leak lock
		// state, then fail loudly: this is exactly finding #1's blind spot —
		// the striped mutex alone would let every OTHER test in this package
		// pass even though no OS-level exclusion is happening here at all.
		_ = unix.Flock(int(probe.Fd()), unix.LOCK_UN)
		t.Fatal("acquired the sidecar flock WHILE Store.Update was inside its critical section — " +
			"fileutil.WithFlock is not actually serializing this path. The in-process striped " +
			"mutex cannot explain a pass here (this probe never touches it), so this failure means " +
			"the cross-process guarantee ADR-054 D3 claims does not hold even in principle.")
	}
	if lockErr != unix.EWOULDBLOCK {
		t.Fatalf("unexpected flock probe error: %v, want EWOULDBLOCK", lockErr)
	}
	t.Logf("flock probe correctly blocked (EWOULDBLOCK) while Update held the sidecar lock")

	// Now that Update has returned (and its defer released + closed its own
	// flock handle), the SAME probe file description must be able to acquire
	// the lock — proving the hold was scoped to the critical section, not a
	// leak.
	if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("flock did not become available after Update finished: %v", err)
	}
	_ = unix.Flock(int(probe.Fd()), unix.LOCK_UN)
}
