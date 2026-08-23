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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

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

// captureRegistry is the process-wide, per-agent WebRTC CaptureSession
// registry, shared between the main browser WS handler (which creates
// sessions on a viewer's browser_webrtc_offer) and the capture-ingest WS
// handler (which must locate a session purely from an inbound
// browser_capture_hello's token — the hello frame carries no agent_id).
type captureRegistry struct {
	mu       sync.Mutex
	sessions map[string]*browser.CaptureSession // keyed by agentID
}

func newCaptureRegistry() *captureRegistry {
	return &captureRegistry{sessions: make(map[string]*browser.CaptureSession)}
}

func (r *captureRegistry) set(agentID string, cs *browser.CaptureSession) {
	r.mu.Lock()
	r.sessions[agentID] = cs
	r.mu.Unlock()
}

// removeIfCurrent drops agentID's entry iff it still equals cs — guards
// against a stopped/superseded session's cleanup clobbering a newer one
// (same discipline as BrowserManager.ClearCaptureSession).
func (r *captureRegistry) removeIfCurrent(agentID string, cs *browser.CaptureSession) {
	r.mu.Lock()
	if r.sessions[agentID] == cs {
		delete(r.sessions, agentID)
	}
	r.mu.Unlock()
}

// otherSessions returns a snapshot of every registered capture session whose
// agentID differs from exclude — the ADR-048 condition-2 fence uses it to
// detect a genuinely conflicting (actively-viewed) capture and to supersede
// viewerless leftovers. Stopped sessions are removed from the registry by
// their onStopped hook (see ensureCaptureSession), so entries here are
// live-or-gracing sessions only.
func (r *captureRegistry) otherSessions(exclude string) map[string]*browser.CaptureSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*browser.CaptureSession, len(r.sessions))
	for id, cs := range r.sessions {
		if id != exclude {
			out[id] = cs
		}
	}
	return out
}

// findByToken scans active sessions for one whose minted token matches
// candidateHex (ValidateToken does the actual constant-time compare per
// candidate — wave-plan W2-A item 3). Returns ("", nil) if no session
// matches. A snapshot is taken under the lock and validated outside it so a
// slow/attacker-controlled candidate string can't hold the registry lock.
func (r *captureRegistry) findByToken(candidateHex string) (agentID string, cs *browser.CaptureSession) {
	r.mu.Lock()
	snapshot := make(map[string]*browser.CaptureSession, len(r.sessions))
	for k, v := range r.sessions {
		snapshot[k] = v
	}
	r.mu.Unlock()
	for id, s := range snapshot {
		if s.ValidateToken(candidateHex) {
			return id, s
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

// webrtcViewerConn is the per-viewer entry in BrowserWSHandler.viewerConns
// (fix-wave findings 3 and 7): a concurrent-safe registry the gateway uses
// to reach a WebRTC-attached viewer's main /api/v1/browser/ws connection
// from a goroutine that is NOT that connection's own readLoop — the
// encoder-liveness watchdog (pushing a browser_webrtc_state frame the
// instant the gateway itself detects the stream died, rather than making
// the viewer wait ~5s for its own ICE connection state to notice) and the
// data-channel input sink (surfacing a real input-dispatch error back to
// the driving viewer, mirroring handleInput's sessionErrorStatus) both need
// this. Registered on a successful browser_webrtc_offer (the SAME moment
// browserConnState.webrtc is set), unregistered by detachWebRTCViewer.
type webrtcViewerConn struct {
	wc        *browserWSConn
	sessionID string

	mu         sync.Mutex
	lastErrAt  time.Time
	lastErrMsg string
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

// handleWebRTCOffer processes a browser_webrtc_offer frame (ADR-047 D4). Gate
// ladder, in order: resolve the agent's BrowserManager -> webrtcUnavailableReason
// (WebRTCEnabled -> lite build -> capture-capable, ADR-048 condition 3) ->
// the ADR-048 condition-2 multi-agent capture fence (only when about to
// start a BRAND NEW session) -> ensure+start the agent's capture session ->
// HandleViewerOffer. Every rejection sends a browser_webrtc_state frame with
// available=false and a reason; the connection's session/control-lock
// attachment (handleAttach, already established independently) is never
// touched by any branch here.
//
// Runs on its own goroutine in production (dispatchWebRTCOffer, above), never
// on readLoop's own goroutine — epoch is the value state.beginWebRTCOffer()
// returned at dispatch time, and MUST be threaded through unchanged to the
// commitWebRTCAttachment call below so a stale/superseded generation is
// detected before this call ever mutates connection-shared state. Tests that
// invoke this method directly (bypassing dispatchWebRTCOffer, exercising the
// gate-ladder logic synchronously) pass 0, matching a fresh browserConnState's
// zero-value webrtcEpoch.
func (h *BrowserWSHandler) handleWebRTCOffer(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
	epoch uint64,
) {
	var frame generated.BrowserWebRTCOfferFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_webrtc_offer: invalid frame"),
			dropContext("", viewerID, "webrtc-offer-invalid"))
		return
	}
	if frame.AgentId == "" || frame.Sdp == "" {
		wc.sendCriticalGen(sessionErrorStatus(frame.SessionId, "browser_webrtc_offer: agent_id and sdp are required"),
			dropContext(frame.SessionId, viewerID, "webrtc-offer-missing-fields"))
		return
	}
	sessID := frame.SessionId

	mgr, ok := h.agentLoop.BrowserManagerForAgent(frame.AgentId)
	if !ok {
		wc.sendCriticalGen(sessionErrorStatus(
			sessID,
			fmt.Sprintf(
				"no browser manager for agent %q (browser tools may not be registered for this agent)",
				frame.AgentId,
			),
		),
			dropContext(sessID, viewerID, "webrtc-offer-no-manager"))
		return
	}

	if reason := webrtcUnavailableReason(cfg, mgr); reason != "" {
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, reason)
		return
	}

	// ADR-048 condition 2 (fix 6, re-scoped after the 2026-07-18 UAT
	// "video black when the agent drives" root-cause): the multi-agent
	// capture conflict. The fence used to deny a new capture whenever ANY
	// other agent merely had a live browser session — which made the panel
	// permanently video-less in the most ordinary single-user flow (human
	// tests agent A's panel → chats with agent B whose tools open B's own
	// session → every subsequent panel attach, to EITHER agent, was denied
	// until restart). Two changes make that blanket deny unnecessary:
	// CaptureSession.Start now brings the requesting agent's tab to front
	// before the encoder resolves its capture target (so a NEW capture
	// deterministically binds THIS agent's tab even with other agents'
	// windows present), and the true remaining conflict — one shared Chrome
	// cannot serve two SIMULTANEOUSLY-VIEWED captures whose focus demands
	// fight — is precisely detectable via the capture registry. So: deny
	// only when another agent's capture session is still actively viewed;
	// silently supersede (Stop) viewerless leftovers (grace-period sessions
	// whose panel already detached). Only checked when about to start a
	// BRAND NEW capture session for this agent (mgr.CaptureSession() ==
	// nil); a second viewer offer for an agent whose session is already
	// running just joins it, which is always fine regardless of what other
	// agents are doing.
	// Fix-wave HIGH (fix 3a/3b): the fence-check-then-ensure sequence is made
	// atomic by h.captureFenceMu (see its doc comment on BrowserWSHandler for
	// the TOCTOU this closes), and a session still inside its own Start()
	// call is skipped rather than superseded (fix 3b: Stop()-ing it
	// mid-startup is SAFE — Start's own "stopped while starting" branch
	// tears down cleanly — but not FAIR: it would let an unrelated agent's
	// later offer abort an already-in-flight capture before its own first
	// viewer even had a chance to register, since AddViewer only happens
	// once Start AND HandleViewerOffer both succeed, so a starting session
	// always LOOKS viewerless to ViewerCount()).
	h.captureFenceMu.Lock()
	if mgr.CaptureSession() == nil {
		for other, otherCS := range h.captures.otherSessions(frame.AgentId) {
			if otherCS.IsStarting() {
				slog.Info("browser-webrtc: skipping supersede of another agent's still-starting capture session",
					"agent_id", frame.AgentId, "starting_agent_id", other)
				continue
			}
			if otherCS.ViewerCount() > 0 {
				h.captureFenceMu.Unlock()
				slog.Warn(
					"browser-webrtc: capture denied — another agent's capture session is actively viewed (ADR-048 condition 2, shared default-context capture is single-capture in v1)",
					"agent_id",
					frame.AgentId,
					"other_live_agent_id",
					other,
				)
				h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "multi_agent_capture_denied")
				h.auditStream(
					userID,
					frame.AgentId,
					audit.SeverityWarn,
					audit.EventBrowserWebRTCStreamStartFailed,
					map[string]any{
						"session_id":     sessID,
						"reason":         "multi_agent_capture_denied",
						"other_agent_id": other,
					},
				)
				return
			}
			slog.Info("browser-webrtc: superseding another agent's viewerless capture session",
				"agent_id", frame.AgentId, "superseded_agent_id", other)
			// FIX WAVE A finding 3: Stop() is synchronous and includes a
			// loopback ingest write bounded at up to captureIngestWriteTimeout
			// (5s, capture_session.go) — calling it INLINE here would hold
			// h.captureFenceMu (a single, process-wide mutex every agent's
			// offer must acquire) for that entire duration, letting one
			// agent's stale/viewerless-session teardown block a brand-new,
			// wholly UNRELATED agent's connect for up to 5s per superseded
			// session. Stop() is documented idempotent and operates on a
			// completely separate CaptureSession (its own encoder tab, its
			// own relay) from the one this offer is about to start — nothing
			// about the fence's single-capture invariant requires WAITING for
			// this teardown to finish before proceeding; it only requires
			// deciding to supersede it, which already happened above under
			// the lock. Firing it off-lock lets it complete in its own time
			// without gating anyone else's fence acquisition.
			go otherCS.Stop()
		}
	}

	cs, err := h.ensureCaptureSession(mgr, frame.AgentId, cfg)
	h.captureFenceMu.Unlock()
	if err != nil {
		slog.Error("browser-webrtc: ensure capture session failed", "error", err, "agent_id", frame.AgentId)
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "error")
		return
	}

	ingestURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", cfg.Gateway.Port)
	justStarted, startErr := cs.Start(context.Background(), ingestURL)
	if startErr != nil {
		slog.Error("browser-webrtc: capture session start failed", "error", startErr, "agent_id", frame.AgentId)
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "error")
		h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCStreamStartFailed,
			map[string]any{"session_id": sessID, "error": startErr.Error()})
		// Fix 1 (sticky failed capture Start): Stop() is idempotent and its
		// onStopped hook (ensureCaptureSession, below) clears BOTH the
		// manager's CaptureSession reference and h.captures' token-lookup
		// entry, so the NEXT offer for this agent builds a fresh session
		// instead of reusing this permanently-broken one forever.
		cs.Stop()
		return
	}
	if justStarted {
		h.auditStream(userID, frame.AgentId, audit.SeverityInfo, audit.EventBrowserWebRTCStreamStarted,
			map[string]any{"session_id": sessID})
	}

	// gen/viewerHandle make this offer's cleanup IDENTITY-AWARE. Both
	// CaptureSession.viewers and the relay's own registry are keyed by
	// viewerID alone, and ensureCaptureSession memoizes, so two offers on
	// this connection for the same agent share one CaptureSession and one
	// registry key. A viewerID-only cleanup from a SUPERSEDED offer would
	// therefore close whatever PeerConnection currently sits at that key —
	// i.e. the NEWER, already-committed offer's live connection — and delete
	// the viewers entry it depends on, dropping ViewerCount() to 0 and arming
	// the 60s captureGracePeriod stop timer while that viewer is actively
	// watching. Symptom: WebRTC connects, then spontaneously drops to picture
	// mode seconds-to-a-minute later, with logs showing only an unrelated
	// supersede/grace-timer trail. CleanupViewerOffer no-ops unless the
	// registered entry is the one THIS offer created — the same identity
	// discipline removeViewer already applies on the ICE-eviction path.
	gen := cs.AddViewer(viewerID)
	answer, viewerHandle, offerErr := cs.HandleViewerOffer(viewerID, frame.Sdp, gen)
	if offerErr != nil {
		// Fix 8: a broken/aborted viewer PeerConnection must not stay
		// registered on the relay — CleanupViewerOffer is idempotent-safe (a
		// no-op if HandleViewerOffer never got far enough to register one).
		cs.CleanupViewerOffer(viewerHandle)
		// 2026-07-28 incident fix: classify this SPECIFIC failure mode
		// (errors.Is against webrtc.ErrNoIngestVideoTrack — waitForTracks
		// gave up before the encoder's video track ever arrived) separately
		// from every other HandleViewerOffer error, so an operator reading
		// logs/audit doesn't have to parse the nested error string to tell
		// "the capture pipeline just hadn't produced a frame in time" (often
		// transient, see waitForTracksTimeout's doc comment in
		// pkg/tools/browser/webrtc/ingest.go) apart from a real defect.
		reason := "error"
		if errors.Is(offerErr, webrtc.ErrNoIngestVideoTrack) {
			reason = "ingest_timeout"
		}
		slog.Warn(
			"browser-webrtc: viewer offer failed",
			"error",
			offerErr,
			"agent_id",
			frame.AgentId,
			"viewer_id",
			viewerID,
			"reason",
			reason,
		)
		h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCViewerOfferFailed,
			map[string]any{"session_id": sessID, "viewer_id": viewerID, "reason": reason, "error": offerErr.Error()})
		// Send the CLASSIFIED reason, not the literal "error". This line used
		// to hardcode "error" while the reason computed six lines above went
		// only to the log and the audit event — so the one surface a human
		// actually reads (the UI, and any E2E assertion quoting it) was
		// strictly LESS informative than the log line beside it. That cost a
		// full investigation: an ingest timeout, which has a specific and
		// actionable cause, presented as the generic "reported an error
		// starting video". ADR-061 deleted the silent JPEG fallback so a
		// WebRTC failure would be visible; a visible failure that names the
		// wrong cause is the same defect one level down.
		h.sendWebRTCState(wc, sessID, viewerID, true, false, false, reason)
		return
	}

	// FIX WAVE A finding 1: commit only if nothing invalidated this offer's
	// epoch while HandleViewerOffer was negotiating above (a newer offer on
	// this same connection, an explicit browser_detach, or the connection
	// itself closing). A stale commit here would attach a viewer state that
	// nothing will ever tear down through the normal detach path (readLoop's
	// cleanup already ran, or is about to run against a DIFFERENT, newer
	// attachment) — tear down what THIS offer just built instead, mirroring
	// detachWebRTCViewer's own teardown exactly.
	if !state.commitWebRTCAttachment(
		epoch,
		&webrtcAttachment{agentID: frame.AgentId, capture: cs, handle: viewerHandle},
	) {
		slog.Info(
			"browser-webrtc: offer superseded before commit (a newer offer, a detach, or the connection closing "+
				"arrived first) — tearing down the viewer this offer just registered",
			"agent_id", frame.AgentId,
			"viewer_id", viewerID,
			"session_id", sessID,
		)
		// Identity-aware, for the same reason as the offer-failure path above:
		// if the newer offer that superseded us has ALREADY registered its own
		// PeerConnection under this viewerID, a viewerID-keyed teardown here
		// would kill that live connection instead of ours.
		cs.CleanupViewerOffer(viewerHandle)
		return
	}
	h.registerWebRTCViewerConn(viewerID, wc, sessID)

	// Cold-start ordering fix (live UAT 2026-07-31, extended by F2, external
	// review 2026-08-13): see applyColdStartRecapture's doc comment for the
	// full mechanism — geometry AND scale.
	h.applyColdStartRecapture(state, cs)

	stats := cs.Stats()
	wc.sendCriticalGen(generated.BrowserWebRTCAnswerFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcAnswer),
		Sdp:       answer,
		SessionId: &sessID,
	}, dropContext(sessID, viewerID, "webrtc-answer"))
	h.sendWebRTCState(wc, sessID, viewerID, true, true, stats.HasAudio, "")
	// ADR-061 / round-2 F6: if the shared media socket fell back off the
	// operator's declared UDP port, say so IN THE PANEL. The answer above is
	// honest — media really is being offered — but on a hosted install it can
	// never reach a remote viewer, and only the panel reaches the person who
	// can fix that. See notifyMediaPortDegraded for why it rides
	// browser_status rather than browser_webrtc_state, and why it is here
	// rather than at attach.
	h.notifyMediaPortDegraded(wc, sessID, viewerID)
}

// applyColdStartRecapture corrects a just-committed WebRTC attachment's
// capture geometry AND scale against whatever a browser_viewport frame
// already told this connection, for whichever of the two (or both) arrived
// before there was an attachment to receive them directly.
//
// Geometry (live UAT 2026-07-31): the panel's viewport frame routinely
// applies BEFORE this attachment exists — handleViewport's recapture is
// gated on peekWebRTCAttachment and silently skips — so the capture spins up
// at launch geometry and nothing ever corrects it (the SPA won't re-send an
// unchanged size). If a CDP-verified viewport is already cached for the live
// tab, issue the corrective recapture with those dims, so the stream the
// viewer is about to receive is built at the panel's real shape. Warm path
// cost: one extra rebuild during attach churn when the capture was already
// correct — accepted; the encoder converges on the expected dims either way.
//
// Scale (F2 fix, external review 2026-08-13): the SAME timing gap drops
// device_scale_factor, not just geometry — handleViewport's direct
// att.capture.SetCaptureScale call is gated on the identical
// peekWebRTCAttachment() check, so a cold panel's first viewport frame
// (often the ONLY one it ever sends, per the SPA's lastSentViewportRef
// dedup) left the Retina-blur fix permanently inert until a manual resize.
// pendingViewportScale() carries whatever handleViewport remembered
// regardless of attachment timing (browser_ws.go); applied here the instant
// an attachment exists to receive it. Deliberately independent of the
// geometry branch below — a scale-only correction (no CDP-verified viewport
// cached yet) still forces a recapture so the encoder picks up the new
// capture_scale, mirroring handleViewport's own "push a recapture so the
// scale takes effect even when the CDP resize handle is not" fallback
// (browser_ws.go's SetViewport-failure branch).
func (h *BrowserWSHandler) applyColdStartRecapture(state *browserConnState, cs *browser.CaptureSession) {
	scale := state.pendingViewportScale()
	if scale > 0 {
		cs.SetCaptureScale(scale)
	}
	// Snapshot under attachMu (FIX WAVE B finding A): this runs on the offer's
	// own background goroutine while handleAttach may be committing
	// state.mgr from the connection's worker. It was ALREADY a data race
	// before that change — offers moved off readLoop first, so this read
	// raced handleAttach's inline write — and the accessor closes it.
	mgr, _ := state.attachment()
	if mgr == nil {
		return
	}
	if w, hgt, ok := mgr.Live().CSSViewport(browser.DefaultSessionID); ok {
		cs.RecaptureAt(w, hgt)
	} else if scale > 0 {
		cs.Recapture()
	}
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

// registerWebRTCViewerConn / unregisterWebRTCViewerConn maintain
// h.viewerConns (see webrtcViewerConn's doc comment) — the SAME lifecycle
// as browserConnState.webrtc: registered once handleWebRTCOffer succeeds,
// unregistered by detachWebRTCViewer.
func (h *BrowserWSHandler) registerWebRTCViewerConn(viewerID string, wc *browserWSConn, sessionID string) {
	h.viewerConns.Store(viewerID, &webrtcViewerConn{wc: wc, sessionID: sessionID})
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
func (h *BrowserWSHandler) notifyViewersStreamStopped(viewerIDs []string) {
	for _, vid := range viewerIDs {
		v, ok := h.viewerConns.Load(vid)
		if !ok {
			continue
		}
		vc, ok := v.(*webrtcViewerConn)
		if !ok {
			continue
		}
		h.sendWebRTCState(vc.wc, vc.sessionID, vid, false, false, false, "error")
	}
}

// announceWebRTCAvailability sends the initial post-attach
// browser_webrtc_state frame (ADR-047 D4 / wave-plan W2-B: "sent after attach
// and again on any availability change"). The SPA's state machine only sends
// its browser_webrtc_offer after receiving available:true, and
// handleWebRTCOffer only replies with a state frame — so without this
// announcement neither side ever moves and the panel silently never offers
// WebRTC at all (W3 e2e finding). This is an announcement, not an authorization:
// the offer-side gate ladder in handleWebRTCOffer re-validates every gate
// when the offer actually arrives.
func (h *BrowserWSHandler) announceWebRTCAvailability(
	wc *browserWSConn,
	mgr *browser.BrowserManager,
	sessID, viewerID string,
	cfg *config.Config,
) {
	if reason := webrtcUnavailableReason(cfg, mgr); reason != "" {
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, reason)
		return
	}
	// Hand the viewer its ICE servers HERE, with the "you may offer" frame:
	// the SPA needs them before it builds its PeerConnection, and credentials
	// are minted per viewer with a bounded lifetime (ADR-062 tier 3).
	h.sendWebRTCStateWithICE(wc, sessID, viewerID, true, false, false, "", h.iceServersForViewer(cfg, viewerID))
}

// sendWebRTCState builds and sends a browser_webrtc_state frame.
func (h *BrowserWSHandler) sendWebRTCState(
	wc *browserWSConn,
	sessID, viewerID string,
	available, active, hasAudio bool,
	reason string,
) {
	h.sendWebRTCStateWithICE(wc, sessID, viewerID, available, active, hasAudio, reason, nil)
}

// sendWebRTCStateWithICE is sendWebRTCState plus the viewer's ICE servers
// (ADR-062 tier 3). Split rather than adding a parameter to every call site
// because only the availability announcement carries them: they are what the
// SPA needs BEFORE it builds its PeerConnection, and a failure frame sent
// afterwards has nothing useful to attach.
func (h *BrowserWSHandler) sendWebRTCStateWithICE(
	wc *browserWSConn,
	sessID, viewerID string,
	available, active, hasAudio bool,
	reason string,
	iceServers []iceServerEntry,
) {
	f := generated.BrowserWebRTCStateFrame{
		Type:       string(generated.WsFrameTypeBrowserWebrtcState),
		Available:  available,
		SessionId:  &sessID,
		IceServers: iceServers,
	}
	if active {
		f.Active = boolPtr(true)
	}
	if hasAudio {
		f.HasAudio = boolPtr(true)
	}
	if reason != "" {
		f.Reason = &reason
	}
	wc.sendCriticalGen(f, dropContext(sessID, viewerID, "webrtc-state:"+reason))
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

// encoderLivenessVideoStallTicks (fix-wave HIGH, reviewer 4 finding 1): the
// ping-recency check alone can be defeated by a dead-capture encoder whose
// ping BEACON is independent of its actual tabCapture/RTP pipeline
// (encoder.js runs them as separate loops) — a wedged capture with a live
// ping keeps this watchdog satisfied forever while a viewer sees a frozen
// picture. This tracks Stats().VideoPackets across ticks instead: if a
// viewer is attached (ViewerCount() > 0) but the packet count hasn't moved
// for this many CONSECUTIVE ticks, treat it as stale exactly like a missed
// ping beacon. Two (not one) tolerates the single-tick window right after a
// viewer first attaches, before any packet has actually been forwarded yet —
// even though the e2e evidence this fix is based on shows VP8 keeps emitting
// packets on completely static content at ~30fps, so in practice one
// no-progress tick already implies a dead capture; two ticks costs at most
// one extra encoderLivenessCheckInterval of latency for the added safety
// margin.
//
// Raised 2 -> 6 (2026-07-30 UAT). The premise quoted above — "VP8 keeps
// emitting packets on completely static content at ~30fps" — is FALSE for
// this capture path, and measured live traffic disproves it: on a static
// Google page the ingest leg forwarded ~2 video packets/sec against ~50
// audio packets/sec, i.e. exactly the 1 Hz blinking text cursor and nothing
// else. tabCapture is REPAINT-driven, not clock-driven: a page with nothing
// animating (no cursor, no video, no spinner) legitimately produces ZERO
// frames, indefinitely, while the capture is perfectly healthy. At 2 ticks
// this watchdog would call cs.Stop() on such a page after only 20s and kill
// a working session — a false positive that reads to the user exactly like
// the freeze we are fixing. 6 ticks (60s) keeps the wedged-capture-with-
// live-ping detection this check exists for while making that false
// positive far less likely. It does NOT eliminate it: a genuinely static
// page still trips this at 60s. The durable fix is to distinguish "no
// frames because nothing repainted" from "no frames because the pipeline is
// wedged" (e.g. an encoder-side frame-production counter rather than an
// RTP-egress counter), which is out of scope here and is why this remains a
// tick-count tuning rather than a redesign.
const encoderLivenessVideoStallTicks = 6

// watchEncoderLiveness runs for the lifetime of one CaptureSession (fix 3),
// exiting as soon as cs.Done() closes (Stop(), from ANY cause) so at most
// one watchdog goroutine is ever live per session. Started exactly once per
// session by ensureCaptureSession's newFn — the same "exactly once per
// session" discipline EnsureCaptureSession already guarantees for newFn
// itself. Stops the session on either of two independent staleness signals:
// no ping beacon within staleAfter (original fix 3), OR no video RTP
// progress across encoderLivenessVideoStallTicks consecutive checks while a
// viewer is attached (fix-wave HIGH addition, see that const's doc comment).
//
// checkInterval/staleAfter are passed in by the caller rather than read from
// the encoderLivenessCheckInterval/encoderLivenessStaleAfter package vars
// IN HERE, and this is load-bearing, not a style choice. An earlier version
// of this function snapshotted those vars into locals once, at function
// entry, reasoning that a single read (vs. re-reading every tick) was safe
// once cs.Stop() started the goroutine's shutdown. That reasoning had a gap:
// the snapshot read still happened on THIS goroutine, which the caller only
// ever fire-and-forgets (`go h.watchEncoderLiveness(...)`) — nothing
// guarantees this goroutine reaches its first statement before the SAME
// test returns, let alone before a LATER test's setup overwrites the
// package vars for its own shrunk-timing scenario. That is exactly what
// happened under `go test -race`: a capture session started by one test
// left its watchdog goroutine scheduled-but-not-yet-run, and a later test's
// bare `encoderLivenessCheckInterval = 5*time.Millisecond` write raced this
// goroutine's still-pending entry-snapshot read (WARNING: DATA RACE,
// browser_webrtc.go:749/750 vs browser_webrtc_fixwave2_test.go:274/275,
// TestWatchEncoderLiveness_StopsSession_WhenVideoPacketsFrozenDespiteFreshPings).
// Moving the read to the CALL SITE closes this for good: Go evaluates a `go`
// statement's arguments on the CALLING goroutine, synchronously, before the
// new goroutine is even spawned — so every caller below reads the package
// vars (or, for the production call site, the vars stay hard-coded live
// values with no test ever touching them concurrently) on a goroutine whose
// ordering relative to the next test IS already established by the normal
// sequential-test happens-before chain. This function itself never touches
// the package vars at all, so no lifetime of the watchdog goroutine — however
// long a slow CI runner leaves it scheduled — can race a later test again.
func (h *BrowserWSHandler) watchEncoderLiveness(cs *browser.CaptureSession, agentID string, checkInterval, staleAfter time.Duration) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	var (
		lastVideoPackets int64
		haveBaseline     bool
		stallTicks       int
	)

	for {
		select {
		case <-cs.Done():
			return
		case <-ticker.C:
			stats := cs.Stats()
			if cs.ViewerCount() > 0 {
				if haveBaseline && stats.VideoPackets == lastVideoPackets {
					stallTicks++
				} else {
					stallTicks = 0
				}
				lastVideoPackets = stats.VideoPackets
				haveBaseline = true
			} else {
				// No viewer attached — VideoPackets naturally not
				// progressing tells us nothing about encoder health; reset
				// so a viewer that (re)attaches later gets a fresh baseline
				// instead of an immediate false-positive stale verdict.
				stallTicks = 0
				haveBaseline = false
			}
			if stallTicks >= encoderLivenessVideoStallTicks {
				slog.Warn(
					"browser-webrtc: encoder liveness watchdog — video RTP has not advanced across consecutive checks with an attached viewer, stopping capture session",
					"agent_id",
					agentID,
					"video_packets",
					stats.VideoPackets,
					"stall_ticks",
					stallTicks,
					"check_interval",
					checkInterval,
				)
				cs.Stop()
				return
			}

			last := cs.LastPingAt()
			if last.IsZero() {
				continue // encoder hasn't bound the ingest connection yet
			}
			if time.Since(last) > staleAfter {
				slog.Warn(
					"browser-webrtc: encoder liveness watchdog — no ping beacon received, stopping capture session",
					"agent_id",
					agentID,
					"last_ping_at",
					last,
					"stale_after",
					staleAfter,
				)
				cs.Stop()
				return
			}
		}
	}
}

// ensureCaptureSession get-or-creates agentID's CaptureSession (one active
// stream per agent, wave-plan W2-A item 4), registering it in h.captures so
// the ingest WS can find it by token, wiring SetOnStopped to remove it from
// both the registry and the manager once it stops AND push a
// browser_webrtc_state to any still-attached viewers (fix 3), and starting
// the encoder-liveness watchdog (fix 3).
func (h *BrowserWSHandler) ensureCaptureSession(
	mgr *browser.BrowserManager,
	agentID string,
	cfg *config.Config,
) (*browser.CaptureSession, error) {
	return mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) {
		webrtcCfg := webrtc.Config{
			StunServer: cfg.Tools.Browser.WebRTCStunServer,
			MediaConn:  h.sharedMediaConn(cfg),
			MediaTCP:   h.sharedMediaTCP(cfg),
			PublicIPs:  resolveWebRTCPublicIPs(cfg),
		}
		sink := h.webrtcInputSink(mgr, cfg)
		logf := webrtcRelayLogf(agentID)
		cs, err := browser.NewCaptureSession(mgr, agentID, webrtcCfg, sink, logf)
		if err != nil {
			return nil, err
		}
		h.captures.set(agentID, cs)
		cs.SetOnStopped(func() {
			h.captures.removeIfCurrent(agentID, cs)
			audit.Emit(context.Background(), h.agentLoop.AuditLogger(), audit.EventBrowserWebRTCStreamStopped,
				audit.SeverityInfo, map[string]any{"agent_id": agentID})
			h.notifyViewersStreamStopped(cs.ViewerIDs())
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

// webrtcInputSink builds the webrtc.InputSink for one agent's CaptureSession:
// schema-validates when enabled (fix-wave HIGH: parity with the WS input
// path, see below), parses as generated.BrowserInputFrame (drop+log
// invalid), convert via the SAME browserInputFrameToLiveInput helper
// handleInput uses (wave-plan W2-A item 4 — "convert EXACTLY like
// browser_ws.go handleInput does"), then dispatch through the identical
// controller-lock/SSRF/rate-limit gate (browser.LiveViewRegistry.Input) the
// WS input path uses. A method on h (fix 7 — was a package-level func) so a
// real dispatch error can be surfaced back to the driving viewer's main WS
// connection, mirroring handleInput's sessionErrorStatus — see
// surfaceWebRTCInputError. cfg is captured once at CaptureSession-creation
// time (ensureCaptureSession's closure), the same lifetime as webrtcRelayLogf's
// captured agentID — matching existing precedent in this file.
//
// Fix-wave HIGH (security F1 / contracts N1): the main /api/v1/browser/ws
// path schema-validates every inbound browser_input frame BEFORE dispatch
// (browser_ws.go's readLoop, gated on cfg.Gateway.ValidateInbound) — this
// data-channel path previously had no equivalent, relying on bare
// json.Unmarshal alone, which enforces no enum/maxLength/modifiers
// constraints at all. ValidateInboundFrameJSON("BrowserInputFrame", raw)
// closes that parity gap using the exact same schema the WS path validates
// against.
func (h *BrowserWSHandler) webrtcInputSink(mgr *browser.BrowserManager, cfg *config.Config) webrtc.InputSink {
	return func(viewerID string, raw []byte) {
		if cfg.Gateway.ValidateInbound {
			if errMsg, serverErr := ValidateInboundFrameJSON("BrowserInputFrame", raw); errMsg != "" {
				if serverErr {
					slog.Debug(
						"browser-webrtc: inbound schema unavailable, dropping input data-channel frame",
						"viewer_id",
						viewerID,
					)
				} else {
					slog.Debug("browser-webrtc: input data-channel frame schema validation failed, dropping",
						"error", errMsg, "viewer_id", viewerID)
				}
				return
			}
		}
		var frame generated.BrowserInputFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			slog.Warn("browser-webrtc: dropping invalid input data-channel frame", "error", err, "viewer_id", viewerID)
			return
		}
		in := browserInputFrameToLiveInput(frame)
		if err := mgr.Live().Input(browser.DefaultSessionID, viewerID, in); err != nil {
			if browser.IsBenignLiveInputError(err) {
				slog.Debug("browser-webrtc: input rejected (benign)", "error", err, "viewer_id", viewerID)
				// 2026-07-30 UAT: "benign" must not mean "invisible" for the
				// not-controller case. Every other benign rejection is
				// self-correcting; this one is not — the client keeps
				// believing it is driving and keeps sending input the server
				// keeps discarding. The operator's panel read "You're
				// driving" through 448 consecutive rejected inputs, so
				// clicks and keystrokes did nothing at all with no error
				// anywhere the user could see. Push the AUTHORITATIVE
				// control state back to this viewer so its UI corrects
				// itself and the next click re-takes the wheel properly.
				// The not-controller rejection this branch used to repair no
				// longer exists: input is never gated on holding a control
				// lock (see dispatchInput). The previous repair — acquire the
				// lock, then retry the same event — still refused a human's
				// click whenever ANOTHER viewer was attached and holding
				// control, which is exactly how a second panel or a stale
				// automation session left the real user with a dead mouse and
				// keyboard. What remains here is the rate limit, which is
				// self-correcting.
				return
			}
			slog.Warn("browser-webrtc: input dispatch failed", "error", err, "viewer_id", viewerID)
			h.surfaceWebRTCInputError(viewerID, frame.Kind, err)
		}
	}
}

func (h *BrowserWSHandler) surfaceWebRTCInputError(viewerID, kind string, dispatchErr error) {
	v, ok := h.viewerConns.Load(viewerID)
	if !ok {
		return
	}
	vc, ok := v.(*webrtcViewerConn)
	if !ok {
		return
	}
	message := fmt.Sprintf("browser input failed: %s", dispatchErr)

	vc.mu.Lock()
	now := time.Now()
	throttled := !inputKindIsDiscrete(kind) &&
		message == vc.lastErrMsg &&
		now.Sub(vc.lastErrAt) < minInputErrorInterval
	if !throttled {
		vc.lastErrAt = now
		vc.lastErrMsg = message
	}
	vc.mu.Unlock()

	if throttled {
		return
	}
	vc.wc.sendCriticalGen(sessionErrorStatus(vc.sessionID, message),
		dropContext(vc.sessionID, viewerID, "webrtc-input-error"))
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
// gorilla/websocket requires a single writer goroutine per connection;
// sendMu serializes the (low-frequency: offer/answer/control/ping) writes
// this socket carries, so a dedicated writePump/sendCh (as browser_ws.go
// uses for the high-volume screencast socket) would be overkill here.
type captureIngestConn struct {
	conn   *websocket.Conn
	sendMu sync.Mutex
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
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("capture-ingest: marshal frame: %w", err)
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(captureIngestWriteTimeout)); err != nil {
		return fmt.Errorf("capture-ingest: set write deadline: %w", err)
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("capture-ingest: write frame: %w", err)
	}
	return nil
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
// described in encoder.js's own header comment: hello is client -> server
// only (no ack; success is silent), then offer -> answer completes the
// non-trickle SDP exchange, then control{ping} beacons arrive periodically
// and control{recapture|shutdown} are pushed server -> client via
// CaptureSession.BindIngest's send callback (wired below).
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

	agentID, cs := h.captures.findByToken(hello.Token)
	if cs == nil {
		h.auditIngestRejected(remoteAddr, "token_mismatch")
		return
	}
	cs.RecordExtVersion(hello.ExtVersion)

	ic := &captureIngestConn{conn: conn}
	// send additionally carries expectedW/expectedH — the CDP-verified CSS
	// viewport CaptureSession.RecaptureAt wants the encoder to converge on
	// (follow-up to
	// docs/internal/browser-viewport-input-rootcause-2026-07-31.md, measured
	// 2026-07-31). 0 means "absent" (RecaptureAt's convention, mirrored by
	// Recapture()'s own 0,0 call), so ExpectedWidth/ExpectedHeight are only
	// set on the wire frame when the corresponding value is actually
	// positive — an absent field, not a literal 0, is what tells the encoder
	// there is no hint to converge on.
	send := func(action string, reason *string, expectedW, expectedH, maxBitrate int) error {
		frame := generated.BrowserCaptureControlFrame{
			Type:   string(generated.WsFrameTypeBrowserCaptureControl),
			Action: action,
			Reason: reason,
		}
		if expectedW > 0 {
			frame.ExpectedWidth = &expectedW
		}
		if expectedH > 0 {
			frame.ExpectedHeight = &expectedH
		}
		// Physical-pixel capture (blur fix, macOS 2026-08-12): tell the
		// encoder what deviceScaleFactor the tab renders at so it can size
		// its tabCapture constraints in physical pixels.
		//
		// F3 fix (external review 2026-08-13): sent UNCONDITIONALLY on every
		// recapture, including scale == 1. The old `scale > 1` gate meant a
		// viewer dropping from DPR 2 back to DPR 1 (moving to a
		// non-Retina monitor, or a second viewer joining a shared
		// per-agent CaptureSession) never got a capture_scale frame at all —
		// and per this field's own contract doc, "absent" is supposed to
		// mean the same thing as 1, but the encoder side of this fix
		// (owned separately, not in this file) currently treats absent as
		// "leave whatever scale is already running unchanged" — sticky at
		// the old, higher value. CaptureScale() is bounded to
		// [1, maxDeviceScaleFactor] by handleViewport's own range check
		// (browser_ws.go, F10 fix) before it ever reaches SetCaptureScale, so
		// unconditionally sending it here can never exceed this field's own
		// contract maximum of 4.
		if action == "recapture" {
			scale := cs.CaptureScale()
			frame.CaptureScale = &scale
		}
		if maxBitrate > 0 {
			frame.MaxBitrate = &maxBitrate
		}
		return ic.sendJSON(frame)
	}
	var closeOnce sync.Once
	closeConn := func() {
		closeOnce.Do(func() { conn.Close() })
	}
	previousClose, epoch := cs.BindIngest(send, closeConn)
	if previousClose != nil {
		// A second hello with the same token supersedes/closes the old
		// conn (wave-plan W2-A item 3).
		previousClose()
	}
	defer cs.UnbindIngest(epoch)

	conn.SetReadDeadline(time.Now().Add(45 * time.Second)) // covers the 15s ping beacon with margin

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				slog.Debug("capture-ingest: read error", "error", err, "agent_id", agentID)
			}
			return
		}
		conn.SetReadDeadline(time.Now().Add(45 * time.Second))

		var typ wsTypeOnly
		if jsonErr := json.Unmarshal(data, &typ); jsonErr != nil {
			slog.Debug("capture-ingest: dropping frame with unparseable type", "error", jsonErr, "agent_id", agentID)
			continue
		}

		if cfg.Gateway.ValidateInbound {
			if schemaName := captureFrameSchemaName(typ.Type); schemaName != "" {
				if errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data); errMsg != "" {
					if serverErr {
						slog.Error("capture-ingest: inbound schema unavailable, dropping frame",
							"schema", schemaName, "frame_type", typ.Type)
					} else {
						slog.Warn("capture-ingest: inbound frame schema validation failed — dropping",
							"schema", schemaName, "frame_type", typ.Type, "error", errMsg, "agent_id", agentID)
					}
					continue
				}
			}
		}

		switch typ.Type {
		case string(generated.WsFrameTypeBrowserCaptureOffer):
			var offerFrame generated.BrowserCaptureOfferFrame
			if jsonErr := json.Unmarshal(data, &offerFrame); jsonErr != nil {
				slog.Debug(
					"capture-ingest: dropping unparseable browser_capture_offer frame",
					"error",
					jsonErr,
					"agent_id",
					agentID,
				)
				continue
			}
			answer, offerErr := cs.HandleIngestOffer(offerFrame.Sdp)
			if offerErr != nil {
				// Fix 2: previously just `continue`d, leaving the encoder
				// connected with no signal at all that its offer was
				// rejected — it would sit there until its own reconnect
				// watchdog eventually gave up. Send the encoder an explicit
				// ErrorFrame and close the connection so its reconnect
				// logic (encoder.js) restarts the hello->offer cycle
				// immediately instead of waiting out that timer.
				slog.Warn("capture-ingest: ingest offer failed — signaling error to encoder and closing connection",
					"error", offerErr, "agent_id", agentID)
				if sendErr := ic.sendJSON(generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: fmt.Sprintf("capture ingest offer failed: %s", offerErr),
				}); sendErr != nil {
					slog.Warn(
						"capture-ingest: send error frame to encoder failed",
						"error",
						sendErr,
						"agent_id",
						agentID,
					)
				}
				return
			}
			if sendErr := ic.sendJSON(generated.BrowserCaptureAnswerFrame{
				Type: string(generated.WsFrameTypeBrowserCaptureAnswer),
				Sdp:  answer,
			}); sendErr != nil {
				slog.Warn("capture-ingest: send answer failed", "error", sendErr, "agent_id", agentID)
			}
		case string(generated.WsFrameTypeBrowserCaptureControl):
			var ctrlFrame generated.BrowserCaptureControlFrame
			if jsonErr := json.Unmarshal(data, &ctrlFrame); jsonErr != nil {
				slog.Debug(
					"capture-ingest: dropping unparseable browser_capture_control frame",
					"error",
					jsonErr,
					"agent_id",
					agentID,
				)
				continue
			}
			if ctrlFrame.Action == "ping" {
				cs.RecordPing()
				// Round-2 finding F7, gateway half. The encoder rides the
				// liveness ping to report a quality-adaptation failure it
				// cannot otherwise surface (a rejected setParameters dies in
				// a devtools console nobody opens). A ping carrying a reason
				// is that report; a bare ping is the ordinary beacon and
				// stays silent. WARN, not INFO, because this project's
				// production log level is warn — at INFO the cause would
				// reach the process and still be invisible to the operator,
				// which is the exact defect F7 names.
				if ctrlFrame.Reason != nil && *ctrlFrame.Reason != "" {
					slog.Warn("capture-ingest: encoder reported a stream-quality failure",
						"reason", *ctrlFrame.Reason, "agent_id", agentID)
				}
			} else {
				slog.Debug("capture-ingest: unexpected control action from encoder, ignoring",
					"action", ctrlFrame.Action, "agent_id", agentID)
			}
		default:
			slog.Debug("capture-ingest: unknown frame type, ignoring", "frame_type", typ.Type, "agent_id", agentID)
		}
	}
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

// notice renders the operator-facing sentence sent to the live-browser panel
// as a browser_status(error) message.
//
// Constraints it must respect, both load-bearing:
//   - BrowserStatusFrame.message is maxLength 512 in
//     contracts/components/schemas/BrowserStatusFrame.yaml. An over-length
//     message is dropped outright by the SPA's zod edge validation, which
//     would turn this fix back into the silence it exists to remove —
//     TestMediaPortFallbackNotice_FitsContractMaxLength pins it.
//   - It must not collide with any pattern in the SPA's
//     translateBrowserErrorMessage (src/lib/browserLiveWs.ts), which rewrites
//     recognised Go-internal strings into plain language and passes
//     everything else through verbatim. This copy is already plain language,
//     so it must stay OUT of those patterns — notably no "blocked", no
//     "could not resolve", no "browser_attach:"-style prefix.
func (s mediaPortFallbackState) notice() string {
	if s.bound > 0 {
		return fmt.Sprintf(
			"Live video is running on UDP port %d, not port %d from your configuration, because port %d could "+
				"not be bound. Video works for a viewer on this machine or your LAN, but a remote viewer "+
				"will get no picture: a hosted install only routes the port you declared. Free port %d (a "+
				"second Omnipus on this host is the usual cause), or set "+
				"tools.browser.webrtc_media_udp_port to a port your provider routes, then restart.",
			s.bound, s.configured, s.configured, s.configured,
		)
	}
	return fmt.Sprintf(
		"Live video is running on a random UDP port: port %d from your configuration, and every port up to "+
			"%d, was unavailable. Video works for a viewer on this machine or your LAN, but a remote viewer "+
			"will get no picture, because a hosted install only routes the port you declared. Free port %d, "+
			"or set tools.browser.webrtc_media_udp_port to a port your provider routes, then restart.",
		s.configured, s.lastProbed, s.configured,
	)
}

// mediaPortFallbackNotice returns the operator-facing degradation sentence,
// or "" when the configured media port was bound exactly (or fixed-port
// media is not configured at all — the laptop default, where there is
// nothing to warn about).
func (h *BrowserWSHandler) mediaPortFallbackNotice() string {
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.mediaPortFallback == nil {
		return ""
	}
	return h.mediaPortFallback.notice()
}

// notifyMediaPortDegraded tells THIS viewer, in the panel, that live video is
// not on the port the operator declared — ADR-061's rule that a degradation
// must name its cause to the user, not only in a log.
//
// Sent as browser_status(error) rather than browser_webrtc_state, and that is
// deliberate on both counts:
//   - browser_webrtc_state.reason is a CLOSED enum (disabled / not_capable /
//     lite_build / error) with additionalProperties:false, and its `reason`
//     is meaningful only alongside available:false. Reporting this as
//     available:false would be a lie — media IS available, and on a laptop it
//     works perfectly — and it would stop the SPA from ever sending an offer,
//     breaking the local dev experience this fallback exists to preserve.
//   - browser_status(error) carries free-text the SPA already renders as a
//     persistent strip UNDER a playing video (BrowserLiveView's "Persistent
//     error strip"), which is exactly the semantics wanted: video keeps
//     working where it can, and the reason it will not work remotely is on
//     screen the whole time.
//
// Called on the offer SUCCESS path (not at attach) because that is the
// earliest point at which the answer is true: sharedMediaConn is bound lazily
// by ensureCaptureSession, so on the very first viewer the degradation is not
// yet known when browser_attach is answered. Sending it after the answer also
// keeps a laptop's ordinary "Connecting…" empty state honest instead of
// replacing it with an error before any video attempt has been made.
//
// No-op — no frame at all — when nothing degraded, so the ordinary install
// (fixed port bound exactly, or not configured) is completely unaffected.
func (h *BrowserWSHandler) notifyMediaPortDegraded(wc *browserWSConn, sessID, viewerID string) {
	notice := strings.TrimSpace(h.mediaPortFallbackNotice() + " " + h.iceTCPUnavailableNotice() + " " + h.turnUnavailableNotice())
	if notice == "" {
		return
	}
	wc.sendCriticalGen(sessionErrorStatus(sessID, notice),
		dropContext(sessID, viewerID, "media-port-fallback"))
}

// iceTCPUnavailableNotice is the operator-facing sentence for a configured
// ICE-TCP port that could not be bound. Same reasoning as
// mediaPortFallbackState.notice: the person who has to free the port or pick
// another one is the operator, and a log line is not a surface they watch.
// Empty when ICE-TCP is not configured or bound fine, so an ordinary install
// says nothing. Deliberately short: BrowserStatusFrame.message is capped at
// 512 and this may be appended to the UDP notice.
func (h *BrowserWSHandler) iceTCPUnavailableNotice() string {
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.mediaTCPBindErr == nil {
		return ""
	}
	return "Live video could not open the TCP media port you configured " +
		"(tools.browser.webrtc_media_tcp_port), so viewers whose network blocks UDP have no fallback. " +
		"Free that port or choose one your provider routes, then restart."
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
	if h.mediaConn != nil {
		return h.mediaConn
	}
	bindAddr := strings.TrimSpace(cfg.Tools.Browser.WebRTCMediaUDPBindAddress)

	conn, err := net.ListenPacket("udp", mediaUDPAddr(bindAddr, port))
	if err == nil {
		slog.Info("browser-webrtc: fixed media UDP socket bound", "addr", conn.LocalAddr().String())
		h.mediaConn = conn
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

// turnUnavailableNotice reports a configured-but-failed relay to the operator,
// same discipline as the media sockets: a log line is not a surface.
func (h *BrowserWSHandler) turnUnavailableNotice() string {
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	if h.turnStartErr == nil {
		return ""
	}
	return "Live video could not start the TURN relay you configured " +
		"(tools.browser.webrtc_turn_udp_port), so viewers that cannot reach the media port directly have no path. " +
		"Free that port or choose one your provider routes, then restart."
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
	return ln
}
