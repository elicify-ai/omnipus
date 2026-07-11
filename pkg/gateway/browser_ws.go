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
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ADR-038 D1 — /api/v1/browser/ws is a DEDICATED WebSocket, deliberately
// separate from /api/v1/chat/ws. Screencast is a high-volume,
// independently-lifecycled stream; keeping it off the chat socket avoids
// interfering with chat's backpressure/replay logic (websocket.go's
// sendRawFrameBytes / replay divert). This file intentionally does not reuse
// wsConn/WSHandler's replay machinery — browser-live has no replay concept
// (a live view is either attached now or it isn't) and screencast frames are
// inherently lossy (D3: "repaint-driven, not fixed-FPS" — dropping a stale
// frame is correct, the next repaint supersedes it).

// browserWSSendCap is the outbound buffer depth for one connection.
// Screencast frames dominate traffic; deep enough to absorb a repaint burst
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

// sendFrame enqueues a screencast frame, dropping it immediately (never
// blocking) if the channel is backed up. Correct for a repaint-driven, lossy
// stream (ADR-038 D3): a dropped frame is superseded by the next repaint, so
// blocking the CDP event-ack goroutine to avoid a drop would be strictly
// worse (it would stall frame delivery to every other attached session).
func (c *browserWSConn) sendFrame(data []byte) {
	select {
	case c.sendCh <- data:
	case <-c.doneCh:
	default:
	}
}

// sendFrameGen marshals and enqueues a screencast frame via sendFrame.
func (c *browserWSConn) sendFrameGen(frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal screencast frame failed", "error", err)
		return
	}
	c.sendFrame(data)
}

// sendCritical enqueues a low-frequency, must-not-drop frame (browser_status,
// error) — these carry state transitions the SPA needs to see, unlike the
// high-volume screencast stream. Blocks briefly rather than silently
// dropping; gives up after 2s so a wedged connection can't hang the caller.
func (c *browserWSConn) sendCritical(data []byte) {
	select {
	case c.sendCh <- data:
	case <-c.doneCh:
	case <-time.After(2 * time.Second):
		slog.Warn("browser-ws: send channel full, dropping critical frame")
	}
}

// sendCriticalGen marshals and enqueues a critical frame via sendCritical.
func (c *browserWSConn) sendCriticalGen(frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("browser-ws: marshal frame failed", "error", err)
		return
	}
	c.sendCritical(data)
}

// browserConnState tracks the single live-browser attachment this connection
// currently holds. Mutated only from readLoop's goroutine — the screencast
// FrameSink/StatusSink callbacks (a different goroutine, driven by chromedp's
// CDP event dispatch) never touch it, only wc.sendFrame(Gen)/wc.sendCritical(Gen),
// which are channel-safe.
type browserConnState struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	mgr *browser.BrowserManager
	// sessionID is the CLIENT-supplied (chat) session id from the attach
	// frame, kept only for logging and for echoing back on outgoing wire
	// frames (ADR-038 finding #1). It is NEVER passed to mgr.Live() — every
	// interaction with the live-view engine uses browser.DefaultSessionID,
	// the one tab the agent's browser_* tools actually drive. A non-empty
	// value also doubles as "this connection currently has a live view
	// attached."
	sessionID string
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
}

// minInputErrorInterval is the minimum gap between two IDENTICAL real-input-
// error browser_status(error) frames sent to the same connection (ADR-038
// finding #4). A different error message bypasses the cooldown entirely —
// see browserConnState.lastInputErrorMessage's doc comment.
const minInputErrorInterval = 2 * time.Second

// BrowserWSHandler implements the /api/v1/browser/ws endpoint (ADR-038):
// screencast-out + input-injection-in for the live interactive browser
// panel. One connection == one viewer == at most one attached (agent,
// session) live view at a time.
type BrowserWSHandler struct {
	agentLoop     *agent.AgentLoop
	allowedOrigin string
	upgrader      websocket.Upgrader

	// activeConns tracks in-flight ServeHTTP goroutines so Wait() can block
	// until all connections have fully torn down (test cleanup, mirroring
	// WSHandler.Wait()).
	activeConns sync.WaitGroup
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

	userID, ok := h.authenticate(conn)
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

// authenticate reads the first frame and validates the token. Mirrors
// WSHandler.authenticateWS's (websocket.go) identity resolution exactly —
// every account in Gateway.Users first (via the shared resolveBearerIdentity
// helper), then Gateway.CLIToken, then the legacy OMNIPUS_BEARER_TOKEN env
// var, then dev_mode_bypass — so browser-live can never authenticate a
// caller chat would have rejected, or vice versa. On failure this has
// already written an error frame + close message; the caller must return
// without proceeding.
//
// Returns the resolved userID. Per the documented Entry.User contract
// (pkg/audit/audit.go), the env-token and dev-bypass paths deliberately
// return "" (not a synthesized identity) — same as chat's wc.userID, which
// only resolveBearerIdentity's match branch ever sets.
func (h *BrowserWSHandler) authenticate(conn *websocket.Conn) (string, bool) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		slog.Warn("browser-ws: auth read failed", "error", err)
		return "", false
	}

	var authFrame generated.AuthFrame
	if jsonErr := json.Unmarshal(data, &authFrame); jsonErr != nil || authFrame.Type != string(generated.WsFrameTypeAuth) {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "first message must be {\"type\":\"auth\",\"token\":\"...\"}",
		})
		return "", false
	}

	cfg := h.agentLoop.GetConfig()
	rawToken := authFrame.Token

	if user, _, matched := resolveBearerIdentity(cfg, rawToken); matched {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return user.Username, true
	}

	if bearerAccountsConfigured(cfg) {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "unauthorized: invalid token",
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
			Message: "no users configured, complete onboarding first",
		})
		writeCloseAuthFailed(conn)
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) != 1 {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "unauthorized: invalid token",
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
	for {
		select {
		case msg, ok := <-wc.sendCh:
			if !ok {
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
func (h *BrowserWSHandler) readLoop(conn *websocket.Conn, wc *browserWSConn, viewerID, userID string, cfg *config.Config) {
	conn.SetReadLimit(browserWSMaxMessageBytes)

	var state browserConnState
	defer func() {
		if state.mgr != nil && state.sessionID != "" {
			h.detach(state.mgr, state.sessionID, viewerID, userID)
		}
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
			})
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
					wc.sendCriticalGen(errorStatus(fmt.Sprintf("frame schema validation failed (%s): %s", schemaName, errMsg)))
					continue
				}
			}
		}

		switch typ.Type {
		case string(generated.WsFrameTypeBrowserAttach):
			h.handleAttach(wc, &state, viewerID, userID, data)
		case string(generated.WsFrameTypeBrowserInput):
			h.handleInput(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserControl):
			h.handleControl(wc, &state, viewerID, userID, data, cfg)
		case string(generated.WsFrameTypeBrowserDetach):
			h.handleDetach(wc, &state, viewerID, userID)
		default:
			wc.sendCriticalGen(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: fmt.Sprintf("unknown frame type %q", typ.Type),
			})
		}
	}
}

// handleAttach binds this connection to the target agent's live browser
// (ADR-038 D3): resolves the agent's BrowserManager, starts (or joins) its
// screencast, and streams browser_screencast frames back until detach. A
// second browser_attach on an already-attached connection first detaches the
// previous attachment — one connection, one live view at a time.
//
// ADR-038 finding #1: the live view ALWAYS binds to browser.DefaultSessionID
// — the one Chromium tab the target agent's browser_* tools actually drive —
// never to frame.SessionId. frame.SessionId is the client's chat session id;
// before this fix it was passed straight to mgr.Live().Attach(), which
// lazily created a brand-new, blank tab keyed by that chat UUID, distinct
// from the tab the agent was navigating. The result: the live view showed an
// unrelated blank tab, and browser_control{take} locked a session the
// agent's own tools (which always check IsControlled(DefaultSessionID)) never
// consulted — "take control" was a no-op from the agent's perspective.
// frame.SessionId is retained ONLY as chatSessionID below, for logging and
// for echoing back on outgoing wire frames so the client can correlate
// responses with its own chat session.
func (h *BrowserWSHandler) handleAttach(wc *browserWSConn, state *browserConnState, viewerID, userID string, data []byte) {
	var frame generated.BrowserAttachFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_attach: invalid frame"))
		return
	}
	if frame.AgentId == "" || frame.SessionId == "" {
		wc.sendCriticalGen(errorStatus("browser_attach: agent_id and session_id are required"))
		return
	}

	if state.mgr != nil && state.sessionID != "" {
		h.detach(state.mgr, state.sessionID, viewerID, userID)
		state.mgr = nil
		state.sessionID = ""
	}

	mgr, ok := h.agentLoop.BrowserManagerForAgent(frame.AgentId)
	if !ok {
		wc.sendCriticalGen(sessionErrorStatus(frame.SessionId,
			fmt.Sprintf("no browser manager for agent %q (browser tools may not be registered for this agent)", frame.AgentId)))
		return
	}

	chatSessionID := frame.SessionId // context/logging + wire echo ONLY — see doc comment above.
	err := mgr.Live().Attach(browser.DefaultSessionID, viewerID, func(lf browser.LiveFrame) {
		pageScale, offsetTop, scrollX, scrollY := lf.PageScale, lf.OffsetTop, lf.ScrollOffsetX, lf.ScrollOffsetY
		wc.sendFrameGen(generated.BrowserScreencastFrame{
			Type:          string(generated.WsFrameTypeBrowserScreencast),
			SessionId:     chatSessionID,
			Seq:           lf.Seq,
			Data:          lf.Data,
			Width:         lf.Width,
			Height:        lf.Height,
			PageScale:     &pageScale,
			OffsetTop:     &offsetTop,
			ScrollOffsetX: &scrollX,
			ScrollOffsetY: &scrollY,
		})
	}, func(message string) {
		// ADR-038 finding #2's split-brain fix: the LiveView's underlying tab
		// context died without an explicit browser_detach — e.g. this
		// connection is still holding a reference to a BrowserManager that
		// registerSharedTools has since Shutdown()'d on hot-reload. Tell the
		// client so it can re-attach (which resolves the CURRENT manager via
		// BrowserManagerForAgent) instead of silently watching a frozen frame
		// forever.
		wc.sendCriticalGen(sessionErrorStatus(chatSessionID, message))
	})
	if err != nil {
		wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_attach failed: %s", err)))
		return
	}

	state.mgr = mgr
	state.sessionID = chatSessionID

	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:      string(generated.WsFrameTypeBrowserStatus),
		State:     "attached",
		SessionId: &chatSessionID,
	})
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
// always see WHY their navigate was refused).
func (h *BrowserWSHandler) handleInput(wc *browserWSConn, state *browserConnState, viewerID string, data []byte) {
	if state.mgr == nil || state.sessionID == "" {
		return
	}
	var frame generated.BrowserInputFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}

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
	if frame.Text != nil {
		in.Text = *frame.Text
	}
	if frame.Url != nil {
		in.URL = *frame.Url
	}
	if frame.Modifiers != nil {
		in.Modifiers = *frame.Modifiers
	}

	if err := state.mgr.Live().Input(browser.DefaultSessionID, viewerID, in); err != nil {
		if browser.IsBenignLiveInputError(err) {
			slog.Debug("browser-ws: input rejected (benign)", "error", err, "session_id", state.sessionID)
			return
		}
		slog.Warn("browser-ws: input dispatch failed", "error", err, "session_id", state.sessionID)
		message := fmt.Sprintf("browser input failed: %s", err)
		now := time.Now()
		if message != state.lastInputErrorMessage || now.Sub(state.lastInputErrorSentAt) >= minInputErrorInterval {
			state.lastInputErrorSentAt = now
			state.lastInputErrorMessage = message
			wc.sendCriticalGen(sessionErrorStatus(state.sessionID, message))
		}
	}
}

// handleControl processes a take/release control request (ADR-038 D6).
// take is refused (audited as deny) when tools.browser.take_control_enabled
// is off, or when another viewer already controls the session — v1 is
// cooperative/first-come, no preemption. Every take/release outcome is
// audit-logged per the ADR's "take/release control is audit-logged"
// requirement.
func (h *BrowserWSHandler) handleControl(wc *browserWSConn, state *browserConnState, viewerID, userID string, data []byte, cfg *config.Config) {
	if state.mgr == nil || state.sessionID == "" {
		wc.sendCriticalGen(errorStatus("browser_control: attach before requesting control"))
		return
	}
	var frame generated.BrowserControlFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_control: invalid frame"))
		return
	}

	// chatSessionID is echoed on outgoing frames / audit entries; every call
	// into mgr.Live() below uses browser.DefaultSessionID, the agent's actual
	// tab (ADR-038 finding #1) — see handleAttach's doc comment.
	chatSessionID := state.sessionID
	switch frame.Action {
	case "take":
		if !cfg.Tools.Browser.TakeControlEnabled {
			h.auditControl(userID, chatSessionID, viewerID, audit.SeverityWarn, "take_control_disabled")
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "take-control is disabled by the operator"))
			return
		}
		if !state.mgr.Live().TakeControl(browser.DefaultSessionID, viewerID) {
			h.auditControl(userID, chatSessionID, viewerID, audit.SeverityWarn, "already_controlled")
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "another viewer already controls this browser"))
			return
		}
		h.auditControl(userID, chatSessionID, viewerID, audit.SeverityInfo, "take")
		controller := userID
		wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:       string(generated.WsFrameTypeBrowserStatus),
			State:      "controlling",
			SessionId:  &chatSessionID,
			Controller: &controller,
		})
	case "release":
		state.mgr.Live().ReleaseControl(browser.DefaultSessionID, viewerID)
		h.auditRelease(userID, chatSessionID, viewerID)
		wc.sendCriticalGen(generated.BrowserStatusFrame{
			Type:      string(generated.WsFrameTypeBrowserStatus),
			State:     "released",
			SessionId: &chatSessionID,
		})
	default:
		wc.sendCriticalGen(errorStatus(fmt.Sprintf("browser_control: unknown action %q", frame.Action)))
	}
}

// handleDetach unbinds this connection from its current live view.
func (h *BrowserWSHandler) handleDetach(wc *browserWSConn, state *browserConnState, viewerID, userID string) {
	if state.mgr == nil || state.sessionID == "" {
		return
	}
	chatSessionID := state.sessionID
	h.detach(state.mgr, chatSessionID, viewerID, userID)
	state.mgr = nil
	state.sessionID = ""
	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:      string(generated.WsFrameTypeBrowserStatus),
		State:     "detached",
		SessionId: &chatSessionID,
	})
}

// detach releases viewerID from the live view (stopping the screencast if it
// was the last viewer) and audits a control release if this viewer was the
// controller — used both by explicit browser_detach and by readLoop's
// disconnect cleanup, so a dropped connection is indistinguishable from a
// clean detach for audit and resource-cleanup purposes. chatSessionID is
// used only for the audit entry / log context; the live-view call always
// targets browser.DefaultSessionID (ADR-038 finding #1).
func (h *BrowserWSHandler) detach(mgr *browser.BrowserManager, chatSessionID, viewerID, userID string) {
	wasController := mgr.Live().Controller(browser.DefaultSessionID) == viewerID
	mgr.Live().Detach(browser.DefaultSessionID, viewerID)
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
