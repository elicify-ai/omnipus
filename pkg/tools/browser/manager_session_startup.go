// manager_session_startup.go: Start the browser behind a manager and create a browsing context's first tab - exec-path resolution, ensureStarted, Session, and the first-tab bootstrap.

package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ensureStarted retains the legacy background caller policy. It must be called
// with m.mu held and returns with it held. Resolution, downloads, process launch,
// and waits all release that mutex. SessionContext supplies a caller and gate
// lifetime through ensureStartedContext instead.
func (m *BrowserManager) ensureStarted() error {
	return m.ensureStartedContext(context.Background())
}

func (m *BrowserManager) ensureStartedContext(ctx context.Context) error {
	if err := sessionStartupError(ctx); err != nil {
		return err
	}
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
	// around it — the same no-lock-across-blocking-call discipline as the
	// local startup cohort. A concurrent ensureStarted that won
	// while m.mu was released is handled by the post-relock m.started check.
	if m.pool != nil || m.coordinator != nil {
		agentID := m.agentID
		pool := m.pool
		coord := m.coordinator
		key := m.key
		registrationGeneration := m.poolRegistrationGeneration
		m.mu.Unlock()
		var (
			rootCtx context.Context
			regErr  error
		)
		if pool != nil {
			coord, rootCtx, regErr = pool.Register(ctx, key, m)
			if regErr == nil && pool.afterRegisterHook != nil {
				pool.afterRegisterHook()
			}
		} else {
			rootCtx, regErr = coord.Register(ctx, agentID, m)
		}
		m.mu.Lock()
		if err := sessionStartupError(ctx); err != nil {
			return err
		}
		if regErr != nil {
			return fmt.Errorf("browser: shared Chrome unavailable: %w", regErr)
		}
		if pool != nil && m.poolRegistrationGeneration != registrationGeneration {
			return ErrBrowserRestarting
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

	// The legacy local browser uses the same waiter lifetime as shared startup;
	// never hold manager.mu while a process is being launched.
	return m.ensureLocalStartedLocked(ctx)
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
// Startup workers resolve with m.mu released, so a slow first-time probe or
// download never blocks concurrent tab/session bookkeeping.
func (m *BrowserManager) resolveExecPath(ctx context.Context) (string, error) {
	return m.execPath.resolve(ctx, m.cfg)
}

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
	release, err := m.acquireLegacyTabCommand(sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	return m.sessionUnderGate(context.Background(), sessionID)
}

func (m *BrowserManager) sessionWithContext(ctx context.Context, sessionID string) (context.Context, error) {
	// Cancels collected under m.mu and run after it is dropped (see the
	// crash-recovery branch below). Declared out here so the retry loop reuses
	// one slice rather than allocating per iteration.
	var pendingCancels []func()
	for {
		m.mu.Lock()
		if err := m.ensureStartedContext(ctx); err != nil {
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

		if err := m.createFirstTabContext(ctx, sessionID); err != nil {
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
	return m.createFirstTabContext(context.Background(), sessionID)
}

func (m *BrowserManager) createFirstTabContext(ctx context.Context, sessionID string) error {
	for {
		m.mu.Lock()
		if gate := m.tabCommands[sessionID]; gate != nil && gate.retired {
			m.mu.Unlock()
			return errBrowserSessionChanged
		}
		if err := m.ensureStartedContext(ctx); err != nil {
			m.mu.Unlock()
			return err
		}
		// Startup may release m.mu while establishing Chrome. Lifecycle removal
		// can retire this command during that work, before a token exists.
		if gate := m.tabCommands[sessionID]; gate != nil && gate.retired {
			m.mu.Unlock()
			return errBrowserSessionChanged
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
			select {
			case <-wait:
			case <-ctx.Done():
				return sessionStartupError(ctx)
			}
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
			tab, err = m.createLiveTab(ctx, browserCtx)
		} else {
			browserCtx, browserCancel, err = m.bootstrapBrowserCtxContext(ctx, allocCtx)
			if err == nil {
				tab, err = m.createLiveTab(ctx, browserCtx)
				if err != nil {
					browserCancel()
				}
			}
		}

		m.mu.Lock()
		if sessionStartupError(ctx) != nil || m.pending[sessionID] != done || m.sessions[sessionID] != existing {
			if m.pending[sessionID] == done {
				delete(m.pending, sessionID)
			}
			m.mu.Unlock()
			m.discardLifecycleTarget(sessionID, tab)
			if existing == nil && browserCancel != nil {
				cancelBounded(browserCancel, map[string]any{"session_id": sessionID, "origin": "retired_first_creation"})
			}
			close(done)
			if callerErr := sessionStartupError(ctx); callerErr != nil {
				return callerErr
			}
			return errBrowserSessionChanged
		}
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
			_ = m.liveTabFocus(ctx, sessionID, newActiveCtx, nil)
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
	return runFirstAttachContext(context.Background(), fn, timeout)
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
	// ADR-075 FR-031: no CDP browser context is adopted or created here,
	// because there are none. Every session bootstraps into Chrome's DEFAULT
	// context — the only one chrome.tabCapture can reach — and isolation is
	// the workspace's own Chrome process and profile directory (FR-037), not
	// a context id.
	//
	// The browser-owning context is a brand-new tab like any other: created,
	// ACTIVATED, then attached, inside one bounded budget (openNewTab). This is
	// exactly where the Chrome 153 un-activated-tab stall surfaced in the field,
	// as "failed to launch browser: ... timed out after 20s waiting for the
	// browser to attach the tab" — see openActivatedTarget.
	deadline := time.Now().Add(firstAttachTimeout)
	remoteCancel := func() {}
	parent := chromedp.FromContext(allocCtx)
	if parent != nil && parent.Browser == nil {
		if allocator, ok := parent.Allocator.(*chromedp.RemoteAllocator); ok {
			// Remote allocators connect lazily. Allocate only the browser here:
			// chromedp.Run would create and attach a page before we activate it.
			// This bootstrap runs outside manager.mu and its parent already
			// carries cancellation until the session has been accepted.
			root, cancelRoot := chromedp.NewContext(allocCtx)
			var cancelOnce sync.Once
			cancel := func() { cancelOnce.Do(cancelRoot) }
			stop := time.AfterFunc(time.Until(deadline), cancel)
			b, err := allocator.Allocate(root, chromedp.WithDialTimeout(time.Until(deadline)))
			stopped := stop.Stop()
			if err != nil || !stopped || root.Err() != nil {
				cancel()
				if err == nil {
					err = root.Err()
				}
				return nil, nil, fmt.Errorf("browser: failed to connect remote browser: %w", err)
			}
			chromedp.FromContext(root).Browser = b
			allocCtx, remoteCancel = root, cancel
		}
	}
	steps, err := newTabStepsFor(allocCtx)
	if err != nil {
		remoteCancel()
		return nil, nil, fmt.Errorf("browser: failed to launch browser: %w", err)
	}
	ctx, cancel, err := openNewTab(allocCtx, time.Until(deadline), steps)
	if err != nil {
		remoteCancel()
		return nil, nil, fmt.Errorf("browser: failed to launch browser: %w", err)
	}
	return ctx, func() { cancel(); remoteCancel() }, nil
}
