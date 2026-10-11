package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// S1 (UAT follow-up) through the one real start path: StartTaskNow on a task
// that is already bound to a session must never return (sessionID, nil) for a
// binding that is not retryable -- no durable record, or a task already Failed.
func TestStartTaskNow_BoundBinding_UnretryableIsRefused_RealLauncher(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, nil)
	store := GetTaskStore(al)
	lifecycle := al.GetSessionLifecycleStore()
	require.NotNil(t, lifecycle)
	_ = filepath.Join // keep import set stable

	// Case 1: bound session, NO durable record.
	noRec := &task.Task{ID: "t-s1-norec", AgentID: rtAssignee, WorkspaceID: testWS, Title: "t1",
		Status: task.StatusNext, SessionID: "sess-s1-norec"}
	require.NoError(t, store.Create(noRec))
	id, err := al.taskExecutor.StartTaskNow(context.Background(), noRec.ID)
	require.Error(t, err, "a bound session with no durable record must be refused")
	require.Empty(t, id)

	// Case 2: durable record exists (task origin, running) but the task already
	// Failed before it could start.
	require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
		SessionID: "sess-s1-failed", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testWS, AgentID: rtAssignee,
		Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "t-s1-failed"},
	}))
	failed := &task.Task{ID: "t-s1-failed", AgentID: rtAssignee, WorkspaceID: testWS, Title: "t2",
		Status: task.StatusFailed, Result: "persist boom", SessionID: "sess-s1-failed"}
	require.NoError(t, store.Create(failed))
	id2, err2 := al.taskExecutor.StartTaskNow(context.Background(), failed.ID)
	require.Error(t, err2, "a Failed task's binding must be refused, not re-dispatched")
	require.Empty(t, id2)
	got, gerr := store.Get(failed.ID)
	require.NoError(t, gerr)
	require.Equal(t, task.StatusFailed, got.Status, "a refused start must leave the Failed task as it was")
}
