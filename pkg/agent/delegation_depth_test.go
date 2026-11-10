// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"testing"
)

// TestResolveEffectiveDelegationDepth_SharedByPromptAndEnforcement pins every
// row of the "Effective depth cap resolution" dataset table in
// docs/internal/specs/agent-delegation-spec.md (#477, FR-D9/FR-D10). This is
// the ONE shared function the edge gate, launcher, and prompt builder consult —
// no second, independently-maintained computation may exist.
func TestResolveEffectiveDelegationDepth_SharedByPromptAndEnforcement(t *testing.T) {
	tests := []struct {
		name          string
		edgeDepth     *int
		globalMax     int
		wantEffective int
	}{
		{
			// An edge with no depth AND no global setting resolves to the
			// shipped backstop — 10 since the founder ruling of 2026-10-27
			// (it was 3). Pinned as a LITERAL, not as the constant, so a
			// wrong defaultMaxSubTurnDepth fails here instead of silently
			// redefining the expectation.
			name:          "row1_both_unset_falls_back_to_safety_backstop",
			edgeDepth:     nil,
			globalMax:     0,
			wantEffective: 10,
		},
		{
			name:          "row2_edge_nil_global_7_global_governs",
			edgeDepth:     nil,
			globalMax:     7,
			wantEffective: 7,
		},
		{
			// Row 3 is the #477 row: an explicit per-edge depth DEEPER than
			// the default backstop must be honoured. The per-edge value is 12
			// — strictly above the shipped backstop (10) — so this row still
			// discriminates "the edge governs" from "the backstop governed"
			// (with both at 10 the row would pass either way).
			name:          "row3_edge_12_global_unset_edge_governs_the_477_bug",
			edgeDepth:     intPtr(12),
			globalMax:     0,
			wantEffective: 12,
		},
		{
			name:          "row4_edge_2_global_7_edge_is_stricter",
			edgeDepth:     intPtr(2),
			globalMax:     7,
			wantEffective: 2,
		},
		{
			name:          "row5_edge_10_global_2_global_is_stricter",
			edgeDepth:     intPtr(10),
			globalMax:     2,
			wantEffective: 2,
		},
		// Row 6 (edge.Depth=0, forbids ALL onward delegation) is NOT resolved by
		// this function — enforceEdgeModeAndDepth's dedicated depth<=0 guard
		// denies unconditionally BEFORE this function would ever be consulted.
		// See TestDelegationDenyChecker_EdgeDepthZeroForbidsOnward
		// (delegation_enforce_test.go) for that guard's own coverage.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEffectiveDelegationDepth(tt.edgeDepth, tt.globalMax)
			if got != tt.wantEffective {
				t.Errorf("resolveEffectiveDelegationDepth(edge=%s, global=%d) = %d, want %d",
					formatIntPtrForTest(tt.edgeDepth), tt.globalMax, got, tt.wantEffective)
			}
		})
	}
}

// formatIntPtrForTest renders a *int as "nil" or its dereferenced value, for
// readable table-test failure messages.
func formatIntPtrForTest(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *p)
}
