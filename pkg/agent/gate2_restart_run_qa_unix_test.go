//go:build linux || darwin

package agent

import (
	"context"
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Architect A4. Oracle: control-plane ADR D2 full execution identity, D8.1/5:
// finish only an intent owning the current run; independently restart-stop
// every interrupted current run. The gateway's actual order is recovery.Run
// followed by FinishUnfinishedStopIntents (gateway_boot.go::wireSteerDeps).
// An old same-generation intent is NOT a recovery exemption for replacement B.
// The original and B are really admitted goal turns, not forged running records.
// Fault injection is only a denied concrete lifecycle append AFTER acceptance.
func TestQAGate2Restart_OldSameGenerationStopIntentCannotExemptReplacementRun(t *testing.T) {
	f := newG5GoalTailFixture(t, "qa2-a4-original-goal-run")
	// PW-D6 (founder 2026-10-15): stage one real provider call for boot's native continuation.
	f.worker.mu.Lock()
	f.worker.answers = append(f.worker.answers, "continued after the restart")
	f.worker.release = append(f.worker.release, make(chan struct{}))
	f.worker.mu.Unlock()
	fixture := qaReceiptFixture{al: f.al, store: f.al.GetSessionStore(), parentID: f.parentID, child: f.original}
	grant, restore := qaReceiptAcceptFailedFence(t, fixture)
	restore()
	intents, err := session.NewLifecycleStore(f.lifecycle.Dir()).UnfinishedStopIntents(f.original.SessionID)
	if err != nil || len(intents) != 1 || intents[0].Selection.Effect.ControlID != grant.ControlID || intents[0].Selection.Effect.Target.RunID != f.original.ExecutionID.RunID {
		t.Fatalf("SETUP reopened queued intent = %+v/%v, want exact original run/control after refused fence", intents, err)
	}

	dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), f.original.SessionID, f.original.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP real same-generation B admission = %+v/%v, want running", dispatched, err)
	}
	r1AwaitProvider(t, f.worker, 1)
	b := qa2ReopenedLifecycleRecord(t, f.al, f.original.SessionID)
	if b.ExecutionID == nil || b.Generation != f.original.Generation || b.ExecutionID.BootSeq != f.original.ExecutionID.BootSeq || reflect.DeepEqual(b.ExecutionID, f.original.ExecutionID) {
		t.Fatalf("SETUP B identity = %+v, original = %+v, want same generation/boot and a distinct admitted run", b.ExecutionID, f.original.ExecutionID)
	}
	ts := f.al.getActiveTurnState(b.SessionID)
	if ts == nil || ts.opts.executionDisposition == nil {
		t.Fatal("SETUP real B has no owning disposal barrier")
	}
	f.worker.open(1)
	select {
	case <-ts.opts.executionDisposition.done:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP B goal turn did not dispose without a terminal claim")
	}
	beforeBoot := qa2ReopenedLifecycleRecord(t, f.al, b.SessionID)
	if beforeBoot.State != session.LifecycleRunning || beforeBoot.Stop != nil || beforeBoot.FinalDelivery != nil || !reflect.DeepEqual(beforeBoot.ExecutionID, b.ExecutionID) {
		t.Fatalf("SETUP interrupted B = %+v, want unfenced/uncommitted working run", beforeBoot)
	}

	// A genuinely newer writing boot, real reopened disk readers, and no
	// surviving producer. No identity is hard-coded and boot dispatch is not
	// simulated by the test. Reopening retains the old acceptance exactly.
	freshLifecycle := session.NewLifecycleStore(f.lifecycle.Dir())
	freshSessions, err := session.NewUnifiedStoreWithHome(f.al.GetSessionStore().BaseDir(), f.al.GetConfig().Agents.Defaults.Home)
	if err != nil {
		t.Fatalf("reopen real conversation store: %v", err)
	}
	t.Cleanup(func() {
		if cerr := freshSessions.Close(); cerr != nil {
			t.Errorf("close reopened conversation store: %v", cerr)
		}
	})
	f.al.SetSessionMessagingStores(f.inbox, freshLifecycle)
	writingBoot := mintGenuineBootEpochForLoop(t, f.al)
	if writingBoot.Current() <= b.ExecutionID.BootSeq {
		t.Fatalf("SETUP writing boot %d is not after B's actual admitting boot %d", writingBoot.Current(), b.ExecutionID.BootSeq)
	}
	var operatorNotices []string
	recovery := u1BootRecovery(t, f.al, &operatorNotices)
	recovery.Sessions = freshSessions
	recovery.Classifier = NewSteerRecordClassifier(freshLifecycle, freshSessions)
	if err = recovery.Run(context.Background()); err != nil {
		t.Fatalf("real first gateway boot stage failed: %v; notices=%v", err, operatorNotices)
	}
	if err = f.al.FinishUnfinishedStopIntents(context.Background()); err != nil {
		t.Fatalf("real second gateway boot stage failed: %v", err)
	}

	// PW-D6/PW-D19 (founder 2026-10-15): A's obsolete selection does not stop B.
	// Preserve B's exact restart landing in history, then require its native continuation.
	landing := pwLegacyRestartLanding(t, f.al, b.SessionID, writingBoot.Current())
	if landing.Stop != nil || landing.StopNote.By != session.StopActorRestart || !reflect.DeepEqual(landing.ExecutionID, b.ExecutionID) {
		t.Errorf("A4: B's restart landing lost its exact selected execution/epoch: %+v, want B=%+v", landing, b.ExecutionID)
	}
	got := qa2ReopenedLifecycleRecord(t, f.al, b.SessionID)
	if got.State != session.LifecycleRunning || got.ExecutionID == nil || got.ExecutionID.RunID == b.ExecutionID.RunID || got.ExecutionID.BootSeq != writingBoot.Current() {
		t.Errorf("PW-D6: replacement B did not continue after restart: state=%s execution=%+v, want a fresh native admission", got.State, got.ExecutionID)
	} else {
		r1AwaitProvider(t, f.worker, 2)
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, b.SessionID))
	if old := latest[grant.ControlID]; old.State != "superseded" || old.Reason == "" || len(old.LandedStop) != 0 {
		t.Errorf("A4: original run A's obsolete intent = %+v, want explicitly superseded, never falsely landed on B", old)
	}
	if len(f.worker.Requests()) != 3 {
		t.Errorf("PW-D6: provider calls=%d, want the two pre-boot admissions plus one native restart continuation", len(f.worker.Requests()))
	}
	if g := goalRecordForSession(t, b.SessionID); g.State != generated.GoalStateActive {
		t.Errorf("restart ended B's session-owned goal: %q, want active (D8)", g.State)
	}
	transitions, err := f.al.GetSessionLifecycleStore().ListStoppedTransitions(b.SessionID)
	if err != nil || len(transitions) != 1 || transitions[0].Cause != session.StopCauseRestart {
		t.Fatalf("B's durable restart transitions=%+v error=%v, want exactly one", transitions, err)
	}
	id := w1hNoticeID(f.parentID, b.SessionID, b.Generation, transitions[0].StopSeq)
	notices := w1hNoticesWithID(t, f.al, f.parentID, id)
	if len(notices) != 1 {
		t.Fatalf("B's durable restart notices=%d, want exactly one", len(notices))
	}
	w1hAssertNoticeMatchesTransition(t, notices[0], f.parentID, transitions[0])
}
