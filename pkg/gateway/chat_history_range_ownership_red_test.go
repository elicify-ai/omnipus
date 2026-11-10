package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T11/D-V1c: actual checked occurrence positions can be derived independently
// from addresses returned by existing canonical appends. No new parser API,
// message lookup or implementation-derived ordering oracle is necessary.
func TestGetSessionHistory_ResolvedRangeOrderingAndForeignPrecedence(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	s := api.agentLoop.GetSessionStore()
	addresses := pagingSeed(t, s, sid, "a", 61)
	pos := func(n int) string { return pagingPositionOracle(sid, addresses[n-1]) }
	other := createTestSession(t, api)
	foreign := pagingSeed(t, s, other, "foreign", 1)
	foreignPos := pagingPositionOracle(other, foreign[0])
	for _, tc := range []struct {
		name, query string
		code        int
		message     string
	}{
		{"reversed_known", "window_start=" + pos(40) + "&window_end=" + pos(21), 400, "invalid history page range"},
		{"before_below_range", "window_start=" + pos(21) + "&window_end=" + pos(40) + "&before=" + pos(20), 400, "invalid history page range"},
		{"before_above_range", "window_start=" + pos(21) + "&window_end=" + pos(40) + "&before=" + pos(41), 400, "invalid history page range"},
		{"foreign_before_order", "window_start=" + foreignPos + "&window_end=" + pos(21), 404, "history position not found"},
		{"foreign_before_inside_range", "window_start=" + pos(21) + "&window_end=" + pos(40) + "&before=" + foreignPos, 404, "history position not found"},
	} {
		t.Run(tc.name, func(t *testing.T) { pagingRefusal(t, pagingRequest(api, sid, tc.query), tc.code, tc.message) })
	}
}

// T12/T13 D4/D-P1b: every tool frame joins the actual tool-only source, not the
// adjacent narration; finished start/result is one owner, pending never invents
// a terminal result. Real shared attach/replay emitter, real store/hub.
func TestSessionHistory_ToolOnlyFrameOriginAndPendingTruth(t *testing.T) {
	f := newAttachFixture(t, "origin-tool-only", "live-answer")
	pagingSeed(t, f.store, f.sid, "previous-prose", 1)
	owner, err := f.store.AppendTranscriptAddressed(f.sid, session.TranscriptEntry{
		ID: "tool-only-owner", Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		ToolCalls: []session.ToolCall{
			{ID: "finished-call", Tool: "read_file", Status: "success", Parameters: map[string]any{"path": "saved-path"}, Result: map[string]any{"text": "saved-result"}},
			{ID: "pending-call", Tool: "read_file", Status: "pending", Parameters: map[string]any{"path": "pending-path"}},
		},
	})
	require.NoError(t, err)
	pagingSeed(t, f.store, f.sid, "following-prose", 1)
	wc := f.newConn("tool-origin-view")
	f.h.handleAttachSession(context.Background(), "tool-origin-view", f.sid, nil, wc)
	frames := pagingDrain(t, wc)
	starts := map[string]generated.ToolCallStartFrame{}
	results := map[string]generated.ToolCallResultFrame{}
	for _, fr := range frames {
		switch fr.Type {
		case "tool_call_start":
			var start generated.ToolCallStartFrame
			require.NoError(t, json.Unmarshal(fr.raw, &start))
			_, duplicate := starts[start.CallId]
			require.False(t, duplicate, "historical tool start emitted once")
			starts[start.CallId] = start
		case "tool_call_result":
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(fr.raw, &result))
			_, duplicate := results[result.CallId]
			require.False(t, duplicate, "historical result emitted once")
			results[result.CallId] = result
		case "done", "session_state", "catch_up_complete":
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(fr.raw, &raw))
			_, hasOrigin := raw["history_origin"]
			require.False(t, hasOrigin, "session/terminal completion is not a chronological source row")
		}
	}
	require.Len(t, starts, 2, "both actual tools retain starts")
	require.Len(t, results, 1, "pending call cannot acquire fake completion")
	require.Contains(t, results, "finished-call")
	require.NotContains(t, results, "pending-call")
	want := pagingPositionOracle(f.sid, owner)
	for _, id := range []string{"finished-call", "pending-call"} {
		start := starts[id]
		require.NotNil(t, start.HistoryOrigin, "source-local owner even without prose")
		require.Equal(t, want, start.HistoryOrigin.Position)
		require.NotNil(t, start.HistoryOrigin.EntryId)
		require.Equal(t, "tool-only-owner", *start.HistoryOrigin.EntryId)
	}
	result := results["finished-call"]
	require.Equal(t, "success", result.Status)
	require.Equal(t, map[string]any{"text": "saved-result"}, result.Result)
	require.NotNil(t, result.HistoryOrigin)
	require.Equal(t, want, result.HistoryOrigin.Position)
	require.Greater(t, indexOfType(frames, "catch_up_complete"), indexOfType(frames, "done"), "not replay-done attach completion")
}
