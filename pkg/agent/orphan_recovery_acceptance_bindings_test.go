//go:build goolm && stdjson

package agent

import (
	"encoding/json"
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
	}{
		{"two_missing_ID_records", orphanACMarker("missing-two"), []int{0, 1, 2, 3, 4, 8, 10, 11, 12, 13, 14, 15}},
		{"repeated_valid_record_same_group", orphanACMarker("missing-one"), []int{0, 1, 2, 3, 4, 8, 10, 11, 12, 13, 14, 15}},
		{"one_record_plus_unrelated_system_record", providers.Message{Role: "system", Content: `{"type":"other_control","content":"not group-owned"}`}, []int{0, 1, 2, 3, 4, 8, 9, 10, 11, 12, 13, 14, 15}},
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
			out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, tc.lines...))
			require.Equal(t, append([]int{-1}, tc.lines...), mapWindowMessages(before, out, -1, 0), "P1: parallel exclusions never broaden to completed groups or control addresses")
			require.NoError(t, validateWindowGroups(out), "completed groups retain all their declared/result identities, independent of parallel completion order")
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
			lines := []int{0, 1, 2, 3, 6, 7, 8, 9, 10, 13, 14, 15}
			if complete {
				raw = append(raw, orphanACResult("call_0", "latest distinct live result"))
				lines = append(lines, 16)
			}
			h.append(t, raw...)
			before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
			want := orphanACPick(raw, lines...)
			out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
			assert.Equal(t, want, out[1:], "I1: call_0 cancellation is positional, never a session-global deny set")
			candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, want...)
			require.Equal(t, append([]int{-1}, lines...), mapWindowMessages(before, candidate, -1, 0), "each surviving reused ID stays attached to its original occurrence")
			if complete {
				require.NoError(t, validateWindowGroups(candidate), "earlier result and marker cannot invalidate later complete reuse")
			} else {
				orphanACAssertRejectedBeforeSend(t, h, h.turn(""), candidate, "context request: incomplete or invalid tool-result group at message 12")
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
	want := orphanACPick(raw, 0, 3, 4, 6)
	cleaned := RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
	after := orphanACSnapshot(t, h)
	require.Len(t, viewArchived(after), 8, "I2: earlier call_0 record cannot suppress cancellation of the later assistant position")
	require.Equal(t, viewArchived(before), viewArchived(after)[:7], "positional recovery appends only, preserving both original orphan declarations")
	require.Equal(t, "system", viewArchived(after)[7].Role)
	var marker map[string]any
	require.NoError(t, json.Unmarshal([]byte(viewArchived(after)[7].Content), &marker))
	require.Equal(t, map[string]any{"type": "turn_canceled_restart", "tool_call_id": "call_0", "reason": "ungraceful_shutdown_recovery"}, marker, "second record binds to the later declaration without a new persisted shape")
	require.Equal(t, 8, after.State.Count)
	require.Equal(t, before.State.Skip, after.State.Skip)
	require.Equal(t, before.State.AnchorLine, after.State.AnchorLine)
	require.Equal(t, want, cleaned, "recovery removes both bound abandoned occurrences but no user/control/narration")
	bytes := orphanACArchiveBytes(t, h)
	require.Equal(t, want, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "second recovery yields the same exact view")
	orphanACAssertUnchanged(t, h, after, bytes)
	orphanACReopen(t, h)
	require.Equal(t, want, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "reloaded repeated recovery remains position-idempotent")
	orphanACAssertView(t, h, h.turn(""), want)
	orphanACAssertUnchanged(t, h, after, bytes)
}
