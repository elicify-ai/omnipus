// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// hubProjActive reads a hub's "unfinished work" gate under its lock.
func hubProjActive(hub *sessionHub) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.proj.active()
}

// ageHub makes hub look idle for an hour so the next sweep may evict it.
func ageHub(hub *sessionHub) {
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()
}

// emitChildToolCall publishes one complete tool call (start + result) the
// way a steered child's own turn does: the payload carries the CHILD's own
// session id (ADR-091 D7) and the event meta carries the child's turn id.
func emitChildToolCall(h *WSHandler, sessionID, turnID, callID string) {
	meta := agent.EventMeta{TurnID: turnID}
	h.hubSyncTap(agent.Event{Kind: agent.EventKindToolExecStart, Meta: meta, Payload: agent.ToolExecStartPayload{
		ToolCallID: session.ToolCallID(callID), SessionID: sessionID, Tool: "read_file",
	}})
	h.hubSyncTap(agent.Event{Kind: agent.EventKindToolExecEnd, Meta: meta, Payload: agent.ToolExecEndPayload{
		ToolCallID: session.ToolCallID(callID), SessionID: sessionID, Tool: "read_file", Result: "ok",
	}})
}

// TestHub_TurnEndWithoutWebStreamer_ForgetsTheTurn pins merge-review F1: a
// turn with no web streamer (an ADR-091 steered child — the launcher gives
// it no channel — or a Telegram/system task session) never publishes a
// `done`, so before this fix its tool items stayed in its hub's projection
// forever, the hub reported unfinished work and idle eviction never
// dropped it. TurnEnd now forgets that turn's items (no frame is emitted),
// even though the payload's SessionID is the ROUTING (root) session, not
// the child's own.
func TestHub_TurnEndWithoutWebStreamer_ForgetsTheTurn(t *testing.T) {
	h := makeMinimalHandler()
	emitChildToolCall(h, "child-sess", "child-turn", "c1")
	hub := h.hubs.lookup("child-sess")
	require.NotNil(t, hub)
	require.True(t, hubProjActive(hub), "precondition: the child's tool call is in its projection")
	headBefore := hub.snapshotHead()

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Meta: agent.EventMeta{TurnID: "child-turn"},
		Payload: agent.TurnEndPayload{
			Status: agent.TurnEndStatusCompleted, ChatID: "child-sess", SessionID: "root-sess",
		},
	})

	assert.False(t, hubProjActive(hub), "the ended turn's items must be forgotten")
	assert.Equal(t, headBefore, hub.snapshotHead(), "TurnEnd publishes no frame")
	ageHub(hub)
	assert.Equal(t, []string{"child-sess"}, h.hubs.evictIdle(time.Now()), "the idle child hub is evictable")
}

// TestHub_TurnEndWithLiveWebStreamer_LeavesTheTurnToDone is the negative
// half of F1: a web turn whose streamer holds the session's live-stream
// claim is closed by its own `done` (published after TurnEnd, once the
// answer is persisted), so TurnEnd must not strip its projection early.
func TestHub_TurnEndWithLiveWebStreamer_LeavesTheTurnToDone(t *testing.T) {
	h := makeMinimalHandler()
	require.True(t, claimStreamOwnership(&h.streamOwners, "web-sess", "web-turn"))
	emitChildToolCall(h, "web-sess", "web-turn", "w1")
	hub := h.hubs.lookup("web-sess")
	require.NotNil(t, hub)

	h.hubSyncTap(agent.Event{
		Kind:    agent.EventKindTurnEnd,
		Meta:    agent.EventMeta{TurnID: "web-turn"},
		Payload: agent.TurnEndPayload{Status: agent.TurnEndStatusCompleted, SessionID: "web-sess", IsRoot: true},
	})
	assert.True(t, hubProjActive(hub), "a streamed web turn keeps its items until its own done")
}
