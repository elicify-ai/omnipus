// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// orphan_markup_task_attempt_test.go is UAT A-12's end-to-end oracle for the
// orphan-tool-markup exit. A task attempt whose model keeps emitting tool-call
// markup as TEXT exhausts runTurn's repair budget and the turn ends with an
// error. That error used to be untyped, so the task executor's type-only
// classifier (task_attempt_turn_error.go::attemptRecoverableTurnErrorCode)
// could not see it and the task went terminal `failed` at attempt 1 with its
// budget unused. The required behaviour (planning-goals-spec FR-045 / US-5
// AS-4, under the two-level model of 2026-09-14, issue #710): the fault spends
// one goal try inside the same run, the worker is re-prompted with a note, and
// its next claim is judged — no task attempt is used.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// orphanMarkupAttemptWorker answers every request that does NOT yet carry the
// malformed-output note with residual tool-call markup and no parsed tool call
// (the verbatim UAT residue), so the attempt exhausts runTurn's repair budget.
// Once the run re-prompts it with the note, it claims through goal_claim.
type orphanMarkupAttemptWorker struct {
	mu       sync.Mutex
	requests []string
}

func (w *orphanMarkupAttemptWorker) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	flat := sb.String()
	w.mu.Lock()
	w.requests = append(w.requests, flat)
	w.mu.Unlock()
	if strings.Contains(flat, malformedOutputNote) {
		return cleanClaimTurn(msgs), nil
	}
	return &providers.LLMResponse{Content: uatOrphanToolMarkupLeaks[3].content, ToolCalls: []providers.ToolCall{}}, nil
}

func (w *orphanMarkupAttemptWorker) GetDefaultModel() string { return "scripted-model" }

func (w *orphanMarkupAttemptWorker) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

// BDD: Given a task whose first try ends because the model's tool calls keep
// arriving as unparseable markup until the repair budget is spent,
// When the run handles that try,
// Then the task is NOT failed and NO task attempt is used — one goal try is
// spent inside the same run,
// And the worker is re-prompted with a note that the previous try ended on
// malformed tool-call output,
// And its next, clean claim is judged and completes the task.
func TestTaskAttempt_OrphanMarkupExhaustion_SpendsOneTryAndContinuesTheRun(t *testing.T) {
	worker := &orphanMarkupAttemptWorker{}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "one line, exit 0"}
	judgeInst.Provider = judge

	maxAttempts := 5
	tk := &task.Task{
		Title: "UAT A-12 greeting script", Prompt: "fix greeting.sh", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "greeting.sh prints exactly one line")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	// Poll for ANY terminal status first, so a regression that fails the task on
	// the spot is reported as the wrong status rather than as a wait timeout:
	// t3WaitForTerminal also waits for the task session to be archived, which
	// the fail-on-the-spot path never does. Only a task that reached done then
	// waits for that archive, so no write races the temp-dir cleanup.
	deadline := time.Now().Add(40 * time.Second)
	var reached *task.Task
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.IsTerminal(got.Status) {
			reached = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if reached == nil {
		t.Fatal("task reached no terminal status within 40s")
	}
	if reached.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q — a try that ended on unparseable tool-call markup must not fail "+
			"the task (result: %s)", reached.Status, task.StatusDone, reached.Result)
	}
	final := t3WaitForTerminal(t, al, tk.ID, 2)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q — a try that ended on unparseable tool-call markup must not fail "+
			"the task (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — the markup-exhausted try spends a goal try inside the run, "+
			"not a task attempt", final.AttemptCount)
	}
	if got := judge.callCount(); got != 1 {
		t.Errorf("judge ran %d time(s), want exactly 1 — the markup try has no claim to judge; "+
			"the clean claim must be judged normally", got)
	}

	reqs := worker.snapshot()
	withoutNote := 0
	for _, r := range reqs {
		if strings.Contains(r, malformedOutputNote) {
			break
		}
		withoutNote++
	}
	if withoutNote != 1+maxOrphanToolMarkupRepairs {
		t.Errorf("the first try made %d request(s) before the re-prompt, want %d (1 + the repair budget) — "+
			"the try must end on the repair-budget exhaustion, not earlier or later", withoutNote, 1+maxOrphanToolMarkupRepairs)
	}
	if len(reqs) == 0 || !strings.Contains(reqs[len(reqs)-1], malformedOutputNote) {
		t.Errorf("the re-prompted try's request does not say the previous try ended on %s", malformedOutputNote)
	}
}
