// Omnipus — spec FR-136 + FR-133: a note indexed before birth time existed
// gets its `file.ctime` filled on the next content-unchanged sync.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// TestFileMeta_CtimeReachesTheIndexFromTheWalk proves ctime arrives when a note
// is INDEXED. It does not exercise the other write site: refreshStatIfDrifted,
// the FR-136 path taken for a note whose content did not change.
//
// That gap was invisible to the suite and is the path that matters most in
// practice. Every vault indexed before FR-133 landed holds a row with NO birth
// time, and a re-index is not triggered by a fix to the indexer — the content
// hash is unchanged, so those notes take the refresh path forever. If the
// refresh does not carry the walk's ctime, `file.ctime` stays NULL on every
// already-synced vault no matter how correct the indexer became.
//
// It hid because the store fills one way and never clears: a test that indexes
// first (writing a good ctime) and re-syncs afterwards still reads a good ctime
// even when the refresh passes nothing at all. Mutating the refresh call to pass
// `0, false` left the whole package green. So the pre-fix state has to be built
// deliberately — delete the row, re-insert it the way the old indexer would
// have — and the absence asserted BEFORE the behaviour is measured.
// ---------------------------------------------------------------------------

func TestFileMeta_StatRefreshFillsCtimeForARowIndexedWithoutOne(t *testing.T) {
	skipWithoutSQLite(t)

	const notePath = "Plants/Fern.md"

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	syncOnce(t, home, root, SyncOptions{})

	// The oracle: what this platform reports for this file, asked independently
	// of everything under test. The assertions below are conditioned on it
	// rather than skipped by it, so a platform without birth times still checks
	// that the index agrees about the absence.
	absNote := filepath.Join(root, "Plants", "Fern.md")
	fi, err := os.Stat(absNote)
	if err != nil {
		t.Fatalf("stat the fixture note: %v", err)
	}
	wantBirth, platformHasBirthTime := fileutil.BirthTime(absNote, fi)

	// --- rebuild the row the way a pre-FR-133 indexer left it ---------------
	store := openStoreForTest(t, home, root)

	var kind, hash string
	var found bool
	if err := store.AllPaths(context.Background(), func(p, k, h string) error {
		if p == notePath {
			kind, hash, found = k, h, true
		}
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	if !found {
		t.Fatalf("the fixture note is not in the index after the first sync; nothing below would mean anything")
	}

	before := fileMetaOf(t, store, notePath)
	if !before.Known {
		t.Fatalf("the freshly indexed row carries no stat at all; this test cannot tell a fill from a no-op")
	}

	if err := store.DeleteNote(context.Background(), notePath); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	// Same path, same kind, same content hash — so the next sync sees the note
	// as unchanged and takes the refresh path — but with the stat the old
	// indexer carried: size and mtime only.
	if err := store.UpsertNote(context.Background(), propindex.NoteRows{
		Path:       notePath,
		Kind:       kind,
		SourceHash: hash,
		Size:       before.Size,
		MtimeNanos: before.ModTime.UnixNano(),
	}); err != nil {
		t.Fatalf("re-inserting the pre-FR-133 row: %v", err)
	}

	// THE PRECONDITION. If the row somehow still holds a birth time here, the
	// measurement below proves nothing whatsoever, so fail loudly instead.
	staged := fileMetaOf(t, store, notePath)
	if staged.HasBirthTime {
		t.Fatalf("the re-inserted row still holds a birth time (%s); the pre-FR-133 state was not reproduced "+
			"and a later non-NULL ctime would prove nothing", staged.BirthTime)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing the store before the second sync: %v", err)
	}

	// --- the behaviour: one content-unchanged sync --------------------------
	stats := syncOnce(t, home, root, SyncOptions{})
	if stats.Indexed != 0 {
		t.Fatalf("the second sync re-indexed %d note(s); it was supposed to take the content-unchanged "+
			"refresh path, so this test is no longer measuring the refresh", stats.Indexed)
	}
	if stats.StatRefreshed == 0 {
		t.Fatalf("the second sync refreshed no stat rows, so the row with the missing birth time was never " +
			"revisited; a vault indexed before FR-133 would keep file.ctime NULL forever")
	}

	after := openStoreForTest(t, home, root)
	defer func() { _ = after.Close() }()
	got := fileMetaOf(t, after, notePath)

	if got.HasBirthTime != platformHasBirthTime {
		if platformHasBirthTime {
			t.Fatalf("this platform reports a birth time of %s for the fixture note and the refresh left the "+
				"index without one: every note indexed before FR-133 keeps a NULL file.ctime, so every "+
				"imported Bases view sorting or filtering on creation date returns nothing useful", wantBirth)
		}
		t.Fatalf("the refresh put a birth time (%s) into the index on a platform that records none; FR-133 "+
			"requires an honest absence, not an invented value", got.BirthTime)
	}
	if platformHasBirthTime && !got.BirthTime.Equal(wantBirth.UTC()) {
		t.Errorf("after the refresh the index holds birth time %s; the file's is %s", got.BirthTime, wantBirth.UTC())
	}
}

// fileMetaOf reads one note's stat back through the ordinary candidate path —
// the same way a query sees it — and insists it appear exactly once.
func fileMetaOf(t *testing.T, store propindex.Store, path string) propindex.FileMeta {
	t.Helper()
	var got propindex.FileMeta
	var seen int
	if err := store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.Path == path {
			got, seen = c.File, seen+1
		}
		return propindex.Accepted, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if seen != 1 {
		t.Fatalf("%q appears %d times in the index, expected exactly once", path, seen)
	}
	return got
}
