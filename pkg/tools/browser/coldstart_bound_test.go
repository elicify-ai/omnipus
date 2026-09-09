package browser

// Fix Wave B, Task 1: regression coverage for two unbounded CDP calls on the
// cold-start critical path — BrowserCoordinator.LoadExtension's dead ctx
// parameter (coordinator.go), and BrowserManager.createTab/bootstrapBrowserCtx's
// unbounded target-attach chromedp.Run (manager.go). Both fed a CRITICAL
// defect another wave fixed: a slow cold start could exceed the browser
// WebSocket's 60s read deadline and tear down the connection.
//
// Split into two tiers:
//   - Pure-logic unit tests for the extracted bound-computing helpers
//     (boundedCallContext, runFirstAttach) — fast, deterministic, no Chrome.
//   - One real-Chrome integration test per fix proving the PRODUCTION call
//     site (LoadExtension, createTab via createFirstTab) is actually wired
//     to the bound, not just that the helper exists in isolation.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeMinimalUnpackedExtension writes the smallest manifest Extensions.
// loadUnpacked will accept into dir — no "key" pinning needed for this test
// (Chrome derives an id from the install path when absent).
func writeMinimalUnpackedExtension(t *testing.T, dir string) {
	t.Helper()
	manifest := `{
  "manifest_version": 3,
  "name": "omnipus-coldstart-bound-test-extension",
  "version": "1.0.0"
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write test extension manifest: %v", err)
	}
}

// --- boundedCallContext (coordinator.go) -----------------------------------

// TestBoundedCallContext_HonorsCallerDeadline proves a caller-supplied
// deadline is respected as-is — even one shorter than the default — rather
// than being silently overridden by the wider default bound. Before the
// LoadExtension fix, the caller's ctx was never even referenced, so this
// exact property did not hold at all.
func TestBoundedCallContext_HonorsCallerDeadline(t *testing.T) {
	// A deadline ALREADY in the past, rather than a 1ms timeout plus a 2ms
	// sleep. context.WithDeadline cancels synchronously at construction when
	// the deadline has passed, so the parent is guaranteed done before
	// boundedCallContext runs. The sleep version depended on the parent's
	// timer goroutine having been scheduled, which a loaded CI runner does
	// not guarantee — it failed exactly that way on 2026-08-17.
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	got, gotCancel := boundedCallContext(parent, time.Hour)
	defer gotCancel()

	select {
	case <-got.Done():
		// expected: the caller's own (already-expired) deadline was honored,
		// not overridden by the much longer default.
	default:
		t.Fatal("boundedCallContext did not honor the caller's own expired deadline")
	}
	if !errors.Is(got.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", got.Err())
	}
}

// TestBoundedCallContext_AppliesDefaultWhenCallerHasNoDeadline proves that a
// caller passing a bare, deadline-free context (e.g. context.Background(),
// exactly what launchChrome's best-effort auto-load call passes) gets the
// supplied default bound applied, instead of running forever.
func TestBoundedCallContext_AppliesDefaultWhenCallerHasNoDeadline(t *testing.T) {
	got, cancel := boundedCallContext(context.Background(), 20*time.Millisecond)
	defer cancel()

	select {
	case <-got.Done():
		t.Fatal("expected NOT done immediately")
	default:
	}

	select {
	case <-got.Done():
		if !errors.Is(got.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", got.Err())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("default timeout never fired — call would have hung unbounded")
	}
}

// TestBoundedCallContext_PropagatesCallerCancellation proves that explicitly
// canceling the caller's context (not just an expired deadline) is also
// honored, matching real usage where a caller aborts an in-flight request.
func TestBoundedCallContext_PropagatesCallerCancellation(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	got, cancel := boundedCallContext(parent, time.Hour)
	defer cancel()

	parentCancel()

	select {
	case <-got.Done():
	case <-time.After(time.Second):
		t.Fatal("boundedCallContext did not propagate the caller's cancellation")
	}
}

// --- runFirstAttach (manager.go) -------------------------------------------

// runFirstAttachTimeoutTestBound is THIS test's own ceiling — deliberately
// NOT the shared runFirstAttachPromptBound (200ms non-race / 2s race; see
// coldstart_bound_race_test.go / coldstart_bound_norace_test.go). Reusing
// the 2s race bound here made the assertion vacuous: `slow` sleeps a FIXED
// 500ms, so even a broken runFirstAttach that blocked for fn's entire
// duration instead of honoring the 20ms timeout would elapse ~500ms — comfortably
// under the 2s race bound, so the assertion could never fire and the "returns
// promptly, does not wait for fn" property went unverified under -race
// (found in the #615/#617/#618 hardening review, F8). 250ms sits well above
// realistic scheduling/race-instrumentation overhead for a 20ms-timeout
// round-trip, yet well below `slow`'s 500ms sleep, so the buggy-wait case
// (~500ms) still trips it in both race and non-race builds — restoring the
// property without touching the shared bound, which
// TestRunFirstAttach_ReturnsUnderlyingErrorWithoutWaitingForTimeout still
// needs at its full 2s (that test's regression elapses 60s, so its own
// discriminating power is unaffected either way).
const runFirstAttachTimeoutTestBound = 250 * time.Millisecond

// TestRunFirstAttach_TimesOutWithoutHanging proves runFirstAttach returns
// promptly with a timeout error when fn takes longer than the bound, instead
// of hanging for fn's full (potentially unbounded) duration. This is the
// exact mechanism createTab/bootstrapBrowserCtx now rely on to stay bounded
// without deriving a timed-out child of chromedp's own ctx (which — per
// runFirstAttach's doc comment — would corrupt the tab's own CDP event loop
// instead of merely bounding the wait).
func TestRunFirstAttach_TimesOutWithoutHanging(t *testing.T) {
	slow := func() error {
		time.Sleep(500 * time.Millisecond)
		return nil
	}

	start := time.Now()
	err := runFirstAttach(slow, 20*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error message, got: %v", err)
	}
	if elapsed > runFirstAttachTimeoutTestBound {
		t.Fatalf("runFirstAttach did not return promptly on timeout: took %s (fn takes 500ms, bound %s)", elapsed, runFirstAttachTimeoutTestBound)
	}
}

// TestRunFirstAttach_ReturnsUnderlyingErrorWithoutWaitingForTimeout proves a
// fast failure from fn is returned immediately (not masked/delayed by the
// timeout branch), preserving the exact "cancel() once, on any error" shape
// createTab/bootstrapBrowserCtx already had before this fix.
func TestRunFirstAttach_ReturnsUnderlyingErrorWithoutWaitingForTimeout(t *testing.T) {
	wantErr := errors.New("boom: attach failed")

	start := time.Now()
	err := runFirstAttach(func() error { return wantErr }, time.Minute)
	elapsed := time.Since(start)

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if elapsed > runFirstAttachPromptBound {
		t.Fatalf("runFirstAttach took %s to return an already-ready error (bound %s)", elapsed, runFirstAttachPromptBound)
	}
}

// TestRunFirstAttach_SuccessWithinTimeout proves the happy path is
// unaffected: a fast, successful fn returns nil promptly.
func TestRunFirstAttach_SuccessWithinTimeout(t *testing.T) {
	if err := runFirstAttach(func() error { return nil }, time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- integration: LoadExtension actually wires ctx through ------------------

// TestCoordinator_LoadExtension_HonorsCallerContext is a real-Chrome
// integration test proving LoadExtension's ACTUAL production call site — not
// just boundedCallContext in isolation — now uses the caller's ctx. Before
// the fix, ctx was accepted but never referenced anywhere in LoadExtension's
// body: the CDP call always ran against the coordinator's own long-lived
// rootCtx, so an already-canceled caller ctx had zero effect and the call
// could only ever complete or hang on Chrome's own response.
//
// Passing an ALREADY-CANCELED context means boundedCallContext's "no
// deadline" branch wraps it via context.WithTimeout(ctx, def) — which
// immediately observes the already-canceled parent (context's own
// propagateCancel fires synchronously for an already-done parent) — so the
// resulting callCtx is Done() before the CDP round trip even starts. Against
// a REAL Chrome, the genuine CDP response requires a measurable IPC round
// trip that cannot complete before this test's own goroutine has finished
// canceling and dispatching, so this is not a flaky race in practice (a real
// io/ipc round trip is never zero-duration) — it deterministically proves
// the ctx is now load-bearing, which was impossible before this fix (the old
// code would have SUCCEEDED here, using rootCtx instead).
func TestCoordinator_LoadExtension_HonorsCallerContext(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	cfg.ExtensionDir = filepath.Join(t.TempDir(), "does-not-need-to-exist-for-this-assertion")
	// A directory need not actually contain a valid manifest for THIS test:
	// we assert on ctx-driven cancellation, which fires before Chrome ever
	// gets far enough to validate the manifest content. (A separate,
	// non-canceled-ctx call against a real extension dir is exercised by
	// TestCoordinator_LoadExtension_SucceedsWithGenerousContext below.)

	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	// Force Chrome live first via a normal, generous-context Register so the
	// LoadExtension call under test exercises ONLY the ctx-cancellation
	// behavior, not coordinator launch latency.
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("warm-agent"))
	if _, err := coord.Register(context.Background(), "warm-agent", mgr); err != nil {
		t.Fatalf("Register (warm-up): %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := coord.LoadExtension(canceledCtx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from LoadExtension given an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the error chain to contain context.Canceled, got: %v", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf(
			"LoadExtension took %s to honor an already-canceled context — ctx is not actually load-bearing",
			elapsed,
		)
	}
}

// --- integration: createTab/bootstrapBrowserCtx are actually bounded -------

// TestCreateFirstTab_BoundedAttach_DoesNotHangOnStuckAttach is a real-Chrome
// integration test proving createFirstTab (which drives BOTH
// bootstrapBrowserCtx and createTab on an agent's cold-start path) returns
// an error promptly instead of hanging when the attach step is forced to
// exceed the bound — proving the ACTUAL production wiring (not just
// runFirstAttach in isolation) is bounded.
//
// firstAttachTimeout is a package var specifically so tests can shrink it
// (restored via t.Cleanup) to a value no real CDP round trip can ever beat,
// forcing the timeout branch deterministically without needing to fabricate
// an artificially wedged CDP transport.
func TestCreateFirstTab_BoundedAttach_DoesNotHangOnStuckAttach(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, _ := newCoordinatorTestConfig(t)
	mgr := newTestManager(t, cfg)
	t.Cleanup(mgr.Shutdown)

	orig := firstAttachTimeout
	firstAttachTimeout = time.Nanosecond
	t.Cleanup(func() { firstAttachTimeout = orig })

	start := time.Now()
	err := mgr.createFirstTab("bounded-attach-session")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected createFirstTab to fail given a 1ns attach bound")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout error to surface from the attach step, got: %v", err)
	}
	// Generous ceiling: real Chrome launch (ensureStarted, NOT bounded by
	// firstAttachTimeout — that's a separate, earlier step) plus real CDP
	// teardown (the internal ~1s cancel-wait cleanup chromedp performs) plus
	// scheduling slack on a possibly contended host, but nowhere near the
	// PageTimeout/real-attach durations this bug historically produced.
	if elapsed > 30*time.Second {
		t.Fatalf("createFirstTab took %s to fail under a 1ns attach bound — attach is not actually bounded", elapsed)
	}
}

// TestCoordinator_LoadExtension_SucceedsWithGenerousContext is the happy-path
// companion to the cancellation test above: a normal, generous context still
// succeeds end-to-end against a real unpacked extension directory, proving
// the bound-wiring fix didn't break the working case.
func TestCoordinator_LoadExtension_SucceedsWithGenerousContext(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	extDir := t.TempDir()
	writeMinimalUnpackedExtension(t, extDir)
	cfg.ExtensionDir = extDir

	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("warm-agent"))
	if _, err := coord.Register(context.Background(), "warm-agent", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	id, err := coord.LoadExtension(context.Background())
	if err != nil {
		t.Fatalf("LoadExtension with a generous context should succeed: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty extension id")
	}
}
