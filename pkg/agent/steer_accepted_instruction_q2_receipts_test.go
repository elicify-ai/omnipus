package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Ledgered delegate fixtures keep Task C's durable control identity check.
// Derive that identity from the REAL durable queued acceptance, not from
// queue output. These are not wake-marker tests' plain correlated fillers.
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

// q2ConsumerEnqueueFiller restores the original Q2 plain queue-item fixture:
// correlated input around a wake, not a delegate-steer delivery subject.
// Use the real queue, but deliberately not EnqueueSteeringMessage: since
// Task C that public delegate entry point accepts a durable ledger control.
// B2 (D4 Delivery) requires that control's exact text to be durable before
// delivery; using one as a filler would test the delegate's refusal instead
// of the wake's consumed-marker/classification failure and suffix restore.
func q2ConsumerEnqueueFiller(t *testing.T, al *AgentLoop, sessionID, correlationID, text string) steeringQueueItem {
	t.Helper()
	item := steeringQueueItem{
		message:       providers.Message{Role: "user", Content: text},
		correlationID: correlationID,
	}
	if err := al.steering.pushItemScope(sessionID, item); err != nil {
		t.Fatalf("SETUP real plain queue filler %q: %v", correlationID, err)
	}
	return item
}
