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

// §3 of /workspace/logs/1534/arch-decision.md is the oracle, not the
// pre-change contract. These exercise the actual embedded inbound schema.
func Test1534Shown_CancelContractAcceptsCheckpointAndLegacy(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"old_cancel_remains_valid", `{"type":"cancel","session_id":"s"}`},
		{"checkpoint", `{"type":"cancel","session_id":"s","shown":{"message_id":"m","turn_id":"t","code_points":1}}`},
		{"empty_placeholder", `{"type":"cancel","session_id":"s","shown":{"code_points":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, serverErr := ValidateInboundFrameJSON("CancelFrame", []byte(tc.raw))
			assert.False(t, serverErr, "schema compiler must be available")
			assert.Empty(t, msg, "§3: the optional checkpoint is a legal cancel, not an additional-property error")
		})
	}
}

func Test1534Shown_CancelContractIDBoundaries(t *testing.T) {
	for _, field := range []string{"message_id", "turn_id"} {
		for _, size := range []int{0, 1, 2, 1023, 1024, 1025} {
			t.Run(fmt.Sprintf("%s_%d", field, size), func(t *testing.T) {
				raw, err := json.Marshal(map[string]any{
					"type": "cancel", "session_id": "s",
					"shown": map[string]any{field: strings.Repeat("x", size), "code_points": 0},
				})
				require.NoError(t, err)
				msg, serverErr := ValidateInboundFrameJSON("CancelFrame", raw)
				assert.False(t, serverErr)
				if size >= 1 && size <= 1024 {
					assert.Empty(t, msg, "§3: both IDs accept 1..1024 characters inclusive")
				} else {
					assert.NotEmpty(t, msg, "§3: reject empty/oversized ID")
					assert.Contains(t, msg, field, "must reject the ID constraint, not merely an unknown shown property")
				}
			})
		}
	}
}

func Test1534Shown_CancelContractRejectsMalformedCheckpoint(t *testing.T) {
	for _, tc := range []struct{ name, shown, field string }{
		{"negative", `{"code_points":-1}`, "code_points"},
		{"fractional", `{"code_points":1.5}`, "code_points"},
		{"wrong_type", `{"code_points":"1"}`, "code_points"},
		{"missing_count", `{}`, "code_points"},
		{"extra_property", `{"code_points":0,"text":"not permitted"}`, "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Positive control prevents rejection of the ENTIRE new property from
			// masquerading as enforcement of its nested value constraints.
			valid := []byte(`{"type":"cancel","session_id":"s","shown":{"code_points":0}}`)
			msg, serverErr := ValidateInboundFrameJSON("CancelFrame", valid)
			assert.False(t, serverErr)
			assert.Empty(t, msg, "instrument: a valid shown must be accepted")
			raw := []byte(`{"type":"cancel","session_id":"s","shown":` + tc.shown + `}`)
			msg, serverErr = ValidateInboundFrameJSON("CancelFrame", raw)
			assert.False(t, serverErr)
			assert.NotEmpty(t, msg)
			assert.Contains(t, msg, tc.field, "reject the malformed nested field for its own constraint")
		})
	}
}

func Test1534Shown_GeneratedCancelFrameRoundTripRetainsCheckpoint(t *testing.T) {
	raw := []byte(`{"type":"cancel","session_id":"s","shown":{"message_id":"m","turn_id":"t","code_points":13}}`)
	var frame generated.CancelFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	encoded, err := json.Marshal(frame)
	require.NoError(t, err)
	assert.JSONEq(t, string(raw), string(encoded), "§3: generated carrier must retain shown; silently dropping it recreates #1534")
}
