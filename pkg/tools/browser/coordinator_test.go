package browser

// Coordinator invariant tests (ADR-043 spec TDD plan). Guard the load-bearing
// invariants the grill surfaced: CRIT-002/C1 (manager.Shutdown drops the
// connection, never the Chrome process or the agent's context), M2 (one Chrome
// for N agents, each in its own context), and FR-008 (coordinator.Shutdown is
// the sole process kill). Integration tests — they launch a real Chrome via the
// coordinator, so they skip when no managed binary can be obtained.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
		if home, err := os.UserHomeDir(); err == nil {
			installRoot := filepath.Join(home, ".omnipus", "browser", "chromium")
			if bin := findInstalledBinary(installRoot, "linux64"); bin != "" {
				sharedTestBin = bin
				return
			}
		}
		// Else download once into a PROCESS-LIFETIME temp dir. This must NOT use
		// t.TempDir(): that dir is deleted as soon as the FIRST test to trigger
		// this sync.Once returns, but sharedTestBin is cached and reused by every
		// later coordinator test — which would then resolve a deleted path
		// ("cannot locate chromium: no such file", a -p1-deterministic /
		// -p4-order-dependent failure). os.MkdirTemp persists for the whole test
		// process and is reclaimed by the OS / CI worker teardown on exit.
		var dir string
		dir, errSharedTestBin = os.MkdirTemp("", "coordinator-shared-chromium-")
		if errSharedTestBin != nil {
			return
		}
		sharedTestBin, errSharedTestBin = EnsureChromium(context.Background(), filepath.Join(dir, "chromium"))
	})
	if errSharedTestBin != nil {
		t.Skipf("no managed Chrome for coordinator test: %v", errSharedTestBin)
	}
	return sharedTestBin
}

func newCoordinatorTestConfig(t *testing.T) (BrowserConfig, string) {
	t.Helper()
	home := t.TempDir()
	return BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30_000_000_000, // 30s
		MaxTabs:     5,
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
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-a")
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, "agent-b")

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
	// CRIT-001: both agents drive the SAME shared root chromedp context (their
	// tab contexts are children of it, multiplexed over one CDP pipe) — there is
	// no per-agent ws:// URL anymore.
	if rootA == nil || rootB == nil || rootA != rootB {
		t.Fatalf("both agents should share the SAME coordinator root context; got A=%v B=%v", rootA, rootB)
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
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, "agent-a")

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
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, "agent-a")
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

// Ownership marker round-trip (M2 primitive): the coordinator can write+read
// its own marker; a stale/foreign pid is detected as not-alive.
func TestCoordinator_OwnershipMarker_RoundTrip(t *testing.T) {
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
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

// fakePipeLaunch returns a coordinator pipeLauncher stand-in that never spawns
// real Chrome: it hands back a cancelable root context and a *exec.Cmd whose
// Process.Pid is the test process's own (guaranteed-alive) pid, so the ownership
// marker records a LIVE owner and PID() reports it. browser is nil, so the
// coordinator's crash detector is not armed.
func fakePipeLaunch() func(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error) {
	return func(_ context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
		rootCtx, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{
			rootCtx: rootCtx,
			cancel:  cancel,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
			browser: nil,
		}, nil
	}
}

// TestCoordinator_LockfileSingleLaunch proves the CRIT-001 single-launch
// guarantee is enforced by the O_EXCL/flock lockfile (NOT the removed port
// bind), and that ownership is proven WITHOUT a port via the marker: two
// coordinators sharing one profile dir contend on the same lockfile — exactly
// one wins, and the second detects the live owner (via its marker pid) and is
// rejected. Hermetic: a fake launcher, no real Chrome.
func TestCoordinator_LockfileSingleLaunch(t *testing.T) {
	home := t.TempDir()
	// A dummy executable so execPath.resolve() short-circuits (no download); the
	// fake launcher ignores it — it never spawns Chrome.
	execPath := filepath.Join(home, "fake-chrome")
	writeExecutable(t, execPath, "#!/bin/sh\nexit 0\n")
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30_000_000_000,
		MaxTabs:     5,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
		ExecPath:    execPath,
	}

	coord1 := NewBrowserCoordinator(home, cfg, 30)
	coord1.pipeLauncher = fakePipeLaunch()
	coord2 := NewBrowserCoordinator(home, cfg, 30)
	coord2.pipeLauncher = fakePipeLaunch()

	// coord1 wins the lock and "launches".
	if err := coord1.ensureLaunched(context.Background()); err != nil {
		t.Fatalf("coord1 ensureLaunched should succeed (first to take the lock): %v", err)
	}
	if coord1.PID() != os.Getpid() {
		t.Fatalf("coord1 should report the launched pid; got %d, want %d", coord1.PID(), os.Getpid())
	}

	// coord2 MUST be rejected — the lock is held by coord1, and coord1's marker
	// names a live pid (ownership proven without a port). No second Chrome.
	err := coord2.ensureLaunched(context.Background())
	if err == nil {
		t.Fatal("coord2 ensureLaunched must FAIL — coord1 holds the single-launch lock")
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Fatalf("coord2 error should explain the held launch lock; got %v", err)
	}
	if coord2.PID() != 0 {
		t.Fatalf("coord2 must NOT have launched a second Chrome; got pid %d", coord2.PID())
	}

	// Releasing coord1's lock (Shutdown) lets coord2 launch.
	coord1.Shutdown()
	if coord1.PID() != 0 {
		t.Fatalf("coord1.PID() should be 0 after Shutdown; got %d", coord1.PID())
	}
	if err := coord2.ensureLaunched(context.Background()); err != nil {
		t.Fatalf("coord2 should launch after coord1 releases the lock: %v", err)
	}
	if coord2.PID() != os.Getpid() {
		t.Fatalf("coord2 should now report the launched pid; got %d", coord2.PID())
	}
	t.Cleanup(func() { coord2.Shutdown() })
}

// TestManager_RootContext_SharedAcrossAgents proves the CRIT-002
// (live-browser-video-streaming) RootContext accessor end to end, hermetically
// (a fake pipeLauncher — no real Chrome, mirrors TestCoordinator_LockfileSingleLaunch):
//  1. A manager with no coordinator attached (managed-mode/remote-CDP
//     fallback) reports ok=false, ctx=nil.
//  2. A manager attached to a coordinator that hasn't launched Chrome yet
//     ALSO reports ok=false — there is no shared root to hand out before a
//     launch.
//  3. Once the coordinator has launched, RootContext returns ok=true with a
//     non-nil context, and a SECOND, independently-attached manager on the
//     SAME coordinator observes the IDENTICAL context — proving the root is
//     shared across every agent on one coordinator, not per-manager — and it
//     is exactly the coordinator's own RootContext() value (the manager
//     accessor is a pure delegate).
func TestManager_RootContext_SharedAcrossAgents(t *testing.T) {
	// (1) No coordinator attached at all.
	noCoordMgr := &BrowserManager{}
	if ctx, ok := noCoordMgr.RootContext(); ok || ctx != nil {
		t.Fatalf("expected ok=false, ctx=nil with no coordinator attached; got ok=%v ctx=%v", ok, ctx)
	}

	home := t.TempDir()
	execPath := filepath.Join(home, "fake-chrome")
	writeExecutable(t, execPath, "#!/bin/sh\nexit 0\n")
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30_000_000_000,
		MaxTabs:     5,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
		ExecPath:    execPath,
	}

	coord := NewBrowserCoordinator(home, cfg, 30)
	coord.pipeLauncher = fakePipeLaunch()

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-a")

	// (2) Coordinator wired but Chrome not yet launched — still ok=false.
	if ctx, ok := mgrA.RootContext(); ok || ctx != nil {
		t.Fatalf("expected ok=false before the coordinator has launched Chrome; got ok=%v ctx=%v", ok, ctx)
	}

	// Launch hermetically — no real Chrome (fakePipeLaunch never spawns one).
	if err := coord.ensureLaunched(context.Background()); err != nil {
		t.Fatalf("ensureLaunched: %v", err)
	}
	t.Cleanup(func() { coord.Shutdown() })

	// (3) Now RootContext resolves for agent A...
	ctxA, ok := mgrA.RootContext()
	if !ok || ctxA == nil {
		t.Fatalf("expected ok=true with a non-nil context once Chrome is live; got ok=%v ctx=%v", ok, ctxA)
	}

	// ...and a SECOND manager on the SAME coordinator sees the IDENTICAL
	// shared root context (CRIT-002: not a per-agent/per-manager context).
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, "agent-b")
	ctxB, ok := mgrB.RootContext()
	if !ok || ctxB == nil {
		t.Fatalf("expected ok=true for a second manager on the same coordinator; got ok=%v ctx=%v", ok, ctxB)
	}
	if ctxA != ctxB {
		t.Fatal("RootContext must return the SAME shared root context for every agent on one coordinator")
	}

	// Sanity: BrowserManager.RootContext is a pure delegate to the
	// coordinator's own accessor.
	coordCtx, coordOK := coord.RootContext()
	if !coordOK || coordCtx != ctxA {
		t.Fatalf(
			"BrowserManager.RootContext must delegate to coordinator.RootContext; coord ok=%v ctx=%v want %v",
			coordOK,
			coordCtx,
			ctxA,
		)
	}
}

// TestManager_NoCoordinator_ManagedPipeLaunch exercises the MIGRATED
// no-coordinator managed-mode fallback with a REAL Chrome: ensureStarted launches
// Chrome over the CDP pipe (CRIT-001, no TCP port), bootstrapBrowserCtx creates a
// tab context as a plain child of the pipe root (no WithExistingBrowserContext,
// since m.browserCtxID is empty here), a real navigation succeeds, and Shutdown
// tears down this manager's own Chrome via m.allocCancel (cdppipe's cancel).
func TestManager_NoCoordinator_ManagedPipeLaunch(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, _ := newCoordinatorTestConfig(t) // cfg.ExecPath = the managed test binary
	mgr := newTestManager(t, cfg)         // NO AttachSharedChrome → no-coordinator managed pipe path
	t.Cleanup(mgr.Shutdown)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body><h1>NO-COORD-PIPE-MARKER</h1></body></html>"))
	}))
	t.Cleanup(srv.Close)

	tabCtx, err := mgr.Session(defaultSessionID)
	if err != nil {
		t.Fatalf("Session (no-coordinator managed pipe launch): %v", err)
	}
	navCtx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancel()
	var body string
	if err := chromedp.Run(
		navCtx,
		chromedp.Navigate(srv.URL),
		chromedp.WaitVisible("h1", chromedp.ByQuery),
		chromedp.Text("body", &body, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate/read over the no-coordinator managed pipe: %v", err)
	}
	if !strings.Contains(body, "NO-COORD-PIPE-MARKER") {
		t.Fatalf("expected the served marker; got %q", body)
	}
}

// TestManagedExecOpts_HeadfulPipeForVideoCapable proves the MAJ-001 launch-args
// contract: video-capable launches are HEADFUL (no --headless, a --window-size,
// DISPLAY in env) and non-video-capable launches preserve the headless-shell
// path — and NEITHER carries a --remote-debugging-port (CDP is over the pipe).
func TestManagedExecOpts_HeadfulPipeForVideoCapable(t *testing.T) {
	cfg := BrowserConfig{ProfileDir: filepath.Join(t.TempDir(), "profile")}

	hasFlag := func(args []string, want string) bool {
		for _, a := range args {
			if a == want {
				return true
			}
		}
		return false
	}
	hasPrefix := func(args []string, prefix string) bool {
		for _, a := range args {
			if strings.HasPrefix(a, prefix) {
				return true
			}
		}
		return false
	}

	// Video-capable: full Chrome in NEW headless mode (--headless=new) +
	// SwiftShader software rendering, NO DISPLAY (no Xvfb/X server), no port.
	// This is Playwright's proven headless-video approach (--headless +
	// --remote-debugging-pipe + swiftshader); the earlier headful-on-Xvfb design
	// failed over the CDP pipe ("no browser is open -32000").
	video := managedExecAllocatorOpts(cfg, managedLaunchParams{VideoCapable: true, Display: ":99"})
	if !hasFlag(video.Args, "--headless") {
		t.Errorf("video-capable launch must run headless (full Chrome, new headless renderer); got %v", video.Args)
	}
	if !hasFlag(video.Args, "--enable-unsafe-swiftshader") {
		t.Errorf("video-capable launch must enable SwiftShader software rendering; got %v", video.Args)
	}
	if !hasFlag(video.Args, "--window-size=1280,720") {
		t.Errorf("video-capable launch must set --window-size=1280,720; got %v", video.Args)
	}
	for _, e := range video.Env {
		if strings.HasPrefix(e, "DISPLAY=") {
			t.Errorf("video-capable launch must NOT set DISPLAY (new-headless renders offscreen); got %v", video.Env)
		}
	}
	if hasPrefix(video.Args, "--remote-debugging-port") {
		t.Errorf("video-capable launch must NOT set --remote-debugging-port (CDP is over the pipe); got %v", video.Args)
	}

	// Non-video-capable: headless-shell preserved, no DISPLAY, no port.
	headless := managedExecAllocatorOpts(cfg, managedLaunchParams{})
	if !hasFlag(headless.Args, "--headless") {
		t.Errorf("non-video-capable launch must preserve the headless-shell path (--headless); got %v", headless.Args)
	}
	if hasPrefix(headless.Args, "--remote-debugging-port") {
		t.Errorf(
			"non-video-capable launch must NOT set --remote-debugging-port (CDP is over the pipe); got %v",
			headless.Args,
		)
	}
	for _, e := range headless.Env {
		if strings.HasPrefix(e, "DISPLAY=") {
			t.Errorf("non-video-capable launch must NOT set DISPLAY; got %v", headless.Env)
		}
	}
}
