// manager_session_startup_test.go: tests for start the browser behind a manager and create a browsing context's first tab - exec-path resolution, ensureStarted, Session, and the first-tab bootstrap.

package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from manager.go tests 2026-09-15 ---

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
	// Trust the fake PATH chrome planted above instead of falling through to
	// resolveExecPath's step-4 managed download: with the security-hardened
	// TrustPathChrome=false default, resolve() discards this test's
	// self-sufficient PATH candidate (WARN-BROWSER-007) and falls through to
	// EnsureChromium -> cftPlatform, which errors on any non-amd64 Linux
	// ("chrome-for-testing has no linux/arm64 build") — confirmed by the
	// Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing here. This
	// test's subject is ensureStarted's m.mu release/discard discipline
	// around resolveExecPath, not which resolution tier wins, so opting in to
	// the already-existing TrustPathChrome seam (same opt-in
	// TestResolveExecPath_SkipsBrokenPATHCandidate uses) makes the test both
	// arch-independent and network-free rather than gating it to amd64.
	cfg.TrustPathChrome = true
	m := &BrowserManager{cfg: cfg}
	// CRIT-001: the fake "google-chrome" above only satisfies resolveExecPath's
	// --version probe — launching it for real over the CDP pipe would fail
	// cdppipe's liveness probe (it never speaks --remote-debugging-pipe
	// framing). Inject a fake pipe launcher so the winner's actual "launch"
	// step is instant and spawns nothing, keeping this test's real subject
	// (the m.mu release/discard discipline around resolveExecPath) isolated
	// from cdppipe/real-Chrome concerns entirely.
	m.pipeLauncherFn = func(ctx context.Context, execPath string, pcfg pipeLaunchConfig) (*pipeLaunchResult, error) {
		fakeCtx, fakeCancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: fakeCtx, cancel: fakeCancel}, nil
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

// --- resolveExecPath: PATH-candidate validation ---

// TestResolveExecPath_SkipsBrokenPATHCandidate reproduces the snap-stub bug:
// the first candidate name (google-chrome) resolves via LookPath but exits
// non-zero when actually run; resolution must skip it and pick the next,
// genuinely-working candidate (chromium) rather than committing to the
// broken one. SEC-ADR052-002: with the security-hardened default
// TrustPathChrome=false the $PATH result is recorded at WARN-BROWSER-007
// and discarded, so this test opts in to trusting $PATH to exercise the
// broken-PATH-candidate skip behavior.
func TestResolveExecPath_SkipsBrokenPATHCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"), "#!/bin/sh\nexit 1\n")
	chromiumPath := filepath.Join(binDir, "chromium")
	writeExecutable(t, chromiumPath, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")

	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.TrustPathChrome = true // SEC-ADR052-002: opt in to trust $PATH
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, chromiumPath, got)
}

// TestResolveExecPath_AllBrokenFallsThroughToManaged covers every PATH
// candidate name failing its probe: resolution must fall through to the
// managed install path and find the pre-seeded binary there, never touching
// the network (no manifest server configured for this test).
func TestResolveExecPath_AllBrokenFallsThroughToManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	binPath := seedManagedBinary(t, installRootFor(cfg), platform)

	m := &BrowserManager{cfg: cfg}
	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, binPath, got)
}

// TestResolveExecPath_CachesAfterFirstProbe verifies the second
// resolveExecPath call on the same manager reuses the cached path instead of
// re-shelling-out to probe the candidate again — proven via a counter file
// the fake script increments on every real invocation. SEC-ADR052-002: with
// the security-hardened default TrustPathChrome=false, $PATH is discarded,
// so this test opts in to trusting $PATH to exercise the cache.
func TestResolveExecPath_CachesAfterFirstProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	binDir := t.TempDir()
	counterFile := filepath.Join(t.TempDir(), "probe-count")
	t.Setenv("BROWSER_TEST_PROBE_COUNTER", counterFile)
	script := "#!/bin/sh\necho x >> \"$BROWSER_TEST_PROBE_COUNTER\"\necho 'Chromium 131.0.6778.108'\nexit 0\n"
	candidatePath := filepath.Join(binDir, "google-chrome")
	writeExecutable(t, candidatePath, script)

	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.TrustPathChrome = true // SEC-ADR052-002: opt in to trust $PATH
	m := &BrowserManager{cfg: cfg}

	first, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, candidatePath, first)

	second, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, second)

	data, err := os.ReadFile(counterFile)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "x"),
		"expected exactly one real probe invocation across two resolveExecPath calls")
}

// --- resolveExecPath: ExecPath override (operator trust) ---

// TestResolveExecPath_ExecPathOverride_ReturnedVerbatim_NoProbe verifies that
// when cfg.ExecPath is set to a real executable file, resolveExecPath returns
// it verbatim and NEVER probes $PATH — proven via a decoy google-chrome on
// PATH that records itself (mkdir is atomic, so concurrent-safe) if its probe
// ever ran. The override is an operator trust: honored as-is after the
// stat/dir/exec-bit check instead of being silently replaced by a discovered
// binary.
func TestResolveExecPath_ExecPathOverride_ReturnedVerbatim_NoProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix executable-bit layout")
	}
	overrideDir := t.TempDir()
	overrideBin := filepath.Join(overrideDir, "my-chrome")
	writeExecutable(t, overrideBin, "#!/bin/sh\nexit 0\n")

	// Decoy google-chrome on a separate PATH: records itself under probeDir if
	// its probe ever runs. Empty probeDir after the call proves no probe ran.
	probeDir := t.TempDir()
	t.Setenv("BROWSER_TEST_PROBE_DIR", probeDir)
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"),
		"#!/bin/sh\nmkdir \"$BROWSER_TEST_PROBE_DIR/$$\" 2>/dev/null\necho 'Chromium 131.0.6778.108'\nexit 0\n")
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = overrideBin
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, overrideBin, got)

	entries, _ := os.ReadDir(probeDir)
	assert.Empty(t, entries, "PATH probe must NOT run when ExecPath override is set and valid")
}

// TestResolveExecPath_ExecPathOverride_Directory_IsClearError verifies the L3
// directory guard: an exec_path that points at a directory fails fast with a
// clear message naming exec_path and "directory", instead of the downstream
// chromedp exec error ("permission denied"/"exec format error") that never
// names the real cause.
func TestResolveExecPath_ExecPathOverride_Directory_IsClearError(t *testing.T) {
	dir := t.TempDir()
	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = dir // a directory, not a file
	m := &BrowserManager{cfg: cfg}

	_, err := m.resolveExecPath(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec_path")
	assert.Contains(t, err.Error(), "directory")
}

// TestResolveExecPath_ExecPathOverride_NotExecutable_IsClearError verifies the
// L3 exec-bit guard (POSIX only): an exec_path that exists and is a regular
// file but lacks any execute bit fails fast with a clear message, instead of
// the downstream "permission denied" that never names exec_path. Skipped on
// Windows where Go's os.FileMode carries no Unix execute bits.
func TestResolveExecPath_ExecPathOverride_NotExecutable_IsClearError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod exec-bit is posix-only; Go FileMode carries no Unix exec bits on Windows")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "not-exec")
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o644))

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = binPath
	m := &BrowserManager{cfg: cfg}

	_, err := m.resolveExecPath(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec_path")
	assert.Contains(t, err.Error(), "not executable")
}

func TestSessionContextLocalStartupCancellationReturnsBeforeLaunch(t *testing.T) {
	m, entered, release, disposed := blockedLocalStartupManager(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	launch := <-entered
	cancel()
	got, prompt := startupResultWithin(done)
	select {
	case <-launch.Done():
	case <-time.After(200 * time.Millisecond):
		t.Error("local launch did not receive caller cancellation")
	}
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("local startup cancellation prompt=%t err=%v", prompt, got.err)
	}
	assertLocalStartupDrained(t, m, disposed)
	m.mu.Lock()
	started, tabs := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || tabs != 0 {
		t.Errorf("canceled local launch published started=%t sessions=%d", started, tabs)
	}
}

func TestSessionContextLocalStartupShutdownDoesNotWaitForLaunch(t *testing.T) {
	testLocalStartupShutdown(t, false)
}

func TestSessionLocalStartupShutdownDoesNotWaitForLaunch(t *testing.T) {
	testLocalStartupShutdown(t, true)
}

func TestSessionContextCancellationReachesColdLaunch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coord := NewBrowserCoordinator(home, cfg)
	m.cfg = cfg
	m.started = false
	m.AttachSharedChrome(coord, testKey)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	coord.pipeLauncher = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		entered <- ctx
		<-release
		root, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }); coord.Shutdown(); m.Shutdown() })
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	launchCtx := <-entered
	cancel()
	got, prompt := startupResultWithin(done)
	propagated := false
	select {
	case <-launchCtx.Done():
		propagated = true
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("cold session cancellation prompt=%t err=%v", prompt, got.err)
	}
	if !propagated {
		t.Error("SessionContext lost caller cancellation before pipe launch")
	}
	m.mu.Lock()
	started, count := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || count != 0 {
		t.Errorf("canceled cold request published manager state: started=%t sessions=%d", started, count)
	}
}

func TestSessionContextCanceledTabGateWaitDoesNotCreate(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancel(context.Background())
	done := make(chan startupResult, 1)
	go func() { root, err := m.SessionContext(caller, testSessionID); done <- startupResult{root, err} }()
	cancel()
	got, prompt := startupResultWithin(done)
	release()
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, context.Canceled) {
		t.Errorf("queued caller cancellation prompt=%t err=%v", prompt, got.err)
	}
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	if count != 0 {
		t.Errorf("canceled caller queued outside startup created %d sessions", count)
	}
	m.Shutdown()
}

func TestSessionContextReturnedTargetOutlivesCaller(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	// Retain actual parent inheritance: the general tab fixture intentionally
	// ignores its parent and would hide a leaked request lifetime here.
	m.createTabFn = func(parent context.Context, _ target.ID) (*tabEntry, error) {
		child, cancel := chromedp.NewContext(parent)
		return &tabEntry{ctx: child, cancel: cancel, targetID: target.ID("persistent")}, nil
	}

	t.Cleanup(m.Shutdown)
	caller, cancel := context.WithCancel(context.Background())
	root, err := m.SessionContext(caller, testSessionID)
	if err != nil || root == nil {
		t.Fatalf("session failed: %v", err)
	}
	cancel()
	select {
	case <-root.Done():
		t.Fatalf("successful target inherited request cancellation: %v", root.Err())
	case <-time.After(50 * time.Millisecond):
	}
	again, err := m.SessionContext(context.Background(), testSessionID)
	if err != nil || again != root {
		t.Fatalf("successful target was not reusable: same=%t err=%v", again == root, err)
	}
}

func TestSessionContextShutdownCancelsPendingStartup(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coord := NewBrowserCoordinator(home, cfg)
	m.cfg = cfg
	m.started = false
	m.AttachSharedChrome(coord, testKey)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	coord.pipeLauncher = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		entered <- ctx
		<-release
		root, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
	}
	t.Cleanup(func() { once.Do(func() { close(release) }); coord.Shutdown(); coord.waitStartupDrain(); m.Shutdown() })
	done := make(chan startupResult, 1)
	go func() {
		root, err := m.SessionContext(context.Background(), testSessionID)
		done <- startupResult{root, err}
	}()
	launchCtx := <-entered
	m.Shutdown()
	got, prompt := startupResultWithin(done)
	canceled := false
	select {
	case <-launchCtx.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if !prompt {
		got = <-done
	}
	if !prompt || !errors.Is(got.err, errBrowserSessionChanged) {
		t.Errorf("retired startup prompt=%t err=%v", prompt, got.err)
	}
	if !canceled {
		t.Error("manager shutdown did not cancel its pending startup request")
	}
	m.mu.Lock()
	started, count := m.started, len(m.sessions)
	m.mu.Unlock()
	if started || count != 0 {
		t.Errorf("late retired startup published manager state: started=%t sessions=%d", started, count)
	}
}

// TestProbeChromiumBinary_HonoursSuppliedTimeout proves the budget is really
// the knob (not a constant read inside), using a script that sleeps past the
// short budget but finishes inside the long one — the shape of the macOS
// first-exec case.
func TestProbeChromiumBinary_HonoursSuppliedTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow-chrome")
	require.NoError(t, os.WriteFile(slow, []byte("#!/bin/sh\nsleep 1\necho 'Chrome 1.2.3'\n"), 0o755))

	ok, reason := probeChromiumBinaryWithTimeout(context.Background(), slow, 200*time.Millisecond)
	require.False(t, ok, "a probe shorter than the binary's startup must fail")
	require.Contains(t, reason, "timed out after 200ms",
		"the reason must report the ACTUAL budget used, not a hardcoded constant")

	ok, reason = probeChromiumBinaryWithTimeout(context.Background(), slow, 10*time.Second)
	require.True(t, ok, "the same binary must pass with a realistic budget; got: %s", reason)
	require.Empty(t, reason)
}

// TestProbeChromiumBinary_StillRejectsGenuinelyBrokenBinaries guards the
// thing the longer timeout must NOT weaken: a binary that runs and exits
// non-zero (the Ubuntu snap-stub case probeChromiumBinary exists to catch)
// is still rejected immediately, with its exit status in the reason.
func TestProbeChromiumBinary_StillRejectsGenuinelyBrokenBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "stub-chrome")
	require.NoError(t, os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755))

	start := time.Now()
	ok, reason := probeChromiumBinaryWithTimeout(context.Background(), broken, managedChromiumProbeTimeout)
	require.False(t, ok)
	require.Contains(t, reason, "binary present but broken")
	require.Less(t, time.Since(start), 10*time.Second,
		"a fast non-zero exit must be rejected immediately, never waited out")
}

func TestSessionStartupContextReadsRetiredGateBeforeCallback(t *testing.T) {
	lifetime, retire := context.WithCancelCause(context.Background())
	original := &startupDeferredCancellation{Context: lifetime}
	operation, stop := sessionStartupContext(context.Background(), original)
	defer stop()
	retire(errBrowserSessionChanged)
	if !errors.Is(operation.Err(), context.Canceled) {
		t.Errorf("retired original gate still admits startup: %v", operation.Err())
	}
	if original.callback != nil {
		original.callback()
	}
}

// TestCreateFirstTab_ActivatesFirstTabInChrome. The first-tab path is reached
// on cold start AND on CloseTab's last-tab replacement — the latter being the
// case that matters, because the tab the user was watching has just been
// destroyed and Chrome's active-tab answer at that instant is a fallback of
// its own choosing.
func TestCreateFirstTab_ActivatesFirstTabInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)

	ctx, err := m.Session(testSessionID) // creates the browsing context's first tab
	require.NoError(t, err)

	activations := rec.calls()
	require.Len(t, activations, 1, "creating a browsing context's first tab must tell Chrome it is active")
	assert.True(t, chromedp.FromContext(activations[0]) == chromedp.FromContext(ctx),
		"the activated context must be the tab Session() resolves")
}

// --- Session(default) creation + following the active tab (ADR-041 D1) ---

func TestSession_CreatesSingleTabBrowsingContext(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	ctx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestSession_FollowsActiveTabAfterSwitch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	firstCtx, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// OpenTab makes the new tab active — Session() must now return ITS
	// context, not the first tab's (ADR-041 D1: "Session always returns the
	// ACTIVE tab's context").
	//
	// PRE-EXISTING DATA RACE FIX (confirmed via -race on base 9d31e106):
	// firstCtx/secondCtx/thirdCtx are real chromedp contexts (fakeTabFactory
	// builds them via chromedp.NewContext(context.Background()), same as
	// production createTab) — chromedp.NewContext starts a background
	// goroutine (chromedp.go's "go func() { <-ctx.Done(); ... }") that reads
	// and CASes the context's internal done-channel pointer the moment its
	// parent is ever Done. require.NotEqual/assert.Equal fall through to
	// testify's ObjectsAreEqual -> reflect.DeepEqual for non-[]byte types,
	// which recursively walks the context's UNEXPORTED fields via
	// reflect.Value.IsNil() on that very same memory — a genuine data race
	// against that goroutine, reproduced here:
	//   WARNING: DATA RACE
	//   Write at ... by chromedp.NewContext.func1 (context.(*valueCtx).Done)
	//   Previous read at ... by reflect.Value.IsNil (via testify's
	//   ObjectsAreEqual -> reflect.DeepEqual, called from this test's
	//   require.NotEqual)
	// The fix: never deep-compare a context.Context's internals at all —
	// this test only ever wants OBJECT IDENTITY ("is this the SAME tab's
	// context"), which the language's own `==`/`!=` on the context.Context
	// interface already gives for free: chromedp.NewContext's returned
	// value is ultimately a *context.valueCtx wrapping a *context.cancelCtx
	// (both pointer types), so `==`/`!=` is a plain, race-free pointer
	// comparison — it never dereferences into the struct's fields the way
	// reflect.DeepEqual does, so the background goroutine's writes are
	// never observed by this comparison at all.
	secondCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.True(
		t,
		firstCtx != secondCtx,
		"Session must follow the active tab, not stay pinned to the first tab created",
	)

	// Switching back to tab 0 makes Session() return the first tab's ctx again.
	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	thirdCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, firstCtx == thirdCtx, "Session must follow SwitchTab back to tab 0")
}

// FR-060: the gate that used to be a tab CAP is now live memory, at the same
// site (createFirstTab). The refusal names memory and a remedy, and names no
// limit and no config key — there is none to raise (ADR-075 D1.5a).
func TestSession_MemoryPressure_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	// A second, DIFFERENT browsing context (session ID) must be refused while
	// the machine is under memory pressure.
	_, err = m.Session("another-session")
	require.Error(t, err)
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)
	assert.Contains(t, err.Error(), "memory")
	assert.Contains(t, err.Error(), "browser_close_tab")
	assert.NotContains(t, strings.ToLower(err.Error()), deletedTabCapConfigKey,
		"the refusal must not name a setting this build no longer has")
	assert.NotContains(t, strings.ToLower(err.Error()), "limit reached")
}

// --- ADR-041 fix F1: Session/OpenTab/CloseTab must not race to
// independently create (and leak) the first tab of a not-yet-existing
// browsing context ---

// TestSession_ConcurrentWithOpenTab_NoOrphanedTabs is the F1 regression
// guard: before the fix, OpenTab (and CloseTab's last-tab-replacement) did
// NOT register in m.pending the way Session() did, so a human's "+ new tab"
// (OpenTab) racing the agent's next Session() call for the SAME brand-new
// sessionID could each independently call createTab (unlocked) and then
// blindly overwrite m.sessions[sessionID] — whichever finished last won,
// silently discarding (leaking: never canceled, never counted by
// totalTabCountLocked, never reachable again) the other's freshly-created
// tab.
//
// The invariant this asserts — every physically-created tab is EITHER
// tracked in the surviving tab set OR explicitly canceled, with nothing
// in-between — holds regardless of goroutine scheduling: OpenTab calls that
// happen to observe the browsing context as not-yet-existing correctly
// converge on ONE shared first tab via createFirstTab's m.pending dedup
// (same as Session()); OpenTab calls that happen to observe it as
// already-existing correctly append their OWN additional tab (that's
// OpenTab's actual contract, not a bug) — so the raw tab COUNT is
// scheduling-dependent and not a safe thing to pin exactly, but "nothing
// vanishes without being tracked or canceled" always must hold.
func TestSession_ConcurrentWithOpenTab_NoOrphanedTabs(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}

	fn, canceled := fakeTabFactory()
	var created int32
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		atomic.AddInt32(&created, 1)
		return fn(allocCtx, targetID)
	}

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = m.Session(testSessionID)
			} else {
				_, errs[i] = m.OpenTab(testSessionID)
			}
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i], "no concurrent Session/OpenTab call for a brand-new session should error")
	}

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	require.True(t, ok, "the browsing context must exist after the race")
	survivingTabs := len(se.tabs)
	total := m.totalTabCountLocked()
	m.mu.Unlock()

	assert.Equal(t, survivingTabs, total,
		"totalTabCountLocked must exactly match the reachable tab count — no undercount from an "+
			"orphaned overwrite")

	createdN := atomic.LoadInt32(&created)
	canceledN := atomic.LoadInt32(canceled)
	assert.EqualValues(t, createdN, int32(survivingTabs)+canceledN,
		"ADR-041 fix F1: every physically-created tab must be EITHER tracked in the surviving "+
			"sessionEntry OR explicitly canceled — never orphaned (created, then silently discarded by "+
			"a blind m.sessions[id]=... overwrite, with its chromedp context and goroutine leaked forever)")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, survivingTabs)
	assert.GreaterOrEqual(t, survivingTabs, 1)
}
