// Regression coverage for two backend-observable reload/replay bugs found by
// live UAT re-verification (2026-07):
//
//   - Symptom A: a reload landing while an async delegate's real sub-turn is
//     still genuinely running previously showed a fabricated "done 0ms"
//     snapshot — read literally from the placeholder ack async delegation
//     writes the instant its spawning tool call returns (Status="success",
//     DurationMS≈0; see session.UnifiedStore.UpdateToolCallStatus's doc
//     comment), long before the real completion (spawnSubTurn's cleanup
//     defer, once EventKindSubTurnEnd actually fires) corrects it.
//   - Symptom B: a completed delegation's specialized, per-agent subagent
//     span widget disappeared on reload even though the flat "Delegate
//     task" pill replayed correctly, because the span-level agent_id on
//     subagent_start/subagent_end was resolved from the PARENT's own
//     identity (the outer spawn/delegate ToolCall's own transcript entry,
//     written by the delegator) instead of the REAL delegate's identity
//     (which live rendering already gets right — see
//     pkg/agent/subturn.go's SubTurnSpawnPayload.AgentID :=
//     childTS.agentID).

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// runReplayWithSpanActive is runReplay's counterpart that also wires an
// isSpanActive callback, letting tests simulate "this spawn/delegate call's
// real sub-turn is still genuinely running" without a real AgentLoop.
func runReplayWithSpanActive(
	t *testing.T,
	entries []session.TranscriptEntry,
	isSpanActive func(string) bool,
) ([]replayFrameDecoder, int) {
	t.Helper()
	sink := &sliceSink{}
	rs := computeReplayStats(entries)
	n, err := streamReplay(context.Background(), "session_test", entries, rs, sink.emit, nil, nil, isSpanActive, nil)
	require.NoError(t, err, "streamReplay must not return an error for valid input")
	return sink.all(), n
}

// TestStreamReplay_ActiveSpawnSpan_WithholdsFabricatedDoneSnapshot is the
// core regression test for Symptom A: when isSpanActive reports the spawn
// call's real sub-turn is STILL running, replay must NOT emit that call's
// own terminal frames (subagent_end, the outer tool_call_result) — only
// tool_call_start / subagent_start, the same shape a genuinely in-flight
// LIVE call shows. Already-completed NESTED child tool calls (real,
// historical data) still replay normally.
func TestStreamReplay_ActiveSpawnSpan_WithholdsFabricatedDoneSnapshot(t *testing.T) {
	// spawnTC carries a PLACEHOLDER terminal-looking record — exactly what
	// async delegation writes the instant the spawning call returns,
	// well before the real sub-turn finishes.
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success", // placeholder ack — NOT the real terminal status
		DurationMS: 0,         // placeholder ack — NOT the real duration
		Parameters: map[string]any{"task": "research something", "label": "research", "async": true},
	}
	// A real, already-completed nested child tool call — historical data
	// that must still replay normally even though the OUTER span is still
	// active.
	nestedTC := session.ToolCall{
		ID:               "t2",
		Tool:             "web_search",
		Status:           "success",
		DurationMS:       850,
		ParentToolCallID: "c1",
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC, nestedTC),
	}

	frames, _ := runReplayWithSpanActive(t, entries, func(id string) bool { return id == "c1" })

	types := frameTypes(frames)
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start",  // spawn call start
			"subagent_start",   // span bracket open
			"tool_call_start",  // nested t2 (real, completed — still replays)
			"tool_call_result", // nested t2 (real, completed — still replays)
			// NO subagent_end, NO tool_call_result for c1: the real
			// sub-turn is still active, so its terminal frames are
			// withheld rather than fabricating "done" from the placeholder.
			"done",
		},
		types,
		"BUG REGRESSION: an active spawn/delegate call's own terminal frames must be withheld, "+
			"not fabricated from its placeholder ack",
	)

	resultFrames := filterByType(frames, "tool_call_result")
	for _, f := range resultFrames {
		assert.NotEqual(t, "c1", f.CallID,
			"the still-active spawn call must never get its own tool_call_result frame on replay")
	}
	assert.Nil(t, findFrame(frames, "subagent_end"),
		"the still-active spawn call must never get a subagent_end frame on replay")
}

// TestStreamReplay_ActiveSpawnSpan_LegacySpawnToolName_WithholdsFabricatedDoneSnapshot
// is the same regression as TestStreamReplay_ActiveSpawnSpan_WithholdsFabricatedDoneSnapshot
// above, but with Tool: "spawn" (the pre-ADR-036 tool name) instead of
// "delegate". streamReplay's isDelegateSpawnCall gate
// (tc.Tool == "spawn" || tc.Tool == "delegate") is only exercised elsewhere in
// this file with Tool: "delegate" — this confirms the liveness-withholding
// also engages correctly for old transcripts recorded before the ADR-036
// spawn->delegate rename and still carrying the legacy value.
func TestStreamReplay_ActiveSpawnSpan_LegacySpawnToolName_WithholdsFabricatedDoneSnapshot(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "spawn",   // legacy pre-ADR-036 tool name
		Status:     "success", // placeholder ack — NOT the real terminal status
		DurationMS: 0,         // placeholder ack — NOT the real duration
		Parameters: map[string]any{"task": "research something", "label": "research", "async": true},
	}
	nestedTC := session.ToolCall{
		ID:               "t2",
		Tool:             "web_search",
		Status:           "success",
		DurationMS:       850,
		ParentToolCallID: "c1",
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC, nestedTC),
	}

	frames, _ := runReplayWithSpanActive(t, entries, func(id string) bool { return id == "c1" })

	types := frameTypes(frames)
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start",  // spawn call start
			"subagent_start",   // span bracket open
			"tool_call_start",  // nested t2 (real, completed — still replays)
			"tool_call_result", // nested t2 (real, completed — still replays)
			// NO subagent_end, NO tool_call_result for c1: the real
			// sub-turn is still active, so its terminal frames are
			// withheld rather than fabricating "done" from the placeholder.
			"done",
		},
		types,
		"BUG REGRESSION: the liveness-withholding gate must engage for the legacy \"spawn\" tool "+
			"name, not just \"delegate\" — old transcripts recorded before the ADR-036 rename must "+
			"get the same protection",
	)

	resultFrames := filterByType(frames, "tool_call_result")
	for _, f := range resultFrames {
		assert.NotEqual(t, "c1", f.CallID,
			"the still-active legacy spawn call must never get its own tool_call_result frame on replay")
	}
	assert.Nil(t, findFrame(frames, "subagent_end"),
		"the still-active legacy spawn call must never get a subagent_end frame on replay")
}

// TestStreamReplay_ActiveSpawnCall_NoChildrenYet_WithholdsResult covers the
// exact live-UAT repro for Symptom A: a background delegate reloaded before
// its first step landed ("0 steps working" live, "done 0ms" on reload). With
// zero recorded children, the call takes the FLAT emission path (not the
// spawn-parent bracket path) — this proves the liveness withholding applies
// there too.
func TestStreamReplay_ActiveSpawnCall_NoChildrenYet_WithholdsResult(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success", // placeholder ack
		DurationMS: 0,         // placeholder ack
		Parameters: map[string]any{"task": "research something", "async": true},
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC),
	}

	frames, _ := runReplayWithSpanActive(t, entries, func(id string) bool { return id == "c1" })

	types := frameTypes(frames)
	// Finding C (A-I4 round 4): every spawn/delegate call now gets a
	// subagent_start bracket unconditionally (streamReplay's isSpawnParent
	// is isDelegateSpawnCall, not "has at least one recorded child") —
	// matching live, which always fires EventKindSubTurnSpawn for a delegate
	// call regardless of how many tool calls the child has made so far. A
	// still-active 0-step call now correctly shows "0 steps, working" on
	// reload instead of no span at all; this assertion was updated from its
	// prior `["replay_message", "tool_call_start", "done"]` (no
	// subagent_start) to match. subagent_end + the outer tool_call_result
	// stay withheld (Symptom A's own regression coverage, unaffected by this
	// change) — the placeholder ack's success/0ms result must still never be
	// shown as done.
	require.Equal(t,
		[]string{"replay_message", "tool_call_start", "subagent_start", "done"},
		types,
		"an active spawn/delegate call with no recorded children yet must emit tool_call_start + "+
			"subagent_start (matching live's always-on EventKindSubTurnSpawn) but withhold subagent_end/"+
			"tool_call_result — the placeholder ack's success/0ms result must never be shown as done",
	)
}

// TestStreamReplay_InactiveSpawnCall_EmitsNormally is the negative-case
// sanity check: when isSpanActive reports false (the real sub-turn HAS
// finished), replay must emit the full, normal sequence exactly as before —
// proving the liveness gate only ever WITHHOLDS, never alters, the terminal
// frame content itself.
func TestStreamReplay_InactiveSpawnCall_EmitsNormally(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success",
		DurationMS: 4200,
		Parameters: map[string]any{"task": "research something"},
	}
	nestedTC := session.ToolCall{
		ID:               "t2",
		Tool:             "web_search",
		Status:           "success",
		DurationMS:       850,
		ParentToolCallID: "c1",
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC, nestedTC),
	}

	frames, _ := runReplayWithSpanActive(t, entries, func(string) bool { return false })

	types := frameTypes(frames)
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start",
			"subagent_start",
			"tool_call_start",
			"tool_call_result",
			"subagent_end",
			"tool_call_result",
			"done",
		},
		types,
		"a genuinely finished spawn call must replay its full terminal frame set as before",
	)
	subEnd := findFrame(frames, "subagent_end")
	require.NotNil(t, subEnd)
	assert.Equal(t, "success", subEnd.Status)
	assert.EqualValues(t, 4200, subEnd.DurationMs)
}

// TestStreamReplay_SpawnSpan_UsesRealChildAgentID_NotDelegatorID is the
// regression test for Symptom B: the span-level subagent_start/subagent_end
// agent_id must be resolved from the REAL delegate's own identity (the
// nested child tool call's own transcript entry.AgentID — written by the
// CHILD sub-turn per ADR-032) rather than the delegator's identity (the
// outer spawn/delegate ToolCall's own entry.AgentID, written by the
// PARENT). The flat "Delegate task" pill (tool_call_start/tool_call_result
// for the spawn call itself) intentionally keeps the delegator's own
// attribution — matching the live UAT observation that the raw pill
// replayed correctly while the specialized named sub-span did not.
func TestStreamReplay_SpawnSpan_UsesRealChildAgentID_NotDelegatorID(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success",
		DurationMS: 18000,
		Parameters: map[string]any{"task": "browse and summarize", "label": "web research"},
	}
	nestedTC := session.ToolCall{
		ID:               "t2",
		Tool:             "web_search",
		Status:           "success",
		DurationMS:       900,
		ParentToolCallID: "c1",
	}
	// The PARENT (delegator) writes the outer spawn call's own transcript
	// entry under ITS OWN identity ("mia").
	parentEntry := session.TranscriptEntry{
		ID:        "entry-parent",
		Role:      "assistant",
		Content:   "delegating to Ray",
		AgentID:   "mia",
		ToolCalls: []session.ToolCall{spawnTC},
	}
	// The CHILD sub-turn writes its own nested tool call under ITS OWN
	// (the real delegate's) identity ("ray") — this is what live rendering
	// already gets right for subagent_start/subagent_end (childTS.agentID).
	childEntry := session.TranscriptEntry{
		ID:        "entry-child",
		Role:      "assistant",
		Content:   "researching",
		AgentID:   "ray",
		ToolCalls: []session.ToolCall{nestedTC},
	}
	entries := []session.TranscriptEntry{parentEntry, childEntry}

	frames, _ := runReplayWithSpanActive(t, entries, func(string) bool { return false })

	subStart := findFrame(frames, "subagent_start")
	require.NotNil(t, subStart, "subagent_start frame must be emitted")
	assert.Equal(t, "ray", subStart.AgentID,
		"BUG REGRESSION: subagent_start.agent_id must reflect the REAL delegate's own identity "+
			"(resolved from the nested child's own entry.AgentID), matching live rendering — "+
			"not the delegator's identity")

	subEnd := findFrame(frames, "subagent_end")
	require.NotNil(t, subEnd, "subagent_end frame must be emitted")
	assert.Equal(t, "ray", subEnd.AgentID,
		"BUG REGRESSION: subagent_end.agent_id must also reflect the REAL delegate's own identity")

	// The outer "Delegate task" pill (spawn call's own start/result frames)
	// intentionally keeps the delegator's own attribution — unaffected by
	// this fix, matching the live UAT observation that the raw pill
	// replayed correctly.
	startFrames := filterByType(frames, "tool_call_start")
	var spawnStart *replayFrameDecoder
	for i := range startFrames {
		if startFrames[i].CallID == "c1" {
			spawnStart = &startFrames[i]
		}
	}
	require.NotNil(t, spawnStart)
	assert.Equal(t, "mia", spawnStart.AgentID,
		"the outer 'Delegate task' pill must keep the delegator's own attribution, unaffected by the fix")
}
