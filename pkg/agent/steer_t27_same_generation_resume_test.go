package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// T27 same-generation resume oracle, from ADR-20260928 D2 (asset cd20cf8b),
// not from SteerCanceller.Revive's current generation bump.
//
// An explicit RESUME of a landed stopped session keeps the generation, clears
// the stop note and any current fence, and queues that same generation. It
// does not mint generation+1 and it does not jump straight to running.
//
// This test does not prove stale-effect exclusion. The production cancel path
// still has no execution identity: steerAdmission.removeQueuedSession and
// AgentLoop.cancelDelegatedSubtree remove by session id, and
// AgentLoop.requestCancelForGeneration matches generation only. A callback
// aimed at an old run cannot be told apart from a newer Stop of the
// replacement, so forbidding that callback would also forbid T27's positive
// control. There is also no pause between CancelSubtree's fence commit and
// its live effect. Those checks wait on the identity seam.
//
// Superseded oracles, left in place for the corrective lane:
// TestRevive_NewGeneration_OldMarkerInert and TestStopRevive_OrderUnderLock
// still expect generation++.
func TestT27_StoppedResume_KeepsGenerationAndQueues(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	parentID := newTestSteeringSession(t, al, "ws-t27-resume")
	rec := launchRunningChild(t, al, parentID, "call-t27-resume")
	generation := rec.Generation
	childID := rec.SessionID

	canceller := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
	if _, err := canceller.CancelSubtree(context.Background(), childID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "t27-owner",
	}); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	stopped, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(stopped): %v", err)
	}
	if stopped.State != session.LifecycleStopped || stopped.Generation != generation || stopped.StopNote == nil {
		t.Fatalf("Stop did not land a same-generation stopped record with a note: state=%q generation=%d note=%v",
			stopped.State, stopped.Generation, stopped.StopNote)
	}

	resumedGeneration, err := canceller.Revive(context.Background(), childID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "t27-owner",
	})
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	got, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(resumed): %v", err)
	}

	if resumedGeneration != generation || got.Generation != generation {
		t.Errorf("generation after RESUME = returned %d record %d, want %d (a stopped session resumes on the same generation)",
			resumedGeneration, got.Generation, generation)
	}
	if got.State != session.LifecycleQueued {
		t.Errorf("state after RESUME = %q, want %q", got.State, session.LifecycleQueued)
	}
	if got.StopNote != nil {
		t.Errorf("stop_note after RESUME = %+v, want cleared", got.StopNote)
	}
	if got.Stop != nil && got.Stop.Generation == got.Generation {
		t.Errorf("current-generation Stop fence still set after RESUME: %+v", got.Stop)
	}
}
