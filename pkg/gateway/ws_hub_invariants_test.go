// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// doneObserverConn is a hubConn that, the moment the hub hands it a done
// frame (inside the publish critical section), records whether the turn's
// answer is already in the transcript.
type doneObserverConn struct {
	mu            sync.Mutex
	store         *session.UnifiedStore
	sid           string
	sawDone       bool
	persistedThen bool
}

func (c *doneObserverConn) enqueue(frame []byte) bool {
	var f struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(frame, &f)
	if f.Type != "done" {
		return true
	}
	entries, _ := c.store.ReadTranscript(c.sid)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sawDone = true
	for _, e := range entries {
		if e.Role == "assistant" && e.Content == "persisted before done" {
			c.persistedThen = true
		}
	}
	return true
}

// TestHub_H7_PersistBeforeDone pins BE-DESIGN.md §4.3 (H7): a turn's done is
// published only AFTER its final answer is in the transcript, so "done(T) has
// seq <= W" implies any transcript read taken after the bind already holds
// T's answer — the invariant that lets the projection forget a turn at done.
func TestHub_H7_PersistBeforeDone(t *testing.T) {
	f := newAttachFixture(t, "turn-h7", "msg-h7")
	obs := &doneObserverConn{store: f.store, sid: f.sid}
	hub := f.h.hubs.getOrCreate(f.sid)
	hub.bindLive(obs)
	require.NoError(t, f.streamer.Update(context.Background(), "persisted before done"))
	require.NoError(t, f.streamer.Finalize(context.Background(), "persisted before done"))
	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.True(t, obs.sawDone)
	assert.True(t, obs.persistedThen, "the answer must already be persisted when its done is published")
}

// TestHub_H14_DelegationRouting pins BE-DESIGN.md §7 (H14): a delegated
// child's tool frame is numbered in the ROUTING (parent) session's hub with
// its producing_session_id kept, no hub is created for the child, and a
// child's own streamed text (a shadow stream) is never published at all.
func TestHub_H14_DelegationRouting(t *testing.T) {
	h := makeMinimalHandler()
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID:         session.ToolCallID("child-call"),
			SessionID:          "parent-session",
			ProducingSessionID: "child-session",
			Tool:               "read_file",
		},
	})
	parent := h.hubs.lookup("parent-session")
	require.NotNil(t, parent)
	starts := journalFramesOfType(t, parent, "tool_call_start")
	require.Len(t, starts, 1)
	assert.Equal(t, "child-session", starts[0]["producing_session_id"])
	assert.Nil(t, h.hubs.lookup("child-session"), "the child session never gets frames of its own")

	child := &wsStreamer{sessionID: "parent-session", chatID: "chat-p", h: h}
	child.SetTurnID("child-turn")
	child.SetParentSpawnCallID("spawn-call")
	require.NoError(t, child.Update(context.Background(), "hidden child narration"))
	assert.Empty(t, journalFramesOfType(t, parent, "token"), "a delegated child's own tokens are never published")
}

// TestHub_H15_ProjectionTruncatesAtBudget pins §3.1's projection bound: past
// 2 MiB the projection is marked truncated and a snapshot falls back to the
// current open message only.
func TestHub_H15_ProjectionTruncatesAtBudget(t *testing.T) {
	var p activeTurnProjection
	chunk := strings.Repeat("a", 256<<10)
	p.update(hubFrameMeta{kind: hubKindToolStart, key: "c1"}, []byte(`{"type":"tool_call_start"}`), "s")
	for i := 0; i < 10; i++ {
		p.update(hubFrameMeta{kind: hubKindToken, messageID: "m1", turnID: "t", content: chunk}, nil, "s")
	}
	require.True(t, p.truncated, "the projection must mark itself truncated past its budget")
	snap := p.snapshot()
	require.Len(t, snap, 1, "a truncated projection yields only the current open message")
	assert.Equal(t, "m1", snap[0].messageID)
	p.update(hubFrameMeta{kind: hubKindDone}, nil, "s")
	assert.False(t, p.truncated)
	assert.False(t, p.active())
}

// TestHub_IdleEvictionWaitsForUnfinishedTurn pins §3.2: a hub whose turn is
// still unfinished (text in its projection) is never evicted, even idle and
// with no tab bound; once the turn's done clears the projection it can be.
func TestHub_IdleEvictionWaitsForUnfinishedTurn(t *testing.T) {
	reg := newHubRegistry("boot-1")
	hub := reg.getOrCreate("sess-busy")
	hub.publishMeta(hubFrameMeta{kind: hubKindToken, messageID: "m", content: "x"}, []byte(`{"type":"token"}`))
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()
	assert.Empty(t, reg.evictIdle(time.Now()), "a hub with an unfinished turn must not be evicted")

	hub.publishMeta(hubFrameMeta{kind: hubKindDone}, []byte(`{"type":"done"}`))
	hub.mu.Lock()
	hub.lastActive = time.Now().Add(-time.Hour)
	hub.mu.Unlock()
	assert.Equal(t, []string{"sess-busy"}, reg.evictIdle(time.Now()))
	assert.True(t, hub.isEvicted())

	// A producer still holding the evicted hub re-routes to the live one:
	// nothing is written into a hub nobody can reach.
	seq, _ := hub.publishBytes([]byte(`{"type":"token"}`))
	live := reg.lookup("sess-busy")
	require.NotNil(t, live)
	assert.NotSame(t, hub, live)
	assert.Equal(t, live.snapshotHead(), seq)
}
