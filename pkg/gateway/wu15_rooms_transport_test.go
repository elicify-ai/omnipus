package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// This is a model-edge fixture, not a room/request/reply implementation. The
// delivery id used in a tool response is obtained from the real room snapshot.
type wu15ScriptProvider struct {
	mu        sync.Mutex
	answer    string
	requestID string
	autoReply bool
	calls     [][]providers.Message
	entered   chan struct{}
	release   chan struct{}
	fail      error
	tool      string
	toolArgs  map[string]any
}

func (p *wu15ScriptProvider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	// Prompt caching appends a user-role volatile tail after the conversation.
	// Keep recording the full request below, but inspect the validated
	// conversation so a completed tool call does not trigger another answer.
	conversation, err := gatewayConversationBeforeVolatileTail(msgs)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.calls = append(p.calls, append([]providers.Message(nil), msgs...))
	first := len(p.calls) == 1
	requestID, answer, failure, tool, args, autoReply := p.requestID, p.answer, p.fail, p.tool, p.toolArgs, p.autoReply
	p.mu.Unlock()
	if first && p.entered != nil {
		close(p.entered)
	}
	if first && p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if failure != nil {
		return nil, failure
	}
	if len(conversation) > 0 && conversation[len(conversation)-1].Role == "tool" {
		return &providers.LLMResponse{Content: "Finished.", FinishReason: "stop"}, nil
	}
	if tool != "" {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "wu15-tool", Type: "function", Name: tool, Arguments: args}}, FinishReason: "tool_calls"}, nil
	}
	if autoReply {
		for _, msg := range conversation {
			if msg.Role == "system" {
				continue
			}
			for _, match := range regexp.MustCompile(`request_id="([^"]+)"`).FindAllStringSubmatch(msg.Content, -1) {
				requestID = match[1]
			}
		}
	}
	if requestID != "" {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "wu15-answer", Type: "function", Name: "answer_request", Arguments: map[string]any{"request_id": requestID, "text": answer}}}, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: answer, FinishReason: "stop"}, nil
}

func (*wu15ScriptProvider) GetDefaultModel() string { return "wu15-unpriced" }

func (p *wu15ScriptProvider) inputs() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.calls...)
}

func (h *wu15RoomHarness) provider(id string, p *wu15ScriptProvider) {
	h.t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(id)
	require.True(h.t, ok, "SETUP: registered receiver %s", id)
	// Existing public model edge; set before any turn starts.
	inst.Provider = p
	inst.StoreProviderPool(map[string]providers.LLMProvider{})
}

func (h *wu15RoomHarness) connect() *websocket.Conn {
	h.t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.baseURL, "http")+"/api/v1/chat/ws", nil)
	if resp != nil {
		require.NoError(h.t, resp.Body.Close())
	}
	require.NoError(h.t, err)
	h.conns = append(h.conns, conn)
	require.NoError(h.t, conn.WriteJSON(gen.AuthFrame{Type: "auth", Token: wu15FixtureToken}))
	return conn
}

func wu15ReadFrame(t *testing.T, conn *websocket.Conn, kind string) json.RawMessage {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(30*time.Second)))
	for {
		_, data, err := conn.ReadMessage()
		require.NoError(t, err, "K45: expected %s frame", kind)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &fields))
		var frameType string
		require.NoError(t, json.Unmarshal(fields["type"], &frameType))
		if frameType == "error" && kind != "error" {
			t.Fatalf("BLOCKED: expected %s but gateway rejected the public frame — required by K45: %s", kind, data)
		}
		if frameType == kind {
			return data
		}
	}
}

func (h *wu15RoomHarness) snapshot(roomID string) gen.RoomSnapshotFrame {
	h.t.Helper()
	conn := h.connect()
	return h.attach(conn, roomID, 0)
}

func (h *wu15RoomHarness) attach(conn *websocket.Conn, roomID string, afterSeq int64) gen.RoomSnapshotFrame {
	h.t.Helper()
	require.NoError(h.t, conn.WriteJSON(gen.AttachRoomFrame{Type: "attach_room", RoomId: roomID, AfterSeq: afterSeq}))
	var snap gen.RoomSnapshotFrame
	require.NoError(h.t, json.Unmarshal(wu15ReadFrame(h.t, conn, "room_snapshot"), &snap))
	require.Equal(h.t, roomID, snap.RoomId)
	require.True(h.t, snap.Complete, "K45: healthy catch-up complete")
	require.NotNil(h.t, snap.Entries, "K45: entries never null")
	require.NotNil(h.t, snap.MemberStates, "K45: states never null")
	return snap
}

func wu15FindEntry(t *testing.T, snap gen.RoomSnapshotFrame, clientID string) gen.RoomEntry {
	t.Helper()
	var matches []gen.RoomEntry
	for _, entry := range snap.Entries {
		if entry.ClientMessageId != nil && *entry.ClientMessageId == clientID {
			matches = append(matches, entry)
		}
	}
	require.Len(t, matches, 1, "K26/FR-216: exactly one entry for client id %s", clientID)
	return matches[0]
}

func wu15Delivery(t *testing.T, entry gen.RoomEntry, agentID string) gen.RoomDeliveryState {
	t.Helper()
	var matches []gen.RoomDeliveryState
	for _, delivery := range entry.Deliveries {
		if delivery.AgentId == agentID {
			matches = append(matches, delivery)
		}
	}
	require.Len(t, matches, 1, "§6.11: one per-member delivery identity for %s", agentID)
	require.NotEmpty(t, matches[0].RequestId, "FR-216: stable delivery id before admission")
	return matches[0]
}

func (h *wu15RoomHarness) messages(agentID string) []gen.Message {
	h.t.Helper()
	status, body := h.http(http.MethodGet, "/api/v1/sessions/"+h.main(agentID)+"/messages", nil)
	require.Equal(h.t, http.StatusOK, status, "K33: main history: %s", body)
	var msgs []gen.Message
	require.NoError(h.t, json.Unmarshal(body, &msgs))
	return msgs
}

func (h *wu15RoomHarness) waitState(roomID, requestID, state string) gen.RoomSnapshotFrame {
	h.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var snap gen.RoomSnapshotFrame
	for {
		snap = h.snapshot(roomID)
		for _, delivery := range snap.MemberStates {
			if delivery.RequestId == requestID && delivery.State == state {
				return snap
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("§6.11: request %s want state %s, got %+v", requestID, state, snap.MemberStates)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Q-A: an unrun main has no lifecycle, while Stop skips a completed lifecycle.
// Hold one genuine main turn at the paid-model edge, Stop it while running,
// then observe its landed stopped state before any room admission is tested.
func (h *wu15RoomHarness) stopMain(id string) {
	h.t.Helper()
	instance, ok := h.al.GetRegistry().GetAgent(id)
	require.True(h.t, ok)
	original := instance.Provider
	prime := &wu15ScriptProvider{answer: "WU15 lifecycle warm-up", entered: make(chan struct{}), release: make(chan struct{})}
	instance.Provider = prime
	var released sync.Once
	release := func() { released.Do(func() { close(prime.release) }) }
	defer release()
	h.startLoop()
	conn := h.connect()
	sid := h.main(id)
	require.NoError(h.t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	wu15ReadFrame(h.t, conn, "catch_up_complete")
	h.sendMain(conn, id, "WU15 lifecycle fixture warm-up")
	select {
	case <-prime.entered:
	case <-time.After(30 * time.Second):
		h.t.Fatal("SETUP: genuine main warm-up did not enter its model edge")
	}
	lifecycle := h.al.GetSessionLifecycleStore()
	require.NotNil(h.t, lifecycle)
	record, err := lifecycle.Load(sid)
	require.NoError(h.t, err, "SETUP: warm-up produced the receiver's real lifecycle")
	require.Equal(h.t, session.LifecycleRunning, record.State, "SETUP: Stop must target a running, not already completed, turn")
	scope := "agent:" + id + ":session:" + sid
	require.NotNil(h.t, h.al.GetActiveTurnBySession(scope), "SETUP: the receiver's own main turn is active")
	result, err := h.al.StopSession(context.Background(), agent.StopRequest{SessionID: sid, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "wu15-tester"}, Channel: "webchat"})
	require.NoError(h.t, err, "SETUP: stop main through real Stop")
	require.True(h.t, result.Durable)
	require.NoError(h.t, result.RootErr)
	require.Empty(h.t, result.Report.Unreachable)
	require.Empty(h.t, result.Report.SkippedTerminal, "SETUP: a durable no-op is not a stopped fixture")
	release()
	var loadErr error
	require.Eventually(h.t, func() bool {
		landed, err := lifecycle.Load(sid)
		loadErr = err
		return err == nil && landed.State == session.LifecycleStopped && h.al.GetActiveTurnBySession(scope) == nil
	}, 30*time.Second, 10*time.Millisecond, "SETUP: warm-up must fully land stopped, not merely emit a done frame")
	require.NoError(h.t, loadErr)
	instance.Provider = original
}

func (h *wu15RoomHarness) sendMain(conn *websocket.Conn, id, text string) {
	h.t.Helper()
	sid := h.main(id)
	require.NoError(h.t, conn.WriteJSON(gen.MessageFrame{Type: "message", SessionId: &sid, AgentId: &id,
		Content: text, Metadata: map[string]any{"workspace_id": wu15Workspace}}))
}

func wu15RequireError(t *testing.T, status int, data []byte, wantStatus int, message string) {
	t.Helper()
	require.Equal(t, wantStatus, status, "explicit contract refusal: %s", data)
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(data, &body), "contract ErrorResponse, never HTML/[]")
	require.NotEmpty(t, body.Error, "contract error must explain the refusal")
	if message != "" {
		require.Contains(t, body.Error, message, "spec-defined error wording")
	}
}

var _ providers.LLMProvider = (*wu15ScriptProvider)(nil)

// Restart helpers keep the same temporary home; no production home is accessed.
func (h *wu15RoomHarness) restart() *wu15RoomHarness {
	h.t.Helper()
	h.shutdown()
	return wu15Boot(h.t, h.home, h.cfg)
}

func wu15AssertProviderSaw(t *testing.T, p *wu15ScriptProvider, text string) {
	t.Helper()
	found := false
	for _, call := range p.inputs() {
		for _, message := range call {
			if message.Role != "system" && strings.Contains(message.Content, text) {
				found = true
			}
		}
	}
	require.True(t, found, "FR-200/202: real receiver model sees %q", text)
}

func wu15LoopErrorIsCancellation(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}

var _ session.SessionStore = (*session.UnifiedStore)(nil)
