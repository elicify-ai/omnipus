package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracles: addendum-u2b-launcher.md items 1-2, binding graceful addendum 3,
// and handoff-2b.txt F1/F4. On fd7a55979, already-green cases are regression
// controls, never represented as pre-fix RED proof.
func TestU2bLauncherOnce_CrashAfterDispatchReturns_ReplaysOnceThenNoMore(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	jobsAtDeath := p1.storedJobs(t, fire.taskID)
	t.Logf("launcher crash boundary: dispatch returned; actual task run open; persisted jobs=%d", len(jobsAtDeath))
	image := u2bAsyncSnapshot(t, home)
	p1.stop() // Join old writers before restoring the unchanged live crash image.
	u2bAsyncRestore(t, home, image)

	p2 := bootU2bLauncherProcess(t, home, clk)
	runs := p2.runs(t, fire.taskID)
	original, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result)
	p2.requireMainChild(t, fire.taskID, replay.SessionID)
	require.Len(t, jobsAtDeath, 1, "the delivered once job outlives actual MAIN dispatch")
	require.Equal(t, fire.jobID, jobsAtDeath[0].ID)
	require.True(t, jobsAtDeath[0].Enabled)
	require.True(t, jobsAtDeath[0].State.Running)
	require.Empty(t, p2.storedJobs(t, fire.taskID), "the completed real replay settles its retained job")
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "another tick adds no replay")
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "another physical boot preserves the entire two-run history")
}

func TestU2bLauncherOnce_GracefulStop_PreservesOneReplay(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	p1.gracefulProcessExit(t, fire.taskID)

	stopped, err := p1.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	before := p1.runs(t, fire.taskID)
	require.Len(t, before, 1, "process-exit image retains the original actual MAIN run")
	t.Logf("launcher orderly stop: task=%q result=%q; run=%q result=%q open=%t; jobs=%d",
		stopped.Status, stopped.Result, before[0].Status, before[0].Result,
		before[0].IsOpen(), len(p1.storedJobs(t, fire.taskID)))

	p2 := bootU2bLauncherProcess(t, home, clk)
	runs := p2.runs(t, fire.taskID)
	_, replay := requireU2bAsyncReplay(t, fire, runs)
	p2.requireMainChild(t, fire.taskID, replay.SessionID)
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID))
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "an orderly restart owes one replay, never another after completion")
}

// Successful real execution is the positive instrument for the provider/Judge,
// MAIN run-history reader and subsequent no-replay boot checks.
func TestU2bLauncherOnce_CompletedBeforeRestart_IsNotReplayed(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	fire := p1.fireStarted(t, worker)
	worker.complete()
	u2bAsyncJoin(t, "completed MAIN execution and its settlement tail", p1.loop.taskExecutor.wg.Wait)
	before := p1.runs(t, fire.taskID)
	require.Len(t, before, 1)
	require.Equal(t, fire.run.RunID, before[0].RunID)
	require.Equal(t, fire.run.SessionID, before[0].SessionID)
	require.Equal(t, task.StatusDone, before[0].Status)
	require.Equal(t, u2bAsyncEvidence, before[0].Result)
	require.NotNil(t, before[0].EndedAt)
	require.Empty(t, p1.storedJobs(t, fire.taskID))
	p1.stop()
	p2 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, before, p2.runs(t, fire.taskID), "F4: the completed once run is not replayed")
	p2.stop()
	p3 := bootU2bLauncherProcess(t, home, clk)
	require.Equal(t, before, p3.runs(t, fire.taskID), "F4: another boot preserves its exact completed history")
}
