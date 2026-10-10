// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// U5A-FIX-R1 regression pack (security re-verification of 60e299a86).
//
// F1 — startingRemainingDepth ignored a RESOLUTION failure of the governing
// workspace. An unbound delegate launch whose default workspace could not be
// re-resolved fell through the `if denial == nil` branch and kept the
// GLOBAL-derived budget, so a child could be published at remaining depth 9
// (global cap 10) instead of the tighter edge depth 2, WITHOUT the
// caller→target edge ever being verified. The fix fails closed for every
// graph-gated launch — a Delegate launch, and a task-origin launch to another
// agent — when the workspace cannot be resolved or its graph cannot be read.
//
// N2 — the graph-read refusal must NOT regress the ruled task-self-reassignment
// exemption: a task-origin launch back to the steering session's OWN agent
// (caller == target) needs no delegation edge, so an UNREADABLE graph must not
// refuse it. No blanket other-agent exemption: the delegate origin, and a
// task-origin launch to a DIFFERENT agent, still refuse.
//
// Every test drives the REAL steer.SessionLauncher.Launch path against a real
// AgentLoop, mirroring sessioncore_u5a_launch_failclosed_test.go.
package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// newSelfSteeringSession creates a chat session OWNED by testDefaultAgentID,
// so a launch to testDefaultAgentID is a genuine caller==target
// self-reassignment (the launcher reads the immutable owner, Session.agent_id).
func newSelfSteeringSession(t *testing.T, al *AgentLoop, workspaceID string) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession(self-steerer): %v", err)
	}
	if workspaceID != "" {
		if err := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID}); err != nil {
			t.Fatalf("SetMeta(self-steerer).WorkspaceID: %v", err)
		}
	}
	return meta.ID
}

// TestLaunch_DefaultResolutionRace_DelegateOrigin_RefusesLaunch is F1: the gate
// authorizes an unbound delegate launch against the is_default workspace's
// graph; before the launcher re-resolves that same workspace, the default
// vanishes. The launcher's second resolution fails, and a Delegate launch MUST
// refuse (no child, no global budget) rather than fall through to the
// global-derived cap. The gate-then-launch ordering is deterministic — no
// sleeps, no hooks: the gate call proves the launch WAS authorized, and the
// file removal makes the launcher's own re-resolution fail.
func TestLaunch_DefaultResolutionRace_DelegateOrigin_RefusesLaunch(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(u5aCallerAgentID, testDefaultAgentID, nil, intPtr(2)),
	})

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	unbound := context.Background()

	// The FIRST gate: an unbound turn resolves the is_default workspace and the
	// caller→target edge exists, so the delegate is authorized. Passes only
	// because the default workspace is still on disk.
	gate := buildDelegationDenyCheckerForDelegate(
		u5aCallerAgentID, al.GetConfig().Performance, config.DelegationModeBackground)
	if d := gate(unbound, testDefaultAgentID); d != nil {
		t.Fatalf("precondition: the gate must authorize the delegate before the race, got denial %q (%s)", d.Reason, d.Policy)
	}

	// The RACE: the default workspace is gone before the launcher re-resolves
	// it. resolveEffectiveWorkspaceID now returns a denial.
	if rmErr := os.Remove(filepath.Join(home, "workspaces", testWS+".json")); rmErr != nil {
		t.Fatalf("simulate default resolution loss: %v", rmErr)
	}

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, "", u5aCallerAgentID) // UNBOUND steering session

	_, err := l.Launch(unbound, steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-resolution-race"},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("Delegate launch whose default workspace no longer resolves = %v, want steer.ErrInvalidEdge "+
			"(fail closed — never a global-derived budget)", err)
	}
	if n := childrenOf(t, al, steerer); n != 0 {
		t.Fatalf("refused launch persisted %d child lifecycle record(s), want 0 — "+
			"an unresolvable governing workspace must never mint a child on a global budget", n)
	}
}

// TestLaunch_UnreadableGraph_TaskSelfReassignment_Exempt is N2: an agent
// reassigns a task to ITSELF (caller == target) from an active steering session
// whose workspace delegation graph is UNREADABLE. That operation needs no
// delegation edge, so the graph-read refusal must NOT fire; the launch keeps
// the global/inherited task budget. The existing
// TestLaunch_MissingEdge_TaskOrigin_Exempt uses a READABLE graph and distinct
// ids, so it never exercised this branch.
func TestLaunch_UnreadableGraph_TaskSelfReassignment_Exempt(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(testDefaultAgentID, testDefaultAgentID, nil, intPtr(1)),
	})
	corruptDelegationStore(t, home, testWS)

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 5

	l := NewSteerLauncher(al)
	// The steering session's ACTIVE agent is testDefaultAgentID and the launch
	// TARGET is the same agent, so caller == target — a genuine task
	// self-reassignment.
	steerer := newSelfSteeringSession(t, al, testWS)

	res, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-self-1", CallID: "call-task-self"},
	})
	if err != nil {
		t.Fatalf("a task self-reassignment (caller==target) must NOT be refused by the graph-read "+
			"refusal (the ruled exemption): %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("expected a child session for the exempt task self-reassignment launch")
	}
	if n := childrenOf(t, al, steerer); n != 1 {
		t.Fatalf("exempt task self-reassignment persisted %d child record(s), want 1", n)
	}
	// The exemption retains the GLOBAL/inherited task budget: global cap 5,
	// parent depth 0 actions => the child's remaining depth is 5-1 = 4.
	rec, loadErr := al.GetSessionLifecycleStore().Load(res.SessionID)
	if loadErr != nil {
		t.Fatalf("load child record: %v", loadErr)
	}
	if rec.SteeredBy == nil {
		t.Fatal("child has no SteeredBy edge")
	}
	if got := rec.SteeredBy.Authorization.RemainingDepth; got != 4 {
		t.Fatalf("exempt task-self child RemainingDepth = %d, want 4 (global cap 5, no edge to read)", got)
	}
}

// TestLaunch_UnreadableGraph_TaskOtherAgent_RefusesLaunch is N2's
// "no blanket other-agent exemption" control: the SAME unreadable graph, but a
// task-origin launch to a DIFFERENT agent, MUST still refuse — the exemption is
// tied to caller==target, never applied to other-agent delegation.
func TestLaunch_UnreadableGraph_TaskOtherAgent_RefusesLaunch(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(u5aCallerAgentID, testDefaultAgentID, nil, nil),
	})
	corruptDelegationStore(t, home, testWS)

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 5

	l := NewSteerLauncher(al)
	steerer := newTestSteeringSessionOwnedBy(t, al, testWS, u5aCallerAgentID)

	_, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-other-1", CallID: "call-task-other"},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("an unreadable graph must still refuse a task-origin launch to a DIFFERENT agent "+
			"(no blanket other-agent exemption); got %v, want steer.ErrInvalidEdge", err)
	}
	if n := childrenOf(t, al, steerer); n != 0 {
		t.Fatalf("refused task-other launch persisted %d child record(s), want 0", n)
	}
}

// TestLaunch_UnreadableGraph_DelegateSelf_RefusesLaunch is N2's delegate-self
// negative control: even with caller == target, a DELEGATE-origin launch (which
// forks a new session and therefore IS delegation, unlike a task reassignment)
// must still refuse on an unreadable graph. The task-self exemption is tied to
// the task ORIGIN, never to "caller == target" alone.
func TestLaunch_UnreadableGraph_DelegateSelf_RefusesLaunch(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(testDefaultAgentID, testDefaultAgentID, nil, nil),
	})
	corruptDelegationStore(t, home, testWS)

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 5

	l := NewSteerLauncher(al)
	// The steering session's ACTIVE agent is testDefaultAgentID and the target
	// is the same agent, but the origin is Delegate — a self-fork, which is
	// ordinary delegation and IS graph-gated.
	steerer := newSelfSteeringSession(t, al, testWS)

	_, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "do work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-delegate-self"},
	})
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("a delegate-self launch on an unreadable graph must be REFUSED (the task-origin "+
			"exemption is not a caller==target exemption); got %v, want steer.ErrInvalidEdge", err)
	}
	if n := childrenOf(t, al, steerer); n != 0 {
		t.Fatalf("refused delegate-self launch persisted %d child record(s), want 0", n)
	}
}
