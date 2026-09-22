package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestPrearm_QueuedDescendantImminentUnderStoppedNode(t *testing.T) {
	lifecycle := session.NewLifecycleStore(t.TempDir())
	persistSteerLifecycle(t, lifecycle, testSteerLifecycleRecord("root", "", session.LifecycleRunning, 1))
	persistSteerLifecycle(t, lifecycle, testSteerLifecycleRecord("child", "root", session.LifecycleRunning, 1))

	al := &AgentLoop{
		cancelPreArm:                  newCancelPreArm(),
		sessionLifecycleStoreForTools: lifecycle,
	}
	worker := newSessionWorker("agent:agent-1:session:child", al, func() {})
	defer worker.cancel()
	worker.inbox <- bus.InboundMessage{SessionID: "child", Content: "wake"}
	al.sessionWorkers.Store(worker.scope, worker)
	defer al.sessionWorkers.Delete(worker.scope)

	al.cancelPreArm.mu.Lock()
	imminent := al.turnImminentForIdentity("root", CancelScope{SessionID: "root"})
	al.cancelPreArm.mu.Unlock()
	if !imminent {
		t.Fatal("queued wake for a durable descendant was not imminent under the stopped root")
	}
}
