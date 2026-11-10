package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestAgentLoopClose_RefusesSessionWorkerDispatchedDuringClose (review T1):
// a dispatch that lands DURING Close - after the spawn fence is set, before
// the worker-drain snapshot - must be refused, not only one that arrives
// after Close returned.
func TestAgentLoopClose_RefusesSessionWorkerDispatchedDuringClose(t *testing.T) {
	al, _, _, _, closeLoop := newTestAgentLoop(t) //nolint:dogsled // only the loop and its Close are needed

	const scope = "close-during-fence-scope"
	var (
		hookRan    bool
		dispatched bool
		spawned    bool
	)
	afterWorkerSpawnFenceTestHook = func(hooked *AgentLoop) {
		if hooked != al {
			return
		}
		hookRan = true
		dispatched = al.dispatchSessionWorker(scope, bus.InboundMessage{
			Channel: "webchat", ChatID: "during-close", SessionID: "during-close-session", Content: "arrives mid-Close",
		})
		_, spawned = al.sessionWorkers.Load(scope)
	}
	t.Cleanup(func() { afterWorkerSpawnFenceTestHook = nil })

	closeLoop()

	if !hookRan {
		t.Fatal("INSTRUMENT: Close never reached the point between the spawn fence and the drain snapshot")
	}
	if dispatched || spawned {
		t.Fatalf("a dispatch during Close spawned a session worker (dispatched=%v, stored=%v); Close must refuse it once the fence is set",
			dispatched, spawned)
	}
}
