// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// S2 H3: MessageFrame.recipient exists only because the WebSocket frame decode
// (websocket.go::handleMessageFrame) turns the client's @-menu selection into
// an addressing.Pair. Every other recipient test calls
// handleChatMessageToRecipient directly, so a broken mapping would silently
// downgrade an @-request into an ordinary turn on the owning chat's OWN agent.
//
// These tests drive the REAL decoder with the JSON the SPA sends, and use the
// REAL gateway address deps (gatewayAddressDeps), so eligibility is decided by
// the production rule (mainSessionPairEligible: a current member that is a chat
// target), not by a test double that encodes it.
//
// Oracles: contract MessageFrame.recipient (AgentAddress.yaml) — valid only
// together with session_id; a pair that resolves to an eligible main NOW; not
// the owning chat's own pair; a refused recipient admits nothing.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const recipientDecodeWorkspace = "ws-recipient-decode"

// newRecipientDecodeFixture wires the REAL decode path: ann owns the chat, ray
// is a fellow workspace member (an eligible main), and worker1 is a WORKER
// (never a chat target, so never an eligible main) — the F17-5 case.
func newRecipientDecodeFixture(t *testing.T) (*WSHandler, *bus.MessageBus, *wsConn, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	// Isolate OMNIPUS_HOME FIRST: this fixture reads config.OmnipusHomeDir() for
	// the workspace file the REAL gatewayAddressDeps consults, and
	// ensureIsolatedTestOmnipusHome only isolates when the variable is EMPTY.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	msgBus := bus.NewMessageBus()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "m"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "ann", Type: config.AgentTypeCustom},
				{ID: "ray", Type: config.AgentTypeCustom},
				{ID: "worker1", Type: config.AgentTypeWorker},
			},
		},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	home := config.OmnipusHomeDir()
	handler.home = home
	// The real deps: eligibility is decided by mainSessionPairEligible against
	// the workspace file below, not by a fixture map.
	al.SetAddressDeps(gatewayAddressDeps{h: handler})
	seedBindingWorkspace(t, home, recipientDecodeWorkspace, "ann", "ray")
	// ray admits a peer request outright, so only the ADDRESS can refuse.
	ray, ok := al.GetRegistry().GetAgent("ray")
	require.True(t, ok, "fixture: ray must be registered")
	ray.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	wc := makeTestConn()
	t.Cleanup(wc.close)
	return handler, msgBus, wc, home
}

// recipientDecodeSend dispatches one raw client frame through the production
// decoder and returns the frames the server queued on the connection.
func recipientDecodeSend(t *testing.T, reader *wsHandlerReadLoop, frameJSON string) []map[string]any {
	t.Helper()
	require.Equal(t, wsHandlerReadLoopNext,
		reader.dispatchFrame([]byte(frameJSON), wsTypeOnly{Type: string(generated.WsFrameTypeMessage)}),
		"the production MessageFrame decoder must accept the frame")
	return recipientDecodeDrain(t, reader.wc)
}

func recipientDecodeDrain(t *testing.T, wc *wsConn) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		select {
		case data := <-wc.sendCh:
			var f map[string]any
			require.NoError(t, json.Unmarshal(data, &f), "server frame must be JSON: %s", data)
			out = append(out, f)
		case <-time.After(300 * time.Millisecond):
			return out
		}
	}
}

func recipientDecodeTypes(frames []map[string]any) []any {
	out := make([]any, 0, len(frames))
	for _, f := range frames {
		out = append(out, f["type"])
	}
	return out
}

// recipientDecodeMintOwnerChat sends the FIRST message through the decoder and
// returns the session id the server acknowledged, so the whole flow under test
// starts at the wire.
func recipientDecodeMintOwnerChat(t *testing.T, reader *wsHandlerReadLoop) string {
	t.Helper()
	frames := recipientDecodeSend(t, reader,
		`{"type":"message","content":"hello","agent_id":"ann","client_message_id":"decode-mint",`+
			`"metadata":{"workspace_id":"`+recipientDecodeWorkspace+`"}}`)
	require.Contains(t, recipientDecodeTypes(frames), "session_started",
		"the owning chat must be minted from the decoded frame: %v", frames)
	for _, f := range frames {
		if f["type"] == "session_started" {
			id, _ := f["session_id"].(string)
			require.NotEmpty(t, id, "session_started must name the new session")
			return id
		}
	}
	t.Fatal("unreachable")
	return ""
}

// TestRecipient_WebSocketFrameDecodeRoutesToThePeerMain is the S2 H3 test: the
// decoded recipient must reach the PEER's main, and the owning chat's agent
// must get no turn at all.
func TestRecipient_WebSocketFrameDecodeRoutesToThePeerMain(t *testing.T) {
	h, b, wc, _ := newRecipientDecodeFixture(t)
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "chat-owner"}
	sid := recipientDecodeMintOwnerChat(t, reader)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	// Drain the owner's own turn from the first message.
	_, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "the first message must admit the owner's own turn")

	recipientDecodeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@ray please review X",`+
			`"client_message_id":"decode-1","recipient":{"workspace_id":"`+recipientDecodeWorkspace+`","agent_id":"ray"}}`)

	in, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "the DECODED recipient must start the peer's turn")
	wantMain, err := session.MainSessionID(recipientDecodeWorkspace, "ray")
	require.NoError(t, err)
	assert.Equal(t, wantMain, in.SessionID, "the request must land in the recipient's main")
	assert.Equal(t, "ray", in.Metadata["agent_id"], "the recipient agent must own the turn")
	assert.False(t, in.UserInitiated, "a peer request is not the owner's own user turn")
	_, second := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, second,
		"a decoded @-request must NOT also become an ordinary turn on the owning chat's agent")
}

// TestRecipient_WebSocketFrameWithoutSessionIDIsRefused: the contract makes
// session_id mandatory for a recipient — a recipient with no owning chat
// admits nothing, not even the user's message.
func TestRecipient_WebSocketFrameWithoutSessionIDIsRefused(t *testing.T) {
	h, b, wc, _ := newRecipientDecodeFixture(t)
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "chat-owner"}

	frames := recipientDecodeSend(t, reader,
		`{"type":"message","content":"@ray no chat here","agent_id":"ann",`+
			`"recipient":{"workspace_id":"`+recipientDecodeWorkspace+`","agent_id":"ray"}}`)

	require.NotContains(t, recipientDecodeTypes(frames), "session_started",
		"a refused recipient must not mint a session: %v", frames)
	errFrames := recipientDecodeErrorFrames(frames)
	require.Len(t, errFrames, 1, "exactly one visible refusal frame: %v", frames)
	assert.Contains(t, errFrames[0], "session_id",
		"the refusal must name the missing owning chat: %q", errFrames[0])
	_, admitted := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, admitted, "a refused recipient admits nothing")
}

// TestRecipient_WebSocketFrameToAWorkerIsRefused is the F17-5 case: a worker
// agent is never a chat target, so it is never an eligible main — the decoded
// recipient must be refused and the user's message must not be saved.
func TestRecipient_WebSocketFrameToAWorkerIsRefused(t *testing.T) {
	h, b, wc, _ := newRecipientDecodeFixture(t)
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "chat-owner"}
	sid := recipientDecodeMintOwnerChat(t, reader)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	_, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "the first message must admit the owner's own turn")

	// Prove the instrument: the workspace file really does not make worker1 an
	// eligible main, using the same production predicate the gateway consults.
	eligible, err := gatewayAddressDeps{h: h}.PairEligible(recipientDecodeWorkspace, "worker1")
	require.NoError(t, err)
	require.False(t, eligible, "instrument: worker1 must not be an eligible main")

	frames := recipientDecodeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@worker1 do it",`+
			`"client_message_id":"decode-worker","recipient":{"workspace_id":"`+recipientDecodeWorkspace+`","agent_id":"worker1"}}`)

	errFrames := recipientDecodeErrorFrames(frames)
	require.Len(t, errFrames, 1, "a worker recipient must produce exactly one visible refusal: %v", frames)
	assert.Contains(t, errFrames[0], "cannot be reached",
		"the refusal must be the curated unreachable message, got %q", errFrames[0])
	_, admitted := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, admitted, "a refused worker recipient admits nothing")

	// "A refused recipient admits nothing" includes the user's own message.
	store := h.agentLoop.ResolveSessionStore(sid)
	require.NotNil(t, store, "the owning chat must still exist")
	entries, rerr := store.ReadTranscript(sid)
	require.NoError(t, rerr)
	for _, e := range entries {
		assert.NotEqual(t, "@worker1 do it", e.Content,
			"the refused @-message must not be saved in the owning chat")
	}
}

// TestRecipient_WebSocketFrameToTheOwningChatsOwnAgentIsRefused: the contract
// refuses a pair equal to the owning chat's own pair.
func TestRecipient_WebSocketFrameToTheOwningChatsOwnAgentIsRefused(t *testing.T) {
	h, b, wc, _ := newRecipientDecodeFixture(t)
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "chat-owner"}
	sid := recipientDecodeMintOwnerChat(t, reader)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	_, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "the first message must admit the owner's own turn")

	frames := recipientDecodeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@ann hello self",`+
			`"client_message_id":"decode-self","recipient":{"workspace_id":"`+recipientDecodeWorkspace+`","agent_id":"ann"}}`)

	errFrames := recipientDecodeErrorFrames(frames)
	require.Len(t, errFrames, 1, "an own-pair recipient must produce one visible refusal: %v", frames)
	assert.Contains(t, errFrames[0], "already belongs to that agent",
		"the refusal must explain the chat already belongs to that agent: %q", errFrames[0])
	_, admitted := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, admitted, "a refused own-pair recipient admits nothing")
}

func recipientDecodeErrorFrames(frames []map[string]any) []string {
	var out []string
	for _, f := range frames {
		if f["type"] != "error" {
			continue
		}
		msg, _ := f["message"].(string)
		out = append(out, msg)
	}
	return out
}
