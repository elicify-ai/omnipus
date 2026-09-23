// Regression coverage for backend-observable reload/replay bugs found by live
// UAT re-verification (2026-07) and by the ADR-091 seven-reviewer gate
// (2026-09, finding 1):
//
//   - Symptom A / finding 1's "still running" case: a reload landing while an
//     async delegate's real sub-turn is still genuinely running previously
//     showed a fabricated "done 0ms" snapshot — read literally from the
//     placeholder ack async delegation writes the instant its spawning tool
//     call returns (Status="success", DurationMS≈0; see
//     session.UnifiedStore.UpdateToolCallStatus's doc comment), long before
//     the real completion.
//   - Symptom B: a completed delegation's specialized, per-agent subagent
//     span widget disappeared on reload even though the flat "Delegate
//     task" pill replayed correctly, because the span-level agent_id on
//     subagent_start/subagent_end was resolved from the PARENT's own
//     identity instead of the REAL delegate's identity.
//   - Finding 1 (CRITICAL, 2026-09 gate): a persisted subagent_end with a
//     REAL terminal status (failed, cancelled, timed out) previously
//     replayed as a fabricated "success, 0 ms" too, because the withhold
//     mechanism that was supposed to protect the "still running" case above
//     was itself structurally dead (isSpanActive was wired to
//     agent.AgentLoop.IsSubTurnActiveForSpawnCall, whose two data sources —
//     steering.go's markSubTurnSpanOpen and turnState.parentSpawnCallID —
//     had zero real callers / were never assigned, ADR-091 having deleted
//     their only writer, pkg/agent/subturn.go). The fix reads the persisted
//     subagent_end entry back (dispatchSpecialEntry in replay.go) and
//     derives "still genuinely running" from the transcript's own persisted
//     subagent_start/subagent_end entries instead of a live callback — see
//     buildPersistedSubagentSpanIndexes and classifyToolCall's stillActive
//     doc comment in replay.go.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// subagentStartEntry builds a persisted subagent_start system entry exactly
// as steer_frames.go's deliverSubagentStart writes one into the PARENT's own
// transcript (ADR-091 D7/I-4) — spanID must follow the "span_" + tool-call-id
// convention (steer_frames.go's subagentSpanID / replay.go's classifyToolCall
// spanID) for classifyToolCall's persisted-span indexes to key it correctly.
func subagentStartEntry(spanID, parentCallID, taskLabel string) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:            spanID + ":start",
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentStart,
		SubagentStart: &generated.SubagentStartFrame{
			Type:         string(generated.WsFrameTypeSubagentStart),
			SessionId:    "session_test",
			SpanId:       spanID,
			ParentCallId: parentCallID,
			TaskLabel:    taskLabel,
		},
	}
}

// subagentEndEntry builds a persisted subagent_end system entry exactly as
// steer_frames.go's deliverSubagentEnd writes one once the real sub-turn
// concludes — carrying the REAL terminal status/duration, never the
// placeholder ack that lives on the spawn/delegate ToolCall record itself.
func subagentEndEntry(spanID, status string, durationMS int) session.TranscriptEntry {
	d := durationMS
	return session.TranscriptEntry{
		ID:            spanID + ":end",
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentEnd,
		SubagentEnd: &generated.SubagentEndFrame{
			Type:       string(generated.WsFrameTypeSubagentEnd),
			SessionId:  "session_test",
			SpanId:     spanID,
			Status:     status,
			DurationMs: &d,
		},
	}
}

// TestStreamReplay_ActiveSpawnSpan_WithholdsFabricatedDoneSnapshot is the
// core regression test for Symptom A / finding 1's "still running" case:
// when the transcript's own persisted subagent_start entry has no matching
// subagent_end yet, replay must NOT emit that call's own terminal frames
// (subagent_end, the outer tool_call_result) — only tool_call_start /
// subagent_start, the same shape a genuinely in-flight LIVE call shows.
// Already-completed NESTED child tool calls (real, historical data) still
// replay normally.
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
		// Persisted at launch (deliverSubagentStart fires synchronously
		// before the child does any work) — NO matching subagent_end entry:
		// this is exactly what a genuinely still-running delegation looks
		// like in the transcript at reload time.
		subagentStartEntry("span_c1", "c1", "research"),
	}

	frames, _ := runReplay(t, entries)

	types := frameTypes(frames)
	// ADR-091 D7: emitNestedToolCalls is deleted — a delegated child's own
	// tool calls (nestedTC, "t2" above) are no longer nested-replayed under
	// the outer span at all (a delegated/task child owns its own transcript
	// now, D1); nestedTC's presence in this fixture only proves it does NOT
	// resurrect the deleted nested-emission path.
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start", // spawn call start
			"subagent_start",  // span bracket open
			// NO subagent_end, NO tool_call_result for c1: the real
			// sub-turn is still active, so its terminal frames are
			// withheld rather than fabricating "done" from the placeholder.
			"done",
		},
		types,
		"BUG REGRESSION: a still-genuinely-running spawn/delegate call's own terminal frames must be "+
			"withheld, not fabricated from its placeholder ack",
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
// also engages correctly for a transcript still carrying the legacy value.
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
		subagentStartEntry("span_c1", "c1", "research"),
	}

	frames, _ := runReplay(t, entries)

	types := frameTypes(frames)
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start", // spawn call start
			"subagent_start",  // span bracket open
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
		subagentStartEntry("span_c1", "c1", "research"),
	}

	frames, _ := runReplay(t, entries)

	types := frameTypes(frames)
	require.Equal(t,
		[]string{"replay_message", "tool_call_start", "subagent_start", "done"},
		types,
		"an active spawn/delegate call with no recorded children yet must emit tool_call_start + "+
			"subagent_start (matching live's always-on EventKindSubTurnSpawn) but withhold subagent_end/"+
			"tool_call_result — the placeholder ack's success/0ms result must never be shown as done",
	)
}

// TestStreamReplay_InactiveSpawnCall_EmitsNormally is the negative-case
// sanity check for a LEGACY (pre-ADR-091) transcript: it carries neither a
// persisted subagent_start nor a persisted subagent_end entry for its spawn
// call — the whole persisted-lifecycle mechanism predates it — so replay
// must fall back to the spawn call's own tc.Status/DurationMS exactly as
// before, proving the new persisted-span gate never turns an
// already-finished OLD delegation into a perpetually-"running" one.
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

	frames, _ := runReplay(t, entries)

	types := frameTypes(frames)
	// ADR-091 D7: emitNestedToolCalls is deleted — nestedTC ("t2") is no
	// longer nested-replayed under the outer span; only the outer span's
	// own start/end bracket and its own tool_call_start/result remain.
	require.Equal(t,
		[]string{
			"replay_message",
			"tool_call_start",
			"subagent_start",
			"subagent_end",
			"tool_call_result",
			"done",
		},
		types,
		"a genuinely finished LEGACY spawn call (no persisted subagent_start/end entries at all) "+
			"must replay its own terminal frame set from tc.Status exactly as before",
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

	frames, _ := runReplay(t, entries)

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

// ─────────────────────────────────────────────────────────────────────────────
// Finding 1 (CRITICAL, ADR-091 seven-reviewer gate, 2026-09): a persisted
// subagent_end with a REAL terminal status must replay as THAT status, never
// as the spawn call's own placeholder "success, 0 ms" ack.
// ─────────────────────────────────────────────────────────────────────────────

// TestStreamReplay_PersistedSubagentEnd_Error_ReplaysAsError is finding 1's
// first required case: a worker that FAILED must show failed, not
// "success, 0 ms". The spawn call's own ToolCall record still carries the
// placeholder ack (Status="success", DurationMS≈0 — async delegation never
// corrects it in place); the REAL outcome lives only in the persisted
// subagent_end entry deliverSubagentEnd wrote once the sub-turn concluded.
func TestStreamReplay_PersistedSubagentEnd_Error_ReplaysAsError(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success", // placeholder ack — NEVER corrected in place
		DurationMS: 0,         // placeholder ack — NEVER corrected in place
		Parameters: map[string]any{"task": "research something", "async": true},
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC),
		subagentStartEntry("span_c1", "c1", "research"),
		// The REAL outcome: the sub-turn errored out after 7.3s.
		subagentEndEntry("span_c1", "error", 7300),
	}

	frames, _ := runReplay(t, entries)

	subEnd := findFrame(frames, "subagent_end")
	require.NotNil(t, subEnd, "subagent_end frame must be emitted")
	assert.Equal(t, "error", subEnd.Status,
		"BUG REGRESSION: a persisted subagent_end with status \"error\" must replay as \"error\", "+
			"not as the spawn call's placeholder \"success\"")
	assert.EqualValues(t, 7300, subEnd.DurationMs,
		"the REAL duration from the persisted subagent_end must replay, not the placeholder 0ms")

	// Exactly one subagent_end frame: the tool-call-derived synthetic one
	// (built from the placeholder tc.Status) must be suppressed, not
	// emitted ALONGSIDE the real persisted one.
	assert.Len(t, filterByType(frames, "subagent_end"), 1,
		"BUG REGRESSION: only the REAL persisted subagent_end may reach the client — the "+
			"tool-call-derived synthetic one (from the placeholder ack) must be suppressed")
}

// TestStreamReplay_PersistedSubagentEnd_Cancelled_ReplaysAsCancelled is
// finding 1's second required case: a worker that was CANCELLED must show
// cancelled, not "success, 0 ms".
func TestStreamReplay_PersistedSubagentEnd_Cancelled_ReplaysAsCancelled(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success", // placeholder ack
		DurationMS: 0,         // placeholder ack
		Parameters: map[string]any{"task": "research something", "async": true},
	}
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC),
		subagentStartEntry("span_c1", "c1", "research"),
		subagentEndEntry("span_c1", "cancelled", 2100),
	}

	frames, _ := runReplay(t, entries)

	subEnd := findFrame(frames, "subagent_end")
	require.NotNil(t, subEnd, "subagent_end frame must be emitted")
	assert.Equal(t, "cancelled", subEnd.Status,
		"BUG REGRESSION: a persisted subagent_end with status \"cancelled\" must replay as "+
			"\"cancelled\", not as the spawn call's placeholder \"success\"")
	assert.EqualValues(t, 2100, subEnd.DurationMs)
	assert.Len(t, filterByType(frames, "subagent_end"), 1,
		"only the REAL persisted subagent_end may reach the client")
}

// TestStreamReplay_PersistedSubagentEnd_SessionIDRestamped proves the
// persisted subagent_end entry replays with SessionId re-stamped from the
// transcript it was read FROM (the self-healing pattern subagent_message/
// subagent_state already use two blocks above in dispatchSpecialEntry) —
// not whatever SessionId the stored frame happens to carry.
func TestStreamReplay_PersistedSubagentEnd_SessionIDRestamped(t *testing.T) {
	spawnTC := session.ToolCall{
		ID:         "c1",
		Tool:       "delegate",
		Status:     "success",
		DurationMS: 0,
	}
	endEntry := subagentEndEntry("span_c1", "timeout", 30000)
	endEntry.SubagentEnd.SessionId = "some-other-session-id" // stale/wrong on disk
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", spawnTC),
		subagentStartEntry("span_c1", "c1", "research"),
		endEntry,
	}

	frames, _ := runReplay(t, entries)

	subEnd := findFrame(frames, "subagent_end")
	require.NotNil(t, subEnd)
	assert.Equal(t, "session_test", subEnd.SessionID,
		"subagent_end must replay with SessionId re-stamped from the transcript owner, not the "+
			"stored (possibly stale) value")
	assert.Equal(t, "timeout", subEnd.Status)
}
