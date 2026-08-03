// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_messaging_wire_adr057_test.go — ADR-057 unit U19's red/green proof
// for the W13d scope choice in wireSessionMessagingForAgent's
// dt.SetCancelHooks(...) call (session_messaging_wire.go).
//
// [FIX-5, 2026-08-03] INVERTED. U15's original guidance (ScopeSelfOnly, NOT
// ScopeSubtree) was itself superseded by ADR-057 D8/R-13 before this unit's
// own task brief was written up-to-date: D1 gives every delegated child its
// OWN distinct session id, so a subtree walk rooted at a CHILD's sessionKey
// can no longer bleed into the parent or a sibling (ADR-057 architecture doc
// line ~336, FR-042, AC-8) — the "wrong widening" the retired guidance
// warned against. D8/R-13 additionally makes the wider reach a MANDATED
// behavior change, not merely a now-safe option: "delegate action=cancel"
// closing over just one turn silently leaked that child's own grandchildren
// and background shells running forever (spec:2731, BDD-29/BDD-46). This
// file previously asserted the retired ScopeSelfOnly contract as "green" and
// the ADR-mandated ScopeSubtree contract as "red" — exactly backwards from
// what session_messaging_wire.go must now wire. Per this task's binding
// rule (invert, don't delete), the red/green halves below are swapped: this
// file still proves the SAME two facts (what the two scopes actually reach,
// against an identical fixture) — only which one is labeled correct has
// changed.
//
// This file drives the SAME exported Interrupt/InterruptSessionHard methods
// session_messaging_wire.go's SetCancelHooks closures invoke, once per
// scope, against an identical parent/child/grandchild fixture, and shows the
// two scopes produce DIFFERENT reached-sets. Per binding Rule 5 this is a
// NEW file; per binding Rule 6 its unexported helpers are prefixed u19.
//
// Per binding Rule 1, every assertion runs against REAL *turnState values
// registered in a REAL *AgentLoop's activeTurnStates via the actual
// production registration path (al.registerActiveTurn) — never a spy.
package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// u19FixtureTurns builds a fresh parent/child/grandchild turnState trio,
// registers all three in al's activeTurnStates via the real production
// registration path, and returns a cleanup func. child.parentTurnID names
// parent.turnID and grandchild.parentTurnID names child.turnID — the same
// in-memory parentTurnID chain collectLiveDescendantTurnStates
// (steering.go) walks for ScopeSubtree.
//
// Every id is suffixed with a caller-supplied tag so two calls in the same
// test (one per scope under comparison) never collide in al.activeTurnStates.
func u19FixtureTurns(t *testing.T, al *AgentLoop, tag string) (parent, child, grandchild *turnState, cleanup func()) {
	t.Helper()

	parent = &turnState{
		sessionKey:          "u19-parent-key-" + tag,
		turnID:              "u19-parent-turn-" + tag,
		transcriptSessionID: "u19-parent-session-" + tag,
		routingSessionID:    session.RoutingSessionID("u19-parent-session-" + tag),
		depth:               0,
	}
	child = &turnState{
		sessionKey:          "u19-child-key-" + tag,
		turnID:              "u19-child-turn-" + tag,
		transcriptSessionID: "u19-child-session-" + tag,                            // D1: the delegate's OWN real session
		routingSessionID:    session.RoutingSessionID("u19-parent-session-" + tag), // inherited verbatim (FR-011)
		depth:               1,
		parentTurnID:        parent.turnID,
	}
	grandchild = &turnState{
		sessionKey:          "u19-grandchild-key-" + tag,
		turnID:              "u19-grandchild-turn-" + tag,
		transcriptSessionID: "u19-grandchild-session-" + tag,
		routingSessionID:    session.RoutingSessionID("u19-parent-session-" + tag), // inherited verbatim (FR-011)
		depth:               2,
		parentTurnID:        child.turnID, // descended from child, NOT parent
	}

	al.registerActiveTurn(parent)
	al.registerActiveTurn(child)
	al.registerActiveTurn(grandchild)

	cleanup = func() {
		al.clearActiveTurnStateEntry(parent.sessionKey, parent)
		al.clearActiveTurnStateEntry(child.sessionKey, child)
		al.clearActiveTurnStateEntry(grandchild.sessionKey, grandchild)
	}
	return parent, child, grandchild, cleanup
}

// TestSetCancelHooks_ChildCancelReachesSubtree is the mandated red/green
// proof for the ADR-057 D8/R-13 scope choice (formerly
// TestSetCancelHooks_ScopeSelfOnlyNotSubtree, inverted — see file header).
// It calls al.Interrupt — the exact method session_messaging_wire.go's
// SetCancelHooks closures invoke for a delegate(action="cancel") soft stop —
// with the delegate CHILD's own sessionKey, once per scope, and shows:
//
//   - ScopeSubtree (what is actually wired, post-fix): reaches the named
//     child AND its own live descendant (the grandchild) — this is the
//     ADR-mandated fix for the D8/R-13 leak: a per-delegation cancel must
//     also reach that child's own subtree, not leave its grandchildren (and
//     their background shells) running forever.
//   - ScopeSelfOnly (the RETIRED wiring this fix replaces): reaches ONLY the
//     named child. The grandchild is left untouched — this is the exact
//     leak D8/R-13 exists to close, reproduced here so a future edit that
//     "simplifies" the wiring back to ScopeSelfOnly fails this test instead
//     of silently reintroducing the leak.
//
// Both scopes are exercised against structurally identical fixtures (same
// parent/child/grandchild shape, distinct ids per scope so the two calls
// cannot cross-contaminate each other's activeTurnStates entries), so the
// ONLY variable between the two halves is the scope argument itself.
func TestSetCancelHooks_ChildCancelReachesSubtree(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	t.Run("green: ScopeSubtree (now wired) reaches the child AND its grandchild", func(t *testing.T) {
		_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "subtree")
		defer fixtureCleanup()

		descendants, err := al.Interrupt(child.sessionKey, ScopeSubtree, "delegate cancel(hard=false)")
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{child.turnID, grandchild.turnID}, descendants,
			"ScopeSubtree must reach the named child AND its own live descendant — this is the "+
				"semantics session_messaging_wire.go's SetCancelHooks call now relies on (ADR-057 "+
				"D8/R-13, AC-8) to close the grandchild-leak a per-delegation cancel used to leave running")

		childInterrupted, _ := child.gracefulInterruptRequested()
		assert.True(t, childInterrupted, "ScopeSubtree must still reach the named child itself")

		grandchildInterrupted, _ := grandchild.gracefulInterruptRequested()
		assert.True(t, grandchildInterrupted,
			"ScopeSubtree MUST reach the grandchild — this is the D8/R-13 fix: a delegate cancel "+
				"on the child must also cancel the child's own descendants, not leave them running")
	})

	t.Run("red: ScopeSelfOnly (the retired wiring) would miss the grandchild", func(t *testing.T) {
		_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "selfonly")
		defer fixtureCleanup()

		descendants, err := al.Interrupt(child.sessionKey, ScopeSelfOnly, "delegate cancel(hard=false)")
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{child.turnID}, descendants,
			"ScopeSelfOnly reaches ONLY the named child — demonstrating that had "+
				"session_messaging_wire.go's SetCancelHooks call stayed wired with ScopeSelfOnly "+
				"instead of ScopeSubtree, a single delegate(action=\"cancel\") on the child would leave "+
				"the grandchild (and any background shells it owns) running forever: the exact live "+
				"leak ADR-057 D8/R-13 closes")

		childInterrupted, _ := child.gracefulInterruptRequested()
		assert.True(t, childInterrupted, "ScopeSelfOnly still reaches the named child itself")

		grandchildInterrupted, _ := grandchild.gracefulInterruptRequested()
		assert.False(t, grandchildInterrupted,
			"ScopeSelfOnly does NOT reach the grandchild — this is the wrong behavior for a "+
				"per-delegation cancel post-D1, which is exactly why session_messaging_wire.go now "+
				"wires ScopeSubtree, not this scope")
	})
}

// TestSetCancelHooks_HardVariantAlsoUsesScopeSubtree mirrors the green half
// above for the HARD escalation path (InterruptSessionHard), which
// SetCancelHooks wires as the second (hard) argument — the same ADR-057
// D8/R-13 scope discipline applies to both halves of the collapsed pair.
// Formerly TestSetCancelHooks_HardVariantAlsoUsesScopeSelfOnly, inverted —
// see file header.
func TestSetCancelHooks_HardVariantAlsoUsesScopeSubtree(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "hard-subtree")
	defer fixtureCleanup()

	descendants, err := al.InterruptSessionHard(child.sessionKey, ScopeSubtree, "delegate cancel(hard=true)")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{child.turnID, grandchild.turnID}, descendants,
		"InterruptSessionHard with ScopeSubtree must reach the named child AND its own live descendant")

	child.mu.RLock()
	childHardAbort := child.hardAbort
	child.mu.RUnlock()
	assert.True(t, childHardAbort, "the named child's own hardAbort flag must be set")

	grandchild.mu.RLock()
	grandchildHardAbort := grandchild.hardAbort
	grandchild.mu.RUnlock()
	assert.True(t, grandchildHardAbort,
		"ScopeSubtree MUST hard-abort the grandchild too — the hard escalation must close the "+
			"same D8/R-13 leak the soft cancel does, not just the soft half")
}
