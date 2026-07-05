// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestResolveEffectiveDelegationDepth_SharedByPromptAndEnforcement pins every
// row of the "Effective depth cap resolution" dataset table in
// docs/internal/specs/agent-delegation-spec.md (#477, FR-D9/FR-D10). This is
// the ONE shared function enforceEdgeModeAndDepth, getSubTurnConfig, and
// wireDelegationInjectors all consult — no second, independently-maintained
// computation of this value may exist anywhere in the codebase.
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

// TestBuildDelegationDepthResolver_TargetedEdgeReturnsResolvedCap proves the
// resolver threaded into SubTurnConfig.ResolvedMaxDepth computes the SAME
// effective cap the delegation-graph gate (enforceEdgeModeAndDepth) already
// authorized this call against — the specific #477 regression: an explicit
// per-edge Depth (10) with the global config left unset must resolve to 10,
// not the backstop's own default of 3.
func TestBuildDelegationDepthResolver_TargetedEdgeReturnsResolvedCap(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	resolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})

	got := resolver(ctxWS(testWS, 4), "ray")
	if got == nil {
		t.Fatal("expected a resolved cap for a targeted, edge-authorized delegation, got nil (no override)")
	}
	if *got != 10 {
		t.Fatalf("expected resolved cap 10 (edge Depth=10, global unset), got %d", *got)
	}
}

// TestBuildDelegationDepthResolver_GlobalTightensEdge proves the resolver
// applies the SAME tighter-of-both logic as resolveEffectiveDelegationDepth
// when the global ceiling is stricter than the edge's own Depth.
func TestBuildDelegationDepthResolver_GlobalTightensEdge(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	defaults := config.AgentDefaults{}
	defaults.SubTurn.MaxDepth = 2
	resolver := buildDelegationDepthResolver("mia", defaults)

	got := resolver(ctxWS(testWS, 0), "ray")
	if got == nil || *got != 2 {
		t.Fatalf("expected resolved cap 2 (global stricter than edge's 10), got %v", got)
	}
}

// TestBuildDelegationDepthResolver_UntargetedReturnsNilOverride proves the
// untargeted path (no explicit agent_id) returns nil — no single edge's Depth
// uniquely applies, so spawnSubTurn falls back to its own shared-function
// default resolution (getSubTurnConfig).
func TestBuildDelegationDepthResolver_UntargetedReturnsNilOverride(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	resolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})

	if got := resolver(ctxWS(testWS, 0), ""); got != nil {
		t.Fatalf("untargeted delegation must return nil (no override), got %d", *got)
	}
}

// TestBuildDelegationDepthResolver_SelfAssignmentReturnsNilOverride proves
// self-assignment (target == caller) — not delegation, no graph edge
// consulted — returns nil (no override).
func TestBuildDelegationDepthResolver_SelfAssignmentReturnsNilOverride(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	resolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})

	if got := resolver(ctxWS(testWS, 0), "mia"); got != nil {
		t.Fatalf("self-assignment must return nil (no override), got %d", *got)
	}
}

// TestBuildDelegationDepthResolver_NoEdgeReturnsNilOverride proves that when
// no authorizing edge exists (a call the deny checker would already have
// rejected), the resolver fails safe and returns nil rather than fabricating
// a cap.
func TestBuildDelegationDepthResolver_NoEdgeReturnsNilOverride(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	resolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})

	// No mia→ava edge in the seeded graph.
	if got := resolver(ctxWS(testWS, 0), "ava"); got != nil {
		t.Fatalf("no authorizing edge must return nil (no override), got %d", *got)
	}
}
