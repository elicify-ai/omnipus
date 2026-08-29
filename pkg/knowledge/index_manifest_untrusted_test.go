// Tests for F5 (code review A): an unreadable, version-mismatched, or
// wrong-root manifest must not be reconciled against a live, populated index
// with an empty in-memory manifest.
//
// # The bug, restated as an oracle
//
// LoadManifest returns an EMPTY manifest plus a non-nil error on corruption or
// a version mismatch (manifest.go's own doc comment on manifestVersion
// invites exactly the version bump this file reproduces). Before this fix,
// SyncWith logged that error and proceeded anyway: present files re-index by
// ID, so a shallow look at the collection afterward finds it "healthy" — but
// the deletion loop at the end of SyncWith only removes documents for paths
// STILL NAMED in manifest.Entries, and a fresh empty manifest names nothing.
// A note deleted from disk before the manifest went bad therefore keeps
// answering search queries with its full body forever. That is not staleness,
// it is a confidentiality failure: the operator sees the file gone and the
// index disagrees, silently, indefinitely.
//
// The fix makes an unusable manifest the same class of event openOrRebuild
// already has two guards for (G1 format staleness, G2 mapping drift): the
// index is untrusted, so it is purged and rebuilt from the collection —
// mirroring createFreshIndex's own converse rule (a manifest that outlives
// its index is removed with the index, so Sync cannot skip everything as
// "unchanged" against nothing). See SyncWith's "F5 / G3" comment for the full
// reasoning this test exists to hold accountable.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestSyncWith_UnusableManifestPurgesTheStaleIndexRatherThanTrustingIt is F5's
// exact reproduction: a note deleted from disk BEFORE the manifest becomes
// unusable must not remain searchable after the reconcile that follows.
func TestSyncWith_UnusableManifestPurgesTheStaleIndexRatherThanTrustingIt(t *testing.T) {
	home, root := w0Collection(t)
	dir, _, hits := w0BuildAndClose(t, home, root)
	if len(hits) != 2 {
		t.Fatalf("fixture sanity: want 2 quicksilver hits from the first build, got %v", hits)
	}

	// The deletion happens BEFORE the manifest goes bad — exactly the
	// reviewer's scenario ("Confidential/Terminated Contract.md, deleted last
	// week"). The manifest on disk still names it; that is the state the next
	// reconcile has to get right.
	if err := os.Remove(filepath.Join(root, "alpha.md")); err != nil {
		t.Fatal(err)
	}

	bumpManifestVersion(t, filepath.Join(dir, ManifestFileName))

	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })

	// Fixture sanity: the deleted file's OLD documents are still physically
	// in the index at this point (OpenIndex does not Sync) — otherwise there
	// is nothing here for the reconcile to fail to clean up.
	preCount, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if preCount == 0 {
		t.Fatalf("fixture sanity: index holds no documents before the reconcile; nothing would be at risk of leaking")
	}

	stats, syncErr := ix.SyncWith(context.Background(), SyncOptions{})
	if syncErr != nil {
		t.Fatalf("SyncWith after an unusable manifest returned an error rather than repairing: %v", syncErr)
	}
	if !stats.ManifestRebuilt {
		t.Errorf("stats.ManifestRebuilt = false, want true — an unusable manifest must be reported, not silently absorbed")
	}

	// THE DECISIVE ASSERTION. Before the fix this is exactly where the bug
	// shows up: alpha.md is gone from disk, the UI would show it gone, and
	// the index keeps answering for it anyway.
	found, sErr := ix.Search("quicksilver", 10)
	if sErr != nil {
		t.Fatal(sErr)
	}
	gotPaths := b2HitPaths(found)
	sort.Strings(gotPaths)
	for _, h := range found {
		if h.Path == "alpha.md" {
			t.Errorf(
				"alpha.md was deleted from disk before the manifest went bad but is still searchable "+
					"after the reconcile that followed — hits=%v (F5's confidentiality failure)", gotPaths)
		}
	}
	if len(gotPaths) != 1 || gotPaths[0] != "bravo.md" {
		t.Errorf("hits after the reconcile = %v, want exactly [bravo.md]", gotPaths)
	}

	// The rebuild must not just purge — it must also faithfully reconstruct
	// what is genuinely still on disk. A rebuild that purged everything and
	// then indexed nothing would also make alpha.md unsearchable, for the
	// wrong reason.
	realRoot, resolveErr := ResolveCollectionRoot(root)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	m2, mErr := LoadManifest(ix.ManifestPath(), realRoot)
	if mErr != nil {
		t.Fatalf("manifest written by the rebuild is itself unusable: %v", mErr)
	}
	if _, ok := m2.Get("bravo.md"); !ok {
		t.Errorf("manifest after the rebuild has no record of bravo.md, which is still on disk")
	}
	if _, ok := m2.Get("alpha.md"); ok {
		t.Errorf("manifest after the rebuild still records alpha.md, which was deleted before the reconcile ran")
	}
	if _, ok := m2.Get("sub/charlie.md"); !ok {
		t.Errorf("manifest after the rebuild has no record of sub/charlie.md, which is still on disk")
	}
}

// TestSyncWith_UnusableManifestStillSurvivesOnATrueFirstSync guards against a
// regression at the other end of the same code path: a collection that has
// never been synced (no manifest file at all — the ordinary "first run" case
// LoadManifest's own doc comment describes) must NOT be treated as an F5
// rebuild. LoadManifest returns (empty manifest, nil error) for a missing
// file specifically so a first-ever sync is not confused with "the manifest
// used to exist and now cannot be trusted".
func TestSyncWith_UnusableManifestStillSurvivesOnATrueFirstSync(t *testing.T) {
	home, root := w0Collection(t)

	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })

	stats, syncErr := ix.SyncWith(context.Background(), SyncOptions{})
	if syncErr != nil {
		t.Fatalf("SyncWith: %v", syncErr)
	}
	if stats.ManifestRebuilt {
		t.Errorf("stats.ManifestRebuilt = true on a genuine first sync (no manifest ever existed); " +
			"LoadManifest reports a missing manifest as (empty, nil), not an error, and this must not " +
			"be mistaken for an untrusted index")
	}
	if stats.Indexed != 3 {
		t.Errorf("Indexed = %d on a first sync of the 3-file fixture, want 3", stats.Indexed)
	}
}

// bumpManifestVersion rewrites the version field of an on-disk manifest to an
// unrecognised value, reproducing exactly the scenario manifestVersion's own
// doc comment names: "A manifest with an unrecognised version is discarded
// (treated as absent)".
func bumpManifestVersion(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest %s: %v", path, err)
	}
	v, _ := m["version"].(float64)
	m["version"] = v + 1
	bumped, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-encode manifest %s: %v", path, err)
	}
	if err := os.WriteFile(path, bumped, 0o600); err != nil {
		t.Fatalf("write manifest %s: %v", path, err)
	}
}
