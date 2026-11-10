package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// #1557 / CHECK F-2: task cancel owns the cascade's task write. The queued
// session closer must not also write or emit an event for that cascade.
func TestU2bLauncherQueuedPersonStop_TaskCancel_WritesOneOutcomeAndEvent(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	goalBefore := readTaskGoal(t, taskID)
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)
	previous := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(previous) })
	engine := NewPlanEngine(p.loop, plan.New(filepath.Join(p.home, "plans")), p.loop.taskStore, p.loop.taskExecutor)

	stopped, err := engine.StopTask(context.Background(), taskID, "uat-person", "system")
	require.NoError(t, err, "task_cancel_single_writer: queued task cancel must succeed")
	require.NotNil(t, stopped)
	stored, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, stopped, stored, "task_cancel_durable_outcome: the returned task equals the persisted task")
	requireU2bQueuedPersonStopOutcome(t, stored)
	requireU2bQueuedPersonStopEvent(t, sub, taskID, sessionID)
	requireU2bQueuedPersonStopGoal(t, taskID, goalBefore)
	record, err := p.loop.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State, "instrument: task cancel reached the queued session")
}

// A Stop of an old queued session cannot write a task moved to a newer run.
func TestU2bLauncherQueuedPersonStop_TaskMovedToNewerSession_LeavesTaskUnchanged(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	newSession := "session-of-a-newer-run"
	before, err := p.loop.taskStore.Update(taskID, task.Patch{SessionID: &newSession})
	require.NoError(t, err)
	require.Equal(t, task.StatusInProgress, before.Status, "instrument: the newer run is still open")
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)

	requireU2bQueuedSessionStopped(t, p, sessionID)

	after, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "newer_session_outcome_untouched: a stale queued Stop must not end or rewrite the newer run's task")
	assert.Empty(t, drainTaskStatusEvents(sub), "newer_session_no_event: a stale Stop must not publish a task transition")
}

// An executor reservation is a run-ownership input, not a fake terminal writer.
// No goroutine is launched here: the real Stop must leave that owner's task to
// its own settlement rather than racing a second task outcome into the store.
func TestU2bLauncherQueuedPersonStop_ExecutorOwnsRun_LeavesTaskUnchanged(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	before, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	te := p.loop.taskExecutor
	te.mu.Lock()
	te.running[taskID] = &taskSlot{reserved: true}
	te.mu.Unlock()
	t.Cleanup(func() {
		te.mu.Lock()
		delete(te.running, taskID)
		te.mu.Unlock()
	})
	require.True(t, te.holdsRun(taskID), "instrument: the executor owns a reserved run")
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)

	requireU2bQueuedSessionStopped(t, p, sessionID)

	after, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "executor_owned_outcome_untouched: Stop must not write a task whose run the executor owns")
	assert.Empty(t, drainTaskStatusEvents(sub), "executor_owned_no_event: only the run's owner may publish its task outcome")
}

// Another writer's already-landed terminal outcome wins over a stale Stop.
func TestU2bLauncherQueuedPersonStop_TaskAlreadyEnded_LeavesOtherWritersOutcome(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	failed, result := task.StatusFailed, "ended by another writer first"
	before, err := p.loop.taskStore.Update(taskID, task.Patch{Status: &failed, Result: &result})
	require.NoError(t, err)
	require.Equal(t, result, before.Result, "instrument: the other writer's outcome landed before Stop")
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)

	requireU2bQueuedSessionStopped(t, p, sessionID)

	after, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "other_writer_outcome_untouched: Stop must preserve the entire prior outcome, including its reason and timestamps")
	assert.Empty(t, drainTaskStatusEvents(sub), "other_writer_no_event: a stale Stop must not republish the prior terminal task")
}

// #1557 / CHECK F-1: repeated session Stop is idempotent, not a second write.
// These are two direct session-scope API requests, not the UI's Stop-all gesture.
func TestU2bLauncherQueuedPersonStop_StopTwice_KeepsOneOutcomeAndEvent(t *testing.T) {
	p, taskID, sessionID := newU2bQueuedStopFixture(t)
	goalBefore := readTaskGoal(t, taskID)
	sub := p.loop.SubscribeEvents(64)
	defer p.loop.UnsubscribeEvents(sub.ID)
	requireU2bQueuedSessionStopped(t, p, sessionID)
	first, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)

	second, err := p.loop.StopSession(context.Background(), u2bQueuedStopHumanRequest(sessionID))
	require.NoError(t, err, "repeat_stop_no_error: a repeated session Stop succeeds")
	require.NoError(t, second.RootErr, "repeat_stop_no_task_error: a repeated session Stop must not repeat the task write")
	after, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, first, after, "repeat_stop_outcome_unchanged: repeated Stop must not rewrite the stored outcome or completion time")
	requireU2bQueuedPersonStopOutcome(t, after)
	requireU2bQueuedPersonStopEvent(t, sub, taskID, sessionID)
	requireU2bQueuedPersonStopGoal(t, taskID, goalBefore)
}

// Optional H1 pins the non-task-origin guard directly, rather than relying on
// unrelated stop tests to notice a spurious empty-task-ID error.
func TestU2bLauncherQueuedPersonStop_QueuedNonTaskSession_ReportsNoTaskError(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	childID, _, _ := queuedExternalRevival(t, al)
	before, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.NotNil(t, before.Origin, "instrument: the queued session has a non-task origin")
	require.NotEqual(t, session.OriginKindTask, before.Origin.Kind)
	require.Empty(t, before.Origin.TaskID)
	require.NotNil(t, al.taskStore, "instrument: a broken task-kind guard could reach the real store")
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: childID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"},
	})
	require.NoError(t, err)
	assert.NoError(t, result.RootErr, "non_task_stop_no_task_error: a queued non-task Stop must not try to read an empty task ID")
	after, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, after.State, "instrument: the non-task session really stopped")
	assert.Empty(t, drainTaskStatusEvents(sub), "non_task_stop_no_task_event: no task outcome belongs to a non-task Stop")
}
