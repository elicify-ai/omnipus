//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACStructureNegatives(t *testing.T) {
	cases := []struct {
		name   string
		suffix []providers.Message
		err    string
		// unprovableAt is the index in suffix of the record admission REFUSES
		// (a tool result that cannot name its issuing assistant), or -1 when
		// every record is admissible. The archive holds only the admissible
		// ones, so the invalid sequence is built in memory for the send
		// boundary (ARCHITECT-ANSWER-FINAL-GATE.md item 4d).
		unprovableAt int
	}{
		{"empty_declared_ID", []providers.Message{
			orphanACGroup("malformed declaration", "", "missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"duplicate_declared_ID", []providers.Message{
			orphanACGroup("duplicate declaration", "missing", "missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"duplicate_declared_ID_and_duplicate_marker", []providers.Message{
			orphanACGroup("duplicate declaration", "missing", "missing"), orphanACMarker("missing"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"duplicate_partial_results", []providers.Message{
			orphanACGroup("invalid partial group", "done", "missing"),
			orphanACResult("done", "first partial result"), orphanACResult("done", "duplicate partial result"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"duplicate_results_with_multiple_missing_markers", []providers.Message{
			orphanACGroup("invalid three-call group", "done", "missing-a", "missing-b"),
			orphanACResult("done", "first partial result"), orphanACResult("done", "duplicate partial result"),
			orphanACMarker("missing-a"), orphanACMarker("missing-b"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"duplicate_result_after_marker", []providers.Message{
			orphanACGroup("duplicate is still corruption", "done", "missing"),
			orphanACResult("done", "first partial result"), orphanACMarker("missing"), orphanACResult("done", "duplicate after record"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"unowned_result_in_group", []providers.Message{
			orphanACGroup("foreign result is not owned partial progress", "missing"),
			orphanACResult("undeclared", "foreign result"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", 1},
		{"empty_result_ID_in_group", []providers.Message{
			orphanACGroup("empty result identity is corruption", "missing"),
			orphanACResult("", "result without ID"), orphanACMarker("missing"),
		}, "context request: incomplete or invalid tool-result group at message 2", 1},
		{"completed_group_with_duplicate_result", []providers.Message{
			orphanACGroup("duplicated completed result", "done"),
			orphanACResult("done", "complete result"), orphanACResult("done", "duplicate complete result"), orphanACMarker("done"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"resolved_result_separated_by_unrelated_control", []providers.Message{
			orphanACGroup("structurally separated result", "done"),
			{Role: "system", Content: "not a tool result"}, orphanACResult("done", "result after control"), orphanACMarker("done"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
		{"standalone_result_and_unbound_marker", []providers.Message{
			orphanACResult("missing", "no assistant declaration exists"), orphanACMarker("missing"),
		}, "context request: orphan tool result at message 2", 0},
		{"complete_group_then_marker_then_orphan_duplicate", []providers.Message{
			orphanACGroup("complete group with later corruption", "done"),
			orphanACResult("done", "owned complete result"), orphanACMarker("done"), orphanACResult("done", "unowned duplicate after record"),
		}, "context request: incomplete or invalid tool-result group at message 2", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix := []providers.Message{{Role: "user", Content: "invalid structure must not be sanitized"}}
			if tc.unprovableAt < 0 {
				raw := append(prefix, tc.suffix...)
				switch tc.name {
				case "duplicate_result_after_marker":
					// DESIGN 5: the group's DUPLICATE result makes the group
					// invalid, so no cancellation can bind to it — the marker
					// authorises nothing and is RETAINED, and no observation is
					// invented. The provider view binds every result to the
					// declaration that issued it, so the duplicate result sits
					// with the declaration and the marker keeps its own
					// address after it.
					orphanACAssertUnsupported(t, raw, tc.err,
						orphanACView5{
							messages:  []providers.Message{raw[0], raw[1], raw[2], raw[4], raw[3]},
							addresses: []int{0, 1, 2, 4, 3},
						})
				case "resolved_result_separated_by_unrelated_control":
					// DESIGN 5: the projection is keyed by assistant
					// OCCURRENCE + call id, so the durable result belongs to
					// the declaration that issued it even when an unrelated
					// control sits between them: the projection binds the
					// result to its declaration, the recognised marker for
					// that (already resolved) occurrence is EXCLUDED, the
					// unrelated control keeps its own address, and the
					// projected group is provider-valid. Nothing is invented
					// and nothing is refused.
					h := cwR1New(t, 100000)
					h.append(t, raw...)
					before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
					ts := h.turn("")
					want, addresses := orphanACDesign5ViewAt(t, raw, []int{0, 1, 3, 2}, nil)
					out := orphanACAssertView(t, h, ts, want)
					require.Equal(t, append([]int{-1}, addresses...), mapWindowMessages(before, out, -1, 0),
						"the bound result keeps its own archive address and the control keeps its own")
					require.NoError(t, validateWindowGroups(out),
						"the bound group is provider-valid once its durable result is bound to its declaration")
					orphanACAssertAcceptedBeforeSend(t, h, ts, out)
					orphanACAssertUnchanged(t, h, before, bytes)
				case "complete_group_then_marker_then_orphan_duplicate":
					// DESIGN 5: the duplicate result makes the group invalid, so
					// the marker cannot bind to it and is RETAINED; the duplicate
					// result is bound to the declaration that issued it. The
					// declaration is RETAINED, so the invalid group is visible at
					// its own message (2) and the refusal names the group — the
					// superseded whole-group-omission view dropped that
					// declaration and left the duplicate looking like an orphan.
					orphanACAssertUnsupported(t, raw, tc.err,
						orphanACView5{
							messages:  []providers.Message{raw[0], raw[1], raw[2], raw[4], raw[3]},
							addresses: []int{0, 1, 2, 4, 3},
						})
				default:
					orphanACAssertUnsupported(t, raw, tc.err)
				}
				return
			}
			// Only the record at unprovableAt is refused at admission; every
			// other record is seeded, and the refused one goes into the
			// in-memory sequence at its original position.
			if tc.name == "standalone_result_and_unbound_marker" {
				// Dedicated path: see the helper's doc comment — the trailing
				// marker legitimately cancels this standalone result, so the
				// send boundary ACCEPTS the marker-bound sequence.
				orphanACAssertStandaloneOrphan(t, prefix, tc.suffix, tc.err)
				return
			}
			admissible := append(prefix, tc.suffix[:tc.unprovableAt]...)
			bad := tc.suffix[tc.unprovableAt]
			orphanACAssertUnsupportedSplit(t, admissible, bad, tc.suffix[tc.unprovableAt+1:], tc.err, true)
		})
	}
}
