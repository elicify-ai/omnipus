// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
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

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/pairing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/validation"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// resolveApprovalToolPolicy resolves the effective tool policy consulted by the WS
// approval hook (the gateway-side exec gate) from the global config alone.
//
// Unification (#438): this delegates to the ONE authoritative single-tool
// resolver tools.EffectiveToolPolicy — the SAME primitive the agent loop's
// FilterToolsByPolicy uses at defs-assembly time — so the gateway exec gate and
// the loop's sent-defs view can never drift. It builds the resolver inputs (the
// sandbox global floor + the agent's builtin policy) from cfg. The primitive
// encapsulates, in order: (1) infra force-allow (load_tool → allow,
// unconditional — infra tools are registration-gated, not policy-gated, so they
// stay executable for EVERY agent including deny-by-default Ava/Mia/Ray, or
// every lazy tool becomes unreachable at exec time), (2) the scope gate, and
// (3) global×agent strictest-wins (deny > ask > allow). The tools that load_tool
// *loads* stay independently policy-gated when they are actually called.
//
// BEHAVIOR CHANGE (intentional): this does NOT preserve the OLD gateway exec
// gate verdict byte-for-byte. The old gateway resolved policy by EXACT-MATCH
// only — cfg.Sandbox.ToolPolicies[name] for the floor and the old per-agent
// exact-match policy lookup for the agent layer, both of which ignore
// wildcard keys. By routing through tools.EffectiveToolPolicy
// (buildWildcardIndex/resolveFromMap), this path now ALIGNS the exec gate to the
// agent loop's wildcard-aware verdict: ".*"/"_*" policy keys on the global floor
// AND the agent policy are now honored (most-specific-wins). This is the correct
// reconciliation — FilterToolsByPolicy always honored wildcards, so the old gate
// could ALLOW at exec time a tool the loop's defs filter had denied via a
// wildcard (or vice-versa). It only narrows or matches; it never widens past the
// loop's verdict.
//
// This config-only entry point cannot know a tool's real scope (it has only a
// name), so it passes ScopeGeneral — no extra scope restriction beyond the
// policy merge. It is also intentionally NOT god-mode-aware (no GodMode flag on
// the built ToolPolicyCfg): when god mode is active this fallback is therefore
// strictly more restrictive (fail-closed) than the live registry path, never
// more permissive. The live hook prefers AgentLoop.ResolveApprovalToolPolicy,
// which resolves the tool's real scope and the agent's live (god-mode-aware)
// policy snapshot from the registry; both paths funnel through
// tools.EffectiveToolPolicy so they agree on the wildcard-aware verdict.
func resolveApprovalToolPolicy(cfg *config.Config, toolName, agentID string) string {
	if tools.ToolManifestTier(toolName) == tools.ManifestInfra {
		return "allow"
	}
	if cfg == nil {
		// No config to build a floor from: an infra tool was already handled
		// above; everything else defaults to interactive approval.
		return "ask"
	}
	// No default-policy fallback (CLAUDE.md hard constraint 6): only explicit
	// global/agent entries are threaded through; a tool with no match on
	// either side fails closed to "deny" inside tools.EffectiveToolPolicy.
	polCfg, agentType := tools.BuildFallbackPolicyCfg(cfg, agentID)
	return tools.EffectiveToolPolicy(polCfg, tools.ScopeGeneral, agentType, toolName)
}

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
	// whatsapp_pairing fields (#283)
	ChannelID string `json:"channel_id,omitempty"`
	QR        string `json:"qr,omitempty"`
	// media frame fields
	Parts []map[string]any `json:"parts,omitempty"`
	// subagent span fields (FR-H-004, FR-H-005)
	SpanID       string `json:"span_id,omitempty"`
	ParentCallID string `json:"parent_call_id,omitempty"`
	TaskLabel    string `json:"task_label,omitempty"`
	// Phase 1B (FR-013/FR-014): per-turn model on replay_message.
	Model string `json:"model,omitempty"`
	// Wave 3 fix 5c: turn-correlation id on replay_message (assistant and
	// turn_canceled entries).
	TurnID string `json:"turn_id,omitempty"`
	// Phase 1B (FR-014): ReplayErrorFrame wire fields. Decoder-only — production
	// code uses the generated type directly. The `Message` field above (the
	// legacy ErrorFrame.message) doubles as the replay_error.message sink
	// since both frames use the same wire key. We just need an additional
	// `kind`, `entry_id`, and `payload` decoder slot.
	ErrorKind    string         `json:"kind,omitempty"`
	ErrorEntryID string         `json:"entry_id,omitempty"`
	ErrorPayload map[string]any `json:"payload,omitempty"`
	// review r2 RV1: JudgeVerdictFrame decoder fields (Model/Scope/ID above are
	// shared with other frame types via the same JSON key).
	Met          bool             `json:"met,omitempty"`
	Round        int              `json:"round,omitempty"`
	JudgeAgentID string           `json:"judge_agent_id,omitempty"`
	JudgedAt     string           `json:"judged_at,omitempty"`
	TaskID       string           `json:"task_id,omitempty"`
	PlanID       string           `json:"plan_id,omitempty"`
	PerCriterion []map[string]any `json:"per_criterion,omitempty"`
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

	// approvalRegV2 is the Central Tool Registry approval registry (FR-016, FR-070).
	// Injected at boot by the gateway after construction.  Nil until then.
	approvalRegV2 *approvalRegistryV2

	// devicePairingRegistry tracks in-flight device pairing requests awaiting operator approval.
	devicePairingRegistry *devicePairingRegistry

	// pairingStore is the global device pairing state (pending + paired devices).
	pairingStore *pairing.PairingStore

	// toolStore persists tool results that exceed InlineToolResultMaxBytes.
	// Set by the gateway after construction (nil = disabled, which is the test default).
	toolStore *toolResultStore

	// home is the OMNIPUS_HOME path, used to validate a client-supplied
	// workspace_id (workspace.Exists) before binding it to a session. Set by the
	// gateway after construction; empty in tests that do not exercise binding.
	home string

	// lastPairingState caches the most-recently-emitted whatsapp_pairing frame
	// bytes for each channelID (key: string, value: []byte).  Written by the
	// eventForwarder when status=="code"; deleted on terminal statuses (linked,
	// error), non-terminal QR-rotation status (timeout — a fresh code typically
	// follows within the next whatsmeow rotation cycle, ~20 s), known waiting
	// status, and any future unknown status so stale codes are never shown.
	// Used by subscribePairingInterest to re-emit the cached QR to late
	// subscribers (#368).
	//
	// WHY a cache is necessary: whatsmeow is not request-driven — it emits QR
	// codes on its own rotation schedule (up to ~60 s for the first code, ~20 s
	// for subsequent codes on whatsmeow's rotation schedule).  A browser tab
	// that opens the pairing UI after the first QR has fired would otherwise
	// have to wait up to ~60 s before seeing any code.  The cache delivers the
	// last-seen code immediately on subscribe via subscribePairingInterest.
	lastPairingState sync.Map

	// streamOwners tracks, per chatID, which turn currently "owns" live
	// TokenFrame delivery to that chat connection. Keys are chatID (string);
	// values are streamOwnerClaim (owning turnID + the time it claimed the
	// slot, the latter backing the stale-claim reclaim safety net described
	// below).
	//
	// Root cause this closes (live UAT bug, persona "Alex"): TokenFrame and
	// DoneFrame (contracts/asyncapi.yaml) carry only session_id (+ an
	// optional agent_id on TokenFrame) — no turn/span discriminator — and
	// TokenFrame.yaml's own documented contract is "the SPA appends each
	// token's content to the last assistant message bubble". That is correct
	// only when a single turn streams to a given chatID at a time. A
	// background (async) delegate sub-turn that outlives its own already-
	// finished parent (see pkg/agent/steering.go's sessionTurnsStillAlive doc
	// comment for that half of the bug) can resurface and stream
	// concurrently with a LATER, unrelated turn's own delegate call on the
	// SAME chatID; without this gate, both streamers' Update() calls race to
	// send TokenFrames the client can only append to whichever bubble
	// happens to be open, word-interleaving two unrelated narrations into
	// one garbled message — confirmed live and reproducible on reload
	// (the live view, not the transcript JSONL, is what a naive client
	// caches and replays; each streamer's own transcript entry, keyed by its
	// own TurnID, was already correctly isolated before this fix).
	//
	// claimStreamOwnership/wsStreamer.Update/wsStreamer.ReleaseStreamOwnership
	// implement the gate: the first streamer to claim a chatID's slot for
	// its turnID streams live as before (zero behavior change for the
	// common single-active-stream case); any OTHER concurrent turnID's
	// streamer still accumulates every token into its own private
	// wsStreamer.accumulated (so its Finalize-written transcript entry stays
	// fully correct) but withholds the live TokenFrame send until the owner
	// releases its claim — so two concurrent streams can never interleave
	// on the wire, regardless of what the frontend does with the frames it
	// receives.
	//
	// Release points: wsStreamer.Finalize (the normal, once-per-turn path
	// via turnState's deferred finalizeStreamer), wsStreamer.Cancel (no
	// production call sites today, kept as defensive symmetry), and
	// turnState.finalizeStreamer's B4 abandoned-turn early return
	// (pkg/agent/turn.go) — which deliberately skips the rest of Finalize
	// (no done frame, no transcript write for a stuck goroutine) but MUST
	// still release any ownership claim its streamer held, via the
	// streamOwnershipReleaser optional interface. Found missing in a
	// 7-reviewer gate (architect/silent-failure-hunter/type-design-analyzer/
	// pr-test-analyzer unanimous): a background delegate that became the
	// live owner for a chatID and was later MarkAbandoned()'d by
	// cancel.go's PHASE C left that chatID's entry pointing at the dead
	// turn's ID forever — no other release path touched it, permanently
	// shadowing every later turn on the same chat. claimStreamOwnership's
	// staleness check (streamOwnershipStaleAfter) is the backstop for any
	// FUTURE bug in this family: an unreleased claim older than the
	// threshold can be force-reclaimed by a new claimant, so a leak
	// degrades to "briefly wrong attribution" instead of "permanently mute
	// chat".
	streamOwners sync.Map

	upgrader websocket.Upgrader
}

type wsConn struct {
	conn           *websocket.Conn
	sendCh         chan []byte
	doneCh         chan struct{}
	closeOnce      sync.Once
	droppedTokens  atomic.Int32
	droppedFrames  atomic.Int32 // non-critical outbound frames dropped due to backpressure
	inboundDropped atomic.Int32 // inbound items dropped: schema validation failures + invalid/oversized media refs
	userID         string       // username resolved at auth time; used for session_state scoping (FR-073)

	// Replay-mode divert (W1-1): during replay, live events arriving via
	// sendConnGenFrame are redirected into replayDivertCh instead of sendCh so
	// they don't interleave with replay frames. After replay finishes they are
	// drained into sendCh in arrival order.
	//
	// Ordering invariant (see docs/internal/investigation/bug-5-replay-order.md,
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

	// isCLIToken is true when authenticateWS resolved this connection via
	// the machine-only Gateway.CLIToken credential rather than a real
	// Gateway.Users row (userID == "cli" in that case). This is the WS-side
	// counterpart of gateway's CLITokenContextKey — a WebSocket connection
	// has no per-request context to carry that key into, so the distinction
	// is tracked here instead for any WS-side logic that needs to tell a
	// CLI caller apart from a human account.
	isCLIToken bool

	// pairingSubs tracks which channels this connection wants whatsapp_pairing
	// (QR/status) frames for, so the QR secret is delivered only to the operator
	// viewing that channel's pairing UI rather than every connected tab (#283,
	// Option B). Guarded by pairingSubsMu; written by the inbound read loop and
	// read by the event forwarder. Nil until the first subscribe.
	pairingSubsMu sync.Mutex
	pairingSubs   map[string]struct{}
}

// setPairingInterest registers (active) or clears this connection's interest in
// channelID's whatsapp_pairing frames (#283, Option B).
func (c *wsConn) setPairingInterest(channelID string, active bool) {
	c.pairingSubsMu.Lock()
	defer c.pairingSubsMu.Unlock()
	if active {
		if c.pairingSubs == nil {
			c.pairingSubs = make(map[string]struct{})
		}
		c.pairingSubs[channelID] = struct{}{}
		return
	}
	delete(c.pairingSubs, channelID)
}

// subscribePairingInterest registers or clears this connection's interest in
// channelID's whatsapp_pairing frames, and immediately re-emits the cached QR
// frame (if any) when active==true so late subscribers don't wait for the next
// QR rotation (#368).
//
// WHY the cache is needed: whatsmeow emits QR codes on its own rotation
// schedule (up to ~60 s for the first code, ~20 s for subsequent codes on
// whatsmeow's rotation schedule) and is not request-driven — there is no way
// to ask it to re-send the current QR on demand.  A subscriber that arrives
// between rotations would otherwise wait up to ~60 s before seeing a code.
// The cache lets us deliver the last-seen code immediately on subscribe.
func (h *WSHandler) subscribePairingInterest(wc *wsConn, channelID string, active bool) {
	wc.setPairingInterest(channelID, active)
	if !active {
		return
	}
	// Re-emit the last-seen QR frame for this channel, if one is cached and the
	// QR is still "live" (terminal states are deleted from the map by the
	// eventForwarder).  Route through sendRawFrameBytes so the replay-divert
	// invariant (isReplayingLive / replayDivertCh) is respected — a direct
	// wc.sendCh write would bypass the divert and interleave with a replay
	// stream (#368 + Wave 2 review).
	if cached, ok := h.lastPairingState.Load(channelID); ok {
		frameBytes, ok := cached.([]byte)
		if ok && len(frameBytes) > 0 {
			sendRawFrameBytes(wc, string(generated.WsFrameTypeWhatsappPairing), frameBytes)
		} else if !ok {
			slog.Error("ws: lastPairingState held non-[]byte value, skipping re-emit", "channel_id", channelID)
		}
	}
}

// wantsPairing reports whether this connection has subscribed to channelID's
// whatsapp_pairing frames (#283, Option B). Safe on a nil map.
func (c *wsConn) wantsPairing(channelID string) bool {
	c.pairingSubsMu.Lock()
	defer c.pairingSubsMu.Unlock()
	_, ok := c.pairingSubs[channelID]
	return ok
}

func (c *wsConn) close() {
	c.closeOnce.Do(func() { close(c.doneCh) })
}

// wsCheckOrigin builds a gorilla websocket.Upgrader.CheckOrigin function that
// allows same-origin requests (Origin hostname+port matches the request
// Host) plus the configured allowedOrigin, and — only when no allowedOrigin
// is configured — localhost/127.0.0.1 for local development. Shared by every
// WS endpoint in the gateway (chat's /api/v1/chat/ws, ADR-038's
// /api/v1/browser/ws) so origin policy can never drift between them.
func wsCheckOrigin(allowedOrigin string) func(r *http.Request) bool {
	return func(r *http.Request) bool {
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
	}
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
		devicePairingRegistry: newDevicePairingRegistry(),
		pairingStore:          pairing.NewPairingStore(),
		upgrader: websocket.Upgrader{
			CheckOrigin: wsCheckOrigin(allowedOrigin),
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

// WS keepalive/backpressure timing constants. Kept in one place so the
// invariant is easy to audit: wsPingPeriod < wsPongWait < the reverse proxy's
// idle timeout (Fly's is ~60s). The keepalive ping must be both frequent
// enough to beat the proxy's idle timeout AND non-blockable — a ping or any
// other frame write that stalls (slow/unresponsive client, TCP
// back-pressure) must fail fast via wsWriteWait rather than hang the single
// writePump goroutine forever, which would silently starve the ping and let
// the proxy kill the TCP connection with no close frame (browser sees code
// 1006).
const (
	// wsWriteWait is the deadline for a single WriteMessage call (ping or
	// data frame). A write that can't complete within this window fails
	// fast so writePump can tear the connection down and the client can
	// reconnect, instead of blocking indefinitely.
	wsWriteWait = 10 * time.Second
	// wsPongWait is how long we wait for a pong (or any client frame, which
	// also re-arms this deadline in readLoop) before considering the
	// connection dead.
	wsPongWait = 60 * time.Second
	// wsPingPeriod is how often the server sends a keepalive ping. Must be
	// well under wsPongWait so pings arrive before the read deadline would
	// otherwise expire.
	wsPingPeriod = 30 * time.Second
)

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

	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	// Create wsConn before auth so authenticateWS can set the role on it.
	wc := &wsConn{
		conn:   conn,
		sendCh: make(chan []byte, 256),
		doneCh: make(chan struct{}),
	}

	if !h.authenticateWS(conn, wc, r) {
		return
	}

	chatID := "webchat:" + uuid.New().String()

	h.mu.Lock()
	h.sessions[chatID] = wc
	h.mu.Unlock()

	// NOTE (ADR-036 §3.4): this connection no longer mounts a per-connection
	// wsApprovalHook. Interactive "ask"-policy tool approval is handled
	// entirely by the gateway's central approvalRegistryV2 + REST
	// POST /api/v1/tool-approvals/{id} path (policyApproverAdapter,
	// AgentLoop.CheckGrantOrRequestApproval) — the wsApprovalHook /
	// exec_approval_request WS-frame gate was retired because it ran BEFORE
	// that path and unconditionally denied every ask-policy call after a 90s
	// timeout once its answering frontend UI (ExecApprovalBlock/
	// ExecApprovalTool) was removed, making the REST path unreachable. See
	// pkg/agent/loop.go's CheckGrantOrRequestApproval doc comment.

	// Subscribe to agent-loop events so we can forward tool_call_start/result
	// frames to the browser in real time. 256 (not 32) to match wc.sendCh's own
	// buffer above: a burst of concurrent subagent dispatches (confirmed live at
	// 24 in ~0.3s, each firing spawn/end plus its own tool-call events) can
	// enqueue far more events than the old 32-slot buffer while eventForwarder
	// is still marshaling+writing the previous one; sizing to sendCh keeps this
	// stage no more likely to overflow than the outbound path it feeds.
	eventSub := h.agentLoop.SubscribeEvents(256)
	eventDone := make(chan struct{})
	go h.eventForwarder(wc, chatID, eventSub, eventDone)

	defer func() {
		h.agentLoop.UnsubscribeEvents(eventSub.ID)
		<-eventDone // wait for forwarder goroutine to exit
		h.mu.Lock()
		// ADR-045: capture this connection's own session mapping BEFORE the
		// deletes below — needed both to arm the watchdog for the right
		// session and so the multi-tab-safety scan just after reflects state
		// with THIS connection already removed.
		sid := h.sessionIDs[chatID]
		if tid, ok := h.taskChatIDs[chatID]; ok {
			// sessions is never keyed by tid (only by chatID); clean up only sessionIDs.
			delete(h.sessionIDs, tid)
			delete(h.taskChatIDs, chatID)
		}
		delete(h.sessions, chatID)
		delete(h.sessionIDs, chatID)
		// ADR-045 multi-tab safety: only arm the orphan-foreground-turn
		// watchdog when no OTHER connection is still watching this same
		// session — a second open tab on the same chat must never have its
		// live turn interrupted just because a sibling tab closed.
		armOrphanWatch := false
		if sid != "" {
			armOrphanWatch = true
			for _, otherSID := range h.sessionIDs {
				if otherSID == sid {
					armOrphanWatch = false
					break
				}
			}
		}
		h.mu.Unlock()
		wc.close()

		if armOrphanWatch {
			grace := h.agentLoop.GetConfig().EffectiveOrphanedTurnGraceSeconds()
			sidCopy := sid
			h.agentLoop.ArmOrphanForegroundTurnWatch(sidCopy, grace,
				func(reason string) { h.reapOrphanForegroundTurn(sidCopy, reason) },
				func() bool { return h.sessionStillOrphaned(sidCopy) },
			)
		}

		// Emit observability counters at connection teardown so operators can
		// act on them (e.g. alert when a client is sending many invalid refs).
		if dropped := wc.inboundDropped.Load(); dropped > 0 {
			slog.Warn("ws: connection closed with dropped inbound items",
				"chat_id", chatID,
				"inbound_dropped", dropped,
			)
		}
		if mediaDropped := h.agentLoop.GetMediaRefsDropped(); mediaDropped > 0 {
			slog.Info("ws: agent loop media-ref drop counter at connection close",
				"chat_id", chatID,
				"media_refs_dropped_total", mediaDropped,
			)
		}
	}()

	go h.writePump(wc)
	go h.pingPump(wc)

	// Emit session_state one-shot on every new WS connection (FR-052, FR-073, FR-081).
	// This lets the SPA reconcile stale approval modals after a gateway restart.
	h.emitSessionState(wc)

	h.readLoop(r.Context(), conn, wc, chatID)
}

// D5 fix (UAT): the three ErrorFrame messages a rejected WS handshake can
// send were raw Go-internal/protocol strings ("first message must be
// {\"type\":\"auth\",\"token\":\"...\"}", "unauthorized: invalid token", "no
// users configured, complete onboarding first") surfaced verbatim to the end
// user via BrowserLiveView's/chat's error banners — see
// friendlyBrowserStatusMessage's/onError's doc comments on the SPA side.
// authenticateWS (this file, the chat WS) and BrowserWSHandler.authenticate
// (browser_ws.go) are deliberately near-identical mirrored implementations
// (browser_ws.go's own doc comment: "mirroring WSHandler.authenticateWS
// exactly") that hit these exact three conditions independently — sharing
// these constants means the two can never drift into two different
// user-facing strings for the identical condition, and fixing the copy here
// fixes both call sites at once.
const (
	// wsAuthErrBadFirstFrame — the client's first WS frame (on the no-cookie
	// fallback path) wasn't a well-formed {"type":"auth","token":"..."}
	// frame. Only reachable by a non-cookie client (the SPA always attaches
	// the same-origin session cookie) — a stale client build or a dropped/
	// malformed handshake is the realistic trigger, so "reload" is the
	// correct recovery action either way.
	wsAuthErrBadFirstFrame = "Your session expired — reload the page to reconnect."
	// wsAuthErrInvalidToken — the presented cookie/bearer token didn't match
	// any configured identity (expired session, revoked token, stale cookie).
	wsAuthErrInvalidToken = "Your session expired — reload the page to reconnect."
	// wsAuthErrNoUsers — no account or token is configured at all yet (fresh
	// install, onboarding not completed).
	wsAuthErrNoUsers = "Setup isn't complete yet — finish onboarding, then reload the page."
)

// authenticateWS authenticates the WS handshake via EITHER the omnipus-session
// cookie (checked first, synchronously, against the upgrade request r) OR the
// legacy first-message {"type":"auth","token":...} frame (FR-009). The SPA no
// longer sends an auth frame at all post-Wave-1 cutover — the browser
// auto-attaches the cookie on the upgrade request (same-origin) — so the
// cookie check MUST happen before blocking on the first frame read, or a
// cookie-only client would hang waiting for a frame that will never arrive.
// Programmatic/CLI clients that don't carry the cookie still authenticate via
// the frame path below, unaffected.
//
// The frame path loops every account in Gateway.Users first (bcrypt; the
// single-user model normally holds exactly one, but a pre-single-user-model
// install may still carry leftover extra accounts — config.warnAboutExtraUsers
// flags that at load time as an advisory; every configured account still
// authenticates here, same as checkBearerAuth), then the CLI's dedicated
// Gateway.CLIToken, then falls back to OMNIPUS_BEARER_TOKEN env var for
// backward compatibility. Sets wc.userID to the resolved identity on success,
// and wc.isCLIToken when that identity came from the CLIToken branch (the
// cookie path never sets isCLIToken — a cookie always identifies a real human
// Gateway.Users account, never the synthetic CLI identity).
func (h *WSHandler) authenticateWS(conn *websocket.Conn, wc *wsConn, r *http.Request) bool {
	cfg := h.agentLoop.GetConfig()

	if user, err := middleware.ResolveUserFromCookie(r, cfg.Gateway.Users); err == nil && user != nil {
		wc.userID = user.Username // FR-073: needed for session_state user scoping
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return true
	}
	// SFH-1: surface "cookie present but invalid" (replay/probe/stale
	// cookie) as a log line — silent for the routine "no cookie at all"
	// case (see LogInvalidSessionCookiePresent's doc). Log-only: falling
	// through to the frame-based auth path below is unchanged either way.
	middleware.LogInvalidSessionCookiePresent(r, cfg)

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
				Message: wsAuthErrBadFirstFrame,
			},
		)
		return false
	}

	rawToken := authFrame.Token

	// 1 & 2. Configured identities — human Gateway.Users accounts, then the
	// CLI's dedicated token. See resolveBearerIdentity's doc (auth.go) for
	// the full rationale (looping every user, ViaCLIToken/isCLIToken
	// semantics, etc.) — shared with checkBearerAuth (auth.go) and
	// withOptionalAuth (rest_auth.go), which previously reimplemented this
	// same lookup independently.
	if user, viaCLIToken, matched := resolveBearerIdentity(cfg, rawToken); matched {
		wc.userID = user.Username // FR-073: needed for session_state user scoping
		wc.isCLIToken = viaCLIToken
		conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return true
	}

	// Auth is configured (a human account and/or a CLI token exist) but the
	// presented token matched neither — reject without falling through to the
	// legacy env-token path below. This preserves prior behavior: once any
	// account-based auth is configured, an unmatched token is rejected
	// immediately rather than being checked against OMNIPUS_BEARER_TOKEN.
	if bearerAccountsConfigured(cfg) {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrInvalidToken,
		})
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
		)
		return false
	}

	// 3. Fallback: legacy OMNIPUS_BEARER_TOKEN env var.
	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			// Dev mode: allow without auth.
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			return true
		}
		// No auth configured — deny by default (fail closed), matching HTTP auth path.
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrNoUsers,
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
			Message: wsAuthErrInvalidToken,
		})
		conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"),
		)
		return false
	}
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
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
				return
			}
			// Every other ReadMessage failure previously fell through to a
			// silent return with zero log line, including a plain
			// read-deadline-exceeded I/O timeout (net.Error, not a close-code
			// error, so neither branch above matches it) — meaning an operator
			// had no visibility into why a WS connection died this way. Log
			// every case here so the actual error/timeout-ness is visible.
			var netErr net.Error
			isTimeout := errors.As(err, &netErr) && netErr.Timeout()
			slog.Debug("ws: readLoop exiting on ReadMessage error",
				"chat_id", chatID, "error", err, "is_timeout", isTimeout)
			return
		}

		if err := conn.SetReadDeadline(time.Now().Add(wsPongWait)); err != nil {
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
			if f.Content == "" && len(f.Media) == 0 {
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
			var modelName string
			if v, ok := f.Metadata["model_name"].(string); ok {
				if strings.TrimSpace(v) != "" {
					modelName = v
				}
			}
			// M4: workspace→turn binding. When the message originates from a
			// workspace chat the SPA sets metadata.workspace_id; we stamp it on
			// the session meta so task_create/delegation lands on this workspace.
			var workspaceID string
			if v, ok := f.Metadata["workspace_id"].(string); ok {
				workspaceID = strings.TrimSpace(v)
			}
			// Workspace-setup kickoff: the SPA sends this flag on a
			// workspace's first open so the server records the trigger as a
			// system-role transcript entry (not a user bubble), clears
			// SetupPending exactly once, and gives the session a clean title —
			// see contracts/asyncapi.yaml metadata.workspace_setup_kickoff.
			//
			// Kickoff intent is signaled by KEY PRESENCE, not a loose
			// type assertion — see parseSetupKickoffMetadata's doc comment. A
			// key that IS present but not exactly boolean true is rejected
			// outright instead of ever reaching handleChatMessage as a normal
			// message.
			setupKickoff, malformedKickoff := parseSetupKickoffMetadata(f.Metadata)
			if malformedKickoff {
				slog.Warn("ws: malformed workspace_setup_kickoff metadata — rejecting",
					"chat_id", chatID, "value", f.Metadata["workspace_setup_kickoff"])
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "malformed workspace_setup_kickoff metadata",
				})
				continue
			}
			h.handleChatMessage(
				ctx, chatID, sessionID, f.Content, agentID, f.Media,
				modelName, workspaceID, setupKickoff, wc,
			)
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
		case string(generated.WsFrameTypeWhatsappPairingSubscribe):
			// #283 (Option B): scope whatsapp_pairing frames to the connection(s)
			// viewing a channel's pairing UI so the QR secret isn't broadcast to
			// every tab. active=true subscribes this conn; false clears it. Any
			// connection reaching this point in readLoop is already authenticated
			// (single-account model), so no further role gate applies.
			var f generated.WhatsAppPairingSubscribeFrame
			if err := json.Unmarshal(data, &f); err != nil {
				slog.Warn("ws: malformed whatsapp_pairing_subscribe frame", "error", err)
				wc.inboundDropped.Add(1)
				continue
			}
			if f.ChannelId == "" {
				wc.inboundDropped.Add(1)
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:    string(generated.WsFrameTypeError),
					Message: "whatsapp_pairing_subscribe requires channel_id",
				})
				continue
			}
			h.subscribePairingInterest(wc, f.ChannelId, f.Active)
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
	case string(generated.WsFrameTypeAttachSession):
		return "AttachSessionFrame"
	case string(generated.WsFrameTypeDevicePairingResponse):
		return "DevicePairingResponseFrame"
	case string(generated.WsFrameTypeSessionClose):
		return "SessionCloseFrame"
	case string(generated.WsFrameTypeWhatsappPairingSubscribe):
		return "WhatsAppPairingSubscribeFrame"
	case string(generated.WsFrameTypePing):
		return "PingFrame"
	// ADR-038 finding #3: the 4 browser-live client→server frame types.
	// These never arrive on the chat WS this function primarily serves —
	// browser_ws.go's own readLoop is the actual caller for these cases —
	// but the mapping lives here so there is exactly one frame-type→schema
	// table for the whole gateway, not two that can drift apart.
	case string(generated.WsFrameTypeBrowserAttach):
		return "BrowserAttachFrame"
	case string(generated.WsFrameTypeBrowserInput):
		return "BrowserInputFrame"
	case string(generated.WsFrameTypeBrowserControl):
		return "BrowserControlFrame"
	case string(generated.WsFrameTypeBrowserDetach):
		return "BrowserDetachFrame"
	// ADR-041 D4: the client→server tab-management frame. browser_ws.go's
	// own readLoop is the actual caller (this socket, not the chat WS), same
	// rationale as the 4 ADR-038 browser-live frame types above.
	case string(generated.WsFrameTypeBrowserTabAction):
		return "BrowserTabActionFrame"
	// 2026-07-31 adaptive viewport: bounds (1..8192, dsf 1..3) are declared in
	// the schema, so without this mapping a malformed frame would reach
	// Emulation.setDeviceMetricsOverride unvalidated.
	case string(generated.WsFrameTypeBrowserViewport):
		return "BrowserViewportFrame"
	// ADR-047 D4 (wave-plan W2-A): the viewer's WebRTC offer, on this same
	// browser WS. browser_webrtc.go's handleWebRTCOffer is the actual
	// caller, same rationale as the ADR-038/ADR-041 browser-live frame types
	// above.
	case string(generated.WsFrameTypeBrowserWebrtcOffer):
		return "BrowserWebRTCOfferFrame"
	default:
		return ""
	}
}

// parseSetupKickoffMetadata decides whether a MessageFrame's
// metadata.workspace_setup_kickoff (contracts/asyncapi.yaml) signals a
// workspace-setup kickoff, based on KEY PRESENCE rather than a loose type
// assertion.
//
// A naive `metadata["workspace_setup_kickoff"].(bool)` type assertion
// silently reads ANY non-bool value — the string "true", a JSON number, an
// object — as ok=false, which used to make setupKickoff=false and silently
// demote a malformed kickoff frame into an ordinary chat message: the
// synthetic interview instruction would then persist as a user-authored
// transcript entry with a content-derived title, instead of ever being
// recognized as an attempted (but malformed) kickoff.
//
// Returns (setupKickoff=false, malformed=false) when the key is absent
// entirely — an ordinary message, handled normally. Returns
// (setupKickoff=true, malformed=false) only when the key is present AND its
// value is the JSON boolean true. Returns (setupKickoff=false,
// malformed=true) for every other case where the key IS present — including
// an explicit `false` — signaling the caller to reject the frame outright
// (error frame, never processed as a normal message) rather than silently
// falling back to non-kickoff handling.
func parseSetupKickoffMetadata(metadata map[string]any) (setupKickoff bool, malformed bool) {
	raw, present := metadata["workspace_setup_kickoff"]
	if !present {
		return false, false
	}
	b, isBool := raw.(bool)
	if !isBool || !b {
		return false, true
	}
	return true, false
}

// handleChatMessage mints a new session when frame.SessionID is empty, records
// every user message to the transcript, and publishes the message to the bus.
//
// modelName, when non-empty and non-whitespace, is forwarded to the agent loop
// as msg.Metadata["model_name"] so the per-turn switch (Phase 1, FR-010) routes
// THIS message to the chosen model instead of the agent's default. Whitespace-only
// or empty values are treated as absent — the agent falls back to its configured model.
//
// setupKickoff is true when the frame carries metadata.workspace_setup_kickoff
// (contracts/asyncapi.yaml) — the SPA sends this on a workspace's first open so
// Ava introduces herself and interviews the user about the workspace's purpose.
// When true (and workspaceID resolves to a real workspace), the trigger is
// recorded as a NEUTRAL system-role transcript entry ("Workspace setup
// started.", never the client-supplied instruction text verbatim — see
// consumeWorkspaceSetupKickoff's caller below) rather than a user bubble, the
// workspace's SetupPending flag is cleared exactly once under the per-workspace
// lock (workspace.LockID, idempotency-guarded), and the minted session is given
// the fixed title "Workspace setup" instead of a content-derived one.
//
// A kickoff frame is MINT-ONLY: any frame carrying setupKickoff=true AND a
// non-empty client-supplied session_id is rejected before the consume step
// (an arbitrary client would otherwise be able to burn the one-time flag
// against an unrelated EXISTING session it doesn't own). This makes the
// "existing session" branch below unreachable for a kickoff — it only ever
// runs for a normal message.
//
// The turn's driving prompt (msg.Content published to the bus) is built
// SERVER-SIDE from the workspace's own Name/Description (read during the
// consume) — the client-supplied `content` is IGNORED entirely for a kickoff
// frame. This closes a forensics gap (an arbitrary client instruction would
// otherwise silently drive Ava's first turn on a new workspace) and a
// stale-SPA/i18n drift risk (an older or translated client sending a
// different template). The persisted/replayed transcript entry stays the
// separate neutral "Workspace setup started." string regardless.
//
// ANY kickoff-flagged frame that cannot complete the kickoff — a
// non-empty session_id, an unresolved workspace_id, a duplicate (SetupPending
// already false), or a consume read/write failure — is REJECTED outright with
// an error frame: no session is minted, no transcript entry is written, and
// nothing is published to the bus. This replaces an earlier "demote to a
// normal message" behavior, which used to persist the synthetic kickoff
// instruction as a fake user-authored transcript entry and session title.
// Flag state is left untouched on a read/write failure (so a later workspace
// open can retry) and cleared only on the success path. Every frame-level
// validation (agent_id / session_id format) runs BEFORE the consume so a
// malformed frame can never burn the one-time flag with no way to recover it.
//
// If a downstream step fails AFTER a successful consume — minting the new
// session, persisting its title/owner/workspace stamp, or publishing to the
// bus — the consumed SetupPending flag is best-effort RESTORED to true (see
// restoreWorkspaceSetupPending) AND the just-minted session is deleted (see
// rollbackKickoffSession) so the one-time setup interview is not permanently
// lost, and a repeated failure does not accumulate orphan empty "Workspace
// setup" sessions. The workspace.setup_consumed audit entry is emitted only
// AFTER a successful publish — never before — so it never records a false
// positive for a turn that was actually rolled back.
//
// Two failure windows are accepted as out of scope for this in-process
// compensation (both are inherent to the file-based, no-distributed-
// transaction design and are not treated as bugs):
//
//  1. A process crash between the consume-persist (SetupPending written to
//     disk) and the bus publish loses the interview permanently — there is no
//     durable outbox to replay from across a restart, only the in-memory
//     restore/rollback path above, which cannot run if the process is gone.
//  2. The commit point for "the kickoff succeeded" is a successful publish,
//     not a successful TURN. If Ava's turn itself errors after the message
//     was accepted by the bus, the flag stays consumed and SetupPending
//     does not revert — recovery at that point is conversational (the
//     operator can just ask her to continue) rather than a retriggerable
//     kickoff.
func (h *WSHandler) handleChatMessage(
	ctx context.Context,
	chatID string,
	frameSessionID string,
	content string,
	agentID string,
	mediaRefs []string,
	modelName string,
	workspaceID string,
	setupKickoff bool,
	wc *wsConn,
) {
	targetAgentID := agentID
	if targetAgentID == "" {
		if reg := h.agentLoop.GetRegistry(); reg != nil {
			if def := reg.GetDefaultAgent(); def != nil {
				targetAgentID = def.ID
			}
		}
		if targetAgentID == "" {
			// Fall back to the first chat-target agent (mirrors handleBoardTaskStart /
			// resolveDefaultAgentID in pkg/routing/route.go). firstChatTargetAgentID
			// already skips workers, so this fallback never lands on one.
			targetAgentID = firstChatTargetAgentID(h.agentLoop.GetConfig())
		}
	} else if isWorkerAgentID(h.agentLoop.GetConfig(), targetAgentID) {
		// An explicit agent_id that resolves to a worker is illegitimate: a worker
		// is a delegation-only labor tier, never a chat target. Refuse to mint a
		// live chat session for it. Mirror the error-frame pattern used for an
		// unknown/invalid session below.
		slog.Warn("ws: rejecting chat frame addressed to a worker agent",
			"agent_id", targetAgentID, "chat_id", chatID,
			"reason", "worker is invoked via delegation, not as a chat target")
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "this agent is a worker and cannot be a chat target — workers are invoked via delegation",
		})
		return
	}

	// No caller-supplied agent_id AND neither fallback resolved one (no
	// seeded default agent, no chat-target agent in the roster at all): reject
	// explicitly rather than letting an empty targetAgentID flow into
	// store.NewSession/transcript writes below. The retired "main" sentinel
	// used to silently absorb this case; there is no substitute default to
	// fall back to now — an empty owner on a persisted session is exactly the
	// unpoliced-shadow-agent bug removing the sentinel was meant to close.
	if targetAgentID == "" {
		slog.Warn("ws: rejecting chat frame — no agent_id supplied and no default agent could be resolved",
			"chat_id", chatID, "workspace_id", workspaceID)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "no agent available to handle this message: no default agent is configured",
		})
		return
	}

	// Validate the raw client-supplied agent_id format HERE, before any of the
	// workspace-kickoff consume/mint/audit work below. The previous placement
	// of this check (immediately before the bus publish, at the very end of
	// the function) ran AFTER the kickoff flag had already been consumed —
	// a malformed agent_id would permanently burn the one-time setup
	// interview and leave behind an orphan minted session plus a false
	// "consumed" audit entry, with no compensation path. Every frame-level
	// validation must complete before the consume step is ever reached.
	if agentID != "" {
		if err := validateEntityID(agentID); err != nil {
			slog.Warn("ws: invalid agent_id in message frame; rejecting", "agent_id", agentID, "error", err)
			var sidPtr *string
			if frameSessionID != "" {
				sidCopy := frameSessionID
				sidPtr = &sidCopy
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "invalid agent_id",
				SessionId: sidPtr,
			})
			return
		}
	}

	sessionID := frameSessionID

	// Validate the client-supplied session_id format up front too — same
	// rationale as the agent_id hoist above: this used to run only inside the
	// "existing session" branch further down, well after the kickoff consume.
	// A malformed session_id therefore no longer has any path to burning the
	// flag before being rejected.
	if sessionID != "" {
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
	}

	// A workspace-setup kickoff is MINT-ONLY: a frame that carries BOTH
	// setupKickoff and a non-empty client-supplied session_id is rejected
	// before the consume step. Without this guard an arbitrary client could
	// attach workspace_setup_kickoff=true to a message addressed at an
	// unrelated EXISTING session (untested, wrong owner, wrong workspace) and
	// burn the one-time flag against it. Rejecting this combination removes
	// the "existing session" branch as a reachable path for a kickoff — see
	// the (sessionID == "") mint branch below, which is the only path a
	// kickoff can now take.
	if setupKickoff && sessionID != "" {
		slog.Warn("ws: workspace setup kickoff with a client-supplied session_id — rejecting",
			"chat_id", chatID, "session_id", sessionID)
		sidCopy := sessionID
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   "workspace setup could not be started",
			SessionId: &sidCopy,
		})
		return
	}

	// M4: validate the client-supplied workspace_id at the binding boundary
	// before it is ever stamped onto session meta. A bad/stale/typo'd id would
	// otherwise persist tasks to a workspace no board renders while the agent
	// reports success. On a miss, drop the binding and fall back to the default
	// (resolveWorkspaceID/ResolveDefaultID picks up the real default) rather
	// than stamping the bogus id.
	if workspaceID != "" && h.home != "" && !workspace.Exists(h.home, workspaceID) {
		slog.Warn("ws: dropping unknown workspace_id binding — falling back to default",
			"workspace_id", workspaceID, "chat_id", chatID)
		workspaceID = ""
	}

	// A workspace-setup kickoff is only meaningful against a real, resolved
	// workspace — either the id was absent to begin with, or the M4 check
	// above just blanked it as unknown. Reject outright (this used to "demote
	// to a normal message", which persisted the kickoff instruction as a
	// fake user-authored transcript entry and session title) — no session is
	// minted, nothing is published.
	if setupKickoff && workspaceID == "" {
		slog.Warn("ws: workspace setup kickoff with no resolved workspace_id — rejecting",
			"chat_id", chatID)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "workspace setup could not be started: unknown workspace",
		})
		return
	}

	// Pick the right store for the operation:
	//
	//   * Resuming an existing session: ask ResolveSessionStore to find the
	//     owning store. New chat sessions live in sharedSessionStore, but
	//     sessions created by the task scheduler, by per-agent tools, or by
	//     custom agents (e.g. Hans completing a task) live under
	//     agents/<id>/sessions and are visible only via the per-agent stores.
	//     The previous code unconditionally picked sharedSessionStore here,
	//     so any non-shared session produced "session not found" on the next
	//     user message even though the SPA could read its transcript through
	//     the REST GET /api/v1/sessions/{id} endpoint (which already uses
	//     ResolveSessionStore).
	//
	//   * Minting a new session: keep using the shared store so every fresh
	//     chat lands in the modern shared layout — that part was never broken.
	var store *session.UnifiedStore
	if sessionID != "" {
		store = h.agentLoop.ResolveSessionStore(sessionID)
		if store == nil {
			// Truly unknown session — surface it explicitly so the SPA can
			// render the "session not found" toast/banner rather than silently
			// publishing the message to the bus against a non-existent session.
			slog.Warn(
				"ws: session not found (no store owns it)",
				"session_id", sessionID,
			)
			sidCopy := sessionID
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				Message:   "session not found",
				SessionId: &sidCopy,
			})
			return
		}
	} else {
		store = h.agentLoop.GetSessionStore()
		if store == nil {
			store = h.agentLoop.GetAgentStore(targetAgentID)
		}
	}

	// A kickoff-flagged frame with no usable session store (a
	// degenerate configuration, not the normal path) must be REJECTED like
	// every other kickoff-cannot-complete case rather than silently
	// falling through to the no-store tail below, which would publish the
	// raw kickoff instruction to the bus as an ordinary message.
	if setupKickoff && store == nil {
		slog.Warn("ws: workspace setup kickoff rejected — no session store available",
			"workspace_id", workspaceID, "chat_id", chatID)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "workspace setup could not be started",
		})
		return
	}

	// kickoffInstruction holds the SERVER-BUILT driving prompt for a kickoff
	// turn (see doc comment above) — populated only when setupKickoff is true
	// and the consume below succeeds. The client-supplied `content` is never
	// used as msg.Content for a kickoff turn.
	var kickoffInstruction string

	// #254: thread client-supplied media refs into the inbound message so the
	// agent loop resolves them into multimodal content blocks. Only accept
	// Hard-cap inbound media to prevent resource exhaustion. Refs beyond
	// maxInboundMediaRefs and refs exceeding maxInboundRefLen are dropped and
	// counted against the inbound-dropped counter.
	//
	// Computed HERE — before the transcript-append block below — so that
	// D2 (library-spec) can persist the accepted, validated set as
	// session.TranscriptEntry.Attachments. It used to be computed AFTER
	// that block, which meant the persisted transcript never had access to
	// the media this very message carried.
	const maxInboundMediaRefs = 16
	const maxInboundRefLen = 256

	var acceptedMedia []string
	for i, ref := range mediaRefs {
		if i >= maxInboundMediaRefs {
			wc.inboundDropped.Add(1)
			slog.Warn("ws: media array exceeds cap — dropping remaining refs",
				"chat_id", chatID, "session_id", sessionID,
				"dropped_from_index", i, "total_supplied", len(mediaRefs))
			break
		}
		if len(ref) > maxInboundRefLen {
			wc.inboundDropped.Add(1)
			slog.Warn("ws: dropping oversized ref in message frame",
				"chat_id", chatID, "session_id", sessionID,
				"ref_prefix", ref[:32])
			continue
		}
		// Accept only well-formed media:// refs (non-empty ID validated by
		// ParseMediaRef — rejects bare "media://" with empty ID, non-prefixed
		// strings, raw paths, and HTTP URLs that a buggy channel might emit).
		// Non-matching strings are a client error or smuggling attempt — drop
		// and count them via the inboundDropped counter.
		if _, err := media.ParseMediaRef(ref); err == nil {
			acceptedMedia = append(acceptedMedia, ref)
		} else {
			wc.inboundDropped.Add(1)
			truncated := ref
			if len(truncated) > 64 {
				truncated = truncated[:64] + "…"
			}
			slog.Warn("ws: dropping invalid media:// ref in message frame",
				"chat_id", chatID, "session_id", sessionID,
				"ref_prefix", truncated)
		}
	}

	if store != nil {
		// Workspace-setup kickoff idempotency guard: clear SetupPending exactly
		// once, under the per-workspace lock (workspace.LockID). ANY outcome
		// other than a clean consume — duplicate (already ran) or a read/write
		// failure against the workspace file — is REJECTED outright: no
		// session minted, no transcript entry written, nothing published.
		if setupKickoff {
			outcome, wsName, wsDescription := h.consumeWorkspaceSetupKickoff(workspaceID)
			switch outcome {
			case kickoffDuplicate:
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup has already run",
					SessionId: &sidCopy,
				})
				return
			case kickoffFailed:
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup could not be started",
					SessionId: &sidCopy,
				})
				return
			case kickoffConsumed:
				// Proceed — SetupPending is now cleared on disk. Build the
				// SERVER-CANONICAL driving instruction from the workspace's
				// own name/description now, while it's in hand — the
				// client-supplied `content` is never used for a kickoff turn.
				kickoffInstruction = buildWorkspaceKickoffInstruction(wsName, wsDescription)
			default:
				// Defensive: a future kickoffOutcome value this switch doesn't
				// know about must never silently fall through as if it were
				// kickoffConsumed — reject outright, same as every other
				// kickoff-cannot-complete case.
				slog.Warn("ws: workspace setup kickoff: unrecognized outcome — rejecting",
					"workspace_id", workspaceID, "outcome", outcome)
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "workspace setup could not be started",
					SessionId: &sidCopy,
				})
				return
			}
		}
		if sessionID == "" {
			// No session_id in frame: mint a new session so all subsequent frames have one.
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", targetAgentID)
			if err != nil {
				slog.Error("ws: could not create session", "error", err)
				// A successful kickoff consume just cleared SetupPending —
				// if minting the session then fails, the one-time interview would
				// otherwise be silently lost. Best-effort restore.
				if setupKickoff {
					h.restoreWorkspaceSetupPending(workspaceID)
				}
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
			var title string
			if setupKickoff {
				// The kickoff instruction text must not leak into the sidebar
				// as a session title — use a fixed, human-readable one instead.
				title = "Workspace setup"
			} else {
				titleRunes := []rune(content)
				if len(titleRunes) > 60 {
					title = string(titleRunes[:57]) + "..."
				} else {
					title = content
				}
			}
			// Stamp the session owner from the authenticated WebSocket user (SEC-2/#406).
			// wc.userID is set at auth time (FR-073); empty on dev-mode bypass.
			ownerCopy := wc.userID
			metaPatch := session.MetaPatch{Title: &title, Owner: &ownerCopy}
			// M4: bind the new session to the active workspace so created tasks
			// land on this workspace's board (not the agent's default).
			if workspaceID != "" {
				wsCopy := workspaceID
				metaPatch.WorkspaceID = &wsCopy
			}
			if err := store.SetMeta(meta.ID, metaPatch); err != nil {
				if setupKickoff {
					// A kickoff turn that fails to persist its title/owner/
					// workspace stamp would run UNBOUND from the workspace
					// that triggered it — worse than a plain warn-and-continue.
					// Treat this as a hard failure: restore the flag, delete
					// the just-minted orphan session, and reject before the
					// session_started ack (below) is ever sent.
					slog.Warn(
						"ws: workspace setup kickoff: could not persist session title/owner/workspace — rejecting",
						"session_id", meta.ID, "error", err)
					h.rollbackKickoffSession(store, workspaceID, meta.ID, chatID)
					sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
						Type:    string(generated.WsFrameTypeError),
						Message: "workspace setup could not be started",
					})
					return
				}
				slog.Warn("ws: could not set session title/owner", "session_id", meta.ID, "error", err)
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
			// This branch — an existing, client-supplied session_id — is
			// reachable only by a NORMAL (non-kickoff) message: the mint-only
			// guard near the top of this function already rejected any
			// setupKickoff frame carrying a non-empty session_id, and format
			// was already validated up front there too. No kickoff-restore
			// compensation is needed here as a result.
			//
			// Validate that the session actually exists in the store.
			existingMeta, err := store.GetMeta(sessionID)
			if err != nil {
				slog.Warn("ws: session not found", "session_id", sessionID, "error", err)
				sidCopy := sessionID
				sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
					Type:      string(generated.WsFrameTypeError),
					Message:   "session not found",
					SessionId: &sidCopy,
				})
				return
			}
			// M4: track the ACTIVE workspace on every message, not just the first
			// one. The SPA resends the CURRENTLY active workspace_id on every
			// outbound frame (src/store/chat.ts sendMessage, read live from
			// useWorkspacesStore) specifically so a task/delegation this turn
			// creates lands on whatever workspace the operator is looking at
			// right now — including an ongoing chat session that started in one
			// workspace and is continuing in another. A stale "first binding
			// wins" rule broke that: a delegation edge wired on a workspace's
			// Team tab AFTER the session's first bind would never be consulted
			// by resolveEffectiveWorkspaceID (pkg/agent/loop.go), which reads
			// this same session meta fresh every turn — so the UI's "Saved just
			// now" was genuine (see TestWorkspaceDelegation_EdgeWiredViaTeamTabPersistsForLiveSession)
			// but the running session kept enforcing/advertising delegation against its
			// ORIGINAL workspace until the operator started a brand new session.
			// Only skip the rewrite when this message carries no workspace_id at
			// all (workspaceID == "") — an absent value must never blank out an
			// existing binding (e.g. a non-workspace-aware channel message
			// resuming a workspace-bound session), it just leaves it as-is.
			if workspaceID != "" && existingMeta != nil && existingMeta.WorkspaceID != workspaceID {
				wsCopy := workspaceID
				if err := store.SetMeta(sessionID, session.MetaPatch{WorkspaceID: &wsCopy}); err != nil {
					slog.Warn("ws: could not bind workspace to session", "session_id", sessionID, "error", err)
				}
			}
			// Track for streamer.
			h.mu.Lock()
			if h.sessionIDs[chatID] == "" {
				h.sessionIDs[chatID] = sessionID
			}
			h.mu.Unlock()

			// ADR-045: this connection just confirmed itself live on an
			// EXISTING session (continuation, not a brand-new one) — cancel
			// any pending orphan-foreground-turn watchdog for it.
			h.agentLoop.DisarmOrphanForegroundTurnWatch(sessionID)
		}

		// ADR-066 D4 / FR-015: this handler persists the user message BEFORE
		// the bus publish, but processMessage is the enforcement point for
		// the user-message bound and refuses an over-bound message with NO
		// transcript entry. So the one thing this intake does for the bound
		// is skip that early write when processMessage is about to refuse —
		// the refusal reply itself comes back through the ordinary outbound
		// path (token + done frames, never an error frame). A kickoff turn
		// discards the client content entirely, so it is never over-bound.
		overUserBound := !setupKickoff &&
			agent.UserMessageChars(content) > h.agentLoop.UserMessageBound()
		if sessionID != "" && !overUserBound {
			entry := session.TranscriptEntry{
				ID:        uuid.New().String(),
				Role:      "user",
				AgentID:   targetAgentID,
				Content:   content,
				Timestamp: time.Now().UTC(),
				// D2 (library-spec, 2026-07-29 UAT): persist which files this
				// message attached. Previously this field was never set even
				// though it has existed on TranscriptEntry all along — a later
				// turn (or a DIFFERENT AGENT after a handoff, exactly what
				// happened in the UAT: Mia -> Ray) had no durable record of
				// what was uploaded, only the live in-flight message. Built
				// from acceptedMedia (the validated ref set), not the raw
				// client-supplied mediaRefs.
				Attachments: buildTranscriptAttachments(h.agentLoop.GetMediaStore(), acceptedMedia, workspaceID),
			}
			if setupKickoff {
				// Record the kickoff trigger as a system-role event, not a user
				// chat bubble. AgentID stays targetAgentID (Ava) so replay/
				// hydration attributes this entry to her on a fresh turn.
				entry.Type = session.EntryTypeSystem
				entry.Role = "system"
				// The PERSISTED/REPLAYED entry carries neutral, fixed
				// content — never the client-supplied kickoff instruction, and
				// not even the SERVER-BUILT one either (session replay renders
				// this as a system pill). The turn itself is instead driven by
				// the SERVER-BUILT kickoffInstruction via msg.Content below —
				// see turnContent — never by this entry or by `content`.
				entry.Content = "Workspace setup started."
			}
			if err := store.AppendTranscript(sessionID, entry); err != nil {
				slog.Warn("ws: could not record user message", "session_id", sessionID, "error", err)
			}
			// The workspace.setup_consumed audit entry is emitted further
			// below, AFTER a successful bus publish — not here. Emitting it
			// at this point (the previous placement) ran before the publish
			// that could still fail, producing a false "consumed" audit
			// record even on a failure that had just restored the flag and
			// rolled back this very session.
		}
	}

	// The turn is driven by the client-supplied content EXCEPT for a kickoff,
	// which uses the SERVER-BUILT canonical instruction assembled above from
	// the workspace's own name/description — the client-supplied content is
	// discarded entirely for a kickoff turn (see doc comment).
	turnContent := content
	if setupKickoff {
		turnContent = kickoffInstruction
	}

	msg := bus.InboundMessage{
		Channel: "webchat",
		Sender: bus.SenderInfo{
			CanonicalID: "webchat_user",
		},
		// FR-017: carry the WS-authenticated gateway principal (set at auth
		// time, e.g. "cli" or the account's username) so the agent loop can
		// stamp audit.Entry.User for turn attribution. This is the ONLY site that sets
		// GatewayUserID — it is a dedicated carrier, NOT Sender.Username, so that
		// platform channels (which fill Sender.Username with the platform handle)
		// can never have their sender misattributed as a gateway principal.
		// Empty under dev-mode bypass / legacy env-token auth (wc.userID is left
		// unset there) — the audit stamp then stays empty rather than guessing.
		GatewayUserID: wc.userID,
		ChatID:        chatID,
		Content:       turnContent,
		SessionID:     sessionID,
		Media:         acceptedMedia,
		// UserInitiated (ADR-049 Gap #8/r2, R6): the webchat WS `message`
		// handler is the "Web WS message handler (authenticated gateway
		// user)" origination point — always a genuine live user action.
		UserInitiated: true,
	}
	if agentID != "" {
		// Format already validated up front, before the kickoff consume step
		// (see the check next to the worker-agent guard near the top of this
		// function) — no re-validation needed here.
		msg.Metadata = map[string]string{"agent_id": agentID}
	}
	// FR-010: forward per-turn model override to the bus so the agent loop's
	// switch-compress path can route THIS turn to the chosen model. Trim
	// whitespace first; empty / whitespace-only values are dropped so the agent
	// falls back to its configured model. The map is created lazily here so a
	// model_name-only frame still produces a populated Metadata on the wire.
	trimmedModel := strings.TrimSpace(modelName)
	if trimmedModel != "" {
		if msg.Metadata == nil {
			msg.Metadata = map[string]string{}
		}
		msg.Metadata["model_name"] = trimmedModel
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.msgBus.PublishInbound(pubCtx, msg); err != nil {
		slog.Warn("ws: failed to publish message", "error", err)
		// Same compensation as the earlier session-mint/SetMeta failures — a
		// successful kickoff consume must not be silently lost just because
		// the bus publish that was supposed to drive Ava's turn failed. Also
		// deletes the just-minted "Workspace setup" session so a repeated
		// failure does not accumulate orphan empty sessions.
		if setupKickoff {
			h.rollbackKickoffSession(store, workspaceID, sessionID, chatID)
		}
		sidCopy := sessionID
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type:      string(generated.WsFrameTypeError),
			Message:   fmt.Sprintf("failed to deliver message: %v", err),
			SessionId: &sidCopy,
		})
		return
	}

	// Audit the kickoff consume only AFTER a successful publish — the turn is
	// now genuinely running (the commit point; see the two accepted-tradeoff
	// notes in the doc comment above). Emitting this before publish (the
	// previous placement) produced a false "consumed" audit entry even on a
	// publish failure that had just restored the flag and rolled back the
	// session. Mirrors the workspace.create/workspace.update audit calls in
	// rest_workspaces.go — best-effort, never blocks the turn.
	if setupKickoff {
		if auditor := h.agentLoop.AuditLogger(); auditor != nil {
			if err := auditor.Log(&audit.Entry{
				Event:     "workspace.setup_consumed",
				Decision:  audit.DecisionAllow,
				AgentID:   targetAgentID,
				SessionID: sessionID,
				User:      wc.userID,
				Details: map[string]any{
					"workspace_id": workspaceID,
				},
			}); err != nil {
				slog.Warn("ws: workspace setup kickoff: audit write failed",
					"workspace_id", workspaceID, "session_id", sessionID, "error", err)
			}
		}
	}
}

// kickoffOutcome is the result of consumeWorkspaceSetupKickoff.
type kickoffOutcome int

const (
	// kickoffConsumed means SetupPending was true and has now been cleared and
	// persisted — the caller should proceed with the kickoff turn.
	kickoffConsumed kickoffOutcome = iota
	// kickoffDuplicate means the workspace's SetupPending was already false (a
	// second kickoff raced or replayed) — the caller must reject the frame
	// outright (error frame, no session minted, nothing published to the bus).
	kickoffDuplicate
	// kickoffFailed means the workspace file could not be read, or the cleared
	// flag could not be persisted — the caller must reject the frame outright
	// (this used to "demote" to a normal message instead, silently
	// persisting the kickoff instruction as a fake user-authored transcript
	// entry). SetupPending is left untouched on disk in this case, so a later
	// workspace open can retry the kickoff.
	kickoffFailed
)

// consumeWorkspaceSetupKickoff clears workspaceID's SetupPending flag exactly
// once, under the workspace's own per-ID lock (workspace.LockID) so concurrent
// kickoff frames (e.g. two tabs opening the same brand-new workspace at once)
// serialize against each other AND against every other writer of this same
// workspace file — a REST PUT/DELETE/delegation-PUT racing this consume can no
// longer resurrect a just-cleared flag with a stale write-back, clobber this
// write, or (for DELETE) have this consume resurrect a just-deleted file as a
// ghost: a delete-then-kickoff race always serializes so the kickoff's
// readWorkspaceFile either sees the workspace fully intact or (post-delete)
// fails with errWorkspaceNotFound, which this function maps to kickoffFailed
// — it never recreates the file.
//
// On a kickoffConsumed outcome, also returns the workspace's Name and
// Description (read under the same lock, from the same on-disk record that
// was just consumed) so the caller can build the SERVER-CANONICAL turn
// instruction from them instead of trusting client-supplied content. Both
// are empty strings for a non-consumed outcome.
func (h *WSHandler) consumeWorkspaceSetupKickoff(
	workspaceID string,
) (outcome kickoffOutcome, name string, description string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(h.home, workspaceID)
	if err != nil {
		slog.Warn("ws: workspace setup kickoff: could not read workspace file — rejecting",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	if !w.SetupPending {
		slog.Warn("ws: workspace setup kickoff: setup already ran — rejecting duplicate",
			"workspace_id", workspaceID)
		return kickoffDuplicate, "", ""
	}
	w.SetupPending = false
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(h.home, w); err != nil {
		slog.Warn("ws: workspace setup kickoff: could not persist cleared setup_pending — rejecting",
			"workspace_id", workspaceID, "error", err)
		return kickoffFailed, "", ""
	}
	return kickoffConsumed, w.Name, w.Description
}

// buildTranscriptAttachments resolves each accepted media ref into a
// session.Attachment for persistence on the user's TranscriptEntry (D2,
// library-spec). A ref that fails to resolve (deleted file, integrity
// failure, cross-workspace mismatch) is logged and skipped rather than
// aborting the whole message — an attachment record is best-effort
// metadata, not load-bearing for the turn itself (the raw ref still reaches
// the agent loop via msg.Media regardless of what this function returns).
//
// Path prefers the EXACT workspace-relative path the D-1 dual-write staged
// the file at (agent.LookupUploadWorkPath), falling back to the best-effort
// plain-name formula (agent.FallbackAnnouncedUploadPath) for a workspace ref
// whose write-time record is unavailable (e.g. a gateway restart between
// the upload and this message), and finally to the bare ref for a
// non-workspace ref (legacy media://<uuid>, channel-native attachments),
// which has no workspace-relative path at all.
func buildTranscriptAttachments(store media.MediaStore, refs []string, callerWorkspace string) []session.Attachment {
	if store == nil || len(refs) == 0 {
		return nil
	}
	opts := media.ResolveOpts{}
	if callerWorkspace != "" {
		opts = media.WithCallerWorkspace(callerWorkspace)
	}
	out := make([]session.Attachment, 0, len(refs))
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMetaOpts(ref, opts)
		if err != nil {
			slog.Warn("ws: could not resolve media ref for transcript attachment",
				"ref", ref, "error", err)
			continue
		}
		var size int64
		if info, statErr := os.Stat(localPath); statErr == nil {
			size = info.Size()
		} else {
			slog.Warn("ws: could not stat media file for transcript attachment size",
				"ref", ref, "error", statErr)
		}
		path := ref
		if media.IsWorkspaceRef(ref) {
			if workPath, ok := agent.LookupUploadWorkPath(ref); ok {
				path = workPath
			} else if fallback := agent.FallbackAnnouncedUploadPath(meta.Filename); fallback != "" {
				path = fallback
			}
		}
		out = append(out, session.Attachment{
			// DetectAttachmentType, NOT DetectFileClass. This value crosses
			// the wire, so it must be the contract's media-category enum
			// (image|audio|video|file). DetectFileClass returns the
			// presentation-noun class (image|document|file) used to phrase
			// the LLM guidance line — "document" is not a legal wire value.
			// Using it here shipped a BLOCKER: the SPA's strict Zod schema
			// rejected the whole messages payload, so chat history refused
			// to load at all after any document upload (live UAT 2026-07-29).
			Type:     agent.DetectAttachmentType(meta.ContentType, meta.Filename),
			Path:     path,
			Size:     size,
			MIMEType: meta.ContentType,
		})
	}
	return out
}

// buildWorkspaceKickoffInstruction assembles the SERVER-CANONICAL prompt that
// drives a workspace-setup kickoff turn, from the workspace's own name and
// (optional) description — never from client-supplied content. See the
// handleChatMessage doc comment for why: an arbitrary client instruction
// driving Ava's first turn on a new workspace is a forensics gap and an
// i18n/stale-SPA drift risk that a fixed, server-built prompt closes.
func buildWorkspaceKickoffInstruction(name, description string) string {
	instruction := fmt.Sprintf("The workspace %q was just created.", name)
	if desc := strings.TrimSpace(description); desc != "" {
		instruction += fmt.Sprintf(" Its description: %s.", desc)
	}
	instruction += " Introduce yourself and interview the user about this workspace's purpose" +
		" so you can determine which agents and skills its team needs, then set up the team."
	return instruction
}

// rollbackKickoffSession undoes a partially-completed workspace-setup kickoff
// after a post-mint failure (SetMeta or PublishInbound): it restores the
// workspace's SetupPending flag (best-effort, see restoreWorkspaceSetupPending)
// AND deletes the just-minted "Workspace setup" session so a repeated
// failure does not accumulate orphan empty sessions — mirroring the
// rollbackCreatedSessions precedent in rest_workspaces.go. Also clears the
// chatID→sessionID tracking entry if it still points at the session being
// deleted, so a subsequent frame on the same connection does not resolve to
// a session that no longer exists.
//
// store may be nil (defensive only — every call site already holds a
// non-nil store by construction) and sessionID may be empty (the NewSession-
// failure path has nothing to delete yet); both are treated as "nothing to
// roll back beyond the flag restore".
func (h *WSHandler) rollbackKickoffSession(store *session.UnifiedStore, workspaceID, sessionID, chatID string) {
	h.restoreWorkspaceSetupPending(workspaceID)
	if store != nil && sessionID != "" {
		if err := store.DeleteSession(sessionID); err != nil {
			slog.Warn("ws: workspace setup kickoff: rollback session delete failed",
				"session_id", sessionID, "error", err)
		}
	}
	h.mu.Lock()
	if h.sessionIDs[chatID] == sessionID {
		delete(h.sessionIDs, chatID)
	}
	h.mu.Unlock()
}

// restoreWorkspaceSetupPending re-sets workspaceID's SetupPending flag to true
// after a successful kickoff consume (consumeWorkspaceSetupKickoff returned
// kickoffConsumed) was followed by a downstream failure — minting the new
// session, looking up an existing session's meta, or publishing the turn to
// the bus — that means the one-time setup interview never actually ran (Fix
// 2). Acquires the same per-workspace lock as consumeWorkspaceSetupKickoff so
// the restore itself cannot race a concurrent writer.
//
// Best-effort: a read/write failure here is logged and otherwise ignored —
// the caller has already committed to sending the user an error frame for the
// original failure, and there is no further fallback. A restore failure
// leaves SetupPending=false despite no interview having run, which is a
// degraded but non-corrupting outcome (the operator can still trigger team
// setup manually via the workspace's Team tab).
func (h *WSHandler) restoreWorkspaceSetupPending(workspaceID string) {
	unlock := workspace.LockID(workspaceID)
	defer unlock()

	w, err := readWorkspaceFile(h.home, workspaceID)
	if err != nil {
		slog.Warn("ws: workspace setup kickoff: could not read workspace file to restore setup_pending",
			"workspace_id", workspaceID, "error", err)
		return
	}
	w.SetupPending = true
	w.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeWorkspaceFile(h.home, w); err != nil {
		slog.Warn("ws: workspace setup kickoff: could not restore setup_pending after downstream failure",
			"workspace_id", workspaceID, "error", err)
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

// u11CollectDescendantSessionIDs walks the durable lifecycle store's
// ParentDurableKey edges (pkg/session/lifecycle.go, FR-019/FR-020;
// LifecycleStore.List(LifecycleFilter{ParentDurableKey: id}) returns X's
// DIRECT children only, index-backed per BDD-19) to collect EVERY descendant
// of rootID, however many delegation levels deep. Returns only descendants —
// rootID itself is never included; the caller prepends it.
//
// ADR-057 FR-032/W10c: cancelAllPendingForSessions matches a pending
// approval by EXACT equality on the registry entry's own acting session id
// (FR-080 — a delegated child's entry carries the CHILD's own id, never the
// chat's), so a chat-level Stop that passed only the chat/root id would
// cancel nothing inside any live child (BDD-33). The DURABLE lifecycle store
// is reachable read-only through h.agentLoop's already-exported
// GetSessionLifecycleStore() (pkg/agent/session_messaging_wire.go), and
// every delegation — live or not — has a LifecycleRecord (User Story 4), so
// this walk is authoritative independent of what is still running.
//
// [FIX-5, Defect 4, 2026-08-03] HOISTED: this used to be a byte-identical
// duplicate of pkg/agent/cancel.go's collectDescendantSessionIDs, kept
// separate only because a parallel-implementation ownership rule (then:
// "neither of which this unit may edit") forbade this unit from touching
// pkg/agent. That rule has expired — this is now a thin signature-compat
// shim over the ONE hoisted implementation, agent.CollectDescendantSessionIDs
// (pkg/agent/cancel.go), which also gained a real error return (Defect 2:
// a lifecycleStore.List failure partway through the walk used to be
// swallowed as "this node has no children" — silently truncating the
// returned set with no signal). This shim is kept, rather than deleted and
// inlined at every call site, because pkg/gateway/rest.go's deleteSession
// handler and pkg/gateway/websocket_adr057_test.go's U11 unit tests call it
// directly by this exact name/signature and are outside this fix's file
// ownership — changing its signature would require edits there too. A
// caller that CAN use the richer (typed-error) signature directly — see
// buildCancelHooks's CancelPendingApprovals closure below — calls
// agent.CollectDescendantSessionIDs itself instead of this shim, so it can
// react to a partial-walk failure with a more specific diagnostic than the
// generic one logged here.
//
// Guards against a corrupted or cyclic ParentDurableKey chain with a visited
// set rather than trusting the system's own delegation-depth cap
// (config.SubTurn.MaxDepth) to bound recursion — this walk must terminate
// even over on-disk state that predates or violates that cap. A nil store
// (no delegation lifecycle store wired — most webchat-only installs never
// mint one) yields an empty slice, so the caller degrades to exactly
// cancelAllPendingForSession's documented single-id behavior. A walk error
// is logged at WARN (never returned — see the shim rationale above) so this
// call's INCOMPLETE-cascade case is never silent, even though it cannot be
// propagated through this signature.
func u11CollectDescendantSessionIDs(ls *session.LifecycleStore, rootID string) []string {
	descendants, err := agent.CollectDescendantSessionIDs(ls, rootID)
	if err != nil {
		slog.Warn("ws: descendant walk: could not list children — the walk is INCOMPLETE; any descendant beyond the failure point is unreachable to this caller",
			"session_id", rootID, "error", err)
	}
	return descendants
}

// buildCancelHooks constructs the transport-specific agent.CancelHooks for a
// web-SPA-originated cancel. wc is the live connection to notify via
// cancel_stage frames — nil when there is no live connection to notify.
//
// The nil case is exactly the orphan-foreground-turn watchdog's reap path
// (ADR-045, reapOrphanForegroundTurn below): it fires precisely because
// nobody is watching the session anymore, so there is no wc to send a
// cancel_stage frame to. sendCancelStageFrame already no-ops safely on a nil
// wc, so the SAME hook set handleCancel builds for a real Stop-click also
// works, unmodified, for the orphan-reap path — there is only one place in
// this file that knows how to build a web-cancel's side effects.
func (h *WSHandler) buildCancelHooks(wc *wsConn) agent.CancelHooks {
	return agent.CancelHooks{
		SendStageFrame: func(sid, stage string) {
			sendCancelStageFrame(wc, sid, stage)
		},
		CancelPendingApprovals: func(sid, reason string) {
			if h.approvalRegV2 == nil {
				return
			}
			// ADR-057 FR-032/W10c: cancel over the DESCENDANT SET, not the
			// single chat/root id — see u11CollectDescendantSessionIDs' doc
			// comment for why a single id would silently cancel nothing
			// inside a live delegated child (BDD-33). The old single-id
			// cancelAllPendingForSession still compiles and remains correct
			// for a session with no descendants (its own doc comment says
			// so); this call site is the one FR-032 requires use the plural
			// form.
			var lifecycleStore *session.LifecycleStore
			if h.agentLoop != nil {
				lifecycleStore = h.agentLoop.GetSessionLifecycleStore()
			}
			// [FIX-5, Defect 2, 2026-08-03] Calls agent.CollectDescendantSessionIDs
			// directly (not the u11CollectDescendantSessionIDs shim above) so this
			// call site can react to a partial-walk failure with the specific,
			// user-visible consequence it has here: a lifecycleStore.List error
			// partway through the walk used to be swallowed as "no more
			// children" and the dropped descendant's pending RequestApproval was
			// left standing — its blocked select only unblocks on ITS OWN
			// multi-minute approval timeout, all while this Stop click's UI
			// already reported the cancel as complete.
			descendants, walkErr := agent.CollectDescendantSessionIDs(lifecycleStore, sid)
			if walkErr != nil {
				slog.Warn("ws: buildCancelHooks: descendant walk failed partway through — the approval-cancel cascade below is INCOMPLETE; a dropped descendant's pending approval will hang until its own timeout instead of being auto-denied by this Stop",
					"session_id", sid, "error", walkErr)
			}
			sessionIDs := append([]string{sid}, descendants...)
			h.approvalRegV2.cancelAllPendingForSessions(sessionIDs, reason)
		},
		KillBackgroundSessions: func(sid string) (killed, failed int) {
			// Cascade the cancel to any detached background bash/exec
			// sessions this chat session started (FR-B10/FR-B11, User Story 5).
			// The returned counts flow back through RequestCancel into the
			// turn_canceled audit event's background_sessions_killed/
			// background_sessions_failed fields, and into CancelOutcome so
			// handleCancel below can notify the client even when there was no
			// active turn to cancel.
			return tools.GetSharedSessionManager().KillAllForSession(sid)
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
		// OnLatchExpired closes the last gap in the chain: handleCancel below
		// already tells the user "acknowledged" (cancel_stage "graceful") the
		// instant CancelOutcome.Armed is true, but a latch that ages out
		// (cancelPreArmTTL, pkg/agent/cancel_prearm.go) with no turn ever
		// registering to consume it means that acknowledgement was never made
		// good on — the turn runs to completion (or the background-only
		// cancel this reap represents never lands at all) uncanceled. Without
		// this, the user is told "stop requested" and then never told it
		// didn't happen — the exact silent-success defect this whole chain
		// exists to close, just moved one step later.
		//
		// Deliberately NOT a second cancel_stage frame: {graceful, hard,
		// detached} (contracts/components/schemas/CancelStageFrame.yaml) is a
		// closed, SPA-validated enum and none of its three members mean
		// "requested but did not land" — graceful/hard/detached all describe
		// a cancel that IS proceeding. Reusing "graceful" here (as the
		// no-active-turn case just above does, deliberately, for the
		// still-pending case) would claim progress that never happened —
		// over-signaling in exactly the place this fix must not. ErrorFrame
		// is the right existing wire type instead: it is documented as
		// "session-scoped... SPA displays the message as a toast or inline
		// error... does NOT terminate the WebSocket connection" — precisely
		// "something the user asked for did not happen", with no enum to
		// extend and no contract change required.
		OnLatchExpired: func(scope agent.CancelScope, canceller agent.CancelCanceller) {
			if wc == nil {
				// No live connection to notify — the orphan-foreground-turn
				// reap path (reapOrphanForegroundTurn below, via
				// buildCancelHooks(nil)) fires precisely because nobody is
				// watching this session anymore. notifyLatchExpired
				// (cancel_prearm.go) already logged the base "latch expired"
				// Warn unconditionally; add site-specific context here so an
				// operator sees not just THAT it expired but that this was a
				// no-connection (reap/background) case with no user to have
				// told anyway.
				slog.Warn("ws: cancel latch expired with no live connection to notify — the cancel it stood in for never took effect",
					"session_id", scope.SessionID,
					"channel", scope.Channel,
					"chat_id", scope.ChatID,
					"canceller_user", canceller.UserID,
					"canceller_channel", canceller.Channel,
				)
				return
			}
			var sidPtr *string
			if scope.SessionID != "" {
				sidCopy := scope.SessionID
				sidPtr = &sidCopy
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				SessionId: sidPtr,
				Message:   "Cancel request did not take effect: no matching operation was found before the request timed out. If something is still running, try Stop again.",
			})
		},
	}
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
	hooks := h.buildCancelHooks(wc)

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
		slog.Debug("ws: cancel — no active turn or already canceled",
			"session_id", sessionID, "armed", outcome.Armed)
		// Observability fix: a cancel with no active turn is the COMMON case
		// for a `bash run_in_background=true` job whose own turn already
		// ended (see cancel.go's RequestCancel doc comment) — the background
		// kill cascade above still ran and may have actually killed real
		// work, but without this the user clicking Stop got ZERO feedback
		// (no frame, no log) despite that. Reuse the existing "graceful"
		// stage value rather than introducing a new CancelStageFrame.stage
		// enum member: that field is a closed, SPA-validated enum
		// ({graceful, hard, detached} — contracts/components/schemas/
		// CancelStageFrame.yaml) and adding a value would require a
		// contract + frontend change beyond this fix's scope; "graceful"'s
		// documented meaning ("cancel request acknowledged; agent is
		// completing the current tool call and will stop at the next safe
		// checkpoint") fits a background-only kill well enough to give the
		// Stop button SOME visible response instead of silence.
		//
		// outcome.Armed closes a second, HIGH-severity gap in the same
		// family (review finding on commit 99d4e729, CancelOutcome.Armed):
		// its doc comment mandates "Callers surfacing Fired to a user MUST
		// also check Armed before reporting a cancel as a no-op" — a contract
		// every RequestCancel caller that surfaces Fired to a user or operator
		// must honour, not one this edit closes everywhere in one pass. This
		// fixes THIS caller (the web-SPA Stop button, handleCancel): a Stop
		// click arriving before its turn has registered
		// (pkg/agent/cancel_prearm.go) now correctly latches and WILL cancel
		// that turn the instant it registers (within cancelPreArmTTL), and
		// the click itself produces a frame here instead of the pre-fix zero
		// frames. Other RequestCancel callers still need their own fix for
		// the same contract — notably pkg/commands' RequestCancelForSession
		// (flattens to a bare bool, dropping Armed, so cmd_cancel.go reports
		// "Nothing to cancel" for an armed latch; owned by a separate agent
		// via a widened adapter signature) and plan_engine.go's cancelSessions
		// fan-out (buckets Armed as notFired; owned separately) — do not
		// treat either as covered by this file. Reuse "graceful" for this
		// case too, for the same reason as the background-kill case above:
		// on the wire it means "acknowledged, pending", never "canceled" —
		// nothing has actually stopped yet, so claiming more than that would
		// just be the same under-signaling bug inverted into over-signaling
		// (the trap this fix must not fall into).
		if outcome.BackgroundSessionsKilled > 0 {
			slog.Info("ws: cancel killed background session(s) with no active turn",
				"session_id", sessionID,
				"background_sessions_killed", outcome.BackgroundSessionsKilled,
				"background_sessions_failed", outcome.BackgroundSessionsFailed,
				"armed", outcome.Armed,
			)
		}
		if outcome.Armed {
			slog.Info("ws: cancel armed a pre-registration latch — no turn registered yet for this session; the next turn to register will be canceled the instant it does, unless the latch's TTL expires first",
				"session_id", sessionID,
			)
		}
		if outcome.BackgroundSessionsKilled > 0 || outcome.Armed {
			sendCancelStageFrame(wc, sessionID, "graceful")
		}
	}
}

// reapOrphanForegroundTurn is the ADR-045 orphan-foreground-turn watchdog's
// reap callback (wired at Arm time in ServeHTTP's teardown defer). It is
// invoked by agent.AgentLoop.fireOrphanForegroundTurnWatch AT MOST ONCE per
// arm, and only once that function has itself confirmed all three safety
// conditions hold (a genuine live root turn, no surviving Critical/background
// delegate, no reconnect) — see that function's doc comment for the full
// gate. This performs the EXACT SAME cancellation every other cancel surface
// gets: RequestCancel's audit/transcript writes, approval auto-deny,
// background-session kill, and graceful->hard->detached escalation —
// attributed to the system rather than a human canceller, since this is
// precisely the WHOLE reason this fired: nobody is here to have clicked Stop.
//
// wc is nil here (see buildCancelHooks) — there is no live connection to
// notify with cancel_stage frames.
func (h *WSHandler) reapOrphanForegroundTurn(sessionID, reason string) {
	scope := agent.CancelScope{SessionID: sessionID}
	canceller := agent.CancelCanceller{
		UserID:  "system",
		Channel: "orphan-watchdog",
	}
	hooks := h.buildCancelHooks(nil)

	outcome, err := h.agentLoop.RequestCancel(context.Background(), scope, canceller, hooks)
	if err != nil {
		slog.Warn("ws: orphan watchdog: RequestCancel error",
			"session_id", sessionID, "reason", reason, "error", err)
		return
	}
	if !outcome.Fired {
		// armed distinguishes "genuinely nothing to do" from "a pre-
		// registration cancel latch now stands in for this reap and will
		// fire on the next turn to register under this session, within
		// cancelPreArmTTL" (CancelOutcome.Armed doc comment, pkg/agent/
		// cancel.go). No frame is sent either way — wc is nil here (see
		// buildCancelHooks above), because a reap fires precisely when
		// nobody is watching this session anymore, so there is no live
		// connection to acknowledge to regardless of which case this is.
		slog.Debug("ws: orphan watchdog: RequestCancel no-op (turn already finished or already canceled)",
			"session_id", sessionID, "reason", reason, "armed", outcome.Armed)
	}
}

// sessionStillOrphaned reports whether sessionID currently has NO live WS
// connection watching it — i.e. nobody has reconnected/reattached since the
// orphan-foreground-turn watch was armed. Called by
// agent.AgentLoop.fireOrphanForegroundTurnWatch immediately before reaping so
// a reconnect that raced the grace timer — landing after
// DisarmOrphanForegroundTurnWatch would have caught it, but before the fire
// goroutine actually ran — still wins (MA-5).
func (h *WSHandler) sessionStillOrphaned(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sid := range h.sessionIDs {
		if sid == sessionID {
			return false
		}
	}
	return true
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
	//
	// Finding E (A-I4 round 5): h.sessionIDs[chatID] — this connection's OWN
	// chatID→session mapping, read by fanOutToSessionPeers/matchesEvent/
	// GetStreamer to decide "does this connection currently belong to session
	// X" — used to be written ONLY after replay+hydrate finished (see the
	// second h.sessionIDs[chatID] assignment below, previously the sole
	// writer). Between this function's entry and that later write, the
	// mapping kept whatever value a PRIOR attach_session on this same
	// connection last set it to (or "" for a brand-new connection). A
	// connection that attaches twice in quick succession — e.g. a reconnect
	// whose first attach_session targets a stale/leftover session id
	// (frontend activeSessionId not yet corrected to the URL's real session)
	// followed immediately by a second, correcting attach_session for the
	// right session — left h.sessionIDs[chatID] pointing at the FIRST
	// (wrong, unrelated) session for this whole replay window. Any OTHER,
	// genuinely unrelated session's background delegate fanning out a live
	// token/done frame during that window (fanOutToSessionPeers, matched
	// purely by sessionID equality) found this connection listed as one of
	// its peers and delivered straight into wc.sendCh — landing mid-replay,
	// live-verified as a stray, uninstructed bubble duplicating another
	// session's already-delivered content with a garbled leading fragment.
	// Setting the real mapping HERE, atomically with taskChatIDs/the
	// self-map, closes the window: from this point on h.sessionIDs[chatID]
	// always reflects the CURRENT attach target, so no other session can
	// ever be mistaken for a peer of this connection. Purely additive — the
	// second assignment after replay/hydrate is left in place as a
	// redundant, idempotent reaffirmation.
	h.mu.Lock()
	if oldTID, ok := h.taskChatIDs[chatID]; ok {
		delete(h.sessionIDs, oldTID)
	}
	h.taskChatIDs[chatID] = attachID
	h.sessionIDs[attachID] = attachID
	h.sessionIDs[chatID] = attachID
	h.mu.Unlock()

	// ADR-045: a live connection just (re)confirmed itself on attachID — cancel
	// any pending orphan-foreground-turn watchdog for this session. Covers the
	// common browser-refresh/reconnect case with zero user-visible effect.
	h.agentLoop.DisarmOrphanForegroundTurnWatch(attachID)

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
	var isSpanActive func(string) bool
	if h.agentLoop != nil {
		mediaStore = h.agentLoop.GetMediaStore()
		// Wire real sub-turn liveness into replay so a spawn/delegate call
		// whose placeholder ack (async delegation: Status="success",
		// DurationMS≈0) has not yet been corrected by the real
		// EventKindSubTurnEnd is never shown as a fabricated "done" — see
		// agent.AgentLoop.IsSubTurnActiveForSpawnCall's doc comment.
		isSpanActive = h.agentLoop.IsSubTurnActiveForSpawnCall
	}
	framesEmitted, replayErr := streamReplay(ctx, attachID, entries, rs, emitFn, mediaStore, h.toolStore, isSpanActive)

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
	// Ordering guarantee (see docs/internal/investigation/bug-5-replay-order.md and
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
	//
	// ADR-066 D5.5 (FR-045): only an EMPTY agent archive is hydrated. An
	// archive with ≥ 1 line is the live record of the session — rebuilding
	// it from the UI transcript was the verified mechanism that dropped
	// every tool result and reset Skip on each reopen (US-15).
	if h.agentLoop.AgentArchiveNonEmpty(attachID) {
		slog.Debug("ws: attach_session: agent archive non-empty; hydration skipped",
			"session_id", attachID)
	} else if err := h.agentLoop.HydrateAgentHistoryFromTranscript(attachID); err != nil {
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
	// 2026-07-31 review finding (mirrors the same fix in browser_ws.go's
	// writePump): returning here on a write-side stall used to leave the
	// connection write-dead but read-alive — nothing else in this function
	// called wc.close(), so pingPump/sendRawFrameBytes kept selecting on a
	// doneCh that was never closed, and readLoop's own read deadline kept
	// getting refreshed by whatever the client was still sending (including
	// the client's own app-level ping). The SetWriteDeadline calls below only
	// bound how long ONE write blocks; without this, the connection was still
	// only actually reaped by the client's independent missed-ping self-heal
	// (ws.ts), not by anything server-side. wc.close() is sync.Once-guarded,
	// so signalling here the moment the writer dies is safe to call alongside
	// whatever else already calls it.
	defer wc.close()

	for {
		select {
		case msg, ok := <-wc.sendCh:
			if !ok {
				return
			}
			if msg == nil {
				// nil sentinel: send a WebSocket ping frame.
				//
				// SetWriteDeadline before every write (including this
				// keepalive ping) so a slow/back-pressured client can't
				// stall this single writer goroutine indefinitely — without
				// it, a blocked write here would silently starve the
				// keepalive ping, and the reverse proxy would eventually
				// reset the TCP connection with no close frame (browser
				// sees code 1006) instead of the deadline firing and
				// tearing the connection down cleanly within wsWriteWait.
				if err := wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
					slog.Debug("ws: SetWriteDeadline failed for ping", "error", err)
					return
				}
				if err := wc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					slog.Debug("ws: ping write error", "error", err)
					return
				}
				continue
			}
			if err := wc.conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
				slog.Debug("ws: SetWriteDeadline failed", "error", err)
				return
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
	ticker := time.NewTicker(wsPingPeriod)
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
// Ordering guarantee (see docs/internal/investigation/bug-5-replay-order.md, code-reviewer
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
	// Bound the write so a slow/back-pressured client can't block this
	// direct write indefinitely (same invariant as writePump's per-write
	// deadline — see wsWriteWait's doc comment).
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		slog.Debug("ws: SetWriteDeadline failed", "error", err)
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

// orphanWatchdogMaxRechecks bounds how many times startOrphanWatchdog will
// reschedule after agent.AgentLoop.IsSubTurnActiveForSpawnCall reports "still
// active" before giving up and force-emitting the synthetic interrupted
// terminal frame regardless of what the liveness check reports (fail-closed).
//
// The re-check-and-reschedule loop is correctly bounded for the NORMAL case
// by the pre-existing sub-turn context timeout (pkg/agent/subturn.go's
// defaultSubTurnTimeout, 5 minutes by default, or subturn.default_timeout_minutes
// when configured) — that timeout cancels the child's context, runTurn
// returns, and IsSubTurnActiveForSpawnCall eventually reports false once
// spawnSubTurn's cleanup defer finishes persisting the real terminal status
// (see turnState.subTurnRecordPersisted's doc comment, pkg/agent/turn.go).
// But a genuinely wedged/deadlocked turn — a goroutine that neither returns
// nor panics, e.g. blocked on a tool call that does not honor context
// cancellation — has no ceiling of its own: IsSubTurnActiveForSpawnCall would
// report "active" forever (isFinished never flips), and without this bound
// the watchdog would reschedule indefinitely, logging only at slog.Debug
// (invisible at typical production log levels) and never emitting a terminal
// frame for that span.
//
// Default 15 reschedules x orphanWatchdogTimeout's default 60s = 15 minutes,
// comfortably (~3x) above pkg/agent/subturn.go's defaultSubTurnTimeout (5
// minutes) — a legitimately still-running sub-turn should never come close to
// exhausting this many reschedules; long before it would, its own context
// timeout has fired and IsSubTurnActiveForSpawnCall is already reporting
// false. Configurable so tests can override to a small value without
// sleeping for the real 15 minutes.
var orphanWatchdogMaxRechecks = 15

// openSpanEntry tracks an in-flight subagent span in the event forwarder.
type openSpanEntry struct {
	spanID          string
	parentCallID    string
	agentID         string
	sessionID       string        // session_id at spawn time; carried on synthesized frames
	parentTurnEnded bool          // set to true when EventKindTurnEnd fires for the parent turn
	closeCh         chan struct{} // closed when EventKindSubTurnEnd arrives (cancels watchdog)
}

// ADR-057 FR-089 — W5 audit classification artefact (U11's half).
//
// generated.SESSION_SCOPED_FRAME_TYPES has 19 members. 13 were classified by
// the spec itself (adr-057-session-unification-spec.md, BDD-16/BDD-98/BDD-99):
// class (a) both-ids — token, done, tool_call_start, tool_call_result,
// tool_approval_required, media; class (b) producing_session_id-absent —
// replay_message, session_started, session_close_ack, subagent_start,
// subagent_end; class (c) documented pre-existing gap — rate_limit,
// replay_done. The remaining 6 were left "class not yet assigned by the W5
// audit" on their generated types pending this classification, verified
// 2026-08 against this tree:
//
//   - agent_switched → class (a). Built at this file's ToolExecEnd case
//     (below, evtSID := p.SessionID from agent.ToolExecEndPayload) immediately
//     after a successful hand_off/return_to_default tool_call_result — the
//     IDENTICAL payload and session-id source as tool_call_result, which is
//     already verified class (a). A delegated child can invoke hand_off on
//     its own session exactly as a root turn can, so evtSID is the child's own
//     producing session whenever that happens, distinct from the routing key.
//     Stamped alongside tool_call_result above.
//
//   - task_status_changed → class (b). Its only non-test construction site is
//     `TaskStatusChangedFrame{..., SessionId: p.SessionID, ...}` (this file's
//     EventKindTaskStatusChanged case), fed solely by
//     agent.TaskStatusChangedPayload, whose ONLY constructor is
//     pkg/agent/task_executor.go:1821-1830 (`sessionID := t.SessionID; ...
//     EmitTaskStatusChanged(TaskStatusChangedPayload{SessionID: sessionID,
//     ...})`) — the TaskExecutor (a system-level component) reporting a
//     scheduled task's OWN session lifecycle, not narration produced by a
//     delegated child turn. No stamping added; producing_session_id stays
//     absent.
//
//   - cancel_stage → class (b). Sole constructor is sendCancelStageFrame
//     (this file), called only with the id RequestCancel's CancelScope.SessionID
//     resolved to (pkg/agent/cancel.go:404-409,
//     `hooks.SendStageFrame(sessionID, "graceful")`) — the cancel machinery's
//     own target id, narrating the Stop's progress across the whole subtree it
//     cascades to (FR-032/W10c below), never a specific descendant's own
//     output. No stamping added.
//
//   - goal_status → class (b). Its only non-test construction site is this
//     file's EventKindGoalStatusChanged case (`SessionId: p.SessionID` from
//     agent.GoalStatusChangedPayload), whose constructors —
//     pkg/agent/goal_loop.go:502-514, several sites in
//     pkg/agent/goal_triggers.go, and pkg/agent/session_messaging_wire.go:602-606
//     /:626-630 — all pass the session that OWNS the /goal loop config being
//     reported, i.e. the session reporting on itself. No call site was found
//     where this payload's SessionID is a delegated child distinct from a
//     parent's routing id. No stamping added.
//
//   - loop_status → class (b). Its only non-test construction site is this
//     file's EventKindLoopStatusChanged case (`SessionId: p.SessionID` from
//     agent.LoopStatusChangedPayload), whose sole constructor
//     (pkg/agent/loop_command.go:301-309, `emitLoopStatusFrame(sessionID,
//     ...)`) reports on the session whose OWN /loop state changed — the
//     identical "reporting on itself" shape as goal_status. No stamping added.
//
//   - system_overload → COULD NOT DETERMINE; not guessed (spec line ~1309
//     forbids it). Verified: `rg -rl 'SystemOverloadFrame|system_overload'
//     pkg/` matches only the generated type
//     (pkg/api/generated/asyncapi_types.gen.go), its fixtures
//     (pkg/api/generated/fixtures.go), and the inbound-schema copy
//     (pkg/gateway/inboundschemas/SystemOverloadFrame.yaml) — ZERO
//     non-generated, non-fixture Go call site constructs or sends this frame
//     anywhere in pkg/gateway or pkg/agent. The type is fully specified on the
//     wire and consumed by the SPA (src/store/chat.ts, src/lib/ws.ts) but is
//     never produced by the backend, so there is no real emission site to
//     classify BY EVIDENCE. Do not assume a class for this type until a
//     producer exists and is audited.
//
// TokenFrame/DoneFrame (wsStreamer.Update/Finalize, below) need no stamping
// change: their shared "shadow stream" gate (isShadowStream forced true
// whenever parentSpawnCallID != "", in both Update and Finalize) means these
// two frames are constructed ONLY when parentSpawnCallID == "" — i.e. only
// for a turn that IS the routing session by definition (turn.go:406,
// session.RoutingSessionID's own contract: "for a root turn, RoutingSessionID
// MUST equal that turn's own SessionID"). SessionId already equals the
// routing key in every case either frame is actually emitted, so
// ProducingSessionId is correctly always absent.
//
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

	// matchesEvent extends matchesChatID with a session-based fallback so a
	// live event reaches a connection that reattached to the same PERSISTED
	// session after a reload, even though the event's own ChatID still names
	// a now-stale, pre-reload connection.
	//
	// Root cause this closes (reload/replay "never self-updates" bug, live
	// UAT re-verification 2026-07): ServeHTTP mints a brand-new chatID
	// ("webchat:" + uuid.New()) for EVERY WebSocket connection, including a
	// browser reload — there is no client-supplied continuity. A turn's own
	// ChatID (turnState.chatID, threaded onto every event payload below) is
	// stamped ONCE at turn-dispatch time from whichever connection sent the
	// message, and never changes even if that connection later closes. A
	// background delegate (Critical:true, 7dd9e7a5) can legitimately keep
	// running long after its ORIGINATING connection is gone — matchesChatID
	// alone can then NEVER match its live completion event against a NEW
	// connection that reattached via attach_session, because rule 1 compares
	// against the stale chatID, and the taskChatIDs alias (rule 2) maps this
	// connection's chatID to the session_id, not to that stale chatID. The
	// browser was stuck showing whatever replay served at attach time until
	// a SECOND reload happened to catch the by-then-corrected transcript.
	//
	// evtSessionID/currentSessionID compares by the durable session_id
	// instead: h.sessionIDs[chatID] is the session THIS connection currently
	// has open (set by handleAttachSession/handleChatMessage), and
	// evtSessionID is threaded end-to-end on every relevant event payload
	// (SubTurnSpawnPayload, SubTurnEndPayload, ToolExec*Payload,
	// TurnEndPayload). session_id survives a reload; chatID does not.
	matchesEvent := func(evtChatID, evtSessionID string) bool {
		if matchesChatID(evtChatID) {
			return true
		}
		if evtSessionID == "" {
			return false
		}
		h.mu.Lock()
		currentSessionID := h.sessionIDs[chatID]
		h.mu.Unlock()
		return currentSessionID != "" && evtSessionID == currentSessionID
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

	// rootTurnEnded latches whether the root turn for this connection has
	// already ended, and with what watchdog reason (#605). The root TurnEnd
	// and a delegate's SubTurnSpawn are emitted from DIFFERENT goroutines
	// (the parent turn's vs the detached async-delegate's), so a spawn event
	// can legally reach this forwarder AFTER the root turn_end. The
	// EventKindTurnEnd case below only arms spans already registered in
	// openSpans — without this latch, such a late-registered span would
	// never be armed and would stay invisible to the orphan watchdog
	// forever. EventKindSubTurnSpawn consults the latch to arm late
	// registrations immediately; a NEW root turn's TurnStart resets it so
	// spans of a live root turn are not spuriously armed.
	// Single-goroutine state like openSpans — no mutex needed.
	rootTurnEnded := false
	rootTurnEndReason := ""

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
		// Snapshot BOTH test-shrinkable knobs ONCE, synchronously, before
		// spawning the goroutine below — never re-read the package-level vars
		// from inside it. This goroutine loops (reschedule on "still active")
		// for however long a genuine delegate keeps running, re-arming
		// time.After(orphanWatchdogTimeout) and re-checking the reschedule
		// count against orphanWatchdogMaxRechecks on every iteration —
		// potentially for the lifetime of a long test. A test that shrinks
		// these vars via
		// SetOrphanWatchdogTimeoutForTest/SetOrphanWatchdogMaxRechecksForTest
		// and restores them (defer/t.Cleanup) the moment its OWN foreground
		// assertions pass has no happens-before edge to this still-running
		// goroutine's later reads — a genuine data race (WARNING: DATA RACE,
		// websocket.go:3229 vs export_test.go:29, caught under
		// `go test -race`, TestOrphanWatchdog_GenuinelyActiveDelegate_
		// NeverSynthesizesInterrupted), not a flake. Capturing both up front
		// removes every later read of the package vars from this goroutine;
		// production behavior is unchanged since neither var is ever mutated
		// outside tests.
		watchdogTimeout := orphanWatchdogTimeout
		maxRechecks := orphanWatchdogMaxRechecks
		go func() {
			rechecks := 0
			for {
				select {
				case <-entry.closeCh:
					// Span resolved normally — nothing to do.
					return
				case <-wc.doneCh:
					// Connection closed while waiting — exit cleanly without emitting.
					return
				case <-time.After(watchdogTimeout):
					// Span is still open after timeout. Before declaring it
					// orphaned, confirm the real sub-turn genuinely isn't
					// still running.
					//
					// Root cause this closes (transient false "interrupted"
					// status flicker, live UAT re-verification 2026-07): this
					// watchdog arms the instant the PARENT turn ends
					// (EventKindTurnEnd, IsRoot), which — for a background
					// delegate — routinely happens within a second or two of
					// dispatch. Before 7dd9e7a5 ("background delegate's
					// final answer lost when parent finishes first"), a
					// delegate needing more than one LLM turn silently exited
					// its own loop early the instant its parent ended, so
					// the real EventKindSubTurnEnd almost always arrived
					// (closing this span via closeCh) well inside
					// orphanWatchdogTimeout. Critical:true now lets it run
					// for its full, genuine duration — so a normal,
					// still-working delegation can legitimately still be
					// open when this timer fires, and synthesizing
					// status:"interrupted" here fabricated a false terminal
					// state for a turn that was, in truth, still generating
					// (self-correcting only once the real EventKindSubTurnEnd
					// arrived later and overwrote it). Re-checking real
					// liveness via agent.AgentLoop.IsSubTurnActiveForSpawnCall
					// and rescheduling instead of firing turns this
					// heuristic, timeout-only guess into a confirm-or-wait
					// check — a genuinely orphaned span (the real check
					// below returns false) is still reported exactly as
					// before.
					stillActive := h.agentLoop != nil && h.agentLoop.IsSubTurnActiveForSpawnCall(entry.parentCallID)
					forceCeiling := false
					if stillActive {
						rechecks++
						if rechecks > maxRechecks {
							// Ceiling exceeded: a genuinely wedged/deadlocked
							// turn — a goroutine that neither returns nor
							// panics, e.g. blocked on a tool call not
							// honoring context cancellation — would
							// otherwise keep IsSubTurnActiveForSpawnCall
							// reporting "active" forever, and this loop
							// would reschedule indefinitely, never emitting
							// a terminal frame for the span. Fail closed:
							// force the synthetic interrupted frame below
							// regardless of what the liveness check
							// reports, matching this codebase's established
							// fail-closed posture elsewhere in delegation
							// gating.
							forceCeiling = true
							slog.Error("ws: subagent span still reports active past the watchdog's reschedule "+
								"ceiling — force-emitting interrupted (fail-closed)",
								"event", "span_orphan_ceiling_exceeded",
								"span_id", entry.spanID,
								"parent_call_id", entry.parentCallID,
								"reason", reason,
								"rechecks", rechecks,
								"max_rechecks", maxRechecks,
							)
						} else {
							// Escalate Debug -> Warn once the loop has
							// re-checked more than once or twice, so a
							// genuinely stuck span (heading toward the
							// ceiling above) is discoverable by an operator
							// without changing production log levels —
							// Debug alone is invisible at typical
							// production log levels.
							logFn := slog.Debug
							if rechecks > 2 {
								logFn = slog.Warn
							}
							logFn("ws: subagent span still genuinely active past watchdog timeout — rescheduling",
								"event", "span_orphan_recheck_still_alive",
								"span_id", entry.spanID,
								"parent_call_id", entry.parentCallID,
								"reason", reason,
								"rechecks", rechecks,
								"max_rechecks", maxRechecks,
							)
							continue
						}
					}
					// Span is still open after timeout AND either the real
					// sub-turn is confirmed no longer active, or the
					// reschedule ceiling was exceeded (forceCeiling, already
					// logged at Error level above). Emit interrupted.
					switch {
					case forceCeiling:
						// Already logged above; avoid a second, redundant log line.
					case reason == "unknown":
						slog.Error("ws: subagent span orphaned with unknown reason — synthesizing interrupted end",
							"event", "span_orphan_interrupted",
							"span_id", entry.spanID,
							"parent_call_id", entry.parentCallID,
							"reason", reason,
						)
					default:
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
					return
				}
			}
		}()
	}

	for evt := range sub.C {
		switch evt.Kind {
		case agent.EventKindTurnStart:
			// #605: a NEW root turn began on this chat — reset the
			// root-turn-ended latch so spans it spawns are registered
			// unarmed (their root is alive; the TurnEnd case will arm them).
			// Only a ROOT turn's start may reset: a child's own turn-start
			// arrives between its SubTurnSpawn and the next root turn, and
			// resetting on it would reopen the arming hole for a sibling
			// delegate's later-arriving spawn event.
			p, ok := evt.Payload.(agent.TurnStartPayload)
			if !ok || !p.IsRoot || !matchesEvent(p.ChatID, "") {
				continue
			}
			rootTurnEnded = false
			rootTurnEndReason = ""

		case agent.EventKindSubTurnSpawn:
			// FR-H-004: emit subagent_start when a sub-turn is spawned.
			p, ok := evt.Payload.(agent.SubTurnSpawnPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
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
			// #605: if the root turn already ended, the EventKindTurnEnd case
			// has already run its arming loop and will never see this entry —
			// arm it now, or the span stays invisible to the orphan watchdog
			// forever (no reschedule ceiling, no forced interrupted frame).
			if rootTurnEnded {
				entry.parentTurnEnded = true
				startOrphanWatchdog(entry, rootTurnEndReason)
			}

		case agent.EventKindSubTurnEnd:
			// FR-H-004: emit subagent_end when a sub-turn finishes.
			p, ok := evt.Payload.(agent.SubTurnEndPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
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
			// FIX 4 (7-reviewer-gate follow-up): surface SubTurnEndPayload.Reason
			// (populated by spawnSubTurn's cleanup defer, pkg/agent/subturn.go,
			// only when Status == "interrupted") as the wire contract's
			// SubagentEndFrame.reason. The frontend (SubagentBlock.tsx) already
			// renders this — it just never received a value before this fix.
			if p.Reason != "" {
				reason := p.Reason
				endFrameEnd.Reason = &reason
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
			if !ok || !p.IsRoot || !matchesEvent(p.ChatID, p.SessionID) {
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
			case agent.TurnEndStatusParked:
				// Behavior-preserving: this previously fell through the
				// `default` branch below to "unknown" (Parked was not a
				// distinct case). Kept identical here rather than guessing
				// a more specific wire value without frontend confirmation
				// of what consumes it.
				watchdogReason = "unknown"
			default:
				watchdogReason = "unknown"
			}
			// #605: latch the root-turn-ended state for spans whose
			// SubTurnSpawn arrives after this event (see rootTurnEnded decl).
			rootTurnEnded = true
			rootTurnEndReason = watchdogReason
			for _, entry := range openSpans {
				if !entry.parentTurnEnded {
					entry.parentTurnEnded = true
					startOrphanWatchdog(entry, watchdogReason)
				}
			}

		case agent.EventKindToolExecStart:
			p, ok := evt.Payload.(agent.ToolExecStartPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
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
			// ADR-057 FR-012/FR-013 (W5b): tool_call_start is class (a) — a
			// genuinely child-turn-produced frame (BDD-16; generated.
			// ToolCallStartFrame's own doc comment). startSID above already
			// carries the routing key per ToolExecStartPayload.SessionID's
			// contract (events.go, U3/U9); ProducingSessionID is the emitting
			// turn's own real session, left zero-valued by the emitter when it
			// equals the routing key. Stamp the wire's optional
			// producing_session_id only when it is non-empty AND differs from
			// what was actually placed in SessionId — never "≥ 1" but the
			// FR-013 "present iff it differs" rule, checked against startSID
			// rather than raw p.SessionID so the sessionIDForChat fallback
			// above can never manufacture a false "differs".
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != startSID {
				startF.ProducingSessionId = &producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallStart), startF)
		case agent.EventKindToolExecEnd:
			p, ok := evt.Payload.(agent.ToolExecEndPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
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
			// Structured tool failure (UAT fix, extended by ADR-059 W5): some
			// tools emit a typed JSON object as their result rather than prose
			// — a denied delegation (DelegationFailure) or a write_file
			// precondition refusal (FileExistsRefusal). Parse it into a real
			// object so the SPA receives the typed shape it can match on, and
			// lift the human-readable reason into the frame's error field so
			// renderers that show only `error` still show a sentence rather
			// than a JSON blob.
			var structuredErr string
			if status == "error" {
				if obj, reason, isStructured := parseStructuredToolFailure(p.Result); isStructured {
					liveResult = obj
					structuredErr = reason
				}
			}
			if liveResult == any(p.Result) && len(p.Result) > InlineToolResultMaxBytes {
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
			// Live/replay error parity (fixes the inverted-parity gap left by
			// RC-5c in pkg/gateway/replay.go's buildResult): that replay
			// reconstruction sets ToolCallResultFrame.Error from tc.Error for
			// EVERY persisted failure (session.ToolCall.Error, populated in
			// pkg/agent/loop.go's runTurn whenever toolResult.IsError and no
			// richer Result was already attached — see tcRecord.Error's own
			// RC-5 comment there), not just delegation denials. Before this,
			// the live path here populated .Error ONLY via the
			// parseStructuredToolFailure special case above, so a failed bash/
			// write_file/etc. call showed NO error live but DID show one
			// after a page reload — the exact opposite of parity. p.Result is
			// ToolExecEndPayload.Result, which loop.go sets to the very same
			// contentForLLM string tcRecord.Error is derived from (pre the
			// persisted side's truncation) — so it is the same string the
			// transcript records.
			//
			// It MUST be truncated to the same bound the persisted side uses,
			// for two independent reasons:
			//
			//  1. SIZE. resultF.Result is subject to the
			//     InlineToolResultMaxBytes offload a few lines above: a result
			//     over 50 KiB is written to disk and replaced with a small
			//     ToolResultRef sentinel, because a multi-megabyte frame can
			//     OOM a constrained client (see maybeOffloadResult). Assigning
			//     the raw p.Result to Error would put the entire string back
			//     into the very same frame, defeating that guard — and the
			//     error path is where large payloads are MOST likely (stderr
			//     dumps, stack traces, build logs).
			//
			//  2. PARITY, which is the whole point of this branch. The
			//     persisted side caps at maxFailClosedOutputChars, so an
			//     untruncated live value means a long error renders one way
			//     live and a different way after a reload — the same class of
			//     divergence this change set out to remove.
			switch {
			case structuredErr != "":
				// Truncated for the same two reasons the comment above states
				// as MUST, and which the branch below already honours: frame
				// size, and parity with the persisted side's own 2000-rune
				// cap. This branch was the one place that skipped it.
				se := truncateRunesForFrame(structuredErr, maxLiveErrorChars)
				resultF.Error = &se
			case status == "error" && p.Result != "" && liveResult != any(p.Result):
				// Only when Result no longer carries the text itself.
				//
				// `liveResult != any(p.Result)` is true exactly when Result was
				// REPLACED above — either offloaded to disk as a ToolResultRef
				// sentinel (over InlineToolResultMaxBytes) or parsed into a
				// structured object. In those cases the frame would otherwise
				// reach the client with no readable reason at all, so Error is
				// the only thing carrying it.
				//
				// When Result IS still the plain string, setting Error would
				// ship the identical text twice in one frame. That is pure
				// duplication: the SPA already has the reason in Result.
				//
				// Replay is unaffected and still sets Error unconditionally
				// from the persisted record — it must, because on the persisted
				// side Result is nil for ordinary tool failures (only media and
				// synchronous delegate calls populate it), so Error is the ONLY
				// carrier there. That asymmetry is deliberate: each path sets
				// Error precisely when its own Result cannot carry the reason.
				liveErr := truncateRunesForFrame(p.Result, maxLiveErrorChars)
				resultF.Error = &liveErr
			}
			// ADR-057 FR-012/FR-013 (W5b): tool_call_result is class (a) —
			// genuinely child-turn-produced (BDD-16; generated.
			// ToolCallResultFrame's own doc comment). See the tool_call_start
			// stamping above for the identical "present iff it differs from
			// what's actually on the wire" contract.
			var producingSIDForResult string
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != evtSID {
				resultF.ProducingSessionId = &producingSID
				producingSIDForResult = producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolCallResult), resultF)
			// When the handoff tool succeeds, notify the frontend to switch agents.
			// Use evtSID (the session ID from the payload) to key the lookup, not chatID.
			//
			// ADR-057 FR-089 (W5 audit): agent_switched is class (a), not
			// "class not yet assigned" (generated.AgentSwitchedFrame's doc
			// comment pre-audit) — it is derived from THIS SAME
			// ToolExecEndPayload, at the exact call site whose tool_call_result
			// sibling is already verified class (a): a delegated child can
			// invoke hand_off/return_to_default on its OWN session exactly as a
			// root turn can, so evtSID here is the CHILD's own producing
			// session whenever the hand_off ran inside a sub-turn, distinct
			// from the routing key placed in SessionId below. Reuses
			// producingSIDForResult computed above rather than re-deriving it,
			// since both frames answer the identical "does this ToolExecEnd's
			// producer differ from its routing key" question.
			if p.Tool == "hand_off" && status == "success" {
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
					if producingSIDForResult != "" {
						pid := producingSIDForResult
						switchF.ProducingSessionId = &pid
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
				if producingSIDForResult != "" {
					pid := producingSIDForResult
					switchF.ProducingSessionId = &pid
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
			// Prefer the routing session stamped on the payload so a
			// second tab / reload attached to the same session still
			// sees the denial. ChatID alone is a dead webchat: uuid
			// after ServeHTTP mints a new connection.
			rateSID := p.SessionID
			if rateSID == "" {
				rateSID = sessionIDForChat(p.ChatID)
			}
			if p.Scope != "global" && !matchesEvent(p.ChatID, rateSID) {
				continue
			}
			// Use generated.RateLimitFrame (contract-first migration).
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
		case agent.EventKindError:
			// ADR-051 §RD6: forward translated provider/LLM errors to the
			// browser so the chat UI can render the typed ErrorFrame inline
			// (Code/Retryable/Detail) instead of the raw provider text.
			//
			// NO CODE IS SUPPRESSED HERE — and `rate_limited` least of all.
			// This arm used to end with an unconditional
			// `if code == agent.CodeRateLimited { continue }`, justified as
			// "the dedicated RateLimitFrame above is authoritative for that
			// class". That justification was false, and it cost a user their
			// only signal: an upstream HTTP 429 produced a turn that opened,
			// said nothing, and closed reporting success.
			//
			// The two mechanisms share a code NAME but not a producer:
			//
			//   - EventKindRateLimit (the arm above) has EXACTLY ONE producer,
			//     AgentLoop.recordRateLimitDenial (pkg/agent/loop.go), called
			//     from two sites both guarded on Omnipus's OWN internal SEC-26
			//     limiter being configured (cfg.Sandbox.RateLimits.MaxAgent{
			//     LLMCallsPerHour,ToolCallsPerMinute} > 0). It means "Omnipus
			//     denied this".
			//   - An UPSTREAM refusal never reaches that function at all. It
			//     travels runTurn's LLM-error block, which emits EventKindError
			//     with Code: "rate_limited". It means "the provider denied
			//     this" — a different fact with a different remedy (wait /
			//     retry / switch model, not raise your own cap).
			//
			// So the suppression could never de-duplicate anything: it only
			// ever deleted the provider case, with nothing replacing it.
			//
			// Nor is a dual-emit lurking behind it. recordRateLimitDenial emits
			// ONE event, and its doc comment records that the prior
			// "EventKindError + RateLimitPayload + EventKindRateLimit"
			// dual-emit was deliberately removed as bus pollution — that
			// removal was correct and stays. Even reinstated in its old shape
			// it could not reach this frame: it carried a RateLimitPayload,
			// which the ErrorPayload type assertion immediately below already
			// rejects. Dedup belongs at the producer (one event per denial),
			// not here — pinned end-to-end by
			// TestEventForwarder_InternalRateLimitDenial_EmitsExactlyOneFrame.
			p, ok := evt.Payload.(agent.ErrorPayload)
			if !ok {
				continue
			}
			// Prefer the routing session stamped on the payload (survives
			// reload: the originating chatID is a dead webchat: uuid).
			// Fall back to the live chatID→session map for older emitters
			// that only set ChatID.
			errSID := p.SessionID
			if errSID == "" {
				errSID = sessionIDForChat(p.ChatID)
			}
			if !matchesEvent(p.ChatID, errSID) {
				continue
			}
			// FIX 2: prefer the already-computed p.Code/p.Message over a
			// fresh TranslateLLMError call. Every ErrorPayload construction
			// site now populates Code (pkg/agent's FIX 3) alongside a
			// Message that is EITHER the classifier's own generic copy OR —
			// for trusted internal stages (hook aborts, model-switch
			// failures, session save/restore, synthetic-error-floor,
			// external-CLI sanitized text) — caller-curated text that must
			// reach the wire verbatim. This mirrors appendErrorTranscript's
			// write-choke-point behavior (pkg/agent/turn.go): re-running
			// TranslateLLMError against already-curated text here would
			// re-classify it against the generic message catalog and
			// silently replace the curated copy with boilerplate whenever
			// the text happened to contain a pinned substring (e.g. a hook
			// abort reason mentioning "safety") — exactly the live-vs-replay
			// divergence this closes. Only fall back to a fresh translation
			// when a call site left Code empty (defensive — after FIX 3
			// every production site sets it).
			translated := agent.TranslateLLMError(p.ProviderError, p.Message)
			code := translated.Code
			message := translated.Message
			retryable := translated.Retryable
			detail := translated.Detail
			if p.Code != "" {
				code = agent.LLMErrorCode(p.Code)
				message = p.Message
				retryable = agent.IsRetryableCode(code)
				// FIX 2 (re-review): detail must follow the same
				// curated-preferred rule as code/message/retryable above,
				// not silently stay pinned to the fresh-classification
				// value computed a few lines up. Recomputing from
				// (p.ProviderError, message) — message is already the
				// curated p.Message reassigned just above — is a no-op
				// TODAY (every curated site passes ProviderError: nil, so
				// agent.BuildDetail(nil, msg) echoes msg exactly like
				// translated.Detail already does), but stops being one the
				// day a curated site pairs a curated Code+Message with a
				// non-nil ProviderError: buildDetail favors pe.Status/
				// pe.Body over the message argument once pe != nil, so
				// leaving this pinned to `translated.Detail` would render a
				// diagnostic string that was never validated against the
				// curated Code/Message this frame actually carries.
				detail = agent.BuildDetail(p.ProviderError, message)
			}
			errF := generated.ErrorFrame{
				Type:      string(generated.WsFrameTypeError),
				SessionId: &errSID,
				Message:   message,
			}
			errF.Payload = &generated.ErrorPayload{
				LlmError: generated.LLMError{
					Code:      string(code),
					Message:   message,
					Retryable: retryable,
					Detail:    &detail,
				},
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeError), errF)
		case agent.EventKindWhatsAppPairing:
			// #283: WhatsApp linked-device pairing (QR + status). Not tied to a
			// chatID. Delivered only to connections that subscribed to this
			// channel's pairing UI (Option B), so the QR pairing secret isn't
			// broadcast to every connected tab.
			p, ok := evt.Payload.(agent.WhatsAppPairingPayload)
			if !ok {
				continue
			}
			pairF := generated.WhatsAppPairingFrame{
				Type:      string(generated.WsFrameTypeWhatsappPairing),
				ChannelId: p.ChannelID,
				Status:    string(p.Status),
			}
			if p.QR != "" {
				qr := p.QR
				pairF.Qr = &qr
			}
			if p.Message != "" {
				msg := p.Message
				pairF.Message = &msg
			}
			// #368: maintain the per-channel QR cache so late subscribers (e.g.
			// a tab that opens the pairing UI after the first QR fires) receive
			// the last-seen code immediately on subscribe rather than waiting for
			// the next QR rotation.  Only "code" (QR available) is cached;
			// terminal states are evicted so stale QRs are not re-emitted.
			switch p.Status {
			case channels.PairingStatusCode:
				if frameBytes, merr := json.Marshal(pairF); merr == nil {
					h.lastPairingState.Store(p.ChannelID, frameBytes)
				} else {
					slog.Error("ws: failed to marshal whatsapp_pairing frame for cache",
						"channel_id", p.ChannelID, "error", merr)
				}
			case channels.PairingStatusLinked, channels.PairingStatusTimeout, channels.PairingStatusError,
				channels.PairingStatusWaiting:
				// PairingStatusWaiting and any other status that is not
				// "code" must not leave a stale QR in the cache — evict so a
				// late subscriber is not shown an outdated code.
				h.lastPairingState.Delete(p.ChannelID)
			default:
				// Any future status not yet in this switch: same fail-safe
				// eviction as above, so an unrecognized status never leaves
				// a stale QR behind.
				h.lastPairingState.Delete(p.ChannelID)
			}
			if !wc.wantsPairing(p.ChannelID) {
				continue
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeWhatsappPairing), pairF)
		case agent.EventKindNotification:
			// #264: a user-facing notification (e.g. a scheduled run failed).
			// Delivered ONLY to the recipient user's connections (filtered by
			// wc.userID) so it never leaks to other tabs/sessions. The
			// NotificationAdminBroadcast sentinel fans out to every connected
			// client unconditionally when no specific recipient could be
			// resolved — under the single-user model, "broadcast to admins" and
			// "broadcast to the one account's connections" are the same thing.
			p, ok := evt.Payload.(agent.NotificationPayload)
			if !ok {
				continue
			}
			if p.Recipient != agent.NotificationAdminBroadcast && wc.userID != p.Recipient {
				continue
			}
			notifF := generated.NotificationFrame{
				Type:             string(generated.WsFrameTypeNotification),
				Id:               p.ID,
				NotificationType: p.NotificationType,
				Title:            p.Title,
				Severity:         p.Severity,
				Read:             p.Read,
				CreatedAtMs:      p.CreatedAtMs,
			}
			if p.Body != "" {
				body := p.Body
				notifF.Body = &body
			}
			if p.ScheduleID != "" {
				sid := p.ScheduleID
				notifF.ScheduleId = &sid
			}
			if p.SessionID != "" {
				ses := p.SessionID
				notifF.SessionId = &ses
			}
			if p.AgentID != "" {
				aid := p.AgentID
				notifF.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeNotification), notifF)
		case agent.EventKindTaskStatusChanged:
			// A workflow task's status changed (queued→running→completed/failed).
			// Not tied to a specific chatID — broadcast to every connection so
			// anyone viewing the tasks board sees live updates. The SPA
			// invalidates its tasks TanStack Query cache on receipt.
			p, ok := evt.Payload.(agent.TaskStatusChangedPayload)
			if !ok {
				continue
			}
			taskF := generated.TaskStatusChangedFrame{
				Type:      string(generated.WsFrameTypeTaskStatusChanged),
				SessionId: p.SessionID,
				TaskId:    p.TaskID,
				Status:    p.Status,
			}
			if p.AgentID != "" {
				aid := p.AgentID
				taskF.AgentId = &aid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeTaskStatusChanged), taskF)
		case agent.EventKindPlanStatusChanged:
			// ADR-049 D4/D7: a Plan's state/phase/progress/paused_reason changed.
			// Not tied to a specific chatID (a Plan is workspace-scoped, not
			// session-scoped) — broadcast to every connection, mirroring
			// EventKindTaskStatusChanged above. The SPA invalidates its plans
			// query cache / updates the plan card on receipt.
			p, ok := evt.Payload.(agent.PlanStatusChangedPayload)
			if !ok {
				continue
			}
			planF := generated.PlanStatusFrame{
				Type:      string(generated.WsFrameTypePlanStatus),
				PlanId:    p.PlanID,
				State:     p.State,
				PlanPhase: p.PlanPhase,
				Progress:  p.Progress,
			}
			if p.PausedReason != "" {
				pr := p.PausedReason
				planF.PausedReason = &pr
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypePlanStatus), planF)
		case agent.EventKindGoalStatusChanged:
			// ADR-049 D6/D7: a session's `/goal` loop status changed (set,
			// round advance, met, bound reached, cleared). Broadcast to every
			// connection, mirroring EventKindPlanStatusChanged — the SPA
			// matches session_id client-side to the currently open session.
			p, ok := evt.Payload.(agent.GoalStatusChangedPayload)
			if !ok {
				continue
			}
			goalF := generated.GoalStatusFrame{
				Type:         string(generated.WsFrameTypeGoalStatus),
				SessionId:    p.SessionID,
				Condition:    p.Condition,
				Round:        p.Round,
				MaxRounds:    p.MaxRounds,
				LatestReason: p.LatestReason,
				ActiveLoops:  p.ActiveLoops,
				Cap:          p.Cap,
				State:        p.State,
			}
			// ADR-053 R§8.11 / UAT S3 fix: goal_id disambiguates which goal
			// generation this frame updates so the SPA's GoalPillTray can key
			// one pill per goal-id instead of collapsing every goal a session
			// ever carried into the `_default` bucket. Optional on the wire —
			// omitted for a legacy pre-upgrade goal that never had one minted.
			if p.GoalID != "" {
				gid := p.GoalID
				goalF.GoalId = &gid
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeGoalStatus), goalF)
		case agent.EventKindLoopStatusChanged:
			// ADR-049 D6/D7: a session's `/loop` status changed (set, run
			// fired, run-cap reached, stop). Broadcast to every connection,
			// mirroring EventKindGoalStatusChanged above.
			p, ok := evt.Payload.(agent.LoopStatusChangedPayload)
			if !ok {
				continue
			}
			loopF := generated.LoopStatusFrame{
				Type:      string(generated.WsFrameTypeLoopStatus),
				SessionId: p.SessionID,
				Mode:      p.Mode,
				Run:       p.Run,
				MaxRuns:   p.MaxRuns,
				State:     p.State,
			}
			if p.NextDelay != nil {
				nd := int64(*p.NextDelay)
				loopF.NextDelay = &nd
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeLoopStatus), loopF)
		case agent.EventKindTaskRunStatus:
			// A per-execution run opened or closed (ADR-050). Broadcast so the
			// calendar's per-occurrence chip updates live without a full refetch.
			// occurrence_ms is nil for an ad-hoc/once/manual run.
			p, ok := evt.Payload.(agent.TaskRunStatusPayload)
			if !ok {
				continue
			}
			runF := generated.TaskRunStatusFrame{
				Type:   string(generated.WsFrameTypeTaskRunStatus),
				TaskId: p.TaskID,
				RunId:  p.RunID,
				Status: p.Status,
			}
			if p.OccurrenceMs != nil {
				ms := *p.OccurrenceMs
				runF.OccurrenceMs = &ms
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeTaskRunStatus), runF)

		case agent.EventKindToolResultProjection:
			// ADR-066 D5 / FR-022 (T066-12): a tool result this session already
			// received was emptied in place in the model's window. Push the
			// typed tool_result_projection frame so the SPA re-renders the
			// matching tool call (the mark only under Verbose chat); on reload
			// the same state arrives as ToolCall.content_state on the
			// transcript. Session-scoped: same matchesEvent / session-id
			// contract as tool_call_result (ToolExecEndPayload).
			p, ok := evt.Payload.(agent.ToolResultProjectionPayload)
			if !ok || !matchesEvent(p.ChatID, p.SessionID) {
				continue
			}
			projSID := p.SessionID
			if projSID == "" {
				projSID = sessionIDForChat(p.ChatID)
			}
			projF := generated.ToolResultProjectionFrame{
				Type:         string(generated.WsFrameTypeToolResultProjection),
				SessionId:    projSID,
				ToolCallId:   string(p.ToolCallID),
				ArchiveLine:  p.ArchiveLine,
				ContentState: p.ContentState,
			}
			if p.Mark != "" {
				mark := p.Mark
				projF.Mark = &mark
			}
			if producingSID := string(p.ProducingSessionID); producingSID != "" && producingSID != projSID {
				projF.ProducingSessionId = &producingSID
			}
			sendConnGenFrame(wc, string(generated.WsFrameTypeToolResultProjection), projF)
		case agent.EventKindLLMRequest, agent.EventKindLLMDelta, agent.EventKindLLMResponse,
			agent.EventKindLLMRetry, agent.EventKindContextCompress,
			agent.EventKindToolExecSkipped, agent.EventKindSteeringInjected, agent.EventKindFollowUpQueued,
			agent.EventKindInterruptReceived, agent.EventKindSubTurnResultDelivered, agent.EventKindSubTurnOrphan,
			agent.EventKindTurnTimeout, agent.EventKindEmptyResponseRetry, agent.EventKindCompactionRetry,
			agent.EventKindBackgroundProcessKill:
			// Not part of the live WS wire protocol — this forwarder only
			// translates the kinds handled above into browser frames.
			// Behavior-preserving: previously these fell through the switch
			// unmatched (no default case existed), which is a silent no-op
			// identical to this explicit, empty case.
		}
	}
}

// wsTranscriptWriteFailures counts how many times wsStreamer.Finalize's
// streamed-assistant-message audit write via AppendTranscriptStrict failed
// against a session id that does not resolve to a real, store-backed session
// (ADR-057 FR-001/FR-002/W3, spec BDD-03 row `pkg/gateway/websocket.go:4256`
// "streamed assistant"). Mirrors pkg/agent/turn.go's transcriptWriteFailures
// and pkg/tools/handoff.go's handoffTranscriptWriteFailures — each unit that
// owns a converted call site gets its own package-local counter rather than
// sharing one across package boundaries. The write itself stays best-effort
// by design (a failed streamed-transcript record must never fail the turn
// that already streamed successfully to the client); this counter is the
// only durable, operator-visible signal that it happened. Exposed via
// WSTranscriptWriteFailures() for tests and operator tooling.
var wsTranscriptWriteFailures atomic.Uint64

// WSTranscriptWriteFailures returns the current value of the
// omnipus_ws_transcript_write_failures_total counter (ADR-057 FR-002).
func WSTranscriptWriteFailures() uint64 {
	return wsTranscriptWriteFailures.Load()
}

// wsStreamer implements bus.Streamer, pushing token/done frames into a wsConn's send channel.
// It also accumulates the full response to persist it to the session transcript on Finalize.
type wsStreamer struct {
	conn       *wsConn
	chatID     string
	sessionID  string                // for recording assistant message
	agentStore *session.UnifiedStore // for recording assistant message
	// agentID identifies the producer this streamer's frames/transcript entry
	// are attributed to. Initialized at streamer-creation time (GetStreamer) to
	// the session's "active" agent — a reasonable default for the common case
	// where the visibly-active chat agent is also the one producing this
	// response. The agent loop overrides it via SetProducerAgentID with the
	// TRUE per-turn producer (ts.agent.ID) immediately after obtaining the
	// streamer for an LLM streaming call (FIX 5a) — required for
	// background/delegated sub-turns, where the delegate's identity (per
	// ADR-032, no inheritance from the parent) differs from the session's
	// "active" (parent) agent. Guarded by statsMu since Update/Finalize may
	// read it from a different goroutine than SetProducerAgentID writes it.
	agentID string
	// turnID identifies the turn that produced this streamer's transcript
	// entry (FIX 5c/1). Stamped by the agent loop via SetTurnID immediately
	// after obtaining the streamer, mirroring SetProducerAgentID's pattern
	// exactly. Without this, the entry Finalize writes carries no TurnID at
	// all — the confirmed cause of two real bugs: (1) the frontend's
	// turn_canceled -> assistant-message replay correlation can never match
	// a real entry (chatTurnCanceledNoMatch fires on every reload after a
	// mid-stream cancel), and (2) MarkLastEntryTruncated's own turn-scoped
	// backward-walk (pkg/session/unified.go, requires e.TurnID == turnID)
	// can never match a real entry either, silently disabling the Truncated
	// flag for every real cancel. Guarded by statsMu like agentID.
	turnID string
	// parentSpawnCallID identifies the spawning "delegate"/"spawn" ToolCall.ID
	// in the PARENT turn when this streamer belongs to a CHILD delegation
	// sub-turn (empty for a root/non-delegated turn). Stamped by the agent
	// loop via SetParentSpawnCallID, mirroring SetTurnID's pattern exactly.
	//
	// [FIX-5, Defect 5, 2026-08-03] Carried onto the transcript entry
	// Finalize writes for per-spawn-call attribution WITHIN the child's OWN
	// durable transcript (read by pkg/tools/delegate.go's recentActivityLines,
	// ADR-057 FR-043, to filter a `delegate` status poll to only THAT
	// delegate call's activity) — NOT, as an earlier revision of this
	// comment claimed, so pkg/gateway/replay.go could withhold the entry
	// from top-level replay. That replay-side skip was DELETED by ADR-057
	// FR-034/FR-038 (a delegated child now owns its own store-backed session
	// and never lands in the parent's transcript at all, so there is
	// nothing left for replay.go to withhold) — see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment
	// (pkg/session/daypartition.go) for the authoritative, corrected
	// post-ADR-057 role of this field. Guarded by statsMu like turnID/agentID.
	parentSpawnCallID string
	channel           *webchatChannel // to mark streaming complete and suppress duplicate Send()
	accumulated       strings.Builder // accumulates full response text

	// producedModel is the model string that produced this streamed response.
	// Set by the agent loop via SetProducedModel before Finalize so the
	// transcript entry written by Finalize carries the per-turn Model field
	// (FR-013). Empty when the agent loop didn't push a value (legacy
	// callers) — the UI omits the model span entirely for those (FR-014);
	// there is no "(model not recorded)" placeholder rendered.
	producedModel string

	// Turn-level stats set by the agent loop via SetTurnStats before Finalize.
	// Populates the "done" frame so the chat UI shows real token counts and
	// cost instead of zeros (issue #12). Mutex-protected because SetTurnStats
	// and Finalize may be called from different goroutines.
	statsMu sync.Mutex
	// statsPromptTokens/statsCompletionTokens/statsCacheRead/statsCacheWrite
	// carry the provider's token split so Finalize can stamp it onto the
	// TranscriptEntry it writes. Guarded by statsMu like the fields below.
	statsPromptTokens     int
	statsCompletionTokens int
	statsCacheRead        int
	statsCacheWrite       int
	statsTokens           int64
	statsCostUSD          float64
	statsDuration         time.Duration
	statsTurnFailed       bool // set by SetTurnFailed when the engine used a synthetic fallback

	// transcriptPersisted records that the agent loop already wrote this
	// streamer's narration to the transcript via
	// appendIntermediateAssistantTranscript (#416). When set, Finalize must NOT
	// append the accumulated content again — it would create a duplicate
	// assistant entry on replay when the turn exits via max_tool_iterations
	// exhaustion (the last executed round is a tool-call round whose streamer is
	// the lastStreamer that gets finalized). Guarded by statsMu, which Finalize
	// already holds while reading stats.
	transcriptPersisted bool

	// shadowResolved/isShadowStream implement the concurrent-stream
	// interleaving fix — see WSHandler.streamOwners' doc comment for the
	// full root-cause writeup. isShadowStream is resolved once, lazily, on
	// this streamer's first Update() call (via claimStreamOwnership) and
	// reused for the rest of this instance's lifetime: a wsStreamer's
	// Update() calls all run on the single goroutine driving its ChatStream
	// callback, so no extra synchronization beyond the existing statsMu
	// (already held for every Update/Finalize access to these fields) is
	// needed. true means a DIFFERENT, still-live turnID already owns live
	// delivery for this chatID — this streamer still accumulates every
	// token (see `accumulated` above) but withholds the live TokenFrame send.
	shadowResolved bool
	isShadowStream bool
}

// streamOwnerClaim is the value stored in WSHandler.streamOwners: which turn
// holds a chatID's live-stream slot, and when it claimed it. claimedAt backs
// claimStreamOwnership's stale-claim force-reclaim safety net.
type streamOwnerClaim struct {
	turnID    string
	claimedAt time.Time
}

// streamOwnershipStaleAfter bounds how long an unreleased live-stream
// ownership claim is honored before a new claimant on the same chatID may
// force-reclaim it. Deliberately generous — every real release path
// (Finalize, Cancel, the abandoned-turn early return) frees the claim
// immediately, well within this window — this exists purely as a backstop
// against a future bug in this family leaving a claim permanently
// unreleased, so such a leak degrades to "briefly wrong attribution" rather
// than "permanently mute chat" (see WSHandler.streamOwners' doc comment).
const streamOwnershipStaleAfter = 10 * time.Minute

// claimStreamOwnership attempts to claim (or re-confirm) chatID's live
// TokenFrame-delivery slot in owners for turnID. See WSHandler.streamOwners'
// doc comment for the full rationale. Returns true when turnID owns the slot
// — either because it just claimed an empty slot, because it already owned
// it (a single turn typically opens several sequential wsStreamer instances
// across its own tool-calling iterations — see turnState.lastStreamer/
// finalizeStreamer in pkg/agent/turn.go — and each must see itself as "still
// the owner", not a foreign claimant), or because the existing claim is
// older than streamOwnershipStaleAfter and was force-reclaimed. Returns
// false only when a DIFFERENT, still-fresh turnID already owns the slot.
func claimStreamOwnership(owners *sync.Map, chatID, turnID string) bool {
	now := time.Now()
	newClaim := streamOwnerClaim{turnID: turnID, claimedAt: now}
	actual, loaded := owners.LoadOrStore(chatID, newClaim)
	for {
		if !loaded {
			return true
		}
		claim, ok := actual.(streamOwnerClaim)
		if !ok || claim.turnID == turnID {
			return true
		}
		if now.Sub(claim.claimedAt) < streamOwnershipStaleAfter {
			return false
		}
		// The existing claim is stale — force-reclaim it. CompareAndSwap only
		// succeeds if the entry is still exactly what we last observed, so a
		// concurrent claimant racing us here safely retries instead of both
		// believing they own the slot.
		if owners.CompareAndSwap(chatID, actual, newClaim) {
			return true
		}
		actual, loaded = owners.Load(chatID)
	}
}

// releaseStreamOwnershipClaim releases turnID's live-stream ownership claim
// for chatID in owners, if it currently holds it. A no-op when turnID never
// held the claim (e.g. a shadow stream, or a turn that never called
// Update()) or when it has already been released or force-reclaimed by a
// stale-claim takeover. Load-then-CompareAndDelete rather than a bare delete
// so a concurrent stale-claim reclaim racing this release can never clobber
// a different, newer claimant's entry.
func releaseStreamOwnershipClaim(owners *sync.Map, chatID, turnID string) {
	if turnID == "" {
		return
	}
	actual, ok := owners.Load(chatID)
	if !ok {
		return
	}
	claim, ok := actual.(streamOwnerClaim)
	if !ok || claim.turnID != turnID {
		return
	}
	owners.CompareAndDelete(chatID, actual)
}

// SetProducedModel stamps the model string that produced this streamed
// response. Called by the agent loop before Finalize so the assistant
// transcript entry carries the per-turn Model field (FR-013). Empty model
// is treated as "not recorded" by the UI; the UI omits the model span
// entirely per FR-014 (no placeholder).
func (s *wsStreamer) SetProducedModel(model string) {
	s.statsMu.Lock()
	s.producedModel = strings.TrimSpace(model)
	s.statsMu.Unlock()
}

// SetProducerAgentID overrides the streamer's attributed agent with the TRUE
// per-turn producer (ts.agent.ID). Called by the agent loop (via the inline
// SetProducerAgentID interface, mirroring the SetProducedModel/
// SuppressTranscriptWrite pattern) immediately after obtaining the streamer
// for an LLM streaming call, before any Update/Finalize can observe it.
//
// FIX 5a: without this, both the live TokenFrame.AgentId (Update) and the
// persisted transcript entry (Finalize) fall back to the "active session
// agent" guess computed at GetStreamer time — correct for an ordinary turn,
// but wrong for a background/delegated sub-turn, where ADR-032 guarantees
// the delegate runs as its own identity, never the parent's. A no-op on an
// empty agentID so a caller that genuinely has no resolved agent (should not
// happen in practice) cannot blank out the GetStreamer-time guess.
func (s *wsStreamer) SetProducerAgentID(agentID string) {
	if agentID == "" {
		return
	}
	s.statsMu.Lock()
	s.agentID = agentID
	s.statsMu.Unlock()
}

// SetTurnID stamps the turn ID that will be attributed to the transcript
// entry Finalize writes. Called by the agent loop (via the inline SetTurnID
// interface, mirroring SetProducerAgentID exactly) immediately after
// obtaining the streamer for an LLM streaming call, before any Update/
// Finalize can observe it.
//
// FIX 5c/1: without this, the assistant entry Finalize writes carries no
// TurnID, so a mid-stream cancel's turn_canceled entry (which DOES carry
// TurnID — see pkg/agent/cancel.go) can never be correlated with the
// assistant message it interrupted on replay, and
// MarkLastEntryTruncated's own turn-scoped matching can never find the
// entry to flag. A no-op on an empty turnID so a caller with no resolved
// turn ID (should not happen in practice) cannot blank out an
// already-stamped value.
func (s *wsStreamer) SetTurnID(turnID string) {
	if turnID == "" {
		return
	}
	s.statsMu.Lock()
	s.turnID = turnID
	s.statsMu.Unlock()
}

// SetParentSpawnCallID stamps the delegation-nesting correlation that will be
// attributed to the transcript entry Finalize writes. Called by the agent
// loop (via the inline SetParentSpawnCallID interface, mirroring SetTurnID
// exactly) immediately after obtaining the streamer for an LLM streaming
// call, before any Update/Finalize can observe it.
//
// Unlike SetTurnID/SetProducerAgentID, an EMPTY parentSpawnCallID is a valid,
// common value — it means "this is a root turn, not a delegation child" —
// so, unlike those two setters, this one does NOT no-op on empty; it always
// stamps whatever the caller passes (including clearing back to "" for a
// caller that resolves no parent span, which should never blank out a
// previously-stamped value in practice since each wsStreamer instance is
// single-use for one turn, but matches the field's own zero-value semantics
// rather than silently refusing a legitimate "no parent" write).
func (s *wsStreamer) SetParentSpawnCallID(parentSpawnCallID string) {
	s.statsMu.Lock()
	s.parentSpawnCallID = parentSpawnCallID
	s.statsMu.Unlock()
}

// SuppressTranscriptWrite marks this streamer so its Finalize skips the
// transcript-append block. The agent loop calls this (via the inline
// SuppressTranscriptWrite interface) after it has already persisted the round's
// narration through appendIntermediateAssistantTranscript (#416 gate fix).
func (s *wsStreamer) SuppressTranscriptWrite() {
	s.statsMu.Lock()
	s.transcriptPersisted = true
	s.statsMu.Unlock()
}

// StreamedContentLen reports how many bytes of streamed content this streamer
// has already emitted to the client for the current attempt. The agent loop's
// inline-retry guard uses this to avoid re-streaming a full response onto a
// partially-streamed bubble after a mid-stream transport drop (which would
// visibly duplicate text in the SPA, since the dropped attempt sent no `done`
// frame). Guarded by statsMu — the same mutex Update holds when it appends to
// accumulated — so the read is race-free across goroutines.
func (s *wsStreamer) StreamedContentLen() int {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.accumulated.Len()
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

// SetTurnIOStats receives the provider's input/output and cache token split
// from the agent loop's finalizeStreamer (streamerIOStatsSetter).
//
// SetTurnStats above carries only a collapsed total, which is why a streamed
// turn used to persist an entry with no split at all — leaving tokens_in at 0
// for every webchat session.
func (s *wsStreamer) SetTurnIOStats(promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsPromptTokens = promptTokens
	s.statsCompletionTokens = completionTokens
	s.statsCacheRead = cacheReadTokens
	s.statsCacheWrite = cacheWriteTokens
}

// SetTurnFailed is called by the agent loop's finalizeStreamer when the turn
// ended via the engine's error/limit fallback rather than a real model response.
// Conditions that set the flag: (1) LLM returned empty after retries and the
// engine substituted its defaultResponse sentinel; (2) tool-iteration limit
// reached; (3) generic empty-content exhaustion resolved to the defaultResponse
// sentinel (excludes caller-supplied success strings like the heartbeat path).
// Implements the streamerFailedSetter interface from pkg/agent. The flag is
// emitted in the done frame as DoneStats.TurnFailed so CLI/automation clients
// can exit non-zero.
func (s *wsStreamer) SetTurnFailed(failed bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTurnFailed = failed
}

func (s *wsStreamer) Update(_ context.Context, content string) error {
	s.statsMu.Lock()
	producerAgentID := s.agentID
	// Guarded by statsMu so StreamedContentLen() (read by the agent loop's
	// inline-retry guard, possibly from a different goroutine) observes a
	// consistent length. Accumulate BEFORE the live-delivery gate below —
	// a shadow (non-owning, see below) stream's content must still be fully
	// captured for its own Finalize/transcript write even though it
	// withholds live frames. Finalize reads accumulated only after
	// streaming has completed, so it remains lock-free there.
	s.accumulated.WriteString(content)
	// Live-stream ownership gate (see WSHandler.streamOwners' doc comment):
	// resolved once, lazily, on this streamer's first Update() call, then
	// reused. A streamer with no turnID (legacy/best-effort caller) or no
	// channel/wsHandler wired (e.g. a bare unit-test fixture) always streams
	// live — the gate degrades to the pre-fix always-live behavior rather
	// than silently withholding content it has no way to attribute.
	//
	// Finding B (A-I4 round 4): a delegated CHILD sub-turn (s.parentSpawnCallID
	// != "", stamped by stampStreamerParentSpawnCallID before any Update/
	// Finalize call can observe it) must NEVER become the live-stream owner
	// for its shared chatID, full stop — never via claimStreamOwnership's
	// normal empty-slot-wins/stale-reclaim paths either. The pre-fix
	// claimStreamOwnership-only check let a background/async child win a
	// vacated ownership claim and start streaming its own raw, hidden-by-
	// design narration live: turnState.finalizeStreamer's B4 abandoned-turn
	// path (pkg/agent/turn.go) calls ReleaseStreamOwnership() on the PARENT's
	// claim the instant the parent is canceled (e.g. user clicks Stop) —
	// but a background delegate is intentionally allowed to keep running
	// past its parent's cancellation (see the Critical:true / "background
	// delegate's final answer lost when parent finishes first" fix), so the
	// child's NEXT streaming round (a fresh wsStreamer instance, its own
	// lazy shadowResolved=false) then finds the slot empty and legitimately
	// "wins" it under the old rule — even though a child's own token stream
	// is supposed to stay hidden unconditionally, exactly like the
	// already-correct sync/await case.
	//
	// [FIX-5, Defect 5, 2026-08-03] This gate's justification is
	// SELF-CONTAINED and does not rest on replay.go: a delegated child's
	// narration must never reach the user as its own top-level chat bubble,
	// live-streamed or replayed. This function enforces the LIVE half of
	// that invariant (withholding the live TokenFrame for a shadow stream);
	// the DURABLE half no longer needs an analogous read-side filter at all
	// (ADR-057 FR-034/FR-038 gave every delegated child its own store-backed
	// session, so its narration never lands in the parent's transcript for
	// replay.go to have to withhold in the first place — see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for that mechanism's retirement). Do NOT
	// read the historical replay.go comparison as this gate's justification
	// and remove this gate on discovering replay.go no longer filters
	// anything — this Update() gate is a DIFFERENT, still-live mechanism
	// (the live-stream ownership claim, not a transcript read filter) with
	// its own reason to exist, stated above. Live-verified: reproduced the
	// leak (a second, delegate-authored top-level bubble with raw narration)
	// via a background delegation canceled mid-flight, confirmed the fix
	// removes it. A root/non-delegated turn is unaffected — this branch only
	// ever narrows behavior for parentSpawnCallID != "".
	if !s.shadowResolved {
		if s.parentSpawnCallID != "" {
			s.isShadowStream = true
		} else if s.turnID != "" && s.channel != nil && s.channel.wsHandler != nil {
			s.isShadowStream = !claimStreamOwnership(&s.channel.wsHandler.streamOwners, s.chatID, s.turnID)
		}
		s.shadowResolved = true
	}
	shadow := s.isShadowStream
	s.statsMu.Unlock()

	if shadow {
		// A DIFFERENT, still-live turn already owns live TokenFrame delivery
		// to this chatID. This stream's content is fully captured in
		// s.accumulated above and will be persisted correctly by Finalize —
		// withholding the live frame here is what prevents two concurrent
		// delegate streams from interleaving their deltas into one garbled
		// message, live or on a client that caches/replays the live view.
		return nil
	}

	frame := generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   content,
		SessionId: s.sessionID,
	}
	// FIX 5a: attribute the frame to the turn's TRUE producer (stamped via
	// SetProducerAgentID) rather than leaving the client to guess based on
	// whichever agent it happens to be actively chatting with — wrong for
	// background/delegated sub-turns.
	if producerAgentID != "" {
		frame.AgentId = &producerAgentID
	}
	data, err := json.Marshal(frame)
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
	// docs/internal/investigation/bug-5-replay-order.md (code-reviewer Finding #2).
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
		slog.Warn("ws: token backpressure", "session_id", s.sessionID, "chat_id", s.chatID, "agent_id", producerAgentID)
		return fmt.Errorf("ws: token channel full, token dropped")
	}
	// Cross-browser session attach (#133): also forward the token to every
	// other connection bound to the same session. The originating chat
	// already received the frame above; secondary tabs see the live stream
	// through this fan-out instead of waiting for a transcript reload.
	s.fanOutToSessionPeers(string(generated.WsFrameTypeToken), data)
	return nil
}

// fanOutToSessionPeers ships a frame to every wsConn that shares this
// streamer's session, skipping the originating connection. Used by Update
// (token frames) and Finalize (done frame) so a second browser tab attached
// mid-turn observes the live stream as it happens.
//
// Finding E (A-I4 round 5): this used to write straight into peer.sendCh,
// bypassing the exact replay-divert/backpressure protocol sendRawFrameBytes
// implements for every other live-frame path (see that function's doc
// comment — "shared by sendConnGenFrame and wsStreamer.Update", which this
// method's own token/done sends already honor for the ORIGINATING
// connection via the sendRawFrameBytes call just above, but never did for
// fanned-out PEERS). A peer mid-attach_session-replay (wc.isReplayingLive
// true) has no way to divert a directly-enqueued sendCh write into
// replayDivertCh — the frame lands interleaved with that peer's own
// in-flight replay frames instead of being correctly ordered after them,
// live-verified as a stray, out-of-place bubble. Routing through
// sendRawFrameBytes closes that gap unconditionally: it degrades to the
// exact same direct-sendCh-with-backoff behavior this used to hand-roll
// when the peer isn't replaying (the common case), and correctly diverts
// when it is.
func (s *wsStreamer) fanOutToSessionPeers(frameType string, data []byte) {
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
		sendRawFrameBytes(peer, frameType, data)
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
	// Read the split under the same lock as the total, so the entry cannot
	// carry a total from one turn and a split from another.
	promptTokensF := s.statsPromptTokens
	completionTokensF := s.statsCompletionTokens
	cacheReadF := s.statsCacheRead
	cacheWriteF := s.statsCacheWrite
	durF := float64(s.statsDuration.Milliseconds())
	transcriptAlreadyPersisted := s.transcriptPersisted
	producedModel := s.producedModel
	turnFailed := s.statsTurnFailed
	// FIX 5a/5c: read under statsMu — SetProducerAgentID/SetTurnID (called by
	// the agent loop at streaming-call start) may run on a different
	// goroutine than Finalize (called at turn end).
	producerAgentID := s.agentID
	turnID := s.turnID
	parentSpawnCallID := s.parentSpawnCallID
	// A-I4 round 4 / Finding A: resolve the live-stream shadow gate HERE too,
	// not just in Update(). Every turn — root OR a delegated child sub-turn —
	// runs through the exact same pkg/agent/loop.go runTurn/finalizeStreamer
	// path (spawnSubTurn calls al.runTurn(childCtx, childTS) directly, see
	// subturn.go), so a child's own Finalize fires — sending an UNCONDITIONAL
	// "done" WS frame to the CHATID IT SHARES WITH ITS PARENT — the instant
	// the child's own sub-turn completes, even while the parent's own turn is
	// still actively streaming (the common case for a synchronous/"await"
	// delegate call, which blocks the parent mid-turn while the child runs).
	// DoneFrame carries no turn/parent discriminator, so the client's `done`
	// handler (src/store/chat.ts) has no way to tell "a nested child
	// finished" from "the outer turn finished" — it unconditionally closes
	// whichever bubble is current, so the parent's still-open bubble is
	// finalized prematurely and every following narration segment opens a
	// brand-new bubble. Live-verified via a real 3-round synchronous
	// delegation: a `done` frame lands right after each child's
	// subagent_start (duration_ms matching that child's own subagent_end),
	// well before the parent's real, final `done` — producing N+1 bubbles for
	// N sequential delegate calls instead of one continuous bubble matching
	// the child's own hidden-narration invariant (this same Update() shadow
	// gate already treats a child's own text as never visible — Finalize
	// simply never enforced that for the "done" signal it also sends).
	//
	// [FIX-5, Defect 5, 2026-08-03] The invariant this paragraph enforces —
	// "a delegated child's own narration/signals are never visible as their
	// own top-level chat event" — no longer needs replay.go as a supporting
	// citation: that read-side skip was DELETED (ADR-057 FR-034/FR-038; see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for why — a delegated child now owns its
	// own store-backed session, so there is nothing left in the PARENT's
	// transcript for a read boundary to withhold). This Finalize() gate and
	// Update()'s shadow-stream gate are BOTH still live, LIVE-side
	// mechanisms (withholding the "done" frame / the live TokenFrame,
	// respectively) — each independently justified by the invariant stated
	// above, not by the retired replay.go mechanism.
	//
	// Mirrors Update()'s lazy resolution exactly, including the same "a
	// delegated child NEVER owns the live slot" rule Update() gained for
	// Finding B below — a streamer whose Update() was never called (e.g. an
	// immediate tool-only round with no narration text) would otherwise
	// reach Finalize with shadowResolved still false and default to
	// isShadowStream=false (treated as live), which is wrong for a child
	// sub-turn that happened to stream zero tokens of its own.
	if !s.shadowResolved {
		if parentSpawnCallID != "" {
			s.isShadowStream = true
		} else if turnID != "" && s.channel != nil && s.channel.wsHandler != nil {
			s.isShadowStream = !claimStreamOwnership(&s.channel.wsHandler.streamOwners, s.chatID, turnID)
		}
		s.shadowResolved = true
	}
	shadow := s.isShadowStream
	s.statsMu.Unlock()

	// Release this turn's live-stream ownership claim (if held) so a
	// different, still-running turn on the same chatID can become the live
	// owner (see WSHandler.streamOwners' doc comment). Finalize is the
	// normal, once-per-turn release point via turnState's deferred
	// finalizeStreamer (pkg/agent/turn.go) — safe/no-op when this stream was
	// never the owner (a shadow stream) or never claimed at all (e.g. an
	// immediate tool-only round with no narration text, so Update() was
	// never called). See ReleaseStreamOwnership for the other release
	// points (Cancel, and finalizeStreamer's B4 abandoned-turn path, which
	// deliberately skips the rest of this method).
	s.ReleaseStreamOwnership()

	doneStats.Tokens = &tokensF
	doneStats.Cost = &costF
	doneStats.DurationMs = &durF
	if turnFailed {
		doneStats.TurnFailed = &turnFailed
	}

	doneFrame := generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: s.sessionID,
		Stats:     doneStats,
	}
	// A-I4 round 4 / Finding A: a shadow stream (a delegated child sub-turn
	// that never owned — and, per the rule above, can never win — this
	// chatID's live-stream slot) must not send its own "done" either. Its
	// content was never shown live in the first place (Update() withheld
	// every token); sending "done" anyway prematurely finalizes whatever
	// bubble the OWNING (parent) turn currently has open. The transcript
	// write below stays unconditional — persistence must not depend on live
	// visibility — only the live-facing signals (done frame, peer fan-out,
	// markStreamed) are gated.
	if !shadow {
		sendConnGenFrame(s.conn, string(generated.WsFrameTypeDone), doneFrame)
		// Cross-browser session attach (#133): a second tab attached mid-turn
		// needs the done frame too, otherwise its UI stays in "streaming" state
		// forever even after our token fan-out delivered the full content.
		if doneData, mErr := json.Marshal(doneFrame); mErr == nil {
			s.fanOutToSessionPeers(string(generated.WsFrameTypeDone), doneData)
		}
		// Only mark as streamed if we actually sent content. If the LLM failed
		// before producing any tokens, let the outbound Send path deliver the
		// error message — otherwise the user sees a stuck "thinking" spinner.
		if s.channel != nil && s.accumulated.Len() > 0 {
			s.channel.markStreamed(s.chatID)
		}
	}
	// Record the full assistant response to the session transcript — unless the
	// agent loop already persisted this round's narration via
	// appendIntermediateAssistantTranscript (#416 gate fix). This happens when
	// the turn exits via max_tool_iterations exhaustion: the last executed round
	// is a tool-call round whose streamer (this one) becomes the lastStreamer.
	// Writing here too would duplicate the assistant bubble on replay. We still
	// sent the done frame, fan-out, and markStreamed above — only the append is
	// suppressed.
	if s.agentStore != nil && s.sessionID != "" && !transcriptAlreadyPersisted {
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
				ID:      uuid.New().String(),
				Role:    "assistant",
				AgentID: producerAgentID,
				// TurnID (FIX 5c/1): stamped via SetTurnID so a mid-stream
				// cancel's turn_canceled entry can be correlated with THIS
				// entry on replay, and so MarkLastEntryTruncated's
				// turn-scoped backward-walk can find it.
				TurnID:    turnID,
				Content:   content,
				Timestamp: time.Now().UTC(),
				Tokens:    int(tokensF),
				Cost:      costF,
				Model:     producedModel,
				// The provider's token split. Without these four fields the
				// session-stats aggregator sees no split and falls back to
				// booking the whole turn total as output, which is how every
				// webchat session came to report tokens_in: 0.
				PromptTokens:     promptTokensF,
				CompletionTokens: completionTokensF,
				CacheReadTokens:  cacheReadF,
				CacheWriteTokens: cacheWriteF,
				// ParentSpawnCallID: stamped via SetParentSpawnCallID so a
				// delegation child sub-turn's own streamed narration/final
				// response carries the same nesting correlation its
				// non-streaming siblings (appendIntermediateAssistantTranscript
				// / appendAssistantTranscript) already stamp — see
				// session.TranscriptEntry.ParentSpawnCallID's doc comment.
				// Empty (the common case) for a root turn.
				ParentSpawnCallID: parentSpawnCallID,
			}
			// ADR-057 FR-001/FR-002 (W3): AppendTranscriptStrict refuses loudly
			// (and creates nothing on disk) when s.sessionID does not resolve to
			// a real, store-backed session, instead of AppendTranscript's old
			// lenient silent-create branch. The error was already checked here
			// before this conversion — only the runtime behavior of a failure
			// changes (loud vs. silently minting an orphan session directory) —
			// so surface it as a counter increment (BDD-03) alongside the
			// pre-existing WARN.
			if err := s.agentStore.AppendTranscriptStrict(s.sessionID, entry); err != nil {
				wsTranscriptWriteFailures.Add(1)
				slog.Warn("ws: could not record streamed assistant message", "session_id", s.sessionID, "error", err)
			}
		}
	}
	return nil
}

func (s *wsStreamer) Cancel(_ context.Context) {
	// Defensive symmetry with Finalize's release: Cancel is not on the
	// agent loop's normal per-turn path (finalizeStreamer always calls
	// Finalize, never Cancel — see turn.go), but release the ownership
	// claim here too so a future caller of Cancel cannot leak the chatID's
	// live-stream slot forever.
	s.ReleaseStreamOwnership()
	s.conn.close()
}

// ReleaseStreamOwnership releases this streamer's live-stream ownership
// claim for its chatID (if held), allowing a different, still-running turn
// on the same chatID to become the live owner (see WSHandler.streamOwners'
// doc comment). Safe to call multiple times, concurrently, or when the claim
// was never held — releaseStreamOwnershipClaim only deletes an entry that
// still matches this exact turnID.
//
// This is the single implementation shared by Finalize, Cancel, and — via
// the streamOwnershipReleaser optional interface pkg/agent's finalizeStreamer
// type-asserts for — the B4 abandoned-turn early return (pkg/agent/turn.go).
// That early return deliberately skips the rest of Finalize (no done frame,
// no transcript write, so a stuck goroutine cannot send a spurious signal to
// the frontend) but was found, in a 7-reviewer gate, to also skip releasing
// this claim: a background delegate that became the live owner for a chatID
// and was later MarkAbandoned()'d by cancel.go's PHASE C left that chatID
// permanently shadowed, since Finalize (the only other release point;
// Cancel has no production call sites) never ran. Exported so pkg/agent can
// reach it through bus.Streamer's optional-interface pattern without either
// package importing the other's concrete type.
func (s *wsStreamer) ReleaseStreamOwnership() {
	s.statsMu.Lock()
	turnID := s.turnID
	s.statsMu.Unlock()
	if s.channel != nil && s.channel.wsHandler != nil {
		releaseStreamOwnershipClaim(&s.channel.wsHandler.streamOwners, s.chatID, turnID)
	}
}
