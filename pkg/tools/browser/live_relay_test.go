package browser

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
)

// fakeViewer is a hermetic Viewer (see stream_relay.go) for relay tests — no
// real network connection, no gateway package dependency. SendChunk records
// every chunk it actually accepts; setting full(true) simulates a backed-up
// outbound queue exactly like browserWSConn.sendChunk's own drop-on-full
// select does in production.
type fakeViewer struct {
	mu            sync.Mutex
	id            string
	authorized    bool
	full          bool
	received      [][]byte
	failedStreams []string
}

func newFakeViewer(id string) *fakeViewer {
	return &fakeViewer{id: id, authorized: true}
}

func (f *fakeViewer) SendChunk(chunk []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.full {
		return false
	}
	cp := make([]byte, len(chunk))
	copy(cp, chunk)
	f.received = append(f.received, cp)
	return true
}

func (f *fakeViewer) Authorized(_ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorized
}

func (f *fakeViewer) Failed(streamID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedStreams = append(f.failedStreams, streamID)
}

func (f *fakeViewer) setFull(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.full = v
}

func (f *fakeViewer) setAuthorized(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorized = v
}

func (f *fakeViewer) chunkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

// allReceived returns a defensive copy of every chunk this viewer has
// received so far, in delivery order — used by tests to inspect the CR2
// replay Attach now flushes internally (Attach no longer returns the replay
// list; it delivers it via SendChunk before returning).
func (f *fakeViewer) allReceived() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.received))
	copy(out, f.received)
	return out
}

func (f *fakeViewer) wasNotifiedFailed(streamID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.failedStreams {
		if id == streamID {
			return true
		}
	}
	return false
}

// Test 4 (FR-003, US-3): a fresh/piggybacking viewer's GOP replay is the
// cached keyframe first, then its deltas in arrival order. CR2: Attach
// flushes the replay to the viewer itself (via SendChunk) rather than
// returning it for the caller to resend, so this asserts against
// v.allReceived() — the wire-encoded chunks the viewer actually got.
func TestStreamRelay_GOPCache_ReplaysKeyframeFirst(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	r.Ingest(
		streamID,
		EncodedChunk{
			Seq:     0,
			TS:      0,
			Key:     true,
			Codec:   "vp8",
			Kind:    KindVideo,
			Payload: []byte("keyframe"),
		},
	)
	r.Ingest(
		streamID,
		EncodedChunk{
			Seq:     1,
			TS:      33,
			Key:     false,
			Codec:   "vp8",
			Kind:    KindVideo,
			Payload: []byte("delta1"),
		},
	)
	r.Ingest(
		streamID,
		EncodedChunk{
			Seq:     2,
			TS:      66,
			Key:     false,
			Codec:   "vp8",
			Kind:    KindVideo,
			Payload: []byte("delta2"),
		},
	)

	v := newFakeViewer("v1")
	if _, err := r.Attach(streamID, v); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	replay := v.allReceived()
	if len(replay) != 3 {
		t.Fatalf(
			"expected 3 replayed chunks (keyframe + 2 deltas), got %d: %+v",
			len(replay),
			replay,
		)
	}
	if replay[0][12] != 1 || string(replay[0][18:]) != "keyframe" {
		t.Fatalf("expected the keyframe first, got %+v", replay[0])
	}
	if string(replay[1][18:]) != "delta1" || string(replay[2][18:]) != "delta2" {
		t.Fatalf("expected deltas in arrival order after the keyframe, got %+v", replay)
	}
}

// FR-003 correctness the GOP cache above relies on: a new keyframe resets
// the delta list rather than accumulating alongside the old one.
func TestStreamRelay_GOPCache_NewKeyframeResetsDeltas(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf1")})
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("d1")})
	r.Ingest(streamID, EncodedChunk{Seq: 2, Key: true, Payload: []byte("kf2")})

	v := newFakeViewer("v1")
	if _, err := r.Attach(streamID, v); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	replay := v.allReceived()
	if len(replay) != 1 {
		t.Fatalf(
			"expected only the latest keyframe cached (deltas reset), got %d entries: %+v",
			len(replay),
			replay,
		)
	}
	if string(replay[0][18:]) != "kf2" {
		t.Fatalf("expected the LATEST keyframe (kf2), got %q", replay[0][18:])
	}
}

// FR-003: the delta list is bounded to N, trimming the oldest first.
func TestStreamRelay_GOPCache_DeltaListBoundedToN(t *testing.T) {
	r := newStreamRelayWithLimits(relayMaxConcurrentStreams, relayMaxAggregateCacheBytes, 3)
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	for i := 1; i <= 5; i++ {
		r.Ingest(
			streamID,
			EncodedChunk{Seq: uint32(i), Key: false, Payload: []byte(fmt.Sprintf("d%d", i))},
		)
	}

	v := newFakeViewer("v1")
	if _, err := r.Attach(streamID, v); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	replay := v.allReceived()
	if len(replay) != 4 { // keyframe + 3 retained deltas (N=3)
		t.Fatalf("expected keyframe + 3 retained deltas, got %d entries: %+v", len(replay), replay)
	}
	want := []string{"d3", "d4", "d5"}
	for i, w := range want {
		if got := string(replay[i+1][18:]); got != w {
			t.Fatalf("delta[%d]: want %q, got %q (full replay: %+v)", i, w, got, replay)
		}
	}
}

// TD1: an audio chunk must never occupy the video keyframe/delta cache slot
// or count against relayGOPMaxDeltas — it fans out live only. A viewer
// attaching after a mix of video and audio ingests must replay ONLY the
// video keyframe + video deltas.
func TestStreamRelay_GOPCache_AudioChunksNeverCached(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Kind: KindVideo, Payload: []byte("kf")})
	r.Ingest(streamID, EncodedChunk{Seq: 1, Kind: KindAudio, Payload: []byte("audio1")})
	r.Ingest(streamID, EncodedChunk{Seq: 2, Key: false, Kind: KindVideo, Payload: []byte("d1")})
	r.Ingest(streamID, EncodedChunk{Seq: 3, Kind: KindAudio, Payload: []byte("audio2")})

	v := newFakeViewer("v1")
	if _, err := r.Attach(streamID, v); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	replay := v.allReceived()
	if len(replay) != 2 { // keyframe + 1 video delta only — audio excluded
		t.Fatalf(
			"expected replay of keyframe + 1 video delta only (audio excluded from GOP cache), got %d: %+v",
			len(replay),
			replay,
		)
	}
	if string(replay[0][18:]) != "kf" || string(replay[1][18:]) != "d1" {
		t.Fatalf("unexpected replay contents: %q, %q", replay[0][18:], replay[1][18:])
	}
}

// TD1/TD2: NewAudioChunk has no Key parameter, so it can never produce an
// audio+key=true chunk — the illegal combination EncodeChunk used to be able
// to silently accept is now unconstructable through the sanctioned path.
func TestNewAudioChunk_NeverKeyframe(t *testing.T) {
	c := NewAudioChunk(1, 100, "opus", []byte("frame"))
	if c.Kind != KindAudio {
		t.Fatalf("expected KindAudio, got %v", c.Kind)
	}
	if c.Key {
		t.Fatal("NewAudioChunk must never produce a keyframe-flagged chunk")
	}
}

func TestNewVideoChunk_PreservesKeyAndKind(t *testing.T) {
	c := NewVideoChunk(2, 200, true, "avc1.4D4028", []byte("kf"))
	if c.Kind != KindVideo || !c.Key {
		t.Fatalf("expected a video keyframe chunk, got %+v", c)
	}
	d := NewVideoChunk(3, 300, false, "avc1.4D4028", []byte("delta"))
	if d.Kind != KindVideo || d.Key {
		t.Fatalf("expected a video delta chunk, got %+v", d)
	}
}

// ParseChunkKind must classify ONLY the exact string "audio" as KindAudio —
// a typo, unexpected casing, or the zero value must all fall through to the
// tolerant KindVideo default (TD1/TD2), matching EncodeChunk's pre-existing
// behavior now that the check has a single named call site.
func TestParseChunkKind_TolerantDefaultIsVideo(t *testing.T) {
	cases := map[string]ChunkKind{
		"audio": KindAudio,
		"video": KindVideo,
		"":      KindVideo,
		"Audio": KindVideo,
		"AUDIO": KindVideo,
		"bogus": KindVideo,
	}
	for in, want := range cases {
		if got := ParseChunkKind(in); got != want {
			t.Errorf("ParseChunkKind(%q) = %v, want %v", in, got, want)
		}
	}
}

// Test 5 (FR-003, MAJ-005): aggregate stream-count ceiling evicts the
// least-recently-viewed IDLE stream.
func TestStreamRelay_AggregateCacheCeiling_Evicts(t *testing.T) {
	r := newStreamRelayWithLimits(2, 10*1024, 5)

	r.Ingest("s1", EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf1")})
	r.Ingest("s2", EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf2")})
	r.Ingest("s3", EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf3")})

	if count := r.StreamCount(); count > 2 {
		t.Fatalf("expected at most 2 streams retained (maxStreams=2), got %d", count)
	}
	if r.streamExists("s1") {
		t.Fatalf("expected s1 (least-recently-viewed idle stream) to have been evicted")
	}
	if !r.streamExists("s3") {
		t.Fatalf("expected s3 (most recently ingested) to survive")
	}
}

// Test 5 continued (MAJ-005): a stream with a live or currently-attaching
// viewer must never be evicted, even under count/byte pressure.
func TestStreamRelay_AggregateCacheCeiling_NeverEvictsViewedOrAttaching(t *testing.T) {
	r := newStreamRelayWithLimits(2, 10*1024, 5)

	v1 := newFakeViewer("v1")
	r.Ingest("viewed", EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf-viewed")})
	if _, err := r.Attach("viewed", v1); err != nil {
		t.Fatalf("Attach viewed: %v", err)
	}

	// Reserve a second stream as "currently attaching" without registering a
	// viewer yet — exercises the same attaching++ reservation the real
	// Attach call makes for its own duration (getOrCreateStreamForAttach).
	attaching := r.getOrCreateStreamForAttach("attaching")
	defer func() {
		attaching.mu.Lock()
		attaching.attaching--
		attaching.mu.Unlock()
	}()
	r.Ingest("attaching", EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf-attaching")})

	// Pressure both the count and byte ceilings with plainly idle streams.
	for i := 0; i < 5; i++ {
		r.Ingest(
			fmt.Sprintf("idle-%d", i),
			EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf-idle")},
		)
	}

	if !r.streamExists("viewed") {
		t.Fatalf("a stream with a live viewer must never be evicted (MAJ-005)")
	}
	if !r.streamExists("attaching") {
		t.Fatalf("a stream with a currently-attaching viewer must never be evicted (MAJ-005)")
	}
}

// Test 6 (FR-004): a full-queue viewer's chunk is dropped in isolation —
// other attached viewers of the same stream are unaffected. Also exercises
// DS-3 row 3 (recovery forces a fresh keyframe rather than resuming on a
// delta the viewer may have a gap before).
func TestStreamRelay_SlowViewerDropsIsolated(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	fast := newFakeViewer("fast")
	slow := newFakeViewer("slow")

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	if _, err := r.Attach(streamID, fast); err != nil {
		t.Fatalf("Attach fast: %v", err)
	}
	if _, err := r.Attach(streamID, slow); err != nil {
		t.Fatalf("Attach slow: %v", err)
	}
	// CR2: Attach itself flushes the cached keyframe replay to both viewers
	// before returning.
	if fast.chunkCount() != 1 || slow.chunkCount() != 1 {
		t.Fatalf(
			"expected both viewers to receive the keyframe replay on Attach, got fast=%d slow=%d",
			fast.chunkCount(),
			slow.chunkCount(),
		)
	}

	slow.setFull(true)
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("delta")})

	if fast.chunkCount() != 2 {
		t.Fatalf(
			"expected the fast viewer to receive the delta chunk (replay + delta), got %d chunks",
			fast.chunkCount(),
		)
	}
	if slow.chunkCount() != 1 {
		t.Fatalf(
			"expected the slow viewer's delta to be dropped in isolation (still just the replay), got %d chunks",
			slow.chunkCount(),
		)
	}

	// Recovery: the slow viewer's queue drains. Its NEXT delivery must
	// replay the FULL cached GOP — keyframe followed by every retained
	// delta (SF-H2) — not just the stale cached keyframe alone, so the
	// viewer catches up on every delta it missed instead of resuming on a
	// live delta it has a decode gap before.
	slow.setFull(false)
	r.Ingest(streamID, EncodedChunk{Seq: 2, Key: false, Payload: []byte("delta2")})

	if fast.chunkCount() != 3 {
		t.Fatalf(
			"expected the fast viewer to keep receiving normally, got %d chunks",
			fast.chunkCount(),
		)
	}
	recovered := slow.allReceived()
	if len(recovered) != 4 { // initial Attach replay (kf) + recovery replay (kf, delta, delta2)
		t.Fatalf(
			"expected the slow viewer to receive 3 more chunks on recovery (keyframe+2 deltas), got %d total: %+v",
			len(recovered),
			recovered,
		)
	}
	if recovered[1][12] != 1 || string(recovered[1][18:]) != "kf" {
		t.Fatalf(
			"expected the recovery replay to start with a fresh keyframe, got %v",
			recovered[1],
		)
	}
	if string(recovered[2][18:]) != "delta" || string(recovered[3][18:]) != "delta2" {
		t.Fatalf(
			"expected the recovery replay to include every cached delta in order, got %+v",
			recovered[2:],
		)
	}
}

// SF-H2: the degraded→recover transition asks the encoder (via the injected
// keyframeRequester seam) for a genuinely fresh IDR exactly once per degraded
// episode — not on every still-full retry — and the relay's interim replay
// ALWAYS carries the full cached GOP (keyframe + every retained delta), not
// just the stale keyframe alone.
func TestStreamRelay_DegradedRecovery_RequestsFreshKeyframeAndReplaysFullGOP(t *testing.T) {
	fkr := &fakeKeyframeRequester{}
	prev := activeKeyframeRequester
	SetKeyframeRequester(fkr)
	t.Cleanup(func() { activeKeyframeRequester = prev })

	r := NewStreamRelay()
	const streamID = "s1"

	slow := newFakeViewer("slow")
	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	if _, err := r.Attach(streamID, slow); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	slow.setFull(true)
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("d1")})
	if got := fkr.count(); got != 0 {
		t.Fatalf(
			"expected no keyframe request yet (this is the drop itself, not a recovery attempt), got %d",
			got,
		)
	}

	// Still full: a recovery ATTEMPT happens (degraded==true) but doesn't
	// succeed — the keyframe request must fire exactly once, on the FIRST
	// such attempt, not again on a second still-failing attempt.
	r.Ingest(streamID, EncodedChunk{Seq: 2, Key: false, Payload: []byte("d2")})
	if got := fkr.count(); got != 1 {
		t.Fatalf(
			"expected exactly 1 keyframe request after the first recovery attempt, got %d",
			got,
		)
	}
	r.Ingest(streamID, EncodedChunk{Seq: 3, Key: false, Payload: []byte("d3")})
	if got := fkr.count(); got != 1 {
		t.Fatalf(
			"expected still exactly 1 keyframe request while the viewer stays degraded, got %d",
			got,
		)
	}
	if got := fkr.requestedStream(); got != streamID {
		t.Fatalf("expected the keyframe request to name streamID %q, got %q", streamID, got)
	}

	// Recovery succeeds: the FULL cached GOP (keyframe + every retained
	// delta) is replayed in one delivery, not just the keyframe.
	slow.setFull(false)
	r.Ingest(streamID, EncodedChunk{Seq: 4, Key: false, Payload: []byte("d4")})

	recovered := slow.allReceived()
	// [0]=initial Attach replay (kf). [1:]=recovery replay: kf, d1, d2, d3, d4.
	if len(recovered) != 6 {
		t.Fatalf(
			"expected initial replay + full 5-chunk GOP recovery replay (kf,d1,d2,d3,d4), got %d chunks",
			len(recovered),
		)
	}
	if string(recovered[1][18:]) != "kf" {
		t.Fatalf("expected recovery replay to start with the keyframe, got %q", recovered[1][18:])
	}
	wantDeltas := []string{"d1", "d2", "d3", "d4"}
	for i, want := range wantDeltas {
		if got := string(recovered[2+i][18:]); got != want {
			t.Fatalf("recovery delta[%d]: want %q, got %q", i, want, got)
		}
	}

	// A subsequent, later degradation must be able to request again — the
	// once-per-episode guard resets on successful recovery.
	slow.setFull(true)
	r.Ingest(streamID, EncodedChunk{Seq: 5, Key: false, Payload: []byte("d5")})
	r.Ingest(streamID, EncodedChunk{Seq: 6, Key: false, Payload: []byte("d6")})
	if got := fkr.count(); got != 2 {
		t.Fatalf("expected a second keyframe request on a NEW degraded episode, got %d", got)
	}
}

// fakeKeyframeRequester is a hermetic browser.keyframeRequester (SF-H2) —
// records every streamID RequestKeyframe was called for, in order.
type fakeKeyframeRequester struct {
	mu        sync.Mutex
	requested []string
}

func (f *fakeKeyframeRequester) RequestKeyframe(streamID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, streamID)
}

func (f *fakeKeyframeRequester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requested)
}

// requestedStream returns the LAST requested streamID, or "" if none yet.
func (f *fakeKeyframeRequester) requestedStream() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requested) == 0 {
		return ""
	}
	return f.requested[len(f.requested)-1]
}

// Test 7 (FR-018): when the ingest side signals the stream failed/closed,
// MarkFailed marks it failed and notifies attached viewers; further ingest
// and further attach are both rejected.
func TestStreamRelay_CaptureExit_MarksFailed(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	v := newFakeViewer("v1")
	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	if _, err := r.Attach(streamID, v); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// CR2: Attach already flushed the keyframe replay to v.
	if v.chunkCount() != 1 {
		t.Fatalf(
			"expected the keyframe replay to be delivered on Attach, got %d chunks",
			v.chunkCount(),
		)
	}

	r.MarkFailed(streamID)

	if !v.wasNotifiedFailed(streamID) {
		t.Fatalf("expected the attached viewer to be notified of the stream failure")
	}

	// Further ingest on a failed stream must not resurrect it or reach viewers.
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("delta-after-failure")})
	if v.chunkCount() != 1 {
		t.Fatalf("expected no additional chunks delivered after MarkFailed, got %d", v.chunkCount())
	}

	// A fresh attach to a failed stream must be rejected.
	v2 := newFakeViewer("v2")
	if _, err := r.Attach(streamID, v2); err == nil {
		t.Fatalf("expected Attach to a failed stream to return an error")
	}

	// MarkFailed is idempotent -- a second call must not panic or re-notify.
	r.MarkFailed(streamID)
}

// Test 27 (FR-015, M-6): an unauthorized viewer attach is rejected BEFORE
// any GOP replay -- no cached frame is served, and the viewer is never
// registered on the stream.
func TestViewerAttach_Unauthorized_RejectedBeforeGOPReplay(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("delta")})

	v := newFakeViewer("unauthorized")
	v.setAuthorized(false)

	if _, err := r.Attach(streamID, v); err == nil {
		t.Fatalf("expected Attach to reject an unauthorized viewer")
	}
	if v.chunkCount() != 0 {
		t.Fatalf(
			"expected NO replay chunks for a rejected/unauthorized attach, got %d",
			v.chunkCount(),
		)
	}

	s, ok := r.lookupStream(streamID)
	if ok {
		s.mu.Lock()
		_, registered := s.viewers[v]
		s.mu.Unlock()
		if registered {
			t.Fatalf("an unauthorized viewer must never be registered on the stream")
		}
	}
}

// EncodeChunk must match the BrowserChunkEnvelope contract exactly: an
// 18-byte big-endian header (seq:u32, ts:u64, key:u8, kind:u8, len:u32)
// followed by the raw payload, with no fragment field and no extra tag byte
// inside the payload.
func TestEncodeChunk_MatchesBrowserChunkEnvelopeLayout(t *testing.T) {
	c := EncodedChunk{
		Seq:     42,
		TS:      1234567890,
		Key:     true,
		Codec:   "vp8",
		Kind:    KindVideo,
		Payload: []byte("hello"),
	}
	buf := EncodeChunk(c)

	if len(buf) != 18+len(c.Payload) {
		t.Fatalf("expected 18-byte header + payload, got %d bytes", len(buf))
	}
	if seq := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3]); seq != 42 {
		t.Fatalf("seq: want 42, got %d", seq)
	}
	if buf[12] != 1 {
		t.Fatalf("key byte: want 1 (keyframe), got %d", buf[12])
	}
	if buf[13] != 0 {
		t.Fatalf("kind byte: want 0 (video), got %d", buf[13])
	}
	l := uint32(buf[14])<<24 | uint32(buf[15])<<16 | uint32(buf[16])<<8 | uint32(buf[17])
	if l != uint32(len(c.Payload)) {
		t.Fatalf("len: want %d, got %d", len(c.Payload), l)
	}
	if string(buf[18:]) != "hello" {
		t.Fatalf("payload: want %q, got %q", "hello", buf[18:])
	}
}

// EncodeChunk must set the kind byte to 1 for an audio chunk, and audio
// chunks must never carry the key=1 keyframe flag (Opus packets are never
// classified key/delta — BrowserChunkEnvelope.yaml).
func TestEncodeChunk_AudioKindByte(t *testing.T) {
	c := EncodedChunk{
		Seq:     7,
		TS:      99,
		Key:     false,
		Codec:   "opus",
		Kind:    KindAudio,
		Payload: []byte("opusframe"),
	}
	buf := EncodeChunk(c)

	if len(buf) != 18+len(c.Payload) {
		t.Fatalf("expected 18-byte header + payload, got %d bytes", len(buf))
	}
	if buf[12] != 0 {
		t.Fatalf("key byte: want 0 (audio is never a keyframe), got %d", buf[12])
	}
	if buf[13] != 1 {
		t.Fatalf("kind byte: want 1 (audio), got %d", buf[13])
	}
	if string(buf[18:]) != "opusframe" {
		t.Fatalf("payload: want %q, got %q", "opusframe", buf[18:])
	}
}

// T-F1/C-F1 (golden fixture, drift guard): pins the 18-byte
// BrowserChunkEnvelope header layout — seq@0(u32), ts@4(u64), key@12(u8),
// kind@13(u8), len@14(u32), payload@18 — as a committed hex vector, produced
// by THIS package's EncodeChunk and independently decoded field-by-field via
// encoding/binary at those exact pinned offsets (not by round-tripping
// through EncodeChunk's own logic again — a self-referential encode/decode
// pair could stay internally consistent while silently drifting from the
// wire contract every OTHER hand-packed producer/consumer of this exact
// layout independently implements: encoder.html's encodeChunkEnvelope,
// browserLiveWs.ts's decodeChunkEnvelope, and browser_ingest.go's
// decodeChunkEnvelope). A future accidental offset/width change to
// EncodeChunk — or to relayChunkEnvelopeHeaderBytes — fails this test loudly
// instead of silently drifting from those other sites.
func TestEncodeChunk_GoldenFixtureEnvelopeLayout(t *testing.T) {
	const wantHex = "000000010000000000000002010000000002aabb"
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatalf("bad golden hex fixture: %v", err)
	}

	c := EncodedChunk{
		Seq:     1,
		TS:      2,
		Key:     true,
		Codec:   "vp8",
		Kind:    KindVideo,
		Payload: []byte{0xAA, 0xBB},
	}
	got := EncodeChunk(c)

	if !bytes.Equal(got, want) {
		t.Fatalf(
			"EncodeChunk output drifted from the committed golden fixture:\n got  = %x\n want = %x",
			got,
			want,
		)
	}

	// Decode field-by-field at the pinned offsets, independent of EncodeChunk.
	const (
		seqOff    = 0
		tsOff     = 4
		keyOff    = 12
		kindOff   = 13
		lenOff    = 14
		headerLen = 18
	)
	if headerLen != relayChunkEnvelopeHeaderBytes {
		t.Fatalf(
			"relayChunkEnvelopeHeaderBytes drifted from the pinned headerLen=18: got %d",
			relayChunkEnvelopeHeaderBytes,
		)
	}
	if len(got) != headerLen+len(c.Payload) {
		t.Fatalf("total length: want %d, got %d", headerLen+len(c.Payload), len(got))
	}
	if seq := binary.BigEndian.Uint32(got[seqOff : seqOff+4]); seq != c.Seq {
		t.Errorf("seq@%d: want %d, got %d", seqOff, c.Seq, seq)
	}
	if ts := binary.BigEndian.Uint64(got[tsOff : tsOff+8]); ts != c.TS {
		t.Errorf("ts@%d: want %d, got %d", tsOff, c.TS, ts)
	}
	if key := got[keyOff]; (key != 0) != c.Key {
		t.Errorf("key@%d: want %v, got byte %d", keyOff, c.Key, key)
	}
	if kind := got[kindOff]; ChunkKind(kind) != c.Kind {
		t.Errorf("kind@%d: want %d, got %d", kindOff, c.Kind, kind)
	}
	if l := binary.BigEndian.Uint32(got[lenOff : lenOff+4]); l != uint32(len(c.Payload)) {
		t.Errorf("len@%d: want %d, got %d", lenOff, len(c.Payload), l)
	}
	if !bytes.Equal(got[headerLen:], c.Payload) {
		t.Fatalf("payload@%d: want %x, got %x", headerLen, c.Payload, got[headerLen:])
	}
}

// streamExists is a small test helper -- reports whether streamID currently
// has a cache entry in the relay (lock-safe; same package as StreamRelay).
func (r *StreamRelay) streamExists(streamID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.streams[streamID]
	return ok
}
