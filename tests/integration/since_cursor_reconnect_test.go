//go:build !cgo

// since_cursor_reconnect_test.go — T1: Integration regression test for
// the since-cursor reconnect feature (Phase 2D, spa-streaming-refactor.md).
//
// Verifies end-to-end that:
//   - A client can attach to an existing session and receive only frames
//     newer than the captured cursor (incremental replay).
//   - A client that attaches without a since parameter receives the full
//     replay (backward-compatible behaviour).
//   - The done frame arrives exactly once in both cases.
//
// Test must run against the embedded Go gateway + mock LLM (no real keys).
// Traces to: spa-streaming-refactor.md Phase 2D, T1.
//
// NOTE: This test uses the helpers_test.go + testmain_test.go scaffolding that
// registers gateway.RunContext via testutil.RegisterGatewayRunner.

package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSinceCursor_IncrementalReplay verifies that attach_session with a
// since timestamp skips frames at or before the cursor.
//
// BDD:
//
//	Given a session that has received LLM turn frames (session_started, tokens, done)
//	When the client reconnects and sends attach_session { session_id, since: <last ts> }
//	Then no replayed frame has a timestamp <= the captured cursor
//	And exactly one "done" frame arrives.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T1
func TestSinceCursor_IncrementalReplay(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	// ── Step 1: Seed a two-turn transcript with known timestamps ──────────────
	// We seed directly rather than driving the live agent loop so we control
	// timestamps precisely. Use writeTranscriptEntries from replay_ordering_test.go.
	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{
			"id":        "sc-entry-1",
			"role":      "user",
			"content":   "first message before cursor",
			"timestamp": base.Format(time.RFC3339Nano),
		},
		{
			"id":        "sc-entry-2",
			"role":      "assistant",
			"content":   "first reply before cursor",
			"timestamp": base.Add(time.Second).Format(time.RFC3339Nano),
		},
		{
			"id":        "sc-entry-3",
			"role":      "user",
			"content":   "second message after cursor",
			"timestamp": base.Add(2 * time.Second).Format(time.RFC3339Nano),
		},
		{
			"id":        "sc-entry-4",
			"role":      "assistant",
			"content":   "second reply after cursor",
			"timestamp": base.Add(3 * time.Second).Format(time.RFC3339Nano),
		},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	// cursor is the timestamp of the 2nd entry — entries 1 and 2 should be
	// skipped; entries 3 and 4 should be replayed.
	cursor := base.Add(time.Second).Format(time.RFC3339Nano)
	t.Logf("cursor: %s", cursor)

	// ── Step 2: Attach with since cursor ─────────────────────────────────────
	conn := wsConnectForAttach(t, gw)
	attachFrame := fmt.Sprintf(
		`{"type":"attach_session","session_id":%s,"since":%s}`,
		jsonQuote(sessionID), jsonQuote(cursor),
	)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session (with since) send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 8*time.Second)
	if len(frames) == 0 {
		t.Fatal("T1: no frames received after attach_session with since cursor")
	}
	logFrameTypes(t, frames)

	// ── Step 3: Assert no frame has content from the skipped entries ──────────
	// The since cursor filters entries whose Timestamp <= cursor. Entry-1 and
	// entry-2 should NOT appear in the replay stream.
	for _, f := range frames {
		tp, _ := f["type"].(string)
		if tp != "replay_message" {
			continue
		}
		content, _ := f["content"].(string)
		if strings.Contains(content, "before cursor") {
			t.Errorf("T1: replay_message with 'before cursor' content appeared after since filter; frame: %v", f)
		}
	}

	// ── Step 4: Assert entries after the cursor appear ────────────────────────
	foundAfterCursor := false
	for _, f := range frames {
		tp, _ := f["type"].(string)
		if tp != "replay_message" {
			continue
		}
		content, _ := f["content"].(string)
		if strings.Contains(content, "after cursor") {
			foundAfterCursor = true
			break
		}
	}
	if !foundAfterCursor {
		t.Error("T1: no replay_message with 'after cursor' content found — entries after cursor were not replayed")
	}

	// ── Step 5: Assert exactly one "done" frame arrives ───────────────────────
	doneCount := 0
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "done" {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("T1: expected exactly 1 done frame, got %d", doneCount)
	}

	// ── Step 6: "done" must be the last frame ─────────────────────────────────
	if len(frames) > 0 {
		last := frames[len(frames)-1]
		if tp, _ := last["type"].(string); tp != "done" {
			t.Errorf("T1: last frame must be 'done', got %q", tp)
		}
	}
}

// TestSinceCursor_FullReplayWithoutSince verifies that attach_session WITHOUT a
// since parameter performs a full replay (backward-compatible behaviour).
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
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{"id": "fr-entry-1", "role": "user", "content": "full-replay-turn1", "timestamp": base.Format(time.RFC3339Nano)},
		{"id": "fr-entry-2", "role": "assistant", "content": "full-replay-reply1", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano)},
		{"id": "fr-entry-3", "role": "user", "content": "full-replay-turn2", "timestamp": base.Add(2 * time.Second).Format(time.RFC3339Nano)},
		{"id": "fr-entry-4", "role": "assistant", "content": "full-replay-reply2", "timestamp": base.Add(3 * time.Second).Format(time.RFC3339Nano)},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	conn := wsConnectForAttach(t, gw)
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
		t.Errorf("T1-neg: expected 4 replay_message frames (full replay), got %d: %v", len(replayContents), replayContents)
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

// TestSinceCursor_CursorAtExactBoundary verifies the "strictly after" semantic:
// an entry whose timestamp equals the cursor is skipped (not replayed).
//
// BDD:
//
//	Given a session with entries at timestamps T, T+1s, T+2s
//	When client attaches with since = T+1s (the exact timestamp of entry 2)
//	Then only the entry at T+2s is replayed (entry at T+1s is excluded)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T1 (boundary semantic)
func TestSinceCursor_CursorAtExactBoundary(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{"id": "bnd-entry-1", "role": "user", "content": "boundary-turn1", "timestamp": base.Format(time.RFC3339Nano)},
		{"id": "bnd-entry-2", "role": "user", "content": "boundary-turn2-AT-cursor", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano)},
		{"id": "bnd-entry-3", "role": "user", "content": "boundary-turn3-AFTER-cursor", "timestamp": base.Add(2 * time.Second).Format(time.RFC3339Nano)},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	// Cursor is the exact timestamp of entry-2.
	cursor := base.Add(time.Second).Format(time.RFC3339Nano)

	conn := wsConnectForAttach(t, gw)
	attachFrame := fmt.Sprintf(
		`{"type":"attach_session","session_id":%s,"since":%s}`,
		jsonQuote(sessionID), jsonQuote(cursor),
	)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session (boundary) send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 8*time.Second)
	logFrameTypes(t, frames)

	// Entry 1 and entry 2 (the exact boundary) must NOT appear.
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp != "replay_message" {
			continue
		}
		content, _ := f["content"].(string)
		if strings.Contains(content, "boundary-turn1") || strings.Contains(content, "boundary-turn2-AT-cursor") {
			t.Errorf("T1-boundary: entry at or before cursor leaked into replay stream; content=%q", content)
		}
	}

	// Entry 3 (after cursor) must appear.
	found := false
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			if c, _ := f["content"].(string); strings.Contains(c, "boundary-turn3-AFTER-cursor") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("T1-boundary: entry after cursor was not replayed")
	}

	// Exactly one done frame.
	doneCount := 0
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "done" {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("T1-boundary: expected exactly 1 done frame, got %d", doneCount)
	}
}

// TestSinceCursor_FutureCursorProducesEmptyReplay verifies that a cursor set to
// the future (beyond all stored entries) produces no content frames — only done.
//
// BDD:
//
//	Given a session with entries in the past
//	When client attaches with since = far future timestamp
//	Then no replay_message frames arrive (nothing to replay)
//	And exactly one "done" frame arrives.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T1 (edge case: empty window)
func TestSinceCursor_FutureCursorProducesEmptyReplay(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{"id": "fut-entry-1", "role": "user", "content": "old message 1", "timestamp": base.Format(time.RFC3339Nano)},
		{"id": "fut-entry-2", "role": "user", "content": "old message 2", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano)},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	// Cursor far in the future — nothing after this timestamp exists.
	futureCursor := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)

	conn := wsConnectForAttach(t, gw)
	attachFrame := fmt.Sprintf(
		`{"type":"attach_session","session_id":%s,"since":%s}`,
		jsonQuote(sessionID), jsonQuote(futureCursor),
	)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session (future cursor) send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 8*time.Second)
	logFrameTypes(t, frames)

	// No replay_message frames expected.
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			t.Errorf("T1-future: replay_message appeared despite future cursor; frame: %v", f)
		}
	}

	// Exactly one done frame.
	doneCount := 0
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "done" {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Errorf("T1-future: expected exactly 1 done frame, got %d", doneCount)
	}

	// Differentiation test: the same session without since must have content.
	conn2 := wsConnectForAttach(t, gw)
	attachFrame2 := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn2.WriteMessage(websocket.TextMessage, []byte(attachFrame2)); err != nil {
		t.Fatalf("attach_session (no since) send: %v", err)
	}
	frames2 := collectFramesUntilDone(t, conn2, 8*time.Second)
	var msgCount int
	for _, f := range frames2 {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			msgCount++
		}
	}
	if msgCount == 0 {
		t.Error("T1-future: differentiation: attach without since must replay stored entries — got zero replay_message frames")
	}
}

// TestSinceCursor_DifferentInputsDifferentOutputs is the differentiation test:
// two different since values on the same session produce different replay counts.
// This proves the filter is not hardcoded.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T1 (differentiation)
func TestSinceCursor_DifferentInputsDifferentOutputs(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	base := time.Date(2026, 4, 1, 7, 0, 0, 0, time.UTC)
	entries := []map[string]any{
		{"id": "diff-entry-1", "role": "user", "content": "diff-msg-1", "timestamp": base.Format(time.RFC3339Nano)},
		{"id": "diff-entry-2", "role": "user", "content": "diff-msg-2", "timestamp": base.Add(time.Second).Format(time.RFC3339Nano)},
		{"id": "diff-entry-3", "role": "user", "content": "diff-msg-3", "timestamp": base.Add(2 * time.Second).Format(time.RFC3339Nano)},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	countReplayMessages := func(since string) int {
		conn := wsConnectForAttach(t, gw)
		var attachFrame string
		if since != "" {
			attachFrame = fmt.Sprintf(
				`{"type":"attach_session","session_id":%s,"since":%s}`,
				jsonQuote(sessionID), jsonQuote(since),
			)
		} else {
			attachFrame = fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
			t.Fatalf("attach_session send: %v", err)
		}
		frames := collectFramesUntilDone(t, conn, 8*time.Second)
		count := 0
		for _, f := range frames {
			if tp, _ := f["type"].(string); tp == "replay_message" {
				count++
			}
		}
		return count
	}

	// No cursor → 3 messages.
	fullCount := countReplayMessages("")
	// Cursor after entry-2 → only 1 message (entry-3).
	sinceEntry2 := base.Add(2 * time.Second).Add(-time.Millisecond).Format(time.RFC3339Nano)
	partialCount := countReplayMessages(sinceEntry2)

	// The two inputs produce different outputs — proves the filter is not hardcoded.
	if fullCount == partialCount {
		t.Errorf("T1-diff: since-cursor filter produced same count for both inputs (%d); filter may be hardcoded", fullCount)
	}
	t.Logf("T1-diff: fullCount=%d partialCount=%d — differentiation confirmed", fullCount, partialCount)
}

// ── json helper (local alias so this file compiles standalone) ────────────────

// jsonQuote is already defined in helpers_test.go in the same package, but we
// use the package-local one available in this integration package.

// ── collectFramesUntilDone is in replay_ordering_test.go ─────────────────────

// NOTE: writeTranscriptEntries and createSession are defined in
// replay_ordering_test.go in this same package, so they are reused here directly.

// ── jsonDecodeFrame is a lightweight JSON decode used in assertions ────────────

func jsonDecodeFrame(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("jsonDecodeFrame: unmarshal: %v", err)
	}
	return f
}
