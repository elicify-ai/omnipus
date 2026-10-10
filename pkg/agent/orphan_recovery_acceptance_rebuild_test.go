//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func orphanACTestRebuild(t *testing.T) {
	t.Run("R1_first_recovery_preserves_unrelated_control", orphanACFirstRecovery)
	t.Run("R2_reload_preserves_exact_view_and_archive", orphanACReload)
	t.Run("R3_later_turn_keeps_live_addresses_and_rejects_live_orphan", orphanACLaterTurn)
	t.Run("R4a_forced_trim_uses_filtered_prefix", orphanACTrimPrefix)
	t.Run("R4b_forced_trim_does_not_resurrect_retained_orphan", orphanACTrimSuffix)
}

func orphanACFirstRecovery(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "interrupted question", Media: []string{"media://original-user"}},
		orphanACGroup("waiting for approval", "first-orphan"),
		{Role: "system", Content: `{"type":"steering_control","sequence":17,"content":"keep this control"}`},
	}
	h.append(t, raw...)
	before := orphanACSnapshot(t, h)
	want := orphanACPick(raw, 0, 2)
	cleaned := RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
	assert.Equal(t, want, cleaned, "R1: real recovery must not strip unrelated controls following the canceled declaration")
	after := orphanACSnapshot(t, h)
	require.Len(t, viewArchived(after), 4, "one durable cancellation record, no invented tool result")
	require.Equal(t, viewArchived(before), viewArchived(after)[:3], "all original records remain byte-for-field intact")
	require.Equal(t, "system", viewArchived(after)[3].Role, "marker is a system record")
	var marker map[string]any
	require.NoError(t, json.Unmarshal([]byte(viewArchived(after)[3].Content), &marker))
	require.Equal(t, map[string]any{"type": "turn_canceled_restart", "reason": "ungraceful_shutdown_recovery", "tool_call_id": "first-orphan"}, marker, "existing cancellation shape is sufficient")
	require.Equal(t, before.State.Skip, after.State.Skip, "recovery is not cursor eviction")
	require.Equal(t, before.State.AnchorLine, after.State.AnchorLine, "recovery is not an anchor replacement")
	require.Equal(t, 4, after.State.Count, "count reflects the durable marker only")
	out := orphanACAssemble(t, h, h.turn(""), cleaned)
	assert.Equal(t, want, out[1:], "R1: snapshot assembly must not overwrite successful recovery with raw history")
	orphanACReopen(t, h)
	orphanACAssertView(t, h, h.turn(""), want)
}

func orphanACReload(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "parallel interrupted question", Media: []string{"media://persisted-user"}},
		orphanACGroup("partially finished", "partial-a", "missing-b"),
		orphanACResult("partial-a", "partial source must remain in archive"),
		orphanACMarker("missing-b"),
		{Role: "system", Content: `{"type":"steering_control","sequence":18,"content":"retain ordered control"}`},
		{Role: "user", Content: "subsequent question"},
		orphanACGroup("later complete step", "later-live"),
		orphanACResult("later-live", "later result"),
		{Role: "assistant", Content: "later finished narration", ReasoningContent: "retained reasoning"},
	}
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	orphanACReopen(t, h)
	wantLines := []int{0, 4, 5, 6, 7, 8}
	out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, wantLines...))
	require.Equal(t, []int{-1, 0, 4, 5, 6, 7, 8}, mapWindowMessages(orphanACSnapshot(t, h), out, -1, 0), "R2: reloaded view uses the original decoded archive addresses")
	require.NoError(t, validateWindowGroups(out), "only complete surviving groups reach validation")
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACLaterTurn(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "old interrupted turn"},
		orphanACGroup("old abandoned step", "old-id"),
		orphanACMarker("old-id"),
		{Role: "system", Content: "ordered control between turns"},
		{Role: "user", Content: "new initiating request", Media: []string{"media://new-user"}},
		orphanACGroup("new complete step", "new-result-id"),
		orphanACResult("new-result-id", "new result"),
		orphanACGroup("new in-flight step is not canceled", "new-inflight-id"),
	}
	h.append(t, raw[:4]...)
	initial := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	assert.Equal(t, orphanACPick(raw, 0, 3), initial[1:], "R3: canceled group is already absent before later appends")
	h.append(t, raw[4:]...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	want := orphanACPick(raw, 0, 3, 4, 5, 6, 7)
	out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	assert.Equal(t, want, out[1:], "R3: later assembly neither resurrects the old orphan nor erases a genuinely live incomplete group")
	candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, want...)
	require.Equal(t, []int{-1, 0, 3, 4, 5, 6, 7}, mapWindowMessages(before, candidate, -1, 0), "R3: every later archived slot has its own original address, including in-flight declarations")
	orphanACAssertRejectedBeforeSend(t, h, h.turn(""), candidate, "context request: incomplete or invalid tool-result group at message 6")
	orphanACAssertUnchanged(t, h, before, bytes)
}

func orphanACTrimPrefix(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "old interrupted turn"},            // 0
		orphanACGroup("abandoned", "trim-orphan"),                  // 1
		orphanACMarker("trim-orphan"),                              // 2
		{Role: "user", Content: "second turn"},                     // 3
		orphanACGroup("second step", "second-result"),              // 4
		orphanACResult("second-result", "second result"),           // 5
		{Role: "assistant", Content: "second completed narration"}, // 6
		{Role: "user", Content: "third turn"},                      // 7
		orphanACGroup("third step", "third-result"),                // 8
		orphanACResult("third-result", "third result"),             // 9
	}
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	result, changed := h.al.trimWindowChecked(context.Background(), h.agent, h.key, h.key, true)
	require.NoError(t, result.Err, "R4a: restart cancellation is removed before trim's structural policy")
	assert.True(t, changed, "R4a: the oldest canceled turn has a legal whole-turn cut")
	assert.False(t, result.NothingToTrim, "a forced legal cut must be reported as relief")
	assert.Equal(t, 1, result.DroppedMessages, "8 visible entries become 7: canceled entries do not count as budget eviction")
	assert.Equal(t, 7, result.RemainingMessages, "the exact surviving second and third turns remain")
	after := orphanACSnapshot(t, h)
	wantState := before.State.Clone()
	wantState.Skip = 3 // Original address of the second user, not compacted position 1.
	assert.Equal(t, wantState, after.State, "R4a: only the legal original-index Skip advances")
	require.Equal(t, viewArchived(before), viewArchived(after), "trim keeps all raw records for recall")
	require.Equal(t, bytes, orphanACArchiveBytes(t, h), "forced trim does not rewrite the archive")
	orphanACReopen(t, h)
	out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, 3, 4, 5, 6, 7, 8, 9))
	require.Equal(t, []int{-1, 3, 4, 5, 6, 7, 8, 9}, mapWindowMessages(orphanACSnapshot(t, h), out, -1, 0), "cut and reload preserve exact original addresses")
}

func orphanACTrimSuffix(t *testing.T) {
	h := cwR1New(t, 100000)
	raw := []providers.Message{
		{Role: "user", Content: "old completed turn"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "interrupted turn retained after trim"},
		orphanACGroup("abandoned retained step", "retained-orphan"),
		orphanACMarker("retained-orphan"),
		{Role: "system", Content: "control that must not disappear with retained orphan"},
		{Role: "user", Content: "latest turn"},
		orphanACGroup("latest complete step", "latest-result"),
		orphanACResult("latest-result", "latest result"),
	}
	h.append(t, raw...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	result, changed := h.al.trimWindowChecked(context.Background(), h.agent, h.key, h.key, true)
	require.NoError(t, result.Err)
	assert.True(t, changed, "R4b: old completed whole turn is legally evictable")
	assert.False(t, result.NothingToTrim)
	assert.Equal(t, 2, result.DroppedMessages, "only the old user and answer count as eviction")
	assert.Equal(t, 5, result.RemainingMessages, "retained suffix contains 5 recovery-visible entries, not the 7 raw entries")
	after := orphanACSnapshot(t, h)
	wantState := before.State.Clone()
	wantState.Skip = 2
	assert.Equal(t, wantState, after.State, "first legal cut is original address 2; cancellation grants no extra Skip movement")
	require.Equal(t, viewArchived(before), viewArchived(after))
	require.Equal(t, bytes, orphanACArchiveBytes(t, h))
	orphanACReopen(t, h)
	out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, 2, 5, 6, 7, 8))
	require.Equal(t, []int{-1, 2, 5, 6, 7, 8}, mapWindowMessages(orphanACSnapshot(t, h), out, -1, 0), "R4b: retained raw canceled slots never poison subsequent alignment")
}
