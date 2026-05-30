//go:build !cgo

// replay_ordering_test.go — regression test for Bug-5: Replay frame ordering
// preserved when leaving + returning to chat.
//
// Bug: when the user disconnects mid-turn and reconnects, the replayed frame
// stream was out of causal order (tool_call_result before tool_call_start,
// or frames from later turns mixed with earlier ones).
//
// Fix: the replay path must emit frames in causal order regardless of how many
// live-event frames were buffered during the disconnect.
//
// Invariants tested:
//   - tool_call_start[i] precedes tool_call_result[i] for the same call_id
//   - done is the last frame in the replay stream
//   - earlier turns precede later turns (entry order is preserved)
//
// Traces to: Bug-5 (replay frame ordering preserved when leaving + returning to chat)

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
)

// TestReplayOrdering_ToolCallStartBeforeResult seeds a transcript with a
// tool_call_start + tool_call_result entry, attaches via WebSocket, and
// verifies:
//  1. tool_call_start comes before tool_call_result for the same call_id
//  2. "done" is the last frame
//
// BDD: Given a session with a saved tool_call_start + tool_call_result
//
//	When the user disconnects mid-replay and reconnects
//	Then the replayed frames arrive in causal order: start before result, result before done
//
// Traces to: Bug-5 (replay frame ordering preserved)
func TestReplayOrdering_ToolCallStartBeforeResult(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)

	// Create a session via REST API.
	sessionID := createSession(t, gw)
	t.Logf("created session %s", sessionID)

	// Seed a transcript with one tool_call entry.
	seedToolCallTranscript(t, gw, sessionID)

	// Attach via WebSocket and collect all frames until "done".
	conn := wsConnect(t, gw)

	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("BUG-5: no frames received after attach_session — replay did not trigger")
	}

	logFrameTypes(t, frames)

	// Invariant 1: done is the last frame.
	lastFrame := frames[len(frames)-1]
	if tp, _ := lastFrame["type"].(string); tp != "done" {
		t.Errorf("BUG-5: last frame must be 'done', got %q", tp)
	}

	// Invariant 2: for every tool_call_start, the matching tool_call_result
	// appears AFTER it.
	startIdx := map[string]int{}
	for i, f := range frames {
		if tp, _ := f["type"].(string); tp == "tool_call_start" {
			if callID, _ := f["call_id"].(string); callID != "" {
				startIdx[callID] = i
			}
		}
	}
	for i, f := range frames {
		if tp, _ := f["type"].(string); tp == "tool_call_result" {
			callID, _ := f["call_id"].(string)
			if callID == "" {
				continue
			}
			si, ok := startIdx[callID]
			if !ok {
				t.Errorf(
					"BUG-5: tool_call_result for call_id=%q at frame[%d] has no preceding tool_call_start",
					callID,
					i,
				)
				continue
			}
			if si >= i {
				t.Errorf(
					"BUG-5: tool_call_start for call_id=%q is at index %d but tool_call_result is at index %d (start must precede result)",
					callID,
					si,
					i,
				)
			}
		}
	}
}

// TestReplayOrdering_EarlierTurnBeforeLaterTurn seeds a two-turn transcript and
// verifies turn 1's replay_message appears before turn 2's replay_message.
//
// BDD: Given a session with 2 user turns already recorded
//
//	When the user attaches to that session
//	Then turn 1 replay_message precedes turn 2 replay_message in the stream
//
// Traces to: Bug-5 multi-turn ordering invariant
func TestReplayOrdering_EarlierTurnBeforeLaterTurn(t *testing.T) {
	gw := startIntegrationGateway(t)
	sessionID := createSession(t, gw)

	// Seed two turns into the transcript.
	seedTwoTurnTranscript(t, gw, sessionID)

	conn := wsConnect(t, gw)
	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("BUG-5: no frames received after attach_session in two-turn test")
	}
	logFrameTypes(t, frames)

	// Collect replay_message frames in order and verify their contents match
	// the expected turn order.
	var messages []string
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			if content, ok := f["content"].(string); ok {
				messages = append(messages, content)
			}
		}
	}

	if len(messages) < 2 {
		t.Fatalf("BUG-5: expected at least 2 replay_message frames, got %d", len(messages))
	}

	// Turn 1 content must precede turn 2 content in the replay stream.
	if !strings.Contains(messages[0], "turn1") {
		t.Errorf("BUG-5: first replay_message should contain 'turn1', got %q", messages[0])
	}
	if !strings.Contains(messages[1], "turn2") {
		t.Errorf("BUG-5: second replay_message should contain 'turn2', got %q", messages[1])
	}
	t.Logf("turn ordering invariant satisfied; messages in order: %v", messages)
}

// ─── Disconnect / Reconnect cycle tests ───────────────────────────────────────

// TestReplayOrdering_DisconnectReconnectDisconnectReconnect verifies that
// across 4 consecutive disconnect/reconnect cycles, the replayed frame order
// is preserved in every cycle.
//
// Bug-5 introduced a race between drain and flag-clear on attach_session.
// A second (and subsequent) attach cycle on the same session reopens the same
// replayDivertCh / isReplayingLive path. This test ensures the invariants hold
// across multiple cycles, not just the first attach.
//
// BDD: Given a session with 2 user turns recorded
//
//	When the user attaches, disconnects, re-attaches — 4 cycles total
//	Then each cycle replays the turns in the same order: turn1 before turn2
//
// Traces to: Bug-5 (replay frame ordering) — multi-cycle disconnect/reconnect
// Traces to: review-pr-test-analyzer.md — "Disconnect → reconnect → disconnect → reconnect"
func TestReplayOrdering_DisconnectReconnectDisconnectReconnect(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)
	sessionID := createSession(t, gw)
	seedTwoTurnTranscript(t, gw, sessionID)

	const cycles = 4
	for cycle := 0; cycle < cycles; cycle++ {
		t.Logf("cycle %d/%d: connecting", cycle+1, cycles)

		conn := wsConnect(t, gw)
		attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
		if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
			t.Fatalf("cycle %d: attach_session send: %v", cycle+1, err)
		}

		frames := collectFramesUntilDone(t, conn, 5*time.Second)
		if len(frames) == 0 {
			t.Fatalf("cycle %d: no frames received after attach_session", cycle+1)
		}
		logFrameTypes(t, frames)

		// Collect replay_message frames in order.
		var messages []string
		for _, f := range frames {
			if tp, _ := f["type"].(string); tp == "replay_message" {
				if content, ok := f["content"].(string); ok {
					messages = append(messages, content)
				}
			}
		}

		if len(messages) < 2 {
			t.Fatalf("cycle %d: expected at least 2 replay_message frames, got %d", cycle+1, len(messages))
		}

		// turn1 must precede turn2 in every cycle.
		if !strings.Contains(messages[0], "turn1") {
			t.Errorf("cycle %d: first replay_message should contain 'turn1', got %q", cycle+1, messages[0])
		}
		if !strings.Contains(messages[1], "turn2") {
			t.Errorf("cycle %d: second replay_message should contain 'turn2', got %q", cycle+1, messages[1])
		}

		// Close the connection to simulate disconnect. The next iteration
		// opens a fresh WS connection simulating a new browser tab load.
		if err := conn.Close(); err != nil {
			t.Logf("cycle %d: close conn: %v (acceptable if already closed)", cycle+1, err)
		}
		t.Logf("cycle %d/%d: ordering invariant satisfied (turn1 before turn2)", cycle+1, cycles)
	}
}

// TestReplayOrdering_LateLiveFrameDuringDrain verifies that a live frame
// emitted by sendConnGenFrame DURING the drain phase — after the replay is
// complete but before isReplayingLive is cleared — routes through replayDivertCh
// and appears after the drained history frames.
//
// This is the "production-realistic race" the pr-test-analyzer flagged:
// the pre-loaded frame test proves correctness for the static case, but this
// test adds a live concurrent writer that fires exactly during the drain window.
//
// Because the integration layer runs through the real gateway, we can only
// approximate this: we seed a long-enough transcript (10 entries) that the
// drain takes measurable time, then send a live message during the attach.
// The ordering assertion checks that the replayed history is contiguous before
// any live frames.
//
// BDD: Given a session with a long transcript (10 entries)
//
//	When the user attaches and a live message arrives during replay drain
//	Then the replayed history appears first, then the live frame
//	(the live frame does NOT interleave with the replay stream)
//
// Traces to: Bug-5 (replay frame ordering) — concurrent live frame during drain
// Traces to: review-pr-test-analyzer.md — "Late-arriving live frame during replay drain"
func TestReplayOrdering_LateLiveFrameDuringDrain(t *testing.T) {
	gw := startIntegrationGateway(t)
	sessionID := createSession(t, gw)

	// Seed 10 turns to give the replay drain enough to do.
	entries := make([]map[string]any, 10)
	for i := range entries {
		entries[i] = map[string]any{
			"id":        fmt.Sprintf("entry-late-%d", i),
			"role":      "user",
			"content":   fmt.Sprintf("late-drain-turn%d message", i),
			"timestamp": fmt.Sprintf("2026-01-01T00:00:%02dZ", i),
		}
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	conn := wsConnect(t, gw)
	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}

	// Send a new user message as a "live" event during the replay.
	// This message will go through the agent loop while replay is draining.
	// We send it immediately after attach so it races with the drain.
	sendMessage(t, conn, "live frame during drain test")

	// Collect all frames (replay + live response).
	frames := collectAllFrames(t, conn, 8*time.Second)
	if len(frames) == 0 {
		t.Fatal("BUG-5: no frames received — replay and live message both silent")
	}
	logFrameTypes(t, frames)

	// Verify invariant: all replay_message frames must appear as a contiguous
	// group before the first "token" or "done" frame from the live message.
	// A live token appearing between two replay_message frames indicates
	// interleaving of live and replay streams.
	foundLive := false
	replayAfterLive := false
	for _, f := range frames {
		tp, _ := f["type"].(string)
		switch tp {
		case "token", "content", "text":
			// First live token — mark it.
			foundLive = true
		case "replay_message":
			if foundLive {
				replayAfterLive = true
			}
		}
	}

	// Replay messages appearing after live tokens is the interleaving bug.
	// It's possible on some frame sequences that the live LLM response is
	// so fast it arrives before replay finishes — in that case this test
	// is inconclusive (not a failure). Log and proceed.
	if replayAfterLive {
		t.Errorf(
			"BUG-5: a replay_message frame appeared after a live token frame — " +
				"replay and live streams interleaved during drain phase. " +
				"Frame sequence: see logFrameTypes above.",
		)
	} else {
		t.Log("ordering invariant satisfied: no replay frames interleaved with live tokens")
	}
}

// TestReplay_ToolCallPairsEmitted seeds a session with 3 tool_call entries and
// verifies that replay emits exactly 3 tool_call_start + 3 tool_call_result
// frames (Bug 2 regression).
//
// The pre-fix behavior emitted only replay_message per entry and zero
// tool_call_start / tool_call_result pairs — the streamReplay path did not
// iterate entry.ToolCalls when emitting replayed frames.
//
// BDD: Given a session with 3 tool_call entries each carrying one tool call
//
//	When the user attaches to that session via WebSocket
//	Then the replay stream contains exactly 3 tool_call_start frames
//	And exactly 3 tool_call_result frames
//	And for each call_id, tool_call_start precedes tool_call_result
//
// Traces to: Bug-2 (replay omits tool_call_start / tool_call_result pairs)
func TestReplay_ToolCallPairsEmitted(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)
	gw := startIntegrationGateway(t)
	sessionID := createSession(t, gw)

	// Seed 3 distinct tool_call entries — each with a unique call_id.
	entries := make([]map[string]any, 3)
	for i := 0; i < 3; i++ {
		entries[i] = map[string]any{
			"id":        fmt.Sprintf("entry-pairs-%d", i),
			"role":      "assistant",
			"agent_id":  "jim",
			"timestamp": fmt.Sprintf("2026-01-01T00:00:%02dZ", i),
			"tool_calls": []map[string]any{{
				"id":          fmt.Sprintf("call-replay-bug2-%d", i),
				"tool":        "system.time",
				"status":      "success",
				"duration_ms": 10,
				"parameters":  map[string]any{},
				"result":      map[string]any{"time": "2026-01-01T00:00:00Z"},
			}},
		}
	}
	writeTranscriptEntries(t, gw, sessionID, entries)

	conn := wsConnect(t, gw)
	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn, 8*time.Second)
	if len(frames) == 0 {
		t.Fatal("BUG-2: no frames received after attach_session — replay did not trigger")
	}
	logFrameTypes(t, frames)

	// Count tool_call_start and tool_call_result frames, recording frame index.
	startIdxByCallID := map[string]int{}  // call_id → frame index
	resultIdxByCallID := map[string]int{} // call_id → frame index

	for i, f := range frames {
		tp, _ := f["type"].(string)
		callID, _ := f["call_id"].(string)
		if callID == "" {
			continue
		}
		switch tp {
		case "tool_call_start":
			startIdxByCallID[callID] = i
		case "tool_call_result":
			resultIdxByCallID[callID] = i
		}
	}

	// Invariant: exactly 3 pairs emitted — one per seeded entry.
	if len(startIdxByCallID) != 3 {
		t.Errorf("BUG-2: want 3 tool_call_start frames, got %d", len(startIdxByCallID))
	}
	if len(resultIdxByCallID) != 3 {
		t.Errorf("BUG-2: want 3 tool_call_result frames, got %d", len(resultIdxByCallID))
	}

	// Invariant: for each call_id, start index must precede result index.
	for callID, si := range startIdxByCallID {
		ri, ok := resultIdxByCallID[callID]
		if !ok {
			t.Errorf("BUG-2: tool_call_start for call_id=%q has no matching tool_call_result", callID)
			continue
		}
		if si >= ri {
			t.Errorf(
				"BUG-2: for call_id=%q, start is at index %d but result is at index %d (start must precede result)",
				callID,
				si,
				ri,
			)
		}
	}
	for callID := range resultIdxByCallID {
		if _, ok := startIdxByCallID[callID]; !ok {
			t.Errorf("BUG-2: tool_call_result for call_id=%q has no matching tool_call_start", callID)
		}
	}
}

// TestReplay_AssistantTextSurvivesDisconnect verifies that when the WS client
// disconnects mid-turn (before the LLM responds), the server completes the
// turn and writes the assistant text to the transcript. When the client
// reconnects and issues attach_session, the replay stream must include the
// assistant text as a replay_message frame.
//
// The pre-fix behavior: wsStreamer.Finalize() was not called if the WS send
// failed, so the assistant text was never appended to the transcript.
//
// BDD: Given an open session with one pending user message
//
//	When the WS client disconnects before the LLM responds
//	And the server finishes the turn and writes the assistant reply
//	When the client reconnects and sends attach_session
//	Then the replay stream includes the assistant text as replay_message
//
// Traces to: Bug-3 (assistant text lost when WS disconnects before LLM reply)
func TestReplay_AssistantTextSurvivesDisconnect(t *testing.T) {
	skipOnMacOSAPFSCleanupRace(t)

	// Use a slow LLM (200 ms delay) so we can reliably disconnect before the
	// server produces its first token.
	const llmDelay = 200 * time.Millisecond
	mock := slowMockLLMServer(t, "assistant text that must survive disconnect", llmDelay)
	gw := testutil.StartTestGateway(t, testutil.WithAPIBase(mock.URL), testutil.WithBearerAuth())

	// Open a WS session and send a message. The LLM won't respond for 200 ms.
	conn := wsConnect(t, gw)
	sendMessage(t, conn, "trigger llm reply")

	// Disconnect immediately (well before the 200 ms LLM delay).
	time.Sleep(50 * time.Millisecond)
	_ = conn.Close()

	// Wait for the server to finish the turn and write the transcript.
	// 200 ms LLM delay + generous 1 s for processing.
	time.Sleep(llmDelay + 1*time.Second)

	// Extract the session ID from the WS connect to reattach. We don't have
	// a stored session ID from the initial WS connect (it was ephemeral). Use
	// the REST API to list sessions and pick the most recently created one.
	sessionID := getMostRecentSessionID(t, gw)
	if sessionID == "" {
		t.Fatal("BUG-3: could not find a session to reattach — server may not have created one")
	}
	t.Logf("reattaching to session %s", sessionID)

	conn2 := wsConnect(t, gw)
	attachFrame := fmt.Sprintf(`{"type":"attach_session","session_id":%s}`, jsonQuote(sessionID))
	if err := conn2.WriteMessage(websocket.TextMessage, []byte(attachFrame)); err != nil {
		t.Fatalf("attach_session send: %v", err)
	}

	frames := collectFramesUntilDone(t, conn2, 10*time.Second)
	logFrameTypes(t, frames)

	// Invariant: at least one replay_message frame must carry the assistant text.
	const expectedText = "assistant text that must survive disconnect"
	found := false
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "replay_message" {
			if content, _ := f["content"].(string); strings.Contains(content, expectedText) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf(
			"BUG-3: assistant text %q not found in replay stream after reconnect — text was lost when WS disconnected before LLM responded",
			expectedText,
		)
	}
}

// getMostRecentSessionID queries GET /api/v1/sessions and returns the most
// recently created session ID, or "" if none exist.
func getMostRecentSessionID(t *testing.T, gw *testutil.TestGateway) string {
	t.Helper()
	req, err := gw.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	if err != nil {
		t.Fatalf("getMostRecentSessionID: build request: %v", err)
	}
	resp, err := gw.Do(req)
	if err != nil {
		t.Fatalf("getMostRecentSessionID: GET /api/v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getMostRecentSessionID: unexpected status %d", resp.StatusCode)
	}
	// /api/v1/sessions returns a JSON array of session metadata, not an
	// object wrapping it.
	var sessions []struct {
		ID        string `json:"id"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatalf("getMostRecentSessionID: decode: %v", err)
	}
	if len(sessions) == 0 {
		return ""
	}
	// Wrap in the previous struct-shape for the rest of the helper.
	body := struct {
		Sessions []struct {
			ID        string `json:"id"`
			UpdatedAt string `json:"updated_at"`
		}
	}{Sessions: sessions}
	// Return the first (most recent) session — the server returns newest first.
	return body.Sessions[0].ID
}

// ─── Transcript seeding helpers ───────────────────────────────────────────────

// seedToolCallTranscript writes a one-turn transcript with a tool_call entry
// to the session directory. The gateway reads this on attach_session.
func seedToolCallTranscript(t *testing.T, gw *testutil.TestGateway, sessionID string) {
	t.Helper()
	entries := []map[string]any{
		{
			"id":        "entry-user-1",
			"role":      "user",
			"content":   "trigger tool call",
			"timestamp": "2026-01-01T00:00:00Z",
		},
		{
			"id":      "entry-assistant-1",
			"role":    "assistant",
			"content": "running tool",
			"tool_calls": []map[string]any{
				{
					"id":          "tc-replay-1",
					"tool":        "exec",
					"status":      "success",
					"duration_ms": 42,
					"parameters":  map[string]any{"cmd": "echo hello"},
					"result":      map[string]any{"stdout": "hello\n"},
				},
			},
			"timestamp": "2026-01-01T00:00:01Z",
		},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)
}

// seedTwoTurnTranscript writes two distinct user messages to the session.
// Each entry generates exactly one replay_message frame, so the replay
// stream has messages[0] from turn1 and messages[1] from turn2.
// Using only user entries avoids emitting assistant tool-call frames that
// would interleave with the ordering assertions in the test.
func seedTwoTurnTranscript(t *testing.T, gw *testutil.TestGateway, sessionID string) {
	t.Helper()
	entries := []map[string]any{
		{"id": "turn1-user", "role": "user", "content": "turn1 message", "timestamp": "2026-01-01T00:00:00Z"},
		{"id": "turn2-user", "role": "user", "content": "turn2 message", "timestamp": "2026-01-01T00:00:01Z"},
	}
	writeTranscriptEntries(t, gw, sessionID, entries)
}

// writeTranscriptEntries writes JSONL transcript entries to the session directory.
// Path: <SessionsBaseDir>/sessions/<sessionID>/transcript.jsonl
//
// The agent loop derives its session store root from
// filepath.Dir(cfg.WorkspacePath()), NOT from cfg.WorkspacePath() itself.
// In the test harness, cfg.Agents.Defaults.Workspace = gw.HomeDir(), so:
//   - cfg.WorkspacePath() = gw.HomeDir()
//   - sharedSessionDir   = filepath.Dir(gw.HomeDir()) + "/sessions"
//
// This means sessions live one level ABOVE gw.HomeDir(), in the parent of
// the test temp dir that was used as Workspace. Using filepath.Dir here
// mirrors the actual loop.go:340 logic.
func writeTranscriptEntries(t *testing.T, gw *testutil.TestGateway, sessionID string, entries []map[string]any) {
	t.Helper()

	// Sessions are stored at parent(workspace)/sessions/, matching loop.go:340:
	//   homePath := filepath.Dir(cfg.WorkspacePath())
	//   sharedDir := filepath.Join(homePath, "sessions")
	sessionsBase := filepath.Join(filepath.Dir(gw.HomeDir()), "sessions")
	sessionDir := filepath.Join(sessionsBase, sessionID)

	// Wait up to 1 second for the gateway to create the session directory.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(sessionDir); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("writeTranscriptEntries: session dir %s did not appear within 1s", sessionDir)
		}
		time.Sleep(50 * time.Millisecond)
	}

	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	var sb strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("writeTranscriptEntries: marshal entry: %v", err)
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(transcriptPath, []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("writeTranscriptEntries: write %s: %v", transcriptPath, err)
	}
	t.Logf("seeded %d entries to %s", len(entries), transcriptPath)
}

// ─── REST / WS helpers ────────────────────────────────────────────────────────

// createSession creates a new chat session via POST /api/v1/sessions.
func createSession(t *testing.T, gw *testutil.TestGateway) string {
	t.Helper()
	req, err := gw.NewRequest(
		http.MethodPost,
		"/api/v1/sessions",
		strings.NewReader(`{"type":"chat","channel":"webchat"}`),
	)
	if err != nil {
		t.Fatalf("createSession: build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := gw.Do(req)
	if err != nil {
		t.Fatalf("createSession: POST /api/v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("createSession: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("createSession: decode response: %v", err)
	}
	if body.ID == "" {
		t.Fatal("createSession: response did not include session id")
	}
	return body.ID
}

// wsConnectForAttach was an exact duplicate of wsConnect in helpers_test.go;
// removed. All call sites in this file now use wsConnect directly.

// collectFramesUntilDone reads WS frames until a "done" frame is received
// or timeout elapses. Returns all collected frames including done.
func collectFramesUntilDone(t *testing.T, conn *websocket.Conn, timeout time.Duration) []map[string]any {
	t.Helper()
	var frames []map[string]any
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f map[string]any
		if jsonErr := json.Unmarshal(msg, &f); jsonErr == nil {
			frames = append(frames, f)
			if tp, _ := f["type"].(string); tp == "done" {
				break
			}
		}
	}
	return frames
}

func logFrameTypes(t *testing.T, frames []map[string]any) {
	t.Helper()
	types := make([]string, len(frames))
	for i, f := range frames {
		if ft, ok := f["type"].(string); ok {
			types[i] = ft
		} else {
			types[i] = "?"
		}
	}
	t.Logf("frame sequence (%d): %v", len(types), types)
}
