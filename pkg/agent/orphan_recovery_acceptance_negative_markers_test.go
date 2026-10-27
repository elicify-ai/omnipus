//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACTestNegatives(t *testing.T) {
	t.Run("N1_unsupported_markers_authorize_no_exclusion", orphanACMarkerNegatives)
	t.Run("N2_user_and_assistant_boundaries_stop_backward_binding", orphanACBoundaryNegatives)
	t.Run("N3_invalid_structure_cannot_be_erased_by_markers", orphanACStructureNegatives)
}

// Full-list preservation is the oracle: no production scanner computes the
// expected view. The expected address property is the identity map for raw
// input.
//
// DESIGN 5 (and its 2026-10-26 addendum) changes the SHAPE of that view for a
// fixture that contains a recognised cancellation record: the declaration and
// its durable results are retained, the marker is excluded, and the occurrence
// the marker left without a result gains the engine observation at ordinal -1.
// A fixture whose records are NOT recognised cancellations keeps the identity
// map — an unsupported record authorises no exclusion at all.
func orphanACAssertUnsupported(t *testing.T, raw []providers.Message, wantErr string, design5 ...orphanACView5) {
	t.Helper()
	h := cwR1New(t, 100000)
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	ts := h.turn("")
	want := raw
	lines := make([]int, 0, 1+len(raw))
	lines = append(lines, -1)
	for i := range raw {
		lines = append(lines, i)
	}
	if len(design5) > 0 {
		want = design5[0].messages
		lines = append([]int{-1}, design5[0].addresses...)
	}
	out := orphanACAssertView(t, h, ts, want)
	require.Equal(t, lines, mapWindowMessages(before, out, -1, 0), "unsupported records cannot remove, reindex or de-identify any archived entry")
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
			prefix := make([]providers.Message, 0, 4)
			prefix = append(prefix,
				providers.Message{Role: "user", Content: "unsupported restart evidence"},
				orphanACGroup("incomplete parallel group", "done", "missing"),
				orphanACResult("done", "completed partial result must remain visible"),
			)
			if tc.role == "" {
				orphanACAssertUnsupported(t, prefix, "context request: incomplete or invalid tool-result group at message 2")
				return
			}
			bad := providers.Message{Role: tc.role, Content: tc.content}
			if tc.role == "tool" {
				// A role:"tool" record with no tool_call_id cannot be admitted at
				// all (ARCHITECT-ANSWER-FINAL-GATE.md item 4d): seed only the
				// prefix and put this record in memory for the send boundary.
				orphanACAssertUnsupportedSplit(t, prefix, bad, nil,
					"context request: incomplete or invalid tool-result group at message 2", true)
				return
			}
			if tc.name == "already_resolved_ID" {
				// DESIGN 5: this IS a recognised cancellation record — it has the
				// exact shape and names a call the group declares. It therefore
				// binds to that group and is EXCLUDED from the view, while the
				// declaration and its durable result are RETAINED. The cancelled
				// occurrence ("done") has a result, so it needs no observation;
				// the group's other call ("missing") was never cancelled, so it
				// keeps no invented result either. The group therefore stays
				// incomplete and the send boundary must still refuse it.
				raw := append(append([]providers.Message{}, prefix...), bad)
				orphanACAssertUnsupported(t, raw,
					"context request: incomplete or invalid tool-result group at message 2",
					orphanACView5{messages: []providers.Message{raw[0], raw[1], raw[2]}, addresses: []int{0, 1, 2}})
				return
			}
			orphanACAssertUnsupported(t, append(prefix, bad),
				"context request: incomplete or invalid tool-result group at message 2")
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
		t.Run(tc.name, func(t *testing.T) {
			switch tc.name {
			case "newer_complete_reused_ID_does_not_authorize_old_group":
				// DESIGN 5: the marker names a call the NEWER group declares, so
				// it binds there and is EXCLUDED. The newer group already has its
				// result, so no observation is needed; the older occurrence was
				// never cancelled, so it gets none either and the group stays
				// incomplete — the send boundary must still refuse it.
				orphanACAssertUnsupported(t, tc.raw, tc.err,
					orphanACView5{messages: []providers.Message{tc.raw[0], tc.raw[1], tc.raw[2], tc.raw[3]}, addresses: []int{0, 1, 2, 3}})
			default:
				orphanACAssertUnsupported(t, tc.raw, tc.err)
			}
		})
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
		// DESIGN 5: the marker binds to the newer group, so it is excluded and
		// that group's missing result gains the engine observation at -1; the
		// older, never-cancelled occurrence keeps no invented result.
		obs := orphanACObservation(t, h, ts, "boundary-id")
		out := orphanACAssertView(t, h, ts, []providers.Message{raw[0], raw[1], raw[2], obs, raw[4]})
		require.Equal(t, []int{-1, 0, 1, 2, -1, 4}, mapWindowMessages(before, out, -1, 0),
			"every retained record keeps its original address; the inserted observation is ephemeral")
		orphanACAssertRejectedBeforeSend(t, h, ts, out, "context request: incomplete or invalid tool-result group at message 2")
		orphanACAssertUnchanged(t, h, before, bytes)
	})
}
