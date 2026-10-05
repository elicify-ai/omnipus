//go:build linux || darwin

package agent

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// A1: real acceptance -> real fence append failure -> boot's public finisher.
// D4 owns intent-first and reconciliation; D6 QA1 requires finishing the
// ORIGINAL identity/seq through StopSession, with the notice before applied.
// Corrected from the dispatch's replacement-control oracle by the architect.
func TestQAStopIntentFinisher_PersistFailureLandsThroughOneStop(t *testing.T) {
	f := newQAReceiptFixture(t)
	grant, restore := qaReceiptAcceptFailedFence(t, f)
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	pending, err := fresh.UnfinishedStopIntents(f.child.SessionID)
	if err != nil || len(pending) != 1 || pending[0].Selection.Effect.ControlID != grant.ControlID || pending[0].Actor != qaReceiptStopActor || pending[0].Cause != session.StopCauseStop {
		t.Fatalf("reopened failed acceptance = %+v/%v, want exact queued original intent", pending, err)
	}
	restore()
	if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
		t.Fatalf("public boot finisher failed after storage repair: %v", err)
	}
	qaReceiptRequireOriginalStop(t, f, grant, session.StopCauseStop, qaReceiptStopActor)
	pending, err = fresh.UnfinishedStopIntents(f.child.SessionID)
	if err != nil || len(pending) != 0 {
		t.Errorf("completed finisher left unfinished intents: %+v/%v", pending, err)
	}
	before, err := os.ReadFile(qaReceiptJournalPath(f.al, f.child.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
		t.Fatalf("idempotent boot finisher replay: %v", err)
	}
	after, err := os.ReadFile(qaReceiptJournalPath(f.al, f.child.SessionID))
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("finisher replay rewrote the landed stop: equal=%v err=%v", bytes.Equal(before, after), err)
	}
	qaReceiptRequireOriginalStop(t, f, grant, session.StopCauseStop, qaReceiptStopActor)
}

// D6 QA1 boot-order control: the gateway runs recovery before the finisher.
// An original accepted Stop must not be replaced with a generic restart stop.
func TestQAStopIntentFinisher_OriginalIntentSurvivesActualBootOrder(t *testing.T) {
	f := newQAReceiptFixture(t)
	grant, restore := qaReceiptAcceptFailedFence(t, f)
	restore()
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	f.al.SetSessionMessagingStores(f.al.GetMessageInboxStore(), fresh)
	var operatorNotices []string
	if err := u1BootRecovery(t, f.al, &operatorNotices).Run(context.Background()); err != nil {
		t.Fatalf("actual boot recovery order: %v", err)
	}
	if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
		t.Fatalf("actual post-recovery finisher: %v", err)
	}
	qaReceiptRequireOriginalStop(t, f, grant, session.StopCauseStop, qaReceiptStopActor)
}

// A2: obsolete unfinished controls must not stop a terminal winner, a landed
// stopped session, or the next generation. Journal bytes are the no-state-
// change oracle, including the store-assigned timestamps and execution data.
func TestQAStopIntentFinisher_ObsoleteIntentSupersededWithoutStateChange(t *testing.T) {
	for _, state := range []string{"completed", "failed", "stopped", "next_generation"} {
		t.Run(state, func(t *testing.T) {
			f := newQAReceiptFixture(t)
			grant, restore := qaReceiptAcceptFailedFence(t, f)
			restore()
			err := f.al.GetSessionLifecycleStore().Mutate(f.child.SessionID, func(cur *session.LifecycleRecord) error {
				switch state {
				case "completed":
					cur.State = session.LifecycleCompleted
				case "failed":
					cur.State = session.LifecycleFailed
					cur.FailedReason = "the original turn failed before the Stop fence committed"
				case "stopped":
					cur.State = session.LifecycleStopped
					cur.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:later-owner", Seq: uint64(grant.Seq + 1), Cause: session.StopCauseStop}
				case "next_generation":
					cur.Generation++
					cur.State = session.LifecycleQueued
					cur.ExecutionID = nil
				}
				return nil
			})
			if err != nil {
				t.Fatalf("SETUP independent later outcome: %v", err)
			}
			path := qaReceiptJournalPath(f.al, f.child.SessionID)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
				t.Fatalf("obsolete intent reconciliation: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("obsolete Stop changed the %s session: identical journal=%v err=%v", state, bytes.Equal(before, after), err)
			}
			latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))
			line := latest[grant.ControlID]
			if len(latest) != 1 || line.Verb != "stop" || line.State != "superseded" || line.Reason == "" || len(line.LandedStop) != 0 {
				t.Errorf("obsolete intent created a new Stop or false landing: %+v, want only original superseded", latest)
			}
			fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
			pending, err := fresh.UnfinishedStopIntents(f.child.SessionID)
			if err != nil || len(pending) != 0 {
				t.Errorf("superseded obsolete control still queued: %+v/%v", pending, err)
			}
		})
	}
}

// A3: a persistent real append failure must not become a log-only success.
// The original durable intent remains queued after reopening the actual store.
func TestQAStopIntentFinisher_FailureVisibleAndIntentStillQueued(t *testing.T) {
	f := newQAReceiptFixture(t)
	grant, _ := qaReceiptAcceptFailedFence(t, f)
	err := qaReceiptRunBootFinisher(t, f.al)
	if err == nil || !strings.Contains(err.Error(), f.child.SessionID) || !strings.Contains(err.Error(), grant.ControlID) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("finisher failure = %v, want visible original session/control and real persist denial", err)
	}
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	cur, loadErr := fresh.Load(f.child.SessionID)
	if loadErr != nil || cur.State != f.child.State || cur.Stop != nil || cur.StopNote != nil {
		t.Errorf("failed finisher changed unfenced session: %+v/%v", cur, loadErr)
	}
	pending, loadErr := fresh.UnfinishedStopIntents(f.child.SessionID)
	if loadErr != nil || len(pending) != 1 {
		t.Fatalf("D6 QA1 persistent retry duplicated/lost the original intent: pending=%+v err=%v, want exactly the ORIGINAL queued intent, no replacement acceptance", pending, loadErr)
	}
	original := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))[grant.ControlID]
	if original.State != "queued" || len(original.LandedStop) != 0 {
		t.Errorf("failed finisher falsely finalized original control: %+v", original)
	}
	found := false
	for _, intent := range pending {
		if intent.Selection.Effect.ControlID == grant.ControlID {
			found = true
		}
	}
	if !found {
		t.Error("reopened finisher discovery dropped the original failed Stop intent")
	}
}
