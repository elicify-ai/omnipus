package browser

import (
	"context"
	"sync"
	"time"

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

// viewportFetchFailureEscalation is how many CONSECUTIVE viewport-read failures
// turn a silently-dropped pointer event into a user-visible error. Two, not
// one: a single miss is routinely transient (a cache invalidated by a legitimate
// resize, a lost race with a reflow) and surfacing it would be noise. A second
// consecutive failure — a full viewportInputFetchBackoff later — is no longer
// plausibly transient, and silence there is precisely the "dead browser looks
// idle" failure ADR-038 finding #4 exists to prevent.
const viewportFetchFailureEscalation = 2

// LiveInput is the engine-level input event the gateway decodes from a
// generated.BrowserInputFrame before calling LiveViewRegistry.Input. Kind
// mirrors the AsyncAPI BrowserInputFrame `kind` enum exactly: mouse_move,
// mouse_down, mouse_up, wheel, key_down, key_up, text, navigate.
type LiveInput struct {
	// SourceContext is the original connection or input-channel lifetime.
	// It is assigned by the gateway, never decoded from a client payload.
	SourceContext context.Context
	// CaptureID and CaptureGeneration are the viewer's claim about the
	// displayed picture.
	CaptureID         string
	CaptureGeneration uint64
	Kind              string
	X, Y              float64
	HasXY             bool   // ADR-038 finding #5: whether X/Y were actually present on the wire — see buildInputAction.
	Button            string // none|left|middle|right|back|forward ("" treated as none)
	DeltaX            float64
	DeltaY            float64
	Key               string
	Code              string
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

	// Timing is an optional in-process diagnostic observer. It receives
	// fixed stage names only, never input contents, and must not block or reenter.
	Timing *LiveInputTimingObserver
}

// LiveInputTimingObserver is local diagnostic state, never serialized. A pointer
// keeps LiveInput comparable and separates the observer from retained key state.
type LiveInputTimingObserver struct { // not-wire-format: callback for local measurements only.
	Observe       func(stage string)
	ObserveBudget func(stage string, remaining time.Duration)
}

// StatusSink receives a live-view lifecycle notification for one attached
// viewer (ADR-038 finding #2's split-brain fix). It carries two events today:
//
//  1. "the session died unexpectedly" (watchForUnexpectedDeath);
//  2. "the window resized but the picture may look soft" — the
//     deviceScaleFactor override timed out (notifyScaleDegraded, round-2
//     finding F5). That one is a DEGRADATION, not a death: nothing needs
//     re-attaching, the viewer is simply being told what happened and how it
//     recovers, per ADR-061's rule that a failure must name its cause to the
//     user rather than only in a WARN log nobody is reading.
//
// The first event means the underlying chromedp tab context was canceled out
// from under an attached viewer WITHOUT going through Detach first. The
// prototypical cause: pkg/agent/loop.go's
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

// ReleaseNotifySink is invoked, addressed to exactly ONE viewer — the former
// holder — when the registry performs a release that viewer did NOT itself
// request (ADR-085 FR-031b): an FR-029 prompt release, an FR-031a idle
// expiry, an FR-047 handover clear, an FR-052 disabled-release sweep, or a
// session teardown. Unlike ControlSink (broadcast to every OTHER viewer,
// excluding the actor), this is addressed to the actor itself, because here
// the SERVER is the actor and the former holder never gets an "own request"
// browser_status reply the way a self-initiated release does
// (pkg/gateway/browser_ws.go::handleControl's case "release"). Registered
// separately from Attach's callback quartet via SetReleaseNotifySink, so
// every existing Attach call site is untouched. Same non-blocking contract
// as StatusSink/ControlSink: invoked with no LiveView lock held; a slow
// consumer must hand off to its own buffered channel.
type ReleaseNotifySink func()

// LiveViewRegistry manages one LiveView per tab set for a single
// BrowserManager. Safe for concurrent use.
//
// ⚠️ A BrowserManager is NOT "scoped to one agent" any more, and this comment
// used to say it was (ADR-038 D4's per-agent manager map). ADR-075 FR-001
// re-keyed that map to the BROWSING KEY: one manager per WORKSPACE, shared by
// every agent on it. What gives a LiveView its identity is therefore the map
// key itself — sessionKey(BrowsingKey, TabOwner) — not the manager it hangs
// off. Reading this registry as "one agent's views" merges every session on the
// workspace, which is the exact defect FR-080 exists to prevent.
type LiveViewRegistry struct {
	mgr *BrowserManager

	mu    sync.Mutex
	views map[string]*LiveView

	// --- ADR-085 FR-031a: the idle-release sweeper ---
	//
	// hooksMu guards hooks. It exists because the hooks are no longer
	// necessarily installed before the sweeper goroutine starts: the gateway
	// registers its observer through SetGlobalControlReleaseHooks at wiring
	// time, and a per-registry override may be installed by a test at any
	// point, while sweepTick reads the field every 30s on its own goroutine.
	// Reading and writing an interface-bearing struct field across two
	// goroutines with no lock is a data race `go test -race` reports.
	hooksMu   sync.RWMutex
	hooks     ControlIdleReleaseHooks
	sweepStop chan struct{}
	sweepDone chan struct{}
}

// ControlIdleReleaseHooks lets the gateway observe every release the
// FR-031a/FR-052 sweeper performs, without pkg/tools/browser importing
// pkg/gateway or pkg/audit's higher-level record shapes. Registered once via
// LiveViewRegistry.SetControlIdleReleaseHooks at wiring time
// (pkg/gateway/browser_ws.go). Both fields are nil-checked individually — a
// caller that only cares about one outcome may leave the other unset.
type ControlIdleReleaseHooks struct {
	// OnIdleRelease fires after the sweeper releases a stood-down tab set
	// whose liveness window elapsed (audited browser_control_idle_release).
	// sessionID is the resolved tab-set key; formerHolder is the released
	// viewerID ("" for a handover-only/latch-only release with no
	// interactive holder).
	OnIdleRelease func(sessionID, formerHolder string)
	// OnDisabledRelease fires instead of OnIdleRelease when the release was
	// forced by tools.browser.take_control_enabled being false (FR-052),
	// regardless of the idle window.
	OnDisabledRelease func(sessionID, formerHolder string)
}

// --- ADR-085 FR-041/FR-044: the stand-down notice seam -----------------

// StandDownNoticeProducer names WHICH of the two stand-down producers raised
// a notice. The wire frame does not distinguish them (see
// contracts/components/schemas/BrowserHandoverNoticeFrame.yaml) — this exists
// so the gateway can choose the sentence and the audit reason, not so the SPA
// can branch.
type StandDownNoticeProducer string

const (
	// StandDownByTake is a HUMAN take: browser_control{take} through the
	// panel, or the click-to-drive EnsureControlForInput path.
	StandDownByTake StandDownNoticeProducer = "take"
	// StandDownByHandover is the AGENT's own browser_handover call
	// (BROWSER-FR-046/FR-048).
	StandDownByHandover StandDownNoticeProducer = "handover"
)

// newLiveViewRegistry constructs a registry bound to mgr. Unexported —
// callers get one via BrowserManager.Live().
//
// ADR-041 D4: wires mgr's tabs-changed callback to this registry so a
// BrowserManager tab-set change (open/close/switch/adopt) fans out to the
// LiveView for that session, if one exists — see handleTabsChanged.
func newLiveViewRegistry(mgr *BrowserManager) *LiveViewRegistry {
	r := &LiveViewRegistry{mgr: mgr, views: make(map[string]*LiveView)}
	mgr.SetTabsChangedFunc(r.handleTabsChanged)
	r.startControlIdleSweeper() // ADR-085 FR-031a/FR-052
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
	sessionID = r.resolveSessionID(sessionID)
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

// resolveSessionID resolves an omitted session id to the WORKSPACE-OWNED tab
// set — the operator's own tabs (ADR-075 §0.2a).
//
// Empty is a real, reachable input here and it comes from exactly one place:
// a gateway-originated live-panel frame that carried no session id. The panel
// IS the operator, so the workspace-owned set is the correct answer for it.
// It is deliberately NOT ErrNoTabOwner, which is the correct answer for a
// TOOL with no transcript session — the two cases are one line apart and mean
// opposite things.
func (r *LiveViewRegistry) resolveSessionID(sessionID string) string {
	if sessionID == "" {
		return r.mgr.OperatorSessionID()
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
	return r.AttachContext(context.Background(), sessionID, viewerID, onStatus, onControl, onTabs)
}

// Detach unbinds viewerID from sessionID's live view. When this was the last
// viewer, the death watch on the session's tab is stopped. Also releases
// control if viewerID currently holds it, so a departing viewer never
// leaves the lock dangling for everyone else.
func (r *LiveViewRegistry) Detach(sessionID, viewerID string) {
	sessionID = r.resolveSessionID(sessionID)
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

// broadcastStatus fans a status message out to every attached viewer's
// StatusSink, one goroutine per sink for the same reason broadcastControl uses
// one (a slow consumer must never stall the caller — here that caller is a
// viewport apply, holding viewportMu). Callers must hold no LiveView lock.
func broadcastStatus(sinks []StatusSink, message string) {
	for _, sink := range sinks {
		go sink(message)
	}
}

// TakeControl grants viewerID exclusive interactive control of sessionID's
// live view. Returns false if another viewer already holds control — v1 is
// cooperative, first-come, no preemption (ADR-038 D6). Creates the LiveView
// entry if one doesn't exist yet (control can be requested before/without an
// attached viewer, though the SPA flow always attaches first).
func (r *LiveViewRegistry) TakeControl(sessionID, viewerID string) bool {
	sessionID = r.resolveSessionID(sessionID)
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
	sessionID = r.resolveSessionID(sessionID)
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
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return
	}
	lv.releaseControl(viewerID)
}

// Controller returns the viewerID currently holding control of sessionID's
// live view, or "" if uncontrolled (including when no live view exists yet).
func (r *LiveViewRegistry) Controller(sessionID string) string {
	sessionID = r.resolveSessionID(sessionID)
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
	inputState *liveInputState // bookkeeping under mu; commands serialize through its gate
	tabCtx     context.Context
	listenCtx  context.Context // child of tabCtx; canceling it stops the death watch without touching the tab
	stopListen context.CancelFunc
	// Watch ownership survives source death and is retired by detach or replacement.
	watchOwnerCtx  context.Context
	stopWatchOwner context.CancelFunc
	documentWatch  *liveDocumentWatch
	// lastKnownActiveCtx (ADR-047, wave-plan W2-A item 5) tracks the most
	// recently observed active-tab context INDEPENDENTLY of tabCtx — tabCtx
	// only reflects the current watch's binding and stays nil until a watch
	// is ever installed (isActiveLocked/hasEpochLocked gate on it), so a
	// session with no viewer ever attached would otherwise never have a
	// reliable "did the active tab actually change" signal for WebRTC
	// recapture. Seeded at attachment and updated on every tab notification;
	// nil only before either establishes the active target.
	lastKnownActiveCtx context.Context
	viewers            map[string]struct{}
	// statusSinks parallels viewers (ADR-038 finding #2): one optional
	// StatusSink per attached viewerID, notified on unexpected session death
	// or failed document refresh, never on a clean Detach.
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

	// --- ADR-085 browser control handover state (BROWSER-FR-020/026a/031a/047) ---
	//
	// releaseSinks parallels viewers/controlSinks: one optional
	// ReleaseNotifySink per attached viewerID, invoked ONLY when the
	// registry performs a server-initiated release that viewer did NOT
	// itself request (FR-031b) — a prompt release, an idle expiry, a
	// handover clear or a disabled-release sweep. Unlike ControlSink (fanned
	// out to every OTHER viewer, excluding the actor), this is addressed
	// directly to the former holder, because here the server is the actor
	// and the holder never gets an "own request" reply the way a
	// self-initiated release does.
	releaseSinks  map[string]ReleaseNotifySink
	acquiredSinks map[string]func()

	// standDownUntilPrompt is the FR-026a stand-down latch: set on every
	// transition INTO a stood-down state (a human take, or an agent
	// handover) and consulted by the tool gate ALONGSIDE lv.controller so
	// that releasing the interactive lock by a means other than a prompt or
	// an idle expiry — Escape, entering annotate mode, closing the panel, a
	// WS disconnect, the end of a turn — does NOT silently re-open the gate.
	// Cleared ONLY by clearStandDownLocked (the FR-029 prompt release and
	// the FR-031a idle expiry) and VOIDED (not cleared) alongside the
	// control lock whenever the gate finds a ghost controller (FR-031),
	// EXCEPT that a live handoverPending is never voided by that rule
	// (FR-047's explicit exemption) — see isStoodDownLocked.
	standDownUntilPrompt bool

	// handoverPending is the FR-047 agent-initiated handover state: distinct
	// from lv.controller because browser_handover (B6's tool) may have no
	// attached viewer to grant the lock to. Defers tools exactly as a held
	// wheel does and is explicitly exempt from the FR-031 ghost rule (there
	// is no "ghost" for a state with no holder in the first place).
	handoverPending bool
	// handoverReason is the FR-048a model-authored reason passed to
	// browser_handover, truncated to 200 runes by the caller before it
	// reaches here. Surfaced in the FR-041 waiting line and the FR-062
	// audit record for a handover; irrelevant (and left as the previous
	// hold's stale value, harmlessly) once handoverPending is false again.
	handoverReason string

	// holdStartedAtUnixNano is the FR-044 idempotency key: a wall-clock
	// nanosecond timestamp minted at the transition INTO a stood-down state
	// and reused verbatim for every FR-041 waiting-surface emission within
	// that one unbroken hold, so a second take with no intervening release
	// (or a re-delivered frame after a reconnect) never produces a second
	// visible line. Zero means "no stood-down period is currently open".
	holdStartedAtUnixNano int64

	// lastControlActivity is the FR-031a liveness clock: reset by viewer
	// input, an attach or detach, a manager.ViewerHeartbeat-adjacent touch
	// (the live panel's WS pong, wired from pkg/gateway/browser_ws.go), or
	// (best-effort) an active WebRTC media track — see TouchControlActivity.
	// A stood-down state with no attached viewer (a handover nobody came
	// to, a latch left behind after the operator closed the panel) has no
	// liveness signal by construction and runs its window from the
	// transition that set the state, which is why this is stamped
	// unconditionally on entry too. Consulted by the registry-level FR-031a
	// sweeper, never inside the hot tool-gate path.
	lastControlActivity time.Time

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

	// cssViewportScale records the deviceScaleFactor SetViewport last applied
	// (Emulation.setDeviceMetricsOverride), so rescaleToCSSViewport can derive
	// the captured surface's CSS size straight from the capture frame's own
	// dimensions when the cached layout viewport is provably inconsistent with
	// it — see viewportBasisForCapture. Zero means "never set / unknown", in
	// which case that fallback is unavailable and the layout viewport is used
	// as before.
	cssViewportScale float64

	// lastAppliedScale records the deviceScaleFactor whose CDP override
	// actually LANDED on this tab, and — unlike cssViewportScale — deliberately
	// survives invalidateCSSViewportCache. The override stays in force on the
	// target until something clears it, so when rescaleToCSSViewport refills an
	// invalidated cache (it can read the layout viewport, but Page.getLayoutMetrics
	// cannot tell it the scale) this is the honest value to restore. Without it
	// the scale stayed 0 forever after any invalidation, silently disabling
	// viewportBasisForCapture's capture-derived fallback.
	lastAppliedScale float64

	// lastRequestedW/H/Scale record the viewport the panel last ASKED for
	// (as opposed to what the tab ended up at). onTabsChanged replays it onto
	// the newly-active tab: the deviceScaleFactor override is per TARGET
	// (measured 2026-08-16), so a freshly-opened tab renders at 1x while the
	// encoder is still capturing at 2x — blur on every tab open — unless the
	// request is re-applied to the new target.
	lastRequestedW, lastRequestedH int
	lastRequestedScale             float64

	// viewportReapplyInFlight/viewportReapplyPending/viewportReapplyTargetCtx
	// COALESCE the onTabsChanged re-apply onto a single background worker, so
	// a burst of tab-set changes can neither stack several multi-round-trip
	// resizes on top of each other nor lose any of them.
	//
	// Round-2 finding F1 (2026-08-16): the in-flight flag alone DROPPED the
	// later changes instead of coalescing them. Switching A -> B -> C faster
	// than one re-apply completes (on the 2-CPU hosted box the settle alone is
	// ~350ms, so overlapping is the NORMAL case there, while macOS at
	// ~100-200ms rarely hits it — a parity defect by construction) left C with
	// neither the panel's viewport nor its own per-target deviceScaleFactor
	// override, silently reinstating the blur-on-every-tab-open the re-apply
	// exists to fix, and logging nothing at all.
	//
	// The shape mirrors CaptureSession.RecaptureForTabChange's: a second call
	// while a worker runs records the NEW target and sets pending, and the
	// worker loops once more against whatever target is current by then. That
	// is safe rather than lossy precisely because the worker re-reads the
	// target (and the panel's last requested geometry) at the top of every
	// pass — a burst converges on the last tab, which is the correct answer.
	viewportReapplyInFlight  bool
	viewportReapplyPending   bool
	viewportReapplyTargetCtx context.Context
	// Blocks capture publication until the newly active target has settled.
	pendingViewportTarget context.Context

	// scaleDegradedNotified/At throttle the user-facing "the picture may look
	// soft" notice applyViewport pushes when the deviceScaleFactor override
	// times out (round-2 finding F5). The override is renderer-bound, so it
	// only ever fails on a loaded box — i.e. only on hosted Linux, never on
	// the operator's Mac — and it used to produce a WARN log and nothing
	// else, in a gateway whose production log level is WARN-only for the
	// operator and invisible to the person actually watching the panel.
	// Cleared on the next successful override so a recovery re-arms the
	// notice; floored at scaleDegradedNoticeInterval so a wedged renderer
	// under a drag-resize cannot turn one degradation into a stream of
	// banners.
	scaleDegradedNotified   bool
	scaleDegradedNotifiedAt time.Time

	// basisWarnedKey latches viewportBasisForCapture's warning per CAPTURE
	// GEOMETRY rather than once per LiveView. The condition is per-geometry but
	// the call site is per input event (hundreds per scroll), so logging every
	// time would bury the line an operator needs — while latching it forever
	// made a mismatch that RECURS (a new capture size, a later resize) look
	// like a single historical event that had already been dealt with.
	basisWarnedKey string

	// basisProbeKey/W/H/At memoize the outcome of viewportBasisForCapture's
	// "who is right, the cache or the capture?" CDP probe for one
	// capture-vs-cache geometry pair, for viewportBasisProbeTTL. The probe costs
	// a round trip and its call site is per input event, so without this a
	// single disagreement would put a CDP call in front of every mouse move.
	basisProbeKey string
	basisProbeW   float64
	basisProbeH   float64
	basisProbeAt  time.Time

	// nextBasisRecaptureAt rate-limits the recapture request
	// viewportBasisForCapture issues when it proves the CAPTURE is the wrong
	// one. A recapture re-negotiates the WebRTC stream and the triggering
	// condition can persist, so an unlimited request would loop the video.
	nextBasisRecaptureAt time.Time

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
// still document — runCDPWithTimeout's doc comment). Attaching a viewer
// now registers its watches under one lv.mu acquisition without waiting for
// CDP. The document watch discovers the current page asynchronously after
// registration and reports failures through the viewer's status sink.
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
	if lv.inputState != nil {
		delete(lv.inputState.retired, tabCtx)
	}
	lv.lastControlActivity = time.Now() // FR-031a liveness: an attach resets the idle clock.
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
	lv.lastKnownActiveCtx = tabCtx
	lv.replaceWatchOwnerLocked()
	listenCtx, cancel := context.WithCancel(tabCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.installDocumentWatchLocked(listenCtx, tabCtx, true)

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

	// The notifying operation already owns target admission. A callback must
	// only read the active target, never re-enter admission or recreate Chrome.
	newCtx, _, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
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
	oldInputTarget := lv.lastKnownActiveCtx
	activeTabChanged := oldInputTarget != nil && oldInputTarget != newCtx
	lv.lastKnownActiveCtx = newCtx
	lv.mu.Unlock()

	if activeTabChanged {
		lv.retireInputTarget(oldInputTarget, newCtx)
		// The cached CSS viewport described the tab we just LEFT. Every
		// coordinate mapped through it from here on would be wrong, so it is
		// dropped rather than carried across — a stale-but-positive cache is
		// worse than an empty one (see invalidateCSSViewportCache).
		lv.invalidateCSSViewportCache()

		// Browser callbacks run under tab admission, so the measured update
		// must enter asynchronously after this callback returns.
		if !lv.reapplyViewportToNewTarget(newCtx) {
			cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
			if cs != nil {
				go func() {
					if err := lv.mgr.Live().RefreshCaptureFrameContext(newCtx, lv.sessionID, cs); err != nil {
						logger.WarnCF("browser", "live view: could not measure the newly active tab", map[string]any{"session_id": lv.sessionID, "error": err.Error()})
					}
				}()
			}
		}
	}

	if needsRebind {
		// A real tab change already scheduled its measured refresh above. Do
		// not let listener discovery start a second competing transition.
		lv.rebindWatch(newCtx, !activeTabChanged)
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
// self-correcting retry loop) are gone with them. The replacement document
// watch schedules asynchronous discovery; no CDP call runs under lv.mu.
func (lv *LiveView) rebindWatch(newCtx context.Context, initializePicture bool) {
	lv.mu.Lock()
	if !lv.hasEpochLocked() || lv.tabCtx == newCtx {
		lv.mu.Unlock()
		return
	}
	oldStopListen := lv.stopListen
	lv.replaceWatchOwnerLocked()
	lv.tabCtx = newCtx
	listenCtx, cancel := context.WithCancel(newCtx)
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.installDocumentWatchLocked(listenCtx, newCtx, initializePicture)
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
		lv.mu.Unlock()
		return
	}
	// Hand-built views may install a listen context directly. They still need
	// the same retirement fence as a view created through Attach.
	if lv.watchOwnerCtx == nil {
		lv.replaceWatchOwnerLocked()
	}
	owner := lv.watchOwnerCtx
	sessionID, mgr := lv.sessionID, lv.mgr
	lv.mu.Unlock()

	// Retain the original picture before observing browser death. Looking it
	// up only afterward can select a recovery capture installed during the check.
	var original *CaptureSession
	var frame CaptureFrameState
	if mgr != nil {
		original = mgr.CaptureSessionForPanel(sessionID)
		if original != nil {
			frame = original.FrameState()
		}
		if mgr.browserAlive(sessionID) {
			return
		}
	}
	if owner.Err() != nil {
		return
	}
	if mgr != nil && mgr.CaptureSessionForPanel(sessionID) != original {
		return
	}
	if original != nil {
		// An unmeasured capture has not claimed this source yet. In particular,
		// a recovery capture waiting for its first frame is not the dead picture.
		if frame.Generation == 0 || frame.TargetID == "" || frame.Width <= 0 || frame.Height <= 0 {
			return
		}
		sameFrame := func(current CaptureFrameState) bool {
			return current.Generation == frame.Generation && current.TargetID == frame.TargetID
		}
		original.stopWhen(func() bool {
			return owner.Err() == nil && sameFrame(original.frameStateLocked())
		})
		// Stop may drain transport work. A rebind or newer frame during that
		// drain must not inherit the old watcher's death notification.
		if owner.Err() != nil || !sameFrame(original.FrameState()) {
			return
		}
	}
	if mgr != nil {
		current := mgr.CaptureSessionForPanel(sessionID)
		// Stopping our own capture normally removes it from the manager.
		if current != nil && current != original {
			return
		}
	}
	lv.mu.Lock()
	if lv.listenCtx != watchedListenCtx || lv.watchOwnerCtx != owner || owner.Err() != nil {
		lv.mu.Unlock()
		return
	}
	lv.listenCtx = nil
	lv.stopListen = nil
	sinks := lv.snapshotStatusSinksLocked()
	lv.mu.Unlock()
	for _, sink := range sinks {
		if owner.Err() != nil {
			return
		}
		sink("browser session ended unexpectedly (the browser was restarted or shut down) — re-attach to resume watching")
	}
}

// replaceWatchOwnerLocked retires cleanup belonging to the previous watch.
// This context is deliberately independent of the source tab's lifetime.
func (lv *LiveView) replaceWatchOwnerLocked() {
	if lv.stopWatchOwner != nil {
		lv.stopWatchOwner()
	}
	lv.watchOwnerCtx, lv.stopWatchOwner = context.WithCancel(context.Background())
}

// detach removes viewerID and, if it was the last viewer, stops watching
// the session's tab for unexpected death.
func (lv *LiveView) detach(viewerID string) {
	lv.mu.Lock()
	delete(lv.viewers, viewerID)
	delete(lv.statusSinks, viewerID)
	delete(lv.controlSinks, viewerID)
	delete(lv.tabsSinks, viewerID)
	delete(lv.releaseSinks, viewerID)
	delete(lv.acquiredSinks, viewerID)
	lv.lastControlActivity = time.Now() // FR-031a liveness: a detach resets the idle clock too.
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
	if len(lv.viewers) == 0 {
		if lv.stopWatchOwner != nil {
			lv.stopWatchOwner()
		}
		lv.watchOwnerCtx, lv.stopWatchOwner = nil, nil
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
	lv.detachInput(viewerID)

	if stopListen != nil {
		stopListen()
	}
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
	return lv.ensureControlForInputContext(context.Background(), nil, nil, viewerID)
}

// A dedicated input source must still own an attached target when it takes
// control. Checking under the same lock as detach prevents a retired viewer
// from recreating a human hold after its attachment was removed.
func (lv *LiveView) ensureControlForInputContext(ctx context.Context, targetCtx, sourceCtx context.Context, viewerID string) bool {
	lv.mu.Lock()
	if targetCtx != nil {
		_, attached := lv.viewers[viewerID]
		if ctx.Err() != nil || inputSourceEnded(sourceCtx) || !attached || lv.tabCtx != targetCtx || lv.inputStateLocked().retired[targetCtx] {
			lv.mu.Unlock()
			return false
		}
	}
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
	// ADR-085 D3/FR-026a: a human hold stands the agent down.
	transitioned, holdStart := lv.enterStandDownLocked()
	notice := lv.noticeFor(StandDownByTake, "", holdStart)
	otherSinks := lv.snapshotControlSinksExceptLocked(viewerID)
	acquired := lv.acquiredSinks[viewerID]
	lv.mu.Unlock()

	if targetCtx != nil && acquired != nil {
		go acquired()
	}

	// FR-041/FR-044: exactly one waiting line per unbroken hold — only the
	// TRANSITION emits (see emitStandDownNotice; no lock is held here).
	if transitioned {
		emitStandDownNotice(notice)
	}
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
	// ADR-085 D3/FR-026a: a human hold stands the agent down.
	transitioned, holdStart := lv.enterStandDownLocked()
	notice := lv.noticeFor(StandDownByTake, "", holdStart)
	otherSinks := lv.snapshotControlSinksExceptLocked(viewerID)
	lv.mu.Unlock()

	// FR-041/FR-044 — see ensureControlForInput's identical call above.
	if transitioned {
		emitStandDownNotice(notice)
	}
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
