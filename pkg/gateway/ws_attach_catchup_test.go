// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_attach_catchup_test.go — #823 Lane A step 4: the attach / reconnect
// algorithm (BE-DESIGN.md §4) driven through the real WSHandler, a real
// session store and the real wsStreamer.
//
// These tests replace the pre-#823 divert-channel suites
// (websocket_replay_order_test.go, websocket_replay_drain_drop_test.go,
// deleted with the mechanism under founder decision Q3). Guarantee mapping:
//
//	"live frames that arrive during replay are delivered after the replay,
//	 in order" (bug-5, TestReplay_Diverted*/ConcurrentUpdateDuringDrain)
//	    → TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate,
//	      TestAttach_H16_TurnEndsDuringCatchUp_DoneArrivesAfterIt
//	"a live frame dropped while reconnecting is reported to the client"
//	 (TestReplayDrain_DroppedFrameIsReportedToTheClient)
//	    → strengthened: TestAttach_HeldLiveFrames_NeverDropped (nothing is
//	      dropped at all, even well past the old 1000-frame divert buffer)
//	"a slow client cannot block the drain forever"
//	 (TestReplayDrain_SlowClientDeadline)
//	    → TestConnQueue_DirectWait_PacedBySocket_AndStallTimesOut and
//	      TestConnQueue_RealSocket_ClientNotReading_Gets4008
//	      (ws_conn_queue_test.go)

package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// wireFrame is a decode-only view of any server frame, for assertions.
type wireFrame struct { // not-wire-format: test-only decode target.
	Type      string  `json:"type"`
	Seq       *int64  `json:"seq"`
	Content   string  `json:"content"`
	MessageID *string `json:"message_id"`
	TurnID    *string `json:"turn_id"`
	Replace   *bool   `json:"replace"`
	Mode      string  `json:"mode"`
	Reason    *string `json:"reason"`
	BootID    *string `json:"boot_id"`
	ID        *string `json:"id"`
	Role      string  `json:"role"`
	Stats     *struct {
		Tokens        *float64 `json:"tokens"`
		TokensDropped *float64 `json:"tokens_dropped"`
	} `json:"stats"`
	raw []byte
}

func decodeWire(t *testing.T, raw []byte) wireFrame {
	t.Helper()
	var f wireFrame
	require.NoError(t, json.Unmarshal(raw, &f), "frame: %s", raw)
	f.raw = raw
	return f
}

// isTurnDone reports a turn's own done (it carries stats.tokens; the
// transcript replay's closing done never does).
func (f wireFrame) isTurnDone() bool {
	return f.Type == "done" && f.Stats != nil && f.Stats.Tokens != nil
}

// collectWire reads frames from wc (acting as writePump) until stop returns
// true or the deadline passes.
func collectWire(t *testing.T, wc *wsConn, stop func(wireFrame) bool) []wireFrame {
	t.Helper()
	var out []wireFrame
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-wc.sendCh:
			f := decodeWire(t, raw)
			out = append(out, f)
			if stop(f) {
				return out
			}
		case <-deadline:
			var types []string
			for _, f := range out {
				types = append(types, f.Type)
			}
			t.Fatalf("timed out collecting frames; got %v", types)
			return out
		}
	}
}

func typesOf(frames []wireFrame) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.Type
	}
	return out
}

func indexOfType(frames []wireFrame, typ string) int {
	for i, f := range frames {
		if f.Type == typ {
			return i
		}
	}
	return -1
}

// attachFixture is a real handler, a real session and a turn's streamer.
type attachFixture struct {
	h        *WSHandler
	store    *session.UnifiedStore
	sid      string
	streamer *wsStreamer
}

func newAttachFixture(t *testing.T, turnID, messageID string) *attachFixture {
	t.Helper()
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	st, ok := h.GetStreamer(context.Background(), "webchat", "chat-origin-"+turnID, meta.ID)
	require.True(t, ok)
	ws, ok := st.(*wsStreamer)
	require.True(t, ok)
	ws.SetTurnID(turnID)
	ws.SetMessageID(messageID)
	return &attachFixture{h: h, store: store, sid: meta.ID, streamer: ws}
}

func (f *attachFixture) newConn(chatID string) *wsConn {
	wc := &wsConn{sendCh: make(chan []byte, 64), doneCh: make(chan struct{})}
	f.h.mu.Lock()
	f.h.sessions[chatID] = wc
	f.h.mu.Unlock()
	return wc
}

func (f *attachFixture) cursorAt(seq uint64) *attachCursor {
	s := int64(seq)
	boot := f.h.hubs.bootID
	return &attachCursor{SinceSeq: &s, BootID: &boot}
}

func (f *attachFixture) head() uint64 {
	return f.h.hubs.getOrCreate(f.sid).snapshotHead()
}

// requireContiguousSeqs asserts the sequenced frames in frames are exactly
// from+1, from+2, … with no gap and no duplicate.
func requireContiguousSeqs(t *testing.T, frames []wireFrame, from int64) int64 {
	t.Helper()
	want := from + 1
	for _, f := range frames {
		if f.Seq == nil || f.Type == "catch_up_complete" || f.Type == "session_snapshot" {
			continue
		}
		require.Equal(t, want, *f.Seq, "sequenced frames must be contiguous (type %s)", f.Type)
		want++
	}
	return want - 1
}

func setAttachHook(t *testing.T, hook func(sessionID string)) {
	t.Helper()
	attachAfterBindHook = hook
	t.Cleanup(func() { attachAfterBindHook = nil })
}

// TestAttach_H1_TokensWithNoTabAttached_IncrementalCatchUpGetsAll pins H1
// through the real attach: tokens published while NO tab was attached are
// numbered and journaled anyway, and a tab re-attaching with its cursor gets
// every one of them — byte-identical to the journal — then catch_up_complete.
func TestAttach_H1_TokensWithNoTabAttached_IncrementalCatchUpGetsAll(t *testing.T) {
	f := newAttachFixture(t, "turn-h1", "msg-h1")
	start := f.head()
	var full strings.Builder
	for i := 0; i < 30; i++ {
		tok := "t" + strconv.Itoa(i) + " "
		full.WriteString(tok)
		require.NoError(t, f.streamer.Update(context.Background(), tok))
	}

	wc := f.newConn("chat-h1")
	f.h.handleAttachSession(context.Background(), "chat-h1", f.sid, f.cursorAt(start), wc)
	frames := collectWire(t, wc, func(w wireFrame) bool { return w.Type == "catch_up_complete" })

	require.Equal(t, "session_state", frames[0].Type, "session_state opens an incremental catch-up")
	var got strings.Builder
	for _, fr := range frames {
		if fr.Type == "token" {
			got.WriteString(fr.Content)
			require.NotNil(t, fr.MessageID)
			assert.Equal(t, "msg-h1", *fr.MessageID)
		}
	}
	assert.Equal(t, full.String(), got.String(), "every token published while no tab was attached must be caught up")
	last := requireContiguousSeqs(t, frames, int64(start))
	done := frames[len(frames)-1]
	assert.Equal(t, "incremental", done.Mode)
	require.NotNil(t, done.BootID)
	assert.Equal(t, f.h.hubs.bootID, *done.BootID)
	assert.Equal(t, last, *done.Seq, "catch_up_complete carries the head the tail ended at")
	assert.Equal(t, -1, indexOfType(frames, "session_snapshot"), "a servable cursor never gets a snapshot")
}

// TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate pins H4 with real
// concurrency (and re-expresses bug-5's ordering guarantee): a tab attaches
// with its cursor while tokens keep streaming; the frames it receives are
// seq-contiguous from its cursor to the turn's done, catch_up_complete sits
// between the caught-up and the live part, and the text reassembles exactly.
func TestAttach_H4_IncrementalWhileStreaming_NoGapNoDuplicate(t *testing.T) {
	f := newAttachFixture(t, "turn-h4", "msg-h4")
	require.NoError(t, f.streamer.Update(context.Background(), "t-first "))
	cursor := f.head()

	const n = 200
	var full strings.Builder
	full.WriteString("t-first ")
	toks := make([]string, n)
	for i := range toks {
		toks[i] = "t" + strconv.Itoa(i) + " "
		full.WriteString(toks[i])
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, tok := range toks {
			_ = f.streamer.Update(context.Background(), tok)
			time.Sleep(200 * time.Microsecond)
		}
	}()
	time.Sleep(5 * time.Millisecond)
	wc := f.newConn("chat-h4")
	f.h.handleAttachSession(context.Background(), "chat-h4", f.sid, f.cursorAt(cursor), wc)
	wg.Wait()
	require.NoError(t, f.streamer.Finalize(context.Background(), full.String()))

	frames := collectWire(t, wc, wireFrame.isTurnDone)
	cuc := indexOfType(frames, "catch_up_complete")
	require.Greater(t, cuc, 0)
	assert.Equal(t, "incremental", frames[cuc].Mode)
	requireContiguousSeqs(t, frames, int64(cursor))
	var got strings.Builder
	for _, fr := range frames {
		if fr.Type == "token" {
			got.WriteString(fr.Content)
		}
	}
	assert.Equal(t, strings.TrimPrefix(full.String(), "t-first "), got.String(),
		"everything after the cursor, exactly once")
	for _, fr := range frames[:cuc] {
		if fr.Seq != nil {
			assert.LessOrEqual(t, *fr.Seq, *frames[cuc].Seq, "caught-up frames are at or below W")
		}
	}
	for _, fr := range frames[cuc+1:] {
		if fr.Seq != nil {
			assert.Greater(t, *fr.Seq, *frames[cuc].Seq, "live frames after catch_up_complete are above W")
		}
	}
}

// TestAttach_H16_TurnEndsDuringCatchUp_DoneArrivesAfterIt re-expresses the
// #822 guarantee (founder decision Q3: the holding area is replaced by hold
// mode, the user-visible guarantee is kept): a turn that finishes while a
// tab is being caught up — incremental or snapshot — delivers its done to
// that tab strictly after catch_up_complete, never ahead of the catch-up,
// and no frame is lost.
func TestAttach_H16_TurnEndsDuringCatchUp_DoneArrivesAfterIt(t *testing.T) {
	for _, mode := range []string{"incremental", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			f := newAttachFixture(t, "turn-h16-"+mode, "msg-h16-"+mode)
			cursor := f.head()
			require.NoError(t, f.streamer.Update(context.Background(), "partial "))
			setAttachHook(t, func(string) {
				require.NoError(t, f.streamer.Update(context.Background(), "rest"))
				require.NoError(t, f.streamer.Finalize(context.Background(), "partial rest"))
			})
			var c *attachCursor
			if mode == "incremental" {
				c = f.cursorAt(cursor)
			}
			wc := f.newConn("chat-h16-" + mode)
			f.h.handleAttachSession(context.Background(), "chat-h16-"+mode, f.sid, c, wc)
			frames := collectWire(t, wc, wireFrame.isTurnDone)

			cuc := indexOfType(frames, "catch_up_complete")
			require.GreaterOrEqual(t, cuc, 0, "frames: %v", typesOf(frames))
			assert.Equal(t, mode, frames[cuc].Mode)
			last := frames[len(frames)-1]
			require.True(t, last.isTurnDone())
			assert.Greater(t, len(frames)-1, cuc,
				"BUG REGRESSION (#822): the turn's done must never jump ahead of the catch-up")
			require.NotNil(t, last.Seq)
			assert.Greater(t, *last.Seq, *frames[cuc].Seq)
			for _, fr := range frames[cuc+1:] {
				assert.NotNil(t, fr.Seq, "only numbered live frames follow the catch-up: %s", fr.Type)
			}
		})
	}
}

// TestAttach_H6_PersistedBetweenBindAndRead_NoDuplicateNoGap pins H6 and
// §4.2's central argument: the transcript is read AFTER the bind. The turn
// persists its answer (and publishes more text and its done) inside the
// window between the bind and the read. The snapshot then shows the answer
// once, from the transcript, and never also as a projection token; every
// frame numbered after the bind arrives after catch_up_complete.
func TestAttach_H6_PersistedBetweenBindAndRead_NoDuplicateNoGap(t *testing.T) {
	f := newAttachFixture(t, "turn-h6", "msg-h6")
	require.NoError(t, f.streamer.Update(context.Background(), "the whole "))
	setAttachHook(t, func(string) {
		require.NoError(t, f.streamer.Update(context.Background(), "answer"))
		require.NoError(t, f.streamer.Finalize(context.Background(), "the whole answer"))
	})
	wc := f.newConn("chat-h6")
	f.h.handleAttachSession(context.Background(), "chat-h6", f.sid, nil, wc)
	frames := collectWire(t, wc, wireFrame.isTurnDone)

	cuc := indexOfType(frames, "catch_up_complete")
	require.GreaterOrEqual(t, cuc, 0)
	assert.Equal(t, "snapshot", frames[cuc].Mode)
	var replayed []wireFrame
	for _, fr := range frames[:cuc] {
		if fr.Type == "replay_message" && fr.Role == "assistant" {
			replayed = append(replayed, fr)
		}
		assert.NotEqual(t, "token", fr.Type,
			"the answer persisted inside the bind→read window must not ALSO be sent as a projection token")
	}
	require.Len(t, replayed, 1)
	require.NotNil(t, replayed[0].ID)
	assert.Equal(t, "msg-h6", *replayed[0].ID)
	assert.Equal(t, "the whole answer", replayed[0].Content)
	// What was published after the bind arrives live, after the catch-up:
	// the rest of the text (the client ignores it — its bubble is complete)
	// and the turn's done.
	live := frames[cuc+1:]
	require.NotEmpty(t, live)
	assert.True(t, live[len(live)-1].isTurnDone())
	for _, fr := range live {
		require.NotNil(t, fr.Seq)
		assert.Greater(t, *fr.Seq, *frames[cuc].Seq)
	}
}

// TestAttach_SnapshotMidMessage_ProjectionCarriesTextSoFar: a first load
// (no cursor) while an answer is streaming gets the snapshot order
// session_snapshot · session_state · replay · token{replace, message_id,
// text so far} · catch_up_complete — and the next live token continues it.
func TestAttach_SnapshotMidMessage_ProjectionCarriesTextSoFar(t *testing.T) {
	f := newAttachFixture(t, "turn-proj", "msg-proj")
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "user-1", Role: "user", Content: "question", Timestamp: time.Now().UTC(), ClientMessageID: "c-1",
	}))
	require.NoError(t, f.streamer.Update(context.Background(), "half an "))
	wc := f.newConn("chat-proj")
	f.h.handleAttachSession(context.Background(), "chat-proj", f.sid, nil, wc)
	frames := collectWire(t, wc, func(w wireFrame) bool { return w.Type == "catch_up_complete" })

	require.GreaterOrEqual(t, len(frames), 4)
	assert.Equal(t, "session_snapshot", frames[0].Type)
	require.NotNil(t, frames[0].Reason)
	assert.Equal(t, reasonUnknownPosition, *frames[0].Reason)
	assert.Equal(t, "session_state", frames[1].Type, "session_state comes right after the snapshot wipe")
	tok := frames[len(frames)-2]
	require.Equal(t, "token", tok.Type, "frames: %v", typesOf(frames))
	assert.Equal(t, "half an ", tok.Content)
	require.NotNil(t, tok.Replace)
	assert.True(t, *tok.Replace)
	require.NotNil(t, tok.MessageID)
	assert.Equal(t, "msg-proj", *tok.MessageID)
	assert.Nil(t, tok.Seq, "snapshot frames are unsequenced")
	cuc := frames[len(frames)-1]
	assert.Equal(t, *frames[0].Seq, *cuc.Seq, "catch_up_complete mirrors the snapshot's seq")

	require.NoError(t, f.streamer.Update(context.Background(), "answer"))
	next := collectWire(t, wc, func(w wireFrame) bool { return w.Type == "token" })
	require.NotNil(t, next[len(next)-1].Seq)
	assert.Equal(t, *cuc.Seq+1, *next[len(next)-1].Seq, "live continues at W+1")
	assert.Equal(t, "answer", next[len(next)-1].Content)
}

// TestAttach_SnapshotReasons pins §3.3/§3.4 through the real attach: a
// cursor from another gateway run (boot id mismatch) or ahead of the head is
// answered with a snapshot carrying that reason.
func TestAttach_SnapshotReasons(t *testing.T) {
	f := newAttachFixture(t, "turn-reasons", "msg-reasons")
	require.NoError(t, f.streamer.Update(context.Background(), "x"))
	head := int64(f.head())
	otherBoot := "a-previous-run"
	ahead := head + 50
	cases := map[string]*attachCursor{
		reasonBootMismatch: {SinceSeq: &head, BootID: &otherBoot},
		reasonCursorAhead:  f.cursorAt(uint64(ahead)),
	}
	for want, cursor := range cases {
		t.Run(want, func(t *testing.T) {
			wc := f.newConn("chat-reason-" + want)
			f.h.handleAttachSession(context.Background(), "chat-reason-"+want, f.sid, cursor, wc)
			frames := collectWire(t, wc, func(w wireFrame) bool { return w.Type == "catch_up_complete" })
			require.Equal(t, "session_snapshot", frames[0].Type)
			require.NotNil(t, frames[0].Reason)
			assert.Equal(t, want, *frames[0].Reason)
			assert.Equal(t, "snapshot", frames[len(frames)-1].Mode)
		})
	}
}

// TestAttach_HeldLiveFrames_NeverDropped: live frames published while an
// attach is being answered are held and delivered — all of them, in order —
// even far past the old divert buffer's 1000-frame cap, where the pre-#823
// code dropped them and could only report the loss.
func TestAttach_HeldLiveFrames_NeverDropped(t *testing.T) {
	f := newAttachFixture(t, "turn-held", "msg-held")
	cursor := f.head()
	const burst = 2000
	setAttachHook(t, func(string) {
		for i := 0; i < burst; i++ {
			require.NoError(t, f.streamer.Update(context.Background(), "."))
		}
		require.NoError(t, f.streamer.Finalize(context.Background(), strings.Repeat(".", burst)))
	})
	wc := f.newConn("chat-held")
	f.h.handleAttachSession(context.Background(), "chat-held", f.sid, f.cursorAt(cursor), wc)
	frames := collectWire(t, wc, wireFrame.isTurnDone)
	tokens := 0
	for _, fr := range frames {
		if fr.Type == "token" {
			tokens++
		}
	}
	assert.Equal(t, burst, tokens, "every held live token must be delivered")
	requireContiguousSeqs(t, frames, int64(cursor))
	assert.Equal(t, int32(0), wc.closeCode.Load())
}

// TestStreamer_H3_TokensAndDoneByteIdenticalAcrossTabs pins H3 / §5 through
// the real streamer: two tabs on one session receive byte-identical token
// and done frames — done included, now that TokensDropped (a per-connection
// drop artifact) no longer exists.
func TestStreamer_H3_TokensAndDoneByteIdenticalAcrossTabs(t *testing.T) {
	f := newAttachFixture(t, "turn-h3", "msg-h3")
	a := f.newConn("chat-a")
	b := f.newConn("chat-b")
	bindTestConnToSession(f.h, "chat-a", f.sid, a)
	bindTestConnToSession(f.h, "chat-b", f.sid, b)
	for _, tok := range []string{"one ", "two ", "three"} {
		require.NoError(t, f.streamer.Update(context.Background(), tok))
	}
	require.NoError(t, f.streamer.Finalize(context.Background(), "one two three"))
	fa := collectWire(t, a, wireFrame.isTurnDone)
	fb := collectWire(t, b, wireFrame.isTurnDone)
	require.Equal(t, len(fa), len(fb))
	for i := range fa {
		assert.Equal(t, string(fa[i].raw), string(fb[i].raw), "frame %d", i)
	}
	done := fa[len(fa)-1]
	assert.Nil(t, done.Stats.TokensDropped, "tokens_dropped is never set (founder decision Q5)")
	require.NotNil(t, done.TurnID)
	assert.Equal(t, "turn-h3", *done.TurnID)
}

// TestSend_H17_FallbackTurnNumberedWithNoTabAttached pins H17: a turn with no
// streamed text (the webchatChannel.Send fallback) still produces a numbered
// token + done in the session's journal when no tab is attached, so a tab
// that attaches later catches it up.
func TestSend_H17_FallbackTurnNumberedWithNoTabAttached(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	wch := newWebchatChannel(h)
	require.NoError(t, wch.Send(context.Background(), bus.OutboundMessage{
		Channel: "webchat", ChatID: "chat-gone", SessionID: meta.ID, Content: "a reply nobody watched",
	}))
	hub := h.hubs.lookup(meta.ID)
	require.NotNil(t, hub, "the fallback reply must be published even with zero tabs")
	tokens := journalFramesOfType(t, hub, "token")
	dones := journalFramesOfType(t, hub, "done")
	require.Len(t, tokens, 1)
	require.Len(t, dones, 1)
	assert.Equal(t, "a reply nobody watched", tokens[0]["content"])
	tokenSeq, ok := tokens[0]["seq"].(float64)
	require.True(t, ok)
	doneSeq, ok := dones[0]["seq"].(float64)
	require.True(t, ok)
	assert.Equal(t, tokenSeq+1, doneSeq)
}

// TestAttach_FailedRebuild_NoCatchUpComplete pins #823 review item 9b: when a
// snapshot rebuild cannot be completed (history unreadable, replay aborted),
// the gateway must NOT send catch_up_complete{W} — that would move the
// client's cursor past a history it never received, so a later reconnect
// would resume from W and the missing part would be lost for good. It sends
// the error (+ done{replay_error}) instead, drops the connection's binding
// and its held live frames, and the client retries the attach.
func TestAttach_FailedRebuild_NoCatchUpComplete(t *testing.T) {
	f := newAttachFixture(t, "turn-fail", "msg-fail")
	require.NoError(t, f.streamer.Update(context.Background(), "before "))
	setAttachHook(t, func(string) {
		// A live frame published after the bind — it must not be delivered
		// behind a failed catch-up.
		require.NoError(t, f.streamer.Update(context.Background(), "after"))
	})
	wc := f.newConn("chat-fail")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the replay aborts on the cancelled context
	f.h.handleAttachSession(ctx, "chat-fail", f.sid, nil, wc)

	frames := drainWire(t, wc)
	assert.Equal(t, -1, indexOfType(frames, "catch_up_complete"),
		"a failed rebuild must never end with catch_up_complete: %v", typesOf(frames))
	assert.GreaterOrEqual(t, indexOfType(frames, "error"), 0, "the client is told the rebuild failed")
	for _, fr := range frames {
		if fr.Type == "session_snapshot" {
			continue // carries W as its position, it is not a numbered frame
		}
		assert.Nil(t, fr.Seq, "no numbered live frame may follow a failed catch-up (%s)", fr.Type)
	}
	assert.False(t, f.h.connBoundToSession(wc, f.sid), "the connection is unbound until it re-attaches")
}

// drainWire reads whatever else arrives within a short quiet period.
func drainWire(t *testing.T, wc *wsConn) []wireFrame {
	t.Helper()
	var out []wireFrame
	for {
		select {
		case raw := <-wc.sendCh:
			out = append(out, decodeWire(t, raw))
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

// TestAttach_N4_UnreadableHistory_NeverWipesTheClient pins the gateway half
// of final-review N4: session_snapshot tells the client to wipe its history
// for the session, so it must only be sent once the history it is about to
// be rebuilt from has actually been read. A read failure is answered with
// the error alone and the client's current view is left untouched.
func TestAttach_N4_UnreadableHistory_NeverWipesTheClient(t *testing.T) {
	f := newAttachFixture(t, "turn-n4", "msg-n4")
	orig := attachReadTranscript
	attachReadTranscript = func(*session.UnifiedStore, string) ([]session.TranscriptEntry, error) {
		return nil, assert.AnError
	}
	t.Cleanup(func() { attachReadTranscript = orig })
	wc := f.newConn("chat-n4")
	f.h.handleAttachSession(context.Background(), "chat-n4", f.sid, nil, wc)
	frames := drainWire(t, wc)
	assert.Equal(t, -1, indexOfType(frames, "session_snapshot"),
		"a history read failure must never send the wipe: %v", typesOf(frames))
	assert.Equal(t, -1, indexOfType(frames, "catch_up_complete"))
	assert.GreaterOrEqual(t, indexOfType(frames, "error"), 0)
}

// TestAttach_SeededTranscriptFreshHub_ReportsNoRunningTurn answers the
// e2e replay-fidelity (e) investigation: a session whose history was written
// straight to disk (never run through a live turn in this gateway process)
// and opened with no cursor must be answered with a snapshot that carries
// NOTHING a client could read as "a turn is running": no active_turn on
// session_state, no token frame, no numbered frame, exactly one done (the
// replay's own, with no turn_id), and catch_up_complete{snapshot} last.
func TestAttach_SeededTranscriptFreshHub_ReportsNoRunningTurn(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	base := time.Now().Add(-time.Hour).UTC()
	for i := 0; i < 10; i++ {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			ID: "entry-user-" + strconv.Itoa(i), Role: "user", Content: "Message " + strconv.Itoa(i),
			Timestamp: base.Add(time.Duration(2*i) * time.Second),
		}))
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
			ID: "entry-asst-" + strconv.Itoa(i), Role: "assistant", AgentID: "mia",
			Content:   "Response to message " + strconv.Itoa(i),
			Timestamp: base.Add(time.Duration(2*i+1) * time.Second),
			ToolCalls: []session.ToolCall{{
				ID: session.ToolCallID("tc-" + strconv.Itoa(i)), Tool: "shell", Status: "success", DurationMS: 30,
				Parameters: map[string]any{"cmd": "echo"}, Result: map[string]any{"stdout": "x"},
			}},
		}))
	}
	require.Nil(t, h.hubs.lookup(meta.ID), "precondition: no hub exists yet for the seeded session")

	wc := &wsConn{sendCh: make(chan []byte, 512), doneCh: make(chan struct{})}
	h.mu.Lock()
	h.sessions["chat-seeded"] = wc
	h.mu.Unlock()
	h.handleAttachSession(context.Background(), "chat-seeded", meta.ID, nil, wc)
	frames := drainWire(t, wc)
	t.Logf("frames: %v", typesOf(frames))

	require.NotEmpty(t, frames)
	assert.Equal(t, "session_snapshot", frames[0].Type)
	var dones, tokens int
	for _, f := range frames {
		switch f.Type {
		case "session_state":
			var st struct {
				ActiveTurn any `json:"active_turn"`
			}
			require.NoError(t, json.Unmarshal(f.raw, &st))
			assert.Nil(t, st.ActiveTurn, "a seeded, never-run session has no active turn")
		case "token":
			tokens++
		case "done":
			dones++
			assert.Nil(t, f.TurnID, "the only done is the replay's own, with no turn_id")
		}
		if f.Type != "session_snapshot" && f.Type != "catch_up_complete" {
			assert.Nil(t, f.Seq, "no numbered frame for a session with no live activity (%s)", f.Type)
		}
	}
	assert.Zero(t, tokens, "no token frame — nothing is streaming")
	assert.Equal(t, 1, dones, "exactly one done: the replay's closing done")
	last := frames[len(frames)-1]
	assert.Equal(t, "catch_up_complete", last.Type)
	assert.Equal(t, "snapshot", last.Mode)
}
