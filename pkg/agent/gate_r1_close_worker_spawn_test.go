package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestAgentLoopClose_RefusesNewSessionWorkers forces the timing behind the
// Train 2 PR CI failure of TestAttentionSecretCard_SubmitClearsTheCue under
// -race ("TempDir RemoveAll cleanup: directory not empty"). A secret-card
// submit queues the resume; when Run dispatched it after Close had
// snapshotted and drained the session workers, a fresh worker ran the turn
// against the torn-down registry and wrote the deferred resume to disk after
// Close returned. Close's contract is that nothing writes after it returns,
// so a dispatch that arrives once Close has begun must not spawn a worker.
func TestAgentLoopClose_RefusesNewSessionWorkers(t *testing.T) {
	al, _, _, _, closeLoop := newTestAgentLoop(t) //nolint:dogsled // only the loop and its Close are needed
	closeLoop()

	const scope = "gate-r1-scope-after-close"
	dispatched := al.dispatchSessionWorker(scope, bus.InboundMessage{
		Channel: "web", ChatID: "late-chat", SessionID: "late-session", Content: "arrives after Close",
	})
	if v, ok := al.sessionWorkers.Load(scope); ok {
		if w, isWorker := v.(*sessionWorker); isWorker {
			w.cancel()
			select {
			case <-w.done:
			case <-time.After(5 * time.Second):
			}
		}
		t.Fatal("a dispatch after Close spawned a session worker that nothing drains")
	}
	if dispatched {
		t.Fatal("a dispatch after Close must report that it was not delivered")
	}
}
