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

// ---------------------------------------------------------------------------
// Finding 3 — an event arriving WHILE a sweep is in flight must force a
// follow-up sweep unconditionally, not only when it also happens to cross
// the burst threshold on its own. Asserted by the MECHANISM (a second
// startSweep call), exactly like TestWatcher_BurstEscalatesToSweep asserts
// escalation itself rather than only final index state — final state alone
// cannot distinguish "the event was requeued via sweepAgain" from "the
// event was applied individually", and on the unfixed code the dropped
// event is applied NEITHER way, so a final-state-only test would need the
// walker to race a write, which this avoids entirely by injecting directly
// into run()'s own input channel.
//
// w.syncFn is overridden to hold the first sweep open under test control
// (see watch.go's syncFn field doc) rather than racing real disk I/O
// timing against a real SyncWith call.
// ---------------------------------------------------------------------------

func TestWatcher_EventDuringSweepForcesFollowUpSweep(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	w := NewWatcher(ix, WatchOptions{
		Debounce: 20 * time.Millisecond,
		// Large enough that the single injected event below cannot cross
		// the threshold on its own — the fix under test must be what
		// forces the second sweep, not ordinary burst counting.
		BurstWindow:    2 * time.Second,
		BurstThreshold: 1_000_000,
		Logger:         testWatchLogger(),
	})

	sweepGate := make(chan struct{})
	var gateOnce sync.Once
	closeGate := func() { gateOnce.Do(func() { close(sweepGate) }) }
	t.Cleanup(closeGate) // safety net: never let a failed assertion hang Stop() in t.Cleanup

	var sweepCalls int64
	w.syncFn = func(ctx context.Context, opts SyncOptions) (SyncStats, error) {
		if atomic.AddInt64(&sweepCalls, 1) == 1 {
			<-sweepGate // hold sweep #1 open until the test releases it
		}
		return ix.SyncWith(ctx, opts)
	}
	var sweepStarted int64
	w.testOnSweepStart = func() { atomic.AddInt64(&sweepStarted, 1) }

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(w.Stop)

	// Trigger sweep #1 deterministically via overflow (simplest possible
	// escalation trigger, and orthogonal to the burst-threshold path this
	// test deliberately avoids).
	w.overflow <- struct{}{}

	waitForCondition(t, 2*time.Second, func() bool {
		return atomic.LoadInt64(&sweepStarted) >= 1
	})

	// PRECONDITION-equivalent: sweep #1 is confirmed running and held open
	// (sweepCalls==1, blocked on sweepGate) before we inject the event that
	// must survive it.
	if got := atomic.LoadInt64(&sweepCalls); got != 1 {
		t.Fatalf("precondition failed: sweepCalls = %d, want exactly 1 sweep in flight before injecting the event", got)
	}

	// Inject a raw event directly into run()'s input channel — exactly what
	// a real platform backend delivers for an ordinary file change — while
	// sweeping is true.
	select {
	case w.rawEvents <- fsEvent{relPath: "duringsweep.md", removed: false}:
	case <-time.After(time.Second):
		t.Fatal("could not inject a raw event: run()'s select loop is not consuming rawEvents")
	}

	// Give run()'s select loop a moment to actually process the injected
	// event (set sweepAgain, on the fixed code) before releasing the sweep.
	time.Sleep(100 * time.Millisecond)
	closeGate()

	// The event injected above MUST have forced a second sweep. On the
	// unfixed code this never happens (the event is silently dropped
	// because it did not itself cross the burst threshold), so this times
	// out — that is finding 3 reproduced.
	waitForCondition(t, 3*time.Second, func() bool {
		return atomic.LoadInt64(&sweepStarted) >= 2
	})
}

// ---------------------------------------------------------------------------
// Finding 4 — a directory that appears with files ALREADY IN IT (a `mv`
// bulk import, a git checkout materialising a folder) must report those
// files, not just start watching the directory for FUTURE changes. Exercised
// through the real per-platform backend (not a white-box hook), since the
// defect lives in addTree/watchDir (darwin) and addTree (linux) themselves.
// ---------------------------------------------------------------------------

func TestWatcher_DirectoryAppearsWithExistingFiles_ContentsBecomeFindable(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	// Build the "already has files in it" directory OUTSIDE root first, so
	// its files exist before the directory itself ever appears under the
	// watched root — the exact `mv ~/Downloads/meeting-notes vault/` shape
	// finding 4 describes.
	staging := t.TempDir()
	b2WriteFile(t, staging, "already-here.md", "a note containing watchbulkimportword right here\n")
	b2WriteFile(t, staging, "nested/deep.md", "another note with watchbulkimportword too\n")

	// PRECONDITION: nothing indexed yet, so "findable after" cannot be
	// passing on a pre-existing state.
	pre, err := ix.Search("watchbulkimportword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre) != 0 {
		t.Fatalf("precondition failed: already findable before the directory existed: %v", b2HitPaths(pre))
	}

	startTestWatcher(t, ix, WatchOptions{Debounce: 30 * time.Millisecond})

	if err := os.Rename(staging, root+"/imported"); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		hits, err := ix.Search("watchbulkimportword", 10)
		return err == nil && len(hits) == 2
	})
}

// ---------------------------------------------------------------------------
// Finding 5 — Stop() must not return while an escalation sweep is still
// genuinely in flight: pkg/gateway/knowledge_lifecycle.go orders "every
// watcher stops BEFORE any index closes" and relies on Stop's own doc
// comment ("blocks until all of that has actually finished ... a genuine
// join point"). sweepCancel narrows the window but does not close it, so
// the fake sweep below deliberately ignores context cancellation — the same
// "still finishing its own in-flight work" gap a real SyncWith call can have
// between being cancelled and actually returning.
// ---------------------------------------------------------------------------

func TestWatcher_StopWaitsForInFlightSweep(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	w := NewWatcher(ix, WatchOptions{Logger: testWatchLogger()})

	sweepGate := make(chan struct{})
	var gateOnce sync.Once
	closeGate := func() { gateOnce.Do(func() { close(sweepGate) }) }
	t.Cleanup(closeGate)

	var sweepRunning int32
	var sweepFinished int32
	w.syncFn = func(ctx context.Context, opts SyncOptions) (SyncStats, error) {
		atomic.StoreInt32(&sweepRunning, 1)
		<-sweepGate // deliberately does NOT select on ctx.Done() — see comment above
		atomic.StoreInt32(&sweepFinished, 1)
		return SyncStats{}, nil
	}

	if err := w.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	w.overflow <- struct{}{}
	waitForCondition(t, 2*time.Second, func() bool {
		return atomic.LoadInt32(&sweepRunning) == 1
	})

	// Release the fake sweep only after a delay comfortably longer than any
	// scheduler noise, so that if Stop() returns BEFORE this fires, that is
	// unambiguous evidence Stop() did not actually wait for the sweep.
	const releaseAfter = 150 * time.Millisecond
	go func() {
		time.Sleep(releaseAfter)
		closeGate()
	}()

	stopReturned := make(chan struct{})
	stopStartedAt := time.Now()
	go func() {
		w.Stop()
		close(stopReturned)
	}()

	select {
	case <-stopReturned:
		elapsed := time.Since(stopStartedAt)
		if atomic.LoadInt32(&sweepFinished) != 1 {
			t.Fatalf("Stop() returned after %s, before the in-flight sweep finished — Stop's join-point contract is broken (finding 5)", elapsed)
		}
		if elapsed < releaseAfter {
			t.Fatalf("Stop() returned after only %s, before the %s release delay — it cannot genuinely have waited for the sweep", elapsed, releaseAfter)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5s")
	}
}
