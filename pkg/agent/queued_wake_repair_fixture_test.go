package agent

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Only the existing provider double is controlled. Launch, boot-epoch mint,
// dispatch, queued-wake intake, lifecycle persistence and promotion stay real.
type queuedWakeNegativeFixture struct {
	al          *AgentLoop
	provider    *queuedWakeCaptureProvider
	childID     string
	generation  int
	owner       executionClaim
	releaseBusy func()
}

func newQueuedWakeNegativeFixture(t *testing.T) *queuedWakeNegativeFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1 // One real busy turn forces FIFO admission.
	provider := &queuedWakeCaptureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	instance, found := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !found {
		t.Fatal("SETUP: registered test agent is missing")
	}
	instance.Provider = provider
	var once sync.Once
	releaseBusy := func() { once.Do(func() { close(provider.release) }) }
	t.Cleanup(releaseBusy) // Registered after Close, so release happens before the join.
	parent := newTestSteeringSession(t, al, "ws-queued-wake-negatives")
	busyID, busyGen := launchParkedChild(t, al, parent, "call-queued-negative-blocker", "occupy the only admission slot")
	childID, childGen := launchParkedChild(t, al, parent, "call-queued-negative-recipient", "original launch instruction")
	dispatchChild(t, al, busyID, busyGen, true)
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: the real blocker did not reach the model boundary")
	}
	dispatchChild(t, al, childID, childGen, false)
	rec := rootReopenedRecord(t, al, childID)
	owner := queuedWakeNegativeOwner(t, rec)
	f := &queuedWakeNegativeFixture{al: al, provider: provider, childID: childID,
		generation: childGen, owner: owner, releaseBusy: releaseBusy}
	if rec.State != session.LifecycleQueued || rec.Generation != childGen || len(rec.PendingUserMessages) != 0 {
		t.Fatalf("SETUP: recipient is not an empty queued admission: %+v", rec)
	}
	f.requirePending(t, owner, "", "")
	return f
}

// Read the four fields directly: the assertion does not reuse the production
// claim matcher that the stale-owner tests are meant to exercise.
func queuedWakeNegativeOwner(t *testing.T, rec *session.LifecycleRecord) executionClaim {
	t.Helper()
	if rec == nil || rec.ExecutionID == nil || rec.ExecutionID.RunID == "" || rec.ExecutionID.BootSeq == 0 {
		t.Fatalf("record lacks a genuine full execution identity: %+v", rec)
	}
	return executionClaim{SessionID: rec.SessionID, Generation: rec.Generation,
		RunID: rec.ExecutionID.RunID, BootSeq: rec.ExecutionID.BootSeq}
}

func queuedWakeNegativeQueue(al *AgentLoop) []steerQueueEntry {
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	entries := slices.Clone(gate.queue)
	for i := range entries {
		entries[i].wakeInputs = slices.Clone(entries[i].wakeInputs)
		for j := range entries[i].wakeInputs {
			if wake := entries[i].wakeInputs[j].wake; wake != nil {
				copyWake := *wake
				entries[i].wakeInputs[j].wake = &copyWake
			}
		}
	}
	return entries
}

func queuedWakeNegativeMessage(childID string, generation int, messageID, content string) bus.InboundMessage {
	return bus.InboundMessage{Channel: "system", AsyncTranscriptSessionID: childID, Content: content,
		Metadata: map[string]string{"steer_message_id": messageID, "steer_generation": strconv.Itoa(generation)}}
}

func (f *queuedWakeNegativeFixture) requirePending(t *testing.T, owner executionClaim, messageID, content string) {
	t.Helper()
	rec := rootReopenedRecord(t, f.al, f.childID)
	if got := queuedWakeNegativeOwner(t, rec); got != owner || rec.State != session.LifecycleQueued {
		t.Fatalf("queued owner/state = %+v/%s, want %+v/%s", got, rec.State, owner, session.LifecycleQueued)
	}
	wantEntry := steerQueueEntry{sessionID: owner.SessionID, generation: owner.Generation, runID: owner.RunID, bootSeq: owner.BootSeq}
	var wantPending []string
	if messageID != "" {
		wantPending = []string{content}
		wantEntry.wakeInputs = []steeringQueueItem{{message: providers.Message{Role: "user", Content: content},
			wake: &steeringWake{messageID: messageID, transcriptSessionID: owner.SessionID, agentID: testDefaultAgentID}}}
	}
	if !slices.Equal(rec.PendingUserMessages, wantPending) {
		t.Errorf("pending payloads = %q, want exactly %q", rec.PendingUserMessages, wantPending)
	}
	if got := queuedWakeNegativeQueue(f.al); !queuedWakeNegativeQueuesEqual(got, []steerQueueEntry{wantEntry}) {
		t.Errorf("admission queue = %+v, want exactly the original owner and payload %+v", got, wantEntry)
	}
	if f.al.steerAdmission().activeCount() != 1 || f.al.getActiveTurnState(f.childID) != nil {
		t.Error("queued recipient consumed a slot or registered a live turn before promotion")
	}
}

func (f *queuedWakeNegativeFixture) requirePromotedOnce(t *testing.T, owner executionClaim, accepted, refused string) {
	t.Helper()
	f.releaseBusy()
	joinGoalFixtureRuns(t, f.al) // Join the real promotion and its entire durable completion tail.
	f.provider.mu.Lock()
	requests := slices.Clone(f.provider.requests)
	f.provider.mu.Unlock()
	if len(requests) != 2 { // Exactly one blocker call plus one rightful recipient call.
		t.Fatalf("provider calls = %d, want exactly blocker + one rightful recipient", len(requests))
	}
	parts := make([]string, 0, len(requests[1]))
	for _, message := range requests[1] {
		parts = append(parts, message.Content)
	}
	prompt := strings.Join(parts, "\n")
	if got := strings.Count(prompt, accepted); got != 1 {
		t.Errorf("promoted prompt contains accepted payload %d times, want exactly once: %q", got, prompt)
	}
	if refused != "" && strings.Contains(prompt, refused) {
		t.Errorf("promoted prompt consumed refused/stale payload %q: %q", refused, prompt)
	}
	finished := rootReopenedRecord(t, f.al, f.childID)
	if got := queuedWakeNegativeOwner(t, finished); got != owner || finished.State != session.LifecycleCompleted {
		t.Fatalf("completed owner/state = %+v/%s, want %+v/%s", got, finished.State, owner, session.LifecycleCompleted)
	}
	if finished.FinalDelivery == nil || finished.FinalDelivery.CommitID != owner.RunID || finished.FinalDelivery.Generation != owner.Generation {
		t.Errorf("final outbox = %+v, want the rightful admission's run %q/generation %d", finished.FinalDelivery, owner.RunID, owner.Generation)
	}
	if len(finished.PendingUserMessages) != 0 || f.al.steerAdmission().activeCount() != 0 || f.al.steerAdmission().queueLen() != 0 {
		t.Errorf("after completion: pending=%q active/queued=%d/%d, want all empty", finished.PendingUserMessages,
			f.al.steerAdmission().activeCount(), f.al.steerAdmission().queueLen())
	}
}

func (f *queuedWakeNegativeFixture) accept(t *testing.T, messageID, content string, generation int) {
	t.Helper()
	reply, err := f.al.processSteeredSystemWake(context.Background(), queuedWakeNegativeMessage(f.childID, generation, messageID, content))
	if err != nil || reply != "" {
		t.Fatalf("queued wake %q: reply=%q error=%v, want empty reply and accepted input", messageID, reply, err)
	}
}

// Compare every admission field without reflecting through disposition errors.
// Queued entries have no disposition; pointer identity also pins that invariant.
func queuedWakeNegativeQueuesEqual(got, want []steerQueueEntry) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.sessionID != w.sessionID || g.generation != w.generation || g.runID != w.runID || g.bootSeq != w.bootSeq || g.disposition != w.disposition {
			return false
		}
		if !reflect.DeepEqual(g.wakeInputs, w.wakeInputs) {
			return false
		}
	}
	return true
}
