package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

// Hold the paid-provider boundary, not the task executor, so the reload occurs
// while a real task turn is alive. No timing-dependent sleep is needed.
type reloadHeldClaimWorker struct {
	*claimingWorker
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func (w *reloadHeldClaimWorker) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	w.enterOnce.Do(func() { close(w.entered) })
	select {
	case <-w.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return w.claimingWorker.Chat(ctx, messages, definitions, model, options)
}

func (w *reloadHeldClaimWorker) unblock() {
	w.releaseOnce.Do(func() { close(w.release) })
}

// UAT B8: a configuration reload reused PlanEngine.Start's crash sweep and
// marked a working task failed(interrupted). Its later, judged completion then
// hit terminal immutability. Reload must not act like a process restart; actual
// crash recovery must still work on a fresh engine over the same saved records.
func TestPlanEngineReload_DoesNotFinalizeLiveTask(t *testing.T) {
	t.Run("live_task_keeps_its_one_terminal_outcome", func(t *testing.T) {
		const evidence = "report.md contains all three supplier quotes and was read back"
		worker := &reloadHeldClaimWorker{
			claimingWorker: newClaimingWorker(turnClaimMet(evidence)),
			entered:        make(chan struct{}),
			release:        make(chan struct{}),
		}
		al, judge := newGoalLoopTestLoop(t, worker, nil)
		judge.Provider = &b6ScriptedJudge{metFromCall: 1, reason: "verified"}
		ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
		al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(t.TempDir(), "session_messages")), ls)
		al.taskExecutor.SetLifecycleStore(ls)
		mintGenuineBootEpochForLoop(t, al)

		pe := NewPlanEngine(al, plan.New(filepath.Join(t.TempDir(), "plans")), al.taskStore, al.taskExecutor)
		pe.tickInterval = time.Hour // All engine activity in this test is explicit.
		pe.SetLifecycleStore(ls)
		var interrupted atomic.Int32
		pe.SetSessionFailedHook(func(_, _ string) { interrupted.Add(1) })
		t.Cleanup(func() {
			worker.unblock()
			al.taskExecutor.wg.Wait() // Join the producer, not just its status write.
			pe.Stop()
		})
		require.NoError(t, pe.Start(context.Background()))
		tk := newRunLoopTask(t, al, nil)
		require.NoError(t, al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil))
		select {
		case <-worker.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the real task never reached the held provider request")
		}
		current, err := al.taskStore.Get(tk.ID)
		require.NoError(t, err)
		require.Equal(t, task.StatusInProgress, current.Status)
		require.NotEmpty(t, current.SessionID)
		rec, err := ls.Load(current.SessionID)
		require.NoError(t, err)
		require.Equal(t, session.LifecycleRunning, rec.State)
		require.Nil(t, rec.SteeredBy, "cover the dispatcher-started, no-edge task from B8")
		before := snapshotBootSweepRecord(t, ls, current.SessionID)

		// Repeated same-instance Stop/Start is the gateway's reload path.
		for reload := 0; reload < 2; reload++ {
			pe.Stop()
			require.NoError(t, pe.Start(context.Background()))
			after := snapshotBootSweepRecord(t, ls, current.SessionID)
			require.Equal(t, before.record, after.record, "reload must not finalize a working task")
			require.Equal(t, before.journal, after.journal, "reload must append no task lifecycle transition")
		}
		require.EqualValues(t, 0, interrupted.Load(), "no crash notice for work still executing")
		runs, err := al.taskStore.ListRuns(tk.ID)
		require.NoError(t, err)
		require.Len(t, runs, 1, "reload must neither restart nor duplicate the run")
		require.Equal(t, task.StatusInProgress, runs[0].Status)

		worker.unblock()
		finished := make(chan struct{})
		go func() { al.taskExecutor.wg.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(15 * time.Second):
			t.Fatal("task did not settle after its provider was released")
		}
		final, err := al.taskStore.Get(tk.ID)
		require.NoError(t, err)
		require.Equal(t, task.StatusDone, final.Status)
		require.Equal(t, evidence, final.Result, "the worker's verified result must survive reload")
		require.Equal(t, 0, final.AttemptCount)
		runs, err = al.taskStore.ListRuns(tk.ID)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, task.StatusDone, runs[0].Status)
		require.Equal(t, evidence, runs[0].Result)
		require.Equal(t, current.SessionID, runs[0].SessionID)
		meta, err := al.GetSessionStore().GetMeta(current.SessionID)
		require.NoError(t, err)
		require.Equal(t, session.StatusArchived, meta.Status)

		journal, err := os.ReadFile(filepath.Join(ls.Dir(), current.SessionID+".jsonl"))
		require.NoError(t, err)
		var terminalStates []session.LifecycleState
		for _, line := range bytes.Split(bytes.TrimSpace(journal), []byte("\n")) {
			var entry session.LifecycleRecord
			require.NoError(t, json.Unmarshal(line, &entry))
			if entry.Terminal() {
				terminalStates = append(terminalStates, entry.State)
			}
		}
		require.Equal(t, []session.LifecycleState{session.LifecycleCompleted}, terminalStates,
			"exactly one terminal write, by the executor; never recovery's failed(interrupted)")
	})

	t.Run("fresh_engine_still_recovers_saved_orphans", func(t *testing.T) {
		h := newBootSweepHarness(t)
		h.pe.tickInterval = time.Hour
		t.Cleanup(h.pe.Stop)
		var failures []string
		h.pe.SetSessionFailedHook(func(id, reason string) {
			require.Equal(t, failedReasonInterrupted, reason)
			failures = append(failures, id)
		})
		orphan := func(id string) *session.LifecycleRecord {
			return &session.LifecycleRecord{
				SessionID: id, Generation: 1, State: session.LifecycleRunning,
				WorkspaceID: "ws", AgentID: "worker", OwnerScopeKind: session.OwnerScopeHuman,
				Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: "task-" + id},
			}
		}
		persistLifecycle(t, h.ls, orphan("before-first-start"))
		require.NoError(t, h.pe.Start(context.Background()))
		first, err := h.ls.Load("before-first-start")
		require.NoError(t, err)
		require.Equal(t, session.LifecycleFailed, first.State)
		require.Equal(t, failedReasonInterrupted, first.FailedReason)
		require.Equal(t, []string{"before-first-start"}, failures)

		persistLifecycle(t, h.ls, orphan("after-first-start"))
		before := snapshotBootSweepRecord(t, h.ls, "after-first-start")
		h.pe.Stop()
		require.NoError(t, h.pe.Start(context.Background()))
		after := snapshotBootSweepRecord(t, h.ls, "after-first-start")
		require.Equal(t, before.journal, after.journal, "same engine reload must not sweep a new execution")
		require.Equal(t, []string{"before-first-start"}, failures)
		h.pe.Stop()

		// A new process constructs a new engine; no sweep-once state may leak
		// between engines. Reopen the durable store, not its in-memory index.
		restarted := newTestPlanEngine(t)
		restarted.pe.tickInterval = time.Hour
		restarted.pe.SetLifecycleStore(session.NewLifecycleStore(h.ls.Dir()))
		restarted.pe.SetSessionFailedHook(func(id, reason string) {
			require.Equal(t, failedReasonInterrupted, reason)
			failures = append(failures, id)
		})
		t.Cleanup(restarted.pe.Stop)
		require.NoError(t, restarted.pe.Start(context.Background()))
		recovered, err := session.NewLifecycleStore(h.ls.Dir()).Load("after-first-start")
		require.NoError(t, err)
		require.Equal(t, session.LifecycleFailed, recovered.State)
		require.Equal(t, failedReasonInterrupted, recovered.FailedReason)
		require.Equal(t, 1, recovered.Generation, "crash recovery must not invent a new execution")
		require.Equal(t, []string{"before-first-start", "after-first-start"}, failures)
		require.Empty(t, h.disp.callList(), "recovery must not redispatch orphan tasks")
		require.Empty(t, restarted.disp.callList())
	})
}
