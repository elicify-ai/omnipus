// Package browser implements browser automation tools using chromedp (pure Go CDP).
//
// Implements US-4 (managed mode), US-6 (remote CDP mode), US-7 (resource limits)
// from the Wave 4 spec. All navigations are SSRF-checked via pkg/security.SSRFChecker.
package browser

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// DebugPort is the fixed loopback TCP port the managed Chromium binds its
// DevTools WebSocket to. Pinning this lets the gateway's Landlock
// NET_CONNECT_TCP allow-list include it (see pkg/gateway/sandbox_apply.go).
// Without a fixed port, chromedp defaults to remote-debugging-port=0,
// Chrome picks a random ephemeral port, and the gateway's connect-to-
// chromedp dial returns EACCES.
//
// Exposed as a package constant (not a config knob) so the sandbox layer
// and the browser layer agree without a circular import. Operators who
// need a different port today can recompile; a config knob is a v0.3
// follow-up if a real conflict surfaces.
const DebugPort = 9223

// browserDebugPort is the string form passed to chromedp.Flag (which takes
// the flag value as a string). Kept private so callers use DebugPort.
const browserDebugPort = "9223"

// checkDebugPortAvailable is a best-effort preflight: it attempts to bind
// port on loopback and immediately releases it, returning a clear,
// diagnosable error if the bind fails. Exists because chromedp's own
// failure mode when the fixed CDP debug port (DebugPort) is already held by
// something else is an opaque, slow "websocket url timeout reached" deep
// inside the CDP handshake — it never names the port or says it's in use.
// The motivating live incident: an orphaned Chrome process left over from an
// unrelated prior run was squatting DebugPort, and every subsequent launch
// timed out with no indication of the real cause.
//
// Deliberately NOT a reason to make DebugPort configurable or random —
// pkg/gateway/sandbox_apply.go's kernel-enforced bind-port allow-list
// depends on it being fixed and known at sandbox-policy-build time (see
// DebugPort's doc comment). This function only makes the failure
// diagnosable when the fixed port is unexpectedly unavailable.
//
// TOCTOU CAVEAT: this is a check-then-act race by construction — the
// listener opened here is closed immediately, and nothing prevents some
// other process from binding the port in the (typically sub-millisecond)
// window between this check and chromedp's own real bind moments later.
// This function is a diagnostics aid for the common case (a long-lived
// squatter already holding the port when we start), not a guarantee; a
// failure that slips past this check still surfaces as chromedp's own
// (less clear, but not silent) launch error.
func checkDebugPortAvailable(port uint16) error {
	// Deliberately IPv4-only (127.0.0.1), not [::1]/"localhost": the managed-
	// mode allocator never sets --remote-debugging-address, so Chrome binds
	// 127.0.0.1 by default and chromedp dials ws://127.0.0.1:<port>. A process
	// squatting ONLY the v6 loopback ([::1]) therefore cannot conflict with
	// our launch — probing v6 here would solve a non-problem and risks a false
	// positive on a legitimate v6-only listener. Matching the actual bind
	// family keeps this preflight truthful.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf(
			"browser: CDP debug port %d is already in use by another process — "+
				"close it or stop the other instance (%w)", port, err)
	}
	// The bind itself is the whole check — it already succeeded. A failure
	// to close the (immediately-discarded) probe listener does not mean the
	// port is unavailable, so it is logged, not treated as this function's
	// result.
	if closeErr := ln.Close(); closeErr != nil {
		logger.WarnCF("browser", "debug port preflight: failed to close probe listener", map[string]any{
			"port":  port,
			"error": closeErr.Error(),
		})
	}
	return nil
}

// BrowserConfig holds browser automation configuration.
// Mapped from config.json: tools.browser.*
type BrowserConfig struct {
	Enabled        bool          `json:"enabled"`
	Headless       bool          `json:"headless"`
	CDPURL         string        `json:"cdp_url"`         // Remote CDP WebSocket URL (US-6)
	PageTimeout    time.Duration `json:"page_timeout"`    // Per-page load timeout (US-7, default 30s)
	MaxTabs        int           `json:"max_tabs"`        // Max concurrent tabs (US-7, default 5)
	PersistSession bool          `json:"persist_session"` // Persist cookies/localStorage across restarts
	ProfileDir     string        `json:"profile_dir"`     // User data dir (default ~/.omnipus/browser/profiles/default/)
	// ExecPath overrides Chromium discovery. When empty the manager prefers
	// a system chromium/google-chrome on PATH and falls back to a managed
	// install under <ProfileDir>/../chromium/ (downloaded in the background
	// at gateway boot via Preprovision, or lazily on first tool use as a
	// fallback).
	ExecPath string `json:"exec_path,omitempty"`
}

// DefaultConfig returns a BrowserConfig with spec-defined defaults.
// Returns an error if no home directory (OMNIPUS_HOME or user home) can
// be determined. Prefers $OMNIPUS_HOME so the profile lands inside the
// gateway's Landlock-allowed workspace; falls back to the user's home
// directory only when OMNIPUS_HOME is unset.
func DefaultConfig() (BrowserConfig, error) {
	base := os.Getenv("OMNIPUS_HOME")
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return BrowserConfig{}, fmt.Errorf("browser: cannot determine home directory: %w", err)
		}
		base = filepath.Join(homeDir, ".omnipus")
	}
	return BrowserConfig{
		Enabled:     false,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		MaxTabs:     5,
		ProfileDir:  filepath.Join(base, "browser", "profiles", "default"),
	}, nil
}

// tabEntry tracks one browser tab — a single chromedp target — within a
// browsing context's tab set (ADR-041 D1). Before ADR-041 this data lived
// directly on sessionEntry (one tab per session); it is now the element type
// of sessionEntry.tabs so a browsing context can hold more than one.
type tabEntry struct {
	ctx      context.Context
	cancel   context.CancelFunc
	targetID target.ID
	// title/url are a best-effort, occasionally-stale cache (refreshed on
	// creation/adoption via refreshTabMeta, and opportunistically on
	// target.EventTargetInfoChanged via handleTargetEvent) — cosmetic only,
	// used for browser_list_tabs and the generated.BrowserTabsFrame
	// broadcast (ADR-041 D3/D4). Never load-bearing for tool correctness.
	title string
	url   string
}

// sessionEntry is a browsing context: an ordered set of tabs (chromedp
// targets) with one active tab (ADR-041 D1). Before ADR-041 a sessionEntry
// held exactly one tab directly; it is now reframed as a tab SET so a
// target="_blank" click or window.open can be adopted as an additional tab
// instead of stranding the agent on the opener page — see manager.go's
// package doc and ADR-041. Guarded by BrowserManager.mu, same as before.
type sessionEntry struct {
	tabs      []*tabEntry
	activeIdx int
	// browserCtx/browserCancel is the browsing context's browser-owning
	// chromedp context — the ONE-TIME chromedp.NewContext(m.allocCtx) + Run
	// that actually launches (managed mode) or attaches to (remote CDP mode)
	// the running *Browser (see bootstrapBrowserCtx's doc comment for the
	// chromedp mechanics: a *Browser is bound to the FIRST context created
	// from an allocator, never to the allocator context itself). EVERY tab in
	// this browsing context — tab 0 included — is created as a CHILD of
	// browserCtx (chromedp.NewContext(se.browserCtx, ...)), never straight off
	// m.allocCtx again; that reuse-off-the-allocator bug (adoptTarget/OpenTab
	// each independently calling chromedp.NewContext(m.allocCtx, ...) for the
	// 2nd+ tab) is exactly what made tab adoption try to launch a SECOND
	// Chromium on the same fixed debug port and fail — the live-UAT-caught
	// bug this field's introduction fixes.
	//
	// browserCtx owns an "implicit" initial target (the about:blank tab
	// Chrome opens on launch, which the bootstrap Run attaches browserCtx's
	// OWN chromedp.Context.Target to as a side effect) that is deliberately
	// NEVER exposed as a tabEntry/Tab — se.tabs holds only EXPLICITLY created
	// children of browserCtx. That implicit target has no opener among our
	// tabs (it's the browser's own default tab, not opened by any page), so
	// ReconcileTabs/handleTargetEvent's existing "OpenerID == '' → not ours"
	// filters already exclude it from adoption without any extra code.
	//
	// browserCancel MUST be called ONLY on whole-session teardown
	// (CloseSession, Shutdown, Session()'s crash-recovery path) — never on an
	// individual tab close. Closing any single user tab (including tab 0)
	// cancels only THAT tab's own cancelFunc, which — because that tab is a
	// non-"first" child of browserCtx, not browserCtx itself — closes just its
	// one CDP target and leaves the browser (and every sibling tab) alive.
	// nil only for hand-built sessionEntry literals in tests that bypass
	// Session()/createFirstTab entirely (e.g. live_deadlock_test.go,
	// inspect_test.go) — every nil-checked at its two call sites.
	browserCtx    context.Context
	browserCancel context.CancelFunc
	// listenerTarget tracks which tab's chromedp ctx currently has the
	// ADR-041 D2 passive Target.targetCreated listener installed (the zero
	// value, "", means none yet). installTargetListenerLocked (re-)installs
	// it on se.tabs[0] whenever that differs from the tab the listener is
	// currently bound to — most commonly once, at browsing-context creation,
	// but also whenever tab 0 itself is closed (ADR-041 fix F3):
	// chromedp.ListenTarget's registration is scoped to the ctx it was given,
	// so closing the tab that ctx belongs to silently ends the listener
	// forever unless it is re-armed on whichever tab becomes the new tab 0.
	// See installTargetListenerLocked's doc comment for why this is
	// installed on ONE tab at a time, not every tab.
	listenerTarget target.ID
}

// active returns the currently-active tab, or nil if activeIdx is somehow
// out of range (defensive; CloseTab/Session's recovery path guarantee at
// least one valid tab exists for any sessionEntry reachable from
// BrowserManager.sessions).
func (se *sessionEntry) active() *tabEntry {
	if se == nil || se.activeIdx < 0 || se.activeIdx >= len(se.tabs) {
		return nil
	}
	return se.tabs[se.activeIdx]
}

// indexOfTarget returns the tab-set index of targetID, or -1 if this
// browsing context doesn't already track it.
func (se *sessionEntry) indexOfTarget(id target.ID) int {
	for i, t := range se.tabs {
		if t.targetID == id {
			return i
		}
	}
	return -1
}

// Tab is the public, metadata-only snapshot of one browser tab in a
// session's tab set (ADR-041 D1/D3). Returned by ListTabs/SwitchTab/
// CloseTab/OpenTab/ReconcileTabs and passed to the ADR-041 D4
// tabs-changed callback (SetTabsChangedFunc) — used by pkg/gateway to build
// the generated.BrowserTabsFrame broadcast and by the browser_list_tabs
// tool. Carries no chromedp context; callers that need the tab's live
// context resolve it via BrowserManager.Session (which always returns the
// ACTIVE tab's context — ADR-041 D1).
type Tab struct {
	Index  int
	Title  string
	URL    string
	Active bool
}

// BrowserManager manages the Chromium lifecycle and tab pool.
// Thread-safe — all methods may be called concurrently.
//
// Session model: tools operate on a persistent "default" session tab so that
// browser_navigate, browser_click, browser_get_text, etc. act on the same page.
// Additional sessions can be created for parallel browsing up to MaxTabs.
type BrowserManager struct {
	cfg         BrowserConfig
	ssrf        *security.SSRFChecker // never nil — enforced by NewBrowserManager
	mu          sync.Mutex
	allocCtx    context.Context    // chromedp allocator context
	allocCancel context.CancelFunc // cancels the allocator (kills browser)
	sessions    map[string]*sessionEntry
	// pending tracks session IDs currently being created by Session() — see
	// Session()'s doc comment (ADR-038 deadlock postmortem) for why tab
	// creation must release m.mu before its blocking chromedp.Run call, and
	// why a concurrent Session() call for the same ID must wait here instead
	// of also calling chromedp.NewContext/Run for that ID (which would
	// create and leak a second tab, and corrupt MaxTabs accounting). Lazily
	// initialized; nil is a valid empty state.
	pending map[string]chan struct{}
	started bool

	// execPath holds the Chromium-binary resolution caches (success + negative),
	// refactored into a reusable struct shared with the BrowserCoordinator
	// (exec_resolver.go). A dedicated lock (execPath.mu), deliberately separate
	// from m.mu, so the (potentially slow) PATH probe or chrome-for-testing
	// download never blocks the tab/session bookkeeping every other browser
	// tool call needs m.mu for (ADR-038 discipline — see Session()'s and
	// ensureStarted's doc comments). See execPathCaches.resolve for the full
	// caching rationale (success cache re-validated with os.Stat per hit;
	// negative cache returned verbatim within its TTL to avoid re-probing on
	// every browser_* call on a dead host).
	execPath execPathCaches

	// coordinator (ADR-043) is the gateway-scoped shared-Chrome owner. nil in
	// remote-CDP-override mode (cfg.CDPURL set) and in tests that exercise the
	// legacy one-manager-one-Chrome path. When non-nil, ensureStarted's managed-
	// mode branch asks the coordinator to launch+provide the shared Chrome
	// instead of building its own ExecAllocator, and bootstrapBrowserCtx
	// re-adopts the coordinator-owned browserCtxID non-owningly.
	//
	// CRIT-003 (load-bearing): when coordinator != nil, NO manager path ever
	// calls chromedp.WithNewBrowserContext — that would auto-dispose the
	// context on the manager's reload-time cancel, destroying cookies every
	// Settings save. The manager only ever re-adopts via WithExistingBrowserContext.
	coordinator *BrowserCoordinator
	// agentID identifies this per-agent manager to the coordinator (Register/
	// Release/RemoveAgent are keyed by it). Set via AttachSharedChrome.
	agentID string
	// browserCtxID is the coordinator-owned browser context id this manager
	// re-adopts. Set by ensureStarted's coordinator branch from Register's
	// return; cleared by dropConnection/invalidateConnection on reload/crash.
	// Empty in remote-CDP-override mode + the no-coordinator test fallback
	// (those paths use Chrome's default "" browser context).
	browserCtxID cdp.BrowserContextID

	// pendingAdopt tracks CDP target IDs currently being adopted (ADR-041
	// D2's adoptTarget), mirroring `pending`'s exact race-guard shape: the
	// best-effort passive listener (installTargetListenerLocked) and a
	// deterministic ReconcileTabs pass can both observe the same
	// freshly-created target and race to adopt it. Unlike `pending`, a racer
	// that finds an entry here does not just no-op — it WAITS on the entry's
	// done channel and returns the SAME result the winning adopter computed
	// (see adoptTarget's doc comment for why: with a real browser attached,
	// the async passive listener routinely wins this race first — CDP
	// target-created events can arrive before the click's own CDP round trip
	// even returns — leaving the caller-visible, deterministic ReconcileTabs
	// path (browser_click's own adoption attempt) with nothing to report
	// unless it waits for the in-flight winner instead of silently
	// discarding the "someone else is already handling this" case). Lazily
	// initialized; nil is a valid empty state.
	pendingAdopt map[target.ID]*pendingAdoptEntry

	// listTargets executes chromedp.Targets against a resolved tab context
	// to list every CDP target the browser currently knows about (ADR-041
	// D2's ReconcileTabs). A field — not a direct chromedp.Targets call at
	// the use site — so tests can substitute a controllable stand-in and
	// deterministically exercise adoption without a real Chromium/CDP
	// connection, exactly mirroring evalCDP's testability rationale (see its
	// doc comment below). nil-checked at the call site and defaults to
	// chromedp.Targets.
	listTargets func(ctx context.Context) ([]*target.Info, error)

	// createTabFn, when non-nil, overrides createTab's default CDP-driving
	// body. A field — not a direct call at each use site — purely for
	// testability, mirroring evalCDP/listTargets' exact rationale: tests
	// substitute a controllable stand-in so OpenTab/CloseTab's
	// last-tab-replacement and adoptTarget's full flow can be exercised
	// deterministically without a real Chromium/CDP connection. nil by
	// default, in which case createTab runs its normal chromedp body.
	createTabFn func(allocCtx context.Context, targetID target.ID) (*tabEntry, error)

	// tabsChanged is invoked (ADR-041 D4) whenever a browsing context's tab
	// set changes shape or its active tab moves — open/close/switch/adopt,
	// and best-effort title/url updates observed via the passive target
	// listener. Optional; nil is a valid no-op default. Set via
	// SetTabsChangedFunc — pkg/tools/browser/live.go wires this to
	// LiveViewRegistry.handleTabsChanged so pkg/gateway's browser WS handler
	// can broadcast generated.BrowserTabsFrame and rebind the live
	// screencast to a newly-active tab. MUST be invoked with NO
	// BrowserManager lock held (ADR-038 rule) — every call site in this
	// file dispatches it only via notifyTabsChanged, after releasing m.mu.
	tabsChanged func(sessionID string, tabs []Tab, activeIdx int)

	// live is the ADR-038 live-interactive-browser engine registry, keyed by
	// session ID. Since BrowserManager is itself scoped to one agent (see
	// pkg/agent/loop.go's per-agent manager map), a LiveView keyed by session
	// ID inside this manager already satisfies the (agentID, sessionID)
	// uniqueness ADR-038 D3 calls for — no separate agentID key is needed
	// here. Never nil after NewBrowserManager.
	live *LiveViewRegistry

	// evalCDP executes a one-off, non-screencast chromedp action against a
	// resolved tab context — currently only InspectPoint's (ADR-039 D-B3)
	// chromedp.Evaluate call. A field rather than a direct chromedp.Run call,
	// mirroring LiveView.runCDP's rationale exactly (see runCDPWithTimeout's
	// doc comment in live.go): it lets tests substitute a controllable
	// stand-in to deterministically exercise InspectPoint's panic-recovery
	// path (ADR-039 UAT BE-2) without a real Chromium/CDP connection — see
	// inspect_test.go's TestInspectPoint_PanicDuringCDPCall_RecoversToSoftNoResult.
	// nil-checked at the call site and defaults to chromedp.Run, so every
	// existing hand-built &BrowserManager{} test literal that never calls
	// InspectPoint is unaffected.
	evalCDP func(ctx context.Context, actions ...chromedp.Action) error
}

// NewBrowserManager creates a manager. ssrf must be non-nil — SSRF protection
// is mandatory for browser tools (SEC-24). The browser is not launched until
// the first tool invocation (lazy init).
func NewBrowserManager(cfg BrowserConfig, ssrf *security.SSRFChecker) (*BrowserManager, error) {
	if ssrf == nil {
		return nil, fmt.Errorf(
			"browser: SSRFChecker is required — cannot create browser manager without SSRF protection (SEC-24)",
		)
	}
	mgr := &BrowserManager{
		cfg:      cfg,
		ssrf:     ssrf,
		sessions: make(map[string]*sessionEntry),
	}
	mgr.live = newLiveViewRegistry(mgr)
	return mgr, nil
}

// Live returns this manager's ADR-038 live-interactive-browser engine
// registry. Never nil for a manager constructed via NewBrowserManager.
func (m *BrowserManager) Live() *LiveViewRegistry {
	return m.live
}

// AttachSharedChrome wires this per-agent manager to the gateway-scoped shared
// Chrome coordinator (ADR-043). When set, ensureStarted asks the coordinator to
// launch/provide the shared Chrome and re-adopts the coordinator-owned browser
// context non-owningly (CRIT-003: no WithNewBrowserContext on a manager path).
// A nil coordinator (the default for direct/test construction) keeps the legacy
// per-manager ExecAllocator behavior. agentID identifies this manager to the
// coordinator (Register/Release/RemoveAgent are keyed by it).
func (m *BrowserManager) AttachSharedChrome(coordinator *BrowserCoordinator, agentID string) {
	m.coordinator = coordinator
	m.agentID = agentID
}

// blockedSchemes are URL schemes that bypass network-level SSRF and must be
// denied at the application layer. file:// would bypass Landlock restrictions.
var blockedSchemes = map[string]bool{
	"file":             true,
	"javascript":       true,
	"data":             true,
	"chrome":           true,
	"chrome-extension": true,
}

// ValidateURL checks a URL against SSRF rules and blocked schemes.
// Returns an error if navigation should be denied.
func (m *BrowserManager) ValidateURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: invalid URL %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return fmt.Errorf("browser: URL %q has no scheme — use http:// or https://", rawURL)
	}
	if blockedSchemes[scheme] {
		return fmt.Errorf("browser: %s:// URLs are blocked for security reasons", scheme)
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("browser: only http:// and https:// URLs are permitted, got %s://", scheme)
	}

	// SSRF check: resolve host, block private IPs and cloud metadata (SEC-24)
	if err := m.ssrf.CheckURL(ctx, rawURL); err != nil {
		return fmt.Errorf("browser: navigation blocked by SSRF policy: %w", err)
	}

	return nil
}

// ensureStarted lazily initializes the browser. Must be called under m.mu.
//
// ADR-038 discipline extended to exec-path resolution: resolveExecPath
// (below) can now shell out to probe PATH candidates (`--version`, up to
// chromiumProbeTimeout each) or even trigger a managed chrome-for-testing
// download on first use. Neither may run with m.mu held — a slow/broken
// probe or an in-flight 100+MB download would otherwise freeze every OTHER
// browser tool call (any session, any tab) for its entire duration,
// recreating the exact "single global mutex held across a blocking external
// call" shape Session()'s doc comment describes as the ADR-038 postmortem
// bug, just with exec(1) standing in for CDP. So: m.mu is released for the
// resolveExecPath call only, then re-acquired before continuing. A
// concurrent caller that raced in during that window (another
// Session()/createFirstTab()/OpenTab() call, still seeing m.started ==
// false) and ALSO ran ensureStarted's managed-mode setup to completion is
// detected by re-checking m.started immediately after re-acquiring the
// lock — this goroutine's own (fully valid, just redundant) exec-path
// resolution is then discarded in favor of whichever goroutine's
// chromedp.NewExecAllocator call and m.allocCtx/m.started assignment
// happened to win, mirroring the discard-the-loser pattern
// createFirstTab/OpenTab already use for a redundant tab. This never
// double-launches a subprocess: chromedp.NewExecAllocator only builds an
// allocator config, it does not spawn Chromium — that happens later and
// lazily, in bootstrapBrowserCtx's chromedp.Run, which only ever reads the
// WINNING m.allocCtx field, never a discarded local variable.
func (m *BrowserManager) ensureStarted() error {
	if m.started {
		return nil
	}

	if m.cfg.CDPURL != "" {
		// US-6: Remote CDP mode — connect to external Chromium (operator
		// override; the coordinator is bypassed entirely here).
		allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), m.cfg.CDPURL)
		m.allocCtx = allocCtx
		m.allocCancel = cancel
		m.started = true
		logger.InfoCF("browser", "Connected to remote CDP", map[string]any{
			"url": m.cfg.CDPURL,
		})
		return nil
	}

	// ADR-043 shared-Chrome mode: when a coordinator is wired (the normal
	// gateway case), ask it to launch+provide the ONE shared Chrome instead of
	// building a per-manager ExecAllocator. The coordinator owns the Chrome
	// process + this agent's browser context; the manager builds only a remote-
	// allocator connection against the coordinator's cdpURL and re-adopts the
	// coordinator-owned browserCtxID non-owningly (CRIT-003: WithNewBrowserContext
	// is NEVER called on a manager path — only the coordinator does that).
	//
	// Register blocks on the (possibly cold) Chrome launch, so m.mu is released
	// around it — same ADR-038 no-lock-across-blocking-call discipline as the
	// resolveExecPath unlock/relock below. A concurrent ensureStarted that won
	// while m.mu was released is handled by the post-relock m.started check.
	if m.coordinator != nil {
		agentID := m.agentID
		m.mu.Unlock()
		cdpURL, browserCtxID, regErr := m.coordinator.Register(context.Background(), agentID, m)
		m.mu.Lock()
		if regErr != nil {
			return fmt.Errorf("browser: shared Chrome unavailable: %w", regErr)
		}
		if m.started {
			// A concurrent ensureStarted won while m.mu was released. Discard
			// our redundant resolution; the winner already set m.allocCtx.
			return nil
		}
		allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), cdpURL)
		m.allocCtx = allocCtx
		m.allocCancel = cancel
		m.browserCtxID = browserCtxID
		m.started = true
		logger.InfoCF("browser", "Browser connected to shared Chrome (coordinator mode)", map[string]any{
			"agent_id":       agentID,
			"browser_ctx_id": string(browserCtxID),
		})
		return nil
	}

	// US-4: Managed mode — launch local Chromium (no coordinator: tests +
	// the legacy one-manager-one-Chrome path).
	if err := os.MkdirAll(m.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("browser: cannot create profile directory %s: %w", m.cfg.ProfileDir, err)
	}

	// Clean up stale SingletonLock files. When Chromium exits ungracefully
	// (kill -9, crash, or the gateway's chromedp allocator canceling mid-
	// startup), `SingletonLock` / `SingletonCookie` / `SingletonSocket`
	// stay behind in the profile dir. The next launch refuses to start with:
	//   "Failed to create .../SingletonLock: File exists (17)
	//    Failed to create a ProcessSingleton for your profile directory."
	// and every subsequent browser_navigate fails. We always own this
	// profile directory exclusively (single chromedp allocator per gateway
	// process; tabs share the same Chromium instance), so it is safe to
	// remove these on each lazy-init. Symlinks (which is what Chrome uses
	// for SingletonLock — a symlink whose target encodes pid + hostname)
	// must be removed with os.Remove; os.RemoveAll would fail to follow
	// them in some edge cases.
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		path := filepath.Join(m.cfg.ProfileDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("browser", "Failed to remove stale Chromium singleton file", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
		}
	}

	// Release m.mu across exec-path resolution — see this function's doc
	// comment above for why (the probe/download it can trigger must never
	// run with m.mu held).
	m.mu.Unlock()
	execPath, err := m.resolveExecPath(context.Background())
	m.mu.Lock()
	if err != nil {
		return fmt.Errorf("browser: cannot locate chromium: %w", err)
	}
	if m.started {
		// A concurrent ensureStarted() call raced in and already finished
		// setting up the allocator while m.mu was released above — discard
		// our own now-redundant resolution instead of launching a second
		// allocator. See this function's doc comment.
		return nil
	}

	// Build the ExecAllocator options via the shared helper (managedExecAllocatorOpts,
	// exec_resolver.go) — identical to the coordinator's launch path, so the two
	// never diverge. See that helper for the per-flag rationale (fixed CDP port,
	// sandbox disable, stealth flags, XDG/HOME jail, etc.).
	opts := managedExecAllocatorOpts(execPath, m.cfg)

	// Preflight the fixed CDP debug port before committing to a launch.
	// Without this, a port already held by something else (observed live: an
	// orphaned Chrome process left over from an unrelated prior run) makes
	// chromedp's launch fail with an opaque "websocket url timeout reached"
	// deep inside the CDP handshake — nothing names the real cause. This
	// runs exactly once per manager (ensureStarted's managed-mode branch is
	// itself latched by m.started, checked above after the resolveExecPath
	// re-lock), guaranteed to happen before this manager has ever launched
	// its own Chrome — so, unlike a check placed at every bootstrapBrowserCtx
	// call (which runs once per NEW browsing context, not just once per
	// manager), it can never mistake a healthy earlier launch of OUR OWN
	// Chrome for "something else is squatting the port."
	if err := checkDebugPortAvailable(DebugPort); err != nil {
		return err
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	m.allocCtx = allocCtx
	m.allocCancel = cancel
	m.started = true

	logger.InfoCF("browser", "Browser allocator ready (managed mode)", map[string]any{
		"headless":    m.cfg.Headless,
		"profile_dir": m.cfg.ProfileDir,
		"exec_path":   execPath,
	})
	return nil
}

// resolveExecPath returns the path to the Chromium binary chromedp should
// launch. Thin wrapper over the shared execPathCaches.resolve (exec_resolver.go)
// — see that method's doc comment for the full resolution order + rationale
// (cfg.ExecPath override → validated $PATH candidate → managed chrome-for-
// testing install; success + negative caches). Kept as a manager method so the
// existing tests + Preprovision call site are unchanged by the ADR-043 refactor.
//
// Safe to call without m.mu held, and safe to call WHILE some other goroutine
// holds m.mu: the only state resolve touches is execPathCaches.mu, never m.mu.
// ensureStarted relies on this — it releases m.mu before calling here so a slow
// first-time probe/download never blocks concurrent tab/session bookkeeping.
func (m *BrowserManager) resolveExecPath(ctx context.Context) (string, error) {
	return m.execPath.resolve(ctx, m.cfg)
}

// cacheExecPath / cacheExecPathFailure remain as thin wrappers for any external
// caller; the caching itself now lives in execPathCaches (exec_resolver.go).
func (m *BrowserManager) cacheExecPath(path string)      { m.execPath.cacheSuccess(path) }
func (m *BrowserManager) cacheExecPathFailure(err error) { m.execPath.cacheFailure(err) }

// execPathNegativeCacheTTL bounds how long a failed resolution is remembered
// (and returned verbatim, without re-probing) before resolveExecPath retries
// the real PATH/managed resolution. Long enough that a dead host's repeated
// browser_* calls do not each pay the full ~50s re-probe cost (4 PATH
// candidates at up to chromiumProbeTimeout each + a CfT manifest fetch); short
// enough that a transient failure (network flap, partial download) is retried
// reasonably soon.
const execPathNegativeCacheTTL = 60 * time.Second

// chromiumProbeTimeout bounds each PATH-candidate `--version` probe in
// resolveExecPath. Short enough that even a fully broken/hanging set of
// candidates (all four names in the loop) adds at most ~20s to a single
// first-time resolution — bounded, and no longer running with m.mu held
// (see ensureStarted's doc comment) — while comfortably long enough for a
// real Chromium/Chrome binary's `--version` to return (typically well under
// a second).
const chromiumProbeTimeout = 5 * time.Second

// probeChromiumBinary reports whether path is a real, runnable
// Chromium/Chrome binary by actually executing it (`--version`), not merely
// checking that it exists and is executable — which exec.LookPath, the only
// check the pre-fix resolveExecPath performed, already confirmed. LookPath
// alone is insufficient: a distro package-manager stub can be a perfectly
// valid, executable file on $PATH that nonetheless fails the moment it
// actually runs (see resolveExecPath's doc comment for the motivating
// Ubuntu snap-redirector case). Bounded by chromiumProbeTimeout so a
// hanging candidate cannot stall resolution indefinitely.
func probeChromiumBinary(ctx context.Context, path string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, chromiumProbeTimeout)
	defer cancel()
	return exec.CommandContext(probeCtx, path, "--version").Run() == nil
}

// Session returns the ACTIVE tab's context for the given browsing context
// (ADR-041 D1). If the browsing context does not exist, a new one is created
// with a single tab (subject to MaxTabs). The "default" session is used by
// all tools unless a session_id is specified. Every existing browser tool
// and the LiveView engine call this and therefore automatically follow
// whichever tab is active — they never need to know about the tab SET.
//
// ADR-038 DEADLOCK POSTMORTEM: creating a brand-new tab is the only path
// here that talks to CDP (chromedp.Run below), and it used to run under m.mu
// (held via defer) with no timeout of its own. Every browser tool calls
// Session() first, so a single wedged/overloaded CDP transport (see
// pkg/tools/browser/live.go's attach() for the sibling bug that hit this
// same shape) could freeze m.mu forever and, with it, every browser tool —
// permanently, since a bare sync.Mutex.Lock() has no deadline and produces
// no error or log line while it waits. The fix: only the MaxTabs check and
// map bookkeeping happen under m.mu; the blocking chromedp.NewContext/Run
// call runs with m.mu released and bounded by m.cfg.PageTimeout. A
// concurrent creator for the SAME sessionID that arrives while creation is
// in flight waits on m.pending instead of independently calling
// chromedp.NewContext/Run for that ID too (which would create and leak a
// second tab for one logical session). ADR-041 preserves this discipline
// exactly — createTab (below) is the same "no lock across CDP" call, shared
// with OpenTab/CloseTab/adoptTarget.
//
// ADR-041 fix F1: the "browsing context doesn't exist yet — create its
// first tab" case is now entirely delegated to createFirstTab, the SAME
// pending-dedup primitive OpenTab (when called on a not-yet-existing
// sessionID) and CloseTab's last-tab-replacement also funnel through. Before
// this fix, OpenTab and CloseTab's replacement each independently created a
// tab and then unconditionally overwrote m.sessions[sessionID] on
// completion — racing either of them against Session()'s own creation for a
// brand-new sessionID could leak a tab (whichever finished LAST won,
// silently discarding, and permanently leaking, the other's freshly-created
// tab and undercounting totalTabCountLocked). Routing every "create the
// first tab" call site through createFirstTab's shared m.pending gate means
// only one goroutine at a time ever creates that first tab; every other
// concurrent caller waits and then observes the now-populated
// m.sessions[sessionID] instead of creating a second one.
func (m *BrowserManager) Session(sessionID string) (context.Context, error) {
	for {
		m.mu.Lock()
		if err := m.ensureStarted(); err != nil {
			m.mu.Unlock()
			return nil, err
		}

		if se, ok := m.sessions[sessionID]; ok {
			if tab := se.active(); tab != nil && tab.ctx.Err() == nil {
				m.mu.Unlock()
				return tab.ctx, nil
			}
			// The active tab's context died (browser crash, etc). Tear down
			// the whole browsing context; createFirstTab (below, once
			// unlocked) recreates a fresh single-tab one — the same
			// crash-recovery behavior the pre-ADR-041 code applied to its
			// one-tab-per-session model, now applied to the tab SET.
			// (Resurrecting a different surviving tab as active instead is a
			// possible future refinement; out of scope here — an active tab
			// dying out from under the manager, as opposed to an explicit
			// CloseTab, is not the case ADR-041 targets.)
			for _, t := range se.tabs {
				t.cancel()
			}
			if se.browserCancel != nil {
				se.browserCancel()
			}
			delete(m.sessions, sessionID)
		}
		m.mu.Unlock()

		if err := m.createFirstTab(sessionID); err != nil {
			return nil, err
		}
		// Loop back to the top to read the freshly-created (or, if we lost
		// the creation race, someone else's freshly-created) active tab.
	}
}

// createFirstTab is the shared "ensure sessionID's browsing context has AT
// LEAST ONE tab" primitive (ADR-041 fix F1), used by Session()'s
// lazy-creation path, OpenTab() when sessionID has no tabs yet, and
// CloseTab()'s last-tab-replacement. It reuses exactly the m.pending dedup
// loop Session() used to run inline: only one goroutine at a time creates
// sessionID's next tab; every other concurrent caller waits on
// m.pending[sessionID] and, on return, finds m.sessions[sessionID] already
// populated instead of racing to create (and leak) a second one.
//
// Two distinct scenarios both funnel through here, distinguished by whether
// m.sessions[sessionID] already exists (with zero tabs) at the moment this
// goroutine wins the m.pending race:
//
//   - sessionID has NEVER been seen before (Session()'s and OpenTab()'s
//     lazy-creation case): bootstraps a BRAND NEW browser-owning context
//     (bootstrapBrowserCtx) and registers a brand-new sessionEntry around it
//     (registerFreshSessionLocked).
//   - sessionID's sessionEntry already exists but currently has zero tabs
//     (CloseTab's last-tab-replacement clears se.tabs to nil before calling
//     here): REUSES the existing, still-running se.browserCtx instead of
//     bootstrapping a second one — bootstrapping a second browser here would
//     try to bind Chrome's fixed debug port a second time and fail. This is
//     the browserCtx lifetime fix: the browser (and its browserCtx) now
//     outlives any single tab, including tab 0.
//
// No-op (nil error, no CDP call) if sessionID already has at least one tab
// by the time this runs — including if a concurrent creator won the race
// while this call was waiting to acquire m.mu.
//
// Must be called with NO BrowserManager lock held.
func (m *BrowserManager) createFirstTab(sessionID string) error {
	for {
		m.mu.Lock()
		if err := m.ensureStarted(); err != nil {
			m.mu.Unlock()
			return err
		}
		if se, ok := m.sessions[sessionID]; ok && len(se.tabs) > 0 {
			m.mu.Unlock()
			return nil
		}

		if wait, ok := m.pending[sessionID]; ok {
			// Someone else is already creating this browsing context's
			// next tab — wait for them to finish, then loop back and
			// re-check m.sessions, rather than racing to create a second
			// tab for the same ID.
			m.mu.Unlock()
			<-wait
			continue
		}

		if m.totalTabCountLocked() >= m.cfg.MaxTabs {
			m.mu.Unlock()
			return maxTabsReachedErr(m.cfg.MaxTabs)
		}

		done := make(chan struct{})
		if m.pending == nil {
			m.pending = make(map[string]chan struct{})
		}
		m.pending[sessionID] = done
		// existing is non-nil exactly in the "reuse" scenario above (see
		// doc comment): a sessionEntry that already exists but currently has
		// zero tabs. nil means "sessionID has never been seen before" —
		// bootstrap a brand new browser-owning context for it below.
		existing := m.sessions[sessionID]
		allocCtx := m.allocCtx
		m.mu.Unlock()

		var (
			tab           *tabEntry
			browserCtx    context.Context
			browserCancel context.CancelFunc
			err           error
		)
		if existing != nil {
			browserCtx = existing.browserCtx
			tab, err = m.createTab(browserCtx, "")
		} else {
			browserCtx, browserCancel, err = m.bootstrapBrowserCtx(allocCtx)
			if err == nil {
				tab, err = m.createTab(browserCtx, "")
				if err != nil {
					browserCancel()
				}
			}
		}

		m.mu.Lock()
		delete(m.pending, sessionID)
		if err != nil {
			m.mu.Unlock()
			close(done)
			return fmt.Errorf("browser: failed to initialize tab: %w", err)
		}

		var tabs []Tab
		notify := true
		if existing == nil {
			tabs = m.registerFreshSessionLocked(sessionID, tab, browserCtx, browserCancel)
		} else {
			switch se := m.sessions[sessionID]; {
			case se == nil:
				// The whole browsing context was torn down (CloseSession/
				// Shutdown/Session()'s crash-recovery) while we were creating
				// its replacement tab. Nothing left to attach to — release it.
				m.mu.Unlock()
				tab.cancel()
				close(done)
				return fmt.Errorf("browser: session %q closed while creating its next tab", sessionID)
			case len(se.tabs) > 0:
				// A concurrent OpenTab (which does not participate in
				// m.pending — it only guards the "session has zero tabs"
				// bootstrap/reuse case, not ordinary appends) already ensured
				// this reused browsing context has a tab while we were
				// creating ours. Discard our now-redundant tab instead of
				// either clobbering theirs (leak) or inflating the count.
				tab.cancel()
				tabs = snapshotTabsLocked(se)
				notify = false
			default:
				se.tabs = []*tabEntry{tab}
				se.activeIdx = 0
				m.installTargetListenerLocked(sessionID, se)
				tabs = snapshotTabsLocked(se)
			}
		}
		m.mu.Unlock()
		close(done)
		if notify {
			m.notifyTabsChanged(sessionID, tabs, 0)
		}
		return nil
	}
}

// registerFreshSessionLocked installs tab as the sole tab of a brand-new
// sessionEntry for sessionID, owned by browserCtx/browserCancel (see
// sessionEntry's doc comment), registers it in m.sessions, arms the ADR-041
// D2 passive target listener on it, and returns a snapshot — the exact
// sequence createFirstTab needs after successfully bootstrapping a session's
// first-ever tab (ADR-041 fix F6: this sequence used to be duplicated inline
// at each call site). Must be called with m.mu held.
func (m *BrowserManager) registerFreshSessionLocked(
	sessionID string,
	tab *tabEntry,
	browserCtx context.Context,
	browserCancel context.CancelFunc,
) []Tab {
	se := &sessionEntry{tabs: []*tabEntry{tab}, activeIdx: 0, browserCtx: browserCtx, browserCancel: browserCancel}
	m.installTargetListenerLocked(sessionID, se)
	m.sessions[sessionID] = se
	return snapshotTabsLocked(se)
}

// bootstrapBrowserCtx creates the ONE-TIME browser-owning chromedp context
// for a brand-new browsing context: chromedp.NewContext(allocCtx) followed by
// a forcing chromedp.Run (no actions) so the browser actually launches
// (managed mode) or attaches (remote CDP mode). This is the ONLY context in
// this manager that may be created directly off allocCtx/m.allocCtx for a
// tab-bearing purpose — every subsequent tab in the resulting browsing
// context must be a CHILD of the returned context (chromedp.NewContext(
// browserCtx, ...)), never straight off the allocator again.
//
// Why: chromedp binds a *Browser to the FIRST context created from an
// allocator (chromedp.NewContext's "c.first = c.Browser == nil" — the
// allocator context's own stored chromedp.Context always has Browser == nil,
// forever), NOT to the allocator context itself. A second
// chromedp.NewContext(allocCtx, ...) + Run therefore tries to launch a SECOND
// browser process rather than attaching to the first one — and since the
// managed-mode ExecAllocator pins a FIXED debug port (browserDebugPort,
// already held by the first browser), that second launch fails outright.
// This was exactly the live-UAT-caught ADR-041 adoption bug: adoptTarget and
// OpenTab each independently called chromedp.NewContext(m.allocCtx, ...) for
// the 2nd+ tab of an already-running session.
//
// Must be called with NO BrowserManager lock held — this issues a real,
// blocking chromedp.Run call, subject to the same "no lock across CDP"
// discipline documented on Session() and createTab() (ADR-038).
//
// Test seam: when m.createTabFn is set (unit tests — see tabs_test.go's
// fakeTabFactory), this returns a bare cancelable context instead of driving
// real chromedp, mirroring createTab's own createTabFn short-circuit exactly
// — the fake tab factory never dials CDP and ignores whatever parent context
// it's given, so there is nothing for a real bootstrap to attach to, and
// running one for real here would dereference the tests' nil m.allocCtx.
func (m *BrowserManager) bootstrapBrowserCtx(allocCtx context.Context) (context.Context, context.CancelFunc, error) {
	if m.createTabFn != nil {
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel, nil
	}
	var opts []chromedp.ContextOption
	if m.browserCtxID != "" {
		// ADR-043 (CRIT-003): re-adopt the coordinator-owned browser context
		// non-owningly. Safe here because chromedp forces c.first=false for a
		// RemoteAllocator context (the shared-Chrome + cdp_url paths); it would
		// panic on a first/root ExecAllocator context, but the manager's
		// browserCtxID is only set in coordinator/cdp_url mode, never the
		// no-coordinator managed-mode fallback. Non-owning = no auto-dispose on
		// cancel, so Shutdown/Release detach tabs + close the WS connection
		// WITHOUT disposing the browser context or killing Chrome (CRIT-002/C1).
		opts = append(opts, chromedp.WithExistingBrowserContext(m.browserCtxID))
	}
	ctx, cancel := chromedp.NewContext(allocCtx, opts...)
	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("browser: failed to launch browser: %w", err)
	}
	return ctx, cancel, nil
}

// maxTabsReachedErr is the shared "MaxTabs cap reached" error every
// tab-creating call site that can return an error to its caller uses
// (ADR-041 fix F6: this string used to be duplicated across Session/
// createFirstTab/OpenTab).
func maxTabsReachedErr(maxTabs int) error {
	return fmt.Errorf("maximum concurrent tabs (%d) reached. Close a tab first", maxTabs)
}

// lookupTabLocked resolves sessionID's browsing context and validates that
// index is in range for its tab set — the shared lookup+bounds-check
// SwitchTab and CloseTab both performed identically (ADR-041 fix F6). Must
// be called with m.mu held; does not unlock on error or success, matching
// every other *Locked helper in this file — callers own the lock.
func (m *BrowserManager) lookupTabLocked(sessionID string, index int) (*sessionEntry, error) {
	se, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("browser: no active session %q", sessionID)
	}
	if index < 0 || index >= len(se.tabs) {
		return nil, fmt.Errorf("browser: tab index %d out of range (0-%d)", index, len(se.tabs)-1)
	}
	return se, nil
}

// createTab performs the CDP work to bind a chromedp context to a browser
// tab — either a brand-new target (targetID == "") or an existing one
// (targetID != "", ADR-041 D2 adoption via chromedp.WithTargetID). MUST be
// called with NO BrowserManager lock held; this is the CDP entry point every
// tab-creating/adopting call site in this file shares, all obeying the exact
// discipline Session()'s doc comment above describes (never wrap the FIRST
// Run on a fresh ctx in a timeout — chromedp binds the target's lifetime to
// that first Run's context).
//
// parentCtx MUST be a context that already owns the browsing context's
// running *Browser — i.e. a sessionEntry.browserCtx (see its doc comment),
// or the raw allocator context ONLY when bootstrapping that very browserCtx
// for the first time (bootstrapBrowserCtx does this, then all further
// createTab calls for that session pass its returned browserCtx here).
// Passing the raw allocator context for anything past that first bootstrap
// is the exact bug this function's callers used to have: chromedp's
// initContextBrowser calls Allocator.Allocate() whenever the context's own
// Browser is nil — which it always is for a context created straight from
// the allocator, since the allocator's own stored chromedp.Context never
// gets its Browser field populated — so a second chromedp.NewContext(
// m.allocCtx, ...) + Run tries to launch a SECOND Chromium process (and,
// with the fixed managed-mode debug port already held by the first one,
// fails outright, even when WithTargetID names an existing target: the
// browser-allocation step in Run's initContextBrowser happens before the
// WithTargetID attach logic ever runs).
func (m *BrowserManager) createTab(parentCtx context.Context, targetID target.ID) (*tabEntry, error) {
	if m.createTabFn != nil {
		return m.createTabFn(parentCtx, targetID)
	}
	var opts []chromedp.ContextOption
	if targetID != "" {
		opts = append(opts, chromedp.WithTargetID(targetID))
	}
	ctx, cancel := chromedp.NewContext(parentCtx, opts...)

	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, err
	}

	// Best-effort stealth on a bounded timeout CHILD of ctx — safe because
	// canceling a child of an already-bound target does NOT tear the tab
	// down. Never fatal to tab creation.
	applyStealth(ctx, m.PageTimeout())

	resolvedID := targetID
	if cc := chromedp.FromContext(ctx); cc != nil && cc.Target != nil && cc.Target.TargetID != "" {
		resolvedID = cc.Target.TargetID
	}

	tab := &tabEntry{ctx: ctx, cancel: cancel, targetID: resolvedID}
	tab.title, tab.url = refreshTabMeta(ctx, m.PageTimeout())
	return tab, nil
}

// refreshTabMeta best-effort reads the current title/url of tabCtx, bounded
// by timeout so a slow/hung page never blocks tab creation or adoption.
// Failures are silent (empty strings) — title/url are cosmetic (tab-strip
// display, browser_list_tabs), never required for tool correctness.
func refreshTabMeta(tabCtx context.Context, timeout time.Duration) (title, url string) {
	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()
	_ = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = chromedp.Title(&title).Do(ctx)
			_ = chromedp.Location(&url).Do(ctx)
			return nil
		}),
	)
	return title, url
}

// totalTabCountLocked sums the number of tabs across every browsing context
// this manager tracks — the MaxTabs enforcement universe (ADR-041: MaxTabs
// now caps tabs-in-the-set, generalizing its pre-ADR-041 meaning of "total
// concurrent tabs" from a proxy of len(m.sessions), back when every session
// held exactly one tab). Must be called with m.mu held.
func (m *BrowserManager) totalTabCountLocked() int {
	n := 0
	for _, se := range m.sessions {
		n += len(se.tabs)
	}
	return n
}

// snapshotTabsLocked builds the public, metadata-only Tab slice for se. Must
// be called with m.mu held; the returned slice is a defensive copy safe to
// use after unlocking.
func snapshotTabsLocked(se *sessionEntry) []Tab {
	out := make([]Tab, len(se.tabs))
	for i, t := range se.tabs {
		out[i] = Tab{Index: i, Title: t.title, URL: t.url, Active: i == se.activeIdx}
	}
	return out
}

// notifyTabsChanged invokes the registered ADR-041 D4 tabs-changed callback
// (if any) with tabs/activeIdx already computed by the caller. Only
// m.tabsChanged itself is read under lock; the callback is always invoked
// with NO BrowserManager lock held (ADR-038 rule) — every mutating tab-set
// method in this file (Session/createFirstTab, OpenTab, CloseTab, SwitchTab,
// adoptTarget, handleTargetEvent) calls this only after releasing m.mu. One
// call site (handleTargetEvent's already-tracked/title-changed branch, ADR-
// 041 fix F5) additionally wraps the call itself in `go` — see its doc
// comment — since it runs on the CDP event-dispatch goroutine, where even a
// lock-free call into notifyTabsChanged must not run synchronously if the
// registered callback might call back into Session() and block on CDP.
func (m *BrowserManager) notifyTabsChanged(sessionID string, tabs []Tab, activeIdx int) {
	m.mu.Lock()
	cb := m.tabsChanged
	m.mu.Unlock()
	if cb != nil {
		cb(sessionID, tabs, activeIdx)
	}
}

// SetTabsChangedFunc registers cb to be invoked (ADR-041 D4) whenever any
// browsing context's tab set changes shape or its active tab moves —
// open/close/switch/adopt, and best-effort title/url updates. Overwrites any
// previously-registered callback; pass nil to unregister. Safe to call at
// any time. The callback itself is always invoked with NO BrowserManager
// lock held — see notifyTabsChanged.
func (m *BrowserManager) SetTabsChangedFunc(cb func(sessionID string, tabs []Tab, activeIdx int)) {
	m.mu.Lock()
	m.tabsChanged = cb
	m.mu.Unlock()
}

// ListTabs returns a snapshot of sessionID's tab set and which index is
// active. Returns a nil, empty slice with no error when the browsing
// context doesn't exist yet (no tool has navigated there) — "nothing open
// yet" is not treated as a failure, mirroring LiveViewRegistry.lookup's
// convention for a not-yet-attached session.
func (m *BrowserManager) ListTabs(sessionID string) (tabs []Tab, activeIdx int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok {
		return nil, 0, nil
	}
	return snapshotTabsLocked(se), se.activeIdx, nil
}

// SwitchTab makes tab `index` the active tab of sessionID's browsing
// context (ADR-041 D3). Subsequent tool calls (via Session) and the live
// screencast (via the ADR-041 D4 tabs-changed callback) follow it.
func (m *BrowserManager) SwitchTab(sessionID string, index int) (Tab, error) {
	m.mu.Lock()
	se, err := m.lookupTabLocked(sessionID, index)
	if err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	se.activeIdx = index
	tabs := snapshotTabsLocked(se)
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, index)
	return tabs[index], nil
}

// CloseTab closes tab `index` in sessionID's browsing context (cancels its
// chromedp target — a cheap, non-blocking call; see BrowserManager.
// CloseSession's identical existing pattern). Canceling a single tab's own
// context never tears down the browsing context's browser-owning
// se.browserCtx (see sessionEntry's doc comment) — the browser, and every
// OTHER tab in the set, stay alive and usable regardless of which tab is
// closed, including tab 0. If the closed tab was the active tab, a neighbor
// is activated instead (the tab that slid into the same index; falls back to
// the new last tab if the closed tab was the set's last). NEVER leaves the
// browsing context with zero tabs (ADR-041 D3/Consequences) — closing the
// last remaining tab opens a fresh blank replacement in the SAME
// still-running browser instead (via createFirstTab's reuse path — see its
// doc comment), which DOES talk to CDP and therefore runs with no
// BrowserManager lock held, mirroring Session()'s discipline.
func (m *BrowserManager) CloseTab(sessionID string, index int) (tabs []Tab, activeIdx int, err error) {
	m.mu.Lock()
	se, lerr := m.lookupTabLocked(sessionID, index)
	if lerr != nil {
		m.mu.Unlock()
		return nil, 0, lerr
	}

	if len(se.tabs) == 1 {
		closing := se.tabs[0]
		// Clear the tab set but keep the sessionEntry — and, critically, its
		// still-running se.browserCtx/browserCancel — in m.sessions. The
		// ADR-041 D3 "never leaves zero tabs" invariant is restored by
		// createFirstTab below, which REUSES this same browserCtx instead of
		// tearing the whole browsing context down and relaunching a second
		// Chromium on the fixed debug port (which would fail to bind — see
		// bootstrapBrowserCtx's doc comment). A concurrent OpenTab/Session()
		// call that observes se.tabs momentarily empty converges on the same
		// createFirstTab pending-dedup gate (ADR-041 fix F1) instead of racing
		// this replacement.
		se.tabs = nil
		m.mu.Unlock()

		closing.cancel() // closes only THIS target; the browser (browserCtx) lives on
		if cerr := m.createFirstTab(sessionID); cerr != nil {
			return nil, 0, fmt.Errorf("browser: closed last tab but failed to open a replacement: %w", cerr)
		}

		m.mu.Lock()
		newSE := m.sessions[sessionID]
		tabs = snapshotTabsLocked(newSE)
		activeIdx = newSE.activeIdx
		m.mu.Unlock()

		return tabs, activeIdx, nil
	}

	closing := se.tabs[index]
	se.tabs = append(se.tabs[:index], se.tabs[index+1:]...)
	switch {
	case se.activeIdx == index && index >= len(se.tabs):
		// Closed the active tab AND it was the set's last slot — fall back
		// to the new last tab.
		se.activeIdx = len(se.tabs) - 1
	case se.activeIdx == index:
		// Closed the active tab; the tab that slid into this same index
		// becomes active — no index change needed.
	case se.activeIdx > index:
		se.activeIdx--
	}
	// ADR-041 fix F3: if the closed tab was tab 0, the tab that slid into
	// index 0 needs the passive Target.targetCreated listener re-armed on
	// it — chromedp.ListenTarget's registration is scoped to the ctx it was
	// given, so closing tab 0 silently ended the listener forever otherwise.
	// A no-op (cheap targetID comparison) when index != 0.
	m.installTargetListenerLocked(sessionID, se)
	tabs = snapshotTabsLocked(se)
	activeIdx = se.activeIdx
	m.mu.Unlock()

	closing.cancel()
	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	return tabs, activeIdx, nil
}

// OpenTab opens a fresh blank tab in sessionID's browsing context and makes
// it active, subject to MaxTabs (ADR-041 D3). Creates the browsing context
// if it doesn't exist yet, mirroring Session()'s lazy-creation semantics.
func (m *BrowserManager) OpenTab(sessionID string) (Tab, error) {
	m.mu.Lock()
	if err := m.ensureStarted(); err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	se, exists := m.sessions[sessionID]
	hasTabs := exists && len(se.tabs) > 0
	m.mu.Unlock()

	if !hasTabs {
		// No browsing context yet (or a stale empty entry mid-creation
		// elsewhere) — route through the SAME first-tab creation path
		// Session() uses so a concurrent Session()/OpenTab()/CloseTab
		// last-tab-replacement race for this sessionID can't each
		// independently create a tab and blindly overwrite
		// m.sessions[sessionID] (ADR-041 fix F1).
		if err := m.createFirstTab(sessionID); err != nil {
			return Tab{}, err
		}
		return m.activeTabSnapshot(sessionID)
	}

	// Existing browsing context with at least one tab — append an
	// additional tab, as a CHILD of the session's own browserCtx (ADR-041
	// live-UAT fix: NOT m.allocCtx — see sessionEntry.browserCtx's and
	// createTab's doc comments for why reusing the raw allocator here tried
	// to launch a SECOND Chromium and failed). Re-check the cap AFTER
	// creating unlocked (ADR-041 fix F4: this recheck used to be gated on
	// `len(se.tabs) > 0`, which is always false the very first time a NEW
	// session's first tab is created — the exact race window the recheck
	// exists to catch — so it silently never fired. Firing it unconditionally
	// here is safe because this branch only runs once hasTabs is already
	// true.)
	m.mu.Lock()
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		return Tab{}, maxTabsReachedErr(m.cfg.MaxTabs)
	}
	se, exists = m.sessions[sessionID]
	if !exists || len(se.tabs) == 0 {
		// The browsing context vanished, or was raced down to zero tabs (e.g.
		// a concurrent CloseTab last-tab-replacement), between the hasTabs
		// check above and now. Don't dereference a stale/absent browserCtx —
		// fall back to the shared first-tab path, which dedups against any
		// concurrent recreation/reuse (ADR-041 fix F1, extended for
		// browserCtx reuse — see createFirstTab's doc comment).
		m.mu.Unlock()
		if err := m.createFirstTab(sessionID); err != nil {
			return Tab{}, err
		}
		return m.activeTabSnapshot(sessionID)
	}
	browserCtx := se.browserCtx
	m.mu.Unlock()

	newTab, err := m.createTab(browserCtx, "")
	if err != nil {
		return Tab{}, fmt.Errorf("browser: failed to open new tab: %w", err)
	}

	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok {
		// The browsing context vanished entirely while createTab ran
		// unlocked (e.g. raced with CloseSession, or CloseTab's last-tab
		// replacement tore it down). Don't resurrect it by blindly
		// installing a bare sessionEntry (ADR-041 fix F1) — release this
		// tab and go through the shared first-tab path instead, which
		// itself dedups against any concurrent recreation.
		m.mu.Unlock()
		newTab.cancel()
		if err := m.createFirstTab(sessionID); err != nil {
			return Tab{}, err
		}
		return m.activeTabSnapshot(sessionID)
	}
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		newTab.cancel()
		return Tab{}, maxTabsReachedErr(m.cfg.MaxTabs)
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1
	m.installTargetListenerLocked(sessionID, se) // no-op: tab 0 is unchanged by an append
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	return tabs[activeIdx], nil
}

// activeTabSnapshot returns the current active-tab snapshot for sessionID.
// Used by OpenTab's createFirstTab-delegation paths, where the browsing
// context is guaranteed (by createFirstTab's contract) to already exist with
// at least one tab by the time this is called.
func (m *BrowserManager) activeTabSnapshot(sessionID string) (Tab, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || len(se.tabs) == 0 {
		return Tab{}, fmt.Errorf("browser: no active session %q", sessionID)
	}
	tabs := snapshotTabsLocked(se)
	return tabs[se.activeIdx], nil
}

// tabAdoptReason is a machine-readable reason code explaining why
// adoptTarget detected a genuinely new CDP target but did NOT adopt it
// (ADR-041 fix F2). Threaded through ReconcileTabs and surfaced by
// browser_click so the agent can tell the user a tab was stranded, instead
// of the pre-fix behavior of silently reporting plain success.
type tabAdoptReason string

const (
	// tabAdoptReasonMaxTabs means the target was detected but MaxTabs was
	// already reached (checked either before or immediately after the CDP
	// attach — see adoptTarget's doc comment).
	tabAdoptReasonMaxTabs tabAdoptReason = "max_tabs_reached"
	// tabAdoptReasonAttachFailed means createTab's CDP attach to the
	// detected target itself failed (e.g. the target closed before attach,
	// or a transport error).
	tabAdoptReasonAttachFailed tabAdoptReason = "attach_failed"
)

// tabAdoptResult is adoptTarget's structured outcome (ADR-041 fix F2). It
// replaces the pre-fix "(*Tab, error)" pair, which collapsed two very
// different outcomes into the same nil-tab return: "nothing new happened"
// (already tracked, a racing adoption already claimed it, no browsing
// context to adopt into, empty targetID) and "a target=\"_blank\" click (or
// window.open) genuinely spawned a new tab, but it could not be adopted"
// (MaxTabs cap, or the CDP attach itself failed) — exactly the silent
// failure ADR-041's Motivation section describes: a click succeeds, a new
// tab opens, and the agent is stranded on the (now-background) opener page
// with no signal anything happened.
type tabAdoptResult struct {
	// Adopted is non-nil only when a NEW tab was actually appended to the
	// tab set and made active.
	Adopted *Tab
	// Unadopted is true when a genuinely new target was detected but
	// adoption was refused or failed — Reason explains why. Callers (chiefly
	// ReconcileTabs → browser_click) should surface this to the agent.
	Unadopted bool
	Reason    tabAdoptReason
}

// pendingAdoptEntry tracks a single in-flight adoptTarget call so a racing
// caller for the SAME target ID can WAIT for it to finish and reuse its
// result, instead of treating "someone else is already handling this" as a
// silent no-op. This matters because of a race exposed once tab adoption's
// CDP attach actually succeeds (the browserCtx fix above): the best-effort
// passive listener (installTargetListenerLocked, dispatched async via `go
// func()` in handleTargetEvent) and the deterministic ReconcileTabs pass
// (browser_click's own guaranteed detection point, called synchronously
// right after the click) both race to adopt the same freshly-created
// target — and the async listener routinely WINS, since a CDP
// Target.targetCreated/EventTargetInfoChanged event can arrive and be
// dispatched before the click's own CDP round trip has even returned. Before
// this fix, ReconcileTabs's own attempt would see "already pending" and
// return a true no-op with nothing to report — so browser_click returned
// plain success with no opened_new_tab, even though the OTHER (async, not
// yet visible to the caller) attempt would go on to succeed moments later.
// Waiting for the winner's actual result closes that gap.
type pendingAdoptEntry struct {
	done   chan struct{}
	result tabAdoptResult
	err    error
}

// adoptTarget attaches a chromedp context to an existing CDP target — one a
// target="_blank" click, window.open, or Ctrl/Cmd+click spawned — and
// appends it as a new tab to sessionID's tab set, making it active by
// default (ADR-041 D2). Idempotent: a target already tracked is a true no-op
// (a zero tabAdoptResult, nil error) — nothing was found, nothing to report.
// A target a concurrent adoption is already in flight for is NOT a no-op —
// this call WAITS for that in-flight attempt (see pendingAdoptEntry's doc
// comment) and returns its actual result, bounded by m.PageTimeout() so a
// wedged concurrent attempt cannot hang this caller forever.
//
// Enforces MaxTabs: a runaway window.open loop is capped, not unbounded.
// Unlike the pre-fix version, refusal is never silent to the CALLER — it is
// reported via tabAdoptResult.Unadopted/Reason (ADR-041 fix F2) rather than
// collapsed into the same nil result as "nothing happened", since the caller
// (ReconcileTabs, and through it browser_click) needs to tell the agent a
// tab was stranded. Only the pre-CDP cap check additionally logs at WARN —
// the narrower post-attach recheck (a race lost to a concurrent
// adopter/cap-filler while createTab ran unlocked) stays silent in the log,
// matching the pre-fix behavior; both set Unadopted/Reason regardless.
//
// Deadlock-safe per ADR-038: createTab's CDP attach, and the bounded wait on
// a racing caller's in-flight attempt, both run with NO BrowserManager lock
// held.
func (m *BrowserManager) adoptTarget(sessionID string, targetID target.ID) (tabAdoptResult, error) {
	if targetID == "" {
		return tabAdoptResult{}, nil
	}

	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return tabAdoptResult{}, nil // no browsing context to adopt into yet
	}
	if se.indexOfTarget(targetID) >= 0 {
		m.mu.Unlock()
		return tabAdoptResult{}, nil // already ours
	}
	if m.pendingAdopt == nil {
		m.pendingAdopt = make(map[target.ID]*pendingAdoptEntry)
	}
	if entry, already := m.pendingAdopt[targetID]; already {
		m.mu.Unlock()
		// Wait for the in-flight winner's actual outcome (see
		// pendingAdoptEntry's doc comment) instead of silently reporting
		// nothing. Bounded so a wedged concurrent attempt cannot hang this
		// caller forever.
		timer := time.NewTimer(m.PageTimeout())
		defer timer.Stop()
		select {
		case <-entry.done:
			return entry.result, entry.err
		case <-timer.C:
			return tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonAttachFailed},
				fmt.Errorf("browser: timed out waiting for a concurrent adoption of target %s", targetID)
		}
	}
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		logger.WarnCF("browser", "new tab target detected but MaxTabs reached — not adopting", map[string]any{
			"session_id": sessionID,
			"target_id":  string(targetID),
			"max_tabs":   m.cfg.MaxTabs,
		})
		return tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonMaxTabs}, nil
	}
	entry := &pendingAdoptEntry{done: make(chan struct{})}
	m.pendingAdopt[targetID] = entry
	// Attach as a CHILD of this browsing context's own browserCtx (ADR-041
	// live-UAT fix) — NOT m.allocCtx. This is THE core fix: adopting a
	// target="_blank"/window.open target used to reuse the raw allocator
	// context here, which chromedp treats as "launch a brand new browser"
	// (see createTab's and sessionEntry.browserCtx's doc comments), and with
	// the managed-mode debug port already held by the running browser, that
	// launch failed outright — the tab silently never got adopted.
	browserCtx := se.browserCtx
	m.mu.Unlock()

	newTab, err := m.createTab(browserCtx, targetID)

	m.mu.Lock()
	delete(m.pendingAdopt, targetID)
	if err != nil {
		m.mu.Unlock()
		result := tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonAttachFailed}
		wrapped := fmt.Errorf("browser: failed to adopt new tab target %s: %w", targetID, err)
		entry.result, entry.err = result, wrapped
		close(entry.done)
		return result, wrapped
	}
	se, ok = m.sessions[sessionID]
	if !ok || se.indexOfTarget(targetID) >= 0 {
		// The browsing context vanished, or another racer already adopted
		// this target — both re-checked post-CDP-call since createTab ran
		// unlocked. Release the just-created tab rather than leaking it.
		// Neither is worth reporting as Unadopted: the browsing context
		// vanishing is a bigger problem surfaced elsewhere, and a racer's
		// own adoptTarget call already reports ITS successful adoption.
		m.mu.Unlock()
		newTab.cancel()
		close(entry.done) // result/err stay zero-value: a true no-op for any waiter too
		return tabAdoptResult{}, nil
	}
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		newTab.cancel()
		result := tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonMaxTabs}
		entry.result = result
		close(entry.done)
		return result, nil
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1 // ADR-041 D2: adopted tabs become active by default
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	active := tabs[activeIdx]
	result := tabAdoptResult{Adopted: &active}
	entry.result = result
	close(entry.done)
	return result, nil
}

// reconcileTargetListTimeout bounds the chromedp.Targets CDP round trip
// ReconcileTabs issues — a read-only "list every target" query, but still
// routed through a deadline (ADR-038 discipline: every CDP round trip is
// bounded) so a wedged transport fails this one reconcile pass instead of
// hanging its caller (browser_click) forever.
const reconcileTargetListTimeout = 5 * time.Second

// ReconcileOutcome is ReconcileTabs's structured result (ADR-041 fix F2),
// mirroring tabAdoptResult one level up: distinguishes "a new tab was
// adopted" from "a new tab was detected but could not be adopted" from
// "nothing new happened", so browser_click (tools.go) can tell the agent a
// tab was stranded instead of silently reporting plain success.
//
// Adopted/NewActive and Unadopted/Reason/UnadoptedCount are aggregated
// INDEPENDENTLY across every target ReconcileTabs's loop processes in one
// pass (ADR-041 second-fix-wave, F2 follow-up): a single click can spawn
// MULTIPLE new targets in one go (e.g. two target="_blank" links inside one
// click handler), and one may adopt cleanly while another is capped or fails
// to attach. Once Unadopted is set true it is STICKY for the rest of the
// pass — a later target that DOES adopt successfully must not clear it (and
// symmetrically a later Unadopted target must not clear an already-set
// Adopted/NewActive) — both signals must reach the caller so
// applyReconcileOutcome (tools.go) can report BOTH "opened_new_tab" and
// "tab_opened_but_not_adopted" in the same tool result instead of silently
// dropping whichever one didn't win a mutually-exclusive if/else.
type ReconcileOutcome struct {
	Adopted   bool
	NewActive *Tab
	Unadopted bool
	// Reason is the FIRST unadopted reason encountered in this pass (stable
	// and deterministic when multiple stranded targets have different
	// reasons); see UnadoptedCount for how many targets were stranded.
	Reason tabAdoptReason
	// UnadoptedCount is how many genuinely-new targets in this pass could
	// NOT be adopted. Always 0 when Unadopted is false; always >= 1 when
	// Unadopted is true.
	UnadoptedCount int
}

// ReconcileTabs looks for CDP targets opened by one of sessionID's own tabs
// (target="_blank", window.open, Ctrl/Cmd+click) that this browsing context
// hasn't adopted yet, and adopts them (ADR-041 D2). Called deterministically
// right after browser_click (tools.go) — the guaranteed detection point —
// complementing the best-effort passive Target.targetCreated listener
// installed on each browsing context's root tab
// (installTargetListenerLocked). Outcome.Adopted/NewActive report a
// successful adoption (if a page opened more than one new tab in one go, the
// LAST one adopted ends up active, same as if they'd been adopted one at a
// time via the passive listener); Outcome.Unadopted/Reason/UnadoptedCount
// report a genuinely new target that could NOT be adopted (ADR-041 fix F2)
// — e.g. the MaxTabs cap, or the CDP attach itself failing — so the caller
// can surface that to the agent instead of the tab silently vanishing from
// view. The two signals are tracked independently across the whole pass
// (see ReconcileOutcome's doc comment) — a click that spawns two new targets
// where one adopts and the other is stranded reports BOTH.
func (m *BrowserManager) ReconcileTabs(sessionID string) (ReconcileOutcome, error) {
	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok || len(se.tabs) == 0 {
		m.mu.Unlock()
		return ReconcileOutcome{}, nil
	}
	execCtx := se.active().ctx
	tracked := make(map[target.ID]struct{}, len(se.tabs))
	for _, t := range se.tabs {
		tracked[t.targetID] = struct{}{}
	}
	listTargets := m.listTargets
	m.mu.Unlock()

	if listTargets == nil {
		listTargets = chromedp.Targets
	}

	timeoutCtx, cancel := context.WithTimeout(execCtx, reconcileTargetListTimeout)
	infos, lerr := listTargets(timeoutCtx)
	cancel()
	if lerr != nil {
		return ReconcileOutcome{}, fmt.Errorf("browser: failed to list targets for reconcile: %w", lerr)
	}

	var out ReconcileOutcome
	for _, info := range infos {
		if info == nil || info.Type != "page" {
			continue
		}
		if _, already := tracked[info.TargetID]; already {
			continue
		}
		if info.OpenerID == "" {
			continue // not opened by a page — a top-level target, not ours to adopt
		}
		if _, openerIsOurs := tracked[info.OpenerID]; !openerIsOurs {
			continue // opened by a target outside this browsing context
		}
		result, aerr := m.adoptTarget(sessionID, info.TargetID)
		if aerr != nil {
			logger.WarnCF("browser", "reconcile: failed to adopt detected tab", map[string]any{
				"session_id": sessionID,
				"target_id":  string(info.TargetID),
				"error":      aerr.Error(),
			})
		}
		// Adopted and Unadopted are set on DISJOINT fields of out — deliberately
		// NOT an if/else — so a click that opens two new targets where one
		// adopts and one is stranded reports BOTH signals, regardless of which
		// order this loop processes them in. See ReconcileOutcome's doc comment.
		switch {
		case result.Adopted != nil:
			out.Adopted = true
			out.NewActive = result.Adopted
			tracked[info.TargetID] = struct{}{} // avoid reprocessing within this pass
		case result.Unadopted:
			out.Unadopted = true
			if out.Reason == "" {
				out.Reason = result.Reason // first reason wins — stable across the pass
			}
			out.UnadoptedCount++
			tracked[info.TargetID] = struct{}{} // avoid reprocessing within this pass
		}
	}
	return out, nil
}

// installTargetListenerLocked attaches the ADR-041 D2 passive
// target-created listener to se's CURRENT root tab (se.tabs[0]) if it isn't
// already installed there. Installed once at browsing-context creation and
// RE-ARMED whenever tab 0 itself is closed (ADR-041 fix F3 — see
// sessionEntry.listenerTarget's doc comment for why: chromedp.ListenTarget's
// registration is scoped to the ctx it was given, so closing the tab that
// ctx belongs to silently ends the listener forever unless something
// re-installs it on whichever tab becomes the new tab 0). chromedp enables
// Target.setDiscoverTargets(true) per tab-session, but discovery itself is
// browser-global, so installing this listener on every tab would multiply
// duplicate (idempotently-handled by adoptTarget, but wasteful) events for
// the same new target — hence exactly one tab at a time. Must be called with
// m.mu held — chromedp.ListenTarget itself is a cheap, non-blocking,
// lock-free append (mirrors how live.go's attach() calls it), never a CDP
// round trip. A no-op (cheap targetID comparison) on every call site except
// the one where tab 0 actually just changed.
func (m *BrowserManager) installTargetListenerLocked(sessionID string, se *sessionEntry) {
	if len(se.tabs) == 0 {
		return
	}
	root := se.tabs[0]
	if se.listenerTarget == root.targetID {
		return
	}
	se.listenerTarget = root.targetID
	chromedp.ListenTarget(root.ctx, func(ev any) {
		m.handleTargetEvent(sessionID, ev)
	})
}

// handleTargetEvent is the chromedp.ListenTarget callback for a browsing
// context's currently-listener-holding tab (ADR-041 D2/D4). Per chromedp's
// contract (mirrors live.go's handleScreencastEvent exactly) this runs
// SYNCHRONOUSLY on the CDP event-dispatch goroutine and must never block or
// call chromedp.Run inline, directly OR transitively: for an already-tracked
// tab, the title/url bookkeeping itself is cheap and lock-protected (no CDP
// call), but ADR-041 fix F5 additionally dispatches its notifyTabsChanged
// call onto its own goroutine — the registered callback
// (LiveView.onTabsChanged, pkg/tools/browser/live.go) can call back into
// mgr.Session(), which, if the active tab's ctx has died, runs a blocking
// chromedp.Run to recreate it; that must never happen on the same goroutine
// chromedp uses to deliver CDP events, or it can deadlock the connection
// (the exact ADR-038 class). Adopting a brand-new target is likewise
// dispatched onto its own goroutine, for the same reason (adoptTarget's CDP
// attach is a blocking chromedp.Run).
func (m *BrowserManager) handleTargetEvent(sessionID string, ev any) {
	info, ok := targetInfoFromEvent(ev)
	if !ok || info == nil || info.Type != "page" {
		return
	}

	m.mu.Lock()
	se, exists := m.sessions[sessionID]
	if exists {
		if idx := se.indexOfTarget(info.TargetID); idx >= 0 {
			// Already-tracked tab: best-effort title/url refresh (ADR-041
			// D4's "or its title/url changes" broadcast trigger) — no CDP
			// call, no adoption needed.
			changed := se.tabs[idx].title != info.Title || se.tabs[idx].url != info.URL
			se.tabs[idx].title = info.Title
			se.tabs[idx].url = info.URL
			if !changed {
				m.mu.Unlock()
				return
			}
			tabs := snapshotTabsLocked(se)
			activeIdx := se.activeIdx
			m.mu.Unlock()
			go m.notifyTabsChanged(sessionID, tabs, activeIdx) // ADR-041 fix F5 — see doc comment above
			return
		}
	}
	m.mu.Unlock()

	if info.OpenerID == "" {
		return // not opened by a page — a top-level/browser-initiated target, not ours
	}
	go func() {
		if _, err := m.adoptTarget(sessionID, info.TargetID); err != nil {
			logger.WarnCF("browser", "auto-attach: failed to adopt new tab target", map[string]any{
				"session_id": sessionID,
				"target_id":  string(info.TargetID),
				"error":      err.Error(),
			})
		}
	}()
}

// targetInfoFromEvent extracts *target.Info from the two CDP event types
// that carry it (mirrors chromedp.WaitNewTarget's own switch in the chromedp
// package — the same pair of events can carry an unattached child target's
// info, depending on timing).
func targetInfoFromEvent(ev any) (*target.Info, bool) {
	switch e := ev.(type) {
	case *target.EventTargetCreated:
		return e.TargetInfo, true
	case *target.EventTargetInfoChanged:
		return e.TargetInfo, true
	default:
		return nil, false
	}
}

// stealthInitScript runs before any page script on every new document, hiding
// the residual automation tells that survive the launch flags. Kept minimal
// and side-effect-free so it can never break a page.
// Each override is independently try/catch-wrapped so one failing (e.g. a
// non-configurable property on a given Chrome build) never aborts the rest,
// and webdriver is also deleted off the prototype as a fallback for builds
// where the accessor lives there.
//
// Effectiveness caveat: the webdriver override lands on full-Chrome
// --headless=new, but NOT on the bundled chrome-headless-shell (--headless=old,
// what the installer fetches) — there navigator.webdriver is set
// non-overridably by the shell, so it still reads true regardless of this
// script or --disable-blink-features=AutomationControlled. Verified in the wild
// that Google still loads without a CAPTCHA anyway (UA + IP dominate); a
// hardcore detector could still flag the webdriver bit. Fully closing it would
// require shipping full Chrome new-headless instead of chrome-headless-shell.
const stealthInitScript = `(function(){` +
	`try{Object.defineProperty(navigator,'webdriver',{get:function(){return undefined},configurable:true})}catch(e){}` +
	`try{delete Navigator.prototype.webdriver}catch(e){}` +
	`try{window.chrome=window.chrome||{runtime:{}}}catch(e){}` +
	`try{Object.defineProperty(navigator,'languages',{get:function(){return['en-US','en']},configurable:true})}catch(e){}` +
	`})();`

// deHeadlessUA rewrites a chrome-headless-shell User-Agent into the equivalent
// regular-Chrome string ("HeadlessChrome/…" → "Chrome/…"), removing the single
// biggest automation giveaway. Returns "" if the input is empty.
func deHeadlessUA(ua string) string {
	if ua == "" {
		return ""
	}
	s := strings.ReplaceAll(ua, "HeadlessChrome", "Chrome")
	return strings.ReplaceAll(s, "Headless", "")
}

// applyStealth best-effort reduces automation fingerprints on an already-bound
// tab (see Session): it de-Headlesses the User-Agent and installs
// stealthInitScript so navigator.webdriver et al. read like a normal browser.
// Every step is best-effort — a failure must NEVER break tab creation, so the
// whole thing runs on a bounded timeout child and only logs on failure. This
// lowers the odds of a CAPTCHA but cannot beat datacenter-IP reputation.
func applyStealth(tabCtx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()
	var ua string
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = chromedp.Evaluate(`navigator.userAgent`, &ua).Do(ctx)
			if clean := deHeadlessUA(ua); clean != "" && clean != ua {
				_ = emulation.SetUserAgentOverride(clean).Do(ctx)
			}
			_, _ = page.AddScriptToEvaluateOnNewDocument(stealthInitScript).Do(ctx)
			return nil
		}),
	); err != nil {
		logger.WarnCF("browser", "stealth setup best-effort failed (tab still usable)",
			map[string]any{"error": err.Error()})
	}
}

// CloseSession closes every tab in a specific browsing context AND its
// browser-owning se.browserCtx (ADR-041 D1: a session is now a tab SET, so
// this cancels all of them, not just one — plus the browser itself, since
// whole-session teardown is one of the few places that's actually meant to
// happen, per sessionEntry.browserCtx's doc comment).
func (m *BrowserManager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if se, ok := m.sessions[sessionID]; ok {
		for _, t := range se.tabs {
			t.cancel()
		}
		if se.browserCancel != nil {
			se.browserCancel()
		}
		delete(m.sessions, sessionID)
	}
}

// PageTimeout returns the configured page load timeout.
func (m *BrowserManager) PageTimeout() time.Duration {
	return m.cfg.PageTimeout
}

// browserAlive reports whether sessionID's browsing context — the
// underlying Chromium browser process/target, not any single tab within it
// — is still alive. A pure, side-effect-free read (m.mu only, never a CDP
// round trip, never Session()'s create-or-recover-on-death recovery
// behavior) so it is safe to call from LiveView.watchForUnexpectedDeath
// (pkg/tools/browser/live.go) without ever accidentally relaunching/
// self-healing a Chromium process for what may be a deliberate whole-manager
// CloseSession()/Shutdown() (ADR-038 finding #2's hot-reload case, where a
// genuine "session ended" broadcast IS the correct signal — it tells
// attached viewers to re-attach, which resolves the fresh manager).
//
// Returns false whenever sessionID has no browsing context registered at all
// (CloseSession/Shutdown already deleted the entry, or nothing ever created
// one) or its browser-owning context has itself been canceled (a genuine
// crash, or an explicit se.browserCancel() call). Returns true whenever the
// browsing context is still running, REGARDLESS of which individual tab
// within it just closed — ADR-041's whole point is that closing any one tab,
// including the active one, only cancels that tab's own context, never
// se.browserCtx (see sessionEntry's doc comment) — so a dead tab context
// alone must never be read as a dead browser.
func (m *BrowserManager) browserAlive(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || se.browserCtx == nil {
		return false
	}
	return se.browserCtx.Err() == nil
}

// Started reports whether the browser allocator has been launched (lazy
// init via ensureStarted, triggered by the first Session call) and not since
// Shutdown(). Exposed for tests that need to observe Shutdown() actually
// resetting manager state without spinning up a real Chromium process — see
// pkg/agent/browser_manager_test.go's ADR-038 finding #2 regression guard
// for the hot-reload leak fix in registerSharedTools.
func (m *BrowserManager) Started() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// Preprovision resolves — and, if resolution lands on the managed
// fallback with no binary installed yet, downloads — a Chromium/
// headless-shell binary for this manager, WITHOUT starting the browser
// allocator itself (that stays lazy, triggered by the first real
// Session() call, exactly as before). It runs the identical resolution
// logic ensureStarted's managed-mode branch uses (resolveExecPath:
// validated $PATH candidate, else the managed chrome-for-testing
// install), so calling this at gateway boot means the (potentially
// 100+MB, multi-second) download already happened in the background by
// the time an agent's first browser_navigate needs it, instead of that
// first tool call being the one to discover — and pay for — a missing
// browser.
//
// True bundling of a Chromium/headless-shell binary directly into the Go
// binary (e.g. go:embed) was considered and rejected: the smallest viable
// chrome-for-testing headless-shell build is on the order of 100+MB,
// which would multiply the omnipus binary size many times over and
// violates Hard Constraint #1 — the single, lean, statically-linked Go
// binary distribution model this project ships by design (CLAUDE.md). This
// is a binary/distribution-footprint argument, NOT the resident-memory
// Hard Constraint #3 (<10MB RAM overhead), which is about runtime cost
// and is unaffected by bundling. Downloading once, lazily-but-eagerly
// (at boot, in the background) and caching the extracted binary under
// <ProfileDir>/../chromium/ is the chosen middle ground: zero footprint
// for operators who never touch the browser tools (CDPURL configured to a
// remote Chromium, or the tools simply unused), and a one-time background
// fetch for everyone else.
//
// Idempotent and cheap to call repeatedly, including concurrently across
// multiple BrowserManagers that happen to share an install root: a
// validated system Chromium/Chrome on $PATH short-circuits with no
// network call at all (and is cached in-process — see resolveExecPath),
// and a managed binary already installed under installRoot is found by
// EnsureChromium's own findInstalledBinary check before any download is
// attempted; concurrent first-time downloads are serialized by
// EnsureChromium's own installMu.
//
// No-op — returns ("", nil), not an error — when cfg.CDPURL is set: remote
// CDP mode attaches to an operator-managed Chromium elsewhere and never
// needs a local binary.
func (m *BrowserManager) Preprovision(ctx context.Context) (string, error) {
	if m.cfg.CDPURL != "" {
		return "", nil
	}
	return m.resolveExecPath(ctx)
}

// Shutdown drops this manager's connection + all its sessions. In ADR-043
// shared-Chrome mode (coordinator wired), m.allocCancel is the REMOTE
// allocator's cancel — canceling it closes the manager's WS connection and
// detaches its tabs, but kills NEITHER the Chrome process NOR disposes the
// agent's browser context (CRIT-002/C1: the coordinator owns both). In the
// no-coordinator managed-mode fallback (tests), m.allocCancel is the
// ExecAllocator's cancel and DOES kill this manager's own Chrome — preserved
// for the one-manager-one-Chrome test path. Either way the bookkeeping
// (sessions, started, allocCancel) is reset cleanly and idempotently.
func (m *BrowserManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, se := range m.sessions {
		for _, t := range se.tabs {
			t.cancel()
		}
		if se.browserCancel != nil {
			se.browserCancel()
		}
		delete(m.sessions, id)
	}

	if m.allocCancel != nil {
		m.allocCancel()
		m.allocCancel = nil
	}
	m.started = false
	m.browserCtxID = ""

	logger.InfoCF("browser", "Browser manager connection shut down", map[string]any{
		"agent_id":    m.agentID,
		"coordinator": m.coordinator != nil,
	})
}

// OpenTabCount returns the number of currently-open tabs across this manager's
// browsing contexts (ADR-043 Stream D / spec round-1 MAJ-001). The coordinator
// sums this across registered managers to enforce the global tab budget. The
// read takes m.mu briefly (the same lock totalTabCountLocked uses), so callers
// must NOT already hold m.mu.
func (m *BrowserManager) OpenTabCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTabCountLocked()
}

// reserveGlobalTab asks the shared-Chrome coordinator for a global tab-budget
// slot before opening a tab (ADR-043 D7). No-op (always allowed) in legacy mode
// (no coordinator): the budget only applies to the shared Chrome. Only the
// agent-facing browser_open_tab tool calls this; an agent's FIRST tab
// (createFirstTab via Session) is intentionally not gated, so a budget full of
// OTHER agents' tabs never blocks a new agent from browsing at all.
//
// MAJ-2: m.coordinator/m.agentID are set once before use (AttachSharedChrome in
// registerSharedTools, before any tool call can reach here) but are read under
// m.mu for consistency with every other field on this struct.
func (m *BrowserManager) reserveGlobalTab() (bool, string) {
	m.mu.Lock()
	coord := m.coordinator
	agentID := m.agentID
	m.mu.Unlock()
	if coord == nil {
		return true, ""
	}
	return coord.TryOpenTab(agentID)
}

// releaseGlobalTab returns a global tab-budget slot when an agent closes a tab,
// OR when an OpenTab that reserved a slot then failed the actual createTab
// (I-1/W3/C1: the reservation must be returned so the budget doesn't grow
// permanently conservative on every failed open).
func (m *BrowserManager) releaseGlobalTab() {
	m.mu.Lock()
	coord := m.coordinator
	agentID := m.agentID
	m.mu.Unlock()
	if coord != nil {
		coord.ReleaseTab(agentID)
	}
}

// dropConnection closes this manager's remote-allocator connection + all its
// sessions WITHOUT touching the Chrome process or the agent's browser context
// (CRIT-002/C1). Called by the coordinator on Release (reload): the old
// manager's WS connection is torn down (so it doesn't leak), but the shared
// Chrome keeps running and the agent's context persists for the next manager
// to re-adopt. Identical in effect to Shutdown in coordinator mode, but named
// separately to make the reload-path intent explicit.
func (m *BrowserManager) dropConnection() {
	m.Shutdown()
}

// invalidateConnection resets this connector manager's "started" latch and
// clears its stale remote-allocator state so its next ensureStarted re-asks the
// coordinator (grill M1 / R2 crash recovery). Called by the coordinator when it
// detects the shared Chrome has crashed: the manager's remote-allocator
// connection is dead, but m.started is still true (it was only ever reset by
// Shutdown), so without this reset the manager would keep reusing its dead
// connection forever. After this call, the next browser tool's ensureStarted
// re-registers with the coordinator, which blocks until the proactive relaunch
// completes, then returns a fresh connection + a fresh browserCtxID (CRIT-001:
// fresh empty context — prior cookies/login are lost by definition on crash).
func (m *BrowserManager) invalidateConnection() {
	m.mu.Lock()
	for id, se := range m.sessions {
		for _, t := range se.tabs {
			t.cancel()
		}
		if se.browserCancel != nil {
			se.browserCancel()
		}
		delete(m.sessions, id)
	}
	// Cancel the dead remote allocator if present (no-op on an already-dead
	// connection). Do NOT nil it under lock in a way that races a concurrent
	// ensureStarted — the next ensureStarted runs under m.mu and overwrites
	// allocCtx/allocCancel/browserCtxID atomically after observing started==false.
	if m.allocCancel != nil {
		m.allocCancel()
		m.allocCancel = nil
	}
	m.allocCtx = nil
	m.started = false
	m.browserCtxID = ""
	m.mu.Unlock()
}
