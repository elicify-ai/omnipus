package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestAgentLoopClose_SpawnDuringTheDrainIsRefused is Train 2 gate r2 pr-test
// C1: shutdown is held right after stopSessionWorkers took its snapshot of
// the workers it will drain. A continuation dispatched to a fresh scope at
// that moment must be refused — a worker created now is outside the snapshot,
// is never drained, and would write after Close returns. This holds only if
// the spawn fence is set BEFORE the snapshot; the serial after-Close test
// cannot tell that ordering apart.
func TestAgentLoopClose_SpawnDuringTheDrainIsRefused(t *testing.T) {
	al, _, _, _, closeLoop := newTestAgentLoop(t) //nolint:dogsled // only al and its Close are used
	snapshotted := make(chan struct{})
	release := make(chan struct{})
	al.workerSnapshotHook = func() {
		close(snapshotted)
		<-release
	}
	closed := make(chan struct{})
	go func() {
		closeLoop()
		close(closed)
	}()
	select {
	case <-snapshotted:
	case <-time.After(30 * time.Second):
		t.Fatal("Close never reached its worker snapshot")
	}

	const scope = "gate-r2-scope-during-drain"
	dispatched := al.dispatchSessionWorker(scope, bus.InboundMessage{
		Channel: "web", ChatID: "late-chat", SessionID: "late-session", Content: "a continuation arriving mid-shutdown",
	})
	_, spawned := al.sessionWorkers.Load(scope)
	close(release)
	select {
	case <-closed:
	case <-time.After(60 * time.Second):
		t.Fatal("Close did not finish after the drain was released")
	}
	if v, ok := al.sessionWorkers.Load(scope); ok {
		if w, isWorker := v.(*sessionWorker); isWorker {
			w.cancel()
			select {
			case <-w.done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	if spawned {
		t.Fatal("a dispatch between the drain snapshot and the end of Close spawned a worker that nothing drains")
	}
	if dispatched {
		t.Fatal("a dispatch during shutdown must report that it was not delivered")
	}
}
