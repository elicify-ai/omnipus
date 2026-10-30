package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// SEC-3 backend; A2.2's 200 Message is NEVER a HistoryResultPreview. A
// persisted valid marker shape must not bypass the full-entry-only invariant.
func TestHistoryEntrySecurity_FullReadNeverReturnsPersistedPreviewMarker(t *testing.T) {
	for _, markerShaped := range []bool{false, true} {
		name := "ordinary_result_positive_control"
		if markerShaped {
			name = "valid_persisted_marker_is_not_a_full_result"
		}
		t.Run(name, func(t *testing.T) {
			api, sid, _ := d2EntryFixture(t)
			value := map[string]any{"text": "ordinary structured full output", "extra": true}
			if markerShaped {
				value = map[string]any{"_history_preview": true, "original_size_bytes": 51201, "preview": "SEC3_SAVED_MARKER_PRIVATE_CANARY"}
				// Instrument: this really is a valid general marker, NOT one of
				// T62b's already-rejected malformed-marker cases.
				raw, err := json.Marshal(value)
				require.NoError(t, err)
				message, serverError := validateBodyAgainstSchema("HistoryResultPreview", raw)
				require.False(t, serverError)
				require.Empty(t, message, "positive instrument: marker passes the generated marker schema")
			}
			address, err := api.agentLoop.GetSessionStore().AppendTranscriptAddressed(sid, session.TranscriptEntry{
				ID: "sec3-source", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", ViewMembership: session.ViewMembershipChat,
				ToolCalls: []session.ToolCall{{ID: "sec3-call", Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: value}},
			})
			require.NoError(t, err, "actual persisted result, not a forged response after projection")
			position := pagingPositionOracle(sid, address)
			response := d2EntryRequest(api, sid, position, "Bearer d2-route-test-token", nil)
			require.Equal(t, http.StatusOK, response.Code, "A2.2/D4 malformed-item replacement preserves a readable positioned occurrence")
			var message generated.Message
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &message))
			require.Equal(t, "sec3-source", message.Id)
			require.NotNil(t, message.HistoryPosition)
			require.Equal(t, position, *message.HistoryPosition)
			require.Nil(t, message.ContentOriginalSizeBytes)
			if !markerShaped {
				require.NotNil(t, message.ToolCalls)
				require.Len(t, *message.ToolCalls, 1)
				require.NotNil(t, (*message.ToolCalls)[0].Result)
				require.Equal(t, value, *(*message.ToolCalls)[0].Result, "ordinary non-marker result must not be removed to pass the negative case")
				return
			}
			if message.ToolCalls != nil {
				for _, call := range *message.ToolCalls {
					if call.Result != nil {
						require.NotContains(t, *call.Result, "_history_preview", "SEC-3/A2.2: full-entry route must never return a preview marker, EVEN a valid persisted marker shape")
					}
				}
			}
			require.NotContains(t, response.Body.String(), "SEC3_SAVED_MARKER_PRIVATE_CANARY", "invalid full-entry payload stays out of the public placeholder")
			require.NotNil(t, message.Content)
			require.Equal(t, "This saved message could not be displayed.", *message.Content, "the existing D4 malformed-item recovery remains visible")
		})
	}
}
