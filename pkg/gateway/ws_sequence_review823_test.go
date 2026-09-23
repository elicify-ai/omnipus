// ws_sequence_review823_test.go — regression coverage for REVIEW-OPUS-823's
// findings 1, 5, 6, 7, 8 against pkg/gateway (squad AX's lane). Finding 9's
// storage-order half is covered by TestSeq_DeadConnectionWindow's assertion
// that the retained journal is contiguous to the head; its wire-delivery-order
// half is a documented, un-closed gap — see ws_sequence.go's emitSessionFrame
// doc comment and the squad report.

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// frameHead is the minimal envelope every WS frame carries, used to route
// drained frames by type in these tests without decoding the full payload.
type frameHead struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
	Seq     *int64 `json:"seq"`
	Replace *bool  `json:"replace"`
}

func decodeHead(t *testing.T, raw []byte) frameHead {
	t.Helper()
	var h frameHead
	require.NoError(t, json.Unmarshal(raw, &h))
	return h
}

// ─────────────────────────────────────────────────────────────────────────────
// Finding 1: done numbered but unstored with no connections; catchUpFrames
// must verify contiguity up to the head else snapshot.
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_DeadConnectionWindow_ReconnectGetsCompleteAnswerIncludingDone
// simulates the exact scenario REVIEW-OPUS-823 finding 1 describes: a real
// network cut leaves the connection attached (per wsPongWait, up to 60s) while
// the turn keeps producing tokens, so they are numbered and stored; the
// gateway then notices the drop and the connection is removed BEFORE the
// turn's own Finalize (sendDone) runs, so sendDone sees ZERO bound
// connections for its terminal frame. A reconnect asking for everything since
// the last token it saw must get the WHOLE rest of the answer, including a
// terminal done frame whose seq reaches the session's head — not a
// truncated-but-successful catch-up with the ending silently missing.
func TestSeq_DeadConnectionWindow_ReconnectGetsCompleteAnswerIncludingDone(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID

	streamerAny, ok := handler.GetStreamer(context.Background(), "webchat", "chat-origin", sid)
	require.True(t, ok)

	// Step 1: the connection is attached and the turn streams while the
	// browser is still receiving — the ordinary case.
	wc, drain := sendRecvConn()
	const chatID = "chat-origin-conn"
	handler.mu.Lock()
	handler.sessions[chatID] = wc
	handler.sessionIDs[chatID] = sid
	handler.mu.Unlock()

	require.NoError(t, streamerAny.Update(context.Background(), "the network "))
	require.NoError(t, streamerAny.Update(context.Background(), "is about to "))
	seenFrames := drain()
	require.Len(t, seenFrames, 2)
	lastSeenSeq := *decodeHead(t, seenFrames[1]).Seq

	// Step 2: the network drops. Per wsPongWait, the gateway keeps the
	// connection attached for up to 60s — tokens keep being numbered and
	// stored during that window, exactly as the review describes.
	require.NoError(t, streamerAny.Update(context.Background(), "drop."))
	drain()

	// Step 3: the gateway notices the drop and tears the connection down —
	// BEFORE the turn ends. This is the ~60s-later moment finding 1 is about:
	// resolveSessionConnsLocked will now find ZERO targets for this session.
	handler.mu.Lock()
	delete(handler.sessions, chatID)
	delete(handler.sessionIDs, chatID)
	handler.mu.Unlock()

	// Step 4: the turn finishes with nobody attached. This is the exact call
	// that used to spend a sequence number for "done" without ever storing a
	// frame for it (wsStreamerFinalize.sendDone, before the #823 review
	// finding 1 fix).
	require.NoError(t, streamerAny.Finalize(context.Background(), "the network is about to drop."))

	headBeforeReconnect := handler.sessionHighSeq(sid)
	require.Greater(t, headBeforeReconnect, uint64(lastSeenSeq),
		"the done frame must have consumed at least one more number than the browser last saw")

	// Step 5: reattach with since_seq = the last position the browser
	// actually applied, exactly like a real reconnect.
	wcRe, reDrain := sendRecvConn()
	const reChatID = "chat-reconnect"
	handler.mu.Lock()
	handler.sessions[reChatID] = wcRe
	handler.mu.Unlock()
	cursor := lastSeenSeq
	handler.handleAttachSession(context.Background(), reChatID, sid, nil, &cursor, nil, wcRe)

	frames := reDrain()
	require.NotEmpty(t, frames)

	var sawSnapshot bool
	var sawDone bool
	var doneSeq int64
	var reassembled strings.Builder
	for _, raw := range frames {
		h := decodeHead(t, raw)
		switch h.Type {
		case string(generated.WsFrameTypeSessionSnapshot):
			sawSnapshot = true
		case string(generated.WsFrameTypeToken):
			reassembled.WriteString(h.Content)
		case string(generated.WsFrameTypeDone):
			// emitIncrementalCatchUp re-delivers this turn's OWN numbered
			// done frame (the one sendDone's fix is about) followed by
			// emitIncrementalTerminator's unnumbered replay terminator
			// (stats.frames_emitted only — deliberately no seq, see its doc
			// comment). Only the numbered one is the turn's real ending;
			// skip the terminator rather than mistake it for a missing seq.
			if h.Seq != nil {
				sawDone = true
				doneSeq = *h.Seq
			}
		}
	}

	// The core assertion: either the gateway proved the range intact (no
	// snapshot, and the done frame it delivers reaches the actual head) OR it
	// safely fell back to a snapshot — but it must NEVER silently claim a
	// complete, gap-free incremental catch-up that stops short of the turn's
	// real ending. That silent-truncation outcome is finding 1's bug.
	if !sawSnapshot {
		assert.True(t, sawDone, "an incremental catch-up that is not a snapshot must include the turn's terminal done frame — "+
			"finding 1: sendDone used to spend a seq number for done without storing a frame when zero connections were attached")
		assert.Equal(t, int64(headBeforeReconnect), doneSeq,
			"the delivered done frame's seq must equal the session's actual head — a catch-up that silently stops "+
				"short of the head (finding 1) is indistinguishable, on the wire, from a lost frame")
		assert.Contains(t, reassembled.String(), "drop.",
			"the reassembled text must include the tokens produced during the dead-connection window")
	}
}

// TestSeq_CatchUpFrames_RefusesRangeThatDoesNotReachHead is a narrower,
// direct unit test of catchUpFrames' contiguity-to-head defense (finding 1's
// second half): even if a future bug again let the retained window fall
// short of the session's high-water mark, catchUpFrames must refuse rather
// than silently serve the short range as if it were complete.
func TestSeq_CatchUpFrames_RefusesRangeThatDoesNotReachHead(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sid = "gap-to-head"
	handler.mu.Lock()
	handler.sequences = map[string]*sessionSeq{
		sid: {
			high: 5, // a 6th frame (done) was numbered...
			frames: []sequencedFrame{
				{seq: 1, kind: "token", raw: []byte(`{"type":"token","seq":1}`)},
				{seq: 2, kind: "token", raw: []byte(`{"type":"token","seq":2}`)},
				// ...but seq 3, 4, 5 were never stored — reproducing exactly
				// the shape finding 1's bug used to produce.
			},
		},
	}
	handler.mu.Unlock()

	frames, reason, ok := handler.catchUpFrames(sid, 0)
	assert.False(t, ok, "a range that does not reach the session's head must be refused, not served as if complete")
	assert.Empty(t, frames)
	assert.Equal(t, seqReasonRetentionExceeded, reason)
}

// ─────────────────────────────────────────────────────────────────────────────
// Finding 5: numbered error frames must respect the replay divert, like done.
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_NumberedErrorFrameDuringReplay_IsDivertedNotJumpedAhead proves a
// NUMBERED session error is diverted into the replay buffer while a
// connection is mid-replay, exactly like "done" — instead of bypassing the
// divert and reaching the client immediately, ahead of the still-pending
// replay/catch-up frames (finding 5).
func TestSeq_NumberedErrorFrameDuringReplay_IsDivertedNotJumpedAhead(t *testing.T) {
	wc, drain := sendRecvConn()
	wc.replayDivertCh = make(chan []byte, 8)
	wc.isReplayingLive.Store(true)

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	sid := "err-divert"
	errMsg := "model provider error"
	handler.emitSessionFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:      string(generated.WsFrameTypeError),
		SessionId: &sid,
		Message:   errMsg,
	})

	// While replaying, sendCh must stay empty — the numbered error must have
	// gone into the divert buffer instead of jumping the queue.
	assert.Empty(t, drain(), "a numbered error frame emitted during replay must not bypass the divert buffer")

	select {
	case raw := <-wc.replayDivertCh:
		h := decodeHead(t, raw)
		assert.Equal(t, string(generated.WsFrameTypeError), h.Type)
		require.NotNil(t, h.Seq, "the diverted error must still carry the seq it was assigned")
	default:
		t.Fatal("the numbered error frame must have been diverted into replayDivertCh")
	}
}

// TestSeq_UnnumberedErrorFrame_StillBypassesDivert proves the fix is scoped
// correctly: an UNNUMBERED error (pre-session validation failure, connection-
// degraded warning) has no cursor position to protect and must keep
// bypassing the divert exactly as before — this is not a behavior change for
// that class of error.
func TestSeq_UnnumberedErrorFrame_StillBypassesDivert(t *testing.T) {
	wc, drain := sendRecvConn()
	wc.replayDivertCh = make(chan []byte, 8)
	wc.isReplayingLive.Store(true)

	sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: "session not found",
	})

	frames := drain()
	require.Len(t, frames, 1, "an unnumbered error must still reach sendCh immediately, bypassing the divert")
	assert.Equal(t, string(generated.WsFrameTypeError), decodeHead(t, frames[0]).Type)
}

// ─────────────────────────────────────────────────────────────────────────────
// Finding 6: planCatchUp (now resolveCatchUpLocked) must run inside
// bindConnection's own h.mu critical section.
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_ReconnectDuringConcurrentSecondTabActivity_NoFrameLost is a
// best-effort concurrency stress test for finding 6: a second, still-attached
// tab keeps a turn's tokens flowing while a first tab reconnects with a
// since_seq cursor. Every token the second tab produces must be observable to
// the reconnecting tab either in its catch-up list or live afterward — never
// neither. The bug window finding 6 describes (a frame numbered between
// resolveCatchUpLocked's old standalone call and bindConnection's later
// diversion-arm step) was microseconds wide, so this test cannot force the
// old interleaving deterministically; it is included as supporting evidence
// that the fixed code — which computes the catch-up list and arms diversion
// in the SAME h.mu critical section — has no observable gap across many
// randomly-timed iterations. See the squad report for the honest limits of
// this reproduction.
func TestSeq_ReconnectDuringConcurrentSecondTabActivity_NoFrameLost(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID

	streamerAny, ok := handler.GetStreamer(context.Background(), "webchat", "chat-tab1", sid)
	require.True(t, ok)

	wc1, drain1 := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-tab1"] = wc1
	handler.sessionIDs["chat-tab1"] = sid
	handler.mu.Unlock()

	const preTokens = 5
	for i := 0; i < preTokens; i++ {
		require.NoError(t, streamerAny.Update(context.Background(), "x"))
	}
	pre := drain1()
	require.Len(t, pre, preTokens)
	cursor := *decodeHead(t, pre[preTokens-1]).Seq

	// A second producer keeps emitting for the SAME session concurrently with
	// the reconnect below — tab1 stays "attached" (still in h.sessions), the
	// scenario finding 6 describes.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			_ = streamerAny.Update(context.Background(), "y")
		}
	}()

	wc2, drain2 := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-tab2"] = wc2
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-tab2", sid, nil, &cursor, nil, wc2)
	<-done

	// Drain both connections' catch-up/live frames and confirm every
	// sequence number the gateway ever assigned for this session between
	// cursor+1 and its final head is accounted for exactly once across
	// (tab2's catch-up ∪ tab2's own live frames received after attach ∪
	// tab1's own live stream) — i.e. nothing vanished into the gap the
	// review describes.
	seen := map[int64]bool{}
	for _, raw := range drain2() {
		if h := decodeHead(t, raw); h.Seq != nil {
			seen[*h.Seq] = true
		}
	}
	for _, raw := range drain1() {
		if h := decodeHead(t, raw); h.Seq != nil {
			seen[*h.Seq] = true
		}
	}
	head := handler.sessionHighSeq(sid)
	for want := cursor + 1; want <= int64(head); want++ {
		assert.True(t, seen[want], "seq %d must have reached SOME connection (catch-up or live) — a missing "+
			"number here is finding 6's silent loss", want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Finding 7: a gateway boot ID so a stale cursor after a restart gets a
// snapshot instead of being read against the wrong counter.
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_BootMismatch_ForcesSnapshotEvenWhenCursorLooksServable reproduces
// finding 7's exact scenario: after a (simulated) gateway restart, another
// device has already pushed the new counter forward, so a stale client's
// old since_seq now falls WITHIN the new, unrelated window and would be
// answered as a normal incremental catch-up if the gateway only looked at
// the numbers. A boot_id mismatch must force a snapshot regardless.
func TestSeq_BootMismatch_ForcesSnapshotEvenWhenCursorLooksServable(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID

	// The client remembers position 500 from before a restart.
	staleCursor := int64(500)

	// Simulate "another device already pushed the NEW counter past 500" by
	// directly seeding the post-restart window so cursor 500 looks
	// perfectly servable by the numbers alone.
	handler.mu.Lock()
	frames := make([]sequencedFrame, 0, 510)
	for i := int64(1); i <= 510; i++ {
		frames = append(frames, sequencedFrame{
			seq: uint64(i), kind: "token",
			raw: []byte(`{"type":"token","session_id":"` + sid + `","seq":` + itoa64(i) + `}`),
		})
	}
	handler.sequences = map[string]*sessionSeq{
		sid: {high: 510, frames: frames},
	}
	handler.mu.Unlock()

	// Sanity: without a boot_id, this position IS servable by the numbers
	// alone (proving the scenario is realistic, not a vacuous refusal).
	_, _, ok := handler.catchUpFrames(sid, uint64(staleCursor))
	require.True(t, ok, "test setup sanity: the stale position must look servable by seq alone")

	staleBootID := "boot-before-restart"
	handler.bootID = "boot-after-restart" // the (simulated) new process' own id

	wc, drain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-stale-boot"] = wc
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-stale-boot", sid, nil, &staleCursor, &staleBootID, wc)

	frames2 := drain()
	require.NotEmpty(t, frames2)
	var sawSnapshot bool
	for _, raw := range frames2 {
		h := decodeHead(t, raw)
		if h.Type == string(generated.WsFrameTypeSessionSnapshot) {
			sawSnapshot = true
			assert.Equal(t, seqReasonBootMismatch, h.Reason,
				"a boot_id mismatch must be reported with its own reason, distinct from cursor_ahead/retention_exceeded")
		}
	}
	assert.True(t, sawSnapshot, "finding 7: a stale boot_id must force a snapshot even when the numeric position looks servable")
}

// TestSeq_MatchingBootID_DoesNotForceSnapshot proves the fix is scoped
// correctly: a boot_id that DOES match must not force an unnecessary
// snapshot — the ordinary since_seq servability check still governs.
func TestSeq_MatchingBootID_DoesNotForceSnapshot(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID

	handler.emitSessionFrame(&wsConn{sendCh: make(chan []byte, 4)}, string(generated.WsFrameTypeToken), tokenFrame(sid, "a"))
	handler.emitSessionFrame(&wsConn{sendCh: make(chan []byte, 4)}, string(generated.WsFrameTypeToken), tokenFrame(sid, "b"))

	matching := handler.bootID
	cursor := int64(1)
	wc, drain := sendRecvConn()
	handler.mu.Lock()
	handler.sessions["chat-match-boot"] = wc
	handler.mu.Unlock()
	handler.handleAttachSession(context.Background(), "chat-match-boot", sid, nil, &cursor, &matching, wc)

	for _, raw := range drain() {
		assert.NotEqual(t, string(generated.WsFrameTypeSessionSnapshot), decodeHead(t, raw).Type,
			"a matching boot_id must not force a snapshot when the position is otherwise servable")
	}
}

// itoa64 avoids importing strconv just for one test's synthetic JSON.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ─────────────────────────────────────────────────────────────────────────────
// Finding 8: unbounded journal — idle eviction + byte cap.
// ─────────────────────────────────────────────────────────────────────────────

// TestSeq_IdleSessionEviction_DropsOldButKeepsActive proves
// evictIdleSessionsLocked drops a session whose last activity is older than
// seqIdleEvictAfter while leaving a recently-active session untouched.
func TestSeq_IdleSessionEviction_DropsOldButKeepsActive(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	realNow := wsSeqNow
	t.Cleanup(func() { wsSeqNow = realNow })

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wsSeqNow = func() time.Time { return base }

	handler.mu.Lock()
	handler.assignSeqLocked("idle-session")
	wsSeqNow = func() time.Time { return base.Add(20 * time.Minute) }
	handler.assignSeqLocked("active-session")
	wsSeqNow = func() time.Time { return base.Add(31 * time.Minute) } // idle-session now 31m old
	evicted := handler.evictIdleSessionsLocked(seqIdleEvictAfter)
	handler.mu.Unlock()

	assert.Equal(t, 1, evicted)
	handler.mu.Lock()
	_, idleStillThere := handler.sequences["idle-session"]
	_, activeStillThere := handler.sequences["active-session"]
	handler.mu.Unlock()
	assert.False(t, idleStillThere, "a session idle past seqIdleEvictAfter must be dropped")
	assert.True(t, activeStillThere, "a session active within seqIdleEvictAfter must survive the sweep")
}

// TestSeq_ByteCap_TrimsRetainedWindowIndependentOfFrameCount proves a
// session whose individual frames are large is trimmed by BYTES, not just by
// frame count — finding 8's independent memory bound.
func TestSeq_ByteCap_TrimsRetainedWindowIndependentOfFrameCount(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sid = "big-frames"
	big := make([]byte, 64*1024) // 64 KiB — well under seqJournalCap's frame-count trim point
	for i := range big {
		big[i] = 'x'
	}
	// seqJournalByteCap (8 MiB) / 64 KiB ≈ 128 frames before the byte cap
	// bites — far fewer than seqJournalCap (1024), proving this trim is
	// driven by bytes, not the frame-count cap.
	const n = 200
	for i := 0; i < n; i++ {
		handler.nextSessionSeq(sid, "tool_call_result", big)
	}

	handler.mu.Lock()
	s := handler.sequences[sid]
	require.NotNil(t, s)
	totalBytes := s.bytes
	frameCount := len(s.frames)
	handler.mu.Unlock()

	assert.Less(t, frameCount, n, "the byte cap must have trimmed the window well before seqJournalCap frames")
	assert.LessOrEqual(t, totalBytes, seqJournalByteCap, "retained bytes must not exceed seqJournalByteCap")

	// The window is still internally consistent: oldest-to-head still
	// resolves through the normal catch-up path (no corruption from the trim).
	head := handler.sessionHighSeq(sid)
	_, _, ok := handler.catchUpFrames(sid, head-1)
	assert.True(t, ok, "the most recent position must remain servable after a byte-cap trim")
}
