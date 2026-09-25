// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"
	"time"

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

// TestReplay_Gen1StartWithoutEnd_FinishedGen2IsNotStillActive is the
// stop-then-revive hole. Revive can bump the generation before generation
// 1's end is written: completeSteeredTurn returns without delivering once
// the generation has changed (pkg/agent/steer_completion.go). The
// transcript then holds a generation-1 start, no generation-1 end, and a
// finished generation 2. The outer delegate call must not stay "still
// running" — its tool_call_result must be replayed.
func TestReplay_Gen1StartWithoutEnd_FinishedGen2IsNotStillActive(t *testing.T) {
	const callID = "call-1"
	const gen1Label = "gen1-open-sentinel"
	gen1Start := followUpStartEntry(callID+":start", "span_"+callID, callID)
	gen1Start.SubagentStart.TaskLabel = gen1Label
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 0, map[string]any{"task": "write the report"}, nil)),
		gen1Start,
		followUpStartEntry(callID+":g2:start", "span_"+callID+"_g2", callID),
		followUpEndEntry(callID+":g2:end", "span_"+callID+"_g2", callID, "success"),
	}
	frames, _ := runReplay(t, entries)

	var sawResult, sawGen2End, sawGen1Start bool
	for _, f := range frames {
		if f.Type == "tool_call_result" && f.CallID == callID {
			sawResult = true
		}
		if f.Type == "subagent_end" && f.SpanID == "span_"+callID+"_g2" && f.Status == "success" {
			sawGen2End = true
		}
		if f.Type == "subagent_start" && f.SpanID == "span_"+callID && f.TaskLabel == gen1Label {
			sawGen1Start = true
		}
	}
	if !sawGen1Start {
		t.Fatal("generation-1 start with label gen1-open-sentinel was not replayed; the fixture was not indexed")
	}
	if !sawGen2End {
		t.Fatal("generation 2's successful end was not replayed")
	}
	if !sawResult {
		t.Fatal("outer delegate call stayed stillActive after a finished later generation: tool_call_result was withheld")
	}
}

func runReplayWithLifecycle(t *testing.T, entries []session.TranscriptEntry, lifecycle lifecycleRecordLoader) []replayFrameDecoder {
	t.Helper()
	sink := &sliceSink{}
	rs := computeReplayStats(entries)
	_, err := streamReplay(context.Background(), "session_test", entries, rs, sink.emit, nil, nil, nil, lifecycle)
	if err != nil {
		t.Fatalf("streamReplay: %v", err)
	}
	return sink.all()
}

func persistChildLifecycle(t *testing.T, childID string, generation, stopGeneration int) *session.LifecycleStore {
	t.Helper()
	ls := session.NewLifecycleStore(t.TempDir())
	rec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     generation,
		State:          session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-1"},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "session_test", RootSessionID: "session_test"},
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   "session_test",
		ParentAgentID:  "mia",
		AgentID:        "worker",
		WorkspaceID:    "ws",
	}
	if stopGeneration > 0 {
		rec.Stop = &session.Stop{
			At:         time.Now().UTC(),
			Generation: stopGeneration,
			By:         session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"},
		}
	}
	if err := ls.Persist(rec); err != nil {
		t.Fatalf("persist lifecycle: %v", err)
	}
	return ls
}

func withChildSession(entry session.TranscriptEntry, childID string) session.TranscriptEntry {
	entry.SubagentStart.ChildSessionId = &childID
	return entry
}

func countEnds(frames []replayFrameDecoder, spanID string) (n int, status string) {
	for _, f := range frames {
		if f.Type == "subagent_end" && f.SpanID == spanID {
			n++
			status = f.Status
		}
	}
	return n, status
}

func sawFrame(frames []replayFrameDecoder, frameType, callID, spanID string) bool {
	for _, f := range frames {
		if f.Type != frameType {
			continue
		}
		if callID != "" && f.CallID != callID {
			continue
		}
		if spanID != "" && f.SpanID != spanID {
			continue
		}
		return true
	}
	return false
}

// gen1OpenGen2Done is a child whose generation 1 started and never wrote an
// end, and whose generation 2 started and finished success. The delegate
// tool call's own status is the placeholder acknowledgement ("success"),
// which replay must not copy onto generation 1.
func gen1OpenGen2Done(callID, childID string) []session.TranscriptEntry {
	gen1 := followUpStartEntry(callID+":start", "span_"+callID, callID)
	gen1.SubagentStart.TaskLabel = "gen1-open-sentinel"
	gen1 = withChildSession(gen1, childID)
	return []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 0, map[string]any{"task": "write the report"}, nil)),
		gen1,
		followUpStartEntry(callID+":g2:start", "span_"+callID+"_g2", callID),
		followUpEndEntry(callID+":g2:end", "span_"+callID+"_g2", callID, "success"),
	}
}

// TestReplay_Gen1Open_StopYieldsCancelledEnd: a recorded stop for generation
// 1, kept across revive, closes that open span as cancelled. The placeholder
// tool-call status is success, so a wrong copy is visible.
func TestReplay_Gen1Open_StopYieldsCancelledEnd(t *testing.T) {
	const callID = "call-1"
	const childID = "child-stopped-gen1"
	ls := persistChildLifecycle(t, childID, 2, 1)
	frames := runReplayWithLifecycle(t, gen1OpenGen2Done(callID, childID), ls)

	if !sawFrame(frames, "subagent_start", "", "span_"+callID) {
		t.Fatal("generation-1 start was not replayed; the fixture was not indexed")
	}
	n, status := countEnds(frames, "span_"+callID)
	if n != 1 || status != "cancelled" {
		t.Fatalf("generation-1 end count=%d status=%q, want exactly one cancelled (not the placeholder success)", n, status)
	}
	n2, status2 := countEnds(frames, "span_"+callID+"_g2")
	if n2 != 1 || status2 != "success" {
		t.Fatalf("generation-2 end count=%d status=%q, want exactly one success", n2, status2)
	}
	if !sawFrame(frames, "tool_call_result", callID, "") {
		t.Fatal("outer delegate call stayed still active after generation 2 finished: tool_call_result was withheld")
	}
}

// TestReplay_Gen1Open_NoStopYieldsNoEnd: the same transcript with no recorded
// stop for generation 1 (a crash, then a follow-up) must not invent an end.
// Generation 2's success end proves an end frame can be seen.
func TestReplay_Gen1Open_NoStopYieldsNoEnd(t *testing.T) {
	const callID = "call-1"
	const childID = "child-no-stop"
	ls := persistChildLifecycle(t, childID, 2, 0)
	frames := runReplayWithLifecycle(t, gen1OpenGen2Done(callID, childID), ls)

	if !sawFrame(frames, "subagent_start", "", "span_"+callID) {
		t.Fatal("generation-1 start was not replayed; the fixture was not indexed")
	}
	n, status := countEnds(frames, "span_"+callID)
	if n != 0 {
		t.Fatalf("generation-1 end count=%d status=%q, want no end at all (neither success nor cancelled)", n, status)
	}
	n2, status2 := countEnds(frames, "span_"+callID+"_g2")
	if n2 != 1 || status2 != "success" {
		t.Fatalf("generation-2 end count=%d status=%q, want exactly one success", n2, status2)
	}
	if !sawFrame(frames, "tool_call_result", callID, "") {
		t.Fatal("outer delegate call stayed still active after generation 2 finished: tool_call_result was withheld")
	}
}

// TestReplay_LatestOpenGenerationStaysActive: generation 3 has started and
// not finished. An earlier finished generation must not clear "still running".
func TestReplay_LatestOpenGenerationStaysActive(t *testing.T) {
	const callID = "call-1"
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 12, map[string]any{"task": "write the report"}, nil)),
		followUpStartEntry(callID+":start", "span_"+callID, callID),
		followUpEndEntry(callID+":end", "span_"+callID, callID, "success"),
		followUpStartEntry(callID+":g2:start", "span_"+callID+"_g2", callID),
		followUpEndEntry(callID+":g2:end", "span_"+callID+"_g2", callID, "success"),
		followUpStartEntry(callID+":g3:start", "span_"+callID+"_g3", callID),
	}
	frames, _ := runReplay(t, entries)

	if !sawFrame(frames, "subagent_start", "", "span_"+callID+"_g3") {
		t.Fatal("generation-3 start was not replayed; the fixture was not indexed")
	}
	n1, s1 := countEnds(frames, "span_"+callID)
	n2, s2 := countEnds(frames, "span_"+callID+"_g2")
	n3, s3 := countEnds(frames, "span_"+callID+"_g3")
	if n1 != 1 || s1 != "success" {
		t.Fatalf("generation-1 end count=%d status=%q, want the saved success", n1, s1)
	}
	if n2 != 1 || s2 != "success" {
		t.Fatalf("generation-2 end count=%d status=%q, want the saved success", n2, s2)
	}
	if n3 != 0 {
		t.Fatalf("generation-3 end count=%d status=%q, want no end while it is still open", n3, s3)
	}
	if sawFrame(frames, "tool_call_result", callID, "") {
		t.Fatal("outer delegate call was not still active: tool_call_result was emitted while generation 3 has no end")
	}
	if !sawFrame(frames, "tool_call_start", callID, "") {
		t.Fatal("tool_call_start missing; the withheld result is not evidence, the call itself was not replayed")
	}
}

// TestReplay_SavedGen1EndIsNotSynthesized: a generation that already saved
// its end keeps that end. The tool call's own status is error, so a
// synthesized end would not say success.
func TestReplay_SavedGen1EndIsNotSynthesized(t *testing.T) {
	const callID = "call-1"
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "error", 40, map[string]any{"task": "write the report"}, nil)),
		followUpStartEntry(callID+":start", "span_"+callID, callID),
		followUpEndEntry(callID+":end", "span_"+callID, callID, "success"),
	}
	frames, _ := runReplay(t, entries)
	n, status := countEnds(frames, "span_"+callID)
	if n != 1 || status != "success" {
		t.Fatalf("generation-1 end count=%d status=%q, want the one saved success, not a synthesized error", n, status)
	}
}

// TestReplay_LegacySpanUsesToolCallEnd: no persisted start at all. The
// closing frame still comes from the tool call, including its duration.
func TestReplay_LegacySpanUsesToolCallEnd(t *testing.T) {
	const callID = "call-1"
	entries := []session.TranscriptEntry{
		assistantEntry("delegating", "mia", toolCall(callID, "delegate", "success", 42, map[string]any{"task": "write the report"}, nil)),
	}
	frames, _ := runReplay(t, entries)
	var got []replayFrameDecoder
	for _, f := range frames {
		if f.Type == "subagent_end" && f.SpanID == "span_"+callID {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("legacy end count=%d, want exactly one tool-call-derived end", len(got))
	}
	if got[0].Status != "success" || got[0].DurationMs != 42 {
		t.Fatalf("legacy end status=%q duration=%d, want success and 42", got[0].Status, got[0].DurationMs)
	}
}
