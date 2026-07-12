// Package browser implements browser automation tools using chromedp (pure Go CDP).
//
// Implements US-4 (managed mode), US-6 (remote CDP mode), US-7 (resource limits)
// from the Wave 4 spec. All navigations are SSRF-checked via pkg/security.SSRFChecker.
package browser

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// install under <ProfileDir>/../chromium/ (downloaded on first use).
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
	// listenerInstalled guards ADR-041 D2's passive Target.targetCreated
	// listener so it is attached only ONCE per browsing context, on the
	// root (first) tab — see installTargetListenerLocked's doc comment for
	// why installing it on every tab would multiply duplicate events.
	listenerInstalled bool
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

	// pendingAdopt tracks CDP target IDs currently being adopted (ADR-041
	// D2's adoptTarget), mirroring `pending`'s exact race-guard shape: the
	// best-effort passive listener (installTargetListenerLocked) and a
	// deterministic ReconcileTabs pass can both observe the same
	// freshly-created target and race to adopt it — without this, the
	// second racer would attach a SECOND chromedp context to the same
	// target and append a duplicate tab. Lazily initialized; nil is a valid
	// empty state.
	pendingAdopt map[target.ID]struct{}

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
func (m *BrowserManager) ensureStarted() error {
	if m.started {
		return nil
	}

	if m.cfg.CDPURL != "" {
		// US-6: Remote CDP mode — connect to external Chromium
		allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), m.cfg.CDPURL)
		m.allocCtx = allocCtx
		m.allocCancel = cancel
		m.started = true
		logger.InfoCF("browser", "Connected to remote CDP", map[string]any{
			"url": m.cfg.CDPURL,
		})
		return nil
	}

	// US-4: Managed mode — launch local Chromium
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

	execPath, err := m.resolveExecPath(context.Background())
	if err != nil {
		return fmt.Errorf("browser: cannot locate chromium: %w", err)
	}

	// Point Chrome's HOME and XDG dirs at the profile directory so any stray
	// writes (Crash Reports, GPUCache, Singleton locks, etc.) land inside
	// the Landlock-allowed workspace instead of $HOME/.config/google-chrome.
	// Use the profile directory itself as a self-contained jail so this
	// stays correct regardless of the configured layout (test tempdirs,
	// custom paths, $OMNIPUS_HOME, etc.).
	chromeHome := m.cfg.ProfileDir

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.UserDataDir(m.cfg.ProfileDir),
		chromedp.DisableGPU,
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		// Crash reporting writes to ~/.config/google-chrome/Crash Reports
		// which is outside the Landlock-allowed paths. Disable it to avoid
		// "Permission denied" spam during browser startup.
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-breakpad", true),
		// /dev/shm is frequently tiny (or absent) in containers; Chrome falls
		// back to disk-backed shared memory for renderer<->GPU transport when
		// this is set, avoiding renderer crashes under container defaults
		// (ADR-038 D4 — needed for the CDP screencast to stream reliably from
		// a containerized gateway).
		chromedp.Flag("disable-dev-shm-usage", true),
		// Pin the DevTools WebSocket to a fixed loopback port so the
		// gateway's Landlock NET_CONNECT_TCP allow-list can include it
		// (v0.1 fix for "browser_navigate: dial tcp 127.0.0.1:<random>:
		// connect: permission denied"). Without this, chromedp picks an
		// ephemeral port and Landlock blocks the dial because only
		// {53, 80, 443} + dev-server ports are allow-listed by default.
		// 9223 was chosen to sit just above Chrome's traditional 9222
		// debug port (which we avoid to reduce collision risk with
		// operator workstations that may already have a remote-debugged
		// Chrome listening on 9222). pkg/gateway/sandbox_apply.go appends
		// this port to ConnectPortRules when the browser tool is enabled.
		chromedp.Flag("remote-debugging-port", browserDebugPort),
		chromedp.Env(
			"HOME="+chromeHome,
			"XDG_CONFIG_HOME="+filepath.Join(chromeHome, "config"),
			"XDG_CACHE_HOME="+filepath.Join(chromeHome, "cache"),
		),
		chromedp.WindowSize(1280, 720),
		// Anti-bot-detection ("stealth"): reduce the trivially-detectable
		// automation signals that lead sites (notably Google) to serve a
		// CAPTCHA even for a human driving the panel. `enable-automation`
		// (added by DefaultExecAllocatorOptions) sets navigator.webdriver=true
		// and shows the automation infobar; the AutomationControlled blink
		// feature is the other source of navigator.webdriver. Removing both,
		// plus the User-Agent de-Headless + navigator overrides in applyStealth
		// (per new tab), covers the obvious fingerprint tells.
		//
		// IMPORTANT: this only hides browser fingerprints. It CANNOT overcome
		// datacenter-IP reputation, which is the dominant factor for Google's
		// "unusual traffic" CAPTCHA — a gateway on a cloud/datacenter IP (e.g.
		// a Fly/pod preview) may still be challenged regardless. These help
		// most on a self-hosted deployment browsing from a residential/office IP.
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("lang", "en-US"),
	)

	if m.cfg.Headless {
		opts = append(opts, chromedp.Headless)
	}

	// Chromium's zygote sandbox depends on creating new user namespaces, which
	// the gateway's Landlock+PR_SET_NO_NEW_PRIVS policy blocks. The gateway
	// already enforces an outer filesystem and network sandbox, so Chrome's
	// inner sandbox is redundant — disable it by default to avoid the
	// permission-denied init failures. Operators can opt out by setting
	// OMNIPUS_BROWSER_NO_SANDBOX=0 if they run the gateway without Landlock.
	if os.Getenv("OMNIPUS_BROWSER_NO_SANDBOX") != "0" {
		opts = append(opts, chromedp.NoSandbox)
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
// launch. Resolution order:
//
//  1. cfg.ExecPath (operator override) — used as-is.
//  2. System chromium/google-chrome on $PATH — preferred when present.
//  3. Managed install under <ProfileDir>/../chromium/ — downloaded from
//     Chrome for Testing on first call. Cached across restarts so the
//     download cost is amortized once per host.
func (m *BrowserManager) resolveExecPath(ctx context.Context) (string, error) {
	if m.cfg.ExecPath != "" {
		if _, err := os.Stat(m.cfg.ExecPath); err != nil {
			return "", fmt.Errorf("configured exec_path %s: %w", m.cfg.ExecPath, err)
		}
		return m.cfg.ExecPath, nil
	}
	// Operators can force the managed install path (skipping the $PATH
	// lookup) by setting OMNIPUS_BROWSER_FORCE_MANAGED=1. Useful for
	// pinning a deterministic Chromium version even when a system
	// google-chrome is present, and for exercising the install flow in
	// CI / staging hosts that already have Chrome installed.
	forceManaged := os.Getenv("OMNIPUS_BROWSER_FORCE_MANAGED") == "1"
	if !forceManaged {
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				return path, nil
			}
		}
	}
	installRoot := filepath.Join(filepath.Dir(filepath.Clean(m.cfg.ProfileDir)), "..", "chromium")
	installRoot = filepath.Clean(installRoot)
	return EnsureChromium(ctx, installRoot)
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
// concurrent Session() call for the SAME sessionID that arrives while
// creation is in flight waits on m.pending instead of independently calling
// chromedp.NewContext/Run for that ID too (which would create and leak a
// second tab for one logical session). ADR-041 preserves this discipline
// exactly — createTab (below) is the same "no lock across CDP" call, now
// factored out so it's shared with OpenTab/CloseTab/adoptTarget.
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
			// the whole browsing context and recreate a fresh single-tab one
			// below — the same crash-recovery behavior the pre-ADR-041 code
			// applied to its one-tab-per-session model, now applied to the
			// tab SET. (Resurrecting a different surviving tab as active
			// instead is a possible future refinement; out of scope here —
			// an active tab dying out from under the manager, as opposed to
			// an explicit CloseTab, is not the case ADR-041 targets.)
			for _, t := range se.tabs {
				t.cancel()
			}
			delete(m.sessions, sessionID)
		}

		if wait, ok := m.pending[sessionID]; ok {
			// Someone else is already creating this session's tab — wait
			// for them to finish, then loop back and re-check m.sessions,
			// rather than racing to create a second tab for the same ID.
			m.mu.Unlock()
			<-wait
			continue
		}

		if m.totalTabCountLocked() >= m.cfg.MaxTabs {
			m.mu.Unlock()
			return nil, fmt.Errorf("maximum concurrent tabs (%d) reached. Close a tab first", m.cfg.MaxTabs)
		}

		done := make(chan struct{})
		if m.pending == nil {
			m.pending = make(map[string]chan struct{})
		}
		m.pending[sessionID] = done
		allocCtx := m.allocCtx
		m.mu.Unlock()

		tab, err := m.createTab(allocCtx, "")

		m.mu.Lock()
		delete(m.pending, sessionID)
		if err != nil {
			m.mu.Unlock()
			close(done)
			return nil, fmt.Errorf("browser: failed to initialize tab: %w", err)
		}
		se := &sessionEntry{tabs: []*tabEntry{tab}, activeIdx: 0}
		m.installTargetListenerLocked(sessionID, se)
		m.sessions[sessionID] = se
		tabs := snapshotTabsLocked(se)
		m.mu.Unlock()
		close(done)
		m.notifyTabsChanged(sessionID, tabs, 0)
		return tab.ctx, nil
	}
}

// createTab performs the CDP work to bind a chromedp context to a browser
// tab — either a brand-new target (targetID == "") or an existing one
// (targetID != "", ADR-041 D2 adoption via chromedp.WithTargetID). MUST be
// called with NO BrowserManager lock held; this is the CDP entry point every
// tab-creating/adopting call site in this file shares, all obeying the exact
// discipline Session()'s doc comment above describes (never wrap the FIRST
// Run on a fresh ctx in a timeout — chromedp binds the target's lifetime to
// that first Run's context).
func (m *BrowserManager) createTab(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
	if m.createTabFn != nil {
		return m.createTabFn(allocCtx, targetID)
	}
	var opts []chromedp.ContextOption
	if targetID != "" {
		opts = append(opts, chromedp.WithTargetID(targetID))
	}
	ctx, cancel := chromedp.NewContext(allocCtx, opts...)

	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, err
	}

	// Best-effort stealth on a bounded timeout CHILD of ctx — safe because
	// cancelling a child of an already-bound target does NOT tear the tab
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
// method in this file (Session, OpenTab, CloseTab, SwitchTab, adoptTarget,
// handleTargetEvent) calls this only after releasing m.mu.
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
	se, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return Tab{}, fmt.Errorf("browser: no active session %q", sessionID)
	}
	if index < 0 || index >= len(se.tabs) {
		m.mu.Unlock()
		return Tab{}, fmt.Errorf("browser: tab index %d out of range (0-%d)", index, len(se.tabs)-1)
	}
	se.activeIdx = index
	tabs := snapshotTabsLocked(se)
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, index)
	return tabs[index], nil
}

// CloseTab closes tab `index` in sessionID's browsing context (cancels its
// chromedp target — a cheap, non-blocking call; see BrowserManager.
// CloseSession's identical existing pattern). If it was the active tab, a
// neighbour is activated instead (the tab that slid into the same index;
// falls back to the new last tab if the closed tab was the set's last).
// NEVER leaves the browsing context with zero tabs (ADR-041 D3/
// Consequences) — closing the last remaining tab opens a fresh blank
// replacement instead, which DOES talk to CDP and therefore runs with no
// BrowserManager lock held, mirroring Session()'s discipline.
func (m *BrowserManager) CloseTab(sessionID string, index int) (tabs []Tab, activeIdx int, err error) {
	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, 0, fmt.Errorf("browser: no active session %q", sessionID)
	}
	if index < 0 || index >= len(se.tabs) {
		m.mu.Unlock()
		return nil, 0, fmt.Errorf("browser: tab index %d out of range (0-%d)", index, len(se.tabs)-1)
	}

	if len(se.tabs) == 1 {
		closing := se.tabs[0]
		delete(m.sessions, sessionID) // torn down; recreated below on success
		allocCtx := m.allocCtx
		m.mu.Unlock()

		closing.cancel()
		newTab, cerr := m.createTab(allocCtx, "")
		if cerr != nil {
			return nil, 0, fmt.Errorf("browser: closed last tab but failed to open a replacement: %w", cerr)
		}

		m.mu.Lock()
		newSE := &sessionEntry{tabs: []*tabEntry{newTab}, activeIdx: 0}
		m.installTargetListenerLocked(sessionID, newSE)
		m.sessions[sessionID] = newSE
		tabs = snapshotTabsLocked(newSE)
		activeIdx = 0
		m.mu.Unlock()

		m.notifyTabsChanged(sessionID, tabs, activeIdx)
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
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		return Tab{}, fmt.Errorf("maximum concurrent tabs (%d) reached. Close a tab first", m.cfg.MaxTabs)
	}
	allocCtx := m.allocCtx
	m.mu.Unlock()

	newTab, err := m.createTab(allocCtx, "")
	if err != nil {
		return Tab{}, fmt.Errorf("browser: failed to open new tab: %w", err)
	}

	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok {
		se = &sessionEntry{}
		m.sessions[sessionID] = se
	}
	// Re-check the cap post-CDP-call — a concurrent OpenTab/adoption could
	// have raced us to it while createTab ran unlocked.
	if len(se.tabs) > 0 && m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		newTab.cancel()
		return Tab{}, fmt.Errorf("maximum concurrent tabs (%d) reached. Close a tab first", m.cfg.MaxTabs)
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1
	m.installTargetListenerLocked(sessionID, se)
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	return tabs[activeIdx], nil
}

// adoptTarget attaches a chromedp context to an existing CDP target — one a
// target="_blank" click, window.open, or Ctrl/Cmd+click spawned — and
// appends it as a new tab to sessionID's tab set, making it active by
// default (ADR-041 D2). Idempotent: a target already tracked, or one a
// concurrent adoption is already in flight for, is a silent no-op (nil, nil)
// rather than an error. Enforces MaxTabs: a runaway window.open loop is
// capped, not unbounded — adoption beyond the cap is silently refused
// (logged at WARN), matching "drop/refuse beyond the cap" per ADR-041,
// since this path has no direct caller to return an error to (it fires from
// a background CDP event or a reconcile pass, never a synchronous tool call
// the LLM is waiting on).
//
// Deadlock-safe per ADR-038: createTab's CDP attach runs with NO
// BrowserManager lock held.
func (m *BrowserManager) adoptTarget(sessionID string, targetID target.ID) (*Tab, error) {
	if targetID == "" {
		return nil, nil //nolint:nilnil // sentinel "nothing to adopt", not a failure — see doc comment
	}

	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, nil //nolint:nilnil // no browsing context to adopt into yet
	}
	if se.indexOfTarget(targetID) >= 0 {
		m.mu.Unlock()
		return nil, nil //nolint:nilnil // already ours
	}
	if m.pendingAdopt == nil {
		m.pendingAdopt = make(map[target.ID]struct{})
	}
	if _, already := m.pendingAdopt[targetID]; already {
		m.mu.Unlock()
		return nil, nil //nolint:nilnil // a concurrent adoption is already in flight for this target
	}
	if m.totalTabCountLocked() >= m.cfg.MaxTabs {
		m.mu.Unlock()
		logger.WarnCF("browser", "new tab target detected but MaxTabs reached — not adopting", map[string]any{
			"session_id": sessionID,
			"target_id":  string(targetID),
			"max_tabs":   m.cfg.MaxTabs,
		})
		return nil, nil //nolint:nilnil // capped, not an error — see doc comment
	}
	m.pendingAdopt[targetID] = struct{}{}
	allocCtx := m.allocCtx
	m.mu.Unlock()

	newTab, err := m.createTab(allocCtx, targetID)

	m.mu.Lock()
	delete(m.pendingAdopt, targetID)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("browser: failed to adopt new tab target %s: %w", targetID, err)
	}
	se, ok = m.sessions[sessionID]
	if !ok || se.indexOfTarget(targetID) >= 0 || m.totalTabCountLocked() >= m.cfg.MaxTabs {
		// The browsing context vanished, another racer already adopted this
		// target, or we're now over cap — all re-checked post-CDP-call since
		// createTab ran unlocked. Release the just-created tab rather than
		// leaking it.
		m.mu.Unlock()
		newTab.cancel()
		return nil, nil //nolint:nilnil // superseded by a concurrent change — not an error
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1 // ADR-041 D2: adopted tabs become active by default
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	m.mu.Unlock()

	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	active := tabs[activeIdx]
	return &active, nil
}

// reconcileTargetListTimeout bounds the chromedp.Targets CDP round trip
// ReconcileTabs issues — a read-only "list every target" query, but still
// routed through a deadline (ADR-038 discipline: every CDP round trip is
// bounded) so a wedged transport fails this one reconcile pass instead of
// hanging its caller (browser_click) forever.
const reconcileTargetListTimeout = 5 * time.Second

// ReconcileTabs looks for CDP targets opened by one of sessionID's own tabs
// (target="_blank", window.open, Ctrl/Cmd+click) that this browsing context
// hasn't adopted yet, and adopts them (ADR-041 D2). Called deterministically
// right after browser_click (tools.go) — the guaranteed detection point —
// complementing the best-effort passive Target.targetCreated listener
// installed on each browsing context's root tab
// (installTargetListenerLocked). Returns adopted=true and the finally-active
// tab when at least one adoption succeeded (if a page opened more than one
// new tab in one go, the LAST one adopted ends up active, same as if they'd
// been adopted one at a time via the passive listener).
func (m *BrowserManager) ReconcileTabs(sessionID string) (adopted bool, newActive *Tab, err error) {
	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok || len(se.tabs) == 0 {
		m.mu.Unlock()
		return false, nil, nil
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
		return false, nil, fmt.Errorf("browser: failed to list targets for reconcile: %w", lerr)
	}

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
		tab, aerr := m.adoptTarget(sessionID, info.TargetID)
		if aerr != nil {
			logger.WarnCF("browser", "reconcile: failed to adopt detected tab", map[string]any{
				"session_id": sessionID,
				"target_id":  string(info.TargetID),
				"error":      aerr.Error(),
			})
			continue
		}
		if tab != nil {
			adopted = true
			newActive = tab
			tracked[info.TargetID] = struct{}{} // avoid reprocessing within this pass
		}
	}
	return adopted, newActive, nil
}

// installTargetListenerLocked attaches the ADR-041 D2 passive
// target-created listener to se's ROOT tab (its first tab at call time) —
// once per browsing context, guarded by se.listenerInstalled. chromedp
// enables Target.setDiscoverTargets(true) per tab-session, but discovery
// itself is browser-global, so installing this listener on every tab would
// multiply duplicate (idempotently-handled by adoptTarget, but wasteful)
// events for the same new target. Must be called with m.mu held — chromedp.
// ListenTarget itself is a cheap, non-blocking, lock-free append (mirrors
// how live.go's attach() calls it), never a CDP round trip.
func (m *BrowserManager) installTargetListenerLocked(sessionID string, se *sessionEntry) {
	if se.listenerInstalled || len(se.tabs) == 0 {
		return
	}
	rootCtx := se.tabs[0].ctx
	se.listenerInstalled = true
	chromedp.ListenTarget(rootCtx, func(ev any) {
		m.handleTargetEvent(sessionID, ev)
	})
}

// handleTargetEvent is the chromedp.ListenTarget callback for a browsing
// context's root tab (ADR-041 D2/D4). Per chromedp's contract (mirrors
// live.go's handleScreencastEvent exactly) this runs SYNCHRONOUSLY on the
// CDP event-dispatch goroutine and must never block or call chromedp.Run
// inline: for an already-tracked tab, a title/url change is cheap
// bookkeeping done inline (no CDP call); adopting a brand-new target is
// dispatched onto its own goroutine.
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
			if changed {
				tabs := snapshotTabsLocked(se)
				activeIdx := se.activeIdx
				m.mu.Unlock()
				m.notifyTabsChanged(sessionID, tabs, activeIdx)
				return
			}
			m.mu.Unlock()
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

// CloseSession closes every tab in a specific browsing context (ADR-041 D1:
// a session is now a tab SET, so this cancels all of them, not just one).
func (m *BrowserManager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if se, ok := m.sessions[sessionID]; ok {
		for _, t := range se.tabs {
			t.cancel()
		}
		delete(m.sessions, sessionID)
	}
}

// PageTimeout returns the configured page load timeout.
func (m *BrowserManager) PageTimeout() time.Duration {
	return m.cfg.PageTimeout
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

// Shutdown gracefully shuts down the browser process and all sessions.
func (m *BrowserManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, se := range m.sessions {
		for _, t := range se.tabs {
			t.cancel()
		}
		delete(m.sessions, id)
	}

	if m.allocCancel != nil {
		m.allocCancel()
		m.allocCancel = nil
	}
	m.started = false

	logger.InfoCF("browser", "Browser shut down", nil)
}
