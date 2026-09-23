// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-2/I-3/I-6 — the two properties Dispatch owns that
// no unit test of the admission primitive can prove:
//
//  1. D9 "a session whose turn has ended holds no slot": a steered turn run
//     to completion through the REAL path gives its admission slot back and
//     the oldest queued session starts. steer_admission_test.go exercises
//     tryAdmit/release/hasReservation directly and
//     steer_launcher_test.go::TestDispatch_AtCap_Queued saturates the gate by
//     hand, so neither would notice a dispatch that never releases.
//
//  2. I-2/I-6: a Stop (or a Revive) that lands between Dispatch's record
//     snapshot and its running-state write survives that write, and a stopped
//     session does not start a turn.
//
// Both run a real *AgentLoop (newSteerAL) with its real session and lifecycle
// stores — never a spy standing in for either.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
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

// TestDispatch_StopLandingAfterTheSnapshotSurvivesAndTheTurnNeverStarts
// proves I-2/I-6 and landing order §0 ("Stop stamps a durable marker on every
// session it reaches, and no dispatch starts a turn on a stamped session")
// against the exact interleaving a naked Load/decide/Persist loses: the Stop
// is stamped after Dispatch has taken its record snapshot and before Dispatch
// writes `running` back.
//
// Three things must hold afterwards: the Stop marker is still on disk, the
// record is not `running`, and no turn is registered for the session.
func TestDispatch_StopLandingAfterTheSnapshotSurvivesAndTheTurnNeverStarts(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	steerer := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, steerer, "call-stop-race", "work that must never start")

	canceller := NewSteerCanceller(lifecycle)
	var once sync.Once
	dispatchStateWriteTestHook = func(hookSessionID string, _ int) {
		if hookSessionID != childID {
			return
		}
		once.Do(func() {
			if _, err := canceller.CancelSubtree(context.Background(), childID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("CancelSubtree inside the dispatch window: %v", err)
			}
		})
	}
	t.Cleanup(func() { dispatchStateWriteTestHook = nil })

	result, err := NewSteerLauncher(al).Dispatch(context.Background(), childID, childGen)
	if err == nil {
		t.Errorf("Dispatch(stopped mid-window) = %+v, nil; want a refusal — the session was stopped before the state write", result)
	}
	if result.State == steer.DispatchRunning {
		t.Errorf("Dispatch(stopped mid-window).State = running; a stopped session must never start a turn")
	}

	rec, loadErr := lifecycle.Load(childID)
	if loadErr != nil {
		t.Fatalf("Load(child): %v", loadErr)
	}
	if rec.Stop == nil {
		t.Fatalf("the Stop marker was erased from disk by Dispatch's write-back — after this there is no record that Stop was ever pressed (state=%q)", rec.State)
	}
	if rec.Stop.Generation != rec.Generation {
		t.Errorf("Stop.Generation = %d, want %d (the record's current generation)", rec.Stop.Generation, rec.Generation)
	}
	if rec.State == session.LifecycleRunning {
		t.Errorf("persisted State = running, want the stopped session left un-started")
	}
	if ts := al.getActiveTurnState(childID); ts != nil {
		t.Errorf("a turn is still registered for the stopped session %s — the refused dispatch must not leave one behind", childID)
	}
	if al.steerAdmission().hasReservation(childID, childGen) {
		t.Errorf("the refused dispatch kept its admission slot")
	}
}

// TestDispatch_ReviveLandingAfterTheSnapshotIsNotRolledBack proves the third
// outcome of the same read-modify-write: a Revive that mints a newer
// generation inside Dispatch's window must not be undone by the stale
// write-back. A record's generation moving DOWN makes every later
// generation-carrying cancel miss.
func TestDispatch_ReviveLandingAfterTheSnapshotIsNotRolledBack(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	lifecycle := al.GetSessionLifecycleStore()
	steerer := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, steerer, "call-revive-race", "work interrupted by a revival")

	canceller := NewSteerCanceller(lifecycle)
	var once sync.Once
	dispatchStateWriteTestHook = func(hookSessionID string, _ int) {
		if hookSessionID != childID {
			return
		}
		once.Do(func() {
			if _, err := canceller.CancelSubtree(context.Background(), childID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("CancelSubtree inside the dispatch window: %v", err)
				return
			}
			if _, err := canceller.Revive(context.Background(), childID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
				t.Errorf("Revive inside the dispatch window: %v", err)
			}
		})
	}
	t.Cleanup(func() { dispatchStateWriteTestHook = nil })

	if _, err := NewSteerLauncher(al).Dispatch(context.Background(), childID, childGen); err == nil {
		t.Error("Dispatch(revived mid-window) = nil error; want a refusal — the snapshot's generation is stale")
	}

	rec, loadErr := lifecycle.Load(childID)
	if loadErr != nil {
		t.Fatalf("Load(child): %v", loadErr)
	}
	if rec.Generation != childGen+1 {
		t.Fatalf("record generation = %d, want %d — Dispatch's stale write-back rolled the revival backwards", rec.Generation, childGen+1)
	}
	if ts := al.getActiveTurnState(childID); ts != nil {
		t.Errorf("a turn is still registered for %s after a refused dispatch", childID)
	}
}
