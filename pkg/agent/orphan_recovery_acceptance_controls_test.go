//go:build goolm && stdjson

package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACTestControls(t *testing.T) {
	t.Run("N4_completed_groups_and_unbound_records_reach_real_HTTP", orphanACCompletedControls)
	t.Run("N5_empty_archive_is_unchanged", orphanACEmptyControl)
	t.Run("N5_unbound_records_and_controls_are_unchanged", orphanACUnboundControls)
	t.Run("parsed_JSON_representation_does_not_change_binding", orphanACParsedRecords)
}

func orphanACCompletedControls(t *testing.T) {
	for _, n := range []int{1, 2} {
		name := "single_marker_for_already_resolved_ID"
		if n == 2 {
			name = "duplicate_markers_for_already_resolved_ID"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			raw := []providers.Message{
				{Role: "user", Content: "fully completed parallel turn"},
				orphanACGroup("both calls completed", "done-a", "done-b"),
				orphanACResult("done-a", "complete source A"),
				orphanACResult("done-b", "complete source B"),
			}
			for range n {
				raw = append(raw, orphanACMarker("done-a"))
			}
			raw = append(raw, providers.Message{Role: "system", Content: "retain completed-turn control"})
			h.append(t, raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			ts := h.turn("")
			out := orphanACAssertView(t, h, ts, raw)
			require.NoError(t, validateWindowGroups(out), "N4: already-completed groups are neither restart-canceled nor invalid")
			r, provider := cwR1OpenAI(t, 0)
			rr := cwR1Flow(h, ts, out, provider)
			rr.rq.prepareCallMessages()
			require.NoError(t, ts.contextWindowError(), "positive instrument control takes the real validation path")
			callMessages := rr.rq.ri.rf.callMessages
			require.Len(t, callMessages, len(raw)+1, "N4 ruling: the complete raw view survives until the normalization/send boundary")
			require.Equal(t, "system", callMessages[0].Role)
			require.Equal(t, raw, callMessages[1:], "pre-send validation/repair cannot erase or reorder the raw complete group, unbound records or control")
			pinnedInput := callMessages[0].Content
			// Architect N4 ruling: derive the exact wire instructions from the
			// immutable pinned INPUT plus literal fixture records, not received
			// output or a production normalizer. Preserve each input occurrence.
			systemPieces := []string{pinnedInput}
			for range n {
				systemPieces = append(systemPieces, `{"type":"turn_canceled_restart","tool_call_id":"done-a","reason":"ungraceful_shutdown_recovery"}`)
			}
			systemPieces = append(systemPieces, "retain completed-turn control")
			wantSystemContent := strings.Join(systemPieces, "\n\n")
			response, err := rr.rq.ri.rf.rt.callProviderOnce(callMessages, nil)
			require.NoError(t, err, "valid complete request must cross the same send boundary negatives are forbidden to cross")
			require.NotNil(t, response)
			require.Equal(t, "r1-success", response.Content, "paid-model edge replacement observes actual provider progress")
			bodies := r.requests(t)
			require.Len(t, bodies, 1, "recorder can see a real request; zero-request negative oracle is not a dead instrument")
			t.Logf("N4 captured full HTTP body (marker occurrences=%d): %s", n, bodies[0])
			received := cwR1Messages(t, bodies[0])
			require.NotEmpty(t, received)
			require.Len(t, received, 5, "N4 ruling: one composed system message followed by the four unchanged non-system messages")
			require.Equal(t, "system", received[0].Role)
			require.Equal(t, raw[:4], received[1:], "final serialized body retains the entire complete group and both results, with every field and order intact")
			require.Equal(t, wantSystemContent, received[0].Content, "serialized instructions retain the pinned input, every unbound marker occurrence and unrelated control in exact order, without loss or duplicate collapse")
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}

func orphanACEmptyControl(t *testing.T) {
	h := cwR1New(t, 100000)
	before := orphanACSnapshot(t, h)
	require.Equal(t, 0, before.State.Count)
	require.Equal(t, 0, before.State.Skip)
	require.Nil(t, before.State.AnchorLine)
	require.Len(t, viewArchived(before), 0)
	require.Len(t, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), 0, "empty history cannot manufacture a cancellation")
	out := orphanACAssertView(t, h, h.turn(""), []providers.Message{})
	require.Equal(t, []int{-1}, mapWindowMessages(before, out, -1, 0), "the only slot is the ephemeral pinned envelope")
	require.NoError(t, validateWindowGroups(out))
	require.Equal(t, before, orphanACSnapshot(t, h), "empty selection/recovery does not create window state or records")
}

func orphanACUnboundControls(t *testing.T) {
	cases := []struct {
		name string
		raw  []providers.Message
	}{
		{"marker_only", []providers.Message{orphanACMarker("no-declaration")}},
		{"marker_then_control", []providers.Message{orphanACMarker("no-declaration"), {Role: "system", Content: "unrelated only control"}}},
		{"control_only", []providers.Message{{Role: "system", Content: "no group and no cancellation"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			h.append(t, tc.raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			require.Equal(t, tc.raw, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "unbound records authorize no removal and no new record")
			out := orphanACAssertView(t, h, h.turn(""), tc.raw)
			require.NoError(t, validateWindowGroups(out))
			lines := []int{-1}
			for i := range tc.raw {
				lines = append(lines, i)
			}
			require.Equal(t, lines, mapWindowMessages(before, out, -1, 0))
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}

func orphanACParsedRecords(t *testing.T) {
	// ASCII backslash retains a JSON Unicode escape through both parsers.
	jsonEscape := string(rune(92))
	cases := []struct {
		name   string
		id     string
		record string
	}{
		{"pretty_reordered_JSON", "call_0", "{\n  \"reason\": \"ungraceful_shutdown_recovery\",\n  \"tool_call_id\": \"call_0\",\n  \"type\": \"turn_canceled_restart\"\n}"},
		{"escaped_ID_and_type_decode_to_exact_values", "call_0", `{"type":"turn_` + jsonEscape + `u0063anceled_restart","tool_call_id":"call_` + jsonEscape + `u0030","reason":"ungraceful_shutdown_recovery"}`},
		{"quoted_and_backslash_ID", `id"with\slash`, `{"type":"turn_canceled_restart","tool_call_id":"id\"with\\slash","reason":"ungraceful_shutdown_recovery"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			raw := []providers.Message{
				{Role: "user", Content: "JSON binding, not substring matching"},
				orphanACGroup("partially completed interrupted group", "done", tc.id),
				orphanACResult("done", "owned partial source"),
				{Role: "system", Content: "control survives before marker"},
				{Role: "system", Content: tc.record},
				{Role: "user", Content: "later complete request"},
				orphanACGroup("later live step", "live-json"),
				orphanACResult("live-json", "later result"),
			}
			h.append(t, raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, 0, 3, 5, 6, 7))
			require.Equal(t, []int{-1, 0, 3, 5, 6, 7}, mapWindowMessages(before, out, -1, 0), "JSON representation cannot change original source addresses")
			require.NoError(t, validateWindowGroups(out))
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}
