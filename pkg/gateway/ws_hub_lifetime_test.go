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

// TestHub_OpenSpanNeverPinsTheHub pins merge-review F2/F3: ADR-091 persists
// every subagent_start/subagent_end into the parent's transcript BEFORE
// publishing it (steer_frames.go), and a snapshot's transcript replay
// re-emits the span from that entry, so a span kept in the projection is
// dead weight — and, with the orphan watchdog retired, a child that never
// ends would have pinned its parent's hub forever. An open span must not
// count as unfinished work, and a reconnect after the hub was evicted and
// recreated must get a snapshot (retention_exceeded), never a silent
// incremental catch-up that skips the span.
func TestHub_OpenSpanNeverPinsTheHub(t *testing.T) {
	h := makeMinimalHandler()
	const sid = "parent-sess"
	hub := h.hubs.getOrCreate(sid)
	hub.publish(tokenFrame(t, 0))
	cursor := int64(hub.snapshotHead()) // the client saw everything up to here
	h.hubSyncTap(agent.Event{Kind: agent.EventKindSubTurnSpawn, Payload: agent.SubTurnSpawnPayload{
		SpanID: "span_c9", ParentSpawnCallID: session.ToolCallID("c9"), SessionID: sid, Label: "child-sess",
	}})
	hub.mu.Lock()
	hub.proj.update(hubFrameMeta{kind: hubKindDone}, nil, sid) // the parent's turn finished
	hub.mu.Unlock()

	assert.False(t, hubProjActive(hub), "an open child span must not keep the parent's hub alive")
	ageHub(hub)
	require.Equal(t, []string{sid}, h.hubs.evictIdle(time.Now()))

	// The client missed the span start; the recreated hub cannot serve it
	// incrementally, so it must be told to rebuild from the transcript.
	fresh := h.hubs.getOrCreate(sid)
	require.NotSame(t, hub, fresh)
	conn := newFakeHubConn("reconnect")
	res := fresh.bind(conn, &cursor, hubStrp(h.hubs.bootID), h.hubs.bootID)
	fresh.unbind(conn)
	assert.False(t, res.Servable)
	assert.Equal(t, reasonRetentionExceeded, res.Reason)
}
