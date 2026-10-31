package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Aggregate size, not one large string, must select the whole-result preview
// constructor. 64 * 1,024 value bytes already exceed A2.1's 51,200-byte bound;
// each key/value encoding is far below A2.8's stand-in threshold.
func historyR5ManySmallFields(t *testing.T) map[string]any {
	t.Helper()
	const fields, valueBytes = 64, 1024
	stored := make(map[string]any, fields)
	for i := 0; i < fields; i++ {
		stored[fmt.Sprintf("zz_%03d", i)] = strings.Repeat("x", valueBytes)
	}
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	// Two braces, 64 equal-sized key/colon/quoted-value members, 63 commas.
	require.Len(t, encoded, 2+fields*(len(`"zz_000":""`)+valueBytes)+(fields-1))
	require.Greater(t, len(encoded), 51200, "aggregate is genuinely oversized before the marker is added")
	historyR5AssertSmallResultStrings(t, stored)
	return stored
}

func historyR5AssertSmallResultStrings(t *testing.T, stored map[string]any) {
	t.Helper()
	for key, value := range stored {
		encoded, err := json.Marshal(key)
		require.NoError(t, err)
		require.LessOrEqual(t, len(encoded), 51200, "key %q cannot trigger a stand-in", key)
		if text, ok := value.(string); ok {
			encoded, err = json.Marshal(text)
			require.NoError(t, err)
			require.LessOrEqual(t, len(encoded), 51200, "value %q cannot trigger a stand-in", key)
		}
	}
}

// SEC-R5-3 / A2.2, D4 / CHECK F3: preserve saved reserved-key provenance on
// the many-small-fields whole-result route as well as the existing large-string
// stand-ins route. The independent CHECK owns the M5b mutation RED.
func TestHistoryPage_R5SavedManySmallFieldsMarkerIsPlaceholder(t *testing.T) {
	const canary = "R5_WHOLE_SAVED_MARKER_CANARY"
	stored := historyR5ManySmallFields(t)
	stored["_history_preview"] = true
	stored["original_size_bytes"] = 60000
	stored["preview"] = canary
	historyR5AssertSmallResultStrings(t, stored)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.Contains(t, string(encoded[:4096]), canary, "a wrongly minted prefix would expose the saved marker canary")
	f := historyR5ReadStoredResult(t, stored)
	t.Run("full_entry_control", func(t *testing.T) {
		historyR5AssertPlaceholder(t, f.full, f.position)
	})
	t.Run("rest_page_carrier", func(t *testing.T) {
		historyR5AssertPlaceholder(t, f.prepared.Response.Entries[0], f.position)
		assert.Equal(t, f.full, f.prepared.Response.Entries[0], "whole-result route must match the full-entry D4 placeholder exactly")
		assert.NotContains(t, string(f.prepared.encodedResponse), "_history_preview", "saved marker is not a legitimate reader preview")
		assert.NotContains(t, string(f.prepared.encodedResponse), canary)
	})
	t.Run("snapshot_carrier", func(t *testing.T) {
		messages, results := 0, 0
		for _, raw := range f.prepared.ReplayFrames {
			var frame generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			assert.NotContains(t, string(raw), "_history_preview", "no saved or minted marker on snapshot")
			assert.NotContains(t, string(raw), canary, "no saved marker body on snapshot")
			switch frame.Type {
			case "replay_message":
				messages++
				assert.Equal(t, "This saved message could not be displayed.", frame.Content)
				assert.Equal(t, "system", frame.Role)
				require.NotNil(t, frame.Id)
				assert.Equal(t, "r5-marker-owner", *frame.Id)
				require.NotNil(t, frame.HistoryOrigin)
				assert.Equal(t, f.position, frame.HistoryOrigin.Position)
			case "tool_call_result":
				results++
			}
		}
		assert.Equal(t, 1, messages, "one real D4 placeholder frame, not an empty-frame false green")
		assert.Equal(t, 0, results, "saved reserved key cannot become a successful tool-result preview")
	})
	t.Logf("whole_result saved_marker=true aggregate_bytes=%d fields=%d", len(encoded), len(stored))
}

// The same aggregate-sized small fields WITHOUT the reserved key must still
// produce the exact legitimate preview on both actual carriers. This proves
// the negative fixture is rejected for the marker, not aggregate field count.
func TestHistoryPage_R5ManySmallFieldsGenuinePreviewRemainsReadable(t *testing.T) {
	stored := historyR5ManySmallFields(t)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	// This fixture is ASCII throughout, so A2.1's full 4,096-byte cut is valid.
	want := map[string]any{"_history_preview": true, "original_size_bytes": float64(len(encoded)), "preview": string(encoded[:4096])}
	f := historyR5ReadStoredResult(t, stored)
	t.Run("full_entry_control", func(t *testing.T) {
		require.NotNil(t, f.full.ToolCalls)
		require.Len(t, *f.full.ToolCalls, 1)
		require.NotNil(t, (*f.full.ToolCalls)[0].Result)
		require.Equal(t, stored, *(*f.full.ToolCalls)[0].Result, "full-entry route keeps every small field whole")
	})
	t.Run("rest_page_carrier", func(t *testing.T) {
		m := f.prepared.Response.Entries[0]
		require.Equal(t, "r5-marker-owner", m.Id)
		require.NotNil(t, m.HistoryPosition)
		require.Equal(t, f.position, *m.HistoryPosition)
		require.NotNil(t, m.ToolCalls)
		require.Len(t, *m.ToolCalls, 1)
		require.NotNil(t, (*m.ToolCalls)[0].Result)
		require.Equal(t, want, *(*m.ToolCalls)[0].Result, "exact whole-result REST preview, bytes and original size")
	})
	t.Run("snapshot_carrier", func(t *testing.T) {
		results := 0
		for _, raw := range f.prepared.ReplayFrames {
			var frame generated.ToolCallResultFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame.Type == "tool_call_result" {
				results++
				require.Equal(t, "r5-marker-call", frame.CallId)
				require.Equal(t, "success", frame.Status)
				require.NotNil(t, frame.HistoryOrigin)
				require.Equal(t, f.position, frame.HistoryOrigin.Position)
				require.Equal(t, want, frame.Result, "snapshot preview equals the independently encoded REST preview")
			}
		}
		require.Equal(t, 1, results, "inspected one real history-origin result, not an empty frame set")
	})
	t.Logf("whole_result saved_marker=false aggregate_bytes=%d fields=%d", len(encoded), len(stored))
}
