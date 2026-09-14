// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_turn_error_test.go pins how a task's goal loop treats an
// attempt that ENDS ON A TURN ERROR (task_attempt_turn_error.go).
//
// The defect (live UAT lane L2, scenario A-12): a task's attempt ended because
// the model emitted a tool call as unparseable output, and the task went
// terminal `failed` at attempt 1/20 — the error branch of finishTaskRun failed
// every error on the spot. The required behaviour, from planning-goals-spec.md
// FR-045 / US-5 AS-4 ("treated as an unmet claim (attempt consumed, re-dispatch
// or owner-wake) — NOT terminally failed on the spot"), and the operator's
// scope for which errors qualify:
//
//   - a malformed-tool-call-output fault (typed *common.ToolArgumentsError,
//     CodeToolArgs / CodeToolCallTruncated) consumes ONE attempt and
//     re-dispatches with a note naming the fault; a later clean attempt is
//     judged normally;
//   - any other execution error (auth/config/provider-hard, Stop) still fails
//     the task, consuming nothing;
//   - the attempt budget still ends the loop when the fault repeats.
//
// Oracles are the spec's observable outcomes — task status, AttemptCount, how
// many times the Judge ran, and what the next attempt's prompt carried — never
// the implementation's own strings.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// malformedOutputNote is the phrase the operator's requirement names for the
// note the next attempt must carry: "the previous one ended on malformed
// tool-call output".
const malformedOutputNote = "malformed tool-call output"

const cleanWorkerClaim = "fixed greeting.sh so it prints one line\n" +
	"[goal:evidence] ran ./greeting.sh | wc -l -> 1, exit 0\n" +
	"TASK_STATUS: success\nTASK_SUMMARY: greeting.sh prints exactly one line."

// typedMalformedToolCall builds the typed refusal a provider raises for an
// undecodable tool call — the same shape pkg/providers/common returns.
func typedMalformedToolCall(truncated bool) error {
	return common.NewToolArgumentsError("write_file",
		fmt.Errorf("%w: tool %q arguments %q", common.ErrToolArgumentsUndecodable,
			"write_file", `{"path":"greeting.sh","content":"#!/bin/sh`),
		truncated)
}

// attemptScriptedWorker is the worker agent's provider. Every call whose
// request does NOT carry the malformed-output note fails with failErr(); once
// the note is present (i.e. the goal loop re-dispatched with it) the worker
// answers with a clean, evidenced success claim — unless failForever is set.
// Keying on the note rather than on a call count keeps the test independent of
// how many in-turn repair calls runTurn spends before giving up.
type attemptScriptedWorker struct {
	mu          sync.Mutex
	failErr     func() error
	failForever bool
	requests    []string // flattened content of every request, in order
}

func (w *attemptScriptedWorker) Chat(
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
	failForever := w.failForever
	w.mu.Unlock()

	if failForever || !strings.Contains(flat, malformedOutputNote) {
		return nil, w.failErr()
	}
	return &providers.LLMResponse{Content: cleanWorkerClaim}, nil
}

func (w *attemptScriptedWorker) GetDefaultModel() string { return "scripted-model" }

func (w *attemptScriptedWorker) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requests...)
}

func countCarryingNote(requests []string) int {
	n := 0
	for _, r := range requests {
		if strings.Contains(r, malformedOutputNote) {
			n++
		}
	}
	return n
}

// waitForTaskStatus polls the store for want. The failTask path leaves the
// session `interrupted` rather than archived, so t3WaitForTerminal's archive
// condition cannot be used for it.
func waitForTaskStatus(t *testing.T, al *AgentLoop, taskID string, want task.Status, within time.Duration) *task.Task {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if got.Status == want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := al.taskStore.Get(taskID)
	t.Fatalf("task did not reach %q within %s (last status %q, result %q) — a WAIT timeout",
		want, within, got.Status, got.Result)
	return nil
}

// BDD: Given a task whose first attempt ends because the model's tool call
// could not be decoded,
// When the goal loop handles that attempt,
// Then the task is NOT failed — exactly one attempt is consumed,
// And the task is re-dispatched with a note that the previous attempt ended on
// malformed tool-call output,
// And the next, clean attempt's claim is judged and completes the task.
func TestTaskAttempt_MalformedToolOutput_ConsumesOneAttemptAndRedispatches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		truncated bool
	}{
		{"tool_args", false},
		{"tool_call_truncated", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := &attemptScriptedWorker{failErr: func() error { return typedMalformedToolCall(tc.truncated) }}
			al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
			judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
				return &providers.LLMResponse{
					Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"one line, exit 0"}]}`,
				}, nil
			}}
			judgeInst.Provider = judge

			maxAttempts := 5
			tk := &task.Task{
				Title: "UAT-J1 build greeting script", Prompt: "fix greeting.sh", Action: task.ActionLLM,
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

			final := t3WaitForTerminal(t, al, tk.ID, 2)
			if final.Status != task.StatusDone {
				t.Fatalf("status = %q, want %q — an attempt that ended on malformed tool-call output must not "+
					"fail the task while attempts remain (result: %s)", final.Status, task.StatusDone, final.Result)
			}
			if final.AttemptCount != 1 {
				t.Errorf("attempt_count = %d, want 1 — the malformed attempt consumes exactly one attempt, "+
					"and the met verdict on the next attempt consumes none", final.AttemptCount)
			}
			if got := judge.callCount(); got != 1 {
				t.Errorf("judge ran %d time(s), want exactly 1 — the malformed attempt has no claim to judge; "+
					"the clean attempt's claim must be judged normally", got)
			}

			reqs := worker.snapshot()
			if len(reqs) < 2 {
				t.Fatalf("worker saw %d request(s), want at least 2 (the failed attempt and its re-dispatch)", len(reqs))
			}
			if strings.Contains(reqs[0], malformedOutputNote) {
				t.Errorf("the FIRST attempt's prompt already carries the malformed-output note — the note must " +
					"only reach the attempt that follows the fault")
			}
			if !strings.Contains(reqs[len(reqs)-1], malformedOutputNote) {
				t.Errorf("the re-dispatched attempt's prompt does not say the previous attempt ended on %s",
					malformedOutputNote)
			}
		})
	}
}

// BDD: Given a task whose attempt ends on an execution error that is NOT a
// malformed-output fault (a provider auth failure),
// When the goal loop handles that attempt,
// Then the task fails on the spot, consuming no attempt,
// And it is never re-dispatched and never judged.
func TestTaskAttempt_NonRecoverableExecutionError_StillFailsTask(t *testing.T) {
	worker := &attemptScriptedWorker{
		failForever: true,
		failErr: func() error {
			return &providers.FailoverError{
				Reason: providers.FailoverAuth, Provider: "scripted", Model: "test-model", Status: 401,
				Wrapped: errors.New("invalid api key"),
			}
		},
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: `{"met": true, "criteria": []}`}, nil
	}}
	judgeInst.Provider = judge

	maxAttempts := 5
	tk := &task.Task{
		Title: "auth failure task", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForTaskStatus(t, al, tk.ID, task.StatusFailed, 30*time.Second)
	if !strings.HasPrefix(final.Result, "execution error:") {
		t.Errorf("result = %q, want the terminal execution-error outcome — an auth failure is not a "+
			"malformed-output fault and must not enter the attempt loop", final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a non-recoverable execution error consumes no attempt", final.AttemptCount)
	}
	// Give any (wrong) re-dispatch time to reach the worker before asserting
	// that none did.
	time.Sleep(300 * time.Millisecond)
	if n := countCarryingNote(worker.snapshot()); n != 0 {
		t.Errorf("%d request(s) carried the malformed-output note — an auth failure must never be re-dispatched as one", n)
	}
	if got, _ := al.taskStore.Get(tk.ID); got.Status != task.StatusFailed {
		t.Errorf("task status moved to %q after failing — it was re-dispatched", got.Status)
	}
	if got := judge.callCount(); got != 0 {
		t.Errorf("judge ran %d time(s), want 0", got)
	}
}

// BDD: Given a task whose EVERY attempt ends on malformed tool-call output,
// When the attempt budget is spent,
// Then the task fails through the attempt-exhaustion path — never an endless
// re-dispatch — with exactly max_attempts attempts consumed.
func TestTaskAttempt_MalformedToolOutputEveryAttempt_ExhaustsBudget(t *testing.T) {
	worker := &attemptScriptedWorker{failForever: true, failErr: func() error { return typedMalformedToolCall(false) }}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: `{"met": true, "criteria": []}`}, nil
	}}
	judgeInst.Provider = judge

	const maxAttempts = 2
	mx := maxAttempts
	tk := &task.Task{
		Title: "always malformed", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &mx,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := t3WaitForTerminal(t, al, tk.ID, maxAttempts)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q once the budget is spent", final.Status, task.StatusFailed)
	}
	if final.AttemptCount != maxAttempts {
		t.Errorf("attempt_count = %d, want %d — each malformed attempt consumes exactly one", final.AttemptCount, maxAttempts)
	}
	if strings.HasPrefix(final.Result, "execution error:") {
		t.Errorf("result = %q — the task failed on the spot through the execution-error path instead of "+
			"through attempt exhaustion", final.Result)
	}
	time.Sleep(300 * time.Millisecond)
	if got, _ := al.taskStore.Get(tk.ID); got.Status != task.StatusFailed || got.AttemptCount != maxAttempts {
		t.Errorf("after exhaustion the task moved on (status %q, attempt_count %d) — the loop did not stop",
			got.Status, got.AttemptCount)
	}
	if got := judge.callCount(); got != 0 {
		t.Errorf("judge ran %d time(s), want 0 — no attempt made a claim", got)
	}
}

// TestAttemptRecoverableTurnErrorCode_TypedOnly pins the classification rule:
// the error's TYPE decides, never its text, and a Stop always wins.
func TestAttemptRecoverableTurnErrorCode_TypedOnly(t *testing.T) {
	typed := typedMalformedToolCall(false)
	cases := []struct {
		name     string
		err      error
		wantOK   bool
		wantCode LLMErrorCode
	}{
		{"nil", nil, false, ""},
		{"typed_tool_args", typed, true, CodeToolArgs},
		{"typed_tool_call_truncated", typedMalformedToolCall(true), true, CodeToolCallTruncated},
		{"typed_wrapped_as_runTurn_returns_it", fmt.Errorf("LLM call failed after retries: %w", typed), true, CodeToolArgs},
		// Identical wording, no type: TranslateTurnError's substring fallback
		// would call this CodeToolArgs. It must NOT qualify.
		{"same_text_untyped", errors.New(common.ErrToolArgumentsUndecodable.Error()), false, ""},
		{"untyped_orphan_markup_exhaustion_as_runTurn_returns_it_today", errors.New(
			`model emitted unparseable tool-call markup (marker "</arg_value>", finish_reason "stop") after 2 repair attempts`),
			false, ""},
		{"stop_wins_over_malformed_output", fmt.Errorf("%w: %w", ErrTurnCanceled, typed), false, ""},
		{"timeout_wins_over_malformed_output", fmt.Errorf("%w: %w", ErrTurnTimedOut, typed), false, ""},
		{"provider_auth_failure", &providers.FailoverError{
			Reason: providers.FailoverAuth, Status: 401, Wrapped: errors.New("invalid api key"),
		}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := attemptRecoverableTurnErrorCode(tc.err)
			if ok != tc.wantOK || code != tc.wantCode {
				t.Fatalf("attemptRecoverableTurnErrorCode = (%q, %v), want (%q, %v)", code, ok, tc.wantCode, tc.wantOK)
			}
		})
	}
}
