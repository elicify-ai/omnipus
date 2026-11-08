package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Chat-goal fixtures need ordinary admission without changing the authority,
// launcher or retry behavior of the general-purpose task/Judge fixture.
// Preserve existing objects: fault/runtime fixtures may have installed their own.
func ensureOrdinaryGoalRuntimeForTest(t *testing.T, al *AgentLoop) {
	t.Helper()
	inbox, lifecycle := al.GetMessageInboxStore(), al.GetSessionLifecycleStore()
	if inbox == nil || lifecycle == nil {
		home := config.OmnipusHomeDir()
		if inbox == nil {
			inbox = session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
		}
		if lifecycle == nil {
			lifecycle = session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
		}
		al.SetSessionMessagingStores(inbox, lifecycle)
	}
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
}

// Keeper-task fixtures mint a genuine queued task session but never dispatch a
// run: a held run would suppress the keeper behavior they intend to exercise.
// The real task producer and reminder reader must use the same lifecycle store.
func prepareKeeperTaskRuntimeForTest(t *testing.T, al *AgentLoop) {
	t.Helper()
	ensureOrdinaryGoalRuntimeForTest(t, al)
	if al.taskExecutor == nil {
		t.Fatal("SETUP: keeper task requires the real task executor")
	}
	al.taskExecutor.SetLifecycleStore(al.GetSessionLifecycleStore())
}
