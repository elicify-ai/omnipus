package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// U7 — FR-024 discard of undelivered human web/channel input (spec
// docs/internal/specs/session-core-spec.md §FR-024, BDD-07.3):
//
//	"Stop MUST discard webchat/channel input not yet committed at the safe
//	 boundary into selected agent/model input, not merely bus-admitted. ...
//	 Show discarded/not delivered; preserve archived bytes/already-consumed
//	 input."
//
// The oracle is implementation-agnostic: the observable is what remains
// pending in the session's steering queue after a REAL Stop. A queued human
// item carries no steer control id (pkg/agent/steering.go::
// AgentLoop.enqueueSteeringMessage builds `steeringQueueItem{message,correlation}`
// with steerControlID empty), which is exactly the "undelivered, not
// committed" input FR-024 says Stop must discard — the Stop's current
// queue-prune (AgentLoop.supersedePendingSteers ->
// takeSteerReceiptItemsScope) removes only receipt-bearing delegate steers and
// leaves the human item standing.
//
// The live turn is a real ordinary root with a provider-held call (via
// newStopRedirectRoot, the same fixture stop_redirect_root_test.go uses), so
// the queue item is genuinely "admitted but not yet consumed into a model
// turn" when the Stop lands — no manufactured lifecycle record or fake
// execution identity.

const sessionCoreU7QueuedHumanText = "Second human web message that must be discarded by Stop."

// TestSessionCoreU7_StopDiscardsQueuedHumanInput is the FR-024 behaviour half.
// RED on the current code: after the real Stop, the queued human item is still
// pending for the session (the Stop prune skips control-id-less items).
func TestSessionCoreU7_StopDiscardsQueuedHumanInput(t *testing.T) {
	al, rec, _, humanDone := newStopRedirectRoot(t)
	sessionID := rec.SessionID

	if _, status, err := al.enqueueSteeringMessage(sessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: sessionCoreU7QueuedHumanText}, "corr-u7-queued-h2"); err != nil {
		t.Fatalf("SETUP: enqueue the queued human web message: %v", err)
	} else if status != EnqueueStatusNormal {
		t.Fatalf("SETUP: queued human message status = %v, want EnqueueStatusNormal (it must sit in the main queue)", status)
	}
	if got := al.pendingSteeringCountForScope(sessionID); got != 1 {
		t.Fatalf("SETUP: pending steering count before Stop = %d, want 1 (the queued human message)", got)
	}

	if _, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "u7-owner"},
		Channel:   "webchat",
	}); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	select {
	case <-humanDone: // the parked turn is released by the Stop
	case <-time.After(5 * time.Second):
		t.Fatal("the parked human turn did not join after the Stop")
	}

	if got := al.pendingSteeringCountForScope(sessionID); got != 0 {
		t.Errorf("FR-024: %d human web message(s) still pending after Stop, want 0 — "+
			"undelivered web input must be discarded at the safe boundary, not left for a later turn "+
			"(BDD-07.3: waiting human IDs discarded/not delivered, never later consumed)", got)
	}
}

// TestSessionCoreU7_StopPreservesCommittedInput is the FR-024 guard: the input
// already committed into the live turn ("H1") must NOT be discarded — only
// undelivered input is. GREEN by design (it pins the already-working
// preservation side), and it must stay green after the discard is added.
func TestSessionCoreU7_StopPreservesCommittedInput(t *testing.T) {
	al, rec, _, _ := newStopRedirectRoot(t)
	sessionID := rec.SessionID

	if _, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "u7-owner"},
		Channel:   "webchat",
	}); err != nil {
		t.Fatalf("StopSession: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("read the chat's transcript: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Role == "user" && e.Content == stopRedirectRootHumanTask {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FR-024: the already-committed human message %q vanished from the transcript after Stop — "+
			"discard must preserve already-consumed input (archived bytes intact)", stopRedirectRootHumanTask)
	}
}
