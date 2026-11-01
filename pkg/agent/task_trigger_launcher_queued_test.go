package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// #1553 PROPOSED oracle: expected outcome pending a chief decision, not a
// verified product requirement. Handoff F2 covers fires missed while down; it
// does not decide the outcome of an already-queued launcher admission. Keep
// these expected values unchanged until that decision. Separate from 6b.
func TestU2bLauncherOnce_QueuedAtCrash_IsMissedNeverReplayed(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	taskID, _ := p1.fireQueued(t, worker)
	image := u2bAsyncSnapshot(t, home)
	p1.stop()
	u2bAsyncRestore(t, home, image)
	p2 := bootU2bLauncherProcess(t, home, clk)
	requireU2bLauncherMissedNotReplayed(t, p2, taskID)
}

func TestU2bLauncherOnce_QueuedAtGracefulStop_IsMissedNeverReplayed(t *testing.T) {
	home, clk := t.TempDir(), newTriggerFakeClock()
	worker := newU2bAsyncWorker()
	p1 := newU2bLauncherProcess(t, home, clk, worker)
	taskID, _ := p1.fireQueued(t, worker)
	p1.gracefulProcessExit(t, taskID) // No actual execution to join; shutdown files are used directly.
	p2 := bootU2bLauncherProcess(t, home, clk)
	requireU2bLauncherMissedNotReplayed(t, p2, taskID)
}

func requireU2bLauncherMissedNotReplayed(t *testing.T, p *u2bLauncherProcess, taskID string) {
	t.Helper()
	runs := p.runs(t, taskID)
	t.Logf("never-started MAIN launch after boot: history=%+v", runs)
	require.Len(t, runs, 1, "#1553 proposed: one missed record, no actually executed or replayed run")
	// Nonfatal assertions expose each difference from the proposed shape,
	// without claiming that this still-undecided shape is a spec rule.
	assert.Equal(t, task.StatusMissed, runs[0].Status, "#1553 proposed: never-started admission must be missed, not failed/abandoned")
	assert.Nil(t, runs[0].OccurrenceMs, "once fires have a null occurrence key")
	assert.Equal(t, task.RunKindScheduled, runs[0].Kind)
	assert.Empty(t, runs[0].SessionID, "a missed record is not an executed session-backed run")
	if assert.NotNil(t, runs[0].EndedAt, "missed is written already closed") {
		assert.Equal(t, runs[0].StartedAt, *runs[0].EndedAt, "#1553 proposed: missed record starts and ends at the same boot observation")
	}
	p.runDueAndJoin(t)
	require.Equal(t, runs, p.runs(t, taskID), "a due tick cannot execute a missed once fire")
	p.stop()
	again := bootU2bLauncherProcess(t, p.home, p.clock)
	require.Equal(t, runs, again.runs(t, taskID), "#1553 proposed: further physical boots preserve the one missed record unchanged")
}

// Saturate the REAL gate through the existing TestDispatch_AtCap_Queued seam.
// Never replace Dispatch with a fake returning queued. The capacity reservation
// has no goroutine, is never released/promoted, and dies with this process.
func (p *u2bLauncherProcess) fireQueued(t *testing.T, worker *u2bAsyncWorker) (string, string) {
	t.Helper()
	admitted, position, limit := p.loop.steerAdmission().tryAdmit("u2b-capacity-reservation", 1)
	require.True(t, admitted)
	require.Zero(t, position)
	require.Equal(t, 1, limit)
	require.NoError(t, p.scheduler.Start())
	tk := newCompletionContractTask(t, p.loop, "native-agent", "U2b never-started launcher once task")
	fireAt := p.clock.Now().Add(time.Minute).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &fireAt}}
	stored, err := p.loop.taskStore.Update(tk.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	p.loop.NotifyTaskUpserted(stored)
	require.Len(t, p.storedJobs(t, tk.ID), 1, "instrument: disk job reader sees the armed once fire")
	p.clock.Advance(2 * time.Minute)
	p.scheduler.RunDueJobs(p.clock.Now())
	u2bAsyncJoin(t, "real queued launcher dispatch", p.scheduler.WaitForLane)
	u2bAsyncJoin(t, "no task execution behind queued admission", p.loop.taskExecutor.wg.Wait)
	queued, err := p.loop.taskStore.Get(tk.ID)
	require.NoError(t, err)
	p.requireMainChild(t, tk.ID, queued.SessionID)
	rec, err := p.loop.GetSessionLifecycleStore().Load(queued.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, rec.State, "instrument: the actual admission remains queued")
	require.NotNil(t, rec.ExecutionID)
	gate := p.loop.steerAdmission()
	gate.mu.Lock()
	entries := append([]steerQueueEntry(nil), gate.queue...)
	gate.mu.Unlock()
	require.Len(t, entries, 1)
	require.Equal(t, queued.SessionID, entries[0].sessionID)
	require.Equal(t, rec.Generation, entries[0].generation)
	require.Equal(t, rec.ExecutionID.RunID, entries[0].runID)
	require.Equal(t, rec.ExecutionID.BootSeq, entries[0].bootSeq)
	require.False(t, gate.hasReservation(queued.SessionID, rec.Generation))
	require.False(t, p.loop.taskExecutor.holdsRun(tk.ID), "instrument: never entered dispatchLaunchedTask")
	require.Nil(t, p.loop.getActiveTurnState(taskTurnSessionKey(tk.AgentID, tk.ID)))
	select {
	case <-worker.entered:
		t.Fatal("fixture: queued task unexpectedly reached its provider before admission")
	default:
	}
	t.Logf("queued boundary: real queue entry; executor owns no run; provider never entered; pre-boot history=%+v; jobs=%d",
		p.runs(t, tk.ID), len(p.storedJobs(t, tk.ID)))
	return tk.ID, queued.SessionID
}
