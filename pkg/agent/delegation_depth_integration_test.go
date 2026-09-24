// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix-lane 7 (Gap 1): the delegation depth limit's refusal had NO
// test anywhere — `grep -rn "ErrDepthExceeded" --include="*_test.go"`
// returned nothing before this file. This matters more under ADR-091 than
// before: the founder's D9 decision removed refusal from the CONCURRENCY
// gate (a launch at the cap queues, it is never refused), so the depth cap
// is now the ONLY bound on recursion. An off-by-one here no longer produces
// a bounded pile of in-memory sub-turns — it produces unbounded creation of
// REAL sessions, each with a record, transcript, inbox and queue entry on
// disk. These tests drive the real steer.SessionLauncher.Launch path (no
// mock, no private-function shortcut) against a real AgentLoop.
package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestLaunch_AtExhaustedDepthBudget_Refused drives root -> A -> B -> (C) with
// MaxDelegationDepth = 2: the first two hops must succeed, and the third
// (which would create a fourth session, one hop past the configured cap)
// must be refused with steer.ErrDepthExceeded — and must leave no trace: no
// lifecycle record for a fourth session, and no admission slot consumed.
//
// Self-delegation (testDefaultAgentID -> testDefaultAgentID at every hop) is
// deliberate: ADR-091 explicitly allows a session to delegate to its own
// agent profile, and using one agent throughout isolates the assertion to
// the depth arithmetic alone, with no workspace delegation-graph edges in
// the way.
func TestLaunch_AtExhaustedDepthBudget_Refused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	// D9's single source of truth for the global depth ceiling (config.go's
	// PerformanceConfig.MaxDelegationDepth) — read live by
	// startingRemainingDepth on every Launch call, so setting it after
	// construction still governs.
	al.GetConfig().Performance.MaxDelegationDepth = 2

	l := NewSteerLauncher(al)
	ctx := context.Background()

	root := newTestSteeringSession(t, al, "")

	resA, err := l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: root, TargetAgentID: testDefaultAgentID, Task: "do A",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-a"},
	})
	if err != nil {
		t.Fatalf("Launch(root->A) at depth budget 2: unexpected error: %v", err)
	}

	resB, err := l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: resA.SessionID, TargetAgentID: testDefaultAgentID, Task: "do B",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-b"},
	})
	if err != nil {
		t.Fatalf("Launch(A->B) at depth budget 2: unexpected error: %v", err)
	}

	_, err = l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: resB.SessionID, TargetAgentID: testDefaultAgentID, Task: "do C",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-c"},
	})
	if !errors.Is(err, steer.ErrDepthExceeded) {
		t.Fatalf("Launch(B->C) once the depth budget (2) is exhausted = %v, want steer.ErrDepthExceeded", err)
	}

	lifecycle := al.GetSessionLifecycleStore()
	children, listErr := lifecycle.List(session.LifecycleFilter{SteeringSessionID: resB.SessionID})
	if listErr != nil {
		t.Fatalf("list B's children: %v", listErr)
	}
	if len(children) != 0 {
		t.Fatalf("B has %d persisted child record(s) after a refused launch, want 0 — "+
			"C's admission slot must never be taken when the depth budget is exhausted", len(children))
	}
}

// TestLaunch_GrandchildRemainingDepth_InheritsFromParentBudget_NotRecomputedFromGlobalCap
// proves the sibling-case regression named in the Gap-1 brief: a grandchild's
// stored RemainingDepth is derived from its own parent's ACTUAL persisted
// remaining budget, never recomputed fresh from (global cap - ancestor
// distance). The global cap alone is deliberately generous (10) here so a
// "recomputed from the cap" bug would NOT be caught by the exhaustion test
// above (that test would still pass even if this specific inheritance step
// were broken, because both compute the SAME number when nothing tightens
// the budget mid-chain) — this test manufactures exactly that mid-chain
// tightening. A can only get a tight budget (1) by way of a stricter
// per-edge cap somewhere upstream (the normal production mechanism); this
// test reproduces "A's true remaining budget is smaller than the global cap
// would suggest" by mutating A's OWN persisted record directly (real
// on-disk shape, same field steer_launcher.go reads) rather than wiring a
// second delegation edge — isolating the assertion to
// startingRemainingDepth's inheritance step alone.
func TestLaunch_GrandchildRemainingDepth_InheritsFromParentBudget_NotRecomputedFromGlobalCap(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	l := NewSteerLauncher(al)
	ctx := context.Background()
	lifecycle := al.GetSessionLifecycleStore()

	root := newTestSteeringSession(t, al, "")
	resA, err := l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: root, TargetAgentID: testDefaultAgentID, Task: "do A",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-a"},
	})
	if err != nil {
		t.Fatalf("Launch(root->A): unexpected error: %v", err)
	}

	// Manufacture "A's true remaining budget is 1" — reachable in production
	// via a stricter per-edge cap somewhere upstream of A; constructed
	// directly here to isolate the assertion. A is one hop below root, so a
	// buggy "recompute from the global cap" implementation would instead
	// compute (globalCap=10 - parentDepth=1) = 9 for B's budget.
	aRec, err := lifecycle.Load(resA.SessionID)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	aRec.SteeredBy.Authorization.RemainingDepth = 1
	if persistErr := lifecycle.Persist(aRec); persistErr != nil {
		t.Fatalf("persist tightened A: %v", persistErr)
	}

	resB, err := l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: resA.SessionID, TargetAgentID: testDefaultAgentID, Task: "do B",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-b"},
	})
	if err != nil {
		t.Fatalf("Launch(A->B) with A's tightened budget: unexpected error: %v", err)
	}

	bRec, err := lifecycle.Load(resB.SessionID)
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	if bRec.SteeredBy == nil {
		t.Fatal("B has no SteeredBy edge")
	}
	const wantFromParentBudget = 0       // A's RemainingDepth(1) - 1
	const wouldBeIfRecomputedFromCap = 8 // (globalCap=10 - parentDepth(A)=1) - 1
	if bRec.SteeredBy.Authorization.RemainingDepth != wantFromParentBudget {
		if bRec.SteeredBy.Authorization.RemainingDepth == wouldBeIfRecomputedFromCap {
			t.Fatalf("B.RemainingDepth = %d — recomputed from the GLOBAL cap and A's ancestor "+
				"distance, ignoring A's own tighter persisted budget (want %d, inherited from "+
				"A.RemainingDepth=1)", bRec.SteeredBy.Authorization.RemainingDepth, wantFromParentBudget)
		}
		t.Fatalf("B.RemainingDepth = %d, want %d (A.RemainingDepth(1) - 1)",
			bRec.SteeredBy.Authorization.RemainingDepth, wantFromParentBudget)
	}
}
