//go:build goolm && stdjson

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACTestBindings(t *testing.T) {
	t.Run("P1_parallel_records_exclude_only_one_abandoned_group", orphanACParallel)
	t.Run("I1_repeated_ID_is_bound_to_each_assistant_position", orphanACReusedIDs)
	t.Run("I2_later_orphan_reusing_ID_gets_exactly_one_new_record", orphanACRecoveryIdempotence)
}

func orphanACParallel(t *testing.T) {
	cases := []struct {
		name   string
		record providers.Message
		lines  []int
		// obs lists the call ids whose cancelled occurrences gain the engine
		// observation; err is the refusal the send boundary must produce when
		// an occurrence has neither a durable result nor a cancellation.
		obs []string
		err string
	}{
		{"two_missing_ID_records", orphanACMarker("missing-two"), []int{0, 1, 2, 3, 4, 5, 6, 8, 10, 11, 12, 13, 14, 15}, []string{"missing-one", "missing-two"}, ""},
		{"repeated_valid_record_same_group", orphanACMarker("missing-one"), []int{0, 1, 2, 3, 4, 5, 6, 8, 10, 11, 12, 13, 14, 15}, []string{"missing-one"}, "context request: incomplete or invalid tool-result group at message 6"},
		{"one_record_plus_unrelated_system_record", providers.Message{Role: "system", Content: `{"type":"other_control","content":"not group-owned"}`}, []int{0, 1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 12, 13, 14, 15}, []string{"missing-one"}, "context request: incomplete or invalid tool-result group at message 6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			raw := []providers.Message{
				{Role: "user", Content: "completed earlier parallel turn"},
				orphanACGroup("earlier complete parallel step", "before-a", "before-b"),
				orphanACResult("before-b", "B completed first"),
				orphanACResult("before-a", "A completed second"),
				{Role: "user", Content: "interrupted parallel turn"},
				orphanACGroup("abandoned parallel step", "partial-done", "missing-one", "missing-two"),
				orphanACResult("partial-done", "owned partial result"),
				orphanACMarker("missing-one"),
				{Role: "system", Content: `{"type":"steering_control","sequence":23,"content":"preserve between markers"}`},
				tc.record,
				{Role: "system", Content: "unrelated record after markers"},
				{Role: "user", Content: "completed later parallel turn"},
				orphanACGroup("later complete parallel step", "after-a", "after-b"),
				orphanACResult("after-a", "later A"),
				orphanACResult("after-b", "later B"),
				{Role: "assistant", Content: "completed later answer"},
			}
			h.append(t, raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			ts := h.turn("")
			// DESIGN 5: the abandoned group's DECLARATION and its durable
			// result are RETAINED; every recognised cancellation record binds
			// to the occurrence it names and is EXCLUDED; each occurrence a
			// bound cancellation left without a result gains the engine
			// observation (ephemeral, ordinal -1) beside the declaration it
			// completes. An occurrence with neither a durable result nor a
			// cancellation is NOT invented — the group stays visible and the
			// send boundary blocks the request instead.
			obs := make([]providers.Message, 0, len(tc.obs))
			for _, id := range tc.obs {
				o := orphanACObservation(t, h, ts, id)
				r2CheckedObservation(t, o, id, r2Issuer(t, before, 5))
				obs = append(obs, o)
			}
			want, addresses := orphanACDesign5ViewAt(t, raw, tc.lines, map[int][]providers.Message{7: obs})
			out := orphanACAssertView(t, h, ts, want)
			require.Equal(t, append([]int{-1}, addresses...), mapWindowMessages(before, out, -1, 0), "P1: parallel exclusions never broaden to completed groups or control addresses")
			if tc.err == "" {
				require.NoError(t, validateWindowGroups(out), "completed groups retain all their declared/result identities, independent of parallel completion order")
			} else {
				orphanACAssertRejectedBeforeSend(t, h, ts, out, tc.err)
			}
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}

func orphanACReusedIDs(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name := "latest_live_complete"
		if !complete {
			name = "latest_live_incomplete_still_rejected"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 100000)
			raw := []providers.Message{
				{Role: "user", Content: "first complete turn"},           // 0
				orphanACGroup("first complete call_0", "call_0"),         // 1
				orphanACResult("call_0", "first complete result"),        // 2
				{Role: "user", Content: "first canceled turn"},           // 3
				orphanACGroup("first canceled call_0", "call_0"),         // 4
				orphanACMarker("call_0"),                                 // 5
				{Role: "user", Content: "middle complete live turn"},     // 6
				orphanACGroup("middle complete call_0", "call_0"),        // 7
				orphanACResult("call_0", "different middle live result"), // 8
				{Role: "assistant", Content: "middle live narration"},    // 9
				{Role: "user", Content: "later canceled turn"},           // 10
				orphanACGroup("later canceled call_0", "call_0"),         // 11
				orphanACMarker("call_0"),                                 // 12
				{Role: "system", Content: "later retained control"},      // 13
				{Role: "user", Content: "latest live turn"},              // 14
				orphanACGroup("latest uncanceled call_0", "call_0"),      // 15
			}
			lines := []int{0, 1, 2, 3, 4, 6, 7, 8, 9, 10, 11, 13, 14, 15}
			if complete {
				raw = append(raw, orphanACResult("call_0", "latest distinct live result"))
				lines = append(lines, 16)
			}
			h.append(t, raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			ts := h.turn("")
			// DESIGN 5: a cancellation binds to ONE assistant occurrence, so
			// both cancelled groups are RETAINED with their engine
			// observations (ephemeral, ordinal -1) and their cancellation
			// records are EXCLUDED; the complete groups, the narration and the
			// control keep their own records and addresses. Reusing one call
			// id across occurrences stays valid.
			assembled := orphanACAssemble(t, h, ts, h.agent.Sessions.GetHistory(h.key))
			observations := orphanACObservations(t, assembled[1:])
			require.Len(t, observations, 2, "each cancelled occurrence gains exactly one engine observation")
			r2CheckedObservation(t, observations[0], "call_0", r2Issuer(t, before, 4))
			r2CheckedObservation(t, observations[1], "call_0", r2Issuer(t, before, 11))
			want, addresses := orphanACDesign5ViewAt(t, raw, lines,
				map[int][]providers.Message{5: {observations[0]}, 11: {observations[1]}})
			assert.Equal(t, want, assembled[1:], "I1: call_0 cancellation is positional, never a session-global deny set")
			candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, want...)
			require.Equal(t, append([]int{-1}, addresses...), mapWindowMessages(before, candidate, -1, 0), "each surviving reused ID stays attached to its original occurrence")
			if complete {
				require.NoError(t, validateWindowGroups(candidate), "earlier result and marker cannot invalidate later complete reuse")
			} else {
				orphanACAssertRejectedBeforeSend(t, h, ts, candidate, "context request: incomplete or invalid tool-result group at message 16")
			}
			orphanACAssertUnchanged(t, h, before, bytes)
		})
	}
}

func orphanACRecoveryIdempotence(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "earlier interrupted turn"},
		orphanACGroup("earlier canceled call_0", "call_0"),
		orphanACMarker("call_0"),
		{Role: "assistant", Content: "retained earlier narration"},
		{Role: "user", Content: "later interrupted turn"},
		orphanACGroup("later unmarked call_0", "call_0"),
		{Role: "system", Content: "preserve later unrelated control"},
	}
	h.append(t, raw...)
	before := orphanACSnapshot(t, h)
	// DESIGN 5: the recovery pass fills EVERY occurrence left without a
	// durable result — the earlier occurrence the cancellation record names
	// and the later unmarked occurrence of the same call id — with its own
	// engine observation bound to the exact declaration that issued it. Both
	// declarations stay visible; the cancellation record is bookkeeping and
	// never reaches the model.
	cleaned := RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
	after := orphanACSnapshot(t, h)
	require.Len(t, viewArchived(after), 9, "I2: each unresolved occurrence gains exactly one durable observation; the earlier call_0 marker cannot suppress the later one")
	require.Equal(t, viewArchived(before), viewArchived(after)[:7], "positional recovery appends only, preserving both original orphan declarations")
	require.Equal(t, "tool", viewArchived(after)[7].Role, "DESIGN 5: the new record is an engine observation, not another cancellation")
	require.Equal(t, "tool", viewArchived(after)[8].Role, "DESIGN 5: the second unresolved occurrence gains its own engine observation")
	earlier := viewArchived(after)[7].Message
	later := viewArchived(after)[8].Message
	r2CheckedObservation(t, earlier, "call_0", r2Issuer(t, before, 1))
	r2CheckedObservation(t, later, "call_0", r2Issuer(t, before, 5))
	require.Equal(t, 9, after.State.Count)
	require.Equal(t, before.State.Skip, after.State.Skip)
	require.Equal(t, before.State.AnchorLine, after.State.AnchorLine)
	want := make([]providers.Message, 0, 8)
	want = append(want, raw[0], raw[1], earlier, raw[3], raw[4], raw[5], later, raw[6])
	require.Equal(t, want, cleaned, "both declarations stay visible with their own truthful observation; no user/control/narration moves")
	require.NoError(t, validateWindowGroups(cleaned), "each retained declaration pairs with its own observation")
	bytes := orphanACArchiveBytes(t, h)
	require.Equal(t, want, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "second recovery yields the same exact view")
	orphanACAssertUnchanged(t, h, after, bytes)
	orphanACReopen(t, h)
	require.Equal(t, want, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "reloaded repeated recovery remains position-idempotent")
	orphanACAssertView(t, h, h.turn(""), want)
	orphanACAssertUnchanged(t, h, after, bytes)
}
