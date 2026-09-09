// Omnipus — R5 finding 7
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

// trim_interval_race_test.go — the one unsynchronised read of a configuration
// value that governs directory deletion.
//
// tools.browser.cache_trim_interval is written by ApplyRuntimeConfig under
// p.mu on every config reload, and it is how often the sweep that DELETES
// directories runs. Every reader takes p.mu — except
// logUnboundedContinuousDriveOnce, which released the lock and only then read
// the field for its log line.
//
// This test is in its own file rather than trim_test.go because that file is
// another unit's in-flight work.

import (
	"sync"
	"testing"
	"time"
)

// TestCacheTrimInterval_ReadUnderTheLock drives a reload concurrently with the
// FR-074 residual log line.
//
// It is a real test on any run and a DECISIVE one under -race: with the read
// outside the critical section the detector reports a write/read data race on
// BrowserPool.cacheTrimInterval and the run fails. Without -race it still
// asserts the thing an operator cares about — that the reload actually takes
// effect and neither call corrupts the other — but the race detector is the
// instrument that can see the defect, so `go test -race` is the run that
// matters here.
//
// The concurrency is genuine, not decorative: ApplyRuntimeConfig and the log
// line run on separate goroutines with no ordering between them, which is
// exactly how they meet in production (a Settings save lands on the gateway's
// HTTP goroutine while the pool's close/trim path runs on its own).
func TestCacheTrimInterval_ReadUnderTheLock(t *testing.T) {
	home := t.TempDir()
	pool := NewBrowserPool(home, BrowserConfig{ProfileDir: home + "/browser/profiles/default"})

	const rounds = 200

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: config reloads, the real one, taking p.mu as production does.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			pool.ApplyRuntimeConfig(BrowserConfig{
				ProfileDir:        home + "/browser/profiles/default",
				CacheTrimInterval: time.Duration(i+1) * time.Minute,
			})
		}
	}()

	// Reader: the FR-074 residual line. Its latch means only the first call
	// logs, but the READ it does happens on every call, which is what makes
	// the loop worth running rather than calling it once.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			pool.logUnboundedContinuousDriveOnce()
		}
	}()

	wg.Wait()

	// The reload must actually have taken effect — a value nobody can observe
	// would make the race above unreachable and this test vacuous.
	if got := pool.CacheTrimInterval(); got != time.Duration(rounds)*time.Minute {
		t.Fatalf("cache_trim_interval = %v after %d reloads, want %v — if the setting does not land, "+
			"this test is not exercising the field the race is on", got, rounds, time.Duration(rounds)*time.Minute)
	}
}

// TestCacheTrimInterval_EveryReaderTakesTheLock is the structural half.
//
// The race test above can only fail when the detector is on and the scheduler
// interleaves the two goroutines. This one fails on any machine, and it is the
// cheaper guard against the specific regression: a future reader of
// cacheTrimInterval added outside a critical section.
//
// It is deliberately narrow — it checks the ONE function that had the defect,
// by asserting the read is captured into a local while the lock is held rather
// than dereferenced through p after the Unlock.
func TestCacheTrimInterval_EveryReaderTakesTheLock(t *testing.T) {
	src := readSourceForTest(t, "trim.go")

	fn := funcBodyForTest(t, src, "func (p *BrowserPool) logUnboundedContinuousDriveOnce()")
	unlock := indexOfForTest(fn, "p.mu.Unlock()")
	if unlock < 0 {
		t.Fatal("logUnboundedContinuousDriveOnce no longer unlocks p.mu — re-read this test before deleting it")
	}
	if after := fn[unlock:]; indexOfForTest(after, "p.cacheTrimInterval") >= 0 {
		t.Error("p.cacheTrimInterval is read AFTER p.mu.Unlock(). ApplyRuntimeConfig writes that field " +
			"under p.mu on every config reload, and it is the cadence of the sweep that deletes " +
			"directories — capture it into a local inside the critical section instead")
	}
	if before := fn[:unlock]; indexOfForTest(before, "p.cacheTrimInterval") < 0 {
		t.Error("the interval is no longer read under the lock at all — if the log line stopped reporting " +
			"it, delete this test deliberately rather than letting it pass vacuously")
	}
}

// funcBodyForTest returns the source from the start of the named function to
// the next top-level closing brace. Crude on purpose: it needs to scope a
// string search to one function, not to parse Go.
func funcBodyForTest(t *testing.T, src, signature string) string {
	t.Helper()
	start := indexOfForTest(src, signature)
	if start < 0 {
		t.Fatalf("could not find %q in the source — the test is looking at the wrong place", signature)
	}
	rest := src[start:]
	if end := indexOfForTest(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func indexOfForTest(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
