package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const catchupFixturePosition1 = "hp1_WHBj2oAOLcz6AgXBMkyMfxCrcjqmF5Du8mgUcXYBNag"
const catchupFixturePosition2 = "hp1_psGlCMLbE6Mp_w1MkKdpQHv5wWiwD0lFE8xVGxR5KX0"

// Unit inputs for the test instrument, not fabricated product execution. The
// independent golden migration map defines the expected placeholder values.
func TestCatchUpPagingNormalizer_PreservesIdentityAndOpaqueRelationships(t *testing.T) {
	p1, p2 := "hp1_"+strings.Repeat("A", 43), "hp1_"+strings.Repeat("B", 42)+"A"
	input := map[string]any{
		"id":             "same-raw-value",
		"history_origin": map[string]any{"entry_id": "same-raw-value", "position": p1},
		"page_info": map[string]any{"first_position": p1, "last_position": p2,
			"retention_generation": "same-raw-value", "projected_bytes": 4096},
		"unrelated": "must remain unchanged",
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	n := newFixtureNormalizer()
	var got map[string]any
	require.NoError(t, json.Unmarshal(n.frame(t, raw), &got))
	require.Equal(t, "id-1", got["id"], "frame identity wins before the new origin visitor")
	origin, ok := got["history_origin"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "id-1", origin["entry_id"], "same original identity keeps its existing placeholder family")
	require.Equal(t, catchupFixturePosition1, origin["position"])
	page, ok := got["page_info"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, catchupFixturePosition1, page["first_position"])
	require.Equal(t, catchupFixturePosition2, page["last_position"])
	require.Equal(t, "fixture-generation-1", page["retention_generation"], "generation is not an ordinary ID alias")
	require.Equal(t, float64(1), page["projected_bytes"], "explicit synthetic measurement placeholder, never a measured receipt")
	require.Equal(t, "must remain unchanged", got["unrelated"], "no blanket unknown-field filter")
}

func catchupPagingControlEvents(t *testing.T) []fixtureEvent {
	t.Helper()
	// One actual-source-shaped batch, independently derived from D10's defaults,
	// closed generated carriers and one plain row. 4096 is a deliberately safe
	// input above these small encoded frames, not a copied product measurement.
	snapshot := `{"type":"session_snapshot","session_id":"control","seq":1,"has_earlier":false,"has_later":false,"page_info":{"mode":"latest","limit":40,"min_rows":20,"row_count":1,"source_group_count":1,"soft_byte_budget":4194304,"projected_bytes":4096,"byte_budget_exceeded":false,"stop_reason":"end","retention_generation":"control-generation","context":{"spans":[],"duplicate_ids":[]},"first_position":"` + catchupFixturePosition1 + `","last_position":"` + catchupFixturePosition1 + `"}}`
	frames := []string{
		snapshot,
		`{"type":"session_state","session_id":"control","emitted_at":"2026-10-22T12:00:00Z","pending_approvals":[],"user_id":"","history_state":{"status":"available"}}`,
		`{"type":"replay_message","session_id":"control","id":"control-source","role":"user","content":"source","history_origin":{"entry_id":"control-source","position":"` + catchupFixturePosition1 + `"}}`,
		`{"type":"done","session_id":"control","stats":{"frames_emitted":1}}`,
		`{"type":"catch_up_complete","session_id":"control","seq":1,"boot_id":"control-boot","mode":"snapshot"}`,
	}
	events := make([]fixtureEvent, len(frames))
	for i, frame := range frames {
		require.True(t, json.Valid([]byte(frame)), "instrument input must be valid JSON")
		events[i] = fixtureEvent{Dir: "server→client", Frame: json.RawMessage(frame)}
	}
	return events
}

func TestCatchUpPagingValidator_RejectsMetadataOriginAndCounterCorruption(t *testing.T) {
	t.Run("valid_raw_batch", func(t *testing.T) {
		require.NoError(t, catchupPagingValidate("F3", &fxTab{events: catchupPagingControlEvents(t)}))
	})
	cases := []struct {
		name   string
		frame  int
		path   []string
		key    string
		value  any
		remove bool
		want   string
	}{
		{"wrong_row_count", 0, []string{"page_info"}, "row_count", 2, false, "exact scenario source/row counts"},
		{"wrong_source_count", 0, []string{"page_info"}, "source_group_count", 2, false, "exact scenario source/row counts"},
		{"wrong_default_limit", 0, []string{"page_info"}, "limit", 39, false, "default options"},
		{"false_continuation", 0, nil, "has_earlier", true, false, "no continuation or clear"},
		{"missing_origin", 2, nil, "history_origin", nil, true, "missing or foreign source origin"},
		{"wrong_origin_entry_id", 2, []string{"history_origin"}, "entry_id", "another-source", false, "missing or foreign source origin"},
		{"wrong_source_session", 2, nil, "session_id", "another-session", false, "missing or foreign source origin"},
		{"wrong_origin_position", 2, []string{"history_origin"}, "position", catchupFixturePosition2, false, "metadata range does not match"},
		{"malformed_position", 2, []string{"history_origin"}, "position", "not-a-position", false, "generated ReplayMessageFrame validation failed"},
		{"missing_history_state", 1, nil, "history_state", nil, true, "scoped state must explicitly report available"},
		{"unavailable_is_not_success", 1, []string{"history_state"}, "status", "unavailable", false, "scoped state must explicitly report available"},
		{"available_state_has_no_reason", 1, []string{"history_state"}, "reason", "storage_error", false, "scoped state must explicitly report available"},
		{"zero_projected_bytes", 0, []string{"page_info"}, "projected_bytes", 0, false, "actual tiny-page byte accounting"},
		{"under_counted_projected_bytes", 0, []string{"page_info"}, "projected_bytes", 1, false, "undercounts actual historical encoded bytes"},
		{"nonzero_optional_orphans", 3, []string{"stats"}, "orphan_count", 1, false, "nonzero optional diagnostic"},
		{"nonzero_optional_duplicates", 3, []string{"stats"}, "duplicate_tool_call_id_count", 1, false, "nonzero optional diagnostic"},
		{"nonzero_optional_truncation", 3, []string{"stats"}, "truncated_result_count", 1, false, "nonzero optional diagnostic"},
		{"wrong_replay_frame_count", 3, []string{"stats"}, "frames_emitted", 0, false, "exact frames_emitted"},
		{"wrong_complete_cut", 4, nil, "seq", 2, false, "exact cut"},
		{"live_only_is_not_snapshot_success", 4, nil, "mode", "live_only", false, "snapshot completion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := catchupPagingControlEvents(t)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(events[tc.frame].Frame, &fields))
			node := fields
			for _, key := range tc.path {
				next, ok := node[key].(map[string]any)
				require.True(t, ok, "control must address an actual object")
				node = next
			}
			if tc.remove {
				require.Contains(t, node, tc.key)
				delete(node, tc.key)
			} else {
				node[tc.key] = tc.value
			}
			raw, err := json.Marshal(fields)
			require.NoError(t, err)
			events[tc.frame].Frame = raw
			require.ErrorContains(t, catchupPagingValidate("F3", &fxTab{events: events}), tc.want)
		})
	}
}
