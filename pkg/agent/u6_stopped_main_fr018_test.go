// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// session-core U6 — FR-018: the stopped-main exemption for a task-origin MAIN
// child.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-018 + BDD-06.2, and the architect's U6 seam decision D-U6.4
// (ARCHITECT-ANSWER-U6-U7.md). Read from the spec/decision text, never from the
// implementation.
//
// FR-018: "Authorized future scheduled MAIN launch under stopped main MUST be
// task-origin-only, ordered against existing Stop/cascade fence, without
// main-model revival. ... ordinary extra-chat helper parentage and general
// stopped-parent guard stay."
//
// D-U6.4: the exemption lives in steer_launcher.go::launchSteered's
// PublishChildUnderParentLock callback — a task-origin launch whose steering
// session is the TARGET agent's own computed main is admitted; everything else
// under a stopped parent is still refused. The exemption must not revive the
// parent, write its state or dispatch it.
//
// This file compiles against the pre-change tree (Launch, MainSessionID and the
// lifecycle store all exist today) and fails BEHAVIOURALLY: today a launch under
// a current-generation-Stop main is refused with steer.ErrSteeringStopped, so
// the task-origin main child is never admitted.

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// u6SeedStoppedMain persists a lifecycle record for (ws, agent)'s computed main
// session carrying a current-generation Stop marker, mirroring the real
// stopped-main shape the cascade leaves behind. It returns the main's session
// id.
func u6SeedStoppedMain(t *testing.T, al *AgentLoop, ws, agentID string) string {
	t.Helper()
	// The launch path reads the steering session's unified meta (its owner
	// agent), so the main must exist as a unified session too, not just as a
	// lifecycle record. GetOrCreateMainSession is the only creator.
	if _, err := al.GetSessionStore().GetOrCreateMainSession(ws, agentID); err != nil {
		t.Fatalf("GetOrCreateMainSession(%q, %q): %v", ws, agentID, err)
	}
	mainID, err := session.MainSessionID(ws, agentID)
	if err != nil {
		t.Fatalf("MainSessionID(%q, %q): %v", ws, agentID, err)
	}
	// State stays LifecycleRunning with a current-generation Stop — the exact
	// shape TestLaunch_UnderStampedParent seeds, and the shape the guard reads
	// (parentRec.Terminal() || parentRec.Stopped()). A LifecycleStopped state
	// would additionally require a StopNote under persistLocked.
	if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID:      mainID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    ws,
		AgentID:        agentID,
		Origin:         &session.Origin{Kind: steer.OriginKindChat},
		Stop:           &session.Stop{Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"}},
	}); err != nil {
		t.Fatalf("seed stopped main %q: %v", mainID, err)
	}
	return mainID
}

// TestU6_FR018_TaskOriginMainChildAdmittedUnderStoppedMain is FR-018's core
// behavioural assertion: an authorized future scheduled MAIN launch under a
// stopped main is admitted as a real child of that main, and the main's own
// record is untouched (never revived, never written, never dispatched).
//
// The FR-018 exemption now lives in steer_launcher.go::isMainTaskChildLaunch,
// read by launchSteered's stopped-parent guard.
func TestU6_FR018_TaskOriginMainChildAdmittedUnderStoppedMain(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	const ws = "ws-1"
	mainID := u6SeedStoppedMain(t, al, ws, testDefaultAgentID)

	// Snapshot the main before the launch so "untouched" is checked by value,
	// not by counting writes.
	mainBefore, err := al.GetSessionLifecycleStore().Load(mainID)
	if err != nil {
		t.Fatalf("load main before launch: %v", err)
	}

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: mainID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "u6 scheduled main run",
		Origin:            steer.Origin{Kind: steer.OriginKindTask, TaskID: "t-u6-main-1"},
	})
	if err != nil {
		t.Fatalf("FR-018: Launch of a task-origin child under its own stopped main was refused (%v). "+
			"An authorized future scheduled MAIN launch under a stopped main must be admitted as a "+
			"task-origin-only child (session-core-spec FR-018 / D-U6.4).", err)
	}
	if res.SessionID == "" {
		t.Fatal("FR-018: the task-origin main child was admitted with an empty session id")
	}
	if res.SessionID == mainID {
		t.Fatalf("FR-018: the launch reused the main's own session id %q — the run must be a NEW child, "+
			"never the main itself", mainID)
	}

	child, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("load child record: %v", err)
	}
	if child.SteeredBy == nil || child.SteeredBy.SteeringSessionID != mainID {
		t.Fatalf("FR-018: child's steering edge = %+v, want the assignee's main %q as its real parent "+
			"(D-U6.4: task-origin launch whose steering session is the target agent's own computed main)",
			child.SteeredBy, mainID)
	}

	// The main must be untouched: same generation, same Stop, same state — the
	// exemption may not revive it, write it or dispatch it.
	mainAfter, err := al.GetSessionLifecycleStore().Load(mainID)
	if err != nil {
		t.Fatalf("load main after launch: %v", err)
	}
	if mainAfter.Generation != mainBefore.Generation {
		t.Fatalf("FR-018: the stopped main's generation changed %d → %d — the exemption must never "+
			"revive the main (D-U6.4: zero main-model wake)", mainBefore.Generation, mainAfter.Generation)
	}
	if mainAfter.State != mainBefore.State {
		t.Fatalf("FR-018: the stopped main's state changed %q → %q — admitting a task-origin child "+
			"must not write the main's state", mainBefore.State, mainAfter.State)
	}
	if (mainAfter.Stop == nil) != (mainBefore.Stop == nil) ||
		(mainAfter.Stop != nil && mainBefore.Stop != nil && mainAfter.Stop.Generation != mainBefore.Stop.Generation) {
		t.Fatalf("FR-018: the stopped main's Stop marker changed %+v → %+v — the main stays stopped",
			mainBefore.Stop, mainAfter.Stop)
	}
}

// TestU6_FR018_DelegateUnderStoppedMainStillRefused pins the other half of
// FR-018: "ordinary extra-chat helper parentage and general stopped-parent guard
// stay." A non-main, delegate-origin launch under the same stopped main must
// still be refused — the exemption is task-origin main children ONLY.
//
// This already holds today (the general guard), so it is a KEEP guard: it must
// stay true after the exemption lands, and it catches an over-broad exemption.
func TestU6_FR018_DelegateUnderStoppedMainStillRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)

	mainID := u6SeedStoppedMain(t, al, "ws-1", testDefaultAgentID)

	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: mainID, // a main id, but a DELEGATE origin — not exempt
		TargetAgentID:     testDefaultAgentID,
		Task:              "u6 delegate under stopped main",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "c-u6-1"},
	})
	if !errors.Is(err, steer.ErrSteeringStopped) {
		t.Fatalf("FR-018: a DELEGATE-origin launch under a stopped main = %v, want "+
			"steer.ErrSteeringStopped. The exemption is task-origin main children ONLY; the general "+
			"stopped-parent guard stays for everything else (session-core-spec FR-018).", err)
	}
}
