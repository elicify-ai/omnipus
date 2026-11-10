// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

import "testing"

// TestFreshSelfDelegationMaxDepthPin_IsTen pins the ADR-090 FR-006 fresh
// self-edge pin to the literal 10 (founder ruling 2026-10-27). The constant
// is exported, but the pin lives here so a moved value fails in the package
// that owns it, next to SeededEdgeDepth.
func TestFreshSelfDelegationMaxDepthPin_IsTen(t *testing.T) {
	if FreshSelfDelegationMaxDepth != 10 {
		t.Fatalf("FreshSelfDelegationMaxDepth = %d, want the literal 10 (founder ruling 2026-10-27)", FreshSelfDelegationMaxDepth)
	}
}
