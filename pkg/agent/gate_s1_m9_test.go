package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// S1 final-gate M9: there is ONE task-start path, through the session launcher
// the gateway installs at boot. A task executor with no launcher refuses to start
// a task instead of running a second, launcher-less start that production never
// takes (tests of such a path were a false green).
func TestStartTaskNow_WithoutALauncherRefusesInsteadOfTakingASecondPath(t *testing.T) {
	te, store := newStartTaskNowExecutor(t)
	tk := createInProgressTask(t, store, "some-agent", "ws-1")

	sessID, err := te.StartTaskNow(context.Background(), tk.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no session launcher is installed")
	require.Empty(t, sessID, "nothing may be started")
	got, gerr := store.Get(tk.ID)
	require.NoError(t, gerr)
	require.Empty(t, got.SessionID, "no session may be bound to the task")
	require.Equal(t, task.StatusInProgress, got.Status)
}
