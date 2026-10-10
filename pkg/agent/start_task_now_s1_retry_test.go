package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: S1 (security re-verification) — two PUBLIC StartTaskNow calls on a
// task whose assigned agent's executor kind cannot be resolved (reserved
// remote-a2a) both refuse, start ZERO runs, and the retry never reports a
// bound session id as success. Previously a failed pre-dispatch classification
// could be followed by a "successful" retry on an unclassified binding.
// Real: TaskExecutor.StartTaskNow -> mintTaskLifecycleRecord ->
// resolveTaskRuntime -> failTaskBeforeDispatch; the instrument is the
// goroutine hook (fires when a run goroutine would start), the running map and
// the task's persisted binding/status. The refusal is the one scenario the
// helper-level TestU5b_S1_BoundSessionRetryGate cannot reach.
func TestS1_StartTaskNowRetryAfterClassificationFailureStartsNothing(t *testing.T) {
	te, store, al, agentID := newStartTaskNowWithRegistry(t)
	te.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	ag, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok)
	ag.Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A}}

	var goroutineStarts atomic.Int32
	te.goroutineCtxHook = func(context.Context, string) { goroutineStarts.Add(1) }
	tk := createInProgressTask(t, store, agentID, "ws-1")

	for call := 1; call <= 2; call++ {
		sessionID, err := te.StartTaskNow(context.Background(), tk.ID)
		require.Errorf(t, err, "call %d must refuse, got session %q", call, sessionID)
		require.Empty(t, sessionID, "call %d must not report a session as started", call)
	}

	te.wg.Wait()
	require.Zero(t, goroutineStarts.Load(), "zero runs: no task goroutine may start on an unclassifiable runtime")
	te.mu.Lock()
	require.Empty(t, te.running, "no run slot may remain reserved")
	te.mu.Unlock()
	got, err := store.Get(tk.ID)
	require.NoError(t, err)
	require.Empty(t, got.SessionID, "no session may be bound to a task whose classification failed")
	require.Equal(t, task.StatusFailed, got.Status, "the refusal is recorded truthfully on the task")
}
