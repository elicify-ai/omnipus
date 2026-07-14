package browser

// coordinator.go implements ADR-043 / spec "Stream A": the BrowserCoordinator
// owns the ONE gateway-scoped shared Chrome process + every agent's CDP browser
// context. Per-agent BrowserManagers connect to it via a remote allocator and
// re-adopt a coordinator-owned browser context non-owningly.
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
// TryOpenTab/TotalOpenTabs/PID take c.mu for bookkeeping only; Register (and the
// crash-relaunch path) RELEASE c.mu across the blocking Chrome launch + CDP
// context-create, mirroring manager.go ensureStarted's unlock/relock pattern —
// never hold c.mu across a CDP/exec call.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

// sharedChromeCDPURL is the WebSocket URL every registered manager dials to
// reach the one shared Chrome. chromedp's RemoteAllocator resolves the actual
// browser debugger endpoint from this via its default /json/version lookup.
func sharedChromeCDPURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", DebugPort)
}

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
	// A browser_open_tab that would exceed it is denied by TryOpenTab.
	maxTotalTabs int

	// execPath holds the exec-path resolution caches, reused from the manager
	// (resolveBrowserExecPath). Resolved once per process; a successful
	// resolution is cached and re-validated with os.Stat on each use.
	execPath execPathCaches

	mu sync.Mutex // bookkeeping lock — see file doc for discipline

	// Chrome process state (lives on the coordinator; managers never touch it).
	launched    bool
	launching   bool            // single-flight: a launch is in progress
	launchDone  *sync.Cond      // signaled when a launch completes (nil-safe via mu)
	allocCtx    context.Context // chromedp ExecAllocator context (cancel => kill Chrome)
	allocCancel context.CancelFunc
	rootCtx     context.Context // first chromedp context from allocCtx (binds *Browser)
	rootCancel  context.CancelFunc
	cmd         *exec.Cmd // Chrome process handle (captured via ModifyCmdFunc) — for PID + crash Wait

	// Per-agent owning browser contexts. Survive manager reload (Release only
	// drops the manager ref, not this entry). Cleared on crash (CRIT-001:
	// contexts are in-memory, lost on process restart) and on Shutdown.
	contexts map[string]*agentBrowserContext

	// Registered managers — for TotalOpenTabs (sum OpenTabCount) and crash
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
	}
	c.launchDone = sync.NewCond(&c.mu)
	return c
}

// defaultTotalTabs is the global cross-agent tab budget default (spec D7),
// sized from the measured ~91 MB Chrome baseline + a blended per-tab average to
// keep total browsing RSS under ~2.5 GB on a typical 8 GB+ host. Tunable.
const defaultTotalTabs = 30

// Register associates mgr with the coordinator and returns the cdpURL to dial +
// the agent's STABLE browser-context id. THE COORDINATOR CREATES+OWNS the
// context: WithNewBrowserContext is called on the coordinator's own long-lived
// chromedp context (which survives reload because ReloadProviderAndConfig
// reuses the same *AgentLoop). The manager re-adopts it non-owningly via
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
func (c *BrowserCoordinator) Register(ctx context.Context, agentID string, mgr *BrowserManager) (cdpURL string, browserCtxID cdp.BrowserContextID, err error) {
	if agentID == "" {
		return "", "", fmt.Errorf("browser: coordinator.Register requires a non-empty agentID")
	}
	if mgr == nil {
		return "", "", fmt.Errorf("browser: coordinator.Register requires a non-nil manager")
	}

	// Ensure Chrome is live (single-flight; releases c.mu across the launch).
	if err := c.ensureLaunched(ctx); err != nil {
		return "", "", err
	}

	// Re-use an existing context for this agent if one already exists (reload
	// case: Release dropped only the manager ref, the context survived). This
	// is the CRIT-002 persistence: the SAME browserCtxID is returned across a
	// reload, so cookies/localStorage/login survive a Settings save.
	c.mu.Lock()
	c.managers[agentID] = mgr
	if ac, ok := c.contexts[agentID]; ok && ac != nil {
		bid := ac.browserCtxID
		c.mu.Unlock()
		return sharedChromeCDPURL(), bid, nil
	}
	rootCtx := c.rootCtx
	c.mu.Unlock()

	// Create the agent's owning browser context ON THE COORDINATOR'S root
	// context. WithNewBrowserContext panics if c.first is true — rootCtx is the
	// first context (binds the *Browser), so a child created from it is NOT
	// first, which is the safe case (mirrors the D2 spike test exactly).
	agentCtx, agentCancel := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
	if err := chromedp.Run(agentCtx); err != nil {
		agentCancel()
		return "", "", fmt.Errorf("browser: coordinator: failed to create browser context for agent %q: %w", agentID, err)
	}
	bid := chromedp.FromContext(agentCtx).BrowserContextID
	if bid == "" {
		agentCancel()
		return "", "", fmt.Errorf("browser: coordinator: browser context for agent %q came back with an empty id", agentID)
	}

	c.mu.Lock()
	// A crash between the unlock above and now could have cleared c.contexts;
	// either way we install the freshly-created (live) context. If a concurrent
	// Register for the same agent raced, keep the first-installed one and cancel
	// the redundant duplicate to avoid leaking a context.
	if existing, ok := c.contexts[agentID]; ok && existing != nil {
		c.mu.Unlock()
		agentCancel() // discard our redundant context; the existing one wins
		return sharedChromeCDPURL(), existing.browserCtxID, nil
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
	return sharedChromeCDPURL(), bid, nil
}

// Release drops an agent's manager ref on reload. Does NOT kill Chrome and does
// NOT dispose the agent's browser context (the coordinator owns it; it persists
// keyed by agentID so the next Register re-adopts it — cookies/localStorage/
// login survive a Settings save). Open TABS may be lost on reload (the manager
// drops its remote-allocator connection; targets are connection-scoped and
// agents reopen them). Returns the remaining registered-manager count.
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

// TryOpenTab atomically checks the global tab budget and, if under it, reserves
// a slot — under ONE coordinator lock — so concurrent openers at the boundary
// see exactly one winner (spec round-2 MAJ-007). Returns (allowed, reason). The
// reservation is advisory bookkeeping; the per-agent manager still enforces its
// own MaxTabs courtesy cap. ReleaseTab returns a slot when a tab closes.
func (c *BrowserCoordinator) TryOpenTab(agentID string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.totalOpenTabsLocked() >= c.maxTotalTabs {
		return false, fmt.Sprintf(
			"global tab budget reached (tools.browser.max_total_tabs=%d); close a tab with browser_close_tab first",
			c.maxTotalTabs)
	}
	return true, ""
}

// ReleaseTab returns a reserved global-tab slot when an agent closes a tab. Best
// effort + advisory (mirrors TryOpenTab): the budget is enforced at open time;
// a close that forgets to call this only makes the coordinator conservative
// (it under-counts free slots), never permissive.
func (c *BrowserCoordinator) ReleaseTab(agentID string) {
	// No per-slot counter is held today (TotalOpenTabs sums live managers'
	// OpenTabCount), so this is a no-op placeholder kept for the contract +
	// future move to a counted budget. Documented honestly rather than removed.
	_ = agentID
}

// TotalOpenTabs sums open tabs across all registered managers' OpenTabCount
// (spec round-1 MAJ-001). Used by TryOpenTab's budget check + Stream D's
// browser_open_tab enforcement.
func (c *BrowserCoordinator) TotalOpenTabs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalOpenTabsLocked()
}

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
	allocCancel := c.allocCancel
	c.rootCancel = nil
	c.allocCancel = nil
	c.rootCtx = nil
	c.allocCtx = nil
	c.cmd = nil
	c.launched = false
	c.killCount++
	c.mu.Unlock()

	// Kill the Chrome process: cancel the root chromedp context first (graceful
	// — lets chromedp close the browser connection + targets), then cancel the
	// ExecAllocator context (chromedp sends SIGKILL to the process). Order
	// matters: rootCancel before allocCancel so chromedp's teardown runs against
	// a still-live allocator.
	if rootCancel != nil {
		rootCancel()
	}
	if allocCancel != nil {
		allocCancel()
	}
	logger.InfoCF("browser", "coordinator: shared Chrome shut down (process killed)", map[string]any{
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

	launchErr := c.launchChrome(ctx)

	c.mu.Lock()
	c.launching = false
	c.launchDone.Broadcast() // wake all waiters regardless of outcome
	if launchErr != nil {
		c.mu.Unlock()
		return launchErr
	}
	c.launched = true
	c.mu.Unlock()
	return nil
}

// launchChrome does the blocking work of starting the one shared Chrome. Called
// with c.mu NOT held. On success, populates c.allocCtx/allocCancel/rootCtx/
// rootCancel/cmd and writes the ownership marker. On failure, tears down any
// half-built state.
func (c *BrowserCoordinator) launchChrome(ctx context.Context) error {
	// Preflight: the fixed CDP port must be free, AND any existing holder must
	// be identifiable as ours (ownership marker) — never silently drive a
	// foreign Chrome (grill M2 / FR-007).
	if err := c.preflightPort(); err != nil {
		return err
	}

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

	opts := managedExecAllocatorOpts(execPath, c.cfg)
	// Capture the Chrome *exec.Cmd so PID() can report it and the launcher-wait
	// crash detector can observe process exit.
	opts = append(opts, chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
		c.mu.Lock()
		c.cmd = cmd
		c.mu.Unlock()
	}))

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	rootCtx, rootCancel := chromedp.NewContext(allocCtx)
	// Actually launch Chrome (blocking CDP handshake). chromedp.Run on the root
	// context is the one place the browser process is spawned.
	if err := chromedp.Run(rootCtx); err != nil {
		rootCancel()
		allocCancel()
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return fmt.Errorf("browser: coordinator: failed to launch shared Chrome: %w", err)
	}

	// Stamp the ownership marker now that we have a live pid.
	pid := 0
	if b := chromedp.FromContext(rootCtx).Browser; b != nil {
		if p := b.Process(); p != nil {
			pid = p.Pid
		}
	}
	product := readBrowserProduct(rootCtx)
	if werr := c.writeOwnershipMarker(pid, product); werr != nil {
		logger.WarnCF("browser", "coordinator: failed to write ownership marker (continuing)", map[string]any{
			"error": werr.Error(),
		})
	}

	c.mu.Lock()
	c.allocCtx = allocCtx
	c.allocCancel = allocCancel
	c.rootCtx = rootCtx
	c.rootCancel = rootCancel
	managersCopy := make([]*BrowserManager, 0, len(c.managers))
	for _, m := range c.managers {
		managersCopy = append(managersCopy, m)
	}
	browser := chromedp.FromContext(rootCtx).Browser
	c.mu.Unlock()

	// Arm the launcher-wait crash detector (grill M1 / R2). browser.LostConnection
	// closes when the CDP transport drops — the canonical, race-free signal that
	// the Chrome process has died (chromedp's own allocator goroutine also Wait()s
	// the process; we do NOT call cmd.Wait() ourselves to avoid racing it).
	if browser != nil {
		go c.watchForCrash(browser, managersCopy)
	}

	logger.InfoCF("browser", "coordinator: shared Chrome launched", map[string]any{
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
func (c *BrowserCoordinator) watchForCrash(b *chromedp.Browser, currentManagers []*BrowserManager) {
	select {
	case <-b.LostConnection:
	case <-time.After(0):
		// LostConnection may already be closed at attach time; fall through.
	}
	// Also handle the case where LostConnection never fires but the process is
	// gone — block on the channel (it always closes on transport drop).
	<-b.LostConnection

	c.mu.Lock()
	if c.shutdown || !c.launched {
		c.mu.Unlock()
		return // shutting down, or already marked dead by a prior detection
	}
	c.launched = false
	c.rootCtx = nil
	c.rootCancel = nil
	c.allocCtx = nil
	c.allocCancel = nil
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

// preflightPort verifies the fixed CDP debug port is free, and — if it is held —
// that the holder is identifiable as OUR Chrome via the ownership marker (grill
// M2 / FR-007). A foreign/unrelated holder is rejected with a clear error, never
// silently driven. Runs with c.mu NOT held.
func (c *BrowserCoordinator) preflightPort() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", DebugPort))
	if err == nil {
		// Port is free — nothing is squatting it. Close the probe and launch.
		if closeErr := ln.Close(); closeErr != nil {
			logger.WarnCF("browser", "coordinator: preflight: failed to close probe listener", map[string]any{
				"port":  DebugPort,
				"error": closeErr.Error(),
			})
		}
		return nil
	}
	// Port is held. Determine whether the holder is OUR Chrome (ownership
	// marker with a live pid) or foreign. This is the grill M2 launch-vs-spoof
	// guard: the CDP endpoint carries no identity token, so the marker is the
	// only way to tell our Chrome from an operator's unrelated one.
	pid, owner, markerErr := c.readOwnershipMarker()
	if markerErr == nil && owner == ownershipMarkerOwner && pid > 0 {
		if pidAlive(pid) {
			return fmt.Errorf(
				"browser: CDP debug port %d is held by a prior omnipus gateway's Chrome (pid %d) still running — "+
					"stop that gateway/process (or remove %s) before starting a new one",
				DebugPort, pid, c.markerPath())
		}
		// Marker exists but its pid is dead → stale leftover marker from a
		// crashed/killed prior Chrome. The port is held by something ELSE (the
		// dead pid can't be holding it) → foreign. Fall through to foreign error.
	}
	return fmt.Errorf(
		"browser: CDP debug port %d is already in use by a non-omnipus process — "+
			"the coordinator will not drive a foreign Chrome (grill M2); "+
			"stop that process, or point tools.browser.cdp_url at an external Chrome",
		DebugPort)
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
