package agent

import (
	"context"
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

	// Case 1: bound session, NO durable record.
	noRec := &task.Task{ID: "t-s1-norec", AgentID: rtAssignee, WorkspaceID: testWS, Title: "t1",
		Status: task.StatusNext, SessionID: "sess-s1-norec"}
	require.NoError(t, store.Create(noRec))
	id, err := al.taskExecutor.StartTaskNow(context.Background(), noRec.ID)
	require.Error(t, err, "a bound session with no durable record must be refused")
	require.Empty(t, id)
	// Name the reason, as the deleted launcher-less test did: an error alone
	// would also be produced by Dispatch for this case, so the reason is what
	// ties the refusal to the gate under test.
	require.Contains(t, err.Error(), "never persisted",
		"the refusal must name the unpersisted classification, not some other failure")

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
	// Case 2 is the DISCRIMINATING case: a Failed task with a durable record is
	// dispatchable by Dispatch alone, so only the bound-session gate can refuse
	// it here. Case 1 is refused by Dispatch too, which is why the reason is
	// asserted above and the mutation probe lives on this case.
	id2, err2 := al.taskExecutor.StartTaskNow(context.Background(), failed.ID)
	require.Error(t, err2, "a Failed task's binding must be refused, not re-dispatched")
	require.Empty(t, id2)
	require.Contains(t, err2.Error(), "persist boom",
		"the refusal must name the recorded failure, so the caller can tell why it is not retryable")
	got, gerr := store.Get(failed.ID)
	require.NoError(t, gerr)
	require.Equal(t, task.StatusFailed, got.Status, "a refused start must leave the Failed task as it was")
}
