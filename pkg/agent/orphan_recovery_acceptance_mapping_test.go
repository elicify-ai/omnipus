//go:build goolm && stdjson

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACTestMapping(t *testing.T) {
	t.Run("M1_only_ephemeral_slots_map_to_minus_one", orphanACMapEphemeral)
	t.Run("M2_projection_uses_live_original_composite_address", orphanACMapProjection)
	t.Run("M3_skip_anchor_boundaries_keep_original_indexes", orphanACCursorBoundaries)
}

// Addresses are deliberately documented, including repeated IDs and a control
// between the partial result and its marker. The control is not group-owned.
func orphanACMappingFixture() []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "original interrupted question", Media: []string{"media://original"}}, // 0
		orphanACGroup("canceled parallel step", "shared", "missing"),                                  // 1
		orphanACResult("shared", strings.Repeat("旧", 600)),                                            // 2
		{Role: "system", Content: "control inside cancellation segment"},                              // 3
		orphanACMarker("missing"), // 4
		{Role: "user", Content: "later user", Media: []string{"media://later"}},                    // 5
		orphanACGroup("later completed shared ID", "shared"),                                       // 6
		orphanACResult("shared", strings.Repeat("新", 1200)),                                        // 7
		{Role: "system", Content: "later ordered control"},                                         // 8
		{Role: "assistant", Content: "later narration", ReasoningContent: "retain this reasoning"}, // 9
		{Role: "user", Content: "newest user"},                                                     // 10
		orphanACGroup("newest completed step", "newest-id"),                                        // 11
		orphanACResult("newest-id", "newest source"),                                               // 12
	}
}

func orphanACMapEphemeral(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := orphanACMappingFixture()
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	// DESIGN 5: the recovered model view retains the declaration (1) and its
	// durable sibling result (2), excludes the marker (4) and carries the
	// engine observation for the still-missing occurrence as an ephemeral
	// record (ordinal -1).
	view, addresses := orphanACDesign5View(t, h, raw, []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "missing", 3)
	candidate := make([]providers.Message, 0, len(view)+4)
	candidate = append(candidate,
		providers.Message{Role: "system", Content: "independent pinned envelope"},
		view[0],
		raw[3], // Transient recall deliberately matches a real archived identity.
		providers.Message{Role: "user", Content: "transient recalled question"},
	)
	candidate = append(candidate, view[1:]...)
	candidate = append(candidate, providers.Message{Role: "user", Content: "not yet archived current request"})
	original := cwR1Clone(t, candidate)
	// Every retained record keeps its ORIGINAL archive address; the envelope,
	// the transient recall block and the inserted observation are ephemeral.
	want := []int{-1}
	want = append(want, addresses[0])
	want = append(want, -1, -1)
	want = append(want, addresses[1:]...)
	want = append(want, -1)
	require.Equal(t, want, mapWindowMessages(before, candidate, 2, 2), "M1: explicit recall span cannot steal an archive slot; the retained group cannot stall later alignment")
	require.Equal(t, original, candidate, "mapping is read-only, including nested declarations and media")
	orphanACAssertUnchanged(t, h, before, bytes)
	// Generic memory selection must remain raw; recovery belongs to the agent.
	selected, lines := before.History()
	require.Equal(t, raw, selected, "agent recovery must not change generic WindowHistory semantics")
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, lines)
}

func orphanACMapProjection(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := orphanACMappingFixture()
	h.append(t, raw...)
	snap := orphanACSnapshot(t, h)
	state := snap.State.Clone()
	// Same ID, different real result addresses: canceling line 2 must neither
	// erase nor project line 7 with line 2's source. No invalid metadata injected.
	for _, line := range []int{2, 7} {
		key := memory.ProjectionKey{ToolCallID: "shared", ArchiveLine: line}
		state.Projection.Entries[key] = memory.ProjectionEmptied
		state.Projection.SourceRunes[key] = 0
	}
	require.NoError(t, h.store.CommitWindow(context.Background(), h.key, snap.State, state), "fixture commits legal projection keys through the real checked store")
	orphanACReopen(t, h)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	// DESIGN 5: the view retains the declaration (1) and its durable sibling
	// result (2), excludes the marker (4), and carries the engine observation
	// for the missing occurrence as an ephemeral record (ordinal -1).
	view, addresses := orphanACDesign5View(t, h, raw, []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "missing", 3)
	candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, view...)
	p, store, err := newWindowCheckpoint(context.Background(), h.turn(""), candidate, h.cfg.Context)
	require.NoError(t, err, "real checkpoint uses the recovery-visible source sequence")
	require.NotNil(t, store, "checkpoint retains the real checked store")
	want := append([]int{-1}, addresses...)
	require.Equal(t, want, p.lines, "M2: every retained record keeps its original address and the inserted observation is ephemeral")
	require.Equal(t, before.State, p.state, "mapping/projecting does not commit cursor or metadata changes")
	// ToolResultRecallMark defines turn as 1 + preceding users: users at 0 and
	// 5 make the later result's turn 3 (the earlier result's turn is 2).
	// Unicode character count is 1200, not 3600 UTF-8 bytes.
	orphanACAssertMark(t, p.messages[3], "shared", 2, 600, 2)
	orphanACAssertMark(t, p.messages[8], "shared", 7, 1200, 3)
	structure := cwR1Clone(t, p.messages)
	structure[3].Content = raw[2].Content // Only mark encoding is normalized after its whole schema was checked.
	structure[8].Content = raw[7].Content
	require.Equal(t, candidate, structure, "projection alters only the surviving result content; controls, IDs, reasoning and media survive")
	out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	require.Len(t, out, 14, "assembly retains the declaration and both durable results beside the inserted observation")
	orphanACAssertMark(t, out[3], "shared", 2, 600, 2)
	orphanACAssertMark(t, out[8], "shared", 7, 1200, 3)
	require.Equal(t, p.messages[1:], out[1:], "assembly and checkpoint reconstruct the same independently verified visible sequence")
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACCursorBoundaries(t *testing.T) {
	raw := orphanACMappingFixture()
	// DESIGN 5 + its 2026-10-26 addendum: the cursor cuts at ORIGINAL
	// indexes. A declaration still inside the window keeps its durable sibling
	// result and gains the engine observation for its missing occurrence
	// (ephemeral, ordinal -1); the marker is always excluded. A declaration
	// already behind the cursor is not resurfaced, and neither is its
	// observation — the addendum's cursor rule — so only the durable result
	// that is still inside the window remains.
	cases := []struct {
		name   string
		skip   int
		anchor int // -1 means no persisted anchor.
		lines  []int
		obs    string // call id of the engine observation the view inserts.
		obsAt  int    // its index in the reconciled view.
	}{
		{"at_zero", 0, -1, []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "missing", 3},
		{"at_canceled_declaration", 1, 0, []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "missing", 3},
		{"inside_canceled_partial_result", 2, 0, []int{0, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "", 0},
		{"at_preserved_segment_control", 3, 0, []int{0, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "", 0},
		{"at_marker_with_declaration_outside_window", 4, 0, []int{0, 5, 6, 7, 8, 9, 10, 11, 12}, "", 0},
		{"after_canceled_group", 5, 0, []int{0, 5, 6, 7, 8, 9, 10, 11, 12}, "", 0},
		{"at_live_declaration_with_new_anchor", 6, 5, []int{5, 6, 7, 8, 9, 10, 11, 12}, "", 0},
		{"at_live_result_preserves_raw_suffix", 7, 5, []int{5, 7, 8, 9, 10, 11, 12}, "", 0},
		{"one_before_count", 12, 10, []int{10, 12}, "", 0},
		{"at_count_keeps_explicit_anchor", 13, 10, []int{10}, "", 0},
		{"at_count_without_anchor_is_empty", 13, -1, []int{}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			h.append(t, raw...)
			snap := orphanACSnapshot(t, h)
			state := snap.State.Clone()
			state.Skip = tc.skip
			if tc.anchor >= 0 {
				anchor := tc.anchor
				state.AnchorLine = &anchor
			}
			require.NoError(t, h.store.CommitWindow(context.Background(), h.key, snap.State, state), "fixture uses the actual cursor/anchor transaction")
			orphanACReopen(t, h)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			view, addresses := orphanACDesign5View(t, h, raw, tc.lines, tc.obs, tc.obsAt)
			candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, view...)
			want := append([]int{-1}, addresses...)
			require.Equal(t, want, mapWindowMessages(before, candidate, -1, 0), "M3: full-archive cancellation binding precedes paired suffix/anchor filtering")
			out := orphanACAssertView(t, h, h.turn(""), view)
			require.Equal(t, want, mapWindowMessages(before, out, -1, 0), "assembly preserves exactly the same original addresses")
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
	// DESIGN 5's addendum, second sentence: "the archive-address aligner
	// skips inserted observations (ordinal -1) in the projected history
	// instead of blocking on them; the never-searched-globally invariant still
	// holds for real records."
	//
	// A model view can legitimately lack the engine observation — a trim cut
	// that evicted it, a request rebuilt from the archive without recovery's
	// repair, or any projection that dropped the ephemeral record. The records
	// that FOLLOW it must still keep their original archive addresses: the
	// aligner must step over the absent observation, not stop there. Blocking
	// there is what makes a later recall mark cite the wrong archive line (the
	// A3 class) and the Skip cursor advance to a compacted position.
	t.Run("aligner_steps_over_an_absent_observation", func(t *testing.T) {
		h := cwR1New(t, 100000)
		h.append(t, raw...)
		before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
		view, addresses := orphanACDesign5View(t, h, raw, []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12}, "missing", 3)
		// The candidate carries every real record but not the ephemeral
		// observation that completes the missing occurrence.
		withoutObservation := make([]providers.Message, 0, len(view))
		for _, m := range view {
			if m.Role == "tool" && m.ToolCallID == "missing" {
				continue
			}
			withoutObservation = append(withoutObservation, m)
		}
		require.Len(t, withoutObservation, len(view)-1, "fixture: exactly the observation is absent from the candidate")
		candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, withoutObservation...)
		want := []int{-1, 0, 1, 2}
		for _, address := range addresses[4:] {
			want = append(want, address)
		}
		require.Equal(t, want, mapWindowMessages(before, candidate, -1, 0),
			"M3: the aligner steps over the absent observation instead of blocking on it; every later record keeps its original address")
		orphanACAssertUnchanged(t, h, before, bytes)
	})
}
