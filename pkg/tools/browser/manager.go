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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
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
	// IdleCloseTTL is the WHOLE-CHROME idle window (ADR-075 FR-040): how long
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
	// for disposable browser cache (ADR-075 FR-072). Reload-applied.
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

	// --- ADR-085 browser control handover (BROWSER-FR-031a/FR-052) ---

	// ControlIdleRelease is the LiveViewRegistry idle-release sweeper's
	// window (tools.browser.control_idle_release), ALREADY resolved to its
	// effective value by config.BrowserToolConfig.EffectiveControlIdleReleaseSec
	// before it reaches here (registerSharedTools' translation) — this
	// field never sees the raw "0 means unset" config zero value, only the
	// resolved default (900s) or an explicit override. Zero here means the
	// operator explicitly disabled expiry (an indefinite hold, opt-in only).
	// Named to match this field's own config key rather than the
	// IdleTTL/IdleCloseTTL naming above, because it governs a DIFFERENT
	// thing (a held control lock, not a browsing context).
	//
	// It carries NO "Sec" suffix even though its config counterpart does:
	// this one is a time.Duration (the suffix would be a lie, and
	// staticcheck's ST1011 says so), while config.BrowserToolConfig's
	// ControlIdleReleaseSec is an int of seconds and earns it. Two
	// differently-typed fields, one config key — the translation between
	// them is loop.go's registerSharedTools.
	ControlIdleRelease time.Duration `json:"control_idle_release,omitempty"`
	// TakeControlEnabled mirrors config.BrowserToolConfig.TakeControlEnabled
	// (tools.browser.take_control_enabled). Read by the sweeper on every
	// tick (BROWSER-FR-052): with this false, no take can succeed
	// (pkg/gateway/browser_ws.go's own check), and this field is what lets
	// the sweeper release an ALREADY-held wheel left over from before the
	// flag flipped, on its own regardless of whether the panel is even
	// attached — see LiveViewRegistry.sweepTick. A config reload rebuilds
	// this manager wholesale (registerSharedTools' Shutdown-old/install-new
	// pattern), so this value is as "live" as every other field on this
	// struct: current as of the last reload, not baked in at process start.
	TakeControlEnabled bool `json:"take_control_enabled,omitempty"`
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
	// was still there (ADR-075 FR-052). Stamped by ViewerAttached and
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
// live memory (ADR-075 D1.5a — every tab counter was deleted).
type BrowserManager struct {
	cfg  BrowserConfig
	ssrf *security.SSRFChecker // never nil — enforced by NewBrowserManager
	mu   sync.Mutex
	// tabCommands serializes target operations and their observers; guarded by mu.
	tabCommands map[string]*liveTabCommandGate
	// At most one metadata publication waits for each exact session entry.
	pendingTabNotifications map[string]*sessionEntry
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
	// This is NOT ownership and must not be read as any (ADR-075 FR-070,
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
	pending      map[string]chan struct{}
	started      bool
	localStartup *startupCohort

	// Pool retirement advances this under m.mu. A registration that releases
	// m.mu must still match before publishing its returned connection.
	poolRegistrationGeneration uint64

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
	// ADR-075 FR-031 there are no CDP browser contexts at all, so no path can
	// create one whose disposal would destroy cookies on a reload. Cookies
	// live in the workspace's profile directory on disk.
	coordinator *BrowserCoordinator
	// pool is the ADR-075 per-workspace browser pool. When set it SUPERSEDES
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
	// captures is keyed by resolved panel tab-set ID. Capture lifecycle code
	// calls back into the manager, so its mutex is separate from m.mu.
	captures  map[string]*CaptureSession
	captureMu sync.Mutex
	// Guard capture admission until every concurrent connection teardown ends.
	captureTeardowns int
	// videoHealthObs is the gateway's live-video health observer (issue
	// #674), registered once per manager by the browser WS handler and
	// installed on every CaptureSession this manager creates. Guarded by
	// captureMu, alongside the session it is installed on. nil until the
	// gateway registers one (and always nil for a manager driven by a caller
	// that has no viewers to notify, e.g. a unit test).
	videoHealthObs func(VideoHealthEvent)

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
// within viewerLivenessWindow (ADR-075 FR-010, FR-052).
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
// manager right now (ADR-075 FR-051).
//
// EVERY browser tool increments it — leased and lease-exempt alike — because
// the question eviction asks is "would killing this Chrome break a call that
// is currently running", and a read-only call breaks exactly as visibly as a
// write one. A screenshot that returns "connection lost" mid-turn is not less
// confusing for having been read-only.
//
// The read and EnterCall's increment use m.mu. The pool holds that same mutex
// through the final retirement claim, so a new call is either counted before
// removal or observes a manager marked unstarted and waits for cleanup.
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
// every tab counter was deleted by ADR-075 D1.5a. It answers one question:
// has this browser got anything left in it (FR-040's idle close).
func (m *BrowserManager) TotalOpenTabs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTabCountLocked()
}

// CaptureSession returns the workspace operator tab set's capture. Callers with
// a resolved panel identity must use CaptureSessionForPanel.
func (m *BrowserManager) CaptureSession() *CaptureSession {
	return m.CaptureSessionForPanel(m.OperatorSessionID())
}

// EnsureCaptureSession reuses or creates the workspace operator's capture.
// Panel-aware callers use EnsureCaptureSessionForPanel with their resolved ID.
func (m *BrowserManager) EnsureCaptureSession(newFn func() (*CaptureSession, error)) (*CaptureSession, error) {
	return m.EnsureCaptureSessionForPanel(m.OperatorSessionID(), newFn)
}

// SetVideoHealthObserver registers fn as the observer notified whenever the
// live-browser video path for this manager's captures changes state — lost,
// recovering, recovered, or unrecoverable (issue #674). It is installed on the
// current captures and on every session created afterwards.
//
// Registered on the manager rather than passed to NewCaptureSession because
// the gateway learns which manager it is dealing with at browser_attach time,
// well before any viewer offer creates a capture session — and because a
// manager outlives the sessions it creates, so one registration covers a
// session that is torn down and rebuilt. Idempotent: the browser WS handler
// re-registers the same observer on every attach.
//
// fn is invoked on whichever goroutine observed the transition, with no
// CaptureSession lock held. Pass nil to unregister.
func (m *BrowserManager) SetVideoHealthObserver(fn func(VideoHealthEvent)) {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	m.videoHealthObs = fn
	for _, cs := range m.captures {
		if cs != nil {
			cs.SetOnVideoHealth(fn)
		}
	}
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
	for panel, cs := range m.captures {
		if cs == cur {
			delete(m.captures, panel)
		}
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

// execPathNegativeCacheTTL bounds how long a failed resolution is remembered
// (and returned verbatim, without re-probing) before resolveExecPath retries
// the real PATH/managed resolution. Long enough that a dead host's repeated
// browser_* calls do not each pay the full ~50s re-probe cost (4 PATH
// candidates at up to chromiumProbeTimeout each + a CfT manifest fetch); short
// enough that a transient failure (network flap, partial download) is retried
// reasonably soon.
const execPathNegativeCacheTTL = 60 * time.Second

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

// firstAttachTimeout bounds opening a tab: for a brand-new tab, creating,
// activating AND attaching it share this one budget (openNewTab); for an
// adopted tab it bounds runFirstAttach's wait for the very first chromedp.Run
// on its freshly created chromedp context. Used by both
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

// errMemoryPressureTabOpen is the shared refusal every tab-open site returns
// when the FR-060 memory gate says stop. It replaces the deleted cap error, which
// named a cap (tools.browser.max_tabs) that ADR-075 D1.5a DELETED.
//
// It names MEMORY and a remedy that exists, and it names NO limit and NO config
// key — deliberately (FR-053, FR-063). An operator told to raise a limit would
// go looking for a setting this build does not have, and a model told the same
// would report a fixable configuration problem where the real answer is "close
// a tab, or free memory on this machine".
var errMemoryPressureTabOpen = errors.New(
	"this machine is low on memory, so no further browser tab can be opened right now. " +
		"Close a tab with browser_close_tab, or wait for memory to free up, and retry")

// tabAdoptReason is a machine-readable reason code explaining why
// adoptTarget detected a genuinely new CDP target but did NOT adopt it
// (ADR-041 fix F2). Threaded through ReconcileTabs and surfaced by
// browser_click so the agent can tell the user a tab was stranded, instead
// of the pre-fix behavior of silently reporting plain success.
type tabAdoptReason string

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

// reconcileTargetListTimeout bounds the chromedp.Targets CDP round trip
// ReconcileTabs issues — a read-only "list every target" query, but still
// routed through a deadline (ADR-038 discipline: every CDP round trip is
// bounded) so a wedged transport fails this one reconcile pass instead of
// hanging its caller (browser_click) forever.
const reconcileTargetListTimeout = 5 * time.Second

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
	m.retireTabCommandsLocked(sessionID)
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
// still there right now (ADR-075 FR-052). Called from the live-panel
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
// ADR-075 D1.5a/FR-059: there is NO tab-budget accounting here any more. Every
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
	var notifications []reapedTabNotification
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
			release := m.tryAcquireTabCommandLocked(sessionID)
			if release == nil {
				continue
			}
			notifications = append(notifications, reapedTabNotification{sessionID: sessionID, session: se, release: release})
			if se.browserCancel != nil {
				reapedBrowsers = append(
					reapedBrowsers,
					reapedSessionInfo{sessionID: sessionID, cancel: se.browserCancel},
				)
			}
			delete(m.pending, sessionID)
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
		release := m.tryAcquireTabCommandLocked(sessionID)
		if release == nil {
			continue
		}
		notifications = append(notifications, reapedTabNotification{sessionID: sessionID, session: se, release: release})

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
			delete(m.pending, sessionID)
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

	for _, notification := range notifications {
		m.publishReapedTabSnapshot(notification)
	}
	// Removed contexts are cleaned up after publication, outside both manager
	// mutex and target admission. They can no longer become active again.
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
	if m.live != nil {
		m.live.Shutdown()
	}

	// Encoder targets outlive the tab contexts below. Stop every panel capture
	// first, outside manager locks: Stop can perform I/O and its callback removes
	// the exact capture from this manager.
	captures := m.beginCaptureTeardown()
	defer m.endCaptureTeardown()
	for _, cs := range captures {
		cs.Stop()
	}

	// Cancels are collected under the lock and run after it — a wedged chromedp
	// cancel here would hang shutdown (and therefore hot-reload) forever. See
	// cancelBounded and ReapIdleSessions for the same discipline.
	var pending []func()
	m.mu.Lock()
	for id := range m.tabCommands {
		m.retireTabCommandsLocked(id)
	}
	m.pending = nil
	m.pendingTabNotifications = nil
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
// browsing contexts. It SURVIVED ADR-075 D1.5a's counter deletion as a COUNT —
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
	captures := m.beginCaptureTeardown()
	defer m.endCaptureTeardown()
	for _, cs := range captures {
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
