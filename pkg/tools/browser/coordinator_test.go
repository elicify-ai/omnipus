package browser

// Coordinator invariant tests (ADR-043 spec TDD plan). Guard the load-bearing
// invariants the grill surfaced: CRIT-002/C1 (manager.Shutdown drops the
// connection, never the Chrome process or the agent's context), M2 (one Chrome
// for N agents, each in its own context), and FR-008 (coordinator.Shutdown is
// the sole process kill). Integration tests — they launch a real Chrome via the
// coordinator, so they skip when no managed binary can be obtained.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// sharedTestBinary resolves the Chrome binary ONCE for all coordinator tests,
// preferring an already-installed managed Chrome (avoids a ~130 MB re-download
// per test on a disk-constrained devpod). Falls back to EnsureChromium.
var (
	sharedTestBinOnce sync.Once
	sharedTestBin     string
	errSharedTestBin  error
)

func resolveTestBinary(t *testing.T) string {
	t.Helper()
	sharedTestBinOnce.Do(func() {
		// Prefer the gateway's real managed install (~/.omnipus/browser/chromium).
		//
		// platform is derived from cftPlatform() (installer.go), NOT
		// hardcoded to "linux64" — the hardcoded value silently made this
		// resolver Linux-only: on macOS a real managed install lives under
		// mac-arm64/mac-x64, so findInstalledBinary("linux64") always missed
		// it and every darwin run fell through to a fresh ~130 MB download
		// (#615/#617/#618 hardening review, F1). A cftPlatform() error means
		// an unsupported platform; fall through to the download attempt
		// below, which will itself fail with a clear message for
		// resolveTestBinary's t.Skipf to surface.
		if home, err := os.UserHomeDir(); err == nil {
			if platform, perr := cftPlatform(); perr == nil {
				installRoot := filepath.Join(home, ".omnipus", "browser", "chromium")
				if bin := findInstalledBinary(installRoot, platform); bin != "" {
					sharedTestBin = bin
					return
				}
			}
		}
		// Else download once into a STABLE shared dir. Deliberately NOT
		// t.TempDir(): that dir is removed when the FIRST test to hit this
		// sync.Once finishes, leaving every later test with a dangling
		// exec_path ("no such file or directory" — seen on the ci-omnipus
		// worker, where Chrome downloads succeed and these tests really
		// run). A fixed os.TempDir() path also lets repeat CI runs reuse
		// the ~130 MB install instead of re-downloading each run.
		sharedTestBin, errSharedTestBin = EnsureChromium(
			context.Background(), filepath.Join(os.TempDir(), "omnipus-shared-test-chromium"),
		)
	})
	if errSharedTestBin != nil {
		t.Skipf("no managed Chrome for coordinator test: %v", errSharedTestBin)
	}
	return sharedTestBin
}

// sharedTestBinaryHeadlessShell resolves chrome-headless-shell SPECIFICALLY,
// once, for tests that need old-headless CDP semantics rather than whichever
// build resolveTestBinary's "detect either, prefer full chrome" resolution
// (findInstalledBinary) happens to find. See resolveTestBinaryHeadlessShell's
// doc comment for why that distinction is load-bearing, not cosmetic.
var (
	sharedTestBinHeadlessShellOnce sync.Once
	sharedTestBinHeadlessShell     string
	errSharedTestBinHeadlessShell  error
)

// resolveTestBinaryHeadlessShell resolves chrome-headless-shell specifically
// — never the full "chrome" build resolveTestBinary/findInstalledBinary
// prefer by default on Linux (installer.go's dual-download: WebRTC
// tabCapture needs full Chrome, so the shared install root prefers it once
// present). D2Spike needs the OTHER build: it drives chromedp's classic
// TCP-debug-port ExecAllocator (chromedp.NewExecAllocator, not this
// package's own cdppipe launch path) to prove pure CDP
// Target.createBrowserContext isolation — a property "old headless" mode
// (chrome-headless-shell, a dedicated binary precisely because Chrome split
// old-headless out of the main "chrome" build) supports, but empirically
// (verified against Chrome 151 here — a full chromeHardeningBaseFlags()
// pass-through plus a settle delay before the second context reproduces the
// SAME failure, ruling out both a missing-flag and a timing-race
// explanation) full "chrome"'s New Headless mode does not: creating a SECOND
// isolated browser context and navigating in it reliably errors CDP -32000
// "no browser is open". Reuses installer.go's per-build resolver
// (EnsureChromiumBuild) so an already-cached chrome-headless-shell install
// (this codebase's managed root, or resolveTestBinary's own temp-dir
// fallback from an earlier pre-dual-download install) is found without a
// network hit; only downloads if genuinely absent.
func resolveTestBinaryHeadlessShell(t *testing.T) string {
	t.Helper()
	sharedTestBinHeadlessShellOnce.Do(func() {
		// See resolveTestBinary's matching comment: platform comes from
		// cftPlatform(), not a hardcoded "linux64" (F1, #615/#617/#618
		// hardening review) — otherwise this resolver never finds a real
		// managed install on macOS.
		if home, err := os.UserHomeDir(); err == nil {
			if platform, perr := cftPlatform(); perr == nil {
				installRoot := filepath.Join(home, ".omnipus", "browser", "chromium")
				if bin := findInstalledBuild(installRoot, platform, headlessShellBuild()); bin != "" {
					sharedTestBinHeadlessShell = bin
					return
				}
			}
		}
		sharedTestBinHeadlessShell, errSharedTestBinHeadlessShell = EnsureChromiumBuild(
			context.Background(), filepath.Join(t.TempDir(), "chromium-headless-shell"), headlessShellBuild(),
		)
	})
	if errSharedTestBinHeadlessShell != nil {
		t.Skipf("no managed chrome-headless-shell for D2 spike: %v", errSharedTestBinHeadlessShell)
	}
	return sharedTestBinHeadlessShell
}

func newCoordinatorTestConfig(t *testing.T) (BrowserConfig, string) {
	t.Helper()
	home := t.TempDir()
	return BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30_000_000_000, // 30s
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
		ExecPath:    resolveTestBinary(t),
	}, home
}

func newTestManager(t *testing.T, cfg BrowserConfig) *BrowserManager {
	t.Helper()
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("NewBrowserManager: %v", err)
	}
	return mgr
}

// M2: two agents Register against one coordinator → exactly one Chrome (one
// PID), two DISTINCT browser contexts (per-agent isolation).
func TestCoordinator_TwoAgents_OneChrome_TwoContexts(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-a"))
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, browserTestKey("agent-b"))

	rootA, ctxA, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register A: %v", err)
	}
	rootB, ctxB, err := coord.Register(context.Background(), "agent-b", mgrB)
	if err != nil {
		t.Fatalf("Register B: %v", err)
	}
	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a live Chrome pid after Register")
	}
	// CRIT-001: there is no per-agent URL anymore — both agents drive chromedp
	// CHILD contexts of the SAME shared rootCtx (one CDP pipe, multiplexed).
	// Assert that identity directly via the underlying *chromedp.Browser
	// pointer (rootA/rootB themselves are distinct context.Context values —
	// each Register call wraps a fresh child — but both must resolve to the
	// one shared Browser).
	brA := chromedp.FromContext(rootA)
	brB := chromedp.FromContext(rootB)
	if brA == nil || brB == nil || brA.Browser == nil || brB.Browser == nil {
		t.Fatal("expected both Register calls to return a context bound to a live *chromedp.Browser")
	}
	if brA.Browser != brB.Browser {
		t.Fatal("both agents should share the SAME underlying Chrome (*chromedp.Browser)")
	}
	if ctxA == "" || ctxB == "" || ctxA == ctxB {
		t.Fatalf("expected two distinct non-empty browser context ids; got A=%q B=%q", ctxA, ctxB)
	}
	if coord.contextCount() != 2 {
		t.Fatalf("expected contextCount==2; got %d", coord.contextCount())
	}
	t.Cleanup(func() { coord.Shutdown() })
}

// CRIT-002 / C1: a per-agent manager.Shutdown() drops only that manager's
// connection — it must NOT kill the Chrome process and must NOT dispose the
// agent's browser context (the coordinator owns both; the context persists so a
// reload re-adopts it and login survives).
func TestManager_Shutdown_DropsConnectionNotProcess(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))

	if _, _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pidBefore := coord.PID()
	ctxBefore := coord.contextCount()
	if pidBefore == 0 {
		t.Fatal("expected a live Chrome pid")
	}

	// This is the exact call the hot-reload path makes (loop.go prior.Shutdown()).
	mgr.Shutdown()

	if coord.PID() != pidBefore {
		t.Fatalf(
			"CRIT-002/C1 VIOLATION: manager.Shutdown() killed the Chrome process (pid %d → %d)",
			pidBefore,
			coord.PID(),
		)
	}
	if coord.contextCount() != ctxBefore {
		t.Fatalf(
			"CRIT-002 VIOLATION: manager.Shutdown() changed the agent's browser context count (%d → %d) — context must persist for reload re-adoption",
			ctxBefore,
			coord.contextCount(),
		)
	}
	if coord.KillCount() != 0 {
		t.Fatalf("manager.Shutdown() must not register a Chrome kill; KillCount=%d", coord.KillCount())
	}
	t.Cleanup(func() { coord.Shutdown() })
}

// FR-008: coordinator.Shutdown() is the SOLE process-kill path. After it the
// pid is gone and KillCount==1.
func TestCoordinator_Shutdown_IsSoleKill(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))
	if _, _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if coord.PID() == 0 {
		t.Fatal("expected a live Chrome pid before Shutdown")
	}

	coord.Shutdown()

	if coord.PID() != 0 {
		t.Fatalf("expected pid 0 after coordinator.Shutdown(); got %d", coord.PID())
	}
	if coord.KillCount() != 1 {
		t.Fatalf("expected KillCount==1 after Shutdown; got %d", coord.KillCount())
	}
}

// ---------------------------------------------------------------------------
// ADR-048 fix-wave: captureSharedContextResolved/SetCaptureSharedContext
// (condition-1 config knob) helper-contract coverage. Fast, no-Chrome unit
// coverage — the coordinator fields these tests read/write are exercised via
// direct method calls (same package), never a real Register()/Chrome launch.
// (The condition-2 multi-agent capture fence itself no longer reads a
// coordinator-level signal here — BrowserCoordinator.LiveSessionAgents was
// removed as dead code once browser_webrtc.go's fence moved to the
// per-session captureRegistry/ViewerCount()-based check; see ADR-048's
// "actively-viewed-only deny + supersede" re-scoping.)
// ---------------------------------------------------------------------------

// TestBrowserCoordinator_CaptureSharedContext_ConfigAndEnvOverride proves
// ADR-048 condition 1's promotion from OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT
// to config: SetCaptureSharedContext drives the resolved value, and the env
// var — when non-empty — is an explicit override that wins regardless of
// the configured value, in EITHER direction. CaptureSharedContextEnabled
// (the capability classifier's read path) and captureSharedContextResolved
// (Register's own read path) must never disagree.
func TestBrowserCoordinator_CaptureSharedContext_ConfigAndEnvOverride(t *testing.T) {
	coord := NewBrowserCoordinator(t.TempDir(), BrowserConfig{})

	if coord.CaptureSharedContextEnabled() {
		t.Fatal(
			"a freshly-constructed coordinator must default to shared-context capture DISABLED (config drives it explicitly, no implicit default)",
		)
	}

	coord.SetCaptureSharedContext(true)
	if !coord.CaptureSharedContextEnabled() {
		t.Fatal("CaptureSharedContextEnabled() must reflect SetCaptureSharedContext(true)")
	}
	if !coord.captureSharedContextResolved() {
		t.Fatal("captureSharedContextResolved() must agree with CaptureSharedContextEnabled()")
	}

	t.Setenv("OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT", "0")
	if coord.CaptureSharedContextEnabled() {
		t.Fatal("a non-empty env override (\"0\") must win over the configured true value")
	}

	coord.SetCaptureSharedContext(false)
	t.Setenv("OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT", "1")
	if !coord.CaptureSharedContextEnabled() {
		t.Fatal("a non-empty env override (\"1\") must win over the configured false value")
	}
}

// TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID
// is QA regression-wave item 3's missing direction: fix-wave A's
// TestBrowserCoordinator_CaptureSharedContext_ConfigAndEnvOverride only
// covers CaptureSharedContextEnabled()/captureSharedContextResolved() (the
// config/env resolution logic) — it never calls Register() at all, so
// NEITHER of Register's two actual behaviors was proven. The flag-OFF
// direction (per-agent browser context created, distinct browserCtxIDs) is
// covered by TestCoordinator_TwoAgents_OneChrome_TwoContexts above; this
// test covers the flag-ON direction: Register must skip the per-agent CDP
// browser context entirely and hand back the coordinator's shared root
// context with an EMPTY browserCtxID (ADR-048 condition 1 — the agent's
// session then bootstraps in the DEFAULT browser context, which
// chrome.tabCapture can actually reach).
func TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	coord.SetCaptureSharedContext(true)
	t.Cleanup(func() { coord.Shutdown() })

	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-shared-ctx"))

	rootCtx, browserCtxID, err := coord.Register(context.Background(), "agent-shared-ctx", mgr)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if browserCtxID != "" {
		t.Fatalf(
			"Register in shared-context mode must return an EMPTY browserCtxID (default context), got %q",
			browserCtxID,
		)
	}

	realRoot, ok := coord.RootContext()
	if !ok {
		t.Fatal("expected a live root context after Register")
	}
	brRoot := chromedp.FromContext(realRoot)
	brReturned := chromedp.FromContext(rootCtx)
	if brRoot == nil || brReturned == nil || brRoot.Browser == nil || brReturned.Browser == nil {
		t.Fatal("expected both contexts to resolve to a live *chromedp.Browser")
	}
	if brRoot.Browser != brReturned.Browser {
		t.Fatal(
			"Register's returned rootCtx must resolve to the coordinator's OWN shared Browser (coord.rootCtx), not a per-agent one",
		)
	}
	if coord.contextCount() != 0 {
		t.Fatalf(
			"shared-context mode must NOT create a per-agent browser context; contextCount() = %d, want 0",
			coord.contextCount(),
		)
	}
}

// Ownership marker round-trip (M2 primitive): the coordinator can write+read
// its own marker; a stale/foreign pid is detected as not-alive. This never
// launches Chrome (writeOwnershipMarker/readOwnershipMarker/pidAlive are pure
// file/OS-signal logic), so it deliberately uses budgetTestConfig — NOT
// newCoordinatorTestConfig, whose ExecPath resolution can trigger a real
// Chrome-for-Testing download this test does not need (#615 finding: this
// call was previously ungated by testing.Short()/skipIfNoBrowser, so it ran —
// and could download — unconditionally in every CI gate).
func TestCoordinator_OwnershipMarker_RoundTrip(t *testing.T) {
	cfg, home := budgetTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	if err := coord.writeOwnershipMarker(999999, "Chrome-for-Testing"); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	pid, owner, err := coord.readOwnershipMarker()
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	// Owner is the fixed omnipus identity tag (ownershipMarkerOwner); the
	// product arg is stored separately. The marker's job is identity (ours vs
	// foreign) + pid, so assert the identity constant + the pid round-trip.
	if pid != 999999 || owner != ownershipMarkerOwner {
		t.Fatalf("marker round-trip mismatch: pid=%d owner=%q (want %q)", pid, owner, ownershipMarkerOwner)
	}
	if pidAlive(999999) {
		t.Fatal("pid 999999 should not be alive — foreign-marker detection relies on pidAlive")
	}
}

// ---------------------------------------------------------------------------
// Global tab-budget default change — operator directive: "remove the limit
// of 30 and keep infinite like chrome, but we keep the limit of 5 per agent".
// Pure unit tests over the coordinator's in-memory budget bookkeeping only
// — no Register call, no Chrome
// launch, matching coordinator_review_test.go's TestCoordinator_TabBudgetDenial
// pattern so these run even when no managed Chrome binary is available (e.g.
// offline CI).
// ---------------------------------------------------------------------------

// budgetTestConfig returns a minimal BrowserConfig for budget-only unit tests
// that never call Register/AttachSharedChrome — no ExecPath resolution (and
// therefore no Chrome download/lookup) needed.
func budgetTestConfig(t *testing.T) (BrowserConfig, string) {
	t.Helper()
	home := t.TempDir()
	return BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30_000_000_000, // 30s
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
	}, home
}
