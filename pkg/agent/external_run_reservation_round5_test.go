// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Re-verification round 4 of the U5 security batch: NEW-7 (Stop clears only the
// holds of the executions it actually selected), NEW-6 (a failed promotion and a
// Stop of a queued descendant retire the hold), NEW-8 (a late lifecycle fault
// never becomes publishable tool text) and NEW-9 (cancelling the last hold
// re-evaluates the idle release).
package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func holdCount(sess *externalCLIRunSession) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return len(sess.reservations)
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the unreadable-journal instrument needs a non-root runner")
	}
}

// queuedExternalRevival builds a stopped, already-started external helper with a
// retained driver, holds the old episode's admission slot, and REALLY revives it:
// the accepted revival is queued behind the slot and owns the driver hold. It
// returns the accepted execution's identity.
func queuedExternalRevival(t *testing.T, al *AgentLoop) (childID string, sess *externalCLIRunSession, claim executionClaim) {
	t.Helper()
	childID, sess = seedRetainedExternalChild(t, al)
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
	revived, err := al.ReviveStoppedSession(context.Background(), childID, by, "carry on")
	require.NoError(t, err)
	require.True(t, revived, "instrument check: the revival must have been accepted")
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, rec.State, "instrument check: the accepted revival must be queued behind the old slot")
	require.NotNil(t, rec.ExecutionID)
	require.Equal(t, 1, holdCount(sess), "instrument check: the accepted revival must own exactly one hold")
	return childID, sess, executionClaim{SessionID: childID, Generation: rec.Generation, RunID: rec.ExecutionID.RunID, BootSeq: rec.ExecutionID.BootSeq}
}

func journalOf(al *AgentLoop, id string) string {
	return filepath.Join(al.GetSessionLifecycleStore().Dir(), id+".jsonl")
}

// Oracle: NEW-7 scenario A — a Stop that fails (its target cannot be read) is no
// accepted Stop: it must leave an accepted queued revival's hold, and so the
// driver that revival needs, alone.
// Real: AgentLoop.StopSession against a real queued revival.
func TestR5_NEW7_FailedStopKeepsAnAcceptedQueuedRevivalsHold(t *testing.T) {
	skipIfRoot(t)
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess, _ := queuedExternalRevival(t, al)

	journal := journalOf(al, childID)
	require.NoError(t, os.Chmod(journal, 0o000))
	t.Cleanup(func() { _ = os.Chmod(journal, 0o600) })
	f, openErr := os.Open(journal)
	require.Error(t, openErr, "instrument check: the journal must really be unreadable")
	if f != nil {
		f.Close()
	}

	_, err := al.StopSession(context.Background(), StopRequest{
		SessionID: childID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"},
	})
	require.Error(t, err, "instrument check: the Stop must have failed on the unreadable target")

	require.Equal(t, 1, holdCount(sess), "a failed Stop erased an accepted revival's hold")
	require.True(t, driverRetained(sess), "a failed Stop released the driver the accepted revival needs")
}

// Oracle: NEW-7 scenario B and NEW-6 (selected admission) — a Stop effect retires
// only the hold of the exact execution it selected. A stale effect (an older run)
// leaves a replacement's hold alone; the matching effect retires its own hold and
// the now-unneeded idle driver.
// Real: removeQueuedStopEffects, the single point every queued-Stop settlement
// passes through, driven with the accepted revival's real identity.
func TestR5_NEW7_StopEffectRetiresOnlyTheSelectedExecutionsHold(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess, claim := queuedExternalRevival(t, al)
	effectFor := func(runID string) session.StopEffect {
		return session.StopEffect{ControlID: "ctl-" + runID, Target: session.StopEffectTarget{
			Generation: claim.Generation, RunID: runID, BootSeq: claim.BootSeq}}
	}

	al.removeQueuedStopEffects(childID, []session.StopEffect{effectFor("an-older-run")})
	require.Equal(t, 1, holdCount(sess), "a stale Stop effect (older run, same generation) erased the replacement's hold")
	require.True(t, driverRetained(sess))

	al.removeQueuedStopEffects(childID, []session.StopEffect{effectFor(claim.RunID)})
	require.Zero(t, holdCount(sess), "the selected admission left the queue; its hold must go with it")
	require.False(t, driverRetained(sess), "with no consumer left the idle driver must be released")
}

// Oracle: NEW-6 — an accepted queued revival whose promotion fails before any
// turn body exists retires its hold (and releases the idle driver).
// Real: drainSteerQueue -> promoteSteeredExecution with the lifecycle record gone.
func TestR5_NEW6_FailedPromotionRetiresTheHold(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess, _ := queuedExternalRevival(t, al)

	require.NoError(t, os.Remove(journalOf(al, childID)), "instrument check: the record must be removable")
	al.drainSteerQueue(executionClaim{SessionID: childID, Generation: 1, RunID: "old-episode", BootSeq: 1})
	al.steerAdmission().turns.Wait()

	require.Zero(t, holdCount(sess), "a failed promotion left the accepted revival's hold behind")
	require.False(t, driverRetained(sess), "a failed promotion left the driver pinned with no consumer")
}

// Oracle: NEW-6 — Stop all of an ancestor removes a queued external descendant's
// admission; that descendant never runs a body, so the Stop settlement must
// retire its hold.
// Real: StopSession{Tree} on the steering session.
func TestR5_NEW6_StoppingAQueuedDescendantRetiresItsHold(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess, _ := queuedExternalRevival(t, al)
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	parentID := rec.SteeredBy.SteeringSessionID

	_, err = al.StopSession(context.Background(), StopRequest{
		SessionID: parentID, Tree: true, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"},
	})
	require.NoError(t, err)
	after, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, after.State, "instrument check: the queued descendant must have been stopped")

	require.Zero(t, holdCount(sess), "the stopped queued descendant's hold was left behind")
	require.False(t, driverRetained(sess), "the stopped queued descendant's driver stayed pinned")
}

// Oracle: NEW-9 — the old episode's disposal releases first (and correctly
// skips: a revival holds the driver); the revival then fails for real and
// cancels its hold. Ending the last hold must re-run the idle release; nothing
// else will.
// Real: ReviveStoppedSession failing after its reservation (the lifecycle record
// vanishes), with the old disposal's release fired from the seam in between.
func TestR5_NEW9_LastCancelReleasesTheIdleDriver(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, sess := seedRetainedExternalChild(t, al)

	reviveAfterAvailabilityTestHook = func(id string) {
		reviveAfterAvailabilityTestHook = nil
		al.releaseExternalRunIfIdle(id) // the old episode's disposal: skips, a hold is live
		require.True(t, driverRetained(sess), "instrument check: the old release must have skipped for the live hold")
		require.NoError(t, os.Remove(journalOf(al, childID)))
	}
	defer func() { reviveAfterAvailabilityTestHook = nil }()

	revived, err := al.ReviveStoppedSession(context.Background(), childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}, "carry on")
	require.Error(t, err, "instrument check: the revival must have failed after reserving")
	require.False(t, revived)

	require.Zero(t, holdCount(sess))
	require.False(t, driverRetained(sess), "the last hold ended but nothing re-ran the idle release: the driver stays pinned forever")
}

// Oracle: NEW-8 — a lifecycle fault that strikes the reservation's own read
// (after the revival's first read succeeded) must not become publishable
// refusal text: no path in any text a caller may show, and the cause stays
// errors.Is-reachable.
// Real: ReviveStoppedSession; the journal becomes unreadable between the
// revival's read and the reservation's read.
func TestR5_NEW8_LateLifecycleFaultIsNotPublishableText(t *testing.T) {
	skipIfRoot(t)
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, _ := seedRetainedExternalChild(t, al)
	journal := journalOf(al, childID)

	reviveBeforeReservationTestHook = func(string) {
		reviveBeforeReservationTestHook = nil
		require.NoError(t, os.Chmod(journal, 0o000))
	}
	defer func() { reviveBeforeReservationTestHook = nil }()
	t.Cleanup(func() { _ = os.Chmod(journal, 0o600) })

	_, err := al.ReviveStoppedSession(context.Background(), childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}, "carry on")
	require.Error(t, err, "instrument check: the late read fault must have refused the revival")

	var texter interface{ RefusalText() string }
	if errors.As(err, &texter) {
		for _, leak := range []string{journal, filepath.Dir(journal), ".jsonl", "permission denied"} {
			require.False(t, strings.Contains(texter.RefusalText(), leak),
				"publishable refusal text %q leaks %q", texter.RefusalText(), leak)
		}
	}
	require.True(t, errors.Is(err, fs.ErrPermission), "the cause must stay reachable for logs and typed inspection: %v", err)
}
