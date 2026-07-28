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
// path layered over the existing JPEG live-browser view (browser_ws.go).
// Two pieces:
//
//  1. Viewer signaling on the EXISTING /api/v1/browser/ws socket:
//     handleWebRTCOffer, dispatched from browser_ws.go's readLoop on a
//     browser_webrtc_offer frame (ADR-047 D4: signaling rides the existing
//     authenticated WS, contract-first, non-trickle).
//  2. The loopback-only /api/v1/browser/capture-ingest WS
//     (captureIngestWSHandler): the gateway-owned encoder page's ingest leg
//     (ADR-047 D6: token-authorized, never a URL param).
//
// Per ADR-047 D3, EVERY failure path here degrades to a browser_webrtc_state
// frame (available/active=false + a reason) — it NEVER breaks the JPEG
// browser_screencast path, which resumes whenever any viewer still needs it
// regardless of WebRTC's fate (CaptureSession.ReconcileScreencast pauses the
// JPEG screencast only while EVERY attached JPEG viewer is also covered by
// an active WebRTC stream, and resumes it the instant that stops being
// true — see capture_session.go — so "keeps running" is a dynamic, per-
// viewer-coverage guarantee, not a literal "always on" one).
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
// readLoop's own cleanup defer tore down BOTH this WebRTC attempt AND the
// independent JPEG browser_screencast fallback with it — directly violating
// ADR-047 D3 ("WebRTC failing must never take the JPEG fallback down with
// it").
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
// available=false and a reason; the JPEG screencast (handleAttach, already
// running independently) is never touched by any branch here.
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
				h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "error")
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
		h.sendWebRTCState(wc, sessID, viewerID, true, false, false, "error")
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

	stats := cs.Stats()
	wc.sendCriticalGen(generated.BrowserWebRTCAnswerFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcAnswer),
		Sdp:       answer,
		SessionId: &sessID,
	}, dropContext(sessID, viewerID, "webrtc-answer"))
	h.sendWebRTCState(wc, sessID, viewerID, true, true, stats.HasAudio, "")
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
// cross-goroutine registry). Independent of the JPEG screencast attachment's
// own detach(), since both can be active on the same connection.
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
// full, real teardown (closes the PeerConnection, arms the grace-stop timer,
// resumes the JPEG screencast via ReconcileScreencast) when it is still
// current — which is always true for the ordinary case of a legitimate
// detach with no second offer in flight, so a normal detach's viewer is
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
// announcement neither side ever moves and the panel silently stays on JPEG
// forever (W3 e2e finding). This is an announcement, not an authorization:
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
	h.sendWebRTCState(wc, sessID, viewerID, true, false, false, "")
}

// sendWebRTCState builds and sends a browser_webrtc_state frame.
func (h *BrowserWSHandler) sendWebRTCState(
	wc *browserWSConn,
	sessID, viewerID string,
	available, active, hasAudio bool,
	reason string,
) {
	f := generated.BrowserWebRTCStateFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcState),
		Available: available,
		SessionId: &sessID,
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
const encoderLivenessVideoStallTicks = 2

// watchEncoderLiveness runs for the lifetime of one CaptureSession (fix 3),
// exiting as soon as cs.Done() closes (Stop(), from ANY cause) so at most
// one watchdog goroutine is ever live per session. Started exactly once per
// session by ensureCaptureSession's newFn — the same "exactly once per
// session" discipline EnsureCaptureSession already guarantees for newFn
// itself. Stops the session on either of two independent staleness signals:
// no ping beacon within encoderLivenessStaleAfter (original fix 3), OR no
// video RTP progress across encoderLivenessVideoStallTicks consecutive
// checks while a viewer is attached (fix-wave HIGH addition, see that
// const's doc comment).
func (h *BrowserWSHandler) watchEncoderLiveness(cs *browser.CaptureSession, agentID string) {
	ticker := time.NewTicker(encoderLivenessCheckInterval)
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
					encoderLivenessCheckInterval,
				)
				cs.Stop()
				return
			}

			last := cs.LastPingAt()
			if last.IsZero() {
				continue // encoder hasn't bound the ingest connection yet
			}
			if time.Since(last) > encoderLivenessStaleAfter {
				slog.Warn(
					"browser-webrtc: encoder liveness watchdog — no ping beacon received, stopping capture session",
					"agent_id",
					agentID,
					"last_ping_at",
					last,
					"stale_after",
					encoderLivenessStaleAfter,
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
		webrtcCfg := webrtc.Config{StunServer: cfg.Tools.Browser.WebRTCStunServer}
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
		go h.watchEncoderLiveness(cs, agentID)
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
				return
			}
			slog.Warn("browser-webrtc: input dispatch failed", "error", err, "viewer_id", viewerID)
			h.surfaceWebRTCInputError(viewerID, frame.Kind, err)
		}
	}
}

// surfaceWebRTCInputError pushes a browser_status(error) frame to viewerID's
// main WS connection (looked up via h.viewerConns — the viewer driving over
// the data channel always has one, registered by handleWebRTCOffer). Fix 7:
// previously a non-benign DC input dispatch error was logged at Warn
// server-side and otherwise silently dropped — the WS input path's
// handleInput surfaces the identical class of failure to the user via
// sessionErrorStatus, and the DC path had no equivalent. Applies the SAME
// content-aware throttle discipline handleInput uses (minInputErrorInterval;
// discrete kinds — navigate/navigate_back/reload — are never throttled) so a
// dead/crashed tab failing every DC input event can't flood the viewer with
// one error frame per event. A no-op if viewerID has no registered
// connection (e.g. the viewer detached between the DC message arriving and
// this dispatch completing).
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
	send := func(action string, reason *string) error {
		return ic.sendJSON(generated.BrowserCaptureControlFrame{
			Type:   string(generated.WsFrameTypeBrowserCaptureControl),
			Action: action,
			Reason: reason,
		})
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
