//go:build linux || darwin

package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// #1557 / CHECK F-3: test actual POSIX directory write permissions. A root
// runner bypasses them, so the repository's permission-instrument guard applies;
// evidence for this row must be a real PASS on a non-root runner, not a SKIP.
func TestU2bLauncherQueuedPersonStop_TaskUnwritable_ReportsWriteFailureAndLeavesTaskOpen(t *testing.T) {
	skipIfRoot(t)
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	before, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, before.Status, "instrument: the task is open before the write fault")
	dir := p.loop.taskStore.Dir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { assert.NoError(t, os.Chmod(dir, 0o700)) })
	probe, createErr := os.CreateTemp(dir, "queued-stop-write-probe-*")
	if probe != nil {
		require.NoError(t, probe.Close())
		require.NoError(t, os.Remove(probe.Name()))
	}
	require.ErrorIs(t, createErr, fs.ErrPermission, "instrument: this directory really rejects writes for the test user")
	readable, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err, "instrument: reads still work; this must exercise the write error, not the read error")
	require.Equal(t, before, readable)
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)

	result, err := p.loop.StopSession(context.Background(), u2bQueuedStopHumanRequest(sessionID))
	require.NoError(t, err, "the session itself still accepts Stop")
	require.Error(t, result.RootErr, "task_write_error_reported: failure to save the task's Stop must not be swallowed")
	assert.ErrorContains(t, result.RootErr,
		fmt.Sprintf("Stop ended session %s, but task %s could not be saved as stopped:", sessionID, taskID),
		"task_write_error_context: identify the failed task save after the session Stop")
	assert.ErrorIs(t, result.RootErr, fs.ErrPermission, "task_write_error_cause: retain the underlying permission error")
	var pathErr *os.PathError
	require.ErrorAs(t, result.RootErr, &pathErr, "task_write_error_type: retain the failing filesystem operation")
	assert.Equal(t, dir, filepath.Dir(pathErr.Path), "task_write_error_path: the failure belongs to the task store")
	after, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "task_write_failure_outcome_unchanged: the unsaved task stays open with its original outcome")
	record, err := p.loop.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State, "task_write_failure_session_stopped: the task error must not undo the landed session Stop")
	assert.Empty(t, drainTaskStatusEvents(sub), "task_write_failure_no_event: do not publish an unsaved task outcome")
}
