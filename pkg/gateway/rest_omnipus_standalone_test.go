// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Navigation FR-038 / "one main per agent per workspace": Omnipus lives in the
// DEFAULT workspace only (omnipus-system-agent FR-003; the retired Admin had the
// same rule), and since FR-005 (landing 1, WU-1) Omnipus has exactly ONE
// conversation — its main chat. Every request to mint another Omnipus chat is
// refused with that main's id (409), wherever it names; other agents' chats
// are unaffected.

func omnipusSessionCount(t *testing.T, env *u1Env) int {
	t.Helper()
	metas, err := env.store(t).ListSessions()
	require.NoError(t, err)
	n := 0
	for _, m := range metas {
		if m.Type != session.SessionTypeMain && len(m.AgentIDs) > 0 && m.AgentIDs[0] == "omnipus" {
			n++
		}
	}
	return n
}

func TestOmnipusStandalone_RESTCreateWithoutWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia", "omnipus")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"omnipus"}`)

	assert.Equal(t, http.StatusConflict, code, body)
	assert.Contains(t, body, "main chat", "FR-005: the refusal points to Omnipus's main")
	assert.Zero(t, omnipusSessionCount(t, env), "no standalone Omnipus session may be minted")
}

func TestOmnipusStandalone_RESTCreateInANonDefaultWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia", "omnipus")
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000002", false, "mia")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"omnipus","workspace_id":"01JADMINSTANDALONEWS000002"}`)

	assert.Equal(t, http.StatusConflict, code, body)
	assert.Zero(t, omnipusSessionCount(t, env))
}

// FR-005: even inside the default workspace a SECOND Omnipus chat is refused;
// control: another agent's workspace-less chat is still created.
func TestOmnipusStandalone_OnlyOtherAgentsMintExtraChats(t *testing.T) {
	env := u1NewEnv(t, false)
	const def = "01JADMINSTANDALONEWS000001"
	env.seedWorkspace(t, def, true, "mia", "omnipus")

	code, body := sf1Do(env, http.MethodPost, "/api/v1/sessions",
		`{"type":"chat","agent_id":"omnipus","workspace_id":"`+def+`"}`)
	assert.Equal(t, http.StatusConflict, code, body)
	assert.Zero(t, omnipusSessionCount(t, env))

	code, body = sf1Do(env, http.MethodPost, "/api/v1/sessions", `{"type":"chat","agent_id":"mia"}`)
	assert.Equal(t, http.StatusCreated, code, "another agent's workspace-less chat is unchanged: %s", body)
	var s gen.Session
	require.NoError(t, json.Unmarshal([]byte(body), &s))
	assert.Equal(t, "mia", s.AgentId)
}

func TestOmnipusStandalone_WebSocketFirstMessageWithoutWorkspaceIsRefused(t *testing.T) {
	env := u1NewEnv(t, false)
	env.seedWorkspace(t, "01JADMINSTANDALONEWS000001", true, "mia", "omnipus")
	h := newWSHandler(bus.NewMessageBus(), env.api.agentLoop, "")
	t.Cleanup(h.Wait)
	wc := makeTestConn()

	h.handleChatMessage(context.Background(), "chat-omnipus", "", "hello omnipus", "omnipus", nil, "", "", wc)

	frames := drainConnFrames(wc)
	assert.Contains(t, frames, `"type":"error"`, frames)
	assert.True(t, strings.Contains(frames, "main chat"), frames)
	assert.Zero(t, omnipusSessionCount(t, env), "no standalone Omnipus session may be minted over the WebSocket either")
}

// Positive control (Gap 8, R2 — a mutant that refuses every Omnipus message
// must die): a message to Omnipus's EXISTING main chat in the default
// workspace is accepted, while a new-chat first message is refused (FR-005).
func TestOmnipusStandalone_WebSocketMessageToTheMainIsAccepted(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JADMINSTANDALONEWS000001"
	env.seedWorkspace(t, ws, true, "mia", "omnipus") // this workspace IS the default
	main, err := env.store(t).GetOrCreateMainSession(ws, "omnipus")
	require.NoError(t, err)
	msgBus := bus.NewMessageBus()
	h := newWSHandler(msgBus, env.api.agentLoop, "")
	t.Cleanup(h.Wait)
	wc := makeTestConn()

	h.handleChatMessage(context.Background(), "chat-omnipus-ok", main.ID, "hello omnipus", "omnipus", nil, "", ws, wc)

	frames := drainConnFrames(wc)
	assert.NotContains(t, frames, `"type":"error"`,
		"a message to Omnipus's main in the default workspace must be accepted, not refused: %s", frames)
	assert.Zero(t, omnipusSessionCount(t, env), "no extra Omnipus chat is minted")
	// F04 / M29: absence of an error is not acceptance. Pin the exact echo,
	// durable user entry and synchronous real bus publication independently.
	var echoes []gen.UserMessageFrame
	for _, line := range strings.Split(strings.TrimSpace(frames), "\n") {
		if line == "" {
			continue
		}
		var head map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &head))
		if head["type"] == "user_message" {
			var echo gen.UserMessageFrame
			require.NoError(t, json.Unmarshal([]byte(line), &echo))
			echoes = append(echoes, echo)
		}
	}
	if assert.Len(t, echoes, 1, "existing-main positive control must echo exactly one accepted input") {
		assert.Equal(t, main.ID, echoes[0].SessionId)
		assert.Equal(t, "hello omnipus", echoes[0].Content)
		require.NotNil(t, echoes[0].AgentId)
		assert.Equal(t, "omnipus", *echoes[0].AgentId)
	}
	entries, err := env.store(t).ReadTranscript(main.ID)
	require.NoError(t, err)
	if assert.Len(t, entries, 1, "the accepted input must be durable, not silently dropped") {
		assert.Equal(t, "user", entries[0].Role)
		assert.Equal(t, "omnipus", entries[0].AgentID)
		assert.Equal(t, "hello omnipus", entries[0].Content)
	}
	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, main.ID, msg.SessionID)
		assert.Equal(t, "omnipus", msg.Metadata["agent_id"])
		assert.Equal(t, "hello omnipus", msg.Content)
	default:
		t.Error("accepted existing-main input did not reach real inbound dispatch")
	}
	l1WU1AssertNoInbound(t, msgBus)
}
