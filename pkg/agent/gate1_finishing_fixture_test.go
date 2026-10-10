package agent

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The real async completion's result is observed after its own done barrier,
// not by calling processFinishingItems or fabricating a terminal transition.
// The existing publication dependency is forward-only; all durable effects,
// commit identity, store appends and completion returns stay production-owned.
type gate1FinishingFixture struct {
	al          *AgentLoop
	child       *session.LifecycleRecord
	provider    *r1CompletionProvider
	publication *goalCommitPublicationGate
	flight      *steeredCompletionFlight
}

func gate1CommittedFinishing(t *testing.T) gate1FinishingFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "ws-gate1-finishing")
	publication := installGoalCommitGate(t, al)
	child, p := r1AdmitChild(t, al, parent, "gate1-real-finishing",
		"immutable old final", "new round answer", "answer after the remaining steer")
	p.open(0)
	select {
	case event := <-publication.event:
		committed := rootReopenedRecord(t, al, child.SessionID)
		raw, marshalErr := event.Message.MarshalJSON()
		if marshalErr != nil || committed.State != session.LifecycleCompleted || committed.FinalDelivery == nil ||
			committed.FinalDelivery.CommitID != child.ExecutionID.RunID || !bytes.Equal(committed.FinalDelivery.Payload, raw) {
			t.Fatalf("SETUP: actual post-commit finishing window lacks its producing completed/outbox tuple: rec=%+v err=%v", committed, marshalErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: actual dispatched completion never reached publication after commit")
	}
	claim := al.executionClaimFor(child)
	key := steeredCompletionFlightKey{loop: al, sessionID: claim.SessionID, generation: claim.Generation, bootSeq: claim.BootSeq, runID: claim.RunID}
	actual, found := steeredCompletionFlights.Load(key)
	flight, ok := actual.(*steeredCompletionFlight)
	if !found || !ok || flight == nil {
		t.Fatal("SETUP: no in-flight production completion result at the post-commit barrier")
	}
	return gate1FinishingFixture{al: al, child: child, provider: p, publication: publication, flight: flight}
}

func (f gate1FinishingFixture) accept(t *testing.T, text, correlation string) qaReceiptLine {
	t.Helper()
	id, status, err := f.al.EnqueueSteeringMessageWithStatus(f.child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: text}, correlation)
	if err != nil || id != correlation || status != EnqueueStatusPostFinish {
		t.Fatalf("SETUP: real accepted post-commit sink=(%q, %v, %v), want original correlation/finishing/nil", id, status, err)
	}
	accepted := qaReceiptAcceptedSteer(t, f.al, f.child.SessionID, text)
	f.al.steering.mu.Lock()
	transition := f.al.steering.terminalizing[f.child.SessionID]
	found := false
	if transition != nil {
		for _, item := range transition.finishingItems {
			if item.steerControlID == accepted.ControlID && item.correlationID == correlation && item.message.Content == text {
				found = true
			}
		}
	}
	f.al.steering.mu.Unlock()
	if !found {
		t.Fatalf("SETUP: exact accepted control %q is not owned by the real finishing buffer", accepted.ControlID)
	}
	return accepted
}

func (f gate1FinishingFixture) completionError(t *testing.T) error {
	t.Helper()
	select {
	case <-f.flight.done:
		return f.flight.err
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: actual completion result did not return after publication was released")
		return nil
	}
}

func gate1RequireQueuedItems(t *testing.T, al *AgentLoop, sessionID string, accepted ...qaReceiptLine) {
	t.Helper()
	al.steering.mu.Lock()
	items := append([]steeringQueueItem(nil), al.steering.queues[sessionID]...)
	al.steering.mu.Unlock()
	gotIDs := make([]string, len(items))
	gotMessages := make([]providers.Message, len(items))
	for i, item := range items {
		gotIDs[i], gotMessages[i] = item.steerControlID, item.message
	}
	wantIDs := make([]string, len(accepted))
	wantMessages := make([]providers.Message, len(accepted))
	latest := qaReceiptLatest(qaReceiptReadLines(t, al, sessionID))
	for i, line := range accepted {
		wantIDs[i], wantMessages[i] = line.ControlID, providers.Message{Role: "user", Content: line.Text}
		if got := latest[line.ControlID]; got.State != "queued" || got.Seq != line.Seq || got.Text != line.Text || got.SupersededBySeq != nil {
			t.Errorf("D4 queued receipt after refused delivery=%+v, want original queued control/seq/text with no superseder: %+v", got, line)
		}
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) || !reflect.DeepEqual(gotMessages, wantMessages) {
		t.Errorf("F3/D4: refused delivery retained queue depth=%d IDs=%q messages=%+v, want depth=%d IDs=%q exact messages=%+v; every accepted item must keep a live owner", len(items), gotIDs, gotMessages, len(accepted), wantIDs, wantMessages)
	}
}
