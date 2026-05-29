//go:build !cgo

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
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/pairing"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/validation"
)

// replayLiveBufferCap is the capacity of replayDivertCh (FR-I-009).
// Frames are diverted here via sendConnGenFrame when isReplayingLive is set;
// drained into sendCh after replay's done frame.  When the channel is full
// sendConnGenFrame drops the frame and emits a degraded warning to sendCh directly
// (W1-6) so the client still receives the overflow notice.
const replayLiveBufferCap = 1000

// wsTypeOnly is used in the readLoop to peek at the "type" discriminator
// before decoding the full frame into its specific generated type.
// It is never emitted as a wire value; it is an inbound decode helper only.
type wsTypeOnly struct { // not-wire-format: inbound decode-only helper to peek "type"; never marshaled as a wire value.
	Type string `json:"type"`
}

// replayFrameDecoder is retained solely as a JSON-unmarshal target for
// replay_test.go's sliceSink.all() — it decodes emitted JSON bytes for test
// assertions. It is never constructed or marshaled as a wire value anywhere in
// production code; all outbound emission sites use generated types.
//
// Fields cover the superset of all server→client frames so that test assertions
// can inspect any field without knowing the concrete frame type.
type replayFrameDecoder struct { // not-wire-format: decode-only test assertion target, never emitted over the WebSocket connection.
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`

	Content    string         `json:"content,omitempty"`
	Role       string         `json:"role,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	Result     any            `json:"result,omitempty"`
	Command    string         `json:"command,omitempty"`
	ID         string         `json:"id,omitempty"`
	CallID     string         `json:"call_id,omitempty"`
	Stats      map[string]any `json:"stats,omitempty"`
	Message    string         `json:"message,omitempty"`
	Status     string         `json:"status,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
	// device_pairing_request fields
	DeviceID    string `json:"device_id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PairingCode string `json:"pairing_code,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	// rate_limit fields (SEC-26)
	Scope             string  `json:"scope,omitempty"`
	Resource          string  `json:"resource,omitempty"`
	PolicyRule        string  `json:"policy_rule,omitempty"`
	RetryAfterSeconds float64 `json:"retry_after_seconds,omitempty"`
	AgentID           string  `json:"agent_id,omitempty"`
	// media frame fields
	Parts []map[string]any `json:"parts,omitempty"`
	// subagent span fields (FR-H-004, FR-H-005)
	SpanID       string `json:"span_id,omitempty"`
	ParentCallID string `json:"parent_call_id,omitempty"`
	TaskLabel    string `json:"task_label,omitempty"`
}

// WSHandler handles the /api/v1/chat/ws WebSocket endpoint for bi-directional
// chat streaming. It implements bus.StreamDelegate so the agent loop can push
// tokens directly to the connected browser. Replaces the Wave 1 SSE handler
// per Wave 5a spec (non-behavior: must not use SSE for chat streaming).
type WSHandler struct {
	msgBus        *bus.MessageBus
	agentLoop     *agent.AgentLoop
	allowedOrigin string

	// activeConns tracks in-flight ServeHTTP goroutines so Wait() can block
	// until all connections have fully torn down (used by tests to avoid
	// tempdir cleanup races).
	activeConns sync.WaitGroup

	mu          sync.Mutex
	sessions    map[string]*wsConn // chatID → connection
	sessionIDs  map[string]string  // chatID → sessionID (for transcript recording)
	taskChatIDs map[string]string  // browser chatID → task chatID for live event forwarding
	webchatCh   *webchatChannel    // reference to mark streaming complete

	// approvalRegistry tracks in-flight exec approval requests sent to the browser.
	// Shared across all connections on this handler; keyed by request UUID.
	// Although the registry is shared, each approval request is associated with the
	// connection that sent it — only that connection's browser tab can respond.
	approvalRegistry *wsApprovalRegistry

	// approvalRegV2 is the Central Tool Registry approval registry (FR-016, FR-070).
	// Injected at boot by the gateway after construction.  Nil until then.
	approvalRegV2 *approvalRegistryV2

	// devicePairingRegistry tracks in-flight device pairing requests awaiting admin approval.
	devicePairingRegistry *devicePairingRegistry

	// pairingStore is the global device pairing state (pending + paired devices).
	pairingStore *pairing.PairingStore

	// toolStore persists tool results that exceed InlineToolResultMaxBytes.
	// Set by the gateway after construction (nil = disabled, which is the test default).
	toolStore *toolResultStore

	upgrader websocket.Upgrader
}

type wsConn struct {
	conn           *websocket.Conn
	sendCh         chan []byte
	doneCh         chan struct{}
	closeOnce      sync.Once
	droppedTokens  atomic.Int32
	droppedFrames  atomic.Int32    // non-critical outbound frames dropped due to backpressure
	inboundDropped atomic.Int32    // inbound frames dropped due to schema validation failure (Part B)
	role           config.UserRole // RBAC role resolved at auth time
	userID         string          // username resolved at auth time; used for session_state scoping (FR-073)

	// Replay-mode divert (W1-1): during replay, live events arriving via
	// sendConnGenFrame are redirected into replayDivertCh instead of sendCh so
	// they don't interleave with replay frames. After replay finishes they are
	// drained into sendCh in arrival order.
	//
	// Ordering invariant (see docs/investigation/bug-5-replay-order.md,
	// code-reviewer Finding #2, and architect Finding #4):
	//   Writers must NOT snapshot isReplayingLive and then send to the snapshotted
	//   channel as two separate operations — the drain can empty replayDivertCh and
	//   disarm the flag between those two steps, orphaning the frame.
	//
	//   replayMu serializes the "read flag + select channel" decision in
	//   sendRawFrameBytes against the "drain channel + disarm flag" sequence in
	//   handleAttachSession.  Writers hold the read-lock (RLock) while choosing a
	//   target channel and sending to it; the drain holds the write-lock (Lock) for
	//   the entire drain+disarm sequence.  On the non-replay hot path
	//   (isReplayingLive == false) sendRawFrameBytes performs one atomic load and
	//   never touches replayMu, keeping the common case lock-free.
	replayMu        sync.RWMutex
	isReplayingLive atomic.Bool
	replayDivertCh  chan []byte // capacity replayLiveBufferCap; allocated lazily by handleAttachSession

	// lastPongSentUnixNano debounces pong responses to client pings. The SPA's
	// heartbeat fires every 30 s; allowing one pong per 100 ms per connection
	// gives ~300× headroom for legitimate clients while bounding amplification
	// if a buggy or malicious client floods pings.
	lastPongSentUnixNano atomic.Int64
}

func (c *wsConn) close() {
	c.closeOnce.Do(func() { close(c.doneCh) })
}

// newWSHandler creates a WSHandler and registers it as the MessageBus stream delegate,
// replacing any previously registered delegate (e.g., the Wave 1 SSE handler).
func newWSHandler(
	msgBus *bus.MessageBus,
	agentLoop *agent.AgentLoop,
	allowedOrigin string,
) *WSHandler {
	h := &WSHandler{
		msgBus:                msgBus,
		agentLoop:             agentLoop,
		allowedOrigin:         allowedOrigin,
		sessions:              make(map[string]*wsConn),
		sessionIDs:            make(map[string]string),
		taskChatIDs:           make(map[string]string),
		approvalRegistry:      newWSApprovalRegistry(),
		devicePairingRegistry: newDevicePairingRegistry(),
		pairingStore:          pairing.NewPairingStore(),
		upgrader: websocket.Upgrader{
			// CheckOrigin: parses the Origin URL and compares hostname against the request
			// Host to allow same-origin requests. Also allows localhost/127.0.0.1 for development.
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // non-browser or same-origin
				}
				if allowedOrigin != "" && origin == allowedOrigin {
					return true
				}
				parsed, err := url.Parse(origin)
				if err != nil {
					return false
				}
				hostname := parsed.Hostname()
				originPort := parsed.Port()
				// Allow same-origin: Origin hostname+port matches the request Host.
				if r.Host != "" {
					hostOnly := r.Host
					hostPort := ""
					if h, p, err := net.SplitHostPort(r.Host); err == nil {
						hostOnly = h
						hostPort = p
					}
					if hostname == hostOnly && originPort == hostPort {
						return true
					}
				}
				// Allow localhost and loopback for development ONLY when no explicit origin is configured.
				return allowedOrigin == "" && (hostname == "localhost" || hostname == "127.0.0.1")
			},
		},
	}
	// NOTE: Do NOT call msgBus.SetStreamDelegate(h) here.
	// The channel Manager is the registered delegate; the bus's atomic.Pointer
	// panics on a type mismatch if you store a different concrete type after boot.
	// Webchat streaming flows through Manager.GetStreamer → WSHandler.GetStreamer.
	return h
}

// GetStreamer implements bus.StreamDelegate.
// Returns a WebSocket streamer for webchat sessions that have an active connection.
// sessionID is provided by the caller (agent loop) so the streamer can record to
// the correct transcript without a map reverse-lookup.
func (h *WSHandler) GetStreamer(_ context.Context, channel, chatID, sessionID string) (bus.Streamer, bool) {
	if channel != "webchat" {
		return nil, false
	}
	h.mu.Lock()
	conn, ok := h.sessions[chatID]
	// If caller didn't supply sessionID, fall back to the map for backward compat.
	sid := sessionID
	if sid == "" {
		sid = h.sessionIDs[chatID]
	}
	h.mu.Unlock()
	if !ok {
		return nil, false
	}

	// Resolve the agent store for transcript recording.
	var agentStore *session.UnifiedStore
	if sid != "" {
		agentStore = h.resolveSessionStore(sid)
	}

	// Resolve the active agent for this session so the transcript entry
	// can be tagged with the correct agent ID (FR-002). Key by sessionID.
	//
	// Prefer the handoff override, then fall back to the session's
	// ActiveAgentID from metadata. Without the fallback, assistant entries
	// for un-handed-off sessions get written with AgentID="", which means
	// HydrateAgentHistoryFromTranscript attributes them to "main" instead
	// of the real owning agent — so the next turn's LLM call has only
	// tool_calls/tool_results in its history, no connecting reasoning text,
	// and the agent re-starts the task from scratch.
	activeAgentID := ""
	if aid, ok := h.agentLoop.GetSessionActiveAgent(sid); ok && aid != "" {
		activeAgentID = aid
	} else if agentStore != nil && sid != "" {
		if meta, err := agentStore.GetMeta(sid); err == nil && meta != nil {
			if meta.ActiveAgentID != "" {
				activeAgentID = meta.ActiveAgentID
			} else if meta.AgentID != "" {
				activeAgentID = meta.AgentID
			}
		}
	}

	return &wsStreamer{
		conn:       conn,
		chatID:     chatID,
		sessionID:  sid,
		agentStore: agentStore,
		agentID:    activeAgentID,
		channel:    h.webchatCh,
	}, true
}

// resolveSessionStore delegates to the shared AgentLoop method.
func (h *WSHandler) resolveSessionStore(sessionID string) *session.UnifiedStore {
	return h.agentLoop.ResolveSessionStore(sessionID)
}

// Wait blocks until all active ServeHTTP goroutines have fully exited.
// Call this in test cleanup (after srv.Close()) to prevent tempdir removal
// races with background session writes.
func (h *WSHandler) Wait() {
	h.activeConns.Wait()
}

// ServeHTTP handles the WebSocket upgrade and full connection lifecycle.
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		slog.Warn("ws: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	// Create wsConn before auth so authenticateWS can set the role on it.
	wc := &wsConn{
		conn:   conn,
		sendCh: make(chan []byte, 256),
		doneCh: make(chan struct{}),
	}

	if !h.authenticateWS(conn, wc) {
		return
	}

	chatID := "webchat:" + uuid.New().String()

	h.mu.Lock()
	h.sessions[chatID] = wc
	h.mu.Unlock()

	// Mount a per-connection approval hook so the agent loop can request interactive
	// approval from this browser tab when a tool call requires user consent. The hook
	// sends an exec_approval_request frame and blocks until the browser responds or
	// the request times out.
	hookName := "ws-approval-" + chatID
	approvalHook := &wsApprovalHook{
		conn:     wc,
		chatID:   chatID,
		registry: h.approvalRegistry,
		timeout:  wsApprovalTimeout,
		policyResolver: func(toolName string, agentID string) string {
			cfg := h.agentLoop.GetConfig()
			// Global policy (floor) — derived from sandbox config.
			globalPolicy := "allow"
			if p, ok := cfg.Sandbox.ToolPolicies[toolName]; ok {
				globalPolicy = p
			} else if cfg.Sandbox.DefaultToolPolicy != "" {
				globalPolicy = cfg.Sandbox.DefaultToolPolicy
			}
			// Agent-level policy.
			agentPolicy := "allow"
			for _, ac := range cfg.Agents.List {
				if ac.ID == agentID && ac.Tools != nil {
					agentPolicy = string(ac.Tools.Builtin.ResolvePolicy(toolName))
					break
				}
			}
			// Strictest wins: deny > ask > allow.
			return resolveEffectivePolicy(globalPolicy, agentPolicy)
		},
	}
	if err := h.agentLoop.MountHook(agent.NamedHook(hookName, approvalHook)); err != nil {
		slog.Error("ws: could not mount approval hook — closing connection", "chat_id", chatID, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "failed to initialize tool approval — please reconnect",
		})
		return
	}

	// Subscribe to agent-loop events so we can forward tool_call_start/result
	// frames to the browser in real time.
	eventSub := h.agentLoop.SubscribeEvents(32)
	eventDone := make(chan struct{})
	go h.eventForwarder(wc, chatID, eventSub, eventDone)

	defer func() {
		h.agentLoop.UnmountHook(hookName)
		h.agentLoop.UnsubscribeEvents(eventSub.ID)
		<-eventDone // wait for forwarder goroutine to exit
		h.mu.Lock()
		if tid, ok := h.taskChatIDs[chatID]; ok {
			// sessions is never keyed by tid (only by chatID); clean up only sessionIDs.
			delete(h.sessionIDs, tid)
			delete(h.taskChatIDs, chatID)
		}
		delete(h.sessions, chatID)
		delete(h.sessionIDs, chatID)
		h.mu.Unlock()
		wc.close()
	}()

	go h.writePump(wc)
	go h.pingPump(wc)

	// Emit session_state one-shot on every new WS connection (FR-052, FR-073, FR-081).
	// This lets the SPA reconcile stale approval modals after a gateway restart.
	h.emitSessionState(wc)

	h.readLoop(r.Context(), conn, wc, chatID)
}

// authenticateWS reads the first frame and validates the token.
// Supports RBAC: checks config.Gateway.Users first (bcrypt), then falls back to
// OMNIPUS_BEARER_TOKEN env var for backward compatibility.
// Sets wc.role to the resolved role on success.
func (h *WSHandler) authenticateWS(conn *websocket.Conn, wc *wsConn) bool {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		slog.Warn("ws: auth read failed", "error", err)
		return false
	}

	var authFrame generated.AuthFrame
	if err := json.Unmarshal(data, &authFrame); err != nil || authFrame.Type != string(generated.WsFrameTypeAuth) {
		sendGenWSFrame(
			conn,
			generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "first message must be {\"type\":\"auth\",\"token\":\"...\"}",
			},
		)
		return false
	}

	cfg := h.agentLoop.GetConfig()
	rawToken := authFrame.Token

	// 1. Check per-user list (RBAC — bcrypt token hash lookup).
	if len(cfg.Gateway.Users) > 0 {
		for _, user := range cfg.Gateway.Users {
			if user.TokenHash.Verify(rawToken) == nil {
				wc.role = user.Role
				wc.userID = user.Username // FR-073: needed for session_state user scoping
				conn.SetReadDeadline(time.Now().Add(60 * time.Second))
				return true
			}
		}
		// Token not in user list — reject.
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "unauthorized: invalid token",
		})
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
		)
		return false
	}

	// 2. Fallback: legacy OMNIPUS_BEARER_TOKEN env var (treated as admin role).
	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			// Dev mode: allow without auth, treated as admin.
			wc.role = config.UserRoleAdmin
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return true
		}
		// No auth configured — deny by default (fail closed), matching HTTP auth path.
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "no users configured, complete onboarding first",
		})
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
		)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) != 1 {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "unauthorized: invalid token",
		})
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
		)
		return false
	}
	wc.role = config.UserRoleAdmin
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	return true
}

// wsMaxMessageBytes is the maximum size of an incoming WebSocket message (5 MB).
// Messages exceeding this limit are rejected with an error frame and the connection
// is closed by gorilla/websocket (SetReadLimit causes a protocol-level close).
const wsMaxMessageBytes = 5 * 1024 * 1024

// readLoop processes client frames until the connection closes.
func (h *WSHandler) readLoop(ctx context.Context, conn *websocket.Conn, wc *wsConn, chatID string) {
	// Enforce a hard read limit so clients cannot exhaust server memory with
	// oversized frames. gorilla/websocket will return an error on the next
	// ReadMessage call if the incoming frame exceeds this limit.
	conn.SetReadLimit(wsMaxMessageBytes)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			// gorilla/websocket returns CloseMessageTooBig (1009) when a frame exceeds
			// SetReadLimit. Notify the client with a human-readable error frame before
			// the connection is torn down (the write may silently fail if already closed,
			// which is acceptable — we make a best-effort attempt).
			if websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
				slog.Warn(
					"ws: message too large, closing connection",
					"chat_id",
					chatID,
					"limit_bytes",
					wsMaxMessageBytes,
				)
				sendGenWSFrame(conn, generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "message too large (max 5MB)",
				})
				return
			}
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Debug("ws: connection closed unexpectedly", "chat_id", chatID, "error", err)
			}
			return
		}

		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			slog.Warn("ws: SetReadDeadline failed, exiting readLoop", "chat_id", chatID, "error", err)
			return
		}

		// Peek at the type discriminator before full decode.
		var peek wsTypeOnly
		if err := json.Unmarshal(data, &peek); err != nil {
			slog.Warn("ws: malformed frame", "error", err)
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "malformed message frame",
			})
			continue
		}

		// Per-frame JSON Schema validation (mirrors REST decodeAndValidate).
		// Gated by gateway.validate_inbound; when false the check is a no-op.
		validateEnabled := h.agentLoop.GetConfig().Gateway.ValidateInbound
		if validateEnabled {
			schemaName := wsFrameSchemaName(peek.Type)
			if schemaName != "" {
				if errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data); errMsg != "" {
					_wsInboundFrameDropped.Add(1)
					wc.inboundDropped.Add(1)
					if serverErr {
						// Server-side compile failure — log and drop; do not reveal details.
						slog.Error("ws: inbound schema unavailable, dropping frame",
							"schema", schemaName, "frame_type", peek.Type, "chat_id", chatID)
					} else {
						// Client-side schema violation — send descriptive error frame.
						slog.Warn("ws: inbound frame schema validation failed — dropping",
							"schema", schemaName, "frame_type", peek.Type, "error", errMsg, "chat_id", chatID)
						sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
							Type:    string(generated.WsFrameTypeError),
							Message: "frame schema validation failed (" + schemaName + "): " + errMsg,
						})
					}
					continue
				}
			}
		}

		switch peek.Type {
		case string(generated.WsFrameTypeMessage):
			var f generated.MessageFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed message frame", "error", err)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "malformed message frame",
				})
				continue
			}
			if f.Content == "" {
				continue
			}
			var agentID string
			if f.AgentId != nil {
				agentID = *f.AgentId
			}
			var sessionID string
			if f.SessionId != nil {
				sessionID = *f.SessionId
			}
			h.handleChatMessage(ctx, chatID, sessionID, f.Content, agentID, wc)
		case string(generated.WsFrameTypeCancel):
			var f generated.CancelFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed cancel frame", "error", err)
				continue
			}
			if f.SessionId == "" {
				wc.inboundDropped.Add(1)
				slog.Warn("ws: cancel frame missing required session_id — dropping",
					"chat_id", chatID)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "cancel requires session_id",
				})
				continue
			}
			h.handleCancel(wc, f.SessionId)
		case string(generated.WsFrameTypeExecApprovalResponse):
			var f generated.ExecApprovalResponseFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed exec_approval_response frame", "error", err)
				wc.inboundDropped.Add(1)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "malformed exec_approval_response frame",
				})
				continue
			}
			// Schema validation: decision must be one of the allowed values.
			switch f.Decision {
			case "allow", "deny", "always":
				// valid
			default:
				wc.inboundDropped.Add(1)
				slog.Warn("ws: exec_approval_response unknown decision — dropping",
					"decision", f.Decision, "id", f.Id, "chat_id", chatID)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "exec_approval_response: unknown decision value",
				})
				continue
			}
			h.handleApprovalResponse(f.Id, f.Decision, "", wc)
		case string(generated.WsFrameTypeAttachSession):
			var f generated.AttachSessionFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed attach_session frame", "error", err)
				continue
			}
			slog.Info("ws: attach_session frame received",
				"chat_id", chatID,
				"requested_session_id", f.SessionId,
			)
			if f.SessionId != "" {
				h.handleAttachSession(ctx, chatID, f.SessionId, f.Since, wc)
			} else {
				slog.Warn("ws: attach_session with empty session_id", "chat_id", chatID)
			}
		case string(generated.WsFrameTypeSessionClose):
			var f generated.SessionCloseFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed session_close frame", "error", err)
				continue
			}
			// FR-023: explicit session close request from the client.
			if f.SessionId == "" {
				wc.inboundDropped.Add(1)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "session_close requires session_id",
				})
				continue
			}
			if err := validation.EntityID(f.SessionId); err != nil {
				wc.inboundDropped.Add(1)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "invalid session_id",
				})
				continue
			}
			h.agentLoop.CloseSession(f.SessionId, "explicit")
			sid := f.SessionId
			sendConnGenFrame(wc, string(generated.WsFrameTypeSessionCloseAck), generated.SessionCloseAckFrame{
				Type:      string(generated.WsFrameTypeSessionCloseAck),
				SessionId: f.SessionId,
				Id:        &sid,
			})
		case string(generated.WsFrameTypePing):
			// Application-layer pong: the SPA's 60s "any frame received" liveness
			// check needs a server-originated frame during idle. Gorilla WS-protocol
			// ping/pong runs independently as NAT-keepalive.
			// Debounced to 1 pong/100ms/conn so a flood of pings cannot amplify into
			// outbound traffic against writePump's serialized sendCh.
			nowNs := time.Now().UnixNano()
			lastNs := wc.lastPongSentUnixNano.Load()
			if nowNs-lastNs >= int64(100*time.Millisecond) {
				wc.lastPongSentUnixNano.Store(nowNs)
				sendConnGenFrame(wc, string(generated.WsFrameTypePong), generated.PongFrame{
					Type: string(generated.WsFrameTypePong),
				})
			}
		case string(generated.WsFrameTypeDevicePairingResponse):
			var f generated.DevicePairingResponseFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed device_pairing_response frame", "error", err)
				wc.inboundDropped.Add(1)
				continue
			}
			h.handleDevicePairingResponse(f.DeviceId, f.Decision)
		default:
			slog.Debug("ws: unknown frame type ignored", "type", peek.Type, "chat_id", chatID)
		}
	}
}

// wsFrameSchemaName maps a WS frame type string to the corresponding inbound
// JSON Schema name (the key used in ValidateInboundFrameJSON). Returns ""
// for frame types that have no inbound schema (e.g. ping — no body to validate).
func wsFrameSchemaName(frameType string) string {
	switch frameType {
	case string(generated.WsFrameTypeMessage):
		return "MessageFrame"
	case string(generated.WsFrameTypeCancel):
		return "CancelFrame"
	case string(generated.WsFrameTypeExecApprovalResponse):
		return "ExecApprovalResponseFrame"
	case string(generated.WsFrameTypeAttachSession):
		return "AttachSessionFrame"
	case string(generated.WsFrameTypeDevicePairingResponse):
		return "DevicePairingResponseFrame"
	case string(generated.WsFrameTypeSessionClose):
		return "SessionCloseFrame"
	case string(generated.WsFrameTypePing):
		return "PingFrame"
	default:
		return ""
	}
}

// handleChatMessage mints a new session when frame.SessionID is empty, records
// every user message to the transcript, and publishes the message to the bus.
func (h *WSHandler) handleChatMessage(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	wc *wsConn,
) {
	targetAgentID := agentID
	if targetAgentID == "" {
		targetAgentID = "main"
	}
	store := h.agentLoop.GetSessionStore()
	if store == nil {
		store = h.agentLoop.GetAgentStore(targetAgentID)
	}

	sessionID := frameSessionID

	if store != nil {
		if sessionID == "" {
			// No session_id in frame: mint a new session so all subsequent frames have one.
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", targetAgentID)
			if err != nil {
				slog.Error("ws: could not create session", "error", err)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: fmt.Sprintf("could not create session: %v", err),
				})
				return
			}
			sessionID = meta.ID
			h.mu.Lock()
			h.sessionIDs[chatID] = meta.ID
			h.mu.Unlock()
			titleRunes := []rune(content)
			var title string
			if len(titleRunes) > 60 {
				title = string(titleRunes[:57]) + "..."
			} else {
				title = content
			}
			if err := store.SetMeta(meta.ID, session.MetaPatch{Title: &title}); err != nil {
				slog.Warn("ws: could not set session title", "session_id", meta.ID, "error", err)
			}
			// Ack the new session_id so the SPA can associate all subsequent frames.
			startedFrame := generated.SessionStartedFrame{
				Type:      string(generated.WsFrameTypeSessionStarted),
				SessionId: meta.ID,
			}
			if targetAgentID != "" {
				aid := targetAgentID
				startedFrame.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeSessionStarted), startedFrame)
		} else {
			// Validate client-supplied session_id format.
			if err := validation.EntityID(sessionID); err != nil {
				slog.Warn("ws: invalid session_id in message frame", "session_id", sessionID, "error", err)
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "invalid session_id format",
					SessionId: &sidCopy,
				})
				return
			}
			// Validate that the session actually exists in the store.
			if _, err := store.GetMeta(sessionID); err != nil {
				slog.Warn("ws: session not found", "session_id", sessionID, "error", err)
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "session not found",
					SessionId: &sidCopy,
				})
				return
			}
			// Track for streamer.
			h.mu.Lock()
			if h.sessionIDs[chatID] == "" {
				h.sessionIDs[chatID] = sessionID
			}
			h.mu.Unlock()
		}

		if sessionID != "" {
			entry := session.TranscriptEntry{
				ID:        uuid.New().String(),
				Role:      "user",
				AgentID:   targetAgentID,
				Content:   content,
				Timestamp: time.Now().UTC(),
			}
			if err := store.AppendTranscript(sessionID, entry); err != nil {
				slog.Warn("ws: could not record user message", "session_id", sessionID, "error", err)
			}
		}
	}

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SenderID:  "webchat_user",
		ChatID:    chatID,
		Content:   content,
		SessionID: sessionID,
	}
	if agentID != "" {
		if err := validateEntityID(agentID); err != nil {
			slog.Warn("ws: invalid agent_id in message frame; rejecting", "agent_id", agentID, "error", err)
			sidCopy := sessionID
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid agent_id",
				SessionId: &sidCopy,
			})
			return
		}
		msg.Metadata = map[string]string{"agent_id": agentID}
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.msgBus.PublishInbound(pubCtx, msg); err != nil {
		slog.Warn("ws: failed to publish message", "error", err)
		sidCopy := sessionID
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   fmt.Sprintf("failed to deliver message: %v", err),
			SessionId: &sidCopy,
		})
	}
}

// sendCancelStageFrame marshals a generated.CancelStageFrame and delivers it via wc.sendCh.
// Mirrors sendConnGenFrame's non-critical send path (immediate try, then 10ms/50ms
// backoffs) but is non-critical so it does not use sendConnGenFrame's critical-frame
// timeout path. Best-effort: marshal/send errors are logged at debug level and do not
// block the cancel state machine.
func sendCancelStageFrame(wc *wsConn, sessionID, stage string) {
	if wc == nil {
		return
	}
	data, err := json.Marshal(generated.CancelStageFrame{
		Type:      string(generated.WsFrameTypeCancelStage),
		SessionId: sessionID,
		Stage:     stage,
	})
	if err != nil {
		slog.Debug("ws: marshal cancel_stage frame failed", "stage", stage, "error", err)
		return
	}
	// Route through sendRawFrameBytes to respect replay-divert logic and the
	// replayMu serialization that prevents the TOCTOU race (code-reviewer Finding #2).
	sendRawFrameBytes(wc, string(generated.WsFrameTypeCancelStage), data)
	// sendRawFrameBytes logs at Warn on drop; suppress the duplicate debug log that
	// existed in the old inline implementation.
}

// handleCancel delegates to agentLoop.RequestCancel — the canonical cancel
// state machine that provides uniform audit, transcript, abuse-detection, and
// 2-stage timer behavior across all four cancel entry points (web SPA,
// Tier A /cancel command, Tier B text-parsing, CLI). FR-10, FR-11, FR-12,
// FR-13a, FR-15, FR-17, FR-18-21, FR-25a, FR-35, FR-36.
//
// This function is intentionally thin: it builds scope/canceller/hooks and
// delegates. All state-machine logic lives in pkg/agent.RequestCancel.
func (h *WSHandler) handleCancel(wc *wsConn, sessionID string) {
	if sessionID == "" {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel requires session_id",
		})
		return
	}

	scope := agent.CancelScope{SessionID: sessionID}
	canceller := agent.CancelCanceller{
		UserID:  wc.userID,
		Channel: "web",
	}
	hooks := agent.CancelHooks{
		SendStageFrame: func(sid, stage string) {
			sendCancelStageFrame(wc, sid, stage)
		},
		CancelPendingApprovals: func(sid, reason string) {
			if h.approvalRegV2 != nil {
				h.approvalRegV2.cancelAllPendingForSession(sid, reason)
			}
		},
		SetSessionInterrupted: func(sid string) {
			store := h.resolveSessionStore(sid)
			if store != nil {
				status := session.StatusInterrupted
				if err := store.SetMeta(sid, session.MetaPatch{Status: &status}); err != nil {
					slog.Warn("ws: could not mark session interrupted",
						"session_id", sid, "error", err)
				}
			}
		},
	}

	outcome, err := h.agentLoop.RequestCancel(context.Background(), scope, canceller, hooks)
	if err != nil {
		slog.Warn("ws: handleCancel: RequestCancel error",
			"session_id", sessionID, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "cancel failed: " + err.Error(),
		})
		return
	}
	if !outcome.Fired {
		slog.Debug("ws: cancel — no active turn or already canceled", "session_id", sessionID)
	}
}

// applySinceCursor applies the since-cursor filter to a slice of transcript entries.
//
// Returns entries strictly after `since`. Entries with zero timestamps (legacy
// data written before timestamps were added) are treated as oldest and dropped
// when a non-zero cursor is set — clients with legacy sessions should omit
// `since` to get full replay.
//
// When since is nil or empty, the original slice is returned unchanged (full
// replay). On parse failure an error frame is sent and the original slice is
// returned so the caller falls through to a full replay.
//
// wc may be nil in tests; the error-frame send is skipped when it is nil.
func applySinceCursor(
	_ context.Context,
	sessionID string,
	since *string,
	entries []session.TranscriptEntry,
	wc *wsConn,
) []session.TranscriptEntry {
	if since == nil || *since == "" {
		return entries
	}

	// Try RFC3339Nano first; fall back to RFC3339.
	cursor, parseErr := time.Parse(time.RFC3339Nano, *since)
	if parseErr != nil {
		var err2 error
		cursor, err2 = time.Parse(time.RFC3339, *since)
		if err2 != nil {
			slog.Warn("ws: attach_session: invalid since cursor — falling through to full replay",
				"event", "replay_since_parse_error",
				"session_id", sessionID,
				"since", *since,
				"error", parseErr,
			)
			if wc != nil {
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "invalid since timestamp — performing full replay",
				})
			}
			return entries
		}
	}

	// Filter: keep only entries with Timestamp strictly after the cursor.
	// <= cursor means the SPA already has this entry; > cursor is new to the SPA.
	// Zero-timestamp entries (legacy data) are never After(cursor) when cursor is
	// non-zero, so they are silently dropped — log a warning if any are present.
	filtered := entries[:0:0] // reuse backing array without aliasing
	var zeroTimestampCount int
	for _, e := range entries {
		if e.Timestamp.IsZero() {
			zeroTimestampCount++
			continue
		}
		if e.Timestamp.After(cursor) {
			filtered = append(filtered, e)
		}
	}

	if zeroTimestampCount > 0 {
		slog.Warn("replay cursor: dropped legacy entries with zero timestamp",
			"event", "replay_cursor_zero_timestamp_drop",
			"session_id", sessionID,
			"zero_timestamp_count", zeroTimestampCount,
		)
	}

	skipped := len(entries) - len(filtered) - zeroTimestampCount
	if skipped > 0 || zeroTimestampCount > 0 {
		slog.Debug("replay cursor applied",
			"event", "replay_cursor_applied",
			"session_id", sessionID,
			"cursor", cursor.Format(time.RFC3339Nano),
			"skipped_count", skipped,
			"zero_timestamp_dropped", zeroTimestampCount,
		)
	}
	return filtered
}

// handleAttachSession loads an existing session's transcript and replays it to
// the client via streamReplay, then sets the connection's active session to the
// requested session.
//
// FR-I-009: the connection is registered for live-event forwarding BEFORE the
// replay starts. Live events arriving during replay are buffered in a capped
// channel; after the done frame is emitted the buffer is drained to the WS in
// arrival order.
//
// since is the optional RFC3339/RFC3339Nano cursor from AttachSessionFrame.Since.
// When non-nil and non-empty, only transcript entries with Timestamp > cursor
// are replayed (O(missed-window) replay).  When nil or empty, a full replay is
// performed (legacy behavior).
func (h *WSHandler) handleAttachSession(
	ctx context.Context,
	chatID string,
	attachID string,
	since *string,
	wc *wsConn,
) {
	if err := validateEntityID(attachID); err != nil {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "invalid session_id",
		})
		return
	}

	store := h.resolveSessionStore(attachID)
	if store == nil {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "session not found",
		})
		return
	}

	entries, err := store.ReadTranscript(attachID)
	if err != nil {
		slog.Warn("ws: attach_session: could not read transcript", "session_id", attachID, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "could not read session transcript",
		})
		return
	}

	// Apply since-cursor filter when the client requests incremental replay.
	// On parse failure we log a warning, send an error frame, and fall through
	// to full replay — the client stays functional.
	//
	// Boundary condition: entries with Timestamp == cursor are skipped (<=).
	// Rationale: the cursor is the most recent frame the SPA has *already processed*,
	// so an entry at exactly that timestamp was already seen.  Strict less-than would
	// re-emit the boundary entry and cause a duplicate on the SPA.
	entries = applySinceCursor(ctx, attachID, since, entries, wc)

	rs := computeReplayStats(entries)

	// FR-I-013: structured log at replay start.
	// Include orphan/duplicate/truncated counts so the replay_start log
	// line carries enough context to debug fidelity issues without replay_end.
	slog.Info("ws: replay_start",
		"event", "replay_start",
		"session_id", attachID,
		"entry_count_loaded", len(entries),
		"tool_call_count_loaded", rs.toolCallCount,
		"span_count_detected", rs.spanCount,
		"orphan_count", rs.orphanCount,
		"duplicate_tool_call_id_count", rs.duplicateToolCallIDCount,
		"truncated_result_count", rs.truncatedResultCount,
	)
	replayStart := time.Now()

	// FR-I-009 / W1-1: register for live-event forwarding BEFORE starting replay
	// so no live events are lost during the replay window.
	//
	// Live events arriving via sendConnGenFrame during replay are diverted into
	// wc.replayDivertCh (allocated below) by the atomic flag wc.isReplayingLive.
	// writePump drains wc.sendCh as normal — replay frames go there directly.
	// After the done frame, the flag is cleared and the divert buffer is drained
	// into wc.sendCh in arrival order.
	//
	// This replaces the previous wc.sendCh swap which caused a data race because
	// writePump and pingPump read wc.sendCh concurrently with no synchronization.
	if wc.replayDivertCh == nil {
		wc.replayDivertCh = make(chan []byte, replayLiveBufferCap)
	}

	// Register for live event forwarding now (before flipping the replay flag).
	// h.sessions is keyed by chatID for the lifetime of the connection — do NOT
	// add an attachID alias here. taskChatIDs maps chatID→attachID so the event
	// forwarder can match events emitted under the attached session's ID.
	h.mu.Lock()
	if oldTID, ok := h.taskChatIDs[chatID]; ok {
		delete(h.sessionIDs, oldTID)
	}
	h.taskChatIDs[chatID] = attachID
	h.sessionIDs[attachID] = attachID
	h.mu.Unlock()

	// Arm the divert: any sendConnGenFrame calls after this point will route live
	// frames into replayDivertCh instead of sendCh.
	wc.isReplayingLive.Store(true)

	// Run replay: emit frames directly into wc.sendCh via emitFn, bypassing the
	// divert.  W1-10: a per-frame 5 s timeout prevents indefinite blocking when
	// the client is not draining the socket.
	emitFn := func(f any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, merr := json.Marshal(f)
		if merr != nil {
			return merr
		}
		select {
		case wc.sendCh <- data:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errSendTimeout
		}
	}

	// Pass pre-computed rs into streamReplay so it doesn't rebuild
	// spawnIDsWithChildren for a second time.
	var mediaStore media.MediaStore
	if h.agentLoop != nil {
		mediaStore = h.agentLoop.GetMediaStore()
	}
	framesEmitted, replayErr := streamReplay(ctx, attachID, entries, rs, emitFn, mediaStore, h.toolStore)

	durationMS := time.Since(replayStart).Milliseconds()

	if replayErr != nil {
		// Disarm the divert before emitting the abort frames so that sendConnGenFrame
		// routes them to sendCh. On the error path we skip the divert drain — the
		// buffered live frames are intentionally discarded because the replay itself
		// is being abandoned and the client is being told to reset.
		wc.isReplayingLive.Store(false)
		slog.Warn("ws: replay_aborted",
			"event", "replay_aborted",
			"session_id", attachID,
			"frames_emitted", framesEmitted,
			"duration_ms", durationMS,
			"error", replayErr,
		)
		// W1-5: emit error + synthetic done so the client clears isReplaying and
		// re-enables the composer.  Use sendConnGenFrame (generated types).
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			SessionId: &attachID,
			Message:   "replay aborted: " + replayErr.Error(),
		})
		replayErrTrue := true
		sendConnGenFrame(wc, string(generated.WsFrameTypeDone), generated.DoneFrame{
			Type:      string(generated.WsFrameTypeDone),
			SessionId: attachID,
			Stats: &generated.DoneStats{
				ReplayError: &replayErrTrue,
			},
		})
		return
	}

	// FR-I-013: structured log at replay end.
	// Include the full stats set so replay_end is a self-contained diagnostic record.
	slog.Info("ws: replay_end",
		"event", "replay_end",
		"session_id", attachID,
		"frames_emitted", framesEmitted,
		"duration_ms", durationMS,
		"orphan_count", rs.orphanCount,
		"duplicate_tool_call_id_count", rs.duplicateToolCallIDCount,
		"truncated_result_count", rs.truncatedResultCount,
	)

	// FR-I-009: drain any live events buffered during replay, in arrival order,
	// BEFORE disarming the divert flag.
	//
	// Ordering guarantee (see docs/investigation/bug-5-replay-order.md and
	// code-reviewer Finding #2 / architect Finding #4):
	//
	//   The flag must be cleared AFTER the drain, not before.  Clearing it first
	//   opens a window where concurrent sendRawFrameBytes callers write live frames
	//   directly to sendCh while the drain loop is still moving buffered divert
	//   frames into sendCh, inverting FIFO order.
	//
	//   Drain-then-disarm is safe when guarded by replayMu:
	//   - We hold replayMu.Lock() for the entire drain+disarm sequence.
	//   - sendRawFrameBytes holds replayMu.RLock() while choosing a target channel
	//     and completing its send.  This prevents the TOCTOU race where a writer
	//     snapshots isReplayingLive==true, is descheduled, the drain empties
	//     replayDivertCh and clears the flag, and the writer then sends to the
	//     now-abandoned replayDivertCh.
	//   - After the drain+disarm, replayMu is released; future sendRawFrameBytes
	//     calls see isReplayingLive==false on the first atomic load (fast path, no
	//     lock taken) and route directly to sendCh in the correct position.
	//
	//   Back-pressure defense (architect Finding #4): each frame send inside the
	//   drain uses a 1-second deadline.  If sendCh is full and the client is slow,
	//   the frame is dropped with a Warn rather than blocking the drain indefinitely.
	//   The connection stays usable; the SPA will reconcile any missing frames on the
	//   next attach_session.
	wc.replayMu.Lock()
	for {
		select {
		case raw := <-wc.replayDivertCh:
			select {
			case wc.sendCh <- raw:
			case <-time.After(1 * time.Second):
				slog.Warn("ws: replay drain frame timed out, dropping",
					"session_id", attachID,
					"chat_id", chatID)
				wc.droppedFrames.Add(1)
			case <-ctx.Done():
				wc.isReplayingLive.Store(false)
				wc.replayMu.Unlock()
				return
			}
		default:
			goto drainDone
		}
	}
drainDone:
	// Disarm AFTER drain, while still holding replayMu.Lock().  Releasing the lock
	// after the Store ensures any writer that is queued behind our Lock() will see
	// isReplayingLive==false on its re-check and route to sendCh directly.
	wc.isReplayingLive.Store(false)
	wc.replayMu.Unlock()

	h.mu.Lock()
	h.sessionIDs[chatID] = attachID
	h.mu.Unlock()

	// Hydrate the per-agent session.SessionStore from the transcript so the
	// next LLM turn sees the prior conversation. Without this, the SPA
	// shows replayed messages but the agent answers as if the session just
	// started — see pkg/agent/attach_hydrate.go for the rationale.
	if err := h.agentLoop.HydrateAgentHistoryFromTranscript(attachID); err != nil {
		slog.Warn("ws: attach_session: hydrate agent history failed",
			"session_id", attachID, "error", err)
		sidCopy := attachID
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			SessionId: &sidCopy,
			Message:   "could not restore conversation context — agent may not remember earlier turns",
		})
	}

	slog.Debug("ws: attached to session", "chat_id", chatID, "session_id", attachID)
}

// handleApprovalResponse resolves a pending exec approval request.
// Called from readLoop when the browser sends an "exec_approval_response" frame.
// "allow" maps to VerdictAllow, "always" maps to VerdictAlways, everything else maps to VerdictDeny.
// If the frame carries a session_id that doesn't match the registry entry, the response is rejected.
func (h *WSHandler) handleApprovalResponse(id, decision, sessionID string, wc *wsConn) {
	if id == "" {
		slog.Warn("ws: exec_approval_response missing id")
		return
	}
	// Validate that the frame's session_id matches the request's recorded session_id.
	// This prevents a tab from approving a request that belongs to a different session.
	if registeredSID, ok := h.approvalRegistry.getSessionID(id); ok && sessionID != "" && registeredSID != "" {
		if sessionID != registeredSID {
			slog.Warn("ws: exec_approval_response session_id mismatch — rejecting",
				"id", id, "frame_session", sessionID, "registered_session", registeredSID)
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "approval session_id mismatch",
			})
			return
		}
	}
	var verdict agent.ApprovalVerdict
	switch decision {
	case "allow":
		verdict = agent.VerdictAllow
	case "always":
		verdict = agent.VerdictAlways
	default:
		verdict = agent.VerdictDeny
	}
	d := agent.ApprovalDecision{Verdict: verdict}
	if verdict == agent.VerdictDeny {
		d.Reason = "user denied via WebSocket"
	}
	if !h.approvalRegistry.resolve(id, d) {
		// The request may have already timed out — this is informational, not an error.
		slog.Debug("ws: exec_approval_response for unknown or expired request", "id", id, "decision", decision)
	} else {
		slog.Info("ws: exec_approval resolved", "id", id, "decision", decision, "verdict", verdict)
		var sidPtr *string
		if sessionID != "" {
			sidCopy := sessionID
			sidPtr = &sidCopy
		}
		sendConnGenFrame(
			wc,
			string(generated.WsFrameTypeExecApprovalResponseAck),
			generated.ExecApprovalResponseAckFrame{
				Type:      string(generated.WsFrameTypeExecApprovalResponseAck),
				Id:        &id,
				SessionId: sidPtr,
			},
		)
	}
}

// wsPingMsg is a nil sentinel enqueued by pingPump to signal writePump to send a WebSocket ping.
// Using a sentinel through sendCh ensures all writes go through the single writer goroutine,
// satisfying gorilla/websocket's single-writer requirement (fix for gorilla write race).
// Important: do not pass nil []byte through sendCh for any other purpose — nil is reserved as the ping sentinel.
var wsPingMsg []byte

// errSendTimeout is returned by the replay emitFn when the send channel is
// full for more than 5 seconds (W1-10). The caller (streamReplay) surfaces
// this via W1-5's error+done emission so the client can recover.
var errSendTimeout = fmt.Errorf("ws: send channel full — replay send timeout")

// writePump is the single goroutine that writes all frames to the WebSocket connection.
// gorilla/websocket requires all writes to happen from the same goroutine.
// A nil message on sendCh is the sentinel for a ping frame.
func (h *WSHandler) writePump(wc *wsConn) {
	for {
		select {
		case msg, ok := <-wc.sendCh:
			if !ok {
				return
			}
			if msg == nil {
				// nil sentinel: send a WebSocket ping frame.
				if err := wc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					slog.Debug("ws: ping write error", "error", err)
					return
				}
				continue
			}
			if err := wc.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				slog.Debug("ws: write error", "error", err)
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// pingPump enqueues a nil sentinel onto sendCh every 30 s for keep-alive pings.
// All writes go through writePump, satisfying gorilla's single-writer requirement.
func (h *WSHandler) pingPump(wc *wsConn) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case wc.sendCh <- wsPingMsg: // nil sentinel triggers a ping in writePump
			case <-wc.doneCh:
				return
			}
		case <-wc.doneCh:
			return
		}
	}
}

// sendConnGenFrame marshals a frame and enqueues it on wc's send channel.
// For "done", "error", and approval frames, blocks up to 5 s rather than dropping,
// because losing these frames would leave the client in a permanently stuck state.
// For non-critical frames, retries with short delays (immediate, 10ms, 50ms) before dropping.
// After 20 cumulative dropped frames a "degraded" error frame is
// injected into the critical path to warn the client; the counter resets on success.
//
// During replay (wc.isReplayingLive == true), live frames arriving from the
// eventForwarder are diverted into wc.replayDivertCh so they do not interleave
// with replay frames that are being written directly to wc.sendCh. After replay
// finishes, handleAttachSession drains replayDivertCh into sendCh in order.
// This replaces the old wc.sendCh swap which caused a data race.
//
// droppedFramesWarnThreshold is the number of consecutively dropped non-critical
// frames after which a "connection degraded" error is sent to the browser.
const droppedFramesWarnThreshold = 20

// sendConnGenFrame marshals any generated frame type (from pkg/api/generated) and
// routes it to the connection with the same backpressure and replay-divert logic as
// sendConnGenFrame.  frameType is the string value of the frame's "type" field, used
// to determine whether the frame is critical (never dropped, blocks briefly).
func sendConnGenFrame(wc *wsConn, frameType string, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal generated frame failed", "type", frameType, "error", err)
		return
	}
	sendRawFrameBytes(wc, frameType, data)
}

// sendRawFrameBytes routes pre-marshaled frame bytes to the connection's send channel.
// It implements the replay-divert logic (W1-1), critical-frame blocking, and
// backpressure drop logic shared by sendConnGenFrame and wsStreamer.Update.
// frameType is used to determine criticality (done, error, exec_approval_*).
//
// Ordering guarantee (see docs/investigation/bug-5-replay-order.md, code-reviewer
// Finding #2): the channel-selection decision (read isReplayingLive + pick targetCh)
// and the channel send are performed while holding wc.replayMu.RLock().  The drain in
// handleAttachSession holds wc.replayMu.Lock() for the entire drain+disarm sequence.
// This prevents the TOCTOU race where a writer snapshots isReplayingLive==true, is
// descheduled, the drain empties replayDivertCh and disarms the flag, and the writer
// then sends to the now-abandoned replayDivertCh.
//
// On the non-replay hot path (isReplayingLive==false) the RLock is never acquired,
// keeping the common case lock-free.
func sendRawFrameBytes(wc *wsConn, frameType string, data []byte) {
	// W1-1: if replay mode is active, divert live frames into the replay buffer
	// instead of wc.sendCh, so writePump never sees them while replay is running.
	// "done", "error", and critical control frames are always sent to the canonical
	// sendCh regardless of replay state — they are emitted by streamReplay itself
	// and must reach writePump immediately.
	isCritical := frameType == "done" || frameType == "error" ||
		frameType == "exec_approval_request" || frameType == "exec_approval_expired"

	// Fast path: not replaying (atomic check, no lock). This is the common case.
	if !wc.isReplayingLive.Load() || isCritical {
		// Fall through to the send logic below with targetCh = sendCh.
	} else {
		// Slow path: replay is active. Hold RLock so the drain's Lock() cannot disarm
		// the flag until after we have completed the send into replayDivertCh.
		wc.replayMu.RLock()
		// Re-check under the lock: the drain may have disarmed the flag while we were
		// waiting for RLock.
		if wc.isReplayingLive.Load() && wc.replayDivertCh != nil {
			// Route to divert channel while holding the read-lock for the ENTIRE
			// send. Pass-2 reviewer caught: previous version RUnlock'd before the
			// send, letting the drain disarm + close the divert channel between
			// our RUnlock and the targetCh <- data write — orphaned-frame race.
			// Holding RLock through the send ensures the drain's exclusive Lock()
			// cannot fire until after our send completes.
			targetCh := wc.replayDivertCh
			defer wc.replayMu.RUnlock()
			//nolint:dupl // Mirrors the sendCh path below; differs by target channel + lock-holding context.
			switch {
			case isCritical:
				select {
				case targetCh <- data:
				case <-time.After(5 * time.Second):
					slog.Warn(
						"ws: send channel full after timeout for critical frame, closing connection",
						"type",
						frameType,
					)
					wc.close()
				}
			default:
				backoffs := [...]time.Duration{0, 10 * time.Millisecond, 50 * time.Millisecond}
				for _, wait := range backoffs {
					if wait == 0 {
						select {
						case targetCh <- data:
							wc.droppedFrames.Store(0)
							return
						default:
						}
					} else {
						t := time.NewTimer(wait)
						select {
						case targetCh <- data:
							t.Stop()
							wc.droppedFrames.Store(0)
							return
						case <-t.C:
						}
					}
				}
				slog.Warn("ws: send channel full after backoff, frame dropped", "type", frameType)
				wc.droppedTokens.Add(1)
				wc.droppedFrames.Add(1)
				if wc.droppedFrames.Load() >= int32(droppedFramesWarnThreshold) {
					wc.droppedFrames.Store(0)
					degraded, merr := json.Marshal(generated.ErrorFrame{
						Type:    string(generated.WsFrameTypeError),
						Message: "connection degraded: frames being dropped due to backpressure",
					})
					if merr != nil {
						slog.Error("ws: marshal degraded frame failed", "error", merr)
						return
					}
					select {
					case wc.sendCh <- degraded:
					case <-time.After(5 * time.Second):
						slog.Warn("ws: could not deliver degraded warning frame, closing connection")
						wc.close()
					}
				}
			}
			return
		}
		wc.replayMu.RUnlock()
		// Flag was cleared before we got the lock — fall through to direct sendCh path.
	}

	targetCh := wc.sendCh

	//nolint:dupl // Mirrors the replayDivertCh path above; differs by target channel + lock-holding context.
	switch {
	case isCritical:
		// Critical frames must not be dropped. Block briefly; force-close on timeout.
		// Approval frames are critical: dropping them leaves the agent turn blocked for
		// the full approval timeout (90 s) and then results in a mysterious denial.
		select {
		case targetCh <- data:
		case <-time.After(5 * time.Second):
			slog.Warn("ws: send channel full after timeout for critical frame, closing connection", "type", frameType)
			wc.close()
		}
	default:
		// Try immediate send, then graduated retry delays (10 ms, 50 ms) before dropping.
		backoffs := [...]time.Duration{0, 10 * time.Millisecond, 50 * time.Millisecond}
		for _, wait := range backoffs {
			if wait == 0 {
				select {
				case targetCh <- data:
					wc.droppedFrames.Store(0)
					return
				default:
				}
			} else {
				t := time.NewTimer(wait)
				select {
				case targetCh <- data:
					t.Stop()
					wc.droppedFrames.Store(0)
					return
				case <-t.C:
					// Timer expired, try next delay.
				}
			}
		}

		// All attempts exhausted — drop the frame and record backpressure.
		slog.Warn("ws: send channel full after backoff, frame dropped", "type", frameType)
		wc.droppedTokens.Add(1)
		wc.droppedFrames.Add(1)

		// After threshold drops, warn the client over the critical path so it knows
		// the connection is degraded. The degraded warning always goes to the canonical
		// wc.sendCh — never to replayDivertCh — so the user sees the overflow warning
		// immediately without waiting for replay to drain (W1-6).
		if wc.droppedFrames.Load() >= int32(droppedFramesWarnThreshold) {
			wc.droppedFrames.Store(0)
			degraded, merr := json.Marshal(generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "connection degraded: frames being dropped due to backpressure",
			})
			if merr != nil {
				slog.Error("ws: marshal degraded frame failed", "error", merr)
				return
			}
			select {
			case wc.sendCh <- degraded:
			case <-time.After(5 * time.Second):
				slog.Warn("ws: could not deliver degraded warning frame, closing connection")
				wc.close()
			}
		}
	}
}

// sendGenWSFrame writes a generated frame directly to a connection (used before the send goroutine starts).
// frameType is the frame's type discriminator value (e.g. "error"), used for logging only.
func sendGenWSFrame(conn *websocket.Conn, frame any) {
	data, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal frame failed", "error", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		slog.Debug("ws: write frame failed", "error", err)
	}
}

// orphanWatchdogTimeout is the duration the forwarder waits after a parent turn ends
// before synthesizing a subagent_end{status:"interrupted"} for any still-open span.
// Configurable so tests can override to a short value (e.g., 200ms) without sleeping.
//
// Bumped 2026-05-11 from 5s → 60s. The old value killed legitimate subagents:
// a sub-turn that runs 3 shell calls back-to-back through a real LLM regularly
// takes 6–12s of wall-clock (1–4s per turn iteration × N tool calls), and Mia's
// root turn ends within ~2s of dispatching `spawn`. With a 5s watchdog the
// subagent was synthesizing `status:"interrupted"` after the second shell call
// even though the agent loop was still executing — closes the cascade of
// suite-load flakes in subagent.spec.ts (a)–(e) and handoff.spec.ts (b).
//
// 60s is a conservative upper bound for a single sub-turn; the parent-loop
// `subturn.default_timeout_minutes` config knob already enforces a hard
// runtime cap higher up the stack for legitimately stuck sub-turns.
var orphanWatchdogTimeout = 60 * time.Second

// openSpanEntry tracks an in-flight subagent span in the event forwarder.
type openSpanEntry struct {
	spanID          string
	parentCallID    string
	agentID         string
	sessionID       string        // session_id at spawn time; carried on synthesized frames
	parentTurnEnded bool          // set to true when EventKindTurnEnd fires for the parent turn
	closeCh         chan struct{} // closed when EventKindSubTurnEnd arrives (cancels watchdog)
}

// eventForwarder listens on the agent EventBus and forwards tool_call_start/result
// frames to the browser so tool call UIs render in real time.
// It also matches events from an attached task session (via taskChatIDs).
// Extended (FR-H-004, FR-H-005): emits subagent_start / subagent_end frames and
// propagates parent_call_id on tool_call_* frames fired inside sub-turns.
// Orphan watchdog (FR-H-004, Scenario 7): when the parent turn ends before all spans
// are closed, a timer fires after orphanWatchdogTimeout and synthesizes
// subagent_end{status:"interrupted"} for each still-open span.
func (h *WSHandler) eventForwarder(wc *wsConn, chatID string, sub agent.EventSubscription, done chan<- struct{}) {
	defer close(done)

	// matchesChatID returns true if evtChatID belongs to this connection's chat or
	// to a task session the connection has attached to via handleAttachSession.
	matchesChatID := func(evtChatID string) bool {
		if evtChatID == chatID {
			return true
		}
		h.mu.Lock()
		tid := h.taskChatIDs[chatID]
		h.mu.Unlock()
		// Note: using exclusive lock for a read-only lookup. Acceptable for now;
		// migrate h.mu to sync.RWMutex if contention becomes measurable.
		return tid != "" && evtChatID == tid
	}

	// sessionIDForChat looks up the active session_id for a given chatID so every
	// event frame can carry it, enabling per-session routing in the SPA.
	sessionIDForChat := func(evtChatID string) string {
		h.mu.Lock()
		sid := h.sessionIDs[evtChatID]
		if sid == "" {
			// Also check the task alias.
			if tid := h.taskChatIDs[evtChatID]; tid != "" {
				sid = h.sessionIDs[tid]
			}
		}
		h.mu.Unlock()
		return sid
	}

	// openSpans tracks in-flight subagent spans keyed by parentCallID.
	// Accessed only from the single eventForwarder goroutine — no mutex needed.
	openSpans := make(map[string]*openSpanEntry)

	// closeSpan marks a span as resolved and signals its watchdog to stop.
	closeSpan := func(parentCallID string) {
		if entry, ok := openSpans[parentCallID]; ok {
			select {
			case <-entry.closeCh: // already closed
			default:
				close(entry.closeCh)
			}
			delete(openSpans, parentCallID)
		}
	}

	// startOrphanWatchdog launches a goroutine that fires after orphanWatchdogTimeout
	// if the span is not closed first. On timeout it synthesizes subagent_end and logs.
	// W1-9: the goroutine also exits cleanly when wc.doneCh is closed (connection torn down).
	startOrphanWatchdog := func(entry *openSpanEntry, reason string) {
		go func() {
			select {
			case <-entry.closeCh:
				// Span resolved normally — nothing to do.
				return
			case <-wc.doneCh:
				// Connection closed while waiting — exit cleanly without emitting.
				return
			case <-time.After(orphanWatchdogTimeout):
				// Span is still open after timeout. Emit interrupted.
				if reason == "unknown" {
					slog.Error("ws: subagent span orphaned with unknown reason — synthesizing interrupted end",
						"event", "span_orphan_interrupted",
						"span_id", entry.spanID,
						"parent_call_id", entry.parentCallID,
						"reason", reason,
					)
				} else {
					slog.Warn("ws: subagent span orphaned — synthesizing interrupted end",
						"event", "span_orphan_interrupted",
						"span_id", entry.spanID,
						"parent_call_id", entry.parentCallID,
						"reason", reason,
					)
				}
				// Use generated.SubagentEndFrame (contract-first migration).
				reason_ := reason // capture for pointer
				agentID_ := entry.agentID
				endFrame := generated.SubagentEndFrame{
					Type:      string(generated.WsFrameTypeSubagentEnd),
					SessionId: entry.sessionID,
					SpanId:    entry.spanID,
					Status:    "interrupted",
					Message:   &reason_,
				}
				if agentID_ != "" {
					endFrame.AgentId = &agentID_
				}
				if entry.parentCallID != "" {
					pc := entry.parentCallID
					endFrame.ParentCallId = &pc
				}
				sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentEnd), endFrame)
			}
		}()
	}

	for evt := range sub.C {
		switch evt.Kind {
		case agent.EventKindSubTurnSpawn:
			// FR-H-004: emit subagent_start when a sub-turn is spawned.
			p, ok := evt.Payload.(agent.SubTurnSpawnPayload)
			if !ok || !matchesChatID(p.ChatID) {
				continue
			}
			slog.Debug("ws: subagent_start",
				"span_id", p.SpanID,
				"parent_call_id", p.ParentSpawnCallID,
				"agent_id", p.AgentID,
			)
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			spawnSID := p.SessionID
			if spawnSID == "" {
				spawnSID = sessionIDForChat(p.ChatID)
			}
			// Use generated.SubagentStartFrame (contract-first migration).
			spawnFrame := generated.SubagentStartFrame{
				Type:         string(generated.WsFrameTypeSubagentStart),
				SessionId:    spawnSID,
				SpanId:       p.SpanID,
				ParentCallId: string(p.ParentSpawnCallID),
				TaskLabel:    p.TaskLabel,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				spawnFrame.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentStart), spawnFrame)
			// Register the span in openSpans for orphan watchdog tracking.
			entry := &openSpanEntry{
				spanID:       p.SpanID,
				parentCallID: string(p.ParentSpawnCallID),
				agentID:      p.AgentID,
				sessionID:    spawnSID,
				closeCh:      make(chan struct{}),
			}
			openSpans[string(p.ParentSpawnCallID)] = entry

		case agent.EventKindSubTurnEnd:
			// FR-H-004: emit subagent_end when a sub-turn finishes.
			p, ok := evt.Payload.(agent.SubTurnEndPayload)
			if !ok || !matchesChatID(p.ChatID) {
				continue
			}
			slog.Debug("ws: subagent_end",
				"span_id", p.SpanID,
				"parent_call_id", p.ParentSpawnCallID,
				"agent_id", p.AgentID,
			)
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			endSID := p.SessionID
			if endSID == "" {
				endSID = sessionIDForChat(p.ChatID)
			}
			// Use generated.SubagentEndFrame (contract-first migration).
			endFrameEnd := generated.SubagentEndFrame{
				Type:      string(generated.WsFrameTypeSubagentEnd),
				SessionId: endSID,
				SpanId:    p.SpanID,
				Status:    string(p.Status),
			}
			if p.DurationMS != 0 {
				dm := int(p.DurationMS)
				endFrameEnd.DurationMs = &dm
			}
			if p.AgentID != "" {
				aid := p.AgentID
				endFrameEnd.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				endFrameEnd.ParentCallId = &pc
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeSubagentEnd), endFrameEnd)
			// Signal the watchdog that the span closed normally.
			closeSpan(string(p.ParentSpawnCallID))

		case agent.EventKindTurnEnd:
			// W1-2: only arm the orphan watchdog when the root turn for this
			// connection ends (IsRoot == true) and the event belongs to our chat
			// (ChatID matches). Sub-turn ends from sibling sub-turns would otherwise
			// spuriously interrupt still-running spans on this connection.
			p, ok := evt.Payload.(agent.TurnEndPayload)
			if !ok || !p.IsRoot || !matchesChatID(p.ChatID) {
				continue
			}
			// Determine watchdog reason from the terminal status of the parent turn.
			var watchdogReason string
			switch p.Status {
			case agent.TurnEndStatusAborted:
				watchdogReason = "parent_cancelled" //nolint:misspell // wire value, frontend TS union
			case agent.TurnEndStatusError:
				watchdogReason = "parent_timeout"
			case agent.TurnEndStatusCompleted:
				watchdogReason = "parent_done_early"
			default:
				watchdogReason = "unknown"
			}
			for _, entry := range openSpans {
				if !entry.parentTurnEnded {
					entry.parentTurnEnded = true
					startOrphanWatchdog(entry, watchdogReason)
				}
			}

		case agent.EventKindToolExecStart:
			p, ok := evt.Payload.(agent.ToolExecStartPayload)
			if !ok || !matchesChatID(p.ChatID) {
				continue
			}
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			startSID := p.SessionID
			if startSID == "" {
				startSID = sessionIDForChat(p.ChatID)
			}
			// FR-H-005: propagate parent_call_id when the tool fires inside a sub-turn.
			// FR-I-008: propagate agent_id so live frames match replay frame parity.
			// Nil-safety: params MUST be object (never null) — SPA calls Object.keys(params).
			startArgs := p.Arguments
			if startArgs == nil {
				startArgs = map[string]any{}
			}
			// Use generated.ToolCallStartFrame (contract-first migration).
			startF := generated.ToolCallStartFrame{
				Type:      string(generated.WsFrameTypeToolCallStart),
				SessionId: startSID,
				CallId:    string(p.ToolCallID),
				Tool:      p.Tool,
				Params:    startArgs,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				startF.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				startF.ParentCallId = &pc
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallStart), startF)
		case agent.EventKindToolExecEnd:
			p, ok := evt.Payload.(agent.ToolExecEndPayload)
			if !ok || !matchesChatID(p.ChatID) {
				continue
			}
			status := "success"
			if p.IsError {
				status = "error"
			}
			// Prefer SessionID from payload; fall back to map lookup for legacy events.
			evtSID := p.SessionID
			if evtSID == "" {
				evtSID = sessionIDForChat(p.ChatID)
			}
			// FR-H-005: propagate parent_call_id when the tool fires inside a sub-turn.
			// FR-I-008: propagate agent_id so live frames match replay frame parity.
			// Use generated.ToolCallResultFrame (contract-first migration).
			//
			// Apply the lazy-fetch offload policy: when the string result exceeds
			// InlineToolResultMaxBytes (50 KiB), persist it to disk and substitute a
			// generated.ToolResultRef sentinel so the WS frame stays small.
			var liveResult any = p.Result
			if len(p.Result) > InlineToolResultMaxBytes {
				// JSON-encode the string to get the exact wire size.
				if encoded, merr := json.Marshal(p.Result); merr == nil {
					if sentinel, offloaded := maybeOffloadResult(h.toolStore, evtSID, encoded); offloaded {
						liveResult = sentinel
					}
				}
			}
			resultF := generated.ToolCallResultFrame{
				Type:      string(generated.WsFrameTypeToolCallResult),
				SessionId: evtSID,
				CallId:    string(p.ToolCallID),
				Tool:      p.Tool,
				Result:    liveResult,
				Status:    status,
			}
			if p.Duration != 0 {
				dm := int(p.Duration.Milliseconds())
				resultF.DurationMs = &dm
			}
			if p.AgentID != "" {
				aid := p.AgentID
				resultF.AgentId = &aid
			}
			if p.ParentSpawnCallID != "" {
				pc := string(p.ParentSpawnCallID)
				resultF.ParentCallId = &pc
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallResult), resultF)
			// When the handoff tool succeeds, notify the frontend to switch agents.
			// Use evtSID (the session ID from the payload) to key the lookup, not chatID.
			if p.Tool == "handoff" && status == "success" {
				if activeAgent, ok := h.agentLoop.GetSessionActiveAgent(evtSID); ok {
					agentName, _ := h.agentLoop.GetRegistry().GetAgentName(activeAgent)
					// Use generated.AgentSwitchedFrame (contract-first migration).
					switchF := generated.AgentSwitchedFrame{
						Type:      string(generated.WsFrameTypeAgentSwitched),
						SessionId: evtSID,
					}
					if activeAgent != "" {
						switchF.AgentId = &activeAgent
					}
					if agentName != "" {
						switchF.Message = &agentName
					}
					sendConnGenFrame(wc, string(generated.WsFrameTypeAgentSwitched), switchF)
				}
			}
			if p.Tool == "return_to_default" && status == "success" {
				defaultAgent := h.agentLoop.GetRegistry().GetDefaultAgent()
				var defaultName string
				if defaultAgent != nil {
					defaultName = defaultAgent.Name
				}
				// Use generated.AgentSwitchedFrame (contract-first migration).
				switchF := generated.AgentSwitchedFrame{
					Type:      string(generated.WsFrameTypeAgentSwitched),
					SessionId: evtSID,
					// AgentId omitted (nil ptr) = return to default agent
				}
				if defaultName != "" {
					switchF.Message = &defaultName
				}
				sendConnGenFrame(wc, string(generated.WsFrameTypeAgentSwitched), switchF)
			}
		case agent.EventKindRateLimit:
			// SEC-26: forward rate-limit denials to the browser so the chat UI
			// can display an inline indicator. Global-scope events (daily cost
			// cap) are broadcast to every connection since they are not tied
			// to a specific chatID.
			p, ok := evt.Payload.(agent.RateLimitPayload)
			if !ok {
				continue
			}
			if p.Scope != "global" && !matchesChatID(p.ChatID) {
				continue
			}
			// Use generated.RateLimitFrame (contract-first migration).
			rateSID := sessionIDForChat(p.ChatID)
			rateF := generated.RateLimitFrame{
				Type:              string(generated.WsFrameTypeRateLimit),
				SessionId:         rateSID,
				Scope:             p.Scope,
				Resource:          p.Resource,
				PolicyRule:        p.PolicyRule,
				RetryAfterSeconds: p.RetryAfterSeconds,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				rateF.AgentId = &aid
			}
			if p.Tool != "" {
				tool := p.Tool
				rateF.Tool = &tool
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeRateLimit), rateF)
		}
	}
}

// wsStreamer implements bus.Streamer, pushing token/done frames into a wsConn's send channel.
// It also accumulates the full response to persist it to the session transcript on Finalize.
type wsStreamer struct {
	conn        *wsConn
	chatID      string
	sessionID   string                // for recording assistant message
	agentStore  *session.UnifiedStore // for recording assistant message
	agentID     string                // active agent at streamer creation time (for transcript AgentID)
	channel     *webchatChannel       // to mark streaming complete and suppress duplicate Send()
	accumulated strings.Builder       // accumulates full response text

	// Turn-level stats set by the agent loop via SetTurnStats before Finalize.
	// Populates the "done" frame so the chat UI shows real token counts and
	// cost instead of zeros (issue #12). Mutex-protected because SetTurnStats
	// and Finalize may be called from different goroutines.
	statsMu       sync.Mutex
	statsTokens   int64
	statsCostUSD  float64
	statsDuration time.Duration
}

// SetTurnStats is called by the agent loop's finalizeStreamer just before
// Finalize. Implements the streamerStatsSetter interface from pkg/agent.
func (s *wsStreamer) SetTurnStats(tokens int64, costUSD float64, duration time.Duration) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTokens = tokens
	s.statsCostUSD = costUSD
	s.statsDuration = duration
}

func (s *wsStreamer) Update(_ context.Context, content string) error {
	data, err := json.Marshal(generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   content,
		SessionId: s.sessionID,
	})
	if err != nil {
		return fmt.Errorf("ws: marshal token frame: %w", err)
	}
	// Route through sendRawFrameBytes so that token frames respect the replay-divert
	// logic.  If a client reconnects mid-turn and attach_session triggers replay while
	// the agent is still streaming tokens, those token frames must be buffered in
	// replayDivertCh — not written directly to sendCh — so they arrive at the client
	// after the replay history rather than interspersed with it.
	//
	// sendRawFrameBytes also holds wc.replayMu.RLock() for the channel-selection +
	// send operation, which prevents the TOCTOU race described in
	// docs/investigation/bug-5-replay-order.md (code-reviewer Finding #2).
	//
	// sendRawFrameBytes uses a 3-attempt backoff and increments BOTH droppedFrames
	// AND droppedTokens on final drop.  We do not duplicate the droppedTokens
	// increment here; sendRawFrameBytes is the single authoritative counter.
	//
	// On back-pressure (channel full after all retries), sendRawFrameBytes logs at
	// Warn level; we return an error so the caller knows the token was lost.
	originalDropped := s.conn.droppedTokens.Load()
	sendRawFrameBytes(s.conn, string(generated.WsFrameTypeToken), data)
	if s.conn.droppedTokens.Load() > originalDropped {
		slog.Warn("ws: token backpressure", "session_id", s.sessionID, "chat_id", s.chatID, "agent_id", s.agentID)
		return fmt.Errorf("ws: token channel full, token dropped")
	}
	s.accumulated.WriteString(content)
	// Cross-browser session attach (#133): also forward the token to every
	// other connection bound to the same session. The originating chat
	// already received the frame above; secondary tabs see the live stream
	// through this fan-out instead of waiting for a transcript reload.
	s.fanOutToSessionPeers(data)
	return nil
}

// fanOutToSessionPeers ships a frame to every wsConn that shares this
// streamer's session, skipping the originating connection. Used by Update
// (token frames) and Finalize (done frame) so a second browser tab attached
// mid-turn observes the live stream as it happens.
func (s *wsStreamer) fanOutToSessionPeers(data []byte) {
	if s.channel == nil || s.sessionID == "" {
		return
	}
	h := s.channel.wsHandler
	if h == nil {
		return
	}
	h.mu.Lock()
	peers := make([]*wsConn, 0, 1)
	for chatID, sid := range h.sessionIDs {
		if sid != s.sessionID || chatID == s.chatID {
			continue
		}
		if conn, ok := h.sessions[chatID]; ok && conn != s.conn {
			peers = append(peers, conn)
		}
	}
	h.mu.Unlock()
	for _, peer := range peers {
		select {
		case peer.sendCh <- data:
		default:
			peer.droppedTokens.Add(1)
			slog.Warn("ws: peer token dropped", "session_id", s.sessionID)
		}
	}
}

func (s *wsStreamer) Finalize(_ context.Context, finalContent string) error {
	// Build the typed DoneStats for the done frame using the generated type.
	doneStats := &generated.DoneStats{}
	if dropped := s.conn.droppedTokens.Load(); dropped > 0 {
		droppedF := float64(dropped)
		doneStats.TokensDropped = &droppedF
	}
	// Include turn-level token/cost/duration if the agent loop pushed them via
	// SetTurnStats before this call (issue #12). Zero values are still emitted
	// so the client can reset the session counters for turns with no LLM usage.
	s.statsMu.Lock()
	tokensF := float64(s.statsTokens)
	costF := s.statsCostUSD
	durF := float64(s.statsDuration.Milliseconds())
	s.statsMu.Unlock()
	doneStats.Tokens = &tokensF
	doneStats.Cost = &costF
	doneStats.DurationMs = &durF

	doneFrame := generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: s.sessionID,
		Stats:     doneStats,
	}
	sendConnGenFrame(s.conn, string(generated.WsFrameTypeDone), doneFrame)
	// Cross-browser session attach (#133): a second tab attached mid-turn
	// needs the done frame too, otherwise its UI stays in "streaming" state
	// forever even after our token fan-out delivered the full content.
	if doneData, mErr := json.Marshal(doneFrame); mErr == nil {
		s.fanOutToSessionPeers(doneData)
	}
	// Only mark as streamed if we actually sent content. If the LLM failed
	// before producing any tokens, let the outbound Send path deliver the
	// error message — otherwise the user sees a stuck "thinking" spinner.
	if s.channel != nil && s.accumulated.Len() > 0 {
		s.channel.markStreamed(s.chatID)
	}
	// Record the full assistant response to the session transcript.
	if s.agentStore != nil && s.sessionID != "" {
		content := s.accumulated.String()
		// Fallback: when accumulated is empty (every Update() call silently
		// failed because the client WS was already closed), use the
		// finalContent the agent loop passed in. Without this fallback,
		// disconnected mid-stream turns would leave no assistant entry in
		// transcript.jsonl and the user sees nothing on reconnect/replay.
		if content == "" && finalContent != "" {
			content = finalContent
		}
		if content != "" {
			entry := session.TranscriptEntry{
				ID:        uuid.New().String(),
				Role:      "assistant",
				AgentID:   s.agentID,
				Content:   content,
				Timestamp: time.Now().UTC(),
			}
			if err := s.agentStore.AppendTranscript(s.sessionID, entry); err != nil {
				slog.Warn("ws: could not record streamed assistant message", "session_id", s.sessionID, "error", err)
			}
		}
	}
	return nil
}

func (s *wsStreamer) Cancel(_ context.Context) {
	s.conn.close()
}
