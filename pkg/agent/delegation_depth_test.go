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
			name:          "row1_both_unset_falls_back_to_safety_backstop",
			edgeDepth:     nil,
			globalMax:     0,
			wantEffective: defaultMaxSubTurnDepth, // 3
		},
		{
			name:          "row2_edge_nil_global_7_global_governs",
			edgeDepth:     nil,
			globalMax:     7,
			wantEffective: 7,
		},
		{
			name:          "row3_edge_10_global_unset_edge_governs_the_477_bug",
			edgeDepth:     intPtr(10),
			globalMax:     0,
			wantEffective: 10,
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
