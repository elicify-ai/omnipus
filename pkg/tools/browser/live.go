package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

// maxInputEventsPerSecond caps injected input events per LiveView session
// (ADR-038 D6: "browser_input is rate-limited"). Generous enough for a real
// mouse-move stream while bounding a runaway or malicious client.
const maxInputEventsPerSecond = 50

// screencastAckTimeout bounds each Page.screencastFrameAck CDP round trip
// issued by runAckWorker. Acks are lightweight, so this is deliberately much
// shorter than BrowserManager.PageTimeout() — if a single ack call hangs
// (wedged/overloaded transport), the worker recovers in bounded time and
// moves on to the next (coalesced, latest) frame instead of getting stuck.
const screencastAckTimeout = 5 * time.Second

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
	return r.attachSession(sessionID, viewerID, true, onFrame, onStatus, onControl, onTabs)
}

// AttachWithoutScreencast binds viewerID to sessionID's live view exactly
// like Attach — viewer/status/control/tabs registration, take-the-wheel
// input eligibility (lv.tabCtx is still resolved and set), controlledByOther
// computation, and the immediate tabs snapshot — but deliberately does NOT
// issue CDP Page.startScreencast, even when this is the first attach for the
// session (Fix 2, double-screencast finding).
//
// Callers on the video-relay path (component M) drive their own, separate
// CDP Page.startScreencast subscription via CaptureDriver (component L,
// capture.go) against the SAME underlying target that this registry's
// screencast would otherwise also subscribe to. If BOTH were started for the
// same viewer, both ListenTarget handlers would receive and ack every
// screencast frame — double-acking (Chrome paces frame delivery on the ack)
// while the two Page.startScreencast calls contend over quality/size — see
// browser_ws.go's handleAttach for the full finding.
//
// onFrame is still required and still registered in lv.viewers (never
// nil-called): if a LATER, non-video-capable viewer attaches to the SAME
// session via the ordinary Attach and thereby starts the legacy screencast
// for real (D3's ref-counted "first viewer starts it" — len(lv.viewers)==0
// is what gates teardown in detach(), not who happened to start it), this
// viewer's sink must still receive those frames (and safely discard them,
// per its own videoCapable branch) rather than leaving deliver()'s fan-out to
// call a nil sink.
func (r *LiveViewRegistry) AttachWithoutScreencast(
	sessionID, viewerID string,
	onFrame FrameSink,
	onStatus StatusSink,
	onControl ControlSink,
	onTabs TabsSink,
) (bool, error) {
	return r.attachSession(sessionID, viewerID, false, onFrame, onStatus, onControl, onTabs)
}

// attachSession is the shared implementation behind Attach and
// AttachWithoutScreencast; wantScreencast selects which of the two.
func (r *LiveViewRegistry) attachSession(
	sessionID, viewerID string,
	wantScreencast bool,
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
	controlledByOther, err := r.view(sessionID).attach(tabCtx, viewerID, onFrame, onStatus, onControl, onTabs, wantScreencast)
	if err != nil {
		return false, err
	}

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
	viewers    map[string]FrameSink
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

	// Rate limiting: input can only ever come from the single controller at a
	// time, so one shared counter per LiveView is sufficient — no per-viewer
	// bookkeeping needed.
	inputWindowStart time.Time
	inputCount       int

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
//
// wantScreencast is a trailing variadic bool (rather than a plain required
// parameter) purely so every existing 6-arg call site — including the
// in-package deadlock/live-view tests, which construct a *LiveView directly
// and call attach() without going through the registry — keeps compiling
// unchanged; omitted (the overwhelming majority of call sites) it defaults
// to true, i.e. today's behavior. LiveViewRegistry.attachSession is the only
// caller that ever passes an explicit value, forwarding the choice between
// its two exported entry points, Attach (true) and AttachWithoutScreencast
// (false, Fix 2 double-screencast finding — see its doc comment).
func (lv *LiveView) attach(
	tabCtx context.Context,
	viewerID string,
	onFrame FrameSink,
	onStatus StatusSink,
	onControl ControlSink,
	onTabs TabsSink,
	wantScreencast ...bool,
) (bool, error) {
	startScreencast := true
	if len(wantScreencast) > 0 {
		startScreencast = wantScreencast[0]
	}

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

	if lv.isActiveLocked() {
		// Screencast already running — this viewer piggybacks on it. Replay the
		// last delivered frame so it renders the current page immediately rather
		// than waiting for the next repaint (which may never come on an idle
		// page — the "Waiting for the first frame…" hang a pop-out / second
		// panel would otherwise show).
		cached := lv.lastFrame
		lv.mu.Unlock()
		if cached != nil {
			onFrame(*cached)
		}
		return controlledByOther, nil
	}

	// lv.tabCtx is kept current regardless of startScreencast: dispatchInput
	// (take-the-wheel input) requires it non-nil, and onTabsChanged's rebind
	// decision compares against it via hasEpochLocked — both must work for a
	// viewer that owns no CDP screencast subscription of its own (Fix 2).
	lv.tabCtx = tabCtx

	if !startScreencast {
		lv.mu.Unlock()
		return controlledByOther, nil
	}

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
	err := lv.runCDP(tabCtx, lv.mgr.PageTimeout(),
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
	lv.mu.Unlock()
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
	err := lv.runCDP(newCtx, lv.mgr.PageTimeout(),
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
	if lv.controller == "" || lv.controller != viewerID {
		lv.mu.Unlock()
		return benignInputError("browser live: viewer does not hold control of this session")
	}
	if !lv.allowInputLocked() {
		lv.mu.Unlock()
		return benignInputError("browser live: input rate limit exceeded (%d/s)", maxInputEventsPerSecond)
	}
	tabCtx := lv.tabCtx
	lv.mu.Unlock()

	if tabCtx == nil {
		return realInputError("browser live: session is not attached")
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

// allowInputLocked applies a simple fixed-window rate limiter. Must be called
// with mu held.
func (lv *LiveView) allowInputLocked() bool {
	now := time.Now()
	if now.Sub(lv.inputWindowStart) >= time.Second {
		lv.inputWindowStart = now
		lv.inputCount = 0
	}
	if lv.inputCount >= maxInputEventsPerSecond {
		return false
	}
	lv.inputCount++
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
		return input.DispatchKeyEvent(input.KeyRawDown).
			WithKey(in.Key).
			WithCode(in.Code).
			WithText(in.Text).
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
