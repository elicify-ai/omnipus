// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type bindingWorkerSnapshot struct {
	calls      int
	turns      int
	sessionIDs []string
}

// Only the paid-provider boundary is scripted. The response invokes the real
// registered goal_claim tool, including its persisted owner-session check.
type bindingClaimWorker struct {
	mu         sync.Mutex
	store      *task.Store
	taskID     string
	calls      int
	turns      int
	sessionIDs []string
}

func (w *bindingClaimWorker) Chat(
	ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stored, err := w.store.Get(w.taskID)
	if err != nil {
		return nil, fmt.Errorf("binding worker fixture: read task before provider reply: %w", err)
	}
	w.sessionIDs = append(w.sessionIDs, stored.SessionID)
	if n := len(messages); n > 0 && messages[n-1].Role == "tool" {
		return &providers.LLMResponse{Content: "I have claimed completion."}, nil
	}
	w.turns++
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: fmt.Sprintf("binding-goal-claim-%d", w.turns), Type: "function",
		Name: tools.GoalClaimToolName,
		Arguments: map[string]any{
			"status": tools.GoalClaimStatusMet, "evidence": "verified report.md exists",
		},
	}}}, nil
}

func (w *bindingClaimWorker) GetDefaultModel() string { return "binding-worker-model" }

func (w *bindingClaimWorker) snapshot() bindingWorkerSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bindingWorkerSnapshot{calls: w.calls, turns: w.turns, sessionIDs: append([]string(nil), w.sessionIDs...)}
}

// bindingMetJudge follows the same no-tools provider protocol as the existing
// task-run fixtures. It echoes the real requested criterion IDs; malformed or
// empty input is a visible fixture error, never a fabricated positive verdict.
type bindingMetJudge struct {
	mu    sync.Mutex
	calls int
}

func (j *bindingMetJudge) Chat(
	ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	j.mu.Lock()
	j.calls++
	j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var user string
	for _, message := range messages {
		if message.Role == "user" {
			user = message.Content
		}
	}
	const header = "## Prose criteria to judge"
	headerAt := strings.Index(user, header)
	if headerAt < 0 {
		return nil, fmt.Errorf("binding judge fixture: missing %q", header)
	}
	block := user[headerAt+len(header):]
	arrayAt := strings.Index(block, "[")
	if arrayAt < 0 {
		return nil, fmt.Errorf("binding judge fixture: missing criterion array")
	}
	var asked []struct { // not-wire-format: provider prompt, not gateway/SPA data
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(strings.NewReader(block[arrayAt:])).Decode(&asked); err != nil {
		return nil, fmt.Errorf("binding judge fixture: decode criteria: %w", err)
	}
	if len(asked) == 0 {
		return nil, fmt.Errorf("binding judge fixture: empty criterion array")
	}
	items := make([]string, 0, len(asked))
	for _, criterion := range asked {
		if criterion.ID == "" || criterion.Text == "" {
			return nil, fmt.Errorf("binding judge fixture: criterion needs ID and text")
		}
		items = append(items, fmt.Sprintf(`{"id":%q,"met":true,"reason":"the recorded work supports the claim"}`, criterion.ID))
	}
	return &providers.LLMResponse{
		Content: fmt.Sprintf(`{"met":true,"criteria":[%s]}`, strings.Join(items, ",")),
	}, nil
}

func (j *bindingMetJudge) GetDefaultModel() string { return "binding-judge-model" }

func (j *bindingMetJudge) callCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.calls
}
