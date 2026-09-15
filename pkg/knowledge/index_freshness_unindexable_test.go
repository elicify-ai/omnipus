// Finding 2c (production reachability): an unreadable/permanently-skipped file
// is on disk but never in the manifest, so a naive New count treats it as "not
// yet indexed" forever — making every search report the vault incomplete. The
// index excludes files the last completed SyncWith could not read from New, so
// the A2(d) coverage warning does not fire on an unreadable file alone. A file
// whose mod-time later moves is worth retrying and counts as New again.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//   -run '^TestFreshness_Unindexable' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFreshness_UnindexableExcludedFromNew(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "prose about azaleas")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix) // 5 files indexed, manifest holds 5

	// A sixth file appears on disk that the index could not read. Pin its
	// mod-time so the failure record and the scan agree exactly.
	rel := mkName(5)
	abs := b2WriteFile(t, root, rel, "unreadable")
	failAt := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(abs, failAt, failAt); err != nil {
		t.Fatal(err)
	}

	// Before the index knows it is unindexable, it is genuine drift: New=1.
	f, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if f.New != 1 {
		t.Fatalf("an unaccounted file on disk must read as New=1, got %d", f.New)
	}

	// The sweep records it as unindexable at that mod-time. Now it must NOT
	// count as New, and the collection must read Fresh — one unreadable file
	// does not make every search incomplete forever.
	ix.recordUnindexable(map[string]int64{filepath.ToSlash(rel): failAt.UnixNano()})
	f, err = ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if f.New != 0 {
		t.Fatalf("an unindexable file must be excluded from New, got New=%d", f.New)
	}
	if !f.Fresh {
		t.Fatalf("an index whose only divergence is an unindexable file must read Fresh, got Fresh=false (New=%d Changed=%d Removed=%d)", f.New, f.Changed, f.Removed)
	}

	// If the file changes on disk (mod-time moves), it is worth retrying and
	// counts as New again — the exclusion is not permanent amnesia.
	retryAt := time.Unix(1_700_000_000, 0)
	if chErr := os.Chtimes(abs, retryAt, retryAt); chErr != nil {
		t.Fatal(chErr)
	}
	f, err = ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if f.New != 1 {
		t.Fatalf("a changed (retry-worthy) formerly-unindexable file must count as New again, got New=%d", f.New)
	}
}

// TestFreshness_UnindexableViaSyncWith drives the real failure path: a file that
// cannot be read is recorded by SyncWith itself and then excluded from New. It
// relies on 0000 perms blocking a read, which does not hold for root, so it is
// skipped there rather than reported as a false pass.
func TestFreshness_UnindexableViaSyncWith(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root: 0000 file perms do not block reads, so the file would index successfully")
	}
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, mkName(0), "readable prose")
	bad := b2WriteFile(t, root, mkName(1), "secret prose")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o600) }) // let TempDir cleanup remove it

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if len(stats.Problems) == 0 {
		t.Fatalf("expected the unreadable file to be reported as a problem; got none (perms may not block reads here)")
	}

	f, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatalf("Freshness: %v", err)
	}
	if f.New != 0 {
		t.Fatalf("SyncWith must record the unreadable file as unindexable so it is excluded from New; got New=%d", f.New)
	}
	if !f.Fresh {
		t.Fatalf("a vault whose only divergence is an unreadable file must read Fresh after a full sync; got Fresh=false")
	}
	// FreshnessCached (the hot-path read) must agree — the case that fires the
	// A2(d) warning in production.
	fc := ix.FreshnessCached(context.Background())
	if fc.New != 0 {
		t.Fatalf("FreshnessCached must also exclude the unreadable file from New; got New=%d", fc.New)
	}
}
