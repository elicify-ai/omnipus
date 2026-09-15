// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_hard_stop_test.go pins that a HARD Stop of a native task worker's
// turn ends the task, exactly like a graceful stop and like an external-CLI
// worker's cancel: Failed "Stopped: …", no attempt used, no restart, no second
// worker call, one goal outcome line.
//
// Why it needs its own test: a hard abort (InterruptSessionHard — the Stop
// escalation, and any direct hard interrupt) ends a turn through abortTurn's
// "clean user action" case, which returns NO error. The task run loop used to
// read that silence as a turn that simply made no claim, re-prompt the worker,
// spend its tries and then an attempt.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// stoppedTurnResult is the task result a stopped turn ends with: "Stopped: "
// followed by the contract's plain message for a stopped turn.
const stoppedTurnResult = "Stopped: This turn was stopped before it finished."

// blockingWorker's first request blocks until its context is cancelled; any
// later request answers at once without claiming.
type blockingWorker struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	once    sync.Once
}

func (w *blockingWorker) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	w.calls++
	n := w.calls
	w.mu.Unlock()
	if n == 1 {
		w.once.Do(func() { close(w.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &providers.LLMResponse{Content: "I am still working."}, nil
}

func (w *blockingWorker) GetDefaultModel() string { return "blocking-worker-model" }

func (w *blockingWorker) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// Given a native task run whose worker is mid-request
// When its session is hard-stopped
// Then the task ends Failed "Stopped: …" with no attempt used, no restart, no
// second worker call, no Judge call, and exactly one goal outcome line.
func TestTaskRun_HardStopOfANativeWorkerTurn_EndsTheTask(t *testing.T) {
	worker := &blockingWorker{started: make(chan struct{})}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	select {
	case <-worker.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker's first request never arrived")
	}
	running, err := al.taskStore.Get(tk.ID)
	if err != nil || running.SessionID == "" {
		t.Fatalf("read the running task: err=%v session=%q", err, running.SessionID)
	}
	turns, err := al.InterruptSessionHard(running.SessionID, ScopeSubtree, "")
	if err != nil || len(turns) == 0 {
		t.Fatalf("InterruptSessionHard reached no turn: turns=%v err=%v", turns, err)
	}

	waitExecutorRunsDone(t, al.taskExecutor)

	final, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("re-read task: %v", err)
	}
	if final.Status != task.StatusFailed || final.Result != stoppedTurnResult {
		t.Fatalf("status = %q result = %q, want failed %q", final.Status, final.Result, stoppedTurnResult)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a stopped run is not a failed attempt", final.AttemptCount)
	}
	if n := worker.callCount(); n != 1 {
		t.Errorf("worker requests = %d, want 1 — a stopped worker must not be prompted again", n)
	}
	if n := judge.callCount(); n != 0 {
		t.Errorf("Judge calls = %d, want 0", n)
	}
	if final.SessionID != running.SessionID {
		t.Errorf("session %q -> %q: the task restarted in a fresh run", running.SessionID, final.SessionID)
	}

	store := al.GetAgentStore(tk.AgentID)
	waitForGoalOutcomeEntry(t, store, final.SessionID)
	rec := taskGoalRecordOf(t, tk.ID)
	e := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
	if !strings.Contains(e.Content, final.Result) {
		t.Errorf("outcome line %q does not carry the task's result %q", e.Content, final.Result)
	}
}
