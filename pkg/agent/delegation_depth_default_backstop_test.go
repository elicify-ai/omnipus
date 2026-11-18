// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Pins for the SHIPPED delegation-depth default (founder ruling 2026-10-27:
// default 10). The explicit-config path has its own coverage in
// delegation_depth_integration_test.go; everything here exercises the
// unset-config path with LITERAL expected numbers, so a wrong backstop value
// or a boundary off-by-one fails these tests rather than silently inheriting
// whatever the constant says.

package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestDelegationShippedDepthDefault_IsTen pins defaultMaxSubTurnDepth to the
// literal 10 and the unset-config resolution to the same literal. The other
// mirrors are pinned separately in their own packages (unexported constants
// cannot be imported here): pkg/gateway, pkg/sysagent/tools, pkg/coreagent,
// pkg/tools and teamGraphModel.test.ts each carry their own pin.
func TestDelegationShippedDepthDefault_IsTen(t *testing.T) {
	if defaultMaxSubTurnDepth != 10 {
		t.Fatalf("defaultMaxSubTurnDepth = %d, want the literal 10 (founder ruling 2026-10-27)", defaultMaxSubTurnDepth)
	}
	if got := resolveEffectiveDelegationDepth(nil, 0); got != 10 {
		t.Fatalf("unset config resolves to %d, want the literal 10", got)
	}
}

// TestDelegationDepthBound_UnsetConfigIsTen pins the bound the bound-style
// consumers (the ownership walk, the task recursion ceiling) install: with
// performance.max_delegation_depth unset it must be the shipped default 10 —
// not the old 3, not any other value.
func TestDelegationDepthBound_UnsetConfigIsTen(t *testing.T) {
	if got := delegationDepthBound(config.PerformanceConfig{}, "test-unset-bound"); got != 10 {
		t.Fatalf("delegationDepthBound with unset config = %d, want the literal 10", got)
	}
}

// TestLaunch_UnsetDepthConfig_TenHopsSucceedEleventhRefused drives the real
// steer.SessionLauncher.Launch path (no mock, no private shortcut) with
// performance.max_delegation_depth UNSET: exactly 10 hops must succeed and
// the 11th must be refused with steer.ErrDepthExceeded, leaving no trace.
// Literal 10/11 on purpose — this is the boundary probe for the shipped
// default, mirroring TestLaunch_AtExhaustedDepthBudget_Refused (which covers
// the explicit-config path at cap 2). Self-delegation to testDefaultAgentID
// at every hop isolates the assertion to the depth arithmetic, exactly as in
// that test; the self-edge is seeded with a nil Depth (inherit) so the
// backstop is the only cap in play. The delegation_tree test helper is NOT
// used: it supports depths 1-3 and a 10-hop chain needs none of its
// fixtures — the launcher harness alone builds the real chain.
func TestLaunch_UnsetDepthConfig_TenHopsSucceedEleventhRefused(t *testing.T) {
	seedWorkspaceGraph(t, "01JXDEPTHDEFAULTBACKSTOP00001", true, []graphEdge{
		edge(testDefaultAgentID, testDefaultAgentID, nil, nil),
	})
	al, cleanup := newSteerAL(t)
	defer cleanup()
	if got := al.GetConfig().Performance.MaxDelegationDepth; got != 0 {
		t.Fatalf("fixture must leave performance.max_delegation_depth unset, got %d", got)
	}

	l := NewSteerLauncher(al)
	ctx := context.Background()

	root := newTestSteeringSession(t, al, "")
	if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: root, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, AgentID: testDefaultAgentID,
	}); err != nil {
		t.Fatalf("persist steerer lifecycle record: %v", err)
	}

	const wantHops = 10 // literal: the shipped default must allow exactly 10 hops
	steering := root
	for i := 1; i <= wantHops; i++ {
		res, err := l.Launch(ctx, steer.LaunchRequest{
			SteeringSessionID: steering,
			TargetAgentID:     testDefaultAgentID,
			Task:              fmt.Sprintf("hop %d", i),
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: fmt.Sprintf("call-hop-%d", i)},
		})
		if err != nil {
			t.Fatalf("Launch(hop %d) with the depth config unset: unexpected error (want %d successful hops): %v", i, wantHops, err)
		}
		steering = res.SessionID
	}

	// Boundary arithmetic: the 10th child carries RemainingDepth 0 — the
	// whole budget is spent, so one more hop has nothing left to inherit.
	lastRec, err := al.GetSessionLifecycleStore().Load(steering)
	if err != nil {
		t.Fatalf("load 10th child record: %v", err)
	}
	if lastRec.SteeredBy == nil || lastRec.SteeredBy.Authorization.RemainingDepth != 0 {
		got := -1
		if lastRec.SteeredBy != nil {
			got = lastRec.SteeredBy.Authorization.RemainingDepth
		}
		t.Fatalf("10th child RemainingDepth = %d, want 0 (the default budget is spent at hop 10)", got)
	}

	_, err = l.Launch(ctx, steer.LaunchRequest{
		SteeringSessionID: steering,
		TargetAgentID:     testDefaultAgentID,
		Task:              "hop 11",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-hop-11"},
	})
	if !errors.Is(err, steer.ErrDepthExceeded) {
		t.Fatalf("Launch(hop 11) with the depth config unset = %v, want steer.ErrDepthExceeded (the shipped default must cap at 10)", err)
	}

	// Leave no trace: the refused 11th hop must not persist a child record.
	children, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{SteeringSessionID: steering})
	if listErr != nil {
		t.Fatalf("list 10th child's children: %v", listErr)
	}
	if len(children) != 0 {
		t.Fatalf("10th hop has %d persisted child record(s) after the refused 11th launch, want 0", len(children))
	}
}
