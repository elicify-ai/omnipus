package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// R3 N8a (QA-02): the delivery mark of a request whose wake is STEERED into a
// live turn (session_worker.go::trySteerIntoLiveTurn). The R2-S1-01 bug was
// "marked delivered before the model got the text"; for this route the model has
// the text only once the steer is ACCEPTED. Two mutations of that function left
// 17 tests green: (M2) mark before the steer is accepted, (M2b) never mark.
// ORACLE: the F1 ruling - a held request is delivered, and its sender row moves
// on, exactly when a turn is given the text; a rejected steer falls back to the
// inbox and is delivered by the turn that really admits it.

// steerWakeFixture runs a worker inside a stuck turn and registers a HELD
// request for its session whose sender row is tracked by the delivered hook.
func steerWakeFixture(t *testing.T) (f *busyClearFixture, recorder *deliveredRecorder, requestID string, wake bus.InboundMessage) {
	t.Helper()
	prov := newStuckProvider()
	f = newBusyClearFixture(t, prov)
	t.Cleanup(func() { close(prov.release) })
	recorder = &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(recorder.hook)

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	require.True(t, f.w.inTurn.Load(), "precondition: the worker is inside a live turn")

	// Registered only now: the first turn's admission would otherwise run the
	// held-request delivery and drop this request (its asker session is not real).
	requestID = "req-steer-1"
	require.NoError(t, f.al.RequestLedger().Put(addressing.Capture{
		RequestID:         requestID,
		ReceiverSessionID: f.sid,
		Receiver:          addressing.Pair{WorkspaceID: "ws", AgentID: testDefaultAgentID},
		Sender:            addressing.Sender{Principal: "p"},
		Source:            addressing.Source{Kind: addressing.SourceConversation, SessionID: "src-session"},
		AdmittedAt:        time.Now().UTC(),
		Text:              "Request req-steer-1 from a person. please review",
		Held:              true,
		SenderMessageID:   "cm-steer-1",
	}))
	f.al.pendingRequests.Store(requestID, f.sid)

	wake = bus.InboundMessage{
		Channel: "webchat", ChatID: "request:" + requestID, SessionID: f.sid, TranscriptEntryID: requestID,
		Sender:   bus.SenderInfo{CanonicalID: "request:" + requestID},
		Content:  "Request req-steer-1 from a person. please review",
		Metadata: map[string]string{"agent_id": testDefaultAgentID, ownerWakeMetadataKey: "1"},
	}
	return f, recorder, requestID, wake
}

// An ACCEPTED steer delivers the request: the ledger says delivered and the
// sender's row moves on, once.
func TestSteerIntoLiveTurn_AcceptedSteerMarksTheRequestDeliveredAndTicksOnce(t *testing.T) {
	f, rec, id, wake := steerWakeFixture(t)

	require.True(t, f.w.trySteerIntoLiveTurn(wake), "the live turn accepts the steer")

	left, err := f.al.RequestLedger().Undelivered(f.sid)
	require.NoError(t, err)
	require.Empty(t, left, "request %s must be marked delivered once its text is steered into the turn", id)
	require.Equal(t, [][2]string{{"src-session", "cm-steer-1"}}, rec.snapshot(), "the sender's row moves on exactly once")
}

// A REJECTED steer delivers nothing: the request stays undelivered (held) and
// the message falls back to the inbox, to be delivered by the turn that admits it.
func TestSteerIntoLiveTurn_RejectedSteerLeavesTheRequestUndelivered(t *testing.T) {
	f, rec, _, wake := steerWakeFixture(t)
	// The session is pinned to an agent that is not registered, so route
	// resolution fails closed and enqueueSteeringFromMessage rejects. The wake
	// still names the live session, so a wrongly-placed mark would hit its ledger.
	ls := f.al.GetSessionLifecycleStore()
	require.NotNil(t, ls, "instrument: the fixture wires a lifecycle store")
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID: f.sid, Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "ghost-not-registered",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-x", RootSessionID: "parent-x"},
	}))
	require.Error(t, f.al.enqueueSteeringFromMessage(wake),
		"instrument: the fixture must make the steer reject, or this pin proves nothing")

	require.False(t, f.w.trySteerIntoLiveTurn(wake), "a rejected steer falls back to the inbox")

	left, err := f.al.RequestLedger().Undelivered(f.sid)
	require.NoError(t, err)
	require.Len(t, left, 1, "a request whose steer was rejected has not been given to any turn")
	require.Empty(t, rec.snapshot(), "and its sender's row must not move on")
}
