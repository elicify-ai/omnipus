package browser

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ADR-044 (Live-Browser Video Streaming, R7) — this file is the WebCodecs
// relay + GOP cache (Wave 1 / component D), added ALONGSIDE the JPEG-era
// LiveViewRegistry/LiveView above, not in place of it (F-02: the JPEG
// screencast path stays live until the video path is reachable and the
// contract cutover — M-10 — actually removes browser_screencast). See
// docs/internal/specs/live-browser-video-streaming-spec.md FR-003, FR-004,
// FR-015, FR-018, MAJ-005, §Go Memory Budget.

const (
	// relayGOPMaxDeltas bounds the number of delta chunks retained in a
	// stream's GOP cache after its most recent keyframe (§Go Memory Budget,
	// AW-3: N=30, sized against a ≤4KB/delta worst case). A new keyframe
	// resets this list to empty (FR-003).
	relayGOPMaxDeltas = 30

	// relayMaxConcurrentStreams bounds the number of streams the relay keeps
	// a GOP cache for at once (FR-003's "max concurrent live-stream count").
	// Matches §Go Memory Budget's AW-3 cap of 8.
	relayMaxConcurrentStreams = 8

	// relayMaxAggregateCacheBytes bounds the total GOP-cache memory across
	// every stream (FR-003's "aggregate memory ceiling"). §Go Memory Budget
	// derives ≈2.2MB worst-case for 8 streams × ~270KB; this constant adds
	// headroom for larger-than-typical keyframes while staying well inside
	// the overall <10MB Go-process budget (NFR-3) alongside per-viewer send
	// queues (owned by the gateway WS layer, not this relay).
	relayMaxAggregateCacheBytes = 4 * 1024 * 1024 // 4 MB

	// relayChunkOverheadBytes is a small fixed per-chunk bookkeeping estimate
	// (struct + slice header + map entry) added to len(Payload) when
	// accounting a chunk against the aggregate cache ceiling. Deliberately
	// coarse — this is a ceiling-enforcement heuristic, not an exact
	// accounting requirement.
	relayChunkOverheadBytes = 32
)

// ChunkKind discriminates a video chunk from an audio chunk on the
// relay/viewer path (TD1/TD2). It replaces a bare `Kind string` field that
// used to be compared via `== "audio"`: a typo, an unexpected casing, or the
// zero value would silently be treated as video with no compile-time
// signal, and nothing stopped a caller from constructing the
// contract-forbidden combination of an audio chunk carrying the video
// keyframe flag (Key=true). ChunkKind is a closed, two-valued uint8 enum —
// the only strings that classify as audio are handled by ParseChunkKind, and
// the only way to mint an EncodedChunk that is unambiguously audio is
// NewAudioChunk, which has no Key parameter at all.
type ChunkKind uint8

const (
	// KindVideo is the wire envelope's kind(u8)=0 value
	// (BrowserChunkEnvelope.yaml) and ChunkKind's zero value — matching the
	// pre-existing "anything that isn't recognized as audio is video"
	// tolerant-default behavior this type preserves.
	KindVideo ChunkKind = 0
	// KindAudio is the wire envelope's kind(u8)=1 value.
	KindAudio ChunkKind = 1
)

// String renders k as the human-readable label used in metrics/logs
// ("video"/"audio") — the SAME strings the pre-typed field used to carry, so
// existing metric label values (kind="video"/kind="audio") are unaffected.
func (k ChunkKind) String() string {
	if k == KindAudio {
		return "audio"
	}
	return "video"
}

// ParseChunkKind maps a "video"/"audio" label (as carried across the
// gateway/relay package boundary, where the ingest endpoint's EncodedChunk
// still carries Kind as a plain string) to a ChunkKind. Only the exact
// string "audio" classifies as KindAudio; every other value — a typo, an
// unexpected casing, an empty/zero value — classifies as KindVideo. This is
// the SAME tolerant-default behavior EncodeChunk's old `c.Kind == "audio"`
// check already had; naming it here makes the default explicit and gives it
// exactly one call site instead of letting the comparison re-appear ad hoc.
func ParseChunkKind(s string) ChunkKind {
	if s == "audio" {
		return KindAudio
	}
	return KindVideo
}

// EncodedChunk is one encoded media chunk on the relay/viewer path — the
// video/audio analog of the JPEG-era LiveFrame above, produced by the
// encoder page (component G) and carried from the capture ingest endpoint
// (component E) through this relay to attached viewers (component F/the
// gateway WS write pump). Prefer NewVideoChunk/NewAudioChunk over a raw
// struct literal (TD1/TD2): they are the only construction path that makes
// an illegal audio+key=true chunk impossible to express.
type EncodedChunk struct {
	Seq   uint32
	TS    uint64 // MILLISECONDS (the wire unit — matches the encoder page & contract)
	Key   bool   // keyframe flag; MUST be false for any chunk with Kind==KindAudio
	Codec string // e.g. "avc1.4D4028", "vp8", "opus"
	Kind  ChunkKind
	// Payload is the raw codec bytes (NO tag byte — the ingest endpoint
	// strips its transport tag before calling Ingest).
	Payload []byte
}

// NewVideoChunk constructs a video EncodedChunk. isKey reports whether c is
// a keyframe (the GOP cache's appendToCacheLocked / deliverToViewer's
// recovery logic both depend on this flag being accurate).
func NewVideoChunk(seq uint32, ts uint64, isKey bool, codec string, payload []byte) EncodedChunk {
	return EncodedChunk{Seq: seq, TS: ts, Key: isKey, Codec: codec, Kind: KindVideo, Payload: payload}
}

// NewAudioChunk constructs an audio EncodedChunk. There is deliberately NO
// key parameter: Opus packets are never classified key/delta
// (BrowserChunkEnvelope.yaml), so this constructor makes the illegal
// audio+key=true combination (TD1/TD2) impossible to express through it.
func NewAudioChunk(seq uint32, ts uint64, codec string, payload []byte) EncodedChunk {
	return EncodedChunk{Seq: seq, TS: ts, Key: false, Codec: codec, Kind: KindAudio, Payload: payload}
}

// Viewer is the relay's view of one attached WS viewer connection —
// satisfied by a small adapter around the gateway's browserWSConn (component
// F). Kept minimal and dependency-free (no import of pkg/gateway) so this
// package never depends on the gateway package.
type Viewer interface {
	// SendChunk enqueues one already wire-encoded chunk (see EncodeChunk)
	// for delivery to this viewer's connection. Implementations MUST be
	// non-blocking (FR-004's "a full-queue viewer MUST have its chunks
	// dropped in isolation" — the same drop-on-full, latest-wins discipline
	// browserWSConn.sendChunk/sendFrame already use in
	// pkg/gateway/browser_ws.go) and MUST report whether the chunk was
	// actually enqueued (true) or dropped because the outbound queue was
	// full (false). The relay uses that signal to detect an isolated
	// slow-viewer drop and force a fresh keyframe on this viewer's next
	// delivery instead of resuming on a delta it may have a gap before (DS-3
	// row 3) — see deliverToViewer.
	SendChunk(chunk []byte) bool

	// Authorized reports whether this viewer may attach to streamID. The
	// relay calls this BEFORE any GOP replay (FR-015, Test 27) — an
	// unauthorized viewer must never see a single cached frame, let alone a
	// live one.
	Authorized(streamID string) bool

	// Failed notifies the viewer that streamID's capture has failed/closed
	// (Test 7) so it can move to the unavailable state. Same
	// non-blocking-sink contract as FrameSink/StatusSink/ControlSink
	// elsewhere in this package: invoked synchronously by the relay with no
	// relay lock held, so a slow consumer must hand off to its own buffered
	// channel exactly as the gateway's per-connection sendCh already does.
	Failed(streamID string)
}

// relayViewerState is the relay's per-(stream, viewer) bookkeeping. degraded
// tracks whether the last chunk offered to this viewer was dropped (SendChunk
// returned false) — see deliverToViewer's doc comment for how this drives the
// DS-3 row 3 "force a fresh keyframe on recovery" rule. Uses atomic.Bool
// rather than the stream's mutex because it is read/written only from
// deliverToViewer, which already runs with no relay/stream lock held (the
// viewer fan-out snapshot in Ingest releases the stream lock first — mirrors
// LiveView.deliver's "snapshot under lock, invoke without" convention above).
type relayViewerState struct {
	degraded atomic.Bool
}

// relayStream holds one stream's GOP cache, attached viewers, and eviction
// bookkeeping. Safe for concurrent use via mu.
type relayStream struct {
	mu       sync.Mutex
	streamID string

	// maxDeltas is copied from the owning StreamRelay at creation so tests
	// can construct relays with a small N (newStreamRelayWithLimits,
	// live_relay_test.go) without a package-level var swap.
	maxDeltas int

	keyframe   *EncodedChunk // most recent keyframe, or nil if none ingested yet
	deltas     []EncodedChunk
	cacheBytes int // approx bytes retained (keyframe + deltas), for the aggregate ceiling

	viewers map[Viewer]*relayViewerState

	// attaching counts in-flight StreamRelay.Attach calls for this stream
	// (MAJ-005: a stream must never be evicted while a viewer is currently
	// attaching, even before it's registered in viewers).
	attaching int

	// failed is set by MarkFailed (Test 7) and is terminal for this stream's
	// lifecycle: further Ingest calls are dropped and further Attach calls
	// are rejected. The stream orchestrator (component M) is responsible for
	// minting a fresh stream ID on relaunch rather than resurrecting a
	// failed one.
	failed bool

	// lastViewed is updated whenever this stream has (or had) an attached
	// viewer — the LRU key for idle-stream eviction (MAJ-005: idle streams
	// are evicted least-recently-viewed first).
	lastViewed time.Time
}

// StreamRelay relays encoded video/audio chunks from the capture ingest path
// (component E) to attached WS viewers (component F), maintaining a bounded
// per-stream GOP cache (keyframe + ≤ N deltas) so a newly attached, AUTHORIZED
// viewer catches up instantly instead of waiting for the next real keyframe
// (FR-003, Test 4). Safe for concurrent use.
type StreamRelay struct {
	mu            sync.Mutex
	streams       map[string]*relayStream
	maxStreams    int
	maxCacheBytes int
	maxDeltas     int
}

// NewStreamRelay constructs a StreamRelay using the production caps from
// §Go Memory Budget (AW-3): relayMaxConcurrentStreams concurrent streams,
// relayMaxAggregateCacheBytes aggregate GOP-cache ceiling, relayGOPMaxDeltas
// deltas retained per stream after its keyframe.
func NewStreamRelay() *StreamRelay {
	return newStreamRelayWithLimits(relayMaxConcurrentStreams, relayMaxAggregateCacheBytes, relayGOPMaxDeltas)
}

// newStreamRelayWithLimits is NewStreamRelay with caller-supplied caps —
// used by tests (live_relay_test.go, same package) to exercise
// eviction/GOP-trim behavior deterministically without needing dozens of
// streams or megabyte-sized payloads to cross the production defaults.
func newStreamRelayWithLimits(maxStreams, maxCacheBytes, maxDeltas int) *StreamRelay {
	return &StreamRelay{
		streams:       make(map[string]*relayStream),
		maxStreams:    maxStreams,
		maxCacheBytes: maxCacheBytes,
		maxDeltas:     maxDeltas,
	}
}

// StreamCount reports the number of streams currently GOP-cached by this
// relay (live or idle) — exposed for the observability surface FR-019
// requires ("live-stream count", Test 28), which component M wires up.
func (r *StreamRelay) StreamCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// getOrCreateLocked returns the existing stream for streamID, or creates one
// — evicting idle streams first to stay within the configured caps (FR-003)
// before inserting the new entry. Must be called with r.mu held.
func (r *StreamRelay) getOrCreateLocked(streamID string) *relayStream {
	if s, ok := r.streams[streamID]; ok {
		return s
	}
	// Make room for the stream about to be inserted.
	r.evictLocked(r.maxStreams-1, r.maxCacheBytes)
	s := &relayStream{
		streamID:   streamID,
		maxDeltas:  r.maxDeltas,
		viewers:    make(map[Viewer]*relayViewerState),
		lastViewed: time.Now(),
	}
	r.streams[streamID] = s
	return s
}

// getOrCreateStreamForIngest returns (creating if necessary) the stream for
// streamID, for use by Ingest. Does not reserve it against eviction — an
// idle, viewerless stream created here is a perfectly valid eviction
// candidate on a later Ingest/Attach if nobody ever attaches to it.
func (r *StreamRelay) getOrCreateStreamForIngest(streamID string) *relayStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getOrCreateLocked(streamID)
}

// getOrCreateStreamForAttach returns (creating if necessary) the stream for
// streamID AND reserves it against eviction for the duration of the caller's
// Attach call, by incrementing attaching under the SAME r.mu critical section
// used to look up/create the stream. This closes a narrow race where a
// concurrent evictLocked call (from another goroutine's Ingest/Attach on a
// different, new stream) could otherwise select this brand-new,
// still-viewerless stream as an idle eviction victim in the window between
// lookup/creation and the caller marking it "attaching" — MAJ-005 requires a
// currently-attaching stream to never be evicted, so that reservation must be
// atomic with the lookup, not a separate later step.
func (r *StreamRelay) getOrCreateStreamForAttach(streamID string) *relayStream {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.getOrCreateLocked(streamID)
	s.mu.Lock()
	s.attaching++
	s.mu.Unlock()
	return s
}

// lookupStream returns the stream for streamID without creating one — used
// by Detach/MarkFailed, which have nothing to do for a stream nobody has
// ever ingested into or attached to.
func (r *StreamRelay) lookupStream(streamID string) (*relayStream, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[streamID]
	return s, ok
}

// aggregateBytesLocked sums cacheBytes across every stream. Must be called
// with r.mu held.
func (r *StreamRelay) aggregateBytesLocked() int {
	total := 0
	for _, s := range r.streams {
		s.mu.Lock()
		total += s.cacheBytes
		s.mu.Unlock()
	}
	return total
}

// leastRecentlyViewedIdleLocked returns the streamID of the IDLE (zero
// attached viewers, zero in-flight attaches) stream with the OLDEST
// lastViewed timestamp, or "" if no idle stream exists. Must be called with
// r.mu held.
func (r *StreamRelay) leastRecentlyViewedIdleLocked() string {
	var victim string
	var oldest time.Time
	for id, s := range r.streams {
		s.mu.Lock()
		idle := len(s.viewers) == 0 && s.attaching == 0
		lastViewed := s.lastViewed
		s.mu.Unlock()
		if !idle {
			continue
		}
		if victim == "" || lastViewed.Before(oldest) {
			victim = id
			oldest = lastViewed
		}
	}
	return victim
}

// evictLocked evicts idle streams — oldest lastViewed first — until the
// stream count is at most maxStreams AND the aggregate cache size is at most
// maxBytes bytes, or until no idle stream remains to evict. MAJ-005: a
// stream with a live or currently-attaching viewer is NEVER evicted, even if
// that means the ceilings stay breached afterward — correctness for an
// active viewer always outranks the ceiling (an accepted, documented
// trade-off; see §Go Memory Budget). Must be called with r.mu held.
func (r *StreamRelay) evictLocked(maxStreams, maxBytes int) {
	for len(r.streams) > maxStreams || r.aggregateBytesLocked() > maxBytes {
		victim := r.leastRecentlyViewedIdleLocked()
		if victim == "" {
			return
		}
		delete(r.streams, victim)
		logger.DebugCF("browser", "stream relay: evicted idle stream under cache pressure", map[string]any{
			"stream_id": victim,
		})
	}
}

// Attach binds v to streamID's stream, authorizing it BEFORE any GOP replay
// (FR-015, Test 27 — an unauthorized viewer must never see a single cached
// frame). On success, it flushes the cached keyframe followed by its
// retained deltas (Test 4) to v via v.SendChunk WHILE s.mu IS STILL HELD
// (CR2): SendChunk is contractually non-blocking (see the Viewer doc
// comment above), so this never stalls the stream lock, and holding the
// lock across the flush guarantees every replay chunk reaches v strictly
// BEFORE s.mu is released and a concurrently in-flight Ingest call can
// enqueue a live chunk to v. Without that ordering guarantee a live delta
// could reach v's queue before (or interleaved with) a replay the caller
// sent afterward, corrupting decode (a delta depends on its keyframe
// arriving first).
//
// The replayed chunks are ALSO returned, for introspection/tests and
// backward-compat with existing callers — but by the time Attach returns,
// they have ALREADY been delivered to v. Callers MUST NOT resend them.
func (r *StreamRelay) Attach(streamID string, v Viewer) ([]EncodedChunk, error) {
	if v == nil {
		return nil, fmt.Errorf("stream relay: viewer is required")
	}
	if streamID == "" {
		return nil, fmt.Errorf("stream relay: stream id is required")
	}
	// Gate BEFORE the stream is even looked up/created and BEFORE any GOP
	// replay is computed — an unauthorized viewer must not be able to infer
	// anything about the stream's existence or cache contents (FR-015).
	if !v.Authorized(streamID) {
		return nil, fmt.Errorf("stream relay: viewer not authorized for stream %q", streamID)
	}

	s := r.getOrCreateStreamForAttach(streamID)
	defer func() {
		s.mu.Lock()
		s.attaching--
		s.mu.Unlock()
	}()

	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return nil, fmt.Errorf("stream relay: stream %q has failed", streamID)
	}
	vst := &relayViewerState{}
	s.viewers[v] = vst
	s.lastViewed = time.Now()
	replay := s.replayLocked()
	for _, c := range replay {
		if !v.SendChunk(EncodeChunk(c)) {
			// A replay chunk was dropped on an already-full queue — mark
			// degraded so the next live Ingest delivery recovers this
			// viewer with a fresh keyframe (deliverToViewer's DS-3 row 3
			// rule) instead of leaving it stuck with an incomplete replay.
			vst.degraded.Store(true)
			activeBrowserMetricsRecorder.IncViewerDrop()
		}
	}
	s.mu.Unlock()

	return replay, nil
}

// Detach unbinds v from streamID's stream. A no-op if the stream doesn't
// exist or v was never attached.
func (r *StreamRelay) Detach(streamID string, v Viewer) {
	s, ok := r.lookupStream(streamID)
	if !ok {
		return
	}
	s.mu.Lock()
	delete(s.viewers, v)
	s.lastViewed = time.Now()
	s.mu.Unlock()
}

// MarkFailed marks streamID's capture as failed/closed (Test 7 — the ingest
// side signals the stream failed or the ingest connection closed) and
// notifies every currently-attached viewer via Failed so it can move to the
// unavailable state. Idempotent — a second call on an already-failed stream
// is a no-op (no duplicate notifications). A no-op if streamID has never been
// ingested/attached.
func (r *StreamRelay) MarkFailed(streamID string) {
	s, ok := r.lookupStream(streamID)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return
	}
	s.failed = true
	viewers := make([]Viewer, 0, len(s.viewers))
	for v := range s.viewers {
		viewers = append(viewers, v)
	}
	s.mu.Unlock()

	logger.WarnCF("browser", "stream relay: stream marked failed", map[string]any{
		"stream_id":    streamID,
		"viewer_count": len(viewers),
	})
	for _, v := range viewers {
		v.Failed(streamID)
	}
}

// Ingest appends c to streamID's GOP cache and fans it out to every
// currently-attached viewer of that stream, dropping in isolation for any
// viewer whose queue is full (FR-004) — reusing SendChunk's return value
// (see the Viewer doc comment) rather than a second queue owned by this
// relay. A no-op (chunk discarded, not cached, not forwarded) if the stream
// has already been MarkFailed — the capture side must mint a fresh stream to
// resume, not keep pushing into a dead one.
func (r *StreamRelay) Ingest(streamID string, c EncodedChunk) {
	s := r.getOrCreateStreamForIngest(streamID)

	s.mu.Lock()
	if s.failed {
		s.mu.Unlock()
		return
	}
	s.appendToCacheLocked(c)
	cachedKeyframe := s.keyframe
	viewers := make(map[Viewer]*relayViewerState, len(s.viewers))
	for v, st := range s.viewers {
		viewers[v] = st
	}
	if len(viewers) > 0 {
		s.lastViewed = time.Now()
	}
	s.mu.Unlock()

	wire := EncodeChunk(c)
	for v, st := range viewers {
		deliverToViewer(v, st, c, wire, cachedKeyframe)
	}

	// Re-check the aggregate ceilings after every ingest — a long-running
	// stream's own GOP cache is bounded by maxDeltas already, so this mainly
	// catches aggregate pressure from many concurrently-active streams; kept
	// here (not only on stream creation) as defense in depth.
	r.mu.Lock()
	r.evictLocked(r.maxStreams, r.maxCacheBytes)
	r.mu.Unlock()
}

// deliverToViewer sends chunk c (already wire-encoded as wire) to v, applying
// the DS-3 row 3 "keyframe-on-recover" rule: once a chunk to v has been
// dropped (SendChunk returned false, tracked via st.degraded), the NEXT
// delivery attempt sends the CACHED keyframe instead of whatever delta
// happens to be next in line — a viewer that missed a chunk cannot correctly
// decode a later delta anyway (every delta depends on its keyframe), so
// resuming on the next delta would only extend the corruption, while a fresh
// keyframe recovers it immediately. A successfully delivered keyframe always
// clears degraded, whether it arrived this way (recovery) or as the next
// real chunk from the encoder. If the viewer is degraded but nothing is
// cached yet (cachedKeyframe == nil — shouldn't happen once any keyframe has
// been ingested), falls through and delivers c normally rather than
// delivering nothing.
func deliverToViewer(v Viewer, st *relayViewerState, c EncodedChunk, wire []byte, cachedKeyframe *EncodedChunk) {
	if !c.Key && st.degraded.Load() && cachedKeyframe != nil {
		if v.SendChunk(EncodeChunk(*cachedKeyframe)) {
			st.degraded.Store(false)
		} else {
			// FR-019 "per-viewer drop rate": the recovery keyframe itself was
			// dropped (queue still full) — still a real drop event.
			activeBrowserMetricsRecorder.IncViewerDrop()
		}
		return
	}
	if v.SendChunk(wire) {
		if c.Key {
			st.degraded.Store(false)
		}
		return
	}
	st.degraded.Store(true)
	// FR-019 "per-viewer drop rate" (Test 28): a full-queue viewer had this
	// chunk dropped in isolation (FR-004) — count it.
	activeBrowserMetricsRecorder.IncViewerDrop()
}

// appendToCacheLocked adds c to s's GOP cache (FR-003): a new keyframe
// replaces the cache outright (Key=true resets the delta list to empty — a
// stale delta from before this keyframe is unusable once a fresh keyframe
// supersedes it); a delta appends, trimming the oldest once more than
// maxDeltas are retained. A delta arriving before any keyframe has ever been
// ingested is discarded from the cache (it would be an orphan a fresh replay
// could never decode) — it is still forwarded live to attached viewers by
// Ingest, just not cached for future replay. Must be called with s.mu held.
//
// TD1: the GOP cache is video-only. An audio chunk must never occupy the
// video keyframe slot or a delta slot, and must never count against
// maxDeltas/cacheBytes — it fans out live via Ingest's viewer loop and is
// simply not retained here.
func (s *relayStream) appendToCacheLocked(c EncodedChunk) {
	if c.Kind != KindVideo {
		return
	}
	if c.Key {
		cp := c
		s.keyframe = &cp
		s.deltas = s.deltas[:0]
		s.cacheBytes = chunkApproxSize(c)
		return
	}
	if s.keyframe == nil {
		return
	}
	s.deltas = append(s.deltas, c)
	s.cacheBytes += chunkApproxSize(c)

	maxDeltas := s.maxDeltas
	if maxDeltas <= 0 {
		maxDeltas = relayGOPMaxDeltas
	}
	for len(s.deltas) > maxDeltas {
		s.cacheBytes -= chunkApproxSize(s.deltas[0])
		s.deltas = s.deltas[1:]
	}
}

// replayLocked returns the cached keyframe (if any) followed by all cached
// deltas, in that order (Test 4: "replays keyframe first, then deltas"), or
// nil if no keyframe has ever been ingested for this stream. Returns a fresh
// copy so the caller's slice is never aliased to s.deltas' backing array,
// which appendToCacheLocked can reslice/truncate concurrently after this call
// returns. Must be called with s.mu held.
func (s *relayStream) replayLocked() []EncodedChunk {
	if s.keyframe == nil {
		return nil
	}
	out := make([]EncodedChunk, 0, 1+len(s.deltas))
	out = append(out, *s.keyframe)
	out = append(out, s.deltas...)
	return out
}

// chunkApproxSize estimates c's contribution to the aggregate cache ceiling:
// its payload size plus a small fixed bookkeeping overhead. Deliberately
// coarse (a ceiling-enforcement heuristic, not exact accounting).
func chunkApproxSize(c EncodedChunk) int {
	return len(c.Payload) + relayChunkOverheadBytes
}

// relayChunkEnvelopeHeaderBytes is the fixed header size EncodeChunk writes
// before the payload: seq (u32) + ts (u64) + key (u8) + kind (u8) + len (u32)
// = 18 bytes. Matches contracts/components/schemas/BrowserChunkEnvelope.yaml
// exactly — the SAME layout the encoder page (component G) and the viewer WS
// write path (component F) both use; there is no fragment field (M-4).
const relayChunkEnvelopeHeaderBytes = 4 + 8 + 1 + 1 + 4

// EncodeChunk serializes c into the wire envelope carried by the viewer WS's
// binary browser_video_chunk/browser_audio_chunk frames (and the ingest
// endpoint's browser_ingest_chunk, same envelope — FR-002/011/012/017,
// DS-2): an 18-byte big-endian header — seq:u32, ts:u64, key:u8, kind:u8,
// len:u32 — followed by exactly len raw payload bytes, with NO fragment
// field (chunks are always sent whole in one binary WS message — M-4). The
// kind byte is c.Kind's own wire value (KindVideo=0, KindAudio=1 —
// TD1/TD2: c.Kind is now a closed ChunkKind enum, so there is no longer a
// string comparison here that a typo/casing/zero-value could silently fall
// through). Exported (not package-private) because the viewer send path
// lives in pkg/gateway (component F/E), a different package, and must
// produce byte-for-byte the same envelope this relay computes
// cache-eviction/recovery decisions against.
func EncodeChunk(c EncodedChunk) []byte {
	buf := make([]byte, relayChunkEnvelopeHeaderBytes+len(c.Payload))
	binary.BigEndian.PutUint32(buf[0:4], c.Seq)
	binary.BigEndian.PutUint64(buf[4:12], c.TS)
	if c.Key {
		buf[12] = 1
	} else {
		buf[12] = 0
	}
	buf[13] = byte(c.Kind)
	binary.BigEndian.PutUint32(buf[14:18], uint32(len(c.Payload))) //nolint:gosec // payload length is bound-checked well below 2^32 by the ingest endpoint's max-message-bytes gate (FR-014)
	copy(buf[18:], c.Payload)
	return buf
}
