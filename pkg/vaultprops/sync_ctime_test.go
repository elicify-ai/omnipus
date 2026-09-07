// Omnipus — spec FR-133 end to end: `file.ctime` reaches the index from the
// walk, on the platforms that have one.
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
// Every piece of the ctime path had its own passing test and the path as a
// whole did not work. propindex stored the column and round-tripped it;
// FR-133's birth-time readers were correct per platform and cross-compiled
// clean; the FR-136 refresh correctly declined to invent a value it had not
// been given. And `file.ctime` was NULL for every note in the product, because
// nothing in between ever put a birth time into a NoteRows — the walk carried
// size and mtime only.
//
// That is a whole-path property, so it is asserted on the whole path: index a
// real vault through the real Sync, reopen the real store, read what came out.
// ---------------------------------------------------------------------------

// TestFileMeta_CtimeReachesTheIndexFromTheWalk.
//
// The assertion is CONDITIONED ON THE PLATFORM, not skipped by it. fileutil is
// asked the same question the walk asks, about the same file; whatever it
// answers, the index must agree. So on macOS and the BSDs this asserts a real
// birth time arrived, on a Linux box with statx it asserts the same, and on a
// kernel or filesystem without one it asserts the honest absence FR-133
// requires — and in every case a disagreement between the two is a failure.
//
// The alternative — `if runtime.GOOS != "darwin" { t.Skip() }` — would report
// green on every platform where the path is broken.
func TestFileMeta_CtimeReachesTheIndexFromTheWalk(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	syncOnce(t, home, root, SyncOptions{})

	// What this platform can actually see, asked of the same file, through the
	// same function the walk uses. This is the ORACLE, and it is independent of
	// the storage path under test.
	notePath := filepath.Join(root, "Plants", "Fern.md")
	fi, err := os.Stat(notePath)
	if err != nil {
		t.Fatalf("stat the fixture note: %v", err)
	}
	wantBirth, platformHasBirthTime := fileutil.BirthTime(notePath, fi)

	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var got propindex.FileMeta
	var seen int
	if err := store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.Path == "Plants/Fern.md" {
			got, seen = c.File, seen+1
		}
		return propindex.Accepted, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if seen != 1 {
		t.Fatalf("the fixture note was not indexed exactly once (seen %d); nothing below would mean anything", seen)
	}

	// The instrument check: if mtime and size did not arrive either, a ctime
	// assertion is measuring a store that was never written to.
	if !got.Known {
		t.Fatal("the indexed note carries NO stat metadata at all; the walk's stat is not reaching UpsertNote")
	}

	if !platformHasBirthTime {
		if got.HasBirthTime {
			t.Errorf("the index holds a birth time (%s) for a file this platform records none for. "+
				"FR-133 forbids substituting the POSIX inode-change time, which is the only other "+
				"candidate value in the same structure", got.BirthTime)
		}
		t.Log("this platform records no birth time; FR-133's absence branch asserted end to end")
		return
	}

	if !got.HasBirthTime {
		t.Fatalf("this platform reports a birth time of %s for the fixture note, and the index holds "+
			"NONE. `file.ctime` is then absent for every note on every platform — not FR-133's honest "+
			"absence, but a dead property: every imported Bases view sorting or filtering on creation "+
			"date returns nothing useful, and nothing reports why.", wantBirth)
	}
	if !got.BirthTime.Equal(wantBirth.UTC()) {
		t.Errorf("the index holds birth time %s; the file's is %s", got.BirthTime, wantBirth.UTC())
	}
}
