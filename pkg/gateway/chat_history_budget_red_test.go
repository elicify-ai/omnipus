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

func TestChatHistoryPage_SoftBytesRespectTwoScreenFloor(t *testing.T) {
	cases := []struct {
		name                                           string
		groups, payloadBytes, limit, minRows, returned int
		stop, override                                 string
		exceeded                                       bool
	}{
		// Spec D-B1:20*200KiB=4096000 <4194304;21*200KiB=4300800 >4194304.
		{"budget_bites_only_after_floor", 100, 200 * 1024, 100, 20, 20, "byte_budget", "", false},
		// Spec D-B1b:20*256KiB=5242880, so floor necessarily exceeds soft budget.
		{"floor_beats_bytes", 40, 256 * 1024, 40, 20, 20, "byte_budget", "height_floor", true},
		{"normal_open_unchanged", 40, 7 * 1024, 40, 20, 40, "row_target", "", false},
		{"normal_prefetch_unchanged", 40, 7 * 1024, 20, 20, 20, "row_target", "", false},
		{"advisory_floor_equals_cap", 100, 48 * 1024, 100, 100, 100, "row_target", "height_floor", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sid := createTestSession(t, api)
			pagingSeedTools(t, api.agentLoop.GetSessionStore(), sid, tc.groups, tc.payloadBytes)
			p := pagingRead(t, api, sid, fmt.Sprintf("limit=%d&min_rows=%d", tc.limit, tc.minRows))
			require.Equal(t, pagingWantIDs("tool-owner", tc.groups-tc.returned+1, tc.groups), pagingIDs(p), "whole nearest groups, never truncating floor or returning all100 when bytes bite")
			require.Equal(t, tc.returned, p.PageInfo.RowCount, "one finished call=start/result logical row")
			require.Equal(t, tc.minRows, p.PageInfo.MinRows)
			require.EqualValues(t, 4194304, p.PageInfo.SoftByteBudget)
			require.GreaterOrEqual(t, p.PageInfo.ProjectedBytes, int64(tc.returned*tc.payloadBytes), "meter includes actual full tool output, not a guessed tiny REST weight")
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
				require.Len(t, *e.ToolCalls, 1)
				call := (*e.ToolCalls)[0]
				require.Equal(t, "success", string(call.Status))
				require.NotNil(t, call.Result)
				require.Equal(t, map[string]any{"text": strings.Repeat("x", tc.payloadBytes)}, *call.Result, "soft budget never truncates an admitted whole result")
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
		calls[i] = session.ToolCall{ID: session.ToolCallID(fmt.Sprintf("atomic-%03d", i)), Tool: "read_file", Status: "success", Parameters: map[string]any{}, Result: map[string]any{"text": "small"}}
	}
	calls[0].Result = map[string]any{"text": strings.Repeat("x", 4*1024*1024+1024)}
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

func TestChatHistoryPage_NearAdmission100FloorHasNoHardByteDefense(t *testing.T) {
	pagingNearAdmissionProbe(t)
}

func TestHistoryPageProjection_OnePreparedSideEffectFreeWeigher(t *testing.T) {
	for _, mode := range []string{"tool_heavy_rest_ws_divergence", "special_heavy_rest_ws_divergence", "nil_weigher", "error_weigher", "negative_weight", "overflow_weight", "inconsistent_dependency", "cancel_unselected_lookahead"} {
		t.Run(mode, func(t *testing.T) {
			pagingProjectionProbe(t, mode)
		})
	}
}
