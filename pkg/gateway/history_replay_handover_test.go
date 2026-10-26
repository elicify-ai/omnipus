package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// E5 (ui-browser browser-control-handover): a persisted
// browser_handover_notice entry must replay as the SAME frame type on a
// prepared snapshot attach (FR-043a: replay emits what the live path
// delivered), with the entry's own id and its selected history origin. The
// entry is written exactly as browser_ws.go's stand-down writer persists it.
func TestHistoryReplay_BrowserHandoverNoticeReplaysOnPreparedSnapshot(t *testing.T) {
	f := newAttachFixture(t, "e5-turn", "e5-msg")
	const noticeID = "handover-e5-notice"
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID:            noticeID,
		Type:          session.EntryTypeSystem,
		SystemSubtype: "browser_handover_notice",
		Role:          "system",
		Content:       "Operator has taken the wheel.",
		Timestamp:     time.Now().UTC(),
	}))
	viewer := f.newConn("e5-snapshot-view")
	f.h.handleAttachSession(context.Background(), "e5-snapshot-view", f.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	found := 0
	for _, frame := range frames {
		if frame.Type != "browser_handover_notice" {
			continue
		}
		found++
		var m generated.BrowserHandoverNoticeFrame
		require.NoError(t, json.Unmarshal(frame.raw, &m))
		require.Equal(t, f.sid, m.SessionId)
		require.Equal(t, noticeID, m.MessageId)
		require.NotNil(t, m.HistoryOrigin, "prepared replay stamps the selected source origin")
		require.NotEmpty(t, m.HistoryOrigin.Position)
	}
	require.Equal(t, 1, found, "reload must replay the persisted handover notice exactly once as the SAME frame type as live (FR-043a)")
}

// E6 / item 7 (steered-session-stop, subagent-span replay warnings): span
// bracket entries persisted in the parent transcript — exactly the shape the
// e2e seed and pkg/agent/steer_frames.go write — must replay their frames on
// a prepared snapshot attach, including when the SPA rehydrates the activity
// bar from them after a reload.
func TestHistoryReplay_SubagentSpanFramesReplayOnPreparedSnapshot(t *testing.T) {
	f := newAttachFixture(t, "e6-turn", "e6-msg")
	now := time.Now().UTC()
	spanID := "span_e6-call"
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "e6-call:start", Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentStart,
		ViewMembership: session.ViewMembershipChat, Timestamp: now, AgentID: "mia",
		SubagentStart: &generated.SubagentStartFrame{Type: "subagent_start", SessionId: f.sid,
			ChildSessionId: &[]string{"child-e6"}[0], SpanId: spanID, ParentCallId: "e6-call",
			TaskLabel: "e6-task", AgentId: &[]string{"mia"}[0]},
	}))
	require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "e6-call:state", Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentState,
		ViewMembership: session.ViewMembershipChat, Timestamp: now, AgentID: "mia",
		SubagentState: &generated.SubagentStateFrame{Type: "subagent_state", SessionId: f.sid,
			ChildSessionId: &[]string{"child-e6"}[0], SpanId: spanID, State: "running", CreatedAt: now.Format(time.RFC3339Nano)},
	}))
	viewer := f.newConn("e6-snapshot-view")
	f.h.handleAttachSession(context.Background(), "e6-snapshot-view", f.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	starts, states := 0, 0
	for _, frame := range frames {
		switch frame.Type {
		case "subagent_start":
			starts++
			var m generated.SubagentStartFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			require.Equal(t, spanID, m.SpanId)
			require.NotNil(t, m.HistoryOrigin, "prepared replay stamps the selected source origin")
		case "subagent_state":
			states++
		}
	}
	require.Equal(t, 1, starts, "the persisted subagent_start must replay exactly once on snapshot")
	require.Equal(t, 1, states, "the persisted subagent_state must replay exactly once on snapshot")
}

// E4 (ui-heavy multi-turn-render): after a reload, the server must supply the
// COMPLETE multi-turn + tool-call history — every assistant bubble row and
// every tool badge's start/result — on both the REST latest page and the
// prepared snapshot replay, with no corruption placeholder anywhere.
func TestHistoryReplay_MultiTurnToolHistoryServesCompletePage(t *testing.T) {
	f := newAttachFixture(t, "e4-turn", "e4-msg")
	now := time.Now().UTC()
	turn := func(n int) {
		require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
			ID: fmt.Sprintf("e4-u%d", n), Type: session.EntryTypeMessage, Role: "user",
			Content: fmt.Sprintf("question %d", n), Timestamp: now.Add(time.Duration(n) * time.Minute), AgentID: "mia",
		}))
		require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
			ID: fmt.Sprintf("e4-a%d-tool", n), Type: session.EntryTypeMessage, Role: "assistant",
			Timestamp: now.Add(time.Duration(n)*time.Minute + 10*time.Second), AgentID: "mia", TurnID: fmt.Sprintf("e4-turn-%d", n),
			ToolCalls: []session.ToolCall{{
				ID: session.ToolCallID(fmt.Sprintf("e4-call-%d", n)), Tool: "echo", Status: "success",
				Result: map[string]any{"ok": true},
			}},
			Content: fmt.Sprintf("working on %d", n),
		}))
		require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
			ID: fmt.Sprintf("e4-a%d", n), Type: session.EntryTypeMessage, Role: "assistant",
			Content: fmt.Sprintf("answer %d", n), Timestamp: now.Add(time.Duration(n)*time.Minute + 20*time.Second), AgentID: "mia",
		}))
	}
	turn(1)
	turn(2)

	// REST latest page: all six source rows survive as entries, none a placeholder.
	api := &restAPI{agentLoop: f.loop, homePath: t.TempDir()}
	response := pagingRequest(api, f.sid, "")
	require.Equal(t, http.StatusOK, response.Code)
	entries := historyMigrationEntryObjects(t, response.Body.Bytes(), "entries")
	ids := map[string]int{}
	placeholders := 0
	for _, entry := range entries {
		if id, ok := entry["id"].(string); ok {
			ids[id]++
		}
		if c, _ := entry["content"].(string); c == "This saved message could not be displayed." {
			placeholders++
		}
	}
	require.Equal(t, 6, len(ids), "every persisted row of both turns is served")
	for n := 1; n <= 2; n++ {
		require.Equal(t, 1, ids[fmt.Sprintf("e4-u%d", n)])
		require.Equal(t, 1, ids[fmt.Sprintf("e4-a%d", n)])
	}
	require.Zero(t, placeholders, "no completed row degrades to the corruption placeholder")

	// Prepared snapshot replay: message frames for every bubble row, tool
	// start+result for every badge.
	viewer := f.newConn("e4-snapshot-view")
	f.h.handleAttachSession(context.Background(), "e4-snapshot-view", f.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	messages, starts, results := 0, 0, 0
	for _, frame := range frames {
		switch frame.Type {
		case "replay_message":
			messages++
		case "tool_call_start":
			starts++
		case "tool_call_result":
			results++
		}
	}
	require.GreaterOrEqual(t, messages, 6, "every message row replays")
	require.Equal(t, 2, starts, "each turn's tool call replays its start")
	require.Equal(t, 2, results, "each turn's finished tool call replays its result")
}
