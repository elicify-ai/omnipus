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

// TestHub_H15_ProjectionOverBudget_KeepsTheCurrentAnswerWhole pins §3.1's
// projection bound as corrected after the #823 review (finding 4): once the
// 2 MiB budget is exceeded, the projection keeps recording the message still
// being written and evicts OLDER items instead. The first version stopped
// appending text for every message at the cap while snapshot() kept
// returning the stale text — a rebuild late in a long, tool-heavy turn showed
// an answer missing everything written after the cap was hit.
func TestHub_H15_ProjectionOverBudget_KeepsTheCurrentAnswerWhole(t *testing.T) {
	var p activeTurnProjection
	bigResult := []byte(`{"type":"tool_call_result","result":"` + strings.Repeat("r", 600<<10) + `"}`)
	for i := 0; i < 4; i++ { // ~2.4 MiB of finished tool calls
		key := "call-" + string(rune('a'+i))
		p.update(hubFrameMeta{kind: hubKindToolStart, key: key}, []byte(`{"type":"tool_call_start"}`), "s")
		p.update(hubFrameMeta{kind: hubKindToolResult, key: key}, bigResult, "s")
	}
	var want strings.Builder
	for i := 0; i < 8; i++ { // the answer keeps streaming well past the cap
		chunk := "part" + string(rune('0'+i)) + " " + strings.Repeat("a", 64<<10)
		want.WriteString(chunk)
		p.update(hubFrameMeta{kind: hubKindToken, messageID: "m-current", turnID: "t", content: chunk}, nil, "s")
	}
	snap := p.snapshot()
	require.NotEmpty(t, snap)
	last := snap[len(snap)-1]
	require.Equal(t, "m-current", last.messageID, "the message being written must survive the budget")
	assert.Equal(t, want.String(), last.text,
		"the current answer must be complete — start, middle and end — not frozen at the cap")
	assert.LessOrEqual(t, p.bytes, hubProjectionMaxBytes+want.Len(),
		"older items are evicted so the projection stays within budget (plus the current answer)")
	assert.Less(t, len(snap), 5, "older finished tool calls were evicted to make room")

	p.update(hubFrameMeta{kind: hubKindDone}, nil, "s")
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

// TestHub_AbandonedTurn_ClearsItsToolCardsToo pins #823 review item 9a: an
// abandoned turn (the B4 path: no done will ever come) must leave nothing of
// itself in the active-turn projection — its streamed text AND its tool
// cards. The first version only dropped the text, so every later snapshot
// showed the dead turn's tool calls "running" forever and the hub could
// never be evicted.
func TestHub_AbandonedTurn_ClearsItsToolCardsToo(t *testing.T) {
	f := newAttachFixture(t, "turn-abandoned", "msg-abandoned")
	require.NoError(t, f.streamer.Update(context.Background(), "working on it"))
	f.h.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Meta: agent.EventMeta{TurnID: "turn-abandoned"},
		Payload: agent.ToolExecStartPayload{
			ToolCallID: session.ToolCallID("call-stuck"), SessionID: f.sid, Tool: "bash",
		},
	})
	hub := f.h.hubs.lookup(f.sid)
	require.NotNil(t, hub)

	f.streamer.ReleaseStreamOwnership() // B4: abandoned, no Finalize

	hub.mu.Lock()
	items := hub.proj.snapshot()
	active := hub.proj.active()
	hub.mu.Unlock()
	assert.Empty(t, items, "nothing of an abandoned turn may stay in the projection")
	assert.False(t, active, "an abandoned turn must not pin the hub against idle eviction")
}
