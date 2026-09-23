// loop_policy_mode_test.go: tests for ResolveEffectiveShellMode (ADR-091 D1
// mode resolution — loop_policy.go). Separated from loop_policy_test.go
// (owned by lane L4's CheckGrantOrRequestApproval-reachability work on the
// same file) so the two lanes' test files never collide.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func modePtr(m ShellMode) *ShellMode { return &m }

// ---------------------------------------------------------------------
// Three-level merge — happy path, each layer alone
// ---------------------------------------------------------------------

func TestResolveEffectiveShellMode_GlobalOnly_NoOverrides(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeAuto, nil, nil)
	assert.Equal(t, ShellModeAuto, got, "no agent or chat override must resolve to the bare global default")
}

func TestResolveEffectiveShellMode_AgentOverrideTightens(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeAuto, modePtr(ShellModeAsk), nil)
	assert.Equal(t, ShellModeAsk, got, "a genuinely stricter per-agent override must be honored")
}

func TestResolveEffectiveShellMode_ChatModifierTightens(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeAuto, nil, modePtr(ShellModeAsk))
	assert.Equal(t, ShellModeAsk, got, "a genuinely stricter per-chat modifier must be honored")
}

func TestResolveEffectiveShellMode_AllThreeLayersTighten_TightestWins(t *testing.T) {
	// global God, agent Auto (tighter than God), chat Ask (tighter still) ->
	// the tightest of all three, Ask.
	got := ResolveEffectiveShellMode(ShellModeGod, modePtr(ShellModeAuto), modePtr(ShellModeAsk))
	assert.Equal(t, ShellModeAsk, got)
}

// ---------------------------------------------------------------------
// Tighten-only refusal — the load-bearing property under test.
//
// Each case below feeds ResolveEffectiveShellMode a LOOSER-than-ceiling
// value at the agent or chat layer — exactly what a bypassed write-time
// validator (FR-003) or a hand-edited config.json/stale SessionModeStore
// entry could produce. "Before" shows what a naive last-write-wins merge
// (no tighterShellMode) would return; "after" is what this function
// actually returns. The two values differing IS the proof the refusal is
// real, not a no-op assertion.
// ---------------------------------------------------------------------

func TestResolveEffectiveShellMode_AgentOverrideCannotLoosen(t *testing.T) {
	global := ShellModeAsk
	looserAgentOverride := ShellModeAuto // would loosen Ask -> Auto if honored verbatim

	naiveLastWriteWins := looserAgentOverride // what a non-tighten-only merge would produce
	require := assert.New(t)
	require.NotEqual(global, naiveLastWriteWins, "sanity: the naive merge really would have loosened")

	got := ResolveEffectiveShellMode(global, modePtr(looserAgentOverride), nil)
	require.Equal(global, got, "a looser per-agent value must be refused at resolution, not honored")
	require.NotEqual(naiveLastWriteWins, got, "resolution must diverge from the naive (unsafe) merge")
}

func TestResolveEffectiveShellMode_ChatModifierCannotLoosen(t *testing.T) {
	global := ShellModeAsk
	looserChatModifier := ShellModeGod // the loosest possible value

	naiveLastWriteWins := looserChatModifier
	require := assert.New(t)
	require.NotEqual(global, naiveLastWriteWins, "sanity: the naive merge really would have loosened")

	got := ResolveEffectiveShellMode(global, nil, modePtr(looserChatModifier))
	require.Equal(global, got, "a looser per-chat value must be refused at resolution, not honored")
	require.NotEqual(naiveLastWriteWins, got, "resolution must diverge from the naive (unsafe) merge")
}

func TestResolveEffectiveShellMode_ChatModifierCannotLoosen_PastTightenedAgentCeiling(t *testing.T) {
	// global Auto, agent tightens to Ask, chat modifier tries to loosen back
	// to Auto — the chat layer must be measured against the AGENT's already
	// -tightened ceiling, not the raw global default.
	global := ShellModeAuto
	agentOverride := ShellModeAsk
	looserChatModifier := ShellModeAuto

	got := ResolveEffectiveShellMode(global, modePtr(agentOverride), modePtr(looserChatModifier))
	assert.Equal(t, ShellModeAsk, got, "the chat layer must not loosen past the agent-tightened ceiling")
}

// ---------------------------------------------------------------------
// God Mode interaction (D1: God is global-only; agent/chat overrides still
// tighten it exactly like any other loose ceiling).
// ---------------------------------------------------------------------

func TestResolveEffectiveShellMode_GodMode_NoOverrides_StaysGod(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeGod, nil, nil)
	assert.Equal(t, ShellModeGod, got)
}

func TestResolveEffectiveShellMode_GodMode_TightenedByAgentOverride(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeGod, modePtr(ShellModeAsk), nil)
	assert.Equal(t, ShellModeAsk, got, "an agent override must tighten God Mode exactly as it tightens Auto")
}

func TestResolveEffectiveShellMode_GodMode_TightenedByChatModifier(t *testing.T) {
	got := ResolveEffectiveShellMode(ShellModeGod, nil, modePtr(ShellModeAuto))
	assert.Equal(t, ShellModeAuto, got, "a chat modifier must tighten God Mode down to Auto")
}

// ---------------------------------------------------------------------
// Delegation inheritance (FR-005): a delegate resolves the tightest of
// (parent's session modifier, delegate's own agent override). SessionMode
// Store.InheritFrom supplies the first half (copied into the child's own
// session key); AgentShellModeOverride supplies the second half, read live
// against the delegate's own agent id. This test drives both together
// through ResolveEffectiveShellMode exactly as lane L4's wiring will.
// ---------------------------------------------------------------------

func TestDelegationInheritance_TightestOfParentModifierAndDelegateOverride(t *testing.T) {
	t.Run("delegate's own stricter agent override wins over a looser inherited modifier", func(t *testing.T) {
		store := NewSessionModeStore()
		store.Set("parent-session", ShellModeAuto)
		store.InheritFrom("parent-session", "child-session")

		inherited, ok := store.Get("child-session")
		assert.True(t, ok)
		assert.Equal(t, ShellModeAuto, inherited)

		delegateOwnOverride := modePtr(ShellModeAsk) // stricter than the inherited Auto
		got := ResolveEffectiveShellMode(ShellModeGod, delegateOwnOverride, &inherited)
		assert.Equal(t, ShellModeAsk, got, "the delegate's own stricter override must win")
	})

	t.Run("inherited parent modifier wins when the delegate has no override of its own", func(t *testing.T) {
		store := NewSessionModeStore()
		store.Set("parent-session-2", ShellModeAsk)
		store.InheritFrom("parent-session-2", "child-session-2")

		inherited, ok := store.Get("child-session-2")
		assert.True(t, ok)
		assert.Equal(t, ShellModeAsk, inherited)

		// Delegate carries no per-agent override at all (nil) — it rides the
		// global ceiling, but the inherited parent modifier still tightens it.
		got := ResolveEffectiveShellMode(ShellModeGod, nil, &inherited)
		assert.Equal(t, ShellModeAsk, got, "the inherited parent modifier must still tighten the delegate")
	})

	t.Run("no parent modifier and no delegate override resolves to the bare global default", func(t *testing.T) {
		store := NewSessionModeStore()
		// Parent never set a modifier — InheritFrom is a documented no-op.
		store.InheritFrom("parent-session-3", "child-session-3")
		_, ok := store.Get("child-session-3")
		assert.False(t, ok, "nothing to inherit when the parent never set a modifier")

		got := ResolveEffectiveShellMode(ShellModeAuto, nil, nil)
		assert.Equal(t, ShellModeAuto, got)
	})
}

// ---------------------------------------------------------------------
// Session modifier dies with the session (FR-004/FR-006): once
// ClearSession runs, resolution for that session id falls back to whatever
// the caller supplies for the chat-modifier slot — in practice, lane L4's
// wiring passes nil after a Get miss, exactly like a session that never had
// a modifier.
// ---------------------------------------------------------------------

func TestSessionModifierDiesWithSession_ResolvesAsUnset(t *testing.T) {
	store := NewSessionModeStore()
	store.Set("sess-1", ShellModeAsk)

	before, ok := store.Get("sess-1")
	assert.True(t, ok)
	gotBefore := ResolveEffectiveShellMode(ShellModeAuto, nil, &before)
	assert.Equal(t, ShellModeAsk, gotBefore, "before clearing, the modifier tightens resolution")

	store.ClearSession("sess-1")

	_, ok = store.Get("sess-1")
	assert.False(t, ok, "cleared session must report no modifier")
	gotAfter := ResolveEffectiveShellMode(ShellModeAuto, nil, nil)
	assert.Equal(t, ShellModeAuto, gotAfter, "after clearing, resolution must fall back to the bare ceiling")
}
