// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestReplay_StatusPollsDoNotSynthesizeSpawnSpans is the reload of a parent
// that launched once and then polled. The one real run has a saved start and
// end. The polls must stay ordinary tool calls: one extra pair per poll is
// the bug.
func TestReplay_StatusPollsDoNotSynthesizeSpawnSpans(t *testing.T) {
	const callID = "call-run"
	const polls = 6
	run := toolCall(callID, "delegate", "success", 0, map[string]any{
		"action":   "run",
		"task":     "draft the note",
		"agent_id": "general-purpose",
	}, map[string]any{"text": `{"session_id":"session_child","state":"running","generation":1}`})
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "jim", run),
		followUpStartEntry(callID+":start", "span_"+callID, callID),
		followUpEndEntry(callID+":end", "span_"+callID, callID, "success"),
	}
	pollsOnTranscript := 0
	pollCalls := make([]session.ToolCall, 0, polls)
	for i := 1; i <= polls; i++ {
		action := "status"
		if i == polls {
			action = "peek"
		}
		pollsOnTranscript++
		pollCalls = append(pollCalls, toolCall(
			"call-poll-"+action+"-"+strconv.Itoa(i),
			"delegate",
			"success",
			5,
			map[string]any{"action": action, "session_id": "session_child"},
			map[string]any{"text": "still running"},
		))
	}
	entries = append(entries, assistantEntry("polling", "jim", pollCalls...))
	if pollsOnTranscript != polls {
		t.Fatalf("fixture built %d polls, want %d — a zero count would make 'no extra span' vacuous", pollsOnTranscript, polls)
	}

	frames, _ := runReplay(t, entries)

	starts := spanIDsOfType(frames, "subagent_start")
	ends := spanIDsOfType(frames, "subagent_end")
	if len(starts) != 1 || starts[0] != "span_"+callID {
		t.Fatalf("subagent_start = %v, want exactly [span_%s]", starts, callID)
	}
	if len(ends) != 1 || ends[0] != "span_"+callID {
		t.Fatalf("subagent_end = %v, want exactly [span_%s]", ends, callID)
	}
	seenPolls := 0
	for _, f := range frames {
		if f.Type == "tool_call_start" && strings.HasPrefix(f.CallID, "call-poll-") {
			seenPolls++
		}
	}
	if seenPolls != polls {
		t.Fatalf("poll tool_call_start count = %d, want %d — the polls must be in the stream, or a missing span proves nothing", seenPolls, polls)
	}
}

// TestReplay_RefusedDelegateRunSynthesizesNoSpan is a run that never created
// a child. Replay must not invent a "delegated / stopped" pair for it. The
// call itself still replays, so the missing span is not an empty transcript.
func TestReplay_RefusedDelegateRunSynthesizesNoSpan(t *testing.T) {
	refused := toolCall("call-refused", "delegate", "error", 3, map[string]any{
		"action": "run",
		"task":   "do the thing",
	}, map[string]any{
		"text":  "delegate: launch: delegation refused: depth cap",
		"error": true,
	})
	frames, _ := runReplay(t, []session.TranscriptEntry{
		assistantEntry("trying", "jim", refused),
	})

	sawCall := false
	for _, f := range frames {
		if f.Type == "subagent_start" || f.Type == "subagent_end" {
			t.Fatalf("refused run synthesized %s (span %q)", f.Type, f.SpanID)
		}
		if f.Type == "tool_call_start" && f.CallID == "call-refused" {
			sawCall = true
		}
	}
	if !sawCall {
		t.Fatal("refused run emitted no tool_call_start — a missing span would be vacuous")
	}
}

// TestReplay_LegacyLaunchWithoutPersistedStartKeepsToolCallSpan is a
// pre-ADR-091 launch: no saved start, and the tool call's own record is the
// span. That path must keep its tool-call-derived start and end.
func TestReplay_LegacyLaunchWithoutPersistedStartKeepsToolCallSpan(t *testing.T) {
	const callID = "call-legacy"
	legacy := toolCall(callID, "delegate", "success", 42, map[string]any{"task": "write the report"}, nil)
	frames, _ := runReplay(t, []session.TranscriptEntry{
		assistantEntry("delegating", "mia", legacy),
	})

	var starts, ends []replayFrameDecoder
	for _, f := range frames {
		if f.SpanID != "span_"+callID {
			continue
		}
		switch f.Type {
		case "subagent_start":
			starts = append(starts, f)
		case "subagent_end":
			ends = append(ends, f)
		}
	}
	if len(starts) != 1 {
		t.Fatalf("legacy start count = %d, want 1", len(starts))
	}
	if starts[0].TaskLabel != "write the report" {
		t.Fatalf("legacy task label = %q, want the tool call's task", starts[0].TaskLabel)
	}
	if len(ends) != 1 {
		t.Fatalf("legacy end count = %d, want 1", len(ends))
	}
	if ends[0].Status != "success" || ends[0].DurationMs != 42 {
		t.Fatalf("legacy end status=%q duration=%d, want success and 42", ends[0].Status, ends[0].DurationMs)
	}
}

// TestReplay_ConsumedWakeMarkerIsNotAVisibleFrame is the wake bookmark
// ("consumed <message id>"). It stays in the transcript for the wake path.
// Replay must not send it as a chat line. A neighbouring system line is
// emitted, so the filter is not "drop every system entry".
func TestReplay_ConsumedWakeMarkerIsNotAVisibleFrame(t *testing.T) {
	const marker = "consumed session_01M3ABCDEF:1:final"
	const visible = "agent switched"
	entries := []session.TranscriptEntry{
		{
			ID:      "sys-visible",
			Type:    session.EntryTypeSystem,
			Role:    "system",
			Content: visible,
		},
		{
			ID:      "consumed-session_01M3ABCDEF:1:final",
			Type:    session.EntryTypeSystem,
			Role:    "system",
			Content: marker,
			AgentID: "jim",
		},
	}
	markerInTranscript := false
	for _, entry := range entries {
		if entry.Content == marker {
			markerInTranscript = true
		}
	}
	if !markerInTranscript {
		t.Fatal("fixture has no consumed marker — an empty replay would pass for the wrong reason")
	}

	frames, _ := runReplay(t, entries)

	sawVisible := false
	for _, f := range frames {
		if f.Type == "replay_message" && f.Content == visible {
			sawVisible = true
		}
		if frameCarriesConsumedMarker(f, marker) {
			t.Fatalf("replay emitted the consumed marker on a %s frame", f.Type)
		}
	}
	if !sawVisible {
		t.Fatal("ordinary system line was not replayed — the marker check cannot tell a filter from a dropped stream")
	}
}

func frameCarriesConsumedMarker(f replayFrameDecoder, marker string) bool {
	if strings.Contains(f.Content, marker) || strings.Contains(f.Message, marker) || strings.Contains(f.Error, marker) {
		return true
	}
	text, ok := f.Result.(string)
	return ok && strings.Contains(text, marker)
}
