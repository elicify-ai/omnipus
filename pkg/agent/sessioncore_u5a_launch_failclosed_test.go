// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// U5A launch fail-closed regression pack (security finding 2, conditions 2-4).
//
// pkg/agent/steer_launcher.go::startingRemainingDepth used to SWALLOW a
// workspace.ReadDelegation error (`if edges, err := ...; err == nil`) and fall
// back to the GLOBAL-derived depth cap. With a global cap above 3 that discards
// the seeded self pin, so an unreadable graph made the launcher MORE permissive
// exactly when it could not verify the caller→target edge — authority silently
// widened between the authorization gate and the launch. This pack pins the
// fail-closed posture:
//
//	cond 2 — an UNREADABLE graph refuses the launch (no child, no global budget);
//	cond 3 — a MISSING edge refuses a Delegate-origin launch, while a Task-origin
//	         self-reassignment is EXEMPT (self-assignment is not graph-gated);
//	cond 4 — an UNBOUND turn reads the SAME resolved (is_default) workspace the
//	         gate would, instead of skipping the graph read.
//
// Every test drives the REAL steer.SessionLauncher.Launch path (no mock, no
// private-function shortcut) against a real AgentLoop, mirroring
// delegation_depth_integration_test.go.
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// corruptDelegationStore writes a delegation-store record whose workspace_id
// disagrees with its filename. loadDelegationStore refuses to trust such a
// record, so workspace.ReadDelegation returns a hard error — an UNREADABLE
// graph (the exact state the gate treats as a closed graph).
func corruptDelegationStore(t *testing.T, home, wsID string) {
	t.Helper()
	path, err := workspace.DelegationStorePath(home, wsID)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		t.Fatalf("mkdir delegation store: %v", mkErr)
	}
	bad := []byte(`{"workspace_id":"some-other-workspace","delegation":[{"from_agent":"mia","to_agent":"mia"}]}`)
	if wErr := os.WriteFile(path, bad, 0o600); wErr != nil {
		t.Fatalf("write corrupt delegation store: %v", wErr)
	}
}

// childrenOf counts the lifecycle children of a steering session.
func childrenOf(t *testing.T, al *AgentLoop, steeringSessionID string) int {
	t.Helper()
	lifecycle := al.GetSessionLifecycleStore()
	kids, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: steeringSessionID})
	if err != nil {
		t.Fatalf("list children of %q: %v", steeringSessionID, err)
	}
	return len(kids)
}

// u5aCallerAgentID is the steering session's delegating-agent identity. It is
// deliberately DISTINCT from testDefaultAgentID (the target): a steering
// session is created owned by this caller (newTestSteeringSessionOwnedBy), the
// immutable owner a launch reads as its delegating agent.
const u5aCallerAgentID = "u5a-seed-delegate-caller"

// TestLaunch_UnreadableGraph_DelegateOrigin_RefusesLaunchNoGlobalBudget is
// condition 2: with the workspace's delegation graph unreadable, a Delegate
// launch must be REFUSED and no child session minted — never granted the
// global-derived budget (set deliberately generous here, so a fallback would
// have granted a large remaining budget).
func TestLaunch_UnreadableGraph_DelegateOrigin_RefusesLaunchNoGlobalBudget(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(u5aCallerAgentID, testDefaultAgentID, nil, nil),
	})
	corruptDelegationStore(t, home, testWS)

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, testWS, u5aCallerAgentID)

	_, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-unreadable"},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("unreadable delegation graph on a Delegate launch = %v, want steer.ErrInvalidEdge (fail closed)", err)
	}
	if n := childrenOf(t, al, steerer); n != 0 {
		t.Fatalf("refused launch persisted %d child lifecycle record(s), want 0 — "+
			"an unverifiable graph must never mint a child on a global budget", n)
	}
}

// TestLaunch_MissingEdge_DelegateOrigin_RefusesLaunch is condition 3's
// Delegate arm: the graph is readable but carries NO caller→target edge. A
// Delegate launch reached the launcher only because the gate found an edge, so
// a missing one here means the graph changed — refuse, do not widen.
func TestLaunch_MissingEdge_DelegateOrigin_RefusesLaunch(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "worker", nil, nil), // a real edge, but not caller→target
	})

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, testWS, u5aCallerAgentID)

	_, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-missing-edge"},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("Delegate launch with no caller→target edge = %v, want steer.ErrInvalidEdge (fail closed)", err)
	}
	if n := childrenOf(t, al, steerer); n != 0 {
		t.Fatalf("refused launch persisted %d child lifecycle record(s), want 0", n)
	}
}

// TestLaunch_MissingEdge_TaskOrigin_Exempt is condition 3's exemption arm:
// a task self-reassignment is deliberately NOT graph-gated — assigning a task
// to oneself needs no edge — so a Task-origin launch with no matching edge must
// still succeed on the global-derived cap. Requiring an edge here would break
// tasks.
func TestLaunch_MissingEdge_TaskOrigin_Exempt(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "worker", nil, nil), // no caller→target edge
	})

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 5

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, testWS, u5aCallerAgentID)

	res, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-1", CallID: "call-task"},
	})
	if err != nil {
		t.Fatalf("Task-origin launch with no edge must be EXEMPT (self-reassignment is not "+
			"graph-gated), got: %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("expected a child session for the exempt Task-origin launch")
	}
	if n := childrenOf(t, al, steerer); n != 1 {
		t.Fatalf("exempt Task-origin launch persisted %d child record(s), want 1", n)
	}
}

// TestLaunch_UnboundTurn_ReadsResolvedDefaultGraph is condition 4: an UNBOUND
// turn (empty workspace on the context AND on the steering session) must read
// the SAME resolved workspace the gate would — the is_default workspace —
// rather than skipping the graph read. The default graph's self-edge is pinned
// TIGHT (depth 1) while the global cap is generous (10): if the read were
// skipped the child would inherit the global budget (RemainingDepth 9), so a
// child RemainingDepth of 0 proves the resolved default graph was actually read.
func TestLaunch_UnboundTurn_ReadsResolvedDefaultGraph(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(u5aCallerAgentID, testDefaultAgentID, nil, intPtr(1)),
	})

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, "", u5aCallerAgentID) // UNBOUND steering session

	res, err := l.Launch(context.Background(), steer.LaunchRequest{ // no ctx workspace
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-unbound"},
	})
	if err != nil {
		t.Fatalf("unbound Delegate launch must resolve the is_default graph, not skip it: %v", err)
	}
	rec, loadErr := al.GetSessionLifecycleStore().Load(res.SessionID)
	if loadErr != nil {
		t.Fatalf("load child record: %v", loadErr)
	}
	if rec.SteeredBy == nil {
		t.Fatal("child has no SteeredBy edge")
	}
	got := rec.SteeredBy.Authorization.RemainingDepth
	if got != 0 {
		t.Fatalf("unbound-turn child RemainingDepth = %d, want 0 (edge depth 1 pins it); "+
			"9 would mean the graph read was SKIPPED and the global cap (10) used instead", got)
	}
}
