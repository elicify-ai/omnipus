package agent

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Copy of the real queue-only seam from local #1553 tests, renamed so those
// tests/branch stay unchanged. None of #1553's undecided outcome assertions is
// copied. The capacity reservation is never promoted and owns no goroutine.
func (p *u2bLauncherProcess) fireQueuedPersonStop(t *testing.T, worker *u2bAsyncWorker) (string, string) {
	t.Helper()
	admitted, position, limit := p.loop.steerAdmission().tryAdmit("u2b-capacity-reservation", 1)
	require.True(t, admitted)
	require.Zero(t, position)
	require.Equal(t, 1, limit)
	require.NoError(t, p.scheduler.Start())
	tk := newCompletionContractTask(t, p.loop, "native-agent", "C1 queued launcher once task stopped by a person")
	fireAt := p.clock.Now().Add(time.Minute).UnixMilli()
	trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &fireAt}}
	stored, err := p.loop.taskStore.Update(tk.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	p.loop.NotifyTaskUpserted(stored)
	require.Len(t, p.storedJobs(t, tk.ID), 1, "instrument: disk reader sees the armed once fire before dispatch")
	p.clock.Advance(2 * time.Minute)
	p.scheduler.RunDueJobs(p.clock.Now())
	u2bAsyncJoin(t, "real queued MAIN launcher dispatch", p.scheduler.WaitForLane)
	u2bAsyncJoin(t, "never-started admission owns no executor goroutine", p.loop.taskExecutor.wg.Wait)
	queued, err := p.loop.taskStore.Get(tk.ID)
	require.NoError(t, err)
	p.requireMainChild(t, tk.ID, queued.SessionID)
	rec, err := p.loop.GetSessionLifecycleStore().Load(queued.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, rec.State, "instrument: target genuinely remains queued")
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
	require.False(t, p.loop.taskExecutor.holdsRun(tk.ID), "instrument: actual task execution never started")
	require.Nil(t, p.loop.getActiveTurnState(taskTurnSessionKey(tk.AgentID, tk.ID)))
	u2bRequireProviderNotEntered(t, worker)
	return tk.ID, queued.SessionID
}

func u2bRequireProviderNotEntered(t *testing.T, worker *u2bAsyncWorker) {
	t.Helper()
	select {
	case <-worker.entered:
		t.Error("a person-stopped queued launch must never enter its provider, before or after either boot")
	default:
	}
}

// Fresh metadata enumeration plus unreadable-file detection avoids a cached
// empty/partial session list falsely proving that boot created no new child.
func u2bQueuedStopSessionIDs(t *testing.T, p *u2bLauncherProcess) []string {
	t.Helper()
	fresh, err := session.NewUnifiedStore(p.loop.GetSessionStore().BaseDir())
	require.NoError(t, err)
	unreadable, err := fresh.UnreadableSessionIDs()
	require.NoError(t, err)
	require.Empty(t, unreadable, "instrument: an unreadable session cannot silently disappear from the no-new-session check")
	metas, err := fresh.ListSessions()
	require.NoError(t, err)
	ids := make([]string, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	sort.Strings(ids)
	return ids
}

func u2bQueuedStopTaskSessionIDs(t *testing.T, p *u2bLauncherProcess, taskID string) []string {
	t.Helper()
	records, err := p.loop.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, err)
	ids := make([]string, 0, len(records))
	for _, record := range records {
		if record.Origin != nil && record.Origin.Kind == session.OriginKindTask && record.Origin.TaskID == taskID {
			ids = append(ids, record.SessionID)
		}
	}
	sort.Strings(ids)
	return ids
}
