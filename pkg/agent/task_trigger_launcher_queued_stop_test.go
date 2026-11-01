package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Addendum 6 / CHECK C1: a person's Stop is never a replayable gateway
// interruption, including before a MAIN launch enters its first task turn.
// This does not choose #1553's outcome for a queued launch nobody stopped.
func TestU2bLauncherQueuedPersonStop_TaskCancel_IsNeverReplayed(t *testing.T) {
	runU2bQueuedPersonStopGuard(t, func(p *u2bLauncherProcess, taskID, _ string) {
		previous := currentVerifierSessionRegistry()
		t.Cleanup(func() { SetVerifierSessionRegistry(previous) })
		engine := NewPlanEngine(p.loop, plan.New(filepath.Join(p.home, "plans")), p.loop.taskStore, p.loop.taskExecutor)
		stopped, err := engine.StopTask(context.Background(), taskID, "uat-person", "system")
		require.NoError(t, err, "the real task-cancel API must accept this queued in-progress task")
		require.NotNil(t, stopped)
		// StopTask's US-7 contract explicitly promises failed + cancel marker.
		assert.Equal(t, task.StatusFailed, stopped.Status)
		assert.Equal(t, task.CancelReasonStoppedByUser, stopped.CancelReason)
	})
}

func TestU2bLauncherQueuedPersonStop_StopButton_IsNeverReplayed(t *testing.T) {
	runU2bQueuedPersonStopGuard(t, func(p *u2bLauncherProcess, _, sessionID string) {
		result, err := p.loop.StopSession(context.Background(), StopRequest{
			SessionID: sessionID,
			By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-person"},
			Channel:   "web",
		})
		require.NoError(t, err)
		assert.NoError(t, result.RootErr)
		assert.Empty(t, result.Report.Unreachable)
		assert.Equal(t, []string{sessionID}, result.Report.Reached, "the queued target is actually reached, not merely accepted")
		assert.True(t, result.Durable)
		assert.True(t, result.Fired, "StopSession explicitly supports a queued-only Stop without a live turn")
	})
}

func TestU2bLauncherQueuedPersonStop_StopCommand_IsNeverReplayed(t *testing.T) {
	runU2bQueuedPersonStopGuard(t, func(p *u2bLauncherProcess, taskID, sessionID string) {
		agent, ok := p.loop.GetRegistry().GetAgent("native-agent")
		require.True(t, ok)
		reply, handled := p.loop.handleCommand(context.Background(), bus.InboundMessage{
			Channel: "cli", ChatID: sessionID, SessionID: sessionID,
			Sender: bus.SenderInfo{CanonicalID: "uat-person"}, Content: "/stop",
		}, agent, &processOptions{
			SessionKey: taskTurnSessionKey("native-agent", taskID), TranscriptSessionID: sessionID,
		})
		require.True(t, handled, "the real command parser, registry and runtime must handle /stop")
		t.Logf("real /stop reply for the never-started queued child: %q", reply)
	})
}

func runU2bQueuedPersonStopGuard(t *testing.T, stop func(*u2bLauncherProcess, string, string)) {
	t.Helper()
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p := newU2bLauncherProcess(t, home, clk, worker)
	taskID, sessionID := p.fireQueuedPersonStop(t, worker)
	beforeRuns := p.runs(t, taskID)
	require.Len(t, beforeRuns, 1, "instrument: the delivered queued launch has one real run identity")
	beforeSessions := u2bQueuedStopSessionIDs(t, p)
	require.Contains(t, beforeSessions, sessionID, "instrument: the fresh durable reader sees the actual queued child")
	require.Equal(t, []string{sessionID}, u2bQueuedStopTaskSessionIDs(t, p, taskID))

	stop(p, taskID, sessionID)
	u2bAsyncJoin(t, "queued person Stop and any executor settlement tail", p.loop.taskExecutor.wg.Wait)
	stopped, err := p.loop.taskStore.Get(taskID)
	require.NoError(t, err)
	assert.NotEqual(t, InterruptedTaskResult, stopped.Result, "a person's Stop is not a gateway interruption")
	record, err := p.loop.GetSessionLifecycleStore().Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State, "the real Stop must land on the queued child")
	assert.NotNil(t, record.StopNote, "instrument: the person's durable Stop, not a fabricated task status")
	assert.Empty(t, p.storedJobs(t, taskID), "a person's Stop must leave no retained once fire")
	assert.Equal(t, beforeSessions, u2bQueuedStopSessionIDs(t, p), "Stop itself must not launch a new execution session")
	u2bRequireProviderNotEntered(t, worker)
	t.Logf("queued person Stop before crash: task=%q result=%q cancel_reason=%q lifecycle=%q; runs=%+v; jobs=%d",
		stopped.Status, stopped.Result, stopped.CancelReason, record.State, p.runs(t, taskID), len(p.storedJobs(t, taskID)))

	// Crash image comes AFTER the real person's Stop. Cleanup cannot supply
	// the status, result, StopNote or Running bit consumed by either boot.
	image := u2bAsyncSnapshot(t, home)
	p.stop()
	u2bAsyncRestore(t, home, image)
	for boot := 1; boot <= 2; boot++ { // Addendum 6 explicitly requires two fresh boots.
		bootWorker := newU2bAsyncWorker()
		bootWorker.complete() // A wrong replay must execute visibly, never hide behind a held provider.
		next := newU2bLauncherProcess(t, home, clk, bootWorker)
		next.physicalBoot(t)
		next.runDueAndJoin(t)
		t.Logf("checking queued person Stop after physical boot %d", boot)
		requireU2bQueuedStopSurvives(t, next, bootWorker, stopped, record, beforeRuns, beforeSessions)
		clk.Advance(time.Minute)
		next.runDueAndJoin(t)
		requireU2bQueuedStopSurvives(t, next, bootWorker, stopped, record, beforeRuns, beforeSessions)
		next.stop()
	}
}

func requireU2bQueuedStopSurvives(
	t *testing.T, p *u2bLauncherProcess, worker *u2bAsyncWorker, stopped *task.Task,
	stoppedSession *session.LifecycleRecord, originalRuns []task.TaskRun, originalSessions []string,
) {
	t.Helper()
	after, err := p.loop.taskStore.Get(stopped.ID)
	require.NoError(t, err)
	assert.Equal(t, stopped.Status, after.Status, "the task's person-stop status must survive crash, boot and a later due tick")
	assert.Equal(t, stopped.Result, after.Result, "boot must preserve the person's task outcome, not rewrite it as gateway interruption")
	assert.Equal(t, stopped.CancelReason, after.CancelReason, "the person's cancel reason must survive both boots")
	assert.NotEqual(t, InterruptedTaskResult, after.Result, "only a gateway interruption may make this task replayable; a person's Stop never may")
	runs := p.runs(t, stopped.ID)
	if assert.Len(t, runs, 1, "a person-stopped queued launch must never gain a replay run") {
		assert.Equal(t, originalRuns[0].RunID, runs[0].RunID)
		assert.Equal(t, originalRuns[0].SessionID, runs[0].SessionID)
	}
	// No #1553 status/closed-time oracle is imposed on the original queued
	// TaskRun: only the person's task outcome and no new run/session matter.
	assert.Equal(t, originalSessions, u2bQueuedStopSessionIDs(t, p), "neither boot nor due tick may create a new task-execution session")
	assert.Equal(t, []string{stoppedSession.SessionID}, u2bQueuedStopTaskSessionIDs(t, p, stopped.ID))
	record, err := p.loop.GetSessionLifecycleStore().Load(stoppedSession.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, record.State)
	assert.Equal(t, stoppedSession.StopNote, record.StopNote, "the person's durable Stop evidence must remain intact")
	assert.Empty(t, p.storedJobs(t, stopped.ID), "no retained once job may survive a person's Stop")
	u2bRequireProviderNotEntered(t, worker)
}
