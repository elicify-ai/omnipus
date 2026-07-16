package browser

import (
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

func (f *fakeViewer) lastChunk() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) == 0 {
		return nil
	}
	return f.received[len(f.received)-1]
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
// cached keyframe first, then its deltas in arrival order.
func TestStreamRelay_GOPCache_ReplaysKeyframeFirst(t *testing.T) {
	r := NewStreamRelay()
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, TS: 0, Key: true, Codec: "vp8", Kind: "video", Payload: []byte("keyframe")})
	r.Ingest(streamID, EncodedChunk{Seq: 1, TS: 33, Key: false, Codec: "vp8", Kind: "video", Payload: []byte("delta1")})
	r.Ingest(streamID, EncodedChunk{Seq: 2, TS: 66, Key: false, Codec: "vp8", Kind: "video", Payload: []byte("delta2")})

	v := newFakeViewer("v1")
	replay, err := r.Attach(streamID, v)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(replay) != 3 {
		t.Fatalf("expected 3 replayed chunks (keyframe + 2 deltas), got %d: %+v", len(replay), replay)
	}
	if !replay[0].Key || string(replay[0].Payload) != "keyframe" {
		t.Fatalf("expected the keyframe first, got %+v", replay[0])
	}
	if string(replay[1].Payload) != "delta1" || string(replay[2].Payload) != "delta2" {
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
	replay, err := r.Attach(streamID, v)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(replay) != 1 {
		t.Fatalf("expected only the latest keyframe cached (deltas reset), got %d entries: %+v", len(replay), replay)
	}
	if string(replay[0].Payload) != "kf2" {
		t.Fatalf("expected the LATEST keyframe (kf2), got %q", replay[0].Payload)
	}
}

// FR-003: the delta list is bounded to N, trimming the oldest first.
func TestStreamRelay_GOPCache_DeltaListBoundedToN(t *testing.T) {
	r := newStreamRelayWithLimits(relayMaxConcurrentStreams, relayMaxAggregateCacheBytes, 3)
	const streamID = "s1"

	r.Ingest(streamID, EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf")})
	for i := 1; i <= 5; i++ {
		r.Ingest(streamID, EncodedChunk{Seq: uint32(i), Key: false, Payload: []byte(fmt.Sprintf("d%d", i))})
	}

	v := newFakeViewer("v1")
	replay, err := r.Attach(streamID, v)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(replay) != 4 { // keyframe + 3 retained deltas (N=3)
		t.Fatalf("expected keyframe + 3 retained deltas, got %d entries: %+v", len(replay), replay)
	}
	want := []string{"d3", "d4", "d5"}
	for i, w := range want {
		if got := string(replay[i+1].Payload); got != w {
			t.Fatalf("delta[%d]: want %q, got %q (full replay: %+v)", i, w, got, replay)
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
		r.Ingest(fmt.Sprintf("idle-%d", i), EncodedChunk{Seq: 0, Key: true, Payload: []byte("kf-idle")})
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

	slow.setFull(true)
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("delta")})

	if fast.chunkCount() != 1 {
		t.Fatalf("expected the fast viewer to receive the delta chunk, got %d chunks", fast.chunkCount())
	}
	if slow.chunkCount() != 0 {
		t.Fatalf("expected the slow viewer's chunk to be dropped in isolation, got %d chunks", slow.chunkCount())
	}

	// Recovery: the slow viewer's queue drains. Its NEXT delivered chunk
	// must be a fresh keyframe (DS-3 row 3), not the plain delta that just
	// arrived — it may have a gap before it.
	slow.setFull(false)
	r.Ingest(streamID, EncodedChunk{Seq: 2, Key: false, Payload: []byte("delta2")})

	if fast.chunkCount() != 2 {
		t.Fatalf("expected the fast viewer to keep receiving normally, got %d chunks", fast.chunkCount())
	}
	if slow.chunkCount() != 1 {
		t.Fatalf("expected the slow viewer to receive exactly 1 chunk on recovery, got %d", slow.chunkCount())
	}
	got := slow.lastChunk()
	if len(got) < 13 || got[12] != 1 {
		t.Fatalf("expected the slow viewer's recovery chunk to be a keyframe (header key byte=1), got %v", got)
	}
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

	r.MarkFailed(streamID)

	if !v.wasNotifiedFailed(streamID) {
		t.Fatalf("expected the attached viewer to be notified of the stream failure")
	}

	// Further ingest on a failed stream must not resurrect it or reach viewers.
	r.Ingest(streamID, EncodedChunk{Seq: 1, Key: false, Payload: []byte("delta-after-failure")})
	if v.chunkCount() != 0 {
		t.Fatalf("expected no chunks delivered after MarkFailed, got %d", v.chunkCount())
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

	replay, err := r.Attach(streamID, v)
	if err == nil {
		t.Fatalf("expected Attach to reject an unauthorized viewer")
	}
	if replay != nil {
		t.Fatalf("expected NO replay chunks for a rejected/unauthorized attach, got %d", len(replay))
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

// EncodeChunk must match the BrowserChunkEnvelope contract exactly: a
// 17-byte big-endian header (seq:u32, ts:u64, key:u8, len:u32) followed by
// the raw payload, with no fragment field.
func TestEncodeChunk_MatchesBrowserChunkEnvelopeLayout(t *testing.T) {
	c := EncodedChunk{Seq: 42, TS: 1234567890, Key: true, Codec: "vp8", Kind: "video", Payload: []byte("hello")}
	buf := EncodeChunk(c)

	if len(buf) != 17+len(c.Payload) {
		t.Fatalf("expected 17-byte header + payload, got %d bytes", len(buf))
	}
	if seq := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3]); seq != 42 {
		t.Fatalf("seq: want 42, got %d", seq)
	}
	if buf[12] != 1 {
		t.Fatalf("key byte: want 1 (keyframe), got %d", buf[12])
	}
	l := uint32(buf[13])<<24 | uint32(buf[14])<<16 | uint32(buf[15])<<8 | uint32(buf[16])
	if l != uint32(len(c.Payload)) {
		t.Fatalf("len: want %d, got %d", len(c.Payload), l)
	}
	if string(buf[17:]) != "hello" {
		t.Fatalf("payload: want %q, got %q", "hello", buf[17:])
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
