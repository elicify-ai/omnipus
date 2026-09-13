// Omnipus — the deterministic interleaving test for the reconcile lock
// (Codex review 2026-09-14, finding 4).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// The finding's ordering, reproduced exactly:
//
//	recovery A scans the collection; a new note appears and its row is
//	committed by a direct write (the instant index refresh after a
//	knowledge_edit write); A then reads the store's paths, sees the new row,
//	and deletes it because its earlier scan did not contain that path.
//
// syncAfterScanProbe stops Sync at the exact point between its scan and its
// AllPaths read, while it HOLDS the reconcile lock, so the interleaving is
// choreographed by channels rather than raced against a scheduler.

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// directWriteRow performs the direct single-path write the way pkg/knowledge's
// author.go does it: its OWN propindex.Open handle on the same index file, one
// public UpsertNote, close. That call path — not a test-only seam — is what
// the statement-level reconcile lock in pkg/records/propindex/reconcile.go
// turns into a participant.
// directWriteOutcome carries the direct writer's result: err is the write's
// own error (nil on success) and finished is CLOSED when the writer is done.
// Closing rather than sending lets BOTH the probe's discriminator and the
// test's final wait observe completion independently — a single buffered send
// can only be consumed once, and the discriminator consuming it turned a
// mutated-world failure into a ten-minute hang instead of an immediate one.
type directWriteOutcome struct {
	err      error
	finished chan struct{}
}

func directWriteRow(t *testing.T, home, root, relPath string, out *directWriteOutcome, started chan<- struct{}) {
	t.Helper()
	ctx := context.Background()
	idxPath, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		out.err = err
		close(out.finished)
		return
	}
	store, err := propindex.Open(ctx, idxPath, propindex.Options{})
	if err != nil {
		out.err = err
		close(out.finished)
		return
	}
	close(started)
	if err := store.UpsertNote(ctx, propindex.NoteRows{Path: relPath, Kind: propindex.KindNote}); err != nil {
		_ = store.Close()
		out.err = err
		close(out.finished)
		return
	}
	if err := store.Close(); err != nil {
		out.err = err
		close(out.finished)
		return
	}
	close(out.finished)
}

// storeHolds reports whether the properties index currently holds a row for
// relPath.
func storeHolds(t *testing.T, home, root, relPath string) bool {
	t.Helper()
	idxPath, err := knowledge.PropertiesIndexPath(home, root)
	if err != nil {
		t.Fatalf("PropertiesIndexPath: %v", err)
	}
	store, err := propindex.Open(context.Background(), idxPath, propindex.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	found := false
	if err := store.AllPaths(context.Background(), func(n propindex.IndexedNote) error {
		if n.Path == relPath {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	return found
}

// TestSync_DirectWriteInsideTheScanWindowIsNotLost — the finding's exact
// interleaving. A direct write that starts INSIDE a reconcile's
// scan-to-deletion window must queue behind the reconcile, commit after it,
// and survive; before the reconcile lock it committed mid-window and the
// deletion pass deleted it.
func TestSync_DirectWriteInsideTheScanWindowIsNotLost(t *testing.T) {
	ctx := context.Background()
	root := syncVault(t, map[string]string{"fern.md": fernNote})
	home := syncHome(t)

	// A healthy prior sync: the store holds fern.md and nothing else.
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	aboutToWrite := make(chan struct{})
	writer := &directWriteOutcome{finished: make(chan struct{})}

	// The probe runs ON Sync's goroutine, INSIDE the reconcile lock, after the
	// scan (which saw only fern.md) and before the AllPaths read. Sync itself
	// runs on THIS goroutine — the only concurrency under test is the direct
	// writer's, which is the concurrency the finding is about.
	prev := syncAfterScanProbe
	syncAfterScanProbe = func() {
		// The note appears on disk only now, after the scan has passed.
		if err := os.WriteFile(filepath.Join(root, "orchid.md"), []byte(orchidNote), 0o600); err != nil {
			t.Errorf("writing orchid.md: %v", err)
			return
		}
		// The direct write starts from its own handle, exactly as the
		// post-knowledge_edit instant refresh does. It signals the moment
		// before it calls UpsertNote, so the window below is measured from
		// "the write is in flight", not from "the goroutine was created".
		go directWriteRow(t, home, root, "orchid.md", writer, aboutToWrite)
		<-aboutToWrite

		// THE DISCRIMINATOR. While the reconcile lock is held there is no
		// amount of time after which an uncoordinated write could have
		// committed — it is blocked. If it DOES commit in this window, the
		// lock did not serialize it, and the deletion pass below will delete
		// the row: that is the finding, caught in the act.
		select {
		case <-writer.finished:
			t.Errorf("the direct write COMMITTED inside the reconcile's scan-to-deletion window (err=%v) — writes are not serialized with reconciles", writer.err)
		case <-time.After(400 * time.Millisecond):
		}
	}

	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	// The probe has served its purpose; restore before the follow-up reconcile.
	syncAfterScanProbe = prev
	defer func() { syncAfterScanProbe = prev }()

	<-writer.finished
	if writer.err != nil {
		t.Fatalf("direct write: %v", writer.err)
	}

	// The row the direct write committed must still be there: the deletion
	// pass ran while the write was still queued, saw no row for orchid.md,
	// and deleted nothing; the write then committed against a reconcile-free
	// store.
	if !storeHolds(t, home, root, "orchid.md") {
		t.Fatal("the direct write's row was deleted by the concurrent reconcile's deletion pass — the exact loss Codex finding 4 describes")
	}
	// And the NEXT reconcile — which now sees orchid.md on disk — keeps it.
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if !storeHolds(t, home, root, "orchid.md") {
		t.Fatal("a follow-up reconcile dropped the direct write's row")
	}
}

// TestOpenFindStore_RevalidatesCoverageAgainstCurrentDisk — the second half of
// finding 4: after a recovery rebuild, coverage is judged against a FRESH scan
// of the collection, not the sync's own (possibly stale) scan count. A file
// that lands on disk after the recovery's scan must make the store be reported
// out of step, not accepted as complete.
func TestOpenFindStore_RevalidatesCoverageAgainstCurrentDisk(t *testing.T) {
	ctx := context.Background()
	root := syncVault(t, map[string]string{"fern.md": fernNote})
	home := syncHome(t)

	// No index exists, so openFindStore's first usability check fails and the
	// RECOVERY path runs: Sync, then re-check.
	prev := syncAfterScanProbe
	syncAfterScanProbe = func() {
		// A file lands on disk AFTER the recovery's scan counted the
		// collection. Nothing indexes it — it is pure disk drift, the exact
		// state a stale `stats.Scanned` used to paper over.
		if err := os.WriteFile(filepath.Join(root, "orchid.md"), []byte(orchidNote), 0o600); err != nil {
			t.Errorf("writing orchid.md: %v", err)
		}
	}
	defer func() { syncAfterScanProbe = prev }()

	store, closer, reason := openFindStoreReturns(t, ctx, home, root)
	if store != nil {
		closer()
		t.Fatalf("the store was accepted after a rebuild that covered 1 file while 2 are on disk (reason=%q)", reason)
	}
	if !strings.Contains(reason, "of the 2 files on disk") {
		t.Fatalf("the refusal must revalidate against current disk state and say so plainly; got %q", reason)
	}
}

// openFindStoreReturns adapts openFindStore's current signature to the tests
// above, so a signature change in one place does not touch three call sites.
func openFindStoreReturns(t *testing.T, ctx context.Context, home, root string) (propindex.Store, func() error, string) {
	t.Helper()
	store, closer, reason, _ := openFindStore(ctx, home, root)
	if closer == nil {
		closer = func() error { return nil }
	}
	return store, closer, reason
}

// orchidNote is a second valid plant note, distinct from fernNote.
const orchidNote = "---\n" +
	"type: plant\n" +
	"id: PL-0002\n" +
	"condition: blooming\n" +
	"height_cm: 30\n" +
	"---\n" +
	"# Orchid\n"
