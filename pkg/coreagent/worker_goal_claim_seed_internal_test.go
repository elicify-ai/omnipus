// Omnipus — the Worker seed's own goal_claim value.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent

import "testing"

// TestCoreAgentSeed_WorkerGoalClaimAllow reads the Worker's seed map directly,
// not through SeedConfig. SeedConfig also runs applyWorkerGoalClaimAllowUpdate,
// which would turn a seeded deny back into allow on a fresh install and hide
// a regression in the seed value itself. The seed must say allow on its own
// (founder decision 2026-09-15, issue #710).
func TestCoreAgentSeed_WorkerGoalClaimInheritsCeiling(t *testing.T) {
	got, present := coreAgentSeed(IDWorker)["goal_claim"]
	if present {
		t.Fatalf("the Worker's sparse seed goal_claim = %q, want absent because it matches the ceiling", got)
	}
}
