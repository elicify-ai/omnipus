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

// The chrome-headless-shell resolver that used to live here is DELETED with
// d2_spike_test.go, its only caller. It existed to obtain the old-headless
// binary the D2 spike needed to prove CDP browser-context isolation — a
// mechanism ADR-075 FR-031 retired outright. Nothing in this package needs a
// specific Chrome BUILD any more; resolveTestBinary's "whichever is installed"
// answer is the right one for every remaining test.

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

// M2, re-scoped by ADR-075: two agents on ONE browsing key Register against
// that key's coordinator and get exactly one Chrome (one PID) and the SAME
// underlying *chromedp.Browser.
//
// The original name was TestCoordinator_TwoAgents_OneChrome_TwoContexts and
// the original third assertion was "two DISTINCT browser context ids". That
// assertion is deleted, not weakened: FR-031 removed CDP browser contexts, so
// there is no id to compare, and per-agent isolation is no longer a thing the
// product offers or claims. Isolation is per WORKSPACE now, and it is proved
// by TestPool_TwoWorkspaces_TwoChromes against two Chrome PROCESSES with two
// --user-data-dir paths — a boundary this test never had.
func TestCoordinator_TwoAgentsOnOneKey_ShareOneChrome(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-a"))
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, browserTestKey("agent-b"))

	rootA, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register A: %v", err)
	}
	rootB, err := coord.Register(context.Background(), "agent-b", mgrB)
	if err != nil {
		t.Fatalf("Register B: %v", err)
	}
	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a live Chrome pid after Register")
	}
	// CRIT-001: both agents drive chromedp CHILD contexts of the SAME shared
	// rootCtx (one CDP pipe, multiplexed). Assert that identity directly via
	// the underlying *chromedp.Browser pointer (rootA/rootB themselves are
	// distinct context.Context values — each Register call wraps a fresh
	// child — but both must resolve to the one shared Browser).
	brA := chromedp.FromContext(rootA)
	brB := chromedp.FromContext(rootB)
	if brA == nil || brB == nil || brA.Browser == nil || brB.Browser == nil {
		t.Fatal("expected both Register calls to return a context bound to a live *chromedp.Browser")
	}
	if brA.Browser != brB.Browser {
		t.Fatal("both agents on one key should share the SAME underlying Chrome (*chromedp.Browser)")
	}
	t.Cleanup(func() { coord.Shutdown() })
}

// CRIT-002 / C1, re-scoped to THE KEY'S OWN CHROME: a manager.Shutdown()
// drops only that manager's connection — it must NOT kill the workspace's
// Chrome process. What makes a login survive the reload is the workspace's
// profile directory on disk (FR-043), not a per-agent CDP context, so the
// context-count half of the original assertion is deleted with the mechanism
// it measured.
func TestManager_Shutdown_DropsConnectionNotProcess(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))

	if _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pidBefore := coord.PID()
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
	if coord.KillCount() != 0 {
		t.Fatalf("manager.Shutdown() must not register a Chrome kill; KillCount=%d", coord.KillCount())
	}
	t.Cleanup(func() { coord.Shutdown() })
}

// FR-008, re-scoped to THE KEY'S OWN CHROME: coordinator.Shutdown() is the
// SOLE kill path for the Chrome this key owns. After it that key's pid is gone
// and KillCount==1. It says nothing about any other key's Chrome — under
// ADR-075 there are N of them, and TestPool_CrashIsContained is what proves
// one going down leaves the others up.
func TestCoordinator_Shutdown_IsSoleKill(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))
	if _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
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
// DELETED with ADR-075 FR-031, and deliberately not replaced:
//
//   - TestBrowserCoordinator_CaptureSharedContext_ConfigAndEnvOverride
//   - TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID
//
// Both exercised the retired shared-context config knob and its env
// override. The knob, the override and the whole CDP-browser-context
// mechanism they selected between
// are gone: every session now bootstraps into Chrome's DEFAULT context, which
// is the only one chrome.tabCapture can reach, and isolation moved down to
// one Chrome process and one profile directory per workspace.
//
// The surviving question — "is a CDP browser context ever created at all?" —
// is answered structurally by TestNoCDPBrowserContextIsEverCreated
// (no_residual_test.go), which cannot be satisfied by a passing runtime
// assertion against a build that reintroduced the branch.
// ---------------------------------------------------------------------------

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
