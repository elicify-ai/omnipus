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

func (f *fakeWarmCapture) ViewerCount() int { return int(f.viewers.Load()) }

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
