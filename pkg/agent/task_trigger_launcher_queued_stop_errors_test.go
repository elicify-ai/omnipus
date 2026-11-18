package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1557 / CHECK F-3: a task-store read failure must reach RootErr even though
// the session Stop has landed. Invalid JSON exercises the real durable reader
// without depending on user ID, file modes, or a mocked task store.
func TestU2bLauncherQueuedPersonStop_TaskUnreadable_ReportsReadFailureAndStopsSession(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	file := filepath.Join(p.loop.taskStore.Dir(), taskID+".json")
	original, err := os.ReadFile(file)
	require.NoError(t, err, "instrument: the real task was readable before the fault")
	broken := []byte("{not json")
	require.NoError(t, os.WriteFile(file, broken, 0o600))
	t.Cleanup(func() { assert.NoError(t, os.WriteFile(file, original, 0o600)) })
	_, readErr := p.loop.taskStore.Get(taskID)
	var parseErr *json.SyntaxError
	require.ErrorAs(t, readErr, &parseErr, "instrument: the real task store rejects the corrupted JSON")
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)

	result, err := p.loop.StopSession(context.Background(), u2bQueuedStopHumanRequest(sessionID))
	require.NoError(t, err, "the session itself still accepts Stop")
	require.Error(t, result.RootErr, "task_read_error_reported: failure to save the task's Stop must not be swallowed")
	assert.ErrorContains(t, result.RootErr,
		fmt.Sprintf("Stop ended session %s, but task %s could not be read to save it as stopped:", sessionID, taskID),
		"task_read_error_context: report both the stopped session and unreadable task")
	assert.ErrorAs(t, result.RootErr, &parseErr, "task_read_error_cause: retain the underlying JSON parse error")
	record, err := p.loop.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State, "task_read_failure_session_stopped: the storage error must not undo the landed session Stop")
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, broken, after, "task_read_failure_no_write: Stop must not overwrite a task it cannot read")
	assert.Empty(t, drainTaskStatusEvents(sub), "task_read_failure_no_event: do not publish a task outcome that was not saved")
}
