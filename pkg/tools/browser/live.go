package browser

import (
	"context"
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
// mouse_down, mouse_up, wheel, key_down, key_up, text.
type LiveInput struct {
	Kind      string
	X, Y      float64
	Button    string // none|left|middle|right|back|forward ("" treated as none)
	DeltaX    float64
	DeltaY    float64
	Key       string
	Code      string
	Text      string
	Modifiers int // bit field: Alt=1, Ctrl=2, Meta=4, Shift=8
}

// FrameSink receives screencast frames for one attached viewer. Implementations
// must not block: the LiveView invokes every registered sink synchronously,
// under its own lock released, from the CDP event-dispatch path. A slow
// consumer should hand off to its own buffered channel (the gateway's
// per-connection sendCh already does this).
type FrameSink func(LiveFrame)

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
		lv = &LiveView{mgr: r.mgr, sessionID: sessionID, viewers: make(map[string]FrameSink)}
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
// viewerID). Resolves the manager's session tab itself, so callers only need
// a session ID, not a chromedp context.
func (r *LiveViewRegistry) Attach(sessionID, viewerID string, onFrame FrameSink) error {
	sessionID = resolveSessionID(sessionID)
	if viewerID == "" {
		return fmt.Errorf("browser live: viewer id is required")
	}
	tabCtx, err := r.mgr.Session(sessionID)
	if err != nil {
		return fmt.Errorf("browser live: cannot resolve session %q: %w", sessionID, err)
	}
	return r.view(sessionID).attach(tabCtx, viewerID, onFrame)
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
		return fmt.Errorf("browser live: no active live view for session %q", sessionID)
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
	seq        int
	controller string // viewerID holding control; "" = uncontrolled

	// Rate limiting: input can only ever come from the single controller at a
	// time, so one shared counter per LiveView is sufficient — no per-viewer
	// bookkeeping needed.
	inputWindowStart time.Time
	inputCount       int
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

// attach registers viewerID's sink and starts the CDP screencast if no
// screencast is currently active for this session.
func (lv *LiveView) attach(tabCtx context.Context, viewerID string, onFrame FrameSink) error {
	lv.mu.Lock()
	defer lv.mu.Unlock()

	lv.viewers[viewerID] = onFrame

	if lv.isActiveLocked() {
		return nil // screencast already running — this viewer just piggybacks on it
	}

	lv.tabCtx = tabCtx
	listenCtx, cancel := context.WithCancel(tabCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel

	// Capture tabCtx by value for the ack goroutine below — reading lv.tabCtx
	// from inside the callback would race the next attach()/detach() cycle.
	ackCtx := tabCtx
	chromedp.ListenTarget(listenCtx, func(ev any) {
		lv.handleScreencastEvent(ackCtx, ev)
	})

	err := chromedp.Run(tabCtx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(screencastQuality).
			WithMaxWidth(screencastMaxWidth).
			WithMaxHeight(screencastMaxHeight).
			WithEveryNthFrame(screencastEveryNthFrame),
	)
	if err != nil {
		delete(lv.viewers, viewerID)
		cancel()
		lv.listenCtx = nil
		lv.stopListen = nil
		return fmt.Errorf("browser live: failed to start screencast: %w", err)
	}

	return nil
}

// handleScreencastEvent is the chromedp.ListenTarget callback. Per chromedp's
// contract this runs synchronously on the CDP event-dispatch goroutine and
// must never block — the frame ack is dispatched from its own goroutine
// (spike-proven, ADR-038 D3: acking inline deadlocks the CDP transport,
// which StartScreencast/Ack share with every other command on this context).
func (lv *LiveView) handleScreencastEvent(tabCtx context.Context, ev any) {
	frame, ok := ev.(*page.EventScreencastFrame)
	if !ok {
		return
	}

	sessionID := frame.SessionID
	go func() {
		if err := chromedp.Run(tabCtx, page.ScreencastFrameAck(sessionID)); err != nil {
			logger.WarnCF("browser", "live view: frame ack failed", map[string]any{
				"error":      err.Error(),
				"session_id": lv.sessionID,
			})
		}
	}()

	lv.deliver(frame)
}

// deliver fans a decoded screencast frame out to every currently-attached
// viewer sink.
func (lv *LiveView) deliver(frame *page.EventScreencastFrame) {
	lv.mu.Lock()
	lv.seq++
	seq := lv.seq
	sinks := make([]FrameSink, 0, len(lv.viewers))
	for _, sink := range lv.viewers {
		sinks = append(sinks, sink)
	}
	lv.mu.Unlock()

	if len(sinks) == 0 {
		return
	}

	lf := LiveFrame{Seq: seq, Data: frame.Data}
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

	for _, sink := range sinks {
		sink(lf)
	}
}

// detach removes viewerID and, if it was the last viewer, stops the CDP
// screencast and unsubscribes the event listener.
func (lv *LiveView) detach(viewerID string) {
	lv.mu.Lock()
	delete(lv.viewers, viewerID)
	if lv.controller == viewerID {
		lv.controller = ""
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

	if !wasActive {
		return
	}
	// Unsubscribe first so no further events reach handleScreencastEvent
	// while we tear down, then ask CDP to actually stop generating frames.
	stopListen()
	if err := chromedp.Run(tabCtx, page.StopScreencast()); err != nil {
		logger.WarnCF("browser", "live view: stop screencast failed", map[string]any{
			"error":      err.Error(),
			"session_id": lv.sessionID,
		})
	}
}

// dispatchInput validates control + rate limit, then dispatches one CDP
// input action. Called with no locks held by the caller.
func (lv *LiveView) dispatchInput(viewerID string, in LiveInput) error {
	lv.mu.Lock()
	if lv.controller == "" || lv.controller != viewerID {
		lv.mu.Unlock()
		return fmt.Errorf("browser live: viewer does not hold control of this session")
	}
	if !lv.allowInputLocked() {
		lv.mu.Unlock()
		return fmt.Errorf("browser live: input rate limit exceeded (%d/s)", maxInputEventsPerSecond)
	}
	tabCtx := lv.tabCtx
	lv.mu.Unlock()

	if tabCtx == nil {
		return fmt.Errorf("browser live: session is not attached")
	}

	action, err := buildInputAction(in)
	if err != nil {
		return err
	}
	if err := chromedp.Run(tabCtx, action); err != nil {
		return fmt.Errorf("browser live: input dispatch failed: %w", err)
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
// controller; returns false if someone else holds it.
func (lv *LiveView) takeControl(viewerID string) bool {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.controller != "" && lv.controller != viewerID {
		return false
	}
	lv.controller = viewerID
	return true
}

// releaseControl clears the control lock only if viewerID currently holds it.
func (lv *LiveView) releaseControl(viewerID string) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.controller == viewerID {
		lv.controller = ""
	}
}

// getController returns the current controller viewerID, or "".
func (lv *LiveView) getController() string {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	return lv.controller
}

// buildInputAction maps a wire-level LiveInput to the corresponding CDP
// Input.dispatch* action (spike-proven mechanics, ADR-038 context section).
func buildInputAction(in LiveInput) (chromedp.Action, error) {
	mods := input.Modifier(in.Modifiers)

	switch in.Kind {
	case "mouse_move":
		return input.DispatchMouseEvent(input.MouseMoved, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithModifiers(mods), nil
	case "mouse_down":
		return input.DispatchMouseEvent(input.MousePressed, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "mouse_up":
		return input.DispatchMouseEvent(input.MouseReleased, in.X, in.Y).
			WithButton(mouseButton(in.Button)).
			WithClickCount(1).
			WithModifiers(mods), nil
	case "wheel":
		return input.DispatchMouseEvent(input.MouseWheel, in.X, in.Y).
			WithDeltaX(in.DeltaX).
			WithDeltaY(in.DeltaY).
			WithModifiers(mods), nil
	case "key_down":
		return input.DispatchKeyEvent(input.KeyRawDown).
			WithKey(in.Key).
			WithCode(in.Code).
			WithText(in.Text).
			WithModifiers(mods), nil
	case "key_up":
		return input.DispatchKeyEvent(input.KeyUp).
			WithKey(in.Key).
			WithCode(in.Code).
			WithModifiers(mods), nil
	case "text":
		if in.Text == "" {
			return nil, fmt.Errorf("browser live: text input requires a non-empty text field")
		}
		return input.InsertText(in.Text), nil
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
