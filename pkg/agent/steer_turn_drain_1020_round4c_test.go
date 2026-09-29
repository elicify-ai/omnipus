package agent

import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// A steer inside the durable commit window belongs to the finishing hand-off,
// not to the now-empty ordinary queue. It must be accepted even after the
// final queue-empty recheck, and its original message/receipt must survive.
func TestSteeredTurnDrain1020Round4c_CommitWindowAcceptsLateSteer(t *testing.T) {
	queue := newSteeringQueue(SteeringAll)
	const scope = "late-steer-child"
	late := steeringQueueItem{
		message:       providers.Message{Role: "user", Content: "late instruction"},
		correlationID: "late-steer-receipt",
	}
	var got []steeringQueueItem
	started, terminal, err := queue.runTerminalTransitionWithFinishing(scope,
		func() error { return nil },
		func() (bool, error) {
			finishing, enqueueErr := queue.pushItemScopeChecked(scope, late, nil)
			if enqueueErr != nil {
				t.Errorf("commit-window enqueue refused late steer: %v", enqueueErr)
			}
			if !finishing {
				t.Error("commit-window enqueue was not classified as post-finish")
			}
			return true, nil
		},
		func(items []steeringQueueItem) { got = items },
	)
	if err != nil || !started || !terminal {
		t.Fatalf("terminal transition = (started=%v, terminal=%v, err=%v), want (true, true, nil)", started, terminal, err)
	}
	if len(got) != 1 {
		t.Fatalf("finishing hand-off contains %d items, want exactly the accepted late steer", len(got))
	}
	if !reflect.DeepEqual(got[0].message, late.message) || got[0].correlationID != late.correlationID {
		t.Errorf("finishing hand-off = (%+v, %q), want (%+v, %q)", got[0].message, got[0].correlationID, late.message, late.correlationID)
	}
}
