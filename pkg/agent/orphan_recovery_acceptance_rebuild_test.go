//go:build goolm && stdjson

package agent

import (
	"context"
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
	issuer := r2Issuer(t, before, 1)
	cleaned := RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil)
	after := orphanACSnapshot(t, h)
	require.Len(t, viewArchived(after), 4, "one durable missing-occurrence observation, no invented original result")
	require.Equal(t, viewArchived(before), viewArchived(after)[:3], "all original records remain byte-for-field intact")
	require.Equal(t, "tool", viewArchived(after)[3].Role, "DESIGN 5: the appended record is an engine observation, not a system cancellation")
	observation := viewArchived(after)[3].Message
	r2CheckedObservation(t, observation, "first-orphan", issuer)
	want := append(orphanACPick(raw, 0, 1), observation, raw[2])
	assert.Equal(t, want, cleaned, "R1: DESIGN 2/5 retains the declaration, preserves the unrelated control and appends one truthful observation")
	require.NoError(t, validateWindowGroups(cleaned), "the retained declaration pairs with its own observation")
	require.Equal(t, before.State.Skip, after.State.Skip, "recovery is not cursor eviction")
	require.Equal(t, before.State.AnchorLine, after.State.AnchorLine, "recovery is not an anchor replacement")
	require.Equal(t, 4, after.State.Count, "count reflects the durable observation only")
	out := orphanACAssemble(t, h, h.turn(""), cleaned)
	assert.Equal(t, want, out[1:], "R1: snapshot assembly must not overwrite successful recovery with raw history")
	assert.Equal(t, want, RecoverOrphanedToolCalls(h.agent.Sessions, h.key, nil), "repeat recovery adds nothing")
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
	// Crash-recovery DESIGN Decision 5 supersedes whole-batch omission:
	// the successful A occurrence remains exact; only B needs an engine
	// recovery observation. Its wording is not specified, so do not invent it.
	out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	require.Len(t, out, 10, "R2: pinned envelope, original entries and one missing-occurrence observation")
	require.Equal(t, raw[:3], out[1:4], "R2: reload retains A's declaration and exact completed result")
	require.Equal(t, "tool", out[4].Role, "R2: B has a provider-valid recovery observation, not an invented original return")
	require.Equal(t, "missing-b", out[4].ToolCallID)
	require.NotContains(t, out[4].Content, "denied", "R2: missing result is not approval denial")
	require.Equal(t, raw[4:], out[5:], "R2: unrelated controls and later completed occurrences remain exact and ordered")
	mapped := mapWindowMessages(orphanACSnapshot(t, h), out, -1, 0)
	require.Equal(t, []int{-1, 0, 1, 2}, mapped[:4], "R2: completed history keeps its original addresses")
	require.Equal(t, []int{4, 5, 6, 7, 8}, mapped[5:], "R2: unrelated/later archive addresses do not shift")
	require.NoError(t, validateWindowGroups(out), "settled siblings and recovery observations form valid provider groups")
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
	issuer := r2Issuer(t, orphanACSnapshot(t, h), 1)
	initial := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	// DESIGN 2/5 reconciliation: the assembly view retains the marker-bound
	// declaration and shows one truthful observation; only the marker record
	// itself is excluded from the model view.
	require.Len(t, initial, 5, "envelope, user, retained declaration, observation, control")
	r2CheckedObservation(t, initial[3], "old-id", issuer)
	assert.Equal(t, orphanACPick(raw, 0, 1), initial[1:3], "R3: the canceled declaration stays in the view (DESIGN 5)")
	assert.Equal(t, raw[3], initial[4], "R3: the unrelated control is preserved")
	h.append(t, raw[4:]...)
	before, bytes := orphanACSnapshot(t, h), orphanACArchiveBytes(t, h)
	out := orphanACAssemble(t, h, h.turn(""), h.agent.Sessions.GetHistory(h.key))
	want := append(orphanACPick(raw, 0, 1), initial[3])
	want = append(want, orphanACPick(raw, 3, 4, 5, 6, 7)...)
	assert.Equal(t, want, out[1:], "R3: later assembly neither resurrects the old orphan nor erases a genuinely live incomplete group")
	candidate := append([]providers.Message{{Role: "system", Content: "independent pinned envelope"}}, want...)
	require.Equal(t, []int{-1, 0, 1, -1, 3, 4, 5, 6, 7}, mapWindowMessages(before, candidate, -1, 0), "R3: every archived slot keeps its original address; the inserted observation is ephemeral")
	orphanACAssertRejectedBeforeSend(t, h, h.turn(""), candidate, "context request: incomplete or invalid tool-result group at message 8")
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
	require.Equal(t, orphanACSurvivingLive(before, after), after.Live,
		"R4a: a trim shrinks the visible window to the surviving tail; the raw records stay in the archive")
	require.Equal(t, bytes, orphanACArchiveBytes(t, h), "forced trim keeps every raw record for recall")
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
	require.Equal(t, orphanACSurvivingLive(before, after), after.Live,
		"R4b: a trim shrinks the visible window to the surviving tail")
	require.Equal(t, bytes, orphanACArchiveBytes(t, h))
	orphanACReopen(t, h)
	out := orphanACAssertView(t, h, h.turn(""), orphanACPick(raw, 2, 5, 6, 7, 8))
	require.Equal(t, []int{-1, 2, 5, 6, 7, 8}, mapWindowMessages(orphanACSnapshot(t, h), out, -1, 0), "R4b: retained raw canceled slots never poison subsequent alignment")
}
