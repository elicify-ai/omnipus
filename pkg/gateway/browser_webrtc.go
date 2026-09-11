// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	pion "github.com/pion/webrtc/v4"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// browser_webrtc.go implements ADR-047 / wave-plan W2-A: the WebRTC media
// path for the live-browser view (browser_ws.go). Two pieces:
//
//  1. Viewer signaling on the EXISTING /api/v1/browser/ws socket:
//     handleWebRTCOffer, dispatched from browser_ws.go's readLoop on a
//     browser_webrtc_offer frame (ADR-047 D4: signaling rides the existing
//     authenticated WS, contract-first, non-trickle).
//  2. The loopback-only /api/v1/browser/capture-ingest WS
//     (captureIngestWSHandler): the gateway-owned encoder page's ingest leg
//     (ADR-047 D6: token-authorized, never a URL param).
//
// ADR-047 D3 ("WebRTC failing must never take the JPEG fallback down with
// it") is VOID as of ADR-061: the JPEG CDP screencast this once protected —
// and the CaptureSession.ReconcileScreencast pause/resume coordination that
// used to run alongside every failure path here — were removed entirely.
// WebRTC is now the ONLY live-browser video path. EVERY failure path here
// still degrades to a browser_webrtc_state frame (available/active=false +
// a reason) — that discipline survives unchanged — but there is no fallback
// tier left for it to protect: a WebRTC failure now means the panel
// genuinely has no video until the next successful offer, and the SPA is
// expected to show that state honestly rather than silently substituting
// another stream. See ADR-061 for the removal rationale.
//
// Fix-wave amendments (ADR-048 default-context capture): a failed capture
// Start() now tears the session down (fix 1) instead of leaving a sticky
// broken session; a failed ingest offer now signals the encoder and closes
// the connection (fix 2); an encoder-liveness watchdog stops a
// wedged/silent capture session and pushes the state change to attached
// viewers immediately, on ANY stop cause, rather than making them wait for
// their own ICE timeout (fix 3); capability classification (fix 4, see
// capability.go's CaptureVideoCapability) now accounts for the capture
// extension being seeded and shared-default-context capture being enabled;
// relay log lines are level-classified instead of always Debug (fix 5); the
// multi-agent capture-target gap (ADR-048 condition 2) is fenced (fix 6);
// data-channel input dispatch errors are surfaced to the driving viewer
// (fix 7); a failed viewer offer now also closes the relay-side viewer PC
// (fix 8); and the WebRTCEnabled/lite_build/not_capable gate ladder is a
// single shared classifier (fix 9, webrtcUnavailableReason).

// captureRegistry locates each active panel capture by its ingest token and
// retains the workspace browsing key used for cross-browser conflict checks.
// Several panel captures can belong to one workspace browser. The viewer
// handler registers captures; the ingest handler resolves them from hello tokens.
type captureRegistry struct {
	mu       sync.Mutex
	sessions map[*browser.CaptureSession]string // value is BrowsingKey.String()
}

func newCaptureRegistry() *captureRegistry {
	return &captureRegistry{sessions: make(map[*browser.CaptureSession]string)}
}

func (r *captureRegistry) set(browsingKey string, cs *browser.CaptureSession) {
	r.mu.Lock()
	r.sessions[cs] = browsingKey
	r.mu.Unlock()
}

// removeIfCurrent removes only this capture in its original workspace.
// Cleanup cannot remove another panel or a replacement capture.
func (r *captureRegistry) removeIfCurrent(browsingKey string, cs *browser.CaptureSession) {
	r.mu.Lock()
	if key, ok := r.sessions[cs]; ok && key == browsingKey {
		delete(r.sessions, cs)
	}
	r.mu.Unlock()
}

// otherSessions returns a snapshot of every registered capture session
// belonging to a DIFFERENT browser than exclude — i.e. a different workspace,
// a different Chrome. The ADR-048 condition-2 fence uses it to detect a
// genuinely conflicting (actively-viewed) capture and to supersede viewerless
// leftovers. Stopped sessions are removed from the registry by their onStopped
// hook (see ensureCaptureSession), so entries here are live-or-gracing
// sessions only.
//
// exclude is a browsing key. Passing an agent id here would exclude nothing
// and make every caller its own conflict — see the type's doc comment.
func (r *captureRegistry) otherSessions(exclude string) map[*browser.CaptureSession]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[*browser.CaptureSession]string, len(r.sessions))
	for cs, key := range r.sessions {
		if key != exclude {
			out[cs] = key
		}
	}
	return out
}

// findByToken scans active sessions for one whose minted token matches
// candidateHex (ValidateToken does the actual constant-time compare per
// candidate — wave-plan W2-A item 3). Returns ("", nil) if no session
// matches. A snapshot is taken under the lock and validated outside it so a
// slow/attacker-controlled candidate string can't hold the registry lock.
//
// The first return is the BROWSING KEY the matched session belongs to, not an
// agent id (FR-016a). The capture-ingest handler consumes it purely as log
// context, and a browsing key is the more honest label there anyway: the
// encoder page belongs to one workspace's Chrome, and no single agent owns it.
func (r *captureRegistry) findByToken(
	candidateHex string,
) (browsingKey string, cs *browser.CaptureSession) {
	r.mu.Lock()
	snapshot := make(map[*browser.CaptureSession]string, len(r.sessions))
	for k, v := range r.sessions {
		snapshot[k] = v
	}
	r.mu.Unlock()
	for s, key := range snapshot {
		if s.ValidateToken(candidateHex) {
			return key, s
		}
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// Viewer signaling (existing /api/v1/browser/ws)
// ---------------------------------------------------------------------------

// webrtcAttachment holds the identity of one connection's attached WebRTC
// viewer (browserConnState.webrtc, browser_ws.go). A single nullable
// pointer rather than a (agentID string, capture *browser.CaptureSession)
// field pair (fix-wave TYPE simplification finding): the two fields were
// always set and cleared together, so the pair could represent an illegal
// half-set state (e.g. a non-empty agentID with a nil capture) the type
// system did nothing to prevent. Both fields non-empty/non-nil together, or
// the pointer itself is nil — structurally enforced.
//
// handle identifies the SPECIFIC HandleViewerOffer attempt that produced this
// attachment (browser.CaptureSession.HandleViewerOffer's return value, the
// same *browser.ViewerAttachHandle handleWebRTCOffer already threads through
// its own failure/superseded-before-commit cleanup branches via
// CleanupViewerOffer). detachWebRTCViewer MUST tear this attachment down via
// CleanupViewerOffer(handle), NOT a bare viewerID-keyed
// Relay().CloseViewer/RemoveViewer pair (fix-wave finding: detachWebRTCViewer
// was the one teardown path still bypassing the identity check the offer
// path already uses) — otherwise a detach or connection-close racing a
// second, still-negotiating offer for the SAME viewerID (an ICE-restart
// reconnect: commitWebRTCAttachment/epoch only guards this connection's
// single-slot webrtc field, not CaptureSession.viewers or the relay's own
// viewer registry, both of which a second HandleViewerOffer call mutates
// BEFORE it commits) would kill the newer, live PeerConnection instead of
// the one this attachment actually owns.
type webrtcAttachment struct {
	agentID string
	capture *browser.CaptureSession
	handle  *browser.ViewerAttachHandle
}

// webrtcViewerConn retains the original attachment and capture for scoped
// frame and video-health publication. Input errors retain their own immutable
// contextual route. Successful offers register entries; detach removes them.
type webrtcViewerConn struct {
	wc            *browserWSConn
	sessionID     string
	attachmentCtx context.Context
	capture       *browser.CaptureSession
	captureID     string
}

// dispatchWebRTCOffer launches handleWebRTCOffer on its own goroutine so a
// slow/CDP-bound offer can never block readLoop's ReadMessage loop (FIX WAVE
// A finding 1): browser_ws.go's readLoop is gorilla/websocket's SOLE reader
// for this connection, and gorilla only invokes the registered PongHandler
// (which refreshes the connection's 60s read deadline) from INSIDE a
// ReadMessage call. handleWebRTCOffer's own documented, BOUNDED steps already
// sum to roughly the same order as that deadline (cs.Start's up-to-20s
// captureStartTimeout + up-to-5s bringToFrontTimeout, HandleViewerOffer's
// up-to-15s waitForTracksTimeout) before ever counting a genuinely unbounded
// CDP call underneath — running it inline (as before this fix) meant no Pong
// could ever be processed while it ran, so the 60s deadline elapsed
// unconditionally regardless of how many Pongs the peer answered, and
// readLoop's own cleanup defer tore down this WebRTC attempt right along
// with the connection's OWN session-tracking attachment (at the time this
// fix landed, that meant the separate JPEG browser_screencast path too —
// ADR-047 D3, since void per ADR-061's removal of that path entirely).
//
// state.beginWebRTCOffer() is called HERE, synchronously, still on
// readLoop's own goroutine, BEFORE the goroutine below is spawned — see that
// method's doc comment (browser_ws.go) for why the epoch bump must happen at
// dispatch time rather than inside the (unpredictably-scheduled) goroutine,
// to keep epoch ordering aligned with the order frames actually arrived in.
//
// The spawned goroutine is tracked via h.activeConns — the SAME WaitGroup
// ServeHTTP itself holds an outstanding Add for over this connection's whole
// lifetime — so Wait() (used by every test in this package via
// t.Cleanup(handler.Wait)) continues to block until this offer has fully
// finished negotiating or been superseded, exactly as it already does for
// the connection's own ServeHTTP goroutine. Add() happening here, on
// readLoop's still-live goroutine, strictly before ServeHTTP's own Done()
// could possibly fire is what keeps that safe (no window where Wait() could
// observe a zero count prematurely).
func (h *BrowserWSHandler) dispatchWebRTCOffer(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
) {
	epoch := state.beginWebRTCOffer()
	h.activeConns.Add(1)
	go func() {
		defer h.activeConns.Done()
		h.handleWebRTCOffer(wc, state, viewerID, userID, data, cfg, epoch)
	}()
}

// handleWebRTCOffer negotiates only for the original committed attachment
// captured synchronously by dispatchWebRTCOffer. The server request epoch
// orders attempts; wire offer IDs are echoed without assuming their order.
func (h *BrowserWSHandler) handleWebRTCOffer(
	wc *browserWSConn, state *browserConnState, viewerID, userID string,
	data []byte, cfg *config.Config, epoch uint64,
) {
	request, ok := state.webRTCOfferRequest(epoch)
	if !ok {
		return
	}
	defer state.finishWebRTCOffer(epoch)
	currentOrigin := func() bool { return state.webRTCRequestOriginCurrent(request) }
	send := func(frame any, sessionID, kind string) bool {
		return wc.sendCriticalScopedGen(frame, dropContext(sessionID, viewerID, kind), request.attachment.ctx, currentOrigin)
	}
	var frame generated.BrowserWebRTCOfferFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		send(errorStatus("browser_webrtc_offer: invalid frame"), "", "webrtc-offer-invalid")
		return
	}
	sessID := frame.SessionId
	if frame.AgentId == "" || frame.Sdp == "" {
		send(sessionErrorStatus(sessID, "browser_webrtc_offer: agent_id and sdp are required"), sessID, "webrtc-offer-missing-fields")
		return
	}
	const maxSafeInteger = uint64(1<<53 - 1)
	validID := func(value *int) bool { return value != nil && *value > 0 && uint64(*value) <= maxSafeInteger }
	if !validID(frame.OfferId) || (frame.CaptureId == nil) != (frame.CaptureGeneration == nil) ||
		(frame.CaptureId != nil && (*frame.CaptureId == "" || !validID(frame.CaptureGeneration))) {
		send(operationErrorStatus(sessID, "browser_webrtc_offer: valid offer_id and paired capture claims are required"), sessID, "webrtc-offer-invalid-claims")
		return
	}
	sendState := func(available, active, audio bool, reason string, cause error) {
		status := generated.BrowserWebRTCStateFrame{Type: string(generated.WsFrameTypeBrowserWebrtcState), SessionId: &sessID, Available: available}
		if active {
			status.Active = boolPtr(true)
		}
		if audio {
			status.HasAudio = boolPtr(true)
		}
		if reason != "" {
			status.Reason = &reason
		}
		if detail := webrtcReasonDetail(cause); detail != "" {
			status.ReasonDetail = &detail
		}
		send(status, sessID, "webrtc-state:"+reason)
	}
	// Resolve the exact attachment captured before dispatch. A later attach is
	// never a route fallback for work that arrived on the previous attachment.
	snapshot, err := state.awaitAttachment(request.ctx, request.attachment)
	if err != nil {
		return
	}
	mgr, outcome := h.agentLoop.BrowserManagerForAgent(request.ctx, frame.AgentId, h.sessionWorkspaceID(sessID))
	if outcome != agent.BrowserResolveOK {
		send(sessionErrorStatus(sessID, browserResolveReason(outcome, frame.AgentId)), sessID, "webrtc-offer-no-manager")
		return
	}
	if snapshot.mgr != mgr || snapshot.sessionID != sessID {
		send(operationErrorStatus(sessID, "browser_webrtc_offer: attachment does not match this browser session"), sessID, "webrtc-offer-route")
		return
	}
	if reason := webrtcUnavailableReason(cfg, mgr); reason != "" {
		sendState(false, false, false, reason, nil)
		return
	}
	if request.ctx.Err() != nil {
		return
	}
	panelSessionID, browsingKey := snapshot.panelSessionID, mgr.BrowsingKey().String()
	h.captureFenceMu.Lock()
	if mgr.CaptureSessionForPanel(panelSessionID) == nil {
		for otherCS, other := range h.captures.otherSessions(browsingKey) {
			if otherCS.IsStarting() {
				continue
			}
			if otherCS.ViewerCount() > 0 {
				h.captureFenceMu.Unlock()
				sendState(false, false, false, "multi_agent_capture_denied", nil)
				h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCStreamStartFailed,
					map[string]any{"session_id": sessID, "reason": "multi_agent_capture_denied", "browsing_key": browsingKey, "other_browsing_key": other})
				return
			}
			go otherCS.Stop()
		}
	}
	if request.ctx.Err() != nil {
		h.captureFenceMu.Unlock()
		return
	}
	cs, err := h.ensureCaptureSession(mgr, frame.AgentId, panelSessionID, cfg)
	h.captureFenceMu.Unlock()
	if err != nil {
		sendState(false, false, false, "error", err)
		return
	}
	// Stop closes Done before its bounded shutdown-control send and relay close.
	// End this attempt immediately, while the peer's parent remains the original
	// attachment rather than this temporary negotiation context.
	negotiation, cancel := context.WithCancel(request.ctx)
	defer cancel()
	go func() {
		select {
		case <-cs.Done():
			cancel()
		case <-negotiation.Done():
		}
	}()
	ingestURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", cfg.Gateway.Port)
	justStarted, startErr := cs.Start(negotiation, ingestURL)
	if startErr != nil {
		if negotiation.Err() == nil {
			sendState(false, false, false, "error", startErr)
		}
		h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCStreamStartFailed,
			map[string]any{"session_id": sessID, "error": startErr.Error()})
		cs.Stop()
		return
	}
	if justStarted {
		h.auditStream(userID, frame.AgentId, audit.SeverityInfo, audit.EventBrowserWebRTCStreamStarted, map[string]any{"session_id": sessID})
	}
	if refreshErr := h.applyColdStartRecapture(negotiation, snapshot, cs); refreshErr != nil {
		if negotiation.Err() == nil {
			sendState(true, false, false, "error", refreshErr)
		}
		return
	}
	captureID, generation := "", uint64(0)
	if frame.CaptureId != nil {
		captureID, generation = *frame.CaptureId, uint64(*frame.CaptureGeneration)
	}
	confirmed, err := cs.WaitConfirmedFrame(negotiation, captureID, generation)
	if err != nil {
		if negotiation.Err() == nil {
			sendState(true, false, false, "error", err)
		}
		return
	}
	parent, err := withWebRTCInputRoute(snapshot.ctx, mgr, panelSessionID, func(source context.Context, kind string, dispatchErr error) {
		message := fmt.Sprintf("browser input failed: %s", dispatchErr)
		if !state.shouldSendInputFailure(snapshot, kind, message, time.Now()) {
			return
		}
		wc.sendCriticalScopedGen(operationErrorStatus(sessID, message), dropContext(sessID, viewerID, "webrtc-input-error"), source, nil)
	})
	if err != nil {
		return
	}
	answer, viewerHandle, offerErr := cs.HandleViewerOfferRequest(negotiation, parent, epoch, viewerID, frame.Sdp)
	if offerErr != nil {
		cs.CleanupViewerOffer(viewerHandle)
		if negotiation.Err() != nil {
			return
		}
		reason := "error"
		if errors.Is(offerErr, webrtc.ErrNoIngestVideoTrack) {
			reason = "ingest_timeout"
		}
		slog.Warn("browser-webrtc: viewer offer failed", "error", offerErr, "agent_id", frame.AgentId, "viewer_id", viewerID, "reason", reason)
		h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCViewerOfferFailed,
			map[string]any{"session_id": sessID, "viewer_id": viewerID, "reason": reason, "error": offerErr.Error()})
		sendState(true, false, false, reason, offerErr)
		return
	}
	frameCurrent := func() bool {
		select {
		case <-cs.Done():
			return false
		default:
		}
		current := cs.FrameState()
		return current.CaptureID == confirmed.CaptureID && current.Generation == confirmed.Generation && current.Width > 0 && current.Height > 0
	}
	att := &webrtcAttachment{agentID: frame.AgentId, capture: cs, handle: viewerHandle}
	if negotiation.Err() != nil || !frameCurrent() || !state.commitWebRTCAttachmentForRequest(request, att) ||
		!h.registerWebRTCViewerConnForCapture(state, epoch, snapshot.ctx, viewerID, wc, sessID, cs) {
		cs.CleanupViewerOffer(viewerHandle)
		return
	}
	if !h.publishCurrentVideoHealth(viewerID, cs, snapshot.ctx) {
		cs.CleanupViewerOffer(viewerHandle)
		return
	}
	answerCurrent := func() bool { return frameCurrent() && state.webRTCAttachmentCurrent(request, att) }
	id, gen, offerID := confirmed.CaptureID, int(confirmed.Generation), *frame.OfferId
	if !wc.sendCriticalScopedGen(generated.BrowserWebRTCAnswerFrame{
		Type: string(generated.WsFrameTypeBrowserWebrtcAnswer), Sdp: answer, SessionId: &sessID,
		CaptureId: &id, CaptureGeneration: &gen, OfferId: &offerID,
	}, dropContext(sessID, viewerID, "webrtc-answer"), snapshot.ctx, answerCurrent) {
		cs.CleanupViewerOffer(viewerHandle)
		return
	}
	stats := cs.Stats()
	active := generated.BrowserWebRTCStateFrame{Type: string(generated.WsFrameTypeBrowserWebrtcState), SessionId: &sessID, Available: true, Active: boolPtr(true)}
	if stats.HasAudio {
		active.HasAudio = boolPtr(true)
	}
	wc.sendCriticalScopedGen(active, dropContext(sessID, viewerID, "webrtc-state"), snapshot.ctx, answerCurrent)
	if notice := h.mediaTransportNotice(); notice != "" {
		wc.sendCriticalScopedGen(sessionErrorStatus(sessID, notice), dropContext(sessID, viewerID, "media-port-fallback"), snapshot.ctx, answerCurrent)
	}
}

// applyColdStartRecapture verifies the original panel's current measured
// geometry before negotiation. An unchanged source needs no recapture.
func (h *BrowserWSHandler) applyColdStartRecapture(ctx context.Context, original browserAttachmentSnapshot, cs *browser.CaptureSession) error {
	if original.ctx == nil || original.mgr == nil || original.panelSessionID == "" {
		return fmt.Errorf("browser capture: original attachment unavailable")
	}
	operation, cancel := original.bindContext(ctx)
	defer cancel()
	return original.mgr.Live().RefreshCaptureFrameContext(operation, original.panelSessionID, cs)
}

// webrtcUnavailableReason evaluates the ADR-047 D3 / ADR-048 condition-3
// gate ladder — WebRTCEnabled -> lite build -> capture-capable — shared by
// announceWebRTCAvailability (the post-attach announcement) and
// handleWebRTCOffer (the actual offer-time re-validation) so the two paths
// can never spell a rejection reason differently (fix 9, SIMPL finding:
// byte-identical tokens, previously spelled out twice). Returns "" when
// every gate passes (available=true); otherwise the browser_webrtc_state
// reason token to send. Logs the capability classifier's operator-only
// Reason at Warn (fix 4 — never sent to the client, only ever the "not_capable"
// token is) on every not_capable rejection this function produces — it was
// previously computed by the classifier and silently discarded.
func webrtcUnavailableReason(cfg *config.Config, mgr *browser.BrowserManager) string {
	if !cfg.Tools.Browser.WebRTCEnabled {
		return "disabled"
	}
	if !webrtc.Available {
		return "lite_build"
	}
	videoCap := mgr.CaptureVideoCapability()
	if !videoCap.Capable {
		slog.Warn("browser-webrtc: video capability not_capable", "reason", videoCap.Reason, "agent_id", mgr.AgentID())
		return "not_capable"
	}
	return ""
}

// detachWebRTCViewer tears down a connection's WebRTC viewer attachment
// (browser_detach, WS close, or connection cleanup) — closes the relay-side
// viewer PeerConnection, decrements the capture session's viewer count
// (RemoveViewer arms the grace-stop timer once it reaches zero, wave-plan
// W2-A item 4), and unregisters the viewer from h.viewerConns (fix 3/7's
// cross-goroutine registry). Independent of the connection's own
// session/control-lock detach() (browser_ws.go's handleDetach), since both
// can be active on the same connection.
//
// ALWAYS bumps state's webrtc epoch (invalidateWebRTCOffer, FIX WAVE A
// finding 1), even when takeWebRTCAttachment finds nothing yet committed —
// a browser_webrtc_offer dispatched via dispatchWebRTCOffer may still be
// negotiating on its own goroutine at the moment this runs (an explicit
// browser_detach, or the connection itself closing, can both arrive before
// that negotiation finishes); invalidating here is what makes that
// goroutine's eventual commit attempt recognize it has been superseded and
// tear down what it built instead of attaching a viewer state nobody wants
// anymore. Safe (and expected) to call unconditionally — every call site now
// does, rather than gating on state.webrtc != nil first.
//
// Fix-wave finding (identity-aware teardown, third bypass): this used to
// tear down the committed attachment via the bare, viewerID-only
// att.capture.Relay().CloseViewer(viewerID) + att.capture.RemoveViewer(viewerID)
// pair — exactly the unsafe pattern handleWebRTCOffer's own failure and
// superseded-before-commit branches were fixed to stop using (see
// CleanupViewerOffer's doc comment). commitWebRTCAttachment/webrtcEpoch only
// guard THIS connection's single-slot state.webrtc field; they do not guard
// CaptureSession.viewers or the relay's own viewer registry, both of which a
// SECOND, still-negotiating browser_webrtc_offer for the SAME viewerID (an
// ICE-restart/reconnect: dispatchWebRTCOffer spawns an unserialized goroutine
// per offer frame) can mutate — via AddViewer minting a newer generation and
// HandleViewerOfferHandle registering a newer PeerConnection — BEFORE that
// second offer ever reaches its own commit attempt. If a detach or
// connection-close raced in at exactly that point, takeWebRTCAttachment()
// here still returns the OLDER, already-committed attachment, and the old
// unconditional pair would close/evict whatever the relay/CaptureSession
// currently hold for viewerID — i.e. the NEWER offer's live, still-
// negotiating connection — instead of the one this attachment actually
// owns. CleanupViewerOffer(att.handle) is the same identity-checked
// mechanism (ViewerAttachHandle's gen + the relay's own
// CloseViewerIfCurrent/RemoveViewerIfCurrent) the offer path already relies
// on: a no-op if att.handle's registration has since been superseded, and a
// full, real teardown (closes the PeerConnection, arms the grace-stop timer)
// when it is still current — which is always true for the ordinary case of
// a legitimate detach with no second offer in flight, so a normal detach's viewer is
// still fully removed exactly as before.
func (h *BrowserWSHandler) detachWebRTCViewer(state *browserConnState, viewerID string) {
	att := state.takeWebRTCAttachment()
	state.invalidateWebRTCOffer()
	h.unregisterWebRTCViewerConn(viewerID)
	if att == nil || att.capture == nil {
		return
	}
	att.capture.CleanupViewerOffer(att.handle)
}

func (h *BrowserWSHandler) unregisterWebRTCViewerConn(viewerID string) {
	h.viewerConns.Delete(viewerID)
}

// notifyViewersStreamStopped pushes browser_webrtc_state{available:false,
// reason:"error"} to every viewer that was still attached when a capture
// session stopped (fix 3: viewers previously learned of a server-side stop
// only ~5s later, once their OWN ICE connection state machine noticed the
// peer was gone). Invoked from the SAME onStopped hook regardless of WHY the
// session stopped — grace timer, browser death, the encoder-liveness
// watchdog, or an ensure/start failure (fix 1) — so this one path covers
// every stop cause. viewerIDs is a snapshot taken via CaptureSession.
// ViewerIDs(), which remains accurate to read even from inside onStopped
// (Stop() never clears cs.viewers itself).
func (h *BrowserWSHandler) notifyViewersStreamStopped(cs *browser.CaptureSession, viewerIDs []string) {
	if cs == nil {
		return
	}
	captureID := cs.FrameState().CaptureID
	for _, viewerID := range viewerIDs {
		value, ok := h.viewerConns.Load(viewerID)
		if !ok {
			continue
		}
		vc, ok := value.(*webrtcViewerConn)
		if !ok || vc == nil {
			continue
		}
		origin := vc.attachmentCtx
		current := func() bool {
			return h.currentVideoViewer(viewerID, vc, cs, captureID, origin)
		}
		if !current() {
			continue
		}
		reason := "error"
		frame := generated.BrowserWebRTCStateFrame{
			Type:      string(generated.WsFrameTypeBrowserWebrtcState),
			SessionId: &vc.sessionID,
			Available: false,
			Reason:    &reason,
		}
		vc.wc.sendCriticalScopedGen(frame, dropContext(vc.sessionID, viewerID, "capture-stopped"), origin, current)
	}
}

// encoderLivenessCheckInterval / encoderLivenessStaleAfter (fix 3):
// CaptureSession.LastPingAt() previously had zero readers — a wedged or
// crashed encoder page that never disconnects cleanly (no TCP RST, just
// silence) could leave a capture session "started" forever with no video
// ever flowing and no signal to the viewer beyond eventually noticing the
// picture is frozen. encoder.js's own startPingBeacon sends a
// browser_capture_control{ping} every 15s; staleAfter is 2x that plus slack
// so a single missed beacon (a network hiccup) never trips the watchdog —
// only sustained silence does.
// vars (not consts) so browser_webrtc_watchdog_test.go can shrink them for a
// fast, deterministic watchdog test without a real 40s wait — mirrors
// capture_session.go's captureGracePeriod pattern.
var (
	encoderLivenessCheckInterval = 10 * time.Second
	encoderLivenessStaleAfter    = 40 * time.Second
)

// encoderLivenessVideoStallTicks debounces positive capture-stage failure
// evidence while relay packets remain unchanged. Silence by itself never
// consumes this budget: a healthy static page may produce no video frames.
const encoderLivenessVideoStallTicks = 6

// watchEncoderLiveness runs once per capture session until Stop closes Done.
// A stale control heartbeat stops the session. Fresh stage-failure evidence
// with no relay progress requests bounded recapture, preserving viewer peers.
// Timing values are arguments so tests cannot race a goroutine reading mutable
// package defaults after its launching test has already returned.
func (h *BrowserWSHandler) watchEncoderLiveness(cs *browser.CaptureSession, agentID string, checkInterval, staleAfter time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	var tracker captureHealthTracker
	var lastReceipt, receiptAtHealthSample uint64
	var lastHealthObserved time.Time
	haveBaseline := false
	stallTicks := 0
	for {
		select {
		case <-cs.Done():
			return
		case now := <-ticker.C:
			snapshot := cs.WatchdogSnapshot()
			health := snapshot.Health
			previous := tracker.previous
			if previous.BindingEpoch != health.BindingEpoch || previous.CaptureGeneration != health.CaptureGeneration || previous.TargetID != health.TargetID || previous.Generation != health.Generation {
				stallTicks = 0
			}
			stageFailure := tracker.observe(health, now, staleAfter)
			serial := snapshot.VideoReceipt.Serial
			// A finite packet can arrive before its next encoder stats sample.
			// Retain the previous sample's receipt baseline until that sample is
			// processed, so later counter catch-up does not invent a relay stall.
			receiptAdvanced := serial > lastReceipt || serial > receiptAtHealthSample
			if haveBaseline && snapshot.ReceiptCurrent && receiptAdvanced {
				stageFailure = tracker.noteRelayProgress()
			}
			if health.ObservedAt != lastHealthObserved {
				lastHealthObserved = health.ObservedAt
				receiptAtHealthSample = serial
			}
			if snapshot.ViewerCount > 0 {
				if haveBaseline && stageFailure != "" {
					stallTicks++
				} else {
					stallTicks = 0
					if haveBaseline && snapshot.ReceiptCurrent {
						// Reconsider retained finite evidence after another stage
						// clears. CaptureSession consumes each serial only once.
						cs.RecordVideoProgress()
					}
				}
				haveBaseline = true
			} else {
				stallTicks, haveBaseline = 0, false
			}
			lastReceipt = serial
			if stallTicks >= encoderLivenessVideoStallTicks {
				if cs.ReportCaptureFailureForObservation(health) {
					slog.Warn("browser-webrtc: capture stage failed; requesting bounded recovery", "stage", stageFailure, "agent_id", agentID, "receipt_serial", serial, "stall_ticks", stallTicks, "check_interval", checkInterval)
				}
				stallTicks = 0
			}
			if cs.StopIfIngestHeartbeatStale(snapshot.BindingEpoch, snapshot.LastPingAt, now, staleAfter) {
				slog.Warn("browser-webrtc: encoder liveness watchdog — no ping beacon received, stopping capture session", "agent_id", agentID, "last_ping_at", snapshot.LastPingAt, "stale_after", staleAfter)
				return
			}
		}
	}
}

// ensureCaptureSession get-or-creates the CaptureSession for the WORKSPACE
// BROWSER mgr owns — one active stream per browsing key (ADR-075 FR-016a,
// narrowing wave-plan W2-A item 4's "one per agent") — registering it in
// h.captures under that key so the ingest WS can find it by token, wiring
// SetOnStopped to remove it from both the registry and the manager once it
// stops AND push a browser_webrtc_state to any still-attached viewers (fix 3),
// and starting the encoder-liveness watchdog (fix 3).
//
// agentID is the REQUESTING agent and is used for log/audit context only. It
// deliberately does NOT key anything: the memoization is mgr's
// (EnsureCaptureSession), so whichever agent on the workspace offers first
// creates the session and every other agent on that team joins it. Reading
// agentID as ownership is the bug FR-016a removes.
//
// panelSessionID (issue #671) is the tab set the capture binds to — the same
// one this connection's control plane resolved, so the video shows the tab the
// clicks drive. It is CONSUMED ONLY when this call actually creates the
// session: one browser has one capture, so a later viewer joins the existing
// stream rather than re-pointing it out from under whoever is already
// watching. Empty means "no panel context" (the boot-time warm-up), which
// binds the operator's workspace-owned set exactly as before.
func (h *BrowserWSHandler) ensureCaptureSession(
	mgr *browser.BrowserManager,
	agentID string,
	panelSessionID string,
	cfg *config.Config,
) (*browser.CaptureSession, error) {
	h.mediaLifecycleMu.RLock()
	defer h.mediaLifecycleMu.RUnlock()
	h.mediaConnMu.Lock()
	closed := h.mediaClosed
	h.mediaConnMu.Unlock()
	if closed {
		return nil, errors.New("browser media transport is closed")
	}
	browsingKey := mgr.BrowsingKey().String()
	return mgr.EnsureCaptureSessionForPanel(panelSessionID, func() (*browser.CaptureSession, error) {
		webrtcCfg := webrtc.Config{
			StunServer:  cfg.Tools.Browser.WebRTCStunServer,
			MediaUDPMux: h.sharedMediaUDPMux(cfg),
			MediaTCPMux: h.sharedMediaTCPMux(cfg),
			PublicIPs:   resolveWebRTCPublicIPs(cfg),
		}
		sink := newWebRTCContextInputSink(cfg.Gateway.ValidateInbound)
		logf := webrtcRelayLogf(agentID)
		cs, err := browser.NewCaptureSessionWithContextInput(mgr, agentID, panelSessionID, webrtcCfg, sink, logf)
		if err != nil {
			return nil, err
		}
		h.captures.set(browsingKey, cs)
		cs.SetOnStopped(func() {
			h.captures.removeIfCurrent(browsingKey, cs)
			audit.Emit(context.Background(), h.agentLoop.AuditLogger(), audit.EventBrowserWebRTCStreamStopped,
				audit.SeverityInfo, map[string]any{"agent_id": agentID, "browsing_key": browsingKey})
			h.notifyViewersStreamStopped(cs, cs.ViewerIDs())
		})
		// Reading encoderLivenessCheckInterval/encoderLivenessStaleAfter HERE
		// (as `go` statement arguments, evaluated on THIS goroutine before the
		// watchdog goroutine is spawned) rather than inside
		// watchEncoderLiveness is load-bearing — see that function's doc
		// comment for the data race this closes.
		go h.watchEncoderLiveness(cs, agentID, encoderLivenessCheckInterval, encoderLivenessStaleAfter)
		return cs, nil
	})
}

// webrtcRelayLogf builds the log sink passed to browser.NewCaptureSession
// (forwarded to webrtc.NewSession as the Pion relay's own logf). Fix 5: this
// was always slog.Debug, so genuinely error-ish relay lines (the webrtc
// package's ingest.go/viewer.go/session.go log lines carry "failed"/
// "WARNING" markers on codec registration failures, PLI send failures, RTP
// forward write failures, and unexpected disconnects — inspected without
// editing that package, per this fix-wave's file fence) were invisible at
// any log level an operator would normally have enabled. Classifies by the
// SAME simple substring markers those log lines already use, rather than
// duplicating a call-site enumeration that would silently drift the moment
// that package's log text changes: any line containing "failed" or
// "warning" (case-insensitive) lands at Warn; every other — purely
// informational — line (connection-state transitions, "answer sent", RTP
// forward progress counters) stays at Debug.
func webrtcRelayLogf(agentID string) func(string, ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if webrtcRelayLineLooksErrorish(msg) {
			slog.Warn("browser-webrtc[" + agentID + "]: " + msg)
			return
		}
		slog.Debug("browser-webrtc[" + agentID + "]: " + msg)
	}
}

// webrtcRelayLineLooksErrorish is webrtcRelayLogf's classifier.
func webrtcRelayLineLooksErrorish(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "failed") || strings.Contains(lower, "warning") || strings.Contains(lower, "error")
}

// auditStream emits a WebRTC stream lifecycle audit entry.
func (h *BrowserWSHandler) auditStream(
	userID, agentID string,
	sev audit.Severity,
	event string,
	fields map[string]any,
) {
	al := h.agentLoop.AuditLogger()
	if al == nil {
		return
	}
	merged := map[string]any{"agent_id": agentID, "user": userID}
	for k, v := range fields {
		merged[k] = v
	}
	audit.Emit(context.Background(), al, event, sev, merged)
}

// ---------------------------------------------------------------------------
// Capture-ingest WS (/api/v1/browser/capture-ingest) — loopback-only
// ---------------------------------------------------------------------------

// captureIngestMaxMessageBytes bounds inbound frames on the ingest socket.
// SDP offers are the largest payload here — a few KB at most — so this is
// generous but still bounds a malformed/hostile local process (ADR-047 D6:
// loopback is not a trust boundary).
const captureIngestMaxMessageBytes = 256 * 1024

// captureIngestConn wraps one capture-ingest connection's write side.
// gorilla/websocket requires at most one concurrent writer per connection.
// A cancelable admission gate serializes the offer/answer/control/ping writes
// this socket carries, so a dedicated writePump/sendCh (as browser_ws.go
// uses for the high-volume screencast socket) would be overkill here.
type captureIngestConn struct {
	conn      *websocket.Conn
	writeOnce sync.Once
	writeGate chan struct{}
}

// captureIngestWriteTimeout bounds every write to the capture-ingest socket
// (fix-wave HIGH). gorilla/websocket requires an explicit deadline for this
// bound to exist at all — without one, a wedged/backpressured encoder socket
// can block a write here forever. That mattered beyond just this one write:
// CaptureSession.Stop()'s requestControl("shutdown", ...) call is
// SYNCHRONOUS and runs before the relay/tabCancel/onStopped teardown below
// it (capture_session.go), so an unbounded write here could permanently wedge
// the ENTIRE capture-session stop path — bricking the agent's WebRTC capture
// for the rest of the process's life, since nothing else ever calls Stop()
// again on a session already "stopping." 5s is generous for a same-host
// loopback write (the only transport this socket ever uses — ADR-047 D6)
// while still bounding the worst case. requestControl only logs a write
// error/timeout (capture_session.go) and unconditionally proceeds to the
// rest of Stop()'s teardown regardless of the outcome, so this deadline is
// sufficient on its own — no additional reordering of Stop() is needed.
// A var (not const) purely as a test seam (mirrors captureGracePeriod's/
// encoderLivenessStaleAfter's established pattern in this codebase).
var captureIngestWriteTimeout = 5 * time.Second

func (c *captureIngestConn) sendJSON(v any) error {
	return c.sendJSONContext(context.Background(), v, nil)
}

// captureIngestWSHandler implements /api/v1/browser/capture-ingest
// (ADR-047 D6): the gateway-owned encoder page's ingest leg. Loopback-only
// (RemoteAddr must resolve to 127.0.0.1/::1 — checked BEFORE the WS
// upgrade), authorized by the first frame's browser_capture_hello token
// (constant-time compared against every active CaptureSession — see
// captureRegistry.findByToken), never by URL/path.
type captureIngestWSHandler struct {
	agentLoop   *agent.AgentLoop
	captures    *captureRegistry
	upgrader    websocket.Upgrader
	activeConns sync.WaitGroup
}

// newCaptureIngestWSHandler constructs the handler, sharing captures with
// the main BrowserWSHandler so a hello's token can be resolved to the
// CaptureSession a browser_webrtc_offer created.
func newCaptureIngestWSHandler(agentLoop *agent.AgentLoop, captures *captureRegistry) *captureIngestWSHandler {
	return &captureIngestWSHandler{
		agentLoop: agentLoop,
		captures:  captures,
		upgrader: websocket.Upgrader{
			// No origin check: this endpoint is loopback-only by RemoteAddr
			// gate (ServeHTTP, below) — the WS Origin header is not a
			// meaningful trust signal for a same-host caller (the CDP-driven
			// encoder page has no browser-enforced Origin at all), and the
			// loopback check is the actual boundary ADR-047 D6 relies on.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

func (h *captureIngestWSHandler) Wait() {
	h.activeConns.Wait()
}

// ServeHTTP enforces the loopback gate, then hands off to serveConn.
func (h *captureIngestWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remoteHost, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		remoteHost = r.RemoteAddr
	}
	remoteIP := net.ParseIP(remoteHost)
	if remoteIP == nil || !remoteIP.IsLoopback() {
		h.auditIngestRejected(r.RemoteAddr, "non_loopback")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("capture-ingest: upgrade failed", "error", err)
		return
	}
	h.activeConns.Add(1)
	defer h.activeConns.Done()
	defer conn.Close()

	conn.SetReadLimit(captureIngestMaxMessageBytes)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))

	h.serveConn(conn, r.RemoteAddr)
}

// serveConn implements the hello -> offer/answer -> control lifecycle
// described in encoder.js: the authenticated hello binds this socket, then
// the server replays the current capture command. Qualified offer/answer
// exchange follows, while health pings remain readable during negotiation.
// Server control commands use the callback bound to this exact socket.
func (h *captureIngestWSHandler) serveConn(conn *websocket.Conn, remoteAddr string) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		slog.Debug("capture-ingest: read hello failed", "error", err)
		return
	}

	cfg := h.agentLoop.GetConfig()
	if cfg.Gateway.ValidateInbound {
		if errMsg, serverErr := ValidateInboundFrameJSON("BrowserCaptureHelloFrame", data); errMsg != "" {
			if serverErr {
				slog.Error("capture-ingest: inbound schema unavailable, dropping hello")
			} else {
				slog.Warn("capture-ingest: hello frame schema validation failed", "error", errMsg)
			}
			h.auditIngestRejected(remoteAddr, "schema_invalid")
			return
		}
	}

	var hello generated.BrowserCaptureHelloFrame
	if jsonErr := json.Unmarshal(
		data,
		&hello,
	); jsonErr != nil ||
		hello.Type != string(generated.WsFrameTypeBrowserCaptureHello) {
		h.auditIngestRejected(remoteAddr, "not_hello")
		return
	}

	// FR-016a: the registry is keyed by browsing key, so what comes back here
	// names one workspace's browser, not an agent. The encoder page belongs to
	// that Chrome and to no single agent, so this is also the more honest log
	// label than the requesting-agent id it replaced.
	browsingKey, cs := h.captures.findByToken(hello.Token)
	if cs == nil {
		h.auditIngestRejected(remoteAddr, "token_mismatch")
		return
	}
	cs.RecordExtVersion(hello.ExtVersion)

	h.serveBoundIngest(conn, cs, browsingKey, cfg.Gateway.ValidateInbound)
}

// captureFrameSchemaName maps a capture-ingest frame type to its inbound
// schema file name (mirrors wsFrameSchemaName's pattern for the main browser
// WS — websocket.go).
func captureFrameSchemaName(frameType string) string {
	switch frameType {
	case string(generated.WsFrameTypeBrowserCaptureOffer):
		return "BrowserCaptureOfferFrame"
	case string(generated.WsFrameTypeBrowserCaptureControl):
		return "BrowserCaptureControlFrame"
	default:
		return ""
	}
}

// auditIngestRejected audits a rejected capture-ingest connection attempt
// (ADR-047 D6: "the gateway audits any hello with a missing/invalid/expired
// token as a rejected ingest-auth attempt").
func (h *captureIngestWSHandler) auditIngestRejected(remoteAddr, reason string) {
	al := h.agentLoop.AuditLogger()
	if al == nil {
		return
	}
	audit.Emit(context.Background(), al, audit.EventBrowserWebRTCIngestAuthRejected, audit.SeverityWarn,
		map[string]any{"remote_addr": remoteAddr, "reason": reason})
}

// resolveWebRTCPublicIPs decides what address viewers are told to send media
// to (ADR-062 tier 1).
//
// Order, and why: an explicit tools.browser.webrtc_public_ip wins, because an
// operator who set it knows something we do not (split DNS, a separate media
// IP). Otherwise it is DERIVED from gateway.public_url -- the setting every
// operator behind a domain has already configured for CSP/CORS/WS origin
// checks. That derivation is the point of ADR-062's "no additional
// configuration for the user": a hosted install must not require the operator
// to discover a WebRTC-specific knob before video works.
//
// Returns nil when neither is available (a laptop install, or a hosted box
// with no public_url). nil is correct there, not a failure: without it the
// gateway advertises its real interface addresses, which is exactly right on
// a laptop -- and on a hosted box with no public_url there is no address we
// could honestly advertise anyway. The ICE-failure log names this case so the
// operator is told what to set rather than left guessing.
//
// A HOSTNAME in public_url is deliberately NOT resolved to an IP here.
// SetNAT1To1IPs takes literal addresses; resolving a name at boot would bake
// in whatever DNS said at that moment and silently rot on a DNS change.
// Operators fronted by a hostname set webrtc_public_ip explicitly.
func resolveWebRTCPublicIPs(cfg *config.Config) []string {
	if explicit := strings.TrimSpace(cfg.Tools.Browser.WebRTCPublicIP); explicit != "" {
		return []string{explicit}
	}
	raw := strings.TrimSpace(cfg.Gateway.PublicURL)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	host := u.Hostname()
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return []string{ip.String()}
	}
	return nil
}

// mediaPortFallbackSpan is how many consecutive ports ABOVE the configured
// one sharedMediaConn will try before giving up. Deliberately small: the
// point is to survive the one realistic collision (a second Omnipus on the
// same host, or anything else already holding the port), not to hunt the
// whole port space for a socket the operator's firewall has not been told
// about.
const mediaPortFallbackSpan = 16

// maxUDPPort bounds the fallback probe so a configured port near the top of
// the range cannot walk past 65535.
const maxUDPPort = 65535

// mediaUDPAddr builds the listen address for a media-port bind attempt.
func mediaUDPAddr(bindAddr string, port int) string {
	if bindAddr == "" {
		return ":" + strconv.Itoa(port)
	}
	// Some platforms route inbound UDP only to a specific address --
	// Fly.io requires "fly-global-services" and documents that binding
	// 0.0.0.0 makes Linux pick the wrong SOURCE address on replies, so
	// the peer discards them silently.
	return net.JoinHostPort(bindAddr, strconv.Itoa(port))
}

// mediaPortFallbackState records that sharedMediaConn could NOT bind the
// fixed media UDP port the operator explicitly configured, and what it did
// instead. Written at most once, under h.mediaConnMu, at the moment the
// process-wide media socket is decided; read-only afterwards for the
// lifetime of the process (the socket is memoised, so the degradation is
// too).
//
// It exists because the log alone is not a user-visible surface (round-2
// finding F6). The person who has to fix this — free the port, or change
// tools.browser.webrtc_media_udp_port and re-declare it to their provider —
// is the operator, and on a hosted install the ONLY symptom they otherwise
// get is a live-browser panel that never shows a picture, with the panel
// itself claiming nothing is wrong. ADR-061 deleted the JPEG screencast
// fallback for exactly this shape: a degradation nobody can see stays broken
// indefinitely. bound == 0 means nothing in the probe range could be bound at
// all and every Session is back on ephemeral ports.
type mediaPortFallbackState struct {
	configured int
	bound      int
	lastProbed int
}

// sharedMediaConn returns the process-wide fixed media socket, binding it on
// first use (ADR-062 tier 1). Returns nil when fixed-port media is not
// configured, or when no port in the fallback range could be bound.
//
// The configured port is always tried FIRST and is the only port the operator
// has declared to their provider/firewall, so it is the only one that can
// actually work on a hosted install. If it is unavailable we fall back to the
// next free port (FIX WAVE B finding C) rather than returning nil, because
// returning nil drops every Session back to EPHEMERAL ports, which is
// strictly worse in every deployment: identical failure on a hosted box, and
// a needless loss of the single stable port on a laptop.
//
// The fallback is LOUD at ERROR, names BOTH ports, AND is recorded in
// h.mediaPortFallback so every viewer is TOLD in the panel (see
// mediaPortFallbackState / notifyMediaPortDegraded). It is a
// misconfiguration the operator has to fix — measured 2026-08-15, a second
// Omnipus on the same host failed with `listen udp :50000: bind: address
// already in use`, logged ERROR, silently continued on ephemeral ports, and
// on a hosted install that means live video just stops working with nothing
// in the product saying why. Silently continuing as if nothing happened is
// the specific behaviour this replaces.
//
// ERROR, not WARN (round-2 finding F6), and that choice is about who has to
// act on it. tools.browser.webrtc_media_udp_port has NO default — 0 means
// "ephemeral, pre-ADR-062" and is the laptop default (see
// config.BrowserConfig.WebRTCMediaUDPPort). So any non-zero value reaching
// this function was typed by an operator into config.json or
// OMNIPUS_TOOLS_BROWSER_WEBRTC_MEDIA_UDP_PORT: there is no "merely defaulted"
// case where the fallback overrides nothing. The fallback ALWAYS overrides an
// explicit, deliberate operator instruction, and on a hosted install it turns
// working live video into a permanently dead panel for every remote viewer.
// The gateway's own default log level is "warn"
// (pkg/config/defaults.go's LogLevel), so a WARN line does survive — but it
// survives as one line among hundreds, which for a defect only the operator
// can fix is indistinguishable from silence. ADR-061's rule is that a
// degradation names its cause where the person affected will see it; ERROR
// plus the panel notice is that rule applied here.
//
// The retry is attempted on ANY bind error rather than on EADDRINUSE
// specifically, and that is a deliberate cross-platform choice: errno
// spelling differs (Linux/macOS EADDRINUSE vs Windows WSAEADDRINUSE) and
// matching it would make the three platforms behave differently for the same
// user-visible situation. A systemic failure instead (a bad bind address, a
// privileged port) fails every probe immediately -- ListenPacket rejects them
// without touching the network -- and lands on exactly the same ERROR the old
// code produced. Both branches quote the ORIGINAL error, so they stay
// truthful whatever the cause was.
func (h *BrowserWSHandler) sharedMediaConn(cfg *config.Config) net.PacketConn {
	port := cfg.Tools.Browser.WebRTCMediaUDPPort
	if port <= 0 {
		return nil
	}
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.mediaClosed {
		return nil
	}
	if h.mediaConn != nil {
		return h.mediaConn
	}
	bindAddr := strings.TrimSpace(cfg.Tools.Browser.WebRTCMediaUDPBindAddress)

	conn, err := net.ListenPacket("udp", mediaUDPAddr(bindAddr, port))
	if err == nil {
		slog.Info("browser-webrtc: fixed media UDP socket bound", "addr", conn.LocalAddr().String())
		h.mediaConn = conn
		h.mediaUDPMux = pion.NewICEUDPMux(nil, conn)
		return conn
	}
	configuredErr := err

	lastProbed := port
	for probe := port + 1; probe <= port+mediaPortFallbackSpan && probe <= maxUDPPort; probe++ {
		lastProbed = probe
		fallback, probeErr := net.ListenPacket("udp", mediaUDPAddr(bindAddr, probe))
		if probeErr != nil {
			continue
		}
		h.mediaPortFallback = &mediaPortFallbackState{configured: port, bound: probe, lastProbed: probe}
		slog.Error(
			"browser-webrtc: OPERATOR ACTION REQUIRED — the fixed media UDP port you configured could not be "+
				"bound, so live video is using the next free port instead. This works for a viewer on the same "+
				"host or LAN, but on a hosted install your provider only routes the port you declared, so video "+
				"will NOT connect for any remote viewer until you fix this: either free the configured port (a "+
				"second Omnipus already running on this host is the usual cause) or set "+
				"tools.browser.webrtc_media_udp_port to the port actually bound, and declare that port to your "+
				"provider",
			"configured_port", port,
			"bound_port", probe,
			"addr", fallback.LocalAddr().String(),
			"configured_port_error", configuredErr,
		)
		h.mediaConn = fallback
		h.mediaUDPMux = pion.NewICEUDPMux(nil, fallback)
		return fallback
	}

	h.mediaPortFallback = &mediaPortFallbackState{configured: port, bound: 0, lastProbed: lastProbed}
	slog.Error(
		"browser-webrtc: OPERATOR ACTION REQUIRED — could not bind the fixed media UDP port you configured, "+
			"nor any port in the fallback range, so live video falls back to ephemeral ports. That works on a "+
			"same-host/LAN viewer but NEVER on a hosted install (no provider routes inbound UDP to an "+
			"undeclared ephemeral port)",
		"configured_port", port,
		"last_probed_port", lastProbed,
		"bind_address", bindAddr,
		"error", configuredErr,
	)
	return nil
}

// iceServerEntry aliases the anonymous struct oapi-codegen generates for
// BrowserWebRTCStateFrame.ice_servers. An alias (not a new type) so it stays
// assignable to the generated field — the generated types are the only legal
// cross-boundary shape (Constraint #8).
type iceServerEntry = struct {
	Credential *string  `json:"credential,omitempty"`
	Urls       []string `json:"urls"`
	Username   *string  `json:"username,omitempty"`
}

// sharedTURN returns the process-wide embedded relay (ADR-062 tier 3),
// starting it on first use. Returns nil when TURN is not configured — the
// default — so every caller can invoke it unconditionally.
//
// Started once and kept for the process lifetime, for the same reason as the
// media sockets: a Session exists per AGENT, and a per-Session relay would
// mean the first agent wins the port and every later one silently gets
// nothing.
func (h *BrowserWSHandler) sharedTURN(cfg *config.Config) *webrtc.TURNServer {
	port := cfg.Tools.Browser.WebRTCTurnUDPPort
	if port <= 0 {
		return nil
	}
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.turnStarted {
		return h.turnServer
	}
	h.turnStarted = true

	publicIPs := resolveWebRTCPublicIPs(cfg)
	var public string
	if len(publicIPs) > 0 {
		public = publicIPs[0]
	}
	srv, err := webrtc.StartTURN(webrtc.TURNConfig{
		UDPPort:     port,
		TCPPort:     cfg.Tools.Browser.WebRTCTurnTCPPort,
		BindAddress: strings.TrimSpace(cfg.Tools.Browser.WebRTCMediaUDPBindAddress),
		PublicIP:    public,
	})
	if err != nil {
		h.turnStartErr = err
		slog.Error("browser-webrtc: embedded TURN relay failed to start — clients that cannot reach the media port directly have no path",
			"udp_port", port, "tcp_port", cfg.Tools.Browser.WebRTCTurnTCPPort, "error", err)
		return nil
	}
	h.turnServer = srv
	slog.Info("browser-webrtc: embedded TURN relay started (ADR-062 tier 3)",
		"udp_port", port, "tcp_port", cfg.Tools.Browser.WebRTCTurnTCPPort, "relay_address", public)
	return srv
}

// iceServersForViewer mints this viewer's ICE servers. Credentials are
// short-lived and per-viewer: pion/turn cannot revoke an allocation, so a
// bounded lifetime is the guarantee (see webrtc.TURNServer).
func (h *BrowserWSHandler) iceServersForViewer(cfg *config.Config, viewerID string) []iceServerEntry {
	srv := h.sharedTURN(cfg)
	if srv == nil {
		return nil
	}
	servers, err := srv.ICEServers(viewerID)
	if err != nil {
		slog.Warn("browser-webrtc: could not mint TURN credentials for this viewer", "viewer_id", viewerID, "error", err)
		return nil
	}
	out := make([]iceServerEntry, 0, len(servers))
	for _, s := range servers {
		user, cred := s.Username, s.Credential
		out = append(out, iceServerEntry{
			Urls:       s.URLs,
			Username:   &user,
			Credential: &cred,
		})
	}
	return out
}

// sharedMediaTCP returns the process-wide ICE-TCP listener (ADR-062 tier 2),
// binding it on first use. Returns nil when ICE-TCP is not configured or the
// listen failed (logged at ERROR).
func (h *BrowserWSHandler) sharedMediaTCP(cfg *config.Config) net.Listener {
	port := cfg.Tools.Browser.WebRTCMediaTCPPort
	if port <= 0 {
		return nil
	}
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.mediaClosed {
		return nil
	}
	if h.mediaTCP != nil {
		return h.mediaTCP
	}
	// Listen on every interface. fly-global-services is a UDP-only
	// source-address trick; Fly's TCP proxy connects to the machine's
	// private IP, which that name does not cover. Binding only
	// fly-global-services:50001 would leave the proxy's SYN unanswered.
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		h.mediaTCPBindErr = err
		slog.Error("browser-webrtc: ICE-TCP listen failed — viewers whose network drops UDP will not connect",
			"addr", ":"+strconv.Itoa(port), "error", err)
		return nil
	}
	h.mediaTCPBindErr = nil
	slog.Info("browser-webrtc: ICE-TCP socket bound", "addr", ln.Addr().String())
	h.mediaTCP = ln
	h.mediaTCPMux = pion.NewICETCPMux(nil, ln, 8)
	return ln
}

// ---------------------------------------------------------------------------
// UAT case 16 — a video failure must name its cause.
// ---------------------------------------------------------------------------

// webrtcReasonDetailMax bounds the free-text cause shipped alongside the
// closed `reason` enum on browser_webrtc_state. It sits just under the wire
// schema's own maxLength (512) so a truncation here is always the one the
// operator sees, never a schema rejection that would drop the whole frame and
// leave the panel silent — the precise failure mode ADR-061 exists to
// prevent.
const webrtcReasonDetailMax = 480

// webrtcReasonDetailSecretPatterns redacts the two shapes a credential can
// take inside a Go error chain on this path. Deliberately narrow: a URL, a
// CDP target id, a port, a file path and a timeout are ALL things the
// operator needs in order to act, and a broad scrubber that ate them would
// re-create the very defect this field fixes.
var webrtcReasonDetailSecretPatterns = []*regexp.Regexp{
	// A labelled credential: token=..., "api_key": ..., Authorization: ...
	regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_-]?key|apikey|authorization|credential)("?\s*[:=]\s*"?)([^\s",;)]+)`),
	// Bearer <opaque>.
	regexp.MustCompile(`(?i)\bBearer\s+[^\s",;)]+`),
	// A bare long hex run — the capture session's own per-stream token is 32
	// bytes rendered as 64 hex chars (capture_session.go mints it and
	// hex-encodes it into the encoder page), so an error that echoed one back
	// would leak it with no label to match on. 24+ is short enough to catch a
	// truncated echo and long enough that a CDP target id (a 32-char
	// uppercase hex handle) is the only false positive; that one is
	// deliberately accepted, because a redacted target id still leaves the
	// sentence actionable while a leaked token does not.
	regexp.MustCompile(`\b[0-9a-f]{24,}\b`),
}

// webrtcReasonDetailWhitespace collapses every run of whitespace (including
// the newlines and tabs a wrapped chromedp error carries) into a single
// space, so the panel renders one readable sentence instead of a ragged
// block.
var webrtcReasonDetailWhitespace = regexp.MustCompile(`\s+`)

// webrtcReasonDetail turns a gateway-side WebRTC failure into the
// operator-facing free text that rides on browser_webrtc_state.reason_detail.
//
// Why this exists at all: the `reason` enum is CLOSED (disabled /
// not_capable / lite_build / error / multi_agent_capture_denied /
// ingest_timeout), and four of those six collapse wildly different causes
// into one token. On UAT, "capture session: create encoder target: browser:
// timed out after 20s waiting for the browser to attach the tab (target may
// be unresponsive)" reached the operator as "The live browser reported an
// error starting video" — the enum was all the wire could carry, so the one
// fact worth acting on stayed in gateway.log. The browser_attach path never
// had this problem (browser_ws.go sends "browser_attach failed: %s" as free
// text on browser_status), which is why the two routes reported the same
// underlying condition at completely different resolutions.
//
// Returns "" for a nil error, which callers use to mean "omit the field" —
// the ordinary capability-gate reasons (disabled / lite_build / not_capable)
// have no error chain and are fully explained by the enum alone.
func webrtcReasonDetail(err error) string {
	if err == nil {
		return ""
	}
	s := webrtcReasonDetailWhitespace.ReplaceAllString(err.Error(), " ")
	for _, re := range webrtcReasonDetailSecretPatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			if sub := re.FindStringSubmatch(match); len(sub) == 4 {
				// Labelled form: keep the label and separator, drop the value,
				// so the sentence still reads ("mint token: token=[redacted]").
				return sub[1] + sub[2] + "[redacted]"
			}
			return "[redacted]"
		})
	}
	s = strings.TrimSpace(s)
	if len(s) > webrtcReasonDetailMax {
		// Cut on a rune boundary — an error chain can carry a URL-escaped or
		// non-ASCII fragment, and a half rune would make the JSON encoder emit
		// U+FFFD in the middle of the operator's only clue.
		cut := webrtcReasonDetailMax - len("…")
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}
