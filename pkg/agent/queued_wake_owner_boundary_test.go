package agent

// D2/T27, amendment C1, and the bounded queue repair brief: an already queued
// actual admission retains its ORIGINAL full owner tuple while receiving two
// distinct system wakes. Replaying a queued message ID is not new work. This
// complements (does not edit) the existing two-wakes/order/once/count test.
// Only the external model is controlled; every identity/queue/commit is real.
// Mutation and independent CHECK remain deferred to a different author.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestQueuedSystemWakes_PreserveOriginalOwnerTupleAndIgnoreQueuedDuplicateIDs(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("queued_duplicate_ids_%v", duplicate), func(t *testing.T) {
			queuedWakeOwnerBoundaryCase(t, duplicate)
		})
	}
}

func queuedWakeOwnerBoundaryCase(t *testing.T, duplicate bool) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1 // Smallest cap: one real busy model call forces one queued admission.
	provider := &queuedWakeCaptureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	instance, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !found {
		t.Fatal("SETUP: real registered agent missing")
	}
	instance.Provider = provider
	var once sync.Once
	releaseBusy := func() { once.Do(func() { close(provider.release) }) }
	t.Cleanup(releaseBusy)
	parent := newTestSteeringSession(t, al, "ws-queued-owner-boundary")
	busyID, busyGeneration := launchParkedChild(t, al, parent, "call-queued-owner-busy", "hold the only actual admission slot")
	queuedID, queuedGeneration := launchParkedChild(t, al, parent, "call-queued-owner-recipient", "original queued launch instruction")
	dispatchChild(t, al, busyID, busyGeneration, true)
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: genuine busy admission did not reach its model boundary")
	}
	dispatchChild(t, al, queuedID, queuedGeneration, false)
	original := rootReopenedRecord(t, al, queuedID)
	if original.State != session.LifecycleQueued || original.Generation != queuedGeneration || original.ExecutionID == nil || original.ExecutionID.RunID == "" || original.ExecutionID.BootSeq != al.bootEpochFor() || len(original.PendingUserMessages) != 0 {
		t.Fatalf("SETUP: no original real queued execution to preserve: %+v", original)
	}
	owner := al.executionClaimFor(original)
	if got := queuedWakeQueueClaims(al); !slices.Equal(got, []executionClaim{owner}) || al.steerAdmission().activeCount() != 1 {
		t.Fatalf("SETUP: queue does not own the original FULL tuple: got=%+v want=%+v", got, owner)
	}
	const first = "Owner control first wake: inspect the delegated evidence"
	const second = "Owner control second wake: retain both distinct instructions"
	contents := []string{first, second}
	pending := make([]string, 0, len(contents))
	for index, content := range contents {
		// System wake identities are input tokens, not execution identities;
		// this published path explicitly permits wakes without inbox entries.
		id := fmt.Sprintf("queued-owner-input:%s:%d", queuedID, index)
		msg := bus.InboundMessage{Channel: "system", AsyncTranscriptSessionID: queuedID, Content: content,
			Metadata: map[string]string{"steer_message_id": id, "steer_generation": strconv.Itoa(queuedGeneration)}}
		pending = append(pending, content)
		repeats := 1
		if duplicate {
			repeats = 2
		}
		for attempt := 0; attempt < repeats; attempt++ {
			if _, err := al.processSteeredSystemWake(context.Background(), msg); err != nil {
				t.Errorf("actual queued wake index=%d retry=%d was refused instead of retained on original owner: %v", index, attempt, err)
			}
			current := rootReopenedRecord(t, al, queuedID)
			if current.State != session.LifecycleQueued || al.executionClaimFor(current) != owner {
				t.Errorf("queued wake replaced original FULL tuple: before=%+v after=%+v state=%s", owner, al.executionClaimFor(current), current.State)
			}
			if !slices.Equal(current.PendingUserMessages, pending) {
				t.Errorf("queued wake index=%d retry=%d pending=%q want=%q; distinct input lost or duplicate ID treated as new instruction", index, attempt, current.PendingUserMessages, pending)
			}
			if got := queuedWakeQueueClaims(al); !slices.Equal(got, []executionClaim{owner}) {
				t.Errorf("queued wake changed/duplicated original queue owner: got=%+v want exactly=%+v", got, owner)
			}
			if al.steerAdmission().activeCount() != 1 || al.getActiveTurnState(queuedID) != nil {
				t.Error("queued wake consumed another slot or started a recipient before promotion")
			}
			entries, err := al.GetSessionStore().ReadTranscript(queuedID)
			if err != nil {
				t.Fatalf("read genuine queued transcript: %v", err)
			}
			for _, entry := range entries {
				if entry.Content == "consumed "+id {
					t.Errorf("queued input %q was marked consumed before the recipient's actual execution", id)
				}
			}
		}
	}
	releaseBusy()
	joinGoalFixtureRuns(t, al) // Join actual promotion and completion/outbox tail, not a state-value poll.
	provider.mu.Lock()
	requests := append([][]providers.Message(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider executions=%d, want exactly blocker+one original queued owner", len(requests))
	}
	parts := make([]string, 0, len(requests[1]))
	for _, message := range requests[1] {
		parts = append(parts, message.Content)
	}
	prompt := strings.Join(parts, "\n")
	firstAt, secondAt := strings.Index(prompt, first), strings.Index(prompt, second)
	if firstAt < 0 || secondAt <= firstAt || strings.Count(prompt, first) != 1 || strings.Count(prompt, second) != 1 {
		t.Errorf("original queued execution did not receive both input IDs once in order: %q", prompt)
	}
	finished := rootReopenedRecord(t, al, queuedID)
	if al.executionClaimFor(finished) != owner || finished.State != session.LifecycleCompleted || finished.FinalDelivery == nil {
		t.Fatalf("promotion/completion lost ORIGINAL full tuple or its real outbox: owner=%+v current=%+v state=%s outbox=%+v", owner, al.executionClaimFor(finished), finished.State, finished.FinalDelivery)
	}
	out := finished.FinalDelivery
	if out.CommitID != owner.RunID || out.Generation != owner.Generation || out.ParentSessionID != parent || out.MessageID != fmt.Sprintf("%s:%d:final", owner.SessionID, owner.Generation) || out.Outcome != string(steer.OutcomeFinalAnswer) {
		t.Errorf("real final outbox is not bound to the original queued execution: %+v want owner=%+v parent=%s", out, owner, parent)
	}
	if len(finished.PendingUserMessages) != 0 || al.steerAdmission().activeCount() != 0 || al.steerAdmission().queueLen() != 0 {
		t.Errorf("queued inputs/slots remained after complete original-owner disposal: pending=%q active/queued=%d/%d", finished.PendingUserMessages, al.steerAdmission().activeCount(), al.steerAdmission().queueLen())
	}
}

func queuedWakeQueueClaims(al *AgentLoop) []executionClaim {
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	claims := make([]executionClaim, 0, len(gate.queue))
	for _, entry := range gate.queue {
		claims = append(claims, entry.executionClaim())
	}
	return claims
}
