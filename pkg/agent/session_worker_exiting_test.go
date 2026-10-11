package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// S1-02: a message that reaches a worker in the instant it starts exiting (the
// dispatcher saw exiting=false, the push saw true) is not "busy" and is not
// dropped: the dispatcher is told to re-dispatch it to a fresh worker, and the
// person gets no false "agent is busy" reply.
func TestSessionWorker_MessageArrivingAsTheWorkerExits_IsRedispatchedNotReportedBusy(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	w := newSessionWorker("agent:"+testDefaultAgentID+":session:s1-02", al, func() {})
	w.exiting.Store(true)

	msg := bus.InboundMessage{Channel: "webchat", ChatID: "chat-s1-02", SessionID: "s1-02", Content: "hello"}
	handled, retry := w.enqueueOrRetry(msg)
	if handled || !retry {
		t.Fatalf("enqueueOrRetry on an exiting worker = handled %v retry %v, want not handled and retry", handled, retry)
	}
	select {
	case out := <-msgBus.OutboundChan():
		t.Fatalf("a false busy reply was sent for a message that only needs re-dispatching: %q", out.Content)
	case <-time.After(300 * time.Millisecond):
	}
	if n := w.waitingInboxCount(); n != 0 {
		t.Fatalf("the exiting worker kept %d message(s) it will never drain", n)
	}
}
