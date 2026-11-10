package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// D4: a saved call that is still "running" is non-terminal exactly like
// "pending" (ToolCall.yaml enum). Replay keeps its start and must not invent a
// result, which toolCallResultStatus would otherwise present as a terminal
// error. A finished sibling keeps its real result (positive control). Turning
// a crash-orphaned "running" into "interrupted" is crash recovery's job.
func TestSessionHistory_RunningCallReplaysAsInProgressStart(t *testing.T) {
	f := newAttachFixture(t, "running-truth", "live-answer")
	pagingSeed(t, f.store, f.sid, "previous-prose", 1)
	owner, err := f.store.AppendTranscriptAddressed(f.sid, session.TranscriptEntry{
		ID: "running-owner", Type: session.EntryTypeToolCall, AgentID: "mia", Timestamp: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		ToolCalls: []session.ToolCall{
			{ID: "finished-call", Tool: "read_file", Status: "success", Parameters: map[string]any{"path": "saved-path"}, Result: map[string]any{"text": "saved-result"}},
			{ID: "running-call", Tool: "read_file", Status: "running", Parameters: map[string]any{"path": "running-path"}},
		},
	})
	require.NoError(t, err)
	wc := f.newConn("running-truth-view")
	f.h.handleAttachSession(context.Background(), "running-truth-view", f.sid, nil, wc)
	starts := map[string]generated.ToolCallStartFrame{}
	results := map[string]generated.ToolCallResultFrame{}
	for _, fr := range pagingDrain(t, wc) {
		switch fr.Type {
		case "tool_call_start":
			var start generated.ToolCallStartFrame
			require.NoError(t, json.Unmarshal(fr.raw, &start))
			starts[start.CallId] = start
		case "tool_call_result":
			var result generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(fr.raw, &result))
			results[result.CallId] = result
		}
	}
	require.Contains(t, starts, "running-call", "the running call is shown as an in-progress start")
	require.Contains(t, starts, "finished-call")
	require.NotContains(t, results, "running-call", "a running call cannot acquire a fabricated (error) completion")
	require.Contains(t, results, "finished-call", "positive control: a finished sibling keeps its real result")
	require.Equal(t, "success", results["finished-call"].Status)
	require.NotNil(t, starts["running-call"].HistoryOrigin)
	require.Equal(t, pagingPositionOracle(f.sid, owner), starts["running-call"].HistoryOrigin.Position)
}
