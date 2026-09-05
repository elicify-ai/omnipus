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
	"os"
	"path/filepath"
	"strings"
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

// warmTestHome returns a throwaway $OMNIPUS_HOME for one test, with a
// workspaces/ directory ready to receive seed files. Kept separate from
// browser_testkey_test.go's process-wide shared home: these tests resolve the
// DEFAULT AGENT's workspace out of the same home they seed, so each needs its
// own membership graph rather than a shared one every test appends to.
func warmTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o755); err != nil {
		t.Fatalf("warmTestHome: %v", err)
	}
	return home
}

// warmTestWorkspace seeds one workspace under home whose CoreTeam is members,
// and returns the BrowsingKey it resolves to. Minting the key through the real
// resolver (rather than a literal) is what makes the manager's key and the
// selection path's key the same string by construction.
func warmTestWorkspace(t *testing.T, home, workspaceID string, members ...string) browser.BrowsingKey {
	t.Helper()
	body := `{"id":"` + workspaceID + `","core_team":["` + strings.Join(members, `","`) + `"]}`
	path := filepath.Join(home, "workspaces", workspaceID+".json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("warmTestWorkspace(%q): %v", workspaceID, err)
	}
	key, err := browser.ResolveBrowsingKeyForAgent(home, members[0], workspaceID)
	if err != nil {
		t.Fatalf("warmTestWorkspace(%q): resolve: %v", workspaceID, err)
	}
	return key
}

// newWarmTestManagerForKey builds a manager attached to coord under key. A nil
// coord leaves the manager coordinator-less, which pickWarmBrowserManager must
// refuse to warm.
func newWarmTestManagerForKey(
	t *testing.T, coord *browser.BrowserCoordinator, key browser.BrowsingKey,
) *browser.BrowserManager {
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
		mgr.AttachSharedChrome(coord, key)
	}
	return mgr
}

// warmedKey names the picked manager in a failure message. *BrowserManager has
// no useful String(), so a bare %v dumps its entire internal struct.
func warmedKey(mgr *browser.BrowserManager) string {
	if mgr == nil {
		return "<none>"
	}
	return mgr.BrowsingKey().String()
}

func newWarmTestCoordinator(t *testing.T) *browser.BrowserCoordinator {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	return browser.NewBrowserCoordinator(t.TempDir(), cfg)
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

// Test 24 — TestPickWarmBrowser_UsesResolvedKey (ADR-075 FR-016b).
//
// This is the regression the migration above exists for, isolated. Selection
// used to compare agents.defaults.default_agent_id against mgr.AgentID(). That
// accessor now returns the manager's BROWSING KEY ("ws:<id>"), so the
// comparison could never match a real agent id again — every boot silently
// took the lexicographic branch and warmed whichever workspace sorted first.
// Nothing failed, nothing logged; the wrong Chrome simply got warm.
//
// The distinguishing setup is the point: the default agent's id and its
// workspace id are DIFFERENT strings, and a THIRD workspace sorts ahead of
// both. An implementation that matched on the agent id, or fell back to a
// sort, lands somewhere other than keyWanted.
func TestPickWarmBrowser_UsesResolvedKey(t *testing.T) {
	home := warmTestHome(t)
	coord := newWarmTestCoordinator(t)

	keyFirstBySort := warmTestWorkspace(t, home, "aaaadecoyworkspace", "decoy")
	keyWanted := warmTestWorkspace(t, home, "zzzzrealworkspace", "mia")

	mgrs := []*browser.BrowserManager{
		newWarmTestManagerForKey(t, coord, keyFirstBySort),
		newWarmTestManagerForKey(t, coord, keyWanted),
	}
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultAgentID = "mia"

	got, reason := pickWarmBrowserManager(cfg, home, mgrs)
	if reason != "" {
		t.Fatalf("expected a pick, got skip reason %q", reason)
	}
	if got == nil {
		t.Fatal("expected the default agent's resolved workspace to be warmed, got nothing")
	}
	if got.BrowsingKey() != keyWanted {
		t.Fatalf("warmed %q, want %q — selection must resolve the default agent to a WORKSPACE, "+
			"not compare its id against a manager's key", warmedKey(got), keyWanted.String())
	}
	if got.BrowsingKey() == keyFirstBySort {
		t.Fatal("warmed the sorted-first workspace: the lexicographic fallback is back")
	}

	// Exactly ONE instance is chosen — never N (FR-016b). A second call with
	// the same inputs must return the same single manager, not accumulate.
	again, _ := pickWarmBrowserManager(cfg, home, mgrs)
	if again != got {
		t.Fatal("selection must be stable: two boots of one install warm the same browser")
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
