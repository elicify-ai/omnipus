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

// wsSendItem is one outbound queue entry on browserWSConn.sendCh (FR-017).
// Binary tags the WS opcode the write pump must use: true → Binary frame
// (encoded video/audio chunks relayed byte-for-byte from the encoder page —
// component D/relay), false → Text frame (JSON control frames: browser_status,
// browser_tabs, browser_screencast, error, etc). A nil *wsSendItem is the
// ping-keepalive sentinel (pingPump) — preserved unchanged from the prior
// `chan []byte` design, where a nil slice played the same role; switching the
// channel's element type to a pointer keeps "nil enqueued item == send a
// ping" working exactly as before with no special-case wrapping at the
// enqueue sites.
type wsSendItem struct {
	Binary bool
	Data   []byte
}

// wsFrameWriter is the write-side seam writePump uses to reach the
// underlying socket. *websocket.Conn satisfies it (its WriteMessage has this
// exact signature), so production behavior is unchanged; the seam exists so
// Test 11 (FR-017) can inject a fake writer and assert per-item opcodes
// hermetically, with no real network connection.
type wsFrameWriter interface {
	WriteMessage(messageType int, data []byte) error
}

// browserWSConn holds one connection's write-side state. It intentionally
// carries far less than chat's wsConn (no replay divert, no session
// tracking) — this socket does exactly one thing: relay one live browser.
type browserWSConn struct { // not-wire-format: internal connection bookkeeping, never marshaled.
	conn      wsFrameWriter
	sendCh    chan *wsSendItem
	doneCh    chan struct{}
	closeOnce sync.Once
}

func (c *browserWSConn) close() {
	c.closeOnce.Do(func() { close(c.doneCh) })
}

// sendFrame enqueues a JSON screencast frame (Text opcode), dropping it
// immediately (never blocking) if the channel is backed up. Correct for a
// repaint-driven, lossy stream (ADR-038 D3): a dropped frame is superseded by
// the next repaint, so blocking the CDP event-ack goroutine to avoid a drop
// would be strictly worse (it would stall frame delivery to every other
// attached session).
func (c *browserWSConn) sendFrame(data []byte) {
	select {
	case c.sendCh <- &wsSendItem{Data: data}:
	case <-c.doneCh:
	default:
	}
}

// sendChunk enqueues an encoded binary media chunk (video/audio, FR-002/017)
// as a WS Binary frame — the binary analog of sendFrame, with the identical
// drop-on-full, latest-wins discipline (FR-004: "a full-queue viewer MUST
// have its chunks dropped in isolation"; a stale encoded chunk is superseded
// by the next one, same rationale as the JPEG-era screencast frame). Called
// by the relay path (component D) for every encoded chunk forwarded to this
// viewer.
func (c *browserWSConn) sendChunk(data []byte) {
	select {
	case c.sendCh <- &wsSendItem{Binary: true, Data: data}:
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
// dropCtx is a short, caller-supplied identifier (see dropContext) logged
// ONLY if the frame is actually dropped (B6, 7-reviewer finding): before
// this the drop-warning below carried nothing identifying, making a dropped
// control-sync/error frame — now the SOLE trail of that event — impossible
// to correlate with a specific session/viewer after the fact.
func (c *browserWSConn) sendCritical(data []byte, dropCtx string) {
	select {
	case c.sendCh <- &wsSendItem{Data: data}:
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
// currently holds. Mutated only from readLoop's goroutine — the screencast
// FrameSink/StatusSink callbacks (a different goroutine, driven by chromedp's
// CDP event dispatch) never touch it, only wc.sendFrame(Gen)/wc.sendCritical(Gen)/
// wc.sendChunk, which are channel-safe.
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
		sendCh: make(chan *wsSendItem, browserWSSendCap),
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
	if jsonErr := json.Unmarshal(
		data,
		&authFrame,
	); jsonErr != nil ||
		authFrame.Type != string(generated.WsFrameTypeAuth) {
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
// goroutine. A nil *wsSendItem on sendCh is the sentinel for a ping frame
// (unchanged from the prior nil-[]byte sentinel — see wsSendItem's doc
// comment). Every non-nil item is written using the WS opcode its Binary tag
// selects (FR-017): Binary frame for encoded video/audio chunks, Text frame
// for JSON control frames (browser_status, browser_tabs, browser_screencast,
// error, etc).
func (h *BrowserWSHandler) writePump(wc *browserWSConn) {
	for {
		select {
		case item, ok := <-wc.sendCh:
			if !ok {
				return
			}
			if item == nil {
				if err := wc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					slog.Debug("browser-ws: ping write error", "error", err)
					return
				}
				continue
			}
			opcode := websocket.TextMessage
			if item.Binary {
				opcode = websocket.BinaryMessage
			}
			if err := wc.conn.WriteMessage(opcode, item.Data); err != nil {
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
			h.handleAttach(wc, &state, viewerID, userID, data)
		case string(generated.WsFrameTypeBrowserInput):
			h.handleInput(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserControl):
			h.handleControl(wc, &state, viewerID, userID, data, cfg)
		case string(generated.WsFrameTypeBrowserTabAction):
			h.handleTabAction(wc, &state, viewerID, data)
		case string(generated.WsFrameTypeBrowserDetach):
			h.handleDetach(wc, &state, viewerID, userID)
		default:
			wc.sendCriticalGen(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: fmt.Sprintf("unknown frame type %q", typ.Type),
			}, dropContext("", viewerID, "unknown-type:"+typ.Type))
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
func (h *BrowserWSHandler) handleAttach(
	wc *browserWSConn,
	state *browserConnState,
	viewerID, userID string,
	data []byte,
) {
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

	if state.mgr != nil && state.sessionID != "" {
		h.detach(state.mgr, state.sessionID, viewerID, userID)
		state.mgr = nil
		state.sessionID = ""
	}

	mgr, ok := h.agentLoop.BrowserManagerForAgent(frame.AgentId)
	if !ok {
		wc.sendCriticalGen(sessionErrorStatus(
			frame.SessionId,
			fmt.Sprintf(
				"no browser manager for agent %q (browser tools may not be registered for this agent)",
				frame.AgentId,
			),
		),
			dropContext(frame.SessionId, viewerID, "attach-no-manager"))
		return
	}

	chatSessionID := frame.SessionId // context/logging + wire echo ONLY — see doc comment above.
	controlledByOther, err := mgr.Live().Attach(browser.DefaultSessionID, viewerID, func(lf browser.LiveFrame) {
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

	state.mgr = mgr
	state.sessionID = chatSessionID

	cbo := controlledByOther
	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:              string(generated.WsFrameTypeBrowserStatus),
		State:             "attached",
		SessionId:         &chatSessionID,
		ControlledByOther: &cbo,
	}, dropContext(chatSessionID, viewerID, "attach-ok"))
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

	if err := state.mgr.Live().Input(browser.DefaultSessionID, viewerID, in); err != nil {
		if browser.IsBenignLiveInputError(err) {
			slog.Debug("browser-ws: input rejected (benign)", "error", err, "session_id", state.sessionID)
			return
		}
		slog.Warn("browser-ws: input dispatch failed", "error", err, "session_id", state.sessionID)
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
			wc.sendCriticalGen(sessionErrorStatus(state.sessionID, message),
				dropContext(state.sessionID, viewerID, "input-error"))
		}
	}
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
	if state.mgr == nil || state.sessionID == "" {
		wc.sendCriticalGen(errorStatus("browser_control: attach before requesting control"),
			dropContext("", viewerID, "control-not-attached"))
		return
	}
	var frame generated.BrowserControlFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_control: invalid frame"),
			dropContext(state.sessionID, viewerID, "control-invalid"))
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
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "take-control is disabled by the operator"),
				dropContext(chatSessionID, viewerID, "control-take-disabled"))
			return
		}
		if !state.mgr.Live().TakeControl(browser.DefaultSessionID, viewerID) {
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
		state.mgr.Live().ReleaseControl(browser.DefaultSessionID, viewerID)
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
// browser.DefaultSessionID — the agent's actual tab set, same convention as
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
	if state.mgr == nil || state.sessionID == "" {
		wc.sendCriticalGen(errorStatus("browser_tab_action: attach before managing tabs"),
			dropContext("", viewerID, "tab-action-not-attached"))
		return
	}
	var frame generated.BrowserTabActionFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		wc.sendCriticalGen(errorStatus("browser_tab_action: invalid frame"),
			dropContext(state.sessionID, viewerID, "tab-action-invalid"))
		return
	}

	// chatSessionID is echoed on outgoing error frames; every call into
	// state.mgr below uses browser.DefaultSessionID, the agent's actual tab
	// set (ADR-038 finding #1 / ADR-041) — see handleAttach's doc comment.
	chatSessionID := state.sessionID

	if controller := state.mgr.Live().Controller(browser.DefaultSessionID); controller != "" && controller != viewerID {
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
		if _, err := state.mgr.SwitchTab(browser.DefaultSessionID, *frame.Index); err != nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_tab_action: %s", err)),
				dropContext(chatSessionID, viewerID, "tab-switch-failed"))
		}
	case "close":
		if frame.Index == nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, "browser_tab_action: index is required for close"),
				dropContext(chatSessionID, viewerID, "tab-close-missing-index"))
			return
		}
		if _, _, err := state.mgr.CloseTab(browser.DefaultSessionID, *frame.Index); err != nil {
			wc.sendCriticalGen(sessionErrorStatus(chatSessionID, fmt.Sprintf("browser_tab_action: %s", err)),
				dropContext(chatSessionID, viewerID, "tab-close-failed"))
		}
	case "open":
		if _, err := state.mgr.OpenTab(browser.DefaultSessionID); err != nil {
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
	}, dropContext(chatSessionID, viewerID, "detach-ok"))
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
