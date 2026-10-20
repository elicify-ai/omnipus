// since_cursor_reconnect_test.go — T1: integration regression tests for
// reconnect catch-up, driven end to end through the embedded Go gateway and
// the mock LLM (no real keys).
//
// #823 catch-up redesign (founder decision Q3): the old timestamp cursor
// (attach_session.since, applySinceCursor) was deleted; a tab now resumes
// from a sequence cursor {since_seq, boot_id}. Every guarantee these tests
// pinned is re-expressed against it — same test names:
//
//	TestSinceCursor_IncrementalReplay
//	    was: entries at/before a timestamp are skipped, later ones replayed,
//	         one done.
//	    now: attach {since_seq: seq of turn 1's last frame} returns exactly
//	         the numbered frames after it (turn 2 only, identical to what a
//	         live tab received), ends with catch_up_complete{incremental},
//	         turn 2's done exactly once, no history replay.
//	TestSinceCursor_CursorAtExactBoundary
//	    was: an entry whose timestamp EQUALS the cursor is skipped.
//	    now: the frame whose seq equals the cursor is skipped, the next one
//	         is sent; a cursor at the head returns no frames at all, only
//	         catch_up_complete.
//	TestSinceCursor_FutureCursorProducesEmptyReplay
//	    was: a cursor beyond all entries replays nothing.
//	    now: a cursor beyond the head (or from another gateway run) can not
//	         be trusted, so it gets a full snapshot rebuild
//	         (session_snapshot{cursor_ahead | boot_mismatch} + history +
//	         catch_up_complete{snapshot}); it never silently returns nothing.
//	TestSinceCursor_DifferentInputsDifferentOutputs
//	    was: two timestamps give different replay counts.
//	    now: two seq cursors give different frame sets, each exactly the
//	         frames after its own cursor.
//	TestSinceCursor_FullReplayWithoutSince (unchanged): no cursor → full
//	    history replay.

package integration

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// seqFrame is one numbered frame a tab received.
type seqFrame struct {
	seq   int64
	typ   string
	frame map[string]any
}

// frameSeqNum returns a frame's seq, or 0 when it carries none.
func frameSeqNum(f map[string]any) int64 {
	if v, ok := f["seq"].(float64); ok {
		return int64(v)
	}
	return 0
}

func isTurnDone(f map[string]any) bool {
	stats, ok := f["stats"].(map[string]any)
	return f["type"] == "done" && ok && stats["tokens"] != nil
}

// readUntil reads frames from conn until stop matches one (inclusive) or
// the timeout passes.
func readUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, stop func(map[string]any) bool) []map[string]any {
	t.Helper()
	var frames []map[string]any
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("reading frames: %v (got %d so far)", err, len(frames))
		}
		var f map[string]any
		if json.Unmarshal(msg, &f) != nil {
			continue
		}
		frames = append(frames, f)
		if stop(f) {
			return frames
		}
	}
}

// liveSession drives two real turns on one session through a live tab and
// returns everything that tab saw: the session id, the gateway boot id, the
// numbered frames, and the seq of the last frame of each turn.
type liveSession struct {
	sessionID string
	bootID    string
	frames    []seqFrame
	turnEnd   []int64 // last seq of turn 1, turn 2
}

func runTwoTurns(t *testing.T, gw *testutil.TestGateway) liveSession {
	t.Helper()
	conn := wsConnect(t, gw)
	var ls liveSession
	record := func(frames []map[string]any) {
		for _, f := range frames {
			if f["type"] == "session_started" {
				ls.sessionID, _ = f["session_id"].(string)
				ls.bootID, _ = f["boot_id"].(string)
				continue
			}
			if n := frameSeqNum(f); n > 0 {
				tp, _ := f["type"].(string)
				ls.frames = append(ls.frames, seqFrame{seq: n, typ: tp, frame: f})
			}
		}
	}
	sendMessage(t, conn, "first question before cursor")
	record(readUntil(t, conn, 20*time.Second, isTurnDone))
	if ls.sessionID == "" || ls.bootID == "" {
		t.Fatalf("session_started must carry session_id and boot_id")
	}
	ls.turnEnd = append(ls.turnEnd, ls.frames[len(ls.frames)-1].seq)
	sendMessage(t, conn, "second question after cursor", ls.sessionID)
	record(readUntil(t, conn, 20*time.Second, isTurnDone))
	ls.turnEnd = append(ls.turnEnd, ls.frames[len(ls.frames)-1].seq)
	return ls
}

// attachWithCursor opens a new tab and attaches with {since_seq, boot_id},
// returning every frame up to and including catch_up_complete.
func attachWithCursor(t *testing.T, gw *testutil.TestGateway, sessionID string, sinceSeq int64, bootID string) []map[string]any {
	t.Helper()
	conn := wsConnect(t, gw)
	// The connection-open session_state (no session yet) comes first.
	readUntil(t, conn, 10*time.Second, func(f map[string]any) bool { return f["type"] == "session_state" })
	frame := fmt.Sprintf(`{"type":"attach_session","session_id":%s,"since_seq":%d,"boot_id":%s}`,
		jsonQuote(sessionID), sinceSeq, jsonQuote(bootID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}
	return readUntil(t, conn, 20*time.Second, func(f map[string]any) bool { return f["type"] == "catch_up_complete" })
}

// seqsOf returns the seq of every numbered frame (catch-up markers excluded).
func seqsOf(frames []map[string]any) []int64 {
	var out []int64
	for _, f := range frames {
		if f["type"] == "catch_up_complete" || f["type"] == "session_snapshot" {
			continue
		}
		if n := frameSeqNum(f); n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// liveSeqsAfter is what a live tab received after cursor.
func (ls liveSession) liveSeqsAfter(cursor int64) []int64 {
	var out []int64
	for _, f := range ls.frames {
		if f.seq > cursor {
			out = append(out, f.seq)
		}
	}
	return out
}

func countType(frames []map[string]any, typ string) int {
	n := 0
	for _, f := range frames {
		if f["type"] == typ {
			n++
		}
	}
	return n
}

// TestSinceCursor_IncrementalReplay: reconnecting with the cursor at the end
// of turn 1 gets exactly turn 2's frames — the same seqs a live tab got —
// then catch_up_complete{incremental}, with turn 2's done exactly once and
// nothing from turn 1.
func TestSinceCursor_IncrementalReplay(t *testing.T) {
	gw := startIntegrationGateway(t)
	ls := runTwoTurns(t, gw)
	cursor := ls.turnEnd[0]

	frames := attachWithCursor(t, gw, ls.sessionID, cursor, ls.bootID)
	logFrameTypes(t, frames)

	if frames[0]["type"] != "session_state" {
		t.Errorf("an incremental catch-up opens with session_state, got %v", frames[0]["type"])
	}
	if n := countType(frames, "session_snapshot") + countType(frames, "replay_message"); n != 0 {
		t.Errorf("a servable cursor must not trigger a history rebuild (%d snapshot/replay frames)", n)
	}
	got, want := seqsOf(frames), ls.liveSeqsAfter(cursor)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("caught-up seqs = %v, want exactly the frames after the cursor %v", got, want)
	}
	for _, f := range frames {
		if f["type"] == "user_message" && f["content"] != "second question after cursor" {
			t.Errorf("a frame from before the cursor leaked into the catch-up: %v", f)
		}
	}
	if countType(frames, "user_message") != 1 {
		t.Errorf("turn 2's user message must be caught up exactly once")
	}
	doneCount := 0
	for _, f := range frames {
		if isTurnDone(f) {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("expected exactly 1 turn done frame, got %d", doneCount)
	}
	last := frames[len(frames)-1]
	if last["type"] != "catch_up_complete" || last["mode"] != "incremental" || frameSeqNum(last) != ls.turnEnd[1] {
		t.Errorf("last frame must be catch_up_complete{incremental, seq=%d}, got %v", ls.turnEnd[1], last)
	}
}

// TestSinceCursor_FullReplayWithoutSince verifies that attach_session WITHOUT a
// since parameter performs a full replay (backward-compatible behavior).
//
// BDD:
//
//	Given a session with 4 transcript entries
//	When the client sends attach_session { session_id } (no since)
//	Then all 4 entries are replayed as replay_message frames
//	And exactly one "done" frame arrives.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T1 (negative case)
func TestSinceCursor_FullReplayWithoutSince(t *testing.T) {
	gw := startIntegrationGateway(t)

	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{
			"id":        "fr-entry-1",
			"role":      "user",
			"content":   "full-replay-turn1",
			"timestamp": base.Format(time.RFC3339Nano),
		},
		{
			"id":        "fr-entry-2",
			"role":      "assistant",
			"content":   "full-replay-reply1",
			"timestamp": base.Add(time.Second).Format(time.RFC3339Nano),
		},
		{
			"id":        "fr-entry-3",
			"role":      "user",
			"content":   "full-replay-turn2",
			"timestamp": base.Add(2 * time.Second).Format(time.RFC3339Nano),
		},
		{
			"id":        "fr-entry-4",
			"role":      "assistant",
			"content":   "full-replay-reply2",
			"timestamp": base.Add(3 * time.Second).Format(time.RFC3339Nano),
		},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	conn := wsConnect(t, gw)
	// No "since" field — triggers full replay.
	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session (no since) send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 8*time.Second)
	if len(frames) == 0 {
		t.Fatal("T1-neg: no frames received after attach_session without since")
	}
	logFrameTypes(t, frames)

	// All 4 entries must appear as replay_message frames.
	replayContents := make([]string, 0)
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			if c, _ := f["content"].(string); c != "" {
				replayContents = append(replayContents, c)
			}
		}
	}
	if len(replayContents) < 4 {
		t.Errorf(
			"T1-neg: expected 4 replay_message frames (full replay), got %d: %v",
			len(replayContents),
			replayContents,
		)
	}

	// Exactly one done frame.
	doneCount := 0
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "done" {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("T1-neg: expected exactly 1 done frame (full replay), got %d", doneCount)
	}
}

// TestSinceCursor_CursorAtExactBoundary: "strictly after" — the frame whose
// seq equals the cursor is not sent again, the next one is; and a cursor at
// the head gets no frames at all, only catch_up_complete.
func TestSinceCursor_CursorAtExactBoundary(t *testing.T) {
	gw := startIntegrationGateway(t)
	ls := runTwoTurns(t, gw)

	// Cursor exactly at turn 2's user_message.
	var boundary int64
	for _, f := range ls.frames {
		if f.typ == "user_message" && f.frame["content"] == "second question after cursor" {
			boundary = f.seq
		}
	}
	if boundary == 0 {
		t.Fatal("turn 2's user_message was never numbered")
	}
	frames := attachWithCursor(t, gw, ls.sessionID, boundary, ls.bootID)
	logFrameTypes(t, frames)
	got := seqsOf(frames)
	if len(got) == 0 || got[0] != boundary+1 {
		t.Errorf("the first caught-up frame must be seq %d (strictly after the cursor), got %v", boundary+1, got)
	}
	if countType(frames, "user_message") != 0 {
		t.Error("the frame AT the cursor must not be sent again")
	}

	// Cursor at the head: nothing to send.
	head := ls.turnEnd[1]
	atHead := attachWithCursor(t, gw, ls.sessionID, head, ls.bootID)
	if s := seqsOf(atHead); len(s) != 0 {
		t.Errorf("a cursor at the head must get no frames, got seqs %v", s)
	}
	last := atHead[len(atHead)-1]
	if last["mode"] != "incremental" || frameSeqNum(last) != head {
		t.Errorf("cursor at head: want catch_up_complete{incremental, seq=%d}, got %v", head, last)
	}
}

// TestSinceCursor_FutureCursorProducesEmptyReplay: a cursor the gateway has
// never issued (ahead of the head), or one from another gateway run, cannot
// be trusted — it is answered with a full snapshot rebuild, never with
// silence.
func TestSinceCursor_FutureCursorProducesEmptyReplay(t *testing.T) {
	gw := startIntegrationGateway(t)
	ls := runTwoTurns(t, gw)
	head := ls.turnEnd[1]

	for _, tc := range []struct {
		name, boot, reason string
		since              int64
	}{
		{"future cursor", ls.bootID, "cursor_ahead", head + 1000},
		{"cursor from another gateway run", "boot-of-a-previous-run", "boot_mismatch", ls.turnEnd[0]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := attachWithCursor(t, gw, ls.sessionID, tc.since, tc.boot)
			logFrameTypes(t, frames)
			if frames[0]["type"] != "session_snapshot" || frames[0]["reason"] != tc.reason {
				t.Fatalf("want session_snapshot{reason:%s} first, got %v", tc.reason, frames[0])
			}
			if n := countType(frames, "replay_message"); n < 4 {
				t.Errorf("the rebuild must replay the whole history (2 questions + 2 answers), got %d replay_message", n)
			}
			last := frames[len(frames)-1]
			if last["mode"] != "snapshot" || frameSeqNum(last) != head {
				t.Errorf("want catch_up_complete{snapshot, seq=%d}, got %v", head, last)
			}
		})
	}
}

// TestSinceCursor_DifferentInputsDifferentOutputs: two different cursors on
// the same session give different results, each exactly the frames after its
// own cursor — the cursor is honoured, not ignored.
func TestSinceCursor_DifferentInputsDifferentOutputs(t *testing.T) {
	gw := startIntegrationGateway(t)
	ls := runTwoTurns(t, gw)
	early := ls.frames[0].seq - 1 // before the first numbered frame
	late := ls.turnEnd[0]

	earlyFrames := seqsOf(attachWithCursor(t, gw, ls.sessionID, early, ls.bootID))
	lateFrames := seqsOf(attachWithCursor(t, gw, ls.sessionID, late, ls.bootID))
	if fmt.Sprint(earlyFrames) != fmt.Sprint(ls.liveSeqsAfter(early)) {
		t.Errorf("early cursor: got %v, want %v", earlyFrames, ls.liveSeqsAfter(early))
	}
	if fmt.Sprint(lateFrames) != fmt.Sprint(ls.liveSeqsAfter(late)) {
		t.Errorf("late cursor: got %v, want %v", lateFrames, ls.liveSeqsAfter(late))
	}
	if len(earlyFrames) <= len(lateFrames) {
		t.Errorf("an earlier cursor must get more frames (%d vs %d)", len(earlyFrames), len(lateFrames))
	}
	t.Logf("early=%d frames, late=%d frames", len(earlyFrames), len(lateFrames))
}
