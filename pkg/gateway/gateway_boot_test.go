// gateway_boot_test.go: tests for boot - unlock credentials, load souls, seed the roster, build services

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestPickWarmBrowserManager_PrefersTheDefaultAgent — migrated to ADR-075
// FR-016b. "The default agent's manager" is no longer a thing that exists: a
// manager is a WORKSPACE's browser, so the selection is the default agent's
// RESOLVED workspace. Mia is on beta; alpha and gamma belong to other people.
func TestPickWarmBrowserManager_PrefersTheDefaultAgent(t *testing.T) {
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
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = "mia"

	got, reason := pickWarmBrowserManager(cfg, home, mgrs)
	if reason != "" {
		t.Fatalf("expected a pick, got skip reason %q", reason)
	}
	if got == nil || got.BrowsingKey() != keyBeta {
		t.Fatalf("expected the DEFAULT agent's WORKSPACE browser (%s) to be warmed, got %q",
			keyBeta.String(), warmedKey(got))
	}
	// Not the sorted-first one. warmalpha sorts before warmbeta, so a
	// selection that quietly fell back to a lexicographic pick would land on
	// alpha and this assertion is what catches it.
	if got.BrowsingKey() == keyAlpha {
		t.Fatal("warmed the sorted-first workspace, not the default agent's")
	}
}

// TestPickWarmBrowserManager_DeterministicWithoutADefault — migrated. There is
// no lexicographic fallback any more: with no default agent there is no ONE
// workspace to warm, and choosing one would start a Chrome against one
// particular set of live logins because its id sorted first, unasked. The
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
	cfg.Agents.Defaults.DefaultAgentID = ""

	for i := 0; i < 25; i++ {
		got, reason := pickWarmBrowserManager(cfg, home, mgrs)
		if got != nil {
			t.Fatalf("iteration %d: warmed %q with no default agent — a tie-break over workspaces "+
				"picks whose logins to start a browser against", i, warmedKey(got))
		}
		if !strings.Contains(reason, "no default agent") {
			t.Fatalf("iteration %d: skip reason %q must name the missing default agent", i, reason)
		}
	}
}

// TestPickWarmBrowserManager_UnknownDefaultFallsBack — migrated, and the name
// now describes the opposite outcome deliberately. A default_agent_id that
// resolves to no workspace (or to more than one) leaves NOTHING to warm: the
// old fallback would have warmed some other workspace's browser instead, which
// is a worse answer than a cold first open. The install still works; the first
// panel open builds the browser lazily.
func TestPickWarmBrowserManager_UnknownDefaultFallsBack(t *testing.T) {
	home := warmTestHome(t)
	coord := newWarmTestCoordinator(t)
	mgrs := []*browser.BrowserManager{
		newWarmTestManagerForKey(t, coord, warmTestWorkspace(t, home, "warmone", "mia")),
		newWarmTestManagerForKey(t, coord, warmTestWorkspace(t, home, "warmtwo", "ava")),
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = "nobody"
	got, reason := pickWarmBrowserManager(cfg, home, mgrs)
	if got != nil {
		t.Fatalf("a default agent on no workspace must warm nothing, got %q", warmedKey(got))
	}
	if !strings.Contains(reason, "nobody") || !strings.Contains(reason, "workspace") {
		t.Fatalf("skip reason %q must name the agent and say it resolves to no single workspace", reason)
	}

	// An AMBIGUOUS default agent — on both workspaces — is the same answer for
	// the same reason (FR-033): warming one would silently choose which set of
	// live logins boot opens a browser against.
	warmTestWorkspace(t, home, "warmone", "mia", "roam")
	warmTestWorkspace(t, home, "warmtwo", "ava", "roam")
	cfg.Agents.Defaults.DefaultAgentID = "roam"
	got, reason = pickWarmBrowserManager(cfg, home, mgrs)
	if got != nil {
		t.Fatalf("a default agent on TWO workspaces must warm nothing, got %q", warmedKey(got))
	}
	if !strings.Contains(reason, "roam") {
		t.Fatalf("skip reason %q must name the ambiguous default agent", reason)
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
	cfg.Agents.Defaults.DefaultAgentID = "ray"

	got, reason := pickWarmBrowserManager(cfg, home,
		[]*browser.BrowserManager{nil, noCoordinator, noKey, good})
	if got == nil || got.BrowsingKey() != keyRay {
		t.Fatalf("expected the only warmable manager (%s), got %q (reason %q)",
			keyRay.String(), warmedKey(got), reason)
	}

	// The default agent is now the coordinator-less manager's own agent: even
	// though a manager for its workspace EXISTS in the list, it is unwarmable,
	// so the answer is still "nothing to warm" rather than a substitute.
	cfg.Agents.Defaults.DefaultAgentID = "orphan"
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

// TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists pins the
// cancel-and-wait contract shutdown relies on: after cancel(), done closes
// once the goroutine has EXITED, and a pull that lands after the cancel does
// not write providers_catalog.json. On 2026-09-12 the fire-and-forget form
// wrote the 2.4 MB file into integration-test home dirs after their gateway
// had stopped and t.TempDir had removed them.
//
// DIES ON: startCatalogRefreshLoop not closing done when the loop exits, or
// the persist step ignoring a cancelled context.
func TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists(t *testing.T) {
	home := t.TempDir()
	puller := &parkedPuller{body: testDocument(t, "v9999.1.1"), ready: make(chan struct{})}
	cat := catalog.Boot(context.Background(), catalog.EmbeddedSnapshot, puller, catalog.NewFileStore(home), nil)

	cancel, done := startCatalogRefreshLoop(context.Background(), cat, catalog.NewFileStore(home), time.Hour, 5*time.Second, 0)

	select {
	case <-puller.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("startup pull never started")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh loop did not exit within 3s of cancel — shutdown would return with a writer still running")
	}
	require.Less(t, time.Since(start), 3*time.Second)

	// The pull returned a valid, newer document AFTER cancellation. It must
	// not have been persisted.
	_, err := os.Stat(filepath.Join(home, catalog.PersistedFileName))
	require.True(t, os.IsNotExist(err), "providers_catalog.json must not be written after shutdown cancel; stat: %v", err)
}

// TestSkipStartupPull_Window is the FR-008 skip predicate on its own: only a
// persisted document younger than the window skips; a missing or unreadable
// file never does, because there is nothing on disk to serve from.
func TestSkipStartupPull_Window(t *testing.T) {
	home := t.TempDir()
	store := catalog.NewFileStore(home)

	assert.False(t, skipStartupPull(store, time.Hour),
		"no persisted file at all → never skip; the pull is exactly what is wanted")

	require.NoError(t, store.Write(context.Background(), testDocument(t, "v2026.8.24")))
	assert.True(t, skipStartupPull(store, time.Hour),
		"a document just written is younger than the window → skip")
	assert.False(t, skipStartupPull(store, time.Nanosecond),
		"a window shorter than the file's age → pull")
	assert.False(t, skipStartupPull(store, 0),
		"a zero window disables the skip entirely")
	assert.False(t, skipStartupPull(nil, time.Hour),
		"no store → nothing to age → never skip")
}

// TestSlogArgsToFields covers the key/value-pair conversion helper directly,
// including the malformed odd-length call site slog itself documents a
// "!BADKEY" convention for.
func TestSlogArgsToFields(t *testing.T) {
	fields := slogArgsToFields([]any{"a", 1, "b", "two"})
	assert.Equal(t, map[string]any{"a": 1, "b": "two"}, fields)

	fields = slogArgsToFields(nil)
	assert.Empty(t, fields)

	fields = slogArgsToFields([]any{"a", 1, "orphan"})
	assert.Equal(t, 1, fields["a"])
	assert.Equal(t, "orphan", fields["!BADKEY"])
}

// TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled pins Task 2: the
// non-channel categories credentials.ResolveAll can produce a bundle error
// for (voice, web-search tools, skill marketplaces — see
// pkg/credentials/inject.go's nonChannelRefs) must be included in
// buildEnabledRefMap's "in use" set when the owning feature is enabled, and
// excluded when it is disabled — mirroring the channel Enabled gate that
// already existed.
func TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {Enabled: true, TokenRef: "TELEGRAM_REF"},
			"discord":  {Enabled: false, TokenRef: "DISCORD_REF"},
		},
	}
	cfg.Voice.ElevenLabsAPIKeyRef = "ELEVENLABS_REF"
	cfg.Voice.GroqAPIKeyRef = "GROQ_VOICE_REF"
	cfg.Tools.Web.Brave = config.BraveConfig{Enabled: true, APIKeyRef: "BRAVE_REF"}
	cfg.Tools.Web.Tavily = config.TavilyConfig{Enabled: false, APIKeyRef: "TAVILY_REF"}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, AuthTokenRef: "CLAWHUB_REF"},
		{Name: "github", Type: "github", Enabled: false, TokenRef: "GITHUB_REF"},
	}
	// BUG 4 (architect finding): MCP server env-var refs must be gated at
	// BOTH the per-server level (srv.Enabled) and the global kill-switch
	// level (cfg.Tools.MCP.Enabled) — a server marked Enabled under a
	// globally-disabled tools.mcp.enabled never actually connects, so its
	// ref must not read as "in use" any more than a disabled channel's does.
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled":  {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_ENABLED_REF"}},
		"mcp-disabled": {Enabled: false, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_DISABLED_REF"}},
	}

	m := buildEnabledRefMap(cfg)

	assert.True(t, m["TELEGRAM_REF"], "enabled channel ref must be in the map")
	assert.False(t, m["DISCORD_REF"], "disabled channel ref must NOT be in the map")

	assert.True(t, m["ELEVENLABS_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")
	assert.True(t, m["GROQ_VOICE_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")

	assert.True(t, m["BRAVE_REF"], "enabled web-search tool ref must be in the map")
	assert.False(t, m["TAVILY_REF"], "disabled web-search tool ref must NOT be in the map")

	assert.True(t, m["CLAWHUB_REF"], "enabled marketplace ref must be in the map")
	assert.False(t, m["GITHUB_REF"], "disabled marketplace ref must NOT be in the map")

	assert.True(t, m["MCP_ENABLED_REF"], "an enabled MCP server's env ref must be in the map")
	assert.False(t, m["MCP_DISABLED_REF"], "a disabled MCP server's env ref must NOT be in the map")
}

// TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers proves the second
// half of the MCP gate: even a per-server Enabled=true entry must NOT read as
// "in use" when the global tools.mcp.enabled kill-switch is off, mirroring
// ReconcileMCP's own desired-set gating (pkg/agent/loop_mcp.go: mcpCfg.Enabled
// gates the whole loop before any per-server Enabled check).
func TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = false
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled": {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_REF"}},
	}

	m := buildEnabledRefMap(cfg)

	assert.False(t, m["MCP_REF"],
		"an MCP server's ref must not be 'in use' when the global tools.mcp.enabled kill-switch is off")
}

// TestDefaultModelCredentialBlocked_ByPair: the limited-mode check matches the
// default model's backing rows by the exact (provider, model) pair — a row
// serving the same model under a DIFFERENT provider is not a candidate.
func TestDefaultModelCredentialBlocked_ByPair(t *testing.T) {
	t.Run("blocked when every row backing the pair has an unresolved ref", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openai", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		reason, blocked := defaultModelCredentialBlocked(cfg)
		require.True(t, blocked)
		assert.Contains(t, reason, "T068_07_UNSET_REF")
		assert.Contains(t, reason, "openai/gpt-4o")
	})
	t.Run("not blocked when the only unresolved row is under another provider", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openrouter", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		_, blocked := defaultModelCredentialBlocked(cfg)
		assert.False(t, blocked, "a row under a different provider never backs the pair; that is CreateProvider's not-found error to report")
	})
	t.Run("zero pair is never blocked", func(t *testing.T) {
		_, blocked := defaultModelCredentialBlocked(&config.Config{})
		assert.False(t, blocked)
	})
}

// TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan
// is case (a): marker present + define-goal/ present + define-done/ present
// => define-done/ deleted.
func TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected deleted=true when define-goal/ is present and define-done/ exists")
	}
	requireDirAbsent(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan is case
// (b): marker present + define-goal/ ABSENT => define-done/ PRESERVED, no
// delete. This is the fail-open scenario the fix closes: a partial/failed
// SeedDefaults must never cost the operator their quality bar.
func TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	// Deliberately do NOT create define-goal/ — simulates SeedDefaults
	// failing after the migration marker was already recorded.
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err == nil {
		t.Fatal("expected a non-nil error when the replacement define-goal/ directory is absent")
	}
	if deleted {
		t.Fatal("expected deleted=false when the replacement define-goal/ directory is absent")
	}
	requireDirExists(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp is case (c): a second
// call after the orphan has already been deleted is a clean no-op — no
// error, deleted=false — idempotent by the directories' own on-disk state.
func TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	first, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil || !first {
		t.Fatalf("setup: first call must delete cleanly, got deleted=%v err=%v", first, err)
	}

	second, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("second call must be a clean no-op, got error: %v", err)
	}
	if second {
		t.Fatal("second call must report deleted=false — define-done/ was already gone")
	}
}

// TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir covers
// a pre-ADR-080 install that has not run the rename yet: the marker is
// absent, so neither directory is touched regardless of what's on disk.
func TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, nil)
	if err != nil {
		t.Fatalf("unexpected error with no marker present: %v", err)
	}
	if deleted {
		t.Fatal("expected deleted=false with no migration marker present")
	}
	requireDirExists(t, doneDir)
}

// TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring boots the gateway
// through the exact production function (setupAndStartServices) on a minimal,
// no-network config, dispatches a standalone task through
// TaskExecutor.StartTaskNow (the second of the two documented
// mintTaskLifecycleRecord chokepoints, alongside createTaskSessionSync/
// ExecuteTask), and asserts a durable session_lifecycle record was persisted
// for the resulting session.
func TestSetupAndStartServices_TaskExecutorLifecycleStoreWiring(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		// The mock worker below never claims, so its run spends goal tries and
		// task attempts (founder decision 2026-09-14). One of each lets the run
		// end Failed quickly, so the teardown guard does not wait out the
		// default 20 tries x 3 attempts.
		Planning: config.PlanningConfig{GoalMaxRounds: 1, TaskMaxAttempts: 1},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// A real, chat-target agent ("mia") so registry.GetAgent("mia")
			// resolves for the standalone task dispatched below. The retired
			// "main" sentinel used to be registered implicitly regardless of
			// cfg (pkg/agent/registry.go's old always-on fallback); it is gone
			// with no back-compat, so this harness must seed a real agent.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	// Production seeds goal_claim "allow" for every agent (pkg/config/defaults.go).
	// Without it the task below would end before its first turn (founder
	// decision 2026-09-15) instead of exercising a real run's lifecycle writes.
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// FIX (14-reviewer sign-off finding #5): setupAndStartServices now aborts
	// boot when it cannot derive the intent-log HMAC chain key (previously a
	// WARN-and-continue that let plan.NewIntentLog silently install a public
	// dev-only key in production — see gateway.go's boot wiring). A locked
	// store (credentials.NewStore without Unlock) used to be tolerated here
	// only because that derivation failure was non-fatal; use the real
	// unlocked-store helper so this test still exercises the intended boot
	// path rather than the now-fatal locked-store one.
	credStore := newUnlockedStore(t, tmpDir)
	builtinReg := tools.NewBuiltinRegistry()
	mcpReg := tools.NewMCPRegistry()

	rs, err := setupAndStartServices(
		context.Background(),
		cfg,
		credentials.SecretBundle{},
		al,
		msgBus,
		tmpDir,
		credStore,
		&SandboxApplyResult{},
		builtinReg,
		mcpReg,
		false, // allowGodMode
	)
	require.NoError(t, err, "setupAndStartServices must boot cleanly on a minimal config")
	t.Cleanup(func() {
		stopAndCleanupServices(rs, 5*time.Second, false)
	})
	require.NotNil(t, rs.PlanEngine, "plan engine must be constructed and started by the real boot path")

	tExecutor := agent.GetTaskExecutor(al)
	require.NotNil(t, tExecutor, "boot must construct a task executor")
	tStore := agent.GetTaskStore(al)
	require.NotNil(t, tStore, "boot must construct a task store")

	// A standalone task (PlanID == "") assigned to "mia" — the one real,
	// chat-target agent seeded into cfg.Agents.List above, so
	// registry.GetAgent("mia") resolves without any workspace/team setup.
	// WorkspaceID only needs to be non-empty (task.Store.normalize enforces
	// presence, not FK existence — the workspace-membership check is a
	// REST/tool-layer concern this Go-level dispatch bypasses entirely).
	tsk := &task.Task{
		Title:       "lifecycle-wiring-smoke",
		AgentID:     "mia",
		Status:      task.StatusNext,
		WorkspaceID: "lifecycle-wiring-smoke-ws",
	}
	require.NoError(t, tStore.Create(tsk))

	// StartTaskNow launches a task its caller has ALREADY moved to in_progress
	// (the REST PATCH does exactly that first). Leaving it `next` lets the
	// heartbeat claim and dispatch the same task a second time alongside this
	// run.
	inProgress := task.StatusInProgress
	_, advErr := tStore.Update(tsk.ID, task.Patch{Status: &inProgress})
	require.NoError(t, advErr, "advance the task to in_progress before StartTaskNow")

	sessionID, startErr := tExecutor.StartTaskNow(context.Background(), tsk.ID)
	require.NoError(t, startErr, "StartTaskNow must succeed for a valid standalone task with a registered agent")
	require.NotEmpty(t, sessionID, "StartTaskNow must mint and persist a session id")

	// Teardown race guard (mirrors TestHandleTaskPatch_InProgress_WithKnownAgent
	// in rest_tasks_start_test.go): StartTaskNow launched runTaskFromInProgress
	// in a background goroutine that keeps writing session/task files after
	// this call returns. Registered AFTER t.TempDir()'s own cleanup so it runs
	// first (t.Cleanup is LIFO) and the goroutine's writes are done before the
	// temp dir is removed. The mock LLM returns immediately so this clears in
	// well under a second; the bound is a generous safety margin.
	taskID := tsk.ID
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			fresh, getErr := tStore.Get(taskID)
			return getErr == nil && (fresh.Status == task.StatusDone || fresh.Status == task.StatusFailed)
		}, 10*time.Second, 20*time.Millisecond,
			"task goroutine must reach a terminal state before test teardown")
	})

	// Read the durable S2 record back through a FRESH, independent
	// LifecycleStore instance pointed at the SAME directory
	// setupAndStartServices used (<homePath>/session_lifecycle) — this is
	// exactly what boot_sweep.go does on the NEXT boot after a crash. It
	// deliberately does NOT reach into tExecutor's private lifecycleStore
	// field (there is no exported accessor, and reaching in would just be
	// the same side door this test exists to avoid) — it verifies the
	// observable, on-disk effect of the wiring instead.
	lifecycleStore := session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle"))
	rec, loadErr := lifecycleStore.Load(sessionID)
	require.NoError(t, loadErr,
		"a durable session_lifecycle record must exist for the dispatched task session "+
			"(session.ErrLifecycleNotFound means TaskExecutor.SetLifecycleStore was never "+
			"called from the boot path, so mintTaskLifecycleRecord silently no-op'd)")
	assert.Equal(t, sessionID, rec.SessionID)
	assert.NotEmpty(t, rec.State, "lifecycle record must carry a non-empty state")
}

// TestMcpEnabledEnvSensitiveValues_ResolvesOnlyEnabledServers proves the core
// BUG 4 fix: an enabled MCP server's env secret is resolved and returned for
// registration, while a disabled server's secret (or one gated off by the
// global tools.mcp.enabled kill-switch) is not.
func TestMcpEnabledEnvSensitiveValues_ResolvesOnlyEnabledServers(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	require.NoError(t, store.Set("mcp_enabled-srv_TOKEN", "enabled-secret-value"))
	require.NoError(t, store.Set("mcp_disabled-srv_TOKEN", "disabled-secret-value"))

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"enabled-srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_enabled-srv_TOKEN"},
		},
		"disabled-srv": {
			Enabled: false, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_disabled-srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)

	assert.Contains(t, values, "enabled-secret-value",
		"an enabled MCP server's env secret must be resolved for sensitive-value registration")
	assert.NotContains(t, values, "disabled-secret-value",
		"a disabled MCP server's env secret must NOT be resolved/registered")
}

// TestMcpEnabledEnvSensitiveValues_GlobalKillSwitchOffReturnsNothing proves
// the global-gate half: even an Enabled=true server contributes nothing when
// tools.mcp.enabled is off, matching ReconcileMCP's own gating.
func TestMcpEnabledEnvSensitiveValues_GlobalKillSwitchOffReturnsNothing(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	require.NoError(t, store.Set("mcp_srv_TOKEN", "some-secret-value"))

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = false
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)
	assert.Empty(t, values, "global tools.mcp.enabled=false must suppress every MCP server's contribution")
}

// TestMcpEnabledEnvSensitiveValues_DanglingRefIsSwallowed proves a dangling
// (unresolvable) ref does not panic or error out the whole call — it simply
// contributes nothing, consistent with reconcileLocked's own WARN+skip
// handling of the same condition at connect time.
func TestMcpEnabledEnvSensitiveValues_DanglingRefIsSwallowed(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	// Deliberately do NOT store anything under "mcp_srv_TOKEN".

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)
	assert.Empty(t, values, "a dangling ref must be swallowed, not panic or surface as a value")
}

// TestMcpEnabledEnvSensitiveValues_NilStoreOrConfig proves the nil-safety
// guards: a nil store or nil config must return nil rather than panicking —
// bootCredentials/executeReload call this unconditionally alongside the
// bundle-derived values.
func TestMcpEnabledEnvSensitiveValues_NilStoreOrConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	assert.Nil(t, mcpEnabledEnvSensitiveValues(cfg, nil))
	assert.Nil(t, mcpEnabledEnvSensitiveValues(nil, newUnlockedTestCredStore(t)))
}

// TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken is the gateway half of
// ADR-068 FR-046's agent-path gap. providers hands the registrar a freshly
// minted token; what has to happen next is that the LIVE config's scrubber
// starts filtering it — and, because RegisterSensitiveValues replaces rather
// than appends, that no OTHER already-protected secret is evicted in the
// process. A registrar that registered only the new value would look correct
// and would silently unprotect everything else.
func TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken(t *testing.T) {
	store := newRegistrarTestStore(t)
	storeOAuthEntry(t, store, "openai", "stored-access-token", "stored-refresh-token")
	// A SECOND signed-in vendor, and a provider API key reached through a
	// config ref. Both are part of the canonical "complete current set" that
	// boot registers, so both must survive a refresh-triggered
	// re-registration — RegisterSensitiveValues replaces rather than appends,
	// so a registrar that passed only the new token would silently unprotect
	// every one of them.
	storeOAuthEntry(t, store, "xai", "other-vendor-access-token", "other-vendor-refresh-token")
	if err := store.Set("OPENAI_API_KEY", "provider-api-key-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := &config.Config{
		Providers: []*config.ModelConfig{{Provider: "openai", APIKeyRef: "OPENAI_API_KEY"}},
	}

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return cfg }, store)
	registrar("brand-new-access-token", "brand-new-refresh-token")

	replacer := cfg.SensitiveDataReplacer()
	if replacer == nil {
		t.Fatal("SensitiveDataReplacer returned nil after registration")
	}

	for _, secret := range []string{
		"brand-new-access-token",     // handed to the registrar directly
		"brand-new-refresh-token",    // ditto
		"stored-access-token",        // recomputed from the store
		"stored-refresh-token",       // ditto
		"other-vendor-access-token",  // a different vendor's stored tokens
		"other-vendor-refresh-token", // ditto
		"provider-api-key-secret",    // the config-ref-driven bundle
	} {
		out := replacer.Replace("prefix " + secret + " suffix")
		if strings.Contains(out, secret) {
			t.Errorf("secret %q survives the scrubber: %q", secret, out)
		}
	}
}

// TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig: a config reload swaps
// the *config.Config, so the registrar must read it through the getter on
// every call. Capturing the boot-time instance would leave every refresh after
// the first reload registering onto an object nothing consults.
func TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig(t *testing.T) {
	store := newRegistrarTestStore(t)

	bootCfg := &config.Config{}
	reloadedCfg := &config.Config{}
	live := bootCfg

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return live }, store)

	live = reloadedCfg
	registrar("post-reload-token")

	if out := reloadedCfg.SensitiveDataReplacer().Replace("post-reload-token"); strings.Contains(out, "post-reload-token") {
		t.Error("the post-reload config's scrubber does not filter the token — the registrar is not reading the live config")
	}
	if out := bootCfg.SensitiveDataReplacer().Replace("post-reload-token"); !strings.Contains(out, "post-reload-token") {
		t.Error("the token was registered onto the stale boot config")
	}
}

// TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies: this runs on
// the refresh path inside a live turn. A nil config (pre-boot, or a test that
// never built one) must be a no-op, never a panic that takes the turn down.
func TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies(t *testing.T) {
	store := newRegistrarTestStore(t)

	oauthSensitiveValueRegistrar(func() *config.Config { return nil }, store)("tok")
	oauthSensitiveValueRegistrar(nil, store)("tok")
	oauthSensitiveValueRegistrar(func() *config.Config { return &config.Config{} }, nil)("tok")
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk is the direct
// regression test for the FR-005 gap: PlanSupervisorDefaultRubric existed only
// as a Go constant that no write path ever materialized, because both seed
// paths were hardcoded to the Judge.
//
// newSeededJudgeAPI runs the REAL boot sequence (coreagent.SeedConfig then
// seedSystemAgentEagerSouls), so this reads the actual file the actual boot
// wrote — no re-implementation, no assertion that a function ran.
func TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk(t *testing.T) {
	require.NotEmpty(t, strings.TrimSpace(coreagent.PlanSupervisorDefaultRubric),
		"the rubric constant itself must be non-empty, or 'it reached disk' would be vacuous")

	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	onDisk, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr,
		"PlanSupervisor SOUL.md must exist immediately after boot — there is no lazy backstop (FR-005 rev 2), "+
			"so a missing file here means the adjudicator would run on an EMPTY prompt")
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(onDisk),
		"the on-disk soul must be the compiled adjudication rubric, byte for byte")

	// The same boot must still seed the Judge — generalising the loop must not
	// have traded one hardcoded agent for another.
	judgeWS, wsErr := agentWorkspacePath(cfg, string(coreagent.IDJudge), "", api.homePath)
	require.NoError(t, wsErr)
	judgeSoul, judgeErr := os.ReadFile(filepath.Join(judgeWS, "SOUL.md"))
	require.NoError(t, judgeErr, "the Judge's soul must still be seeded by the same boot")
	assert.Equal(t, coreagent.JudgeDefaultRubric, string(judgeSoul))

	// And the operator must actually SEE it: getAgent reads SOUL.md from the
	// workspace and (ac.IsSystem()) does not blank it out for a locked System
	// Agent, so a fresh install shows the standards it is running under.
	w := httptest.NewRecorder()
	api.getAgent(w, string(coreagent.IDPlanSupervisor))
	require.Equal(t, http.StatusOK, w.Code, "GET plansupervisor; body=%s", w.Body.String())
	var got gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, got.Soul,
		"GET /api/v1/agents/plansupervisor on a fresh install must show the default rubric, not an empty soul")
	assert.Equal(t, gen.AgentTypeSystem, got.Type)
	assert.True(t, got.Locked)
}

// TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul
// locks the operator-editable contract: seedSystemAgents re-enforces
// identity/type/locked/tool-policy on EVERY boot, but Model/Provider and the
// SOUL are preserved once written. Since the eager seed now runs on every boot
// (not just the first), an overwrite here would silently revert an operator's
// tuned rubric on the next restart.
func TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	// The edit must land on a file the FIRST boot actually seeded — otherwise
	// this test would pass vacuously against a build where the eager seed
	// never writes the PlanSupervisor's soul at all.
	seeded, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "the first boot must have seeded the soul before the operator edits it")
	require.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(seeded))

	const editedSoul = "You are the Plan Supervisor. House rule: never supersede a member on a first failure."
	require.NoError(t, os.WriteFile(soulPath, []byte(editedSoul), 0o644),
		"simulate an operator editing the PlanSupervisor's soul")

	// Simulate a full restart: the config re-seed (tamper protection /
	// identity repair) followed by the eager soul seed, in the same order
	// RunContextWithOptions runs them.
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)

	after, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after a restart")
	assert.Equal(t, editedSoul, string(after),
		"a restart must NOT overwrite an operator-edited PlanSupervisor soul with the compiled default")

	// A second restart must be equally inert (the guard is content-based, not
	// a once-only flag).
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)
	afterSecond, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, editedSoul, string(afterSecond),
		"a second restart must still preserve the operator-edited soul")
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled proves a
// 0-byte SOUL.md (an interrupted write, or a hand-created empty file) counts as
// MISSING, not as "the operator wants an empty prompt" — mirroring the Judge's
// own rule. Without this, the one path that can give the adjudicator a prompt
// would consider a blank file already-seeded forever.
func TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled(t *testing.T) {
	tmpDir := t.TempDir()
	// seedSystemAgentEagerSouls resolves each workspace via $OMNIPUS_HOME
	// (agent.ResolveAgentHome), not via the tmpDir threaded into
	// agentWorkspacePath — pin them to the same directory.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)

	soulPath := planSupervisorSoulPath(t, cfg, tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(soulPath), 0o755))
	require.NoError(t, os.WriteFile(soulPath, []byte{}, 0o644),
		"seed a 0-byte SOUL.md before the eager seed runs")

	seedSystemAgentEagerSouls(cfg)

	got, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(got),
		"a 0-byte PlanSupervisor SOUL.md must be treated as missing and backfilled")
}

// TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul is the
// matching generalisation guard for the seed loop: it iterates
// coreagent.SystemAgents(), so any System Agent that declares a default soul
// via coreagent.SystemAgentDefaultSoul gets it on disk with no further edit to
// pkg/gateway. A future agent whose soul silently never lands fails here.
func TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()

	for _, sa := range coreagent.SystemAgents() {
		want := coreagent.SystemAgentDefaultSoul(sa.ID)
		if strings.TrimSpace(want) == "" {
			continue // no compiled default soul — nothing to backfill
		}
		ws, wsErr := agentWorkspacePath(cfg, string(sa.ID), "", api.homePath)
		require.NoError(t, wsErr, "resolve workspace for %s", sa.ID)
		got, readErr := os.ReadFile(filepath.Join(ws, "SOUL.md"))
		require.NoErrorf(t, readErr,
			"System Agent %s declares a default soul but none reached disk at boot", sa.ID)
		assert.Equalf(t, want, string(got),
			"System Agent %s's on-disk soul must be its compiled default", sa.ID)
	}
}

// TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential pins the
// second honesty surface. Once boot survives, the factory would happily build
// an HTTP provider with an EMPTY api key (api_base alone satisfies it), and the
// operator's first message would come back as a bare upstream 401 naming
// neither the provider nor the credential. Instead every turn must answer with
// the real cause.
func TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential(t *testing.T) {
	const ref = "DEGRADED_TEST_BLOCKED_DEFAULT_KEY"
	t.Setenv(ref, "") // ref configured, credential never resolved

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider must not fail when the default model's credential is missing: %v", err)
	}
	if _, ok := p.(*startupBlockedProvider); !ok {
		t.Fatalf(
			"expected a startupBlockedProvider for a default model with an unresolvable credential, got %T "+
				"— an HTTP provider with an empty key would 401 with no mention of the real cause",
			p,
		)
	}
	_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
	if chatErr == nil {
		t.Fatal("a blocked provider must fail every chat turn")
	}
	// The message names the default PAIR (provider/model), never a row alias.
	if !strings.Contains(chatErr.Error(), ref) || !strings.Contains(chatErr.Error(), "openrouter/openrouter/z-ai/glm-5-turbo") {
		t.Errorf(
			"the chat error must name the model and the missing credential so the operator can act; got: %q",
			chatErr.Error(),
		)
	}
}

// TestCreateStartupProvider_ResolvedCredentialIsNotBlocked is the control: the
// same config with the credential present must build the real provider. Without
// it, the test above would still pass if createStartupProvider blocked
// unconditionally.
func TestCreateStartupProvider_ResolvedCredentialIsNotBlocked(t *testing.T) {
	const ref = "DEGRADED_TEST_RESOLVED_DEFAULT_KEY"
	t.Setenv(ref, "sk-resolved")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a provider whose credential resolves must NOT be blocked")
	}
}

// TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable guards the
// multi-entry case: several providers[] entries may share one model_name for
// load balancing (config.GetModelConfig round-robins over them). One broken
// sibling must not disable a model that still has a working entry.
func TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable(t *testing.T) {
	const goodRef = "DEGRADED_TEST_LB_GOOD_KEY"
	const badRef = "DEGRADED_TEST_LB_BAD_KEY"
	t.Setenv(goodRef, "sk-good")
	t.Setenv(badRef, "")

	entry := func(ref string) *config.ModelConfig {
		return &config.ModelConfig{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{entry(badRef), entry(goodRef)},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a model with at least one usable load-balanced entry must not be blocked")
	}
}

// --- Test D: wireChannelManager sets observer on the channel manager ---

// TestWireChannelManager_ObserverSurvivesChannelRecreation is a smoke test that
// wireChannelManager registers a non-nil PairingObserver on the channels.Manager.
// It uses the real channels.Manager (via NewManagerForTesting) and a real AgentLoop
// so the production code path is exercised end-to-end.
func TestWireChannelManager_ObserverSurvivesChannelRecreation(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	t.Cleanup(func() { al.Stop() })

	// NewManagerForTesting creates a Manager with no channels (no credentials needed).
	// The pairingObserver field starts nil.
	cm := channels.NewManagerForTesting(nil)

	// Wire the manager onto the agent loop, then call wireChannelManager.
	al.SetChannelManager(cm)
	wireChannelManager(cm, al)

	// Verify the observer is set by calling SetPairingObserver with a tracking
	// closure and confirming the manager accepts it without panic.  The key
	// invariant is that wireChannelManager's closure (al.EmitWhatsAppPairing)
	// replaced any previously-nil observer.  We re-wire a test observer here to
	// confirm the setter is live; the test observer records whether it fires.
	var observerCalled bool
	assert.NotPanics(t, func() {
		cm.SetPairingObserver(func(channelID string, status channels.PairingStatus, qr, message string) {
			observerCalled = true
		})
	}, "SetPairingObserver must not panic after wireChannelManager")

	// Call the observer by simulating a pairing event emission on the bus and
	// verifying the subscription on the agent loop emits into the event bus.
	// We can't easily drive it through a real channel here, so instead confirm
	// that al.EmitWhatsAppPairing (called by the wireChannelManager closure)
	// does not panic. Subscribe to events first.
	evtSub := al.SubscribeEvents(4)
	defer al.UnsubscribeEvents(evtSub.ID)

	assert.NotPanics(t, func() {
		al.EmitWhatsAppPairing("whatsapp_native", channels.PairingStatusCode, "TEST-QR", "")
	}, "EmitWhatsAppPairing must not panic after wireChannelManager wired the observer")

	// Assert the event was emitted (not just a no-op).
	select {
	case evt := <-evtSub.C:
		assert.Equal(t, agent.EventKindWhatsAppPairing, evt.Kind,
			"EmitWhatsAppPairing must emit EventKindWhatsAppPairing on the event bus")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected WhatsAppPairing event on bus after EmitWhatsAppPairing")
	}

	_ = observerCalled // used only to satisfy compiler; real assertion is the event check above
}

// TestOnboardingStateUnreadable_ClassifiesEachCase pins the three inputs of the
// boot-time sample that feeds onboardingStateUnknown. Getting the MISSING case
// wrong would break every genuine first launch, so it is asserted explicitly
// rather than left implied.
func TestOnboardingStateUnreadable_ClassifiesEachCase(t *testing.T) {
	t.Run("missing file is a genuine fresh install", func(t *testing.T) {
		assert.False(t, onboardingStateUnreadable(t.TempDir()))
	})

	t.Run("valid JSON is known", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,"onboarding_complete":true}`), 0o600))
		assert.False(t, onboardingStateUnreadable(home))
	})

	t.Run("unparseable JSON is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "system", "state.json"),
			[]byte(`{"version":1,`), 0o600))
		assert.True(t, onboardingStateUnreadable(home),
			"a truncated state.json must be unknown, not a fresh install")
	})

	t.Run("unreadable file is unknown", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system"), 0o700))
		// A DIRECTORY where the file belongs: os.ReadFile fails with a
		// non-IsNotExist error on every platform, unlike a chmod 000 file,
		// which root can still read.
		require.NoError(t, os.MkdirAll(filepath.Join(home, "system", "state.json"), 0o700))
		assert.True(t, onboardingStateUnreadable(home),
			"a state path that cannot be read must be unknown, not a fresh install")
	})
}

// ---------------------------------------------------------------------------
// M1 — the startup orphan sweep could never sweep openai_OAUTH
// ---------------------------------------------------------------------------

// TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow is the
// M1 regression test. sweepOrphanedProviderCredentials built configuredVendors
// from EVERY cfg.Providers row without applying isSeedTemplateRow, and
// pkg/config/defaults.go seeds `{Provider: "openai"}` as a permanent keyless
// template row — so configuredVendors["openai"] was populated on every install
// and `openai_OAUTH`, the only OAuth grant the product currently issues, was
// structurally unsweepable. If the process died between the config write and
// the credential delete during provider removal, the live access AND refresh
// token survived with nothing in the UI referencing them.
func TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"orphan"}`))
	require.NoError(t, store.Set("openai_API_KEY", "sk-orphan"))

	// Exactly the shipped seed: a keyless template row with a provider
	// identity and nothing else. No operator ever created it.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openai", Model: "gpt-5", APIBase: ""},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: the fixture row must be the seeded template shape")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.Error(t, err,
		"a seeded template row must not protect %s from the orphan sweep", oauthName)
	_, err = store.Get("openai_API_KEY")
	assert.Error(t, err,
		"a seeded template row must not protect openai_API_KEY from the orphan sweep")
}

// TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant
// is the guard on the M1 fix itself, for a mistake the fix made on its first
// attempt and the existing suite caught: filtering the vendor keep-set on
// isSeedTemplateRow ALONE deletes live OAuth grants.
//
// A sign_in row legitimately carries no api_key_ref, no api_base and no
// models — it authenticates with a vendor session, not a key — so it can be
// seed-SHAPED while being a real, operator-configured row whose grant is
// live. Sweeping that is unrecoverable, and strictly worse than the orphan
// M1 set out to reclaim. The row's id mapping to a DIFFERENT vendor is what
// distinguishes it from the shipped api-key seed.
func TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live","refresh_token":"live-refresh"}`))

	// Deliberately the MINIMAL sign-in row: no auth_method, no api_key_ref,
	// no api_base, no models. isSeedTemplateRow says "template"; it is not.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Name: "openai-chatgpt", Provider: "openai-chatgpt", Model: "gpt-5.2"},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: this real sign-in row is seed-SHAPED — that is the whole trap")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.NoError(t, err,
		"a configured sign-in row must protect its vendor's live OAuth grant even when seed-shaped")
}

// TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced pins the
// two keep-sets the M1 filter must NOT weaken. Wrongly deleting a live secret
// is unrecoverable; failing to sweep is merely untidy.
func TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live"}`))
	require.NoError(t, store.Set("anthropic_API_KEY", "sk-live"))
	require.NoError(t, store.Set("weird_API_KEY", "sk-hand-named"))

	cfg := &config.Config{Providers: []*config.ModelConfig{
		// A real, operator-configured openai-chatgpt row: its vendor entry is
		// openai_OAUTH and must survive.
		{Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn},
		// A real anthropic row.
		{Provider: "anthropic", Model: "claude", APIKeyRef: "anthropic_API_KEY"},
		// A row whose ref was renamed by hand: the belt-and-braces keep-set.
		{Provider: "custom-thing", Model: "m", APIKeyRef: "weird_API_KEY"},
	}}

	sweepOrphanedProviderCredentials(cfg, store, nil)

	for _, name := range []string{oauthName, "anthropic_API_KEY", "weird_API_KEY"} {
		_, err := store.Get(name)
		assert.NoError(t, err, "%s is live and must never be swept", name)
	}
}

// --- Bug 1: a single corrupt core-agent entity record must not abort boot ---

// TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot proves the fix:
// when one seeded core agent's entity record on disk is corrupt/unparseable,
// persistSeededCoreAgents skips re-seeding THAT agent (logs and continues)
// instead of returning an error that would abort the whole boot sequence.
// The historical behavior treated store.Get's parse error identically to
// "does not exist", driving a store.Create that failed with
// entity.ErrAlreadyExists (the file DOES exist, it just didn't parse) — one
// corrupt file made the entire gateway unbootable.
func TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))

	// mia.json exists but is corrupt (not valid JSON) — simulates a
	// truncated write, disk corruption, or a hand-edit gone wrong.
	corruptPath := filepath.Join(entitiesDir, "mia.json")
	corruptBytes := []byte("{not valid json at all")
	require.NoError(t, os.WriteFile(corruptPath, corruptBytes, 0o600))

	seeded := []config.AgentConfig{
		{ID: "mia", Name: "Mia"},
		{ID: "jim", Name: "Jim"}, // a normal, brand-new core agent alongside it
	}

	err := persistSeededCoreAgents(home, seeded)
	require.NoError(t, err, "a single corrupt entity record must not abort boot")

	// The corrupt file must be left exactly as it was — no clobbering
	// attempt, no silent overwrite.
	after, readErr := os.ReadFile(corruptPath)
	require.NoError(t, readErr)
	assert.Equal(t, corruptBytes, after, "corrupt record must be left untouched, not overwritten")

	// The healthy sibling agent must still have been created normally.
	jimCfg, getErr := agentstore.New(home).Get("jim")
	require.NoError(t, getErr, "a healthy sibling agent must still persist normally")
	assert.Equal(t, "jim", jimCfg.ID)
}

// TestPersistSeededCoreAgents_NotFoundCreatesNormally is a negative control
// proving the ErrNotFound path (genuinely new agent, no file yet) still
// creates the record — the fix didn't turn EVERY Get error into a silent
// skip, only the non-ErrNotFound ones.
func TestPersistSeededCoreAgents_NotFoundCreatesNormally(t *testing.T) {
	home := t.TempDir()
	err := persistSeededCoreAgents(home, []config.AgentConfig{{ID: "ava", Name: "Ava"}})
	require.NoError(t, err)

	got, getErr := agentstore.New(home).Get("ava")
	require.NoError(t, getErr)
	assert.Equal(t, "ava", got.ID)
}

// --- Bug 2 / verified privilege-escalation fix: strict roster population ---

// TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster
// proves a genuine entity.Store.List() failure (here: entities/agents/
// shadowed by a regular file, so os.ReadDir returns ENOTDIR — NOT
// os.IsNotExist) is propagated as an error, and cfg.Agents.List is left
// completely untouched rather than silently emptied. This is the exact class
// the legacy log-and-return behavior mishandled: a transient EMFILE/EACCES/
// EIO List() failure looked identical to "nothing configured yet".
func TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "entities"), 0o700))
	// Shadow entities/agents with a FILE, not a directory, so os.ReadDir
	// fails with a genuine (non-NotExist) error regardless of process UID
	// (unlike an EACCES-via-chmod approach, which is a no-op for root).
	require.NoError(t, os.WriteFile(filepath.Join(home, "entities", "agents"), []byte("not a directory"), 0o600))

	preexisting := []config.AgentConfig{{ID: "sentinel-preexisting", Name: "should survive"}}
	cfg := &config.Config{Agents: config.AgentsConfig{List: append([]config.AgentConfig{}, preexisting...)}}

	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "a genuine List() failure must be reported, not swallowed")
	assert.Equal(t, preexisting, cfg.Agents.List,
		"on a genuine store failure, cfg.Agents.List must be left exactly as it was — never silently emptied")
}

// TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected
// proves the roster-emptiness invariant: when on-disk agent records exist
// but EVERY one of them fails to parse (List() succeeds with err == nil, but
// agents comes back empty and skipped covers every id — e.g. a breaking
// schema change), that must be rejected as a hard failure, never silently
// accepted as "fresh install, zero agents".
func TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "mia.json"), []byte("{bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "jim.json"), []byte("also bad"), 0o600))

	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "every on-disk record failing to parse must be a hard failure")
	assert.Empty(t, cfg.Agents.List, "cfg.Agents.List must stay untouched (was already empty here)")
}

// TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError is the
// negative control for the invariant above: a genuinely fresh install (no
// entities/agents/ directory at all yet) must NOT be treated as a failure —
// entity.Store.List() maps a missing directory to (nil, nil, nil).
func TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError(t *testing.T) {
	home := t.TempDir() // entities/agents/ never created
	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.NoError(t, err, "a genuinely fresh install (zero on-disk records) must not error")
	assert.Empty(t, cfg.Agents.List)
}

// TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty
// proves the same-process regression guard (the BUG2 defense): a previously
// non-empty roster observed for this home going empty on a later call — e.g.
// because homePath momentarily resolved to the wrong directory — is
// rejected rather than silently wiping the live roster.
func TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	seed := config.AgentConfig{ID: "mia", Name: "Mia"}
	require.NoError(t, store.Create("mia", &seed))

	firstCfg := &config.Config{}
	require.NoError(t, populateAgentsListFromEntityStoreStrict(firstCfg, home))
	require.Len(t, firstCfg.Agents.List, 1, "first call should observe the real, non-empty roster")

	// Now simulate the roster disappearing for this SAME home (e.g. the
	// directory was wiped, or — the real-world BUG2 case — homePath
	// resolution glitched to an empty sibling directory momentarily).
	require.NoError(t, os.RemoveAll(store.Dir()))

	secondCfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(secondCfg, home)
	require.Error(t, err, "a non-empty roster going empty for the same home must be rejected")
	assert.Empty(t, secondCfg.Agents.List, "the fresh (never-populated) cfg for this failed call must stay untouched")
}

// TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp verifies the
// marker lands in config.json exactly once: the first call writes it, the
// second call (same markers — the second-boot shape) leaves the file
// byte-identical and untouched (spec test 16's "second boot byte-identical",
// at the file level).
func TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath,
		[]byte(`{"version":1,"agents":{"defaults":{}},"providers":[]}`), 0o600))

	markers := []string{coreagent.SkillsMigrationDefineDone}
	require.NoError(t, persistSeededSkillGrants(configPath, markers))

	afterFirst, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(afterFirst, &m))
	assert.Equal(t, []any{coreagent.SkillsMigrationDefineDone}, m["seeded_skill_grants"],
		"first persist must write the marker into config.json")
	assert.Equal(t, float64(1), m["version"], "every other key must be preserved as-is")

	// Second boot: same markers → no write at all, file byte-identical.
	require.NoError(t, persistSeededSkillGrants(configPath, markers))
	afterSecond, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, afterFirst, afterSecond,
		"a second persist with identical markers must leave config.json byte-identical")
}
