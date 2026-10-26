package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// ADR-091's historical replay compatibility drops a legacy nested child step
// from its parent's thread. Paging D4 must not turn a page boundary into proof
// that the parent never existed. REST keeps the recorded parent correlation.
func TestHistoryReplay_NestedLegacyCallKeepsParentAcrossPageCut(t *testing.T) {
	for _, tc := range []struct {
		name, parentTool string
		gap              int
		wantNested       int
	}{
		{name: "captured_shape", parentTool: "spawn"},
		{name: "parent_before_page", parentTool: "spawn", gap: 40},
		{name: "nondelegating_parent", parentTool: "echo", wantNested: 1},
		{name: "genuine_orphan", wantNested: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAttachFixture(t, "nested-"+tc.name, "nested-reply")
			now := time.Date(2026, 10, 25, 18, 33, 47, 0, time.UTC)
			if tc.parentTool != "" {
				require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
					ID: "entry-asst-spawn", Role: "assistant", Content: "Spawning a subagent.", AgentID: "mia", Timestamp: now,
					ToolCalls: []session.ToolCall{{ID: "c1", Tool: tc.parentTool, Status: "running", Parameters: map[string]any{"task": "nested-replay"}}},
				}))
			}
			// Forty intervening records put the parent outside the default
			// detail/snapshot window (40) and the explicit 20-row messages page.
			for i := 0; i < tc.gap; i++ {
				require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
					ID: fmt.Sprintf("gap-%02d", i), Role: "user", Content: "intervening message", AgentID: "mia", Timestamp: now,
				}))
			}
			require.NoError(t, f.store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
				ID: "entry-nested-1", Role: "assistant", Content: "", AgentID: "ray", Timestamp: now.Add(time.Second),
				ToolCalls: []session.ToolCall{{ID: "t2", Tool: "shell", Status: "success", DurationMS: 20,
					Parameters: map[string]any{"cmd": "echo nested"}, Result: map[string]any{"stdout": "nested\n"}, ParentToolCallID: "c1"}},
			}))
			api := pagingAPIForAttach(f)
			page := pagingRead(t, api, f.sid, "limit=20&min_rows=1")
			detail := pagingDetail(t, api, f.sid)
			require.Equal(t, "available", detail.HistoryState.Status)
			require.NotNil(t, detail.Messages)
			for _, entries := range [][]generated.Message{page.Entries, *detail.Messages} {
				entry := entries[len(entries)-1]
				require.Equal(t, "entry-nested-1", entry.Id)
				require.NotNil(t, entry.ToolCalls)
				require.Len(t, *entry.ToolCalls, 1)
				call := (*entry.ToolCalls)[0]
				require.Equal(t, "t2", call.Id)
				require.NotNil(t, call.ParentToolCallId, "REST/detail must keep the saved correlation, never flatten the child")
				require.Equal(t, "c1", *call.ParentToolCallId)
				if tc.gap > 0 {
					for _, e := range entries {
						require.NotEqual(t, "entry-asst-spawn", e.Id, "the actual selected page excludes the parent")
					}
				}
			}
			metrics := &session.ChatHistoryReadMetrics{}
			request, err := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{Limit: 20, MinRows: 1}, api.historyProjectionDeps(f.store, f.sid))
			require.NoError(t, err)
			_, err = request.Read(session.WithChatHistoryReadMetrics(context.Background(), metrics), f.store)
			require.NoError(t, err)
			wantParentReads := int64(0)
			if tc.parentTool != "" {
				wantParentReads = 1
			}
			require.Equal(t, wantParentReads, metrics.DependencyArchiveRecords, "one indexed parent read, not a walk through the intervening transcript")
			require.Equal(t, wantParentReads, metrics.DependencyCount)
			viewer := f.newConn("nested-snapshot-" + tc.name)
			f.h.handleAttachSession(context.Background(), "nested-snapshot-"+tc.name, f.sid, nil, viewer)
			frames := pagingDrain(t, viewer)
			require.Contains(t, typesOf(frames), "session_snapshot")
			require.Contains(t, typesOf(frames), "catch_up_complete")
			starts, results, sourceCarriers := 0, 0, 0
			for _, frame := range frames {
				switch frame.Type {
				case "tool_call_start":
					var start generated.ToolCallStartFrame
					require.NoError(t, json.Unmarshal(frame.raw, &start))
					if start.CallId == "t2" {
						starts++
						require.Nil(t, start.ParentCallId, "only genuine or nondelegating orphans take the flat compatibility path")
					}
				case "tool_call_result":
					var result generated.ToolCallResultFrame
					require.NoError(t, json.Unmarshal(frame.raw, &result))
					if result.CallId == "t2" {
						results++
					}
				case "replay_message":
					var message generated.ReplayMessageFrame
					require.NoError(t, json.Unmarshal(frame.raw, &message))
					if message.Id != nil && *message.Id == "entry-nested-1" {
						sourceCarriers++
						require.Equal(t, "", message.Content, "skipped legacy tool group contributes no invented narration")
						require.NotNil(t, message.HistoryOrigin)
						require.Equal(t, *page.Entries[len(page.Entries)-1].HistoryPosition, message.HistoryOrigin.Position)
					}
				}
			}
			require.Equal(t, tc.wantNested, starts, "a proven legacy nested child must not replay as an unrelated flat tool")
			require.Equal(t, tc.wantNested, results)
			if tc.wantNested == 0 {
				require.Equal(t, 1, sourceCarriers, "retain the selected source's empty origin carrier without its skipped tool")
			}
		})
	}
}
