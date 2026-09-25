// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func followUpStartEntry(entryID, storedSpanID, parentCallID string) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID:            entryID,
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentStart,
		SubagentStart: &generated.SubagentStartFrame{
			Type:         string(generated.WsFrameTypeSubagentStart),
			SessionId:    "session_test",
			SpanId:       storedSpanID,
			ParentCallId: parentCallID,
			TaskLabel:    "write the report",
		},
	}
}

func followUpEndEntry(entryID, storedSpanID, parentCallID, status string) session.TranscriptEntry {
	parent := parentCallID
	return session.TranscriptEntry{
		ID:            entryID,
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentEnd,
		SubagentEnd: &generated.SubagentEndFrame{
			Type:         string(generated.WsFrameTypeSubagentEnd),
			SessionId:    "session_test",
			SpanId:       storedSpanID,
			Status:       status,
			ParentCallId: &parent,
		},
	}
}

func spanIDsOfType(frames []replayFrameDecoder, frameType string) []string {
	out := make([]string, 0)
	for _, f := range frames {
		if f.Type == frameType {
			out = append(out, f.SpanID)
		}
	}
	return out
}

func countSpan(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}

// TestReplay_FollowUpGen2SpanMatchesLiveAndGen1StaysTerminal is D9 on a
// cold load. The stored generation-2 frames still carry the pre-fix bare
// span id; replay must rebuild span_<callID>_g2, and generation 1's own
// terminal end must still be present on the bare id.
func TestReplay_FollowUpGen2SpanMatchesLiveAndGen1StaysTerminal(t *testing.T) {
	const callID = "call-1"
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 0, map[string]any{"task": "write the report"}, nil)),
		followUpStartEntry(callID+":start", "span_"+callID, callID),
		followUpEndEntry(callID+":end", "span_"+callID, callID, "success"),
		// Stale bare span id, the bug a reload used to replay. The entry id
		// is what records that this bracket belongs to generation 2.
		followUpStartEntry(callID+":g2:start", "span_"+callID, callID),
		followUpEndEntry(callID+":g2:end", "span_"+callID, callID, "success"),
	}

	frames, _ := runReplay(t, entries)
	starts := spanIDsOfType(frames, "subagent_start")
	ends := spanIDsOfType(frames, "subagent_end")

	if countSpan(starts, "span_call-1_g2") != 1 {
		t.Fatalf("rebuilt generation-2 starts = %v, want exactly one span_call-1_g2", starts)
	}
	if countSpan(ends, "span_call-1_g2") != 1 {
		t.Fatalf("rebuilt generation-2 ends = %v, want exactly one span_call-1_g2", ends)
	}
	if countSpan(ends, "span_call-1") != 1 {
		t.Fatalf("generation-1 terminal ends = %v, want exactly one span_call-1", ends)
	}
	gen1EndStatus := ""
	for _, f := range frames {
		if f.Type == "subagent_end" && f.SpanID == "span_call-1" {
			gen1EndStatus = f.Status
		}
	}
	if gen1EndStatus != "success" {
		t.Fatalf("generation-1 end status = %q, want success", gen1EndStatus)
	}
}

// TestReplay_GenerationOneSpanStaysBare pins the bare id. A stored
// generation-1 span that a later tidy-up suffixed _g1 must reload as
// span_<callID>, or every span already on disk is orphaned.
func TestReplay_GenerationOneSpanStaysBare(t *testing.T) {
	entries := []session.TranscriptEntry{
		followUpStartEntry("call-1:start", "span_call-1_g1", "call-1"),
		followUpEndEntry("call-1:end", "span_call-1_g1", "call-1", "success"),
	}
	frames, _ := runReplay(t, entries)
	for _, f := range frames {
		if f.Type != "subagent_start" && f.Type != "subagent_end" {
			continue
		}
		if f.SpanID != "span_call-1" {
			t.Fatalf("%s span_id = %q, want the bare span_call-1 (generation 1 must not keep a _g1 suffix)", f.Type, f.SpanID)
		}
	}
}

// TestReplay_FinishedGen2FollowUpIsNotStillRunning is the still-active
// trap: a generation-2 start stored under the bare id and an end stored
// under the generation-2 id used to look like "started, never ended" for
// the original tool call, so a finished follow-up reloaded as still running.
func TestReplay_FinishedGen2FollowUpIsNotStillRunning(t *testing.T) {
	const callID = "call-1"
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 0, map[string]any{"task": "write the report"}, nil)),
		followUpStartEntry(callID+":g2:start", "span_"+callID, callID),
		followUpEndEntry(callID+":g2:end", "span_"+callID+"_g2", callID, "success"),
	}
	frames, _ := runReplay(t, entries)

	var sawResult, sawGen2End bool
	for _, f := range frames {
		if f.Type == "tool_call_result" && f.CallID == callID {
			sawResult = true
		}
		if f.Type == "subagent_end" && f.SpanID == "span_call-1_g2" && f.Status == "success" {
			sawGen2End = true
		}
	}
	if !sawResult {
		t.Fatal("finished generation-2 follow-up was treated as still running: the original delegate tool_call_result was withheld")
	}
	if !sawGen2End {
		t.Fatal("finished generation-2 follow-up has no terminal subagent_end on span_call-1_g2")
	}
}

// TestReplay_SteeringReceiptSpanIDRoundTrips confirms a receipt persisted
// with the generation-2 span id is replayed with that same id. Replay does
// not rebuild state frames; the live writer is what puts the id on the frame.
func TestReplay_SteeringReceiptSpanIDRoundTrips(t *testing.T) {
	entry := session.TranscriptEntry{
		ID:            "call-1:2:state:running:receipt:corr-1",
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentState,
		SubagentState: &generated.SubagentStateFrame{
			Type:      string(generated.WsFrameTypeSubagentState),
			SessionId: "session_test",
			SpanId:    "span_call-1_g2",
			State:     "running",
			CreatedAt: "2026-09-25T00:00:00Z",
		},
	}
	frames, _ := runReplay(t, []session.TranscriptEntry{entry})
	var got string
	for _, f := range frames {
		if f.Type == "subagent_state" {
			got = f.SpanID
		}
	}
	if got != "span_call-1_g2" {
		t.Fatalf("replayed receipt span_id = %q, want span_call-1_g2", got)
	}
}
