package agent

// Frozen D2/T11's commit-before-publication and the no-silent-error rule:
// the ACTUAL queued-promotion caller must expose a failed owning commit. A
// stopped-only old reporter or a failed fake deliverer does not exercise it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestQueuedPromotion_RealOutcomeCommitFailureReachesParent(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("owning_append_fault_%v", fault), func(t *testing.T) { promotionCommitCase(t, fault) })
	}
}

func promotionCommitCase(t *testing.T, fault bool) {
	t.Helper()
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	blocker := newGoalRunGate("blocker real final", nil)
	installGoalRunProvider(t, al, blocker)
	parent := newTestSteeringSession(t, al, "ws-real-promotion-fault")
	u2LaunchLive(t, al, parent, "call-promotion-blocker", blocker)
	id, gen := launchParkedChild(t, al, parent, "call-promotion-failure", "queued instruction must never be rerun by error recovery")
	dispatchChild(t, al, id, gen, false)
	selected := rootReopenedRecord(t, al, id)
	if selected.ExecutionID == nil || selected.State != session.LifecycleQueued || al.steerAdmission().queueLen() != 1 {
		t.Fatal("SETUP: real original queued admission was not stamped and waiting")
	}
	claim := al.executionClaimFor(selected)
	// Normal registry removal models a profile becoming unavailable after its
	// admission queued. The running blocker already owns its actual instance;
	// reconstruction of the promoted admission now genuinely fails. No dispatcher
	// or reporter is mocked, and no lifecycle state/identity is edited.
	registry := al.GetRegistry()
	inst, ok := registry.GetAgent(selected.AgentID)
	if !ok || !registry.RemoveAgent(selected.AgentID) {
		t.Fatal("SETUP: actual admitted profile could not be removed")
	}
	t.Cleanup(func() { registry.UpsertAgent(inst) })
	journal := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", id+".jsonl")
	if fault {
		if err := os.Chmod(journal, 0o400); err != nil {
			t.Fatalf("install real owning-journal append fault: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(journal, 0o600); err != nil {
				t.Errorf("restore owning-journal permissions: %v", err)
			}
		})
		// Test the instrument before trusting the fault: reads succeed while the
		// EXACT open mode used by fileutil.AppendJSONL is genuinely refused.
		read, err := al.GetSessionLifecycleStore().Load(id)
		if err != nil || !claim.matches(read) {
			t.Fatalf("instrument: selected owning journal is not independently readable: %+v err=%v", read, err)
		}
		f, err := os.OpenFile(journal, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
		if err == nil {
			f.Close()
			t.Fatal("BLOCKED: environment bypasses read-only journal permissions; real append-denial instrument cannot prove D2 commit failure")
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("instrument: append refusal=%v, want permission error while reads succeed", err)
		}
	}
	blocker.open()
	// Actual release promotes the real queued immutable identity through
	// drainSteerQueue -> dispatchSteeredSessionReserved ->
	// reportSteeredExecutionFailure -> commitSteeredCompletion. Join every tail.
	joinGoalFixtureRuns(t, al)
	rec := rootReopenedRecord(t, al, id)
	if !claim.matches(rec) {
		t.Errorf("failed promotion lost/replaced original genuine admission: before=%+v after=%+v", claim, rec.ExecutionID)
	}
	if al.getActiveTurnState(id) != nil || al.steerAdmission().hasReservation(id, gen) || al.steerAdmission().queueLen() != 0 {
		t.Error("failed promotion silently redispatched or retained an active/queued consumer")
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parent, id, "", 10)
	if err != nil {
		t.Fatalf("read actual direct-parent failure surface: %v", err)
	}
	if !fault {
		if rec.State != session.LifecycleFailed || rec.FinalDelivery == nil || rec.FinalDelivery.CommitID != claim.RunID || rec.FinalDelivery.MessageID != fmt.Sprintf("%s:%d:final", id, gen) {
			t.Fatalf("positive control did not commit actual failed outcome/outbox: %+v", rec)
		}
		if len(msgs) != 1 {
			t.Fatalf("positive failure parent messages=%d, want exactly one committed fatal outcome", len(msgs))
		}
		failure, err := msgs[0].AsSessionMessageError()
		if err != nil || !failure.Fatal || !strings.Contains(failure.Text, "dispatch_failed:") {
			t.Errorf("positive control did not expose the real promotion failure: %+v err=%v", failure, err)
		}
		return
	}
	if rec.FinalDelivery != nil || rec.Terminal() {
		t.Errorf("failed owning append falsely created committed final/outcome: %+v", rec)
	}
	if len(msgs) == 0 {
		t.Error("actual promotion outcome/outbox commit failed, but the real caller gave its direct parent no visible failure/pending notice; the original queued claim has no consumer or committed recovery outbox — a log is not error propagation")
	}
	for _, msg := range msgs {
		failure, err := msg.AsSessionMessageError()
		if err != nil || failure.Fatal || strings.TrimSpace(failure.Text) == "" {
			t.Errorf("uncommitted promotion error must be an explicit nonempty error notice, never a delivered fatal final: %+v err=%v", failure, err)
		}
		if messageIDOf(msg) == fmt.Sprintf("%s:%d:final", id, gen) {
			t.Error("uncommitted failure consumed the generation's protected final identity")
		}
	}
	// Restore the real writer without dispatching the failed instruction again.
	if err := os.Chmod(journal, 0o600); err != nil {
		t.Fatalf("repair owning journal: %v", err)
	}
	pending, err := al.GetSessionLifecycleStore().ListPendingFinalDeliveries()
	if err != nil {
		t.Fatalf("discover committed recovery after repair: %v", err)
	}
	for _, delivery := range pending {
		if delivery.SessionID == id && delivery.Commit.CommitID != claim.RunID {
			t.Errorf("recovery discovered a fabricated/replacement failure claim: %+v", delivery)
		}
	}
	// A future pre-commit failure ledger, if implemented, must be independently
	// CHECKed for restart discovery/retry; this test cannot certify a log as one.
}
