package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Addendum 5 item 3: the three intentional person-stop entry points must never
// become replayable gateway interruptions, including on the MAIN child path.
func TestU2bLauncherOnce_UserStopButton_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bLauncherProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireStarted(t, worker)
	result, err := p.loop.StopSession(context.Background(), StopRequest{
		SessionID: fire.run.SessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-person"},
		Channel:   "web",
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.Empty(t, result.Report.Unreachable)
	require.True(t, result.Fired, "instrument: Stop reached the real running MAIN task")
	requireU2bLauncherUserStopNotReplayed(t, p, fire)
}

func TestU2bLauncherOnce_UserStopCommand_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bLauncherProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireStarted(t, worker)
	agent, ok := p.loop.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)
	reply, handled := p.loop.handleCommand(context.Background(), bus.InboundMessage{
		Channel: "cli", ChatID: fire.run.SessionID, SessionID: fire.run.SessionID,
		Sender: bus.SenderInfo{CanonicalID: "uat-person"}, Content: "/stop",
	}, agent, &processOptions{
		SessionKey: taskTurnSessionKey("native-agent", fire.taskID), TranscriptSessionID: fire.run.SessionID,
	})
	require.True(t, handled, "instrument: real command parsing/registry/runtime, not a provider message")
	require.NotEmpty(t, reply)
	requireU2bLauncherUserStopNotReplayed(t, p, fire)
}

func TestU2bLauncherOnce_UserTaskCancel_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bLauncherProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireStarted(t, worker)
	previousRegistry := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(previousRegistry) })
	engine := NewPlanEngine(p.loop, plan.New(filepath.Join(p.home, "plans")), p.loop.taskStore, p.loop.taskExecutor)
	updated, err := engine.StopTask(context.Background(), fire.taskID, "uat-person", "system")
	require.NoError(t, err)
	require.Equal(t, task.CancelReasonStoppedByUser, updated.CancelReason)
	requireU2bLauncherUserStopNotReplayed(t, p, fire)
}

func requireU2bLauncherUserStopNotReplayed(t *testing.T, p *u2bLauncherProcess, fire u2bAsyncFire) {
	t.Helper()
	u2bAsyncJoin(t, "person-stopped MAIN execution and settlement", p.loop.taskExecutor.wg.Wait)
	before := p.runs(t, fire.taskID)
	require.Len(t, before, 1)
	require.Equal(t, fire.run.RunID, before[0].RunID)
	require.Equal(t, fire.run.SessionID, before[0].SessionID)
	require.Equal(t, task.StatusFailed, before[0].Status)
	require.NotNil(t, before[0].EndedAt)
	require.NotEmpty(t, before[0].Result)
	stopped, err := p.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, stopped.Status)
	require.NotEqual(t, InterruptedTaskResult, stopped.Result, "intentional Stop is not a replayable gateway interruption")
	record, err := p.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, record.State)
	require.NotNil(t, record.StopNote)
	require.Empty(t, p.storedJobs(t, fire.taskID), "person Stop settles, rather than retaining, the once fire")
	t.Logf("MAIN person Stop: one closed failed run; task result=%q cancel_reason=%q; lifecycle=%q",
		stopped.Result, stopped.CancelReason, record.State)
	p.stop()

	afterBoot := bootU2bLauncherProcess(t, p.home, p.clock)
	require.Equal(t, before, afterBoot.runs(t, fire.taskID), "intentional Stop must never replay or rewrite the stopped run")
	after, err := afterBoot.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Equal(t, stopped.Status, after.Status)
	require.Equal(t, stopped.Result, after.Result)
	require.Equal(t, stopped.CancelReason, after.CancelReason)
	recordAfter, err := afterBoot.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, recordAfter.State)
	afterBoot.stop()
	thirdBoot := bootU2bLauncherProcess(t, p.home, p.clock)
	require.Equal(t, before, thirdBoot.runs(t, fire.taskID), "even repeated physical boots cannot replay a person-stopped MAIN run")
}
