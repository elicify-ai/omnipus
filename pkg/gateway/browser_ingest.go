// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_ingest.go — Wave 1 component E of the live-browser video epic.
//
// The capture-ingest endpoint (/api/v1/browser/capture-ingest, FR-012) is the
// gateway's LOOPBACK-ONLY receiving side for the WebCodecs encoder page. The
// gateway drives CDP Page.startScreencast on the agent tab, pushes each JPEG
// frame DOWN this connection to the encoder page (FeedFrame → browser_frame_feed),
// and the encoder page pushes encoded H.264/VP8/Opus chunks back UP
// (browser_ingest_init + binary browser_ingest_chunk). Encoded chunks are
// handed to the stream relay (component D) via the injected Relay interface.
//
// THREAT MODEL / SECURITY POSTURE (why this endpoint is distinct from the
// viewer WS in browser_ws.go):
//
//   - The encoder page holds NO user bearer token. It authenticates with a
//     per-stream CAPABILITY TOKEN (FR-013) minted here and delivered to the page
//     out-of-band via CDP addScriptToEvaluateOnNewDocument — never via URL. So
//     this endpoint uses a capability-token model, not the viewer AuthFrame
//     user-identity model.
//   - The endpoint is loopback-only (FR-012): any connection whose RemoteAddr is
//     not a loopback address is rejected BEFORE the WebSocket upgrade and before
//     any relay. A remote attacker can never reach it.
//   - Single connection per token (CRIT-002): a token is good for exactly ONE
//     successful connection. A second concurrent connection presenting a live
//     token is rejected; once a connection closes the token is DEAD (there is no
//     same-token reconnect — a transient drop is recovered by orchestration
//     (W2-M) re-minting a fresh token and relaunching the encoder page). A dead,
//     mis-scoped, or absent token is rejected before any relay.
//   - Every ingest-auth rejection and every stream lifecycle transition is
//     audit-logged (FR-024).
//
// This file OWNS only the ingest endpoint. It depends on component D purely
// through the Relay interface, and on nothing in browser_ws.go. Integration
// (registering the handler + wiring MintIngestToken/FeedFrame/EndStream into the
// stream orchestrator) is documented at RegisterCaptureIngest.

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// captureIngestPath is the loopback-only ingest endpoint (FR-012).
const captureIngestPath = "/api/v1/browser/capture-ingest"

// defaultIngestMaxMessageBytes is the FR-014 max-message bound for a single
// encoded chunk. Sized for a realistic keyframe (>= 2 MB) and independent of
// browser_ws.go's 64 KB viewer-inbound cap. A chunk whose payload exceeds this
// is rejected + triggers an encoder step-down (never fragmented/reassembled).
const defaultIngestMaxMessageBytes = 2 * 1024 * 1024

// ingestChunkHeaderLen is the fixed BigEndian header of an upstream
// browser_ingest_chunk: seq(u32) + ts(u64) + key(u8) + kind(u8) + len(u32) =
// 18 bytes, followed by exactly `len` payload bytes.
const ingestChunkHeaderLen = 18

// ingestFrameFeedHeaderLen is the fixed BigEndian header of a downstream
// browser_frame_feed: seq(u32) + ts(u64) + len(u32) = 16 bytes, followed by
// exactly `len` JPEG bytes (no keyframe flag — every screencast frame is a
// complete JPEG still).
const ingestFrameFeedHeaderLen = 16

// ingestFeedCap is the per-connection downstream (JPEG feed + control frame)
// buffer depth. The JPEG feed is lossy repaint-driven traffic like the
// viewer screencast: dropping a stale frame on a briefly-slow encoder is
// correct (the next screencast frame supersedes it), so FeedFrame never
// blocks the capture driver. RequestKeyframe's control frame (SF-H2) shares
// this same queue and the same lossy, never-blocking discipline — a dropped
// force_keyframe request is a missed optimization, not a correctness bug
// (the relay's own GOP-cache replay covers the gap either way).
const ingestFeedCap = 64

// forceKeyframeFrameJSON is the fixed JSON text control frame RequestKeyframe
// sends DOWN the ingest connection (SF-H2): a best-effort request that the
// encoder page force a real IDR on its next video encode, by resetting its
// own framesSinceKeyframe counter to 0. See encoder.html's ingest-WS
// onmessage handler for the receiving half.
const forceKeyframeFrameJSON = `{"type":"force_keyframe"}`

// ingestSendItem is one downstream queue entry on an ingestStream's sendCh:
// binary tags the WS opcode writePump must use — true for a
// browser_frame_feed JPEG frame (FeedFrame), false for a JSON text control
// frame (e.g. RequestKeyframe's force_keyframe signal, SF-H2). Mirrors
// browser_ws.go's wsSendItem, which plays the identical role on the
// viewer-facing socket.
type ingestSendItem struct {
	binary bool
	data   []byte
}

// ingestInitDeadline bounds how long the endpoint waits for the first
// (browser_ingest_init) frame before giving up on a connection.
const ingestInitDeadline = 15 * time.Second

// ingestReadDeadline bounds silence on an established ingest connection. The
// encoder page streams continuously; a read-silence this long means a dead
// encoder, so the connection is closed and orchestration (W2-M) re-mints.
const ingestReadDeadline = 120 * time.Second

// Wire values of the envelope's kind(u8) field
// (contracts/components/schemas/BrowserChunkEnvelope.yaml): 0 = video, 1 =
// audio. A single ingest connection multiplexes both video and Opus audio
// chunks over browser_ingest_chunk, so this explicit discriminator byte
// (independent of the key(u8) keyframe flag) is what routes each chunk to
// the video or audio relay path — see decodeChunkEnvelope / classifyChunkKind.
//
// TD3: these are derived from pkg/tools/browser's canonical browser.ChunkKind
// enum (the SAME wire values the relay's EncodeChunk uses) rather than
// re-declaring the 0/1 literals independently, so the two packages can never
// drift apart on what the kind byte means.
const (
	chunkKindVideo byte = byte(browser.KindVideo)
	chunkKindAudio byte = byte(browser.KindAudio)
)

// Video keyframe flag values carried in the envelope's key(u8) field
// (DS-2). Meaningless for an audio chunk, which is always non-key.
const (
	chunkVideoDelta byte = 0
	chunkVideoKey   byte = 1
)

// Audit event names for the ingest lifecycle (FR-024). Local aliases of the
// pkg/audit typed constants — EventBrowserLiveStreamStarted,
// EventBrowserLiveStreamStopped, EventBrowserLiveIngestRejected
// (pkg/audit/events.go) — rather than independently-declared string
// literals: C-F2 (review round 2) flagged the earlier direct-literal form as
// a drift risk (a future rename on either side could silently diverge with
// no compiler signal). Aliased here — not replaced call-site-by-call-site —
// so this file's own eventIngestStreamStarted/Stopped/Rejected identifiers
// (and the tests that reference them) stay stable.
const (
	eventIngestStreamStarted = audit.EventBrowserLiveStreamStarted
	eventIngestStreamStopped = audit.EventBrowserLiveStreamStopped
	eventIngestRejected      = audit.EventBrowserLiveIngestRejected
)

// ingestRejectReason is the machine-readable `reason` recorded on every ingest
// rejection audit entry (FR-024 / SC-009 — explainable rejects).
type ingestRejectReason string

const (
	rejectNonLoopback     ingestRejectReason = "non_loopback"
	rejectBadInit         ingestRejectReason = "bad_init_frame"
	rejectChunkBeforeInit ingestRejectReason = "chunk_before_init"
	rejectAbsentToken     ingestRejectReason = "absent_token"
	rejectUnknownStream   ingestRejectReason = "unknown_or_dead_token"
	rejectDeadToken       ingestRejectReason = "dead_token"
	rejectBadToken        ingestRejectReason = "bad_token"
	rejectMisScoped       ingestRejectReason = "mis_scoped_token"
	rejectDuplicateConn   ingestRejectReason = "duplicate_connection"
	rejectOversizeChunk   ingestRejectReason = "oversize_chunk"
)

var (
	errEnvelopeTooShort    = errors.New("ingest envelope shorter than 18-byte header")
	errEnvelopeLenMismatch = errors.New("ingest envelope declared len != actual payload len")
)

// EncodedChunk is one encoded media chunk decoded off the ingest leg and handed
// to the stream relay (component D). Field-identical to the relay's own chunk
// type by design; the integration adapter (see RegisterCaptureIngest) maps
// between the two so the two parallel components stay file-disjoint.
// TD3: Kind stays a plain string here (rather than the relay's typed
// browser.ChunkKind) because gateway.EncodedChunk is a wire-adjacent
// boundary type constructed directly by test doubles elsewhere in this
// package; classifyChunkKind is the ONLY producer on the production path and
// it only ever returns "video"/"audio" (see browser.ChunkKind.String()).
// The relay-side conversion (videoRelayAdapter.Ingest, browser_stream.go)
// funnels this string through browser.ParseChunkKind + the
// NewVideoChunk/NewAudioChunk constructors, so the illegal-state risk
// (TD1/TD2) is closed at the one real production boundary even though the
// two packages don't share a single Go type end-to-end.
type EncodedChunk struct {
	Seq     uint32 // monotonic per-connection sequence (wraps at 2^32-1)
	TS      uint64 // monotonic capture timestamp (ms), source-injected for glass-to-glass
	Key     bool   // true == keyframe (video only)
	Codec   string // e.g. "avc1.4D4028" (H.264 main), "vp8", "opus"
	Kind    string // "video" | "audio"
	Payload []byte // encoded bytes; owned by the relay (copied off the read buffer)
}

// Relay is the stream relay (component D) dependency. This endpoint depends ONLY
// on this interface — component D's concrete StreamRelay (pkg/tools/browser)
// implements the equivalent; the integration adapter bridges the two chunk types.
type Relay interface {
	Ingest(streamID string, c EncodedChunk)
}

// IngestMux is the minimal HTTP-registration surface this component needs —
// a type alias (not an independently-declared interface, iface lint
// round-2 finding) of rest.go's httpHandlerRegistrar: both declare the exact
// same single method (RegisterHTTPHandler(pattern string, handler
// http.Handler)), so keeping them as two separately-declared identical
// interfaces was redundant. The gateway's channel manager (*channels.Manager)
// already satisfies it via RegisterHTTPHandler — matching how browser_ws.go's
// handler is wired (gateway.go:2091). Keeping it an interface keeps this
// file testable and decoupled from the concrete mux.
type IngestMux = httpHandlerRegistrar

// IngestDeps are the injected dependencies for the ingest endpoint.
type IngestDeps struct {
	// Relay receives decoded encoded chunks (component D). Required in
	// production; if nil, chunks are dropped with an error log (degraded).
	Relay Relay

	// Audit is the audit sink (FR-024). nil disables audit (no-op), matching
	// the package-wide "audit disabled == nil logger" contract.
	Audit *audit.Logger

	// MaxMessageBytes overrides the FR-014 per-chunk bound. <= 0 uses
	// defaultIngestMaxMessageBytes (>= 2 MB).
	MaxMessageBytes int

	// StepDown is the FR-014 encoder step-down hook, invoked (best-effort) when
	// an oversize chunk is rejected so the encoder can drop bitrate/resolution.
	// nil is tolerated (reject-only, no step-down signal).
	StepDown func(streamID string)

	// ConnFailed is the SF-M3 proactive-recovery hook: invoked (best-effort,
	// off its own goroutine — see writePump) when a downstream write to the
	// encoder connection fails, meaning that connection is unreachable. In
	// production this is wired to the SAME re-mint+relaunch recovery
	// handleEncoderDrop already performs for a genuine CRIT-002 ingest drop
	// (the encoder tab's CDP target dying) — a broken write path is just
	// another way the encoder side has gone away, and previously the read
	// loop kept running silently until the 15s liveness timeout eventually
	// noticed. nil is tolerated (write failures still close the connection
	// and log at Warn; recovery then waits on the liveness timeout as before).
	ConnFailed func(streamID string)

	// InitReceived is invoked SYNCHRONOUSLY from the ingest connection's read
	// goroutine after a successful claim, BEFORE the first chunk is read —
	// carrying the codec(s) the encoder page reported it will ACTUALLY
	// produce (BrowserIngestInitFrame). The encoder independently re-probes
	// WebCodecs and may legitimately land on a different codec than the one
	// the orchestrator negotiated from the viewer's caps (e.g. h264-main
	// falls back to vp8); every relayed chunk is stamped with the actual
	// codec, but the viewer-facing browser_stream_init would otherwise still
	// announce the stale negotiated intent — making the SPA configure a
	// decoder for the wrong codec and fail on every chunk. The orchestrator
	// wires this to re-announce browser_stream_init with the actual codec to
	// already-attached viewers (the wire contract explicitly allows a fresh
	// stream_init superseding the prior one). The synchronous, pre-first-chunk
	// call site guarantees the corrected init reaches attached viewers before
	// chunk #1 relays. nil is tolerated (no re-announce).
	InitReceived func(streamID, videoCodec, audioCodec string)
}

// streamState is the token/connection lifecycle of one registered stream.
type streamState int

const (
	streamFresh     streamState = iota // token minted, no connection yet — the only acceptable state
	streamConnected                    // a live ingest connection currently holds the token
	streamDead                         // token consumed/ended — reject any connection (CRIT-002)
)

// ingestToken is the per-stream ingest capability token (FR-013), typed
// distinctly from a plain string (TD4) so a token value can never be
// silently passed where a streamID is expected, or vice versa — both are
// opaque-looking hex/random strings, and without a distinct type a same-type
// mix-up at a call site would compile without complaint.
type ingestToken string

// ingestStream is the per-stream registry entry. All fields are guarded by
// CaptureIngestHandler.mu EXCEPT the codec fields, which are set once under mu
// at claim time and thereafter read only by the owning connection's read loop.
type ingestStream struct {
	token      ingestToken
	state      streamState
	conn       ingestConn          // the single live connection (nil when not connected)
	sendCh     chan ingestSendItem // downstream JPEG feed + control-frame queue (nil when not connected)
	doneCh     chan struct{}       // closed to stop this connection's write pump
	doneClosed bool
	videoCodec string
	audioCodec string

	// firstFrameFedOnce / firstFrameDroppedOnce / firstChunkOnce: diagnostic
	// instrumentation (live-browser-video-streaming bring-up debugging) —
	// logs, once per stream rather than per-frame (which would spam at
	// capture framerate), the first time a captured frame is actually handed
	// to a live encoder connection (FeedFrame), the first time one is dropped
	// because no live encoder connection exists yet, and the first decoded
	// chunk received back from the encoder (handleChunk). Together with
	// startCaptureAt's own logging (browser_stream.go) these mark every hop
	// of the capture->feed->encode->chunk chain so a stalled bring-up shows
	// exactly where it broke. sync.Once is safe for concurrent use, so no
	// extra locking is needed even though FeedFrame and handleChunk run on
	// different goroutines (the capture driver's ackWorker vs. this
	// connection's serveConn read loop).
	firstFrameFedOnce     sync.Once
	firstFrameDroppedOnce sync.Once
	firstChunkOnce        sync.Once
}

// stopPump closes doneCh exactly once. Caller MUST hold CaptureIngestHandler.mu.
func (s *ingestStream) stopPump() {
	if s.doneCh != nil && !s.doneClosed {
		close(s.doneCh)
		s.doneClosed = true
	}
}

// ingestConn is the minimal WebSocket surface the connection logic needs.
// *gorilla/websocket.Conn satisfies it directly; tests inject a fake conn.
type ingestConn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	Close() error
}

// CaptureIngestHandler implements the loopback-only capture-ingest endpoint.
type CaptureIngestHandler struct {
	deps          IngestDeps
	maxChunkBytes int
	upgrader      websocket.Upgrader

	mu      sync.Mutex
	streams map[string]*ingestStream
}

// NewCaptureIngestHandler constructs a handler. Prefer RegisterCaptureIngest,
// which also wires the route.
func NewCaptureIngestHandler(deps IngestDeps) *CaptureIngestHandler {
	maxBytes := deps.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = defaultIngestMaxMessageBytes
	}
	return &CaptureIngestHandler{
		deps:          deps,
		maxChunkBytes: maxBytes,
		streams:       make(map[string]*ingestStream),
		upgrader: websocket.Upgrader{
			// Defense-in-depth against cross-site WS hijack from an
			// agent-navigated page: only a loopback (or empty) Origin may
			// upgrade. The real gate is loopback RemoteAddr + capability token.
			CheckOrigin: ingestCheckOrigin,
		},
	}
}

// RegisterCaptureIngest constructs the handler, registers it on the mux at
// captureIngestPath, and returns it so the stream orchestrator (W2-M) and the
// screencast capture driver (W2-L) can call MintIngestToken / EndStream /
// FeedFrame.
//
// INTEGRATION SEAM (for the lead): wire this exactly where browser_ws.go's
// handler is wired — pkg/gateway/gateway.go around line 2091, right after
//
//	browserWSHandler := newBrowserWSHandler(agentLoop, allowedOrigin)
//	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/ws", browserWSHandler)
//
// add, e.g.:
//
//	captureIngest := gateway.RegisterCaptureIngest(runningServices.ChannelManager, gateway.IngestDeps{
//	    Relay:    relayAdapter,               // bridges gateway.EncodedChunk → browser.EncodedChunk (component D)
//	    Audit:    agentLoop.AuditLogger(),
//	    StepDown: streamOrchestrator.StepDown, // component M
//	})
//	// hand `captureIngest` to the stream orchestrator (W2-M) and capture driver (W2-L).
//
// *channels.Manager already satisfies IngestMux. The relay adapter is a ~5-line
// shim because gateway.EncodedChunk and browser.EncodedChunk are field-identical.
func RegisterCaptureIngest(mux IngestMux, deps IngestDeps) *CaptureIngestHandler {
	h := NewCaptureIngestHandler(deps)
	mux.RegisterHTTPHandler(captureIngestPath, h)
	return h
}

// MintIngestToken registers a stream (or re-mints its token) and returns an
// unguessable per-stream capability token (FR-013). For a NEW stream id this
// emits stream_started (FR-024). For an EXISTING stream id this is a re-mint
// (CRIT-002 drop recovery, driven by W2-M): the OLD token is invalidated, any
// live connection is closed, and the entry is reset to a fresh token — NO
// lifecycle audit (it is the same logical stream, so exactly one start/stop is
// emitted per stream regardless of how many transient drops/re-mints occur).
func (h *CaptureIngestHandler) MintIngestToken(streamID string) ingestToken {
	tok := newIngestToken()

	h.mu.Lock()
	s, ok := h.streams[streamID]
	var oldConn ingestConn
	if !ok {
		h.streams[streamID] = &ingestStream{token: tok, state: streamFresh}
	} else {
		// Re-mint: replace the entry with a fresh struct so any in-flight
		// connection's deferred releaseConn sees it has been superseded and
		// cannot mutate the new state. Old token value is dropped → dead.
		oldConn = s.conn
		s.stopPump()
		h.streams[streamID] = &ingestStream{token: tok, state: streamFresh}
	}
	h.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
	}
	if !ok {
		h.audit(eventIngestStreamStarted, audit.SeverityInfo, "allow", map[string]any{
			"stream_id": streamID,
		})
	}
	return tok
}

// EndStream invalidates a stream's token, closes any live ingest connection, and
// emits stream_stopped (FR-024). Idempotent: ending an unknown stream is a no-op.
func (h *CaptureIngestHandler) EndStream(streamID string) {
	h.mu.Lock()
	s, ok := h.streams[streamID]
	if ok {
		delete(h.streams, streamID)
		s.state = streamDead
		s.stopPump()
	}
	h.mu.Unlock()

	if !ok {
		return
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	h.audit(eventIngestStreamStopped, audit.SeverityInfo, "allow", map[string]any{
		"stream_id": streamID,
	})
}

// FeedFrame pushes one CDP screencast JPEG frame down to the encoder page for
// streamID (browser_frame_feed, FR-001/FR-016). Called by the capture driver
// (W2-L). Non-blocking: if there is no live connection or its feed queue is
// full, the frame is dropped (lossy repaint-driven feed — the next frame
// supersedes it), never stalling the capture driver.
func (h *CaptureIngestHandler) FeedFrame(streamID string, jpeg []byte, seq uint32, ts uint64) {
	h.mu.Lock()
	s := h.streams[streamID]
	var sendCh chan ingestSendItem
	var doneCh chan struct{}
	if s != nil && s.state == streamConnected {
		sendCh = s.sendCh
		doneCh = s.doneCh
	}
	h.mu.Unlock()

	if sendCh == nil {
		// Diagnostic instrumentation: logged once (never per-frame) so a
		// bring-up where the encoder connection isn't up yet when the first
		// captured frames arrive is visible without spamming the log.
		if s != nil {
			s.firstFrameDroppedOnce.Do(func() {
				slog.Info("browser video: frame dropped, no live encoder connection yet",
					"stream_id", streamID, "seq", seq)
			})
		}
		return // no live encoder connection; drop
	}
	data := encodeFrameFeed(seq, ts, jpeg)
	select {
	case sendCh <- ingestSendItem{binary: true, data: data}:
		s.firstFrameFedOnce.Do(func() {
			slog.Info("browser video: first captured frame handed to encoder connection",
				"stream_id", streamID, "seq", seq)
		})
	case <-doneCh:
	default: // queue full → drop (lossy feed)
	}
}

// RequestKeyframe sends a best-effort gateway→encoder-page control frame
// (SF-H2) asking the encoder to force a real IDR on its next video encode —
// the belt-and-suspenders half of the relay's degraded→recover fix
// (stream_relay.go's deliverToViewer): the relay's own GOP-cache replay only
// resends what the encoder ALREADY produced, which cannot repair actual
// encoder-side drift; this asks the encoder itself to mint a new keyframe. A
// no-op (dropped) if there is no live connection for streamID or its feed
// queue is momentarily full — matching FeedFrame's lossy, never-blocking
// discipline; the relay's cache-replay fallback covers the gap either way.
func (h *CaptureIngestHandler) RequestKeyframe(streamID string) {
	h.mu.Lock()
	s := h.streams[streamID]
	var sendCh chan ingestSendItem
	var doneCh chan struct{}
	if s != nil && s.state == streamConnected {
		sendCh = s.sendCh
		doneCh = s.doneCh
	}
	h.mu.Unlock()

	if sendCh == nil {
		return // no live encoder connection; drop
	}
	select {
	case sendCh <- ingestSendItem{data: []byte(forceKeyframeFrameJSON)}:
	case <-doneCh:
	default: // queue full → drop (best-effort control signal)
	}
}

// ServeHTTP enforces loopback-only (FR-012) BEFORE the WebSocket upgrade, then
// upgrades and serves the connection.
func (h *CaptureIngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// FR-012: loopback-only. Reject non-loopback sources before anything else —
	// before the upgrade, before any relay.
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		h.auditReject("", rejectNonLoopback, r.RemoteAddr)
		http.Error(w, "capture-ingest is loopback-only", http.StatusForbidden)
		return
	}

	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("browser-ingest: upgrade failed", "error", err)
		return
	}
	h.serveConn(conn, r.RemoteAddr)
}

// serveConn drives one ingest connection: read+validate the init frame, claim
// the single-connection slot, then relay chunks until the connection closes.
// Split out from ServeHTTP so tests can drive it with a fake conn (no real
// WebSocket / no HTTP hijack).
func (h *CaptureIngestHandler) serveConn(conn ingestConn, remoteAddr string) {
	defer conn.Close()

	// Read ceiling is 2x the logical bound + header so an OVERSIZE chunk is
	// still received and rejected+step-down at the app layer (FR-014) rather
	// than the transport silently killing the connection. This also bounds
	// per-message memory to ~2x the bound.
	conn.SetReadLimit(int64(h.maxChunkBytes*2 + ingestChunkHeaderLen))

	// --- init frame ---
	_ = conn.SetReadDeadline(time.Now().Add(ingestInitDeadline))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		h.auditReject("", rejectBadInit, remoteAddr)
		return
	}
	if mt != websocket.TextMessage {
		// A binary message before init means a chunk arrived with no init.
		h.auditReject("", rejectChunkBeforeInit, remoteAddr)
		return
	}
	var init generated.BrowserIngestInitFrame
	if jerr := json.Unmarshal(data, &init); jerr != nil ||
		init.Type != string(generated.WsFrameTypeBrowserIngestInit) {
		h.auditReject("", rejectBadInit, remoteAddr)
		return
	}
	streamID := init.StreamId
	if init.Token == "" {
		h.auditReject(streamID, rejectAbsentToken, remoteAddr)
		return
	}

	// --- token validation + single-connection claim (atomic under mu) ---
	s, reason := h.claim(streamID, conn, init)
	if reason != "" {
		h.auditReject(streamID, reason, remoteAddr)
		return
	}
	// Accepted. On any exit, mark the token dead (single-use, CRIT-002) and stop
	// the write pump.
	defer h.releaseConn(streamID, s)

	// The encoder's self-reported ACTUAL codec(s) — see IngestDeps.InitReceived.
	// Logged via the omnipus logger (raw slog does not reach the runtime log on
	// every install) so a codec fallback is always visible operator-side.
	logger.InfoCF("browser", "browser-ingest: init claimed",
		map[string]any{
			"stream_id":   streamID,
			"video_codec": init.VideoCodec,
			"has_audio":   init.HasAudio,
		})
	if h.deps.InitReceived != nil {
		h.deps.InitReceived(streamID, init.VideoCodec, s.audioCodec)
	}

	if h.deps.Relay == nil {
		// SF6: Relay is a boot-time wiring dependency — for this handler's
		// entire lifetime it is either always nil or never nil, never a
		// per-chunk condition. Log the misconfiguration ONCE here, per
		// connection, instead of handleChunk re-logging it on every single
		// chunk (a 30fps stream would otherwise emit ~30 Error logs/sec for
		// as long as the encoder keeps pushing chunks into a black hole).
		slog.Error(
			"browser-ingest: no relay configured; every chunk on this connection will be dropped",
			"stream_id",
			streamID,
		)
	}

	go h.writePump(streamID, conn, s.sendCh, s.doneCh)

	// --- chunk relay loop ---
	for {
		_ = conn.SetReadDeadline(time.Now().Add(ingestReadDeadline))
		mt, msg, rerr := conn.ReadMessage()
		if rerr != nil {
			if errors.Is(rerr, websocket.ErrReadLimit) {
				// SF2: a chunk larger than even the read-limit ceiling
				// (2x+header, set via SetReadLimit above) trips gorilla's
				// own frame reader before handleChunk's app-layer length
				// check (FR-014) ever runs. Previously this silently
				// dropped the connection with no audit trail and no
				// encoder step-down signal — route it through the same
				// reject+step-down path an ordinary (<=2x) oversize chunk
				// already gets.
				h.auditReject(streamID, rejectOversizeChunk, remoteAddr)
				if h.deps.StepDown != nil {
					h.deps.StepDown(streamID)
				}
			}
			return // drop; orchestration (W2-M) re-mints + relaunches
		}
		if mt != websocket.BinaryMessage {
			if mt == websocket.TextMessage {
				// TEMP DIAG (removed after root-causing the decode failure).
				// Via the omnipus logger — raw slog does not reach the
				// runtime log on every install.
				logger.InfoCF("browser", "DIAG browser-ingest: text status frame from encoder",
					map[string]any{"stream_id": streamID, "text": string(msg)})
			}
			continue // ignore stray text frames after init
		}
		h.handleChunk(streamID, s, msg)
	}
}

// claim validates the presented token against the registry and atomically
// claims the single-connection slot. On success it transitions the stream to
// streamConnected, records the connection, and provisions the downstream feed
// queue. Returns a non-empty reason on rejection (nothing is relayed).
func (h *CaptureIngestHandler) claim(
	streamID string,
	conn ingestConn,
	init generated.BrowserIngestInitFrame,
) (*ingestStream, ingestRejectReason) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.streams[streamID]
	if !ok {
		// No such stream (never minted, or already ended → token invalidated).
		if h.tokenMatchesOtherLocked(streamID, init.Token) {
			return nil, rejectMisScoped
		}
		return nil, rejectUnknownStream
	}

	// Constant-time token comparison (capability-token confidentiality).
	if subtle.ConstantTimeCompare([]byte(s.token), []byte(init.Token)) != 1 {
		if h.tokenMatchesOtherLocked(streamID, init.Token) {
			return nil, rejectMisScoped
		}
		return nil, rejectBadToken
	}

	switch s.state {
	case streamDead:
		return nil, rejectDeadToken
	case streamConnected:
		// Single connection per token (CRIT-002): a second concurrent
		// connection presenting a live token is rejected; the existing holder
		// is left untouched.
		return nil, rejectDuplicateConn
	case streamFresh:
		s.state = streamConnected
		s.conn = conn
		s.videoCodec = init.VideoCodec
		if init.AudioCodec != nil {
			s.audioCodec = *init.AudioCodec
		}
		s.sendCh = make(chan ingestSendItem, ingestFeedCap)
		s.doneCh = make(chan struct{})
		s.doneClosed = false
		return s, ""
	default:
		return nil, rejectDeadToken
	}
}

// tokenMatchesOtherLocked reports whether `token` is the live token of some
// stream OTHER than exceptID — i.e. the caller presented a token scoped to a
// different stream (mis-scoped, DS-4). Caller MUST hold mu.
func (h *CaptureIngestHandler) tokenMatchesOtherLocked(exceptID, token string) bool {
	for id, s := range h.streams {
		if id == exceptID || s.state == streamDead {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(s.token), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

// releaseConn is deferred once a connection is accepted. It marks the token dead
// (single-use — no same-token reconnect, CRIT-002) and stops the write pump. If
// the entry was superseded by a re-mint or removed by EndStream, only this
// connection's pump is stopped; the new/absent entry is left alone.
func (h *CaptureIngestHandler) releaseConn(streamID string, s *ingestStream) {
	h.mu.Lock()
	if cur, ok := h.streams[streamID]; ok && cur == s && s.state == streamConnected {
		s.state = streamDead
	}
	s.stopPump()
	h.mu.Unlock()
}

// writePump is the single goroutine that writes downstream frame-feed and
// control messages for one connection (gorilla requires all writes from one
// goroutine). It exits when the feed queue is closed or the connection is
// torn down (doneCh).
//
// SF-M3: a write failure means the encoder-side connection is unreachable —
// previously this only logged at slog.Debug and returned, leaving the READ
// loop (serveConn's chunk relay loop, a separate goroutine) running until
// the 15s liveness timeout eventually noticed the encoder had gone silent,
// starving it in the meantime. This now logs at Warn (a write-path failure
// here is the write-side half of a genuine ingest drop, not routine churn)
// and — like a real CRIT-002 ingest drop — proactively invokes ConnFailed
// (deps.ConnFailed, wired in production to the SAME re-mint+relaunch
// recovery handleEncoderDrop already performs when the encoder tab's CDP
// target dies) instead of waiting out the liveness window. Invoked off its
// own goroutine so a (potentially slow, CDP-driven) relaunch never blocks
// this pump's own teardown.
func (h *CaptureIngestHandler) writePump(
	streamID string,
	conn ingestConn,
	sendCh chan ingestSendItem,
	doneCh chan struct{},
) {
	for {
		select {
		case item, ok := <-sendCh:
			if !ok {
				return
			}
			opcode := websocket.BinaryMessage
			if !item.binary {
				opcode = websocket.TextMessage
			}
			if err := conn.WriteMessage(opcode, item.data); err != nil {
				slog.Warn(
					"browser-ingest: downstream write error; connection unreachable",
					"stream_id",
					streamID,
					"error",
					err,
				)
				if h.deps.ConnFailed != nil {
					go h.deps.ConnFailed(streamID)
				}
				return
			}
		case <-doneCh:
			return
		}
	}
}

// handleChunk decodes one upstream browser_ingest_chunk envelope and relays it.
// Enforces FR-014 bounds: a malformed or zero-length chunk is dropped (never
// relayed); an oversize chunk is rejected + signals encoder step-down (never
// fragmented/reassembled).
func (h *CaptureIngestHandler) handleChunk(streamID string, s *ingestStream, msg []byte) {
	seq, ts, keyByte, kindByte, payload, err := decodeChunkEnvelope(msg)
	if err != nil {
		// Malformed framing — never relay a partial/garbled chunk (FR-014).
		// Not an auth rejection; slog only (avoids per-frame audit spam from a
		// broken encoder), and drop.
		slog.Warn("browser-ingest: malformed chunk dropped", "stream_id", streamID, "error", err)
		return
	}
	if len(payload) == 0 {
		// Zero-length payload MUST be rejected, never relayed (FR-014 / DS-2).
		slog.Warn("browser-ingest: empty chunk dropped", "stream_id", streamID)
		return
	}
	if len(payload) > h.maxChunkBytes {
		// FR-014: oversize → reject + step-down; NEVER fragment/reassemble.
		h.audit(eventIngestRejected, audit.SeverityWarn, "deny", map[string]any{
			"stream_id":   streamID,
			"reason":      string(rejectOversizeChunk),
			"chunk_bytes": len(payload),
			"max_bytes":   h.maxChunkBytes,
		})
		if h.deps.StepDown != nil {
			h.deps.StepDown(streamID)
		}
		return
	}

	if h.deps.Relay == nil {
		// Already logged once for this connection in serveConn (SF6) — just
		// drop silently here to avoid re-logging on every chunk.
		return
	}

	// Diagnostic instrumentation: the first chunk actually decoded off the
	// encoder's WS connection — the final hop in the capture->feed->encode
	// chain (see startCaptureAt's doc comment, browser_stream.go). Logged
	// once per stream, before the relay hand-off below.
	s.firstChunkOnce.Do(func() {
		slog.Info("browser video: first encoded chunk received from encoder",
			"stream_id", streamID, "seq", seq, "bytes", len(payload))
	})

	kind, codec, key := classifyChunkKind(kindByte, keyByte, s)
	// Copy the payload off the read buffer — the relay's GOP cache retains
	// chunks, so it must own an independent slice.
	buf := make([]byte, len(payload))
	copy(buf, payload)
	h.deps.Relay.Ingest(streamID, EncodedChunk{
		Seq:     seq,
		TS:      ts,
		Key:     key,
		Codec:   codec,
		Kind:    kind,
		Payload: buf,
	})
}

// classifyChunkKind maps the envelope's kind(u8) discriminator + key(u8)
// keyframe flag + the connection's init-declared codecs to (kind, codec,
// key). Any kind byte other than chunkKindAudio is treated as video (the
// default, matching decodeChunkEnvelope's tolerance of an out-of-range
// value rather than dropping the chunk outright). An audio chunk is never a
// keyframe (Opus packets are never classified key/delta), regardless of
// what the key(u8) byte carries.
func classifyChunkKind(kindByte, keyByte byte, s *ingestStream) (kind, codec string, key bool) {
	if kindByte == chunkKindAudio {
		codec = s.audioCodec
		if codec == "" {
			codec = "opus"
		}
		return browser.KindAudio.String(), codec, false
	}
	return browser.KindVideo.String(), s.videoCodec, keyByte == chunkVideoKey
}

// audit emits one audit record (FR-024). No-op when audit is disabled (nil
// logger). `decision` (allow|deny) is carried in fields for explainability.
func (h *CaptureIngestHandler) audit(
	event string,
	sev audit.Severity,
	decision string,
	fields map[string]any,
) {
	if h.deps.Audit == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	if decision != "" {
		fields["decision"] = decision
	}
	audit.Emit(context.Background(), h.deps.Audit, event, sev, fields)
}

// auditReject records one ingest-auth rejection (FR-024). Every rejection path
// funnels through here so no rejection is ever silent. Also feeds FR-019's
// "ingest-auth-reject count" metric (Test 28) — same single funnel point, so
// no rejection reason is ever missed there either.
func (h *CaptureIngestHandler) auditReject(
	streamID string,
	reason ingestRejectReason,
	remoteAddr string,
) {
	globalBrowserVideoMetrics().IncIngestAuthReject(string(reason))
	h.audit(eventIngestRejected, audit.SeverityWarn, "deny", map[string]any{
		"stream_id":   streamID,
		"reason":      string(reason),
		"remote_addr": remoteAddr,
	})
}

// newIngestToken mints a 256-bit unguessable capability token (FR-013).
func newIngestToken() ingestToken {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic; a predictable token would defeat
		// the capability model, so fail closed with an unusable value that no
		// mint result can accidentally match.
		panic(fmt.Sprintf("browser-ingest: crypto/rand failed: %v", err))
	}
	return ingestToken(hex.EncodeToString(b[:]))
}

// decodeChunkEnvelope parses the fixed BigEndian upstream chunk envelope:
// seq(u32) | ts(u64) | key(u8) | kind(u8) | len(u32) | payload[len]. It
// returns an error if the message is shorter than the header or the declared
// len does not equal the actual payload length (framing corruption — never
// relayed).
func decodeChunkEnvelope(
	msg []byte,
) (seq uint32, ts uint64, keyByte byte, kindByte byte, payload []byte, err error) {
	if len(msg) < ingestChunkHeaderLen {
		return 0, 0, 0, 0, nil, errEnvelopeTooShort
	}
	seq = binary.BigEndian.Uint32(msg[0:4])
	ts = binary.BigEndian.Uint64(msg[4:12])
	keyByte = msg[12]
	kindByte = msg[13]
	declaredLen := binary.BigEndian.Uint32(msg[14:18])
	payload = msg[ingestChunkHeaderLen:]
	if uint32(len(payload)) != declaredLen {
		return 0, 0, 0, 0, nil, errEnvelopeLenMismatch
	}
	return seq, ts, keyByte, kindByte, payload, nil
}

// encodeFrameFeed builds a downstream browser_frame_feed binary message:
// seq(u32) | ts(u64) | len(u32) | jpeg[len] (BigEndian, no keyframe flag).
func encodeFrameFeed(seq uint32, ts uint64, jpeg []byte) []byte {
	buf := make([]byte, ingestFrameFeedHeaderLen+len(jpeg))
	binary.BigEndian.PutUint32(buf[0:4], seq)
	binary.BigEndian.PutUint64(buf[4:12], ts)
	binary.BigEndian.PutUint32(buf[12:16], uint32(len(jpeg)))
	copy(buf[ingestFrameFeedHeaderLen:], jpeg)
	return buf
}

// isLoopbackRemoteAddr reports whether an http.Request.RemoteAddr is a loopback
// source (FR-012). Handles "ip:port", bare IPs, and the literal "localhost".
func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ingestCheckOrigin allows only an empty or loopback Origin to upgrade — a
// cheap defense-in-depth against cross-site WS hijack from an agent-navigated
// page. The primary gate is loopback RemoteAddr (FR-012) + capability token.
func ingestCheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
