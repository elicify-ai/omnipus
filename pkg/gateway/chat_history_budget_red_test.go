package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// D-B1/B1b/B2: one tool start/result pair is one drawn row; no prose wrapper
// doubles its weight. The ASCII result is independently sized, so JSON escaping
// cannot secretly turn a nominal payload into a different fixture class.
func pagingSeedTools(t *testing.T, s *session.UnifiedStore, sid string, n, payloadBytes int) {
	t.Helper()
	payload := strings.Repeat("x", payloadBytes)
	for i := 1; i <= n; i++ {
		require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{
			ID: fmt.Sprintf("tool-owner-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
			ToolCalls: []session.ToolCall{{ID: session.ToolCallID(fmt.Sprintf("call-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": payload}}},
		}))
	}
}

// D2 A2.7: construct post-shortening contributions from independently sized
// inline results, not a large single result which must now become a preview.
func pagingSeedInlineToolGroups(t *testing.T, s *session.UnifiedStore, sid string, groups, calls, payloadBytes int) {
	t.Helper()
	for i := 1; i <= groups; i++ {
		tools := make([]session.ToolCall, calls)
		for j := range tools {
			tools[j] = session.ToolCall{ID: session.ToolCallID(fmt.Sprintf("inline-%03d-%03d", i, j)), Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: map[string]any{"text": strings.Repeat("x", payloadBytes)}}
		}
		require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: fmt.Sprintf("tool-owner-%03d", i), Type: session.EntryTypeToolCall, AgentID: "mia", ToolCalls: tools}))
	}
}
func TestChatHistoryPage_SoftBytesRespectTwoScreenFloor(t *testing.T) {
	cases := []struct {
		name                                                  string
		groups, calls, payloadBytes, limit, minRows, returned int
		stop, override                                        string
		exceeded                                              bool
	}{
		// D2 A2.7 re-derivation:5 inline calls/group, each JSON exactly51200
		// bytes (payload51189 +11 JSON syntax).16 groups=80 rows=4096000
		// result bytes <4194304 with room for envelopes;17=85 rows=4352000.
		{"budget_bites_only_after_floor", 20, 5, 51189, 100, 80, 16, "byte_budget", "", false},
		{"floor_beats_bytes", 20, 5, 51189, 100, 85, 17, "byte_budget", "height_floor", true},
		{"normal_open_unchanged", 40, 1, 7 * 1024, 40, 20, 40, "row_target", "", false},
		{"normal_prefetch_unchanged", 40, 1, 7 * 1024, 20, 20, 20, "row_target", "", false},
		{"advisory_floor_equals_cap", 100, 1, 48 * 1024, 100, 100, 100, "row_target", "height_floor", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			pagingSeedInlineToolGroups(t, api.agentLoop.GetSessionStore(), sid, tc.groups, tc.calls, tc.payloadBytes)
			p := pagingRead(t, api, sid, fmt.Sprintf("limit=%d&min_rows=%d", tc.limit, tc.minRows))
			require.Equal(t, pagingWantIDs("tool-owner", tc.groups-tc.returned+1, tc.groups), pagingIDs(p), "whole nearest groups, never truncating floor or returning all100 when bytes bite")
			require.Equal(t, tc.returned*tc.calls, p.PageInfo.RowCount, "one finished call=start/result logical row; whole owning groups")
			require.Equal(t, tc.minRows, p.PageInfo.MinRows)
			require.EqualValues(t, 4194304, p.PageInfo.SoftByteBudget)
			require.GreaterOrEqual(t, p.PageInfo.ProjectedBytes, int64(tc.returned*tc.calls*(tc.payloadBytes+11)), "meter includes actual full tool output, not a guessed tiny REST weight")
			require.Equal(t, tc.exceeded, p.PageInfo.ByteBudgetExceeded)
			if tc.override == "" {
				require.Nil(t, p.PageInfo.BudgetOverride)
			} else {
				require.NotNil(t, p.PageInfo.BudgetOverride)
				require.Equal(t, tc.override, *p.PageInfo.BudgetOverride)
			}
			// D4a allows source_cap when the100-source and row-target boundaries
			// coincide. Neither may be mislabeled a byte cut of the capped chunk.
			if tc.limit == 100 && tc.returned == 100 {
				require.Contains(t, []string{"row_target", "source_cap"}, p.PageInfo.StopReason)
			} else if tc.name == "normal_open_unchanged" {
				require.Contains(t, []string{"end", "row_target"}, p.PageInfo.StopReason, "D-B2 pins no byte stop; D4a does not order coincident true-end/row-target stops")
			} else {
				require.Equal(t, tc.stop, p.PageInfo.StopReason)
			}
			if !tc.exceeded {
				require.LessOrEqual(t, p.PageInfo.ProjectedBytes, int64(4194304))
			}
			for _, e := range p.Entries {
				require.NotNil(t, e.ToolCalls)
				require.Len(t, *e.ToolCalls, tc.calls)
				for _, call := range *e.ToolCalls {
					require.Equal(t, "success", string(call.Status))
					require.NotNil(t, call.Result)
					require.Equal(t, map[string]any{"text": strings.Repeat("x", tc.payloadBytes)}, *call.Result, "post-shortening inline result stays byte-identical")
				}
			}
			pagingShape(t, p, tc.groups > tc.returned, false)
		})
	}
}

func TestChatHistoryPage_Atomic101RowsAndOversizedToolRemainWhole(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	pagingSeed(t, api.agentLoop.GetSessionStore(), sid, "old", 1)
	calls := make([]session.ToolCall, 100)
	for i := range calls {
		// A2.7:100 independently inline51200-byte results keep the atomic
		// owner over4MiB AFTER shortening (rather than one result that shrinks).
		calls[i] = session.ToolCall{ID: session.ToolCallID(fmt.Sprintf("atomic-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": strings.Repeat("x", 51189)}}
	}
	require.NoError(t, api.agentLoop.GetSessionStore().AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "atomic-owner", Role: "assistant", Content: "one narration row", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ToolCalls: calls}), "fixture is below canonical8-MiB record admission")
	p := pagingRead(t, api, sid, "limit=20&min_rows=20")
	require.Equal(t, []string{"atomic-owner"}, pagingIDs(p))
	require.Equal(t, 101, p.PageInfo.RowCount, "one narration+100 logical tools, not201 frames or a capped100")
	require.Equal(t, 1, p.PageInfo.SourceGroupCount)
	require.NotNil(t, p.Entries[0].ToolCalls)
	require.Len(t, *p.Entries[0].ToolCalls, 100)
	require.Greater(t, p.PageInfo.ProjectedBytes, int64(4194304))
	require.True(t, p.PageInfo.ByteBudgetExceeded)
	require.NotNil(t, p.PageInfo.BudgetOverride)
	require.Equal(t, "atomic_group", *p.PageInfo.BudgetOverride)
	pagingShape(t, p, true, false)
}

// T63/B75: founder-authorized D2 replacement of the old no-hard-defense rule.
func TestChatHistoryPage_NearAdmissionCorpusIsShortened(t *testing.T) {
	pagingNearAdmissionProbe(t)
}

func TestHistoryPageProjection_OnePreparedSideEffectFreeWeigher(t *testing.T) {
	for _, mode := range []string{"tool_heavy_rest_ws_divergence", "special_heavy_rest_ws_divergence", "nil_weigher", "error_weigher", "negative_weight", "overflow_weight", "inconsistent_dependency", "cancel_unselected_lookahead"} {
		t.Run(mode, func(t *testing.T) {
			pagingProjectionProbe(t, mode)
		})
	}
}
