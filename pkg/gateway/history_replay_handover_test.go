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
	origin := historyReplayAppendOrigin(t, f, session.TranscriptEntry{
		ID:            noticeID,
		Type:          session.EntryTypeSystem,
		SystemSubtype: "browser_handover_notice",
		Role:          "system",
		Content:       "Operator has taken the wheel.",
		Timestamp:     time.Now().UTC(),
	})
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
		require.Equal(t, "Operator has taken the wheel.", m.Text, "reload preserves the saved waiting-line text, not just its frame type")
		require.NotNil(t, m.HistoryOrigin, "prepared replay stamps the selected source origin")
		require.NotEmpty(t, m.HistoryOrigin.Position)
		require.Equal(t, &origin, m.HistoryOrigin, "notice belongs to its actual canonical occurrence")
	}
	require.Equal(t, 1, found, "reload must replay the persisted handover notice exactly once as the SAME frame type as live (FR-043a)")
}

// E6 / item 7 (steered-session-stop, subagent-span replay warnings): span
// bracket entries persisted in the parent transcript — exactly the shape the
// e2e seed and pkg/agent/steer_frames.go write — must replay their frames on
// a prepared snapshot attach, including when the SPA rehydrates the activity
// bar from them after a reload. This pins saved running -> stopped replay,
// not the engine's Stop command or persistence path.
func TestHistoryReplay_SubagentSpanFramesReplayOnPreparedSnapshot(t *testing.T) {
	f := newAttachFixture(t, "e6-turn", "e6-msg")
	now := time.Now().UTC()
	spanID := "span_e6-call"
	childID, agentID := "child-e6", "mia"
	startOrigin := historyReplayAppendOrigin(t, f, session.TranscriptEntry{
		ID: "e6-call:start", Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentStart,
		ViewMembership: session.ViewMembershipChat, Timestamp: now, AgentID: "mia",
		SubagentStart: &generated.SubagentStartFrame{Type: "subagent_start", SessionId: f.sid,
			ChildSessionId: &childID, SpanId: spanID, ParentCallId: "e6-call",
			TaskLabel: "e6-task", AgentId: &agentID},
	})
	runningOrigin := historyReplayAppendOrigin(t, f, session.TranscriptEntry{
		ID: "e6-call:state", Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentState,
		ViewMembership: session.ViewMembershipChat, Timestamp: now, AgentID: "mia",
		SubagentState: &generated.SubagentStateFrame{Type: "subagent_state", SessionId: f.sid,
			ChildSessionId: &childID, SpanId: spanID, State: "running", CreatedAt: now.Format(time.RFC3339Nano)},
	})
	stoppedAt := now.Add(time.Second)
	stoppedOrigin := historyReplayAppendOrigin(t, f, session.TranscriptEntry{
		ID: "e6-call:stopped", Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentState,
		ViewMembership: session.ViewMembershipChat, Timestamp: stoppedAt, AgentID: "mia",
		SubagentState: &generated.SubagentStateFrame{Type: "subagent_state", SessionId: f.sid,
			ChildSessionId: &childID, SpanId: spanID, State: "stopped", CreatedAt: stoppedAt.Format(time.RFC3339Nano)},
	})
	viewer := f.newConn("e6-snapshot-view")
	f.h.handleAttachSession(context.Background(), "e6-snapshot-view", f.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	starts, states := 0, 0
	stoppedStates := 0
	var replayedStates []string
	for _, frame := range frames {
		switch frame.Type {
		case "subagent_start":
			starts++
			var m generated.SubagentStartFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			require.Equal(t, spanID, m.SpanId)
			require.NotNil(t, m.HistoryOrigin, "prepared replay stamps the selected source origin")
			require.Equal(t, f.sid, m.SessionId)
			require.Equal(t, &childID, m.ChildSessionId)
			require.Equal(t, "e6-call", m.ParentCallId)
			require.Equal(t, "e6-task", m.TaskLabel)
			require.Equal(t, &agentID, m.AgentId)
			require.Equal(t, &startOrigin, m.HistoryOrigin)
		case "subagent_state":
			var m generated.SubagentStateFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			require.Equal(t, f.sid, m.SessionId)
			require.Equal(t, &childID, m.ChildSessionId)
			require.Equal(t, spanID, m.SpanId)
			replayedStates = append(replayedStates, m.State)
			switch m.State {
			case "running":
				states++ // Keep the original one-running-state assertion below.
				require.Equal(t, now.Format(time.RFC3339Nano), m.CreatedAt)
				require.Equal(t, &runningOrigin, m.HistoryOrigin)
			case "stopped":
				stoppedStates++
				require.Equal(t, stoppedAt.Format(time.RFC3339Nano), m.CreatedAt)
				require.Equal(t, &stoppedOrigin, m.HistoryOrigin)
			default:
				t.Fatalf("unexpected saved child state %q", m.State)
			}
		}
	}
	require.Equal(t, 1, starts, "the persisted subagent_start must replay exactly once on snapshot")
	require.Equal(t, 1, states, "the persisted subagent_state must replay exactly once on snapshot")
	require.Equal(t, 1, stoppedStates, "the saved stopped state must survive a fresh snapshot attach")
	require.Equal(t, []string{"running", "stopped"}, replayedStates, "reload finishes with the actual saved stopped state, not a rewritten running state")
}

// E4 (ui-heavy multi-turn-render): after a reload, the server must supply the
// COMPLETE multi-turn + tool-call history — every assistant bubble row and
// every tool badge's start/result — on both the REST latest page and the
// prepared snapshot replay, with no corruption placeholder anywhere.
func TestHistoryReplay_MultiTurnToolHistoryServesCompletePage(t *testing.T) {
	f := newAttachFixture(t, "e4-turn", "e4-msg")
	now := time.Now().UTC()
	origins := map[string]generated.HistoryEntryOrigin{}
	turn := func(n int) {
		userID, toolID, answerID := fmt.Sprintf("e4-u%d", n), fmt.Sprintf("e4-a%d-tool", n), fmt.Sprintf("e4-a%d", n)
		origins[userID] = historyReplayAppendOrigin(t, f, session.TranscriptEntry{
			ID: userID, Type: session.EntryTypeMessage, Role: "user",
			Content: fmt.Sprintf("question %d", n), Timestamp: now.Add(time.Duration(n) * time.Minute), AgentID: "mia",
		})
		origins[toolID] = historyReplayAppendOrigin(t, f, session.TranscriptEntry{
			ID: toolID, Type: session.EntryTypeMessage, Role: "assistant",
			Timestamp: now.Add(time.Duration(n)*time.Minute + 10*time.Second), AgentID: "mia", TurnID: fmt.Sprintf("e4-turn-%d", n),
			ToolCalls: []session.ToolCall{{
				ID: session.ToolCallID(fmt.Sprintf("e4-call-%d", n)), Tool: "echo", Status: "success",
				Parameters: map[string]any{"value": fmt.Sprintf("input %d", n)},
				Result:     map[string]any{"ok": true, "text": fmt.Sprintf("saved result %d", n)},
			}},
			Content: fmt.Sprintf("working on %d", n),
		})
		origins[answerID] = historyReplayAppendOrigin(t, f, session.TranscriptEntry{
			ID: answerID, Type: session.EntryTypeMessage, Role: "assistant",
			Content: fmt.Sprintf("answer %d", n), Timestamp: now.Add(time.Duration(n)*time.Minute + 20*time.Second), AgentID: "mia",
		})
	}
	turn(1)
	turn(2)
	wantIDs := []string{"e4-u1", "e4-a1-tool", "e4-a1", "e4-u2", "e4-a2-tool", "e4-a2"}
	wantText := map[string]string{
		"e4-u1": "question 1", "e4-a1-tool": "working on 1", "e4-a1": "answer 1",
		"e4-u2": "question 2", "e4-a2-tool": "working on 2", "e4-a2": "answer 2",
	}
	wantCalls := []string{"e4-call-1", "e4-call-2"}
	callOwners := map[string]string{"e4-call-1": "e4-a1-tool", "e4-call-2": "e4-a2-tool"}
	entryCalls := map[string]string{"e4-a1-tool": "e4-call-1", "e4-a2-tool": "e4-call-2"}
	wantParams := map[string]map[string]any{
		"e4-call-1": {"value": "input 1"}, "e4-call-2": {"value": "input 2"},
	}
	wantResults := map[string]map[string]any{
		"e4-call-1": {"ok": true, "text": "saved result 1"}, "e4-call-2": {"ok": true, "text": "saved result 2"},
	}

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
	var page generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Equal(t, wantIDs, pagingIDs(page), "both turns keep exact ascending source identity/order")
	for _, entry := range page.Entries {
		require.NotNil(t, entry.Content)
		require.Equal(t, wantText[entry.Id], *entry.Content, "REST saved message %s", entry.Id)
		origin := origins[entry.Id]
		require.Equal(t, &origin.Position, entry.HistoryPosition)
		if expectedCall := entryCalls[entry.Id]; expectedCall != "" {
			require.NotNil(t, entry.ToolCalls)
			require.Len(t, *entry.ToolCalls, 1)
			call := (*entry.ToolCalls)[0]
			require.Equal(t, expectedCall, call.Id)
			require.Equal(t, entry.Id, callOwners[call.Id])
			require.Equal(t, "echo", call.Tool)
			require.Equal(t, "success", string(call.Status))
			require.NotNil(t, call.Parameters)
			require.Equal(t, wantParams[expectedCall], *call.Parameters)
			require.NotNil(t, call.Result)
			require.Equal(t, wantResults[expectedCall], *call.Result)
		} else {
			require.Nil(t, entry.ToolCalls, "a plain message cannot acquire its neighbour's call")
		}
	}

	// Prepared snapshot replay: decode the actual generated payloads, not only
	// frame counts. Each tool result belongs to its own saved source occurrence.
	viewer := f.newConn("e4-snapshot-view")
	f.h.handleAttachSession(context.Background(), "e4-snapshot-view", f.sid, nil, viewer)
	frames := pagingDrain(t, viewer)
	messages, starts, results := 0, 0, 0
	snapshotPlaceholders := 0
	var messageIDs, startIDs, resultIDs []string
	for _, frame := range frames {
		switch frame.Type {
		case "replay_message":
			messages++
			var m generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			require.NotNil(t, m.Id)
			messageIDs = append(messageIDs, *m.Id)
			if m.Content == "This saved message could not be displayed." {
				snapshotPlaceholders++
			}
			require.Contains(t, wantText, *m.Id, "no invented/adjacent source row")
			require.Equal(t, wantText[*m.Id], m.Content, "snapshot saved message %s", *m.Id)
			require.Equal(t, f.sid, m.SessionId)
			origin := origins[*m.Id]
			require.Equal(t, &origin, m.HistoryOrigin)
		case "tool_call_start":
			starts++
			var m generated.ToolCallStartFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			startIDs = append(startIDs, m.CallId)
			require.Contains(t, callOwners, m.CallId)
			require.Equal(t, f.sid, m.SessionId)
			require.Equal(t, "echo", m.Tool)
			require.Equal(t, wantParams[m.CallId], m.Params)
			origin := origins[callOwners[m.CallId]]
			require.Equal(t, &origin, m.HistoryOrigin)
		case "tool_call_result":
			results++
			var m generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(frame.raw, &m))
			resultIDs = append(resultIDs, m.CallId)
			require.Contains(t, callOwners, m.CallId)
			require.Equal(t, f.sid, m.SessionId)
			require.Equal(t, "echo", m.Tool)
			require.Equal(t, "success", m.Status)
			require.Equal(t, wantResults[m.CallId], m.Result)
			origin := origins[callOwners[m.CallId]]
			require.Equal(t, &origin, m.HistoryOrigin)
		}
	}
	require.GreaterOrEqual(t, messages, 6, "every message row replays")
	require.Equal(t, 2, starts, "each turn's tool call replays its start")
	require.Equal(t, 2, results, "each turn's finished tool call replays its result")
	require.Zero(t, snapshotPlaceholders, "valid saved snapshot messages never degrade to placeholders")
	require.Equal(t, wantIDs, messageIDs)
	require.Equal(t, wantCalls, startIDs)
	require.Equal(t, wantCalls, resultIDs)
}

// The addressed writer has the same strict canonical append assertion as the
// original pins, and additionally supplies the independent D2 position oracle.
func historyReplayAppendOrigin(t *testing.T, f *attachFixture, entry session.TranscriptEntry) generated.HistoryEntryOrigin {
	t.Helper()
	address, err := f.store.AppendTranscriptAddressed(f.sid, entry)
	require.NoError(t, err)
	id := entry.ID
	return generated.HistoryEntryOrigin{Position: pagingPositionOracle(f.sid, address), EntryId: &id}
}
