package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracles: /workspace/logs/6b-fix/red/addendum-u2b.md (all three addenda)
// and /workspace/logs/6b-uat/handoff-2b.txt, sections 4.1/4.3, F1 and F4.
// The held provider is an external process edge. Dispatch, actual TaskRuns,
// persisted crash images, boot recovery and judged completion are all real.
func TestU2bAsyncOnce_CrashAfterDispatchReturns_ReplaysOnceThenNoMore(t *testing.T) {
	home := t.TempDir()
	clk := newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bAsyncProcess(t, home, clk, worker)
	fire := p1.fireWhileProviderHeld(t, worker)
	jobsAtDeath := p1.storedJobs(t, fire.taskID)
	t.Logf("crash boundary: dispatch lane returned; real run is open; persisted task jobs=%d", len(jobsAtDeath))
	image := u2bAsyncSnapshot(t, home)
	// Cleanup may settle the old execution, but only the earlier, unchanged
	// crash bytes enter boot 2. No old goroutine can write after restoration.
	p1.stop()
	u2bAsyncRestore(t, home, image)

	p2 := bootU2bAsyncProcess(t, home, clk)
	runs := p2.runs(t, fire.taskID)
	original, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, "abandoned: gateway restarted mid-execution", original.Result,
		"F1: physical boot closes the dead process's actual open run as abandoned")
	require.Equal(t, u2bAsyncEvidence, replay.Result)
	// Approved option (a): replay must come from the original retained job,
	// not newly manufactured eligibility inferred from a task's status alone.
	require.Len(t, jobsAtDeath, 1, "the delivered once job must outlive asynchronous dispatch")
	require.Equal(t, fire.jobID, jobsAtDeath[0].ID)
	require.True(t, jobsAtDeath[0].Enabled)
	require.True(t, jobsAtDeath[0].State.Running, "the in-flight fire stays durably owned until actual settlement")
	require.Empty(t, p2.storedJobs(t, fire.taskID), "a completed replay settles its retained job")

	// Another tick is not another fire; another physical boot that interrupts
	// nothing also adds zero. Compare the full persisted history, not a counter
	// in a dispatch stub, and preserve the original run identities/results.
	p2.runDueAndJoin(t)
	require.Equal(t, runs, p2.runs(t, fire.taskID), "F1: never more than one replay per interrupted fire")
	p2.stop()
	p3 := bootU2bAsyncProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "F1: a further boot after replay completion adds no run")
}

// F4 is a positive control: a real once run that completed before a restart
// stays one run, with its original identity and outcome, across both boots.
func TestU2bAsyncOnce_CompletedBeforeRestart_IsNotReplayed(t *testing.T) {
	home := t.TempDir()
	clk := newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bAsyncProcess(t, home, clk, worker)
	fire := p1.fireWhileProviderHeld(t, worker)
	worker.complete()
	u2bAsyncJoin(t, "completed real task", p1.loop.taskExecutor.wg.Wait)
	before := p1.runs(t, fire.taskID)
	require.Len(t, before, 1)
	require.Equal(t, fire.run.RunID, before[0].RunID)
	require.Equal(t, task.StatusDone, before[0].Status)
	require.Equal(t, u2bAsyncEvidence, before[0].Result)
	require.NotNil(t, before[0].EndedAt)

	p1.stop()
	p2 := bootU2bAsyncProcess(t, home, clk)
	require.Equal(t, before, p2.runs(t, fire.taskID), "F4: a completed once run is never replayed")
	p2.stop()
	p3 := bootU2bAsyncProcess(t, home, clk)
	require.Equal(t, before, p3.runs(t, fire.taskID), "F4: repeated boots still leave exactly one original run")
}

// Addendum (d) prevents a partial fix that keeps a Running job for replay but
// never removes it on actual normal completion. No manual completion/upsert
// notification is sent by this test: the real executor must do its own work.
func TestU2bAsyncOnce_NormalCompletion_RemovesPersistedJob(t *testing.T) {
	home := t.TempDir()
	clk := newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bAsyncProcess(t, home, clk, worker)
	fire := p1.fireWhileProviderHeld(t, worker)
	worker.complete()
	u2bAsyncJoin(t, "settlement of the real asynchronous run", p1.loop.taskExecutor.wg.Wait)
	before := p1.runs(t, fire.taskID)
	require.Len(t, before, 1)
	require.Equal(t, task.StatusDone, before[0].Status)
	require.Equal(t, u2bAsyncEvidence, before[0].Result)
	require.NotNil(t, before[0].EndedAt)
	require.Empty(t, p1.storedJobs(t, fire.taskID), "normal completion must leave no persisted cron job for this task")

	p1.stop()
	p2 := bootU2bAsyncProcess(t, home, clk)
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	require.Equal(t, before, p2.runs(t, fire.taskID), "a settled fire is not recreated at a later boot")
}

// Binding addendum 3: orderly shutdown is not a user Stop. It must preserve
// exactly one replay of the once run it interrupted, just as a kill does.
// Unlike the crash test, this never restores or alters the shutdown's files.
func TestU2bAsyncOnce_GracefulStop_PreservesOneReplay(t *testing.T) {
	home := t.TempDir()
	clk := newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bAsyncProcess(t, home, clk, worker)
	fire := p1.fireWhileProviderHeld(t, worker)
	p1.stop()

	stopped, err := p1.loop.taskStore.Get(fire.taskID)
	require.NoError(t, err)
	stoppedRuns := p1.runs(t, fire.taskID)
	require.Len(t, stoppedRuns, 1, "instrument: orderly shutdown has joined the original execution")
	t.Logf("orderly stop persisted: task status=%q result=%q; run status=%q result=%q open=%t; task jobs=%d",
		stopped.Status, stopped.Result, stoppedRuns[0].Status, stoppedRuns[0].Result,
		stoppedRuns[0].IsOpen(), len(p1.storedJobs(t, fire.taskID)))

	p2 := bootU2bAsyncProcess(t, home, clk)
	runs := p2.runs(t, fire.taskID)
	_, replay := requireU2bAsyncReplay(t, fire, runs)
	require.Equal(t, u2bAsyncEvidence, replay.Result, "addendum 3: one real replay also follows a graceful restart")
	require.Empty(t, p2.storedJobs(t, fire.taskID))
	p2.stop()
	p3 := bootU2bAsyncProcess(t, home, clk)
	require.Equal(t, runs, p3.runs(t, fire.taskID), "the graceful restart's replay is not repeated at a later boot")
}

func bootU2bAsyncProcess(t *testing.T, home string, clk *triggerFakeClock) *u2bAsyncProcess {
	t.Helper()
	worker := newU2bAsyncWorker()
	worker.complete()
	p := newU2bAsyncProcess(t, home, clk, worker)
	p.physicalBoot(t)
	p.runDueAndJoin(t)
	return p
}

func requireU2bAsyncReplay(t *testing.T, fire u2bAsyncFire, runs []task.TaskRun) (task.TaskRun, task.TaskRun) {
	t.Helper()
	require.Len(t, runs, 2,
		"U2b/F1: the interrupted once fire owes exactly one real replay; history must contain the original and replay, not only the original")
	original, replay := runs[0], runs[1]
	if replay.RunID == fire.run.RunID {
		original, replay = replay, original
	}
	require.Equal(t, fire.run.RunID, original.RunID, "boot retains the original run's identity")
	require.Equal(t, fire.run.SessionID, original.SessionID)
	require.Equal(t, task.StatusFailed, original.Status)
	require.NotNil(t, original.EndedAt)
	require.NotEqual(t, original.RunID, replay.RunID)
	require.Equal(t, task.StatusDone, replay.Status, "the replay's real worker claim is judged and completed")
	require.Equal(t, u2bAsyncEvidence, replay.Result)
	require.NotNil(t, replay.EndedAt)
	require.NotEmpty(t, replay.SessionID)
	require.NotEqual(t, original.SessionID, replay.SessionID)
	require.Equal(t, task.RunKindScheduled, replay.Kind)
	require.Nil(t, replay.OccurrenceMs, "a once fire is not a recurring occurrence")
	return original, replay
}
