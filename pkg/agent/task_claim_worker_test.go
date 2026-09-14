// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_claim_worker_test.go — shared fixtures for the two-level task run model
// (task_run_loop.go). A native task worker can only finish by calling the
// goal_claim tool (founder decision 2026-09-14), so an end-to-end task test
// needs a scripted worker that does exactly that through the REAL agent loop:
// the provider returns a goal_claim tool call, the loop executes the real
// tool (policy-checked, transcript-recorded), and the executor resolves the
// claim off the session transcript like production.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// workerTurn is one scripted worker turn.
type workerTurn struct {
	// claim is "" (no claim) or a tools.GoalClaimStatus* value.
	claim    string
	evidence string
	// content is the turn's final text.
	content string
	// reasoningOnly makes the turn end at the output-token limit with no
	// answer at all (ADR-087 D4a) — the founder's "reasoning-only try".
	reasoningOnly bool
}

func turnClaimMet(evidence string) workerTurn {
	return workerTurn{claim: tools.GoalClaimStatusMet, evidence: evidence, content: "I have claimed completion."}
}

func turnClaimBlocked(reason string) workerTurn {
	return workerTurn{claim: tools.GoalClaimStatusBlocked, evidence: reason, content: "I cannot proceed."}
}

func turnNoClaim(content string) workerTurn { return workerTurn{content: content} }

func turnReasoningOnly() workerTurn { return workerTurn{reasoningOnly: true} }

// claimingWorker replays turns in order (the last one repeats). A turn that
// claims answers its FIRST LLM call with a goal_claim tool call and the call
// after the tool result with its final text.
type claimingWorker struct {
	mu       sync.Mutex
	turns    []workerTurn
	started  int
	requests []string // the flattened request that STARTED each turn
}

func newClaimingWorker(turns ...workerTurn) *claimingWorker {
	return &claimingWorker{turns: turns}
}

func (w *claimingWorker) turnAt(i int) workerTurn {
	if len(w.turns) == 0 {
		return workerTurn{content: "no script"}
	}
	if i >= len(w.turns) {
		return w.turns[len(w.turns)-1]
	}
	return w.turns[i]
}

func (w *claimingWorker) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		cur := w.turnAt(w.started - 1)
		content := cur.content
		if content == "" {
			content = "Turn finished."
		}
		return &providers.LLMResponse{Content: content}, nil
	}
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	w.requests = append(w.requests, sb.String())
	cur := w.turnAt(w.started)
	w.started++
	switch {
	case cur.reasoningOnly:
		return &providers.LLMResponse{ReasoningContent: "thinking about it at length", FinishReason: "length"}, nil
	case cur.claim == "":
		return &providers.LLMResponse{Content: cur.content}, nil
	}
	args := map[string]any{"status": cur.claim}
	if cur.evidence != "" {
		args["evidence"] = cur.evidence
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: fmt.Sprintf("call-goal-claim-%d", w.started), Type: "function",
		Name: tools.GoalClaimToolName, Arguments: args,
	}}}, nil
}

func (w *claimingWorker) GetDefaultModel() string { return "claiming-worker-model" }

func (w *claimingWorker) turnsStarted() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started
}

func (w *claimingWorker) requestSnapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

// alwaysUnmet is a b6ScriptedJudge metFromCall value no test reaches.
const alwaysUnmet = 1 << 30

// createTaskWithGoal creates tk and its DEFINING-phase goal record the way
// task creation does in production (criteria off the stored task, so the ids
// are shared; the given Definition of Done), and returns the stored task.
func createTaskWithGoal(t *testing.T, al *AgentLoop, tk *task.Task, dodText string) *task.Task {
	t.Helper()
	store := GetTaskStore(al)
	if err := store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	stored, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	prompt := stored.Prompt
	if prompt == "" {
		prompt = stored.Title
	}
	g, err := goal.New(generated.GoalOwnerKindTask, stored.ID, generated.GoalSourceTaskExplicit, prompt, "",
		stored.Criteria, []task.AcceptanceCriterion{proseCriterion("", dodText)},
		config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("goal.New: %v", err)
	}
	if err := goal.NewStore(config.OmnipusHomeDir()).Create(g); err != nil {
		t.Fatalf("create goal record: %v", err)
	}
	return stored
}

// taskGoalRecordOf reads the goal record owned by taskID from the store the
// test harness points at.
func taskGoalRecordOf(t *testing.T, taskID string) *goal.Goal {
	t.Helper()
	g, err := goal.NewStore(config.OmnipusHomeDir()).GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("read goal record of task %q: %v", taskID, err)
	}
	return g
}
