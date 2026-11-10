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

// D4/T12/T13 and architect addendum I. This isolates ownership from the existing
// pending-terminal defect tracked by squad as #1397. The original two-starts /
// one-terminal-result / no-pending-result test is preserved unchanged.
func TestSessionHistory_FinishedToolOnlyOriginIsIndependentOfPendingTruth(t *testing.T) {
	f := newAttachFixture(t, "finished-origin", "live-answer")
	pagingSeed(t, f.store, f.sid, "previous-prose", 1)
	owner, err := f.store.AppendTranscriptAddressed(f.sid, session.TranscriptEntry{
		ID: "finished-tool-only-owner", Type: session.EntryTypeToolCall, AgentID: "mia",
		Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		ToolCalls: []session.ToolCall{{ID: "finished-only-call", Tool: "read_file", Status: "success",
			Parameters: map[string]any{"path": "saved-path"}, Result: map[string]any{"text": "saved-result"}}},
	})
	require.NoError(t, err)
	pagingSeed(t, f.store, f.sid, "following-prose", 1)
	wc := f.newConn("finished-origin-view")
	f.h.handleAttachSession(context.Background(), "finished-origin-view", f.sid, nil, wc)
	frames := pagingDrain(t, wc)
	var starts []generated.ToolCallStartFrame
	var results []generated.ToolCallResultFrame
	for _, fr := range frames {
		switch fr.Type {
		case "tool_call_start":
			var start generated.ToolCallStartFrame
			require.NoError(t, json.Unmarshal(fr.raw, &start))
			starts = append(starts, start)
		case "tool_call_result":
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(fr.raw, &result))
			results = append(results, result)
		case "replay_message":
			var message generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(fr.raw, &message))
			if message.Id != nil {
				require.NotEqual(t, "finished-tool-only-owner", *message.Id, "ownership must not be manufactured as a dummy prose row")
			}
		}
	}
	require.Len(t, starts, 1, "positive control: real finished-only start is emitted")
	require.Len(t, results, 1, "positive control: real finished-only terminal result is emitted")
	require.Equal(t, "finished-only-call", starts[0].CallId)
	require.Equal(t, map[string]any{"path": "saved-path"}, starts[0].Params)
	require.Equal(t, "finished-only-call", results[0].CallId)
	require.Equal(t, "success", results[0].Status)
	require.Equal(t, map[string]any{"text": "saved-result"}, results[0].Result)
	wantPosition := pagingPositionOracle(f.sid, owner)
	for _, origin := range []*generated.HistoryEntryOrigin{starts[0].HistoryOrigin, results[0].HistoryOrigin} {
		require.NotNil(t, origin, "D4 finished tool-only start/result must carry the actual local history_origin, independently of pending-result truth")
		require.Equal(t, wantPosition, origin.Position, "checked source occurrence, not neighboring prose or live seq")
		require.NotNil(t, origin.EntryId)
		require.Equal(t, "finished-tool-only-owner", *origin.EntryId)
	}
}
