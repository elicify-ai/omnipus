package browser

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
