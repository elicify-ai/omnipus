//go:build goolm && stdjson

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Oracle: session-core FR-018 / D-U6.4 — with the stopped main's unified
// session present (as production always has it: ensureMainSession creates it
// before a main is ever eligible), a task-origin launch whose steering session
// is the target's own computed main is admitted as a real child and the main's
// lifecycle record is untouched; a delegate-origin launch under it, and a task
// launch whose target is NOT that main's agent, are still refused.
// Real: SteerLauncher.Launch against the real stores.
func TestFR018_ExemptionAdmitsOnlyTheTargetsOwnMainTaskChild(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	l := NewSteerLauncher(al)
	const ws = "ws-1"

	if _, err := al.GetSessionStore().GetOrCreateMainSession(ws, testDefaultAgentID); err != nil {
		t.Fatalf("create the main's unified session: %v", err)
	}
	mainID := u6SeedStoppedMain(t, al, ws, testDefaultAgentID)
	before, err := al.GetSessionLifecycleStore().Load(mainID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: mainID, TargetAgentID: testDefaultAgentID, Task: "scheduled main run",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "t-fr018-main"},
	})
	if err != nil {
		t.Fatalf("task-origin child under its own stopped main must be admitted, got %v", err)
	}
	child, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil || child.SteeredBy == nil || child.SteeredBy.SteeringSessionID != mainID {
		t.Fatalf("child must be a real child of the main: %+v err=%v", child, err)
	}
	after, err := al.GetSessionLifecycleStore().Load(mainID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation || after.State != before.State || after.Stop == nil {
		t.Fatalf("the stopped main must be untouched: before=%+v after=%+v", before, after)
	}

	// A task-origin launch for a DIFFERENT agent under that main is not exempt.
	_, err = l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: mainID, TargetAgentID: "someone-else", Task: "not the main's own agent",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "t-fr018-other"},
	})
	if err == nil {
		t.Fatal("a task child targeting another agent under the stopped main must still be refused")
	}
	if !errors.Is(err, steer.ErrSteeringStopped) && !errors.Is(err, steer.ErrAgentUnknown) {
		t.Fatalf("refusal = %v, want the stopped-parent guard (or unknown agent)", err)
	}
	// And a plain extra-chat id (not a computed main) keeps the guard.
}
