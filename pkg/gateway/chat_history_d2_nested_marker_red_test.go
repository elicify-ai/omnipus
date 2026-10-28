package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T62b/A2.2: go through generated Message and an actual history-origin result
// frame emitted by the real prepared projector, not the marker schema alone.
func TestHistoryPreviewMarker_NestedValidation(t *testing.T) {
	cases := []struct {
		name  string
		value map[string]any
	}{
		{"missing_size", map[string]any{"_history_preview": true, "preview": "MARKER_PRIVATE_CANARY"}},
		{"wrong_discriminator_type", map[string]any{"_history_preview": "true", "original_size_bytes": 51201, "preview": "MARKER_PRIVATE_CANARY"}},
		{"extra_field", map[string]any{"_history_preview": true, "original_size_bytes": 51201, "preview": "MARKER_PRIVATE_CANARY", "extra": true}},
		// 2048 characters but6144 UTF8 bytes: maxLength alone cannot enforce it.
		{"multibyte_over4096_bytes", map[string]any{"_history_preview": true, "original_size_bytes": 51201, "preview": strings.Repeat("€", 2048)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readLogs := pagingLogCapture(t)
			before := len(readLogs())
			position := session.HistoryPosition("hp1_" + strings.Repeat("A", 43))
			e := session.ChatHistoryEntry{Position: position, Entry: session.TranscriptEntry{ID: "nested-owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), ToolCalls: []session.ToolCall{{ID: "nested-call", Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: tc.value}}}}
			request, err := newHistoryPageRequest("nested-marker-session", session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
			require.NoError(t, err)
			request.reset("fixture-cut")
			g, err := request.prepareGroup(context.Background(), session.ChatHistoryPage{SessionID: "nested-marker-session"}, e)
			require.NoError(t, err, "malformed item degrades locally, not a whole-page failure")
			require.NotNil(t, g.message.Content)
			require.Equal(t, "This saved message could not be displayed.", *g.message.Content, "invalid nested marker is NEVER a normal result")
			require.Equal(t, string(position), *g.message.HistoryPosition)
			for _, raw := range g.frames {
				var f map[string]any
				require.NoError(t, json.Unmarshal(raw, &f))
				require.NotContains(t, string(raw), "MARKER_PRIVATE_CANARY", "invalid result cannot escape on snapshot carrier")
				if f["type"] == "tool_call_result" {
					r, ok := f["result"].(map[string]any)
					if ok {
						require.NotContains(t, r, "_history_preview", "malformed marker is not sent as a valid preview")
					}
				}
			}
			rows := readLogs()
			require.Greater(t, len(rows), before, "A2.2 requires a sanitized malformed-item diagnostic")
			for _, row := range rows[before:] {
				encoded, e := json.Marshal(row)
				require.NoError(t, e)
				require.NotContains(t, string(encoded), "MARKER_PRIVATE_CANARY")
				require.NotContains(t, string(encoded), fmt.Sprint(tc.value))
			}
		})
	}
	t.Run("ordinary_non_marker_objects_remain_legal", func(t *testing.T) {
		s, sid, _ := pagingProjectionFixture(t)
		want := map[string]any{"text": "small ordinary value", "extra": true}
		d2AppendResult(t, s, sid, want)
		g := d2PrepareOnlyGroup(t, s, sid)
		require.Equal(t, want, d2Result(t, g))
		require.NotNil(t, g.message.ToolCalls)
	})
}
