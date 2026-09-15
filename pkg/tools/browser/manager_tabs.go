// manager_tabs.go: Tabs - open, close, switch, list, and their bookkeeping, including creating tabs, adopting Chrome-spawned targets, reconciling the tab set, and the tab-open memory gate.

package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

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
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if targetID != "" {
		// ADOPTING a target that already exists (a popup, window.open): attach
		// only. Chrome created and showed it; the Chrome 153 activation stall
		// openNewTab works around was measured on targets created over CDP, not
		// on these.
		ctx, cancel = chromedp.NewContext(parentCtx, chromedp.WithTargetID(targetID))
		if err := runFirstAttachContext(parentCtx, func() error { return chromedp.Run(ctx) }, firstAttachTimeout); err != nil {
			cancel()
			return nil, err
		}
	} else {
		steps, err := newTabStepsFor(parentCtx)
		if err != nil {
			return nil, err
		}
		ctx, cancel, err = openNewTab(parentCtx, firstAttachTimeout, steps)
		if err != nil {
			return nil, err
		}
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
// this manager tracks. It SURVIVED ADR-075 D1.5a's counter deletion as a
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

// notifyTabsChanged invokes the registered observer with a complete snapshot.
// m.mu is released before invocation, while the caller retains target-command
// admission through publication. Observers may read manager state but must not
// start or mutate targets. Chrome metadata events use a coalesced worker so the
// event-dispatch goroutine never waits for admission or observer work.
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
// any time. m.mu is never held during the callback, but target admission is:
// use read-only lookups, not Session or tab mutations — see notifyTabsChanged.
func (m *BrowserManager) SetTabsChangedFunc(cb func(sessionID string, tabs []Tab, activeIdx int)) {
	m.mu.Lock()
	m.tabsChanged = cb
	m.mu.Unlock()
}

// TabState is the CLOSED three-value answer to "what is there to see in this
// tab set?" (ADR-075 FR-013). It exists because `ListTabs` used to return the
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
	release, admissionErr := m.acquireLegacyTabCommand(sessionID)
	if admissionErr != nil {
		return Tab{}, admissionErr
	}
	defer release()

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
	if !modelMoved {
		m.recaptureForTabChange(sessionID)
	}
	return tabs[index], nil
}

// recaptureForTabChange asks this manager's WebRTC CaptureSession (if any)
// to re-bind its capture to the current model-active tab. A no-op when no
// capture session exists (WebRTC never used, or the panel is closed), which
// is why every call site can invoke it unconditionally.
//
// Called after releasing the manager state lock. The capture lookup uses
// captureMu; foreground recapture work acquires tab admission asynchronously.
func (m *BrowserManager) recaptureForTabChange(sessionID string) {
	if cs := m.CaptureSessionForPanel(sessionID); cs != nil {
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
	release, admissionErr := m.acquireLegacyTabCommand(sessionID)
	if admissionErr != nil {
		return nil, 0, admissionErr
	}
	defer release()

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
	release, admissionErr := m.acquireLegacyTabCommand(sessionID)
	if admissionErr != nil {
		return Tab{}, admissionErr
	}
	defer release()
	startupCtx, stopStartup, startupErr := m.startupContextUnderGate(context.Background(), sessionID)
	if startupErr != nil {
		return Tab{}, startupErr
	}
	defer stopStartup()

	m.mu.Lock()
	if err := m.ensureStartedContext(startupCtx); err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	se, exists := m.sessions[sessionID]
	expected := se
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
	if se != expected {
		m.mu.Unlock()
		return Tab{}, errBrowserSessionChanged
	}
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
	m.mu.Lock()
	se = m.sessions[sessionID]
	if se != expected {
		m.mu.Unlock()
		m.discardLifecycleTarget(sessionID, newTab)
		return Tab{}, errBrowserSessionChanged
	}
	if err != nil {
		m.mu.Unlock()
		return Tab{}, fmt.Errorf("browser: failed to open new tab: %w", err)
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
	return m.adoptTargetForSession(sessionID, targetID, nil)
}

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

// Passive events retain the original session across dispatch and retries. A nil
// owner preserves direct adoption without an externally retained snapshot.
func (m *BrowserManager) adoptTargetForSession(sessionID string, targetID target.ID, owner *sessionEntry) (tabAdoptResult, error) {
	if targetID == "" {
		return tabAdoptResult{}, nil
	}

	m.mu.Lock()
	if !m.adoptionSessionCurrentLocked(sessionID, owner) {
		m.mu.Unlock()
		return tabAdoptResult{}, errBrowserSessionChanged
	}
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
			m.mu.Lock()
			current := m.adoptionSessionCurrentLocked(sessionID, owner)
			m.mu.Unlock()
			if !current {
				return tabAdoptResult{}, errBrowserSessionChanged
			}
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

	// Attaching the new target may take the entire first-attach budget. It
	// does not mutate the existing tab set, so leave its command gate usable.
	// pendingAdopt retains the single shared outcome throughout preparation.
	newTab, err := m.createTab(browserCtx, targetID)
	if err == nil {
		// Publication, activation, and observer callbacks still serialize with
		// existing-tab commands. Revalidate the captured session below.
		release, admissionErr := m.acquireLegacyTabCommand(sessionID)
		if admissionErr != nil {
			m.mu.Lock()
			delete(m.pendingAdopt, targetID)
			entry.result = tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonAttachFailed}
			entry.err = admissionErr
			close(entry.done)
			m.mu.Unlock()
			m.discardLifecycleTarget(sessionID, newTab)
			return entry.result, admissionErr
		}
		defer release()
	}

	m.mu.Lock()
	if m.sessions[sessionID] != se || !m.adoptionSessionCurrentLocked(sessionID, owner) {
		delete(m.pendingAdopt, targetID)
		entry.result = tabAdoptResult{Unadopted: true, Reason: tabAdoptReasonAttachFailed}
		entry.err = errBrowserSessionChanged
		close(entry.done)
		m.mu.Unlock()
		m.discardLifecycleTarget(sessionID, newTab)
		return entry.result, entry.err
	}
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

// sessionExists reports whether sessionID currently has a browsing context.
// Presence alone must not authorize retained asynchronous work.
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
	m.mu.Lock()
	owner := m.sessions[sessionID]
	m.mu.Unlock()
	m.adoptTargetWithRetryForSession(sessionID, targetID, owner)
}

// Opener membership is historical provenance, checked at event admission. The
// popup may survive its opener, but it cannot migrate to a replacement session.
func (m *BrowserManager) adoptTargetWithRetryForSession(sessionID string, targetID target.ID, owner *sessionEntry) {
	if owner == nil {
		return
	}
	_, err := m.adoptTargetForSession(sessionID, targetID, owner)
	if err == nil || errors.Is(err, errBrowserSessionChanged) {
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
		timer := time.NewTimer(delay)
		select {
		case <-owner.browserCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		timer.Stop()
		if _, err = m.adoptTargetForSession(sessionID, targetID, owner); err == nil || errors.Is(err, errBrowserSessionChanged) {
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

// Caller holds m.mu. A nil owner denotes direct adoption without a snapshot.
func (m *BrowserManager) adoptionSessionCurrentLocked(sessionID string, owner *sessionEntry) bool {
	return owner == nil || m.sessions[sessionID] == owner && owner.browserCtx != nil && owner.browserCtx.Err() == nil
}

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
	return m.reconcileTabs(sessionID, nil)
}

func (m *BrowserManager) reconcileTabs(sessionID string, before *clickTabSnapshot) (ReconcileOutcome, error) {
	infos, tracked, owner, err := m.reconcileTargetSnapshot(sessionID)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	return m.reconcileListedTabs(sessionID, infos, tracked, owner, before)
}

func (m *BrowserManager) reconcileListedTabs(sessionID string, infos []*target.Info, tracked map[target.ID]struct{}, owner *sessionEntry, before *clickTabSnapshot) (ReconcileOutcome, error) {
	if before != nil && owner != before.owner {
		return ReconcileOutcome{}, errBrowserSessionChanged
	}

	var out ReconcileOutcome
	for _, info := range infos {
		if info == nil || info.Type != "page" {
			continue
		}
		report := before == nil || before.reports(info)
		if _, already := tracked[info.TargetID]; already && (before == nil || !report) {
			continue
		}
		if info.OpenerID == "" {
			continue // not opened by a page — a top-level target, not ours to adopt
		}
		if _, openerIsOurs := tracked[info.OpenerID]; !openerIsOurs && (before == nil || info.OpenerID != before.opener) {
			continue // opened by a target outside this browsing context
		}
		result, aerr := m.adoptTargetForSession(sessionID, info.TargetID, owner)
		if before != nil && report && aerr == nil && result.Adopted == nil && !result.Unadopted {
			result, aerr = m.completedClickAdoption(sessionID, before, info.TargetID)
		}
		if errors.Is(aerr, errBrowserSessionChanged) {
			// Earlier results no longer describe the current tab set.
			return ReconcileOutcome{}, aerr
		}
		if aerr != nil {
			logger.WarnCF("browser", "reconcile: failed to adopt detected tab", map[string]any{
				"session_id": sessionID,
				"target_id":  string(info.TargetID),
				"error":      aerr.Error(),
			})
		}
		if result.Adopted != nil || result.Unadopted {
			tracked[info.TargetID] = struct{}{}
		}
		// Reconciliation still adopts other owned popups normally; only the
		// click's report is restricted to its original opener and time window.
		if !report {
			continue
		}
		// Adopted and Unadopted are set on DISJOINT fields of out — deliberately
		// NOT an if/else — so a click that opens two new targets where one
		// adopts and one is stranded reports BOTH signals, regardless of which
		// order this loop processes them in. See ReconcileOutcome's doc comment.
		switch {
		case result.Adopted != nil:
			out.Adopted = true
			out.NewActive = result.Adopted
		case result.Unadopted:
			out.Unadopted = true
			if out.Reason == "" {
				out.Reason = result.Reason // first reason wins — stable across the pass
			}
			out.UnadoptedCount++
		}
	}
	if owner != nil {
		m.mu.Lock()
		current := m.adoptionSessionCurrentLocked(sessionID, owner)
		m.mu.Unlock()
		if !current {
			return ReconcileOutcome{}, errBrowserSessionChanged
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
			m.queueTabNotificationLocked(sessionID, se)
			m.mu.Unlock()
			return
		}
	}
	// Discovery is browser-global; another tab set's popup is not ours.
	ownedOpener := exists && info.OpenerID != "" && se.indexOfTarget(info.OpenerID) >= 0
	m.mu.Unlock()

	if !ownedOpener {
		return
	}
	// adoptTargetWithRetry, not a bare adoptTarget: Target.targetCreated
	// fires exactly once per target, so a single transient failure here used
	// to strand the tab permanently — see defaultAdoptRetryBackoff's doc
	// comment for the measurement.
	go m.adoptTargetWithRetryForSession(sessionID, info.TargetID, se)
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
