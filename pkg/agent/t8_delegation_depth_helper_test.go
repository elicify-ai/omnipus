// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestCurrentDelegationDepth_HelperChainDepthIsEnforced covers r17 US-21 AS-3
// and scenario 21.3. A helper or task session runs its own root turn (turnState
// depth 0) and carries its chain depth on the context, so the gate must use
// the larger of the two. Before the change currentDelegationDepth read only
// the turn state, so a depth-1 helper looked like a top-level caller and was
// admitted by the gate.
func TestCurrentDelegationDepth_HelperChainDepthIsEnforced(t *testing.T) {
	agent := &AgentInstance{ID: "omnipus"}
	rootTurn := func() *turnState {
		return newTurnState(agent, processOptions{SessionKey: "k"}, turnEventScope{turnID: "tid"})
	}
	// One onward step allowed from a top-level caller: the default shape of a
	// self-delegation edge (depth cap 1, no chains).
	edge := &workspace.DelegationEdge{FromAgent: "omnipus", ToAgent: "omnipus", Depth: ptr(1)}

	t.Run("top level caller at depth 0 is admitted", func(t *testing.T) {
		ctx := withTurnState(context.Background(), rootTurn())
		require.Equal(t, 0, currentDelegationDepth(ctx))
		denial := enforceEdgeModeAndDepth(ctx, edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
		assert.Nil(t, denial, "depth 0 is unaffected by the helper rule")
	})

	t.Run("helper at depth 1 on a depth 0 root turn is refused with the depth denial", func(t *testing.T) {
		ctx := withTurnState(context.Background(), rootTurn())
		ctx = tools.WithDelegationDepth(ctx, 1)
		require.Equal(t, 1, currentDelegationDepth(ctx), "the chain depth on ctx wins over turn depth 0")
		denial := enforceEdgeModeAndDepth(ctx, edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
		require.NotNil(t, denial, "a helper may not delegate onward (no chains)")
		assert.Equal(t, tools.DenyDepth, denial.Policy)
		assert.Contains(t, denial.Reason, "maximum delegation depth (1) reached")

		// Control: the turn-state-only reading the old code used admits the same
		// caller, so this test fails against it instead of passing vacuously.
		old := enforceEdgeModeAndDepthAt(edge, "omnipus", "omnipus", config.DelegationModeBackground, 0, rootTurn().depth)
		assert.Nil(t, old, "reading only turnState depth 0 would have admitted the helper")
	})

	t.Run("the larger of chain depth and turn depth is used", func(t *testing.T) {
		ts := rootTurn()
		ts.depth = 2
		ctx := tools.WithDelegationDepth(withTurnState(context.Background(), ts), 1)
		assert.Equal(t, 2, currentDelegationDepth(ctx))
		ctx = tools.WithDelegationDepth(withTurnState(context.Background(), ts), 3)
		assert.Equal(t, 3, currentDelegationDepth(ctx))
	})

	t.Run("no turn state falls back to the chain depth alone", func(t *testing.T) {
		assert.Equal(t, 0, currentDelegationDepth(context.Background()))
		assert.Equal(t, 1, currentDelegationDepth(tools.WithDelegationDepth(context.Background(), 1)))
	})
}
