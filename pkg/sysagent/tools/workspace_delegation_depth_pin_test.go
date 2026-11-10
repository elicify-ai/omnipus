// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import "testing"

// TestWorkspaceDelegationDepthCeilingFallbackPin_IsTen pins this package's
// mirror of the shipped delegation-depth backstop to the literal 10 (founder
// ruling 2026-10-27). pkg/sysagent/tools cannot import pkg/agent's unexported
// constant, so the mirror is pinned here, in the package that owns it.
func TestWorkspaceDelegationDepthCeilingFallbackPin_IsTen(t *testing.T) {
	if workspaceDelegationDepthCeilingFallback != 10 {
		t.Fatalf("workspaceDelegationDepthCeilingFallback = %d, want the literal 10 (founder ruling 2026-10-27)", workspaceDelegationDepthCeilingFallback)
	}
}
