// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// ADR-038 D1 — /api/v1/browser/ws is a DEDICATED WebSocket, deliberately
// separate from /api/v1/chat/ws. It carries an independently-lifecycled
// stream of session/control/tab-strip lifecycle frames and WebRTC signaling;
// keeping it off the chat socket avoids interfering with chat's
// backpressure/replay logic (websocket.go's sendRawFrameBytes / replay
// divert). This file intentionally does not reuse wsConn/WSHandler's replay
// machinery — browser-live has no replay concept (a live view is either
// attached now or it isn't).

// browserWSSendCap is the outbound buffer depth for one connection. Deep
// enough to absorb a burst of tab-strip/status/WebRTC-signaling frames
// without immediately dropping frames on a client that's briefly slow to
// drain the socket.
const browserWSSendCap = 64

// browserWSMaxMessageBytes caps incoming frames. Every inbound frame type
// (attach/input/control/detach) is tiny; this just bounds a malformed or
// malicious client, mirroring websocket.go's wsMaxMessageBytes pattern at a
// much smaller scale since browser-live has no large inbound payloads.
const browserWSMaxMessageBytes = 64 * 1024

// browserWSConn holds one connection's write-side state. It intentionally
// carries far less than chat's wsConn (no replay divert, no session
// tracking) — this socket does exactly one thing: relay one live browser.
type browserWSConn struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	conn      *websocket.Conn
	sendCh    chan []byte
	doneCh    chan struct{}
	closeOnce sync.Once
}

func (c *browserWSConn) close() {
	c.closeOnce.Do(func() { close(c.doneCh) })
}

// sendCritical enqueues a must-not-drop frame (browser_status, browser_tabs,
// browser_webrtc_*, error) — every frame this socket carries is a state
// transition the SPA needs to see (ADR-061: there is no separate high-volume
// lossy stream on this connection any more). Blocks briefly rather than
// silently dropping; gives up after 2s so a wedged connection can't hang the caller.
// dropCtx is a short, caller-supplied identifier (see dropContext) logged
// ONLY if the frame is actually dropped (B6, 7-reviewer finding): before
// this the drop-warning below carried nothing identifying, making a dropped
// control-sync/error frame — now the SOLE trail of that event — impossible
// to correlate with a specific session/viewer after the fact.
func (c *browserWSConn) sendCritical(data []byte, dropCtx string) {
	select {
	case c.sendCh <- data:
	case <-c.doneCh:
	case <-time.After(2 * time.Second):
		slog.Warn("browser-ws: send channel full, dropping critical frame", "context", dropCtx)
	}
}

// sendCriticalGen marshals and enqueues a critical frame via sendCritical.
// dropCtx is forwarded verbatim — see sendCritical's doc comment.
func (c *browserWSConn) sendCriticalGen(frame any, dropCtx string) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal frame failed", "error", err)
		return
	}
	c.sendCritical(data, dropCtx)
}

// dropContext builds the short, human-readable identifier sendCritical(Gen)
// logs on a drop (B6): session id + viewer id + a short label for what was
// being sent, using whichever the call site has cheaply on hand (sessionID
// may legitimately be "" before a live view is attached).
func dropContext(sessionID, viewerID, label string) string {
	return fmt.Sprintf("session=%q viewer=%q frame=%s", sessionID, viewerID, label)
}

// browserConnState tracks the single live-browser attachment this connection
// currently holds.
//
// It is NO LONGER a readLoop-goroutine-only structure (FIX WAVE B finding A).
// browser_attach and browser_viewport used to be handled inline on readLoop,
// which made "only readLoop touches this" true by construction; both are now
// dispatched onto the connection's own serial worker (dispatchAttach /
// dispatchViewport, see browserConnWorkQueue) for the reason
// dispatchWebRTCOffer already documents — gorilla services the PongHandler
// only from inside a ReadMessage call, so a multi-second handler running
// inline starves every Pong and eventually kills the connection. Measured
// before this change: handleViewport -> SetViewport 6.95s against a busy
// page, handleAttach -> Live().Attach -> ensureStarted 1.0-2.2s warm and
// ~9.5s on a fresh profile, during all of which the panel accepted no
// clicks, keys, tab actions or detach.
//
// Consequently mgr/sessionID are written from the worker goroutine and read
// from readLoop's (handleInput/handleControl/handleTabAction) AND from a
// background WebRTC offer goroutine (applyColdStartRecapture,
// browser_webrtc.go — which already raced handleAttach's write before this
// change, since offer processing moved off readLoop first). Every access
// outside a fresh single-goroutine test fixture must go through
// attachment/bindAttachment/clearAttachment below, never the bare fields.
// The StatusSink/ControlSink/TabsSink callbacks still touch nothing here,
// only wc.sendCriticalGen, which is channel-safe.
type browserConnState struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	// attachMu guards mgr, sessionID, attachEpoch and the viewport-refusal
	// throttle pair below. Deliberately NOT webrtcMu: the two protect
	// independent lifecycles (a session attachment vs a WebRTC viewer) that
	// are established and torn down separately on the same connection, and
	// handleViewport legitimately needs both — folding them into one lock
	// would mean holding the WebRTC lock across a CDP-bound resize.
	attachMu sync.Mutex
	// attachEpoch is the attach-path twin of webrtcEpoch below, and works
	// identically: bumped synchronously on readLoop's goroutine the instant
	// a browser_attach frame is dispatched (beginAttach, called from
	// dispatchAttach BEFORE the worker runs it) so epoch order matches frame
	// arrival order, and bumped again whenever this connection is told to
	// give up any in-flight attach (invalidateAttach — an explicit
	// browser_detach or the connection closing). handleAttach captures the
	// value beginAttach returned and only commits its result via
	// bindAttachment if that value is still current; a superseded attach
	// tears down the LiveView attachment it just created instead of leaving
	// a viewer registered on a connection that no longer wants it.
	attachEpoch uint64
	mgr         *browser.BrowserManager
	// sessionID is the CLIENT-supplied (chat) session id from the attach
	// frame, kept for logging, for audit entries and for echoing back on
	// outgoing wire frames (ADR-038 finding #1). It is never itself a
	// manager-level session id and is never passed to mgr.Live() — the
	// live-view engine is addressed through panelSessionID below. A non-empty
	// value also doubles as "this connection currently has a live view
	// attached."
	sessionID string
	// panelSessionID is the RESOLVED manager-level tab set this connection's
	// live view drives — mgr.PanelTabSetID(sessionID), computed ONCE in
	// handleAttach and pinned here for the life of the attachment (issue
	// #671).
	//
	// It is either the chat's own tab set (when that chat has browsed) or the
	// operator's workspace-owned set, resolved by the mirror image of the rule
	// the agent's own tools resolve through — so the panel and the agent can
	// never end up on different tabs. Before #671 this was hardwired to the
	// operator's workspace-owned set at every call site, which meant that
	// whenever the operator's set was empty the panel lazily created a blank
	// /browser-start tab and showed THAT while the agent browsed elsewhere,
	// reporting success, with no error anywhere.
	//
	// Pinned rather than re-resolved per frame, for the same reason
	// attachEpoch exists: a viewer must stay on one tab set for the life of
	// the connection, not migrate mid-stream because a tab opened or closed
	// somewhere else. The SPA re-attaches (and so re-resolves) on reconnect.
	panelSessionID string
	// lastInputErrorSentAt throttles real-input-error browser_status(error)
	// frames (ADR-038 finding #4): a dead/crashed browser tab can fail every
	// subsequent input dispatch, and without this a fast input stream (mouse
	// moves while "driving") would flood the connection with one error frame
	// per rejected event. Only touched from readLoop's goroutine, same as
	// every other field here.
	lastInputErrorSentAt time.Time
	// lastInputErrorMessage is the text of the last real-input-error
	// browser_status(error) frame actually sent (7-reviewer LOW finding,
	// ADR-039): paired with lastInputErrorSentAt so the minInputErrorInterval
	// cooldown only ever suppresses a REPEATED, identical failure (e.g. a
	// dead tab failing every mouse-move the same way) — a genuinely NEW
	// failure reason, such as the user retrying navigate against a
	// DIFFERENT blocked URL right after an earlier one, is never swallowed
	// just because it landed inside the same 2s window. See handleInput's
	// doc comment.
	lastInputErrorMessage string
	// lastViewportAt throttles browser_viewport handling (review finding).
	// The SPA debounces at 400ms, but that is client-side and any
	// authenticated client can send raw frames faster. Each accepted frame
	// costs a CDP resize AND a full capture rebuild on the encoder, so an
	// unthrottled flood is a genuine thrash/DoS vector against the single
	// shared Chrome. Still touched ONLY from readLoop's goroutine: the
	// throttle check moved out of handleViewport and into dispatchViewport
	// (which runs on readLoop) when viewport handling moved onto the worker,
	// precisely so this field would not need a lock. Leaving it in
	// handleViewport would also have double-counted — dispatch stamps it,
	// then the worker would re-check it and drop its own frame.
	lastViewportAt time.Time

	// lastViewportRefusalAt / lastViewportRefusalMessage throttle the
	// not-the-controller viewport refusal message (FIX WAVE B finding B),
	// exactly as lastInputErrorSentAt/lastInputErrorMessage do for input
	// errors and for the same reason: a user dragging a resize handle emits
	// a frame every SPA debounce interval for as long as they drag, and each
	// one is refused identically. Guarded by attachMu because handleViewport
	// now runs on the worker goroutine. See minInputErrorInterval.
	lastViewportRefusalAt      time.Time
	lastViewportRefusalMessage string

	// work is this connection's serial worker for the slow frame handlers
	// (browser_attach, browser_viewport). See browserConnWorkQueue.
	work browserConnWorkQueue

	// webrtcMu guards webrtc and webrtcEpoch below (FIX WAVE A finding 1).
	// browser_webrtc_offer processing now runs off readLoop's own goroutine
	// (dispatchWebRTCOffer, browser_webrtc.go) so a slow/CDP-bound offer can
	// never block readLoop's ReadMessage loop — which means committing the
	// resulting attachment can now race readLoop's OWN synchronous
	// browser_detach handling and its connection-close cleanup, both of
	// which still run directly on readLoop's goroutine while an offer's
	// background goroutine may still be negotiating. Every access to
	// webrtc/webrtcEpoch outside a fresh, single-goroutine test fixture must
	// go through the methods below, never the bare fields.
	webrtcMu sync.Mutex
	// webrtc tracks this connection's attached WebRTC viewer (ADR-047 D4,
	// wave-plan W2-A) — separate from the session/control-lock attachment
	// above (sessionID/mgr), since both are established independently on the
	// SAME connection (handleAttach binds sessionID/mgr; a subsequent
	// browser_webrtc_offer is what populates this field). A single nullable pointer rather
	// than a (webrtcAgentID string, webrtcCapture *browser.CaptureSession)
	// field pair (fix-wave simplification): the two were always set and
	// cleared together, so the pair could represent an illegal
	// half-set/half-nil state the type system did nothing to prevent.
	// webrtc != nil iff a browser_webrtc_offer has succeeded, COMMITTED (see
	// commitWebRTCAttachment), and not yet been torn down (viewer detach,
	// connection close, or a stream failure).
	webrtc *webrtcAttachment
	// webrtcEpoch increments every time this connection either (a) begins
	// processing a NEW browser_webrtc_offer frame (beginWebRTCOffer, called
	// synchronously from readLoop the instant the frame is dispatched — see
	// that method's doc comment for why the bump must happen there, not
	// inside the goroutine it spawns) or (b) is told to give up any
	// in-flight offer (invalidateWebRTCOffer — an explicit browser_detach or
	// the connection itself closing). A background offer goroutine captures
	// the epoch beginWebRTCOffer returned and, once it finishes negotiating,
	// only commits its result via commitWebRTCAttachment if that captured
	// value still matches current — i.e. nothing newer (another offer, a
	// detach, or a close) has superseded it in the meantime. A stale commit
	// attempt tears down what it built instead of silently attaching a
	// viewer state this connection no longer wants.
	webrtcEpoch uint64

	// pendingCaptureScale remembers the device_scale_factor the most recent
	// browser_viewport frame carried, even when no WebRTC attachment yet
	// exists to receive it directly (F2, external review 2026-08-13, see
	// commitWebRTCAttachment's caller in browser_webrtc.go): a viewport
	// frame routinely arrives before a slow-negotiating browser_webrtc_offer
	// commits — cs.Start's own doc comment says that can take up to
	// captureStartTimeout (20s) — and the SPA's lastSentViewportRef dedup
	// means that first frame is often the ONLY one a cold-opened panel ever
	// sends. Without remembering it here, handleViewport's SetCaptureScale
	// call (gated on peekWebRTCAttachment() != nil) silently no-ops and the
	// Retina-blur fix stays inert until the user manually resizes the panel.
	// 0 is the sentinel for "nothing remembered yet", distinct from a
	// legitimately-sent 1 (see rememberViewportScale/pendingViewportScale).
	// Guarded by webrtcMu (not a new mutex) since it is written from
	// readLoop's goroutine (handleViewport) and read from a background offer
	// goroutine (handleWebRTCOffer's cold-start recapture) — the same
	// cross-goroutine timing webrtc/webrtcEpoch above already have to
	// account for.
	pendingCaptureScale float64
}

// beginAttach bumps this connection's attachEpoch and returns the new value.
// Called synchronously — still on readLoop's own goroutine — the instant a
// browser_attach frame is dispatched (dispatchAttach), BEFORE the worker
// runs handleAttach. Bumping here rather than inside the worker is what
// keeps epoch ordering aligned with the order frames actually ARRIVED in,
// the same argument beginWebRTCOffer's doc comment makes at length.
func (s *browserConnState) beginAttach() uint64 {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.attachEpoch++
	return s.attachEpoch
}

// invalidateAttach bumps attachEpoch without starting a new attach — used by
// an explicit browser_detach and by readLoop's connection-close cleanup, so
// an attach still negotiating on the worker recognizes, whenever it
// eventually finishes, that this connection no longer wants its result.
// Safe (and expected) to call unconditionally, exactly like
// detachWebRTCViewer's invalidateWebRTCOffer call: the whole point is to
// cover the case where nothing is committed YET.
func (s *browserConnState) invalidateAttach() {
	s.attachMu.Lock()
	s.attachEpoch++
	s.attachMu.Unlock()
}

// bindAttachment installs mgr/sessionID/panelSessionID as this connection's
// live-view attachment iff epoch still matches the CURRENT attachEpoch —
// returns false (and installs nothing) if a newer browser_attach, an explicit
// browser_detach, or the connection closing already superseded this
// generation while Live().Attach was still starting the browser. A caller
// that gets false owns the teardown of what it built (handleAttach detaches
// it), mirroring commitWebRTCAttachment's contract.
//
// panelSessionID is the RESOLVED tab set the live view drives (issue #671);
// sessionID stays the client's chat session id. The two are installed
// together, under one lock, so no handler can ever read a live manager
// against an unresolved (empty) tab set.
func (s *browserConnState) bindAttachment(
	epoch uint64, mgr *browser.BrowserManager, sessionID, panelSessionID string,
) bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attachEpoch != epoch {
		return false
	}
	s.mgr = mgr
	s.sessionID = sessionID
	s.panelSessionID = panelSessionID
	return true
}

// attachment returns this connection's current live-view attachment without
// clearing it: the manager, the client's CHAT session id, and the RESOLVED
// manager-level tab set the live view drives (issue #671). (nil, "", "") means
// "not attached" — every caller must check, exactly as the bare
// `state.mgr == nil || state.sessionID == ""` guards did before these fields
// needed a lock.
//
// All three come from one acquisition of attachMu on purpose: reading the
// manager and the tab set separately could mix a live manager with a
// just-cleared tab set and silently address the operator's set instead.
func (s *browserConnState) attachment() (mgr *browser.BrowserManager, sessionID, panelSessionID string) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return s.mgr, s.sessionID, s.panelSessionID
}

// resolvePanelTabSet answers "which tab set should this connection's live video
// bind to" for a path that runs OUTSIDE the attach handler — today, the
// WebRTC offer (issue #671).
//
// It prefers the id handleAttach already resolved and pinned, so the video and
// the control plane are provably the same tab set rather than two independent
// resolutions that agree by luck. The pinned value is only trusted when it
// belongs to the SAME manager this caller resolved: an offer whose agent (and
// therefore whose workspace browser) differs from the attachment's would
// otherwise be handed a session key minted for another browser entirely.
//
// The fallback re-runs the identical resolution against the same chat session
// id, for the real case where an offer's background goroutine reaches here
// before an attach has committed. mgr must not be nil — every caller has
// already failed the request otherwise.
func resolvePanelTabSet(
	state *browserConnState, mgr *browser.BrowserManager, chatSessionID string,
) string {
	if state != nil {
		if attached, _, pinned := state.attachment(); attached == mgr && pinned != "" {
			return pinned
		}
	}
	return mgr.PanelTabSetID(chatSessionID)
}

// clearAttachment atomically reads and clears the attachment, so teardown is
// idempotent no matter how many paths race for it (an explicit
// browser_detach, readLoop's close cleanup, and a re-attach all clear it).
// The reader that gets a non-nil manager back is the one that owns calling
// h.detach for it — the others get nil and do nothing.
func (s *browserConnState) clearAttachment() (mgr *browser.BrowserManager, sessionID, panelSessionID string) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	mgr, sessionID, panelSessionID = s.mgr, s.sessionID, s.panelSessionID
	s.mgr = nil
	s.sessionID = ""
	s.panelSessionID = ""
	return mgr, sessionID, panelSessionID
}

// shouldSendViewportRefusal reports whether the not-the-controller viewport
// refusal message should be sent to this viewer now, recording it when so.
// Content-aware like handleInput's cooldown: only a byte-identical repeat
// inside minInputErrorInterval is suppressed, so a genuinely different
// refusal reason is never swallowed by an earlier one.
func (s *browserConnState) shouldSendViewportRefusal(message string, now time.Time) bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if message == s.lastViewportRefusalMessage && now.Sub(s.lastViewportRefusalAt) < minInputErrorInterval {
		return false
	}
	s.lastViewportRefusalAt = now
	s.lastViewportRefusalMessage = message
	return true
}

// browserConnWorkKind identifies which slow frame handler a queued job runs.
// It exists solely to give browserConnWorkQueue its single-slot-per-kind
// coalescing rule — see submit.
type browserConnWorkKind uint8

const (
	workKindAttach browserConnWorkKind = iota
	workKindViewport
)

// browserConnWork is one queued job.
type browserConnWork struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	kind browserConnWorkKind
	run  func()
}

// browserConnWorkQueue is one connection's serial worker for the frame
// handlers that are too slow to run inline on readLoop (FIX WAVE B finding
// A). It is the attach/viewport equivalent of what dispatchWebRTCOffer does
// for offers, and it exists for the identical reason, spelled out in that
// function's doc comment: browser_ws.go's readLoop is gorilla/websocket's
// SOLE reader for this connection, and gorilla only invokes the registered
// PongHandler — which refreshes the 60s read deadline — from INSIDE a
// ReadMessage call. Anything multi-second running inline therefore starves
// every Pong and the peer tears the connection down, taking the session
// attachment with it.
//
// Three properties, all load-bearing:
//
//  1. submit NEVER blocks its caller. readLoop returns to ReadMessage
//     immediately, so Pongs keep being serviced and browser_input,
//     browser_control, browser_tab_action and browser_detach keep being
//     handled while a resize or an attach is still running.
//
//  2. Exactly one job runs at a time, in the order frames arrived. This is
//     the "two frames of the same kind cannot interleave" discipline
//     dispatchWebRTCOffer gets from its epoch, made stronger: a single
//     worker also preserves ordering ACROSS the two kinds, so the
//     attach-then-viewport sequence the SPA actually sends still applies in
//     that order. (Per-kind goroutines would not: a viewport could win the
//     race against the attach that has to precede it and be dropped as "no
//     live view bound".) It also means no two callers ever contend for
//     LiveView.viewportMu from this connection.
//
//  3. A newly submitted job REPLACES an already-queued job of the same kind,
//     keeping its queue position. For viewport that is exactly right — only
//     the final geometry of a drag matters, and each superseded frame would
//     otherwise cost a full CDP resize plus an encoder rebuild. For attach
//     it is equally right: one connection holds one live view, so the newest
//     attach is the only one whose result could survive anyway (the epoch
//     would discard the others' commits).
//
// The worker goroutine is tracked on h.activeConns — the SAME WaitGroup
// ServeHTTP holds an outstanding Add on for the connection's whole lifetime
// — so handler.Wait() (used by every test in this package via
// t.Cleanup(handler.Wait)) blocks until queued work has drained. Add()
// happens on readLoop's still-live goroutine, strictly before ServeHTTP's
// own Done() could fire, which is what makes that safe.
type browserConnWorkQueue struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	mu      sync.Mutex
	closed  bool
	running bool
	queue   []browserConnWork
}

// submit enqueues run under kind's single slot and starts the worker if it is
// not already going. Never blocks. A no-op once close has been called, so a
// frame that arrived just before the connection died is dropped rather than
// acted on against a dead connection.
func (q *browserConnWorkQueue) submit(wg *sync.WaitGroup, kind browserConnWorkKind, run func()) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	replaced := false
	for i := range q.queue {
		if q.queue[i].kind == kind {
			// Supersede the older, not-yet-STARTED job of this kind, keeping
			// its position so cross-kind arrival order is preserved.
			q.queue[i].run = run
			replaced = true
			break
		}
	}
	if !replaced {
		q.queue = append(q.queue, browserConnWork{kind: kind, run: run})
	}
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			q.mu.Lock()
			if len(q.queue) == 0 {
				q.running = false
				q.mu.Unlock()
				return
			}
			job := q.queue[0]
			q.queue = q.queue[1:]
			q.mu.Unlock()
			job.run()
		}
	}()
}

// browserConnWorkHook is a TEST-ONLY seam, nil in production, invoked as the
// FIRST statement of handleAttach and handleViewport — the two slow handlers
// — on whichever goroutine is running them. It exists for the same reason
// pkg/session routes its lock acquire/release through swappable function
// values (ADR-057 FR-101): the property that matters here — that readLoop
// keeps reading, and keeps servicing gorilla's PongHandler, WHILE a
// multi-second attach or resize is in flight — is otherwise only observable
// by making one of those handlers genuinely take multiple seconds, which
// needs a real Chromium and a real busy page.
//
// It is deliberately in the HANDLERS and not in browserConnWorkQueue. A test
// that blocks here holds up whichever goroutine actually executes the
// handler, so it fails by timeout the moment either handler is moved back
// inline onto readLoop — which a seam inside the queue could not detect,
// because reverting the fix removes the queue from the path entirely and the
// hook would simply never fire.
//
// Cost in production is one uncontended RLock per attach/viewport frame.
var (
	browserConnWorkHookMu sync.RWMutex
	browserConnWorkHook   func(browserConnWorkKind)
)

func runBrowserConnWorkHook(kind browserConnWorkKind) {
	browserConnWorkHookMu.RLock()
	hook := browserConnWorkHook
	browserConnWorkHookMu.RUnlock()
	if hook != nil {
		hook(kind)
	}
}

// setBrowserConnWorkHook installs (or clears, with nil) the test seam above.
func setBrowserConnWorkHook(hook func(browserConnWorkKind)) {
	browserConnWorkHookMu.Lock()
	browserConnWorkHook = hook
	browserConnWorkHookMu.Unlock()
}

// close drops every job not yet started and refuses further submissions. It
// does NOT wait for a job already running — readLoop's cleanup does not need
// to, because the attach epoch (invalidateAttach) makes a late-committing
// attach tear itself down, and h.activeConns still covers the goroutine for
// test Wait(). Blocking here instead would serialize connection teardown
// behind a CDP call that can take twenty seconds.
func (q *browserConnWorkQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.queue = nil
	q.mu.Unlock()
}

// beginWebRTCOffer bumps this connection's webrtcEpoch and returns the new
// value. Called synchronously — still on readLoop's own goroutine — the
// instant a browser_webrtc_offer frame is dispatched (dispatchWebRTCOffer,
// browser_webrtc.go), BEFORE the goroutine that will actually process it is
// spawned. Bumping here rather than inside that (unpredictably-scheduled)
// goroutine is what keeps epoch ordering aligned with the ORDER frames
// actually arrived in: a second offer frame dispatched a moment later always
// observes (and invalidates) whatever the first offer's goroutine captured,
// regardless of which goroutine the Go scheduler happens to run first.
func (s *browserConnState) beginWebRTCOffer() uint64 {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	s.webrtcEpoch++
	return s.webrtcEpoch
}

// invalidateWebRTCOffer bumps webrtcEpoch without starting a new offer —
// used by an explicit browser_detach and by the connection-close cleanup
// (both via detachWebRTCViewer) so a background offer goroutine that hasn't
// committed yet (see commitWebRTCAttachment) recognizes, whenever it
// eventually finishes negotiating, that this connection no longer wants its
// result.
func (s *browserConnState) invalidateWebRTCOffer() {
	s.webrtcMu.Lock()
	s.webrtcEpoch++
	s.webrtcMu.Unlock()
}

// commitWebRTCAttachment installs att as this connection's WebRTC attachment
// iff epoch still matches the CURRENT webrtcEpoch — returns false (does not
// install) if a newer offer, an explicit detach, or the connection closing
// already invalidated this generation while handleWebRTCOffer was
// negotiating in the background.
func (s *browserConnState) commitWebRTCAttachment(epoch uint64, att *webrtcAttachment) bool {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	if s.webrtcEpoch != epoch {
		return false
	}
	s.webrtc = att
	return true
}

// takeWebRTCAttachment atomically reads and clears this connection's WebRTC
// attachment (used by detachWebRTCViewer) — safe to call whether or not an
// offer goroutine is concurrently trying to commit one, since both go
// through the same webrtcMu.
func (s *browserConnState) takeWebRTCAttachment() *webrtcAttachment {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	att := s.webrtc
	s.webrtc = nil
	return att
}

// peekWebRTCAttachment returns the currently-committed attachment WITHOUT
// clearing it, for callers that need to act on the live capture session and
// leave it in place (handleViewport). Distinct from takeWebRTCAttachment,
// whose clear-on-read is what makes teardown idempotent — using that here
// would silently detach the viewer as a side effect of a resize.
func (s *browserConnState) peekWebRTCAttachment() *webrtcAttachment {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	return s.webrtc
}

// rememberViewportScale records dsf as this connection's pendingCaptureScale
// (F2 fix) — called unconditionally from handleViewport on every accepted
// browser_viewport frame, regardless of whether a WebRTC attachment exists
// yet to apply it to directly. See pendingCaptureScale's doc comment for why
// this exists and pendingViewportScale for the read side.
func (s *browserConnState) rememberViewportScale(dsf float64) {
	s.webrtcMu.Lock()
	s.pendingCaptureScale = dsf
	s.webrtcMu.Unlock()
}

// pendingViewportScale returns the last device_scale_factor remembered via
// rememberViewportScale, or 0 if no browser_viewport frame has arrived on
// this connection yet. Consulted by handleWebRTCOffer's cold-start recapture
// (browser_webrtc.go) the moment a WebRTC attachment actually commits, so a
// scale that arrived too early to apply directly is not lost.
func (s *browserConnState) pendingViewportScale() float64 {
	s.webrtcMu.Lock()
	defer s.webrtcMu.Unlock()
	return s.pendingCaptureScale
}

// minInputErrorInterval is the minimum gap between two IDENTICAL real-input-
// error browser_status(error) frames sent to the same connection (ADR-038
// finding #4). A different error message bypasses the cooldown entirely —
// see browserConnState.lastInputErrorMessage's doc comment.
const minInputErrorInterval = 2 * time.Second

// minViewportInterval is the server-side floor between two accepted
// browser_viewport frames on one connection. Comfortably below the SPA's
// 400ms debounce so normal resizing is never throttled, while bounding what a
// hostile or buggy client can force. See browserConnState.lastViewportAt.
const minViewportInterval = 300 * time.Millisecond

// maxDeviceScaleFactor is the range-check ceiling handleViewport applies to
// an inbound device_scale_factor BEFORE recording it anywhere (F10 fix,
// external review 2026-08-13). It mirrors two independent values that must
// stay in lockstep: BrowserViewportFrame.device_scale_factor's contract
// maximum (contracts/components/schemas/BrowserViewportFrame.yaml) and
// pkg/tools/browser/live.go's unexported maxViewportScaleFactor, which
// SetViewport uses for its OWN range check. Both exist already — this const
// does not relax or duplicate either, it just makes the same bound apply
// BEFORE the value reaches CaptureSession.SetCaptureScale, which today has
// no upper clamp of its own (CaptureScale() only floors values below 1).
// gateway.validate_inbound defaults to false, so on a default install this
// local check is the ONLY thing enforcing the schema maximum: without it, a
// malformed client sending device_scale_factor:50 could persist an
// out-of-contract value on the capture session, which the
// browser_capture_control frame's capture_scale field (max 4) would then
// ship downstream in violation of its own contract.
const maxDeviceScaleFactor = 3.0

// BrowserWSHandler implements the /api/v1/browser/ws endpoint (ADR-038):
// session/control/tab-strip lifecycle out, input-injection in, and WebRTC
// signaling (ADR-047) for the live interactive browser panel. One connection
// == one viewer == at most one attached (agent, session) live view at a
// time.
type BrowserWSHandler struct {
	agentLoop     *agent.AgentLoop
	allowedOrigin string
	upgrader      websocket.Upgrader

	// activeConns tracks in-flight ServeHTTP goroutines so Wait() can block
	// until all connections have fully torn down (test cleanup, mirroring
	// WSHandler.Wait()).
	activeConns sync.WaitGroup

	// captures is the ADR-047 / wave-plan W2-A per-agent WebRTC capture
	// session registry (browser_webrtc.go), shared with the capture-ingest
	// WS handler so a browser_capture_hello can locate the CaptureSession
	// its token belongs to.
	captures *captureRegistry

	// mediaConn is the ONE gateway-owned UDP socket every agent's viewer leg
	// multiplexes media over (ADR-062 tier 1), lazily bound on first use and
	// reused for the process's lifetime. nil when fixed-port media is not
	// configured (the laptop default) -- Sessions then use ephemeral ports,
	// exactly as before ADR-062.
	//
	// Gateway-owned, not Session-owned, because a Session exists PER AGENT:
	// if each bound the same fixed port itself, the first agent would win and
	// every later one would silently fall back to an ephemeral port, giving a
	// multi-agent hosted install working video for one agent and an
	// inexplicable failure for the rest.
	mediaConnMu sync.Mutex
	mediaConn   net.PacketConn
	mediaTCP    net.Listener
	// mediaTCPBindErr records that ICE-TCP (ADR-062 tier 2) was configured
	// but its listener could not be bound. Guarded by mediaConnMu. Like
	// mediaPortFallback it exists so the failure reaches the PANEL, not just
	// a log line: an operator who declared a TCP media port and silently got
	// nothing has no other way to find out.
	mediaTCPBindErr error

	// turnServer is the process-wide embedded TURN relay (ADR-062 tier 3),
	// lazily started on first use and nil when the operator has not declared a
	// port for it. Guarded by mediaConnMu, like the sockets above.
	turnServer   *webrtc.TURNServer
	turnStarted  bool
	turnStartErr error

	// mediaPortFallback is non-nil ONLY when the fixed media UDP port the
	// operator explicitly configured could not be bound and sharedMediaConn
	// fell back (to a neighbouring port, or all the way to ephemeral ports).
	// Guarded by mediaConnMu, written at most once alongside mediaConn.
	//
	// The fallback keeps live video working on a laptop, but on a hosted
	// install it silently guarantees the opposite: providers route only the
	// port you declared, so every remote viewer gets a dead panel. This field
	// is what lets the gateway TELL that viewer (notifyMediaPortDegraded)
	// instead of leaving the failure to a log line only the operator would
	// ever see, and only if they went looking. See mediaPortFallbackState in
	// browser_webrtc.go.
	mediaPortFallback *mediaPortFallbackState

	// captureFenceMu serializes handleWebRTCOffer's ADR-048 condition-2
	// fence-check + ensure/registry-set sequence (fix-wave HIGH, TOCTOU
	// fix): without it, two DIFFERENT agents' very first viewer offers could
	// both observe every OTHER agent's capture session as absent/viewerless
	// (h.captures.otherSessions is a point-in-time snapshot) and both
	// proceed to start a capture session, defeating the single-capture
	// invariant the fence exists to enforce. Held ONLY across the cheap
	// fence-check + ensureCaptureSession call (registers/reuses the
	// CaptureSession object, no CDP round trip) — released BEFORE
	// cs.Start(), whose encoder-page CDP round trip can take up to
	// captureStartTimeout (20s), so concurrent offers for an agent that
	// already has a registered session (the common multi-viewer case) are
	// never serialized behind an unrelated agent's slow start.
	captureFenceMu sync.Mutex

	// viewerConns is the fix-wave per-viewer registry (browser_webrtc.go's
	// webrtcViewerConn) letting the encoder-liveness watchdog and the
	// data-channel input sink reach a WebRTC-attached viewer's main WS
	// connection from a goroutine other than that connection's own
	// readLoop. Keyed by viewerID; zero value (unstored sync.Map) is ready
	// to use.
	viewerConns sync.Map
}

// newBrowserWSHandler constructs a BrowserWSHandler. allowedOrigin is the
// same value the gateway computes for the chat WS (middleware.
// CanonicalGatewayOrigin) — passed in rather than recomputed so the two
// sockets can never disagree on CORS/origin policy.
func newBrowserWSHandler(agentLoop *agent.AgentLoop, allowedOrigin string) *BrowserWSHandler {
	return &BrowserWSHandler{
		agentLoop:     agentLoop,
		allowedOrigin: allowedOrigin,
		upgrader: websocket.Upgrader{
			CheckOrigin: wsCheckOrigin(allowedOrigin),
		},
		captures: newCaptureRegistry(),
	}
}

// Wait blocks until all active ServeHTTP goroutines have fully exited.
func (h *BrowserWSHandler) Wait() {
	h.activeConns.Wait()
}

// ServeHTTP handles the WebSocket upgrade and full connection lifecycle.
func (h *BrowserWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.activeConns.Add(1)
	defer h.activeConns.Done()

	origin := h.allowedOrigin
	if origin == "" {
		origin = "http://localhost:5000"
	}

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().
			Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Upgrade, Connection, Sec-WebSocket-Key, Sec-WebSocket-Version")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("browser-ws: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	userID, ok := h.authenticate(conn, r)
	if !ok {
		return
	}

	// Config gate (ADR-038 D6): checked post-auth so an unauthenticated probe
	// can't learn whether the feature is enabled. Refusal is a browser_status
	// error frame, not an HTTP status — the SPA already expects to parse WS
	// frames for this endpoint's state, and a raw upgrade-time HTTP rejection
	// surfaces to browser JS as an opaque WebSocket error with no message.
	cfg := h.agentLoop.GetConfig()
	if !cfg.Tools.Browser.LiveViewEnabled {
		msg := "live browser view is disabled (tools.browser.live_view_enabled=false)"
		sendGenWSFrame(conn, generated.BrowserStatusFrame{
			Type:    string(generated.WsFrameTypeBrowserStatus),
			State:   "error",
			Message: &msg,
		})
		return
	}

	wc := &browserWSConn{
		conn:   conn,
		sendCh: make(chan []byte, browserWSSendCap),
		doneCh: make(chan struct{}),
	}
	viewerID := uuid.New().String()

	go h.writePump(wc)
	go h.pingPump(wc)
	defer wc.close()

	h.readLoop(conn, wc, viewerID, userID, cfg)
}

// authenticate authenticates the WS handshake via EITHER the omnipus-session
// cookie (checked first, synchronously, against the upgrade request r) OR the
// legacy first-message {"type":"auth","token":...} frame (FR-009), mirroring
// WSHandler.authenticateWS (websocket.go) exactly — see that function's doc
// for the full rationale (cookie checked before the blocking frame read so a
// cookie-only client, which sends no frame at all post-Wave-1, isn't stuck
// waiting on one). The frame path's identity resolution is unchanged: every
// account in Gateway.Users first (via the shared resolveBearerIdentity
// helper), then Gateway.CLIToken, then the legacy OMNIPUS_BEARER_TOKEN env
// var, then dev_mode_bypass — so browser-live can never authenticate a
// caller chat would have rejected, or vice versa. On failure this has
// already written an error frame + close message; the caller must return
// without proceeding.
//
// Returns the resolved userID. Per the documented Entry.User contract
// (pkg/audit/audit.go), the env-token and dev-bypass paths deliberately
// return "" (not a synthesized identity) — same as chat's wc.userID, which
// only resolveBearerIdentity's match branch (and now the cookie branch) ever
// sets.
func (h *BrowserWSHandler) authenticate(conn *websocket.Conn, r *http.Request) (string, bool) {
	cfg := h.agentLoop.GetConfig()

	if user, err := middleware.ResolveUserFromCookie(r, cfg.Gateway.Users); err == nil && user != nil {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return user.Username, true
	}
	// SFH-1: surface "cookie present but invalid" (replay/probe/stale
	// cookie) as a log line — silent for the routine "no cookie at all"
	// case (see LogInvalidSessionCookiePresent's doc). Log-only: falling
	// through to the frame-based auth path below is unchanged either way.
	middleware.LogInvalidSessionCookiePresent(r, cfg)

	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		slog.Warn("browser-ws: auth read failed", "error", err)
		return "", false
	}

	var authFrame generated.AuthFrame
	if jsonErr := json.Unmarshal(
		data,
		&authFrame,
	); jsonErr != nil ||
		authFrame.Type != string(generated.WsFrameTypeAuth) {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrBadFirstFrame,
		})
		return "", false
	}

	rawToken := authFrame.Token

	if user, _, matched := resolveBearerIdentity(cfg, rawToken); matched {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return user.Username, true
	}

	if bearerAccountsConfigured(cfg) {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrInvalidToken,
		})
		writeCloseAuthFailed(conn)
		return "", false
	}

	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return "", true
		}
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrNoUsers,
		})
		writeCloseAuthFailed(conn)
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) != 1 {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrInvalidToken,
		})
		writeCloseAuthFailed(conn)
		return "", false
	}
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	return "", true
}

// writeCloseAuthFailed sends the WS close control frame used by every
// authentication failure branch, matching authenticateWS's behavior exactly.
func writeCloseAuthFailed(conn *websocket.Conn) {
	// Same invariant as writePump above: a close frame to an unresponsive
	// client must not block this goroutine forever.
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		slog.Debug("browser-ws: SetWriteDeadline failed for close frame", "error", err)
		return
	}
	if err := conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
	); err != nil {
		slog.Debug("browser-ws: write close frame failed", "error", err)
	}
}

// writePump is the single goroutine that writes all frames to the
// connection. gorilla/websocket requires all writes to happen from the same
// goroutine. A nil message on sendCh is the sentinel for a ping frame.
func (h *BrowserWSHandler) writePump(wc *browserWSConn) {
	// defer close (2026-07-31, found by the sibling instance's reviewers and
	// verified here): every exit path below is a bare `return`. Without this,
	// a write failure left the connection WRITE-dead but READ-alive — doneCh
	// was never closed, so sendCritical kept selecting on a channel
	// nobody would ever close, and readLoop kept refreshing its deadline from
	// whatever the client was still sending. The socket was then only reaped
	// by the CLIENT's own missed-ping self-heal ~60s later. close() is
	// sync.Once-guarded, so this is safe alongside every other caller.
	defer wc.close()

	for {
		select {
		case msg, ok := <-wc.sendCh:
			if !ok {
				return
			}
			// SetWriteDeadline before EVERY write, ping included (2026-07-31).
			// This socket had none at all, while the chat socket
			// (websocket.go's writePump, wsWriteWait) has had them for some
			// time — the same invariant, applied to only one of the two.
			//
			// Without a deadline, WriteMessage blocks INDEFINITELY once the
			// client's TCP receive window fills, wedging this single writer
			// goroutine for good: no further frames, and — worse — no further
			// keepalive pings. The peer then hits its own read timeout and
			// tears the connection down, which is what surfaces as the
			// abnormal `close 1006` the operator has been seeing (33 of them
			// in one session's log). At the time this fix landed, this socket
			// was the one carrying the high-volume JPEG screencast stream
			// (since removed, ADR-061), so it was by far the most likely of
			// the two to fill a window in the first place.
			//
			// With the deadline, a stalled write fails fast and this pump
			// exits cleanly, letting the normal reconnect path run instead of
			// leaving a half-dead connection nobody times out for 60s.
			if err := wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
				slog.Debug("browser-ws: SetWriteDeadline failed", "error", err)
				return
			}
			if msg == nil {
				if err := wc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					slog.Debug("browser-ws: ping write error", "error", err)
					return
				}
				continue
			}
			if err := wc.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Debug("browser-ws: write error", "error", err)
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// pingPump enqueues a nil sentinel onto sendCh every 30s for keep-alive.
func (h *BrowserWSHandler) pingPump(wc *browserWSConn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case wc.sendCh <- nil:
			case <-wc.doneCh:
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// readLoop processes client frames until the connection closes, dispatching
// each on its "type" discriminator. On return, it detaches any live-view
// attachment this connection still held (also releasing control, if held) so
// a disconnect without an explicit browser_detach never leaves a dangling
// viewer or a stuck control lock.
func (h *BrowserWSHandler) readLoop(
	conn *websocket.Conn,
	wc *browserWSConn,
	viewerID, userID string,
	cfg *config.Config,
) {
	conn.SetReadLimit(browserWSMaxMessageBytes)

	var state browserConnState

	// Re-register the PongHandler now that this connection has a viewer
	// identity and a place to record one. It still does what the handshake's
	// handler did — refresh the 60s read deadline — and additionally stamps
	// the manager's viewer heartbeat (ADR-075 FR-052).
	//
	// The pong is the right signal precisely because it owes nothing to the
	// user: pingPump sends a ping every 30s and the peer's browser answers it
	// whether the person is clicking or just reading. A viewer who has not
	// touched anything in an hour keeps proving it is there, while a socket
	// whose owner is gone stops within one deadline. Without this the manager
	// has only ViewerAttached/ViewerDetached to go on, and a connection whose
	// cleanup never runs (SIGKILL, a half-open socket, a panic past readLoop's
	// defer) pins its workspace's Chrome permanently.
	//
	// Cheap enough to run inline in gorilla's read path: state.attachment()
	// and ViewerHeartbeat each take a mutex that is never held across I/O.
	// Before the first successful browser_attach there is nothing to stamp,
	// and the deadline refresh still happens.
	//
	// It stamps the RESOLVED panel tab set, NOT the chat session id state also
	// carries. state's sessionID is the CHAT session id — kept for audit and
	// wire echo only — whereas the live view, and therefore
	// ViewerAttached/ViewerDetached, target the manager-level tab set this
	// connection resolved at attach (issue #671; see handleAttach's
	// Live().Attach call and h.detach). Stamping the chat id here would find
	// no such session on the manager and silently do nothing, which does not
	// fail loudly: it looks fine until every live viewer ages out of
	// viewerLivenessWindow and has its browser closed while somebody is
	// watching it.
	conn.SetPongHandler(func(_ string) error {
		if mgr, sessionID, panelSessionID := state.attachment(); mgr != nil && sessionID != "" {
			mgr.ViewerHeartbeat(panelSessionID)
		}
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	defer func() {
		// Order matters. close() first, so a browser_attach/browser_viewport
		// that arrived moments before the socket died is dropped instead of
		// acted on. invalidateAttach() second, so an attach ALREADY running
		// on the worker (Live().Attach can take seconds on a fresh profile)
		// fails its commit and detaches what it built. clearAttachment()
		// third, which covers the other side of that race — the attach
		// committed just before we invalidated — and returns non-nil to
		// exactly one of the two paths, so the viewer is detached once and
		// only once.
		state.work.close()
		state.invalidateAttach()
		if mgr, sessionID, panelSessionID := state.clearAttachment(); mgr != nil && sessionID != "" {
			h.detach(mgr, sessionID, panelSessionID, viewerID, userID)
		}
		// detachWebRTCViewer is called unconditionally (not gated on
		// state.webrtc != nil): a browser_webrtc_offer dispatched via
		// dispatchWebRTCOffer may still be negotiating in the background
		// when this connection closes, with nothing committed to state.webrtc
		// yet — detachWebRTCViewer's invalidateWebRTCOffer call (FIX WAVE A
		// finding 1) is what tells that goroutine, once it finishes, to tear
		// down what it built instead of attaching a viewer state this
		// now-closed connection can never use.
		h.detachWebRTCViewer(&state, viewerID)
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				slog.Debug("browser-ws: read error", "error", err)
			}
			return
		}

		var typ wsTypeOnly
		if jsonErr := json.Unmarshal(data, &typ); jsonErr != nil {
			wc.sendCriticalGen(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "invalid frame: not JSON",
			}, dropContext("", viewerID, "invalid-json"))
			continue
		}

		// Inbound schema validation (ADR-038 finding #3), mirroring
		// websocket.go's chat-WS readLoop exactly: gated by
		// gateway.validate_inbound, enforces the enum/maxLength/modifiers
		// (0-15) constraints declared in
		// contracts/components/schemas/Browser{Attach,Input,Control,Detach}Frame.yaml
		// server-side, before the frame is dispatched. A schema-invalid
		// frame is dropped and reported as a browser_status(error) — this
		// socket has no ErrorFrame-based rejection path for client frames,
		// browser_status is the one client-visible failure channel.
		if cfg.Gateway.ValidateInbound {
			if schemaName := wsFrameSchemaName(typ.Type); schemaName != "" {
				if errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data); errMsg != "" {
					if serverErr {
						slog.Error("browser-ws: inbound schema unavailable, dropping frame",
							"schema", schemaName, "frame_type", typ.Type)
					} else {
						slog.Warn("browser-ws: inbound frame schema validation failed — dropping",
							"schema", schemaName, "frame_type", typ.Type, "error", errMsg)
					}
					wc.sendCriticalGen(
						errorStatus(fmt.Sprintf("frame schema validation failed (%s): %s", schemaName, errMsg)),
						dropContext("", viewerID, "schema-invalid:"+typ.Type),
					)
					continue
				}
			}
		}

		switch typ.Type {
		case string(generated.WsFrameTypeBrowserAttach):
			// FIX WAVE B finding A: dispatched onto this connection's serial
			// worker rather than handled inline, for exactly the reason
			// dispatchWebRTCOffer already documents. handleAttach ->
			// Live().Attach -> Session() -> ensureStarted creates the browser
			// context and the first tab; MEASURED 1.0-2.2s warm and ~9.5s for
			// a whole first open on a fresh profile (which once failed
			// outright). Run inline that starved every Pong and the panel
			// accepted no clicks, keys, tab actions or detach for the
			// duration. See browserConnWorkQueue and beginAttach.
			h.dispatchAttach(wc, &state, viewerID, userID, data, cfg)
		case string(generated.WsFrameTypeBrowserInput):
			h.handleInput(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserControl):
			h.handleControl(wc, &state, viewerID, userID, data, cfg)
		case string(generated.WsFrameTypeBrowserTabAction):
			h.handleTabAction(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserViewport):
			// FIX WAVE B finding A, same reasoning as browser_attach above.
			// handleViewport -> SetViewport was MEASURED at 6.95s against a
			// busy page — LiveView.SetViewport holds viewportMu across
			// several CDP round trips by design — during which this
			// connection could read nothing at all.
			h.dispatchViewport(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserDetach):
			h.handleDetach(wc, &state, viewerID, userID)
			// Unconditional for the same reason as readLoop's own cleanup
			// defer above: an in-flight background offer (dispatchWebRTCOffer)
			// may not have committed to state.webrtc yet, but this explicit
			// detach must still invalidate it.
			h.detachWebRTCViewer(&state, viewerID)
		case string(generated.WsFrameTypeBrowserWebrtcOffer):
			// FIX WAVE A finding 1: dispatched onto its own goroutine
			// (dispatchWebRTCOffer, browser_webrtc.go) rather than handled
			// inline — handleWebRTCOffer's slow path (cs.Start's CDP round
			// trip, HandleViewerOffer's waitForTracks) must never block THIS
			// ReadMessage loop, since gorilla only services the PongHandler
			// (which refreshes the 60s read deadline set below) from inside a
			// ReadMessage call. A synchronous call here previously starved
			// every Pong, killing the connection's own session attachment
			// along with the stalled WebRTC attempt — see dispatchWebRTCOffer's
			// doc comment.
			h.dispatchWebRTCOffer(wc, &state, viewerID, userID, data, cfg)
		default:
			wc.sendCriticalGen(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: fmt.Sprintf("unknown frame type %q", typ.Type),
			}, dropContext("", viewerID, "unknown-type:"+typ.Type))
		}
	}
}

// dispatchAttach hands a browser_attach frame to this connection's serial
// worker so handleAttach's browser-start cost never blocks readLoop's
// ReadMessage call (FIX WAVE B finding A) — the attach-path twin of
// dispatchWebRTCOffer, and deliberately shaped the same way.
//
// state.beginAttach() is called HERE, synchronously, still on readLoop's own
// goroutine, BEFORE the job is queued — see that method's doc comment for
// why the epoch bump must happen at dispatch time rather than inside the
// (unpredictably-scheduled) worker.
//
// Ordering preserved by construction: attach and viewport share ONE worker,
// so a browser_viewport that arrived after this frame still runs after it,
// exactly as when both ran inline. What is deliberately NOT preserved is
// attach's ordering against the frames that still run inline
// (browser_input / browser_control / browser_tab_action): each of those
// already had, and still has, an explicit "not attached yet" guard, and each
// now takes that branch during the attach window instead of waiting the
// whole 1-9.5s for it. That is the trade the fix exists to make — the panel
// staying responsive is worth an input sent before the video exists being
// dropped, which is what the guard did with it anyway.
func (h *BrowserWSHandler) dispatchAttach(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
) {
	epoch := state.beginAttach()
	state.work.submit(&h.activeConns, workKindAttach, func() {
		h.handleAttach(wc, state, viewerID, userID, data, cfg, epoch)
	})
}

// dispatchViewport hands a browser_viewport frame to this connection's serial
// worker (FIX WAVE B finding A). The minViewportInterval floor is applied
// HERE, on readLoop's goroutine, not inside handleViewport: it is a couple of
// clock reads, it keeps lastViewportAt single-goroutine (no lock), and
// leaving it in the handler would have made the worker re-check a timestamp
// dispatch had just stamped and drop its own frame every time.
//
// The floor and the work queue's coalescing are complementary, not
// redundant: the floor bounds how fast a hostile client can make us do
// anything at all, while coalescing means a legitimate drag that clears the
// floor still costs ONE CDP resize plus one encoder rebuild at the final
// geometry instead of one per frame.
func (h *BrowserWSHandler) dispatchViewport(
	wc *browserWSConn,
	state *browserConnState,
	viewerID string,
	data []byte,
) {
	now := time.Now()
	if now.Sub(state.lastViewportAt) < minViewportInterval {
		slog.Debug("browser-ws: viewport frame throttled", "viewer_id", viewerID)
		return
	}
	state.lastViewportAt = now
	state.work.submit(&h.activeConns, workKindViewport, func() {
		h.handleViewport(wc, state, viewerID, data)
	})
}

// handleAttach binds this connection to the target agent's live browser
// (ADR-038 D3, video path retired per ADR-061): resolves the agent's
// BrowserManager and starts (or joins) watching its session for control-lock
// and tab-strip bookkeeping until detach. Video for the panel is carried
// exclusively by WebRTC (announceWebRTCAvailability below) — there is no
// screencast frame stream on this path any more. A second browser_attach on
// an already-attached connection first detaches the previous attachment —
// one connection, one live view at a time.
//
// ADR-038 finding #1, as amended by issue #671: the live view binds to the
// tab set mgr.PanelTabSetID(frame.SessionId) RESOLVES — never to
// frame.SessionId itself, and no longer unconditionally to the operator's
// workspace-owned set either.
//
// frame.SessionId is the client's chat session id. ADR-038 found it being
// passed straight to mgr.Live().Attach(), which lazily created a brand-new,
// blank tab keyed by that chat UUID, distinct from the tab the agent was
// navigating: the live view showed an unrelated blank tab, and
// browser_control{take} locked a session the agent's own tools (which check
// IsControlled on their own resolved owner) never consulted — "take control"
// was a no-op from the agent's perspective. Hardwiring the operator's set
// instead fixed that, and introduced #671's mirror image: with an EMPTY
// operator set the agent browses in the chat's own tab set, so the panel
// lazily created a workspace-owned tab parked on /browser-start and showed
// that instead — again with no error anywhere.
//
// PanelTabSetID (pkg/tools/browser/manager.go) is the mirror image of the
// rule the agent's own tools resolve through, so both land on one tab set in
// both states. It is resolved ONCE, here, and pinned on the connection
// (bindAttachment) for the life of the attachment. frame.SessionId is
// retained as chatSessionID below for logging, audit and for echoing back on
// outgoing wire frames so the client can correlate responses with its own
// chat session.
//
// Runs on this connection's serial worker in production (dispatchAttach,
// above), never on readLoop's own goroutine — epoch is the value
// state.beginAttach() returned at dispatch time and MUST be threaded through
// unchanged to the bindAttachment call below, so an attach superseded while
// Live().Attach was starting the browser is detected before it mutates
// connection-shared state. Tests that invoke this method directly
// (exercising the gate ladder synchronously) pass 0, matching a fresh
// browserConnState's zero-value attachEpoch.
func (h *BrowserWSHandler) handleAttach(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
	epoch uint64,
) {
	runBrowserConnWorkHook(workKindAttach) // test-only seam; nil in production
	var frame generated.BrowserAttachFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_attach: invalid frame"), dropContext("", viewerID, "attach-invalid"))
		return
	}
	if frame.AgentId == "" || frame.SessionId == "" {
		wc.sendCriticalGen(errorStatus("browser_attach: agent_id and session_id are required"),
			dropContext(frame.SessionId, viewerID, "attach-missing-fields"))
		return
	}

	if prev, prevSession, prevPanel := state.clearAttachment(); prev != nil && prevSession != "" {
		h.detach(prev, prevSession, prevPanel, viewerID, userID)
	}

	// FR-017: the workspace is resolved on the SERVER from the attaching chat
	// session's own meta — the client sends a session id and nothing else, and
	// never gets to name a workspace. See sessionWorkspaceID.
	mgr, outcome := h.agentLoop.BrowserManagerForAgent(
		context.Background(), frame.AgentId, h.sessionWorkspaceID(frame.SessionId))
	if outcome != agent.BrowserResolveOK {
		wc.sendCriticalGen(
			sessionErrorStatus(frame.SessionId, browserResolveReason(outcome, frame.AgentId)),
			dropContext(frame.SessionId, viewerID, "attach-no-manager"))
		return
	}

	chatSessionID := frame.SessionId // context/logging + wire echo ONLY — see doc comment above.
	// Issue #671: resolved ONCE, here, and used for every live-view call this
	// connection makes from now on (pinned via bindAttachment below).
	panelSessionID := mgr.PanelTabSetID(chatSessionID)
	controlledByOther, err := mgr.Live().Attach(panelSessionID, viewerID, func(message string) {
		// ADR-038 finding #2's split-brain fix: the LiveView's underlying tab
		// context died without an explicit browser_detach — e.g. this
		// connection is still holding a reference to a BrowserManager that
		// registerSharedTools has since Shutdown()'d on hot-reload. Tell the
		// client so it can re-attach (which resolves the CURRENT manager via
		// BrowserManagerForAgent) instead of silently watching a frozen frame
		// forever.
		wc.sendCriticalGen(
			sessionErrorStatus(chatSessionID, message),
			dropContext(chatSessionID, viewerID, "status-death"),
		)
	}, func(controlledByOther bool) {
		// ADR-039 UAT BE-1: fan-out from LiveView.takeControl/releaseControl —
		// some OTHER connection on this session just took or released
		// control. state="idle" here (never "controlling"/"released", which
		// describe THIS connection's own action) — see BrowserStatusFrame's
		// enum and BrowserLiveView.tsx's pillConfig, where 'idle' already
		// falls into the same "no human holds the lock" display bucket as
		// 'attached'/'released' by default, so this is a safe no-op display
		// change for a client that hasn't yet started reading
		// controlled_by_other, and the correct signal for one that has.
		cbo := controlledByOther
		wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:              string(generated.WsFrameTypeBrowserStatus),
			State:             "idle",
			SessionId:         &chatSessionID,
			ControlledByOther: &cbo,
			// ControlOnly (B1): this frame's SOLE purpose is to update
			// control-ownership on this OTHER viewer — it carries no real
			// lifecycle/error meaning. Without this flag it's
			// indistinguishable on the wire from a genuine status
			// transition, so the SPA was wiping any displayed error banner
			// and resetting other state on every take/release/detach by a
			// DIFFERENT viewer. Deliberately NOT set on the initial attach
			// response below (state="attached") — that one is a real
			// lifecycle frame that also happens to carry
			// controlled_by_other.
			ControlOnly: boolPtr(true),
		}, dropContext(chatSessionID, viewerID, "control-broadcast"))
	}, func(tabs []browser.Tab, activeIdx int) {
		// ADR-041 D4: the tab set changed (open/close/switch/adopt, or a
		// best-effort title/url update) — broadcast the current tab strip.
		// Fired once immediately on attach (with the CURRENT tab set) and
		// again on every subsequent change; delivered to every attached
		// viewer, including the one that caused the change (unlike
		// ControlSink, a tabs update carries no "who acted" distinction that
		// needs excluding the actor).
		wc.sendCriticalGen(generated.BrowserTabsFrame{
			Type:        string(generated.WsFrameTypeBrowserTabs),
			SessionId:   &chatSessionID,
			ActiveIndex: activeIdx,
			Tabs:        tabsToBrowserTabsWire(tabs),
		}, dropContext(chatSessionID, viewerID, "tabs-broadcast"))
	})
	if err != nil {
		wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_attach failed: %s", err)),
			dropContext(chatSessionID, viewerID, "attach-failed"))
		return
	}

	// Commit under the epoch (FIX WAVE B finding A). Live().Attach above can
	// take seconds — long enough for a newer browser_attach, an explicit
	// browser_detach, or the connection closing to have superseded this one
	// while it ran. If that happened, tear down the LiveView attachment this
	// call just created rather than leaving a viewer (and possibly a control
	// lock) registered against a connection that no longer wants it, and send
	// nothing: an "attached" status frame for an attachment we just threw
	// away would be a lie the SPA would act on.
	if !state.bindAttachment(epoch, mgr, chatSessionID, panelSessionID) {
		slog.Debug("browser-ws: attach superseded before commit — detaching what it built",
			"viewer_id", viewerID, "session_id", chatSessionID, "panel_session_id", panelSessionID)
		h.detach(mgr, chatSessionID, panelSessionID, viewerID, userID)
		return
	}

	cbo := controlledByOther
	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:              string(generated.WsFrameTypeBrowserStatus),
		State:             "attached",
		SessionId:         &chatSessionID,
		ControlledByOther: &cbo,
	}, dropContext(chatSessionID, viewerID, "attach-ok"))

	// Issue #674: register the live-video health observer on the manager. Done
	// here, at attach, because this is the earliest point the gateway knows
	// WHICH manager this connection is talking to — well before a viewer offer
	// creates the CaptureSession the observer ultimately lands on
	// (BrowserManager.EnsureCaptureSession installs it on every session it
	// creates). Idempotent by design: h.onVideoHealth is a method value on the
	// process-wide handler and fans out via the event's own viewer list, so
	// re-registering it on every attach — including a second viewer's — simply
	// overwrites it with an identical observer.
	mgr.SetVideoHealthObserver(h.onVideoHealth)

	// ADR-047: announce WebRTC availability for this fresh attach — the SPA
	// only sends its offer after an available:true state frame (see
	// announceWebRTCAvailability's doc for why omitting this deadlocks the
	// upgrade handshake).
	h.announceWebRTCAvailability(wc, mgr, chatSessionID, viewerID, cfg)
}

// handleInput dispatches a viewer input event, gated by the LiveView's
// control lock (ADR-038 D6). ADR-038 finding #4: LiveViewRegistry.Input
// classifies its failure as benign or real (browser.IsBenignLiveInputError).
// Benign, high-frequency rejections (not attached, not controlling,
// rate-limited) are logged at Debug and NOT surfaced as a status frame — a
// stray mouse-move sent just before/after losing control is an expected,
// frequent occurrence, and a status frame per rejected event would flood a
// client that's still moving the mouse. Real failures (session not attached
// at all, an unknown input kind, an SSRF-/scheme-blocked "navigate" URL
// (ADR-039 D-A2), or — most importantly — a genuine CDP transport error
// meaning the tab crashed or is unreachable) ARE surfaced, throttled to at
// most one IDENTICAL browser_status(error) per minInputErrorInterval so a
// burst of failed dispatches against a dead tab can't flood the connection —
// but a DIFFERENT failure message (e.g. the user's quick retry against a
// different blocked navigate URL) is never suppressed just for landing
// inside that same cooldown window (7-reviewer LOW finding: the user must
// always see WHY their navigate was refused), AND a "navigate"-kind error is
// NEVER throttled at all regardless of message content (B4, 7-reviewer
// finding): unlike a mouse-move stream, a navigate is one submission per
// Enter keypress, and the SPA clears its error banner optimistically on each
// submit — so suppressing even a byte-identical repeat (e.g. resubmitting
// the exact same blocked URL) would leave the user looking at no error at
// all after their retry was refused again.
func (h *BrowserWSHandler) handleInput(wc *browserWSConn, state *browserConnState, viewerID string, data []byte) {
	// Read the attachment ONCE, under attachMu, and use that snapshot for the
	// whole handler: browser_attach now commits from the worker goroutine, so
	// re-reading state.mgr/state.sessionID field-by-field could observe an
	// attach landing mid-handler and mix a nil manager with a live session id.
	mgr, sessionID, panelSessionID := state.attachment()
	if mgr == nil || sessionID == "" {
		return
	}
	var frame generated.BrowserInputFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}

	in := browserInputFrameToLiveInput(frame)

	if err := mgr.Live().Input(panelSessionID, viewerID, in); err != nil {
		if browser.IsBenignLiveInputError(err) {
			slog.Debug("browser-ws: input rejected (benign)", "error", err, "session_id", sessionID)
			// The not-controller repair that lived here is gone: input is
			// never gated on a control lock (see dispatchInput). It could
			// still refuse a human whenever a DIFFERENT viewer was attached
			// and holding control — a second panel or a stale automation
			// session was enough to leave the real user with a dead mouse
			// and keyboard. Only the self-correcting rate limit remains.
			return
		}
		slog.Warn("browser-ws: input dispatch failed", "error", err, "session_id", sessionID)
		message := fmt.Sprintf("browser input failed: %s", err)
		now := time.Now()
		// B4 (7-reviewer finding): a navigate error is one-per-Enter and
		// user-initiated, unlike the high-frequency mouse_move/etc kinds this
		// cooldown exists to tame. The SPA clears its error banner
		// optimistically on every navigate submit, so suppressing a
		// byte-identical repeat here — the same URL rejected twice in a row
		// — would leave the user looking at NO error after resubmitting,
		// even though their submission was refused again. Navigate errors
		// therefore always emit; every other kind keeps the content-aware
		// cooldown.
		throttled := !inputKindIsDiscrete(frame.Kind) &&
			message == state.lastInputErrorMessage &&
			now.Sub(state.lastInputErrorSentAt) < minInputErrorInterval
		if !throttled {
			state.lastInputErrorSentAt = now
			state.lastInputErrorMessage = message
			wc.sendCriticalGen(sessionErrorStatus(sessionID, message),
				dropContext(sessionID, viewerID, "input-error"))
		}
	}
}

// browserInputFrameToLiveInput converts a generated.BrowserInputFrame into
// the engine-level browser.LiveInput dispatchInput expects. Extracted from
// handleInput (ADR-047 / wave-plan W2-A item 4) so the WS input path
// (handleInput, above) and the WebRTC data-channel input path
// (browser_webrtc.go's webrtcInputSink) convert EXACTLY the same way and can
// never drift — both funnel into the SAME
// mgr.Live().Input(<the tab set this connection resolved at attach>, viewerID,
// in) call this function's result feeds (issue #671).
func browserInputFrameToLiveInput(frame generated.BrowserInputFrame) browser.LiveInput {
	in := browser.LiveInput{Kind: frame.Kind}
	if frame.X != nil {
		in.X = *frame.X
	}
	if frame.Y != nil {
		in.Y = *frame.Y
	}
	// HasXY records whether BOTH coordinates were actually present on the
	// wire (ADR-038 finding #5) — LiveInput.X/Y are plain float64, so without
	// this flag "coordinate omitted" would be indistinguishable from "0,0
	// sent explicitly," and buildInputAction would silently dispatch a
	// (0,0)-origin mouse event for a malformed frame instead of rejecting it.
	in.HasXY = frame.X != nil && frame.Y != nil
	if frame.Button != nil {
		in.Button = *frame.Button
	}
	if frame.DeltaX != nil {
		in.DeltaX = *frame.DeltaX
	}
	if frame.DeltaY != nil {
		in.DeltaY = *frame.DeltaY
	}
	if frame.Key != nil {
		in.Key = *frame.Key
	}
	if frame.Code != nil {
		in.Code = *frame.Code
	}
	if frame.KeyCode != nil {
		in.KeyCode = *frame.KeyCode
	}
	if frame.Text != nil {
		in.Text = *frame.Text
	}
	if frame.Url != nil {
		in.URL = *frame.Url
	}
	if frame.Modifiers != nil {
		in.Modifiers = *frame.Modifiers
	}
	// CaptureWidth/CaptureHeight (contracts/components/schemas/BrowserInputFrame.yaml):
	// the intrinsic pixel size of the capture frame the client mapped X/Y
	// into. Absent (older client, or a kind with no coordinates) leaves both
	// at their zero value, which dispatchInput's rescale gate
	// (CaptureWidth > 0 && CaptureHeight > 0) treats as "dispatch X/Y
	// unscaled" — see root-cause doc Fault 3
	// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md).
	if frame.CaptureWidth != nil {
		in.CaptureWidth = *frame.CaptureWidth
	}
	if frame.CaptureHeight != nil {
		in.CaptureHeight = *frame.CaptureHeight
	}
	return in
}

// inputKindIsDiscrete reports whether an input kind is a one-shot action
// (navigate / navigate_back / reload) rather than high-frequency pointer input
// (mouse_move/wheel/key). Discrete kinds are exempt from the repeated-error
// cooldown (minInputErrorInterval) so a refused navigate/back/reload always
// surfaces its reason immediately, exactly as "navigate" did before
// navigate_back/reload were added.
func inputKindIsDiscrete(kind string) bool {
	switch kind {
	case "navigate", "navigate_back", "reload":
		return true
	}
	return false
}

// handleControl processes a take/release control request (ADR-038 D6).
// take is refused (audited as deny) when tools.browser.take_control_enabled
// is off, or when another viewer already controls the session — v1 is
// cooperative/first-come, no preemption. Every take/release outcome is
// audit-logged per the ADR's "take/release control is audit-logged"
// requirement.
func (h *BrowserWSHandler) handleControl(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
) {
	// One snapshot under attachMu for the whole handler — see handleInput.
	//
	// chatSessionID is echoed on outgoing frames / audit entries; every call
	// into mgr.Live() below uses panelSessionID, the tab set this connection
	// resolved at attach (issue #671) — see handleAttach's doc comment. The
	// control lock therefore runs on the SAME owner the panel is showing and
	// the agent's tools consult, never split across two tab sets.
	mgr, chatSessionID, panelSessionID := state.attachment()
	if mgr == nil || chatSessionID == "" {
		wc.sendCriticalGen(errorStatus("browser_control: attach before requesting control"),
			dropContext("", viewerID, "control-not-attached"))
		return
	}
	var frame generated.BrowserControlFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_control: invalid frame"),
			dropContext(chatSessionID, viewerID, "control-invalid"))
		return
	}

	switch frame.Action {
	case "take":
		if !cfg.Tools.Browser.TakeControlEnabled {
			h.auditControl(userID, chatSessionID, viewerID, audit.SeverityWarn, "take_control_disabled")
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "take-control is disabled by the operator"),
				dropContext(chatSessionID, viewerID, "control-take-disabled"))
			return
		}
		if !mgr.Live().TakeControl(panelSessionID, viewerID) {
			h.auditControl(userID, chatSessionID, viewerID, audit.SeverityWarn, "already_controlled")
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "another viewer already controls this browser"),
				dropContext(chatSessionID, viewerID, "control-take-denied"))
			return
		}
		h.auditControl(userID, chatSessionID, viewerID, audit.SeverityInfo, "take")
		controller := userID
		wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:       string(generated.WsFrameTypeBrowserStatus),
			State:      "controlling",
			SessionId:  &chatSessionID,
			Controller: &controller,
		}, dropContext(chatSessionID, viewerID, "control-take-ok"))
	case "release":
		mgr.Live().ReleaseControl(panelSessionID, viewerID)
		h.auditRelease(userID, chatSessionID, viewerID)
		wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:      string(generated.WsFrameTypeBrowserStatus),
			State:     "released",
			SessionId: &chatSessionID,
		}, dropContext(chatSessionID, viewerID, "control-release-ok"))
	default:
		wc.sendCriticalGen(errorStatus(fmt.Sprintf("browser_control: unknown action %q", frame.Action)),
			dropContext(chatSessionID, viewerID, "control-unknown-action"))
	}
}

// handleTabAction processes a browser_tab_action frame (ADR-041 D3/D4):
// switch/close/open a tab in the attached session's browsing context (always
// the WORKSPACE-OWNED tab set — the operator's own tabs, same convention as
// handleControl/handleInput). The resulting browser_tabs broadcast to every
// attached viewer (including this one) is delivered automatically via the
// BrowserManager.tabsChanged → LiveView.onTabsChanged → TabsSink fan-out
// wired at attach time (see handleAttach's onTabs callback) — this handler's
// only DIRECT response to the caller is a browser_status(error) frame on
// failure (an out-of-range index, no live session attached, an unknown
// action, or the F3 control-lock rejection below). Unlike browser_control's
// take/release, a tab action has no "who acted" distinction that needs
// excluding the actor from the broadcast, so there is no direct success
// response either.
//
// F3 control-lock gate (7-reviewer MAJOR, ADR-041 fix wave): honored only
// when the acting viewer currently holds the control lock, OR nobody holds
// it (idle) — mirroring browser_input's dispatchInput gate exactly, via the
// SAME accessor (LiveViewRegistry.Controller, the pure-read counterpart of
// the getController() dispatchInput/controlledResult consult). Before this
// fix, any merely-attached (non-controlling) viewer — a second panel or a
// pop-out — could switch/close/open tabs with no check at all, yanking the
// active tab out from under whoever DOES hold control (human or, per ADR-040,
// the agent's own turn) or fighting the agent's own tab tools mid-flight.
// The agent's own tab tools (browser_list_tabs/switch_tab/close_tab,
// tabs.go) are UNAFFECTED — they call BrowserManager.SwitchTab/CloseTab/
// OpenTab directly, never through this WS handler.
func (h *BrowserWSHandler) handleTabAction(wc *browserWSConn, state *browserConnState, viewerID string, data []byte) {
	// One snapshot under attachMu for the whole handler — see handleInput.
	mgr, chatSessionID, panelSessionID := state.attachment()
	if mgr == nil || chatSessionID == "" {
		wc.sendCriticalGen(errorStatus("browser_tab_action: attach before managing tabs"),
			dropContext("", viewerID, "tab-action-not-attached"))
		return
	}
	var frame generated.BrowserTabActionFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_tab_action: invalid frame"),
			dropContext(chatSessionID, viewerID, "tab-action-invalid"))
		return
	}

	// chatSessionID is echoed on outgoing error frames; every call into mgr
	// below uses panelSessionID, the tab set this connection resolved at
	// attach (ADR-038 finding #1 / ADR-041, amended by issue #671) — see
	// handleAttach's doc comment.

	if controller := mgr.Live().Controller(panelSessionID); controller != "" && controller != viewerID {
		wc.sendCriticalGen(
			sessionErrorStatus(chatSessionID, "another viewer is driving — take control first to manage tabs"),
			dropContext(chatSessionID, viewerID, "tab-action-not-controller"),
		)
		return
	}

	switch frame.Action {
	case "switch":
		if frame.Index == nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "browser_tab_action: index is required for switch"),
				dropContext(chatSessionID, viewerID, "tab-switch-missing-index"))
			return
		}
		if _, err := mgr.SwitchTab(panelSessionID, *frame.Index); err != nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_tab_action: %s", err)),
				dropContext(chatSessionID, viewerID, "tab-switch-failed"))
		}
	case "close":
		if frame.Index == nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "browser_tab_action: index is required for close"),
				dropContext(chatSessionID, viewerID, "tab-close-missing-index"))
			return
		}
		if _, _, err := mgr.CloseTab(panelSessionID, *frame.Index); err != nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_tab_action: %s", err)),
				dropContext(chatSessionID, viewerID, "tab-close-failed"))
		}
	case "open":
		if _, err := mgr.OpenTab(panelSessionID); err != nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_tab_action: %s", err)),
				dropContext(chatSessionID, viewerID, "tab-open-failed"))
		}
	default:
		wc.sendCriticalGen(errorStatus(fmt.Sprintf("browser_tab_action: unknown action %q", frame.Action)),
			dropContext(chatSessionID, viewerID, "tab-action-unknown"))
	}
}

// browserTabWire is the exact anonymous shape oapi-codegen generated for
// generated.BrowserTabsFrame.Tabs' item type — same field names, types,
// json tags, and ORDER (Go struct type identity considers field sequence).
// A type ALIAS (`=`), not a new named type, so it and the inlined anonymous
// struct field on BrowserTabsFrame remain the identical type — this is the
// SAME generated wire shape, just given a name so tabsToBrowserTabsWire
// doesn't have to respell it three times. Mirrors
// rest_executor_preview.go's executorPreviewDroppedArg convention.
type browserTabWire = struct { // not-wire-format: type alias of generated.BrowserTabsFrame.Tabs' inlined element shape, not a new wire type
	Active *bool   `json:"active,omitempty"`
	Index  int     `json:"index"`
	Title  *string `json:"title,omitempty"`
	Url    *string `json:"url,omitempty"`
}

// tabsToBrowserTabsWire converts a []browser.Tab snapshot to []browserTabWire
// (generated.BrowserTabsFrame.Tabs' element type — see browserTabWire's doc
// comment). A plain []browser.Tab is NOT directly assignable to it:
// browser.Tab's fields are non-pointer and untagged, and its URL field is
// spelled "URL" where the wire type spells it "Url" — every element must be
// converted field-by-field, pointer-boxing Active/Title/Url per the wire
// schema's omitempty semantics.
func tabsToBrowserTabsWire(tabs []browser.Tab) []browserTabWire {
	out := make([]browserTabWire, len(tabs))
	for i, t := range tabs {
		active := t.Active
		title := t.Title
		url := t.URL
		out[i] = browserTabWire{Active: &active, Index: t.Index, Title: &title, Url: &url}
	}
	return out
}

// handleDetach unbinds this connection from its current live view.
func (h *BrowserWSHandler) handleDetach(wc *browserWSConn, state *browserConnState, viewerID, userID string) {
	// Unconditional, and BEFORE the clear — the same discipline
	// detachWebRTCViewer applies with invalidateWebRTCOffer, for the same
	// reason: a browser_attach dispatched onto the worker may still be
	// inside Live().Attach right now with nothing committed yet, and this
	// explicit detach must make its eventual commit fail so it tears down
	// what it built instead of attaching a view the user just closed.
	state.invalidateAttach()
	mgr, chatSessionID, panelSessionID := state.clearAttachment()
	if mgr == nil || chatSessionID == "" {
		return
	}
	h.detach(mgr, chatSessionID, panelSessionID, viewerID, userID)
	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:      string(generated.WsFrameTypeBrowserStatus),
		State:     "detached",
		SessionId: &chatSessionID,
	}, dropContext(chatSessionID, viewerID, "detach-ok"))
}

// detach releases viewerID from the live view (stopping the death watch on
// its tab if it was the last viewer) and audits a control release if this
// viewer was the controller — used both by explicit browser_detach and by
// readLoop's disconnect cleanup, so a dropped connection is indistinguishable
// from a clean detach for audit and resource-cleanup purposes. chatSessionID
// is used only for the audit entry / log context; panelSessionID is the
// RESOLVED tab set the attachment was bound to (issue #671), and detaching
// must run on exactly the one Attach ran on — otherwise a departing viewer
// leaves its control lock dangling on a set nobody releases.
func (h *BrowserWSHandler) detach(
	mgr *browser.BrowserManager, chatSessionID, panelSessionID, viewerID, userID string,
) {
	wasController := mgr.Live().Controller(panelSessionID) == viewerID
	mgr.Live().Detach(panelSessionID, viewerID)
	if wasController {
		h.auditRelease(userID, chatSessionID, viewerID)
	}
}

// auditControl logs a take-control attempt (allowed or denied). ADR-038
// finding #6b: audit.EventBrowserLiveControlTaken's doc comment claims
// INFO/WARN severity, but audit.Entry (the al.Log wire shape this used to go
// through) has no Severity field at all — the claim was never actually true
// on the wire. audit.Emit is the shape that DOES carry Severity, so this now
// goes through Emit with the severity the comment always claimed: INFO for
// an allowed take, WARN for a denied one — appropriate for a remote-control
// security surface.
func (h *BrowserWSHandler) auditControl(userID, sessionID, viewerID string, sev audit.Severity, reason string) {
	al := h.agentLoop.AuditLogger()
	if al == nil {
		return
	}
	audit.Emit(context.Background(), al, audit.EventBrowserLiveControlTaken, sev, map[string]any{
		"session_id": sessionID,
		"user":       userID,
		"viewer_id":  viewerID,
		"reason":     reason,
	})
}

// auditRelease logs a control release (explicit or implicit via detach).
// Always INFO (see auditControl's doc comment) — a release is never itself a
// denied action.
func (h *BrowserWSHandler) auditRelease(userID, sessionID, viewerID string) {
	al := h.agentLoop.AuditLogger()
	if al == nil {
		return
	}
	audit.Emit(context.Background(), al, audit.EventBrowserLiveControlReleased, audit.SeverityInfo, map[string]any{
		"session_id": sessionID,
		"user":       userID,
		"viewer_id":  viewerID,
	})
}

// errorStatus builds a session-less browser_status(error) frame.
func errorStatus(message string) generated.BrowserStatusFrame {
	return generated.BrowserStatusFrame{
		Type:    string(generated.WsFrameTypeBrowserStatus),
		State:   "error",
		Message: &message,
	}
}

// sessionErrorStatus builds a browser_status(error) frame scoped to a session.
func sessionErrorStatus(sessionID, message string) generated.BrowserStatusFrame {
	return generated.BrowserStatusFrame{
		Type:      string(generated.WsFrameTypeBrowserStatus),
		State:     "error",
		SessionId: &sessionID,
		Message:   &message,
	}
}

// handleViewport resizes the captured tab to the viewer's panel geometry and
// then forces the WebRTC encoder to rebuild its stream at that new geometry.
//
// Operator UAT 2026-07-31: the tab was pinned to a hardcoded 1280x720 while
// the docked panel is an arbitrary resizable shape (measured ~890x1010,
// portrait). `object-fit: contain` preserves the SOURCE aspect, so the page
// could fill only one dimension and the rest of the panel was letterboxed
// black — unusable for interaction. device_scale_factor addresses the same
// report's second half, blur, by rendering above DPR 1.
//
// ORDER MATTERS and is the whole subtlety here: SetViewport changes only the
// TAB. A capture already in flight keeps its old geometry, because tabCapture
// constraints are pinned per stream (encoder.js pins minWidth==maxWidth at
// stream creation) and cannot be renegotiated on a running track. Recapture()
// tears that stream down and re-runs captureActiveTabStream, which re-reads
// chrome.tabs.get() — so it must come AFTER the resize, or the encoder
// rebuilds at the OLD size and the resize appears to do nothing.
//
// Best-effort by design: a viewport frame is a display optimisation, never
// load-bearing for the session. A failure is logged and reported to this
// viewer, but never tears down the connection — a browser that cannot be
// resized is still a browser that works.
//
// Runs on this connection's serial worker in production (dispatchViewport),
// never on readLoop's own goroutine: SetViewport holds LiveView.viewportMu
// across several CDP round trips by design and was MEASURED at 6.95s against
// a busy page, which is far too long to sit inside a ReadMessage call (see
// browserConnWorkQueue). The minViewportInterval floor lives in
// dispatchViewport, not here.
func (h *BrowserWSHandler) handleViewport(wc *browserWSConn, state *browserConnState, viewerID string, data []byte) {
	runBrowserConnWorkHook(workKindViewport) // test-only seam; nil in production
	var frame generated.BrowserViewportFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		slog.Warn("browser-ws: dropping invalid browser_viewport frame", "error", err, "viewer_id", viewerID)
		return
	}
	// One snapshot under attachMu for the whole handler — see handleInput.
	// This ALSO makes a viewport job that was queued before an intervening
	// browser_detach (or a connection close) a no-op rather than a resize of
	// a tab this connection no longer watches.
	mgr, sessionID, panelSessionID := state.attachment()
	if mgr == nil || sessionID == "" {
		return
	}

	// Control gate, mirroring handleTabAction's F3 check in this same file.
	// The resolved tab set (issue #671) is ONE tab shared by every attached
	// viewer on it AND by the agent's own browser_* tools. Without this, any merely-attached
	// viewer — a second panel, a pop-out — could resize the tab out from under
	// whoever holds control, or under an agent tool call mid-flight. A resize
	// is if anything more disruptive than the tab switch that gate already
	// covers: it reflows responsive layout and shifts coordinate-based element
	// targeting for whoever IS driving. Uncontrolled (controller == "") stays
	// permitted, so a lone viewer sizing the panel before taking the wheel
	// still works.
	//
	// FIX WAVE B finding B — the refusal is now VISIBLE. This branch used to
	// be slog.Debug and nothing else, so the second viewer sat watching a
	// mis-shaped picture (measured: the tab stayed at the FIRST viewer's
	// size) with no explanation anywhere in the product. That silence is
	// especially indefensible next to the deliberate policy split beside it:
	// LiveView.dispatchInput has NO control gate at all (operator directive
	// 2026-08-03, "a browser that refuses input is not a browser"), so the
	// very same viewer's clicks and keystrokes DO land — only their resize is
	// refused, and until now refused invisibly. Telling them why, and what to
	// do about it, is the whole fix; the gate itself is unchanged and
	// deliberately kept.
	//
	// Throttled on identical content (shouldSendViewportRefusal) because a
	// resize drag emits one frame per debounce interval for as long as the
	// drag lasts and every one of them is refused the same way — the same
	// flood handleInput's cooldown exists to prevent.
	if controller := mgr.Live().Controller(panelSessionID); controller != "" && controller != viewerID {
		slog.Debug("browser-ws: refusing viewport from a non-controlling viewer",
			"viewer_id", viewerID, "controller", controller)
		const message = "another viewer is driving this browser, so the shared tab keeps their window size — " +
			"your clicks and typing still work, and the picture will fit your panel once they release control"
		if state.shouldSendViewportRefusal(message, time.Now()) {
			wc.sendCriticalGen(
				sessionErrorStatus(sessionID, message),
				dropContext(sessionID, viewerID, "viewport-not-controller"),
			)
		}
		return
	}

	dsf := 1.0
	if frame.DeviceScaleFactor != nil {
		dsf = float64(*frame.DeviceScaleFactor)
	}
	// F10 fix: clamp to the contract range BEFORE dsf is used for ANYTHING
	// below — recorded on the capture session, remembered on the connection,
	// or handed to SetViewport. See maxDeviceScaleFactor's doc comment for
	// why this has to live here rather than relying on SetViewport's own
	// (later, CDP-call-shaped) range check alone.
	if dsf < 1 {
		dsf = 1
	} else if dsf > maxDeviceScaleFactor {
		dsf = maxDeviceScaleFactor
	}

	// Record the viewer's deviceScaleFactor on the capture session BEFORE the
	// CDP resize attempt. The two are independent: the encoder captures via
	// the extension's tabs API and needs no gateway-side CDP handle, so a
	// failed resize (e.g. "get window for target: context canceled" after a
	// managed-Chrome relaunch under a still-attached panel — observed live
	// 2026-08-12) must not swallow the scale. Before this ordering the blur
	// fix's trigger sat unreachable behind exactly that failure, and Retina
	// viewers stayed on 1x capture whenever the resize path was broken.
	//
	// F2 fix: remembered on the connection UNCONDITIONALLY, not only when an
	// attachment already exists — a cold-opened panel's first (and often
	// only) viewport frame routinely arrives before browser_webrtc_offer has
	// finished negotiating, so peekWebRTCAttachment() is nil here and the
	// direct SetCaptureScale call below would otherwise be the only chance
	// this scale ever gets applied. See pendingCaptureScale's doc comment.
	state.rememberViewportScale(dsf)
	if att := state.peekWebRTCAttachment(); att != nil && att.capture != nil {
		att.capture.SetCaptureScale(dsf)
	}

	applied, err := mgr.Live().SetViewport(panelSessionID, frame.Width, frame.Height, dsf)
	if err != nil {
		slog.Warn("browser-ws: viewport resize failed",
			"error", err, "viewer_id", viewerID, "width", frame.Width, "height", frame.Height)
		// Still push a recapture so the scale (and the encoder's own
		// tabs.get-derived size) take effect — the capture pipeline is
		// healthy even when the CDP resize handle is not.
		if att := state.peekWebRTCAttachment(); att != nil && att.capture != nil {
			att.capture.Recapture()
		}
		wc.sendCriticalGen(
			errorStatus("could not resize the browser viewport"),
			dropContext(sessionID, viewerID, "viewport-failed"),
		)
		return
	}
	if !applied {
		// No live view bound yet (panel opened before the capture exists).
		// Not an error: the next attach starts the tab at whatever size the
		// browser was launched with, and the SPA re-sends on attach.
		slog.Debug("browser-ws: viewport frame with no live view bound — ignoring", "viewer_id", viewerID)
		return
	}

	slog.Debug("browser-ws: viewport applied",
		"viewer_id", viewerID, "width", frame.Width, "height", frame.Height, "device_scale_factor", dsf)

	// Rebuild the WebRTC capture at the new geometry. peek (not take) — this
	// must not detach the viewer as a side effect of a resize. Thread the
	// CDP-verified CSS viewport SetViewport just cached through to
	// RecaptureAt (follow-up to
	// docs/internal/browser-viewport-input-rootcause-2026-07-31.md, measured
	// 2026-07-31: a recapture racing this very resize otherwise pins the
	// WebRTC stream to a stale tab size, because the encoder's own
	// chrome.tabs.get-based resolution lags the OS window reflow). Falls
	// back to the no-hint Recapture() if the cache came back empty (e.g.
	// SetViewport's own read-back was invalidated).
	if att := state.peekWebRTCAttachment(); att != nil && att.capture != nil {
		if w, h, ok := mgr.Live().CSSViewport(panelSessionID); ok {
			// scale already recorded above, before the resize attempt
			att.capture.RecaptureAt(w, h)
		} else {
			att.capture.Recapture()
		}
	}
}

// sessionWorkspaceID reads the workspace a chat session is bound to, from that
// session's OWN meta on disk. It is ADR-075 FR-017's whole mechanism, and the
// reason no wire field was added for the workspace (FR-016): the client tells
// the gateway which chat it is looking at, and the gateway — not the client —
// decides which workspace that is.
//
// The distinction matters more than it looks. A workspace's browser holds that
// workspace's live logins, so a client-supplied workspace_id would be a request
// to act as whoever that workspace is signed in as, honoured on the client's
// say-so. Reading it server-side means the only thing a caller can influence is
// WHICH OF ITS OWN chat sessions it names, and even that is not taken on trust:
// the id returned here is a PREFERENCE, and
// browser.ResolveBrowsingKeyForAgent accepts it only when the agent really is
// on that workspace's team (FindForAgentPreferring must return the same id).
// A session naming a workspace the agent is not on falls through to the plain
// membership ladder, exactly as if it had named none.
//
// Returns "" — never an error — for every "no answer" case: no session id, no
// store that owns it, unreadable meta, or a session that simply predates
// workspace tagging. "" is not a failure here; it is the honest input to the
// ladder, which then resolves from the agent's own membership and refuses
// under FR-033 if that is ambiguous. Degrading to "" can only ever make the
// resolution MORE conservative, never less.
func (h *BrowserWSHandler) sessionWorkspaceID(chatSessionID string) string {
	if h == nil || h.agentLoop == nil || chatSessionID == "" {
		return ""
	}
	store := h.agentLoop.ResolveSessionStore(chatSessionID)
	if store == nil {
		return ""
	}
	meta, err := store.GetMeta(chatSessionID)
	if err != nil || meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.WorkspaceID)
}

// browserNoWorkspaceRemedy is the ONE sentence-ending clause every surface that
// reports "this agent has no browser of its own" must end with, word for word
// (ADR-075 FR-008a, round-2 MIN-107). It is quoted verbatim out of
// browser.ErrNoBrowsingContext, which is the authority: the panel, the tool
// error an agent reads back, and the boot log must not each invent their own
// phrasing of the same fix, because an operator who tries the panel's wording
// and then reads the log has no way to tell whether they are being told to do
// one thing or two.
//
// Keep this in sync with browser.ErrNoBrowsingContext by COPYING its text, and
// never by paraphrase; TestGateway_ResolveOutcomes_AreDistinct asserts the
// panel's no-workspace reason contains this exact substring AND that the
// substring is really part of ErrNoBrowsingContext's own message, so a
// re-wording on either side fails rather than silently diverging.
const browserNoWorkspaceRemedy = "add this agent to a workspace's team, " +
	"or run the request in a workspace chat"

// browserResolveReason renders an agent.BrowserResolveOutcome as the sentence a
// panel shows. The three failure reasons are DIFFERENT operator problems and
// were indistinguishable before ADR-075 FR-008a — every one of them reported
// "browser tools may not be registered for this agent", which is actionable
// advice for exactly one of them and a wild goose chase for the other two.
func browserResolveReason(outcome agent.BrowserResolveOutcome, agentID string) string {
	switch outcome {
	case agent.BrowserResolveNoWorkspace:
		return fmt.Sprintf(
			"agent %q is not on any workspace's team, so it has no browser of its own — %s",
			agentID, browserNoWorkspaceRemedy)
	case agent.BrowserResolveAmbiguous:
		return fmt.Sprintf(
			"agent %q is on more than one workspace's team, so which workspace's browser — and "+
				"whose live logins — this panel would show is ambiguous; it is refused rather "+
				"than guessed. Open this panel from a chat that belongs to the workspace you "+
				"mean", agentID)
	case agent.BrowserResolveLaunchFailed:
		return fmt.Sprintf(
			"the browser for agent %q could not be started — ensure Chromium/Chrome is installed "+
				"or set tools.browser.cdp_url", agentID)
	case agent.BrowserResolveNotRegistered:
		return fmt.Sprintf("browser tools are not registered for agent %q", agentID)
	case agent.BrowserResolveOK:
		return ""
	}
	return fmt.Sprintf("the browser for agent %q could not be resolved", agentID)
}
