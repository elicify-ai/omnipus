// A2(d) — the index-health/freshness verb: a caller must be able to tell a
// STALE index (built, then the collection changed) from a NEVER-BUILT one and
// from a genuinely FRESH one, so a partial result set ("1 hit where 68 files
// contain the term") is explainable as staleness rather than absence.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"testing"
)

func TestFreshness_NeverBuiltIsDistinctFromStale(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "some prose about azaleas")
	}
	ix := b2Open(t, home, root)

	// Never synced: no manifest, everything pending, NOT fresh, NOT built.
	f, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if f.Built {
		t.Errorf("Built = true on a never-synced index, want false")
	}
	if f.Fresh {
		t.Errorf("Fresh = true on a never-synced index, want false")
	}
	if f.Scanned != 5 || f.New != 5 || f.Pending != 5 {
		t.Errorf("never-built: got Scanned=%d New=%d Pending=%d, want 5/5/5", f.Scanned, f.New, f.Pending)
	}
}

func TestFreshness_FreshAfterSyncThenStaleAfterDiskChange(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "some prose about azaleas")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	// Immediately after a full sync the index reflects the collection exactly.
	f, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if !f.Built || !f.Fresh {
		t.Fatalf("after full sync: Built=%v Fresh=%v, want both true", f.Built, f.Fresh)
	}
	if f.Pending != 0 || f.Indexed != 5 || f.Scanned != 5 {
		t.Fatalf("after full sync: Indexed=%d Scanned=%d Pending=%d, want 5/5/0", f.Indexed, f.Scanned, f.Pending)
	}

	// Add three notes on disk WITHOUT re-syncing: the index is now stale. This
	// is the exact shape behind "1 hit where N files contain the term".
	for i := 5; i < 8; i++ {
		b2WriteFile(t, root, mkName(i), "more prose about azaleas")
	}
	f2, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness (after drift): %v", err)
	}
	if !f2.Built {
		t.Errorf("Built = false after a real sync, want true (this is stale, not never-built)")
	}
	if f2.Fresh {
		t.Errorf("Fresh = true with 3 unindexed files on disk, want false")
	}
	if f2.New != 3 || f2.Pending != 3 {
		t.Errorf("after drift: got New=%d Pending=%d, want 3/3", f2.New, f2.Pending)
	}
	if f2.Scanned != 8 || f2.Indexed != 5 {
		t.Errorf("after drift: got Scanned=%d Indexed=%d, want 8/5", f2.Scanned, f2.Indexed)
	}
}

func mkName(i int) string {
	return "notes/n-" + string(rune('a'+i)) + ".md"
}
