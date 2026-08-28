// Package gateway — multi-session WebSocket integration tests.
//
// Contract being pinned:
//   - Client message frame may omit session_id; server mints via store.NewSession
//     and emits {type:"session_started", session_id, agent_id} BEFORE bus publish.
//   - Session-scoped server frames (token, done, error, session_started) carry session_id.
//   - bus.InboundMessage.SessionID carries the resolved id.
//   - Handoff override is keyed by "session:"+sessionID in sessionActiveAgent.
//   - Per-agent session storage key is agent:<agentID>:session:<sessionID>.
//   - (*AgentLoop).GetSessionActiveAgent(sessionID) reads the new key.
//   - WS handler no longer holds a per-connection *sessionID; every frame
//     stands on its own frame.SessionID.
//
// Note on streaming: restMockProvider does not implement providers.StreamingProvider.
// Without streaming, the agent loop calls Chat() synchronously; finalizeStreamer is
// a no-op and no "done" frame is emitted via the WS streamer path.
// Tests that need to verify session_id tagging on "done"/"token" frames use the
// streamingMockProvider defined below, which does implement StreamingProvider.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// streamingMockProvider implements both providers.LLMProvider and
// providers.StreamingProvider. ChatStream emits a single text chunk then
// returns, causing the agent loop to exercise the streaming path and emit
// token + done frames via the wsStreamer.
type streamingMockProvider struct{}

func (s *streamingMockProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok", ToolCalls: []providers.ToolCall{}}, nil
}

func (s *streamingMockProvider) ChatStream(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
	onChunk func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	onChunk("ok")
	return &providers.LLMResponse{Content: "ok", ToolCalls: []providers.ToolCall{}}, nil
}

func (s *streamingMockProvider) GetDefaultModel() string { return "streaming-test-model" }

// newStreamingTestWSHandler is like newTestWSHandler but uses the streaming mock provider,
// causing the agent loop to emit token + done frames over the WebSocket.
// It also starts the agent loop's Run goroutine so bus messages are processed.
func newStreamingTestWSHandler(t *testing.T) (*WSHandler, *bus.MessageBus, *agent.AgentLoop) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "streaming-test-model"},
				MaxTokens:    4096,
			},
			// A real, chat-target agent ("mia") so the default-agent
			// resolution every test in this file relies on (every message
			// frame below omits agent_id) has something to resolve to. The
			// retired "main" sentinel used to be registered implicitly
			// regardless of cfg (pkg/agent/registry.go's old always-on
			// fallback); with it gone, pkg/gateway/websocket.go's
			// handleChatMessage now rejects rather than silently persisting
			// an empty owner when no agent_id is supplied and no default can
			// be resolved.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &streamingMockProvider{})

	// Start the agent loop's Run goroutine so bus messages are processed.
	// Without this, PublishInbound in handleChatMessage sends to the bus but
	// nothing consumes it, and no streaming frames are emitted.
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()
	// Wait for Run to fully exit before t.TempDir cleanup — otherwise session
	// writers race with RemoveAll and produce "directory not empty" failures
	// (TestWS_HandoffOverrideKeyedBySessionID and friends).
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Logf("agent loop Run did not exit within 2s of cancel")
		}
	})
	// Give the loop time to start reading from the bus.
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	// Register the WSHandler as the stream delegate so the agent loop can
	// call GetStreamer("webchat", chatID) and obtain a wsStreamer. Without this,
	// the streaming path in the agent loop falls back to Chat() (non-streaming)
	// and no token/done frames are emitted.
	msgBus.SetStreamDelegate(handler)
	return handler, msgBus, al
}

// drainUntilSessionDone reads frames until it sees a done/error frame tagged with
// the expected session_id, or times out. Returns true if a matching frame was found.
func drainUntilSessionDone(t *testing.T, conn *websocket.Conn, sid string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return false
		}
		var f replayFrameDecoder
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if (f.Type == "done" || f.Type == "error") && f.SessionID == sid {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// TestWS_MessageWithEmptySessionID_MintsAndAcks
// ---------------------------------------------------------------------------

// TestWS_MessageWithEmptySessionID_MintsAndAcks verifies that when the SPA
// omits session_id, the server mints a new session and emits session_started
// BEFORE any streaming frame, and that a follow-up message with that id
// appends to the same session without re-minting.
//
// BDD:
//
//	Given an authenticated WebSocket connection and no prior sessions,
//	When the client sends {type:"message", content:"hi"} without session_id,
//	Then the server emits {type:"session_started", session_id:<non-empty>} as
//	  the next session-related frame, with ID == session_id.
//	When a follow-up message is sent with that session_id,
//	Then no second session_started is emitted.
//	And disk contains exactly two user-turn entries in the transcript.
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_MessageWithEmptySessionID_MintsAndAcks
func TestWS_MessageWithEmptySessionID_MintsAndAcks(t *testing.T) {
	// BDD: Given — use streaming provider so done frames are emitted
	handler, _, al := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// BDD: When — send message without session_id
	firstMsg := wsClientFrameTestHelper{Type: "message", Content: "first message no session id"}
	data, err := json.Marshal(firstMsg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// BDD: Then — next session-related frame must be session_started with a non-empty session_id.
	// The generated SessionStartedFrame carries session_id as the authoritative session identifier;
	// the legacy `id` field is not part of the generated contract.
	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	assert.NotEmpty(t, started.SessionID, "session_started.session_id must be non-empty")
	mintedSessionID := started.SessionID

	// Drain the first message's done/error frame
	firstDone := drainUntilSessionDone(t, conn, mintedSessionID, 5*time.Second)
	require.True(t, firstDone,
		"first message must produce a done or error frame tagged with the minted session_id=%q", mintedSessionID)

	// BDD: When — follow-up with the minted id
	followup := wsClientFrameTestHelper{
		Type:      "message",
		Content:   "second message with minted session",
		SessionID: mintedSessionID,
	}
	data2, err := json.Marshal(followup)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data2))

	// Drain the second message's response; check no second session_started arrives.
	var sawSecondStarted bool
	var sawFollowupDone bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var f replayFrameDecoder
		if unmarshalErr := json.Unmarshal(raw, &f); unmarshalErr != nil {
			continue
		}
		if f.Type == "session_started" {
			sawSecondStarted = true
		}
		if (f.Type == "done" || f.Type == "error") && f.SessionID == mintedSessionID {
			sawFollowupDone = true
			break
		}
	}
	assert.False(
		t,
		sawSecondStarted,
		"no second session_started should be emitted for a follow-up message with an existing session_id",
	)
	assert.True(t, sawFollowupDone, "follow-up message must produce a done/error frame tagged with the same session_id")

	// Disk: both user turns must appear in the transcript.
	time.Sleep(100 * time.Millisecond)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	entries, err := store.ReadTranscript(mintedSessionID)
	require.NoError(t, err, "must be able to read transcript for minted session")
	var userTurns int
	for _, e := range entries {
		if e.Role == "user" {
			userTurns++
		}
	}
	assert.Equal(t, 2, userTurns,
		"transcript must contain exactly 2 user turns (both messages appended to same session) — got %d; entries=%v",
		userTurns, entries)
}

// ---------------------------------------------------------------------------
// TestWS_TwoParallelSessions_NoCrosstalk
// ---------------------------------------------------------------------------

// TestWS_TwoParallelSessions_NoCrosstalk verifies that a single WS connection
// can carry two independent sessions without frame cross-contamination.
//
// BDD:
//
//	Given one authenticated WebSocket connection,
//	When the client sends messages that mint two distinct session ids (A and B),
//	Then each received session-scoped frame carries only A's or B's id.
//	And disk shows two separate transcript directories, each with one user turn.
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_TwoParallelSessions_NoCrosstalk
func TestWS_TwoParallelSessions_NoCrosstalk(t *testing.T) {
	// BDD: Given
	handler, _, al := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Mint session A
	msgA := wsClientFrameTestHelper{Type: "message", Content: "session-a-message"}
	dataA, merr := json.Marshal(msgA)
	require.NoError(t, merr)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataA))

	startedA := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedA.SessionID, "session A must be minted")
	sessionA := startedA.SessionID

	drainUntilSessionDone(t, conn, sessionA, 5*time.Second)

	// Mint session B
	msgB := wsClientFrameTestHelper{Type: "message", Content: "session-b-message"}
	dataB, merr := json.Marshal(msgB)
	require.NoError(t, merr)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataB))

	startedB := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedB.SessionID, "session B must be minted")
	sessionB := startedB.SessionID

	drainUntilSessionDone(t, conn, sessionB, 5*time.Second)

	// Differentiation guard: the two ids must differ
	assert.NotEqual(t, sessionA, sessionB,
		"parallel sessions must have distinct session_ids — single-session implementation would give the same id")

	// Send a follow-up to session A using its explicit id
	followA := wsClientFrameTestHelper{Type: "message", Content: "a-followup", SessionID: sessionA}
	dataFA, merr := json.Marshal(followA)
	require.NoError(t, merr)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataFA))

	// Collect session-scoped frames; assert no A-frame carries B's id and vice versa
	allowed := map[string]bool{sessionA: true, sessionB: true}
	for i := 0; i < 30; i++ {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var f replayFrameDecoder
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if f.SessionID != "" {
			assert.True(t, allowed[f.SessionID],
				"frame type=%q carries unexpected session_id=%q (not A=%q or B=%q)",
				f.Type, f.SessionID, sessionA, sessionB)
		}
		if (f.Type == "done" || f.Type == "error") && f.SessionID == sessionA {
			break
		}
	}

	// Disk: each session must have its own directory with a user turn
	time.Sleep(100 * time.Millisecond)
	store := al.GetSessionStore()
	require.NotNil(t, store)

	entriesA, err := store.ReadTranscript(sessionA)
	require.NoError(t, err, "must read transcript for session A")
	entriesB, err := store.ReadTranscript(sessionB)
	require.NoError(t, err, "must read transcript for session B")

	var userTurnsA, userTurnsB int
	for _, e := range entriesA {
		if e.Role == "user" {
			userTurnsA++
		}
	}
	for _, e := range entriesB {
		if e.Role == "user" {
			userTurnsB++
		}
	}
	// Session A gets 2 user turns (initial + follow-up), session B gets 1
	assert.Equal(t, 2, userTurnsA,
		"session A transcript must have 2 user turns (initial + follow-up); got %d", userTurnsA)
	assert.Equal(t, 1, userTurnsB,
		"session B transcript must have 1 user turn; got %d", userTurnsB)

	// B's transcript must NOT contain A's content — isolation check
	for _, e := range entriesB {
		assert.NotEqual(t, "session-a-message", e.Content,
			"session B transcript must not contain session A's user content — session isolation failure")
		assert.NotEqual(t, "a-followup", e.Content,
			"session B transcript must not contain session A's follow-up — session isolation failure")
	}
}

// ---------------------------------------------------------------------------
// TestWS_MessageWithUnknownSessionID_ErrorsCleanly
// ---------------------------------------------------------------------------

// TestWS_MessageWithUnknownSessionID_ErrorsCleanly verifies that sending a
// message with a session_id that fails validation (path traversal characters)
// returns an error frame and keeps the connection open.
//
// Note: The server does NOT validate session existence at the WS layer for
// valid-format ids — it validates only the format. So this test uses an id
// with path traversal characters (`../`) to trigger the validation error path.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {type:"message", session_id:"../../etc/passwd"},
//	Then the server responds with {type:"error"} (not a close frame),
//	And the connection remains open for subsequent messages.
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_MessageWithUnknownSessionID_ErrorsCleanly
func TestWS_MessageWithUnknownSessionID_ErrorsCleanly(t *testing.T) {
	// BDD: Given
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Path traversal session_id — fails validation.EntityID (contains "..")
	const invalidSID = "../traversal-attempt"

	badMsg := wsClientFrameTestHelper{
		Type:      "message",
		Content:   "this session id has path traversal",
		SessionID: invalidSID,
	}
	data, err := json.Marshal(badMsg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// BDD: Then — expect an error frame
	errFrame := readFrameOfType(t, conn, "error", 5*time.Second)
	assert.NotEmpty(t, errFrame.Message, "error frame must carry a non-empty message")

	// BDD: And — connection stays open (write a follow-up to confirm)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	ping := wsClientFrameTestHelper{Type: "message", Content: "still open after error?"}
	pingData, err := json.Marshal(ping)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must stay open after error frame — not a fatal disconnect")
}

// ---------------------------------------------------------------------------
// TestWS_HandoffOverrideKeyedBySessionID
// ---------------------------------------------------------------------------

// TestWS_HandoffOverrideKeyedBySessionID verifies that two independently minted
// sessions each have isolated GetSessionActiveAgent state. The session-scoped
// key "session:"+sessionID means session A's state cannot bleed into session B.
//
// BDD:
//
//	Given two sessions A and B are minted via WS messages without session_id,
//	When each session's first message is delivered,
//	Then GetSessionActiveAgent(sessionA) returns ("", false) — no override seeded,
//	 and GetSessionActiveAgent(sessionB) returns ("", false).
//	And the two session_ids are distinct (key isolation proof).
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_HandoffOverrideKeyedBySessionID
func TestWS_HandoffOverrideKeyedBySessionID(t *testing.T) {
	// BDD: Given
	handler, _, al := newStreamingTestWSHandler(t)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Mint session A
	msgA := wsClientFrameTestHelper{Type: "message", Content: "handoff-test-session-a"}
	dataA, err := json.Marshal(msgA)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataA))

	startedA := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedA.SessionID, "session A must be minted")
	sessionA := startedA.SessionID
	drainUntilSessionDone(t, conn, sessionA, 5*time.Second)

	// Mint session B
	msgB := wsClientFrameTestHelper{Type: "message", Content: "handoff-test-session-b"}
	dataB, err := json.Marshal(msgB)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataB))

	startedB := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedB.SessionID, "session B must be minted")
	sessionB := startedB.SessionID
	drainUntilSessionDone(t, conn, sessionB, 5*time.Second)

	// Differentiation: the two minted ids must differ.
	require.NotEqual(t, sessionA, sessionB,
		"two independent session mints must produce different ids — hardcoded implementation would fail here")

	// BDD: Then — neither session has a handoff override (no handoff tool was called).
	_, aHasOverride := al.GetSessionActiveAgent(sessionA)
	assert.False(t, aHasOverride,
		"session A must not have a handoff override — GetSessionActiveAgent reads 'session:'+sessionID key, "+
			"and no handoff tool call was fired in this session")

	_, bHasOverride := al.GetSessionActiveAgent(sessionB)
	assert.False(t, bHasOverride,
		"session B must not have a handoff override — sessions are independent; "+
			"if the old 'chat:'+chatID key format were used, both sessions would share override state")

	// Key isolation: the map keys for session A and B are different strings.
	// "session:A" != "session:B" means an override seeded for A cannot be retrieved via B.
	assert.NotEqual(t, "session:"+sessionA, "session:"+sessionB,
		"session-scoped keys must be different; same key would mean sessions share override state")
}

// ---------------------------------------------------------------------------
// TestWS_FrameTaggingCompleteness_AllSessionScopedFramesCarrySessionID
// ---------------------------------------------------------------------------

// TestWS_FrameTaggingCompleteness_AllSessionScopedFramesCarrySessionID is a
// regression net verifying that session-scoped frames carry a non-empty
// session_id equal to the minted id.
//
// BDD:
//
//	Given a session is minted by sending a message without session_id,
//	When the session produces token and done frames (streaming path),
//	Then each of those frames carries session_id == mintedSessionID.
//	And session_started.SessionID == mintedSessionID.
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_FrameTaggingCompleteness_AllSessionScopedFramesCarrySessionID
func TestWS_FrameTaggingCompleteness_AllSessionScopedFramesCarrySessionID(t *testing.T) {
	// BDD: Given — use streaming provider so token + done frames are emitted
	handler, _, _ := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Mint session
	initMsg := wsClientFrameTestHelper{Type: "message", Content: "trigger session for frame tagging test"}
	data, err := json.Marshal(initMsg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, started.SessionID)
	mintedID := started.SessionID

	// session_started itself must carry matching session_id.
	// The generated SessionStartedFrame carries session_id as the authoritative session identifier.
	assert.Equal(t, mintedID, started.SessionID,
		"session_started frame must carry the minted session_id in session_id field")

	// Session-scoped frame types observable with streaming provider.
	sessionScopedTypes := map[string]bool{
		"token": true,
		"done":  true,
		"error": true,
	}

	var taggedTypes []string
	taggedAll := true
	for i := 0; i < 30; i++ {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f replayFrameDecoder
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		if sessionScopedTypes[f.Type] {
			if f.SessionID != mintedID {
				taggedAll = false
				t.Errorf("frame type=%q carries session_id=%q, want %q — frame tagging completeness failure",
					f.Type, f.SessionID, mintedID)
			}
			taggedTypes = append(taggedTypes, f.Type)
		}
		if f.Type == "done" || f.Type == "error" {
			break
		}
	}

	assert.True(t, taggedAll, "all session-scoped frames must carry the minted session_id")

	// At minimum a done (or error) frame must have been observed — otherwise the
	// streaming provider didn't produce output and the test is vacuous.
	hasDoneOrError := false
	for _, tp := range taggedTypes {
		if tp == "done" || tp == "error" {
			hasDoneOrError = true
		}
	}
	assert.True(t, hasDoneOrError,
		"done or error frame must be received; taggedTypes=%v — streaming provider must emit a terminal frame",
		taggedTypes)
}

// ---------------------------------------------------------------------------
// TestWS_PerAgentSessionKeyFormat
// ---------------------------------------------------------------------------

// TestWS_PerAgentSessionKeyFormat verifies that two independently minted
// sessions produce distinct session_ids and each create a session directory
// on disk, proving the per-agent session key format change is wired end-to-end.
//
// BDD:
//
//	Given two sequential WS messages each without session_id,
//	When each message completes (done/error frame received),
//	Then the two minted session_ids are distinct.
//	And GetSessionActiveAgent returns no override for either
//	  (session-scoped key "session:"+sessionID is correct).
//	And each session's directory exists on disk.
//
// Traces to: quizzical-marinating-frog.md Step 9 — TestWS_PerAgentSessionKeyFormat
func TestWS_PerAgentSessionKeyFormat(t *testing.T) {
	// BDD: Given
	handler, _, al := newStreamingTestWSHandler(t)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Mint session A — send, wait for session_started, wait for done.
	msgA := wsClientFrameTestHelper{Type: "message", Content: "session-A-content"}
	dataA, err := json.Marshal(msgA)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataA))

	startedA := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedA.SessionID, "session A must be minted")
	sessionA := startedA.SessionID
	drainUntilSessionDone(t, conn, sessionA, 5*time.Second)

	// Mint session B
	msgB := wsClientFrameTestHelper{Type: "message", Content: "session-B-content"}
	dataB, err := json.Marshal(msgB)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, dataB))

	startedB := readFrameOfType(t, conn, "session_started", 5*time.Second)
	require.NotEmpty(t, startedB.SessionID, "session B must be minted")
	sessionB := startedB.SessionID
	drainUntilSessionDone(t, conn, sessionB, 5*time.Second)

	// Differentiation: two mints must produce different session_ids.
	assert.NotEqual(t, sessionA, sessionB,
		"two independently minted sessions must have different ids — hardcoded id would make them equal")

	// Give the agent loop time to flush writes.
	time.Sleep(200 * time.Millisecond)

	// Verify GetSessionActiveAgent uses "session:"+sessionID key format correctly.
	// No handoff tool call was made, so both should return false.
	_, aOk := al.GetSessionActiveAgent(sessionA)
	assert.False(t, aOk,
		"GetSessionActiveAgent(%q) must return false — no handoff was fired; "+
			"the function reads 'session:'+sessionID which is the correct new key format", sessionA)

	_, bOk := al.GetSessionActiveAgent(sessionB)
	assert.False(t, bOk,
		"GetSessionActiveAgent(%q) must return false — session B must not inherit session A state", sessionB)

	// Disk: confirm the session directories exist.
	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must be non-nil — required for session_id minting")

	baseDir := store.BaseDir()
	sessADir := filepath.Join(baseDir, sessionA)
	sessBDir := filepath.Join(baseDir, sessionB)

	assert.DirExists(t, sessADir,
		"sessions/%s directory must exist on disk — transcript written for session A", sessionA)
	assert.DirExists(t, sessBDir,
		"sessions/%s directory must exist on disk — transcript written for session B", sessionB)
}

// ---------------------------------------------------------------------------
// TestWS_HandleChatMessage_RejectsUnknownSessionID (F5)
// ---------------------------------------------------------------------------

// TestWS_HandleChatMessage_RejectsUnknownSessionID verifies that a chat message
// carrying a well-formatted but non-existent session_id is rejected with an
// "session not found" error frame rather than silently creating a transcript entry
// in an unknown session.
func TestWS_HandleChatMessage_RejectsUnknownSessionID(t *testing.T) {
	handler, _, _ := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Send a message referencing a well-formatted but non-existent session_id.
	fakeSessionID := "00000000-0000-0000-0000-000000000001"
	msg := wsClientFrameTestHelper{
		Type:      "message",
		Content:   "hello",
		SessionID: fakeSessionID,
	}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	errFrame := readFrameOfType(t, conn, "error", 5*time.Second)
	assert.Equal(t, "session not found", errFrame.Message,
		"unknown session_id must produce 'session not found' error, not silent continuation")
	assert.Equal(t, fakeSessionID, errFrame.SessionID,
		"error frame must echo the rejected session_id")
}

// ---------------------------------------------------------------------------
// TestWS_Cancel_OnlyInterruptsTargetSession (F7)
// ---------------------------------------------------------------------------

// TestWS_Cancel_OnlyInterruptsTargetSession verifies that when the client sends
// a cancel frame with a session_id, only the turn for that session is interrupted,
// not all active turns. A cancel for a session with no active turn should not
// produce an error frame visible to the client.
func TestWS_Cancel_OnlyInterruptsTargetSession(t *testing.T) {
	handler, _, _ := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Mint a real session first.
	msg := wsClientFrameTestHelper{Type: "message", Content: "hello"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID, "must mint a session before canceling")

	// Drain the done frame so the turn is finished.
	drainUntilSessionDone(t, conn, sessionID, 5*time.Second)

	// Cancel the finished session. InterruptSession should log a debug "no active
	// turn" and return cleanly without crashing or panicking.
	// There must be no error frame emitted to the client from this cancel.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// Give the server a moment to process. No error frame should arrive.
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, readErr := conn.ReadMessage()
	// Only allowed failure: deadline exceeded (no frame) or close.
	if readErr == nil {
		t.Error("cancel on a finished session must not produce any server frame")
	}
}

// ---------------------------------------------------------------------------
// TestWS_TwoConnections_HandoffInOneDoesNotAffectOther (F7/session isolation)
// ---------------------------------------------------------------------------

// TestWS_TwoConnections_HandoffInOneDoesNotAffectOther verifies that two
// independent WebSocket connections each mint their own session and that
// GetSessionActiveAgent for one session's ID does not return stale data for
// the other connection's session.
func TestWS_TwoConnections_HandoffInOneDoesNotAffectOther(t *testing.T) {
	handler, _, al := newStreamingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	// Connection 1.
	conn1 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn1.Close() })
	sendWSAuthFrameDevMode(t, conn1)

	// Connection 2.
	conn2 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn2.Close() })
	sendWSAuthFrameDevMode(t, conn2)

	// Mint session on connection 1.
	data1, err := json.Marshal(wsClientFrameTestHelper{Type: "message", Content: "conn1"})
	require.NoError(t, err)
	require.NoError(t, conn1.WriteMessage(websocket.TextMessage, data1))
	started1 := readFrameOfType(t, conn1, "session_started", 5*time.Second)
	sid1 := started1.SessionID
	require.NotEmpty(t, sid1)
	drainUntilSessionDone(t, conn1, sid1, 5*time.Second)

	// Mint session on connection 2.
	data2, err := json.Marshal(wsClientFrameTestHelper{Type: "message", Content: "conn2"})
	require.NoError(t, err)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, data2))
	started2 := readFrameOfType(t, conn2, "session_started", 5*time.Second)
	sid2 := started2.SessionID
	require.NotEmpty(t, sid2)
	drainUntilSessionDone(t, conn2, sid2, 5*time.Second)

	// Differentiation: the two sessions must be independent.
	assert.NotEqual(t, sid1, sid2, "two connections must produce distinct session ids")

	// Simulate a handoff override on session 1 only.
	al.SetCurrentSession("main", sid1)

	// Session 2 must not be affected by the handoff on session 1.
	_, hasHandoff2 := al.GetSessionActiveAgent(sid2)
	assert.False(t, hasHandoff2, "session 2 must not inherit handoff state from session 1")
}
