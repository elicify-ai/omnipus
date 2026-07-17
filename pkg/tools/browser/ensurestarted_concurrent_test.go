package browser

// ensurestarted_concurrent_test.go — coverage for ensureStarted's
// release-mu-across-resolveExecPath-then-discard-the-loser discipline
// (manager.go's ensureStarted, the m.mu.Unlock()/resolveExecPath/m.mu.Lock()/
// "if m.started { return nil }" block). The discard path was previously
// untested: ensureStarted releases m.mu for the (potentially slow)
// resolveExecPath call, re-acquires it, and if a concurrent caller already
// flipped m.started to true in that window, the loser returns nil instead of
// building a SECOND chromedp allocator and overwriting m.allocCtx/m.allocCancel
// (leaking the first winner's cancel func). This test widens that window with
// a slow fake probe so N goroutines genuinely overlap inside resolveExecPath,
// then asserts the final state is a single consistent allocator.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnsureStarted_Concurrent_LosersDiscard spins N goroutines that each hold
// m.mu only across ensureStarted (which itself releases m.mu across
// resolveExecPath). A slow fake google-chrome probe (sleep 0.3s) widens the
// release window so all N overlap inside resolveExecPath concurrently; exactly
// one must win (build the allocator + flip m.started), the rest must discard
// their redundant resolution via the post-relock "if m.started" check instead
// of overwriting m.allocCtx/m.allocCancel and leaking the first allocator.
//
// Observability note: ensureStarted does not expose how many goroutines reached
// the "build allocator" section, so exact winner-count is not directly readable
// from outside without production instrumentation we deliberately avoid. The
// test instead proves the lock was actually released across resolveExecPath
// (the precondition for a discard to even be possible) by counting how many
// goroutines ran the fake probe — a per-probe append counter (shell-builtin
// `echo >>`, no PATH needed). With the lock correctly released, >= 2 goroutines
// enter the unlocked window before the winner flips m.started; if the lock were
// held across resolveExecPath (the bug), only the first goroutine would probe
// and the rest would short-circuit at ensureStarted's top "if m.started" check
// (count == 1). It then asserts the final state is ONE consistent allocator
// (m.started==true, a single non-nil m.allocCancel/m.allocCtx that Shutdown
// tears down cleanly). Under -race this also catches any accidental unlocked
// field access.
func TestEnsureStarted_Concurrent_LosersDiscard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script probe double")
	}

	// CRIT-001: there is no CDP port to preflight anymore (CDP is over the pipe).
	// The managed launch is stubbed with a fake pipe launcher below so the
	// concurrency test never spawns real Chrome — it exercises ONLY ensureStarted's
	// resolveExecPath unlock/relock + discard-the-loser discipline.

	// Slow fake probe: sleep 0.3s to widen ensureStarted's m.mu-release window
	// so concurrent callers genuinely overlap, then record itself by appending
	// one line (its PID) to probes.log. `echo >> ` is a shell-builtin redirect,
	// so it works even under the stripped PATH below (which contains only the
	// fake google-chrome) — an external helper like mkdir would not be found.
	probeDir := t.TempDir()
	t.Setenv("BROWSER_TEST_PROBE_DIR", probeDir)
	binDir := t.TempDir()
	writeExecutable(
		t,
		filepath.Join(binDir, "google-chrome"),
		"#!/bin/sh\nsleep 0.3\necho $$ >> \"$BROWSER_TEST_PROBE_DIR/probes.log\"\necho 'Chromium 131.0.6778.108'\nexit 0\n",
	)
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}
	// Fake pipe launcher: no real Chrome. Runs AFTER resolveExecPath (so the fake
	// google-chrome probe above still records probeCount), and sets the winner's
	// m.allocCtx/m.allocCancel from a plain cancelable context.
	m.managedPipeLaunch = func(_ context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		rootCtx, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: rootCtx, cancel: cancel}, nil
	}

	const N = 8
	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			<-start // release all goroutines as simultaneously as possible
			m.mu.Lock()
			err := m.ensureStarted()
			m.mu.Unlock()
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("ensureStarted goroutines did not complete within 30s")
	}

	if firstErr != nil {
		t.Fatalf("ensureStarted returned an error under concurrency: %v", firstErr)
	}

	m.mu.Lock()
	started := m.started
	allocCancel := m.allocCancel
	allocCtx := m.allocCtx
	m.mu.Unlock()

	assert.True(t, started, "exactly one winner must flip m.started to true")
	require.NotNil(t, allocCancel, "winner must set m.allocCancel (not leaked/overwritten)")
	require.NotNil(t, allocCtx, "winner must set m.allocCtx")

	// THE discriminator that m.mu was actually released across resolveExecPath
	// (the whole point of ensureStarted's unlock/probe/relock shape): at least
	// two goroutines must have entered the unlocked probe window and run the
	// fake google-chrome. If m.mu were held across resolveExecPath (the bug),
	// the first goroutine would run its probe under the lock, flip m.started,
	// and EVERY subsequent goroutine would short-circuit at ensureStarted's TOP
	// "if m.started { return nil }" check without ever calling resolveExecPath
	// — probeCount would be exactly 1. With the lock correctly released, the
	// 0.3s probe widens the window so multiple goroutines race into
	// resolveExecPath before the winner flips the latch, yielding probeCount >=
	// 2. (Wall-clock elapsed is NOT a valid discriminator here: the buggy
	// serialize-then-short-circuit case is also ~one probe width, since the
	// losers return instantly — so a timing assertion would pass either way and
	// is deliberately omitted.)
	//
	// We do NOT assert probeCount == N: under loaded CI a couple of goroutines
	// may be scheduled late enough to hit the success cache after the first
	// probe completes (correct cache behavior). The robust lower bound is >= 2
	// (a diagnostic run on a 2-core devpod observed 6/8); even cache-hitters
	// still exercise the discard via ensureStarted's post-relock "if m.started"
	// check once the winner has flipped the latch.
	probeCount := 0
	if logData, le := os.ReadFile(filepath.Join(probeDir, "probes.log")); le == nil {
		probeCount = strings.Count(string(logData), "\n")
	}
	assert.GreaterOrEqual(t, probeCount, 2,
		"at least 2 of %d concurrent ensureStarted callers should have entered the unlocked probe window (got %d) — "+
			"a count of 1 would mean m.mu was held across resolveExecPath (the bug)",
		N, probeCount)

	// Clean teardown of exactly one allocator: Shutdown calls the final
	// m.allocCancel (idempotent context cancel) and must reset started /
	// allocCancel without panic, confirming a single consistent allocator
	// state. (A leaked FIRST allocator from a broken discard is not directly
	// observable from outside without production instrumentation; this test
	// still exercises the discard path under -race, which is the high-value
	// guard.)
	m.Shutdown()
	m.mu.Lock()
	assert.False(t, m.started, "Shutdown must reset m.started")
	assert.Nil(t, m.allocCancel, "Shutdown must clear m.allocCancel")
	m.mu.Unlock()
}
