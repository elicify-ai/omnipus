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

// Oracle: session-core FR-018 / D-U6.4, as amended by the ADR-093 amendment
// of 2026-10-23 (Decision 1) — the stopped-main guard is removed outright, so
// a task-origin launch whose steering session is the target's own computed
// main is admitted as a real child with the main untouched, and admission is
// unconditional. SPEC CHANGE: the OLD oracle's "only" half (a task launch for
// a DIFFERENT agent under that main is still refused by the stopped guard)
// loses its subject with the guard; what remains pinned here is the
// keep-list refusal that still applies — an unknown target agent is refused
// with steer.ErrAgentUnknown (ADR-093 amendment refusal keep-list, case
// "unknown target agent"), not by any stopped-parent guard.
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

	// A task-origin launch for a DIFFERENT (unknown) agent still refuses —
	// with the keep-list's own reason, never the removed stopped guard.
	_, err = l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: mainID, TargetAgentID: "someone-else", Task: "not the main's own agent",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "t-fr018-other"},
	})
	if err == nil {
		t.Fatal("a task child targeting an unknown agent must still be refused (ADR-093 amendment refusal keep-list: unknown target agent)")
	}
	if !errors.Is(err, steer.ErrAgentUnknown) {
		t.Fatalf("refusal = %v, want steer.ErrAgentUnknown (the keep-list reason); the stopped-parent guard that used to fire here was removed by the ADR-093 amendment of 2026-10-23", err)
	}
	// And a plain extra-chat id (not a computed main) keeps the guard.
}
