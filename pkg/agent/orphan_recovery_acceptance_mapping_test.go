//go:build goolm && stdjson

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
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
	candidate := make([]providers.Message, 0, 4)
	candidate = append(candidate,
		providers.Message{Role: "system", Content: "independent pinned envelope"},
		raw[0],
		raw[3], // Transient recall deliberately matches a real archived identity.
		providers.Message{Role: "user", Content: "transient recalled question"},
	)
	candidate = append(candidate, orphanACPick(raw, 3, 5, 6, 7, 8, 9, 10, 11, 12)...)
	candidate = append(candidate, providers.Message{Role: "user", Content: "not yet archived current request"})
	original := cwR1Clone(t, candidate)
	want := []int{-1, 0, -1, -1, 3, 5, 6, 7, 8, 9, 10, 11, 12, -1}
	require.Equal(t, want, mapWindowMessages(session.WindowViewFromSnapshot(before), candidate, 2, 2), "M1: explicit recall span cannot steal an archive slot; removed group cannot stall later alignment")
	require.Equal(t, original, candidate, "mapping is read-only, including nested declarations and media")
	orphanACAssertUnchanged(t, h, before, bytes)
	// Generic memory selection must remain raw; recovery belongs to the agent.
	selected, lines := memory.WindowHistory(before)
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
	want := orphanACPick(raw, 0, 3, 5, 6, 7, 8, 9, 10, 11, 12)
	candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, want...)
	p, store, err := newWindowCheckpoint(context.Background(), h.turn(""), candidate, h.cfg.Context)
	require.NoError(t, err, "real checkpoint uses the recovery-visible source sequence")
	require.NotNil(t, store, "checkpoint retains the real checked store")
	require.Equal(t, []int{-1, 0, 3, 5, 6, 7, 8, 9, 10, 11, 12}, p.lines, "M2: projected result uses original address 7, not -1 or compacted position 4")
	require.Equal(t, before.State, p.state, "mapping/projecting does not commit cursor or metadata changes")
	// ToolResultRecallMark defines turn as 1 + preceding users: users at 0 and
	// 5 make this turn 3. Unicode character count is 1200, not 3600 UTF-8 bytes.
	orphanACAssertMark(t, p.messages[5], "shared", 7, 1200, 3)
	structure := cwR1Clone(t, p.messages)
	structure[5].Content = raw[7].Content // Only mark encoding is normalized after its whole schema was checked.
	require.Equal(t, candidate, structure, "projection alters only the surviving result content; controls, IDs, reasoning and media survive")
	out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	require.Len(t, out, 11, "assembly filters canceled result before projecting live result")
	orphanACAssertMark(t, out[5], "shared", 7, 1200, 3)
	require.Equal(t, p.messages[1:], out[1:], "assembly and checkpoint reconstruct the same independently verified visible sequence")
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACCursorBoundaries(t *testing.T) {
	raw := orphanACMappingFixture()
	cases := []struct {
		name   string
		skip   int
		anchor int // -1 means no persisted anchor.
		lines  []int
	}{
		{"at_zero", 0, -1, []int{0, 3, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"at_canceled_declaration", 1, 0, []int{0, 3, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"inside_canceled_partial_result", 2, 0, []int{0, 3, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"at_preserved_segment_control", 3, 0, []int{0, 3, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"at_marker_with_declaration_outside_window", 4, 0, []int{0, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"after_canceled_group", 5, 0, []int{0, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"at_live_declaration_with_new_anchor", 6, 5, []int{5, 6, 7, 8, 9, 10, 11, 12}},
		{"at_live_result_preserves_raw_suffix", 7, 5, []int{5, 7, 8, 9, 10, 11, 12}},
		{"one_before_count", 12, 10, []int{10, 12}},
		{"at_count_keeps_explicit_anchor", 13, 10, []int{10}},
		{"at_count_without_anchor_is_empty", 13, -1, []int{}},
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
			candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, orphanACPick(raw, tc.lines...)...)
			want := append([]int{-1}, tc.lines...)
			require.Equal(t, want, mapWindowMessages(session.WindowViewFromSnapshot(before), candidate, -1, 0), "M3: full-archive cancellation binding precedes paired suffix/anchor filtering")
			out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, tc.lines...))
			require.Equal(t, want, mapWindowMessages(session.WindowViewFromSnapshot(before), out, -1, 0), "assembly preserves exactly the same original addresses")
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}
