// Omnipus — the per-index reconcile lock: the one coordinator between
// whole-store scan-and-reconcile runs and single-path direct writes.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS (Codex review 2026-09-14, finding 4)
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
//     reconcile must be able to write while it holds it, and Go's sync.Mutex
//     is not reentrant).
//   - The public write methods (UpsertNote, UpsertNotes, DeleteNote,
//     RefreshNoteStat) hold the same lock for the duration of ONE write. A
//     direct single-path write therefore queues behind any reconcile in
//     flight, and can never commit inside one's scan-to-deletion window —
//     WITHOUT the direct-write call sites knowing anything about it. That is
//     the property that mattered most: pkg/knowledge's write path needed no
//     change to become a participant, because the coordination lives where
//     the writes already go.
//
// Reads (AllPaths, Candidates, Tasks, ...) take NO lock. They are single
// statements under WAL, which is already the documented serialization for
// multi-handle readers (sqlite.go's pool note).
//
// The lock is IN-PROCESS. Two gateway processes against one data directory
// are outside what it coordinates — the same posture as every other store
// guarantee on this file-store family (ADR-054).
// ---------------------------------------------------------------------------

package propindex

import (
	"context"
	"path/filepath"
	"sync"
)

// reconcileLocks is one mutex per properties-index file path. Entries are
// never removed: the key set is bounded by the number of collections a
// process has open (dozens), and a mutex pointer is a few dozen bytes.
var reconcileLocks = struct {
	sync.Mutex
	byPath map[string]*sync.Mutex
}{byPath: make(map[string]*sync.Mutex)}

// reconcileMutexFor returns the mutex that serializes writers to one index
// file, creating it on first use. The key is cleaned so two spellings of the
// same path (a trailing separator, a doubled slash) cannot take two locks for
// one file. Callers hand in the path propindex.Open was given, which every
// production writer derives from knowledge.PropertiesIndexPath — already
// resolved and stable — so in practice the Clean is a no-op; it is here so
// the lock's correctness does not REST on that being true.
func reconcileMutexFor(indexPath string) *sync.Mutex {
	key := filepath.Clean(indexPath)
	reconcileLocks.Lock()
	defer reconcileLocks.Unlock()
	mu, ok := reconcileLocks.byPath[key]
	if !ok {
		mu = &sync.Mutex{}
		reconcileLocks.byPath[key] = mu
	}
	return mu
}

// Reconcile runs fn as one whole-of-store reconcile: the index's write lock
// is held for the entire call, so no other reconcile and no single-path
// direct write can commit while fn runs.
//
// THE STORE fn RECEIVES IS THE ONE IT MUST WRITE THROUGH. Its write methods
// skip the reconcile lock (fn already holds it); writing through any OTHER
// handle to the same index — the receiver this method was called on, a store
// captured from elsewhere — self-deadlocks, because the public write methods
// take the same lock fn is running under.
//
// fn returning an error releases the lock normally and returns that error
// unchanged; a panic releases the lock via the deferred Unlock and
// propagates.
func (ix *Index) Reconcile(fn func(Store) error) error {
	mu := reconcileMutexFor(ix.path)
	mu.Lock()
	defer mu.Unlock()
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
