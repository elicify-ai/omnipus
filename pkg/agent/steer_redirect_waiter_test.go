// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED-pack test for the 2026-10-04 founder contract ("a stopped helper is
// paused, not failed"), redirect-waiter half:
//
//	awaitStoppedAndRevive (steer_redirect.go) must NOT revive while the
//	record is still running/queued with a stop fence stamped for its CURRENT
//	generation — LifecycleRecord.Stopped() is true for that shape too, and an
//	early revive re-queues a generation the old, still-registered turn then
//	refuses, stranding the record queued with nobody running it. It revives
//	once the state has LANDED (LifecycleStopped, fence cleared by
//	TransitionSession) or the record is terminal.
//
// The method is called directly (package-internal test) — the direct test
// hook the dispatch asks for; NO production hook is added. The gate's
// timing constants are the production ones (redirectPollInterval /
// redirectWaitDeadline); the fence-phase wait below spans several poll
// intervals, so a buggy gate that revives on Stopped() would fire on the
// very first poll, well inside the observation window.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestRedirectWaiter_NotRevivingWhileFenceInFlight_RevivesOnceLanded(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	// The steerer needs its own root lifecycle record (SteeredBy nil) so the
	// child's SteeredBy edge classifies as a genuine steered session (see
	// TestReviveStoppedSession_LandedAndClearedRecordRevives for the same
	// setup rationale).
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))

	childID := newTestSteeringSession(t, al, adr093Workspace)
	parentMeta := steererID
	if err := al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &parentMeta}); err != nil {
		t.Fatalf("SetMeta(child).ParentSessionID: %v", err)
	}
	// The IN-FLIGHT FENCE shape: state running, Stop stamped for the CURRENT
	// generation — exactly what SteerCanceller.StopTurns leaves behind while
	// the cooperative stop is still unwinding. Stopped() is true for this
	// record; that must not be enough for the waiter.
	childRec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:    adr093Workspace,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-redirect-waiter"},
		Stop: &session.Stop{
			At:         time.Now(),
			Generation: 1,
			By:         session.Principal{Kind: session.PrincipalKindHuman, ID: "tester"},
		},
		// No StopNote: the stop has not LANDED.
	}
	adr093Persist(t, al, childRec)

	const instruction = "the replacement instruction"
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		al.awaitStoppedAndRevive(context.Background(), childRec, by, instruction)
	}()

	// --- Fence phase: several polls' worth of waiting, no revive allowed ---
	time.Sleep(6*redirectPollInterval + 100*time.Millisecond)

	got := adr093Load(t, al, childID)
	if got.Generation != 1 {
		t.Fatalf("waiter revived while the stop fence was still in flight: generation = %d, want 1 — an early revive re-queues a generation the old, still-registered turn refuses, stranding the record queued with nobody running it", got.Generation)
	}
	if got.State != session.LifecycleRunning {
		t.Errorf("record state during the fence phase = %q, want %q (untouched while the stop has not landed)", got.State, session.LifecycleRunning)
	}
	if lastUserEntry(t, al, childID) == instruction {
		t.Errorf("the replacement instruction was already appended while the fence was in flight — delivery must wait for the landing")
	}
	select {
	case <-done:
		t.Fatal("the waiter exited during the fence phase — it may neither revive nor give up while the stop has not landed (the deadline, not this window, ends the wait)")
	default:
	}

	// --- Land the stop: TransitionSession's landing semantics — the fence
	// clears, the state lands LifecycleStopped, the note is retained. ---
	if err := al.GetSessionLifecycleStore().Mutate(childID, func(cur *session.LifecycleRecord) error {
		cur.State = session.LifecycleStopped
		cur.Stop = nil
		cur.StopNote = &session.StopNote{
			At:    time.Now().UTC(),
			By:    "human:tester",
			Seq:   1,
			Cause: session.StopCauseStop,
		}
		return nil
	}); err != nil {
		t.Fatalf("land the stop: %v", err)
	}

	// --- Landed phase: the waiter revives on the next poll. ---
	// Revival observables (Correction C1's stopped row — the waiter's own
	// doc comment: "stopped: same conversation, same generation"): the
	// replacement instruction is appended to the SAME session's transcript,
	// and the record LEAVES the landed-stopped state (the revived turn is
	// admitted — running/queued — and may already have finished, which is a
	// terminal state, not a stopped one). Generation is NOT the revival
	// signal: SteerCanceller.Revive releases a landed-stopped record on its
	// SAME generation (only a terminal record mints G+1).
	deadline := time.Now().Add(20 * time.Second)
	for lastUserEntry(t, al, childID) != instruction {
		if time.Now().After(deadline) {
			got = adr093Load(t, al, childID)
			t.Fatalf("waiter never revived after the stop landed: generation = %d, state = %q, last user entry = %.60q — want the replacement instruction appended to the same conversation within the poll window", got.Generation, got.State, lastUserEntry(t, al, childID))
		}
		time.Sleep(100 * time.Millisecond)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		got = adr093Load(t, al, childID)
		if got.State != session.LifecycleStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record still %q after the replacement was delivered — the revived session must leave the stopped state (admission dispatch)", got.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got.State == session.LifecycleFailed {
		t.Errorf("record after revive = failed, want the revived turn admitted (running/queued) or already finished (completed)")
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the waiter goroutine never exited after the revival")
	}
}

// lastUserEntry returns the content of the child transcript's LAST user
// entry ("" when there is none) — the entry the revived turn would read as
// its instruction.
func lastUserEntry(t *testing.T, al *AgentLoop, sessionID string) string {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" {
			return entries[i].Content
		}
	}
	return ""
}
