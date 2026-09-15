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
