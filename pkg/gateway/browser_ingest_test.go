// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Hermetic unit tests for the capture-ingest endpoint (component E). No real
// WebSocket / no HTTP hijack: serveConn is driven with an in-memory fake conn,
// and the loopback gate is exercised via httptest before the upgrade.

package gateway

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// --- test doubles ---------------------------------------------------------

type spyRelay struct {
	mu     sync.Mutex
	stream []string
	chunks []EncodedChunk
}

func (r *spyRelay) Ingest(streamID string, c EncodedChunk) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stream = append(r.stream, streamID)
	r.chunks = append(r.chunks, c)
}

func (r *spyRelay) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.chunks)
}

func (r *spyRelay) snapshot() []EncodedChunk {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EncodedChunk, len(r.chunks))
	copy(out, r.chunks)
	return out
}

type fakeMsg struct {
	mt   int
	data []byte
}

type fakeConn struct {
	incoming chan fakeMsg
	mu       sync.Mutex
	written  [][]byte
	writeSig chan struct{}
	closed   bool
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		incoming: make(chan fakeMsg, 32),
		writeSig: make(chan struct{}, 16),
	}
}

func (c *fakeConn) feedText(data []byte) {
	c.incoming <- fakeMsg{mt: websocket.TextMessage, data: data}
}
func (c *fakeConn) feedBinary(data []byte) {
	c.incoming <- fakeMsg{mt: websocket.BinaryMessage, data: data}
}
func (c *fakeConn) closeIncoming() { close(c.incoming) }

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	m, ok := <-c.incoming
	if !ok {
		return 0, nil, io.EOF
	}
	return m.mt, m.data, nil
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return io.ErrClosedPipe
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.written = append(c.written, cp)
	c.mu.Unlock()
	select {
	case c.writeSig <- struct{}{}:
	default:
	}
	return nil
}

func (c *fakeConn) SetReadLimit(int64)              {}
func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }
func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) writes() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.written))
	copy(out, c.written)
	return out
}

// --- helpers --------------------------------------------------------------

const testLoopback = "127.0.0.1:50505"

func initFrameBytes(streamID, token, videoCodec string) []byte {
	b, _ := json.Marshal(generated.BrowserIngestInitFrame{
		Type:       string(generated.WsFrameTypeBrowserIngestInit),
		StreamId:   streamID,
		Token:      token,
		VideoCodec: videoCodec,
	})
	return b
}

func chunkBytes(seq uint32, ts uint64, key byte, payload []byte) []byte {
	buf := make([]byte, ingestChunkHeaderLen+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], seq)
	binary.BigEndian.PutUint64(buf[4:12], ts)
	buf[12] = key
	binary.BigEndian.PutUint32(buf[13:17], uint32(len(payload)))
	copy(buf[ingestChunkHeaderLen:], payload)
	return buf
}

func serveAsync(h *CaptureIngestHandler, c *fakeConn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		h.serveConn(c, testLoopback)
		close(done)
	}()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serveConn did not finish within 3s")
	}
}

func (h *CaptureIngestHandler) stateFor(streamID string) (streamState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.streams[streamID]
	if !ok {
		return streamDead, false
	}
	return s.state, true
}

func waitState(t *testing.T, h *CaptureIngestHandler, streamID string, want streamState) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if st, ok := h.stateFor(streamID); ok && st == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("stream %q did not reach state %d in time", streamID, want)
}

type auditRec struct {
	Event    string         `json:"event"`
	Severity string         `json:"severity"`
	Fields   map[string]any `json:"fields"`
}

// newAuditIngest builds a handler wired to a real audit logger writing to a temp
// dir. Returns the handler and a reader func that flushes (Close) and returns the
// browser.live.* audit records. The reader may be called once.
func newAuditIngest(t *testing.T, deps IngestDeps) (*CaptureIngestHandler, func() []auditRec) {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	deps.Audit = lg
	h := NewCaptureIngestHandler(deps)
	read := func() []auditRec {
		if cerr := lg.Close(); cerr != nil {
			t.Fatalf("audit close: %v", cerr)
		}
		data, rerr := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		if rerr != nil {
			t.Fatalf("read audit.jsonl: %v", rerr)
		}
		var recs []auditRec
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var r auditRec
			if uerr := json.Unmarshal([]byte(line), &r); uerr != nil {
				t.Fatalf("unmarshal audit line %q: %v", line, uerr)
			}
			if strings.HasPrefix(r.Event, "browser.live.") {
				recs = append(recs, r)
			}
		}
		return recs
	}
	return h, read
}

func rejectReasonsFor(recs []auditRec) []string {
	var out []string
	for _, r := range recs {
		if r.Event == eventIngestRejected {
			if reason, ok := r.Fields["reason"].(string); ok {
				out = append(out, reason)
			}
		}
	}
	return out
}

func countEvent(recs []auditRec, event string) int {
	n := 0
	for _, r := range recs {
		if r.Event == event {
			n++
		}
	}
	return n
}

func hasReason(recs []auditRec, reason string) bool {
	for _, r := range rejectReasonsFor(recs) {
		if r == reason {
			return true
		}
	}
	return false
}

// --- Test 1: RejectsUnauthenticated --------------------------------------

func TestIngest_RejectsUnauthenticated(t *testing.T) {
	t.Run("absent_token", func(t *testing.T) {
		relay := &spyRelay{}
		h, read := newAuditIngest(t, IngestDeps{Relay: relay})
		c := newFakeConn()
		c.feedText(initFrameBytes("streamA", "", "vp8")) // empty token
		c.closeIncoming()
		waitDone(t, serveAsync(h, c))

		if relay.count() != 0 {
			t.Fatalf("relay should not have received chunks, got %d", relay.count())
		}
		recs := read()
		if !hasReason(recs, string(rejectAbsentToken)) {
			t.Fatalf("expected absent_token reject audit, got reasons %v", rejectReasonsFor(recs))
		}
	})

	t.Run("bad_token", func(t *testing.T) {
		relay := &spyRelay{}
		h, read := newAuditIngest(t, IngestDeps{Relay: relay})
		h.MintIngestToken("streamA") // stream exists, but we present a wrong token
		c := newFakeConn()
		c.feedText(initFrameBytes("streamA", "totally-wrong-token", "vp8"))
		c.feedBinary(chunkBytes(0, 0, chunkVideoKey, []byte("keyframe"))) // must never be relayed
		c.closeIncoming()
		waitDone(t, serveAsync(h, c))

		if relay.count() != 0 {
			t.Fatalf("bad token must reject before any relay, got %d chunks", relay.count())
		}
		recs := read()
		if !hasReason(recs, string(rejectBadToken)) {
			t.Fatalf("expected bad_token reject audit, got reasons %v", rejectReasonsFor(recs))
		}
	})

	t.Run("chunk_before_init", func(t *testing.T) {
		relay := &spyRelay{}
		h, read := newAuditIngest(t, IngestDeps{Relay: relay})
		c := newFakeConn()
		c.feedBinary(chunkBytes(0, 0, chunkVideoKey, []byte("data"))) // binary before init
		c.closeIncoming()
		waitDone(t, serveAsync(h, c))

		if relay.count() != 0 {
			t.Fatalf("chunk-before-init must not relay")
		}
		if !hasReason(read(), string(rejectChunkBeforeInit)) {
			t.Fatal("expected chunk_before_init reject audit")
		}
	})
}

// --- Test 2: TokenStreamScoped -------------------------------------------

func TestIngest_TokenStreamScoped(t *testing.T) {
	t.Run("mis_scoped", func(t *testing.T) {
		relay := &spyRelay{}
		h, read := newAuditIngest(t, IngestDeps{Relay: relay})
		tokA := h.MintIngestToken("streamA")
		h.MintIngestToken("streamB")

		// Present A's token but claim to be stream B → mis-scoped.
		c := newFakeConn()
		c.feedText(initFrameBytes("streamB", tokA, "vp8"))
		c.closeIncoming()
		waitDone(t, serveAsync(h, c))

		if relay.count() != 0 {
			t.Fatal("mis-scoped token must not relay")
		}
		if !hasReason(read(), string(rejectMisScoped)) {
			t.Fatal("expected mis_scoped_token reject audit")
		}
	})

	t.Run("post_stream_end", func(t *testing.T) {
		relay := &spyRelay{}
		h, read := newAuditIngest(t, IngestDeps{Relay: relay})
		tokA := h.MintIngestToken("streamA")
		h.EndStream("streamA") // token invalidated at stream end

		c := newFakeConn()
		c.feedText(initFrameBytes("streamA", tokA, "vp8"))
		c.closeIncoming()
		waitDone(t, serveAsync(h, c))

		if relay.count() != 0 {
			t.Fatal("post-end token must not relay")
		}
		if !hasReason(read(), string(rejectUnknownStream)) {
			t.Fatalf("expected unknown_or_dead_token reject, got %v", rejectReasonsFor(read()))
		}
	})
}

// --- Test 10: OversizeKeyframe_RejectedNotFragmented (+ empty reject, DS-2) --

func TestIngest_OversizeKeyframe_RejectedNotFragmented(t *testing.T) {
	var stepDowns []string
	var mu sync.Mutex
	relay := &spyRelay{}
	h, read := newAuditIngest(t, IngestDeps{
		Relay:           relay,
		MaxMessageBytes: 1024, // small configurable bound to keep the test light
		StepDown: func(id string) {
			mu.Lock()
			stepDowns = append(stepDowns, id)
			mu.Unlock()
		},
	})
	tok := h.MintIngestToken("streamA")

	under := make([]byte, 512)    // < bound → relayed
	atBound := make([]byte, 1024) // == bound → relayed (accepted at the bound, DS-2)
	over := make([]byte, 2048)    // > bound → rejected + step-down, NOT fragmented

	c := newFakeConn()
	c.feedText(initFrameBytes("streamA", tok, "vp8"))
	c.feedBinary(chunkBytes(0, 0, chunkVideoKey, under))
	c.feedBinary(chunkBytes(1, 33, chunkVideoKey, atBound))
	c.feedBinary(chunkBytes(2, 66, chunkVideoKey, over))
	c.feedBinary(chunkBytes(3, 99, chunkVideoDelta, []byte{})) // empty payload → rejected (DS-2)
	c.closeIncoming()
	waitDone(t, serveAsync(h, c))

	// Only the under-bound + at-bound chunks are relayed; oversize + empty dropped.
	if relay.count() != 2 {
		t.Fatalf("expected 2 relayed chunks (under + at-bound), got %d", relay.count())
	}
	for _, ch := range relay.snapshot() {
		if len(ch.Payload) > 1024 {
			t.Fatalf("relayed a chunk over the bound (%d bytes) — must never happen", len(ch.Payload))
		}
		if len(ch.Payload) == 0 {
			t.Fatal("relayed an empty chunk — must never happen")
		}
	}
	mu.Lock()
	sd := append([]string(nil), stepDowns...)
	mu.Unlock()
	if len(sd) != 1 || sd[0] != "streamA" {
		t.Fatalf("expected exactly one step-down for streamA, got %v", sd)
	}
	if !hasReason(read(), string(rejectOversizeChunk)) {
		t.Fatal("expected oversize_chunk reject audit")
	}
}

// --- Test 32: Audit_StreamLifecycle_And_IngestRejections ------------------

func TestAudit_StreamLifecycle_And_IngestRejections(t *testing.T) {
	relay := &spyRelay{}
	h, read := newAuditIngest(t, IngestDeps{Relay: relay})

	// (1) mint → stream_started
	tok := h.MintIngestToken("streamA")

	// (2) reject: wrong token while stream fresh → bad_token
	cBad := newFakeConn()
	cBad.feedText(initFrameBytes("streamA", "nope", "vp8"))
	cBad.closeIncoming()
	waitDone(t, serveAsync(h, cBad))

	// (3) valid connection consumes the single-use token → A goes dead (no lifecycle audit on accept)
	cOK := newFakeConn()
	cOK.feedText(initFrameBytes("streamA", tok, "vp8"))
	cOK.closeIncoming()
	waitDone(t, serveAsync(h, cOK))
	waitState(t, h, "streamA", streamDead)

	// (4) reconnect with the now-dead token → dead_token
	cDead := newFakeConn()
	cDead.feedText(initFrameBytes("streamA", tok, "vp8"))
	cDead.closeIncoming()
	waitDone(t, serveAsync(h, cDead))

	// (5) EndStream → stream_stopped
	h.EndStream("streamA")

	recs := read()
	if got := countEvent(recs, eventIngestStreamStarted); got != 1 {
		t.Fatalf("expected exactly 1 stream_started, got %d", got)
	}
	if got := countEvent(recs, eventIngestStreamStopped); got != 1 {
		t.Fatalf("expected exactly 1 stream_stopped, got %d", got)
	}
	if !hasReason(recs, string(rejectBadToken)) {
		t.Fatalf("expected bad_token reject audit, reasons=%v", rejectReasonsFor(recs))
	}
	if !hasReason(recs, string(rejectDeadToken)) {
		t.Fatalf("expected dead_token reject audit, reasons=%v", rejectReasonsFor(recs))
	}
	// Every rejection must carry decision=deny (explainability, FR-024/SC-009).
	for _, r := range recs {
		if r.Event == eventIngestRejected {
			if dec, _ := r.Fields["decision"].(string); dec != "deny" {
				t.Fatalf("reject audit missing decision=deny: %+v", r.Fields)
			}
		}
	}
}

// --- FR-012: LoopbackOnly_RejectsRemote ----------------------------------

func TestIngest_LoopbackOnly_RejectsRemote(t *testing.T) {
	relay := &spyRelay{}
	h, read := newAuditIngest(t, IngestDeps{Relay: relay})

	req := httptest.NewRequest(http.MethodGet, captureIngestPath, nil)
	req.RemoteAddr = "203.0.113.9:4444" // public IP, non-loopback
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback, got %d", rec.Code)
	}
	if !hasReason(read(), string(rejectNonLoopback)) {
		t.Fatal("expected non_loopback reject audit")
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:5000", true},
		{"127.0.0.5:1", true},
		{"[::1]:8080", true},
		{"localhost", true},
		{"10.0.0.4:80", false},
		{"192.168.1.9:443", false},
		{"203.0.113.1:22", false},
		{"169.254.169.254:80", false},
		{"[fe80::1]:80", false},
	}
	for _, tc := range cases {
		if got := isLoopbackRemoteAddr(tc.addr); got != tc.want {
			t.Errorf("isLoopbackRemoteAddr(%q)=%v, want %v", tc.addr, got, tc.want)
		}
	}
}

// --- Tests 29/30 half: single-connection + dead-token ---------------------

func TestIngest_SingleConnection_And_DeadToken(t *testing.T) {
	relay := &spyRelay{}
	h, read := newAuditIngest(t, IngestDeps{Relay: relay})
	tok := h.MintIngestToken("streamA")

	// conn1 connects and stays live (do not close its incoming yet).
	c1 := newFakeConn()
	c1.feedText(initFrameBytes("streamA", tok, "vp8"))
	done1 := serveAsync(h, c1)
	waitState(t, h, "streamA", streamConnected)

	// conn2 presents the same live token → single-connection reject.
	c2 := newFakeConn()
	c2.feedText(initFrameBytes("streamA", tok, "vp8"))
	c2.closeIncoming()
	waitDone(t, serveAsync(h, c2))

	// Tear down conn1 → token becomes dead (single-use, no same-token reconnect).
	c1.closeIncoming()
	waitDone(t, done1)
	waitState(t, h, "streamA", streamDead)

	// conn3 presents the now-dead token → dead_token reject.
	c3 := newFakeConn()
	c3.feedText(initFrameBytes("streamA", tok, "vp8"))
	c3.closeIncoming()
	waitDone(t, serveAsync(h, c3))

	recs := read()
	if !hasReason(recs, string(rejectDuplicateConn)) {
		t.Fatalf("expected duplicate_connection reject, reasons=%v", rejectReasonsFor(recs))
	}
	if !hasReason(recs, string(rejectDeadToken)) {
		t.Fatalf("expected dead_token reject, reasons=%v", rejectReasonsFor(recs))
	}
}

// --- happy path: valid chunks relayed with correct fields -----------------

func TestIngest_RelaysValidChunks(t *testing.T) {
	relay := &spyRelay{}
	audioCodec := "opus"
	h := NewCaptureIngestHandler(IngestDeps{Relay: relay})
	tok := h.MintIngestToken("streamA")

	// init with an audio codec so the audio discriminator maps correctly.
	initB, _ := json.Marshal(generated.BrowserIngestInitFrame{
		Type:       string(generated.WsFrameTypeBrowserIngestInit),
		StreamId:   "streamA",
		Token:      tok,
		VideoCodec: "avc1.4D401E",
		HasAudio:   true,
		AudioCodec: &audioCodec,
	})

	c := newFakeConn()
	c.feedText(initB)
	c.feedBinary(chunkBytes(0, 0, chunkVideoKey, []byte("KEYFRAME")))
	c.feedBinary(chunkBytes(1, 33, chunkVideoDelta, []byte("delta")))
	c.feedBinary(chunkBytes(2, 66, chunkAudio, []byte("opusframe")))
	c.closeIncoming()
	waitDone(t, serveAsync(h, c))

	got := relay.snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3 relayed chunks, got %d", len(got))
	}
	// video keyframe
	if got[0].Kind != "video" || !got[0].Key || got[0].Codec != "avc1.4D401E" ||
		got[0].Seq != 0 || got[0].TS != 0 || string(got[0].Payload) != "KEYFRAME" {
		t.Fatalf("keyframe chunk decoded wrong: %+v", got[0])
	}
	// video delta
	if got[1].Kind != "video" || got[1].Key || got[1].Seq != 1 || got[1].TS != 33 ||
		string(got[1].Payload) != "delta" {
		t.Fatalf("delta chunk decoded wrong: %+v", got[1])
	}
	// audio
	if got[2].Kind != "audio" || got[2].Key || got[2].Codec != "opus" || got[2].Seq != 2 ||
		got[2].TS != 66 || string(got[2].Payload) != "opusframe" {
		t.Fatalf("audio chunk decoded wrong: %+v", got[2])
	}
}

// --- max seq/ts boundary (DS-2 row 5) -------------------------------------

func TestIngest_MaxSeqTsBoundary(t *testing.T) {
	relay := &spyRelay{}
	h := NewCaptureIngestHandler(IngestDeps{Relay: relay})
	tok := h.MintIngestToken("streamA")

	c := newFakeConn()
	c.feedText(initFrameBytes("streamA", tok, "vp8"))
	c.feedBinary(chunkBytes(4294967295, 18446744073709551615, chunkVideoDelta, make([]byte, 65535)))
	c.closeIncoming()
	waitDone(t, serveAsync(h, c))

	got := relay.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(got))
	}
	if got[0].Seq != 4294967295 || got[0].TS != 18446744073709551615 {
		t.Fatalf("seq/ts max not preserved: seq=%d ts=%d", got[0].Seq, got[0].TS)
	}
}

// --- malformed envelope: framing corruption never relayed -----------------

func TestIngest_MalformedChunk_NotRelayed(t *testing.T) {
	relay := &spyRelay{}
	h := NewCaptureIngestHandler(IngestDeps{Relay: relay})
	tok := h.MintIngestToken("streamA")

	// declared len says 100 but only 4 payload bytes present → len mismatch.
	bad := make([]byte, ingestChunkHeaderLen+4)
	binary.BigEndian.PutUint32(bad[13:17], 100)
	tooShort := []byte{1, 2, 3} // shorter than the 17-byte header

	c := newFakeConn()
	c.feedText(initFrameBytes("streamA", tok, "vp8"))
	c.feedBinary(bad)
	c.feedBinary(tooShort)
	c.feedBinary(chunkBytes(9, 9, chunkVideoKey, []byte("valid"))) // this one should still relay
	c.closeIncoming()
	waitDone(t, serveAsync(h, c))

	got := relay.snapshot()
	if len(got) != 1 || string(got[0].Payload) != "valid" {
		t.Fatalf("expected only the valid chunk relayed, got %d: %+v", len(got), got)
	}
}

// --- downstream: FeedFrame writes a correct browser_frame_feed envelope ----

func TestFeedFrame_WritesFrameFeedEnvelope(t *testing.T) {
	relay := &spyRelay{}
	h := NewCaptureIngestHandler(IngestDeps{Relay: relay})
	tok := h.MintIngestToken("streamA")

	c := newFakeConn()
	c.feedText(initFrameBytes("streamA", tok, "vp8"))
	done := serveAsync(h, c)
	waitState(t, h, "streamA", streamConnected)

	jpeg := []byte("JPEGDATA-0123456789")
	h.FeedFrame("streamA", jpeg, 7, 12345)

	select {
	case <-c.writeSig:
	case <-time.After(2 * time.Second):
		t.Fatal("FeedFrame did not produce a downstream write")
	}

	w := c.writes()
	if len(w) == 0 {
		t.Fatal("no frame-feed written")
	}
	msg := w[0]
	if len(msg) != ingestFrameFeedHeaderLen+len(jpeg) {
		t.Fatalf("frame-feed length wrong: %d", len(msg))
	}
	seq := binary.BigEndian.Uint32(msg[0:4])
	ts := binary.BigEndian.Uint64(msg[4:12])
	ln := binary.BigEndian.Uint32(msg[12:16])
	if seq != 7 || ts != 12345 || int(ln) != len(jpeg) || string(msg[16:]) != string(jpeg) {
		t.Fatalf("frame-feed envelope wrong: seq=%d ts=%d len=%d payload=%q", seq, ts, ln, msg[16:])
	}

	// FeedFrame to an unknown / ended stream is a silent no-op (no panic).
	h.FeedFrame("no-such-stream", jpeg, 1, 1)

	c.closeIncoming()
	waitDone(t, done)
}

// --- envelope codec round-trips -------------------------------------------

func TestChunkEnvelope_RoundTrip(t *testing.T) {
	payload := []byte("hello-encoded-chunk")
	msg := chunkBytes(42, 9999, chunkVideoKey, payload)
	seq, ts, keyByte, got, err := decodeChunkEnvelope(msg)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if seq != 42 || ts != 9999 || keyByte != chunkVideoKey || string(got) != string(payload) {
		t.Fatalf("round-trip mismatch: seq=%d ts=%d key=%d payload=%q", seq, ts, keyByte, got)
	}

	if _, _, _, _, err := decodeChunkEnvelope([]byte{1, 2}); err != errEnvelopeTooShort {
		t.Fatalf("expected errEnvelopeTooShort, got %v", err)
	}
	mism := make([]byte, ingestChunkHeaderLen+2)
	binary.BigEndian.PutUint32(mism[13:17], 999)
	if _, _, _, _, err := decodeChunkEnvelope(mism); err != errEnvelopeLenMismatch {
		t.Fatalf("expected errEnvelopeLenMismatch, got %v", err)
	}
}

func TestFrameFeed_EncodeRoundTrip(t *testing.T) {
	jpeg := []byte("some-jpeg-bytes")
	msg := encodeFrameFeed(3, 77, jpeg)
	if binary.BigEndian.Uint32(msg[0:4]) != 3 ||
		binary.BigEndian.Uint64(msg[4:12]) != 77 ||
		int(binary.BigEndian.Uint32(msg[12:16])) != len(jpeg) ||
		string(msg[16:]) != string(jpeg) {
		t.Fatalf("encodeFrameFeed round-trip failed: %v", msg)
	}
}

// --- token unguessability -------------------------------------------------

func TestMintIngestToken_UnguessableAndUnique(t *testing.T) {
	h := NewCaptureIngestHandler(IngestDeps{Relay: &spyRelay{}})
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := h.MintIngestToken("stream-" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
		if len(tok) != 64 { // 32 bytes hex
			t.Fatalf("token not 256-bit hex: %q (len %d)", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("duplicate token minted: %q", tok)
		}
		seen[tok] = true
	}
}

// --- re-mint invalidates the old token (CRIT-002 drop recovery) ------------

func TestMintIngestToken_RemintInvalidatesOldToken(t *testing.T) {
	relay := &spyRelay{}
	h, read := newAuditIngest(t, IngestDeps{Relay: relay})
	oldTok := h.MintIngestToken("streamA")
	newTok := h.MintIngestToken("streamA") // re-mint (drop recovery, W2-M)

	if oldTok == newTok {
		t.Fatal("re-mint must produce a fresh token")
	}

	// Old token now dead → reject.
	cOld := newFakeConn()
	cOld.feedText(initFrameBytes("streamA", oldTok, "vp8"))
	cOld.closeIncoming()
	waitDone(t, serveAsync(h, cOld))

	// New token accepted.
	cNew := newFakeConn()
	cNew.feedText(initFrameBytes("streamA", newTok, "vp8"))
	cNew.feedBinary(chunkBytes(0, 0, chunkVideoKey, []byte("frame")))
	cNew.closeIncoming()
	waitDone(t, serveAsync(h, cNew))

	if relay.count() != 1 {
		t.Fatalf("new token should relay 1 chunk, got %d", relay.count())
	}
	recs := read()
	// Exactly one stream_started for the whole logical stream despite the re-mint.
	if got := countEvent(recs, eventIngestStreamStarted); got != 1 {
		t.Fatalf("expected exactly 1 stream_started across re-mint, got %d", got)
	}
}
