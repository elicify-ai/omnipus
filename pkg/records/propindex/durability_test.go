// Omnipus — CONFIRMED #30: the journal mode, the durability setting, and the
// corruption self-heal that `synchronous = OFF` was always predicated on.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `PRAGMA synchronous = OFF` was justified in one sentence: "a derived index
// has nothing to lose in a crash — it rebuilds". That sentence is only true if
// something DETECTS that the file needs rebuilding. Nothing did. A torn write
// left a file SQLite refuses to open, `openFindStore` swallowed the open error
// to Debug and returned nil, and every knowledge_find call from then on refused
// with "the properties index is not open" — permanently, until an operator who
// had no way to know found and deleted `properties.db` by hand.
//
// The tests below pin the two halves of the answer: a corrupt file is
// discarded and rebuilt rather than refused forever, and the database is opened
// in a mode where a crash costs at most the last commits instead of the file.
// ---------------------------------------------------------------------------

func tempIndex(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "properties.db")
}

func mustOpen(t *testing.T, path string) Store {
	t.Helper()
	store, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return store
}

// TestOpen_ACorruptIndexIsDiscardedAndRebuiltRatherThanRefusedForever is the
// missing half of `synchronous = OFF`.
func TestOpen_ACorruptIndexIsDiscardedAndRebuiltRatherThanRefusedForever(t *testing.T) {
	path := tempIndex(t)

	store := mustOpen(t, path)
	if err := store.UpsertNote(context.Background(), NoteRows{
		Path: "Notes/a.md", Kind: KindNote, SourceHash: "h1",
	}); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Corrupt the file the way a torn write does: keep SQLite's 16-byte magic
	// header so the file is still recognised as a database, and destroy the
	// page that follows it. A file that is not a database at all takes a
	// DIFFERENT SQLite error code, so overwriting the whole file would test the
	// easier case.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the index: %v", err)
	}
	if len(raw) < 4096 {
		t.Fatalf("the index is only %d bytes; the fixture assumes at least one full page", len(raw))
	}
	for i := 16; i < 4096; i++ {
		raw[i] = 0xFF
	}
	if werr := os.WriteFile(path, raw, 0o600); werr != nil {
		t.Fatalf("write the corrupted index: %v", werr)
	}

	reopened, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("a corrupt properties index must be discarded and rebuilt, not refused: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	if !reopened.NeedsFullIndex() {
		t.Error("after discarding a corrupt index the store must report NeedsFullIndex so the " +
			"caller re-derives it from the notes")
	}
	// And it is usable: the rebuild is real, not a handle that fails on first
	// write.
	if err := reopened.UpsertNote(context.Background(), NoteRows{
		Path: "Notes/a.md", Kind: KindNote, SourceHash: "h1",
	}); err != nil {
		t.Fatalf("the rebuilt index does not accept writes: %v", err)
	}
	var seen int
	if err := reopened.AllPaths(context.Background(), func(IndexedNote) error {
		seen++
		return nil
	}); err != nil {
		t.Fatalf("AllPaths on the rebuilt index: %v", err)
	}
	if seen != 1 {
		t.Fatalf("expected the rebuilt index to hold the one note just written, holds %d", seen)
	}
}

// TestOpen_UsesAWriteAheadLogAndKeepsCommitsDurable pins the two pragmas
// together, because neither is defensible on its own: WAL without
// `synchronous` at least NORMAL loses the file on a power cut, and
// `synchronous = OFF` on a rollback journal loses it on an ordinary crash.
func TestOpen_UsesAWriteAheadLogAndKeepsCommitsDurable(t *testing.T) {
	path := tempIndex(t)
	store := mustOpen(t, path)
	defer func() { _ = store.Close() }()

	ix, ok := store.(*Index)
	if !ok {
		t.Fatalf("Open returned %T, expected *Index", store)
	}

	var mode string
	if err := ix.queryRow(context.Background(), PhaseOpen, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("reading journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode is %q; the index must use a write-ahead log so a reader never "+
			"blocks the indexer on the one file they share", mode)
	}

	var sync int
	if err := ix.queryRow(context.Background(), PhaseOpen, "PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("reading synchronous: %v", err)
	}
	// 0 = OFF, 1 = NORMAL, 2 = FULL. NORMAL in WAL mode is the documented safe
	// setting: a crash can lose the most recent commits and cannot corrupt the
	// database.
	if sync < 1 {
		t.Errorf("synchronous is %d (OFF); a derived index may lose recent commits in a crash, "+
			"but it must not be left in a mode that can corrupt the file", sync)
	}
}

// TestOpen_EverySecondConnectionCarriesThePragmas is the false-green guard on
// the deadlock fix.
//
// The pragmas used to be executed once, against whichever connection the pool
// happened to hand out, and that was correct ONLY because the pool held exactly
// one connection. Raising the pool to break the nested-read deadlock silently
// makes every connection after the first unconfigured — busy_timeout back to 0,
// synchronous back to the default — and nothing observable from the outside
// would say so. So the pragmas moved into the DSN, which the driver applies to
// every connection it opens.
//
// The second connection is forced the way production forces it: a live
// candidate stream holds the first, and the pragma is read from INSIDE that
// stream's visit callback. Everything goes through sqlgate.go, so the AC-8.10
// recorder still sees every statement (TestSQLGate_OnlyOnePathToTheDriver reads
// this file too).
func TestOpen_EverySecondConnectionCarriesThePragmas(t *testing.T) {
	path := tempIndex(t)
	store := mustOpen(t, path)
	defer func() { _ = store.Close() }()

	ix, ok := store.(*Index)
	if !ok {
		t.Fatalf("Open returned %T, expected *Index", store)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, p := range []string{"Notes/a.md", "Notes/b.md"} {
		if err := store.UpsertNote(ctx, NoteRows{Path: p, Kind: KindNote, SourceHash: "h"}); err != nil {
			t.Fatalf("UpsertNote(%q): %v", p, err)
		}
	}

	var onFirst int
	if err := ix.queryRow(ctx, PhaseOpen, "PRAGMA busy_timeout").Scan(&onFirst); err != nil {
		t.Fatalf("reading busy_timeout with no stream open: %v", err)
	}
	if onFirst == 0 {
		t.Fatal("busy_timeout is 0 on the very first connection; the pragmas are not being applied at all")
	}

	// EVERY reading is kept, not the last one, and that is the difference
	// between this test working and this test lying. streamCandidates flushes
	// its FINAL record after the row set is exhausted — by which point
	// database/sql has already returned the connection to the pool — so the
	// last visit runs on the ORIGINAL connection and would quietly overwrite a
	// wrong reading taken from a second one.
	var readings []int
	done := make(chan error, 1)
	go func() {
		done <- store.Candidates(ctx, Selector{}, func(Candidate) (Verdict, error) {
			// For every record but the last this runs while the stream still
			// holds a connection, so the read below is served by a different
			// one.
			var got int
			if err := ix.queryRow(ctx, PhaseOpen, "PRAGMA busy_timeout").Scan(&got); err != nil {
				return Rejected, err
			}
			readings = append(readings, got)
			return Rejected, nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reading a pragma from inside a candidate stream: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("reading a pragma from inside a candidate stream never returned: the store " +
			"deadlocked on its own connection pool")
	}

	if len(readings) < 2 {
		t.Fatalf("expected a reading per candidate, got %d — the fixture is not forcing a nested "+
			"read while the stream holds its connection", len(readings))
	}
	for i, got := range readings {
		if got != onFirst {
			t.Fatalf("the pooled connection serving nested read %d of %d reports busy_timeout %d "+
				"where the first connection reports %d; the pragmas are not being applied per "+
				"connection", i+1, len(readings), got, onFirst)
		}
	}
}

// TestIndex_ANestedReadInsideAStreamCompletes is the propindex-level statement
// of CONFIRMED #8, independent of pkg/vaultprops' resolver: any read issued
// from inside a visit callback must complete rather than wait for the
// connection its own caller is holding.
func TestIndex_ANestedReadInsideAStreamCompletes(t *testing.T) {
	path := tempIndex(t)
	store := mustOpen(t, path)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	for _, p := range []string{"Notes/a.md", "Notes/b.md", "Notes/c.md"} {
		if err := store.UpsertNote(ctx, NoteRows{Path: p, Kind: KindNote, SourceHash: "h"}); err != nil {
			t.Fatalf("UpsertNote(%q): %v", p, err)
		}
	}

	// Its own deadline, so a regression fails in seconds instead of hanging the
	// package until the go test timeout kills every other test with it.
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		var nested int
		done <- store.Candidates(bounded, Selector{}, func(Candidate) (Verdict, error) {
			n, err := store.CountCandidates(bounded, Selector{Kind: KindNote})
			if err != nil {
				return Rejected, err
			}
			if n != 3 {
				t.Errorf("the nested count read %d notes, expected 3", n)
			}
			nested++
			return Accepted, nil
		})
		if nested != 3 {
			t.Errorf("expected a nested read per candidate, got %d", nested)
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a read issued from inside a candidate stream failed: %v", err)
		}
	case <-bounded.Done():
		t.Fatal("a read issued from inside a candidate stream never returned: the store " +
			"deadlocked on its own connection pool")
	}
}

// TestOpen_RefusesAnIndexPathItCannotExpressAsADSN is the loud half of moving
// the pragmas into the DSN.
//
// The driver splits a DSN at its first `?`, so a database path containing one
// would silently become a shorter path plus a garbage query string — a
// different file, opened with no pragmas at all. That is a wrong answer, so it
// is refused by name instead.
func TestOpen_RefusesAnIndexPathItCannotExpressAsADSN(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "wh?t", "properties.db")
	if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
		t.Skipf("this filesystem does not accept a directory name containing '?': %v", err)
	}
	_, err := Open(context.Background(), bad, Options{})
	if err == nil {
		t.Fatal("a path containing '?' must be refused, not silently opened as a different file")
	}
	if !strings.Contains(err.Error(), "?") {
		t.Errorf("the refusal must name the character that caused it, got: %v", err)
	}
}
