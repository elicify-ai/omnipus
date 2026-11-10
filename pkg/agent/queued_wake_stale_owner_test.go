package agent

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Repair brief 2; frozen ADR D2 execution identity/effect-boundary checks:
// a replaced run cannot lend its queue entry, late input or promotion to the
// new run, including a RESUME that keeps the generation. The real replacement
// must still accept its own wake and run, so refusal alone cannot satisfy this.
// Independent CHECK and mutation proof are deferred to a different qa-lead.
func TestQueuedWakeOwner_StaleEntryCannotMutateOrRunReplacement(t *testing.T) {
	for _, tc := range []struct {
		name           string
		nextGeneration bool
	}{
		{name: "same_generation_resume"},
		{name: "next_generation_resume", nextGeneration: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queuedWakeNegativeReplacementCase(t, tc.nextGeneration)
		})
	}
}

func queuedWakeNegativeReplacementCase(t *testing.T, nextGeneration bool) {
	t.Helper()
	f := newQueuedWakeNegativeFixture(t)
	originalEntry := queuedWakeNegativeQueue(f.al)[0]
	ctx := context.Background()
	canceller := f.al.steerCanceller()
	principal := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "queued-negative-owner"}
	wantGeneration := f.generation
	if nextGeneration {
		// A genuine queued-admission failure commits the old generation's
		// outcome/outbox. Its queue entry remains to become the stale input.
		if err := f.al.reportSteeredExecutionFailure(ctx, f.owner, "fixture: queued admission failed before its model call"); err != nil {
			t.Fatalf("SETUP: commit the old admission failure: %v", err)
		}
		failed := rootReopenedRecord(t, f.al, f.childID)
		if failed.State != session.LifecycleFailed || failed.FinalDelivery == nil || failed.FinalDelivery.CommitID != f.owner.RunID {
			t.Fatalf("SETUP: no real failed outcome/outbox for the old owner: %+v", failed)
		}
		wantGeneration++ // ADR D2: only done/failed RESUME creates the next generation.
	} else {
		if _, err := canceller.CancelSubtree(ctx, f.childID, principal); err != nil {
			t.Fatalf("SETUP: stop the original queued admission: %v", err)
		}
		stopped := rootReopenedRecord(t, f.al, f.childID)
		if stopped.State != session.LifecycleStopped || stopped.StopNote == nil || stopped.Generation != f.generation {
			t.Fatalf("SETUP: same-generation queued stop did not land: %+v", stopped)
		}
	}
	generation, err := canceller.Revive(ctx, f.childID, principal)
	if err != nil || generation != wantGeneration {
		t.Fatalf("SETUP: real RESUME generation=%d error=%v, want %d", generation, err, wantGeneration)
	}
	dispatchChild(t, f.al, f.childID, generation, false)
	replacement := rootReopenedRecord(t, f.al, f.childID)
	owner := queuedWakeNegativeOwner(t, replacement)
	if owner.SessionID != f.owner.SessionID || owner.Generation != wantGeneration || owner.BootSeq != f.owner.BootSeq || owner.RunID == f.owner.RunID {
		t.Fatalf("SETUP: replacement has no fresh full tuple: original=%+v replacement=%+v", f.owner, owner)
	}
	if replacement.State != session.LifecycleQueued || len(replacement.PendingUserMessages) != 0 || f.al.getActiveTurnState(f.childID) != nil {
		t.Fatalf("SETUP: replacement must be queued, empty and not yet executing: %+v", replacement)
	}
	if !nextGeneration {
		// Stop normally removed A. Seed precisely that obsolete real entry
		// ahead of B to model delayed stale queue data. This is state fault
		// injection, not a mocked queue/claim matcher or a production hook.
		queuedWakeNegativePrependStale(f.al, originalEntry)
	}
	queue := queuedWakeNegativeQueue(f.al)
	wantReplacement := steerQueueEntry{sessionID: owner.SessionID, generation: owner.Generation, runID: owner.RunID, bootSeq: owner.BootSeq}
	if !queuedWakeNegativeQueuesEqual(queue, []steerQueueEntry{originalEntry, wantReplacement}) {
		t.Fatalf("SETUP: stale entry must precede the real replacement: got=%+v old=%+v new=%+v", queue, originalEntry, wantReplacement)
	}
	const (
		currentID = "queued-negative-replacement-input"
		accepted  = "Replacement wake: use only the new run's evidence"
		refused   = "Obsolete owner wake: this delayed input must never run"
	)
	// Intake must skip the obsolete tuple, not select it merely by session
	// (or session+generation), while still accepting the rightful B input.
	f.accept(t, currentID, accepted, generation)
	beforeRecord := rootReopenedRecord(t, f.al, f.childID)
	if got := queuedWakeNegativeOwner(t, beforeRecord); got != owner || !reflect.DeepEqual(beforeRecord.PendingUserMessages, []string{accepted}) {
		t.Fatalf("rightful wake did not stay on B: owner=%+v pending=%q", got, beforeRecord.PendingUserMessages)
	}
	wantReplacement.wakeInputs = []steeringQueueItem{{message: providers.Message{Role: "user", Content: accepted},
		wake: &steeringWake{messageID: currentID, transcriptSessionID: f.childID, agentID: testDefaultAgentID}}}
	beforeQueue := queuedWakeNegativeQueue(f.al)
	if !queuedWakeNegativeQueuesEqual(beforeQueue, []steerQueueEntry{originalEntry, wantReplacement}) {
		t.Fatalf("rightful wake changed the obsolete entry or its own tuple: %+v", beforeQueue)
	}
	beforeJournal := queuedWakeNegativeJournal(t, f)
	gate := f.al.steerAdmission()
	gate.entryMu.Lock() // The delayed production writer's documented lock protocol.
	lateErr := f.al.commitQueuedSystemWake(f.al.GetSessionLifecycleStore(), f.owner, f.generation,
		"queued-negative-obsolete-owner-input", refused, testDefaultAgentID)
	gate.entryMu.Unlock()
	if !errors.Is(lateErr, steer.ErrStaleGeneration) || lateErr.Error() != steer.ErrStaleGeneration.Error() {
		t.Errorf("obsolete queued writer error=%v, want the published stale-generation refusal %q", lateErr, steer.ErrStaleGeneration.Error())
	}
	if after := rootReopenedRecord(t, f.al, f.childID); !reflect.DeepEqual(after, beforeRecord) {
		t.Errorf("obsolete queued writer changed B's durable record: before=%+v after=%+v", beforeRecord, after)
	}
	if after := queuedWakeNegativeQueue(f.al); !queuedWakeNegativeQueuesEqual(after, beforeQueue) {
		t.Errorf("obsolete queued writer changed accepted queue data: before=%+v after=%+v", beforeQueue, after)
	}
	if after := queuedWakeNegativeJournal(t, f); !bytes.Equal(after, beforeJournal) {
		t.Error("obsolete queued writer appended or rewrote B's lifecycle journal")
	}
	// Do not remove the stale entry: real FIFO promotion must refuse A and
	// move on to B without consuming B's pending input under A's claim.
	f.requirePromotedOnce(t, owner, accepted, refused)
}

func queuedWakeNegativePrependStale(al *AgentLoop, original steerQueueEntry) {
	gate := al.steerAdmission()
	gate.entryMu.Lock()
	defer gate.entryMu.Unlock()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.queue = append([]steerQueueEntry{original}, gate.queue...)
}
