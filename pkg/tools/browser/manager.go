// Package browser implements browser automation tools using chromedp (pure Go CDP).
//
// Implements US-4 (managed mode), US-6 (remote CDP mode), US-7 (resource limits)
// from the Wave 4 spec. All navigations are SSRF-checked via pkg/security.SSRFChecker.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// BrowserConfig holds browser automation configuration.
// Mapped from config.json: tools.browser.*
type BrowserConfig struct {
	Enabled     bool          `json:"enabled"`
	Headless    bool          `json:"headless"`
	CDPURL      string        `json:"cdp_url"`      // Remote CDP WebSocket URL (US-6)
	PageTimeout time.Duration `json:"page_timeout"` // Per-page load timeout (US-7, default 30s)
	// LeaseWait bounds how long a leased browser tool retries for the write
	// lease before it defers (§14, FR-023). Zero leaves leaseWaitTimeout's 2s
	// default in force. The operator-facing key is tools.browser.lease_wait,
	// CLAMPED to at most half PageTimeout at load and on reload
	// (config.ClampLeaseWait, FR-023a) — the clamp is what keeps the whole
	// retry window inside the tool's own CDP deadline.
	LeaseWait      time.Duration `json:"lease_wait"`
	PersistSession bool          `json:"persist_session"` // Persist cookies/localStorage across restarts
	ProfileDir     string        `json:"profile_dir"`     // User data dir (default ~/.omnipus/browser/profiles/default/)
	// ExecPath overrides Chromium discovery. When empty the manager prefers
	// a system chromium/google-chrome on PATH and falls back to a managed
	// install under <ProfileDir>/../chromium/ (downloaded in the background
	// at gateway boot via Preprovision, or lazily on first tool use as a
	// fallback).
	ExecPath string `json:"exec_path,omitempty"`
	// ExtensionDir + ExtensionID (WebRTC build W1-A item 3) are an optional
	// hook for loading an unpacked extension into the managed shared Chrome
	// via CDP Extensions.loadUnpacked (see coordinator.go's LoadExtension).
	// Both empty by default — nothing in this package sets them yet; a later
	// wave wires the gateway's capture extension through this pair. When
	// ExtensionID is set, managedExecAllocatorOpts (exec_resolver.go) adds
	// --allowlisted-extension-id=<ExtensionID> and the
	// --enable-unsafe-extension-debugging flag Extensions.loadUnpacked
	// requires; ExtensionDir must then also be set for the coordinator's
	// post-launch auto-load to actually run.
	ExtensionDir string `json:"extension_dir,omitempty"`
	ExtensionID  string `json:"extension_id,omitempty"`
	// PreferPackaged (ADR-052 D2/M1) makes the runtime package-managed Chrome
	// (sibling chromium/ dir next to the binary, computed at runtime via
	// os.Executable()) OUTRANK a system Chrome on $PATH during resolution —
	// intended for fleets that want the pinned package Chrome to win for
	// reproducibility. Default false preserves operator autonomy (M1): a
	// deliberately newer/patched $PATH Chrome still wins on a fresh install.
	// Operator `exec_path` override (above) ALWAYS outranks this, as before.
	PreferPackaged bool `json:"prefer_packaged,omitempty"`
	// TrustPathChrome (ADR-052 SEC-002) gates whether a Chrome found on
	// $PATH is permitted to launch WITHOUT integrity verification. Default
	// false (the security-hardened default): a $PATH resolution is recorded
	// at WARN-BROWSER-007 and the resolver falls through to the verified
	// package Chrome (SEC-ADR052-002 — an unverified binary is a "trusted
	// RCE-engine origin" risk on a multi-tenant / CI-runner / compromised
	// host). Operators who deliberately want a custom Chrome set this to
	// true (or use the explicit ExecPath override, which always wins).
	TrustPathChrome bool `json:"trust_path_chrome,omitempty"`
	// IdleTTL reaps individual browser TABS that have had NO activity for this
	// long — no attached live-panel viewer on the tab's browsing context, and
	// no agent tool call touching that specific tab. Without it a browsing
	// context lived forever: closing the live panel is a pure UI dismiss (the
	// SPA sends no shutdown), so reopening it days later showed the exact
	// page the user left, on a Chrome that had been resident the whole time.
	// Zero disables reaping entirely.
	//
	// Reaping runs PER TAB, not per browsing context (see
	// BrowserManager.ReapIdleSessions): an idle tab is individually closed
	// while any tab still in active use survives untouched, and a browsing
	// context with an attached live-panel viewer is skipped in its ENTIRETY
	// regardless of any individual tab's own idle time — the tab strip shows
	// every tab in that context to the watching human, so none of them
	// should vanish out from under them.
	//
	// Default 5m (operator directive, 2026-08): safe to keep short BECAUSE
	// reaping is per-tab and gated on real activity — an agent mid-task in a
	// tab refreshes that tab's own clock on every tool call that touches it
	// (see BrowserManager.touchTabLocked), and a tab a human is watching is
	// fully protected via the viewer count regardless of this TTL. What a
	// short TTL actually cleans up is tabs nobody is using or watching at
	// all — exactly the steady-state resident-tab count this reaper exists
	// to bound.
	IdleTTL time.Duration `json:"idle_ttl,omitempty"`
	// IdleCloseTTL is the WHOLE-CHROME idle window (ADR-072 FR-040): how long
	// a workspace's browser may sit with zero tabs, zero live viewers and no
	// call in flight before the process itself is closed. The profile
	// directory survives, so the workspace is still logged in next time.
	//
	// It is a strictly coarser thing than IdleTTL, which reaps one TAB. The
	// per-tab reaper is what brings a browser to zero tabs in the first
	// place, so this window is deliberately a multiple of that one — see
	// pool.go's defaultIdleCloseTTL for the value and for the fact that it is
	// a reasoned default, not a measured one.
	//
	// Zero means "use the default". There is no way to disable it: idle close
	// is one of the two things bounding this pool's memory, and FR-061
	// forbids either of them shipping behind an off switch.
	IdleCloseTTL time.Duration `json:"idle_close_ttl,omitempty"`
	// CacheTrimInterval is how often the pool sweeps CLOSED workspace profiles
	// for disposable browser cache (ADR-072 FR-072). Reload-applied.
	//
	// ⚠ It does NOT bound a profile's size, and the config documentation must
	// not imply that it does (FR-074). Nothing is trimmed while a Chrome is
	// live — trimming a running browser's cache would mean closing a browser
	// somebody is using — so a workspace driven with no idle gap keeps growing
	// its cache for as long as it is driven, whatever this is set to.
	//
	// Zero means "use the default" (1h).
	CacheTrimInterval time.Duration `json:"cache_trim_interval,omitempty"`
	// StartPageURL is what a fresh tab opens instead of about:blank. Empty
	// falls back to about:blank (the pre-existing behavior). The gateway sets
	// this to its own served start page so a reopened panel lands somewhere
	// branded and actionable rather than on a blank void that reads as broken.
	StartPageURL string `json:"start_page_url,omitempty"`
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
		LeaseWait:   leaseWaitTimeout,
		ProfileDir:  filepath.Join(base, "browser", "profiles", "default"),
		IdleTTL:     DefaultIdleTTL,
	}, nil
}

// DefaultIdleTTL is how long an individual browser tab may sit with no
// viewer on its browsing context and no agent tool call touching it before
// ReapIdleSessions closes it. See BrowserConfig.IdleTTL for the per-tab
// reaping model and why 5 minutes is safe rather than aggressive.
const DefaultIdleTTL = 5 * time.Minute

// BlankPageURL is the fallback a fresh tab opens when no start page is
// configured — the historical behavior, kept as the floor so a misconfigured
// StartPageURL can never leave a tab with nowhere to go.
const BlankPageURL = "about:blank"

// StartPageURL returns the URL a freshly created tab should open. Falls back
// to about:blank when no start page is configured.
//
// Why a start page at all: a reopened live panel used to land on about:blank —
// a blank void that is indistinguishable from the panel being broken, which is
// exactly the failure mode the streaming bugs already made users suspect. A
// branded, actionable page makes "nothing loaded yet" legible as a state
// rather than a fault.
func (m *BrowserManager) StartPageURL() string {
	if u := strings.TrimSpace(m.cfg.StartPageURL); u != "" {
		return u
	}
	return BlankPageURL
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
	// lastActivity is when THIS SPECIFIC TAB was last touched — by its own
	// creation (createTab), an agent tool call resolving it via Session()
	// (it is the browsing context's active tab at that moment), SwitchTab
	// making it the active tab, or a live-panel viewer attaching/detaching
	// on this tab's browsing context (which touches EVERY tab in the
	// context, not just the active one — see touchAllTabsLocked).
	// BrowserManager.ReapIdleSessions judges every tab in a browsing context
	// independently against this timestamp; a context with an attached
	// live-panel viewer is additionally protected in its entirety regardless
	// of any individual tab's value here (see ReapIdleSessions' doc comment
	// for why). Guarded by BrowserManager.mu like every other tabEntry
	// field — see touchTabLocked/touchAllTabsLocked.
	lastActivity time.Time
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

	// emptySince is when this session was first observed with ZERO tabs, and
	// exists ONLY so a stranded empty session can still be reaped — every
	// other idle decision is per-tab (tabEntry.lastActivity). Zero whenever the
	// session has tabs. See ReapIdleSessions' zero-tab branch for why this is
	// stamped-then-judged rather than acted on immediately.
	emptySince time.Time
	// viewers counts currently-attached live-panel viewers. A browsing
	// context with a viewer attached is NEVER idle no matter how long ago
	// any of its tabs were last touched — somebody is literally watching it,
	// and the tab strip shows them EVERY tab in the context, not just the
	// active one, so ReapIdleSessions protects the whole context in its
	// entirety while viewers > 0 rather than judging tabs individually (see
	// ReapIdleSessions' doc comment). Incremented/decremented via
	// ViewerAttached/ViewerDetached.
	viewers int
	// lastViewerBeat is when a viewer on this browsing context last PROVED it
	// was still there (ADR-072 FR-052). Stamped by ViewerAttached and
	// re-stamped by every ViewerHeartbeat; never cleared, because a session
	// with zero viewers is judged by the count alone.
	//
	// It exists because `viewers` on its own is a raw count of attaches that
	// were never matched by a detach — and "never matched by a detach" is not
	// the same claim as "somebody is still watching". A live-panel WebSocket
	// whose cleanup never ran (the process holding it was SIGKILLed, the
	// socket half-opened behind a NAT that dropped state, a panic that
	// skipped readLoop's defer) leaves the count at 1 with nobody behind it.
	// That phantom pins its workspace's Chrome against BOTH eviction (pool.go
	// `pinned`) and idle close (`idle`) forever — a deadlock, not a leak: the
	// browser can never be reclaimed, and under memory pressure the pool
	// refuses to launch others while it holds one open.
	//
	// See viewerLivenessWindow for how stale is defined, and
	// liveViewersLocked for where the two are combined.
	lastViewerBeat time.Time
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

	// dialogListeners records which tabs already have a
	// Page.javascriptDialogOpening listener, keyed by target id.
	//
	// This is NOT the same shape as listenerTarget above, and the difference
	// is the whole point. Target DISCOVERY is browser-global, so one listener
	// on tab 0 sees every new target. A JavaScript dialog is not: it is
	// per-target, so a dialog raised on tab 2 with a tab-0-only listener is
	// invisible — the tab is wedged and nothing anywhere records that a
	// dialog exists. EVERY tab needs its own.
	//
	// The map exists because chromedp.ListenTarget is an APPEND: installing
	// twice on one ctx stacks two handlers and records every dialog twice.
	// Checked-and-set under m.mu immediately before the append.
	dialogListeners map[target.ID]struct{}

	// pendingDialogs records the dialog currently blocking each tab.
	//
	// An open dialog blocks ALL further CDP on that target, so this map is
	// the only evidence any other tool has for why it just timed out.
	// Entries are removed BEFORE the Page.handleJavaScriptDialog call that
	// clears them, never after — see BrowserManager.TakePendingDialog.
	pendingDialogs map[target.ID]*PendingDialog

	// lastActivation records the last action a tool completed on each tab
	// ("a click", "a key press", …). It is advisory wording ONLY: nothing
	// branches on it. When it is set, a timeout message can say "stopped
	// answering after a click" instead of just "stopped answering", which is
	// strictly more useful to the agent; when it is not, the message is
	// simply less specific. Written by the TOOL under m.mu after its own CDP
	// call returns — never by handleTargetEvent, whose doc forbids blocking.
	// A second concurrent tool on the same tab overwrites it, which is fine
	// for wording and would not be for a decision.
	lastActivation map[target.ID]string
}

// PendingDialog is one JavaScript dialog blocking one tab.
type PendingDialog struct {
	// Type is the CDP dialog type: alert, confirm, prompt or beforeunload.
	Type string
	// Message is the text the page asked to display.
	Message string
	// URL is the frame that raised it.
	URL string
	// DefaultPrompt is the pre-filled value of a prompt() dialog.
	DefaultPrompt string
	// OpenedAt is when the listener observed it.
	OpenedAt time.Time
}

// Summary renders a pending dialog for an error message an agent reads.
func (d *PendingDialog) Summary() string {
	if d == nil {
		return ""
	}
	if d.Message == "" {
		return fmt.Sprintf("a %s dialog", d.Type)
	}
	return fmt.Sprintf("a %s dialog saying %q", d.Type, d.Message)
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
// Additional sessions can be created for parallel browsing; the only limit is
// live memory (ADR-072 D1.5a — every tab counter was deleted).
type BrowserManager struct {
	cfg  BrowserConfig
	ssrf *security.SSRFChecker // never nil — enforced by NewBrowserManager
	mu   sync.Mutex
	// allocCtx is the chromedp context ensureStarted's tab-creating callers
	// (bootstrapBrowserCtx etc.) build off. In coordinator (shared-Chrome)
	// mode this is the coordinator's rootCtx itself (CRIT-001: chromedp CHILD
	// contexts of the one CDP pipe, not a RemoteAllocator dial — see
	// coordinator.go's Register doc comment); allocCancel is then a no-op,
	// since there is no manager-local connection to tear down (the
	// coordinator owns the pipe's whole lifecycle). In the no-coordinator
	// managed-mode fallback (tests + the legacy one-manager-one-Chrome path)
	// this is the pipe allocator's own rootCtx (pipeLaunchResult.rootCtx) and
	// allocCancel DOES kill this manager's own Chrome. In remote-CDP-override
	// mode (cfg.CDPURL set) this is a chromedp.NewRemoteAllocator context.
	allocCtx    context.Context
	allocCancel context.CancelFunc
	// instanceAudited is FR-027's once-only latch for the
	// browser_instance_created audit event: the first tool call to resolve
	// this manager records that the workspace's browser came into existence,
	// and no later call records it again. Atomic rather than guarded by m.mu
	// because it is read on the resolve path of EVERY browser tool call, and
	// m.mu is held across CDP work elsewhere in this type. See
	// markInstanceAudited in audit.go.
	instanceAudited atomic.Bool
	// pipeLauncherFn launches Chrome over the CDP pipe for the no-coordinator
	// managed-mode fallback (ensureStarted). A field — not a direct
	// launchManagedPipe call — purely for testability, mirroring
	// createTabFn/listTargets/evalCDP's exact rationale: tests substitute a
	// fake so the mutex/discard-the-loser concurrency discipline around
	// ensureStarted can be exercised deterministically without spawning real
	// Chrome. nil-checked at the call site and defaults to launchManagedPipe.
	pipeLauncherFn func(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error)
	sessions       map[string]*sessionEntry
	// tabFocus records, for a chat session that has taken over the operator's
	// workspace-owned tabs, which tab set its NEXT call addresses. Keyed by
	// the session's own TabOwner; the value is the TabOwner it is currently
	// driving. Guarded by m.mu. Lazily created; a nil map is a valid empty
	// state, and an absent entry means "its own set", which is every turn
	// that has not taken anything over.
	//
	// This is NOT ownership and must not be read as any (ADR-072 FR-070,
	// §0.7's C-403 note). The operator's tabs stay owned by the workspace:
	// every session on the workspace can still see them, every session can
	// still drive them, and nothing here transfers on a take-over. What it
	// records is where ONE session's cursor is pointing — the thing
	// browser_switch_tab's own description has always promised ("subsequent
	// browser_* tool calls follow the newly-active tab") and that had no
	// referent once a tab set stopped being the only one a turn could reach.
	//
	// It self-heals rather than needing a cleanup pass: focusedTabSet drops
	// an entry whose target set no longer exists, so a reaped operator set
	// puts the session back on its own tabs instead of silently resurrecting
	// a workspace-owned one.
	tabFocus map[string]TabOwner
	// inFlight counts browser tool calls currently executing against this
	// manager. Guarded by m.mu — see InFlight()'s doc comment for why it is
	// deliberately not an atomic.
	inFlight int
	// memoryPressureFn is the FR-060 gate's test seam — a field, not a direct
	// config.MemoryPressureHigh call, for exactly the reason createTabFn /
	// listTargets / evalCDP are fields: a test must be able to drive the gate
	// deterministically without a host whose real memory it cannot control.
	// nil in production, where the gate reads the ONE shared accessor
	// (config.MemoryPressureHigh) against the ONE shared threshold.
	//
	// The open-tab count is passed IN rather than read by the fake, because
	// this is called with m.mu HELD and any accessor that re-takes m.mu would
	// deadlock.
	memoryPressureFn func(openTabs int) (high bool, ok bool)

	// key is the BrowsingKey this manager IS — one browser, one workspace,
	// one profile directory (FR-001). Set at construction; never mutated.
	// It is half of every sessions-map key (sessionKey(key, owner)), so a
	// manager that does not know its own key cannot name its own tab sets.
	key BrowsingKey
	// leases is the write-lease table for this browser (§14). Its mutex is
	// deliberately NOT m.mu: a leased tool holds the lease across seconds of
	// CDP, and the ADR-038 "no lock across a blocking call" discipline forbids
	// holding m.mu for that long. Lock order is writeLease -> pool.mu -> m.mu.
	leases writeLeaseTable
	// pending tracks session IDs currently being created by Session() — see
	// Session()'s doc comment (ADR-038 deadlock postmortem) for why tab
	// creation must release m.mu before its blocking chromedp.Run call, and
	// why a concurrent Session() call for the same ID must wait here instead
	// of also calling chromedp.NewContext/Run for that ID (which would
	// create and leak a second tab, and corrupt the tab count). Lazily
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
	// mode branch asks the coordinator to launch+provide that key's Chrome
	// instead of building its own ExecAllocator.
	//
	// CRIT-003 is now trivially true rather than carefully maintained: since
	// ADR-072 FR-031 there are no CDP browser contexts at all, so no path can
	// create one whose disposal would destroy cookies on a reload. Cookies
	// live in the workspace's profile directory on disk.
	coordinator *BrowserCoordinator
	// pool is the ADR-072 per-workspace browser pool. When set it SUPERSEDES
	// the coordinator field: ensureStarted asks the pool for this key's
	// Chrome, and the pool hands back the coordinator that owns it. The
	// coordinator field is then a cached result, refreshed on every
	// re-register — which matters because an idle close or an eviction
	// replaces a key's coordinator, and a manager holding the old pointer
	// would drive a dead pipe forever.
	//
	// A nil pool with a non-nil coordinator is the direct/test path.
	pool *BrowserPool
	// agentID identifies this per-agent manager to the coordinator (Register/
	// Release/RemoveAgent are keyed by it). Set via AttachSharedChrome.
	agentID string
	// capture/captureMu (ADR-047, wave-plan W2-A) hold this manager's single
	// active WebRTC CaptureSession, guarded by their own mutex — see
	// CaptureSession()/EnsureCaptureSession()'s doc comments for why this is
	// deliberately NOT m.mu.
	capture   *CaptureSession
	captureMu sync.Mutex

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

	// adoptRetryBackoff overrides defaultAdoptRetryBackoff, the bounded
	// schedule adoptTargetWithRetry walks after a failed adoption. A field
	// rather than a package var purely so tests can shrink the delays
	// without mutating state shared with every other test in this package
	// (the same isolation rationale as createTabFn/tabFocusFn). nil in
	// production. Guarded by m.mu.
	adoptRetryBackoff []time.Duration

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

	// tabFocusFn is the test seam for the CDP round trips that move Chrome's
	// foreground between this session's tabs — activateTabInChrome's
	// foregroundTabActions and releaseTabFocusInChrome's
	// backgroundTabActions. It mirrors createTabFn's rationale exactly: the
	// fake tab contexts unit tests build (tabs_test.go's fakeTabFactory) are
	// chromedp contexts with no CDP connection behind them, so a real
	// chromedp.Run against one would block until PageTimeout rather than
	// doing anything observable. nil by default, in which case both
	// functions run their normal chromedp body.
	//
	// The actions are passed THROUGH rather than hidden behind the seam
	// (review finding F9, 2026-08-13) so a test can see WHICH treatment a tab
	// got, not merely that some CDP call happened — the defect it closes was
	// precisely a path that did half the treatment.
	tabFocusFn func(tabCtx context.Context, actions ...chromedp.Action) error

	// abandonCDPFn is the test seam for the two recovery CDP round trips
	// abandonTabAfterFailedLoad makes (tools.go): the diagnostic location read
	// and the security-critical about:blank navigation. Each is handed an
	// ALREADY-BOUNDED context (one independent tabAbandonTimeout budget per
	// call), so a stand-in can reproduce "this step burned its entire budget"
	// — the wedged-renderer case the whole helper exists for — deterministically
	// and observe what context the NEXT step then received. Same rationale as
	// tabFocusFn/LiveView.runCDP (see runCDPWithTimeout's doc comment); nil
	// in production, where the calls go to chromedp.Run.
	abandonCDPFn func(ctx context.Context, actions ...chromedp.Action) error

	// nowFn overrides the clock for idle/TTL logic (ReapIdleSessions), so
	// tests can age a session deterministically instead of sleeping. nil in
	// production, where m.now() falls through to time.Now().
	nowFn func() time.Time

	// navigateFn is the test seam for navigateNewTabToStartPage's single CDP
	// navigation, same rationale as createTabFn/tabFocusFn: unit tests hold
	// chromedp contexts with no CDP connection behind them, where a real
	// chromedp.Run would block until PageTimeout. nil in production.
	navigateFn func(tabCtx context.Context, url string) error

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

// AttachSharedChrome wires this manager to the coordinator that owns its
// browsing key's Chrome (ADR-043, re-keyed by ADR-072 FR-001). When set,
// ensureStarted asks that coordinator to launch/provide the key's Chrome. A
// nil coordinator (the default for direct/test construction) keeps the legacy
// per-manager ExecAllocator behavior.
//
// key identifies this manager to the coordinator. It is the BROWSING KEY, not
// an agent id (ADR-072 FR-001): there is one manager, one Chrome and one
// profile directory per workspace, and N agents on that workspace share it, so
// the coordinator's Register/Release/RemoveAgent bookkeeping is keyed by the
// browser rather than by whichever agent happened to touch it first.
func (m *BrowserManager) AttachSharedChrome(coordinator *BrowserCoordinator, key BrowsingKey) {
	m.coordinator = coordinator
	m.key = key
	m.agentID = key.String()
}

// AttachPool wires this manager to the per-workspace browser pool (ADR-072
// FR-037) instead of to one coordinator directly. This is production's path.
//
// The difference that matters: with a pool, WHICH Chrome this manager drives
// is resolved on every ensureStarted rather than fixed at attach time. That is
// what lets the pool close an idle browser, or evict one under memory
// pressure, and have the next tool call quietly bring a fresh one up from the
// same profile directory — the agent sees a slower call, not an error.
func (m *BrowserManager) AttachPool(pool *BrowserPool, key BrowsingKey) {
	m.pool = pool
	m.key = key
	m.agentID = key.String()
}

// OperatorSessionID is the manager-level session id naming the WORKSPACE-OWNED
// tab set — the tabs the operator opened through the live panel, visible to
// every agent on this workspace (ADR-072 §0.2a).
//
// It is the exported seam the gateway addresses instead of the deleted
// shared session constant. Exported rather than exposing sessionKey because
// the gateway has no business minting an arbitrary (key, owner) pair: the live
// panel is the operator, and the operator's tabs are the only set it drives.
func (m *BrowserManager) OperatorSessionID() string {
	return sessionKey(m.key, TabOwnerWorkspace())
}

// BrowsingKey reports which browser this manager IS.
func (m *BrowserManager) BrowsingKey() BrowsingKey { return m.key }

// focusedTabSet reports which tab set a turn whose OWN set is `home` currently
// addresses — its own, or the operator's workspace-owned set it has taken over
// (ADR-072 D1.9b ruling 1, FR-070).
//
// Every browser tool resolves through this, on the ONE path resolveTurn takes,
// which is why "take over the operator's browsing" is a property of the turn
// rather than of eleven separate tools each having to remember it.
//
// The liveness check is load-bearing, not defensive. Without it a session that
// took over an operator tab set which was later reaped keeps pointing at a
// dead key, and the next call LAZILY RECREATES a workspace-owned set with a
// blank tab in it — an agent silently browsing in the operator's name, in a
// window the operator is not looking at.
func (m *BrowserManager) focusedTabSet(home TabOwner) TabOwner {
	if home.IsZero() {
		return home
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	focused, ok := m.tabFocus[home.String()]
	if !ok || focused.IsZero() || focused == home {
		return home
	}
	if _, live := m.sessions[sessionKey(m.key, focused)]; !live {
		delete(m.tabFocus, home.String())
		return home
	}
	return focused
}

// focusTabSet points `home`'s next call at `target`. Called by
// browser_switch_tab AFTER a successful switch — acquisition is by ACTING on
// the tab, so there is nothing to record until the action has happened
// (FR-070).
//
// Pointing a session back at its own set DELETES the entry rather than storing
// it, so the map holds only the sessions that have actually taken something
// over.
func (m *BrowserManager) focusTabSet(home, target TabOwner) {
	if home.IsZero() || target.IsZero() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if target == home {
		delete(m.tabFocus, home.String())
		return
	}
	if m.tabFocus == nil {
		m.tabFocus = make(map[string]TabOwner)
	}
	m.tabFocus[home.String()] = target
}

// writeLeases returns this browser's write-lease table (§14). Not guarded by
// m.mu — the table owns its own mutex, and the lock order is
// writeLease -> pool.mu -> m.mu, never the reverse.
func (m *BrowserManager) writeLeases() *writeLeaseTable { return &m.leases }

// leaseWait is the bound acquireWrite retries within. Reads the
// operator-configured, already-CLAMPED value (FR-023a) and falls back to the
// package default when unset.
func (m *BrowserManager) leaseWait() time.Duration {
	m.mu.Lock()
	d := m.cfg.LeaseWait
	m.mu.Unlock()
	if d <= 0 {
		return leaseWaitTimeout
	}
	return d
}

// InstallRoot returns the managed-Chromium install root this manager's
// config resolves to (see InstallRootForProfileDir) — the same directory
// installer.go's EnsureChromium/EnsureChromiumFullBuild install into and
// capability.go's ClassifyVideoCapability inspects. WebRTC build (W2-A):
// exposed so the gateway's WebRTC availability gate can classify video
// capability for this agent's managed Chrome without duplicating the
// ProfileDir-derived path arithmetic.
func (m *BrowserManager) InstallRoot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return InstallRootForProfileDir(m.cfg.ProfileDir)
}

// VideoCapability classifies this manager's live-view WebRTC video capability
// (ADR-047), honoring the operator's exec_path override: a full Chrome
// pinned via tools.browser.exec_path is video-capable even though no managed
// full-Chrome download exists under the install root (W3 e2e finding — the
// install-root-only check wrongly classified such hosts not_capable and
// permanently disabled WebRTC for them). See ClassifyVideoCapabilityWithExec.
//
// When cfg.ExecPath is unset — the common case, since most installs never
// set an explicit override — this also falls back to the already-RESOLVED
// exec path cached by execPathCaches (m.execPath.cachedPath(),
// exec_resolver.go). Rationale (download-vs-launch mismatch): exec_resolver's
// resolve() checks $PATH for a system google-chrome/chromium BEFORE falling
// back to the managed Chrome-for-Testing download. On a host with a system
// Chrome on $PATH, that system binary is what actually launches every real
// browser session, and the managed install root this method otherwise
// inspects is NEVER populated — so without this fallback,
// ClassifyVideoCapability would permanently misclassify a perfectly capable
// full-Chrome host as not_capable ("full-Chrome build not installed yet"),
// disabling WebRTC live-view video for good on that host.
//
// This reads m.execPath's cache field only — it never calls resolve() /
// resolveExecPath() itself. Those probe up to 4 PATH candidates (5s timeout
// each) and can fetch the Chrome-for-Testing manifest over the network, which
// is unacceptable on this method's call path (gateway request handling, see
// CaptureVideoCapability's callers in pkg/gateway/browser_webrtc.go) — it
// must stay a fast, non-blocking, no-network classification. If the cache is
// empty (nothing resolved yet), behavior is unchanged from before: falls
// through to the install-root-only check.
func (m *BrowserManager) VideoCapability() VideoCapability {
	m.mu.Lock()
	execPath := m.cfg.ExecPath
	profileDir := m.cfg.ProfileDir
	m.mu.Unlock()
	if execPath == "" {
		// m.execPath has its own mutex (see execPathCaches' doc comment) —
		// deliberately read after releasing m.mu above, mirroring every other
		// caller in this package that touches both locks (ADR-038 discipline:
		// never hold m.mu while touching execPath's lock, and vice versa).
		execPath = m.execPath.cachedPath()
	}
	return ClassifyVideoCapabilityWithExec(execPath, InstallRootForProfileDir(profileDir))
}

// AgentID returns the agent identifier this manager was attached to via
// AttachSharedChrome (empty in remote-CDP-override mode or before
// attachment). WebRTC build (W2-A): the capture session needs it for
// logging/audit context without reaching into an unexported field from
// outside this package.
func (m *BrowserManager) AgentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agentID
}

// Coordinator returns the shared-Chrome coordinator this manager is attached
// to (nil in remote-CDP-override mode or the no-coordinator test fallback —
// see AttachSharedChrome). WebRTC build (W2-A): the capture session needs it
// to load/verify the capture extension (BrowserCoordinator.LoadExtension)
// before creating the encoder page.
func (m *BrowserManager) Coordinator() *BrowserCoordinator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.coordinator
}

// viewerLivenessWindow is how long a viewer's last proof of life stays good.
// Past it the viewer is treated as gone even though nothing ever detached it.
//
// It is TWICE the live-panel WebSocket's 60-second read deadline
// (pkg/gateway/browser_ws.go, where the PongHandler both refreshes that
// deadline and stamps the heartbeat). The doubling is the whole safety
// argument and must not be tuned down to 1x:
//
// A person can watch a page for an hour without touching anything. Their
// browser still answers the server's ping with a pong every 30 seconds — the
// keep-alive is protocol-level and owes nothing to user interaction — so an
// idle-but-alive viewer keeps stamping. At 1x, ONE pong lost to a garbage
// collection pause, a scheduling hiccup or a half-second of packet loss would
// make a watching human look detached, and the pool would close the window
// they are looking at. At 2x, a viewer must miss four consecutive pings —
// four chances to speak — before anything reclaims its browser. And a truly
// dead socket is reclaimed within two minutes either way.
//
// The asymmetry is deliberate. Reaping a live viewer's browser out from under
// them is a worse failure than holding a phantom's browser two minutes longer
// than strictly necessary, so the window errs long.
const viewerLivenessWindow = 2 * 60 * time.Second

// liveViewersLocked reports how many of se's attached viewers still count as
// present at time now — se.viewers if the context's last proof of life is
// inside viewerLivenessWindow, and 0 if it is not.
//
// All-or-nothing per browsing context, by design: liveness is tracked for the
// context, not per viewer id, because every consumer of this asks a yes/no
// question ("may this Chrome be reclaimed?") and the answer is the same
// either way. Two viewers on one context where only one still breathes is
// still "somebody is watching" — the context must be pinned, which is exactly
// what returning the raw count does. The count is only ever wrong in the
// direction that keeps a browser alive.
//
// Must be called with m.mu HELD.
func (m *BrowserManager) liveViewersLocked(se *sessionEntry, now time.Time) int {
	if se == nil || se.viewers == 0 {
		return 0
	}
	if now.Sub(se.lastViewerBeat) > viewerLivenessWindow {
		return 0
	}
	return se.viewers
}

// Viewers reports how many live-panel viewers are attached across every
// browsing context this manager owns AND have proved they are still there
// within viewerLivenessWindow (ADR-072 FR-010, FR-052).
//
// It exists because BOTH lifetime controls need it and neither may guess: the
// pool refuses to evict a browser somebody is watching (FR-050) and refuses to
// idle-close one (FR-040). "Somebody is watching" is not something either can
// infer from tab activity — a person reading a page touches nothing for
// minutes at a time, and treating that as idle closes the window they are
// looking at.
//
// FR-052: this deliberately reports LIVE viewers, not the raw attach count. A
// viewer whose WebSocket cleanup never ran never decrements the count, and a
// raw count would let that phantom pin its workspace's Chrome permanently
// against both controls above. See sessionEntry.lastViewerBeat.
func (m *BrowserManager) Viewers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	n := 0
	for _, se := range m.sessions {
		n += m.liveViewersLocked(se, now)
	}
	return n
}

// InFlight reports how many browser_* tool calls are executing against this
// manager right now (ADR-072 FR-051).
//
// EVERY browser tool increments it — leased and lease-exempt alike — because
// the question eviction asks is "would killing this Chrome break a call that
// is currently running", and a read-only call breaks exactly as visibly as a
// write one. A screenshot that returns "connection lost" mid-turn is not less
// confusing for having been read-only.
//
// It is an int64 read under m.mu rather than an atomic, so that the pool's
// eviction selection and a call's own increment serialise: see
// BrowserPool.evictableLocked for why a call starting DURING selection must
// be either seen or landed on a relaunched instance, never lost between them.
func (m *BrowserManager) InFlight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inFlight
}

// EnterCall marks the start of a browser tool call and returns the function
// that marks its end. The caller defers the returned function, so a panicking
// or cancelled call still releases — an in-flight counter that leaks is a
// browser that can never be evicted or idle-closed, which is a deadlock
// rather than a leak.
func (m *BrowserManager) EnterCall() func() {
	m.mu.Lock()
	m.inFlight++
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if m.inFlight > 0 {
				m.inFlight--
			}
			m.mu.Unlock()
		})
	}
}

// TotalOpenTabs reports how many tabs are open across every browsing context
// this manager owns. It is NOT a budget and nothing compares it to a cap —
// every tab counter was deleted by ADR-072 D1.5a. It answers one question:
// has this browser got anything left in it (FR-040's idle close).
func (m *BrowserManager) TotalOpenTabs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTabCountLocked()
}

// CaptureSession returns this manager's active WebRTC CaptureSession, or nil
// if none has been created yet (ADR-047 D2, wave-plan W2-A). Guarded by its
// own mutex (m.captureMu), deliberately separate from m.mu, because
// CaptureSession's own lifecycle methods call back into m.Session()/
// m.createTab() etc., which take m.mu themselves — holding m.mu across that
// would deadlock.
func (m *BrowserManager) CaptureSession() *CaptureSession {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	return m.capture
}

// EnsureCaptureSession returns this manager's existing CaptureSession, or
// lazily constructs one via newFn if none exists yet. newFn is called at
// most once per manager (subsequent viewers reuse the same session, "one
// active stream per agent" — wave-plan W2-A item 4); it receives no
// arguments because every dependency a production CaptureSession needs
// (this manager, its agent id, WebRTC config, the input sink) is already
// known to the caller's closure. newFn's error (NewCaptureSession's own
// crypto/rand.Read failure — effectively never happens, but MUST NOT be
// silently swallowed) is propagated to the caller rather than caching a nil
// session, so a transient failure doesn't wedge this manager into always
// returning nil for the rest of the process's life.
func (m *BrowserManager) EnsureCaptureSession(newFn func() (*CaptureSession, error)) (*CaptureSession, error) {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	if m.capture != nil {
		return m.capture, nil
	}
	cs, err := newFn()
	if err != nil {
		return nil, err
	}
	m.capture = cs
	return cs, nil
}

// ClearCaptureSession drops this manager's CaptureSession reference,
// provided it still IS cur (guards against a stale caller clearing a session
// that was already replaced by a newer one — mirrors the "only touch the
// entry if it still points at what I expect" discipline used throughout this
// package, e.g. removeViewer in the webrtc package). Called once a
// CaptureSession has fully stopped (grace-timer fire, browser death, or
// manager Shutdown) so the NEXT viewer offer creates a fresh session rather
// than reusing torn-down state.
func (m *BrowserManager) ClearCaptureSession(cur *CaptureSession) {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	if m.capture == cur {
		m.capture = nil
	}
}

// errFileSchemeBlocked is the file:// refusal. It names the tool that DOES
// work — serve_web, the actual registered name; there is no tool called
// "web_serve" and pointing an agent at one would waste a whole turn — and the
// URL shape it produces, so the agent's next move is obvious instead of being
// a guess.
var errFileSchemeBlocked = errors.New(
	"file:// URLs are blocked: the browser cannot read the agent's filesystem. " +
		"To look at a local file or a site you have built, serve it with the serve_web tool and " +
		"navigate to the /preview/<agent>/<token>/ URL it returns (requires the serve_web tool and " +
		"gateway.preview_enabled)")

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
		// file:// gets its own message. The other four blocked schemes are
		// things an agent tried and should stop trying; file:// is almost
		// always an agent trying to LOOK AT SOMETHING IT JUST BUILT, and
		// "blocked for security reasons" leaves it with nowhere to go. There
		// is somewhere to go, so the error says where.
		if scheme == "file" {
			return fmt.Errorf("browser: %w", errFileSchemeBlocked)
		}
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
	// gateway case), ask it to launch+provide this KEY's Chrome instead of
	// building a per-manager ExecAllocator. The coordinator owns the Chrome
	// process; the manager drives it through chromedp CHILD contexts of the
	// coordinator's rootCtx (CRIT-001 — no RemoteAllocator dial anymore, the
	// CDP pipe has no ws:// URL and is private to this OS process; see
	// coordinator.go's Register doc comment).
	//
	// Register blocks on the (possibly cold) Chrome launch, so m.mu is released
	// around it — same ADR-038 no-lock-across-blocking-call discipline as the
	// resolveExecPath unlock/relock below. A concurrent ensureStarted that won
	// while m.mu was released is handled by the post-relock m.started check.
	if m.pool != nil || m.coordinator != nil {
		agentID := m.agentID
		pool := m.pool
		coord := m.coordinator
		key := m.key
		m.mu.Unlock()
		var (
			rootCtx context.Context
			regErr  error
		)
		if pool != nil {
			coord, rootCtx, regErr = pool.Register(context.Background(), key, m)
		} else {
			rootCtx, regErr = coord.Register(context.Background(), agentID, m)
		}
		m.mu.Lock()
		if regErr != nil {
			return fmt.Errorf("browser: shared Chrome unavailable: %w", regErr)
		}
		if m.started {
			// A concurrent ensureStarted won while m.mu was released. Discard
			// our redundant resolution; the winner already set m.allocCtx.
			return nil
		}
		m.allocCtx = rootCtx
		// No manager-local connection to tear down anymore (CRIT-001): the
		// coordinator owns the pipe's whole lifecycle (Shutdown / crash-
		// relaunch), never this manager. allocCancel stays a field other code
		// paths (Shutdown/dropConnection/invalidateConnection) call
		// unconditionally, so it must be non-nil, not omitted.
		m.allocCancel = func() {}
		// Refresh the cached coordinator: under a pool this may be a DIFFERENT
		// coordinator than last time (idle close, eviction, crash recovery),
		// and capture_session.go reaches Chrome through m.Coordinator().
		m.coordinator = coord
		m.started = true
		logger.InfoCF("browser", "Browser connected to this workspace's Chrome", map[string]any{
			"agent_id": agentID,
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

	// Render the Chrome command line via the shared helper (managedExecAllocatorOpts,
	// exec_resolver.go) — identical to the coordinator's launch path, so the two
	// never diverge (MAJ-001). See that helper for the per-flag rationale
	// (hardening set, sandbox disable, stealth flags, XDG/HOME jail, etc.).
	//
	// CRIT-001: launch over the CDP pipe (cdppipe — no TCP debug port; see
	// coordinator.go's file doc). The launcher is a seam (m.pipeLauncherFn)
	// so tests never spawn real Chrome — mirrors the coordinator's
	// pipeLauncher field exactly.
	cmdline := managedExecAllocatorOpts(m.cfg, chromeMajorVersion(context.Background(), execPath))
	launch := m.pipeLauncherFn
	if launch == nil {
		launch = launchManagedPipe
	}
	res, err := launch(context.Background(), execPath, pipeLaunchConfig{
		args:        cmdline.Args,
		env:         cmdline.Env,
		userDataDir: m.cfg.ProfileDir,
	})
	if err != nil {
		return fmt.Errorf("browser: failed to launch managed Chrome over the CDP pipe: %w", err)
	}
	m.allocCtx = res.rootCtx
	m.allocCancel = res.cancel
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

// managedChromiumProbeTimeout bounds the MANAGED binary's `--version` probe,
// which is a fundamentally different situation from a PATH candidate and
// needs a far longer budget (macOS, measured 2026-08-13).
//
// The short chromiumProbeTimeout above is sized for "is this one of four
// unknown PATH candidates a real browser, or a hung stub?" — where being
// wrong is cheap and the loop's total cost is what matters. The managed
// binary is the opposite: exactly ONE candidate, one we downloaded and
// extracted ourselves, with NO fallback after it. A slow answer there means
// "still starting", not "wrong candidate".
//
// And on macOS the FIRST execution of a freshly-downloaded ~200MB app bundle
// is genuinely slow: Gatekeeper verifies the bundle's code signature before
// letting it run, and that verification is cached only afterwards. Observed
// on a 4-core Intel MacBook Pro: the first `--version` on a just-extracted
// Chrome for Testing exceeded 5s and the probe declared the install corrupt
// with "remove and retry" — while the very same binary answered in under a
// second immediately after, verification now cached. A fresh macOS install
// could therefore fail its first browser call with a wrong, alarming
// diagnosis and no way for the operator to tell it was a false alarm.
const managedChromiumProbeTimeout = 90 * time.Second

// probeChromiumBinary reports whether path is a real, runnable
// Chromium/Chrome binary by actually executing it (`--version`), not merely
// checking that it exists and is executable — which exec.LookPath, the only
// check the pre-fix resolveExecPath performed, already confirmed. LookPath
// alone is insufficient: a distro package-manager stub can be a perfectly
// valid, executable file on $PATH that nonetheless fails the moment it
// actually runs (see resolveExecPath's doc comment for the motivating
// Ubuntu snap-redirector case). Bounded by chromiumProbeTimeout so a
// hanging candidate cannot stall resolution indefinitely.
//
// MEDIUM fix: returns a human-readable reason alongside ok (empty when
// ok==true) so callers can log/report WHY the probe failed instead of one
// generic line for every cause. Permission-denied (a real ACL/sandbox
// misconfiguration), a broken binary that runs but exits non-zero (the
// snap-stub case this function exists to catch), and a timed-out/canceled
// probe (the process may be hung, or resolution itself was canceled) are
// different operational problems with different fixes — collapsing them
// into one message hides which one actually applies.
func probeChromiumBinary(ctx context.Context, path string) (ok bool, reason string) {
	return probeChromiumBinaryWithTimeout(ctx, path, chromiumProbeTimeout)
}

// probeChromiumBinaryWithTimeout is probeChromiumBinary with an explicit
// budget — see managedChromiumProbeTimeout for why the managed binary needs
// a different one from a PATH candidate.
func probeChromiumBinaryWithTimeout(
	ctx context.Context,
	path string,
	timeout time.Duration,
) (ok bool, reason string) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := exec.CommandContext(probeCtx, path, "--version").Run()
	if err == nil {
		return true, ""
	}
	switch {
	case errors.Is(probeCtx.Err(), context.DeadlineExceeded):
		return false, fmt.Sprintf("probe timed out after %s (binary may be hung)", timeout)
	case errors.Is(probeCtx.Err(), context.Canceled):
		return false, "probe was canceled"
	case errors.Is(err, os.ErrPermission):
		return false, fmt.Sprintf("permission denied executing %s", path)
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, fmt.Sprintf("ran but exited with an error (%s) — binary present but broken", exitErr.Error())
		}
		return false, err.Error()
	}
}

// Session returns the ACTIVE tab's context for the given browsing context
// (ADR-041 D1). If the browsing context does not exist, a new one is created
// with a single tab (subject to the memory gate). The session key is
// sessionKey(BrowsingKey, TabOwner) — never a shared constant. Used by
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
// no error or log line while it waits. The fix: only the memory check and
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
	// Cancels collected under m.mu and run after it is dropped (see the
	// crash-recovery branch below). Declared out here so the retry loop reuses
	// one slice rather than allocating per iteration.
	var pendingCancels []func()
	for {
		m.mu.Lock()
		if err := m.ensureStarted(); err != nil {
			m.mu.Unlock()
			return nil, err
		}

		if se, ok := m.sessions[sessionID]; ok {
			if tab := se.active(); tab != nil && tab.ctx.Err() == nil {
				// An agent tool call resolving this session is activity ON
				// THIS SPECIFIC TAB — it keeps ReapIdleSessions (which judges
				// each tab in a browsing context independently) from closing
				// the tab an agent is actively working in, even with no
				// viewer attached. Every browser_* tool funnels through here,
				// so this single call site covers every "agent tool call
				// resolves/uses this tab" path.
				m.touchTabLocked(tab)
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
			// Collected, NOT canceled, while m.mu is held — see cancelBounded.
			// This is the hottest path in the file (every browser_* tool call
			// resolves through Session()) AND it fires precisely when a tab's
			// context has already died, which is the condition most likely to
			// wedge a chromedp cancel. Canceling here under the lock would
			// freeze every browser tool call for every agent on this manager,
			// with no error and no log — a harder failure than the reaper's,
			// because nothing would even warn.
			for _, t := range se.tabs {
				pendingCancels = append(pendingCancels, t.cancel)
			}
			if se.browserCancel != nil {
				pendingCancels = append(pendingCancels, se.browserCancel)
			}
			delete(m.sessions, sessionID)
		}
		m.mu.Unlock()

		for _, cancel := range pendingCancels {
			cancelBounded(cancel, map[string]any{"session_id": sessionID, "origin": "session_crash_recovery"})
		}
		pendingCancels = nil

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

		if m.memoryRefusesTabOpenLocked() {
			m.mu.Unlock()
			return errMemoryPressureTabOpen
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
		// newActiveCtx is the context of the tab this call actually installed
		// as index 0/active, captured under the SAME lock that installed it.
		// Left nil in the "someone else already populated this session"
		// branch below, where this call's tab is discarded and the active tab
		// belongs to (and was activated by) whoever won that race.
		var newActiveCtx context.Context
		if existing == nil {
			tabs = m.registerFreshSessionLocked(sessionID, tab, browserCtx, browserCancel)
			newActiveCtx = tab.ctx
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
				m.syncDialogListenersLocked(sessionID, se)
				tabs = snapshotTabsLocked(se)
				newActiveCtx = tab.ctx
			}
		}
		m.mu.Unlock()
		close(done)
		if notify {
			// Tell Chrome which tab is active, BEFORE notifyTabsChanged fires
			// the WebRTC recapture — the last of the five paths that moved
			// se.activeIdx without stating its intent to Chrome (SwitchTab,
			// OpenTab, CloseTab and adoptTarget are the other four). This one
			// matters most on CloseTab's last-tab replacement: the tab the
			// user was watching has just been destroyed, so Chrome's own
			// active-tab answer at that instant is whatever it fell back to,
			// and the encoder would bind to that instead of the replacement
			// this call just created. Best-effort and no lock held, like every
			// other call site.
			m.activateTabInChrome(newActiveCtx, sessionID, 0)
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
	m.syncDialogListenersLocked(sessionID, se)
	m.sessions[sessionID] = se
	return snapshotTabsLocked(se)
}

// firstAttachTimeout bounds runFirstAttach's wait for the very first
// chromedp.Run on a freshly created chromedp context — used by both
// bootstrapBrowserCtx (a session's initial browser-owning context) and
// createTab (every subsequent/adopted tab). Both sit on the cold-start
// critical path a slow attach can push past the browser WS handler's 60s
// read deadline (createFirstTab → bootstrapBrowserCtx/createTab for an
// agent's default tab; capture_session.go's defaultEncoderStarter →
// createTab for the WebRTC encoder page).
//
// By the time either call runs, Chrome itself is ALREADY launched and
// dialed — the coordinator's ensureLaunched (coordinator.go) / this
// manager's own managed-mode ensureStarted already completed the actual
// process spawn + CDP-pipe handshake, bounded by cdppipe's own
// defaultDialTimeout (20s). What's left here is only CDP target
// creation/adoption + attach (AttachToTarget plus a handful of Enable()
// round trips) against a browser that is already up and responding, which
// should be fast. 20s matches that same dial-timeout scale (and
// capture_session.go's sibling captureStartTimeout, also 20s, which bounds
// the very next step in the encoder-page flow) — generous enough to absorb
// a slow/loaded host without falsely failing a legitimately slow-but-working
// attach, while keeping the total cold-start budget comfortably under the
// 60s WS deadline this bug feeds.
var firstAttachTimeout = 20 * time.Second

// runFirstAttach races fn — expected to be exactly one first chromedp.Run(ctx)
// call on a freshly created chromedp context — against timeout, WITHOUT
// deriving a timed-out child of ctx to pass into Run itself. That distinction
// is load-bearing: chromedp.Run's own doc comment warns "it's generally a bad
// idea to use a context timeout on the first Run call, as it will stop the
// entire browser" — and here specifically, chromedp's attachTarget spawns
// `go c.Target.run(ctx)`, a background goroutine that reads CDP events for
// the tab's ENTIRE remaining lifetime using the exact ctx object handed to
// Run (chromedp.go/target.go, chromedp v0.15.1). Wrapping that ctx itself in
// context.WithTimeout would silently kill that goroutine — and therefore the
// tab's own CDP event processing — the instant the bound elapsed, corrupting
// an otherwise perfectly healthy, already-attached tab, not merely bounding
// how long we wait for the attach to finish.
//
// Racing fn on its own goroutine avoids that: ctx (the object chromedp.
// NewContext returned, whose cancel is the tab's own lifetime) is passed to
// Run completely unmodified. On timeout we do NOT touch ctx here — the
// caller cancels it (via the same cancel() it already calls on any other
// attach error), which unblocks fn's in-flight CDP calls via ctx.Done() and
// lets its goroutine exit on its own; fn's result (buffered, capacity 1) is
// then discarded. This mirrors the exact call-then-cancel-on-error shape
// createTab/bootstrapBrowserCtx already had — only the wait itself is now
// bounded.
func runFirstAttach(fn func() error, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf(
			"browser: timed out after %s waiting for the browser to attach the tab (target may be unresponsive)",
			timeout,
		)
	}
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
	// ADR-072 FR-031: no CDP browser context is adopted or created here,
	// because there are none. Every session bootstraps into Chrome's DEFAULT
	// context — the only one chrome.tabCapture can reach — and isolation is
	// the workspace's own Chrome process and profile directory (FR-037), not
	// a context id.
	ctx, cancel := chromedp.NewContext(allocCtx)
	if err := runFirstAttach(func() error { return chromedp.Run(ctx) }, firstAttachTimeout); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("browser: failed to launch browser: %w", err)
	}
	return ctx, cancel, nil
}

// errMemoryPressureTabOpen is the shared refusal every tab-open site returns
// when the FR-060 memory gate says stop. It replaces the deleted cap error, which
// named a cap (tools.browser.max_tabs) that ADR-072 D1.5a DELETED.
//
// It names MEMORY and a remedy that exists, and it names NO limit and NO config
// key — deliberately (FR-053, FR-063). An operator told to raise a limit would
// go looking for a setting this build does not have, and a model told the same
// would report a fixable configuration problem where the real answer is "close
// a tab, or free memory on this machine".
var errMemoryPressureTabOpen = errors.New(
	"this machine is low on memory, so no further browser tab can be opened right now. " +
		"Close a tab with browser_close_tab, or wait for memory to free up, and retry")

// memoryRefusesTabOpenLocked is the FR-060 tab-open gate. It occupies the exact
// five sites the deleted per-agent tab cap used to occupy (createFirstTab, OpenTab x2,
// adoptTarget x2) — vacated and re-occupied in ONE change, never left empty
// across a commit boundary, because an unguarded tab-open path is a runaway
// window.open loop with nothing between it and the OOM killer.
//
// It is a RATIO and carries no per-tab byte constant: the whole point of D1.5a
// is that a tab's cost is not a constant anybody can name, so the gate asks the
// one shared question (config.MemoryPressureHigh) against the one shared
// threshold rather than pricing a tab.
//
// The unmeasurable host is the interesting case (FR-065, FR-082). It is treated
// as FULL rather than empty, but only PAST A FLOOR: the FIRST tab in this
// browser opens, the second is refused. A floor of zero would remove browsing
// entirely from gVisor and GKE Sandbox — /proc-less Linux deployments this
// project SUPPORTS — on the strength of a reading the host declines to give.
//
// Must be called with m.mu held: it reads totalTabCountLocked.
func (m *BrowserManager) memoryRefusesTabOpenLocked() bool {
	open := m.totalTabCountLocked()
	ask := m.memoryPressureFn
	if ask == nil {
		ask = func(int) (bool, bool) { return config.MemoryPressureHigh() }
	}
	high, ok := ask(open)
	if !ok {
		return open >= 1
	}
	return high
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
// that first Run's context). The wait for that first Run is still bounded —
// via runFirstAttach(firstAttachTimeout), which races the call on its own
// goroutine and cancels ctx on timeout instead of deriving a timed-out child
// of ctx to hand to Run itself; see runFirstAttach's doc comment for exactly
// why that distinction matters.
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

	if err := runFirstAttach(func() error { return chromedp.Run(ctx) }, firstAttachTimeout); err != nil {
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

	// Land a BRAND-NEW tab on the start page. chromedp.NewContext opens a bare
	// about:blank, which reads as a broken panel on this surface (see
	// StartPageURL). Only for genuinely new tabs: when targetID is set we are
	// ADOPTING an existing target (a popup, a window.open) that already has its
	// own destination, and navigating it away would destroy the page the user
	// or agent actually opened.
	//
	// Best-effort and never fatal — a tab that failed to reach the start page
	// is still a perfectly usable tab, and failing creation over a cosmetic
	// landing page would be a far worse trade.
	m.navigateNewTabToStartPage(ctx, targetID)

	// Stamp lastActivity at the moment of creation ("on tab creation" is one
	// of the required touch points — see tabEntry.lastActivity's doc
	// comment): a brand-new tab must never read as already-idle to
	// ReapIdleSessions just because nothing has explicitly touched it yet.
	// m.now() is read under a brief m.mu acquisition, matching every other
	// call site in this file that reads m.nowFn (ADR-038 discipline: this
	// function itself runs with NO BrowserManager lock held).
	m.mu.Lock()
	createdAt := m.now()
	m.mu.Unlock()

	tab := &tabEntry{ctx: ctx, cancel: cancel, targetID: resolvedID, lastActivity: createdAt}
	tab.title, tab.url = refreshTabMeta(ctx, m.PageTimeout())
	return tab, nil
}

// navigateNewTabToStartPage lands a BRAND-NEW tab on the configured start page.
//
// chromedp.NewContext opens a bare about:blank, and on this surface a blank
// rectangle is indistinguishable from a broken panel — a real capture failure
// renders identically (operator report, 2026-08-03). See StartPageURL.
//
// Only for genuinely new tabs: a non-empty targetID means we are ADOPTING an
// existing target (a popup, a window.open) that already has its own
// destination, and navigating it away would destroy the page the user or the
// agent actually opened.
//
// Best-effort, never fatal — a tab that failed to reach the start page is still
// a perfectly usable tab, and failing tab creation over a cosmetic landing page
// would be a far worse trade.
func (m *BrowserManager) navigateNewTabToStartPage(ctx context.Context, targetID target.ID) {
	if targetID != "" {
		return
	}
	start := m.StartPageURL()
	if start == BlankPageURL {
		return
	}

	m.mu.Lock()
	fn := m.navigateFn
	m.mu.Unlock()

	var err error
	if fn != nil {
		err = fn(ctx, start) // test seam — see navigateFn's doc comment
	} else {
		navCtx, navCancel := context.WithTimeout(ctx, m.PageTimeout())
		err = chromedp.Run(navCtx, chromedp.Navigate(start))
		navCancel()
	}
	if err != nil {
		logger.WarnCF("browser", "new tab: navigate to start page failed (tab still usable, showing about:blank)",
			map[string]any{"error": err.Error(), "start_page": start})
	}
}

// refreshTabMeta best-effort reads the current title/url of tabCtx, bounded
// by timeout so a slow/hung page never blocks tab creation or adoption.
// Failures are silent (empty strings) — title/url are cosmetic (tab-strip
// display, browser_list_tabs), never required for tool correctness.
func refreshTabMeta(tabCtx context.Context, timeout time.Duration) (title, url string) {
	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()
	_ = chromedp.Run(
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = chromedp.Title(&title).Do(ctx)
			_ = chromedp.Location(&url).Do(ctx)
			return nil
		}),
	)
	return title, url
}

// totalTabCountLocked sums the number of tabs across every browsing context
// this manager tracks. It SURVIVED ADR-072 D1.5a's counter deletion as a
// COUNT — nothing compares it against a cap any more; the FR-082 floor on an
// unmeasurable host is its one remaining reader, plus OpenTabCount's reporting
// (ADR-041 generalized its pre-ADR-041 meaning of "total
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

// TabState is the CLOSED three-value answer to "what is there to see in this
// tab set?" (ADR-072 FR-013). It exists because `ListTabs` used to return the
// identical `nil, 0, nil` for two genuinely different situations, and the tool
// on top of it told the model "no tabs" in a case where the truthful answer was
// "there is no browser here at all". §1.1 of the ADR records what that
// ambiguity cost.
//
// Three members, and DELIBERATELY no fourth. In particular there is NO
// "denied" member: ADR D1.12 withdrew it as unreachable, because
// FilterToolsByPolicy (pkg/tools/compositor.go) `continue`s past a deny
// verdict, so a policy-denied agent is never shown browser_list_tabs, never
// calls it, and answers from the tool's ABSENCE. A state nothing can ever
// return is not a state — it is a lie with a name.
type TabState string

const (
	// TabStateNoContext — this browser has no tab set for this owner at all.
	// No tool has ever browsed here. NOT "zero tabs": there is nothing to
	// have tabs.
	TabStateNoContext TabState = "no_context"
	// TabStateOpen — a live tab set with at least one tab in it.
	TabStateOpen TabState = "open"
	// TabStateEmpty — a live tab set that currently holds no tabs. Reachable
	// in production through CloseTab's last-tab path (it empties se.tabs and
	// then calls createFirstTab to restore the never-zero invariant; a failed
	// replacement leaves the entry live and empty until the reaper takes it).
	TabStateEmpty TabState = "empty"
)

// ListTabsState returns which of the three TabStates sessionID is in, together
// with a snapshot of its tab set and which index is active.
//
// sessionID is the MANAGER-LEVEL session key — sessionKey(BrowsingKey,
// TabOwner) — exactly as every sibling method on this type takes it
// (Session/SwitchTab/CloseTab/OpenTab). It is deliberately NOT addressed by
// BrowsingKey alone: one key names one browser, and a browser holds one tab set
// per session that has browsed plus the operator's workspace-owned one, so a
// key on its own cannot say whose tabs are being asked for (FR-080).
//
// The error is reserved for a genuine failure. "Nothing here" is a STATE, not
// an error and not an empty success.
func (m *BrowserManager) ListTabsState(sessionID string) (TabState, []Tab, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok {
		return TabStateNoContext, nil, 0, nil
	}
	tabs := snapshotTabsLocked(se)
	if len(tabs) == 0 {
		return TabStateEmpty, tabs, se.activeIdx, nil
	}
	return TabStateOpen, tabs, se.activeIdx, nil
}

// ListTabs returns a snapshot of sessionID's tab set and which index is active.
//
// It DELEGATES to ListTabsState and drops the state. That is the whole point of
// the split: the (tabs, activeIdx, err) triple cannot distinguish "no browsing
// context here" from "a context with no tabs" — it returned the same empty
// answer for both, with no error, for as long as this method has existed
// (FR-013). Callers that need to tell the two apart MUST call ListTabsState;
// this signature is retained for the callers that genuinely only want the tabs.
func (m *BrowserManager) ListTabs(sessionID string) (tabs []Tab, activeIdx int, err error) {
	_, tabs, activeIdx, err = m.ListTabsState(sessionID)
	return tabs, activeIdx, err
}

// SwitchTab makes tab `index` the active tab of sessionID's browsing
// context (ADR-041 D3). Subsequent tool calls (via Session) and the live
// screencast (via the ADR-041 D4 tabs-changed callback) follow it.
//
// activateTabInChrome is what makes the switch visible to CHROME, not just to
// this manager's own se.activeIdx bookkeeping — see its doc comment. Without
// it the WebRTC capture path silently keeps streaming the PREVIOUS tab
// (live-measured 2026-08-03; the three-way desync where the tab strip said one
// tab, the URL bar said another, and the pixels showed a third).
func (m *BrowserManager) SwitchTab(sessionID string, index int) (Tab, error) {
	m.mu.Lock()
	se, err := m.lookupTabLocked(sessionID, index)
	if err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	// The tab being left. Captured BEFORE activeIdx moves, under the same
	// lock, so its focus emulation can be cleared below (review finding F9 —
	// see releaseTabFocusInChrome for the measured background-compositing
	// cost of leaving it set).
	var prevCtx context.Context
	// modelMoved records whether THIS manager's own bookkeeping actually
	// changed, captured BEFORE the mutation below — it is what decides
	// whether anyone downstream will fire a recapture (see the call to
	// recaptureForTabChange at the end of this function).
	modelMoved := se.activeIdx != index
	if modelMoved && se.activeIdx >= 0 && se.activeIdx < len(se.tabs) {
		prevCtx = se.tabs[se.activeIdx].ctx
	}
	se.activeIdx = index
	// Switching TO a tab is activity on it — a human/agent flipping to a tab
	// via browser_switch_tab is unambiguously "using" it, so it must not read
	// as idle to ReapIdleSessions the instant this call returns.
	m.touchTabLocked(se.tabs[index])
	tabs := snapshotTabsLocked(se)
	// Capture the newly-active tab's context under the SAME lock that just
	// moved activeIdx, so the BringToFront below targets exactly the tab this
	// call activated even if a concurrent switch/close lands right after the
	// unlock (it would then run its own activation for its own tab).
	tabCtx := se.tabs[index].ctx
	m.mu.Unlock()

	// Before notifyTabsChanged: the tabs-changed callback triggers the WebRTC
	// recapture, whose encoder resolves its capture target via
	// chrome.tabs.query({active:true}) — so Chrome must already agree about
	// which tab is active by the time that fires, or the recapture re-binds to
	// the old tab and the stream never moves.
	m.activateTabInChrome(tabCtx, sessionID, index)
	// After, not before: the new tab takes over the foreground first, so
	// there is never a moment with no focused tab.
	m.releaseTabFocusInChrome(prevCtx, sessionID)

	m.notifyTabsChanged(sessionID, tabs, index)

	// The model did NOT move, so nobody downstream will ask for a recapture
	// and the picture would stay on whatever tab Chrome was showing — the
	// live-measured 2026-08-15 defect. Mechanism: LiveView.onTabsChanged
	// (live.go) triggers the WebRTC recapture only when the ACTIVE TAB
	// CHANGED (its activeTabChanged check, which compares the resolved
	// active-tab context against the last one it saw). That is correct for
	// its own purposes but assumes this manager's model and Chrome's own
	// idea of the active tab never disagree. They do disagree, routinely:
	// when a page-opened tab fails to be adopted (measured on the operator's
	// box — "auto-attach: failed to adopt new tab target ... timed out after
	// 20s", three times, from an advert), Chrome activates that tab and this
	// manager never learns about it. The user then clicks the tab strip
	// entry that is ALREADY the model's active index to get back: the
	// activateTabInChrome call above genuinely corrects Chrome, the call
	// returns success — and the video never follows, because no recapture
	// was ever requested. Reproduced deterministically by forcing a
	// recapture with no model change: the picture snapped straight back.
	//
	// Guarded on !modelMoved precisely so the normal path does not fire
	// twice: when the model DID move, onTabsChanged's own activeTabChanged
	// branch has already issued exactly one recapture from the
	// notifyTabsChanged call immediately above.
	//
	// Round-2 finding F3: that branch now goes through the SAME entry point
	// this one does (CaptureSession.RecaptureForTabChangeAt, via
	// LiveView.signalRecaptureForTabChange), so the independent foreground
	// re-assert — the second attempt that exists because activateTabInChrome
	// above is best-effort and its failure is a WARN log and nothing more —
	// is on the path every ordinary tab click takes, not only on this rare
	// recovery one. It used to be reachable ONLY from here, which had the
	// hardening exactly backwards.
	if !modelMoved {
		m.recaptureForTabChange()
	}
	return tabs[index], nil
}

// recaptureForTabChange asks this manager's WebRTC CaptureSession (if any)
// to re-bind its capture to the current model-active tab. A no-op when no
// capture session exists (WebRTC never used, or the panel is closed), which
// is why every call site can invoke it unconditionally.
//
// Must be called with NO BrowserManager lock held: CaptureSession() takes
// m.captureMu, and the work RecaptureForTabChange schedules calls back into
// m.Session(), which takes m.mu.
func (m *BrowserManager) recaptureForTabChange() {
	if cs := m.CaptureSession(); cs != nil {
		cs.RecaptureForTabChange()
	}
}

// activateTabInChrome makes tabCtx's tab the one Chrome itself considers
// active, via CDP Page.bringToFront.
//
// Why this is load-bearing (root-caused live on UAT, 2026-08-03): switching
// tabs used to update ONLY this manager's se.activeIdx. At the time this was
// root-caused, the (since-removed, ADR-061) JPEG screencast path happened to
// survive that because it called page.BringToFront() itself before every
// StartScreencast, but the WebRTC path does not: its encoder picks a capture
// target with chrome.tabs.query({active: true, lastFocusedWindow: true})
// (captureext/embedded/encoder.js findActiveTargetTab). With Chrome never told
// about the switch, that query kept returning the OLD tab, so every recapture
// re-bound chrome.tabCapture to the tab the user had just switched AWAY from —
// a completely silent failure (track stayed live, zero console errors, only a
// stalled-RTP watchdog warning downstream).
//
// Best-effort by design: a failure here is logged, never fatal. The switch has
// already been recorded in se.activeIdx, so every server-side consumer
// (Session(), tool calls) still follows the new tab correctly; only the
// WebRTC capture's own tab resolution degrades to its previous behavior.
// Runs with NO BrowserManager lock held — the same ADR-038 rule every other
// CDP call in this file follows.
func (m *BrowserManager) activateTabInChrome(tabCtx context.Context, sessionID string, index int) {
	if err := m.runTabFocusCDP(tabCtx, foregroundTabActions()...); err != nil {
		logger.WarnCF(
			"browser",
			"switch tab: bring new active tab to front failed (WebRTC capture may keep streaming the previous tab)",
			map[string]any{"error": err.Error(), "session_id": sessionID, "index": index},
		)
	}
}

// releaseTabFocusInChrome is activateTabInChrome's counterpart for the tab
// being left behind: it clears the focus emulation that made that tab render
// as if it were foreground.
//
// Why (review finding F9, 2026-08-13): focus emulation is sticky per target.
// Without this, every tab the agent ever visited stays convinced it is
// foreground forever. Measured on this project's own Chrome (headless,
// 4 paired trials, rAF ticks under a full-viewport animation): a tab the user
// had switched AWAY from kept rendering at 25–35 fps while still emulated, and
// dropped to 0 fps the moment the emulation was cleared. That is pure waste —
// nothing captures or displays a background tab — and it scales with every tab
// the agent opens.
//
// Best-effort and non-fatal, exactly like activateTabInChrome: failing to
// un-emulate a tab costs CPU, never correctness.
func (m *BrowserManager) releaseTabFocusInChrome(tabCtx context.Context, sessionID string) {
	if err := m.runTabFocusCDP(tabCtx, backgroundTabActions()...); err != nil {
		logger.WarnCF(
			"browser",
			"switch tab: could not clear focus emulation on the previous tab (it will keep compositing in the background)",
			map[string]any{"error": err.Error(), "session_id": sessionID},
		)
	}
}

// foregroundTabActions is THE treatment a tab gets when it becomes the one
// Chrome should be compositing for: told to come to front, AND told to render
// as focused.
//
// Both halves, always, on every path (review finding F9, 2026-08-13). Focus
// emulation used to be applied ONLY by the capture-start path
// (CaptureSession.bringAgentTabToFront), while the tab-switch path did
// Page.bringToFront alone — so one browser_switch_tab silently downgraded the
// captured tab to a different rendering regime than the one capture start had
// established. Splitting a treatment across two call sites is how that
// happened; keeping the sequence in one place is what stops it recurring.
//
// Honest scope of the second half: bringToFront alone was NOT measurably
// slower here (headless, brought-to-front tab, 6/6 trials at 60 rAF/s with and
// without emulation), so this is not a claimed framerate win on the
// switched-TO tab — it is identical treatment on every path, which is what
// makes the tab the encoder re-binds to indistinguishable from the tab capture
// started on. The measured win is on the other side: see
// releaseTabFocusInChrome.
func foregroundTabActions() []chromedp.Action {
	return []chromedp.Action{
		page.BringToFront(),
		emulation.SetFocusEmulationEnabled(true),
	}
}

// backgroundTabActions is the exact inverse of foregroundTabActions' focus
// half — see releaseTabFocusInChrome. There is deliberately no
// "send to back" counterpart to Page.bringToFront: Chrome has no such call,
// and bringing the NEW tab to front is what backgrounds the old one.
func backgroundTabActions() []chromedp.Action {
	return []chromedp.Action{
		emulation.SetFocusEmulationEnabled(false),
	}
}

// runTabFocusCDP executes one tab-focus round trip against tabCtx, bounded by
// PageTimeout, with NO BrowserManager lock held (the ADR-038 rule every CDP
// call in this file follows). A dead or nil tab context is skipped rather than
// dispatched: in production that is a guaranteed PageTimeout stall for a tab
// that cannot be focused anyway.
func (m *BrowserManager) runTabFocusCDP(tabCtx context.Context, actions ...chromedp.Action) error {
	if tabCtx == nil || tabCtx.Err() != nil {
		return nil
	}
	m.mu.Lock()
	fn := m.tabFocusFn
	m.mu.Unlock()
	if fn != nil {
		// Test seam — see tabFocusFn's doc comment.
		return fn(tabCtx, actions...)
	}
	// Focus helpers only ever talk to an EXISTING tab. chromedp.Run on
	// anything else starts a new Chrome:
	//
	//   - FromContext == nil: a plain context.Background() (or any
	//     non-chromedp ctx). Run installs the default ExecAllocator.
	//   - FromContext != nil but Target == nil: chromedp.NewContext
	//     (context.Background()) — the fake-tab factory shape. FromContext
	//     is already the default ExecAllocator, so the FromContext-only
	//     check is not enough. The first Run still launches Chrome with
	//     chromedp's default flags (no --no-sandbox), which dies on CI
	//     ("No usable sandbox") and panics in Allocate cleanup
	//     (`close of closed channel`).
	//
	// A real tab has Target set: createTab's runFirstAttach does the first
	// Run before the tab is stored, so activate/release after that always
	// see a live target. Skipping here never drops a production focus
	// round-trip.
	c := chromedp.FromContext(tabCtx)
	if c == nil || c.Target == nil {
		return nil
	}
	runCtx, cancel := context.WithTimeout(tabCtx, m.PageTimeout())
	defer cancel()
	return chromedp.Run(runCtx, actions...)
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
	m.syncDialogListenersLocked(sessionID, se)
	tabs = snapshotTabsLocked(se)
	activeIdx = se.activeIdx
	// Captured under the SAME lock that just settled activeIdx, for the same
	// reason SwitchTab captures it there: the tab this call made active must
	// be the one told to come forward, even if a concurrent switch/close
	// lands right after the unlock.
	var newActiveCtx context.Context
	if activeIdx >= 0 && activeIdx < len(se.tabs) {
		newActiveCtx = se.tabs[activeIdx].ctx
	}
	m.mu.Unlock()

	closing.cancel()
	// Tell Chrome which tab is active now — the third path that needed this
	// and did not have it (review F9 follow-up, 2026-08-13). SwitchTab and
	// OpenTab both activate; CloseTab moved activeIdx and then fired
	// notifyTabsChanged (-> WebRTC recapture) without ever telling Chrome,
	// leaving the encoder's chrome.tabs.query({active:true}) resolution to
	// whatever Chrome happened to pick on target close. That is exactly the
	// silent capture-follows-the-wrong-tab failure activateTabInChrome was
	// written for on 2026-08-03 -- see its doc comment. Whether Chrome's own
	// choice agrees with this manager's ("the tab that slid into this index")
	// is not something to leave to chance in the one path that never states
	// its intent. Best-effort, like every other call site; no lock held.
	//
	// No corresponding releaseTabFocusInChrome: the tab that was left is the
	// one just closed, and its context is already cancelled.
	m.activateTabInChrome(newActiveCtx, sessionID, activeIdx)
	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	return tabs, activeIdx, nil
}

// OpenTab opens a fresh blank tab in sessionID's browsing context and makes
// it active, subject to the FR-060 memory gate (ADR-041 D3). Creates the browsing context
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
	if m.memoryRefusesTabOpenLocked() {
		m.mu.Unlock()
		return Tab{}, errMemoryPressureTabOpen
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
	if m.memoryRefusesTabOpenLocked() {
		m.mu.Unlock()
		newTab.cancel()
		return Tab{}, errMemoryPressureTabOpen
	}
	// The tab being left, captured before activeIdx moves — same rationale as
	// SwitchTab's (review finding F9).
	var prevCtx context.Context
	if se.activeIdx >= 0 && se.activeIdx < len(se.tabs) {
		prevCtx = se.tabs[se.activeIdx].ctx
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1
	m.installTargetListenerLocked(sessionID, se)
	// NOT a no-op, unlike the line above it: the target listener stays on
	// tab 0, but a JavaScript dialog is per-target, so the tab just appended
	// needs its OWN dialog listener or a dialog raised on it is invisible.
	m.syncDialogListenersLocked(sessionID, se)
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	newCtx := newTab.ctx
	m.mu.Unlock()

	// Opening a tab moves the active tab just as switching does, and the
	// tabs-changed callback below drives the SAME WebRTC recapture — so the
	// new tab needs the SAME treatment, or a browser_open_tab lands the
	// encoder on a tab that was never told it is foreground (review finding
	// F9; before this, OpenTab told Chrome nothing at all). Before
	// notifyTabsChanged for the ordering reason SwitchTab documents.
	m.activateTabInChrome(newCtx, sessionID, activeIdx)
	m.releaseTabFocusInChrome(prevCtx, sessionID)

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
	// tabAdoptReasonMemoryPressure means the target was detected but memory was
	// already reached (checked either before or immediately after the CDP
	// attach — see adoptTarget's doc comment).
	tabAdoptReasonMemoryPressure tabAdoptReason = "memory_pressure"
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
// (memory pressure, or the CDP attach itself failed) — exactly the silent
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
// Enforces the memory gate: a runaway window.open loop is refused when this
// machine is short of memory, not left unbounded (FR-060).
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
	if m.memoryRefusesTabOpenLocked() {
		m.mu.Unlock()
		logger.WarnCF("browser", "new tab target detected but this machine is low on memory — not adopting",
			map[string]any{
				"session_id": sessionID,
				"target_id":  string(targetID),
				"open_tabs":  m.OpenTabCount(),
			})
		return tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonMemoryPressure}, nil
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
	// Finalize the entry (result/err + close(entry.done)) BEFORE unlocking in
	// EVERY branch below — this closes a TOCTOU gap that could let a
	// concurrent caller for the SAME target see an inconsistent outcome
	// under scheduler contention. Before this fix, the delete above and the
	// outcome publication straddled an unlock: a caller making its OWN
	// first-time check could acquire m.mu in the window after the entry was
	// deleted from m.pendingAdopt but before entry.result/entry.err were set
	// and entry.done closed. That caller would find neither "still pending"
	// (map entry gone) nor "already ours" (se.tabs not updated yet for the
	// success path) and treat the target as unclaimed — starting its own
	// redundant createTab call and, on losing that race too, returning a
	// blind zero-value no-op to itself instead of the winner's real outcome
	// (exactly the silent-no-op bug pendingAdoptEntry exists to prevent).
	// Provable by inspection: the window is real regardless of scheduling
	// luck, since delete/unlock/finalize are three separate statements with
	// no ordering guarantee relative to another goroutine's lock attempt in
	// between. Keeping the map delete, any se.tabs mutation, and the entry
	// finalization inside ONE unbroken critical section closes the gap: any
	// goroutine that acquires m.mu after this section either sees the
	// target already adopted (se.indexOfTarget hit at the top of this
	// function) or a clean slate to legitimately retry — never a state in
	// between. Side effects that don't need m.mu (newTab.cancel(),
	// notifyTabsChanged — which itself takes m.mu, so calling it here would
	// deadlock) stay after Unlock(), using only locally snapshotted values.
	if err != nil {
		result := tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonAttachFailed}
		wrapped := fmt.Errorf("browser: failed to adopt new tab target %s: %w", targetID, err)
		entry.result, entry.err = result, wrapped
		close(entry.done)
		m.mu.Unlock()
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
		close(entry.done) // result/err stay zero-value: a true no-op for any waiter too
		m.mu.Unlock()
		newTab.cancel()
		return tabAdoptResult{}, nil
	}
	if m.memoryRefusesTabOpenLocked() {
		result := tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonMemoryPressure}
		entry.result = result
		close(entry.done)
		m.mu.Unlock()
		newTab.cancel()
		return result, nil
	}
	// The tab being left, captured BEFORE activeIdx moves — same rationale as
	// SwitchTab's and OpenTab's (review finding F9).
	var prevCtx context.Context
	if se.activeIdx >= 0 && se.activeIdx < len(se.tabs) {
		prevCtx = se.tabs[se.activeIdx].ctx
	}
	se.tabs = append(se.tabs, newTab)
	se.activeIdx = len(se.tabs) - 1 // ADR-041 D2: adopted tabs become active by default
	// The adopted tab is the one a click just opened, so it is the tab most
	// likely to raise a dialog next. It gets its own dialog listener here;
	// the target listener deliberately stays where it is.
	m.syncDialogListenersLocked(sessionID, se)
	tabs := snapshotTabsLocked(se)
	activeIdx := se.activeIdx
	active := tabs[activeIdx]
	result := tabAdoptResult{Adopted: &active}
	entry.result = result
	close(entry.done)
	newCtx := newTab.ctx
	m.mu.Unlock()

	// Adoption moves the active tab exactly as SwitchTab/OpenTab/CloseTab do,
	// and the notifyTabsChanged below drives the SAME WebRTC recapture — but
	// until now this path was the one that moved se.activeIdx WITHOUT ever
	// telling Chrome, so the encoder's chrome.tabs.query({active:true})
	// resolution was left to agree with us by luck. It is also the path where
	// disagreement is MOST likely: an adopted target is one Chrome itself just
	// opened and (for a user-initiated target="_blank"/window.open) usually
	// already activated, so "whatever Chrome picked" and "the tab we just made
	// active" are two independent answers. State the intent instead of
	// inheriting it. Best-effort and no lock held, like every other call site.
	m.activateTabInChrome(newCtx, sessionID, activeIdx)
	m.releaseTabFocusInChrome(prevCtx, sessionID)

	m.notifyTabsChanged(sessionID, tabs, activeIdx)
	return result, nil
}

// defaultAdoptRetryBackoff is the bounded backoff schedule
// adoptTargetWithRetry walks after a FAILED adoption attempt (an ERROR — a
// refusal such as memory pressure is a decision, not a failure, and is never
// retried).
//
// Why retries exist at all (measured on the operator's box, 2026-08-15): a
// failed adoption used to be PERMANENT. adoptTarget deletes its pendingAdopt
// entry on the error path, and Chrome fires Target.targetCreated for a given
// target exactly once, so nothing ever looks at that target again — one
// transient stall stranded the tab for the entire life of the browsing
// context. The observed stall was exactly that: "auto-attach: failed to
// adopt new tab target ... timed out after 20s", three times over, from an
// advert opening tabs on a 2-CPU hosted box where the CDP transport was
// already saturated. A tab stranded this way is the ROOT of the tab-switch
// symptom this fix wave is about: Chrome activates the tab it opened, our
// model never learns of it, and the two sources of truth are desynced from
// then on.
//
// Shape of the schedule: the first attempt's own failure already cost up to
// firstAttachTimeout (20s), so the point of the backoff is not speed, it is
// giving a saturated CDP transport progressively more room to drain while
// staying bounded. Three retries ≈ 14s of added waiting in the worst case,
// after which the tab is genuinely reported as stranded rather than retried
// forever — an unbounded retry against a target that no longer exists would
// be a per-advert goroutine leak.
var defaultAdoptRetryBackoff = []time.Duration{
	750 * time.Millisecond,
	3 * time.Second,
	10 * time.Second,
}

// adoptRetrySchedule returns this manager's adoption backoff schedule,
// falling back to defaultAdoptRetryBackoff. A field on the manager (see
// adoptRetryBackoff) rather than a package var so tests can shrink the
// schedule without mutating global state shared with every other test in
// the package.
func (m *BrowserManager) adoptRetrySchedule() []time.Duration {
	m.mu.Lock()
	sched := m.adoptRetryBackoff
	m.mu.Unlock()
	if sched == nil {
		return defaultAdoptRetryBackoff
	}
	return sched
}

// sessionExists reports whether sessionID still has a browsing context. Used
// by adoptTargetWithRetry to abandon a pending retry promptly when the
// context is torn down (Shutdown/CloseSession) rather than sleeping out the
// rest of its schedule against a session that no longer exists.
func (m *BrowserManager) sessionExists(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[sessionID]
	return ok
}

// adoptTargetWithRetry runs adoptTarget and, on a genuine ERROR, retries it
// on defaultAdoptRetryBackoff's bounded schedule — see that var's doc
// comment for the stranded-tab defect this closes.
//
// Only an error is retried. Every non-error outcome ends the loop
// immediately, and all of them are correct terminal states: a successful
// adoption, "already ours" (a racing adopter won), "no browsing context"
// (torn down), and a REFUSAL such as memory pressure — which is a policy
// decision already reported to the agent via tabAdoptResult.Unadopted, not
// something a retry could improve.
//
// Blocking by design; every caller already runs it on its own goroutine
// (handleTargetEvent must never block the CDP event-dispatch goroutine —
// see its doc comment).
func (m *BrowserManager) adoptTargetWithRetry(sessionID string, targetID target.ID) {
	_, err := m.adoptTarget(sessionID, targetID)
	if err == nil {
		return
	}

	sched := m.adoptRetrySchedule()
	for attempt, delay := range sched {
		logger.WarnCF("browser", "auto-attach: failed to adopt new tab target — retrying", map[string]any{
			"session_id":   sessionID,
			"target_id":    string(targetID),
			"error":        err.Error(),
			"attempt":      attempt + 1,
			"of":           len(sched),
			"retry_in_ms":  delay.Milliseconds(),
			"consequences": "tab stays stranded (not in the tab strip, and Chrome may show it) until a retry succeeds",
		})
		if !m.sessionExists(sessionID) {
			return // browsing context torn down under us — nothing left to adopt into
		}
		timer := time.NewTimer(delay)
		<-timer.C
		timer.Stop()
		if !m.sessionExists(sessionID) {
			return
		}
		if _, err = m.adoptTarget(sessionID, targetID); err == nil {
			return
		}
	}

	logger.WarnCF("browser", "auto-attach: gave up adopting new tab target", map[string]any{
		"session_id": sessionID,
		"target_id":  string(targetID),
		"error":      err.Error(),
		"attempts":   len(sched) + 1,
	})
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
// — e.g. memory pressure, or the CDP attach itself failing — so the caller
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
// contract this runs SYNCHRONOUSLY on the CDP event-dispatch goroutine and
// must never block or
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
	// adoptTargetWithRetry, not a bare adoptTarget: Target.targetCreated
	// fires exactly once per target, so a single transient failure here used
	// to strand the tab permanently — see defaultAdoptRetryBackoff's doc
	// comment for the measurement.
	go m.adoptTargetWithRetry(sessionID, info.TargetID)
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
	if err := chromedp.Run(
		ctx,
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
	// Same cancel-outside-the-lock discipline as ReapIdleSessions, and for the
	// same reason: a chromedp cancel can block indefinitely (see
	// cancelBounded's doc comment), and doing that under m.mu freezes every
	// browser tool call for every agent on this manager. Collect the cancels
	// while holding the lock, drop it, then run them bounded.
	var pending []func()
	m.mu.Lock()
	if se, ok := m.sessions[sessionID]; ok {
		for _, t := range se.tabs {
			pending = append(pending, t.cancel)
		}
		if se.browserCancel != nil {
			pending = append(pending, se.browserCancel)
		}
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	for _, cancel := range pending {
		cancelBounded(cancel, map[string]any{"session_id": sessionID, "origin": "close_session"})
	}
}

// PageTimeout returns the configured page load timeout.
func (m *BrowserManager) PageTimeout() time.Duration {
	return m.cfg.PageTimeout
}

// touchTabLocked records activity on ONE specific tab, resetting its own
// idle clock. Called wherever a single tab is created, made active, or
// directly acted on by an agent tool call — createTab (the tab's own
// creation instant), Session() (resolving the active tab for every
// browser_* tool call), and SwitchTab (the tab that becomes active).
//
// Must be called with m.mu HELD — it is a plain field write on an entry the
// caller already looked up, deliberately not re-locking (Session() holds the
// lock across its own lookup, and re-entering would deadlock).
func (m *BrowserManager) touchTabLocked(tab *tabEntry) {
	if tab != nil {
		tab.lastActivity = m.now()
	}
}

// now returns the current time through an overridable seam so idle/TTL logic
// is testable without sleeping. Tests set m.nowFn; production leaves it nil.
func (m *BrowserManager) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

// touchAllTabsLocked records activity on EVERY tab in a browsing context —
// used by ViewerAttached/ViewerDetached, where the activity signal applies
// to the WHOLE session, not one tab: the live panel's tab strip displays
// every tab in the context, so a viewer attaching or detaching is a
// "somebody (was/is) looking at ALL of these" event, not just the
// currently-active one. This preserves the PRE-EXISTING, already-shipped
// session-level semantics of a viewer detach "restarting the idle clock"
// (touchSession's old doc comment: "Detaching also COUNTS as activity: it
// starts the idle clock from the moment the last viewer left, rather than
// from whenever the session was last touched before that") — now applied to
// every tab in the context individually rather than one session-wide
// timestamp: each tab gets a fresh grace window from the moment the last
// viewer leaves, so a tab that was visible in the tab strip a moment ago
// does not age straight into reap-eligibility the instant the panel closes.
// This is a deliberate carry-forward of existing behavior, not a new
// design — see reaper_edge_test.go's
// TestReapIdleSessions_ViewerDetach_RestartsIdleClockFromDetachMoment for the
// pinned contract.
//
// Must be called with m.mu HELD, same discipline as touchTabLocked.
func (m *BrowserManager) touchAllTabsLocked(se *sessionEntry) {
	if se == nil {
		return
	}
	now := m.now()
	for _, t := range se.tabs {
		if t != nil {
			t.lastActivity = now
		}
	}
}

// ViewerAttached records that a live-panel viewer attached to sessionID's
// browsing context. A context with at least one attached viewer is never
// considered idle — somebody is watching it right now, for as long as they
// keep saying so.
//
// The attach itself is the first proof of life (FR-052): it stamps
// lastViewerBeat, so a viewer that attaches and then goes silent without ever
// heart-beating — the socket wedged before the first pong could come back —
// still ages out of viewerLivenessWindow rather than pinning forever. Every
// subsequent proof arrives via ViewerHeartbeat.
func (m *BrowserManager) ViewerAttached(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if se, ok := m.sessions[sessionID]; ok {
		se.viewers++
		se.lastViewerBeat = m.now()
		m.touchAllTabsLocked(se)
	}
}

// ViewerHeartbeat records that a viewer on sessionID's browsing context is
// still there right now (ADR-072 FR-052). Called from the live-panel
// WebSocket's PongHandler — the peer answering the server's keep-alive ping
// is the proof, which is why an idle-but-alive watcher keeps pinning its
// browser without touching anything.
//
// Deliberately does NOT touch any tab's lastActivity. A heartbeat means
// "somebody is still watching", not "somebody used this tab"; the whole
// context is already protected while live viewers > 0 (see ReapIdleSessions),
// and stamping tabs here would hand a phantom's tabs a fresh grace period the
// moment its pin finally expired.
//
// A no-op for an unknown session or one with no attached viewers, so a
// heartbeat that outlives a session recreation cannot conjure a pin from
// nothing.
func (m *BrowserManager) ViewerHeartbeat(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if se, ok := m.sessions[sessionID]; ok && se.viewers > 0 {
		se.lastViewerBeat = m.now()
	}
}

// ViewerDetached records that a live-panel viewer detached. Detaching also
// COUNTS as activity on every tab in the context: it starts each tab's idle
// clock from the moment the last viewer left, rather than from whenever that
// tab was last touched before that — see touchAllTabsLocked's doc comment.
// Never lets the count go negative (a detach without a matching attach — e.g.
// a viewer that outlived a session recreation — must not underflow into a
// permanently unreapable session).
func (m *BrowserManager) ViewerDetached(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if se, ok := m.sessions[sessionID]; ok {
		if se.viewers > 0 {
			se.viewers--
		}
		m.touchAllTabsLocked(se)
	}
}

// reapedTabInfo records one tab ReapIdleSessions actually closed during a
// sweep, carried past the m.mu.Unlock() below so the actual cancel() call and
// the log line can both run WITHOUT the lock held (ADR-038 discipline —
// cancel() can legitimately block on real work, and running it under the lock
// acquired at the top of ReapIdleSessions would freeze every OTHER browser tool
// call across the whole manager until it returned).
type reapedTabInfo struct {
	sessionID string
	tab       *tabEntry
}

// reapedSessionInfo pairs a fully-torn-down session's ID with its
// browserCancel func, carried past the unlock for the same reason as
// reapedTabInfo — see its doc comment.
type reapedSessionInfo struct {
	sessionID string
	cancel    context.CancelFunc
}

// cancelBoundedTimeout bounds how long a teardown path waits for a single
// cancel() call (a tab's own, or a browsing context's browserCancel) to
// return. chromedp.NewContext's returned cancel is not a bare stdlib
// context.CancelFunc — it also waits on an internal sync.WaitGroup and, for a
// context that OWNS a browser allocation, on a channel drained by the real
// allocate/attach flow.
//
// Be precise about when that channel can actually block, because getting this
// wrong in either direction is expensive. In chromedp v0.15.1 (chromedp.go),
// `c.allocated` is created ONLY when `c.Browser == nil`, and both wait sites
// guard on `if c.allocated != nil`. A TAB context is always a child of an
// already-allocated browsing context (createTab is only ever called with
// se.browserCtx as parent), so c.Browser is non-nil, c.allocated is nil, and
// the wait is skipped entirely — tab cancels cannot hang on this mechanism at
// all, however many times they are called.
//
// The real exposure is narrow: a context that owns an allocation, whose
// Allocate() never ran, canceled a SECOND time — the first call drains the
// single buffered token and nothing refills or closes the channel. That is
// reachable for browserCancel after a failed bootstrap, not for tabs.
//
// So this bound is deliberate insurance, not a fix for a demonstrated
// production tab hang: it is cheap, and it guarantees no teardown path can
// turn a slow or double cancel into a manager-wide freeze. Do NOT remove it on
// the strength of "tabs were never at risk" — browserCancel still is, and
// every caller here runs with no manager lock held precisely so that a wedged
// cancel cannot strand m.mu. A call that doesn't return within the bound is
// logged and abandoned (its goroutine leaked until it eventually returns, if
// ever). Sized like this file's other CDP-round-trip bounds
// (reconcileTargetListTimeout).
const cancelBoundedTimeout = 5 * time.Second

// cancelBounded runs cancel with a bounded wait — see cancelBoundedTimeout's
// doc comment for why a chromedp cancel func needs this instead of a bare
// synchronous call. Must be called with NO BrowserManager lock held.
func cancelBounded(cancel context.CancelFunc, logFields map[string]any) {
	if cancel == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(cancelBoundedTimeout):
		logger.WarnCF("browser", "reap: cancel did not return within the bound — abandoning the wait, not the sweep",
			logFields)
	}
}

// ReapIdleSessions closes every browser TAB that has had NO agent tool call
// touching it for at least cfg.IdleTTL, in every browsing context that has NO
// attached live-panel viewer — returning the session IDs of any browsing
// contexts whose LAST tab was closed this way (i.e. whose underlying Chrome
// browsing context/process was fully torn down this sweep). A context that
// merely lost SOME of its tabs but still has at least one survivor is NOT in
// the returned list — callers (pkg/gateway/gateway.go) log the returned IDs
// as "browsing contexts closed", which only applies to a full teardown.
//
// Why per TAB, not per browsing context (how this used to work): a browsing
// context can hold several tabs (ADR-041), and an agent legitimately leaves
// stale ones open — a lookup tab from ten minutes ago sitting behind the tab
// it is actually working in right now. Reaping the WHOLE context because ONE
// tab was recently touched let every other tab in it live forever; reaping
// the whole context because ANY tab went idle would kill a tab the agent is
// mid-task in, just because a sibling tab happened to be older. Judging each
// tab independently against its own tabEntry.lastActivity fixes both.
//
// The one whole-context exception is viewers: se.viewers > 0 PROTECTS THE
// ENTIRE BROWSING CONTEXT, not just the active tab, regardless of any
// individual tab's own idle time. The live panel's tab strip lists EVERY tab
// in that context — all of them read as "open in the UI" to the human
// watching it — so a background tab vanishing out from under a session
// someone is actively looking at would be a UI-visible bug, not a cleanup. A
// viewed context is therefore skipped wholesale: nothing in it is even
// evaluated this sweep.
//
// A tab that has never been touched (zero lastActivity) is not treated as
// infinitely idle — that would reap a tab created microseconds ago by a path
// that raced this sweep before its first touch landed. It is stamped now and
// judged fairly on the NEXT sweep, mirroring the pre-per-tab session-level
// guard this replaces.
//
// Never leaves a browsing context with zero tabs silently forgotten: if every
// tab in a context is idle past the TTL in the same sweep, the context itself
// (se.browserCancel, its underlying Chrome browsing context) is torn down and
// removed from m.sessions — exactly the pre-per-tab behavior for a fully idle
// session. There is no "closed every tab but left an empty sessionEntry"
// state.
//
// activeIdx correctness (the highest-risk part of this change): when the
// browsing context's active tab survives the sweep, activeIdx is recomputed
// to keep pointing at that SAME tab by IDENTITY (its CDP target ID), never by
// numeric index — removing an earlier tab from the slice shifts every later
// index down, and silently pointing an agent's next tool call at a DIFFERENT
// tab than the one it was using would be a correctness bug no test on the
// return value alone would catch. If the active tab itself was the one
// reaped, activeIdx falls back to the new last tab, which is NOT what CloseTab does (it keeps activeIdx when a
// tab slides into the same slot, and only falls back to the last tab when
// the closed one WAS last). Deliberately simpler here: reaping can remove
// several tabs at once, so there is no single "tab that slid into this
// slot" to inherit, and the edge case has no one correct answer — see
// TestReapIdleSessions_ActiveTabItselfReaped_ActiveIdxStaysCoherent, which
// pins only that the index stays in range.
//
// ADR-072 D1.5a/FR-059: there is NO tab-budget accounting here any more. Every
// counter was deleted, so a reaped tab hands nothing back — the memory gate
// re-reads live memory at the next open rather than tracking slots.
//
// Safe to call on any schedule; it is a no-op when nothing qualifies.
func (m *BrowserManager) ReapIdleSessions() []string {
	ttl := m.cfg.IdleTTL
	if ttl <= 0 {
		return nil
	}

	m.mu.Lock()
	now := m.now()
	var reapedSessions []string
	var reapedTabs []reapedTabInfo
	var reapedBrowsers []reapedSessionInfo
	for sessionID, se := range m.sessions {
		// The live panel's tab strip shows every tab in this context — a
		// viewer watching it protects ALL of them, in full, regardless of
		// any individual tab's idle time. See doc comment above.
		//
		// LIVE viewers, not the raw attach count (FR-052): the pin belongs to
		// somebody who is still there. A phantom whose WebSocket cleanup never
		// ran would otherwise keep every tab in this context off the sweep
		// forever, which also keeps the instance permanently non-idle
		// (pool.go's `idle` requires zero tabs) even once eviction has stopped
		// treating it as watched. See liveViewersLocked.
		if m.liveViewersLocked(se, now) > 0 {
			continue
		}

		// A session with NO tabs has no clock of its own, so without this it
		// would be skipped by the per-tab loop below forever — a leak this
		// rewrite would otherwise INTRODUCE, since the old session-level clock
		// used to catch it. Reachable in production: CloseTab's last-tab path
		// empties se.tabs and then calls createFirstTab to restore the
		// "never zero tabs" invariant; if that replacement fails (Chrome under
		// load — precisely the condition this reaper exists to survive) the
		// entry stays in m.sessions with a live browserCtx and no tabs.
		//
		// Stamped-then-judged rather than torn down on sight, because
		// CloseTab's empty window is legitimate and momentary; tearing down
		// inside it would race a normal tab replacement. A real replacement
		// completes in milliseconds, so anything still empty a whole TTL later
		// is genuinely stranded.
		if len(se.tabs) == 0 {
			if se.emptySince.IsZero() {
				se.emptySince = now
				continue
			}
			if now.Sub(se.emptySince) < ttl {
				continue
			}
			if se.browserCancel != nil {
				reapedBrowsers = append(
					reapedBrowsers,
					reapedSessionInfo{sessionID: sessionID, cancel: se.browserCancel},
				)
			}
			delete(m.sessions, sessionID)
			reapedSessions = append(reapedSessions, sessionID)
			continue
		}
		se.emptySince = time.Time{}

		var keep []*tabEntry
		var closing []*tabEntry
		for _, t := range se.tabs {
			if t.lastActivity.IsZero() {
				// Never touched yet — stamp it now and judge it fairly on
				// the NEXT sweep, not as infinitely idle.
				t.lastActivity = now
				keep = append(keep, t)
				continue
			}
			if now.Sub(t.lastActivity) < ttl {
				keep = append(keep, t)
				continue
			}
			closing = append(closing, t)
		}
		if len(closing) == 0 {
			continue
		}

		// Defer the actual t.cancel() calls until AFTER m.mu is released
		// below (ADR-038 discipline) — cancel() is not guaranteed to return
		// quickly (see cancelBoundedTimeout's doc comment), and calling it
		// while holding m.mu would freeze every other browser tool call
		// across this entire manager for however long it took.
		for _, t := range closing {
			reapedTabs = append(reapedTabs, reapedTabInfo{sessionID: sessionID, tab: t})
		}

		if len(keep) == 0 {
			// Every tab in this browsing context was idle past the TTL —
			// tear the whole context down too, exactly the pre-per-tab
			// behavior for a fully idle session. browserCancel is likewise
			// deferred past the unlock, for the same reason as the tabs.
			if se.browserCancel != nil {
				reapedBrowsers = append(
					reapedBrowsers,
					reapedSessionInfo{sessionID: sessionID, cancel: se.browserCancel},
				)
			}
			delete(m.sessions, sessionID)
			reapedSessions = append(reapedSessions, sessionID)
			continue
		}

		// Some tabs survive — shrink the tab set and keep the browsing
		// context alive. Recompute activeIdx by IDENTITY (target ID), not by
		// numeric index: see the activeIdx-correctness section of this
		// function's doc comment above.
		oldActive := se.active()
		se.tabs = keep
		newIdx := -1
		if oldActive != nil {
			newIdx = se.indexOfTarget(oldActive.targetID)
		}
		if newIdx < 0 {
			// The active tab itself was the one reaped — fall back to the
			// new last tab, mirroring CloseTab's own fallback.
			newIdx = len(se.tabs) - 1
		}
		se.activeIdx = newIdx
		// ADR-041 fix F3 precedent: re-arm the passive target-created
		// listener if tab 0 itself was replaced by this sweep.
		m.installTargetListenerLocked(sessionID, se)
		m.syncDialogListenersLocked(sessionID, se)
	}
	m.mu.Unlock()

	for _, rt := range reapedTabs {
		cancelBounded(rt.tab.cancel, map[string]any{"session_id": rt.sessionID, "target_id": string(rt.tab.targetID)})
		logger.InfoCF("browser", "reaped idle browser tab (no viewer and no agent activity within idle_ttl)",
			map[string]any{"session_id": rt.sessionID, "idle_ttl": ttl.String(), "tab_url": rt.tab.url})
	}
	for _, rb := range reapedBrowsers {
		cancelBounded(rb.cancel, map[string]any{"session_id": rb.sessionID})
	}
	for _, sessionID := range reapedSessions {
		logger.InfoCF(
			"browser",
			"reaped idle browsing context (last tab closed; no viewer and no agent activity within idle_ttl)",
			map[string]any{"session_id": sessionID, "idle_ttl": ttl.String()},
		)
	}
	return reapedSessions
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

// InvalidateExecPathCache clears this manager's exec-path resolution caches
// (success + negative), the per-agent counterpart to
// BrowserCoordinator.ApplyRuntimeConfig's c.execPath.invalidate() call
// (coordinator.go). Preprovision (called once, at gateway boot) is the only
// writer of this cache in the common ADR-043 shared-Chrome (coordinator)
// path — ensureStarted's own managed-mode branch, which would otherwise
// re-resolve, is bypassed entirely whenever a coordinator is attached (it
// asks the coordinator to launch instead, consulting the COORDINATOR's own,
// already-invalidated cache). So without this method, a policy change
// (trust_path_chrome / prefer_packaged / exec_path) that landed after boot
// could leave THIS field holding a resolution computed under the OLD
// policy — read back by VideoCapability() (this file), which is a real,
// gateway-request-path WebRTC capability classification, independent of
// coordinator mode.
//
// pkg/agent/loop.go's registerSharedTools currently retires and replaces
// the whole *BrowserManager on every hot reload (a fresh instance means a
// fresh, empty execPath cache — see that function's doc comments), so in
// today's wiring this call is a belt-and-braces no-op for the manager being
// torn down: call it anyway, at the same reload trigger the coordinator's
// ApplyRuntimeConfig responds to (right where the retired manager's
// Shutdown() is invoked), so a future refactor that mutates an existing
// manager's config in place — instead of always replacing the object —
// does not silently reintroduce the staleness this closes.
func (m *BrowserManager) InvalidateExecPathCache() {
	m.execPath.invalidate()
}

// Shutdown drops this manager's connection + all its sessions. In ADR-043
// shared-Chrome mode (coordinator wired), CRIT-001's pipe rework means there
// is no manager-local connection anymore (chromedp CHILD contexts of the
// coordinator's rootCtx share the one CDP pipe) — m.allocCancel is a no-op
// installed by ensureStarted, so calling it here kills NEITHER the Chrome
// process NOR disposes the agent's browser context (CRIT-002/C1: the
// coordinator owns both and tears the pipe down only via its own
// Shutdown/crash-relaunch). In the no-coordinator managed-mode fallback
// (tests + the legacy one-manager-one-Chrome path), m.allocCancel is the
// pipe allocator's own cdppipe.CancelFunc and DOES kill this manager's own
// Chrome. Either way the bookkeeping (sessions, started, allocCancel) is
// reset cleanly and idempotently.
func (m *BrowserManager) Shutdown() {
	// Fix-wave CRIT (reviewer 1, conf 88): stop this manager's WebRTC
	// CaptureSession, if any, BEFORE the connection/session teardown below.
	// Without this, a live capturing session was orphaned by Shutdown — its
	// encoder tab lives in the shared-Chrome coordinator's own root context
	// (capture_session.go's defaultEncoderStarter), not any of the sessions
	// torn down here, so it survives; its ping beacon keeps the encoder-
	// liveness watchdog happy; and the registry entry it still occupies
	// (BrowserManager.capture) is never cleared, making it unstoppable once
	// this manager itself is gone (e.g. hot-reload via pkg/agent/loop.go's
	// registerSharedTools, which calls Shutdown on the OLD manager while a
	// NEW one takes over — the orphaned session's own token still resolves
	// in the gateway's captureRegistry, but nothing ever calls Stop() on it
	// again). cs.Stop() is idempotent and safe to call even if the manager
	// never actually started a capture session (CaptureSession() returns nil
	// then). Deliberately taken via m.CaptureSession() BEFORE m.mu.Lock()
	// below: cs.Stop() can block for a few seconds (its own best-effort
	// ingest control-frame write, now bounded by fix 5's write deadline) and
	// must never hold m.mu — a separate mutex (m.captureMu) — while doing so.
	if cs := m.CaptureSession(); cs != nil {
		cs.Stop()
	}

	// Cancels are collected under the lock and run after it — a wedged chromedp
	// cancel here would hang shutdown (and therefore hot-reload) forever. See
	// cancelBounded and ReapIdleSessions for the same discipline.
	var pending []func()
	m.mu.Lock()
	for id, se := range m.sessions {
		for _, t := range se.tabs {
			pending = append(pending, t.cancel)
		}
		if se.browserCancel != nil {
			pending = append(pending, se.browserCancel)
		}
		delete(m.sessions, id)
	}

	allocCancel := m.allocCancel
	m.allocCancel = nil
	m.started = false
	m.mu.Unlock()

	for _, cancel := range pending {
		cancelBounded(cancel, map[string]any{"agent_id": m.agentID, "origin": "shutdown"})
	}
	if allocCancel != nil {
		cancelBounded(allocCancel, map[string]any{"agent_id": m.agentID, "origin": "shutdown_allocator"})
	}

	logger.InfoCF("browser", "Browser manager connection shut down", map[string]any{
		"agent_id":    m.agentID,
		"coordinator": m.coordinator != nil,
	})
}

// OpenTabCount returns the number of currently-open tabs across this manager's
// browsing contexts. It SURVIVED ADR-072 D1.5a's counter deletion as a COUNT —
// it is reporting and diagnostics, never compared against a cap. The read takes
// m.mu briefly (the same lock totalTabCountLocked uses), so callers must NOT
// already hold m.mu.
func (m *BrowserManager) OpenTabCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTabCountLocked()
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
	// Fix-wave CRIT: same orphaned-CaptureSession hazard Shutdown() guards
	// against (see its doc comment above) — invalidateConnection is a
	// SEPARATE teardown path (coordinator-detected crash recovery) that does
	// NOT route through Shutdown, so it needs its own identical guard. Taken
	// before m.mu.Lock() for the same reason: cs.Stop() must never block
	// while holding m.mu.
	if cs := m.CaptureSession(); cs != nil {
		cs.Stop()
	}

	// Cancels are COLLECTED here and run after the lock is dropped — see
	// cancelBounded. This path runs on coordinator-detected crash recovery,
	// i.e. exactly when a chromedp cancel is most likely to be wedged, so
	// canceling under m.mu would freeze every browser tool call for every
	// agent on this manager.
	var pending []func()
	m.mu.Lock()
	for id, se := range m.sessions {
		for _, t := range se.tabs {
			pending = append(pending, t.cancel)
		}
		if se.browserCancel != nil {
			pending = append(pending, se.browserCancel)
		}
		delete(m.sessions, id)
	}
	// Cancel the dead remote allocator if present (no-op on an already-dead
	// connection). Do NOT nil it under lock in a way that races a concurrent
	// ensureStarted — the next ensureStarted runs under m.mu and overwrites
	// allocCtx/allocCancel/browserCtxID atomically after observing started==false.
	allocCancel := m.allocCancel
	m.allocCancel = nil
	m.allocCtx = nil
	m.started = false
	m.mu.Unlock()

	for _, cancel := range pending {
		cancelBounded(cancel, map[string]any{"agent_id": m.agentID, "origin": "invalidate_connection"})
	}
	if allocCancel != nil {
		cancelBounded(allocCancel, map[string]any{"agent_id": m.agentID, "origin": "invalidate_allocator"})
	}
}
