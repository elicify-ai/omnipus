// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-3/D9 — the property Dispatch owns that no unit
// test of the admission primitive can prove: "a session whose turn has ended
// holds no slot". A steered turn run to completion through the REAL path
// gives its admission slot back and the oldest queued session starts.
// steer_admission_test.go exercises tryAdmit/release/hasReservation directly
// and steer_launcher_test.go::TestDispatch_AtCap_Queued saturates the gate by
// hand, so neither would notice a dispatch that never releases.
//
// Runs a real *AgentLoop (newSteerAL) with its real session and lifecycle
// stores — never a spy standing in for either.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// launchSteeredChild launches one steered child under steerer and returns its
// session id and generation.
func launchSteeredChild(t *testing.T, al *AgentLoop, steerer, callID, task string) (string, int) {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: steerer,
		TargetAgentID:     testDefaultAgentID,
		Task:              task,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch(%s): %v", callID, err)
	}
	return res.SessionID, res.Generation
}

// TestDispatch_FinishedTurnReleasesItsSlotAndPromotesTheQueue proves D9 /
// landing order I-3 "Admission": when a delegated child's turn ends, its
// admission slot comes back and the oldest queued session is dispatched.
//
// Regression for the delivery defect this test's absence hid: the slot was
// released only from turn_exit.go::Finish, guarded by ts.al, which
// reconstructSteeredTurn never set — so every finished child leaked its slot
// and, at a realistic max_parallel_agents, delegation wedged permanently.
func TestDispatch_FinishedTurnReleasesItsSlotAndPromotesTheQueue(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	// Pin the effective cap to 1 so the second child is queued behind the
	// first (an unconfigured test AgentLoop otherwise resolves
	// EffectiveMaxParallelAgents() to the physical safety backstop).
	al.GetConfig().Performance.MaxParallelAgents = 1

	launcher := NewSteerLauncher(al)
	steerer := newTestSteeringSession(t, al, "ws-1")
	firstID, firstGen := launchSteeredChild(t, al, steerer, "call-slot-first", "first worker")
	secondID, secondGen := launchSteeredChild(t, al, steerer, "call-slot-second", "second worker")

	first, err := launcher.Dispatch(context.Background(), firstID, firstGen)
	if err != nil {
		t.Fatalf("Dispatch(first): %v", err)
	}
	if first.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(first) = %+v, want State=running", first)
	}
	second, err := launcher.Dispatch(context.Background(), secondID, secondGen)
	if err != nil {
		t.Fatalf("Dispatch(second): %v", err)
	}
	if second.State != steer.DispatchQueued || second.QueuePosition != 1 {
		t.Fatalf("Dispatch(second) = %+v, want State=queued at position 1", second)
	}

	firstTS := al.getActiveTurnState(firstID)
	if firstTS == nil {
		t.Fatal("no turn registered for the first child after a `running` Dispatch")
	}
	select {
	case <-firstTS.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the first child's turn did not finish within 30s")
	}

	gate := al.steerAdmission()
	secondTS := waitForActiveTurn(t, al, secondID, 30*time.Second)
	if gate.hasReservation(firstID, firstGen) {
		t.Errorf("the finished first child still holds an admission slot (D9: a session whose turn has ended holds no slot)")
	}
	if !gate.hasReservation(secondID, secondGen) {
		t.Errorf("the queued second child was never promoted into the admission gate")
	}
	if got := gate.queueLen(); got != 0 {
		t.Errorf("queue length after the first turn ended = %d, want 0 (the queued session must have started)", got)
	}
	if got := gate.activeCount(); got != 1 {
		t.Errorf("active admission slots = %d, want exactly 1 (the promoted second child)", got)
	}

	// Let the promoted child finish before the harness tears its temp dir
	// down underneath an in-flight write.
	select {
	case <-secondTS.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the promoted second child's turn did not finish within 30s")
	}
}

// waitForActiveTurn polls for the turn registered under sessionID. The
// promotion is deliberately asynchronous (drainSteerQueue dispatches the FIFO
// head in its own goroutine so a finishing turn never blocks on the next
// one), so there is nothing to await synchronously.
func waitForActiveTurn(t *testing.T, al *AgentLoop, sessionID string, within time.Duration) *turnState {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ts := al.getActiveTurnState(sessionID); ts != nil {
			return ts
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no turn was ever registered for session %s within %s — the queue never moved", sessionID, within)
	return nil
}
