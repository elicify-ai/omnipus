// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Re-verification round 3 of the U5 security batch: NEW-5 (a revival's hold on
// the retained driver is OWNED — another revival giving up cannot end it),
// NEW-6 (an accepted revival whose turn fails before reaching the holder does
// not pin the driver) and the post-finish wake lead (a terminal external
// recipient is revived only through the same guarded reservation).
package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func driverRetained(sess *externalCLIRunSession) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.driver != nil
}

// seedRetainedExternalChild builds a stopped, already-started external helper
// whose holder still retains its driver — the state a revival may continue.
func seedRetainedExternalChild(t *testing.T, al *AgentLoop) (string, *externalCLIRunSession) {
	t.Helper()
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))
	childID := newTestSteeringSession(t, al, adr093Workspace)
	parentID := steererID
	require.NoError(t, al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}))
	adr093Persist(t, al, &session.LifecycleRecord{
		SessionID:          childID,
		Generation:         1,
		State:              session.LifecycleStopped,
		OwnerScopeKind:     session.OwnerScopeHuman,
		SteeredBy:          &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:        adr093Workspace,
		AgentID:            testDefaultAgentID,
		Origin:             &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-r4"},
		Is3P:               true,
		ExternalRunStarted: true,
		StopNote:           &session.StopNote{At: time.Now(), By: "human:tester", Seq: 1, Cause: session.StopCauseStop},
	})
	sess := al.externalRunSession(childID)
	sess.mu.Lock()
	sess.started = true
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()
	return childID, sess
}

// Oracle: NEW-5 — two reservations of the same retained driver are separate
// holds. One revival giving up (cancel) must not end the other's hold, so an
// idle release still keeps the driver; only when the last owner is gone does
// the idle release drop it. A repeated cancel by the same owner is harmless.
func TestNEW5_ReservationIsOwned_OneCancelDoesNotEndTheOthersHold(t *testing.T) {
	al := u5bLoop(t)
	lc := al.GetSessionLifecycleStore()
	n7SeedStoppedExternal(t, lc, "r4-own", true)
	sess := al.externalRunSession("r4-own")
	sess.mu.Lock()
	sess.started = true
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()
	rec, err := lc.Load("r4-own")
	require.NoError(t, err)

	holdA, err := al.reserveExternalConversation("r4-own", rec)
	require.NoError(t, err)
	holdB, err := al.reserveExternalConversation("r4-own", rec)
	require.NoError(t, err)
	require.NotNil(t, holdA)
	require.NotNil(t, holdB)

	holdB.cancel()
	holdB.cancel() // idempotent: a second cancel must not touch A's hold
	al.releaseExternalRunIfIdle("r4-own")
	require.True(t, driverRetained(sess), "B giving up must not release the driver revival A still holds")

	holdA.cancel()
	al.releaseExternalRunIfIdle("r4-own")
	require.False(t, driverRetained(sess), "with no live hold left the idle release drops the driver (N6)")
}

// Oracle: NEW-5 end to end — two REAL overlapping revivals of the same stopped
// external helper. B wins (queued/admitted); A, interleaved in the window after
// its reservation, loses its dispatch and fails. A's failure must not release
// the driver B's accepted revival relies on.
// Real: AgentLoop.ReviveStoppedSession x2; the seam interleaves B inside A.
func TestNEW5_LosingRevivalDoesNotStrandTheWinnersDriver(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess := seedRetainedExternalChild(t, al)
	// The old episode is still unwinding: its admission slot is held, so B's
	// admitted revival is QUEUED behind it (the NEW-5 window) and A, arriving later,
	// finds the record already revived and queued.
	gate := al.steerAdmission()
	gate.mu.Lock()
	gate.active[childID] = steerQueueEntry{sessionID: childID, generation: 1, runID: "old-episode", bootSeq: 1}
	gate.mu.Unlock()
	t.Cleanup(func() {
		gate.mu.Lock()
		delete(gate.active, childID)
		gate.mu.Unlock()
	})
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}

	var bRevived bool
	var bErr error
	reviveAfterAvailabilityTestHook = func(id string) {
		reviveAfterAvailabilityTestHook = nil // B must not recurse
		bRevived, bErr = al.ReviveStoppedSession(context.Background(), id, by, "instruction B")
	}
	defer func() { reviveAfterAvailabilityTestHook = nil }()

	aRevived, aErr := al.ReviveStoppedSession(context.Background(), childID, by, "instruction A")
	require.NoError(t, bErr)
	require.True(t, bRevived, "instrument check: B (nested, first to dispatch) must have won")
	require.Error(t, aErr, "instrument check: A must have lost its dispatch, or this test cannot see A cancelling")
	require.False(t, aRevived)

	// The old episode's deferred release lands after A gave up.
	al.releaseExternalRunIfIdle(childID)
	require.True(t, driverRetained(sess),
		"the losing revival cancelled the winner's hold: the driver B's accepted revival needs was released")
}

// Oracle: NEW-6 — an accepted revival whose turn fails before it reaches
// beginExternalRun (here: the recorded external classification no longer matches
// the live native executor, so the body refuses) leaves no hold behind: when
// that turn has ended, the driver and its prompt/env snapshot are released.
// Real: ReviveStoppedSession -> dispatched turn -> runSteeredTurnBody refusal ->
// disposeSteeredTurnResult. The join is the admission gate's own turn WaitGroup.
func TestNEW6_AcceptedRevivalFailingBeforeTheHolderDoesNotPinTheDriver(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess := seedRetainedExternalChild(t, al)
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}

	revived, err := al.ReviveStoppedSession(context.Background(), childID, by, "carry on")
	require.NoError(t, err)
	require.True(t, revived, "instrument check: the revival must have been accepted")
	al.steerAdmission().turns.Wait()

	require.False(t, driverRetained(sess),
		"the admitted turn failed before the holder and nothing released the driver it was pinning")
}

// Oracle: wake lead — a terminal recipient with an already-started external
// conversation and NO retained driver is refused by the post-finish wake
// replay BEFORE it is revived (generation and state unchanged, entry stays
// unacknowledged), exactly as the public revival path refuses it.
// Real: AgentLoop.replayPostFinishWake with a real inbox entry and lifecycle.
func TestWakeReplay_TerminalExternalRecipientWithoutDriverRefusesBeforeRevive(t *testing.T) {
	al := u5bLoop(t)
	lc := al.GetSessionLifecycleStore()
	inbox := al.GetMessageInboxStore()
	const recipient = "r4-wake"
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: recipient, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "claude-code", Is3P: true, ExternalRunStarted: true,
	}))
	_, err := inbox.Append(recipient, pauseHandbackMessage(t, "child-x", "wake-1"))
	require.NoError(t, err)

	err = al.replayPostFinishWake(recipient, "wake-1")
	require.Error(t, err)
	require.True(t, errors.Is(err, errExternalResumeUnavailable),
		"the refusal must be the visible resume-unavailable one, got: %v", err)
	rec, lerr := lc.Load(recipient)
	require.NoError(t, lerr)
	require.Equal(t, 1, rec.Generation, "a refused wake must not mint a generation")
	require.Equal(t, session.LifecycleCompleted, rec.State, "a refused wake must not revive the recipient")
	acked, ackErr := deliverEntryIsAcked(inbox, recipient, "", "wake-1")
	require.NoError(t, ackErr)
	require.False(t, acked, "a refused wake leaves the inbox entry unacknowledged")
}
