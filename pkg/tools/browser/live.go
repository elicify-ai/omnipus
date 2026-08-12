package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// agentWindowWidth/Height size each agent's Chrome window (coordinator.go's
// CreateTarget). A window must be large enough to satisfy the largest CSS
// viewport a panel may request AT ITS deviceScaleFactor, and in headless
// Chrome a window can never exceed the virtual screen (--window-size,
// exec_resolver.go).
//
// Sized at 1280x720 before, these two limits collided: a panel asking for a
// 512-CSS-px-tall viewport at dsf 2 needs 1024 device px, so Chrome clamped it
// and the live panel visibly shrank moments after opening and stayed shrunk
// (operator report 2026-08-03; gateway log "window resize not fully reflected
// in the tab's CSS viewport", requested_height 512 -> actual_height 425). The
// chrome-delta compensation could not converge, because the ceiling was the
// screen rather than a constant offset.
//
// Kept in lockstep with --window-size in exec_resolver.go.
const (
	agentWindowWidth  = 2560
	agentWindowHeight = 1440
)

// Input rate limiting (ADR-038 D6: "browser_input is rate-limited") is split
// into two budgets by event kind, because a single shared one made the limiter
// itself a source of the bug it was meant to be neutral about.
//
// The budget is per SESSION, not per connection — and since the exclusive
// controller lock was removed (2026-08-03) so a human and the agent can drive
// together, several senders now draw from it concurrently. Under one shared
// counter a sustained pointer stream could consume the whole allowance and the
// NEXT mouse_up would be refused. A dropped mouse_move is self-healing (the
// following one supersedes it); a dropped mouse_up is not — the remote page
// keeps believing the button is held, which is a stuck drag or a runaway text
// selection with nothing in the UI explaining why.
//
// So: coalescible position kinds get a large bucket, and discrete state
// transitions get their own, which a flood of movement can no longer exhaust.
// Both remain bounded, so a runaway or malicious client is still capped.
const (
	// maxCoalescibleInputEventsPerSecond bounds mouse_move and wheel. The
	// client paces each at ~40/s (MOVE_FLUSH_MS) and coalesces both, so this
	// leaves room for several concurrent viewers plus the agent before anyone
	// is throttled. The old value of 50 sat BELOW what a single 60Hz pointer
	// stream produces on its own, so legitimate input was being dropped
	// routinely — reported as "clicks work only sometimes".
	maxCoalescibleInputEventsPerSecond = 300
	// maxDiscreteInputEventsPerSecond bounds button and key transitions. No
	// human produces anywhere near this; it exists purely to cap automation.
	maxDiscreteInputEventsPerSecond = 100
)

// viewportSetTimeout bounds the Emulation.setDeviceMetricsOverride round trip
// in SetViewport. Kept short: a resize arrives on the UI's debounce and a slow
// or wedged tab must not stall the WS reader goroutine that dispatched it.
const viewportSetTimeout = 5 * time.Second

// viewportDriftTolerancePx is the acceptable gap between SetViewport's
// requested width/height and the tab's actual read-back CSS viewport before
// treating the resize as imperfectly reflected. Below this, a small
// scrollbar/AA-width discrepancy is normal noise; above it, something real
// diverged — confirmed live (UAT v24, 2026-07-31): a requested 615x744
// landed at an actual CSS viewport of 615x657, an 87px HEIGHT deficit from
// Chrome's own window chrome (tab strip/toolbar), width matching exactly.
// See the compensation step in SetViewport's mechanism doc comment.
const viewportDriftTolerancePx = 8

// viewportFetchFailureEscalation is how many CONSECUTIVE viewport-read failures
// turn a silently-dropped pointer event into a user-visible error. Two, not
// one: a single miss is routinely transient (a cache invalidated by a legitimate
// resize, a lost race with a reflow) and surfacing it would be noise. A second
// consecutive failure — a full viewportInputFetchBackoff later — is no longer
// plausibly transient, and silence there is precisely the "dead browser looks
// idle" failure ADR-038 finding #4 exists to prevent.
const viewportFetchFailureEscalation = 2

// viewportInputFetchTimeout bounds rescaleToCSSViewport's best-effort
// cache-miss fetch of the tab's CSS viewport. Deliberately much shorter than
// viewportSetTimeout: that timeout is sized for a user-triggered resize
// (rare, debounced), while this fetch runs on the dispatchInput -> WS
// read-loop hot path, once per input event on every cache miss — a
// slow/wedged CDP transport must fail fast here rather than stall input
// throughput up to a multi-second timeout per event (review CRITICAL
// finding: under a sustained CDP hiccup, the old shared timeout collapsed
// input throughput to roughly one event per timeout, with a fresh WARN log
// line each time).
const viewportInputFetchTimeout = 1 * time.Second

// viewportInputFetchBackoff bounds how soon rescaleToCSSViewport retries its
// cache-miss fetch after a failure. Once a fetch fails, further input events
// dispatch unscaled — without retrying the fetch or re-logging the failure —
// for this long, rather than repeating the same (bounded, but non-zero cost)
// failing CDP round trip on every single subsequent input event. See
// rescaleToCSSViewport's doc comment for the full failure-backoff mechanism.
const viewportInputFetchBackoff = 3 * time.Second

// Viewport bounds. Per-field limits mirror BrowserViewportFrame's schema so
// SetViewport is safe even when gateway.validate_inbound is off (it defaults
// to false, making this the ONLY check on a default install).
const (
	maxViewportDimension   = 8192
	maxViewportScaleFactor = 3.0
	// maxViewportPhysicalPixels bounds width*height*dsf^2 — the actual
	// framebuffer Chromium must allocate. ~33.2M is a generous 8K-class
	// surface (7680x4320 = 33.2M) while refusing the ~604M that the per-field
	// maxima alone would permit.
	maxViewportPhysicalPixels = 33_200_000.0
)

// LiveInput is the engine-level input event the gateway decodes from a
// generated.BrowserInputFrame before calling LiveViewRegistry.Input. Kind
// mirrors the AsyncAPI BrowserInputFrame `kind` enum exactly: mouse_move,
// mouse_down, mouse_up, wheel, key_down, key_up, text, navigate.
type LiveInput struct {
	Kind   string
	X, Y   float64
	HasXY  bool   // ADR-038 finding #5: whether X/Y were actually present on the wire — see buildInputAction.
	Button string // none|left|middle|right|back|forward ("" treated as none)
	DeltaX float64
	DeltaY float64
	Key    string
	Code   string
	// KeyCode is the Windows virtual key code for key_down/key_up (the DOM
	// KeyboardEvent.keyCode, e.g. Backspace=8, Enter=13, Delete=46,
	// arrows=37-40). CDP's Input.dispatchKeyEvent needs it to actually PERFORM
	// editing/navigation key actions and modifier shortcuts (Ctrl+A/C/V) —
	// key/code alone deliver the event but do not delete/submit/move/select.
	KeyCode int
	Text    string
	// URL is the target for the "navigate" kind (ADR-039 D-A2: user-driven
	// address bar). Unlike every other kind, dispatchInput runs this through
	// BrowserManager.ValidateURL — the same SSRF/scheme gate the agent's
	// browser_navigate tool applies — before dispatch, since the live-WS
	// input path otherwise has no URL gate of its own.
	//
	// Type-safety note (7-reviewer LOW finding): LiveInput is a flat struct,
	// so URL can technically coexist with X/Y/HasXY on one value — that is
	// safe TODAY only because buildInputAction/dispatchInput both switch
	// exclusively on Kind and never read URL for a non-"navigate" kind (or
	// X/Y for "navigate"). buildInputAction additionally rejects a
	// "navigate" input that also carries HasXY, as defense-in-depth against
	// a future refactor accidentally reading X/Y (or skipping the SSRF gate)
	// for what the wire actually meant as a navigate. If LiveInput is ever
	// split into a real discriminated union, preserve this invariant.
	URL       string
	Modifiers int // bit field: Alt=1, Ctrl=2, Meta=4, Shift=8 — clamped to [0,15] by buildInputAction.

	// CaptureWidth/CaptureHeight are the intrinsic pixel size of the capture
	// frame the client mapped X/Y into (0/0 = absent, meaning an older
	// client already sent X/Y in CSS pixels — mirrors
	// generated.BrowserInputFrame.CaptureWidth's doc comment). When both are
	// present for a pointer-position kind (mouse_move/mouse_down/mouse_up/
	// wheel), dispatchInput rescales X/Y from this space into the tab's
	// actual CSS viewport before CDP dispatch — root-cause doc Fault 3
	// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md): with
	// adaptive resize/encoder downscaling, the capture's pixel size can
	// differ from the page's CSS pixel space by several times (measured
	// 319x158 capture vs ~1280 page), so a raw 1:1 mapping lands clicks far
	// from their intended target. Never rescaled for wheel's DeltaX/DeltaY
	// (scroll deltas, not positions) or for key/text kinds (no coordinates
	// at all).
	CaptureWidth, CaptureHeight float64
}

// StatusSink receives a live-view lifecycle notification for one attached
// viewer (ADR-038 finding #2's split-brain fix). Today the only event it
// carries is "the session died unexpectedly" — the underlying chromedp tab
// context was canceled out from under an attached viewer WITHOUT going
// through Detach first. The prototypical cause: pkg/agent/loop.go's
// registerSharedTools now calls Shutdown() on an agent's PRIOR
// BrowserManager before installing a fresh one on hot-reload
// (ReloadProviderAndConfig) — Shutdown() cancels every session context,
// including one a viewer's WS connection is still attached to. Without this
// sink, that connection would never learn why; the message is meant to be
// surfaced as a browser_status(error) frame so the client re-attaches (which
// resolves the CURRENT manager). Implementations must not block: the
// LiveView invokes every registered sink synchronously with no lock held. A
// slow consumer should hand off to its own buffered channel (the gateway's
// per-connection sendCh already does this).
type StatusSink func(message string)

// TabsSink receives a tab-set snapshot for one attached viewer (ADR-041 D4).
// Invoked once immediately on Attach (with the CURRENT tab set, so a viewer
// renders the tab strip right away instead of waiting for the next change —
// a session with a single tab may never emit one during this viewer's whole
// attachment) and again on every subsequent tab-set change
// (open/close/switch/adopt/title-url-update). Same non-blocking contract as
// StatusSink/ControlSink: the LiveView invokes every registered sink
// synchronously with no lock held, so a slow consumer must hand off to its
// own buffered channel exactly like the gateway's per-connection sendCh
// already does.
type TabsSink func(tabs []Tab, activeIdx int)

// ControlSink receives a control-ownership change notification for one
// attached viewer (ADR-039 UAT BE-1: "two viewers of the same live session
// disagree about who's driving"). The server already single-controller-locks
// (takeControl/releaseControl below) — this sink is what fixes the DISPLAY
// side: every viewer OTHER than the one that just took/released control is
// invoked with the freshly-computed controlledByOther value so it can update
// its own status pill instead of continuing to show stale "Agent driving" /
// "You're driving" state. Invoked with true when a DIFFERENT viewer just
// took control (this viewer is not — and was never — the new controller);
// invoked with false when control was released. The acting viewer itself is
// never sent a ControlSink notification for its own take/release — it
// already gets an authoritative browser_status frame as the direct response
// to its own browser_control request (handleControl, browser_ws.go). Same
// non-blocking contract as StatusSink: the LiveView invokes every registered
// sink synchronously with no lock held (see takeControl/releaseControl), so
// a slow consumer must hand off to its own buffered channel exactly like the
// gateway's per-connection sendCh already does for status frames.
type ControlSink func(controlledByOther bool)

// LiveViewRegistry manages one LiveView per browser session for a single
// BrowserManager. Since a BrowserManager is itself scoped to one agent
// (pkg/agent/loop.go's per-agent manager map, ADR-038 D4), keying LiveViews
// by session ID here already gives the (agentID, sessionID) uniqueness
// ADR-038 D3 calls for. Safe for concurrent use.
type LiveViewRegistry struct {
	mgr *BrowserManager

	mu    sync.Mutex
	views map[string]*LiveView
}

// newLiveViewRegistry constructs a registry bound to mgr. Unexported —
// callers get one via BrowserManager.Live().
//
// ADR-041 D4: wires mgr's tabs-changed callback to this registry so a
// BrowserManager tab-set change (open/close/switch/adopt) fans out to the
// LiveView for that session, if one exists — see handleTabsChanged.
func newLiveViewRegistry(mgr *BrowserManager) *LiveViewRegistry {
	r := &LiveViewRegistry{mgr: mgr, views: make(map[string]*LiveView)}
	mgr.SetTabsChangedFunc(r.handleTabsChanged)
	return r
}

// handleTabsChanged is the callback registered via mgr.SetTabsChangedFunc in
// newLiveViewRegistry (ADR-041 D4): it fans a BrowserManager tab-set change
// out to the LiveView for that session, if one exists. Uses lookup rather
// than view() — if nobody has ever attached a live view for this session,
// there is nothing to broadcast to or rebind, and creating a LiveView here
// would allocate state nobody is watching (mirrors the lookup-vs-view
// rationale documented on LiveViewRegistry.lookup).
func (r *LiveViewRegistry) handleTabsChanged(sessionID string, tabs []Tab, activeIdx int) {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return
	}
	lv.onTabsChanged(tabs, activeIdx)
}

// runCDPWithTimeout executes actions via chromedp.Run against a
// timeout-bounded child of ctx. Every CDP round trip in this file goes
// through this indirection (LiveView.runCDP) rather than calling
// chromedp.Run directly, for two reasons:
//
//  1. Correctness: a bare chromedp.Run(tabCtx, ...) call has no deadline of
//     its own — under a wedged/overloaded CDP transport it can hang
//     forever. See attach()'s doc comment for the ADR-038 deadlock
//     postmortem this caused.
//  2. Testability: LiveView.runCDP is a field, not a package-level call, so
//     tests can substitute a controllable stand-in to deterministically
//     simulate a slow/hung CDP round trip without a real Chromium — see
//     live_deadlock_test.go.
func runCDPWithTimeout(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
	boundedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return chromedp.Run(boundedCtx, actions...)
}

// resolveSessionID applies the same default-session convention the rest of
// pkg/tools/browser uses (tools.go's defaultSessionID) so callers can omit
// session_id and mean "the agent's one shared tab."
func resolveSessionID(sessionID string) string {
	if sessionID == "" {
		return defaultSessionID
	}
	return sessionID
}

// view returns (creating if necessary) the LiveView for sessionID. Creating
// an entry does NOT start watching the session's tab — that only happens on
// Attach.
func (r *LiveViewRegistry) view(sessionID string) *LiveView {
	r.mu.Lock()
	defer r.mu.Unlock()
	lv, ok := r.views[sessionID]
	if !ok {
		lv = &LiveView{
			mgr:          r.mgr,
			sessionID:    sessionID,
			viewers:      make(map[string]struct{}),
			statusSinks:  make(map[string]StatusSink),
			controlSinks: make(map[string]ControlSink),
			tabsSinks:    make(map[string]TabsSink),
			runCDP:       runCDPWithTimeout,
		}
		r.views[sessionID] = lv
	}
	return lv
}

// lookup returns the LiveView for sessionID without creating one — used by
// read-only queries (Controller/IsControlled) and by browser tools checking
// the control-lock (ADR-038 D6) so a plain tool call never has the side
// effect of allocating live-view state for a session nobody is watching.
func (r *LiveViewRegistry) lookup(sessionID string) (*LiveView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lv, ok := r.views[sessionID]
	return lv, ok
}

// Attach binds viewerID to sessionID's live view, starting to watch the
// session's active tab for unexpected death if this is the first viewer of
// that session (ref-counted). Video for the panel is carried exclusively by
// WebRTC (ADR-061) — this registry's job is session/tab/control-lock
// bookkeeping only, no frame delivery.
//
// onStatus (ADR-038 finding #2, may be nil) is invoked if the underlying tab
// context dies unexpectedly before Detach(sessionID, viewerID) is called;
// onControl (ADR-039 UAT BE-1, may be nil) is invoked whenever some OTHER
// viewer takes or releases control after this call returns. onTabs
// (ADR-041 D4, may be nil) is invoked once immediately with the CURRENT tab
// set and again on every subsequent tab-set change.
// Resolves the manager's session tab itself, so callers only need a session
// ID, not a chromedp context.
//
// Returns controlledByOther: true when, AT THE MOMENT OF THIS ATTACH,
// sessionID is already controlled by a viewer other than viewerID — so a
// newly-attaching connection (a second panel, a pop-out) can render the
// correct "someone else is driving" state on its very first status frame
// instead of only learning about it on the NEXT take/release broadcast.
func (r *LiveViewRegistry) Attach(
	sessionID, viewerID string,
	onStatus StatusSink,
	onControl ControlSink,
	onTabs TabsSink,
) (bool, error) {
	sessionID = resolveSessionID(sessionID)
	if viewerID == "" {
		return false, fmt.Errorf("browser live: viewer id is required")
	}
	tabCtx, err := r.mgr.Session(sessionID)
	if err != nil {
		return false, fmt.Errorf("browser live: cannot resolve session %q: %w", sessionID, err)
	}
	controlledByOther, err := r.view(sessionID).attach(tabCtx, viewerID, onStatus, onControl, onTabs)
	if err != nil {
		return false, err
	}
	// A watched browsing context is never idle — see ReapIdleSessions.
	r.mgr.ViewerAttached(sessionID)

	// ADR-041 D4: give the newly-attached viewer the CURRENT tab strip
	// immediately — a session with only one tab may never emit another
	// tabs-changed event during this viewer's whole attachment.
	if onTabs != nil {
		if tabs, activeIdx, terr := r.mgr.ListTabs(sessionID); terr == nil && len(tabs) > 0 {
			onTabs(tabs, activeIdx)
		}
	}
	return controlledByOther, nil
}

// Detach unbinds viewerID from sessionID's live view. When this was the last
// viewer, the death watch on the session's tab is stopped. Also releases
// control if viewerID currently holds it, so a departing viewer never
// leaves the lock dangling for everyone else.
func (r *LiveViewRegistry) Detach(sessionID, viewerID string) {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return
	}
	lv.detach(viewerID)
	// Starts the idle clock from the moment the last viewer left, rather than
	// from whenever the session was last touched before that — see
	// ViewerDetached / ReapIdleSessions.
	r.mgr.ViewerDetached(sessionID)
}

// SetViewport resizes the captured tab to width x height CSS pixels and
// renders it at deviceScaleFactor, so the capture's shape and resolution
// follow the viewer's panel instead of a fixed constant.
//
// Why (operator UAT 2026-07-31): the tab was pinned to a hardcoded
// --window-size=1280,720 (exec_resolver.go) while the docked panel is an
// arbitrary, resizable shape — measured ~890x1010 (portrait). Since
// `object-fit: contain` preserves the SOURCE aspect, the page could only ever
// fill one dimension and the rest of the panel was letterboxed black. No CSS
// change can correct a source whose shape is wrong. The same report's second
// half was blur: the managed headless Chrome renders at DPR 1, so a capture
// displayed larger than its CSS size upscales — deviceScaleFactor fixes that
// in the same call.
//
// Mechanism (rewritten 2026-07-31 — root-caused via live measurement, not
// hypothetical, see docs/internal/browser-viewport-input-rootcause-2026-07-31.md
// Fault 1): this USED TO call only
// Emulation.setDeviceMetricsOverride(width, height, dsf, false). That
// override is real inside the CDP/renderer world — the page's own CSS media
// queries and layout genuinely see the new size — but it is NOT reflected in
// what the extension-side capture reads: encoder.js's
// captureActiveTabStream sizes the tabCapture stream from
// chrome.tabs.get(tabId).width/height, which is the tab's real OS window
// size and stays put regardless of the emulation override. Every layer
// (this method, CaptureSession.Recapture, runCaptureAndOffer) logged success
// while the captured stream never actually reshaped — confirmed live: stream
// aspect stuck at 2.02 against a 0.96 panel, three consecutive "viewport
// applied" log lines notwithstanding. Textbook silent failure.
//
// Fixed by driving the actual OS-level browser window via
// Browser.getWindowForTarget + Browser.setWindowBounds, which DOES change
// what chrome.tabs.get() reports, so the extension's capture follows.
// Emulation.setDeviceMetricsOverride is kept, but ONLY for
// deviceScaleFactor now — passing width/height 0 to it means "no size
// override" to CDP, so it can never fight the window-bounds resize above.
// When deviceScaleFactor <= 1, this calls Emulation.clearDeviceMetricsOverride
// instead of setDeviceMetricsOverride(0, 0, 1, false) — clearing any stale
// override outright rather than setting a redundant no-op one, so a viewer
// moving from a 2x display back to a 1x one doesn't leave Chromium still
// rendering at the old scale.
//
// This ONLY changes the tab. A capture already in flight keeps its old
// geometry, because tabCapture constraints are pinned per stream
// (encoder.js's minWidth/maxWidth) and cannot be renegotiated on a running
// track. The caller must follow this with CaptureSession.Recapture(), which
// tears the stream down and re-reads chrome.tabs.get() — see
// pkg/gateway/browser_ws.go's handleViewport for the ordering.
//
// After applying, this reads back the tab's ACTUAL CSS layout viewport via
// Page.getLayoutMetrics — the only thing that can prove the resize really
// took effect, per the root-cause doc's "Exit proof" section — and caches it
// on the LiveView (cssViewportW/H, guarded by lv.mu). That cache is the
// source of truth dispatchInput's rescaleToCSSViewport uses to map a
// viewer's capture-space input coordinates into CSS pixels (Fault 3).
//
// Chrome-delta compensation (fix-wave, live evidence UAT v24, 2026-07-31):
// the deployed read-back WARN fired with a requested 615x744 landing at an
// ACTUAL CSS viewport of 615x657 — an 87px HEIGHT deficit, width matching
// exactly. Cause: Browser.setWindowBounds sizes the OUTER OS-level window;
// the tab's own CSS viewport is that minus Chrome's window chrome (tab
// strip/toolbar), which the window-bounds call has no way to account for up
// front. That chrome delta is constant for a given window, so a single
// correction converges: when the read-back gap exceeds
// viewportDriftTolerancePx in either dimension, this re-applies
// Browser.setWindowBounds ONCE more with the request plus its own just-
// measured deficit added (requested + (requested - actual)), then reads back
// again — the FINAL read-back (compensated or not) is what gets logged and
// cached. Exactly one compensation attempt is made, reusing viewportSetTimeout
// as its timeout budget; the per-field maxViewportDimension ceiling is
// re-applied to the compensated bounds (they can legitimately exceed the
// original request by the chrome delta), but the combined physical-pixel
// ceiling is deliberately NOT re-run against them — it already gated the
// original request above, and the compensation delta is a small,
// window-chrome-sized correction, not an independent size request.
//
// A requested/actual gap over viewportDriftTolerancePx in either dimension
// (after any compensation attempt) is logged at WARN, explicitly saying the
// window resize was not fully reflected — this is what would have caught
// Fault 1 instead of every layer silently reporting success. A partial
// resize still returns applied=true; it is not treated as a failure, only
// flagged loudly. A read-back that fails outright, or comes back degenerate
// (width or height <= 0), invalidates the cache instead of leaving a stale
// value behind (review CRITICAL finding) — see invalidateCSSViewportCache's
// doc comment for why a stale-but-positive cache is worse than an empty one.
//
// Returns false if no live view exists for sessionID (nothing to resize).
func (r *LiveViewRegistry) SetViewport(sessionID string, width, height int, deviceScaleFactor float64) (bool, error) {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false, nil
	}
	lv.mu.Lock()
	tabCtx := lv.tabCtx
	lv.mu.Unlock()
	if tabCtx == nil {
		return false, nil
	}
	// Serialize the whole apply→compensate→read-back→cache sequence per
	// LiveView (live UAT 2026-07-31, pop-out): two viewers may legally send
	// viewport frames near-simultaneously while the tab is uncontrolled (the
	// docked panel's first-frame re-send racing the pop-out's attach frame).
	// Interleaved, one caller's raw bounds-write lands in the middle of the
	// other's compensation and the window ends at a hybrid neither asked for
	// (measured: outer bounds stuck at the pop-out's UNcompensated first
	// apply, tab pinned 86px short, self-heal correctly seeing "no drift"
	// against a genuinely wrong tab). NOT lv.mu — this holds across several
	// CDP round trips, and lv.mu must never be held across a CDP call
	// (ADR-038 discipline).
	lv.viewportMu.Lock()
	defer lv.viewportMu.Unlock()
	// Bounds are also enforced by the wire schema (BrowserViewportFrame), but
	// re-checked here because this is a public method on the registry and a
	// future non-WS caller must not be able to hand Chromium a degenerate or
	// enormous allocation.
	if width < 1 || height < 1 || width > maxViewportDimension || height > maxViewportDimension {
		return false, fmt.Errorf("browser live: viewport %dx%d out of range", width, height)
	}
	if deviceScaleFactor < 1 || deviceScaleFactor > maxViewportScaleFactor {
		// Reject rather than silently clamp (review finding): a caller asking
		// for dsf 50 got no feedback at all under the old clamp, while an
		// out-of-range width got an explicit error. Same input class, same
		// treatment.
		return false, fmt.Errorf("browser live: device scale factor %.2f out of range (1..%.0f)",
			deviceScaleFactor, maxViewportScaleFactor)
	}
	// Combined ceiling. Each dimension and the scale factor are individually
	// bounded above, but nothing bounded their PRODUCT: 8192x8192 at dsf 3 is
	// inside every per-field limit and asks Chromium for a ~24576x24576
	// physical surface — on the order of gigabytes of framebuffer, against the
	// single shared Chrome backing the agent's browsing.
	physicalPixels := float64(width) * float64(height) * deviceScaleFactor * deviceScaleFactor
	if physicalPixels > maxViewportPhysicalPixels {
		return false, fmt.Errorf(
			"browser live: viewport %dx%d @%.1fx = %.0f physical pixels, over the %.0f ceiling",
			width, height, deviceScaleFactor, physicalPixels, maxViewportPhysicalPixels)
	}

	// Step 1: actually reshape the OS-level browser window (Fault 1 fix —
	// see the mechanism section above). windowBoundsAction folds
	// Browser.getWindowForTarget (which resolves the current tab's own
	// window with no explicit target ID, because it is called "as a part of
	// the session" — tabCtx IS that session) and Browser.setWindowBounds
	// into one chromedp.Action — see its doc comment for why this is a
	// small named type rather than a bare chromedp.ActionFunc closure, and
	// for why the windowID no longer needs to live in an outer variable
	// (test-seam review MEDIUM finding).
	actions := []chromedp.Action{windowBoundsAction{width: width, height: height}}
	// Step 2: deviceScaleFactor only (see the mechanism section above for
	// why width/height are 0 here, not width/height again). dsf==1 clears
	// any stale override instead of setting a no-op one, so a viewer moving
	// from a 2x display to a 1x one doesn't leave Chromium still rendering
	// at the old scale.
	if deviceScaleFactor > 1 {
		actions = append(actions, emulation.SetDeviceMetricsOverride(0, 0, deviceScaleFactor, false))
	} else {
		actions = append(actions, emulation.ClearDeviceMetricsOverride())
	}
	// Bundled into one runCDPWithTimeout call / one timeout budget — not
	// "one CDP round trip" as the mechanism section used to describe it: it
	// is actually THREE sequential protocol commands (GetWindowForTarget,
	// SetWindowBounds, and SetDeviceMetricsOverride/
	// ClearDeviceMetricsOverride) run in order inside a single bounded
	// chromedp.Run, since all three are cheap, sequential, and any one
	// failing makes the rest pointless. Routed through lv.runCDP, not the
	// package-level runCDPWithTimeout directly (test-seam review MEDIUM
	// finding) — every other CDP call site in this file uses the same
	// injectable seam; see its doc comment.
	if err := lv.runCDP(tabCtx, viewportSetTimeout, actions...); err != nil {
		// One retry, and ONLY for a deadline timeout (2026-08-13 UAT:
		// "could not resize the browser viewport" toast mid-session). A
		// GetWindowForTarget that cannot answer within viewportSetTimeout
		// means the browser process is momentarily starved (encode burst +
		// input backlog), not that the resize is invalid -- by the second
		// attempt the stall has typically cleared. Any other error is a
		// real failure and still surfaces immediately.
		if !errors.Is(err, context.DeadlineExceeded) {
			return false, fmt.Errorf("browser live: resize viewport: %w", err)
		}
		logger.WarnCF(
			"browser",
			"live view: set viewport timed out; retrying once (browser process momentarily starved)",
			map[string]any{"session_id": sessionID},
		)
		if err := lv.runCDP(tabCtx, viewportSetTimeout, actions...); err != nil {
			return false, fmt.Errorf("browser live: resize viewport (after retry): %w", err)
		}
	}

	// Step 3: read back what ACTUALLY happened — see the mechanism section
	// above. readBack is called once here and, at most, once more by the
	// compensation step below — both share the same lv.runCDP-routed
	// layoutMetricsAction (see its doc comment for why a small named type is
	// used here too, in place of an inline ActionFunc closure).
	readBack := func() (int64, int64, error) {
		var w, h int64
		err := lv.runCDP(tabCtx, viewportSetTimeout, layoutMetricsAction{w: &w, h: &h})
		return w, h, err
	}

	actualW, actualH, readErr := readBack()
	if readErr == nil && (actualW <= 0 || actualH <= 0) {
		readErr = fmt.Errorf("degenerate CSS viewport read back (%dx%d)", actualW, actualH)
	}
	if readErr != nil {
		// A failure/degenerate read-back here does not undo the resize
		// above (best-effort: the resize itself already succeeded), so this
		// is logged and swallowed rather than turned into an error return —
		// but (review CRITICAL finding) it DOES invalidate the cache rather
		// than leaving a stale value in place; see
		// invalidateCSSViewportCache's doc comment for why that matters.
		lv.invalidateCSSViewportCache()
		logger.WarnCF(
			"browser",
			"live view: set viewport applied but could not read back the actual CSS viewport to verify it — cache invalidated, input coordinates will re-fetch it on the next event",
			map[string]any{
				"error":            readErr.Error(),
				"session_id":       sessionID,
				"requested_width":  width,
				"requested_height": height,
			},
		)
		return true, nil
	}

	// Chrome-delta compensation — SINGLE PASS, deliberately not iterated.
	//
	// Browser.setWindowBounds' OUTER-window size does not fully carry through
	// to the tab's CSS viewport, so one correction (request + observed gap) is
	// attempted. It frequently does not work, and the diagnostic fields added
	// for exactly this question show why iterating would NOT help (v52 logs,
	// deviceScaleFactor 1):
	//
	//	requested 587 -> first read-back 444 -> asked 730 -> still 444
	//	requested 564 -> first read-back 421 -> asked 707 -> still 421
	//
	// The second setWindowBounds changes NOTHING: the post-compensation
	// read-back equals the pre-compensation one exactly. Chrome is ignoring the
	// resize outright, not partially honoring it — so a convergence loop would
	// merely repeat a no-op N times and burn CDP round trips. (A loop was built
	// and reverted twice on this evidence; do not reintroduce one without first
	// showing that a SECOND setWindowBounds moves the viewport at all.)
	//
	// The cause is still open. It is NOT the screen bound: the managed Chrome
	// launches with --window-size=2560,1440 (exec_resolver.go, verified on the
	// running container) so a ~730px request has ample headroom and is still
	// ignored. Whatever the reason, the cache below records the TRUE viewport,
	// so input-coordinate mapping stays correct even while the panel renders
	// undersized — a cosmetic shrink, not mis-aimed clicks.
	compensated := false
	var compensatedAskW, compensatedAskH int
	if viewportDeltaPx(width, actualW) > viewportDriftTolerancePx ||
		viewportDeltaPx(height, actualH) > viewportDriftTolerancePx {
		compW := clampViewportDim(width + (width - int(actualW)))
		compH := clampViewportDim(height + (height - int(actualH)))
		compensatedAskW, compensatedAskH = compW, compH
		if err := lv.runCDP(tabCtx, viewportSetTimeout, windowBoundsAction{width: compW, height: compH}); err != nil {
			logger.WarnCF(
				"browser",
				"live view: set viewport — chrome-delta compensation re-apply failed, keeping the pre-compensation read-back",
				map[string]any{
					"error":              err.Error(),
					"session_id":         sessionID,
					"compensated_width":  compW,
					"compensated_height": compH,
				},
			)
		} else {
			compW2, compH2, compErr := readBack()
			if compErr == nil && (compW2 <= 0 || compH2 <= 0) {
				compErr = fmt.Errorf("degenerate CSS viewport read back after compensation (%dx%d)", compW2, compH2)
			}
			if compErr != nil {
				lv.invalidateCSSViewportCache()
				logger.WarnCF("browser",
					"live view: set viewport — could not read back the CSS viewport after "+
						"chrome-delta compensation — cache invalidated, input coordinates will "+
						"re-fetch it on the next event",
					map[string]any{
						"error":              compErr.Error(),
						"session_id":         sessionID,
						"requested_width":    width,
						"requested_height":   height,
						"compensated_width":  compW,
						"compensated_height": compH,
					})
				return true, nil
			}
			// Keep the CLOSEST read-back, not merely the latest: if the
			// compensated attempt came back worse (or, as measured, identical),
			// the cache must not be degraded. cssViewportW/H feeds input
			// coordinate mapping, so "closest to the page's real size" is the
			// property that matters.
			if viewportDeltaPx(width, compW2)+viewportDeltaPx(height, compH2) <
				viewportDeltaPx(width, actualW)+viewportDeltaPx(height, actualH) {
				actualW, actualH = compW2, compH2
			}
			compensated = true
		}
	}

	fields := map[string]any{
		"session_id":          sessionID,
		"requested_width":     width,
		"requested_height":    height,
		"actual_width":        actualW,
		"actual_height":       actualH,
		"device_scale_factor": deviceScaleFactor,
		"compensated":         compensated,
		// What compensation actually asked Chrome for (0 when it never ran).
		// Present so a recurrence of the DSF-2 shrink is diagnosable from the
		// log alone — reconstructing it by hand produced two wrong models.
		"compensated_ask_width":  compensatedAskW,
		"compensated_ask_height": compensatedAskH,
	}
	if viewportDeltaPx(width, actualW) > viewportDriftTolerancePx ||
		viewportDeltaPx(height, actualH) > viewportDriftTolerancePx {
		// The silent-success failure mode the root-cause doc documents:
		// every prior layer reported success while the capture never
		// actually reshaped. Loud enough here that it can't be missed the
		// way it was during the 2026-07-31 UAT.
		logger.WarnCF(
			"browser",
			"live view: set viewport — window resize not fully reflected in the tab's CSS viewport",
			fields,
		)
	} else {
		logger.InfoCF("browser", "live view: viewport applied", fields)
	}

	// The FINAL read-back (post-compensation when compensation fired) is
	// always sane/non-degenerate by this point — both failure/degenerate
	// paths above already returned early via invalidateCSSViewportCache.
	lv.mu.Lock()
	lv.cssViewportW = int(actualW)
	lv.cssViewportH = int(actualH)
	lv.mu.Unlock()

	return true, nil
}

// windowBoundsAction folds Browser.getWindowForTarget + Browser.setWindowBounds
// into ONE chromedp.Action (test-seam review MEDIUM finding): the windowID no
// longer needs to live in a variable shared across two chromedp.Tasks slice
// entries — chromedp.Tasks runs its actions in order and aborts on the first
// error, so a two-entry [get, set] slice with an outer `var windowID
// browser.WindowID` behaved identically to resolving and using it entirely
// inside one action. Implemented as a small named type — rather than a bare
// chromedp.ActionFunc closure — specifically so SetViewport's compensation
// step is testable: live_test.go type-asserts the *requested* width/height
// straight off this struct's exported-to-the-package fields, without ever
// having to execute a real CDP round trip to observe them (a closure hides
// that same information behind an opaque func value with no way to inspect
// it short of actually calling Do(ctx) against a live browser). SetViewport
// constructs this at most twice: once for the requested size, and — only
// when the chrome-delta compensation step fires (see SetViewport's mechanism
// doc comment) — once more for the compensated size.
type windowBoundsAction struct {
	width, height int
}

// Do implements chromedp.Action.
func (a windowBoundsAction) Do(ctx context.Context) error {
	windowID, _, werr := browser.GetWindowForTarget().Do(ctx)
	if werr != nil {
		return fmt.Errorf("get window for target: %w", werr)
	}
	return browser.SetWindowBounds(windowID, &browser.Bounds{
		Width:  int64(a.width),
		Height: int64(a.height),
	}).Do(ctx)
}

// layoutMetricsAction reads the tab's actual CSS layout viewport via
// Page.getLayoutMetrics and writes the result through w/h (test-seam review
// MEDIUM finding: the duplicated Page.GetLayoutMetrics closure in
// SetViewport's read-back and rescaleToCSSViewport's cache-miss fetch is
// factored into readCSSLayoutViewport, and this type is the one shared
// chromedp.Action wrapper both call sites use around it). A small named type
// with pointer output fields, like windowBoundsAction, rather than a bare
// chromedp.ActionFunc closure capturing outer variables — same rationale:
// live_test.go's scripted runCDP stubs need to write scripted width/height
// values straight through w/h without executing a real CDP round trip.
type layoutMetricsAction struct {
	w, h *int64
}

// Do implements chromedp.Action.
func (a layoutMetricsAction) Do(ctx context.Context) error {
	w, h, err := readCSSLayoutViewport(ctx)
	if err != nil {
		return err
	}
	*a.w, *a.h = w, h
	return nil
}

// readCSSLayoutViewport issues Page.getLayoutMetrics and returns the tab's
// CSS layout viewport's client width/height — the only thing that can prove
// a resize actually took effect (root-cause doc's "Exit proof" section).
// Pure CDP-call helper wrapped by layoutMetricsAction above; factored out on
// its own so the two call sites (SetViewport's read-back, rescaleToCSSViewport's
// cache-miss fetch) share exactly one implementation of the underlying
// protocol call instead of two near-identical duplicated closures (review
// MEDIUM finding).
func readCSSLayoutViewport(ctx context.Context) (w, h int64, err error) {
	// CDP GetLayoutMetrics returns 7 values and only two are meaningful here;
	// naming five throwaways would be noisier than the blanks.
	//nolint:dogsled // see above
	_, _, _, cssLayout, _, _, lerr := page.GetLayoutMetrics().Do(ctx)
	if lerr != nil {
		return 0, 0, lerr
	}
	if cssLayout != nil {
		w = cssLayout.ClientWidth
		h = cssLayout.ClientHeight
	}
	return w, h, nil
}

// viewportDeltaPx returns the absolute pixel gap between a requested int
// dimension and an actual CDP-reported int64 dimension — shared by
// SetViewport's compensation-trigger check and its final requested-vs-actual
// log/cache decision, which both need the identical comparison.
func viewportDeltaPx(requested int, actual int64) int {
	d := requested - int(actual)
	if d < 0 {
		return -d
	}
	return d
}

// clampViewportDim bounds a compensated window-bounds dimension to
// maxViewportDimension (SetViewport's compensation step, item 1 of the
// 2026-07-31 fix wave): the compensated size legitimately exceeds the
// ORIGINAL request by the window's own chrome delta, so the per-field
// ceiling is re-applied to the compensated value alone. The combined
// physical-pixel ceiling is deliberately NOT re-run here — it already gated
// the original request in SetViewport above, and a compensation delta is a
// small, window-chrome-sized correction, not an independent size request.
func clampViewportDim(v int) int {
	if v > maxViewportDimension {
		return maxViewportDimension
	}
	if v < 1 {
		return 1
	}
	return v
}

// invalidateCSSViewportCache zeroes the cached CSS viewport (review CRITICAL
// finding, item 2 of the 2026-07-31 fix wave): a stale-but-positive cache
// passes rescaleToCSSViewport's cache-hit guard (cssViewportW/H > 0) and
// silently mis-maps every subsequent click by the old/new ratio — exactly the
// bug class this whole fix exists to kill. Called whenever SetViewport's
// read-back (or its at-most-one compensated re-read) fails outright or comes
// back degenerate, so a broken read-back can never leave a confidently-wrong
// cache behind — the next input event's rescaleToCSSViewport call re-fetches
// from a known-empty (0,0) state instead of trusting a value that no longer
// corresponds to reality.
func (lv *LiveView) invalidateCSSViewportCache() {
	lv.mu.Lock()
	lv.cssViewportW, lv.cssViewportH = 0, 0
	lv.mu.Unlock()
}

// CSSViewport returns sessionID's cached CSS layout viewport — SetViewport's
// Page.getLayoutMetrics read-back (including its at-most-one chrome-delta
// compensation re-read), the CDP-verified truth of the tab's actual size
// (see SetViewport's mechanism doc comment). ok is false when no live view
// exists for sessionID, or the cache is unset/invalidated (zero — either
// SetViewport has never run for this session, or its last read-back failed
// or came back degenerate; see invalidateCSSViewportCache).
//
// Follow-up to
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md (measured
// 2026-07-31): the gateway's browser_ws.go handleViewport calls this right
// after a successful SetViewport so it can thread the verified dimensions
// through to CaptureSession.RecaptureAt — without them, the encoder's own
// chrome.tabs.get-based resolution can race the OS window reflow and pin
// the WebRTC stream to a stale tab size.
func (r *LiveViewRegistry) CSSViewport(sessionID string) (w, h int, ok bool) {
	sessionID = resolveSessionID(sessionID)
	lv, exists := r.lookup(sessionID)
	if !exists {
		return 0, 0, false
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.cssViewportW <= 0 || lv.cssViewportH <= 0 {
		return 0, 0, false
	}
	return lv.cssViewportW, lv.cssViewportH, true
}

// Input dispatches a viewer input event via CDP, but ONLY when viewerID
// currently holds control of sessionID (ADR-038 D6). Returns an error
// (nothing is applied) when the viewer doesn't hold control, no live view is
// active for the session, or the event is rate-limited.
func (r *LiveViewRegistry) Input(sessionID, viewerID string, in LiveInput) error {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		// Real, not benign (ADR-038 finding #4): nobody has ever attached
		// (or the tab was torn down entirely), which the caller needs to
		// know about — unlike a not-controller/rate-limit rejection, this
		// isn't an expected steady-state occurrence.
		return realInputError("browser live: no active live view for session %q", sessionID)
	}
	return lv.dispatchInput(viewerID, in)
}

// TakeControl grants viewerID exclusive interactive control of sessionID's
// live view. Returns false if another viewer already holds control — v1 is
// cooperative, first-come, no preemption (ADR-038 D6). Creates the LiveView
// entry if one doesn't exist yet (control can be requested before/without an
// attached viewer, though the SPA flow always attaches first).
func (r *LiveViewRegistry) TakeControl(sessionID, viewerID string) bool {
	sessionID = resolveSessionID(sessionID)
	if viewerID == "" {
		return false
	}
	return r.view(sessionID).takeControl(viewerID)
}

// EnsureControlForInput grants viewerID control when a human viewer sends
// input, unless a DIFFERENT, STILL-ATTACHED viewer genuinely holds the lock.
// Returns whether viewerID holds control afterwards.
//
// Product model (operator, 2026-07-30): "the user is driving per default
// unless the agent is evidently driving, and even then the user can click
// into the browser to take over." The lock's original design was the
// inverse — nobody drives until an explicit take frame arrives — which made
// a human's input silently invalid whenever client and server disagreed
// about who held it.
//
// Two concrete failures this closes, both seen in the 2026-07-30 UAT:
//
//  1. STALE LOCK. lv.controller is cleared in detach(), which only runs on a
//     CLEAN WebSocket close. A mobile/VPN client that vanishes abruptly
//     leaves its viewerID owning the lock forever, and every later
//     connection is locked out of a browser nobody is driving. Checking
//     lv.viewers membership distinguishes a live holder from a ghost.
//  2. LOST TAKE ACROSS RECONNECT. The client kept believing it was driving
//     across a WS reconnect, so it never re-sent the take under its NEW
//     viewerID; the server rejected all 448 of its inputs while the panel
//     read "You're driving".
//
// The agent is not a viewer and never holds this lock (its activity is the
// separate agent-working axis), so "a different attached viewer" here always
// means another human — the one case where refusing is correct.
func (r *LiveViewRegistry) EnsureControlForInput(sessionID, viewerID string) bool {
	sessionID = resolveSessionID(sessionID)
	if viewerID == "" {
		return false
	}
	return r.view(sessionID).ensureControlForInput(viewerID)
}

// ReleaseControl releases viewerID's control of sessionID's live view, if it
// currently holds it. A no-op (not an error) if viewerID isn't the current
// controller — releasing twice, or after already losing it (e.g. on detach),
// is always safe.
func (r *LiveViewRegistry) ReleaseControl(sessionID, viewerID string) {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return
	}
	lv.releaseControl(viewerID)
}

// Controller returns the viewerID currently holding control of sessionID's
// live view, or "" if uncontrolled (including when no live view exists yet).
func (r *LiveViewRegistry) Controller(sessionID string) string {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return ""
	}
	return lv.getController()
}

// IsControlled reports whether sessionID currently has a human viewer holding
// interactive control. This is the turn-coordination gate (ADR-038 D6): the
// agent's own browser tools (pkg/tools/browser/tools.go) consult this before
// driving the page, and defer with a soft result instead of fighting the
// viewer for the cursor. There is deliberately no mid-tool preemption in v1 —
// a tool call already in flight when a human takes control finishes normally.
func (r *LiveViewRegistry) IsControlled(sessionID string) bool {
	return r.Controller(sessionID) != ""
}

// LiveView is a session-tracking + input-injection engine bound to one
// browser session (one Chromium tab). Video is carried exclusively by
// WebRTC (ADR-061); this type owns viewer/control-lock bookkeeping and a
// death watch on the session's tab. Reference-counted by attached viewers:
// the death watch starts on the first Attach and stops on the last Detach.
// Safe for concurrent use — all state is guarded by mu.
type LiveView struct {
	mgr       *BrowserManager
	sessionID string

	mu         sync.Mutex
	tabCtx     context.Context
	listenCtx  context.Context // child of tabCtx; canceling it stops the death watch without touching the tab
	stopListen context.CancelFunc
	// lastKnownActiveCtx (ADR-047, wave-plan W2-A item 5) tracks the most
	// recently observed active-tab context INDEPENDENTLY of tabCtx — tabCtx
	// only reflects the current watch's binding and stays nil until a watch
	// is ever installed (isActiveLocked/hasEpochLocked gate on it), so a
	// session with no viewer ever attached would otherwise never have a
	// reliable "did the active tab actually change" signal for WebRTC
	// recapture. Set unconditionally at the end of every onTabsChanged call;
	// nil only before the first call.
	lastKnownActiveCtx context.Context
	viewers            map[string]struct{}
	// statusSinks parallels viewers (ADR-038 finding #2): one optional
	// StatusSink per attached viewerID, notified only on an unexpected
	// session death (watchForUnexpectedDeath), never on a clean Detach.
	statusSinks map[string]StatusSink
	// controlSinks parallels viewers (ADR-039 UAT BE-1): one optional
	// ControlSink per attached viewerID, notified whenever some OTHER
	// viewer takes or releases control (takeControl/releaseControl below).
	controlSinks map[string]ControlSink
	// tabsSinks parallels viewers (ADR-041 D4): one optional TabsSink per
	// attached viewerID, notified once on attach with the current tab set
	// and again on every subsequent tab-set change (see onTabsChanged).
	tabsSinks  map[string]TabsSink
	controller string // viewerID holding control; "" = uncontrolled

	// cssViewportW/cssViewportH cache the tab's actual CSS layout viewport
	// (Page.getLayoutMetrics' cssLayoutViewport ClientWidth/ClientHeight),
	// read back after every SetViewport call (including its at-most-one
	// chrome-delta compensation re-read) — that read-back is the source of
	// truth dispatchInput's rescaleToCSSViewport uses to map a viewer's
	// capture-space input coordinates into CSS pixels (root-cause doc
	// Fault 3). Zero until the first SetViewport call, or the first input
	// event that needs it and finds the cache empty, populates it. Also
	// explicitly zeroed by invalidateCSSViewportCache (review CRITICAL
	// finding, 2026-07-31 fix wave) whenever a SetViewport read-back fails
	// or comes back degenerate — a stale-but-positive value here is worse
	// than an empty one, since it passes rescaleToCSSViewport's cache-hit
	// guard and silently mis-maps input by the old/new ratio.
	cssViewportW, cssViewportH int

	// viewportMu serializes SetViewport's multi-round-trip
	// apply→compensate→read-back sequence (see SetViewport). Separate from mu
	// because it is held across CDP calls, which mu never may be.
	viewportMu sync.Mutex

	// nextFetchAfter backs off rescaleToCSSViewport's cache-miss fetch after
	// a failure (2026-07-31 fix wave, item 3): zero until the first fetch
	// failure; set to time.Now().Add(viewportInputFetchBackoff) on failure,
	// and consulted (then implicitly cleared by a subsequent successful
	// fetch) on every later cache-miss call so a sustained CDP hiccup
	// doesn't repeat the same failing round trip — and its WARN log — once
	// per input event. See rescaleToCSSViewport's doc comment.
	nextFetchAfter time.Time
	// viewportFetchFailures counts CONSECUTIVE cache-miss fetch failures in
	// rescaleToCSSViewport, reset on any success. A single failure is a
	// transient the user simply retries past; a sustained streak means the CDP
	// transport is wedged or the tab is dead, which LiveInputErrorReal's own
	// doc comment names as the single most important thing to surface ("a dead
	// browser looked identical to a healthy, idle one" — ADR-038 finding #4).
	// Escalating on the streak keeps a routine hiccup quiet while ensuring a
	// genuinely dead tab still reaches the user.
	viewportFetchFailures int

	// Rate limiting: input can only ever come from the single controller at a
	// time, so one shared counter per LiveView is sufficient — no per-viewer
	// bookkeeping needed.
	inputWindowStart time.Time
	inputCount       int // coalescible kinds (mouse_move, wheel)
	discreteCount    int // button/key transitions

	// runCDP executes a bounded chromedp CDP round trip. See
	// runCDPWithTimeout's doc comment for why this is a field instead of a
	// direct chromedp.Run call. Every call site in this file MUST call it
	// with no LiveView lock held.
	runCDP func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error
}

// isActiveLocked reports whether this LiveView is currently watching its
// tab for unexpected death (must be called with mu held). Checking
// listenCtx.Err() rather than a separate bool means a tab whose context died
// out-of-band (session recreated, crash) is detected and cleanly re-armed on
// the next attach, instead of leaving the registry believing the watch is
// still live when chromedp silently dropped the target along with the
// canceled context.
func (lv *LiveView) isActiveLocked() bool {
	return lv.listenCtx != nil && lv.listenCtx.Err() == nil
}

// hasEpochLocked reports whether a watch epoch is currently installed on
// this LiveView (lv.listenCtx != nil) — regardless of whether its underlying
// context has already died (lv.listenCtx.Err() != nil). Must be called with
// mu held.
//
// Deliberately WEAKER than isActiveLocked, which additionally requires
// Err() == nil — the right check for attach()'s piggyback decision and
// detach()'s teardown decision, where an already-dead epoch correctly means
// "not a live watch to preserve/piggyback on". onTabsChanged and
// rebindWatch need this weaker check instead (live-UAT fix, 2026-07-12 —
// "closing the ACTIVE tab shows a false 'session ended' banner and leaves
// the live view stuck on the old tab"): BrowserManager.CloseTab cancels the
// closed tab's own chromedp context BEFORE it calls notifyTabsChanged — and
// since lv.listenCtx is a CHILD of the active tab's context, that
// cancellation SYNCHRONOUSLY kills lv.listenCtx too, before
// onTabsChanged/rebindWatch ever run. By the time either of them runs, the
// just-closed epoch therefore already looks "dead" by isActiveLocked's
// definition even though nothing has cleaned it up yet and the browsing
// context itself (plus every sibling tab) is perfectly alive — closing any
// one tab, including the active one, never tears down the browser (ADR-041's
// browserCtx fix). Gating the rebind decision on isActiveLocked() (== alive)
// therefore deterministically skipped the rebind in exactly this case.
// Gating on "an epoch is installed at all" instead correctly recognizes
// there is still a (now-defunct) epoch owed a replacement, whether or not
// its underlying context happened to have already died by the time this
// runs. See watchForUnexpectedDeath's doc comment for the matching false
// "session ended" broadcast half of this fix.
func (lv *LiveView) hasEpochLocked() bool {
	return lv.listenCtx != nil
}

// attach registers viewerID's sinks and, if no watch is currently active for
// this session, starts watching its tab for unexpected death (ADR-038
// finding #2). onStatus may be nil.
//
// ADR-061: this used to also start (or piggyback on) a CDP JPEG screencast
// here, which required releasing lv.mu before a blocking chromedp.Run call
// (see the ADR-038 deadlock postmortem this file's other CDP call sites
// still document — runCDPWithTimeout's doc comment). Attaching a viewer is
// now pure in-memory bookkeeping with no CDP round trip at all, so that
// unlock/relock dance is gone: this method runs start-to-finish under one
// lv.mu acquisition and cannot fail.
//
// Returns controlledByOther (ADR-039 UAT BE-1): true when sessionID is
// already controlled by a viewer other than viewerID at the moment of this
// attach.
func (lv *LiveView) attach(
	tabCtx context.Context,
	viewerID string,
	onStatus StatusSink,
	onControl ControlSink,
	onTabs TabsSink,
) (bool, error) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.viewers[viewerID] = struct{}{}
	if onStatus != nil {
		lv.statusSinks[viewerID] = onStatus
	}
	if onControl != nil {
		lv.controlSinks[viewerID] = onControl
	}
	if onTabs != nil {
		lv.tabsSinks[viewerID] = onTabs
	}
	controlledByOther := lv.controller != "" && lv.controller != viewerID

	if lv.isActiveLocked() {
		// Already watching this session — this viewer piggybacks on it.
		return controlledByOther, nil
	}

	lv.tabCtx = tabCtx
	listenCtx, cancel := context.WithCancel(tabCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel

	// ADR-038 finding #2: watch for this tab context dying WITHOUT going
	// through detach() first — e.g. BrowserManager.Shutdown() canceling
	// every session context out from under an attached viewer during a
	// hot-reload manager replacement (pkg/agent/loop.go's
	// registerSharedTools). One watcher per watch "epoch" (i.e. per
	// listenCtx); it self-identifies as stale once a clean detach or a
	// fresh attach cycle has moved lv.listenCtx on.
	go lv.watchForUnexpectedDeath(listenCtx)

	return controlledByOther, nil
}

// onTabsChanged is invoked (ADR-041 D4, via LiveViewRegistry.handleTabsChanged
// ← BrowserManager.tabsChanged) whenever this session's tab set changes.
// Broadcasts a snapshot to every attached viewer's TabsSink and, if the
// active tab moved to a different underlying chromedp target, rebinds the
// death watch to follow it. Never tears down the browsing context — only
// the watch's target moves.
func (lv *LiveView) onTabsChanged(tabs []Tab, activeIdx int) {
	lv.mu.Lock()
	sinks := make([]TabsSink, 0, len(lv.tabsSinks))
	for _, s := range lv.tabsSinks {
		sinks = append(sinks, s)
	}
	lv.mu.Unlock()

	for _, s := range sinks {
		s(tabs, activeIdx)
	}

	// Session() always resolves the ACTIVE tab's context (ADR-041 D1) — reuse
	// it here instead of threading a raw ctx through the tabs-changed
	// callback, so Tab (the public snapshot type) can stay metadata-only.
	newCtx, err := lv.mgr.Session(lv.sessionID)
	if err != nil {
		// Nothing to rebind to — e.g. the browsing context is mid-recreation
		// after a crash. watchForUnexpectedDeath already handles notifying
		// attached viewers if the tab context died out from under them.
		return
	}

	lv.mu.Lock()
	// hasEpochLocked, not isActiveLocked (live-UAT fix, 2026-07-12): see its
	// doc comment — closing the ACTIVE tab already kills lv.listenCtx (a
	// child of that tab's own context) by the time this runs, so gating on
	// "alive" would deterministically skip the rebind this close owes.
	needsRebind := lv.hasEpochLocked() && lv.tabCtx != newCtx
	// activeTabChanged (ADR-047, wave-plan W2-A item 5): "did the active tab
	// actually change" signal for the WebRTC recapture hook below — see
	// lastKnownActiveCtx's doc comment for why this can't reuse
	// needsRebind/tabCtx. Guarded on lastKnownActiveCtx != nil so the very
	// first onTabsChanged call (which only establishes the baseline) never
	// counts as a "change".
	activeTabChanged := lv.lastKnownActiveCtx != nil && lv.lastKnownActiveCtx != newCtx
	lv.lastKnownActiveCtx = newCtx
	lv.mu.Unlock()

	if activeTabChanged {
		// Notify the agent's WebRTC capture session (if any) that the active
		// tab changed, so its encoder re-binds chrome.tabCapture to the new
		// target (wave-plan W2-A item 5: "recapture on active-tab switch").
		// A no-op when no CaptureSession is active (WebRTC never used, or
		// this LiveView's session isn't the one CaptureSessions key off —
		// CaptureSession() is scoped to the manager, not per-sessionID).
		if cs := lv.mgr.CaptureSession(); cs != nil {
			cs.Recapture()
		}
	}

	if needsRebind {
		lv.rebindWatch(newCtx)
	}
}

// rebindWatch re-targets an ALREADY-ACTIVE death watch to newCtx (ADR-041
// D4 — the tab-strip switch), without touching the browsing context (a
// chromedp target's lifetime is independent of this) and without dropping
// any attached viewer's registration — only the watched target moves, so
// every viewer keeps watching the SAME logical live session, now following
// the new active tab. A no-op if no watch is currently installed (e.g. no
// viewers attached — the next Attach simply resolves the by-then-current
// active tab via mgr.Session, so there's nothing to rebind yet).
//
// ADR-061: this used to stop a CDP screencast on the old tab and start a
// fresh one on the new tab, which — because both were real CDP round trips
// — could not be done under a single lock held throughout (ADR-038's no-
// lock-across-a-CDP-call discipline), and needed a self-correcting loop plus
// two documented races (F1 ordering, Findings A/B) to stay correct across
// concurrent attach/detach/rebind. Retargeting a death watch has no CDP call
// at all: canceling the old watch and installing the new one is pure
// in-memory bookkeeping, so the whole operation now runs under one lv.mu
// acquisition with no unlock in between — the interleaving windows those
// fixes existed to close no longer exist, and the fixes (along with the
// self-correcting retry loop) are gone with them.
func (lv *LiveView) rebindWatch(newCtx context.Context) {
	lv.mu.Lock()
	if !lv.hasEpochLocked() || lv.tabCtx == newCtx {
		lv.mu.Unlock()
		return
	}
	oldStopListen := lv.stopListen
	lv.tabCtx = newCtx
	listenCtx, cancel := context.WithCancel(newCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.mu.Unlock()

	// Cancel the OLD watch after installing the new one, under no lock — the
	// old watcher's own "am I still the installed epoch" check
	// (watchForUnexpectedDeath) already sees lv.listenCtx pointing at the
	// NEW epoch by the time it wakes, so it correctly treats this as
	// superseded, not a genuine death.
	if oldStopListen != nil {
		oldStopListen()
	}
	go lv.watchForUnexpectedDeath(listenCtx)
}

// watchForUnexpectedDeath blocks until watchedListenCtx is Done, then decides
// whether that was a genuine, unexpected browser death or merely a tab
// close/switch that happened to cancel THIS epoch's own listenCtx (a child
// of the tab's own chromedp context) — only the former is ever broadcast to
// attached viewers as "session ended". detach() always nils lv.listenCtx out
// BEFORE canceling it, so watchedListenCtx dying while STILL installed as
// lv.listenCtx never means a clean detach; it means either (a) the whole
// browsing context died (BrowserManager.Shutdown/CloseSession, or a genuine
// crash) or (b) CloseTab/SwitchTab (ADR-041) canceled the specific tab this
// epoch was bound to while the browser and its sibling tabs stayed alive.
//
// Live-UAT fix (2026-07-12, "closing the ACTIVE tab shows a false 'session
// ended unexpectedly' banner and leaves the live view stuck on the closed
// tab's stale content"): before this fix, this function could not tell (a)
// from (b) — any dead watchedListenCtx was always treated as a genuine
// death. Since the ADR-041 browserCtx fix, the browser (and every OTHER tab)
// reliably SURVIVES closing any one tab, including the active one — so a
// dead listenCtx no longer implies a dead browser. mgr.browserAlive is a
// cheap, side-effect-free check (BrowserManager's own lock only — never a
// CDP round trip, never Session()'s create-or-recover-on-death behavior, so
// it can never accidentally relaunch a Chromium for what might be a
// deliberate whole-manager Shutdown()) that distinguishes the two:
//
//   - Browsing context still alive (case b): deliberately leave
//     lv.listenCtx/lv.stopListen UNTOUCHED and return WITHOUT broadcasting.
//     CloseTab/SwitchTab's own notifyTabsChanged -> onTabsChanged call
//     (which always fires, independent of this watcher goroutine's own
//     scheduling) is what performs the actual rebind to the surviving/
//     newly-active tab, via hasEpochLocked's weaker "an epoch is installed,
//     dead or alive" gate (see its doc comment) rather than this now
//     intentionally-untouched dead epoch being mistaken for "nothing to
//     rebind". Leaving the watcher a no-op here — rather than having it ALSO
//     attempt its own rebind — avoids a second, independent
//     teardown/install racing onTabsChanged's; rebindWatch installs the new
//     epoch and only THEN cancels the old watch (see its doc comment), so
//     onTabsChanged's path is safe to run unconditionally.
//   - Browsing context genuinely gone (case a): the pre-fix behavior,
//     preserved — clear the epoch and broadcast "session ended" so attached
//     viewers know to re-attach.
func (lv *LiveView) watchForUnexpectedDeath(watchedListenCtx context.Context) {
	<-watchedListenCtx.Done()

	lv.mu.Lock()
	if lv.listenCtx != watchedListenCtx {
		// A clean detach, or a rebind already triggered elsewhere (e.g.
		// onTabsChanged, possibly racing this very watcher), already
		// superseded this epoch — nothing left for this watcher to do.
		lv.mu.Unlock()
		return
	}
	sessionID := lv.sessionID
	mgr := lv.mgr
	lv.mu.Unlock()

	if mgr != nil && mgr.browserAlive(sessionID) {
		// Not a death — a tab close/switch. See the doc comment above for
		// why leaving lv.listenCtx exactly as-is (and returning without
		// broadcasting) is what lets the real rebind proceed correctly.
		return
	}

	// ADR-047 / wave-plan W2-A item 5 ("also on browser_status-relevant
	// lifecycle: browser death -> stop session"): the browsing context is
	// genuinely gone, so any active WebRTC capture session for this agent
	// has nothing left to capture — its encoder page's own CDP target died
	// along with the rest of the browser context. Stop() is idempotent and
	// safe even if the session already noticed independently.
	if mgr != nil {
		if cs := mgr.CaptureSession(); cs != nil {
			cs.Stop()
		}
	}

	lv.mu.Lock()
	if lv.listenCtx != watchedListenCtx {
		// Superseded while this goroutine was checking mgr.browserAlive.
		lv.mu.Unlock()
		return
	}
	lv.listenCtx = nil
	lv.stopListen = nil
	sinks := lv.snapshotStatusSinksLocked()
	lv.mu.Unlock()

	for _, s := range sinks {
		s("browser session ended unexpectedly (the browser was restarted or shut down) — re-attach to resume watching")
	}
}

// detach removes viewerID and, if it was the last viewer, stops watching
// the session's tab for unexpected death.
func (lv *LiveView) detach(viewerID string) {
	lv.mu.Lock()
	delete(lv.viewers, viewerID)
	delete(lv.statusSinks, viewerID)
	delete(lv.controlSinks, viewerID)
	delete(lv.tabsSinks, viewerID)
	wasController := lv.controller == viewerID
	if wasController {
		lv.controller = ""
	}
	var otherSinks []ControlSink
	if wasController {
		// The departing viewer held control — every other attached viewer
		// must learn the lock is now free (ADR-039 UAT BE-1), same as an
		// explicit release. viewerID was already removed from controlSinks
		// above, so this snapshot naturally excludes it.
		otherSinks = lv.snapshotControlSinksExceptLocked(viewerID)
	}

	var stopListen context.CancelFunc
	if len(lv.viewers) == 0 && lv.isActiveLocked() {
		stopListen = lv.stopListen
		lv.listenCtx = nil
		lv.stopListen = nil
	}
	lv.mu.Unlock()

	// No lock held here — see the takeControl() convention this mirrors.
	// Fired unconditionally (independent of whether this was the last
	// viewer) so a departing controller's implicit release is broadcast even
	// when other viewers remain attached. Dispatched via broadcastControl
	// (B2) so a slow OTHER viewer's sink can never stall this connection's
	// own detach/disconnect cleanup path — see broadcastControl's doc
	// comment.
	broadcastControl(otherSinks, false)

	if stopListen != nil {
		stopListen()
	}
}

// LiveInputErrorKind classifies a LiveViewRegistry.Input / dispatchInput
// failure (ADR-038 finding #4) so the gateway's handleInput can decide
// whether it's worth surfacing to the user as a browser_status(error) frame.
type LiveInputErrorKind int

const (
	// LiveInputErrorBenign covers expected, high-frequency rejections: the
	// viewer doesn't currently hold control (e.g. a stray event sent just
	// after losing control) or the per-second rate limit was hit. These are
	// routine and NOT worth a status frame — the previous behavior (Debug
	// log only, no status frame) is preserved for this kind.
	LiveInputErrorBenign LiveInputErrorKind = iota
	// LiveInputErrorReal covers everything else: no live view/session ever
	// attached, the tab context is missing, an unknown/malformed input kind,
	// or — most importantly — a genuine chromedp.Run transport failure,
	// which is the signal that the browser tab crashed or is unreachable.
	// Before this fix ALL of these were logged at Debug and invisible to the
	// user; a dead browser looked identical to a healthy, idle one.
	LiveInputErrorReal
)

// LiveInputError wraps a LiveViewRegistry.Input failure with its
// classification. Implements error (via Unwrap, so errors.Is/As still see
// through to the underlying cause).
type LiveInputError struct {
	Kind LiveInputErrorKind
	err  error
}

func (e *LiveInputError) Error() string { return e.err.Error() }
func (e *LiveInputError) Unwrap() error { return e.err }

func benignInputError(format string, args ...any) error {
	return &LiveInputError{Kind: LiveInputErrorBenign, err: fmt.Errorf(format, args...)}
}

// ErrViewerNotController is the specific benign rejection meaning "this
// viewer sent input but does not hold the session's control lock".
//
// 2026-07-30 UAT — why this needs to be distinguishable from every other
// benign rejection: it is the only one that indicates the CLIENT'S BELIEF IS
// WRONG rather than that the input was merely unwanted. A rate-limit
// rejection is self-correcting (slow down and the next event lands); this
// one never self-corrects, because the client goes on believing it is
// driving and the server goes on discarding everything it sends. The
// operator hit exactly that: the panel read "You're driving" while 448
// consecutive inputs were rejected and silently dropped at debug level.
// Callers use IsNotControllerLiveInputError to detect it and push the
// authoritative control state back to that viewer so the UI can correct
// itself. See pkg/gateway/browser_webrtc.go's data-channel input handler.
var ErrViewerNotController = errors.New("browser live: viewer does not hold control of this session")

// IsNotControllerLiveInputError reports whether err is the "viewer does not
// hold control" rejection — see ErrViewerNotController.
func IsNotControllerLiveInputError(err error) bool {
	return errors.Is(err, ErrViewerNotController)
}

func realInputError(format string, args ...any) error {
	return &LiveInputError{Kind: LiveInputErrorReal, err: fmt.Errorf(format, args...)}
}

// IsBenignLiveInputError reports whether err (returned by
// LiveViewRegistry.Input) is a benign, high-frequency rejection that callers
// should log quietly rather than surface to the user (ADR-038 finding #4).
// An error that isn't a *LiveInputError at all (shouldn't happen — every
// return path in this file uses the classified constructors) is treated as
// real/not-benign, the fail-safe direction for a security-adjacent surface.
func IsBenignLiveInputError(err error) bool {
	var liveErr *LiveInputError
	return errors.As(err, &liveErr) && liveErr.Kind == LiveInputErrorBenign
}

// dispatchInput validates control + rate limit, then dispatches one CDP
// input action. Called with no locks held by the caller.
func (lv *LiveView) dispatchInput(viewerID string, in LiveInput) error {
	lv.mu.Lock()
	// NO CONTROL GATE (operator directive, 2026-08-03). The live panel is a
	// REAL BROWSER the human uses normally, and the agent can steer it too —
	// both, concurrently. Input is never refused because some other viewer
	// "holds the wheel".
	//
	// This replaced an exclusive single-controller lock that refused every
	// event unless viewerID matched lv.controller. Measured consequence: a
	// second attached viewer (another panel, a pop-out, an automation session
	// that never detached) left the actual human with a dead mouse, dead
	// keyboard, and a URL bar that would not submit — the panel showed
	// "Someone else is driving" and silently dropped everything the user did.
	// A browser that refuses input is not a browser.
	//
	// lv.controller is retained for PRESENTATION only (who to show as active
	// in the header, the ADR-039 controlSinks broadcast); it must never again
	// become an authorization decision on this path.
	if !lv.allowInputLocked(in.Kind) {
		lv.mu.Unlock()
		limit := maxDiscreteInputEventsPerSecond
		if isCoalescibleInputKind(in.Kind) {
			limit = maxCoalescibleInputEventsPerSecond
		}
		return benignInputError("browser live: input rate limit exceeded for %s (%d/s)", in.Kind, limit)
	}
	tabCtx := lv.tabCtx
	lv.mu.Unlock()

	if tabCtx == nil {
		return realInputError("browser live: session is not attached")
	}

	// Root-cause doc Fault 3: x/y arrive in the CLIENT's capture-frame pixel
	// space, which is no longer guaranteed to equal the tab's CSS pixel
	// space now that SetViewport (Fault 1 fix, above) can resize the tab
	// independently of what the encoder's downscaling happens to produce.
	// Only pointer-position kinds carry a meaningful position to rescale —
	// wheel's DeltaX/DeltaY are scroll deltas, not positions, and key/text
	// carry no coordinates at all.
	switch in.Kind {
	case "mouse_move", "mouse_down", "mouse_up", "wheel":
		if in.HasXY && in.CaptureWidth > 0 && in.CaptureHeight > 0 {
			rx, ry, ok := lv.rescaleToCSSViewport(tabCtx, in.X, in.Y, in.CaptureWidth, in.CaptureHeight)
			if !ok {
				// DROP rather than dispatch at an unmapped coordinate — see
				// rescaleToCSSViewport's doc comment: unscaled coordinates
				// land ~34% off (measured), i.e. on the wrong element, and a
				// mis-aimed click can navigate away, delete or submit.
				//
				// Classification matters as much as the drop. A one-off miss
				// is a transient the user retries past, so it stays benign and
				// silent. But a SUSTAINED streak means the CDP transport is
				// wedged or the tab is dead — and LiveInputErrorReal's own doc
				// comment names exactly that as the thing that must reach the
				// user ("a dead browser looked identical to a healthy, idle
				// one", ADR-038 finding #4). Without this escalation a crashed
				// tab would swallow every click forever with no error, since
				// pointer kinds bail out here and never reach the real-error
				// CDP dispatch below.
				lv.mu.Lock()
				failures := lv.viewportFetchFailures
				lv.mu.Unlock()
				if failures >= viewportFetchFailureEscalation {
					return realInputError(
						"browser live: cannot read the tab's CSS viewport after %d consecutive attempts — the browser tab may have crashed or the CDP transport is wedged",
						failures,
					)
				}
				return benignInputError(
					"browser live: viewport unknown, dropped %s to avoid a mis-aimed dispatch",
					in.Kind,
				)
			}
			in.X, in.Y = rx, ry
		}
	}

	action, err := buildInputAction(in)
	if err != nil {
		return realInputError("%w", err)
	}

	// ADR-039 D-A2 (BLOCKING): a user-driven navigate MUST pass the same
	// SSRF/scheme gate the agent's browser_navigate tool applies
	// (BrowserManager.ValidateURL — tools.go's NavigateTool.Execute) before
	// ever reaching CDP. The live-WS input path otherwise has no URL gate of
	// its own. A blocked URL is a real, user-visible failure (not the benign
	// not-controller/rate-limit kind) so the gateway surfaces it as a
	// browser_status(error) frame instead of silently dropping it.
	//
	// 7-reviewer BLOCKER: ValidateURL's SSRF check does DNS resolution
	// (resolver.LookupIPAddr) with no deadline of its own. tabCtx is the
	// live agent tab's own context — it does not expire on any per-call
	// schedule — so calling ValidateURL(tabCtx, ...) directly means a
	// blackholed/slow-DNS hostname can hang this call for however long the
	// resolver is willing to wait (its own internal ceiling, 10-30s+ or
	// unbounded). Because handleInput (browser_ws.go) runs synchronously in
	// the connection's single readLoop goroutine, that hang freezes the
	// WHOLE connection — it can't even process a browser_detach — which is
	// exactly the unbounded-wait hazard the ADR-038 deadlock postmortem
	// documented in this file (see runCDPWithTimeout's doc comment) exists
	// to prevent. Mirror that same fix here: bound the call to a
	// context.WithTimeout child of tabCtx, so even a wedged resolver fails
	// this one navigate attempt in bounded time instead of hanging forever.
	if in.Kind == "navigate" {
		validateCtx, cancel := context.WithTimeout(tabCtx, lv.mgr.PageTimeout())
		err := lv.mgr.ValidateURL(validateCtx, in.URL)
		cancel()
		if err != nil {
			return realInputError("browser live: navigate blocked: %w", err)
		}
	}

	// No lock held here (already released above) — bounded via lv.runCDP so
	// a wedged transport can't hang the caller (the gateway's input-handling
	// goroutine) forever.
	if err := lv.runCDP(tabCtx, lv.mgr.PageTimeout(), action); err != nil {
		return realInputError("browser live: input dispatch failed: %w", err)
	}
	return nil
}

// rescaleToCSSViewport maps (x, y) from the client's capture-frame pixel
// space (capW x capH) into the tab's actual CSS pixel space, using the
// cssViewportW/H cache SetViewport maintains (root-cause doc Fault 3). Called
// with no LiveView lock held, per the ADR-038 CDP-call discipline this file
// observes everywhere else.
//
// If nothing has populated the cache yet — no SetViewport call this session
// yet, or a prior read-back was invalidated (invalidateCSSViewportCache) —
// this fetches it once via Page.getLayoutMetrics, bounded by the much
// shorter viewportInputFetchTimeout, NOT SetViewport's viewportSetTimeout
// (review CRITICAL finding, item 3): this call runs on the dispatchInput ->
// WS-read-loop hot path, once per input event on every cache miss, not on a
// rare, debounced user-triggered resize, so a slow/wedged CDP transport must
// fail fast here instead of stalling input throughput up to a multi-second
// timeout per event.
//
// On fetch failure it reports ok=false and the caller DROPS the event.
// This reverses the previous behavior, which dispatched unscaled on the
// reasoning that "a slightly-off click still beats a completely dead panel".
// Measurement killed that argument (2026-08-03): the capture frame and the CSS
// viewport differed by 562 vs 369 px, so an unscaled click lands ~34% below
// where the user aimed — reliably on the WRONG element, not merely near the
// right one. A dropped click is a no-op the user retries; a mis-aimed click
// activates something they did not choose, which on a real page can navigate
// away, delete, or submit. Silence beats a wrong action.
//
// Failure backoff (item 3b): a failed fetch arms lv.nextFetchAfter
// viewportInputFetchBackoff into the future. While that window is open,
// further cache-miss calls are DROPPED (see above) WITHOUT retrying the fetch or
// re-logging the failure — before this fix, a sustained CDP hiccup meant
// every subsequent input event repeated the same (bounded, but non-zero
// cost) failing round trip and logged a fresh WARN, collapsing input
// throughput to roughly one event per timeout. The failure is logged once,
// when it actually happens, not once per event that finds the cache still
// empty.
func (lv *LiveView) rescaleToCSSViewport(tabCtx context.Context, x, y, capW, capH float64) (rx, ry float64, ok bool) {
	lv.mu.Lock()
	cssW, cssH := lv.cssViewportW, lv.cssViewportH
	inBackoff := !lv.nextFetchAfter.IsZero() && time.Now().Before(lv.nextFetchAfter)
	lv.mu.Unlock()

	if cssW <= 0 || cssH <= 0 {
		if inBackoff {
			// Already logged when the fetch actually failed — staying quiet
			// here is the whole point of the backoff window.
			return 0, 0, false
		}

		var w, h int64
		err := lv.runCDP(tabCtx, viewportInputFetchTimeout, layoutMetricsAction{w: &w, h: &h})
		if err != nil || w <= 0 || h <= 0 {
			logger.WarnCF(
				"browser",
				"live view: input rescale — could not read the tab's CSS viewport, DROPPING this positional event (backing off further fetches)",
				map[string]any{
					"session_id":      lv.sessionID,
					"backoff_seconds": viewportInputFetchBackoff.Seconds(),
				},
			)
			lv.mu.Lock()
			lv.nextFetchAfter = time.Now().Add(viewportInputFetchBackoff)
			lv.viewportFetchFailures++
			lv.mu.Unlock()
			return 0, 0, false
		}

		lv.mu.Lock()
		lv.cssViewportW, lv.cssViewportH = int(w), int(h)
		lv.nextFetchAfter = time.Time{}
		lv.viewportFetchFailures = 0
		lv.mu.Unlock()
		cssW, cssH = int(w), int(h)
	}

	rx, ry = rescaleInputCoords(x, y, capW, capH, float64(cssW), float64(cssH))
	return rx, ry, true
}

// rescaleInputCoords maps (x, y) from the capture frame's pixel space
// (capW x capH) into the tab's CSS pixel space (cssW x cssH). Pure math, no
// CDP — factored out of rescaleToCSSViewport so it is unit-testable without
// a real Chromium (root-cause doc Fault 3: the assumption that
// videoWidth/videoHeight == page CSS pixels is false whenever the encoder
// downscales, e.g. the measured 319x158 capture against a ~1280-wide page).
// Callers must guard against capW/capH <= 0 themselves — this function does
// not, so it can stay a trivial, allocation-free ratio computation.
func rescaleInputCoords(x, y, capW, capH, cssW, cssH float64) (float64, float64) {
	return x * cssW / capW, y * cssH / capH
}

// allowInputLocked applies a simple fixed-window rate limiter. Must be called
// with mu held.
// isCoalescibleInputKind reports whether a dropped event of this kind is
// self-healing: a later event of the same kind fully supersedes it. Position
// updates are; state transitions are not, and must not share their budget.
func isCoalescibleInputKind(kind string) bool {
	return kind == "mouse_move" || kind == "wheel"
}

// allowInputLocked charges the event against the bucket for its kind and
// reports whether it may proceed. Both buckets share one fixed window.
func (lv *LiveView) allowInputLocked(kind string) bool {
	now := time.Now()
	if now.Sub(lv.inputWindowStart) >= time.Second {
		lv.inputWindowStart = now
		lv.inputCount = 0
		lv.discreteCount = 0
	}
	if isCoalescibleInputKind(kind) {
		if lv.inputCount >= maxCoalescibleInputEventsPerSecond {
			return false
		}
		lv.inputCount++
		return true
	}
	if lv.discreteCount >= maxDiscreteInputEventsPerSecond {
		return false
	}
	lv.discreteCount++
	return true
}

// takeControl grants viewerID control if uncontrolled or already the
// controller; returns false if someone else holds it. On a successful grant,
// every OTHER attached viewer's ControlSink is invoked with true (ADR-039
// UAT BE-1: "two viewers disagree about who's driving") — the server has
// always single-controller-locked here, this is what makes every OTHER
// connection's display agree with that lock instead of continuing to show
// stale state. The sinks are dispatched via broadcastControl (no lock held).
// ensureControlForInput is EnsureControlForInput's per-view half — see that
// method's doc comment for the model and the two failures it closes.
func (lv *LiveView) ensureControlForInput(viewerID string) bool {
	lv.mu.Lock()
	if lv.controller == viewerID {
		lv.mu.Unlock()
		return true
	}
	if lv.controller != "" {
		if _, stillAttached := lv.viewers[lv.controller]; stillAttached {
			// A real, live viewer is driving — refuse. This is the only case
			// where denying a human's input is correct, and it is what keeps
			// two simultaneous humans from fighting over the same page.
			lv.mu.Unlock()
			return false
		}
		// Holder is a ghost: it owns the lock but is no longer attached, so
		// it can never release it. Steal rather than stay wedged forever.
	}
	lv.controller = viewerID
	otherSinks := lv.snapshotControlSinksExceptLocked(viewerID)
	lv.mu.Unlock()

	broadcastControl(otherSinks, true)
	return true
}

func (lv *LiveView) takeControl(viewerID string) bool {
	lv.mu.Lock()
	if lv.controller != "" && lv.controller != viewerID {
		lv.mu.Unlock()
		return false
	}
	lv.controller = viewerID
	otherSinks := lv.snapshotControlSinksExceptLocked(viewerID)
	lv.mu.Unlock()

	broadcastControl(otherSinks, true)
	return true
}

// releaseControl clears the control lock only if viewerID currently holds
// it. On an actual release, every OTHER attached viewer's ControlSink is
// invoked with false (ADR-039 UAT BE-1) so a stale "someone else is driving"
// display clears the moment the lock is actually freed. No-op (and no
// broadcast) if viewerID isn't the current controller.
func (lv *LiveView) releaseControl(viewerID string) {
	lv.mu.Lock()
	if lv.controller != viewerID {
		lv.mu.Unlock()
		return
	}
	lv.controller = ""
	otherSinks := lv.snapshotControlSinksExceptLocked(viewerID)
	lv.mu.Unlock()

	broadcastControl(otherSinks, false)
}

// broadcastControl dispatches controlledByOther to every sink in sinks, each
// on its own goroutine (B2, 7-reviewer finding: reintroduced-deadlock-hazard
// class per the ADR-038 postmortem documented above attach()). handleControl
// (browser_ws.go) runs in the acting connection's single-goroutine readLoop,
// and each sink's underlying delivery (the gateway's sendCritical) can block
// up to 2s waiting for a slow/wedged peer connection's send buffer to drain.
// Invoking N sinks synchronously in a loop — as takeControl/releaseControl/
// detach used to — would therefore freeze the ACTING viewer's own connection
// for up to N*2s whenever other viewers are slow, even though the actor's
// own take/release/detach has nothing to do with how fast anyone else is
// draining their socket. Firing each sink on its own goroutine bounds the
// caller's wait to O(1) regardless of N or how slow any individual sink is —
// the same "never let a slow consumer stall the actor" contract
// StatusSink/ControlSink's doc comments already require, just not previously
// honored at THIS fan-out site. Callers must still invoke this with no
// LiveView lock held (sinks may themselves call back into LiveView methods
// that take lv.mu).
func broadcastControl(sinks []ControlSink, controlledByOther bool) {
	for _, sink := range sinks {
		go sink(controlledByOther)
	}
}

// getController returns the current controller viewerID, or "".
func (lv *LiveView) getController() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.controller
}

// snapshotStatusSinksLocked returns every registered StatusSink — used by
// watchForUnexpectedDeath's "session ended" broadcast. Must be called with
// mu held; the returned sinks must be invoked with no lock held.
func (lv *LiveView) snapshotStatusSinksLocked() []StatusSink {
	sinks := make([]StatusSink, 0, len(lv.statusSinks))
	for _, s := range lv.statusSinks {
		sinks = append(sinks, s)
	}
	return sinks
}

// snapshotControlSinksExceptLocked returns every registered ControlSink
// except excludeViewerID's own (ADR-039 UAT BE-1: the acting viewer gets its
// outcome via its own direct browser_status response, not a broadcast — see
// ControlSink's doc comment). Must be called with mu held; the returned
// sinks must be invoked with no lock held (mirrors broadcastControl's callers).
func (lv *LiveView) snapshotControlSinksExceptLocked(excludeViewerID string) []ControlSink {
	sinks := make([]ControlSink, 0, len(lv.controlSinks))
	for id, sink := range lv.controlSinks {
		if id == excludeViewerID {
			continue
		}
		sinks = append(sinks, sink)
	}
	return sinks
}

// clampModifiers clamps the CDP modifier bitmask to the valid [0,15] range
// (Alt=1, Ctrl=2, Meta=4, Shift=8; ADR-038 finding #5) — defense in depth
// alongside the wire schema's minimum/maximum constraint
// (BrowserInputFrame.yaml), since schema validation only runs when
// gateway.validate_inbound=true (finding #3). Without this, an
// out-of-range value would be passed straight to cdproto's input.Modifier,
// which has no validation of its own.
func clampModifiers(m int) int {
	if m < 0 {
		return 0
	}
	if m > 15 {
		return 15
	}
	return m
}

// buildInputAction maps a wire-level LiveInput to the corresponding CDP
// Input.dispatch* action (spike-proven mechanics, ADR-038 context section).
//
// ADR-038 finding #5: every kind is validated before building its action —
// previously only "text" was guarded, so a malformed mouse/wheel frame with
// no coordinates silently dispatched a (0,0)-origin event (a click in the
// page's top-left corner) instead of being rejected, and a malformed key
// event with neither key nor code silently dispatched a no-op keystroke.
func buildInputAction(in LiveInput) (chromedp.Action, error) {
	mods := input.Modifier(clampModifiers(in.Modifiers))

	switch in.Kind {
	case "mouse_move":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_move input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseMoved, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithModifiers(mods), nil
	case "mouse_down":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_down input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MousePressed, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "mouse_up":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: mouse_up input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseReleased, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "wheel":
		if !in.HasXY {
			return nil, fmt.Errorf("browser live: wheel input requires x and y coordinates")
		}
		return input.DispatchMouseEvent(input.MouseWheel, in.X, in.Y).
			WithDeltaX(in.DeltaX).
			WithDeltaY(in.DeltaY).
			WithModifiers(mods), nil
	case "key_down":
		if in.Key == "" && in.Code == "" {
			return nil, fmt.Errorf("browser live: key_down input requires a key or code")
		}
		// CDP dispatches two keydown variants and the split is what decides
		// whether the browser PERFORMS the key: "keyDown" runs text processing
		// and default actions (Enter submits the focused form, inserts a
		// newline in a textarea); "rawKeyDown" delivers the DOM event only.
		// Live UAT 2026-07-31: typing into a remote page's search box then
		// pressing Enter did nothing, because every key_down went out as
		// rawKeyDown with empty text — the virtual key code alone never
		// triggers form submission. Mirror Puppeteer's convention exactly:
		// synthesize text "\r" for Enter when the client sent none, and use
		// keyDown whenever text is present, rawKeyDown otherwise.
		text := in.Text
		if text == "" && in.Key == "Enter" {
			text = "\r"
		}
		keyType := input.KeyRawDown
		if text != "" {
			keyType = input.KeyDown
		}
		return input.DispatchKeyEvent(keyType).
			WithKey(in.Key).
			WithCode(in.Code).
			WithText(text).
			WithWindowsVirtualKeyCode(int64(in.KeyCode)).
			WithNativeVirtualKeyCode(int64(in.KeyCode)).
			WithModifiers(mods), nil
	case "key_up":
		if in.Key == "" && in.Code == "" {
			return nil, fmt.Errorf("browser live: key_up input requires a key or code")
		}
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(in.Key).
			WithCode(in.Code).
			WithWindowsVirtualKeyCode(int64(in.KeyCode)).
			WithNativeVirtualKeyCode(int64(in.KeyCode)).
			WithModifiers(mods), nil
	case "text":
		if in.Text == "" {
			return nil, fmt.Errorf("browser live: text input requires a non-empty text field")
		}
		return input.InsertText(in.Text), nil
	case "navigate":
		// Defense-in-depth (7-reviewer LOW finding, see LiveInput.URL's doc
		// comment): a "navigate" input carrying HasXY would be a malformed/
		// confused frame — reject it rather than silently ignoring X/Y, so a
		// future refactor that starts reading X/Y for this kind fails loudly
		// instead of quietly bypassing the SSRF gate for what looked like a
		// mouse event.
		if in.HasXY {
			return nil, fmt.Errorf("browser live: navigate input must not carry x/y coordinates")
		}
		if in.URL == "" {
			return nil, fmt.Errorf("browser live: navigate input requires a non-empty url field")
		}
		return chromedp.Navigate(in.URL), nil
	case "navigate_back":
		// History back — no URL (goes to a previously-navigated page, already
		// SSRF-cleared on its original navigate). Discrete, like navigate.
		if in.HasXY || in.URL != "" {
			return nil, fmt.Errorf("browser live: navigate_back input must not carry x/y or url")
		}
		return chromedp.NavigateBack(), nil
	case "reload":
		// Reload the current URL (already SSRF-cleared). Discrete, like navigate.
		if in.HasXY || in.URL != "" {
			return nil, fmt.Errorf("browser live: reload input must not carry x/y or url")
		}
		return chromedp.Reload(), nil
	default:
		return nil, fmt.Errorf("browser live: unknown input kind %q", in.Kind)
	}
}

// mouseButton maps the wire button string to its cdproto MouseButton value.
// Unknown/empty maps to None, matching CDP's own default.
func mouseButton(b string) input.MouseButton {
	switch b {
	case "left":
		return input.Left
	case "middle":
		return input.Middle
	case "right":
		return input.Right
	case "back":
		return input.Back
	case "forward":
		return input.Forward
	default:
		return input.None
	}
}
