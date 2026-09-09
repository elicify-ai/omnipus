// handoff_agent_id_test.go — regression test for Bug 1: post-handoff tool_call
// entries in transcript.jsonl must carry the NEW agent's agent_id, not the
// handing-off agent's id.
//
// Bug: when mia hands off to jim, all subsequent tool_call entries in
// transcript.jsonl carry agent_id="mia" even though jim is the active agent.
//
// Fix: the active agent for a new WS turn must be resolved from
// session.ActiveAgentID at turn-creation time, so turnState.agentID == jim
// after a handoff, and appendToolCallTranscript writes agent_id="jim".
//
// Invariant tested:
//   - After the active agent is switched to "jim" on a session, any tool_call
//     entries written to transcript.jsonl by subsequent turns carry
//     agent_id="jim" (not the original creating agent's id).
//
// Traces to: Round-2 bug regressions — Bug 1 (post-handoff agent_id labeling)

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// mockLLMWithSingleToolCall returns a mock LLM server that emits one streaming
// tool call (tool name "switch_agent", args: target jim) on the FIRST
// request, then a plain text reply on all subsequent requests.
//
// switch_agent (ADR-071 D4) is a core builtin so no special tool registration
// is needed. The agent loop will execute it, call SwitchAgent, and the
// session's ActiveAgentID will flip to "jim".
//
// On the second+ request (after handoff, when jim is processing), the mock
// returns a second tool call — this is the "jim tool call" whose agent_id we
// assert in the transcript.
func mockLLMHandoffThenToolCall(tb testing.TB) *httptest.Server {
	tb.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		callCount++
		n := callCount

		if !body.Stream {
			// Non-streaming fallback: plain text.
			writeMockJSON(w, fmt.Sprintf("response-%d", n))
			return
		}

		switch n {
		case 1:
			// First request (mia's turn): return a switch_agent tool call to
			// jim (ADR-071 D4: hand_off/return_to_default merged into
			// switch_agent(target, note)).
			writeMockToolCallStream(w, "switch_agent", `{"target":"jim","note":"regression test handoff"}`)
		case 2:
			// Second request (mia wrapping up after handoff executes): return plain
			// text so the turn finishes.
			writeMockStream(w, "Handoff complete, jim is now active.")
		case 3:
			// Third request (jim's first turn after handoff): return a tool call.
			// We use "system.time" which is a no-side-effect builtin.
			// Even if execution fails (policy / unknown), transcript entry is written.
			writeMockToolCallStream(w, "system.time", `{}`)
		default:
			// Any further request: plain text reply.
			writeMockStream(w, fmt.Sprintf("jim reply %d", n))
		}
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// writeMockToolCallStream emits an OpenAI-compatible SSE stream that contains a
// single tool call with the given name and JSON arguments string.
func writeMockToolCallStream(w http.ResponseWriter, toolName, argsJSON string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	// Chunk 1: tool call header (id + name).
	c1 := map[string]any{
		"id":      "mock-tc-1",
		"object":  "chat.completion.chunk",
		"model":   "mock-model",
		"created": 1700000000,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"id":    "tc-jim-post-handoff",
					"type":  "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": "",
					},
				}},
			},
		}},
	}
	d1, _ := json.Marshal(c1)
	fmt.Fprintf(w, "data: %s\n\n", d1)
	if flusher != nil {
		flusher.Flush()
	}

	// Chunk 2: tool call arguments delta.
	c2 := map[string]any{
		"id":      "mock-tc-1",
		"object":  "chat.completion.chunk",
		"model":   "mock-model",
		"created": 1700000000,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"function": map[string]any{
						"arguments": argsJSON,
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
	d2, _ := json.Marshal(c2)
	fmt.Fprintf(w, "data: %s\n\n", d2)
	if flusher != nil {
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// TestPostHandoff_ToolCallEntriesCarryNewAgentID verifies that after a handoff
// from mia to jim, subsequent tool_call entries in transcript.jsonl carry
// agent_id="jim", not agent_id="mia".
//
// BDD: Given a session started with mia as the active agent
//
//	When mia executes a handoff to jim
//	And jim subsequently executes a tool call
//	Then the tool_call transcript entry has agent_id="jim"
//
// Expected failure mode BEFORE the fix: the tool_call entries written by jim's
// turn will have agent_id="mia" because the WS streamer's agentID was resolved
// at session-creation time (when mia was active) and never updated after
// SwitchAgent changed ActiveAgentID to jim.
//
// Traces to: Round-2 Bug 1 — post-handoff agent_id labeling
func TestPostHandoff_ToolCallEntriesCarryNewAgentID(t *testing.T) {
	// Boot a gateway with a mock LLM that performs a handoff sequence.
	mock := mockLLMHandoffThenToolCall(t)
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	// Connect a WebSocket and send the first message. The gateway mints a
	// fresh session for this WS connection — the test must use the
	// gateway-minted ID (looked up via getMostRecentSessionID after the
	// first turn fires), not a pre-created REST session. The mock LLM
	// returns handoff(jim) on request 1.
	conn := wsConnect(t, gw)

	// Send first message: triggers mia → handoff → jim switch.
	sendMessage(t, conn, "please hand off to jim")
	t.Log("sent first message (expecting handoff to jim)")

	// Wait for the first turn to complete (done frame).
	frames1 := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames1) == 0 {
		t.Fatal("BUG-1: no frames received after first message — turn did not complete")
	}
	logFrameTypes(t, frames1)
	t.Log("first turn complete (handoff should have fired)")

	// Capture the gateway-minted session_id from message 1 so message 2
	// targets the SAME session — without it the gateway mints a fresh
	// session for the second message, the handoff state lives only in
	// session 1, and the test reads the WRONG session below.
	sessionID := extractSessionID(frames1)
	if sessionID == "" {
		t.Fatal("BUG-1: no session_id captured from first turn's session_started frame")
	}
	t.Logf("captured session_id %s from first turn", sessionID)

	// Send a second message: jim is now active and the mock LLM returns a tool call.
	sendMessage(t, conn, "jim please run a tool", sessionID)
	t.Log("sent second message (expecting jim tool call)")

	frames2 := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames2) == 0 {
		t.Fatal("BUG-1: no frames received after second message — jim turn did not complete")
	}
	logFrameTypes(t, frames2)
	t.Log("second turn complete")

	// Give the session store a moment to flush the transcript write.
	time.Sleep(200 * time.Millisecond)

	t.Logf("reading transcript for session %s", sessionID)

	// Read the transcript and find all tool_call entries.
	entries := readTranscriptDirect(t, gw, sessionID)
	if len(entries) == 0 {
		t.Fatal("BUG-1: transcript is empty — no entries were persisted")
	}

	// Find all tool_call typed entries written AFTER the handoff (i.e., by jim).
	// The handoff itself writes a system entry; tool_call entries should belong to jim.
	var toolCallEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeToolCall {
			toolCallEntries = append(toolCallEntries, e)
		}
	}

	if len(toolCallEntries) == 0 {
		// No tool_call entries written. This may happen if the tool call was
		// denied before appendToolCallTranscript was called. Check tool_calls
		// embedded in assistant entries.
		var embeddedToolCalls []session.TranscriptEntry
		for _, e := range entries {
			if len(e.ToolCalls) > 0 {
				embeddedToolCalls = append(embeddedToolCalls, e)
			}
		}
		if len(embeddedToolCalls) == 0 {
			t.Logf(
				"BUG-1: no tool_call entries found in transcript — tool call may not have reached appendToolCallTranscript. Transcript entry types:",
			)
			for _, e := range entries {
				t.Logf("  type=%q role=%q agent_id=%q content_len=%d", e.Type, e.Role, e.AgentID, len(e.Content))
			}
			t.Fatal("BUG-1 BLOCKED: transcript has no tool_call entries — cannot assert agent_id. " +
				"The fix must ensure jim's tool calls are written to transcript.")
			return
		}
		// Check embedded tool calls in assistant entries: the entry's AgentID
		// must be "jim", not "mia".
		for i, e := range embeddedToolCalls {
			if e.AgentID == "" {
				t.Errorf("BUG-1: assistant entry[%d] with tool_calls has empty agent_id (expected \"jim\")", i)
			} else if e.AgentID != "jim" {
				t.Errorf("BUG-1: assistant entry[%d] with tool_calls has agent_id=%q, want \"jim\" "+
					"(post-handoff entries must carry the NEW agent's id)", i, e.AgentID)
			} else {
				t.Logf("OK: assistant entry[%d] tool_calls have agent_id=%q", i, e.AgentID)
			}
		}
		return
	}

	// Assert that all tool_call entries written after the handoff belong to jim.
	// The BUG: before the fix, these will have agent_id="mia".
	for i, e := range toolCallEntries {
		if e.AgentID == "" {
			t.Errorf("BUG-1: tool_call entry[%d] has empty agent_id (expected \"jim\")", i)
		} else if e.AgentID != "jim" {
			t.Errorf("BUG-1: tool_call entry[%d] has agent_id=%q, want \"jim\" — "+
				"post-handoff tool calls must carry the NEW agent's id, not the handing-off agent's id",
				i, e.AgentID)
		} else {
			t.Logf("OK: tool_call entry[%d] has agent_id=%q", i, e.AgentID)
		}
	}
}

// readTranscriptDirect reads transcript.jsonl from the session directory on
// disk, bypassing the API. Used to assert persisted state after turns complete.
func readTranscriptDirect(t *testing.T, gw *testutil.TestGateway, sessionID string) []session.TranscriptEntry {
	t.Helper()

	// Sessions live at parent(workspace)/sessions/<sessionID>/transcript.jsonl
	// (same path derivation as writeTranscriptEntries in replay_ordering_test.go).
	sessionsBase := filepath.Join(filepath.Dir(gw.HomeDir()), "sessions")
	transcriptPath := filepath.Join(sessionsBase, sessionID, "transcript.jsonl")

	// Retry for up to 2 seconds to allow async writes to flush.
	var data []byte
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		data, err = os.ReadFile(transcriptPath)
		if err == nil && len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Logf("readTranscriptDirect: could not read %s within 2s (last err: %v)", transcriptPath, err)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	var entries []session.TranscriptEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry session.TranscriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Logf("readTranscriptDirect: skipping malformed line: %v", err)
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}
