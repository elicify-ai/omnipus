// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/pairing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// resolveApprovalToolPolicy resolves the effective tool policy consulted by the WS
// approval hook (the gateway-side exec gate) from the global config alone.
//
// Unification (#438): this delegates to the ONE authoritative single-tool
// resolver tools.EffectiveToolPolicy — the SAME primitive the agent loop's
// FilterToolsByPolicy uses at defs-assembly time — so the gateway exec gate and
// the loop's sent-defs view can never drift. It builds the resolver inputs (the
// sandbox global floor + the agent's builtin policy) from cfg. The primitive
// encapsulates, in order: (1) the scope gate, and (2) global×agent
// strictest-wins (deny > ask > allow). ToolSearch resolves through this same
// merge as every other static builtin tool — it is seeded "allow" as real,
// explicit data for every agent (pkg/coreagent/core.go), not a code-level
// force-allow (there used to be one here; it was a CLAUDE.md hard-constraint-6
// violation and has been removed). The tools that ToolSearch *loads* stay
// independently policy-gated when they are actually called.
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
	if cfg == nil {
		// No config to build a floor from: no seeded policy data of any kind
		// (CLAUDE.md hard constraint 6 — no code-level fallback), so this
		// defaults to interactive approval rather than a language-level allow
		// or deny.
		return "ask"
	}
	// No default-policy fallback (CLAUDE.md hard constraint 6): only explicit
	// global/agent entries are threaded through; a tool with no match on
	// either side fails closed to "deny" inside tools.EffectiveToolPolicy.
	polCfg, agentType := tools.BuildFallbackPolicyCfg(cfg, agentID)
	return tools.EffectiveToolPolicy(polCfg, tools.ScopeGeneral, agentType, toolName)
}

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
	// ADR-087 D2: replay_message truncation fields. *bool so a test can tell
	// "field absent" (nil) from "explicitly false" — mirrors the generated
	// ReplayMessageFrame.Truncated's own *bool shape.
	Truncated        *bool  `json:"truncated,omitempty"`
	TruncationReason string `json:"truncation_reason,omitempty"`
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
	// AskUserQuestionFrame decoder slot (spec v3 §0.6 replay reconstruction).
	Card map[string]any `json:"card,omitempty"`
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

	// liveStreamers tracks, per session id, the wsStreamer instance CURRENTLY
	// streaming a foreground turn's live round (ADR-082 D2/D3). Registered by
	// GetStreamer on every streaming round (a turn spanning several
	// tool-calling rounds gets a fresh wsStreamer per round — see
	// pkg/agent/loop.go's per-round GetStreamer call — so a later round's
	// entry simply overwrites an earlier one for the same session; the
	// catch-up snapshot below is therefore scoped to the CURRENTLY streaming
	// round's own accumulated text, not the whole turn's cross-round
	// narration — a documented, accepted scope limit, since a mid-turn
	// tool-call boundary is outside this wave's test matrix), unregistered by
	// Finalize once that round's/turn's done frame has been sent. Guarded by
	// mu — see wsStreamer.Update's doc comment for why the bind
	// (handleAttachSession) and the append+resolve (Update) must share this
	// SAME critical section to get "no duplicate, no gap" catch-up ordering.
	liveStreamers map[string]*wsStreamer
	// pendingMessageStatuses holds persisted user messages until the agent
	// actually opens a streamer for their turn. It backs the received→working
	// distinction exposed to the SPA. Guarded by mu.
	pendingMessageStatuses map[string][]pendingMessageStatus

	// approvalRegV2 is the Central Tool Registry approval registry (FR-016, FR-070).
	// Injected at boot by the gateway after construction.  Nil until then.
	approvalRegV2 *approvalRegistryV2

	// askUserReg is the AskUserQuestion pending registry (askuserquestion-
	// tool-spec v3; pkg/askuser). Injected at boot alongside approvalRegV2.
	// Nil until then — handleAskUserAnswer and emitSessionState's
	// pending_asks snapshot degrade gracefully.
	askUserReg *askuser.Registry

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

	// streamOwners tracks, per sessionID, which turn currently "owns" live
	// TokenFrame delivery to that session's bound connections. Keys are
	// sessionID (string); values are streamOwnerClaim (owning turnID + the
	// time it claimed the slot, the latter backing the stale-claim reclaim
	// safety net described below).
	//
	// [ADR-082 review F6] Originally keyed by chatID — the ORIGINATING
	// connection's chatID a wsStreamer happened to be created with. Once
	// ADR-082 D2 moved live delivery itself to be resolved purely by
	// sessionID (resolveSessionConnsLocked), a chatID-keyed ownership claim
	// stopped matching what it was supposed to gate: two turns with
	// DIFFERENT origin chatIDs (e.g. a keeper/background turn's own internal
	// chatID versus the user's live webchat chatID) that both deliver into
	// the SAME session's bound connections never contended for the same
	// claim, so both could become "owner" simultaneously and interleave
	// their live tokens into the same viewers — the exact bug this map
	// exists to prevent, just re-opened via the mismatched key. Keying by
	// sessionID instead makes the claim key match the delivery key exactly.
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
		liveStreamers:         make(map[string]*wsStreamer),
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
		if tid, ok := h.taskChatIDs[chatID]; ok {
			// sessions is never keyed by tid (only by chatID); clean up only sessionIDs.
			delete(h.sessionIDs, tid)
			delete(h.taskChatIDs, chatID)
		}
		delete(h.sessions, chatID)
		delete(h.sessionIDs, chatID)
		h.mu.Unlock()
		wc.close()

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

	go h.writePump(wc, chatID)
	go h.pingPump(wc)

	// Emit session_state one-shot on every new WS connection (FR-052, FR-073, FR-081).
	// This lets the SPA reconcile stale approval modals after a gateway restart.
	// No session id is known yet at this point (before any attach/message) —
	// ADR-082 D4's active_turn is necessarily absent here; handleAttachSession
	// emits a follow-up session_state once a session is bound.
	h.emitSessionState(wc, "")

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
	// wsAuthErrNotSignedIn — accounts DO exist, but this handshake carried no
	// omnipus-session cookie at all. Distinct from wsAuthErrInvalidToken
	// ("expired") because the user action differs: there is nothing to
	// refresh, they have to sign in. Previously this state produced no
	// message whatsoever — the server just let the auth-frame read time out
	// (see classifyWSAuthRefusal).
	wsAuthErrNotSignedIn = "You're not signed in — reload the page to sign in and reconnect."
)

// ---------------------------------------------------------------------------
// WebSocket auth refusal — fast, loud, diagnosable (see classifyWSAuthRefusal)
// ---------------------------------------------------------------------------

// wsAuthRefusal is a decided, terminal WebSocket authentication failure,
// carrying the three different renderings the same fact needs:
//
//	userMessage — human copy for the ErrorFrame the SPA renders (the D5 fix's
//	              constants above; never a raw protocol string).
//	closeReason — short technical diagnostic on the WS close frame, visible in
//	              browser devtools and to any programmatic client. Must stay
//	              under wsCloseReasonMaxBytes.
//	code        — stable, greppable machine reason for the server log. This is
//	              what an operator triaging "why can't chat connect?" reads.
type wsAuthRefusal struct { // not-wire-format: internal auth-decision value; only its userMessage field is ever serialized, via generated.ErrorFrame.
	userMessage string
	closeReason string
	code        string
}

// wsAuthFrameDeadline bounds how long a handshake that could not authenticate
// from the upgrade request alone waits for the legacy
// {"type":"auth","token":...} first frame. Only programmatic clients ever
// reach this wait now (browsers are decided before it — classifyWSAuthRefusal),
// so the value is a courtesy to a slow CLI client, not a UX cost.
//
// A var, not a const, for exactly one reason: it is the only way to exercise
// the read-failure branch in a test without sleeping 10 real seconds, and that
// branch used to be the silent one. Both sockets read this single symbol so
// the two can never drift apart. Never mutate it outside a test.
var wsAuthFrameDeadline = 10 * time.Second

// wsCloseReasonMaxBytes is the largest close-frame reason gorilla will accept.
// A WebSocket control frame payload is capped at 125 bytes (RFC 6455 §5.5) and
// a close payload spends 2 of them on the status code, leaving 123. Exceeding
// it makes WriteMessage fail with ErrInvalidControlFrame — i.e. the close
// frame silently never reaches the client and the refusal degrades right back
// into the opaque disconnect this whole change exists to eliminate, so
// truncateCloseReason enforces the bound rather than trusting call sites.
const wsCloseReasonMaxBytes = 123

// truncateCloseReason bounds reason to wsCloseReasonMaxBytes, cutting on a
// UTF-8 rune boundary so a multi-byte character is never split into invalid
// UTF-8 (close reasons are required to be valid UTF-8 by RFC 6455 §5.5.1).
func truncateCloseReason(reason string) string {
	if len(reason) <= wsCloseReasonMaxBytes {
		return reason
	}
	cut := wsCloseReasonMaxBytes
	for cut > 0 && !utf8.ValidString(reason[:cut]) {
		cut--
	}
	return reason[:cut]
}

// wsHandshakeIsBrowser reports whether this upgrade request came from a web
// browser, which determines whether waiting for an auth frame is worth doing.
//
// Rationale: a browser's WebSocket constructor is REQUIRED to send an Origin
// header (WHATWG WebSocket spec / RFC 6455 §4.1), and non-browser clients
// (gorilla's Dialer, omnipus run, the CLI readiness probe — all of which dial
// with nil headers) do not send one unless they deliberately opt in. So an
// Origin header means "this is the SPA", and the SPA provably cannot send an
// auth frame: it holds no JS-visible token at all post-Wave-1 (src/lib/ws.ts
// — "no client-sent {type:'auth', token} frame is needed or possible"). Its
// only credential is the session cookie the upgrade request already carried,
// and by the time this is consulted that cookie has already failed.
//
// SAFETY: this predicate can only ever cause a REFUSAL, never an admission.
// A false positive costs a hypothetical non-browser client that sets Origin
// its frame handshake; a false negative costs nothing but the pre-existing
// 10-second wait. It therefore cannot widen any auth posture, which is why a
// header heuristic is acceptable here and would not be acceptable on an
// allow path.
func wsHandshakeIsBrowser(r *http.Request) bool {
	return r != nil && r.Header.Get("Origin") != ""
}

// classifyWSAuthRefusal diagnoses a WS handshake that has already failed
// cookie authentication, and reports whether waiting on the legacy auth-frame
// read is futile (futile=true → refuse NOW, do not block on the read).
//
// THE DEFECT THIS FIXES. Both WS auth paths blocked for 10 seconds on a first
// frame that the SPA stopped sending at the Wave-1 cookie cutover, then
// returned on `i/o timeout` having written nothing at all to the client — no
// error frame, no close frame, no reason. Measured on a live gateway with
// gateway.dev_mode_bypass=true and gateway.users=[]: 55 WS auth failures, 0
// successes, against 2023 successful REST AUTH-BYPASS hits, one
// "ws: auth read failed" log line every ~10s. The SPA loaded, the agent
// picker populated, the screen said "Your agent is ready. Start a
// conversation below." — and chat could never connect. That is the
// "looks normal while silently degraded" class this project treats as a
// serious bug (docs/internal/false-green-patterns.md).
//
// NOT FIXED HERE, DELIBERATELY (operator directive): dev_mode_bypass still
// does not authenticate a browser's WebSocket. This function never returns
// "allow" — it only ever decides how fast and how loudly a doomed handshake
// dies. The bypass branch further down authenticateWS is left exactly as it
// was; see its comment.
//
// futile is true for browser handshakes ONLY. A browser has already spent its
// one credential (the cookie) on the upgrade request and cannot follow up with
// a frame, so waiting is pure dead time. A programmatic client is left
// completely untouched — same 10-second window, same branches, same outcome —
// because it genuinely may be about to send a valid frame, and refusing it
// early would break `omnipus run` and the CLI readiness probe, which both
// authenticate exactly that way.
func classifyWSAuthRefusal(r *http.Request, cfg *config.Config) (wsAuthRefusal, bool) {
	cookiePresent := middleware.HasSessionCookie(r)
	accounts := cfg != nil && bearerAccountsConfigured(cfg)
	envToken := os.Getenv("OMNIPUS_BEARER_TOKEN") != ""
	bypass := cfg != nil && cfg.Gateway.DevModeBypass

	var refusal wsAuthRefusal
	switch {
	case !accounts && !envToken && !bypass:
		// Fail-closed posture with nothing configured at all: no credential of
		// any kind could match. Onboarding is genuinely incomplete.
		refusal = wsAuthRefusal{
			userMessage: wsAuthErrNoUsers,
			closeReason: "no accounts configured: complete onboarding, then reload",
			code:        "no_auth_configured",
		}

	case cookiePresent:
		// A session cookie WAS sent and matched no account: expired, revoked,
		// or replayed. Distinct from "never signed in" — the user had a
		// session and it is gone.
		refusal = wsAuthRefusal{
			userMessage: wsAuthErrInvalidToken,
			closeReason: "session expired: session cookie matched no account",
			code:        "stale_session_cookie",
		}

	case bypass && !accounts && !envToken:
		// The exact reported configuration: REST is wide open via
		// dev_mode_bypass while the WebSocket cannot authenticate anyone. An
		// operator must be able to read this one log line and understand that
		// those two facts are not in conflict — the bypass has never covered
		// the SPA's WebSocket, because the SPA sends no auth frame for the
		// bypass branch to fire on.
		refusal = wsAuthRefusal{
			userMessage: wsAuthErrNoUsers,
			closeReason: "dev_mode_bypass does not authenticate websockets; sign in first",
			code:        "dev_bypass_not_on_websocket",
		}

	default:
		// Accounts (or an env token) exist and no cookie was presented: this
		// client has simply never signed in.
		refusal = wsAuthRefusal{
			userMessage: wsAuthErrNotSignedIn,
			closeReason: "not signed in: no session cookie on the websocket handshake",
			code:        "no_session_cookie",
		}
	}
	return refusal, wsHandshakeIsBrowser(r)
}

// refuseWSAuth writes the terminal refusal to the client: the human-readable
// error frame first (so the SPA has something to render), then a
// policy-violation close carrying the technical reason. Both writes are
// deadline-bounded, so an unresponsive client cannot pin this goroutine.
//
// It also emits the diagnostic server log. scope names the socket ("ws" or
// "browser-ws") so the two handlers stay distinguishable in one log stream;
// readErr is the underlying read failure when the refusal follows a failed
// auth-frame read, and nil when the refusal was decided before the read.
func refuseWSAuth(
	scope string,
	conn *websocket.Conn,
	r *http.Request,
	cfg *config.Config,
	refusal wsAuthRefusal,
	readErr error,
) {
	origin := ""
	remote := ""
	if r != nil {
		origin = r.Header.Get("Origin")
		remote = r.RemoteAddr
	}
	slog.Warn(scope+": websocket authentication refused",
		"reason", refusal.code,
		"detail", refusal.closeReason,
		"remote_addr", remote,
		"origin", origin,
		"cookie_present", middleware.HasSessionCookie(r),
		"accounts_configured", cfg != nil && bearerAccountsConfigured(cfg),
		"env_token_set", os.Getenv("OMNIPUS_BEARER_TOKEN") != "",
		"dev_mode_bypass", cfg != nil && cfg.Gateway.DevModeBypass,
		"read_error", readErr,
	)
	sendGenWSFrame(conn, generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: refusal.userMessage,
	})
	writeCloseAuthFailedWithReason(conn, refusal.closeReason)
}

// refuseWSAuthAfterFailedRead handles the auth-frame read failing (the 10s
// deadline expiring, or the peer vanishing). Before this existed the whole
// branch was `slog.Warn("ws: auth read failed"); return false` — nothing was
// written to the client, so the SPA saw an unexplained disconnect and
// reconnected forever. Now the client is told why, in the same shape every
// other auth refusal uses.
func refuseWSAuthAfterFailedRead(scope string, conn *websocket.Conn, r *http.Request, cfg *config.Config, readErr error) {
	refusal, _ := classifyWSAuthRefusal(r, cfg)
	refusal.closeReason = "no auth frame received before the handshake deadline"
	refusal.code = "auth_frame_timeout"
	refuseWSAuth(scope, conn, r, cfg, refusal, readErr)
}

// writeCloseAuthFailedWithReason sends the policy-violation close frame that
// terminates a rejected handshake, carrying reason as the close-frame text.
// The write deadline is the same invariant writePump enforces: a close frame
// to an unresponsive client must not block this goroutine forever.
func writeCloseAuthFailedWithReason(conn *websocket.Conn, reason string) {
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteWait)); err != nil {
		slog.Debug("ws-auth: SetWriteDeadline failed for close frame", "error", err)
		return
	}
	if err := conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, truncateCloseReason(reason)),
	); err != nil {
		slog.Debug("ws-auth: write close frame failed", "error", err)
	}
}

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
// When the cookie does NOT resolve, a browser is refused immediately rather
// than waiting out the frame deadline in silence — see classifyWSAuthRefusal,
// which documents the outage that motivated it. That gate has no allow
// outcome, so it changes who can authenticate not at all; it changes only how
// fast and how audibly a handshake that never could authenticate dies.
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

	// Cookie auth failed. Decide NOW whether the legacy auth-frame read can
	// possibly help, instead of blocking 10 seconds on a frame that (for the
	// SPA) provably never arrives and then returning in silence. This gate
	// only ever refuses — it cannot admit anyone — so it widens nothing; see
	// classifyWSAuthRefusal for the full rationale and the measured defect.
	if refusal, futile := classifyWSAuthRefusal(r, cfg); futile {
		refuseWSAuth("ws", conn, r, cfg, refusal, nil)
		return false
	}

	conn.SetReadDeadline(time.Now().Add(wsAuthFrameDeadline))
	_, data, err := conn.ReadMessage()
	if err != nil {
		refuseWSAuthAfterFailedRead("ws", conn, r, cfg, err)
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
		writeCloseAuthFailedWithReason(conn, "authentication failed")
		return false
	}

	// 3. Fallback: legacy OMNIPUS_BEARER_TOKEN env var.
	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			// Dev mode: allow without auth.
			//
			// UNREACHABLE FROM ANY BROWSER, and always has been — this is the
			// branch the 55-failures/0-successes report bottomed out in. It
			// fires only for a client that sent {"type":"auth","token":...},
			// and the SPA has sent no such frame since the Wave-1 cookie
			// cutover (src/lib/ws.ts: "no client-sent {type:'auth', token}
			// frame is needed or possible"). So dev_mode_bypass has never
			// authenticated the SPA's WebSocket even though it authenticates
			// every REST call — it only ever authenticates programmatic
			// clients (omnipus run, the CLI readiness probe, Go tests) that
			// still perform the legacy frame handshake.
			//
			// Deliberately left AS IS by operator directive: browsers are now
			// refused fast and loudly by classifyWSAuthRefusal above rather
			// than being admitted here. Do not "fix" this by hoisting the
			// DevModeBypass check ahead of the read — that would extend the
			// bypass to the browser surface, which is the opposite of the
			// decision taken. Removal of the branch entirely is the operator's
			// call, not this code's.
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			return true
		}
		// No auth configured — deny by default (fail closed), matching HTTP auth path.
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrNoUsers,
		})
		writeCloseAuthFailedWithReason(conn, "authentication failed")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) != 1 {
		sendGenWSFrame(conn, generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: wsAuthErrInvalidToken,
		})
		writeCloseAuthFailedWithReason(conn, "authentication failed")
		return false
	}
	conn.SetReadDeadline(time.Now().Add(wsPongWait))
	return true
}

// wsMaxMessageBytes is the maximum size of an incoming WebSocket message (5 MB).
// Messages exceeding this limit are rejected with an error frame and the connection
// is closed by gorilla/websocket (SetReadLimit causes a protocol-level close).
const wsMaxMessageBytes = 5 * 1024 * 1024

// wsHandlerReadLoop carries the shared state of readLoop across its stages.
type wsHandlerReadLoop struct {
	h      *WSHandler
	ctx    context.Context
	wc     *wsConn
	chatID string
}

// wsHandlerReadLoopFlow reports how a block stage of wsHandlerReadLoop wants the conductor to proceed.
type wsHandlerReadLoopFlow int

const (
	wsHandlerReadLoopNext wsHandlerReadLoopFlow = iota
	wsHandlerReadLoopReturn
	wsHandlerReadLoopContinue
	wsHandlerReadLoopBreak
)

// readLoop processes client frames until the connection closes.
func (h *WSHandler) readLoop(ctx context.Context, conn *websocket.Conn, wc *wsConn, chatID string) {
	wh := &wsHandlerReadLoop{h: h, ctx: ctx, wc: wc, chatID: chatID}

	// Enforce a hard read limit so clients cannot exhaust server memory with
	// oversized frames. gorilla/websocket will return an error on the next
	// ReadMessage call if the incoming frame exceeds this limit.
	conn.SetReadLimit(wsMaxMessageBytes)

wsHandlerReadLoopLoop1:
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
					wh.chatID,
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
				slog.Debug("ws: connection closed unexpectedly", "chat_id", wh.chatID, "error", err)
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
				"chat_id", wh.chatID, "error", err, "is_timeout", isTimeout)
			return
		}

		if err := conn.SetReadDeadline(time.Now().Add(wsPongWait)); err != nil {
			slog.Warn("ws: SetReadDeadline failed, exiting readLoop", "chat_id", wh.chatID, "error", err)
			return
		}

		// Peek at the type discriminator before full decode.
		var peek wsTypeOnly
		if err := json.Unmarshal(data, &peek); err != nil {
			slog.Warn("ws: malformed frame", "error", err)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "malformed message frame",
			})
			continue
		}

		// Per-frame JSON Schema validation (mirrors REST decodeAndValidate).
		// Gated by gateway.validate_inbound; when false the check is a no-op.
		validateEnabled := wh.h.agentLoop.GetConfig().Gateway.ValidateInbound
		if validateEnabled {
			schemaName := wsFrameSchemaName(peek.Type)
			if schemaName != "" {
				if errMsg, serverErr := ValidateInboundFrameJSON(schemaName, data); errMsg != "" {
					_wsInboundFrameDropped.Add(1)
					wh.wc.inboundDropped.Add(1)
					if serverErr {
						// Server-side compile failure — log and drop; do not reveal details.
						slog.Error("ws: inbound schema unavailable, dropping frame",
							"schema", schemaName, "frame_type", peek.Type, "chat_id", wh.chatID)
					} else {
						// Client-side schema violation — send descriptive error frame.
						slog.Warn("ws: inbound frame schema validation failed — dropping",
							"schema", schemaName, "frame_type", peek.Type, "error", errMsg, "chat_id", wh.chatID)
						sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
							Type:    string(generated.WsFrameTypeError),
							Message: "frame schema validation failed (" + schemaName + "): " + errMsg,
						})
					}
					continue
				}
			}
		}

		switch wh.dispatchFrame(data, peek) {
		case wsHandlerReadLoopContinue:
			continue wsHandlerReadLoopLoop1
		}
	}
}

// stringPtrOrEmpty dereferences an optional wire-format string field,
// returning "" for a nil pointer. Kept out of dispatchFrame's own body so
// the #823 client_message_id field doesn't grow dispatchFrame's grandfathered
// gocyclo budget (scripts/budgets/gocyclo.txt) — mirrors the existing
// agentID/sessionID deref pattern inline below.
func stringPtrOrEmpty(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// dispatchFrame dispatches one validated WebSocket frame to its type-specific handler.
func (wh *wsHandlerReadLoop) dispatchFrame(data []byte, peek wsTypeOnly) wsHandlerReadLoopFlow {
	switch peek.Type {
	case string(generated.WsFrameTypeMessage):
		var f generated.MessageFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed message frame", "error", err)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "malformed message frame",
			})
			return wsHandlerReadLoopContinue
		}
		if f.Content == "" && len(f.Media) == 0 {
			return wsHandlerReadLoopContinue
		}
		var agentID string
		if f.AgentId != nil {
			agentID = *f.AgentId
		}
		var sessionID string
		if f.SessionId != nil {
			sessionID = *f.SessionId
		}
		clientMessageID := stringPtrOrEmpty(f.ClientMessageId)
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
				"chat_id", wh.chatID, "value", f.Metadata["workspace_setup_kickoff"])
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "malformed workspace_setup_kickoff metadata",
			})
			return wsHandlerReadLoopContinue
		}
		wh.h.handleChatMessageWithClientID(
			wh.ctx, wh.chatID, sessionID, f.Content, agentID, f.Media,
			modelName, workspaceID, setupKickoff, clientMessageID, wh.wc,
		)
	case string(generated.WsFrameTypeCancel):
		var f generated.CancelFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed cancel frame", "error", err)
			return wsHandlerReadLoopContinue
		}
		if f.SessionId == "" {
			wh.wc.inboundDropped.Add(1)
			slog.Warn("ws: cancel frame missing required session_id — dropping",
				"chat_id", wh.chatID)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "cancel requires session_id",
			})
			return wsHandlerReadLoopContinue
		}
		wh.h.handleCancel(wh.wc, f.SessionId)
	case string(generated.WsFrameTypeAttachSession):
		var f generated.AttachSessionFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed attach_session frame", "error", err)
			return wsHandlerReadLoopContinue
		}
		slog.Info("ws: attach_session frame received",
			"chat_id", wh.chatID,
			"requested_session_id", f.SessionId,
		)
		if f.SessionId != "" {
			wh.h.handleAttachSession(wh.ctx, wh.chatID, f.SessionId, f.Since, wh.wc)
		} else {
			slog.Warn("ws: attach_session with empty session_id", "chat_id", wh.chatID)
		}
	case string(generated.WsFrameTypeSessionClose):
		return wh.handleSessionCloseFrame(data)
	case string(generated.WsFrameTypeSessionModeUpdate):
		return wh.handleSessionModeUpdateFrame(data)
	case string(generated.WsFrameTypePing):
		// Application-layer pong: the SPA's 60s "any frame received" liveness
		// check needs a server-originated frame during idle. Gorilla WS-protocol
		// ping/pong runs independently as NAT-keepalive.
		// Debounced to 1 pong/100ms/conn so a flood of pings cannot amplify into
		// outbound traffic against writePump's serialized sendCh.
		nowNs := time.Now().UnixNano()
		lastNs := wh.wc.lastPongSentUnixNano.Load()
		if nowNs-lastNs >= int64(100*time.Millisecond) {
			wh.wc.lastPongSentUnixNano.Store(nowNs)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypePong), generated.PongFrame{
				Type: string(generated.WsFrameTypePong),
			})
		}
	case string(generated.WsFrameTypeDevicePairingResponse):
		var f generated.DevicePairingResponseFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed device_pairing_response frame", "error", err)
			wh.wc.inboundDropped.Add(1)
			return wsHandlerReadLoopContinue
		}
		wh.h.handleDevicePairingResponse(f.DeviceId, f.Decision)
	case string(generated.WsFrameTypeWhatsappPairingSubscribe):
		// #283 (Option B): scope whatsapp_pairing frames to the connection(s)
		// viewing a channel's pairing UI so the QR secret isn't broadcast to
		// every tab. active=true subscribes this conn; false clears it. Any
		// connection reaching this point in readLoop is already authenticated
		// (single-account model), so no further role gate applies.
		var f generated.WhatsAppPairingSubscribeFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed whatsapp_pairing_subscribe frame", "error", err)
			wh.wc.inboundDropped.Add(1)
			return wsHandlerReadLoopContinue
		}
		if f.ChannelId == "" {
			wh.wc.inboundDropped.Add(1)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "whatsapp_pairing_subscribe requires channel_id",
			})
			return wsHandlerReadLoopContinue
		}
		wh.h.subscribePairingInterest(wh.wc, f.ChannelId, f.Active)
	case string(generated.WsFrameTypeAskUserAnswer):
		// AskUserQuestion card submission/cancel (askuserquestion-tool-
		// spec v3 §3): bridge to askuser.Registry.Submit / CancelByUser.
		// Full semantic validation (ownership, membership, arity,
		// first-valid-wins) is the registry's; the schema gate above
		// (wsFrameSchemaName → AskUserAnswerFrame) bounds the shape.
		var f generated.AskUserAnswerFrame
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Warn("ws: malformed ask_user_answer frame", "error", err)
			wh.wc.inboundDropped.Add(1)
			return wsHandlerReadLoopContinue
		}
		if f.CardId == "" || f.SessionId == "" {
			wh.wc.inboundDropped.Add(1)
			sendConnGenFrame(wh.wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
				Type:    string(generated.WsFrameTypeError),
				Message: "ask_user_answer requires card_id and session_id",
			})
			return wsHandlerReadLoopContinue
		}
		wh.h.handleAskUserAnswer(wh.wc, f)
	default:
		slog.Debug("ws: unknown frame type ignored", "type", peek.Type, "chat_id", wh.chatID)
	}
	return wsHandlerReadLoopNext
}

// wsFrameSchemaName maps a WS frame type string to the corresponding inbound
// JSON Schema name (the key used in ValidateInboundFrameJSON). Returns ""
// for frame types that have no inbound schema (e.g. ping — no body to validate).
func wsFrameSchemaName(frameType string) string {
	switch frameType {
	case "browser_input_offer":
		return "BrowserInputOfferFrame"
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
	case string(generated.WsFrameTypeSessionModeUpdate):
		return "SessionModeUpdateFrame"
	case string(generated.WsFrameTypeWhatsappPairingSubscribe):
		return "WhatsAppPairingSubscribeFrame"
	case string(generated.WsFrameTypeAskUserAnswer):
		return "AskUserAnswerFrame"
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

// wsStreamer implements bus.Streamer, pushing token/done frames into a wsConn's send channel.
// It also accumulates the full response to persist it to the session transcript on Finalize.
type wsStreamer struct {
	// chatID is the ORIGINATING connection's chatID — kept as an advisory
	// "origin" label only (ADR-082 D2). It still keys the shadow-stream
	// ownership claim (claimStreamOwnership/releaseStreamOwnershipClaim) and
	// webchatChannel.markStreamed, but it is NO LONGER used to resolve which
	// connection(s) receive this streamer's frames — see Update/Finalize,
	// which resolve the CURRENT set of connections bound to sessionID (via
	// WSHandler.resolveSessionConnsLocked) on every call instead of holding a
	// single *wsConn captured at streamer-creation time. That single-target
	// design was ADR-082's E2/E4: a `conn` field pinned to a since-closed
	// connection meant every later frame silently had nowhere live to go.
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
	// h is the *WSHandler this streamer resolves live connections through
	// (ADR-082 D2) — see wsHandler()'s doc comment for why this is a
	// SEPARATE field from channel.wsHandler rather than derived from it.
	// Always set by GetStreamer; nil for a bare test fixture.
	h *WSHandler
	// accumulated holds this streamer's own (per-round) response text.
	// ADR-082 D2/D3: guarded by the WSHandler's own mu (via wsHandler(), NOT
	// statsMu) whenever a *WSHandler is wired, because every write here
	// (Update) must be atomic with resolving the CURRENT set of connections
	// bound to sessionID, under the SAME lock handleAttachSession's catch-up
	// bind+snapshot uses (WSHandler.snapshotLiveStreamerLocked) — that shared
	// critical section is what gives "no duplicate, no gap" catch-up
	// ordering. A bare test fixture with no channel/wsHandler wired accesses
	// it lock-free (single-goroutine use only, matching every other
	// wsHandler==nil degrade in this type).
	accumulated strings.Builder

	// producedModel is the model string that produced this streamed response.
	// Set by the agent loop via SetProducedModel before Finalize so the
	// transcript entry written by Finalize carries the per-turn Model field
	// (FR-013). Empty when the agent loop didn't push a value (legacy
	// callers) — the UI omits the model span entirely for those (FR-014);
	// there is no "(model not recorded)" placeholder rendered.
	producedModel string

	// continuationContent/hasContinuation carry the FULL accumulated answer
	// across an ADR-087 D6 auto-continuation (turnState.continuationAccum),
	// set by the agent loop via SetContinuationContent before Finalize.
	// wsStreamer is per PROVIDER CALL, not per turn (ADR-087 §2.8): a
	// continuation's own streamer only ever accumulated the LAST call's
	// text (the suffix), never the full prefix+suffix the user actually
	// read. When hasContinuation is true, Finalize persists
	// continuationContent as the transcript entry's content INSTEAD OF its
	// own `accumulated` buffer (and instead of the `finalContent`
	// fallback), so the reconnect/replay snapshot matches what the live
	// bubble showed. Tokens are never re-emitted here — the SPA already
	// received them live; this only fixes what gets PERSISTED. Guarded by
	// statsMu, like producedModel: SetContinuationContent (agent loop) and
	// Finalize (turn end) may run on different goroutines.
	continuationContent string
	hasContinuation     bool

	// truncationReason carries ADR-087 D2's narrow reason enum
	// ("max_output_tokens" — the only value pkg/agent writes) for this
	// streamer's transcript entry, set by the agent loop via SetTruncation
	// (finalizeStreamer's streamerTruncationSetter probe, pkg/agent/turn.go)
	// immediately BEFORE Finalize is called. Finalize stamps Truncated/
	// TruncationReason onto the SAME write that persists the assistant
	// entry — including a ZERO-CONTENT entry when the turn produced no text
	// at all (D4a) — instead of relying on a post-hoc
	// MarkLastEntryTruncated backward-walk after the fact. That post-hoc
	// walk was the bug this field replaces: on the streamed path it ran
	// AFTER Finalize's own write, so for a zero-content D4a turn Finalize's
	// old `if content != ""` gate skipped writing any entry at all — the
	// backward-walk then found nothing (silent no-op) or, when an EARLIER
	// same-turn narration entry existed (this round's own TurnID, written by
	// appendIntermediateAssistantTranscript during an earlier tool-calling
	// round), it walked back and mis-stamped THAT completed narration as
	// truncated instead. Guarded by statsMu, like continuationContent:
	// SetTruncation (agent loop) and Finalize (turn end) may run on
	// different goroutines.
	truncationReason string

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
