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

// ADR-038 D3 — screencast tuning. Spike-proven on chrome-headless-shell
// (2026-07-11): JPEG quality 60 at 1280x720, every frame, keeps bandwidth
// reasonable for a browser-rendered <img>/canvas while staying legible.
const (
	screencastQuality       = 60
	screencastMaxWidth      = 1280
	screencastMaxHeight     = 720
	screencastEveryNthFrame = 1
)

// agentWindowWidth/Height size each agent's Chrome window (coordinator.go's
// CreateTarget). Deliberately SEPARATE from the screencast caps above, which
// tune JPEG bandwidth: a window must be large enough to satisfy the largest
// CSS viewport a panel may request AT ITS deviceScaleFactor, and in headless
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

// screencastAckTimeout bounds each Page.screencastFrameAck CDP round trip
// issued by runAckWorker. Acks are lightweight, so this is deliberately much
// shorter than BrowserManager.PageTimeout() — if a single ack call hangs
// (wedged/overloaded transport), the worker recovers in bounded time and
// moves on to the next (coalesced, latest) frame instead of getting stuck.
const screencastAckTimeout = 5 * time.Second

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

// LiveFrame is one CDP screencast frame plus the metadata a viewer needs to
// map its own rendered coordinates back to CSS pixels on the page. Field set
// mirrors generated.BrowserScreencastFrame minus session_id/seq/type, which
// the gateway attaches per-connection (seq is engine-assigned, monotonic per
// LiveView, and returned here so the gateway doesn't need its own counter).
type LiveFrame struct {
	Seq           int
	Data          string // base64 JPEG, no data-URI prefix (cdp gives us this directly)
	Width         int
	Height        int
	PageScale     float64
	OffsetTop     float64
	ScrollOffsetX float64
	ScrollOffsetY float64
}

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

// FrameSink receives screencast frames for one attached viewer. Implementations
// must not block: the LiveView invokes every registered sink synchronously,
// under its own lock released, from the CDP event-dispatch path. A slow
// consumer should hand off to its own buffered channel (the gateway's
// per-connection sendCh already does this).
type FrameSink func(LiveFrame)

// StatusSink receives a live-view lifecycle notification for one attached
// viewer (ADR-038 finding #2's split-brain fix). Today the only event it
// carries is "the screencast died unexpectedly" — the underlying chromedp
// tab context was canceled out from under an attached viewer WITHOUT going
// through Detach first. The prototypical cause: pkg/agent/loop.go's
// registerSharedTools now calls Shutdown() on an agent's PRIOR
// BrowserManager before installing a fresh one on hot-reload
// (ReloadProviderAndConfig) — Shutdown() cancels every session context,
// including one a viewer's WS connection is still attached to. Without this
// sink, that connection would keep streaming nothing forever and never learn
// why; the message is meant to be surfaced as a browser_status(error) frame
// so the client re-attaches (which resolves the CURRENT manager). Same
// non-blocking contract as FrameSink.
type StatusSink func(message string)

// TabsSink receives a tab-set snapshot for one attached viewer (ADR-041 D4).
// Invoked once immediately on Attach (with the CURRENT tab set, so a viewer
// renders the tab strip right away instead of waiting for the next change —
// a session with a single tab may never emit one during this viewer's whole
// attachment) and again on every subsequent tab-set change
// (open/close/switch/adopt/title-url-update). Same non-blocking contract as
// FrameSink/StatusSink/ControlSink: the LiveView invokes every registered
// sink synchronously with no lock held, so a slow consumer must hand off to
// its own buffered channel exactly like the gateway's per-connection sendCh
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
// non-blocking contract as FrameSink: the LiveView invokes every registered
// sink synchronously with no lock held (see takeControl/releaseControl), so
// a slow consumer must hand off to its own buffered channel exactly like the
// gateway's per-connection sendCh already does for frames/status.
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
// an entry does NOT start a screencast — that only happens on Attach.
func (r *LiveViewRegistry) view(sessionID string) *LiveView {
	r.mu.Lock()
	defer r.mu.Unlock()
	lv, ok := r.views[sessionID]
	if !ok {
		lv = &LiveView{
			mgr:          r.mgr,
			sessionID:    sessionID,
			viewers:      make(map[string]FrameSink),
			statusSinks:  make(map[string]StatusSink),
			controlSinks: make(map[string]ControlSink),
			tabsSinks:    make(map[string]TabsSink),
			ackCh:        make(chan int64, 1),
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

// Attach binds viewerID to sessionID's live view, starting the CDP
// screencast if this is the first viewer of that session (ref-counted, D3).
// onFrame is invoked for every screencast frame until Detach(sessionID,
// viewerID); onStatus (ADR-038 finding #2, may be nil) is invoked if the
// underlying tab context dies unexpectedly before that Detach happens;
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
	onFrame FrameSink,
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
	controlledByOther, err := r.view(sessionID).attach(tabCtx, viewerID, onFrame, onStatus, onControl, onTabs)
	if err != nil {
		return false, err
	}
	// A watched browsing context is never idle — see ReapIdleSessions.
	r.mgr.ViewerAttached(sessionID)

	// ADR-041 D4: give the newly-attached viewer the CURRENT tab strip
	// immediately, mirroring lastFrame's "don't make a piggybacking/fresh
	// viewer wait for the next change" rationale — a session with only one
	// tab may never emit another tabs-changed event during this viewer's
	// whole attachment.
	if onTabs != nil {
		if tabs, activeIdx, terr := r.mgr.ListTabs(sessionID); terr == nil && len(tabs) > 0 {
			onTabs(tabs, activeIdx)
		}
	}
	return controlledByOther, nil
}

// Detach unbinds viewerID from sessionID's live view. When this was the last
// viewer, the CDP screencast is stopped (D3). Also releases control if
// viewerID currently holds it, so a departing viewer never leaves the lock
// dangling for everyone else.
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

// PauseScreencast stops the underlying CDP screencast for sessionID WITHOUT
// touching any attached viewer's registration or the cached lastFrame
// (ADR-047 fix-wave finding 3: WebRTC media covering every attached viewer
// makes the JPEG screencast pure wasted pod CPU — the UAT symptom "inputs
// feel dead / choppy under heavy video" traced to pod CPU saturation). A
// no-op (returns false) if no live view exists for sessionID, the
// screencast isn't currently active, or it is already paused. The caller
// (pkg/tools/browser's CaptureSession.ReconcileScreencast) decides WHEN
// pausing is appropriate; this method only performs the mechanical stop.

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
		return false, fmt.Errorf("browser live: resize viewport: %w", err)
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

func (r *LiveViewRegistry) PauseScreencast(sessionID string) bool {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false
	}
	return lv.pauseScreencast()
}

// ResumeScreencast restarts the CDP screencast for sessionID if it was
// paused by PauseScreencast and at least one viewer is still attached to
// resume it for (ADR-047 fix-wave finding 3). A no-op (returns false) if no
// live view exists for sessionID, the screencast wasn't paused, or nobody
// is attached — the next Attach will start a fresh screencast normally in
// that case.
func (r *LiveViewRegistry) ResumeScreencast(sessionID string) bool {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false
	}
	return lv.resumeScreencast()
}

// AttachedViewerIDs returns a snapshot of the viewer IDs currently attached
// to sessionID's JPEG live view (ADR-047 fix-wave finding 3) — used by
// CaptureSession.ReconcileScreencast to check whether EVERY JPEG-attached
// viewer also has a WebRTC attachment before pausing the screencast. Empty
// (not an error) if no live view exists for sessionID.
func (r *LiveViewRegistry) AttachedViewerIDs(sessionID string) []string {
	sessionID = resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return nil
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	out := make([]string, 0, len(lv.viewers))
	for id := range lv.viewers {
		out = append(out, id)
	}
	return out
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
// attached screencast, though the SPA flow always attaches first).
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

// LiveView is a screencast + input-injection engine bound to one browser
// session (one Chromium tab). Reference-counted by attached viewers: the CDP
// screencast starts on the first Attach and stops on the last Detach. Safe
// for concurrent use — all state is guarded by mu.
type LiveView struct {
	mgr       *BrowserManager
	sessionID string

	mu         sync.Mutex
	tabCtx     context.Context
	listenCtx  context.Context // child of tabCtx; canceling it detaches the chromedp.ListenTarget subscription without touching the tab
	stopListen context.CancelFunc
	// lastKnownActiveCtx (ADR-047, wave-plan W2-A item 5) tracks the most
	// recently observed active-tab context INDEPENDENTLY of tabCtx —
	// tabCtx only reflects the JPEG screencast's own current binding and
	// stays nil until a JPEG epoch is ever installed (isActiveLocked/
	// hasEpochLocked gate on it), so a WebRTC-only session (no JPEG viewer
	// ever attached) would otherwise never have a reliable "did the active
	// tab actually change" signal for recapture. Set unconditionally at the
	// end of every onTabsChanged call; nil only before the first call.
	lastKnownActiveCtx context.Context
	viewers            map[string]FrameSink
	// statusSinks parallels viewers (ADR-038 finding #2): one optional
	// StatusSink per attached viewerID, notified only on an unexpected
	// screencast death (watchForUnexpectedDeath), never on a clean Detach.
	statusSinks map[string]StatusSink
	// controlSinks parallels viewers (ADR-039 UAT BE-1): one optional
	// ControlSink per attached viewerID, notified whenever some OTHER
	// viewer takes or releases control (takeControl/releaseControl below).
	controlSinks map[string]ControlSink
	// tabsSinks parallels viewers (ADR-041 D4): one optional TabsSink per
	// attached viewerID, notified once on attach with the current tab set
	// and again on every subsequent tab-set change (see onTabsChanged).
	tabsSinks map[string]TabsSink
	seq       int
	// lastFrame caches the most recently delivered screencast frame so a viewer
	// that attaches to an already-running screencast (a second panel, a pop-out)
	// sees the current state immediately instead of waiting for the next repaint
	// (which never comes on an idle page). Guarded by mu.
	lastFrame  *LiveFrame
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

	// pausedForWebRTC (ADR-047 fix-wave finding 3, pod-CPU-saturation UAT:
	// "inputs feel dead / choppy under heavy video") is true when the CDP
	// screencast was deliberately stopped by PauseScreencast because WebRTC
	// media already covers every currently-attached viewer, rather than
	// through the normal ref-counted detach() path. attach() treats this
	// the SAME as the piggyback (already-active) case -- register the new
	// viewer and replay lastFrame immediately, but do NOT restart the CDP
	// screencast -- so a late-attaching viewer's frame!=null gate is
	// satisfied without undoing the pause. It is the CALLER's job (the
	// browser package's CaptureSession.ReconcileScreencast, invoked
	// whenever either viewer set changes) to call ResumeScreencast if that
	// new viewer turns out to be JPEG-only.
	pausedForWebRTC bool

	// Rate limiting: input can only ever come from the single controller at a
	// time, so one shared counter per LiveView is sufficient — no per-viewer
	// bookkeeping needed.
	inputWindowStart time.Time
	inputCount       int // coalescible kinds (mouse_move, wheel)
	discreteCount    int // button/key transitions

	// ackCh is the mailbox runAckWorker consumes from: handleScreencastEvent
	// hands off each frame's session ID here (coalescing to the latest via
	// queueAck) instead of spawning a chromedp.Run goroutine per frame. Never
	// nil for a LiveView constructed via LiveViewRegistry.view (the only
	// production constructor); tests that build a LiveView by hand must set
	// it explicitly before calling queueAck/handleScreencastEvent.
	ackCh chan int64

	// runCDP executes a bounded chromedp CDP round trip. See
	// runCDPWithTimeout's doc comment for why this is a field instead of a
	// direct chromedp.Run call. Every call site in this file MUST call it
	// with no LiveView lock held.
	runCDP func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error
}

// isActiveLocked reports whether the screencast is currently subscribed
// (must be called with mu held). Checking listenCtx.Err() rather than a
// separate bool means a tab whose context died out-of-band (session
// recreated, crash) is detected and cleanly re-armed on the next attach,
// instead of leaving the registry believing a screencast is running when
// chromedp silently dropped the listener along with the canceled context.
func (lv *LiveView) isActiveLocked() bool {
	return lv.listenCtx != nil && lv.listenCtx.Err() == nil
}

// hasEpochLocked reports whether a screencast epoch is currently installed
// on this LiveView (lv.listenCtx != nil) — regardless of whether its
// underlying context has already died (lv.listenCtx.Err() != nil). Must be
// called with mu held.
//
// Deliberately WEAKER than isActiveLocked, which additionally requires
// Err() == nil — the right check for attach()'s piggyback decision and
// detach()'s teardown decision, where an already-dead epoch correctly means
// "not a live, running screencast to preserve/piggyback on". onTabsChanged
// and rebindScreencastOnce need this weaker check instead (live-UAT fix,
// 2026-07-12 — "closing the ACTIVE tab shows a false 'session ended'
// banner and leaves the screencast frozen on stale content"):
// BrowserManager.CloseTab cancels the closed tab's own chromedp context
// BEFORE it calls notifyTabsChanged — and since lv.listenCtx is a CHILD of
// the active tab's context, that cancellation SYNCHRONOUSLY kills
// lv.listenCtx too, before onTabsChanged/rebindScreencastOnce ever run. By
// the time either of them runs, the just-closed epoch therefore already
// looks "dead" by isActiveLocked's definition even though nothing has
// cleaned it up yet and the browsing context itself (plus every sibling
// tab) is perfectly alive — closing any one tab, including the active one,
// never tears down the browser (ADR-041's browserCtx fix). Gating the
// rebind decision on isActiveLocked() (== alive) therefore deterministically
// skipped the rebind in exactly this case. Gating on "an epoch is installed
// at all" instead correctly recognizes there is still a (now-defunct) epoch
// owed a replacement, whether or not its underlying context happened to
// have already died by the time this runs. See watchForUnexpectedDeath's
// doc comment for the matching false "session ended" broadcast half of this
// fix.
func (lv *LiveView) hasEpochLocked() bool {
	return lv.listenCtx != nil
}

// attach registers viewerID's sinks and starts the CDP screencast if no
// screencast is currently active for this session. onStatus may be nil.
//
// ADR-038 DEADLOCK POSTMORTEM: this method used to hold lv.mu (via
// defer lv.mu.Unlock()) across the blocking page.StartScreencast()
// chromedp.Run call below, which also ran on the bare tabCtx with no
// timeout of its own. Under a heavy page and/or the (separately fixed)
// unbounded ack-goroutine pile-up in handleScreencastEvent, that CDP round
// trip could hang indefinitely — and because it hung while lv.mu was held,
// lv.mu never unlocked. Every browser tool's controlledResult() check
// (browser_navigate/click/type/evaluate — tools.go) calls
// LiveViewRegistry.IsControlled → ... → lv.getController(), which takes
// lv.mu with a bare, non-context-aware sync.Mutex.Lock(). Once lv.mu was
// stuck, EVERY subsequent call to those tools blocked forever: no timeout
// (Lock() has none), no error, no log line, and "Stop" (which only cancels
// the agent turn's context) had no effect on a plain mutex wait. This is
// exactly the reported symptom: a single browser_screenshot timeout was
// followed by every later browser_navigate call hanging indefinitely.
//
// The fix has two parts, both required: (1) lv.mu is released before the
// CDP call, so a concurrent getController()/dispatchInput()/takeControl()
// is never blocked by it; (2) the CDP call itself now runs through
// lv.runCDP, which bounds it with mgr.PageTimeout() so even a wedged
// transport fails this one attach() attempt in bounded time instead of
// hanging the calling goroutine forever.
//
// Returns controlledByOther (ADR-039 UAT BE-1): true when sessionID is
// already controlled by a viewer other than viewerID at the moment of this
// attach — computed and returned under lv.mu before any CDP call, so it is
// available even on the fast piggyback path below.
func (lv *LiveView) attach(
	tabCtx context.Context,
	viewerID string,
	onFrame FrameSink,
	onStatus StatusSink,
	onControl ControlSink,
	onTabs TabsSink,
) (bool, error) {
	lv.mu.Lock()
	lv.viewers[viewerID] = onFrame
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

	if lv.isActiveLocked() || lv.pausedForWebRTC {
		// Screencast already running — this viewer piggybacks on it. Replay the
		// last delivered frame so it renders the current page immediately rather
		// than waiting for the next repaint (which may never come on an idle
		// page — the "Waiting for the first frame…" hang a pop-out / second
		// panel would otherwise show).
		//
		// ADR-047 fix-wave finding 3: pausedForWebRTC takes the SAME branch
		// even though isActiveLocked() is false while paused (PauseScreencast
		// nils listenCtx exactly like a real stop) — a late-attaching viewer
		// gets the cached frame immediately without undoing the pause; see
		// pausedForWebRTC's doc comment for who is responsible for resuming.
		cached := lv.lastFrame
		lv.mu.Unlock()
		if cached != nil {
			onFrame(*cached)
		}
		return controlledByOther, nil
	}

	lv.tabCtx = tabCtx
	listenCtx, cancel := context.WithCancel(tabCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.mu.Unlock()

	// Capture tabCtx by value for the ack worker/callback below — reading
	// lv.tabCtx from inside the callback would race the next
	// attach()/detach() cycle.
	ackCtx := tabCtx
	chromedp.ListenTarget(listenCtx, func(ev any) {
		lv.handleScreencastEvent(ackCtx, ev)
	})

	// Single dedicated ack worker for this screencast epoch (see
	// handleScreencastEvent/runAckWorker) instead of one chromedp.Run
	// goroutine per frame. Scoped to listenCtx so it exits when this epoch
	// ends, whether via a clean detach() or watchForUnexpectedDeath.
	go lv.runAckWorker(ackCtx, listenCtx)

	// No lock held here — see the deadlock postmortem above.
	//
	// Page.bringToFront BEFORE StartScreencast (W3 e2e finding): in FULL
	// Chrome --headless=new (the WebRTC-capable build ADR-047 D2 switched
	// managed launches to), Page.startScreencast succeeds but delivers ZERO
	// EventScreencastFrame for a target the compositor considers hidden —
	// and CDP-created targets start hidden there. chrome-headless-shell (the
	// pre-WebRTC managed build this path was spike-proven on, see the tuning
	// consts' doc) rendered every target regardless, which is why this was
	// never needed before. Measured on real Chrome 150: 0 frames in 4s on an
	// animating page without bringToFront; ~60fps with it.
	err := lv.runCDP(
		tabCtx, lv.mgr.PageTimeout(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return page.BringToFront().Do(ctx)
		}),
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(screencastQuality).
			WithMaxWidth(screencastMaxWidth).
			WithMaxHeight(screencastMaxHeight).
			WithEveryNthFrame(screencastEveryNthFrame),
	)

	lv.mu.Lock()
	if err != nil {
		delete(lv.viewers, viewerID)
		delete(lv.statusSinks, viewerID)
		delete(lv.controlSinks, viewerID)
		delete(lv.tabsSinks, viewerID)
		// Only tear down the shared listen/ack state if nothing else (a
		// concurrent attach() that piggybacked while this call was in
		// flight, or a concurrent detach()) has since superseded it.
		if lv.listenCtx == listenCtx {
			cancel()
			lv.listenCtx = nil
			lv.stopListen = nil
		}
		lv.mu.Unlock()
		return false, fmt.Errorf("browser live: failed to start screencast: %w", err)
	}
	lv.mu.Unlock()

	// ADR-038 finding #2: watch for this screencast's tab context dying
	// WITHOUT going through detach() first — e.g. BrowserManager.Shutdown()
	// canceling every session context out from under an attached viewer
	// during a hot-reload manager replacement (pkg/agent/loop.go's
	// registerSharedTools). One watcher per screencast "epoch" (i.e. per
	// listenCtx); it self-identifies as stale once a clean detach or a fresh
	// attach cycle has moved lv.listenCtx on.
	go lv.watchForUnexpectedDeath(listenCtx)

	return controlledByOther, nil
}

// onTabsChanged is invoked (ADR-041 D4, via LiveViewRegistry.handleTabsChanged
// ← BrowserManager.tabsChanged) whenever this session's tab set changes.
// Broadcasts a snapshot to every attached viewer's TabsSink and, if the
// active tab moved to a different underlying chromedp target, rebinds the
// live screencast to follow it. Never tears down the browsing context — only
// the screencast subscription moves.
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
	// activeTabChanged (ADR-047, wave-plan W2-A item 5): a JPEG-independent
	// "did the active tab actually change" signal for the WebRTC recapture
	// hook below — see lastKnownActiveCtx's doc comment for why this can't
	// reuse needsRebind/tabCtx. Guarded on lastKnownActiveCtx != nil so the
	// very first onTabsChanged call (which only establishes the baseline)
	// never counts as a "change".
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
		lv.rebindScreencast(newCtx)
	}
}

// rebindScreencast re-targets an ALREADY-ACTIVE screencast to newCtx
// (ADR-041 D4 — the trickiest piece of the tab-strip switch): stops the CDP
// screencast + event listener on the previous active tab's context and
// starts it fresh on newCtx, without touching the browsing context (a
// chromedp target's lifetime is independent of this) and without dropping
// any attached viewer's registration — only the underlying CDP subscription
// moves, so every viewer keeps watching the SAME logical live session, now
// following the new active tab. A no-op if the screencast isn't currently
// active (e.g. no viewers attached — the next Attach simply resolves the
// by-then-current active tab via mgr.Session, so there's nothing to rebind
// yet).
//
// Deadlock-safe per ADR-038: mirrors attach()'s discipline exactly — no
// LiveView lock held across any CDP call.
//
// F1 ordering fix (7-reviewer BLOCKER, ADR-041 fix wave): the previous
// version captured oldListenCtx under lock but left lv.listenCtx POINTING AT
// IT all the way through oldStopListen()'s cancellation and the blocking
// StopScreencast CDP call below — exactly the window watchForUnexpectedDeath
// treats as "my watched listenCtx died while still installed as
// lv.listenCtx", i.e. a genuine external death. On every tab switch the
// watcher (started by the earlier attach()/rebind for the OLD epoch) would
// race in — woken by oldStopListen()'s cancellation — see its watched ctx
// STILL installed, fire a FALSE "session ended unexpectedly" broadcast to
// every viewer, and nil out lv.listenCtx itself. This method would then
// re-lock below, see lv.listenCtx no longer matched the oldListenCtx it
// captured, and bail out without ever starting the new screencast — the
// live view sat dead until a manual re-attach. The fix mirrors detach()'s
// discipline exactly (see the len(lv.viewers)==0 branch there): nil
// lv.listenCtx/lv.stopListen under lv.mu BEFORE releasing the lock and
// calling oldStopListen(). This establishes a genuine happens-before
// relationship (the nil write happens-before the unlock, which
// happens-before oldStopListen()'s cancellation, which happens-before the
// watcher's <-watchedListenCtx.Done() wakes it, which happens-before its own
// lv.mu.Lock()) — so the watcher's "still installed" check is GUARANTEED to
// see the nil, not a race that merely makes the false broadcast unlikely.
// The re-lock guard below changes correspondingly: since the old epoch's
// identity was already cleared (not merely captured), "did anything else
// claim the slot while I was tearing down the old screencast" is now
// expressed as "is lv.listenCtx still nil" rather than a pointer comparison
// against the (now-erased) old value.
//
// Second fix wave (7-reviewer findings A/B, ADR-041, 2026-07-12):
//
// Finding A — orphaned screencast + leaked ack-worker when the LAST viewer
// detaches during the rebind's unlocked teardown window. Interleaving: the
// F1 fix above nils lv.listenCtx and unlocks BEFORE calling oldStopListen();
// if the last attached viewer's detach() lands in that exact window, its
// `len(lv.viewers)==0 && lv.isActiveLocked()` guard reads isActiveLocked()
// as false (listenCtx is transiently nil) and removes the viewer WITHOUT
// stopping the screencast itself — reasonably so, since this very rebind is
// already mid-teardown of that same epoch. The bug was in what happened
// next: rebindScreencastOnce would re-lock, see nobody had reclaimed the
// listenCtx slot, and go ahead and install a BRAND NEW screencast + ack
// worker for a session that now has ZERO registered viewers — an orphaned
// CDP screencast and a leaked goroutine that nothing will ever detach or
// stop. Fixed by re-checking len(lv.viewers)==0 under the SAME re-acquired
// lock the "did someone else reclaim the slot" check already uses,
// immediately before installing the new epoch: if nobody is watching
// anymore, leave the screencast torn down rather than starting one nobody
// will ever tear down.
//
// Finding B — a second, concurrent rebindScreencast call silently drops its
// target. Two goroutines can call onTabsChanged -> rebindScreencast for the
// same session concurrently (e.g. a human browser_switch_tab racing the
// agent's own async adopt-and-switch); whichever reaches
// rebindScreencastOnce's very first "if !lv.isActiveLocked() { return }"
// guard SECOND observes lv.listenCtx already nilled by the first caller's
// teardown and bails out immediately, discarding its own -- possibly more
// current -- target tab with no further effect. The manager's tab-set
// state (activeIdx, browser_tabs) is correct throughout; only the
// screencast binding lags. Rather than trying to serialize the two callers
// (which would mean holding lv.mu across a CDP call -- exactly the ADR-038
// deadlock this file exists to avoid), rebindScreencast now self-corrects:
// after EVERY successful rebindScreencastOnce install, it re-reads the
// manager's actual current active tab via lv.mgr.Session -- the SAME
// authoritative call onTabsChanged itself already trusts, independent of
// which racing goroutine's local newCtx parameter would otherwise have
// won. If the tab that's now actually active differs from what was just
// bound, it loops and rebinds again to the fresh target, so the live view
// always converges on the LAST real switch instead of getting stuck on
// whichever concurrent caller happened to finish its install first. This
// cannot busy-spin: it only loops when Session() reports an actual,
// different active tab, and each iteration performs a real bounded CDP
// round trip (via lv.runCDP) -- there is no lock held across any of it,
// and no lock is held across the Session() call itself either.
func (lv *LiveView) rebindScreencast(newCtx context.Context) {
	for {
		boundCtx, installed := lv.rebindScreencastOnce(newCtx)
		if !installed {
			return
		}
		currentCtx, err := lv.mgr.Session(lv.sessionID)
		if err != nil || currentCtx == boundCtx {
			return
		}
		// Finding B: the active tab moved again while we were rebinding —
		// chase it instead of leaving the screencast bound to a tab the tab
		// strip no longer shows as active. Reassigning newCtx to the fresh,
		// INDEPENDENT Session() context re-targets the next loop iteration; it
		// is a deliberate target-swap, not a nested/growing context chain, so
		// fatcontext's warning here is a false positive.
		newCtx = currentCtx //nolint:fatcontext // intentional per-iteration target swap, not a wrapped/growing context
	}
}

// rebindScreencastOnce performs a single stop-old/start-new rebind pass
// targeting newCtx, on behalf of rebindScreencast's self-correcting loop
// (see its doc comment for the Finding A/B context). Returns (newCtx, true)
// if it actually installed a new screencast epoch bound to newCtx; returns
// (nil, false) if it declined to — the screencast wasn't active to begin
// with, a concurrent attach()/rebind already reclaimed the slot, no viewers
// were left to serve (Finding A), or the new screencast failed to start
// (logged/notified at that site, same as before). Deadlock-safe per
// ADR-038: mirrors attach()'s discipline exactly — no LiveView lock held
// across any CDP call.
func (lv *LiveView) rebindScreencastOnce(newCtx context.Context) (context.Context, bool) {
	lv.mu.Lock()
	// hasEpochLocked, not isActiveLocked (live-UAT fix, 2026-07-12 — see its
	// doc comment): the OLD epoch installed here may already be dead (its
	// tab was just closed) by the time this runs, and that is exactly the
	// case this function must still handle — tearing down a dead epoch and
	// installing a fresh one on newCtx (the surviving tab). Only a truly
	// UNINSTALLED epoch (lv.listenCtx == nil — nobody has ever attached, or
	// a clean detach already cleared it) is a genuine no-op here.
	if !lv.hasEpochLocked() {
		lv.mu.Unlock()
		return nil, false
	}
	oldTabCtx := lv.tabCtx
	oldStopListen := lv.stopListen
	// Nil the shared listen state now, under the same lock — see the F1 doc
	// comment above for why this ordering is load-bearing.
	lv.listenCtx = nil
	lv.stopListen = nil
	lv.mu.Unlock()

	// Stop the old screencast + unsubscribe, bounded — mirrors detach()'s
	// teardown sequence (unsubscribe first, then ask CDP to stop) exactly.
	// Best-effort: oldTabCtx may already be canceled (the opener tab was
	// closed via browser_close_tab right after the switch, say), in which
	// case this simply fails harmlessly — there's nothing left to stop.
	oldStopListen()
	if err := lv.runCDP(oldTabCtx, lv.mgr.PageTimeout(), page.StopScreencast()); err != nil {
		logger.WarnCF("browser", "live view: rebind — stop old screencast failed (old tab may already be closed)",
			map[string]any{"error": err.Error(), "session_id": lv.sessionID})
	}

	lv.mu.Lock()
	if lv.listenCtx != nil {
		// A concurrent attach()/rebind already reclaimed the slot while we
		// were tearing down the old screencast (e.g. a fresh viewer
		// attached mid-rebind, or a second onTabsChanged fired a
		// concurrent rebind that won the race) — back off rather than
		// clobber whatever it installed.
		lv.mu.Unlock()
		return nil, false
	}
	if len(lv.viewers) == 0 {
		// Finding A: the last attached viewer detached while we were
		// tearing down the old screencast above — its detach() saw
		// isActiveLocked()==false (listenCtx was already nilled) and so
		// correctly left the screencast teardown to us, trusting this
		// rebind to leave things consistent. With nobody left to watch,
		// don't install a new screencast + ack worker that nothing will
		// ever detach or stop.
		lv.mu.Unlock()
		return nil, false
	}
	lv.tabCtx = newCtx
	listenCtx, cancel := context.WithCancel(newCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.mu.Unlock()

	ackCtx := newCtx
	chromedp.ListenTarget(listenCtx, func(ev any) {
		lv.handleScreencastEvent(ackCtx, ev)
	})
	go lv.runAckWorker(ackCtx, listenCtx)

	// No lock held here — see the deadlock postmortem above attach().
	// Page.bringToFront before StartScreencast: same full-Chrome
	// --headless=new requirement as attach() — a newly-activated tab is
	// hidden to the compositor until brought to front, and a hidden target
	// produces zero screencast frames (see attach()'s comment).
	err := lv.runCDP(
		newCtx, lv.mgr.PageTimeout(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return page.BringToFront().Do(ctx)
		}),
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(screencastQuality).
			WithMaxWidth(screencastMaxWidth).
			WithMaxHeight(screencastMaxHeight).
			WithEveryNthFrame(screencastEveryNthFrame),
	)
	if err != nil {
		logger.WarnCF("browser", "live view: rebind — start screencast on new active tab failed",
			map[string]any{"error": err.Error(), "session_id": lv.sessionID})
		lv.mu.Lock()
		var sinks []StatusSink
		if lv.listenCtx == listenCtx {
			cancel()
			lv.listenCtx = nil
			lv.stopListen = nil
			// F2 fix (7-reviewer HIGH, ADR-041 fix wave): without this, no
			// StatusSink ever fires for this failure —
			// watchForUnexpectedDeath is only armed AFTER a successful
			// StartScreencast (see the "go lv.watchForUnexpectedDeath(...)"
			// call below, never reached on this branch) — so every attached
			// viewer keeps rendering the stale OLD tab's cached lastFrame
			// under a tab strip that already says a DIFFERENT tab is
			// active, with no error banner explaining why. Reuse the same
			// fan-out mechanism watchForUnexpectedDeath uses.
			sinks = lv.snapshotStatusSinksLocked()
		}
		lv.mu.Unlock()
		for _, s := range sinks {
			s("couldn't switch the live view to the new tab — re-attach to resume watching")
		}
		return nil, false
	}

	go lv.watchForUnexpectedDeath(listenCtx)
	return newCtx, true
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
// ended unexpectedly' banner and leaves the screencast frozen on the closed
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
//     attempt its own rebind — avoids a second, independent teardown/install
//     racing onTabsChanged's; rebindScreencastOnce's own "did someone else
//     already reclaim the slot" checks (F1/Finding A/B) are what make
//     onTabsChanged's path safe to run unconditionally, exactly as they
//     already do for the pre-existing SwitchTab case.
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

// handleScreencastEvent is the chromedp.ListenTarget callback. Per chromedp's
// contract this runs synchronously on the CDP event-dispatch goroutine and
// must never block — the frame ack is handed off to the single ack worker
// via queueAck (never blocks, see its doc comment) rather than acked inline
// or from a per-frame goroutine.
//
// ADR-038 DEADLOCK POSTMORTEM: the previous implementation spawned one
// `go func() { chromedp.Run(tabCtx, page.ScreencastFrameAck(...)) }()` per
// frame. Under a heavy page (full frame rate, no throttling) those
// goroutines could pile up faster than their CDP round trips completed —
// every chromedp.Run for this browser process funnels through one
// fixed-capacity command queue drained by one goroutine
// (chromedp@v0.15.1 browser.go: cmdQueue is buffered 32, Browser.run is
// the sole writer/reader loop) — saturating it and stalling every other
// command on this browser (any session, any tool) behind the backlog. This
// is a plausible contributor to the reported "single browser_screenshot
// timeout wedges everything" symptom even independent of the lv.mu bug
// fixed in attach(): reducing frame/ack volume here removes the pressure
// that made the wedged CDP call in attach() likely in the first place.
func (lv *LiveView) handleScreencastEvent(tabCtx context.Context, ev any) {
	frame, ok := ev.(*page.EventScreencastFrame)
	if !ok {
		return
	}

	lv.queueAck(frame.SessionID)
	lv.deliver(frame)
}

// queueAck hands sessionID off to runAckWorker, coalescing to the newest
// frame instead of piling up: if the worker hasn't drained the previous
// pending ack yet, it is overwritten rather than queued behind. This keeps
// queueAck O(1) and non-blocking regardless of frame rate or how slow the
// worker currently is — see handleScreencastEvent's contract (must never
// block, it runs on chromedp's own CDP event-dispatch goroutine) and the
// ADR-038 deadlock postmortem above. Losing an ack for a stale frame is
// harmless: acking the newest frame is what unblocks Chrome's screencast
// pipeline to keep sending further frames.
func (lv *LiveView) queueAck(sessionID int64) {
	for {
		select {
		case lv.ackCh <- sessionID:
			return
		default:
			select {
			case <-lv.ackCh:
			default:
			}
		}
	}
}

// runAckWorker is the single goroutine that acks screencast frames for one
// screencast epoch (bounded by workerCtx, which is that epoch's listenCtx —
// it exits when the screencast stops, cleanly or unexpectedly). Reading from
// lv.ackCh here instead of handleScreencastEvent spawning a goroutine per
// frame means acks can never pile up: queueAck always coalesces to the
// latest frame, so this worker is always working the newest frame available,
// never a backlog, and a slow/stuck ack (bounded by screencastAckTimeout)
// only delays the NEXT ack, it never blocks the CDP event-dispatch path.
func (lv *LiveView) runAckWorker(tabCtx, workerCtx context.Context) {
	for {
		select {
		case <-workerCtx.Done():
			return
		case sessionID := <-lv.ackCh:
			if err := lv.runCDP(tabCtx, screencastAckTimeout, page.ScreencastFrameAck(sessionID)); err != nil {
				logger.WarnCF("browser", "live view: frame ack failed", map[string]any{
					"error":      err.Error(),
					"session_id": lv.sessionID,
				})
			}
		}
	}
}

// deliver fans a decoded screencast frame out to every currently-attached
// viewer sink.
func (lv *LiveView) deliver(frame *page.EventScreencastFrame) {
	lf := LiveFrame{Data: frame.Data}
	if frame.Metadata != nil {
		lf.Width = int(frame.Metadata.DeviceWidth)
		lf.Height = int(frame.Metadata.DeviceHeight)
		lf.PageScale = frame.Metadata.PageScaleFactor
		lf.OffsetTop = frame.Metadata.OffsetTop
		lf.ScrollOffsetX = frame.Metadata.ScrollOffsetX
		lf.ScrollOffsetY = frame.Metadata.ScrollOffsetY
	}
	// generated.BrowserScreencastFrame requires width/height >= 1; CDP
	// metadata is normally populated, but fall back to the configured max
	// dimensions rather than emit a schema-invalid 0 (Constraint #8).
	if lf.Width <= 0 {
		lf.Width = screencastMaxWidth
	}
	if lf.Height <= 0 {
		lf.Height = screencastMaxHeight
	}

	lv.mu.Lock()
	lv.seq++
	lf.Seq = lv.seq
	cached := lf
	lv.lastFrame = &cached // replayed to viewers that attach mid-screencast
	sinks := make([]FrameSink, 0, len(lv.viewers))
	for _, sink := range lv.viewers {
		sinks = append(sinks, sink)
	}
	lv.mu.Unlock()

	for _, sink := range sinks {
		sink(lf)
	}
}

// detach removes viewerID and, if it was the last viewer, stops the CDP
// screencast and unsubscribes the event listener.
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

	var (
		stopListen context.CancelFunc
		tabCtx     context.Context
		wasActive  bool
	)
	if len(lv.viewers) == 0 && lv.isActiveLocked() {
		wasActive = true
		stopListen = lv.stopListen
		tabCtx = lv.tabCtx
		lv.listenCtx = nil
		lv.stopListen = nil
	}
	lv.mu.Unlock()

	// No lock held here — see the deliver()/takeControl() convention this
	// mirrors. Fired unconditionally (independent of wasActive/the
	// screencast-teardown path below) so a departing controller's implicit
	// release is broadcast even when other viewers remain attached and the
	// screencast keeps running — the common case for the two-viewer scenario
	// this fixes. Dispatched via broadcastControl (B2) so a slow OTHER
	// viewer's sink can never stall this connection's own detach/disconnect
	// cleanup path — see broadcastControl's doc comment.
	broadcastControl(otherSinks, false)

	if !wasActive {
		return
	}
	// Unsubscribe first so no further events reach handleScreencastEvent
	// while we tear down, then ask CDP to actually stop generating frames.
	// No lock held here (already released above) — bounded via lv.runCDP so
	// a wedged transport can't hang the caller (e.g. the gateway's WS
	// cleanup path) forever.
	stopListen()
	if err := lv.runCDP(tabCtx, lv.mgr.PageTimeout(), page.StopScreencast()); err != nil {
		logger.WarnCF("browser", "live view: stop screencast failed", map[string]any{
			"error":      err.Error(),
			"session_id": lv.sessionID,
		})
	}
}

// pauseScreencast is PauseScreencast's LiveView-level implementation
// (ADR-047 fix-wave finding 3). Mirrors detach()'s teardown sequence exactly
// (nil the shared listen state under lock, then unsubscribe, then ask CDP to
// stop — bounded via lv.runCDP, no LiveView lock held across the CDP call,
// per the ADR-038 deadlock discipline every CDP call site in this file
// observes) but deliberately leaves lv.viewers/statusSinks/controlSinks/
// tabsSinks/lastFrame untouched and sets pausedForWebRTC instead of clearing
// viewer registrations — a paused screencast still has viewers, it just
// isn't capturing frames for them right now.
func (lv *LiveView) pauseScreencast() bool {
	lv.mu.Lock()
	if lv.pausedForWebRTC || !lv.isActiveLocked() {
		lv.mu.Unlock()
		return false
	}
	stopListen := lv.stopListen
	tabCtx := lv.tabCtx
	lv.listenCtx = nil
	lv.stopListen = nil
	lv.pausedForWebRTC = true
	lv.mu.Unlock()

	stopListen()
	if err := lv.runCDP(tabCtx, lv.mgr.PageTimeout(), page.StopScreencast()); err != nil {
		logger.WarnCF(
			"browser",
			"live view: pause screencast (WebRTC covering every viewer) — stop failed",
			map[string]any{
				"error":      err.Error(),
				"session_id": lv.sessionID,
			},
		)
	}
	return true
}

// resumeScreencast is ResumeScreencast's LiveView-level implementation
// (ADR-047 fix-wave finding 3). Mirrors attach()'s screencast-start sequence
// exactly (no LiveView lock held across any CDP call, BringToFront before
// StartScreencast — see attach()'s doc comment for the full-Chrome
// hidden-target rationale). A no-op if this LiveView was never paused, or if
// the last viewer detached before this could run (in which case
// pausedForWebRTC is simply cleared so a future Attach starts a normal fresh
// screencast rather than silently staying "paused" forever with nobody
// watching).
func (lv *LiveView) resumeScreencast() bool {
	lv.mu.Lock()
	if !lv.pausedForWebRTC {
		lv.mu.Unlock()
		return false
	}
	if len(lv.viewers) == 0 {
		lv.pausedForWebRTC = false
		lv.mu.Unlock()
		return false
	}
	lv.pausedForWebRTC = false
	lv.mu.Unlock()

	tabCtx, err := lv.mgr.Session(lv.sessionID)
	if err != nil {
		// Nothing to resume onto — e.g. the browsing context is mid-recreation
		// after a crash. watchForUnexpectedDeath (armed the last time the
		// screencast was genuinely active) already handles notifying attached
		// viewers if the tab context died out from under them; there is
		// nothing further to do here since pausedForWebRTC is already cleared
		// above (not re-armed — a future Attach or another ReconcileScreencast
		// call will retry the resume normally).
		logger.WarnCF("browser", "live view: resume screencast — cannot resolve session", map[string]any{
			"error":      err.Error(),
			"session_id": lv.sessionID,
		})
		return false
	}

	lv.mu.Lock()
	if lv.isActiveLocked() || len(lv.viewers) == 0 {
		// Superseded by a concurrent Attach/rebind that already installed a
		// fresh epoch, or the last viewer left while Session() above was
		// resolving — nothing left for this call to do.
		lv.mu.Unlock()
		return false
	}
	lv.tabCtx = tabCtx
	listenCtx, cancel := context.WithCancel(tabCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.mu.Unlock()

	ackCtx := tabCtx
	chromedp.ListenTarget(listenCtx, func(ev any) {
		lv.handleScreencastEvent(ackCtx, ev)
	})
	go lv.runAckWorker(ackCtx, listenCtx)

	err = lv.runCDP(
		tabCtx, lv.mgr.PageTimeout(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return page.BringToFront().Do(ctx)
		}),
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(screencastQuality).
			WithMaxWidth(screencastMaxWidth).
			WithMaxHeight(screencastMaxHeight).
			WithEveryNthFrame(screencastEveryNthFrame),
	)
	if err != nil {
		logger.WarnCF("browser", "live view: resume screencast — start failed", map[string]any{
			"error":      err.Error(),
			"session_id": lv.sessionID,
		})
		lv.mu.Lock()
		if lv.listenCtx == listenCtx {
			cancel()
			lv.listenCtx = nil
			lv.stopListen = nil
		}
		lv.mu.Unlock()
		return false
	}

	go lv.watchForUnexpectedDeath(listenCtx)
	return true
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
// stale state. The sinks are dispatched via broadcastControl (no lock held),
// mirroring deliver().
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
// the same "never let a slow consumer stall the actor" contract FrameSink/
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

// snapshotStatusSinksLocked returns every registered StatusSink (F2 fix,
// ADR-041 fix wave: reused by rebindScreencast's start-new-screencast
// failure branch, alongside watchForUnexpectedDeath's existing use). Must be
// called with mu held; the returned sinks must be invoked with no lock held
// (mirrors deliver()'s convention).
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
// sinks must be invoked with no lock held (mirrors deliver()'s convention).
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
