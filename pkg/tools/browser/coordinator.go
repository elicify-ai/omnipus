package browser

// coordinator.go implements ADR-043 / spec "Stream A": the BrowserCoordinator
// owns the ONE gateway-scoped shared Chrome process + every agent's CDP browser
// context. Per-agent BrowserManagers connect to it via a remote allocator and
// re-adopt a coordinator-owned browser context non-owningly.
//
// CRIT-001 (live-browser-video-streaming / ADR-044 §6.0.3, ported into this
// branch by the WebRTC build's W1-A task): CDP now flows over Chromium's
// --remote-debugging-pipe (inherited fd 3/4, NUL-delimited JSON) via
// pkg/tools/browser/cdppipe — there is NO TCP debug port, NO /json HTTP
// surface, and NO ws:// URL, so a co-tenant process cannot reach CDP on any
// kernel. Consequences carried here:
//   - The single-launch guarantee moved from the net.Listen(":9223") bind to
//     an O_EXCL/flock lockfile (coordinator_lock_{unix,other}.go); the
//     ownership marker stays the identity layer (pid + product).
//   - In-process CDP sharing is chromedp CHILD contexts of rootCtx (one pipe,
//     multiplexed), NOT RemoteAllocator(ws://9223) — the pipe is PRIVATE to
//     the launcher (this OS process), so any cross-OS-process CDP consumer
//     (e.g. an external CLI) MUST route through the gateway API instead of
//     dialing the browser directly. Register returns the shared rootCtx
//     itself (not a URL); manager.go's ensureStarted builds its allocCtx
//     directly from it — see bootstrapBrowserCtx's doc comment for why a
//     context whose Browser is already populated safely produces
//     c.first==false child contexts without needing chromedp's
//     RemoteAllocator special-case.
//
// PARTIALLY changed from the pre-pipe design (WebRTC build W1-A): Register
// still keeps this branch's per-agent ISOLATION MODEL — one dedicated,
// coordinator-owned browser context per agent, not the archive's shared
// "encoder tab in the DEFAULT browser context" scheme — but the CDP call
// that creates each context switched from chromedp.WithNewBrowserContext()
// to a raw CDP CreateBrowserContext + CreateTarget(WithNewWindow(true))
// pair. Verified against real Chrome (149 over this branch's pipe launch;
// the archive independently records the same finding on 151):
// WithNewBrowserContext()'s internal CreateTarget call omits WithNewWindow,
// which fails "-32000 no browser is open" against a brand-new, zero-window
// browser context on full-Chrome new-headless. WithNewWindow(true) fixes
// that one CDP call; it is NOT the archive's WithBackground(true) foreground-
// steal workaround (irrelevant here — no encoder tab exists in this
// coordinator). See Register's doc comment for the full rationale, and
// RemoveAgent's for the disposal consequence (chromedp's implicit
// browserContextOwner auto-dispose no longer fires; disposal is explicit via
// disposeBrowserContextRaw).
//
// Load-bearing invariants (grill C1 / spec §3 Stream A):
//   - CRIT-002/C1: a manager reload (any Settings save) must NOT kill Chrome and
//     must NOT dispose the agent's browser context. The coordinator owns both.
//   - CRIT-003: the agent's owning browser context is created ONLY on the
//     coordinator's own long-lived chromedp context (which lives on the
//     *AgentLoop and therefore survives ReloadProviderAndConfig). No manager
//     path ever creates one; managers only re-adopt via
//     WithExistingBrowserContext (non-owning, no auto-dispose).
//   - FR-008/R3: the Chrome process is killed exactly once, only by Shutdown
//     (gateway Close). Release + reload never touch the process.
//
// Locking discipline (ADR-038 / spec round-2 MAJ-007): Register/Release/
// RemoveAgent/PID take c.mu for bookkeeping only; Register
// (and the crash-relaunch path) RELEASE c.mu across the blocking Chrome launch +
// CDP context-create, mirroring manager.go ensureStarted's unlock/relock pattern —
// never hold c.mu across a CDP/exec call.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/extensions"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ownershipMarkerOwner is the owner string stamped into the shared-Chrome
// ownership marker (shared-chrome.pid). Its presence + a live pid is the proof
// that the process holding the launch lockfile is OUR Chrome (ADR-043 D1 /
// grill M2). CRIT-001 replaced the old net.Listen(":9223") port bind with an
// O_EXCL/flock lockfile, so the marker is now the identity layer over the
// lock: a held lock whose marker is absent or whose marker pid is dead is
// treated as stale (removable), never silently driven.
const ownershipMarkerOwner = "omnipus"

// agentBrowserContext is a coordinator-owned CDP browser context for one agent
// (spec D2). The owning chromedp context (ctx/cancel) lives on the
// COORDINATOR, created via raw CDP (target.CreateBrowserContext +
// CreateTarget WithNewWindow(true) — see Register's doc comment for why, not
// chromedp.WithNewBrowserContext()), so chromedp's own browserContextOwner
// flag is NEVER set for these and cancel() alone does not dispose the CDP
// browser context — RemoveAgent/disposeBrowserContextRaw do that explicitly.
// The manager only ever sees the BrowserContextID and re-adopts it
// non-owningly.
type agentBrowserContext struct {
	browserCtxID cdp.BrowserContextID
	ctx          context.Context
	cancel       context.CancelFunc
}

// BrowserCoordinator owns the single shared Chrome process and every agent's
// browser context for one gateway (ADR-043 D1/D4). It lives on *AgentLoop (not
// per-agent), so it survives ReloadProviderAndConfig — which is the whole basis
// of CRIT-002 (reload preserves the Chrome process + each agent's context).
type BrowserCoordinator struct {
	homeDir string // $OMNIPUS_HOME; ownership marker + profile dir root
	cfg     BrowserConfig

	// execPath holds the exec-path resolution caches, reused from the manager
	// (resolveBrowserExecPath). Resolved once per process; a successful
	// resolution is cached and re-validated with os.Stat on each use.
	execPath execPathCaches

	mu sync.Mutex // bookkeeping lock — see file doc for discipline

	// pipeLauncher launches the one shared Chrome over the CDP pipe transport
	// (CRIT-001 — no TCP port). A field so tests inject a fake and never spawn
	// real Chrome; defaults to launchManagedPipe (exec_resolver.go).
	pipeLauncher func(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error)

	// Chrome process state (lives on the coordinator; managers never touch it).
	launched   bool
	launching  bool       // single-flight: a launch is in progress
	launchDone *sync.Cond // signaled when a launch completes (nil-safe via mu)
	// rootCtx is the chromedp context returned by the pipe allocator (binds the
	// shared *Browser). In-process managers drive the shared Chrome through
	// chromedp CHILD contexts of this (one pipe, multiplexed) — NOT a
	// RemoteAllocator(ws://) connection, which no longer exists (CRIT-001).
	rootCtx context.Context
	// rootCancel tears the pipe allocator down: it cancels the chromedp
	// context, closes the pipe (Chrome exits), and reaps the process. The
	// pre-pipe allocCtx/allocCancel pair (a separate ExecAllocator context) is
	// gone — the pipe transport has exactly one teardown handle.
	rootCancel context.CancelFunc
	cmd        *exec.Cmd // Chrome process handle (captured via cdppipe ModifyCmd) — the ONLY PID source (Browser.Process() is nil over the pipe)
	// lockFile holds the O_EXCL/flock single-launch lock (CRIT-001) for the
	// coordinator's lifetime. Released (flock LOCK_UN + close) on Shutdown and
	// on crash-relaunch. Replaces the removed net.Listen(":9223") atomic guard.
	lockFile *os.File

	// loadedExtensionID is the id Chrome assigned the last successfully
	// loaded extension (LoadExtension / the launchChrome auto-load below).
	// Empty until a load succeeds. WebRTC build hook (W1-A item 3): exposed
	// for the gateway's future encoder-extension wiring; nothing in this
	// package sets cfg.ExtensionDir/ExtensionID today.
	loadedExtensionID string

	// Per-agent owning browser contexts. Survive manager reload (Release only
	// drops the manager ref, not this entry). Cleared on crash (CRIT-001:
	// contexts are in-memory, lost on process restart) and on Shutdown.
	contexts map[string]*agentBrowserContext

	// Registered managers — for crash
	// notification (invalidateConnection on each). The manager ref is dropped
	// on Release; the context entry above intentionally is NOT.
	managers map[string]*BrowserManager

	// killCount is incremented exactly once per Chrome process kill, ONLY in
	// Shutdown (FR-008 / R3). Stays 0 across reload/Release; ==1 after Close.
	killCount int

	// shutdown flag prevents new launches / crash-relaunch racing with a
	// Shutdown in progress.
	shutdown bool

	// captureSharedContext is the ADR-048 condition-1 config knob
	// (tools.browser.capture_shared_context, default true) promoted from the
	// former OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT-only experimental flag.
	// Set via SetCaptureSharedContext (the gateway's agent-loop seed wiring
	// calls it on every fresh-seed and reload pass); read by Register (which
	// browsing context an agent's session bootstraps into) and by
	// CaptureSharedContextEnabled (capability.go's CaptureVideoCapability
	// ADR-048 condition-3 check). The env var is still honored as an
	// explicit override layered on top — see captureSharedContextResolved —
	// for tests and operators who need to force a value without touching
	// config.json.
	captureSharedContext bool
}

// NewBrowserCoordinator constructs a coordinator. homeDir is $OMNIPUS_HOME (the
// ownership marker lands at <homeDir>/browser/shared-chrome.pid; the profile
// dir comes from cfg.ProfileDir). There is no tab budget parameter: ADR-072
// D1.5a deleted every tab counter, and live memory is the only limit.
// startPageURL mirrors BrowserManager.StartPageURL for the coordinator's own
// target-creation path, which builds targets from c.cfg rather than through a
// manager. Same fallback: about:blank when nothing is configured.
func (c *BrowserCoordinator) startPageURL() string {
	if u := strings.TrimSpace(c.cfg.StartPageURL); u != "" {
		return u
	}
	return BlankPageURL
}

func NewBrowserCoordinator(homeDir string, cfg BrowserConfig) *BrowserCoordinator {
	c := &BrowserCoordinator{
		homeDir:      homeDir,
		cfg:          cfg,
		contexts:     make(map[string]*agentBrowserContext),
		managers:     make(map[string]*BrowserManager),
		pipeLauncher: launchManagedPipe,
	}
	c.launchDone = sync.NewCond(&c.mu)
	// ADR-072 D1.5a/FR-059: there is no tab budget to report. Every counter —
	// the global one and the per-agent one — is deleted, and the only limit is
	// live memory, enforced at each tab open by the manager's own gate. An
	// operator reading this line used to be told a number to raise; there is no
	// number now, and telling them one that no longer exists is worse than
	// telling them nothing.
	logger.InfoCF("browser", "browser tab count is bounded by live memory, not by a configured cap",
		map[string]any{
			"idle_ttl":   cfg.IdleTTL.String(),
			"real_bound": "host RAM — idle reaping does not bound tabs kept actively in use",
		})
	return c
}

// HISTORICAL NOTE — there is no tab counter of any kind here any more.
// Spec D7 originally seeded a global cross-agent budget of 30, sized from the
// measured ~91 MB Chrome baseline + a blended per-tab average. That number was
// a proxy for a memory guard, not a correctness guard, and ADR-072 D1.5a
// replaced the whole family — the global budget, the per-agent courtesy cap and
// the in-flight reservation bookkeeping — with a live-memory gate at each tab
// open (FR-059, FR-060). Do not reintroduce a counter here: a cap an operator
// can raise is a number that has to be right on every host, and the reason it
// was removed is that no single number ever was.

// Register associates mgr with the coordinator and returns the coordinator's
// SHARED root chromedp context (rootCtx) + the agent's STABLE browser-context
// id. CRIT-001: the manager builds its allocCtx directly from rootCtx and
// drives the shared Chrome through chromedp CHILD contexts of it (one CDP
// pipe, multiplexed) — there is no RemoteAllocator dial anymore (the pipe has
// no ws:// URL and is private to this OS process). THE COORDINATOR
// CREATES+OWNS the context: it is created on the coordinator's own long-lived
// chromedp context (which survives reload because ReloadProviderAndConfig
// reuses the same *AgentLoop) via raw CDP CreateBrowserContext +
// CreateTarget(WithNewWindow(true)) — see the "create the agent's owning
// browser context" block below for why not chromedp.WithNewBrowserContext().
// The manager re-adopts it non-owningly via WithExistingBrowserContext(id) —
// INVARIANT CRIT-003: no manager path ever creates a browser context itself
// (that would auto-dispose on the manager's reload-time cancel, destroying
// cookies every Settings save).
//
// Launches Chrome if none is live. The blocking launch + CDP context-create
// run with c.mu RELEASED (ADR-038 no-lock-across-blocking-call; the launch can
// shell out to resolve the binary, download Chrome-for-Testing, or block on the
// CDP handshake for seconds). Concurrent Register callers serialize on the
// single-flight launch (c.launching / c.launchDone); the winner launches, the
// losers wait and then observe c.launched.
//
// createTargetParamsForTest, when non-nil, is invoked with the fully-built
// target.CreateTargetParams for the per-agent window right before it is
// sent over CDP (D17 test seam — see the call site inside Register). Nil
// in production; tests restore it via t.Cleanup, matching this package's
// other for-test seams (e.g. package_chrome.go's packageChromeRootForTest).
var createTargetParamsForTest func(*target.CreateTargetParams)

func (c *BrowserCoordinator) Register(
	ctx context.Context,
	agentID string,
	mgr *BrowserManager,
) (rootCtx context.Context, browserCtxID cdp.BrowserContextID, err error) {
	if agentID == "" {
		return nil, "", fmt.Errorf("browser: coordinator.Register requires a non-empty agentID")
	}
	if mgr == nil {
		return nil, "", fmt.Errorf("browser: coordinator.Register requires a non-nil manager")
	}

	// Ensure Chrome is live (single-flight; releases c.mu across the launch).
	if err := c.ensureLaunched(ctx); err != nil {
		return nil, "", err
	}

	// ADR-048 condition 1 (promoted from the former, env-only
	// OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT experimental flag to the
	// first-class tools.browser.capture_shared_context config knob, default
	// true): skip the per-agent CDP browser context and let the agent's
	// manager bootstrap its session in the DEFAULT browser context (empty
	// browserCtxID → the manager's bootstrapBrowserCtx omits
	// WithExistingBrowserContext, exactly like the dedicated-Chrome managed
	// mode). WHY: chrome.tabCapture (the ADR-047 D2 capture mechanism)
	// hard-fails with "Invalid tab specified." for ANY tab living in a
	// CDP-created browser context — those contexts are independent
	// off-the-record profiles outside the extension's include_incognito
	// reach, even with Extensions.loadUnpacked's enableInIncognito granted
	// (verified against real Chrome 150; the extension can SEE the tabs via
	// chrome.tabs.query but cannot capture them). This REVERSES the ADR-043
	// per-agent-context isolation model for every agent registered while
	// enabled (config field doc comment carries the full isolation
	// warning) — trading per-agent cookie/storage isolation for a
	// capturable agent tab. Costs while enabled: no CRIT-002 context
	// persistence across reload, and all agents share the default context's
	// cookie partition.
	if c.captureSharedContextResolved() {
		c.mu.Lock()
		c.managers[agentID] = mgr
		rootCtx = c.rootCtx
		c.mu.Unlock()
		logger.WarnCF(
			"browser",
			"coordinator: shared default-context capture mode is ON (tools.browser.capture_shared_context) — per-agent browser-context isolation is OFF",
			map[string]any{"agent_id": agentID},
		)
		return rootCtx, "", nil
	}

	// Re-use an existing context for this agent if one already exists (reload
	// case: Release dropped only the manager ref, the context survived). This
	// is the CRIT-002 persistence: the SAME browserCtxID is returned across a
	// reload, so cookies/localStorage/login survive a Settings save.
	//
	// LOW-1: c.managers[agentID] is installed BEFORE the context-create (which
	// can fail) rather than after. This is deliberate: the reload re-adopt
	// early-return path below MUST install the new manager so TotalOpenTabs +
	// Release find it, and that path never creates a context. A failed
	// context-create on the fresh-agent path leaves a manager with no context
	// in the map, but that is harmless — a fresh manager has no tabs
	// (so it doesn't inflate the budget) and Release's dropConnection on a
	// never-started manager is a no-op.
	c.mu.Lock()
	c.managers[agentID] = mgr
	rootCtx = c.rootCtx
	if ac, ok := c.contexts[agentID]; ok && ac != nil {
		bid := ac.browserCtxID
		c.mu.Unlock()
		return rootCtx, bid, nil
	}
	c.mu.Unlock()

	// Create the agent's owning browser context ON THE COORDINATOR'S root
	// context — via RAW CDP (target.CreateBrowserContext + target.CreateTarget
	// WithNewWindow(true)) rather than chromedp.WithNewBrowserContext().
	//
	// WHY (WebRTC build W1-A finding, verified against real Chrome 149 over
	// cdppipe — the SAME underlying CDP behavior the archive branch's
	// coordinator.go documents hitting on Chrome 151): chromedp's
	// WithNewBrowserContext() internally issues
	// target.CreateTarget("about:blank").WithBrowserContextID(bid) WITHOUT
	// WithNewWindow(true) (chromedp v0.15.1 chromedp.go:375-378). A freshly
	// created browser context has NO window yet, and on full-Chrome
	// --headless (new headless) over the CDP pipe that CreateTarget call
	// fails verbatim with "Failed to open new tab - no browser is open
	// (-32000)" — reproduced directly against this coordinator's real launch
	// path before this fix landed. WithNewWindow(true) gives the new context
	// its own window, which fixes it. This is a narrow, targeted fix for that
	// one CDP call — NOT the archive's broader "encoder-tab-in-default-
	// context" redesign (which additionally used WithBackground(true) to
	// avoid stealing an encoder tab's foreground slot; there is no encoder
	// tab in this coordinator, so that flag is deliberately omitted).
	//
	// Consequence: chromedp's own browserContextOwner auto-dispose-on-cancel
	// (set only inside its WithNewBrowserContext path, chromedp.go:367-373)
	// does NOT fire for a context built this way — ac.cancel() alone would
	// only close the tab, not the whole browser context/cookie partition.
	// RemoveAgent below therefore disposes explicitly via
	// disposeBrowserContextRaw (see its doc comment).
	rc := chromedp.FromContext(rootCtx)
	if rc == nil || rc.Browser == nil {
		return nil, "", fmt.Errorf("browser: coordinator: shared Chrome not ready while registering agent %q", agentID)
	}
	bid, bcErr := target.CreateBrowserContext().Do(cdp.WithExecutor(rootCtx, rc.Browser))
	if bcErr != nil {
		return nil, "", fmt.Errorf(
			"browser: coordinator: failed to create browser context for agent %q: %w",
			agentID,
			bcErr,
		)
	}
	// D17: pin the new window's size to the screencast cap (live.go) instead
	// of leaving it at Chrome's version/platform-dependent new-window
	// default. Without this, --window-size=1280,720
	// (chromeHardeningBaseFlags, exec_resolver.go) only ever sizes the
	// FIRST window Chrome opens at process start; every subsequent
	// per-agent window created here (one per Register call) got whatever
	// size Chrome picked on its own, then that size was cached for the
	// agent's whole lifetime (c.contexts, below) — reproduced in the field
	// as a live-browser panel that sometimes filled the panel and sometimes
	// shrank to a ~320x155 letterboxed rectangle, with no visible trigger
	// (just "which agent/session got created first"). WithWidth/WithHeight
	// requires newWindow:true (CreateTargetParams doc comment), already
	// satisfied by WithNewWindow(true) below — purely additive, keeps
	// window size and the screencast's own cap in lockstep so there is no
	// separate magic-number size to drift out of sync.
	// Start page rather than about:blank (see BrowserManager.StartPageURL):
	// a blank void reads as "the panel is broken", which is precisely the
	// confusion this surface does not need. Falls back to about:blank when no
	// start page is configured, preserving the historical behavior exactly.
	ctParams := target.CreateTarget(c.startPageURL()).
		WithBrowserContextID(bid).
		WithNewWindow(true).
		WithWidth(agentWindowWidth).
		WithHeight(agentWindowHeight)
	// createTargetParamsForTest (D17 test seam, nil in production) lets
	// tests assert the Width/Height pinning directly on the outgoing CDP
	// params, without depending on any particular Chrome build/platform's
	// new-window default actually differing when the fields are absent —
	// on the Chrome build this fix shipped from, headless new-window sizing
	// already happened to default to 1280x720, so a live-Chrome
	// window-bounds assertion alone could not distinguish fixed from
	// unfixed code. See TestCoordinator_Register_CreateTargetParams_PinsWindowToScreencastCap.
	if createTargetParamsForTest != nil {
		createTargetParamsForTest(ctParams)
	}
	tid, ctErr := ctParams.Do(cdp.WithExecutor(rootCtx, rc.Browser))
	if ctErr != nil {
		_ = target.DisposeBrowserContext(bid).Do(cdp.WithExecutor(rootCtx, rc.Browser))
		return nil, "", fmt.Errorf("browser: coordinator: failed to open first tab for agent %q: %w", agentID, ctErr)
	}
	agentCtx, agentCancel := chromedp.NewContext(rootCtx, chromedp.WithTargetID(tid))
	if err := chromedp.Run(agentCtx); err != nil {
		agentCancel()
		_ = target.DisposeBrowserContext(bid).Do(cdp.WithExecutor(rootCtx, rc.Browser))
		return nil, "", fmt.Errorf(
			"browser: coordinator: failed to attach browser context for agent %q: %w",
			agentID,
			err,
		)
	}

	c.mu.Lock()
	// A crash between the unlock above and now could have cleared c.contexts;
	// either way we install the freshly-created (live) context. If a concurrent
	// Register for the same agent raced, keep the first-installed one and cancel
	// the redundant duplicate to avoid leaking a context.
	if existing, ok := c.contexts[agentID]; ok && existing != nil {
		c.mu.Unlock()
		agentCancel()                   // discard our redundant context (closes its tab)...
		c.disposeBrowserContextRaw(bid) // ...and its browser context/partition (no auto-dispose — see above)
		return rootCtx, existing.browserCtxID, nil
	}
	c.contexts[agentID] = &agentBrowserContext{
		browserCtxID: bid,
		ctx:          agentCtx,
		cancel:       agentCancel,
	}
	c.mu.Unlock()

	logger.InfoCF("browser", "coordinator: created browser context for agent", map[string]any{
		"agent_id":       agentID,
		"browser_ctx_id": string(bid),
		"total_contexts": c.contextCount(),
		"total_managers": c.managerCount(),
	})
	return rootCtx, bid, nil
}

// Release drops an agent's manager ref on reload. Does NOT kill Chrome and does
// NOT dispose the agent's browser context (the coordinator owns it; it persists
// keyed by agentID so the next Register re-adopts it — cookies/localStorage/
// login survive a Settings save). Open TABS may be lost on reload (the manager
// drops its remote-allocator connection; targets are connection-scoped and
// agents reopen them). Returns the remaining registered-manager count.
//
// D4 invariant 3 (W1/C2/F-INFO-3): the reload path in pkg/agent/loop.go calls
// this — NOT manager.Shutdown() — so the coordinator's managers map is cleaned
// on every Settings save. Release internally calls the registered manager's
// dropConnection (connection-only teardown = Shutdown in coordinator mode,
// which never kills Chrome or disposes the context), making Release a full
// substitute for the old prior.Shutdown() reload call.
func (c *BrowserCoordinator) Release(agentID string) int {
	c.mu.Lock()
	mgr := c.managers[agentID]
	delete(c.managers, agentID)
	remaining := len(c.managers)
	stopping := c.shutdown
	c.mu.Unlock()

	// Drop the old manager's remote-allocator connection (closes its WS +
	// detaches its tabs) WITHOUT touching the Chrome process or the agent's
	// browser context. This is connection teardown only — safe to call here.
	// Skip while shutting down: Shutdown tears everything down itself.
	if mgr != nil && !stopping {
		mgr.dropConnection()
	}
	logger.DebugCF("browser", "coordinator: released agent (context preserved)", map[string]any{
		"agent_id":       agentID,
		"remaining_mgrs": remaining,
	})
	return remaining
}

// RemoveAgent disposes a REMOVED agent's browser context + drops its manager
// ref (W4). Unlike Release (reload — the context is preserved for re-adoption),
// RemoveAgent frees that agent's cookie/localStorage partition. Called by
// registerSharedTools for agentIDs present in the coordinator's map but no
// longer in cfg.Agents. Safe to call for an agentID that was never registered
// (no-op).
func (c *BrowserCoordinator) RemoveAgent(agentID string) {
	c.mu.Lock()
	mgr := c.managers[agentID]
	delete(c.managers, agentID)
	ac := c.contexts[agentID]
	delete(c.contexts, agentID)
	stopping := c.shutdown
	c.mu.Unlock()

	if mgr != nil && !stopping {
		mgr.dropConnection()
	}
	if ac != nil && !stopping {
		// Cancel first (closes the agent's tab / releases chromedp's
		// bookkeeping goroutines for this context), then explicitly dispose
		// the browser context via raw CDP. This is the difference from
		// Release: a truly-removed agent's partition is freed, not preserved.
		//
		// NOT chromedp's implicit browserContextOwner auto-dispose-on-cancel:
		// Register (W1-A) creates this context via raw CDP
		// (target.CreateBrowserContext + CreateTarget WithNewWindow(true), a
		// -32000 workaround — see Register's doc comment), not
		// chromedp.WithNewBrowserContext(), so that chromedp-internal flag is
		// never set and cancel() alone would leak the partition.
		if ac.cancel != nil {
			ac.cancel()
		}
		c.disposeBrowserContextRaw(ac.browserCtxID)
	}
	logger.InfoCF("browser", "coordinator: removed agent (context disposed)", map[string]any{
		"agent_id": agentID,
	})
}

// disposeBrowserContextRaw explicitly disposes a browser context via raw CDP
// (target.DisposeBrowserContext). Required because Register (W1-A) creates
// each agent's browser context via raw CDP rather than
// chromedp.WithNewBrowserContext() (see Register's doc comment for why), so
// chromedp's own browserContextOwner auto-dispose-on-cancel never fires for
// these contexts — ac.cancel() alone only closes the tab, not the whole
// context/cookie partition. Runs with c.mu NOT held. A no-op (best-effort
// WARN log on failure) when the shared Chrome is no longer live or bid is
// empty — nothing to dispose once the process is gone.
func (c *BrowserCoordinator) disposeBrowserContextRaw(bid cdp.BrowserContextID) {
	if bid == "" {
		return
	}
	c.mu.Lock()
	rootCtx := c.rootCtx
	c.mu.Unlock()
	if rootCtx == nil {
		return
	}
	rc := chromedp.FromContext(rootCtx)
	if rc == nil || rc.Browser == nil {
		return
	}
	if err := target.DisposeBrowserContext(bid).Do(cdp.WithExecutor(rootCtx, rc.Browser)); err != nil {
		logger.WarnCF("browser", "coordinator: failed to dispose browser context", map[string]any{
			"browser_ctx_id": string(bid),
			"error":          err.Error(),
		})
	}
}

// RegisteredAgents returns a snapshot of agentIDs currently in the coordinator's
// managers map (W4: the reload-removal diff iterates this). NOTE: registration
// is lazy (a manager is added only on its first ensureStarted → Register), so
// this undercounts agents that exist in config but have never used a browser
// tool — the reload-removal diff in registerSharedTools keys off
// al.browserMgrs instead, which is the authoritative per-agent set.
func (c *BrowserCoordinator) RegisteredAgents() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.managers))
	for id := range c.managers {
		out = append(out, id)
	}
	return out
}

// ApplyRuntimeConfig applies the runtime-cheap portions of an updated
// BrowserConfig to a coordinator that survived a reload (MED-1).
// headless/exec_path/profile_dir are
// launch-time properties of the ALREADY-RUNNING Chrome and cannot take effect
// without restarting the gateway; changes to them are warn-logged as "applies
// after gateway restart" so an operator is not silently misled. CRIT-002 stays
// intact: the coordinator itself is never rebuilt on reload.
func (c *BrowserCoordinator) ApplyRuntimeConfig(newCfg BrowserConfig) {
	c.mu.Lock()
	oldCfg := c.cfg
	c.mu.Unlock()
	if oldCfg.Headless != newCfg.Headless {
		logger.WarnCF(
			"browser",
			"coordinator: tools.browser.headless changed on reload — applies after gateway restart (Chrome already running)",
			map[string]any{
				"old": oldCfg.Headless,
				"new": newCfg.Headless,
			},
		)
	}
	if oldCfg.ExecPath != newCfg.ExecPath {
		logger.WarnCF(
			"browser",
			"coordinator: tools.browser.exec_path changed on reload — applies after gateway restart (Chrome already running)",
			nil,
		)
	}
	if oldCfg.ProfileDir != newCfg.ProfileDir {
		logger.WarnCF(
			"browser",
			"coordinator: tools.browser.profile_dir changed on reload — applies after gateway restart (Chrome already running)",
			nil,
		)
	}
	// ADR-052 D2/M1 + SPEC-002: policy flips invalidate the resolved-path
	// cache immediately. A cached PATH result can otherwise survive a toggle
	// until the resolution negative TTL expires, defeating the new policy.
	if oldCfg.PreferPackaged != newCfg.PreferPackaged || oldCfg.TrustPathChrome != newCfg.TrustPathChrome {
		c.execPath.invalidate()
		logger.InfoCF(
			"browser",
			"coordinator: browser resolution policy changed; exec-path cache invalidated, next resolution uses the new policy",
			map[string]any{
				"prefer_packaged_old":   oldCfg.PreferPackaged,
				"prefer_packaged_new":   newCfg.PreferPackaged,
				"trust_path_chrome_old": oldCfg.TrustPathChrome,
				"trust_path_chrome_new": newCfg.TrustPathChrome,
			},
		)
	}
	// Persist the new config on the coordinator so subsequent
	// reloads compare against the latest-applied state. Without this,
	// a back-to-back reload with the same config would re-log the
	// change (because oldCfg would be the original, not the latest).
	c.mu.Lock()
	c.cfg = newCfg
	c.mu.Unlock()
}

// captureSharedContextResolved returns the effective ADR-048
// capture_shared_context value: the OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT
// env var, if set to a non-empty string, is an explicit override ("1" ->
// true, anything else -> false) — kept for tests and operators who need to
// force a value without touching config.json; otherwise the configured
// c.captureSharedContext field (set via SetCaptureSharedContext) applies.
// Both Register (actual context-placement decision) and
// CaptureSharedContextEnabled (capability classification) call this so the
// two can never disagree about which mode is actually in effect.
func (c *BrowserCoordinator) captureSharedContextResolved() bool {
	if envOverride := os.Getenv("OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT"); envOverride != "" {
		return envOverride == "1"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.captureSharedContext
}

// SetCaptureSharedContext updates the ADR-048 shared-default-context capture
// mode (config field: tools.browser.capture_shared_context). Called by the
// gateway's agent-loop seed wiring on every fresh-seed and reload pass so the
// coordinator's decision is always driven by config, never a bare
// os.Getenv. A flip after an agent has already registered does not
// retroactively move that agent's session between browser contexts —
// Register only decides context placement once, at that agent's first
// registration — but it DOES take effect for any agent that registers
// afterward (new agent, or a coordinator that hasn't seen this agentID
// yet), so unlike headless/exec_path/profile_dir this is not purely
// restart-only and is not warn-logged as such.
func (c *BrowserCoordinator) SetCaptureSharedContext(v bool) {
	c.mu.Lock()
	old := c.captureSharedContext
	c.captureSharedContext = v
	c.mu.Unlock()
	if old != v {
		logger.InfoCF("browser", "coordinator: tools.browser.capture_shared_context updated", map[string]any{
			"old": old,
			"new": v,
		})
	}
}

// CaptureSharedContextEnabled reports the effective ADR-048
// capture_shared_context value — the capability classifier
// (capability.go's CaptureVideoCapability, ADR-048 condition 3) reads this
// to decide whether WebRTC capture can possibly succeed for an agent
// attached to this coordinator.
func (c *BrowserCoordinator) CaptureSharedContextEnabled() bool {
	return c.captureSharedContextResolved()
}

// RootContext returns the coordinator's SHARED, session-independent root
// chromedp context (the same rootCtx Register returns) — the context every
// agent's browser context is a CHILD of, over the one multiplexed CDP pipe.
// Used by callers that only hold a *BrowserManager (not the coordinator
// itself).
//
// ok is false when no Chrome is currently live — never launched yet, or torn
// down by Shutdown/a crash awaiting relaunch. Callers MUST treat ok=false as
// "no shared root available right now" and degrade cleanly rather than using
// a nil/stale context.
func (c *BrowserCoordinator) RootContext() (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rootCtx == nil {
		return nil, false
	}
	return c.rootCtx, true
}

// PID returns the shared Chrome process pid (0 if none live) — the R1/R3
// regression tests observe reload-no-kill + Close-only-kill through this.
func (c *BrowserCoordinator) PID() int {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// KillCount returns the number of times Shutdown has killed the Chrome process
// (FR-008 / R3). 0 across reload/Release; 1 after the first Close.
func (c *BrowserCoordinator) KillCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.killCount
}

// loadExtensionDefaultTimeout bounds LoadExtension's Extensions.loadUnpacked
// CDP call when the caller's own ctx carries no deadline of its own (see
// boundedCallContext). 20s matches cdppipe's own defaultDialTimeout (the
// bound on actually launching + dialing Chrome, which by the time
// LoadExtension runs has already succeeded) and capture_session.go's sibling
// captureStartTimeout, which bounds the very next step (encoder-page
// inject+navigate) in the one production flow that calls this with a
// meaningful caller ctx (defaultEncoderStarter) — that constant's own doc
// comment describes its budget as "generous for a cold managed-Chrome
// extension load", i.e. sized for exactly the operation this constant now
// bounds.
const loadExtensionDefaultTimeout = 20 * time.Second

// boundedCallContext returns a context to use for a one-shot bounded CDP
// call: ctx unchanged (wrapped in a no-op cancel for a uniform, always-safe
// `defer cancel()`) when it already carries a deadline — honoring the
// caller's own bound, however short or long — otherwise a child of ctx
// bounded by def. ctx must not be nil (callers normalize it first).
func boundedCallContext(ctx context.Context, def time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, def)
}

// LoadExtension installs the unpacked extension at c.cfg.ExtensionDir into the
// shared Chrome via CDP Extensions.loadUnpacked — requires the running Chrome
// to have been launched with --remote-debugging-pipe (always true here) and
// --enable-unsafe-extension-debugging (managedExecAllocatorOpts adds it
// whenever cfg.ExtensionID is set). Two callers: launchChrome's best-effort
// auto-load below (fires once, right after the shared Chrome first comes up),
// and capture_session.go's defaultEncoderStarter (fix-wave comment
// correction — an earlier version of this doc comment claimed "nothing in
// this package calls it except launchChrome's ... auto-load," which stopped
// being true once WebRTC capture landed: defaultEncoderStarter re-checks
// coord.LoadedExtensionID() on every capture-session Start() and calls this
// again whenever it doesn't match captureext.ExtensionID — e.g. a crash-
// relaunch that lost the previously-loaded extension state). Returns the
// extension id Chrome assigned, which should match cfg.ExtensionID when the
// manifest pins a "key" (wave-plan key decision 1).
func (c *BrowserCoordinator) LoadExtension(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	rootCtx := c.rootCtx
	dir := c.cfg.ExtensionDir
	c.mu.Unlock()
	if rootCtx == nil {
		return "", fmt.Errorf("browser: coordinator: cannot load extension — shared Chrome is not live")
	}
	if dir == "" {
		return "", fmt.Errorf("browser: coordinator: no ExtensionDir configured")
	}
	rc := chromedp.FromContext(rootCtx)
	if rc == nil || rc.Browser == nil {
		return "", fmt.Errorf("browser: coordinator: shared Chrome browser handle unavailable")
	}

	// Bound the CDP round trip and HONOR the caller's own context — before
	// this fix, ctx was accepted as a parameter but referenced NOWHERE in
	// this function's body: the actual CDP call ran against c.rootCtx (the
	// coordinator's long-lived, deadline-free context) instead, so a
	// caller-supplied deadline/cancellation (e.g. capture_session.go's
	// defaultEncoderStarter, which forwards its own request-scoped ctx) had
	// no effect whatsoever, and the call could only ever be unblocked by the
	// coordinator itself shutting down.
	//
	// boundedCallContext(ctx, loadExtensionDefaultTimeout) honors ctx as-is
	// when it already carries a deadline (however short or long the caller
	// chose), and falls back to the 20s default otherwise (e.g.
	// launchChrome's best-effort auto-load below, which passes
	// context.Background()). Unlike createTab's target-attach (manager.go's
	// runFirstAttach), Extensions.loadUnpacked has no long-lived background
	// goroutine bound to its call context — chromedp.Browser.execute's
	// per-command listener (browser.go) is scoped to just this one round
	// trip — so wrapping ctx directly in a plain deadline is safe here; no
	// racing-goroutine trick is needed.
	callCtx, cancel := boundedCallContext(ctx, loadExtensionDefaultTimeout)
	defer cancel()
	// WithEnableInIncognito(true) (WebRTC build W2-A, corrected per ADR-048):
	// every per-agent browser context this coordinator creates is a raw CDP
	// target.CreateBrowserContext — which cdproto's own doc comment
	// describes as "similar to an incognito profile" — never the DEFAULT
	// browser context Extensions.loadUnpacked otherwise scopes to. Without
	// this flag the extension is confined to the default context and
	// cannot even SEE (chrome.tabs.query) a tab living in a per-agent CDP
	// context, let alone capture one.
	//
	// This flag grants VISIBILITY only, not capturability: with it,
	// encoder.js's chrome.tabs.query resolves an agent's tab even when that
	// tab lives in its own CDP-created context — but ADR-048 proved (real
	// Chrome 150) that chrome.tabCapture still CANNOT capture a tab living
	// in a CDP-created browser context at all ("Invalid tab specified."),
	// regardless of this flag. Separately, the encoder PAGE itself cannot
	// even be HOSTED inside a CDP-created context (chrome-extension://
	// navigation there fails net::ERR_BLOCKED_BY_CLIENT) — which is why
	// capture_session.go's defaultEncoderStarter creates the encoder target
	// in the coordinator's DEFAULT context, never inside any agent's own
	// context/window.
	//
	// Net effect: this flag alone does not make capture work. Capture only
	// succeeds when the AGENT's own session also shares the default context
	// — tools.browser.capture_shared_context (ADR-048 condition 1, see
	// Register's doc comment above and BrowserCoordinator.
	// CaptureSharedContextEnabled) — at which point tabCapture is
	// operating on a tab that is ALSO in the default context, sidestepping
	// the CDP-context capture restriction entirely. enableInIncognito
	// remains useful even then: it is what lets the extension's
	// chrome.tabs.query resolve the RIGHT tab (the attached agent's) among
	// whichever tabs currently live in the default context.
	id, err := extensions.LoadUnpacked(dir).WithEnableInIncognito(true).Do(cdp.WithExecutor(callCtx, rc.Browser))
	if err != nil {
		// Name the bound when it was OURS that fired. A bare "context deadline
		// exceeded" leaves an operator unable to tell a wedged CDP transport
		// from this call's own budget expiring, or to know how long it waited
		// — and this call sits on the WebRTC cold-start critical path, where
		// that distinction is exactly what a timeout investigation needs.
		// Only claim the default bound when the caller supplied no earlier
		// deadline of its own; otherwise the caller's budget is what expired.
		if errors.Is(err, context.DeadlineExceeded) {
			if _, callerHadDeadline := ctx.Deadline(); !callerHadDeadline {
				return "", fmt.Errorf(
					"browser: coordinator: Extensions.loadUnpacked timed out after %s (Chrome may be unresponsive): %w",
					loadExtensionDefaultTimeout, err,
				)
			}
			return "", fmt.Errorf(
				"browser: coordinator: Extensions.loadUnpacked exceeded the caller's deadline (Chrome may be unresponsive): %w",
				err,
			)
		}
		return "", fmt.Errorf("browser: coordinator: Extensions.loadUnpacked failed: %w", err)
	}
	c.mu.Lock()
	c.loadedExtensionID = id
	c.mu.Unlock()
	return id, nil
}

// rootContext returns the shared Chrome's pipe root context (nil when Chrome
// is not live). W3 e2e: the WebRTC capture EXTENSION'S ENCODER PAGE (the CDP
// target capture_session.go's defaultEncoderStarter navigates to
// chrome-extension://<id>/encoder.html — not to be confused with the
// unrelated /api/v1/browser/capture-ingest HTTP/WS endpoint, which is a
// gateway route, not a CDP context) MUST be created as a child of THIS
// context — i.e. in the DEFAULT browser context — because Chrome refuses to
// load chrome-extension:// pages inside CDP-created browser contexts
// (net::ERR_BLOCKED_BY_CLIENT) even with Extensions.loadUnpacked's
// enableInIncognito:true, which only grants the extension VISIBILITY/capture
// of those contexts' tabs, not page hosting inside them (verified against
// real Chrome 150 — see capture_session.go's defaultEncoderStarter).
func (c *BrowserCoordinator) rootContext() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rootCtx
}

// LoadedExtensionID returns the id of the last successfully loaded extension
// (empty if none has loaded yet). WebRTC build hook (W1-A item 3).
func (c *BrowserCoordinator) LoadedExtensionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadedExtensionID
}

// Shutdown disposes all contexts + kills the Chrome process. Called ONLY by
// gateway Close() — the SOLE process-kill path (FR-008 / MIN-008). Idempotent.
func (c *BrowserCoordinator) Shutdown() {
	c.mu.Lock()
	if c.shutdown {
		c.mu.Unlock()
		return
	}
	c.shutdown = true
	// Cancel every agent's owning context (closes its tab / releases
	// chromedp's bookkeeping goroutines). No explicit
	// target.disposeBrowserContext CDP round-trip here (unlike RemoveAgent):
	// the pipe allocator is about to be killed below (rootCancel + the
	// process SIGKILL), which destroys every browser context along with the
	// whole Chrome process — an explicit per-context dispose call would be a
	// wasted CDP round-trip against a process already on its way down.
	for id, ac := range c.contexts {
		if ac != nil && ac.cancel != nil {
			ac.cancel()
		}
		delete(c.contexts, id)
	}
	rootCancel := c.rootCancel
	lockFile := c.lockFile
	c.rootCancel = nil
	c.rootCtx = nil
	c.lockFile = nil
	c.cmd = nil
	c.launched = false
	c.killCount++
	c.mu.Unlock()

	// Kill the Chrome process by canceling the pipe allocator context:
	// cdppipe's CancelFunc cancels the chromedp context, closes the pipe
	// (Chrome observes EOF and exits), and reaps the process. This is the ONE
	// teardown handle in the pipe model (the pre-pipe rootCancel+allocCancel
	// pair is gone).
	if rootCancel != nil {
		rootCancel()
	}
	// Release the single-launch lock so a fresh gateway can take it (CRIT-001).
	releaseLaunchLock(lockFile)
	logger.InfoCF(
		"browser",
		"coordinator: shared Chrome shut down (process killed, launch lock released)",
		map[string]any{
			"kill_count": c.killCount,
		},
	)
}

// WarmUp ensures the shared Chrome process is launched and ready (CDP pipe
// live, and — when tools.browser.* configured an ExtensionDir/ExtensionID —
// the capture extension loaded, via launchChrome's existing best-effort
// auto-load) WITHOUT registering any agent or creating a per-agent browser
// context. This is the gateway's boot-time warm-up hook: instead of leaving
// Chrome launch, first tab, and extension load entirely lazy until an
// agent's first browser tool call (the unbounded cold start recorded at
// 30-60s historically), the gateway calls this once at boot so the first
// real interaction finds Chrome already up.
//
// Thin public wrapper over the exact same ensureLaunched every Register call
// already funnels through: single-flight (a concurrent WarmUp racing a real
// Register serializes on the same c.launching/c.launchDone latch — whichever
// wins launches, the other observes c.launched), idempotent (a no-op once
// Chrome is already live), and panic-safe (ensureLaunched's own deferred
// cleanup). A per-agent Chrome pool (one Chrome per agent) is explicitly out
// of scope here — tracked separately as issue #570; this warms only the ONE
// shared, gateway-scoped Chrome this file documents (D1/D4).
func (c *BrowserCoordinator) WarmUp(ctx context.Context) error {
	return c.ensureLaunched(ctx)
}

// --- internals ------------------------------------------------------------

// contextCount / managerCount are best-effort diagnostic reads for logging.
// Callers may call them WITHOUT holding c.mu (they take it themselves); they
// MUST NOT be called while the caller holds c.mu (would deadlock).
func (c *BrowserCoordinator) contextCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.contexts)
}

func (c *BrowserCoordinator) managerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.managers)
}

// ensureLaunched guarantees the shared Chrome is live (single-flight). Safe to
// call concurrently from many Register callers and from the crash-relaunch
// goroutine. Releases c.mu across the blocking launch. Returns nil if Chrome is
// already live or was just launched by this/another goroutine.
func (c *BrowserCoordinator) ensureLaunched(ctx context.Context) error {
	c.mu.Lock()
	if c.launched {
		c.mu.Unlock()
		return nil
	}
	if c.shutdown {
		c.mu.Unlock()
		return fmt.Errorf("browser: coordinator is shut down — cannot launch Chrome")
	}
	// Single-flight: if another goroutine is already launching, wait for it.
	for c.launching {
		c.launchDone.Wait()
		if c.launched {
			c.mu.Unlock()
			return nil
		}
		if c.shutdown {
			c.mu.Unlock()
			return fmt.Errorf("browser: coordinator shut down during Chrome launch")
		}
		// Loop: re-check launched (the launcher may have failed; try once more
		// as the designated launcher).
	}
	if c.launched { // re-check after the loop
		c.mu.Unlock()
		return nil
	}
	c.launching = true
	c.mu.Unlock()

	// CRIT-2 (panic-safety): c.launching MUST always be cleared + c.launchDone
	// broadcast, even if launchChrome panics — otherwise every future Register
	// deadlocks on c.launchDone.Wait(). The cleanup runs via defer so a panic
	// in launchChrome (chromedp internals, a nil deref in a CDP handler) can
	// never wedge the single-flight latch. launchChrome's own chromedp.Run-
	// failure cleanup (rootCancel/allocCancel on its locals) is separate and
	// still runs for the ordinary error path.
	defer func() {
		c.mu.Lock()
		c.launching = false
		c.launchDone.Broadcast() // wake all waiters regardless of outcome
		c.mu.Unlock()
	}()

	if err := c.launchChrome(ctx); err != nil {
		return err
	}

	// CRIT-1 (Shutdown races in-flight launch → orphan Chrome): there is a
	// window between the pipe launch succeeding (Chrome alive) and
	// launchChrome installing c.rootCancel/c.lockFile. If Shutdown runs in
	// that window it sees nil cancels, logs a FALSE "process killed", and
	// returns — then launchChrome installs the LIVE Chrome's cancel AFTER
	// Shutdown, producing an unkillable orphan. Close the window: re-check
	// c.shutdown now (launchChrome has installed the cancel + lock by the
	// time it returned nil). If Shutdown won the race, tear down the
	// just-launched Chrome + release the lock ourselves and return an error;
	// do NOT set c.launched=true. (cdppipe's CancelFunc is idempotent, so a
	// double-cancel is harmless.)
	c.mu.Lock()
	if c.shutdown {
		rootCancel := c.rootCancel
		lockFile := c.lockFile
		c.rootCtx = nil
		c.rootCancel = nil
		c.lockFile = nil
		c.cmd = nil
		c.mu.Unlock()
		if rootCancel != nil {
			rootCancel()
		}
		releaseLaunchLock(lockFile)
		return fmt.Errorf("browser: shared Chrome launch aborted by concurrent shutdown")
	}
	c.launched = true
	c.mu.Unlock()
	return nil
}

// launchChrome does the blocking work of starting the one shared Chrome over
// the CDP pipe (CRIT-001 — no TCP port). Called with c.mu NOT held. On success
// it holds the single-launch lock and populates c.rootCtx/rootCancel/cmd/
// lockFile and writes the ownership marker. On failure it tears down any
// half-built state (including releasing the lock).
func (c *BrowserCoordinator) launchChrome(ctx context.Context) error {
	// Single-launch atomicity via an O_EXCL/flock lockfile (CRIT-001): the
	// removed net.Listen(":9223") bind was the atomic guard, and the CDP pipe
	// has no port, so a cross-process lockfile takes its place. The ownership
	// marker stays the identity layer (pid + product) — see takeLaunchLock. A
	// held lock with a live omnipus pid means a prior gateway's Chrome is
	// still running.
	lockFile, err := c.takeLaunchLock()
	if err != nil {
		return err
	}
	// Release the lock on ANY failure below; success clears this flag so the
	// coordinator keeps the lock for its lifetime.
	releaseLockOnErr := true
	defer func() {
		if releaseLockOnErr {
			releaseLaunchLock(lockFile)
		}
	}()

	if err = os.MkdirAll(c.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("browser: coordinator: cannot create profile directory %s: %w", c.cfg.ProfileDir, err)
	}
	cleanStaleSingletons(c.cfg.ProfileDir)

	// Resolve the Chromium binary (may shell out to probe PATH candidates or
	// download Chrome-for-Testing — runs with c.mu released, per the file doc).
	execPath, err := c.execPath.resolve(ctx, c.cfg)
	if err != nil {
		return fmt.Errorf("browser: coordinator: cannot locate chromium: %w", err)
	}

	cmdline := managedExecAllocatorOpts(c.cfg, chromeMajorVersion(ctx, execPath))

	// Launch over the pipe (fail closed — err reports launch + CDP
	// connectivity failure directly). The launcher is a seam so tests never
	// spawn real Chrome.
	launch := c.pipeLauncher
	if launch == nil {
		launch = launchManagedPipe
	}
	res, err := launch(ctx, execPath, pipeLaunchConfig{
		args:        cmdline.Args,
		env:         cmdline.Env,
		userDataDir: c.cfg.ProfileDir,
	})
	if err != nil {
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return fmt.Errorf("browser: coordinator: failed to launch shared Chrome over the CDP pipe: %w", err)
	}

	// PID from the captured *exec.Cmd — chromedp.Browser.Process() is nil
	// under the pipe allocator (cdppipe/doc.go), so the *exec.Cmd is the ONLY
	// PID source.
	pid := 0
	if res.cmd != nil && res.cmd.Process != nil {
		pid = res.cmd.Process.Pid
	}
	product := readBrowserProduct(res.rootCtx)
	// MED-2: a pid of 0 (Process()==nil — e.g. cdppipe couldn't capture the
	// cmd handle) MUST NOT be written as a marker: takeLaunchLock treats a
	// held lock whose marker pid is dead/zero as stale (removable), so a 0
	// marker could let a second launch clobber the lock while our Chrome is
	// live. The hard guarantee is the flock; the marker is the identity/
	// diagnostic layer.
	if pid > 0 {
		if werr := c.writeOwnershipMarker(pid, product); werr != nil {
			logger.WarnCF("browser", "coordinator: failed to write ownership marker (continuing)", map[string]any{
				"error": werr.Error(),
			})
		}
	} else {
		logger.WarnCF(
			"browser",
			"coordinator: could not capture Chrome pid over the pipe — ownership marker NOT written (preflight identity check disabled until a pid is available)",
			nil,
		)
	}

	c.mu.Lock()
	c.rootCtx = res.rootCtx
	c.rootCancel = res.cancel
	c.cmd = res.cmd
	c.lockFile = lockFile
	managersCopy := make([]*BrowserManager, 0, len(c.managers))
	for _, m := range c.managers {
		managersCopy = append(managersCopy, m)
	}
	sharedBrowser := res.browser
	c.mu.Unlock()

	// The coordinator now owns the lock for its lifetime — do NOT release on
	// the deferred error path (only Shutdown / crash-relaunch releases it).
	releaseLockOnErr = false

	// WebRTC build W1-A item 3: best-effort auto-load of the configured
	// extension (empty ExtensionDir/ExtensionID today — no caller sets them
	// yet, so this is inert until the gateway wires the capture extension in
	// a later wave). A load failure never fails the Chrome launch itself —
	// browsing tools must keep working even if the optional extension can't
	// load.
	if c.cfg.ExtensionDir != "" && c.cfg.ExtensionID != "" {
		if _, lerr := c.LoadExtension(context.Background()); lerr != nil {
			logger.WarnCF(
				"browser",
				"coordinator: failed to auto-load configured extension (continuing without it)",
				map[string]any{
					"error": lerr.Error(),
				},
			)
		}
	}

	// Arm the launcher-wait crash detector (grill M1 / R2). browser.LostConnection
	// closes when the pipe drops — the canonical, race-free signal that the Chrome
	// process has died (cdppipe's own goroutine also watches it + reaps the
	// process; we do NOT call cmd.Wait() ourselves to avoid racing it).
	if sharedBrowser != nil {
		go c.watchForCrash(sharedBrowser, managersCopy)
	}

	logger.InfoCF("browser", "coordinator: shared Chrome launched (CDP over pipe, no TCP port)", map[string]any{
		"pid":       pid,
		"exec_path": execPath,
		"product":   product,
	})
	return nil
}

// watchForCrash is the launcher-wait crash detector (grill M1). It blocks on
// the shared Chrome's LostConnection channel; when the transport drops (process
// exited / killed), it marks Chrome dead, resets every connector manager so its
// next ensureStarted re-asks the coordinator, and proactively relaunches so
// coordinator.PID() is a fresh live pid within the recovery bound T (CRIT-001:
// recovery is into FRESH empty contexts — prior per-agent cookies/login are lost
// by definition since CDP contexts are in-memory).
//
// HIGH-1/N2: this blocks until chromedp closes LostConnection on transport
// drop. There is no fallback if it doesn't — chromedp's allocator goroutine
// owns cmd.Wait and closes LostConnection as soon as the process exits, so this
// is the canonical, race-free signal. (A bounded pidAlive poll OR'd via select
// was considered as defense-in-depth but adds complexity for a path chromedp
// already covers; the honest single-channel block is preferred.)
func (c *BrowserCoordinator) watchForCrash(b *chromedp.Browser, currentManagers []*BrowserManager) {
	<-b.LostConnection

	c.mu.Lock()
	if c.shutdown || !c.launched {
		c.mu.Unlock()
		return // shutting down, or already marked dead by a prior detection
	}
	c.launched = false
	oldCancel := c.rootCancel
	oldLock := c.lockFile
	c.rootCtx = nil
	c.rootCancel = nil
	c.lockFile = nil
	c.cmd = nil
	// CRIT-001: all agent contexts are gone with the process. Clear them so the
	// next Register creates FRESH empty contexts (not stale, dead ids).
	for id := range c.contexts {
		delete(c.contexts, id)
	}
	managersCopy := make([]*BrowserManager, 0, len(c.managers))
	for _, m := range c.managers {
		managersCopy = append(managersCopy, m)
	}
	c.mu.Unlock()

	// Cancel the dead pipe allocator (idempotent — cdppipe's own
	// LostConnection goroutine already fired it; this just reaps
	// deterministically) and RELEASE the single-launch lock BEFORE
	// relaunching. The relaunch below re-acquires the lock via a fresh open
	// FD; keeping the old FD's flock held would make takeLaunchLock
	// self-deadlock (two open descriptions of the same file contend, even in
	// one process).
	if oldCancel != nil {
		oldCancel()
	}
	releaseLaunchLock(oldLock)

	logger.WarnCF("browser", "coordinator: shared Chrome connection lost — resetting connectors + relaunching", nil)

	// Reset every connector manager so its started latch drops and its next
	// ensureStarted re-registers (which blocks on the relaunch below).
	for _, mgr := range managersCopy {
		if mgr != nil {
			mgr.invalidateConnection()
		}
	}

	// Proactive relaunch: get a fresh live PID without waiting for a tool call,
	// bounded by the launch timeout. Ignore the error here — if relaunch fails,
	// the next Register's ensureLaunched will retry + surface the error to the
	// calling tool (honest, not silent).
	relaunchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.ensureLaunched(relaunchCtx); err != nil {
		logger.ErrorCF(
			"browser",
			"coordinator: proactive relaunch after crash failed (next tool call will retry)",
			map[string]any{
				"error": err.Error(),
			},
		)
	}
}

// lockPath is the single-launch lockfile (CRIT-001). It lives in the profile
// dir so it shares the shared Chrome's directory lifecycle.
func (c *BrowserCoordinator) lockPath() string {
	return filepath.Join(c.cfg.ProfileDir, "shared-chrome.lock")
}

// takeLaunchLock acquires the exclusive shared-Chrome single-launch lock
// (CRIT-001), replacing the removed net.Listen(":9223") atomic guard. Returns
// the held *os.File the caller keeps open for the coordinator's lifetime (and
// releases via releaseLaunchLock). Runs with c.mu NOT held.
//
// Ownership is proven WITHOUT a port: a held lock whose ownership marker names
// a LIVE omnipus pid means a prior gateway's Chrome is still running (rejected
// with a clear error). A held lock with a missing/dead-pid marker is a stale
// lockfile left by a crashed prior process (only reachable off Unix, where
// flock does not auto-release) — it is cleared and re-acquired once. The
// pre-pipe "foreign Chrome squatting our port" case is gone: nothing but an
// omnipus coordinator ever locks this file (it lives inside our own profile
// dir), so a held-but-unidentifiable lock always means "stale", never
// "foreign", and is safe to clear rather than reject.
func (c *BrowserCoordinator) takeLaunchLock() (*os.File, error) {
	path := c.lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("browser: coordinator: cannot create lock directory for %s: %w", path, err)
	}

	f, ok, err := acquireLaunchLock(path)
	if err != nil {
		return nil, fmt.Errorf("browser: coordinator: cannot open shared-Chrome launch lock %s: %w", path, err)
	}
	if ok {
		return f, nil
	}

	// Lock held. Prove the holder identity via the ownership marker (the
	// marker is the identity layer now the port is gone). A live omnipus pid
	// → a prior gateway's Chrome is genuinely still running.
	pid, owner, markerErr := c.readOwnershipMarker()
	if markerErr == nil && owner == ownershipMarkerOwner && pid > 0 && pidAlive(pid) {
		return nil, fmt.Errorf(
			"browser: the shared-Chrome launch lock %s is held by a prior omnipus gateway's Chrome (pid %d) still running — "+
				"stop that gateway/process before starting a new one",
			path,
			pid,
		)
	}

	// Marker missing or its pid is dead → a stale lockfile from a crashed
	// process. Clear it and retry once (a no-op on Unix, where flock
	// auto-releases so the first acquire would already have succeeded).
	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		return nil, fmt.Errorf("browser: coordinator: cannot clear stale launch lock %s: %w", path, rmErr)
	}
	f, ok, err = acquireLaunchLock(path)
	if err != nil {
		return nil, fmt.Errorf("browser: coordinator: cannot re-acquire launch lock %s: %w", path, err)
	}
	if !ok {
		return nil, fmt.Errorf("browser: the shared-Chrome launch lock %s is held by another live process", path)
	}
	return f, nil
}

// cleanStaleSingletons removes Chromium's stale SingletonLock/Cookie/Socket
// symlinks left behind by an ungraceful exit, mirroring manager.go's managed-
// mode launch path (the coordinator now owns the profile dir).
func cleanStaleSingletons(profileDir string) {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		path := filepath.Join(profileDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("browser", "coordinator: failed to remove stale Chromium singleton file", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
		}
	}
}

// readBrowserProduct best-effort reads the browser's product/version string via
// CDP, for the ownership marker (diagnostics). Returns "" on any failure.
func readBrowserProduct(rootCtx context.Context) string {
	ctx, cancel := context.WithTimeout(rootCtx, 3*time.Second)
	defer cancel()
	var ver string
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, product, _, _, _, gerr := browser.GetVersion().Do(ctx)
		if gerr == nil {
			ver = product
		}
		return nil
	}))
	return ver
}

// --- ownership marker (grill M2) ------------------------------------------

// ownershipMarker is the JSON shape persisted at markerPath() (no wire format —
// an internal on-disk file, not a gateway/SPA boundary type).
type ownershipMarker struct {
	PID     int    `json:"pid"`
	Owner   string `json:"owner"`
	Product string `json:"product,omitempty"`
	Created int64  `json:"created_unix"`
}

func (c *BrowserCoordinator) markerPath() string {
	return filepath.Join(c.homeDir, "browser", "shared-chrome.pid")
}

func (c *BrowserCoordinator) writeOwnershipMarker(pid int, product string) error {
	dir := filepath.Dir(c.markerPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create marker dir: %w", err)
	}
	m := ownershipMarker{
		PID:     pid,
		Owner:   ownershipMarkerOwner,
		Product: product,
		Created: time.Now().Unix(),
	}
	data, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	if err := os.WriteFile(c.markerPath(), data, 0o600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

func (c *BrowserCoordinator) readOwnershipMarker() (pid int, owner string, err error) {
	data, rerr := os.ReadFile(c.markerPath())
	if rerr != nil {
		return 0, "", rerr
	}
	var m ownershipMarker
	if jerr := json.Unmarshal(data, &m); jerr != nil {
		return 0, "", jerr
	}
	return m.PID, m.Owner, nil
}

// pidAlive reports whether the given pid is currently a running process. On
// non-Unix platforms it conservatively reports true (the marker check is a
// best-effort guard; the hard guarantee is the single-launch lockfile — flock
// on Unix (coordinator_lock_unix.go), O_EXCL on other platforms
// (coordinator_lock_other.go) — not a port bind, which CRIT-001 removed).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes existence without delivering a signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}
