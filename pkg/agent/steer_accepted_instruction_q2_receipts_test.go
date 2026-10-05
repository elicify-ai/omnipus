package agent

import "testing"

// C: the three W2 Q2 cases keep their entire DeepEqual oracle unchanged
// except for the newly specified receipt identity. Derive that identity from
// the REAL durable queued acceptance, not from the queue output. DeepEqual
// still checks every message, wake, correlation, ordering, and returned item.
func q2ReceiptEnqueuePlain(t *testing.T, al *AgentLoop, sessionID, correlationID, text string) steeringQueueItem {
	t.Helper()
	want := q2ConsumerEnqueuePlain(t, al, sessionID, correlationID, text)
	line := qaReceiptAcceptedSteer(t, al, sessionID, text)
	if line.ControlID == "" {
		t.Fatal("D4 accepted delegate steer has no durable control identity")
	}
	want.steerControlID = line.ControlID
	return want
}
