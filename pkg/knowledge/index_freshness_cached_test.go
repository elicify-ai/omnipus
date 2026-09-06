// Finding 1 (perf regression on the search hot path): every non-zero words
// query called Index.Freshness, which runs a full directory Scan + LoadManifest
// under the index write-lock. SearchFiltered/searchRaw were previously
// lock-free; this put a stat-walk of the whole vault, and a wait on the
// reconcile lock, on every successful search. FreshnessCached is the fix: a
// lock-free, TTL-throttled read that serves recent counts without walking.
//
// These tests instrument the freshnessScan seam to PROVE the hot-path read no
// longer triggers a Scan, and that it never blocks behind ix.mu.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//   -run '^TestFreshnessCached' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingScan swaps freshnessScan for one that counts calls, restoring it on
// cleanup. It returns a pointer to the live counter.
func countingScan(t *testing.T) *int32 {
	t.Helper()
	var n int32
	orig := freshnessScan
	freshnessScan = func(root string) (*ScanResult, error) {
		atomic.AddInt32(&n, 1)
		return orig(root)
	}
	t.Cleanup(func() { freshnessScan = orig })
	return &n
}

func TestFreshnessCached_WarmReadDoesNotScan(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "some prose about azaleas")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix) // warms the cache from the reconcile, no extra walk

	// Add three notes on disk. A warm FreshnessCached must NOT notice them,
	// because noticing them is exactly the filesystem walk the hot path must not
	// pay on every query.
	for i := 5; i < 8; i++ {
		b2WriteFile(t, root, mkName(i), "more prose about azaleas")
	}

	scans := countingScan(t)
	// A generous TTL so every read in this test is served from the cache.
	restore := setFreshnessTTL(t, time.Hour)
	defer restore()

	for q := 0; q < 20; q++ {
		f := ix.FreshnessCached(context.Background())
		if f.Scanned != 5 {
			t.Fatalf("query %d: FreshnessCached walked and saw %d files; a warm read must serve the cached count (5)", q, f.Scanned)
		}
	}
	if got := atomic.LoadInt32(scans); got != 0 {
		t.Fatalf("the search hot path triggered %d filesystem walks across 20 warm queries; want 0 (Finding 1)", got)
	}
}

func TestFreshnessCached_RefreshesAfterTTL(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "some prose about azaleas")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	for i := 5; i < 8; i++ {
		b2WriteFile(t, root, mkName(i), "more prose about azaleas")
	}

	scans := countingScan(t)
	restore := setFreshnessTTL(t, 0) // every read is past the (zero) TTL
	defer restore()

	f := ix.FreshnessCached(context.Background())
	if f.Scanned != 8 {
		t.Fatalf("an expired FreshnessCached must walk and see all 8 files, saw %d", f.Scanned)
	}
	if f.New != 3 || f.Fresh {
		t.Fatalf("an expired FreshnessCached over a drifted index must report New=3, Fresh=false; got New=%d Fresh=%v", f.New, f.Fresh)
	}
	if got := atomic.LoadInt32(scans); got != 1 {
		t.Fatalf("expired FreshnessCached scanned %d times, want exactly 1", got)
	}
}

func TestFreshness_AlwaysScans(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, mkName(i), "prose")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	scans := countingScan(t)
	restore := setFreshnessTTL(t, time.Hour) // TTL must not spare the always-fresh verb
	defer restore()

	for i := 0; i < 3; i++ {
		if _, err := ix.Freshness(context.Background()); err != nil {
			t.Fatalf("Freshness: %v", err)
		}
	}
	if got := atomic.LoadInt32(scans); got != 3 {
		t.Fatalf("Freshness must walk on every call (it is the accurate, un-throttled verb); scanned %d times, want 3", got)
	}
}

// TestFreshnessCached_DoesNotBlockOnReconcileLock proves the second half of
// Finding 1: a freshness read during a running SyncWith must not wait on ix.mu
// until its ctx deadline. We simulate an in-flight reconcile by holding ix.mu
// directly, then require the warm read to return promptly.
func TestFreshnessCached_DoesNotBlockOnReconcileLock(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 3; i++ {
		b2WriteFile(t, root, mkName(i), "prose")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix) // warm the cache so the read is served without a walk

	restore := setFreshnessTTL(t, time.Hour)
	defer restore()

	ix.mu.Lock() // stand in for a long-running SyncWith holding the write lock
	defer ix.mu.Unlock()

	done := make(chan IndexFreshness, 1)
	go func() { done <- ix.FreshnessCached(context.Background()) }()

	select {
	case f := <-done:
		if f.Scanned != 3 {
			t.Fatalf("read returned the wrong cached snapshot: Scanned=%d, want 3", f.Scanned)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FreshnessCached blocked on the reconcile lock (ix.mu); it must be lock-free on the hot path (Finding 1)")
	}
}

// setFreshnessTTL pins freshnessCacheTTL for a test and returns a restorer.
func setFreshnessTTL(t *testing.T, d time.Duration) func() {
	t.Helper()
	prev := freshnessCacheTTL
	freshnessCacheTTL = d
	return func() { freshnessCacheTTL = prev }
}
