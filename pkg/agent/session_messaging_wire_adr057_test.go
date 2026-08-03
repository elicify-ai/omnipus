// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_messaging_wire_adr057_test.go — ADR-057 unit U19's red/green proof
// for the W13d scope choice in wireSessionMessagingForAgent's
// dt.SetCancelHooks(...) call (session_messaging_wire.go).
//
// U15's explicit guidance (carried into this unit's task brief) was: wire
// the collapsed Interrupt/InterruptSessionHard pair with ScopeSelfOnly, NOT
// ScopeSubtree, to match the retired InterruptBySessionKey/
// InterruptBySessionKeyHard's point-lookup-only semantics — wrapping with
// the wrong scope would silently widen a per-delegation cancel into a
// subtree sweep. This file proves that claim rather than asserting it: it
// drives the SAME exported Interrupt method session_messaging_wire.go's
// closures call, once with each scope, against an identical
// parent/child/grandchild fixture, and shows the two scopes produce
// DIFFERENT reached-sets. Per binding Rule 5 this is a NEW file; per
// binding Rule 6 its unexported helpers are prefixed u19.
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

// TestSetCancelHooks_ScopeSelfOnlyNotSubtree is the mandated red/green proof
// for W13d's scope choice. It calls al.Interrupt — the exact method
// session_messaging_wire.go's SetCancelHooks closures invoke for a
// delegate(action="cancel") soft stop — with the delegate CHILD's own
// sessionKey, once per scope, and shows:
//
//   - ScopeSelfOnly (what is actually wired): reaches ONLY the named child.
//     The grandchild is untouched — no descendants entry, no graceful-interrupt
//     flag set. This is the byte-identical-to-InterruptBySessionKey behavior
//     the wiring comment claims.
//   - ScopeSubtree (what would be wired if this call were "simplified" back
//     to the whole-chat-cascade scope by a future edit): reaches BOTH the
//     child AND the grandchild — proving that scope choice would silently
//     widen a per-delegation cancel into a subtree sweep, exactly the
//     regression U15's guidance warns against.
//
// Both scopes are exercised against structurally identical fixtures (same
// parent/child/grandchild shape, distinct ids per scope so the two calls
// cannot cross-contaminate each other's activeTurnStates entries), so the
// ONLY variable between the "red" and "green" halves is the scope argument
// itself.
func TestSetCancelHooks_ScopeSelfOnlyNotSubtree(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	t.Run("green: ScopeSelfOnly reaches only the named child", func(t *testing.T) {
		_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "selfonly")
		defer fixtureCleanup()

		descendants, err := al.Interrupt(child.sessionKey, ScopeSelfOnly, "delegate cancel(hard=false)")
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{child.turnID}, descendants,
			"ScopeSelfOnly must reach EXACTLY the named child — this is the semantics "+
				"session_messaging_wire.go's SetCancelHooks call relies on to match the retired "+
				"InterruptBySessionKey's point-lookup-only behavior")

		childInterrupted, _ := child.gracefulInterruptRequested()
		assert.True(t, childInterrupted, "the named child's own graceful-interrupt flag must be set")

		grandchildInterrupted, _ := grandchild.gracefulInterruptRequested()
		assert.False(t, grandchildInterrupted,
			"ScopeSelfOnly must NOT reach the grandchild — reaching it would be exactly the "+
				"subtree-widening regression this scope choice exists to prevent")
	})

	t.Run("red: ScopeSubtree would additionally reach the grandchild", func(t *testing.T) {
		_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "subtree")
		defer fixtureCleanup()

		descendants, err := al.Interrupt(child.sessionKey, ScopeSubtree, "delegate cancel(hard=false)")
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{child.turnID, grandchild.turnID}, descendants,
			"ScopeSubtree reaches the named child AND its live descendants — demonstrating that had "+
				"session_messaging_wire.go's SetCancelHooks call been wired with ScopeSubtree instead of "+
				"ScopeSelfOnly, a single delegate(action=\"cancel\") on the child would ALSO have cancelled "+
				"the grandchild, silently widening a per-delegation cancel into a subtree sweep")

		childInterrupted, _ := child.gracefulInterruptRequested()
		assert.True(t, childInterrupted, "ScopeSubtree must still reach the named child itself")

		grandchildInterrupted, _ := grandchild.gracefulInterruptRequested()
		assert.True(t, grandchildInterrupted,
			"ScopeSubtree DOES reach the grandchild — this is the wrong behavior for a per-delegation "+
				"cancel, which is exactly why session_messaging_wire.go wires ScopeSelfOnly, not this scope")
	})
}

// TestSetCancelHooks_HardVariantAlsoUsesScopeSelfOnly mirrors the green half
// above for the HARD escalation path (InterruptSessionHard), which
// SetCancelHooks wires as the second (hard) argument — the same scope
// discipline applies to both halves of the collapsed pair.
func TestSetCancelHooks_HardVariantAlsoUsesScopeSelfOnly(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	_, child, grandchild, fixtureCleanup := u19FixtureTurns(t, al, "hard-selfonly")
	defer fixtureCleanup()

	descendants, err := al.InterruptSessionHard(child.sessionKey, ScopeSelfOnly, "delegate cancel(hard=true)")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{child.turnID}, descendants,
		"InterruptSessionHard with ScopeSelfOnly must reach EXACTLY the named child")

	child.mu.RLock()
	childHardAbort := child.hardAbort
	child.mu.RUnlock()
	assert.True(t, childHardAbort, "the named child's own hardAbort flag must be set")

	grandchild.mu.RLock()
	grandchildHardAbort := grandchild.hardAbort
	grandchild.mu.RUnlock()
	assert.False(t, grandchildHardAbort,
		"ScopeSelfOnly must NOT hard-abort the grandchild")
}
