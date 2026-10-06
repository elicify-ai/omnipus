package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

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
