//go:build linux || darwin

// Tests for the filesystem watcher (watch.go, watch_linux.go, watch_darwin.go)
// against docs/internal/design/knowledge-index-freshness.md.
//
// docs/internal/false-green-patterns.md governs. In particular:
//
//   - every "becomes findable" test asserts the PRECONDITION (not findable
//     yet) first, so the later assertion cannot be passing on a pre-existing
//     state;
//   - the debounce and burst tests assert the MECHANISM (an apply count, a
//     sweep-start signal), not just the final index state — final state
//     alone cannot distinguish "the optimisation fired" from "every event
//     was applied individually and happened to converge on the same
//     answer", which is exactly the distinction design §5 cares about;
//   - every async assertion polls with waitForCondition rather than a fixed
//     sleep-then-check, and where a fixed wait IS used (to prove something
//     did NOT happen), the window is chosen to be several multiples of the
//     relevant debounce/threshold, not a guess.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testWatchLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// waitForCondition polls cond until it reports true or timeout elapses,
// failing the test on timeout. Everything in this file that waits on the
// watcher's asynchronous effect goes through this rather than a fixed sleep,
// so a slow CI box gets more time rather than a flaky failure, and a stuck
// watcher fails promptly rather than after an arbitrarily long fixed sleep.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func startTestWatcher(t *testing.T, ix *Index, opts WatchOptions) *Watcher {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = testWatchLogger()
	}
	w := NewWatcher(ix, opts)
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)
	return w
}

// ---------------------------------------------------------------------------
// A created file becomes findable.
// ---------------------------------------------------------------------------

func TestWatcher_CreatedFileBecomesFindable(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	// PRECONDITION: nothing indexed yet, so "findable after" cannot be
	// passing on a pre-existing state.
	pre, err := ix.Search("brandnewwatchword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre) != 0 {
		t.Fatalf("precondition failed: already findable before the file existed: %v", b2HitPaths(pre))
	}

	startTestWatcher(t, ix, WatchOptions{Debounce: 30 * time.Millisecond})

	b2WriteFile(t, root, "created.md", "a note containing brandnewwatchword right here\n")

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("brandnewwatchword", 10)
		return err == nil && containsPath(hits, "created.md")
	})
}

// ---------------------------------------------------------------------------
// An edit replaces old content with new: old stops matching, new matches.
// ---------------------------------------------------------------------------

func TestWatcher_EditedFile_OldStopsNewMatches(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "edited.md", "alpha watchkeyword_old body\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	// PRECONDITIONS.
	if hits, err := ix.Search("watchkeyword_old", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "edited.md") {
		t.Fatalf("precondition failed: old term not findable before the edit: %v", b2HitPaths(hits))
	}
	if hits, err := ix.Search("watchkeyword_new", 10); err != nil {
		t.Fatal(err)
	} else if containsPath(hits, "edited.md") {
		t.Fatalf("precondition failed: new term already findable before the edit: %v", b2HitPaths(hits))
	}

	startTestWatcher(t, ix, WatchOptions{Debounce: 30 * time.Millisecond})

	b2WriteFile(t, root, "edited.md", "beta watchkeyword_new body\n")

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("watchkeyword_new", 10)
		return err == nil && containsPath(hits, "edited.md")
	})

	hits, err := ix.Search("watchkeyword_old", 10)
	if err != nil {
		t.Fatal(err)
	}
	if containsPath(hits, "edited.md") {
		t.Fatalf("old content still findable after the edit: %v", b2HitPaths(hits))
	}
}

// ---------------------------------------------------------------------------
// A deleted file stops matching.
// ---------------------------------------------------------------------------

func TestWatcher_DeletedFileStopsMatching(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	path := b2WriteFile(t, root, "deleted.md", "gone soon watchdeleteword\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	// PRECONDITION: findable before deletion.
	if hits, err := ix.Search("watchdeleteword", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "deleted.md") {
		t.Fatalf("precondition failed: not findable before deletion: %v", b2HitPaths(hits))
	}

	startTestWatcher(t, ix, WatchOptions{Debounce: 30 * time.Millisecond})

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("watchdeleteword", 10)
		return err == nil && !containsPath(hits, "deleted.md")
	})
}

// ---------------------------------------------------------------------------
// Debounce collapses rapid saves to ONE file into ONE update — asserted by
// counting actual applyOne calls, not just by checking the final content.
// ---------------------------------------------------------------------------

func TestWatcher_DebounceCollapsesRapidSaves(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "rapid.md", "start content\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	const debounce = 150 * time.Millisecond
	w := NewWatcher(ix, WatchOptions{Debounce: debounce, Logger: testWatchLogger()})

	var applyCount int64
	w.testOnApply = func(relPath string, _ bool) {
		if relPath == "rapid.md" {
			atomic.AddInt64(&applyCount, 1)
		}
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)

	// Eight saves to the SAME file, each well inside the previous save's
	// quiet period (spacing debounce/6 << debounce), so a working debounce
	// keeps resetting one timer rather than ever letting it fire.
	for i := range 8 {
		b2WriteFile(t, root, "rapid.md", fmt.Sprintf("revision %d watchrapidword\n", i))
		time.Sleep(debounce / 6)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("watchrapidword", 10)
		return err == nil && containsPath(hits, "rapid.md")
	})

	// The debounce timer has now had two full quiet periods to fire (and, if
	// broken, to fire repeatedly) since the last write.
	time.Sleep(debounce * 2)

	if got := atomic.LoadInt64(&applyCount); got != 1 {
		t.Fatalf("applyOne ran %d times for one file's rapid-save burst, want exactly 1 — debounce should have collapsed them", got)
	}
}

// ---------------------------------------------------------------------------
// A burst escalates to ONE sweep rather than N individual updates — asserted
// by observing the escalation itself (a sweep-start signal, and an apply
// count well under the file count), not just that every file ends up
// findable (which per-file updates would ALSO produce, hiding a broken
// escalation entirely).
// ---------------------------------------------------------------------------

func TestWatcher_BurstEscalatesToSweep(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	const threshold = 6
	w := NewWatcher(ix, WatchOptions{
		Debounce:       20 * time.Millisecond,
		BurstWindow:    2 * time.Second,
		BurstThreshold: threshold,
		Logger:         testWatchLogger(),
	})

	var sweepStarted int64
	w.testOnSweepStart = func() { atomic.AddInt64(&sweepStarted, 1) }
	var applyCount int64
	w.testOnApply = func(string, bool) { atomic.AddInt64(&applyCount, 1) }

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)

	const numFiles = 40
	for i := range numFiles {
		b2WriteFile(t, root, fmt.Sprintf("burst%02d.md", i), fmt.Sprintf("watchburstword file %d\n", i))
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return atomic.LoadInt64(&sweepStarted) >= 1
	})

	waitForCondition(t, 15*time.Second, func() bool {
		hits, err := ix.Search("watchburstword", numFiles+10)
		return err == nil && len(hits) == numFiles
	})

	if got := atomic.LoadInt64(&applyCount); got >= numFiles {
		t.Fatalf("applyOne ran %d times for a %d-file burst; escalation should have preempted most of them via one sweep, not applied every file individually", got, numFiles)
	}
}

// ---------------------------------------------------------------------------
// Watching-unavailable is reported, never silent — both "cannot start" and
// "stopped mid-run" (design §8). Both use the watchBackend test seam so the
// failure is deterministic rather than depending on actually exhausting an
// OS watch limit.
// ---------------------------------------------------------------------------

func TestWatcher_StartFailure_ReportsUnavailable(t *testing.T) {
	orig := watchBackend
	t.Cleanup(func() { watchBackend = orig })

	wantErr := errors.New("boom: simulated backend failure")
	watchBackend = func(string, chan<- fsEvent, chan<- struct{}, <-chan struct{}) (<-chan error, error) {
		return nil, wantErr
	}

	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	w := NewWatcher(ix, WatchOptions{Logger: testWatchLogger()})

	err := w.Start(context.Background())
	if err == nil {
		t.Fatal("Start returned a nil error for a backend that failed to start")
	}
	var unavailable *WatchUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Start error = %v (%T), want it to be (or wrap) *WatchUnavailableError", err, err)
	}
	if !errors.Is(err, wantErr) {
		t.Error("Start error does not unwrap to the underlying backend error")
	}

	select {
	case <-w.Unavailable():
	default:
		t.Fatal("Unavailable() channel not closed after Start failed")
	}
	if w.Err() == nil {
		t.Fatal("Err() is nil after Start failed")
	}
}

func TestWatcher_BackendDiesMidRun_ReportsUnavailable(t *testing.T) {
	orig := watchBackend
	t.Cleanup(func() { watchBackend = orig })

	runErrCh := make(chan error, 1)
	watchBackend = func(string, chan<- fsEvent, chan<- struct{}, <-chan struct{}) (<-chan error, error) {
		return runErrCh, nil
	}

	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)
	w := NewWatcher(ix, WatchOptions{Logger: testWatchLogger()})
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)

	select {
	case <-w.Unavailable():
		t.Fatal("Unavailable() closed before the backend ever failed")
	default:
	}

	runErrCh <- errors.New("simulated backend death")

	waitForCondition(t, 2*time.Second, func() bool {
		select {
		case <-w.Unavailable():
			return true
		default:
			return false
		}
	})
	if w.Err() == nil {
		t.Fatal("Err() is nil after the backend died mid-run")
	}
}

// ---------------------------------------------------------------------------
// Stop is a genuine join point: repeated Start/Stop cycles do not accumulate
// running goroutines.
// ---------------------------------------------------------------------------

func TestWatcher_StopLeavesNoGoroutinesRunning(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	runtime.GC()
	before := runtime.NumGoroutine()

	for i := range 10 {
		w := NewWatcher(ix, WatchOptions{Debounce: 5 * time.Millisecond})
		if err := w.Start(context.Background()); err != nil {
			t.Fatalf("Start iteration %d: %v", i, err)
		}
		b2WriteFile(t, root, fmt.Sprintf("leak%02d.md", i), "content\n")
		w.Stop()
		// Stop must be idempotent and must not hang.
		w.Stop()
	}

	waitForCondition(t, 3*time.Second, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= before+2 // small slack for scheduler/runtime noise
	})
}

// ---------------------------------------------------------------------------
// iCloud download placeholders are skipped by name (design §7) — proven
// against a real file created in the same burst, so this cannot pass merely
// because "nothing happened yet".
// ---------------------------------------------------------------------------

func TestWatcher_ICloudPlaceholderIgnored(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	w := NewWatcher(ix, WatchOptions{Debounce: 20 * time.Millisecond, Logger: testWatchLogger()})
	var mu sync.Mutex
	var applied []string
	w.testOnApply = func(relPath string, _ bool) {
		mu.Lock()
		applied = append(applied, relPath)
		mu.Unlock()
	}
	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)

	b2WriteFile(t, root, "real.md", "watchrealword content\n")
	b2WriteFile(t, root, ".Placeholder.md.icloud", "")

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("watchrealword", 10)
		return err == nil && containsPath(hits, "real.md")
	})

	// Give the placeholder every chance the real file just had.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, p := range applied {
		if p == ".Placeholder.md.icloud" {
			t.Fatalf("watcher applied an iCloud placeholder path; design §7 requires it be skipped by name (applied=%v)", applied)
		}
	}
}

// ---------------------------------------------------------------------------
// WatchUnavailableError's plumbing (Error text, Unwrap) — cheap unit
// coverage for the type callers errors.As-match against.
// ---------------------------------------------------------------------------

func TestWatchUnavailableError_UnwrapsAndFormats(t *testing.T) {
	inner := errors.New("inner cause")
	e := &WatchUnavailableError{Reason: "some reason", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("WatchUnavailableError does not unwrap to its Err")
	}
	if got := e.Error(); got == "" {
		t.Error("Error() returned an empty string")
	}

	bare := &WatchUnavailableError{Reason: "no underlying error"}
	if bare.Unwrap() != nil {
		t.Error("Unwrap() on a WatchUnavailableError with no Err should be nil")
	}
	if got := bare.Error(); got == "" {
		t.Error("Error() returned an empty string for a bare reason")
	}
}
