package gateway

// Warm-boot coverage: the boot-time browser warm-up ladder's two NEW steps —
// the first TAB (tools.browser.warm_tab_at_boot) and the WebRTC CAPTURE
// (tools.browser.warm_capture_at_boot, with its idle stop). Step 0, the Chrome
// PROCESS, is covered by browser_warmup_test.go and unchanged here.
//
// What is testable off a live host, and what is not: the DECISIONS are
// (gating, agent selection, the idle-stop/handover rule), and they are what
// this file pins. Actually launching Chrome, loading the capture extension,
// negotiating WebRTC and producing a frame are not — they need a real browser,
// a real encoder page and a real relay, so those stay covered by the
// browser-package e2e tests and by live UAT. The idle watcher is written
// against a small interface (warmCaptureHandle) precisely so its rule can be
// proven here without any of that.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// --- gating -----------------------------------------------------------------

// TestWarmBoot_DefaultConfigWarmsTabAndCapture pins the shipped defaults
// against the REAL constructor: a fresh install must warm the tab AND the
// capture, and must idle-stop the capture rather than encoding forever.
func TestWarmBoot_DefaultConfigWarmsTabAndCapture(t *testing.T) {
	cfg := config.DefaultConfig()
	if !browserWarmTabEnabled(cfg) {
		t.Fatal("expected a default config to warm the first browser TAB at boot")
	}
	if !browserWarmCaptureEnabled(cfg) {
		t.Fatal("expected a default config to warm the WebRTC CAPTURE at boot")
	}
	if got, want := warmCaptureIdleTimeout(cfg), 5*time.Minute; got != want {
		t.Fatalf("default warm-capture idle timeout = %s, want %s", got, want)
	}
}

// TestWarmBoot_StepsAreIndependentlyControllable is the whole point of two
// flags instead of one: the cheap step must stay available to an operator who
// refuses the expensive one, and vice versa.
func TestWarmBoot_StepsAreIndependentlyControllable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Browser.WarmCaptureAtBoot = false
	if !browserWarmTabEnabled(cfg) {
		t.Fatal("turning the CAPTURE warm-up off must not disable the TAB warm-up")
	}
	if browserWarmCaptureEnabled(cfg) {
		t.Fatal("expected capture warm-up disabled")
	}

	cfg = config.DefaultConfig()
	cfg.Tools.Browser.WarmTabAtBoot = false
	if browserWarmTabEnabled(cfg) {
		t.Fatal("expected tab warm-up disabled")
	}
	if !browserWarmCaptureEnabled(cfg) {
		t.Fatal("turning the TAB warm-up off must not disable the CAPTURE warm-up")
	}
}

// TestWarmBoot_InheritsEveryProcessWarmUpOptOut is the constraint that matters
// most for an operator: the escape hatches they already know about
// (warm_at_boot, enabled, a remote cdp_url, OMNIPUS_SKIP_BROWSER_PREPROVISION)
// must silence the NEW warm surfaces too. A remote cdp_url in particular must
// never cause a local launch of anything.
func TestWarmBoot_InheritsEveryProcessWarmUpOptOut(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*config.Config)
		env   string
	}{
		{"warm_at_boot=false", func(c *config.Config) { c.Tools.Browser.WarmAtBoot = false }, ""},
		{"browser tools disabled", func(c *config.Config) { c.Tools.Browser.Enabled = false }, ""},
		{"remote cdp_url", func(c *config.Config) { c.Tools.Browser.CDPURL = "ws://elsewhere:9222" }, ""},
		{"skip env var", func(c *config.Config) {}, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("OMNIPUS_SKIP_BROWSER_PREPROVISION", tc.env)
			}
			cfg := config.DefaultConfig()
			tc.apply(cfg)
			if browserWarmTabEnabled(cfg) {
				t.Error("expected the TAB warm-up to be disabled by this opt-out")
			}
			if browserWarmCaptureEnabled(cfg) {
				t.Error("expected the CAPTURE warm-up to be disabled by this opt-out")
			}
		})
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

// --- agent selection --------------------------------------------------------

func newWarmTestManager(t *testing.T, coord *browser.BrowserCoordinator, agentID string) *browser.BrowserManager {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	cfg.ProfileDir = t.TempDir()
	mgr, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("browser.NewBrowserManager: %v", err)
	}
	if coord != nil {
		mgr.AttachSharedChrome(coord, browserTestKey(t, agentID))
	}
	return mgr
}

// warmedAgentID names the picked manager in a failure message. *BrowserManager
// has no useful String(), so a bare %v dumps its entire internal struct.
func warmedAgentID(mgr *browser.BrowserManager) string {
	if mgr == nil {
		return "<none>"
	}
	return mgr.AgentID()
}

func newWarmTestCoordinator(t *testing.T) *browser.BrowserCoordinator {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	return browser.NewBrowserCoordinator(t.TempDir(), cfg)
}

func TestPickWarmBrowserManager_PrefersTheDefaultAgent(t *testing.T) {
	coord := newWarmTestCoordinator(t)
	mgrs := []*browser.BrowserManager{
		newWarmTestManager(t, coord, "ava"),
		newWarmTestManager(t, coord, "mia"),
		newWarmTestManager(t, coord, "jim"),
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = "mia"

	got := pickWarmBrowserManager(cfg, mgrs)
	if got == nil || got.AgentID() != "mia" {
		t.Fatalf("expected the DEFAULT agent's manager to be warmed, got %q", warmedAgentID(got))
	}
}

// TestPickWarmBrowserManager_DeterministicWithoutADefault: BrowserManagers()
// ranges a map, so an unsorted pick would warm a different agent on each boot
// — and could differ between a macOS and a Linux host of the same install,
// which is the platform-divergent behaviour this project forbids. Repeat it
// enough times that map-order luck cannot pass.
func TestPickWarmBrowserManager_DeterministicWithoutADefault(t *testing.T) {
	coord := newWarmTestCoordinator(t)
	mgrs := []*browser.BrowserManager{
		newWarmTestManager(t, coord, "zed"),
		newWarmTestManager(t, coord, "ava"),
		newWarmTestManager(t, coord, "mia"),
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = ""

	for i := 0; i < 25; i++ {
		got := pickWarmBrowserManager(cfg, mgrs)
		if got == nil || got.AgentID() != "ava" {
			t.Fatalf("iteration %d: expected the lexicographically-first agent (ava), got %q", i, warmedAgentID(got))
		}
	}
}

// TestPickWarmBrowserManager_UnknownDefaultFallsBack — a default_agent_id that
// has no browser manager (browser tools not registered for it) must not leave
// the install with a cold first open; fall back to the deterministic pick.
func TestPickWarmBrowserManager_UnknownDefaultFallsBack(t *testing.T) {
	coord := newWarmTestCoordinator(t)
	mgrs := []*browser.BrowserManager{
		newWarmTestManager(t, coord, "mia"),
		newWarmTestManager(t, coord, "ava"),
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = "nobody"

	got := pickWarmBrowserManager(cfg, mgrs)
	if got == nil || got.AgentID() != "ava" {
		t.Fatalf("expected a fallback to the lexicographically-first agent, got %q", warmedAgentID(got))
	}
}

// TestPickWarmBrowserManager_SkipsUnwarmableManagers: a manager with no
// coordinator would launch its OWN second Chrome (legacy managed mode) if
// warmed, and one with no agent id cannot key a capture session. Neither is a
// candidate — and a nil entry must not panic.
func TestPickWarmBrowserManager_SkipsUnwarmableManagers(t *testing.T) {
	coord := newWarmTestCoordinator(t)
	noCoordinator := newWarmTestManager(t, nil, "")
	noAgentID := newWarmTestManager(t, coord, "")
	good := newWarmTestManager(t, coord, "ray")

	got := pickWarmBrowserManager(config.DefaultConfig(),
		[]*browser.BrowserManager{nil, noCoordinator, noAgentID, good})
	if got == nil || got.AgentID() != "ray" {
		t.Fatalf("expected the only warmable manager (ray), got %q", warmedAgentID(got))
	}

	if got := pickWarmBrowserManager(config.DefaultConfig(),
		[]*browser.BrowserManager{nil, noCoordinator, noAgentID}); got != nil {
		t.Fatalf("expected nil when nothing is warmable, got %q", warmedAgentID(got))
	}
	if got := pickWarmBrowserManager(config.DefaultConfig(), nil); got != nil {
		t.Fatalf("expected nil for an empty manager list, got %q", warmedAgentID(got))
	}
}

// --- the idle stop ----------------------------------------------------------

// fakeWarmCapture stands in for *browser.CaptureSession — see this file's
// header for why the watcher is written against an interface.
type fakeWarmCapture struct {
	viewers    atomic.Int64
	stops      atomic.Int64
	recaptures atomic.Int64
	done       chan struct{}
	closeOnce  sync.Once
}

func newFakeWarmCapture() *fakeWarmCapture {
	return &fakeWarmCapture{done: make(chan struct{})}
}

func (f *fakeWarmCapture) ViewerCount() int      { return int(f.viewers.Load()) }
func (f *fakeWarmCapture) Done() <-chan struct{} { return f.done }

// Recapture counts the handover rebuild the watcher forces on a capture that
// warmed unwatched for long enough to have adapted — see
// browser_warmboot_handover_test.go for the rule it proves.
func (f *fakeWarmCapture) ResetAdaptation(string) { f.recaptures.Add(1) }

// Stop mirrors CaptureSession.Stop's documented idempotence.
func (f *fakeWarmCapture) Stop() {
	f.stops.Add(1)
	f.closeOnce.Do(func() { close(f.done) })
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
	startBrowserWarmBoot(context.Background(), cfg, nil, nil) // nil agent loop: must not panic

	cfg = config.DefaultConfig()
	startBrowserWarmBoot(context.Background(), cfg, nil, nil)
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
