// gateway_boot_browser_test.go: tests for browser warm boot at startup - the warmed tab and warm capture surfaces

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// --- moved from gateway_boot.go tests 2026-09-15 ---

// --- moved from gateway.go tests 2026-09-15 ---

// TestWatchWarmCaptureIdle_HandoverAfterUnwatchedRunResetsAdaptation is the F2
// guarantee: a viewer adopting a capture that has been encoding unwatched must
// not inherit whatever resolution that unwatched run settled on — and must get
// that guarantee WITHOUT a capture rebuild.
func TestWatchWarmCaptureIdle_HandoverAfterUnwatchedRunResetsAdaptation(t *testing.T) {
	// 0 means "any age qualifies", i.e. this handover is the
	// warmed-long-enough-to-have-adapted case.
	withWarmCaptureAdaptResetMinAge(t, 0)

	cs := newFakeWarmCapture()
	cs.viewers.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Hour, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned after a viewer attached")
	}
	if got := cs.recaptures.Load(); got != 1 {
		t.Fatalf("expected exactly ONE handover recapture so the viewer starts at full quality, got %d — "+
			"without it the first panel open keeps the resolution an unwatched, boot-contended warm-up settled on", got)
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("a capture a viewer is watching must never be stopped, got %d stop(s)", got)
	}
}

// TestWatchWarmCaptureIdle_HandoverBeforeAdaptationCouldHappenIsFree protects
// the measured win. A panel opened seconds after boot cannot have inherited an
// adaptation — the loop only starts on the PeerConnection's first 'connected'
// transition and needs two further 2s samples before it can step — so the
// handover must cost that viewer nothing at all.
func TestWatchWarmCaptureIdle_HandoverBeforeAdaptationCouldHappenIsFree(t *testing.T) {
	// An hour: no capture in this test has been warming that long, so every
	// handover here is the "arrived early" case.
	withWarmCaptureAdaptResetMinAge(t, time.Hour)

	cs := newFakeWarmCapture()
	cs.viewers.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Hour, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned after a viewer attached")
	}
	if got := cs.recaptures.Load(); got != 0 {
		t.Fatalf("a viewer who arrived before any adaptation was possible must not pay for a rebuild, got %d recapture(s) — "+
			"that rebuild is the boot-warm-up's whole latency win being spent on resetting nothing", got)
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("a capture a viewer is watching must never be stopped, got %d stop(s)", got)
	}
}

// TestWatchWarmCaptureIdle_IdleStopDoesNotRecapture — the OTHER exit. A
// capture nobody ever watched is stopped, not rebuilt: sending a recapture to
// an encoder page that is about to be torn down is pure waste, and would burn
// exactly the CPU the idle stop exists to release.
func TestWatchWarmCaptureIdle_IdleStopDoesNotRecapture(t *testing.T) {
	withWarmCaptureAdaptResetMinAge(t, 0)

	cs := newFakeWarmCapture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Millisecond, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned")
	}
	if got := cs.stops.Load(); got != 1 {
		t.Fatalf("expected the viewerless warm capture to be stopped exactly once, got %d", got)
	}
	if got := cs.recaptures.Load(); got != 0 {
		t.Fatalf("an idle-stopped capture must not be recaptured on the way out, got %d", got)
	}
}

// TestWarmCaptureIdleTimeout_NonPositiveMeansNeverStop pins the deliberate
// asymmetry: 0/negative is an operator opting OUT of the idle stop, NOT a
// request for the shipped default back.
func TestWarmCaptureIdleTimeout_NonPositiveMeansNeverStop(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, v := range []int{0, -1} {
		cfg.Tools.Browser.WarmCaptureIdleSec = v
		if got := warmCaptureIdleTimeout(cfg); got != 0 {
			t.Fatalf("warm_capture_idle_sec=%d → %s, want 0 (never idle-stop)", v, got)
		}
	}
	cfg.Tools.Browser.WarmCaptureIdleSec = 42
	if got, want := warmCaptureIdleTimeout(cfg), 42*time.Second; got != want {
		t.Fatalf("warm_capture_idle_sec=42 → %s, want %s", got, want)
	}
}

// TestPickWarmBrowserManager_PrefersTheDefaultWorkspace — omnipus-system-agent
// FR-126: a manager is a WORKSPACE's browser and there is no default agent, so
// the selection is the DEFAULT WORKSPACE. Beta is the default; alpha sorts
// first and must not be chosen.
func TestPickWarmBrowserManager_PrefersTheDefaultWorkspace(t *testing.T) {
	home := warmTestHome(t)
	coord := newWarmTestCoordinator(t)
	keyAlpha := warmTestWorkspace(t, home, "warmalpha", "ava")
	keyBeta := warmTestWorkspace(t, home, "warmbeta", "mia")
	keyGamma := warmTestWorkspace(t, home, "warmgamma", "jim")
	mgrs := []*browser.BrowserManager{
		newWarmTestManagerForKey(t, coord, keyAlpha),
		newWarmTestManagerForKey(t, coord, keyBeta),
		newWarmTestManagerForKey(t, coord, keyGamma),
	}
	markWarmTestDefaultWorkspace(t, home, "warmbeta")
	cfg := config.DefaultConfig()

	got, reason := pickWarmBrowserManager(cfg, home, mgrs)
	if reason != "" {
		t.Fatalf("expected a pick, got skip reason %q", reason)
	}
	if got == nil || got.BrowsingKey() != keyBeta {
		t.Fatalf("expected the DEFAULT WORKSPACE's browser (%s) to be warmed, got %q",
			keyBeta.String(), warmedKey(got))
	}
	// Not the sorted-first one. warmalpha sorts before warmbeta, so a
	// selection that quietly fell back to a lexicographic pick would land on
	// alpha and this assertion is what catches it.
	if got.BrowsingKey() == keyAlpha {
		t.Fatal("warmed the sorted-first workspace, not the default workspace's")
	}
}

// TestPickWarmBrowserManager_DeterministicWithoutADefault — there is no
// lexicographic fallback: with no default workspace there is no ONE workspace
// to warm, and choosing one would start a Chrome against one particular set of
// live logins because its id sorted first, unasked. The
// requirement the old name carried — that two boots of one install never
// disagree — is now trivially satisfied, and is still asserted by repetition
// so a future map-order-dependent pick cannot pass by luck.
func TestPickWarmBrowserManager_DeterministicWithoutADefault(t *testing.T) {
	home := warmTestHome(t)
	coord := newWarmTestCoordinator(t)
	mgrs := []*browser.BrowserManager{
		newWarmTestManagerForKey(t, coord, warmTestWorkspace(t, home, "warmzed", "zed")),
		newWarmTestManagerForKey(t, coord, warmTestWorkspace(t, home, "warmava", "ava")),
		newWarmTestManagerForKey(t, coord, warmTestWorkspace(t, home, "warmmia", "mia")),
	}
	cfg := config.DefaultConfig()

	for i := 0; i < 25; i++ {
		got, reason := pickWarmBrowserManager(cfg, home, mgrs)
		if got != nil {
			t.Fatalf("iteration %d: warmed %q with no default workspace — a tie-break over workspaces "+
				"picks whose logins to start a browser against", i, warmedKey(got))
		}
		if !strings.Contains(reason, "no default workspace") {
			t.Fatalf("iteration %d: skip reason %q must name the missing default workspace", i, reason)
		}
	}
}

// TestPickWarmBrowserManager_SkipsUnwarmableManagers: a manager with no
// coordinator would launch its OWN second Chrome (legacy managed mode) if
// warmed, and one with a zero browsing key names no browser at all. Neither is
// a candidate — and a nil entry must not panic.
func TestPickWarmBrowserManager_SkipsUnwarmableManagers(t *testing.T) {
	home := warmTestHome(t)
	coord := newWarmTestCoordinator(t)
	keyRay := warmTestWorkspace(t, home, "warmray", "ray")
	keyOrphan := warmTestWorkspace(t, home, "warmorphan", "orphan")

	noCoordinator := newWarmTestManagerForKey(t, nil, keyOrphan)
	noKey := newWarmTestManagerForKey(t, coord, browser.BrowsingKey{})
	good := newWarmTestManagerForKey(t, coord, keyRay)

	cfg := config.DefaultConfig()
	markWarmTestDefaultWorkspace(t, home, "warmray")

	got, reason := pickWarmBrowserManager(cfg, home,
		[]*browser.BrowserManager{nil, noCoordinator, noKey, good})
	if got == nil || got.BrowsingKey() != keyRay {
		t.Fatalf("expected the only warmable manager (%s), got %q (reason %q)",
			keyRay.String(), warmedKey(got), reason)
	}

	// The default workspace is now the coordinator-less manager's: even
	// though a manager for it EXISTS in the list, it is unwarmable, so the
	// answer is still "nothing to warm" rather than a substitute.
	markWarmTestDefaultWorkspace(t, home, "warmorphan")
	if got, reason := pickWarmBrowserManager(cfg, home,
		[]*browser.BrowserManager{nil, noCoordinator, noKey}); got != nil {
		t.Fatalf("expected nil when nothing is warmable, got %q (reason %q)", warmedKey(got), reason)
	}
	if got, reason := pickWarmBrowserManager(cfg, home, nil); got != nil {
		t.Fatalf("expected nil for an empty manager list, got %q (reason %q)", warmedKey(got), reason)
	}
	if _, reason := pickWarmBrowserManager(cfg, home, nil); !strings.Contains(reason, "no workspace") {
		t.Fatalf("an empty manager list must say so, got %q", reason)
	}
}

// TestWatchWarmCaptureIdle_StopsACaptureNobodyEverWatched is the load-bearing
// one: CaptureSession's own grace-stop timer is armed by RemoveViewer, so a
// session that never had a viewer is never stopped by anything else. Without
// this watcher a boot-warmed capture encodes video for the process's entire
// lifetime — on a shared-CPU host, the very starvation the warm-up exists to
// avoid.
func TestWatchWarmCaptureIdle_StopsACaptureNobodyEverWatched(t *testing.T) {
	cs := newFakeWarmCapture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Millisecond, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned")
	}
	if cs.stops.Load() == 0 {
		t.Fatal("expected the viewerless warm capture to be STOPPED after its idle timeout — it would otherwise encode video forever")
	}
}

// TestWatchWarmCaptureIdle_HandsOverToAViewer: once a viewer is watching, the
// warm-up must get out of the way entirely — stopping a stream someone is
// actually watching would be a far worse bug than a slow first open.
func TestWatchWarmCaptureIdle_HandsOverToAViewer(t *testing.T) {
	cs := newFakeWarmCapture()
	cs.viewers.Store(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, time.Millisecond, "mia")
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("idle watcher never returned")
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("expected NO stop while a viewer is attached, got %d stop(s)", got)
	}
}

// TestWatchWarmCaptureIdle_ExitsOnShutdownAndOnAnEarlierStop — the watcher must
// not outlive the gateway, and must not fight another stopper (a superseding
// offer, a browser death).
func TestWatchWarmCaptureIdle_ExitsOnShutdownAndOnAnEarlierStop(t *testing.T) {
	t.Run("gateway shutdown", func(t *testing.T) {
		cs := newFakeWarmCapture()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			watchWarmCaptureIdle(ctx, cs, time.Hour, "mia")
		}()
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("idle watcher did not exit on context cancellation")
		}
		if got := cs.stops.Load(); got != 0 {
			t.Fatalf("shutdown must not double-stop the capture, got %d stop(s)", got)
		}
	})

	t.Run("already stopped by someone else", func(t *testing.T) {
		cs := newFakeWarmCapture()
		done := make(chan struct{})
		go func() {
			defer close(done)
			watchWarmCaptureIdle(context.Background(), cs, time.Hour, "mia")
		}()
		cs.Stop() // e.g. a superseding offer from another agent
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("idle watcher did not exit once the session was stopped elsewhere")
		}
		if got := cs.stops.Load(); got != 1 {
			t.Fatalf("expected exactly the one external stop, got %d", got)
		}
	})
}

// TestWatchWarmCaptureIdle_NeverStopIsRespected — warm_capture_idle_sec <= 0 is
// an explicit "leave it running" choice; the watcher must not stop it, and must
// not spin.
func TestWatchWarmCaptureIdle_NeverStopIsRespected(t *testing.T) {
	cs := newFakeWarmCapture()
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchWarmCaptureIdle(context.Background(), cs, 0, "mia")
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("watcher should return immediately when idle-stop is disabled")
	}
	if got := cs.stops.Load(); got != 0 {
		t.Fatalf("expected no stop when idle-stop is disabled, got %d", got)
	}
}

// TestStartBrowserWarmBoot_NoOpsWhenThereIsNothingToWarm — the whole warm-boot
// entry point must be inert (no goroutine, no panic, no nil deref) when it is
// disabled or when no agent has a browser manager. This is the path a
// browser-inert test harness and an operator opt-out both take.
func TestStartBrowserWarmBoot_NoOpsWhenThereIsNothingToWarm(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Browser.WarmTabAtBoot = false
	cfg.Tools.Browser.WarmCaptureAtBoot = false
	startBrowserWarmBoot(context.Background(), cfg, t.TempDir(), nil, nil) // nil agent loop: must not panic

	cfg = config.DefaultConfig()
	startBrowserWarmBoot(context.Background(), cfg, t.TempDir(), nil, nil)
}

// TestWaitForGatewayListener_ReportsAnUnreachableListener — the wait must give
// up on its budget rather than blocking boot's warm-up goroutine forever, and
// must report the failure so the caller can log that a warm tab may land on
// about:blank.
func TestWaitForGatewayListener_ReportsAnUnreachableListener(t *testing.T) {
	start := time.Now()
	// Port 1 on loopback: nothing listens there, and it is not a port any
	// test harness would bind.
	if waitForGatewayListener(context.Background(), 1, 200*time.Millisecond) {
		t.Fatal("expected waitForGatewayListener to report failure for a port nothing listens on")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("wait overran its budget by a wide margin: %s", elapsed)
	}
}

// TestWaitForGatewayListener_ExitsOnCancellation — gateway shutdown during the
// wait must abandon it immediately.
func TestWaitForGatewayListener_ExitsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForGatewayListener(ctx, 1, time.Hour) {
		t.Fatal("expected a canceled wait to report failure")
	}
}

// TestBrowserWarmUpEnabled_DefaultConfigEnablesIt pins the shipped default
// against the REAL config constructor rather than a hand-built literal: a
// fresh install must warm Chrome at boot. If someone flips the default in
// pkg/config/defaults.go, this fails — the hand-built configs in the other
// tests here would not notice.
func TestBrowserWarmUpEnabled_DefaultConfigEnablesIt(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Tools.Browser.WarmAtBoot {
		t.Fatal("expected tools.browser.warm_at_boot to default TRUE in DefaultConfig()")
	}
	if !cfg.Tools.Browser.Enabled {
		t.Fatal("precondition: expected tools.browser.enabled to default true")
	}
	if !browserWarmUpEnabled(cfg) {
		t.Fatal("expected a default config to be warm-up eligible")
	}
}

func TestBrowserWarmUpEnabled_DefaultLocalConfig(t *testing.T) {
	if !browserWarmUpEnabled(warmUpEligibleConfig()) {
		t.Fatal("expected warm-up enabled for a local (non-remote-CDP), enabled browser config")
	}
}

// TestBrowserWarmUpEnabled_WarmAtBootFalseDisablesIt covers the operator
// opt-out itself — the whole point of the config field. Turning it off must
// disable EAGER warm-up while leaving browser tools available (Chrome then
// launches lazily at first use); this predicate is the eager half.
func TestBrowserWarmUpEnabled_WarmAtBootFalseDisablesIt(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.WarmAtBoot = false

	if browserWarmUpEnabled(cfg) {
		t.Fatal("expected warm-up DISABLED when tools.browser.warm_at_boot is false")
	}
}

func TestBrowserWarmUpEnabled_RemoteCDPURLDisablesWarmUp(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.CDPURL = "ws://remote-chrome:9222"

	if browserWarmUpEnabled(cfg) {
		t.Fatal(
			"expected warm-up DISABLED when tools.browser.cdp_url is set — must never launch a local Chrome in remote-CDP mode",
		)
	}
}

func TestBrowserWarmUpEnabled_ToolDisabledDisablesWarmUp(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.Enabled = false

	if browserWarmUpEnabled(cfg) {
		t.Fatal("expected warm-up DISABLED when tools.browser.enabled is false")
	}
}

func TestBrowserWarmUpEnabled_SkipEnvVarFullyShortCircuits(t *testing.T) {
	cfg := warmUpEligibleConfig()

	t.Setenv("OMNIPUS_SKIP_BROWSER_PREPROVISION", "1")

	if browserWarmUpEnabled(cfg) {
		t.Fatal(
			"expected OMNIPUS_SKIP_BROWSER_PREPROVISION=1 to fully disable warm-up regardless of an otherwise-eligible config",
		)
	}
}

func TestBrowserWarmUpEnabled_SkipEnvVarOnlyHonorsExactValueOne(t *testing.T) {
	cfg := warmUpEligibleConfig()

	t.Setenv("OMNIPUS_SKIP_BROWSER_PREPROVISION", "true") // not the literal "1"

	if !browserWarmUpEnabled(cfg) {
		t.Fatal("expected only the literal value \"1\" to trip the skip — got a false positive on \"true\"")
	}
}

func TestFindSharedBrowserCoordinator_EmptySlice(t *testing.T) {
	if got := findSharedBrowserCoordinator(nil); got != nil {
		t.Fatalf("expected nil for an empty manager list, got %v", got)
	}
}

func TestFindSharedBrowserCoordinator_NoManagerHasACoordinator(t *testing.T) {
	// Managers never attached via AttachSharedChrome (remote-CDP-override or
	// legacy no-coordinator mode) — Coordinator() is nil on every one.
	mgrs := []*browser.BrowserManager{newTestBrowserManager(t), newTestBrowserManager(t), nil}
	if got := findSharedBrowserCoordinator(mgrs); got != nil {
		t.Fatalf("expected nil when no manager has a coordinator attached, got %v", got)
	}
}

func TestFindSharedBrowserCoordinator_FindsTheSharedInstance(t *testing.T) {
	coordCfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	coord := browser.NewBrowserCoordinator(t.TempDir(), coordCfg)

	// Realistic shape: some agents' managers never got attached (e.g. an
	// earlier registration error), one or more share the SAME coordinator —
	// exactly loop.go's per-key AttachSharedChrome(coordinator, key)
	// wiring (every agent's manager attached to the ONE gateway-scoped
	// coordinator instance).
	unattached := newTestBrowserManager(t)
	attachedA := newTestBrowserManager(t)
	attachedA.AttachSharedChrome(coord, browserTestKey(t, "agent-a"))
	attachedB := newTestBrowserManager(t)
	attachedB.AttachSharedChrome(coord, browserTestKey(t, "agent-b"))

	got := findSharedBrowserCoordinator([]*browser.BrowserManager{unattached, attachedA, attachedB})
	if got == nil {
		t.Fatal("expected a non-nil coordinator")
	}
	if got != coord {
		t.Fatal("expected the exact SAME shared coordinator instance every attached manager points to")
	}
}

// markWarmTestDefaultWorkspace marks workspaceID as the install's default
// workspace (is_default) and clears the flag on every other workspace file.
func markWarmTestDefaultWorkspace(t *testing.T, home, workspaceID string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("markWarmTestDefaultWorkspace: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("markWarmTestDefaultWorkspace: %v", err)
		}
		var ws map[string]any
		if err = json.Unmarshal(raw, &ws); err != nil {
			t.Fatalf("markWarmTestDefaultWorkspace: %v", err)
		}
		ws["is_default"] = strings.TrimSuffix(e.Name(), ".json") == workspaceID
		out, err := json.Marshal(ws)
		if err != nil {
			t.Fatalf("markWarmTestDefaultWorkspace: %v", err)
		}
		if err = os.WriteFile(path, out, 0o600); err != nil {
			t.Fatalf("markWarmTestDefaultWorkspace: %v", err)
		}
	}
}
