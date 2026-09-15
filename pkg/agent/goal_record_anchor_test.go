// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_record_anchor_test.go — ADR-082 D9, review CR8: every goal record
// written by an ENGINE path that never ran the set_goal tool (marker-path
// activation, marker-path restate, the D7 keeper fallback compile) must be
// anchored in the session transcript as a `set_goal` tool call whose result
// is the same payload a real set_goal success returns — because under D9
// the record card renders ONLY from such a call's own result at the call's
// position. Each test drives the real production path end to end and then
// reads the transcript back: the LAST assistant entry must carry exactly one
// set_goal call whose result parses with the SAME goal_id the session meta
// holds, and the live tool_call start/end pair must have been emitted on
// the event bus keyed by the session id.
package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// anchoredSetGoalResult is the subset of set_goal's result payload these
// tests assert on — decoded from the transcript ToolCall's {"text": <json>}
// Result (the same persisted shape loop.go's tcRecord gives every plain-text
// tool result).
type anchoredSetGoalResult struct {
	Mode          string `json:"mode"`
	GoalID        string `json:"goal_id"`
	Definition    string `json:"definition"`
	CriteriaCount int    `json:"criteria_count"`
	DoDCount      int    `json:"dod_count"`
	Criteria      []struct {
		Text     string `json:"text"`
		Judgment string `json:"judgment"`
	} `json:"criteria"`
	DoD []struct {
		Text       string `json:"text"`
		Provenance string `json:"provenance"`
	} `json:"dod"`
	Assessment struct {
		Clarity     string   `json:"clarity"`
		Assumptions []string `json:"assumptions"`
	} `json:"assessment"`
}

// lastAssistantEntry returns the last transcript entry with Role "assistant"
// and fails the test when there is none.
func lastAssistantEntry(t *testing.T, store *session.UnifiedStore, sid string) session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "assistant" {
			return entries[i]
		}
	}
	t.Fatalf("no assistant entry in transcript (%d entries)", len(entries))
	return session.TranscriptEntry{}
}

// decodeAnchoredSetGoal asserts entry carries exactly one successful
// set_goal call with schema-shaped params and a parseable result, returning
// the decoded result.
func decodeAnchoredSetGoal(t *testing.T, entry session.TranscriptEntry) anchoredSetGoalResult {
	t.Helper()
	if len(entry.ToolCalls) != 1 {
		t.Fatalf("assistant entry carries %d tool calls, want exactly 1 (set_goal)", len(entry.ToolCalls))
	}
	tc := entry.ToolCalls[0]
	if tc.Tool != tools.SetGoalToolName {
		t.Fatalf("tool = %q, want %q", tc.Tool, tools.SetGoalToolName)
	}
	if tc.Status != "success" {
		t.Fatalf("status = %q, want success", tc.Status)
	}
	if tc.ID == "" {
		t.Fatal("anchored set_goal call has no tool-call id — replay dedups on id and would drop it")
	}
	for _, key := range []string{"mode", "definition", "criteria", "dod", "assessment"} {
		if _, ok := tc.Parameters[key]; !ok {
			t.Fatalf("params missing %q: %+v", key, tc.Parameters)
		}
	}
	if def, _ := tc.Parameters["definition"].(string); def == "" {
		t.Fatal("params.definition is empty — set_goal's own schema requires it")
	}
	text, _ := tc.Result["text"].(string)
	if text == "" {
		t.Fatalf("result must be persisted as {\"text\": <json>} like every plain-text tool result; got %+v", tc.Result)
	}
	var out anchoredSetGoalResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result text is not set_goal's JSON payload: %v\n%s", err, text)
	}
	return out
}

// toolExecEventsFor returns the (start, end) tool-exec payloads the
// collector saw for a set_goal call on sid.
func toolExecEventsFor(c *eventCollector, sid string) (starts []ToolExecStartPayload, ends []ToolExecEndPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		switch e.Kind {
		case EventKindToolExecStart:
			if p, ok := e.Payload.(ToolExecStartPayload); ok && p.SessionID == sid && p.Tool == tools.SetGoalToolName {
				starts = append(starts, p)
			}
		case EventKindToolExecEnd:
			if p, ok := e.Payload.(ToolExecEndPayload); ok && p.SessionID == sid && p.Tool == tools.SetGoalToolName {
				ends = append(ends, p)
			}
		}
	}
	return starts, ends
}

// waitForAnchorFrames polls the collector until one set_goal start AND one
// end frame for sid have landed (the event bus delivers asynchronously).
func waitForAnchorFrames(t *testing.T, c *eventCollector, sid string) (ToolExecStartPayload, ToolExecEndPayload) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		starts, ends := toolExecEventsFor(c, sid)
		if len(starts) > 0 && len(ends) > 0 {
			if len(starts) != 1 || len(ends) != 1 {
				t.Fatalf("want exactly one set_goal start/end pair, got %d starts / %d ends", len(starts), len(ends))
			}
			return starts[0], ends[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("CR8: the anchored set_goal call must emit a live tool_call start AND end frame keyed by the session id")
	return ToolExecStartPayload{}, ToolExecEndPayload{}
}

// TestGoalMarkerActivation_AnchorsSetGoalCallInTranscript — CR8(a): a
// marker-only `/goal [tests pass]` activation writes the record with no
// turn and no tool; the transcript must still end with an assistant entry
// carrying one set_goal(mode:register) call whose result names the SAME
// goal_id the meta minted, and the live frames must be emitted.
func TestGoalMarkerActivation_AnchorsSetGoalCallInTranscript(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	allowBashPolicy(agentInst) // [tests pass] compiles to a KindCheck criterion — see goal_record_wiring_test.go
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "chat-anchor-1", SessionKey: "sk1", UserInitiated: true,
	}
	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("marker activation: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	meta := goalRecordForSession(t, sid)
	if meta.GoalID == "" || goalRecordCompiledJSON(meta) == "" {
		t.Fatalf("precondition: activation must mint a goal id and a record; got id=%q record=%q",
			meta.GoalID, goalRecordCompiledJSON(meta))
	}

	entry := lastAssistantEntry(t, store, sid)
	if entry.Content == "" {
		t.Fatal("the anchor entry must carry a narration line (replay emits replay_message only for non-empty content)")
	}
	if entry.AgentID != agentInst.ID {
		t.Fatalf("anchor entry agent_id = %q, want %q", entry.AgentID, agentInst.ID)
	}
	res := decodeAnchoredSetGoal(t, entry)
	if res.Mode != tools.SetGoalModeRegister {
		t.Fatalf("mode = %q, want register", res.Mode)
	}
	if res.GoalID != meta.GoalID {
		t.Fatalf("result goal_id = %q, want the goal record's %q", res.GoalID, meta.GoalID)
	}
	if res.CriteriaCount == 0 || len(res.Criteria) != res.CriteriaCount {
		t.Fatalf("criteria: count=%d len=%d — the anchored result must carry the compiled ladder", res.CriteriaCount, len(res.Criteria))
	}
	if res.DoDCount == 0 || len(res.DoD) != res.DoDCount {
		t.Fatalf("dod: count=%d len=%d — the floor DoD must ride the anchored result", res.DoDCount, len(res.DoD))
	}
	if res.Definition == "" {
		t.Fatal("definition must fall back to the goal condition for a marker-shaped record")
	}
	if res.Assessment.Clarity != "clear" || len(res.Assessment.Assumptions) == 0 {
		t.Fatalf("assessment must say the engine authored this record: %+v", res.Assessment)
	}

	start, end := waitForAnchorFrames(t, collector, sid)
	if start.ToolCallID != entry.ToolCalls[0].ID || end.ToolCallID != entry.ToolCalls[0].ID {
		t.Fatalf("live frames must carry the transcript call's id %q; got start=%q end=%q",
			entry.ToolCalls[0].ID, start.ToolCallID, end.ToolCallID)
	}
	if start.ChatID != "chat-anchor-1" || start.AgentID != agentInst.ID {
		t.Fatalf("start frame routing: chat=%q agent=%q", start.ChatID, start.AgentID)
	}
	if end.IsError || end.Result == "" {
		t.Fatalf("end frame must carry the success result verbatim; got is_error=%v result=%q", end.IsError, end.Result)
	}
	var live anchoredSetGoalResult
	if err := json.Unmarshal([]byte(end.Result), &live); err != nil || live.GoalID != meta.GoalID {
		t.Fatalf("end frame result must parse to the same payload (goal_id %q): err=%v got=%q", meta.GoalID, err, end.Result)
	}
}

// TestGoalMarkerRestate_AnchorsSetGoalUpdateCallAtRestatePosition — CR8(b):
// a marker-only restate of an ACTIVE goal (applyGoalMarkerRestate) rewrites
// the record in place with no turn; the transcript must gain a SECOND
// assistant entry carrying one set_goal(mode:update) call for the amended
// record, under the SAME goal_id, positioned after the register anchor.
func TestGoalMarkerRestate_AnchorsSetGoalUpdateCallAtRestatePosition(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	// Both marker kinds used below must pass the compile-time feasibility
	// gate: [tests pass] needs bash reachable, [search: N] needs search_web
	// allowed. One StoreToolPolicy call (a second would overwrite the first).
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			"bash":       config.ToolPolicyAllow,
			"search_web": config.ToolPolicyAllow,
		},
	})
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "chat-anchor-2", SessionKey: "sk2", UserInitiated: true,
	}
	if matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [tests pass]", UserInitiated: true}, agentInst, &opts); !matched || handled {
		t.Fatalf("activation: matched=%v handled=%v", matched, handled)
	}
	before := goalRecordForSession(t, sid)
	registerEntry := lastAssistantEntry(t, store, sid)

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	// Marker-only restate on the now-active goal: deterministic, no LLM,
	// answered synchronously (handled=true) — no turn ever runs. Marker
	// ORDER matters here: parseIntentMarkers appends hits per regex
	// (search before tests) and exciseMarkers assumes position order, so
	// "[tests pass] [search: 2]" leaves the whole string as prose residue
	// and would route to the LLM-compile branch instead (a latent
	// goal_compile.go quirk, outside this test's subject).
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal [search: 2] [tests pass]", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("restate: matched=%v handled=%v reply=%q, want matched=true handled=true", matched, handled, reply)
	}
	after := goalRecordForSession(t, sid)
	if after.GoalID != before.GoalID {
		t.Fatalf("a restate must keep the goal id (FR-001): %q -> %q", before.GoalID, after.GoalID)
	}

	entry := lastAssistantEntry(t, store, sid)
	if entry.ID == registerEntry.ID {
		t.Fatal("the restate must append its OWN anchor entry, not reuse the register anchor")
	}
	if !entry.Timestamp.After(registerEntry.Timestamp) && entry.Timestamp != registerEntry.Timestamp {
		t.Fatalf("restate anchor must be positioned after the register anchor: %v vs %v", entry.Timestamp, registerEntry.Timestamp)
	}
	res := decodeAnchoredSetGoal(t, entry)
	if res.Mode != tools.SetGoalModeUpdate {
		t.Fatalf("mode = %q, want update", res.Mode)
	}
	if res.GoalID != after.GoalID {
		t.Fatalf("result goal_id = %q, want %q", res.GoalID, after.GoalID)
	}
	if res.CriteriaCount != 2 {
		t.Fatalf("the amended record carries the restated ladder: criteria_count = %d, want 2", res.CriteriaCount)
	}
	entries, _ := store.ReadTranscript(sid)
	anchors := 0
	for _, e := range entries {
		if e.Role == "assistant" && len(e.ToolCalls) == 1 && e.ToolCalls[0].Tool == tools.SetGoalToolName {
			anchors++
		}
	}
	if anchors != 2 {
		t.Fatalf("want two anchors (register + update, each at its own position), got %d", anchors)
	}
	waitForAnchorFrames(t, collector, sid)
}

// TestKeeperFallbackCompile_AnchorsSetGoalCallAndClosesWebBubble — CR8(c):
// after nudge exhaustion the keeper's engine-authored fallback compile
// registers a record with no turn and no tool. The transcript must end with
// an assistant entry carrying one set_goal(mode:register) call for it, the
// live frames must be emitted, and — because a web-routed goal gets no
// FR-020 channel echo and no turn follows to close the SPA bubble the live
// tool_call_start opens — exactly one narration outbound must be published
// to the webchat channel keyed by the session id.
func TestKeeperFallbackCompile_AnchorsSetGoalCallAndClosesWebBubble(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "chat-fallback-anchor", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "make the tests pass", 0, time.Now().Add(-1*time.Hour))
	// ADR-086: setGoalRoundsArmed's own record already carries a real, minted
	// goal id — the synthetic `SetMeta(GoalID: …)` stamp this test used to
	// need is gone with the field.
	judgeInst.Provider = unmetJudgeProvider("fallback compile reason")

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	al.goalQuietWindowSettle(time.Now()) // nudge 1
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // nudge 2
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now()) // exhausted — engine fallback compile

	after := goalRecordForSession(t, sid)
	if goalRecordCompiledJSON(after) == "" {
		t.Fatal("setup: the fallback compile must have registered a record")
	}

	entry := lastAssistantEntry(t, store, sid)
	res := decodeAnchoredSetGoal(t, entry)
	if res.Mode != tools.SetGoalModeRegister {
		t.Fatalf("mode = %q, want register", res.Mode)
	}
	if res.GoalID != after.GoalID {
		t.Fatalf("result goal_id = %q, want the goal record's %q", res.GoalID, after.GoalID)
	}
	if res.CriteriaCount == 0 {
		t.Fatal("the anchored result must carry the engine-compiled ladder")
	}
	if entry.AgentID != agentInst.ID {
		t.Fatalf("anchor agent_id = %q, want the goal-bearing agent %q", entry.AgentID, agentInst.ID)
	}

	start, _ := waitForAnchorFrames(t, collector, sid)
	if start.ChatID != "chat-fallback-anchor" {
		t.Fatalf("live frame chat id = %q, want the recorded route's", start.ChatID)
	}

	select {
	case msg := <-al.bus.OutboundChan():
		if msg.Channel != "webchat" || msg.SessionID != sid || msg.ChatID != "chat-fallback-anchor" {
			t.Fatalf("narration outbound routed to %s/%s session=%q, want webchat/chat-fallback-anchor session=%q",
				msg.Channel, msg.ChatID, msg.SessionID, sid)
		}
		if msg.Content != goalAnchorNarrationFallback {
			t.Fatalf("narration content = %q", msg.Content)
		}
		if msg.AgentID != "" {
			t.Fatalf("a system-originated send must leave AgentID empty (bus.OutboundMessage contract), got %q", msg.AgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a web-routed fallback registration must publish exactly one narration outbound to close the SPA bubble")
	}
	select {
	case msg := <-al.bus.OutboundChan():
		t.Fatalf("expected exactly ONE outbound, got a second: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestKeeperFallbackCompile_ChannelOrigin_NoWebNarration — the narration
// outbound is webchat-only: a channel-routed goal already gets its FR-020
// formatted echo from afterGoalRecordWrite, so the anchor must not add a
// second outbound there (the existing "exactly ONE echo" contract holds).
func TestKeeperFallbackCompile_ChannelOrigin_NoWebNarration(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "telegram", "chat-tg-anchor", "sk1", agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "make the tests pass", 0, time.Now().Add(-1*time.Hour))
	judgeInst.Provider = unmetJudgeProvider("fallback compile reason")

	al.goalQuietWindowSettle(time.Now())
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())
	rewindGoalActivity(t, al, store, sid)
	al.goalQuietWindowSettle(time.Now())

	entry := lastAssistantEntry(t, store, sid)
	decodeAnchoredSetGoal(t, entry)

	outbounds := 0
	deadline := time.After(1 * time.Second)
loop:
	for {
		select {
		case msg := <-al.bus.OutboundChan():
			outbounds++
			if msg.Channel != "telegram" {
				t.Fatalf("unexpected outbound to %q: %+v", msg.Channel, msg)
			}
		case <-deadline:
			break loop
		}
	}
	if outbounds != 1 {
		t.Fatalf("channel-origin fallback: want exactly one outbound (the FR-020 echo), got %d", outbounds)
	}
}

// TestGoalRecordAccess_ReadGoalState_IDAlwaysPresentOrEmptyTriple is the
// ADR-086 successor to the retired
// TestGoalRecordAccess_ReadGoalState_ActiveGoalWithoutID (F8).
//
// WHAT WAS RETIRED, AND WHY. F8 covered an active goal whose SESSION META
// carried a condition and a compiled record but NO goal id — the
// "pre-ADR-053 meta" legacy shape — and asserted ReadGoalState returned that
// condition/record with an empty id rather than failing or synthesizing one.
// That state is unrepresentable under ADR-086 and cannot be re-pointed:
//
//  1. The goal is its own entity (D1). There is no session-meta goal shape
//     left for a legacy row to be in — wave S6 deleted the fields.
//  2. A goal record ALWAYS has an id: Store.Create mints one when the caller
//     left it empty, Goal.Validate is the only gate before it, and
//     Goal.Activate refuses a record that is not in the defining phase. An
//     "active goal with no id" cannot be constructed through any public path
//     in pkg/goal.
//  3. Operator decision D-F (no migration, detection or rescue path anywhere
//     across ADR-084/085/086 — greenfield is assumed) forbids adding a
//     legacy-shape reader to keep the old scenario alive.
//
// The half of F8's contract that SURVIVES is "ReadGoalState must never mint
// an id, and must not fail". Both halves are asserted below, against the two
// states that DO exist now: an active goal (real id, real condition/record)
// and no goal at all (clean empty triple, nil error).
func TestGoalRecordAccess_ReadGoalState_IDAlwaysPresentOrEmptyTriple(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	access := agentLoopGoalRecordAccess{al: al}

	// No goal at all: a clean empty triple, never an error.
	goalID, gotCondition, gotRecord, err := access.ReadGoalState(sid)
	if err != nil {
		t.Fatalf("ReadGoalState (no goal): %v", err)
	}
	if goalID != "" || gotCondition != "" || gotRecord != "" {
		t.Fatalf("ReadGoalState (no goal) must return the empty triple, got id=%q condition=%q record=%q",
			goalID, gotCondition, gotRecord)
	}

	// An ACTIVE goal: a real id is always present — the F8 defect (an active
	// goal the SPA could not key an overlay to) is structurally impossible.
	condition := "goal with a real record"
	armGoalRecord(t, sid, condition, recordedGoalCriteria("do it"), 0, time.Now())

	goalID, gotCondition, gotRecord, err = access.ReadGoalState(sid)
	if err != nil {
		t.Fatalf("ReadGoalState (active goal): %v", err)
	}
	if goalID == "" {
		t.Fatal("an ACTIVE goal always carries a real id — ReadGoalState returned an empty one")
	}
	if gotCondition != condition {
		t.Fatalf("condition = %q, want %q", gotCondition, condition)
	}
	if gotRecord == "" {
		t.Fatal("a goal with a non-empty criteria ladder must return a non-empty record")
	}
	if want := goalRecordForSession(t, sid).GoalID; goalID != want {
		t.Fatalf("ReadGoalState must return the record's OWN id (never a synthesized one): got %q want %q", goalID, want)
	}
}
