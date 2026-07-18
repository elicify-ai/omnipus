// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
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
// browser_screencast path, which keeps running unconditionally regardless of
// WebRTC's fate.

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

// handleWebRTCOffer processes a browser_webrtc_offer frame (ADR-047 D4). Gate
// ladder, in order (wave-plan W2-A item 1): WebRTCEnabled -> lite build ->
// ClassifyVideoCapability -> ensure+start the agent's capture session ->
// HandleViewerOffer. Every rejection sends a browser_webrtc_state frame with
// available=false and a reason; the JPEG screencast (handleAttach, already
// running independently) is never touched by any branch here.
func (h *BrowserWSHandler) handleWebRTCOffer(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
	cfg *config.Config,
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

	if !cfg.Tools.Browser.WebRTCEnabled {
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "disabled")
		return
	}
	if !webrtc.Available {
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "lite_build")
		return
	}

	mgr, ok := h.agentLoop.BrowserManagerForAgent(frame.AgentId)
	if !ok {
		wc.sendCriticalGen(sessionErrorStatus(sessID,
			fmt.Sprintf("no browser manager for agent %q (browser tools may not be registered for this agent)", frame.AgentId)),
			dropContext(sessID, viewerID, "webrtc-offer-no-manager"))
		return
	}

	cap := mgr.VideoCapability()
	if !cap.Capable {
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "not_capable")
		return
	}

	cs, err := h.ensureCaptureSession(mgr, frame.AgentId, cfg)
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
		h.auditStream(userID, frame.AgentId, audit.SeverityWarn, audit.EventBrowserWebRTCStreamStarted,
			map[string]any{"session_id": sessID, "error": startErr.Error()})
		return
	}
	if justStarted {
		h.auditStream(userID, frame.AgentId, audit.SeverityInfo, audit.EventBrowserWebRTCStreamStarted,
			map[string]any{"session_id": sessID})
	}

	cs.AddViewer(viewerID)
	answer, offerErr := cs.HandleViewerOffer(viewerID, frame.Sdp)
	if offerErr != nil {
		cs.RemoveViewer(viewerID)
		slog.Warn("browser-webrtc: viewer offer failed", "error", offerErr, "agent_id", frame.AgentId, "viewer_id", viewerID)
		h.sendWebRTCState(wc, sessID, viewerID, true, false, false, "error")
		return
	}

	state.webrtcAgentID = frame.AgentId
	state.webrtcCapture = cs

	stats := cs.Stats()
	wc.sendCriticalGen(generated.BrowserWebRTCAnswerFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcAnswer),
		Sdp:       answer,
		SessionId: &sessID,
	}, dropContext(sessID, viewerID, "webrtc-answer"))
	h.sendWebRTCState(wc, sessID, viewerID, true, true, stats.HasAudio, "")
}

// detachWebRTCViewer tears down a connection's WebRTC viewer attachment
// (browser_detach, WS close, or connection cleanup) — closes the relay-side
// viewer PeerConnection and decrements the capture session's viewer count
// (RemoveViewer arms the grace-stop timer once it reaches zero, wave-plan
// W2-A item 4). Independent of the JPEG screencast attachment's own
// detach(), since both can be active on the same connection.
func (h *BrowserWSHandler) detachWebRTCViewer(state *browserConnState, viewerID string) {
	cs := state.webrtcCapture
	state.webrtcAgentID = ""
	state.webrtcCapture = nil
	if cs == nil {
		return
	}
	cs.Relay().CloseViewer(viewerID)
	cs.RemoveViewer(viewerID)
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
	switch {
	case !cfg.Tools.Browser.WebRTCEnabled:
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "disabled")
	case !webrtc.Available:
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "lite_build")
	case !mgr.VideoCapability().Capable:
		h.sendWebRTCState(wc, sessID, viewerID, false, false, false, "not_capable")
	default:
		h.sendWebRTCState(wc, sessID, viewerID, true, false, false, "")
	}
}

// sendWebRTCState builds and sends a browser_webrtc_state frame.
func (h *BrowserWSHandler) sendWebRTCState(wc *browserWSConn, sessID, viewerID string, available, active, hasAudio bool, reason string) {
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

// ensureCaptureSession get-or-creates agentID's CaptureSession (one active
// stream per agent, wave-plan W2-A item 4), registering it in h.captures so
// the ingest WS can find it by token, and wiring SetOnStopped to remove it
// from both the registry and the manager once it stops.
func (h *BrowserWSHandler) ensureCaptureSession(mgr *browser.BrowserManager, agentID string, cfg *config.Config) (*browser.CaptureSession, error) {
	return mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) {
		webrtcCfg := webrtc.Config{StunServer: cfg.Tools.Browser.WebRTCStunServer}
		sink := webrtcInputSink(mgr)
		logf := func(format string, args ...any) {
			slog.Debug(fmt.Sprintf("browser-webrtc[%s]: "+format, append([]any{agentID}, args...)...))
		}
		cs, err := browser.NewCaptureSession(mgr, agentID, webrtcCfg, sink, logf)
		if err != nil {
			return nil, err
		}
		h.captures.set(agentID, cs)
		cs.SetOnStopped(func() {
			h.captures.removeIfCurrent(agentID, cs)
			audit.Emit(context.Background(), h.agentLoop.AuditLogger(), audit.EventBrowserWebRTCStreamStopped,
				audit.SeverityInfo, map[string]any{"agent_id": agentID})
		})
		return cs, nil
	})
}

// webrtcInputSink builds the webrtc.InputSink for one agent's CaptureSession:
// parse as generated.BrowserInputFrame (drop+log invalid), convert via the
// SAME browserInputFrameToLiveInput helper handleInput uses (wave-plan W2-A
// item 4 — "convert EXACTLY like browser_ws.go handleInput does"), then
// dispatch through the identical controller-lock/SSRF/rate-limit gate
// (browser.LiveViewRegistry.Input) the WS input path uses.
func webrtcInputSink(mgr *browser.BrowserManager) webrtc.InputSink {
	return func(viewerID string, raw []byte) {
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
		}
	}
}

// auditStream emits a WebRTC stream lifecycle audit entry.
func (h *BrowserWSHandler) auditStream(userID, agentID string, sev audit.Severity, event string, fields map[string]any) {
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

func (c *captureIngestConn) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("capture-ingest: marshal frame: %w", err)
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
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
	if jsonErr := json.Unmarshal(data, &hello); jsonErr != nil || hello.Type != string(generated.WsFrameTypeBrowserCaptureHello) {
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
				continue
			}
			answer, offerErr := cs.HandleIngestOffer(offerFrame.Sdp)
			if offerErr != nil {
				slog.Warn("capture-ingest: ingest offer failed", "error", offerErr, "agent_id", agentID)
				continue
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
