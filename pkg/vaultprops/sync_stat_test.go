// Omnipus — spec test 99 (FR-136): stat metadata survives the hash-equal sync skip.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHY ITS ASSERTIONS ARE SHAPED THE WAY THEY ARE
//
// FR-136 was found by a reviewer, not by a test suite, and the reason is worth
// stating before the first assertion: the defect it fixes produces NO error, no
// refusal and no gap. `sort by file.mtime desc` just returns a plausible,
// stable, WRONG order. Any test written carelessly enough to be satisfied by
// "the mtime ends up correct" would have been GREEN over the bug, because a
// full re-index also ends with the correct mtime — and a full re-index on every
// `touch` is the other bug.
//
// So each test here carries three assertions that a naive one would not:
//
//   1. AN INSTRUMENT CHECK BEFORE THE ACT. Every test syncs twice before
//      touching anything and asserts StatRefreshed == 0 on the second run. That
//      proves the fixture actually reaches the hash-equal skip, and that the
//      counter is capable of reporting zero. Without it, a StatRefreshed that
//      was simply always 1 — or a fixture that never produced a skip at all —
//      would pass.
//
//   2. AN IDEMPOTENCE ORACLE FOR THE VALUE WRITTEN. Store.RefreshNoteStat
//      reports true only when the row actually differed from the numbers handed
//      to it. So syncing a THIRD time and requiring StatRefreshed == 0 proves
//      the stored stat is now byte-for-byte the walk's stat: if the UPDATE had
//      written a zero, a rounded time, or the old value, the next run would
//      report drift again. This is how the value is verified through the
//      package's own API, without reaching around it into SQLite.
//
//   3. THE STATEMENTS, NOT THE OUTCOME. A metadata-only refresh is a claim
//      about which SQL ran. The recorder is the only instrument that can tell
//      it from a re-index, because both end with the right answer.
// ---------------------------------------------------------------------------

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// statDrift is how far a test moves a file's timestamp.
//
// Two hours, not two nanoseconds: filesystems disagree about timestamp
// granularity (HFS+ stores whole seconds, some network mounts round to two),
// and a test that depended on nanosecond retention would fail for a reason that
// has nothing to do with FR-136.
const statDrift = 2 * time.Hour

// touchFile moves a file's modification time WITHOUT changing one byte of its
// content — `git checkout`, rsync, `touch` and an iCloud resync in miniature.
func touchFile(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	want := fi.ModTime().Add(statDrift)
	if chErr := os.Chtimes(path, want, want); chErr != nil {
		t.Fatalf("chtimes %s: %v", path, chErr)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("re-stat %s: %v", path, err)
	}
	// The instrument check for the fixture itself: if the filesystem silently
	// refused the new time, every assertion below would be meaningless.
	if after.ModTime().Equal(fi.ModTime()) {
		t.Fatalf("the filesystem did not accept the new mtime for %s; the fixture cannot test what it claims to", path)
	}
	return after.ModTime()
}

// syncOnce runs one Sync and fails on error, returning the stats.
func syncOnce(t *testing.T, home, root string, opts SyncOptions) SyncStats {
	t.Helper()
	stats, err := Sync(context.Background(), home, root, opts)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return stats
}

// settle runs Sync twice and asserts the SECOND run was a pure no-op: nothing
// indexed, nothing removed, nothing refreshed, and the entry actually reached
// the unchanged path.
//
// This is assertion 1 from the header. It is the difference between a test that
// proves something and a test that cannot fail.
func settle(t *testing.T, home, root string, wantUnchanged int) {
	t.Helper()
	syncOnce(t, home, root, SyncOptions{})
	second := syncOnce(t, home, root, SyncOptions{})
	if second.Unchanged != wantUnchanged {
		t.Fatalf("the fixture never reached the content-unchanged skip: Unchanged = %d, want %d (%+v)",
			second.Unchanged, wantUnchanged, second)
	}
	if second.Indexed != 0 {
		t.Fatalf("a settled sync re-indexed %d path(s); the fixture is not settled (%+v)", second.Indexed, second)
	}
	if second.StatRefreshed != 0 {
		t.Fatalf("a settled sync reported %d stat refresh(es) with nothing touched — either the initial index "+
			"never stamped the stat, or StatRefreshed cannot report zero and every assertion below is vacuous (%+v)",
			second.StatRefreshed, second)
	}
}

// --- test 99, first half: a note ------------------------------------------

// TestFileMeta_StatRefreshSurvivesHashEqualSkip is spec test 99's note half and
// FR-136's headline case.
//
// A note is touched and NOT edited. Its content hash is therefore identical, so
// sync takes the skip branch — the branch that, before this fix, was
// `stats.Unchanged++; continue` and nothing else. The index's mtime froze there
// at the last CONTENT change, and every view sorting on `file.mtime` quietly
// returned a wrong order.
func TestFileMeta_StatRefreshSurvivesHashEqualSkip(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
	})
	notePath := filepath.Join(root, "Plants", "Fern.md")

	settle(t, home, root, 1)

	before, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	touchFile(t, notePath)
	after, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("re-read note: %v", err)
	}
	// The whole premise: the BYTES did not move. If they had, this would be an
	// ordinary re-index test and would prove nothing about FR-136.
	if string(before) != string(after) {
		t.Fatalf("touchFile changed the note's content; this test no longer exercises the hash-equal skip")
	}

	rec := propindex.NewRecorder()
	drifted := syncOnce(t, home, root, SyncOptions{Recorder: rec})

	if drifted.StatRefreshed != 1 {
		t.Errorf("a touched note did not have its stat refreshed: StatRefreshed = %d, want 1 (%+v)",
			drifted.StatRefreshed, drifted)
	}
	if drifted.Indexed != 0 {
		t.Errorf("a touched note was RE-INDEXED rather than stat-refreshed: Indexed = %d, want 0 (%+v)",
			drifted.Indexed, drifted)
	}
	if drifted.Unchanged != 1 {
		t.Errorf("a touched note was not reported as content-unchanged: Unchanged = %d, want 1 (%+v)",
			drifted.Unchanged, drifted)
	}

	assertMetadataOnlyWrite(t, rec)

	// Assertion 2 from the header — the idempotence oracle. RefreshNoteStat
	// reports true only on a real difference, so a run that now reports zero
	// proves the row holds exactly the walk's numbers rather than a zero, a
	// rounded value or the old one.
	steady := syncOnce(t, home, root, SyncOptions{})
	if steady.StatRefreshed != 0 {
		t.Errorf("the refreshed stat did not match the file: a following sync still reported %d refresh(es), "+
			"so the UPDATE wrote something other than the walk's mtime (%+v)", steady.StatRefreshed, steady)
	}
}

// --- test 99, second half: an attachment ----------------------------------

// TestFileMeta_AttachmentReplacedInPlaceRefreshesItsStat is the half the
// original design got wrong for a defensible reason.
//
// FR-039a forbids opening an attachment, so it carries no hash, so sync's
// attachment skip was UNCONDITIONAL: "there is nothing about it that COULD
// change without its path changing". That was true of a row holding {Path,
// Kind}. The moment the row carried size and mtime it became false — a picture
// replaced in place, same filename, would report its FIRST-EVER size forever,
// with no error and nothing to notice.
//
// The fix takes the WALK's stat, which knowledge.Scan already performed. This
// test proves that literally: the attachment is left unreadable (mode 0000)
// across the sync, so any code path that opened it would fail loudly instead of
// refreshing.
func TestFileMeta_AttachmentReplacedInPlaceRefreshesItsStat(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Photos/fern.png": "the first picture",
	})
	pngPath := filepath.Join(root, "Photos", "fern.png")

	settle(t, home, root, 1)

	// Replaced in place: same path, different bytes, different length.
	replacement := "the second picture, which is materially longer than the first one"
	if err := os.WriteFile(pngPath, []byte(replacement), 0o600); err != nil {
		t.Fatalf("replace attachment: %v", err)
	}
	touchFile(t, pngPath)

	if runtime.GOOS != "windows" {
		// FR-039a, proved rather than asserted: sync must reach the new size
		// through stat alone. A file it cannot open must not stop it.
		if err := os.Chmod(pngPath, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(pngPath, 0o600) })
	}

	rec := propindex.NewRecorder()
	drifted := syncOnce(t, home, root, SyncOptions{Recorder: rec})

	if drifted.StatRefreshed != 1 {
		t.Errorf("an attachment replaced in place kept its first-ever stat: StatRefreshed = %d, want 1 (%+v)",
			drifted.StatRefreshed, drifted)
	}
	if drifted.Indexed != 0 {
		t.Errorf("the attachment was re-indexed rather than stat-refreshed: Indexed = %d, want 0 (%+v)",
			drifted.Indexed, drifted)
	}
	for _, p := range drifted.Problems {
		if p.RelPath == "Photos/fern.png" {
			t.Errorf("sync reported a problem for the attachment, which means something tried to READ it — "+
				"FR-039a says stat only: %+v", p)
		}
	}

	assertMetadataOnlyWrite(t, rec)

	steady := syncOnce(t, home, root, SyncOptions{})
	if steady.StatRefreshed != 0 {
		t.Errorf("the refreshed attachment stat did not match the file: a following sync still reported %d "+
			"refresh(es) (%+v)", steady.StatRefreshed, steady)
	}
}

// --- the statements, not the outcome --------------------------------------

// assertMetadataOnlyWrite is assertion 3 from the header.
//
// "No re-parse, no child-row rewrite" is a claim about which SQL executed, and
// it is NOT observable from the resulting data: a full UpsertNote leaves the
// index holding exactly the same correct mtime. The recorder is the only
// instrument that separates them, which is why SyncOptions carries one.
func assertMetadataOnlyWrite(t *testing.T, rec *propindex.Recorder) {
	t.Helper()
	writes := rec.InPhase(propindex.PhaseWrite)
	if len(writes) == 0 {
		t.Fatalf("the stat refresh executed no write statements at all; nothing was recorded")
	}
	for _, sql := range writes {
		norm := strings.ToLower(strings.Join(strings.Fields(sql), " "))
		for _, child := range []string{"note_props", "note_relations", "note_tasks"} {
			if strings.Contains(norm, child) {
				t.Errorf("a metadata-only stat refresh touched %s — that is a re-index, which is the bug "+
					"FR-136 exists to avoid, not the fix: %s", child, sql)
			}
		}
		if strings.Contains(norm, "source_hash") {
			t.Errorf("a stat refresh wrote source_hash; no content moved, so D16.5's write ordering has "+
				"nothing to order: %s", sql)
		}
		if strings.Contains(norm, "insert into notes") {
			t.Errorf("a stat refresh re-inserted the note row rather than updating its stat columns: %s", sql)
		}
	}
}
