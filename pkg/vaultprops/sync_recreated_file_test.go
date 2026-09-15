// Omnipus — CONFIRMED #25: a re-created file's birth time must be corrected,
// and CONFIRMED #28: the index file must never exist world-readable.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// ---------------------------------------------------------------------------
// #25 — THE RE-CREATED FILE
//
// RefreshNoteStat kept a stored birth time whatever the caller offered, and
// justified it by asserting that "a birth time that changed did so because the
// file was replaced, and that is a content change, which takes the UpsertNote
// path". That is false for the four ways a file is most often replaced with
// IDENTICAL bytes: `git checkout` of a deleted file, an rsync temp-file rename,
// a backup restore, and an iCloud re-download. Each produces a new inode with a
// new birth time and the same content — so the note takes the hash-equal skip,
// only the stat refresh runs, mtime and size are corrected, and `file.ctime`
// stays frozen at the FIRST file's creation date forever.
//
// FR-133's one-way rule is preserved by the fix and is a different rule: a
// platform that cannot READ a birth time must never CLEAR one another platform
// wrote. "The caller has nothing to offer" is what protects a stored value —
// not "the caller disagrees".
// ---------------------------------------------------------------------------

func TestSync_ARecreatedFileGetsItsBirthTimeCorrected(t *testing.T) {
	skipWithoutSQLite(t)

	const notePath = "Plants/Fern.md"

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		notePath:                            fernNote,
	})
	ctx := context.Background()
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	abs := filepath.Join(root, filepath.FromSlash(notePath))
	fi, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	_, platformHasBirthTime := fileutil.BirthTime(abs, fi)
	if !platformHasBirthTime {
		t.Skip("this platform records no birth time, so there is no ctime to correct (FR-133)")
	}

	store := openStoreForTest(t, home, root)
	first := fileMetaOf(t, store, notePath)
	if cerr := store.Close(); cerr != nil {
		t.Fatalf("close: %v", cerr)
	}
	if !first.HasBirthTime {
		t.Fatal("setup: the first sync recorded no birth time; nothing below would mean anything")
	}

	// Replace the file the way `git checkout` of a deleted note does: same
	// bytes, new inode, new birth time. The content hash does not move, so this
	// takes the stat-refresh path and not the re-index path.
	if rerr := os.Remove(abs); rerr != nil {
		t.Fatalf("remove: %v", rerr)
	}
	if werr := os.WriteFile(abs, []byte(fernNote), 0o600); werr != nil {
		t.Fatalf("recreate: %v", werr)
	}
	fi2, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat after recreate: %v", err)
	}
	wantBirth, ok := fileutil.BirthTime(abs, fi2)
	if !ok {
		t.Fatal("the recreated file reports no birth time on a platform that reported one a moment ago")
	}
	if wantBirth.UTC().Equal(first.BirthTime) {
		t.Skip("the filesystem gave the recreated file the same birth time; nothing to correct")
	}

	stats, err := Sync(ctx, home, root, SyncOptions{})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Unchanged != 1 {
		t.Fatalf("the recreated file has identical bytes, so it must take the content-unchanged "+
			"path — got %+v", stats)
	}

	store2 := openStoreForTest(t, home, root)
	defer func() { _ = store2.Close() }()
	after := fileMetaOf(t, store2, notePath)
	if !after.HasBirthTime {
		t.Fatal("the refresh cleared the birth time; FR-133's one-way rule was broken in the other direction")
	}
	if !after.BirthTime.Equal(wantBirth.UTC()) {
		t.Fatalf("file.ctime was not corrected for a re-created file: the index holds %s, "+
			"the file on disk was born at %s", after.BirthTime, wantBirth.UTC())
	}
}

// ---------------------------------------------------------------------------
// #28 — THE PERMISSIONS WINDOW
//
// The chmod to 0600 was the LAST statement of Sync, so every early return
// between opening the index and reaching it left the file at whatever the
// umask produced — 0644 on a default umask, and permanently so if the sync
// never gets to succeed. The containing directory is 0700, which bounds the
// exposure to mode-preserving backup tooling and to a host that widened the
// directory; that is a narrower hole than an open file, not a closed one.
// ---------------------------------------------------------------------------

func TestSync_TheIndexFileIsNeverWorldReadableEvenWhenTheSyncFails(t *testing.T) {
	skipWithoutSQLite(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes only")
	}

	home := syncHome(t)
	root := syncVault(t, map[string]string{"Notes/a.md": "# A\n"})
	ctx := context.Background()

	// A schema directory that cannot be read makes LoadSchemas fail, and
	// LoadSchemas runs AFTER the index file has been created by Open and
	// BEFORE the chmod at the end of Sync — the exact window under test.
	recordsDir := filepath.Join(root, ".omnipus-vault", "records")
	if err := os.MkdirAll(recordsDir, 0o700); err != nil {
		t.Fatalf("mkdir records: %v", err)
	}
	if err := os.Chmod(recordsDir, 0o000); err != nil {
		t.Fatalf("chmod records: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(recordsDir, 0o700) })

	if _, err := Sync(ctx, home, root, SyncOptions{}); err == nil {
		t.Skip("an unreadable schema directory did not fail the sync on this host; " +
			"the early-return window cannot be reached here")
	}

	idxPath, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		t.Fatalf("PropertiesIndexPath: %v", err)
	}
	fi, err := os.Stat(idxPath)
	if err != nil {
		t.Fatalf("the failing sync left no index file to check: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("the properties index is readable beyond its owner after a failed sync: mode %o", mode)
	}
}

func TestSync_TheIndexFileIsOwnerOnlyAfterASuccessfulSync(t *testing.T) {
	skipWithoutSQLite(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes only")
	}

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	if _, err := Sync(context.Background(), home, root, SyncOptions{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	idxPath, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		t.Fatalf("PropertiesIndexPath: %v", err)
	}
	// Every file SQLite creates alongside the database carries the same
	// content — the rollback journal and, in WAL mode, the write-ahead log —
	// so each is checked, not only the database itself.
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		fi, statErr := os.Stat(idxPath + suffix)
		if statErr != nil {
			continue // that companion file does not exist, which is fine
		}
		if mode := fi.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%q is readable beyond its owner: mode %o", filepath.Base(idxPath+suffix), mode)
		}
	}
}
