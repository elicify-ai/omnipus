// Omnipus — the per-index reconcile lock: the one coordinator between
// whole-store scan-and-reconcile runs and single-path direct writes.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS (Codex review 2026-09-14, finding 4)
// Build-gated to the SQLite half (same constraint as sqlite.go): the
// reconcile lock coordinates SQLite-store writes; on a no-SQLite build Open
// refuses outright (nosqlite.go), there is no Index to reconcile, and the
// Direct methods this file calls do not exist.
//
// SQLite serializes individual STATEMENTS. It does not serialize OPERATIONS,
// and a reconcile is an operation that spans three of them:
//
//	1. Scan the collection (a disk walk — no SQL at all).
//	2. AllPaths — read every path the store currently holds.
//	3. A deletion pass — delete every store path step 1 did not see.
//
// A single-path direct write (pkg/knowledge/author.go's instant index refresh
// after a knowledge_edit write) is one UpsertNote. If it commits BETWEEN a
// reconcile's step 1 and its step 2, the reconcile's step 2 reads the new row,
// its step 1 scan did not contain the file, and its step 3 DELETES the row the
// direct write just committed — the write is silently lost, and the writer
// already returned success. Two concurrent reconciles have the same window on
// each other. The recovery path in pkg/vaultprops (openFindStore) then judged
// coverage against the reconcile's OWN scan count — a count taken before the
// interleaved write — and could accept the now-incomplete store as complete.
//
// The fix is a per-index mutual-exclusion lock with TWO granularities, both
// keyed by the index FILE PATH (the one thing every writer already shares —
// knowledge.PropertiesIndexPath resolves any spelling of a collection to the
// same path):
//
//   - Store.Reconcile holds the lock for a whole scan-and-reconcile run. The
//     function it runs receives a Store whose write methods SKIP the lock (a
//     reconcile must be able to write while it holds it, and this lock is
//     not reentrant).
//   - The public write methods (UpsertNote, UpsertNotes, DeleteNote,
//     RefreshNoteStat) hold the same lock for the duration of ONE write. A
//     direct single-path write therefore queues behind any reconcile in
//     flight, and can never commit inside one's scan-to-deletion window —
//     WITHOUT the direct-write call sites knowing anything about it. That is
//     the property that mattered most: pkg/knowledge's write path needed no
//     change to become a participant, because the coordination lives where
//     the writes already go.
//
// Two more rules (silent-failure review 2026-09-14, F7):
//
//   - THE SCHEMA REBUILD TAKES THE SAME LOCK. Opening a file written by a
//     different schema version drops every table and re-creates them
//     (sqlite.go's discardIncompatible). That is the most destructive write
//     this package makes, and it used to take no lock at all, so a rebuild on
//     one handle could drop the tables under another handle's reconcile. It
//     now holds the lock across drop, re-create and version stamp, and
//     re-reads the version once it holds the lock so a second handle that
//     queued behind a first rebuild does not drop what the first rebuilt.
//   - A WRITER WAITING FOR THE LOCK HONOURS ITS CONTEXT. A sync.Mutex waits
//     forever, so a request goroutine's UpsertNote queued behind a
//     whole-collection reconcile on a large vault could outlive the gateway's
//     30 s write timeout: the client saw a bare "socket hang up" and the
//     server never said why. The lock is therefore a one-slot channel, and
//     every wait that has a caller context selects on it; a cancelled or
//     expired context returns an error naming the lock and wrapping
//     ctx.Err(). Reconcile's own signature carries no context, so its wait
//     is unchanged — and once acquired, a reconcile holds the lock for its
//     whole run, as before.
//
// Reads (AllPaths, Candidates, Tasks, ...) take NO lock. They are single
// statements under WAL, which is already the documented serialization for
// multi-handle readers (sqlite.go's pool note).
//
// The lock is IN-PROCESS. Two gateway processes against one data directory
// are outside what it coordinates — the same posture as every other store
// guarantee on this file-store family (ADR-054).
// ---------------------------------------------------------------------------

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
)

// lockWaitProbe is nil in every production build. A test sets it to learn,
// deterministically, that a writer found the index lock held and is about to
// wait for it — the only way to prove "this waits" without a sleep. It is
// called with the index path, from the waiting goroutine, before the wait.
var lockWaitProbe func(indexPath string)

// indexWriteLock is the per-index reconcile lock. It is a one-slot channel
// rather than a sync.Mutex for exactly one reason: a waiter can stop waiting
// when its context ends (see the file header). Holding it means having put
// the one token into slot.
type indexWriteLock struct {
	slot chan struct{}
}

// lock takes the lock, waiting at most until ctx ends. A nil error means the
// caller holds the lock and must call unlock. When ctx ends first the lock is
// NOT held, and the error wraps ctx.Err() — errors.Is(err,
// context.DeadlineExceeded) / context.Canceled work on it.
//
// If the lock frees at the same instant ctx ends, either outcome may win;
// both are correct (a write that gets the lock with a dead context fails at
// its first statement instead).
func (l *indexWriteLock) lock(ctx context.Context, indexPath string) error {
	select {
	case l.slot <- struct{}{}:
		return nil
	default:
	}
	if probe := lockWaitProbe; probe != nil {
		probe(indexPath)
	}
	select {
	case l.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("propindex: gave up waiting for the write lock on the properties index %q "+
			"(a whole-collection reconcile or a schema rebuild was still holding it when the caller's context ended): %w",
			indexPath, ctx.Err())
	}
}

// unlock releases a held lock. Releasing a lock that is not held is a
// programming error and panics, as sync.Mutex.Unlock does — a silent no-op
// would let a double release hand the lock to two writers at once.
func (l *indexWriteLock) unlock() {
	select {
	case <-l.slot:
	default:
		panic("propindex: unlock of an index write lock that is not held")
	}
}

// reconcileLocks is one lock per properties-index file path. Entries are
// never removed: the key set is bounded by the number of collections a
// process has open (dozens), and a lock is a few dozen bytes.
var reconcileLocks = struct {
	sync.Mutex
	byPath map[string]*indexWriteLock
}{byPath: make(map[string]*indexWriteLock)}

// reconcileLockFor returns the lock that serializes writers to one index
// file, creating it on first use. The key is cleaned so two spellings of the
// same path (a trailing separator, a doubled slash) cannot take two locks for
// one file. Callers hand in the path propindex.Open was given, which every
// production writer derives from knowledge.PropertiesIndexPath — already
// resolved and stable — so in practice the Clean is a no-op; it is here so
// the lock's correctness does not REST on that being true.
func reconcileLockFor(indexPath string) *indexWriteLock {
	key := filepath.Clean(indexPath)
	reconcileLocks.Lock()
	defer reconcileLocks.Unlock()
	lk, ok := reconcileLocks.byPath[key]
	if !ok {
		lk = &indexWriteLock{slot: make(chan struct{}, 1)}
		reconcileLocks.byPath[key] = lk
	}
	return lk
}

// Reconcile runs fn as one whole-of-store reconcile: the index's write lock
// is held for the entire call, so no other reconcile, no schema rebuild and
// no single-path direct write can commit while fn runs.
//
// THE STORE fn RECEIVES IS THE ONE IT MUST WRITE THROUGH. Its write methods
// skip the reconcile lock (fn already holds it); writing through any OTHER
// handle to the same index — the receiver this method was called on, a store
// captured from elsewhere — self-deadlocks (or, with a context that ends,
// fails with the lock-wait error), because the public write methods take the
// same lock fn is running under.
//
// fn returning an error releases the lock normally and returns that error
// unchanged; a panic releases the lock via the deferred unlock and
// propagates.
func (ix *Index) Reconcile(fn func(Store) error) error {
	lk := reconcileLockFor(ix.path)
	// No caller context reaches this method (Store.Reconcile's signature), so
	// the wait is unbounded exactly as it was with a sync.Mutex.
	if err := lk.lock(context.Background(), ix.path); err != nil {
		return err
	}
	defer lk.unlock()
	return fn(reconcileScope{ix})
}

// reconcileScope is the Store a Reconcile hands its function: every read
// delegates unchanged through the embedded *Index, and the write methods
// overridden below route to the lock-free internals (upsertNotesDirect,
// deleteNoteDirect, refreshNoteStatDirect) instead of the public forms, so
// the reconcile can write while it holds the lock the public forms take.
type reconcileScope struct {
	*Index
}

func (s reconcileScope) UpsertNote(ctx context.Context, rows NoteRows) error {
	return s.upsertNotesDirect(ctx, []NoteRows{rows})
}

func (s reconcileScope) UpsertNotes(ctx context.Context, batch []NoteRows) error {
	return s.upsertNotesDirect(ctx, batch)
}

func (s reconcileScope) DeleteNote(ctx context.Context, path string) error {
	return s.deleteNoteDirect(ctx, path)
}

func (s reconcileScope) RefreshNoteStat(ctx context.Context, path string, size, mtimeNanos, ctimeNanos int64, hasCtime bool) (bool, error) {
	return s.refreshNoteStatDirect(ctx, path, size, mtimeNanos, ctimeNanos, hasCtime)
}
