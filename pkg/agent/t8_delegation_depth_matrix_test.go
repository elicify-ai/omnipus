// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// depthCtx builds a context whose turn state carries parentDepth (nil means no
// turn state at all) and whose tool context carries ctxDepth (nil means the
// depth was never seeded).
func depthCtx(parentDepth, ctxDepth *int) context.Context {
	ctx := context.Background()
	if parentDepth != nil {
		ts := newTurnState(&AgentInstance{ID: "omnipus"}, processOptions{SessionKey: "k"}, turnEventScope{turnID: "tid"})
		ts.depth = *parentDepth
		ctx = withTurnState(ctx, ts)
	}
	if ctxDepth != nil {
		ctx = tools.WithDelegationDepth(ctx, *ctxDepth)
	}
	return ctx
}

// TestCurrentDelegationDepth_OrderingMatrix pins what currentDelegationDepth
// returns for every ordering of the turn-state (parent) depth and the depth
// carried on the context: the larger wins; equal gives that value; an absent
// source counts as zero (r17 US-21 AS-3, scenario 21.3, developer deviation).
func TestCurrentDelegationDepth_OrderingMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent *int // nil = no turn state on the context
		chain  *int // nil = depth never seeded on the context
		want   int
	}{
		{"parent greater than context", ptr(3), ptr(1), 3},
		{"parent greater than unseeded context", ptr(2), nil, 2},
		{"context greater than parent", ptr(1), ptr(3), 3},
		{"context greater than a root turn at zero", ptr(0), ptr(2), 2},
		{"context greater than missing turn state", nil, ptr(2), 2},
		{"equal depths", ptr(2), ptr(2), 2},
		{"equal at zero", ptr(0), ptr(0), 0},
		{"zero root turn, unseeded context", ptr(0), nil, 0},
		{"no turn state, unseeded context", nil, nil, 0},
		{"explicit zero context, missing turn state", nil, ptr(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, currentDelegationDepth(depthCtx(tc.parent, tc.chain)))
		})
	}

	// Instrument check: the matrix is not vacuous. A reading of only one source
	// would give a different answer for at least one row in each direction.
	parentOnly := func(parent *int) int {
		if parent == nil {
			return 0
		}
		return *parent
	}
	chainOnly := func(chain *int) int {
		if chain == nil {
			return 0
		}
		return *chain
	}
	assert.NotEqual(t, parentOnly(ptr(1)), currentDelegationDepth(depthCtx(ptr(1), ptr(3))), "a parent-only reading would be wrong when the context is deeper")
	assert.NotEqual(t, chainOnly(ptr(1)), currentDelegationDepth(depthCtx(ptr(3), ptr(1))), "a context-only reading would be wrong when the parent is deeper")
}

// TestEnforceEdgeModeAndDepth_BoundaryRefusal pins the max-depth refusal at the
// boundary for both sources of the depth: with a depthCap of N a caller at depth
// N-1 is admitted and a caller at depth N (or deeper) is refused with the depth
// policy and the depthCap named in the reason.
func TestEnforceEdgeModeAndDepth_BoundaryRefusal(t *testing.T) {
	for _, depthCap := range []int{1, 2, 3} {
		edge := &workspace.DelegationEdge{FromAgent: "omnipus", ToAgent: "omnipus", Depth: ptr(depthCap)}
		for _, source := range []string{"turn state", "context", "both"} {
			seed := func(depth int) context.Context {
				switch source {
				case "turn state":
					return depthCtx(ptr(depth), nil)
				case "context":
					return depthCtx(nil, ptr(depth))
				default:
					return depthCtx(ptr(depth), ptr(depth))
				}
			}
			t.Run(fmt.Sprintf("depthCap %d via %s", depthCap, source), func(t *testing.T) {
				below := enforceEdgeModeAndDepth(seed(depthCap-1), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
				assert.Nil(t, below, "depth %d is below the depthCap and is admitted", depthCap-1)

				for _, depth := range []int{depthCap, depthCap + 1} {
					denial := enforceEdgeModeAndDepth(seed(depth), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
					require.NotNil(t, denial, "depth %d reaches the depthCap %d and is refused", depth, depthCap)
					assert.Equal(t, tools.DenyDepth, denial.Policy)
					assert.Equal(t, "omnipus", denial.TargetAgentID)
					assert.Contains(t, denial.Reason, fmt.Sprintf("maximum delegation depth (%d) reached", depthCap))
				}
			})
		}
	}

	t.Run("the deeper source decides at the boundary", func(t *testing.T) {
		edge := &workspace.DelegationEdge{FromAgent: "omnipus", ToAgent: "omnipus", Depth: ptr(2)}
		// Parent at the depthCap, context below it: refused.
		denial := enforceEdgeModeAndDepth(depthCtx(ptr(2), ptr(0)), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
		require.NotNil(t, denial)
		assert.Equal(t, tools.DenyDepth, denial.Policy)
		// Context at the depthCap, parent below it: refused.
		denial = enforceEdgeModeAndDepth(depthCtx(ptr(0), ptr(2)), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
		require.NotNil(t, denial)
		assert.Equal(t, tools.DenyDepth, denial.Policy)
		// Both just below the depthCap: admitted.
		assert.Nil(t, enforceEdgeModeAndDepth(depthCtx(ptr(1), ptr(1)), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0))
		// Neither source present: depth 0 is admitted under a depthCap of 2.
		assert.Nil(t, enforceEdgeModeAndDepth(depthCtx(nil, nil), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0))
	})

	t.Run("an edge that forbids onward delegation refuses even at depth zero", func(t *testing.T) {
		edge := &workspace.DelegationEdge{FromAgent: "omnipus", ToAgent: "omnipus", Depth: ptr(0)}
		denial := enforceEdgeModeAndDepth(depthCtx(nil, nil), edge, "omnipus", "omnipus", config.DelegationModeBackground, 0)
		require.NotNil(t, denial)
		assert.Equal(t, tools.DenyDepth, denial.Policy)
		assert.Contains(t, denial.Reason, "forbids onward delegation")
	})
}
