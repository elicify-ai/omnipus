// ws_sequence_test.go — issue #823 phase 2: per-conversation sequence numbers
// for reconnect catch-up.
//
// These tests pin the properties the founder's spec requires: numbering is
// per-session (not global, not per-connection), strictly increasing and
// gap-free; a client's cursor is per session; re-delivery is byte-exact and
// therefore idempotent; the terminal frame is itself numbered; and a position
// the gateway cannot serve contiguously produces a snapshot instead of a
// silent drop.

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// sendRecvConn returns a connection whose frames the test can read back, and a
// helper that drains whatever has been enqueued so far.
func sendRecvConn() (*wsConn, func() [][]byte) {
	wc := &wsConn{
		sendCh: make(chan []byte, 4096),
		doneCh: make(chan struct{}),
	}
	drain := func() [][]byte {
		var out [][]byte
		for {
			select {
			case b := <-wc.sendCh:
				out = append(out, b)
			default:
				return out
			}
		}
	}
	return wc, drain
}

// tokenFrame builds a minimal session-scoped token frame.
func tokenFrame(sessionID, content string) generated.TokenFrame {
	return generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		SessionId: sessionID,
		Content:   content,
	}
}

// decodeSeq returns the seq field of a marshalled frame, or -1 when absent.
func decodeSeq(t *testing.T, raw []byte) int64 {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	v, ok := m["seq"]
	if !ok {
		return -1
	}
	f, ok := v.(float64)
	require.True(t, ok, "seq must be numeric, got %T", v)
	return int64(f)
}

// ─────────────────────────────────────────────────────────────────────────────
// Numbering: per session, monotonic, gap-free
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_NumberingIsMonotonicAndGapFreePerSession pins the core invariant:
// two sessions advance their own counters independently, and each session's
// numbers are contiguous — no holes a client could mistake for a lost frame.
func TestSeq_NumberingIsMonotonicAndGapFreePerSession(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionA = "session-a"
	const sessionB = "session-b"

	var aSeqs, bSeqs []int64
	for i := 0; i < 5; i++ {
		var a, b string
		if i%2 == 0 {
			a, b = sessionA, sessionB
		} else {
			a, b = sessionB, sessionA
		}
		aSeqs = append(aSeqs, int64(handler.nextSessionSeq(a, "token", []byte(`{}`))))
		bSeqs = append(bSeqs, int64(handler.nextSessionSeq(b, "token", []byte(`{}`))))
	}

	for i, s := range aSeqs {
		require.Equal(t, int64(i+1), s, "session A seqs must be 1..N with no gaps")
	}
	for i, s := range bSeqs {
		require.Equal(t, int64(i+1), s, "session B counts independently of A")
	}
	assert.Equal(t, uint64(5), handler.sessionHighSeq(sessionA))
	assert.Equal(t, uint64(5), handler.sessionHighSeq(sessionB))
}

// TestSeq_MultiSessionIsolation_CursorsDoNotInterfere is the interleaving case
// from the spec: two sessions receiving events interleaved, each catching up
// from its own position, must each receive exactly their own missed frames.
func TestSeq_MultiSessionIsolation_CursorsDoNotInterfere(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionA = "iso-a"
	const sessionB = "iso-b"

	// Interleave: A1 B1 A2 B2 A3 B3.
	for i := 0; i < 3; i++ {
		handler.nextSessionSeq(sessionA, "token", []byte(`{"n":"a`+string(rune('1'+i))+`"}`))
		handler.nextSessionSeq(sessionB, "token", []byte(`{"n":"b`+string(rune('1'+i))+`"}`))
	}

	// A catching up from 1 must get A's frames 2 and 3 only.
	aFrames, _, ok := handler.catchUpFrames(sessionA, 1)
	require.True(t, ok, "session A's position must be servable")
	require.Len(t, aFrames, 2, "A asks from seq 2 and is owed exactly its own 2 frames")
	assert.Equal(t, uint64(2), aFrames[0].seq)
	assert.Equal(t, uint64(3), aFrames[1].seq)
	for _, f := range aFrames {
		assert.Contains(t, string(f.raw), `"n":"a`, "A must never receive B's frames")
	}

	bFrames, _, ok := handler.catchUpFrames(sessionB, 2)
	require.True(t, ok)
	require.Len(t, bFrames, 1, "B is one frame behind its own head")
	assert.Equal(t, uint64(3), bFrames[0].seq)
	assert.Contains(t, string(bFrames[0].raw), `"n":"b3`)
}

// TestSeq_CatchUpReDeliveryIsByteExactAndIdempotent pins the idempotency rule:
// asking twice for the same range yields the identical bytes and the identical
// numbers, so a client that applies a frame twice cannot be changed by it.
func TestSeq_CatchUpReDeliveryIsByteExactAndIdempotent(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sid = "idem"
	wc, drain := sendRecvConn()
	for i := 0; i < 3; i++ {
		handler.emitSessionFrame(wc, string(generated.WsFrameTypeToken), tokenFrame(sid, "c"))
	}
	live := drain()
	require.Len(t, live, 3)

	first, _, ok := handler.catchUpFrames(sid, 0)
	require.True(t, ok)
	second, _, ok := handler.catchUpFrames(sid, 0)
	require.True(t, ok)

	require.Len(t, first, 3)
	require.Len(t, second, 3)
	for i := range first {
		assert.Equal(t, first[i].seq, second[i].seq, "re-delivery must not renumber")
		assert.Equal(t, first[i].raw, second[i].raw, "re-delivery must be byte-exact")
		assert.Equal(t, live[i], first[i].raw, "the retained bytes are the ones the live client saw")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Snapshot fallback: never guess, never drop
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_CursorAheadIsRefusedWithSnapshotReason covers a cursor at or beyond
// the gateway's newest frame — the gateway-restart shape. Answering "you are up
// to date" would be a guess, so it must refuse and let the caller snapshot.
func TestSeq_CursorAheadIsRefusedWithSnapshotReason(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sid = "ahead"
	handler.nextSessionSeq(sid, "token", []byte(`{}`))

	// Strictly ahead of the gateway's head.
	frames, reason, ok := handler.catchUpFrames(sid, 99)
	assert.False(t, ok, "a position beyond the gateway's head cannot be served")
	assert.Empty(t, frames)
	assert.Equal(t, seqReasonCursorAhead, reason)

	// Exactly AT the head is also refused: frames are only emitted while a
	// client is attached, so "nothing after your cursor" does NOT prove
	// "nothing happened" — an outage with no listeners leaves no trace here.
	frames, reason, ok = handler.catchUpFrames(sid, 1)
	assert.False(t, ok, "a cursor at the head cannot prove the client is current")
	assert.Empty(t, frames)
	assert.Equal(t, seqReasonCursorAhead, reason)
}

// TestSeq_UnknownPositionIsRefused covers a cursor for a session the gateway has
// never emitted a numbered frame for — a fresh process, or a session that has
// only ever been read.
func TestSeq_UnknownPositionIsRefused(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	frames, reason, ok := handler.catchUpFrames("never-emitted", 4)
	assert.False(t, ok)
	assert.Empty(t, frames)
	assert.Equal(t, seqReasonUnknownPosition, reason)
}

// TestSeq_RetentionExceededIsRefused proves the window is bounded and, once a
// cursor falls out of it, the gateway refuses rather than serving a range with a
// hole in it.
func TestSeq_RetentionExceededIsRefused(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sid = "retention"
	// Trimming is batched: the window is reclaimed once it exceeds 150% of
	// seqJournalCap, so emit comfortably past that before expecting the oldest
	// frames to be gone.
	total := seqJournalCap * 2
	for i := 0; i < total; i++ {
		handler.nextSessionSeq(sid, "token", []byte(`{"i":1}`))
	}

	// The oldest frames are gone: a client that asks from 0 would need frames
	// 1..N that no longer exist.
	frames, reason, ok := handler.catchUpFrames(sid, 0)
	assert.False(t, ok, "a range the window no longer reaches must be refused")
	assert.Empty(t, frames)
	assert.Equal(t, seqReasonRetentionExceeded, reason)

	// A position still inside the window stays servable.
	head := handler.sessionHighSeq(sid)
	recent, reason, ok := handler.catchUpFrames(sid, head-1)
	require.True(t, ok, "the most recent position is always servable, reason=%q", reason)
	require.Len(t, recent, 1)
	assert.Equal(t, head, recent[0].seq)
}

// ─────────────────────────────────────────────────────────────────────────────
// Frame stamping
// ─────────────────────────────────────────────────────────────────────────────

// TestEmitSessionFrame_StampsSessionScopedFrames pins that the frames a client
// actually applies carry a number, and that the numbers increase per session.
func TestEmitSessionFrame_StampsSessionScopedFrames(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc, drain := sendRecvConn()
	const sid = "stamp"

	handler.emitSessionFrame(wc, string(generated.WsFrameTypeToken), tokenFrame(sid, "a"))
	handler.emitSessionFrame(wc, string(generated.WsFrameTypeDone), generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: sid,
		Stats:     &generated.DoneStats{},
	})

	frames := drain()
	require.Len(t, frames, 2)
	assert.Equal(t, int64(1), decodeSeq(t, frames[0]), "first session frame is seq 1")
	assert.Equal(t, int64(2), decodeSeq(t, frames[1]), "terminal done is its own numbered event")
}

// TestEmitSessionFrame_LeavesGlobalFramesUnnumbered keeps the numbering scoped
// to conversation state: a connection-scoped or global frame must not consume a
// session's number, or the client's cursor would advance on frames that say
// nothing about the session.
func TestEmitSessionFrame_LeavesGlobalFramesUnnumbered(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc, drain := sendRecvConn()

	handler.emitSessionFrame(wc, string(generated.WsFrameTypePong), generated.PongFrame{
		Type: string(generated.WsFrameTypePong),
	})
	handler.emitSessionFrame(wc, string(generated.WsFrameTypePlanStatus), generated.PlanStatusFrame{
		Type: string(generated.WsFrameTypePlanStatus),
	})

	for i, raw := range drain() {
		assert.Equal(t, int64(-1), decodeSeq(t, raw), "frame %d must carry no seq", i)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Attach-level catch-up
// ─────────────────────────────────────────────────────────────────────────────

// TestAttach_IncrementalCatchUpDeliversMissedFramesThenTerminator is the core
// reconnect path: a client that was attached, missed frames, and re-attaches
// with its cursor receives exactly the frames it missed — including the
// terminal done, which is what stops a finished answer from staying stuck in a
// running state — and nothing it already had.
func TestAttach_IncrementalCatchUpDeliversMissedFramesThenTerminator(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	sid := meta.ID

	// A first client is attached and applies two tokens; its cursor is 2.
	wcLive, liveDrain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-live"] = wcLive
	handler.mu.Unlock()

	handler.emitSessionFrame(wcLive, string(generated.WsFrameTypeToken), tokenFrame(sid, "one"))
	handler.emitSessionFrame(wcLive, string(generated.WsFrameTypeToken), tokenFrame(sid, "two"))
	require.Len(t, liveDrain(), 2)

	// While that client is away, the turn finishes: two more tokens and the
	// turn's own terminal done.
	handler.emitSessionFrame(wcLive, string(generated.WsFrameTypeToken), tokenFrame(sid, "three"))
	handler.emitSessionFrame(wcLive, string(generated.WsFrameTypeDone), generated.DoneFrame{
		Type:      string(generated.WsFrameTypeDone),
		SessionId: sid,
		Stats:     &generated.DoneStats{},
	})
	liveDrain()

	// It reconnects, asking from 3 (i.e. it has applied seq 1 and 2).
	cursor := int64(2)
	wcRe, reDrain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-re"] = wcRe
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-re", sid, nil, &cursor, nil, wcRe)

	frames := reDrain()
	require.NotEmpty(t, frames, "an incremental catch-up must deliver the missed frames")

	var sawToken, sawDone bool
	var seqs []int64
	for _, raw := range frames {
		var head struct {
			Type string `json:"type"`
			Seq  *int64 `json:"seq"`
		}
		require.NoError(t, json.Unmarshal(raw, &head))
		if head.Seq != nil {
			seqs = append(seqs, *head.Seq)
		}
		switch head.Type {
		case string(generated.WsFrameTypeToken):
			var tf generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &tf))
			if tf.Content == "three" {
				sawToken = true
				assert.False(t, tf.Replace != nil && *tf.Replace,
					"a live catch-up token for missed frames appends, it does not replace")
			}
			if tf.Content == "one" || tf.Content == "two" {
				t.Errorf("frame the client already applied was re-delivered: %q", tf.Content)
			}
		case string(generated.WsFrameTypeDone):
			var df generated.DoneFrame
			require.NoError(t, json.Unmarshal(raw, &df))
			// Only the turn's own terminal done (no replay-terminator shape)
			// closes the bubble; the attach terminator that follows carries
			// frames_emitted only.
			if df.Stats != nil && df.Stats.Tokens == nil && df.Stats.FramesEmitted == nil {
				sawDone = true
			}
		}
	}
	assert.True(t, sawToken, "the token produced during the outage must be delivered")
	assert.True(t, sawDone, "the terminal done must be delivered — otherwise the answer stays stuck running")

	// Numbers must be strictly increasing across everything delivered.
	for i := 1; i < len(seqs); i++ {
		assert.Greater(t, seqs[i], seqs[i-1], "delivered seqs must be strictly increasing")
	}
}

// TestAttach_NoCursorPerformsFullReplay preserves the first-load path: with no
// cursor the client gets replay history and a terminator, exactly as before the
// sequence work — and the terminator now also carries its cursor.
func TestAttach_NoCursorPerformsFullReplay(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID:        "entry-1",
		Role:      "user",
		Content:   "hello there",
		Timestamp: time.Now().UTC(),
	}))

	wc, drain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-fresh"] = wc
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-fresh", meta.ID, nil, nil, nil, wc)

	frames := drain()
	require.NotEmpty(t, frames)

	var sawReplay, sawTerminator bool
	for _, raw := range frames {
		var head struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &head))
		switch head.Type {
		case string(generated.WsFrameTypeReplayMessage):
			sawReplay = true
		case string(generated.WsFrameTypeDone):
			sawTerminator = true
			// The terminator is a control frame, not a conversation event: it
			// is delivered BEFORE the divert-buffered live frames, which were
			// emitted earlier in wall time but hold higher numbers. Stamping it
			// with a position would make the client discard those frames, so it
			// must stay unnumbered.
			assert.Equal(t, int64(-1), decodeSeq(t, raw),
				"the replay terminator must not claim a cursor")
			var df generated.DoneFrame
			require.NoError(t, json.Unmarshal(raw, &df))
			require.NotNil(t, df.Stats)
			assert.Nil(t, df.Stats.Tokens, "a replay terminator must not look like a turn done")
		}
	}
	assert.True(t, sawReplay, "a fresh client must receive its history")
	assert.True(t, sawTerminator, "a fresh client must receive the replay terminator")
}

// TestAttach_StaleCursorGetsSnapshotNotSilentGap pins the no-silent-drop rule:
// a cursor the gateway cannot serve yields an explicit snapshot notice naming
// the reason, never an empty catch-up that leaves the user's view stale.
func TestAttach_StaleCursorGetsSnapshotNotSilentGap(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Give the session a numbered frame so the gateway HAS a position to
	// compare against — without one the honest reason is "unknown_position"
	// (a position it cannot validate at all), which the unit test above covers.
	seed, seedDrain := sendRecvConn()
	handler.emitSessionFrame(seed, string(generated.WsFrameTypeToken), tokenFrame(meta.ID, "seed"))
	require.Len(t, seedDrain(), 1)

	// The client now claims a position the gateway has never reached — the
	// gateway-restart shape.
	cursor := int64(500)
	wc, drain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-stale"] = wc
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-stale", meta.ID, nil, &cursor, nil, wc)

	frames := drain()
	require.NotEmpty(t, frames)

	var sawSnapshot bool
	for _, raw := range frames {
		var head struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
			Seq    *int64 `json:"seq"`
		}
		require.NoError(t, json.Unmarshal(raw, &head))
		if head.Type == string(generated.WsFrameTypeSessionSnapshot) {
			sawSnapshot = true
			require.NotNil(t, head.Seq, "the snapshot tells the client which position to reset to")
			assert.Equal(t, int64(1), *head.Seq,
				"the reset position is the high-water mark from BEFORE the bind, so the "+
					"frames delivered after the snapshot are all above it")
			assert.Equal(t, seqReasonCursorAhead, head.Reason)
		}
	}
	assert.True(t, sawSnapshot,
		"an unservable cursor must produce an explicit snapshot, never a silent empty catch-up")
}
