package browser

// coordinator.go implements ADR-043 / spec "Stream A": the BrowserCoordinator
// owns the ONE gateway-scoped shared Chrome process + every agent's CDP browser
// context. Per-agent BrowserManagers drive it via chromedp CHILD contexts of the
// coordinator's rootCtx (one CDP pipe, multiplexed) and re-adopt a
// coordinator-owned browser context non-owningly.
//
// CRIT-001 (live-browser-video-streaming / ADR-044 §6.0.3): CDP now flows over
// Chromium's --remote-debugging-pipe (inherited fd 3/4, NUL-delimited JSON) via
// pkg/tools/browser/cdppipe — there is NO TCP debug port, NO /json HTTP surface,
// and NO ws:// URL, so a co-tenant process cannot reach CDP on any kernel and the
// per-stream ingest token is unrecoverable (EC-3). Consequences carried here:
//   - The single-launch guarantee moved from the net.Listen(":9223") bind to an
//     O_EXCL/flock lockfile (coordinator_lock_{unix,other}.go); the ownership
//     marker stays the identity layer (pid + product).
//   - In-process CDP sharing is chromedp child contexts of rootCtx, NOT
//     RemoteAllocator(ws://9223) — the pipe is PRIVATE to the launcher, so any
//     cross-OS-process CDP consumer (e.g. an external CLI) MUST route through the
//     gateway API instead of dialing the browser directly.
//
// Load-bearing invariants (grill C1 / spec §3 Stream A):
//   - CRIT-002/C1: a manager reload (any Settings save) must NOT kill Chrome and
//     must NOT dispose the agent's browser context. The coordinator owns both.
//   - CRIT-003: WithNewBrowserContext is called ONLY on the coordinator's own
//     long-lived chromedp context (which lives on the *AgentLoop and therefore
//     survives ReloadProviderAndConfig). No manager path ever calls it; managers
//     only re-adopt via WithExistingBrowserContext (non-owning, no auto-dispose).
//   - FR-008/R3: the Chrome process is killed exactly once, only by Shutdown
//     (gateway Close). Release + reload never touch the process.
//
// Locking discipline (ADR-038 / spec round-2 MAJ-007): Register/Release/
// RemoveAgent/TryOpenTab/ReleaseTab/PID take c.mu for bookkeeping only; Register
// (and the crash-relaunch path) RELEASE c.mu across the blocking Chrome launch +
// CDP context-create, mirroring manager.go ensureStarted's unlock/relock pattern —
// never hold c.mu across a CDP/exec call.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ownershipMarkerOwner is the owner string stamped into the shared-Chrome
// ownership marker (shared-chrome.pid). Its presence + a live pid is the proof
// that a 9223 holder is OUR Chrome (ADR-043 D1 / grill M2). Its ABSENCE on a
// held port means the holder is foreign and must be rejected, never silently
// driven.
const ownershipMarkerOwner = "omnipus"

// agentBrowserContext is a coordinator-owned CDP browser context for one agent
// (spec D2). The owning chromedp context (ctx/cancel) lives on the COORDINATOR
// (created with WithNewBrowserContext, so chromedp sets browserContextOwner=
// true and disposes it when cancel is called). The manager only ever sees the
// BrowserContextID and re-adopts it non-owningly.
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

	// maxTotalTabs is the global cross-agent tab budget (spec D7, default 30).
	// A browser_open_tab that would exceed it is denied by TryOpenTab. Mutable
	// under c.mu — SetMaxTotalTabs/ApplyRuntimeConfig update it on reload so a
	// config change to max_total_tabs takes effect without a gateway restart.
	maxTotalTabs int
	// reservedTabs counts in-flight tab RESERVATIONS (I-1/W3/C1): each
	// TryOpenTab that passes the budget check increments this BEFORE the
	// opener's createTab actually opens the tab, so N concurrent openers at
	// the boundary see exactly one winner instead of all passing the
	// check-then-act race. Decremented by ReleaseTab (on close) and by the
	// OpenTab-failure return path (when reserve succeeded but the open itself
	// failed). totalOpenTabsLocked()+reservedTabs is the true live+in-flight
	// count TryOpenTab must gate on.
	reservedTabs int

	// execPath holds the exec-path resolution caches, reused from the manager
	// (resolveBrowserExecPath). Resolved once per process; a successful
	// resolution is cached and re-validated with os.Stat on each use.
	execPath execPathCaches

	mu sync.Mutex // bookkeeping lock — see file doc for discipline

	// pipeLauncher launches the one shared Chrome over the CDP pipe transport
	// (CRIT-001 — no TCP port). A field so tests inject a fake and never spawn
	// real Chrome; defaults to launchManagedPipe (exec_resolver.go).
	pipeLauncher func(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error)

	// display, when non-nil AND healthy, makes managed launches VIDEO-CAPABLE
	// (FR-001): headful Chrome rendering into the sidecar's virtual framebuffer
	// (DISPLAY=:N), wrapped in dbus-run-session. When nil — the default until
	// stream orchestration wires an Xvfb sidecar via SetDisplaySidecar — launches
	// stay headless-shell (non-video-capable), still over the CDP pipe.
	display DisplaySidecar

	// Chrome process state (lives on the coordinator; managers never touch it).
	launched   bool
	launching  bool       // single-flight: a launch is in progress
	launchDone *sync.Cond // signaled when a launch completes (nil-safe via mu)
	// rootCtx is the chromedp context returned by the pipe allocator (binds the
	// shared *Browser). In-process managers drive the shared Chrome through
	// chromedp CHILD contexts of this (one pipe, multiplexed) — NOT a
	// RemoteAllocator(ws://) connection, which no longer exists (CRIT-001).
	rootCtx context.Context
	// rootCancel tears the pipe allocator down: it cancels the chromedp context,
	// closes the pipe (Chrome exits), and reaps the process. The pre-pipe
	// allocCtx/allocCancel pair (a separate ExecAllocator context) is gone — the
	// pipe transport has exactly one teardown handle.
	rootCancel context.CancelFunc
	cmd        *exec.Cmd // Chrome process handle (captured via cdppipe ModifyCmd) — the ONLY PID source (Browser.Process() is nil over the pipe)
	// lockFile holds the O_EXCL/flock single-launch lock (CRIT-001) for the
	// coordinator's lifetime. Released (flock LOCK_UN + close) on Shutdown and on
	// crash-relaunch. Replaces the removed net.Listen(":9223") atomic guard.
	lockFile *os.File

	// Per-agent owning browser contexts. Survive manager reload (Release only
	// drops the manager ref, not this entry). Cleared on crash (CRIT-001:
	// contexts are in-memory, lost on process restart) and on Shutdown.
	contexts map[string]*agentBrowserContext

	// Registered managers — for totalOpenTabsLocked (sum OpenTabCount) and crash
	// notification (invalidateConnection on each). The manager ref is dropped
	// on Release; the context entry above intentionally is NOT.
	managers map[string]*BrowserManager

	// killCount is incremented exactly once per Chrome process kill, ONLY in
	// Shutdown (FR-008 / R3). Stays 0 across reload/Release; ==1 after Close.
	killCount int

	// shutdown flag prevents new launches / crash-relaunch racing with a
	// Shutdown in progress.
	shutdown bool
}

// NewBrowserCoordinator constructs a coordinator. homeDir is $OMNIPUS_HOME (the
// ownership marker lands at <homeDir>/browser/shared-chrome.pid; the profile
// dir comes from cfg.ProfileDir). maxTotalTabs is the global tab budget (0 →
// defaultDefaultTotalTabs).
func NewBrowserCoordinator(homeDir string, cfg BrowserConfig, maxTotalTabs int) *BrowserCoordinator {
	if maxTotalTabs <= 0 {
		maxTotalTabs = defaultTotalTabs
	}
	c := &BrowserCoordinator{
		homeDir:      homeDir,
		cfg:          cfg,
		maxTotalTabs: maxTotalTabs,
		contexts:     make(map[string]*agentBrowserContext),
		managers:     make(map[string]*BrowserManager),
		pipeLauncher: launchManagedPipe,
	}
	c.launchDone = sync.NewCond(&c.mu)
	return c
}

// defaultTotalTabs is the global cross-agent tab budget default (spec D7),
// sized from the measured ~91 MB Chrome baseline + a blended per-tab average to
// keep total browsing RSS under ~2.5 GB on a typical 8 GB+ host. Tunable.
const defaultTotalTabs = 30

// Register associates mgr with the coordinator and returns the SHARED root
// chromedp context (rootCtx) the manager drives + the agent's STABLE
// browser-context id. CRIT-001: the manager creates its tab contexts as chromedp
// CHILD contexts of rootCtx (one CDP pipe, multiplexed) — NOT a
// RemoteAllocator(ws://) connection (the pipe has no ws URL and is private to
// the launcher). THE COORDINATOR CREATES+OWNS the browser context:
// WithNewBrowserContext is called on the coordinator's own long-lived chromedp
// context (which survives reload because ReloadProviderAndConfig reuses the same
// *AgentLoop). The manager re-adopts it non-owningly via
// WithExistingBrowserContext(id) — INVARIANT CRIT-003: no manager path ever
// calls WithNewBrowserContext (that would auto-dispose on the manager's
// reload-time cancel, destroying cookies every Settings save).
//
// Launches Chrome if none is live. The blocking launch + CDP context-create
// run with c.mu RELEASED (ADR-038 no-lock-across-blocking-call; the launch can
// shell out to resolve the binary, download Chrome-for-Testing, or block on the
// CDP handshake for seconds). Concurrent Register callers serialize on the
// single-flight launch (c.launching / c.launchDone); the winner launches, the
// losers wait and then observe c.launched.
func (c *BrowserCoordinator) Register(ctx context.Context, agentID string, mgr *BrowserManager) (rootCtx context.Context, browserCtxID cdp.BrowserContextID, err error) {
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
	// in the map, but that is harmless — a fresh manager has OpenTabCount()==0
	// (so it doesn't inflate the budget) and Release's dropConnection on a
	// never-started manager is a no-op.
	c.mu.Lock()
	c.managers[agentID] = mgr
	// The shared root the manager will drive (rootCtx is the named return). Its
	// child contexts multiplex over the one CDP pipe.
	rootCtx = c.rootCtx
	if ac, ok := c.contexts[agentID]; ok && ac != nil {
		bid := ac.browserCtxID
		c.mu.Unlock()
		return rootCtx, bid, nil
	}
	c.mu.Unlock()

	// Create the agent's owning browser context ON THE COORDINATOR'S root
	// context. WithNewBrowserContext panics if c.first is true — rootCtx is the
	// first context (binds the *Browser), so a child created from it is NOT
	// first, which is the safe case (mirrors the D2 spike test exactly).
	agentCtx, agentCancel := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
	if err := chromedp.Run(agentCtx); err != nil {
		agentCancel()
		return nil, "", fmt.Errorf("browser: coordinator: failed to create browser context for agent %q: %w", agentID, err)
	}
	bid := chromedp.FromContext(agentCtx).BrowserContextID
	if bid == "" {
		agentCancel()
		return nil, "", fmt.Errorf("browser: coordinator: browser context for agent %q came back with an empty id", agentID)
	}

	c.mu.Lock()
	// A crash between the unlock above and now could have cleared c.contexts;
	// either way we install the freshly-created (live) context. If a concurrent
	// Register for the same agent raced, keep the first-installed one and cancel
	// the redundant duplicate to avoid leaking a context.
	if existing, ok := c.contexts[agentID]; ok && existing != nil {
		c.mu.Unlock()
		agentCancel() // discard our redundant context; the existing one wins
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
// RemoveAgent CANCELS the owning chromedp context so chromedp runs
// Target.disposeBrowserContext, freeing that agent's cookie/localStorage
// partition. Called by registerSharedTools for agentIDs present in the
// coordinator's map but no longer in cfg.Agents. Safe to call for an agentID
// that was never registered (no-op).
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
	// Cancel the OWNING context (WithNewBrowserContext marked it owner, so
	// chromedp disposes the CDP browser context on cancel). This is the
	// difference from Release: a truly-removed agent's partition is freed,
	// not preserved.
	if ac != nil && ac.cancel != nil && !stopping {
		ac.cancel()
	}
	logger.InfoCF("browser", "coordinator: removed agent (context disposed)", map[string]any{
		"agent_id": agentID,
	})
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

// SetMaxTotalTabs updates the global tab budget at runtime (MED-1). Cheap and
// live: TryOpenTab reads c.maxTotalTabs under c.mu on every open, so a reload
// that changes tools.browser.max_total_tabs takes effect immediately — no
// gateway restart needed. Bounds n to >=1 to avoid an accidental zero disabling
// all tab opens.
func (c *BrowserCoordinator) SetMaxTotalTabs(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	old := c.maxTotalTabs
	c.maxTotalTabs = n
	c.mu.Unlock()
	if old != n {
		logger.InfoCF("browser", "coordinator: max_total_tabs updated on reload", map[string]any{
			"old": old,
			"new": n,
		})
	}
}

// ApplyRuntimeConfig applies the runtime-cheap portions of an updated
// BrowserConfig to a coordinator that survived a reload (MED-1).
// max_total_tabs is a live policy (TryOpenTab reads it under c.mu) and is
// applied immediately via SetMaxTotalTabs. headless/exec_path/profile_dir are
// launch-time properties of the ALREADY-RUNNING Chrome and cannot take effect
// without restarting the gateway; changes to them are warn-logged as "applies
// after gateway restart" so an operator is not silently misled. CRIT-002 stays
// intact: the coordinator itself is never rebuilt on reload.
func (c *BrowserCoordinator) ApplyRuntimeConfig(newCfg BrowserConfig, newMaxTotalTabs int) {
	c.SetMaxTotalTabs(newMaxTotalTabs)
	c.mu.Lock()
	oldCfg := c.cfg
	c.mu.Unlock()
	if oldCfg.Headless != newCfg.Headless {
		logger.WarnCF("browser", "coordinator: tools.browser.headless changed on reload — applies after gateway restart (Chrome already running)", map[string]any{
			"old": oldCfg.Headless,
			"new": newCfg.Headless,
		})
	}
	if oldCfg.ExecPath != newCfg.ExecPath {
		logger.WarnCF("browser", "coordinator: tools.browser.exec_path changed on reload — applies after gateway restart (Chrome already running)", nil)
	}
	if oldCfg.ProfileDir != newCfg.ProfileDir {
		logger.WarnCF("browser", "coordinator: tools.browser.profile_dir changed on reload — applies after gateway restart (Chrome already running)", nil)
	}
}

// TryOpenTab atomically checks the global tab budget AND reserves a slot under
// ONE coordinator lock — so concurrent openers at the boundary see exactly one
// winner (I-1/W3/C1, spec round-2 MAJ-007). It counts live tabs
// (totalOpenTabsLocked — the sum of every registered manager's OpenTabCount)
// PLUS in-flight reservations (c.reservedTabs — slots held by openers between
// this reserve and their createTab completing). Returns (allowed, reason). The
// per-agent manager still enforces its own MaxTabs courtesy cap. The caller
// MUST return the slot via ReleaseTab when the open SUCCEEDS, or via the
// manager's releaseGlobalTab when the open FAILS after reserve succeeded.
func (c *BrowserCoordinator) TryOpenTab(agentID string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.totalOpenTabsLocked()+c.reservedTabs >= c.maxTotalTabs {
		return false, fmt.Sprintf(
			"global tab budget reached (tools.browser.max_total_tabs=%d); close a tab with browser_close_tab first",
			c.maxTotalTabs)
	}
	c.reservedTabs++
	return true, ""
}

// ReleaseTab returns a reserved global-tab slot when an agent closes a tab
// (decrements c.reservedTabs). Each successful TryOpenTab reserved one slot;
// the matching return is: ReleaseTab on a successful open+later close, OR the
// OpenTab-failure return path (manager.releaseGlobalTab) when the open itself
// failed after the reserve. A close that forgets to call ReleaseTab would leak
// a reservation (the coordinator grows conservative, never permissive); the
// OpenTab-failure path forgetting to return its reservation would do the same.
func (c *BrowserCoordinator) ReleaseTab(agentID string) {
	c.mu.Lock()
	if c.reservedTabs > 0 {
		c.reservedTabs--
	}
	c.mu.Unlock()
}

// totalOpenTabsLocked sums open tabs across all registered managers'
// OpenTabCount (spec round-1 MAJ-001). Used by TryOpenTab's budget check (which
// adds c.reservedTabs for the live+in-flight count). Must be called with c.mu
// held.
func (c *BrowserCoordinator) totalOpenTabsLocked() int {
	n := 0
	for _, mgr := range c.managers {
		if mgr != nil {
			n += mgr.OpenTabCount()
		}
	}
	return n
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

// Shutdown disposes all contexts + kills the Chrome process. Called ONLY by
// gateway Close() — the SOLE process-kill path (FR-008 / MIN-008). Idempotent.
func (c *BrowserCoordinator) Shutdown() {
	c.mu.Lock()
	if c.shutdown {
		c.mu.Unlock()
		return
	}
	c.shutdown = true
	// Dispose every agent's owning browser context (canceling the owning
	// chromedp context runs chromedp's Target.disposeBrowserContext, since
	// WithNewBrowserContext marked these contexts as owner).
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

	// Kill the Chrome process by canceling the pipe allocator context: cdppipe's
	// CancelFunc cancels the chromedp context, closes the pipe (Chrome observes
	// EOF and exits), and reaps the process. This is the ONE teardown handle in
	// the pipe model (the pre-pipe rootCancel+allocCancel pair is gone).
	if rootCancel != nil {
		rootCancel()
	}
	// Release the single-launch lock so a fresh gateway can take it (CRIT-001).
	releaseLaunchLock(lockFile)
	logger.InfoCF("browser", "coordinator: shared Chrome shut down (process killed, launch lock released)", map[string]any{
		"kill_count": c.killCount,
	})
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
	// window between the pipe launch succeeding (Chrome alive) and launchChrome
	// installing c.rootCancel/c.lockFile. If Shutdown runs in that window it sees
	// nil cancels, logs a FALSE "process killed", and returns — then launchChrome
	// installs the LIVE Chrome's cancel AFTER Shutdown, producing an unkillable
	// orphan. Close the window: re-check c.shutdown now (launchChrome has
	// installed the cancel + lock by the time it returned nil). If Shutdown won
	// the race, tear down the just-launched Chrome + release the lock ourselves
	// and return an error; do NOT set c.launched=true. (cdppipe's CancelFunc is
	// idempotent, so a double-cancel is harmless.)
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

// launchChrome does the blocking work of starting the one shared Chrome over the
// CDP pipe (CRIT-001 — no TCP port). Called with c.mu NOT held. On success it
// holds the single-launch lock and populates c.rootCtx/rootCancel/cmd/lockFile
// and writes the ownership marker. On failure it tears down any half-built state
// (including releasing the lock).
func (c *BrowserCoordinator) launchChrome(ctx context.Context) error {
	// Single-launch atomicity via an O_EXCL/flock lockfile (CRIT-001): the
	// removed net.Listen(":9223") bind was the atomic guard, and the CDP pipe has
	// no port, so a cross-process lockfile takes its place. The ownership marker
	// stays the identity layer (pid + product) — see takeLaunchLock. A held lock
	// with a live omnipus pid means a prior gateway's Chrome is still running.
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

	if err := os.MkdirAll(c.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("browser: coordinator: cannot create profile directory %s: %w", c.cfg.ProfileDir, err)
	}
	cleanStaleSingletons(c.cfg.ProfileDir)

	// Resolve the Chromium binary (may shell out to probe PATH candidates or
	// download Chrome-for-Testing — runs with c.mu released, per the file doc).
	execPath, err := c.execPath.resolve(ctx, c.cfg)
	if err != nil {
		return fmt.Errorf("browser: coordinator: cannot locate chromium: %w", err)
	}

	// Video-capable (headful on the Xvfb virtual display) vs headless-shell. nil
	// or unhealthy sidecar → headless-shell (still over the pipe). FR-001.
	videoCapable, display := c.videoLaunchMode()
	cmdline := managedExecAllocatorOpts(c.cfg, managedLaunchParams{
		VideoCapable: videoCapable,
		Display:      display,
	})

	// dbus-run-session wrapping for the headful video path (the coordinator "sets
	// this up"): headful Chrome wants a session bus. When video-capable and
	// dbus-run-session is on PATH, launch `dbus-run-session -- <chrome> <args>`;
	// the CDP pipe fds (3/4) inherit through the exec wrapper (dbus-run-session
	// execs its command, preserving inherited fds). Falls back to launching
	// Chrome directly (warn) when the wrapper is unavailable.
	launchPath, launchArgs := execPath, cmdline.Args
	if videoCapable {
		if dbusPath, derr := exec.LookPath("dbus-run-session"); derr == nil {
			launchPath = dbusPath
			launchArgs = append([]string{"--", execPath}, cmdline.Args...)
		} else {
			logger.WarnCF("browser", "coordinator: dbus-run-session not found — launching headful Chrome without a session bus", nil)
		}
	}

	// Launch over the pipe (fail closed — err reports launch + CDP connectivity
	// failure directly). The launcher is a seam so tests never spawn real Chrome.
	launch := c.pipeLauncher
	if launch == nil {
		launch = launchManagedPipe
	}
	res, err := launch(ctx, launchPath, pipeLaunchConfig{
		args:        launchArgs,
		env:         cmdline.Env,
		userDataDir: c.cfg.ProfileDir,
	})
	if err != nil {
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return fmt.Errorf("browser: coordinator: failed to launch shared Chrome over the CDP pipe: %w", err)
	}

	// PID from the captured *exec.Cmd — chromedp.Browser.Process() is nil under
	// the pipe allocator (cdppipe.doc), so the *exec.Cmd is the ONLY PID source.
	pid := 0
	if res.cmd != nil && res.cmd.Process != nil {
		pid = res.cmd.Process.Pid
	}
	product := readBrowserProduct(res.rootCtx)
	// A pid of 0 (cmd not captured) MUST NOT be written as a marker: takeLaunchLock
	// treats a held lock whose marker pid is dead/zero as stale (removable), so a 0
	// marker could let a second launch clobber the lock while our Chrome is live.
	// The hard guarantee is the flock; the marker is the identity/diagnostic layer.
	if pid > 0 {
		if werr := c.writeOwnershipMarker(pid, product); werr != nil {
			logger.WarnCF("browser", "coordinator: failed to write ownership marker (continuing)", map[string]any{
				"error": werr.Error(),
			})
		}
	} else {
		logger.WarnCF("browser", "coordinator: could not capture Chrome pid over the pipe — ownership marker NOT written", nil)
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
	browser := res.browser
	c.mu.Unlock()

	// The coordinator now owns the lock for its lifetime — do NOT release on the
	// deferred error path (only Shutdown / crash-relaunch releases it).
	releaseLockOnErr = false

	// Arm the launcher-wait crash detector (grill M1 / R2). browser.LostConnection
	// closes when the pipe drops — the canonical, race-free signal that the Chrome
	// process has died (cdppipe's own goroutine also watches it + reaps the
	// process; we do NOT call cmd.Wait() ourselves to avoid racing it).
	if browser != nil {
		go c.watchForCrash(browser, managersCopy)
	}

	logger.InfoCF("browser", "coordinator: shared Chrome launched (CDP over pipe, no TCP port)", map[string]any{
		"pid":           pid,
		"exec_path":     execPath,
		"product":       product,
		"video_capable": videoCapable,
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

	// Cancel the dead pipe allocator (idempotent — cdppipe's own LostConnection
	// goroutine already fired it; this just reaps deterministically) and RELEASE
	// the single-launch lock BEFORE relaunching. The relaunch below re-acquires
	// the lock via a fresh open FD; keeping the old FD's flock held would make
	// takeLaunchLock self-deadlock (two open descriptions of the same file
	// contend, even in one process).
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
		logger.ErrorCF("browser", "coordinator: proactive relaunch after crash failed (next tool call will retry)", map[string]any{
			"error": err.Error(),
		})
	}
}

// SetDisplaySidecar wires (or clears) the virtual-display sidecar that makes
// managed launches video-capable (FR-001). Set by stream orchestration once an
// Xvfb sidecar is up and healthy; the NEXT launch reads it via videoLaunchMode.
// nil (the default) keeps launches headless-shell. Launch-time only (SEC-12
// spirit) — it does not restart an already-running Chrome.
func (c *BrowserCoordinator) SetDisplaySidecar(d DisplaySidecar) {
	c.mu.Lock()
	c.display = d
	c.mu.Unlock()
}

// videoLaunchMode reports whether the NEXT launch should be video-capable
// (headful on the sidecar's DISPLAY) or headless-shell. A nil or unhealthy
// sidecar, or an empty DISPLAY, means headless-shell (still over the pipe).
func (c *BrowserCoordinator) videoLaunchMode() (videoCapable bool, display string) {
	c.mu.Lock()
	d := c.display
	c.mu.Unlock()
	if d != nil && d.Healthy() {
		if disp := d.Display(); disp != "" {
			return true, disp
		}
	}
	return false, ""
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
// Ownership is proven WITHOUT a port: a held lock whose ownership marker names a
// LIVE omnipus pid means a prior gateway's Chrome is still running (rejected with
// a clear error). A held lock with a missing/dead-pid marker is a stale lockfile
// left by a crashed prior process (only reachable off Unix, where flock does not
// auto-release) — it is cleared and re-acquired once. The pre-pipe "foreign
// Chrome squatting our port" case is gone: nothing but an omnipus coordinator
// ever locks this file.
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

	// Lock held. Prove the holder identity via the ownership marker (the marker is
	// the identity layer now the port is gone). A live omnipus pid → a prior
	// gateway's Chrome is genuinely still running.
	pid, owner, markerErr := c.readOwnershipMarker()
	if markerErr == nil && owner == ownershipMarkerOwner && pid > 0 && pidAlive(pid) {
		return nil, fmt.Errorf(
			"browser: the shared-Chrome launch lock %s is held by a prior omnipus gateway's Chrome (pid %d) still running — "+
				"stop that gateway/process before starting a new one",
			path, pid)
	}

	// Marker missing or its pid is dead → a stale lockfile from a crashed process.
	// Clear it and retry once (a no-op on Unix, where flock auto-releases so the
	// first acquire would already have succeeded).
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
// best-effort guard; the hard guarantee is the port-bind preflight).
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
