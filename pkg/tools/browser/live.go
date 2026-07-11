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
	Text   string
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
func newLiveViewRegistry(mgr *BrowserManager) *LiveViewRegistry {
	return &LiveViewRegistry{mgr: mgr, views: make(map[string]*LiveView)}
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
// viewer takes or releases control after this call returns.
// Resolves the manager's session tab itself, so callers only need a session
// ID, not a chromedp context.
//
// Returns controlledByOther: true when, AT THE MOMENT OF THIS ATTACH,
// sessionID is already controlled by a viewer other than viewerID — so a
// newly-attaching connection (a second panel, a pop-out) can render the
// correct "someone else is driving" state on its very first status frame
// instead of only learning about it on the NEXT take/release broadcast.
func (r *LiveViewRegistry) Attach(sessionID, viewerID string, onFrame FrameSink, onStatus StatusSink, onControl ControlSink) (bool, error) {
	sessionID = resolveSessionID(sessionID)
	if viewerID == "" {
		return false, fmt.Errorf("browser live: viewer id is required")
	}
	tabCtx, err := r.mgr.Session(sessionID)
	if err != nil {
		return false, fmt.Errorf("browser live: cannot resolve session %q: %w", sessionID, err)
	}
	return r.view(sessionID).attach(tabCtx, viewerID, onFrame, onStatus, onControl)
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
	seq          int
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
func (lv *LiveView) attach(tabCtx context.Context, viewerID string, onFrame FrameSink, onStatus StatusSink, onControl ControlSink) (bool, error) {
	lv.mu.Lock()
	lv.viewers[viewerID] = onFrame
	if onStatus != nil {
		lv.statusSinks[viewerID] = onStatus
	}
	if onControl != nil {
		lv.controlSinks[viewerID] = onControl
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

// watchForUnexpectedDeath blocks until watchedListenCtx is Done, then — ONLY
// if lv.listenCtx is still that exact context (i.e. nothing else already
// handled the teardown) — clears the active-screencast state and notifies
// every attached viewer's StatusSink. detach() always nils lv.listenCtx out
// BEFORE canceling it, so a context that dies while still installed as
// lv.listenCtx can only mean an external cancellation (the tab/allocator
// context above it died), never a normal detach.
func (lv *LiveView) watchForUnexpectedDeath(watchedListenCtx context.Context) {
	<-watchedListenCtx.Done()

	lv.mu.Lock()
	if lv.listenCtx != watchedListenCtx {
		// A clean detach (or a subsequent attach cycle) already superseded
		// this watcher — nothing unexpected happened.
		lv.mu.Unlock()
		return
	}
	lv.listenCtx = nil
	lv.stopListen = nil
	sinks := make([]StatusSink, 0, len(lv.statusSinks))
	for _, s := range lv.statusSinks {
		sinks = append(sinks, s)
	}
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
			WithModifiers(mods), nil
	case "key_up":
		if in.Key == "" && in.Code == "" {
			return nil, fmt.Errorf("browser live: key_up input requires a key or code")
		}
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(in.Key).
			WithCode(in.Code).
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
