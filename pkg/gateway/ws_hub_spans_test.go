// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// journalFramesOfType returns the journaled (seq-stamped) frames of hub
// whose "type" equals frameType, in seq order.
func journalFramesOfType(t *testing.T, hub *sessionHub, frameType string) []map[string]any {
	t.Helper()
	hub.mu.Lock()
	entries := append([]journalEntry(nil), hub.journal...)
	hub.mu.Unlock()
	var out []map[string]any
	for _, e := range entries {
		var m map[string]any
		require.NoError(t, json.Unmarshal(e.bytes, &m))
		if m["type"] == frameType {
			out = append(out, m)
		}
	}
	return out
}

// TestHub_H12_SpanFramesOncePerSession_TwoTabs pins BE-DESIGN.md §7 / H12,
// re-expressed on ADR-091 semantics: sub-agent lifecycle frames
// (subagent_start, subagent_message, subagent_state, subagent_end) are
// produced ONCE per event per session (not once per connected tab), carry
// the session hub's sequence number, are journaled contiguously, and reach
// every tab bound to the session byte-identically. The #823 original also
// asserted ONE synthetic orphan end for two tabs; ADR-091 UAT defect 2
// retired the orphan watchdog outright (a steered child is designed to
// outlive its parent's turn), so that half is replaced by
// TestHub_ParentTurnEnd_NeverSynthesizesSpanEnd below.
func TestHub_H12_SpanFramesOncePerSession_TwoTabs(t *testing.T) {
	const sid = "sess-h12"
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	bus.SetSyncTap(h.hubSyncTap)

	wcA, chA := makeForwarderTestConn(64)
	wcB, chB := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-a", sid, wcA)
	bindTestConnToSession(h, "chat-b", sid, wcB)

	child := "child-session-1"
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "ray",
			Label:             child,
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "research",
			ChatID:            "chat-a",
			SessionID:         sid,
		},
	})
	text := "halfway there"
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubagentMessage,
		Payload: agent.SubagentMessagePayload{
			SessionID: sid,
			MessageID: "m1",
			Frame: generated.SubagentMessageFrame{
				Type: string(generated.WsFrameTypeSubagentMessage), SessionId: sid, SpanId: "span_c1",
				ChildSessionId: &child, MessageId: "m1", Kind: "progress", Text: &text,
				SenderIdentity: "ray", CreatedAt: "2026-09-25T10:00:00Z",
			},
		},
	})
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubagentState,
		Payload: agent.SubagentStatePayload{
			SessionID: sid,
			MessageID: "s1",
			Frame: generated.SubagentStateFrame{
				Type: string(generated.WsFrameTypeSubagentState), SessionId: sid, SpanId: "span_c1",
				ChildSessionId: &child, State: "completed", CreatedAt: "2026-09-25T10:00:01Z",
			},
		},
	})
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnEnd,
		Payload: agent.SubTurnEndPayload{
			AgentID: "ray", Status: agent.SubTurnStatusSuccess, SpanID: "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"), ChatID: "chat-a", SessionID: sid,
		},
	})

	framesA := drainAllFrames(chA)
	framesB := drainAllFrames(chB)
	require.Len(t, framesA, 4, "tab A: start, message, state, end — exactly once each")
	require.Len(t, framesB, 4, "tab B: start, message, state, end — exactly once each")
	for i := range framesA {
		assert.Equal(t, string(framesA[i]), string(framesB[i]),
			"frame %d must be byte-identical on both tabs (one publish, one seq)", i)
	}

	hub := h.hubs.lookup(sid)
	require.NotNil(t, hub)
	var seqs []float64
	for _, typ := range []string{"subagent_start", "subagent_message", "subagent_state", "subagent_end"} {
		got := journalFramesOfType(t, hub, typ)
		require.Len(t, got, 1, "the session journal must hold exactly one %s for two tabs", typ)
		seq, ok := got[0]["seq"].(float64)
		require.True(t, ok, "%s must carry the hub's seq", typ)
		seqs = append(seqs, seq)
	}
	for i := 1; i < len(seqs); i++ {
		assert.Equal(t, seqs[i-1]+1, seqs[i], "lifecycle frames are numbered contiguously in the session journal")
	}
	start := journalFramesOfType(t, hub, "subagent_start")[0]
	assert.Equal(t, child, start["child_session_id"], "ADR-091 I-4: the open control's target rides subagent_start")
	assert.Equal(t, "success", journalFramesOfType(t, hub, "subagent_end")[0]["status"])
}

// TestHub_ParentTurnEnd_NeverSynthesizesSpanEnd pins ADR-091 UAT defect 2
// through the hub: the parent's root turn ending while a steered child's
// span is still open publishes NOTHING for that span — no synthetic
// subagent_end{interrupted} is journaled or delivered. Only the child's own
// real end closes the span.
func TestHub_ParentTurnEnd_NeverSynthesizesSpanEnd(t *testing.T) {
	const sid = "sess-real-end"
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	bus.SetSyncTap(h.hubSyncTap)
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-a", sid, wc)

	bus.Emit(agent.Event{Kind: agent.EventKindSubTurnSpawn, Payload: agent.SubTurnSpawnPayload{
		SpanID: "span_c2", ParentSpawnCallID: session.ToolCallID("c2"), ChatID: "chat-a", SessionID: sid,
	}})
	bus.Emit(agent.Event{Kind: agent.EventKindTurnEnd, Payload: agent.TurnEndPayload{
		Status: agent.TurnEndStatusCompleted, ChatID: "chat-a", SessionID: sid, IsRoot: true,
	}})
	time.Sleep(200 * time.Millisecond)
	require.Len(t, drainAllFrames(ch), 1, "only subagent_start — the parent's turn end must not close the span")

	bus.Emit(agent.Event{Kind: agent.EventKindSubTurnEnd, Payload: agent.SubTurnEndPayload{
		Status: agent.SubTurnStatusSuccess, SpanID: "span_c2", ParentSpawnCallID: session.ToolCallID("c2"),
		ChatID: "chat-a", SessionID: sid,
	}})
	frames := drainAllFrames(ch)
	require.Len(t, frames, 1, "the child's real subagent_end")
	assert.Equal(t, "success", subagentEndStatus(t, frames[0]))
	hub := h.hubs.lookup(sid)
	require.NotNil(t, hub)
	require.Len(t, journalFramesOfType(t, hub, "subagent_end"), 1)
}

// drainAllFrames non-blockingly drains every currently-queued raw frame
// from ch. (Moved here from the retired orphan_watchdog_liveness_test.go.)
func drainAllFrames(ch chan []byte) [][]byte {
	var out [][]byte
	for {
		select {
		case raw := <-ch:
			out = append(out, raw)
		default:
			return out
		}
	}
}

// subagentEndStatus decodes a subagent_end frame's status.
func subagentEndStatus(t *testing.T, raw []byte) string {
	t.Helper()
	var f replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "subagent_end", f.Type)
	return f.Status
}
