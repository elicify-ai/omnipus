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

// TestHub_H12_SpanFramesOncePerSession_OneOrphanEndForTwoTabs pins
// BE-DESIGN.md §7 / H12: sub-agent span frames are produced ONCE per event
// per session (not once per connected tab), carry the session hub's
// sequence number, reach every tab bound to the session byte-identically,
// and an orphaned span gets exactly ONE synthetic subagent_end{interrupted}
// no matter how many tabs are watching — the pre-#823 per-connection
// forwarder ran one watchdog per tab and synthesized one end per tab.
func TestHub_H12_SpanFramesOncePerSession_OneOrphanEndForTwoTabs(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(30 * time.Millisecond)
	defer restore()

	const sid = "sess-h12"
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	bus.SetSyncTap(h.hubSyncTap)

	wcA, chA := makeForwarderTestConn(64)
	wcB, chB := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-a", sid, wcA)
	bindTestConnToSession(h, "chat-b", sid, wcB)

	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnSpawn,
		Payload: agent.SubTurnSpawnPayload{
			AgentID:           "ray",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "research",
			ChatID:            "chat-a",
			SessionID:         sid,
		},
	})
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{
			Status:    agent.TurnEndStatusCompleted,
			ChatID:    "chat-a",
			SessionID: sid,
			IsRoot:    true,
		},
	})

	// The span is never really ended; with no agent loop wired the liveness
	// check reports "not active", so the watchdog must synthesize the end.
	require.Eventually(t, func() bool { return len(chA) >= 2 && len(chB) >= 2 },
		3*time.Second, 5*time.Millisecond,
		"both tabs must receive subagent_start and the synthesized subagent_end")
	// Give any second (per-tab) watchdog ample time to misfire.
	time.Sleep(200 * time.Millisecond)

	framesA := drainAllFrames(chA)
	framesB := drainAllFrames(chB)
	require.Len(t, framesA, 2, "tab A: exactly one start and ONE synthetic end — never one end per tab")
	require.Len(t, framesB, 2, "tab B: exactly one start and ONE synthetic end — never one end per tab")
	for i := range framesA {
		assert.Equal(t, string(framesA[i]), string(framesB[i]),
			"frame %d must be byte-identical on both tabs (one publish, one seq)", i)
	}

	hub := h.hubs.lookup(sid)
	require.NotNil(t, hub)
	starts := journalFramesOfType(t, hub, "subagent_start")
	ends := journalFramesOfType(t, hub, "subagent_end")
	require.Len(t, starts, 1, "the session journal must hold exactly one subagent_start")
	require.Len(t, ends, 1, "the session journal must hold exactly one synthetic subagent_end for two tabs")
	assert.Equal(t, "interrupted", ends[0]["status"])
	assert.Equal(t, "parent_done_early", ends[0]["message"])
	startSeq, ok := starts[0]["seq"].(float64)
	require.True(t, ok, "subagent_start must carry the hub's seq")
	endSeq, ok := ends[0]["seq"].(float64)
	require.True(t, ok, "the synthetic subagent_end must carry the hub's seq")
	assert.Equal(t, startSeq+1, endSeq, "span frames are numbered contiguously in the session journal")
}

// TestHub_SpanRealEndBeatsWatchdog_NoSyntheticEnd pins the race the old
// orphanFires hand-off guarded: once the real subagent_end has been
// published, a watchdog that fires afterwards must not add a synthetic one.
func TestHub_SpanRealEndBeatsWatchdog_NoSyntheticEnd(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(20 * time.Millisecond)
	defer restore()

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
	bus.Emit(agent.Event{Kind: agent.EventKindSubTurnEnd, Payload: agent.SubTurnEndPayload{
		Status: agent.SubTurnStatusSuccess, SpanID: "span_c2", ParentSpawnCallID: session.ToolCallID("c2"),
		ChatID: "chat-a", SessionID: sid,
	}})
	time.Sleep(200 * time.Millisecond) // 10x the watchdog period

	frames := drainAllFrames(ch)
	require.Len(t, frames, 2, "subagent_start + the real subagent_end only")
	assert.Equal(t, "success", subagentEndStatus(t, frames[1]))
}
