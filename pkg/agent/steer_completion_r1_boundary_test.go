package agent

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// R1 ruling, frozen D2 CRIT-001/D4: an answer from the provider is not a
// completed generation. These tests enter through real Dispatch and its
// disposeSteeredTurnResult caller; no completion sentinel is a user error.
func TestR1Completion_InputBeforeCommitContinuesSameGeneration(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "ws-r1-boundary")
	child, provider := r1AdmitChild(t, al, parent, "r1-before-commit", "candidate answer", "answer after the accepted steer")
	const text = "R1 BEFORE COMMIT: keep this exact instruction\nand this line break"
	var status EnqueueStatus
	var enqueueErr error
	r1InjectBeforeCommit(t, al, child, func() {
		_, status, enqueueErr = al.EnqueueSteeringMessageWithStatus(child.SessionID, testDefaultAgentID,
			providers.Message{Role: "user", Content: text}, "r1-before-commit-control")
	})
	provider.open(0) // First provider answer returns before the real commit hook.
	r1AwaitProvider(t, provider, 1)
	if enqueueErr != nil || status != EnqueueStatusPostFinish {
		t.Fatalf("accepted before commit: status=%v error=%v, want accepted finishing input", status, enqueueErr)
	}
	// The continuation is paused inside its real provider call. Reopen the
	// lifecycle journal: neither the candidate final nor a G+1 may exist.
	r1AssertNoFinal(t, al, child)
	got := rootReopenedRecord(t, al, child.SessionID)
	if got.ExecutionID == nil || *got.ExecutionID != *child.ExecutionID {
		t.Fatalf("pre-commit steer replaced its producing identity: got=%+v want=%+v", got.ExecutionID, child.ExecutionID)
	}
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider calls=%d, want original plus exactly one continuation", len(requests))
	}
	assertSteerRevivalInput1020(t, requests[1:], []string{text})
	entries, err := al.GetSessionStore().ReadTranscript(parent)
	if err != nil {
		t.Fatalf("ReadTranscript(parent before commit): %v", err)
	}
	for _, entry := range entries {
		if entry.SubagentEnd != nil && entry.SubagentEnd.SpanId == SubagentSpanID("r1-before-commit", child.Generation) {
			t.Error("candidate answer published a terminal end frame before the real commit")
		}
		if entry.SubagentState != nil && entry.SubagentState.ChildSessionId != nil && *entry.SubagentState.ChildSessionId == child.SessionID && entry.SubagentState.State == string(session.LifecycleCompleted) {
			t.Error("candidate answer published a completed state before processing the accepted instruction")
		}
		if entry.SubagentMessage != nil && entry.SubagentMessage.ChildSessionId != nil && *entry.SubagentMessage.ChildSessionId == child.SessionID && entry.SubagentMessage.Kind == "handback" {
			t.Error("candidate answer published a final handback frame before the real commit")
		}
	}
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1RequireSameGenerationFinal(t, al, child, "answer after the accepted steer")
	if count := len(provider.Requests()); count != 2 {
		t.Errorf("provider calls after disposal=%d, want exactly two with no duplicate continuation", count)
	}
}

// R1 post-commit row and D2 'Discovery after a newer-generation RESUME':
// committing G before acceptance makes a next round, but does not release G's
// owed final. The barrier forwards to the actual upward publisher unchanged.
func TestR1Completion_InputAfterCommitStartsNextGenerationPreservesOwedFinal(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "ws-r1-boundary")
	publication := installGoalCommitGate(t, al)
	child, provider := r1AdmitChild(t, al, parent, "r1-after-commit", "immutable committed old answer", "new-round answer")
	provider.open(0)
	var event steer.UpwardEvent
	select {
	case event = <-publication.event:
	case <-time.After(5 * time.Second):
		t.Fatal("real final never reached post-commit publication")
	}
	committed := rootReopenedRecord(t, al, child.SessionID)
	if committed.State != session.LifecycleCompleted || committed.FinalDelivery == nil {
		t.Fatalf("post-commit premise: state=%q outbox=%+v, want real completed+outbox", committed.State, committed.FinalDelivery)
	}
	raw, err := event.Message.MarshalJSON()
	if err != nil || !bytes.Equal(raw, committed.FinalDelivery.Payload) || committed.FinalDelivery.CommitID != child.ExecutionID.RunID {
		t.Fatalf("publication payload/identity differs from the actual commit: error=%v outbox=%+v", err, committed.FinalDelivery)
	}
	const text = "R1 AFTER COMMIT: start the next round exactly once"
	_, status, enqueueErr := al.EnqueueSteeringMessageWithStatus(child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: text}, "r1-after-commit-control")
	if enqueueErr != nil || status != EnqueueStatusPostFinish {
		t.Fatalf("post-commit steer status=%v error=%v, want accepted finishing input", status, enqueueErr)
	}
	publication.open()
	r1AwaitProvider(t, provider, 1)
	current := rootReopenedRecord(t, al, child.SessionID)
	if current.Generation != child.Generation+1 || current.State != session.LifecycleRunning || current.ExecutionID == nil || current.ExecutionID.RunID == child.ExecutionID.RunID {
		t.Fatalf("post-commit next-round admission: G=%d state=%q identity=%+v, want G=%d fresh running execution", current.Generation, current.State, current.ExecutionID, child.Generation+1)
	}
	old, _, _, _, readErr := al.GetSessionLifecycleStore().CommittedFinalDelivery(child.SessionID, child.Generation, child.ExecutionID.RunID)
	if readErr != nil || !bytes.Equal(old.Payload, raw) || old.MessageID != committed.FinalDelivery.MessageID || old.PayloadHash != committed.FinalDelivery.PayloadHash {
		t.Fatalf("old committed final was changed/hidden by G+1: old=%+v error=%v", old, readErr)
	}
	r1AssertFinals(t, al, child, map[string]string{fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation): "immutable committed old answer"})
	// Retry the older committed obligation while G+1 is actually in flight.
	// The production delivery-only path must deduplicate G's final and leave
	// the new execution unchanged; no boot/revival path is simulated here.
	recovery := &SteerBootRecovery{Lifecycle: al.GetSessionLifecycleStore(), Sessions: al.GetSessionStore(),
		Inbox: al.GetMessageInboxStore(), Deliverer: publication.real}
	if retryErr := recovery.runFinalDeliveryPass(context.Background()); retryErr != nil {
		t.Fatalf("retry owed old G final during G+1: %v", retryErr)
	}
	if afterRetry := rootReopenedRecord(t, al, child.SessionID); !reflect.DeepEqual(afterRetry, current) {
		t.Errorf("old-final retry rewrote G+1: before=%+v after=%+v", current, afterRetry)
	}
	r1AssertFinals(t, al, child, map[string]string{fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation): "immutable committed old answer"})
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{text})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	if len(provider.Requests()) != 2 || al.pendingSteeringCountForScope(child.SessionID) != 0 {
		t.Fatalf("post-commit input duplicated or stranded: calls=%d pending=%d", len(provider.Requests()), al.pendingSteeringCountForScope(child.SessionID))
	}
	r1AssertFinals(t, al, child, map[string]string{
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation):   "immutable committed old answer",
		fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation+1): "Follow-up after a late instruction: new-round answer",
	})
}
