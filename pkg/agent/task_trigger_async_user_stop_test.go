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

// Chief-directed positive guard controls: an intentional person's Stop is
// never a gateway interruption. Each real user-stop seam must leave the one
// stopped run unchanged at the next physical boot, not authorize a replay.
// These controls PASS before the U2b fix and must stay green afterwards.
func TestU2bAsyncOnce_UserStopButton_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bAsyncProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireWhileProviderHeld(t, worker)

	// The runtime seam called by gateway/websocket_stop_scope.go's Stop
	// button adapter. Browser/WebSocket transport hooks are outside this test.
	result, err := p.loop.StopSession(context.Background(), StopRequest{
		SessionID: fire.run.SessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "uat-person"},
		Channel:   "web",
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.Empty(t, result.Report.Unreachable)
	require.True(t, result.Fired, "instrument: the person's Stop reached the actual running task")
	requireU2bAsyncUserStopNotReplayed(t, p, fire)
}

func TestU2bAsyncOnce_UserStopCommand_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bAsyncProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireWhileProviderHeld(t, worker)
	agent, ok := p.loop.GetRegistry().GetAgent("native-agent")
	require.True(t, ok)

	// Real command parsing/registry/handler/runtime, not a fake stop callback.
	// CLI provides the server-side /stop command seam; the web client also
	// intercepts /stop, but that transport is not claimed exercised here.
	reply, handled := p.loop.handleCommand(context.Background(), bus.InboundMessage{
		Channel:   "cli",
		ChatID:    fire.run.SessionID,
		SessionID: fire.run.SessionID,
		Sender:    bus.SenderInfo{CanonicalID: "uat-person"},
		Content:   "/stop",
	}, agent, &processOptions{
		SessionKey:          taskTurnSessionKey("native-agent", fire.taskID),
		TranscriptSessionID: fire.run.SessionID,
	})
	require.True(t, handled, "instrument: /stop executes as a command, not another model turn")
	require.NotEmpty(t, reply, "instrument: the command acknowledges the request")
	requireU2bAsyncUserStopNotReplayed(t, p, fire)
}

func TestU2bAsyncOnce_UserTaskCancel_IsNotReplayed(t *testing.T) {
	worker := newU2bAsyncWorker()
	p := newU2bAsyncProcess(t, t.TempDir(), newTriggerFakeClock(), worker)
	fire := p.fireWhileProviderHeld(t, worker)

	previousRegistry := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(previousRegistry) })
	// The production constructor supplies the real loop as session canceller;
	// no fake dispatcher/canceller and no direct terminal-state patch. No
	// background plan engine is started for this standalone task cancellation.
	engine := NewPlanEngine(p.loop, plan.New(filepath.Join(p.home, "plans")), p.loop.taskStore, p.loop.taskExecutor)
	updated, err := engine.StopTask(context.Background(), fire.taskID, "uat-person", "system")
	require.NoError(t, err)
	require.Equal(t, task.CancelReasonStoppedByUser, updated.CancelReason,
		"instrument: this is the person's explicit task-cancel path")
	requireU2bAsyncUserStopNotReplayed(t, p, fire)
}

func requireU2bAsyncUserStopNotReplayed(t *testing.T, p *u2bAsyncProcess, fire u2bAsyncFire) {
	t.Helper()
	u2bAsyncJoin(t, "the intentionally stopped task's actual execution", p.loop.taskExecutor.wg.Wait)
	before := p.runs(t, fire.taskID)
	require.Len(t, before, 1, "a person's Stop ends the original execution without another attempt")
	require.Equal(t, fire.run.RunID, before[0].RunID)
	require.Equal(t, task.StatusFailed, before[0].Status)
	require.NotNil(t, before[0].EndedAt)
	require.NotEmpty(t, before[0].Result)
	stopped, err := p.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, stopped.Status)
	require.NotEqual(t, InterruptedTaskResult, stopped.Result, "a person's Stop is not an interrupted-gateway marker")
	record, err := p.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, record.State, "the person's accepted Stop actually landed")
	require.NotNil(t, record.StopNote)
	t.Logf("user stop settled: one closed run; task result=%q cancel_reason=%q; lifecycle=%q",
		stopped.Result, stopped.CancelReason, record.State)

	p.stop()
	afterBoot := bootU2bAsyncProcess(t, p.home, p.clock)
	require.Equal(t, before, afterBoot.runs(t, fire.taskID),
		"a person deliberately stopped this once run: physical boot must neither replay nor rewrite it")
	after, err := afterBoot.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	require.Equal(t, stopped.Status, after.Status)
	require.Equal(t, stopped.Result, after.Result, "the person's stopped outcome must remain stopped")
	require.Equal(t, stopped.CancelReason, after.CancelReason)
	recordAfter, err := afterBoot.loop.GetSessionLifecycleStore().Load(fire.run.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, recordAfter.State)
}
