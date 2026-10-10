//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func orphanACTestNegatives(t *testing.T) {
	t.Run("N1_unsupported_markers_authorize_no_exclusion", orphanACMarkerNegatives)
	t.Run("N2_user_and_assistant_boundaries_stop_backward_binding", orphanACBoundaryNegatives)
	t.Run("N3_invalid_structure_cannot_be_erased_by_markers", orphanACStructureNegatives)
}

// Full-list preservation is the oracle: no production scanner computes the
// expected view. The expected address property is the identity map for raw input.
func orphanACAssertUnsupported(t *testing.T, raw []providers.Message, wantErr string) {
	t.Helper()
	h := cwR1New(t, 100000)
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	ts := h.turn("")
	out := orphanACAssertView(t, h, ts, raw)
	lines := make([]int, 0, 1+len(raw))
	lines = append(lines, -1)
	for i := range raw {
		lines = append(lines, i)
	}
	require.Equal(t, lines, mapWindowMessages(session.WindowViewFromSnapshot(before), out, -1, 0), "unsupported records cannot remove, reindex or de-identify any archived entry")
	orphanACAssertRejectedBeforeSend(t, h, ts, out, wantErr)
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACMarkerNegatives(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		content string
	}{
		{"absent", "", ""},
		{"empty_content", "system", ""},
		{"truncated_JSON", "system", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"`},
		{"JSON_null", "system", `null`},
		{"JSON_array", "system", `[{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}]`},
		{"plain_substring", "system", `recovery note: {"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"nested_object", "system", `{"note":{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}}`},
		{"wrong_role_assistant", "assistant", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"wrong_role_user", "user", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"wrong_role_tool", "tool", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"wrong_type", "system", `{"type":"other_event","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"stale_two_l_spelling", "system", `{"type":"turn_cancelled_restart","tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"missing_type", "system", `{"tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"nonstring_type", "system", `{"type":true,"tool_call_id":"missing","reason":"ungraceful_shutdown_recovery"}`},
		{"missing_reason", "system", `{"type":"turn_canceled_restart","tool_call_id":"missing"}`},
		{"wrong_reason", "system", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"operator_note"}`},
		{"nonstring_reason", "system", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":42}`},
		{"graceful_reason_even_with_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":"missing","reason":"graceful_shutdown"}`},
		{"actual_graceful_record_without_ID", "system", `{"type":"turn_canceled_restart","reason":"graceful_shutdown"}`},
		{"missing_ID", "system", `{"type":"turn_canceled_restart","reason":"ungraceful_shutdown_recovery"}`},
		{"empty_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":"","reason":"ungraceful_shutdown_recovery"}`},
		{"numeric_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":123,"reason":"ungraceful_shutdown_recovery"}`},
		{"null_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":null,"reason":"ungraceful_shutdown_recovery"}`},
		{"undeclared_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":"unrelated","reason":"ungraceful_shutdown_recovery"}`},
		{"already_resolved_ID", "system", `{"type":"turn_canceled_restart","tool_call_id":"done","reason":"ungraceful_shutdown_recovery"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []providers.Message{
				{Role: "user", Content: "unsupported restart evidence"},
				orphanACGroup("incomplete parallel group", "done", "missing"),
				orphanACResult("done", "completed partial result must remain visible"),
			}
			if tc.role != "" {
				raw = append(raw, providers.Message{Role: tc.role, Content: tc.content})
			}
			orphanACAssertUnsupported(t, raw, "context request: incomplete or invalid tool-result group at message 2")
		})
	}
}

func orphanACBoundaryNegatives(t *testing.T) {
	cases := []struct {
		name string
		raw  []providers.Message
		err  string
	}{
		{"marker_before_declaration", []providers.Message{
			{Role: "user", Content: "before marker"}, orphanACMarker("boundary-id"),
			orphanACGroup("future group cannot bind backward", "boundary-id"),
		}, "context request: incomplete or invalid tool-result group at message 3"},
		{"newer_user_ends_segment", []providers.Message{
			{Role: "user", Content: "old turn"}, orphanACGroup("old incomplete group", "boundary-id"),
			{Role: "user", Content: "newer user boundary"}, orphanACMarker("boundary-id"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"newer_plain_assistant_ends_segment", []providers.Message{
			{Role: "user", Content: "old turn"}, orphanACGroup("old incomplete group", "boundary-id"),
			{Role: "assistant", Content: "newer narration boundary"}, orphanACMarker("boundary-id"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"newer_different_ID_group_prevents_backward_search", []providers.Message{
			{Role: "user", Content: "old turn"}, orphanACGroup("old incomplete group", "boundary-id"),
			orphanACGroup("newer declaration of different ID", "newer-id"), orphanACMarker("boundary-id"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
		{"newer_complete_reused_ID_does_not_authorize_old_group", []providers.Message{
			{Role: "user", Content: "old turn"}, orphanACGroup("old incomplete occurrence", "boundary-id"),
			orphanACGroup("newer complete reused occurrence", "boundary-id"),
			orphanACResult("boundary-id", "newer occurrence already resolved"), orphanACMarker("boundary-id"),
		}, "context request: incomplete or invalid tool-result group at message 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { orphanACAssertUnsupported(t, tc.raw, tc.err) })
	}
	t.Run("newer_matching_group_cancellation_does_not_erase_older_invalidity", func(t *testing.T) {
		h := cwR1New(t, 100000)
		raw := []providers.Message{
			{Role: "user", Content: "old turn"}, orphanACGroup("older unmarked occurrence", "boundary-id"),
			orphanACGroup("newer legitimately canceled occurrence", "boundary-id"), orphanACMarker("boundary-id"),
			{Role: "system", Content: "unrelated surviving control"},
		}
		h.append(t, raw...)
		before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
		ts := h.turn("")
		out := orphanACAssertView(t, h, ts, orphanACPick(raw, 0, 1, 4))
		require.Equal(t, []int{-1, 0, 1, 4}, mapWindowMessages(session.WindowViewFromSnapshot(before), out, -1, 0))
		orphanACAssertRejectedBeforeSend(t, h, ts, out, "context request: incomplete or invalid tool-result group at message 2")
		orphanACAssertUnchanged(t, h, before, bytes)
	})
}
