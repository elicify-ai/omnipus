// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_turn_error_test.go pins how a task run treats a try that ENDS
// ON A TURN ERROR (task_attempt_turn_error.go, task_run_loop.go).
//
// The defect (live UAT lane L2, scenario A-12): a task's run ended because the
// model emitted a tool call as unparseable output, and the task went terminal
// `failed` on the spot. The required behaviour comes from planning-goals-spec.md
// FR-045 / US-5 AS-4 ("NOT terminally failed on the spot") under the two-level
// model the founder set on 2026-09-14 (issue #710) — goal TRIES inside a run,
// task ATTEMPTS across runs:
//
//   - a malformed-tool-call-output fault (typed *common.ToolArgumentsError,
//     CodeToolArgs / CodeToolCallTruncated) spends ONE goal try and the worker
//     is re-prompted in the SAME run with a note naming the fault; its next
//     claim is judged normally and no task attempt is used;
//   - an error only an operator can fix (rejected credentials, an unknown
//     provider, no model) ends the task at once with no attempt used — founder
//     decision 2026-09-15, pinned in task_run_operator_fix_test.go;
//   - any other execution error (a rate limit, a provider outage, a timeout)
//     BREAKS the run: the run fails as a whole, which uses one task attempt and
//     restarts the task until its attempt limit;
//   - a fault that repeats on every try spends the run's tries, then the
//     attempts, and the task fails — never an endless loop.
//
// Oracles are the spec's observable outcomes — task status, AttemptCount, how
// many times the Judge ran, and what the next try's request carried — never the
// implementation's own strings.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// malformedOutputNote is the phrase the operator's requirement names for the
// note the next try must carry: "the previous one ended on malformed
// tool-call output".
const malformedOutputNote = "malformed tool-call output"

// cleanWorkerEvidence is the one-line evidence a clean try claims with.
const cleanWorkerEvidence = "ran ./greeting.sh | wc -l -> 1, exit 0"

// cleanClaimTurn answers a clean try the one way a native task worker can
// finish: a goal_claim(met) call, then — once the tool result is back — the
// turn's closing text.
func cleanClaimTurn(msgs []providers.Message) *providers.LLMResponse {
	if n := len(msgs); n > 0 && msgs[n-1].Role == "tool" {
		return &providers.LLMResponse{Content: "greeting.sh now prints exactly one line."}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "call-goal-claim-clean", Type: "function", Name: tools.GoalClaimToolName,
		Arguments: map[string]any{"status": tools.GoalClaimStatusMet, "evidence": cleanWorkerEvidence},
	}}}
}

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
// the note is present (i.e. the run re-prompted with it) the worker claims
// through goal_claim — unless failForever is set. Keying on the note rather
// than on a call count keeps the test independent of how many in-turn repair
// calls runTurn spends before giving up.
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
	return cleanClaimTurn(msgs), nil
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

// waitForTaskStatus polls the store for want. A task whose last run broke
// leaves its session `interrupted` rather than archived, so t3WaitForTerminal's
// archive condition cannot be used for it.
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

func newTurnErrorTask(t *testing.T, al *AgentLoop, title string, maxAttempts int) *task.Task {
	t.Helper()
	mx := maxAttempts
	tk := &task.Task{
		Title: title, Prompt: "fix greeting.sh", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &mx,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "greeting.sh prints exactly one line")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	return tk
}

// BDD: Given a task whose first try ends because the model's tool call could
// not be decoded,
// When the run handles that try,
// Then the task is NOT failed and NO task attempt is used — one goal try is
// spent inside the same run,
// And the worker is re-prompted with a note that the previous try ended on
// malformed tool-call output,
// And its next, clean claim is judged and completes the task.
func TestTaskAttempt_MalformedToolOutput_SpendsOneTryAndContinuesTheRun(t *testing.T) {
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
			judge := &b6ScriptedJudge{metFromCall: 1, reason: "one line, exit 0"}
			judgeInst.Provider = judge

			tk := newTurnErrorTask(t, al, "UAT-J1 build greeting script", 5)

			final := t3WaitForTerminal(t, al, tk.ID, 2)
			if final.Status != task.StatusDone {
				t.Fatalf("status = %q, want %q — a try that ended on malformed tool-call output must not "+
					"fail the task (result: %s)", final.Status, task.StatusDone, final.Result)
			}
			if final.AttemptCount != 0 {
				t.Errorf("attempt_count = %d, want 0 — a malformed-output try spends a goal try inside the run, "+
					"not a task attempt", final.AttemptCount)
			}
			if got := judge.callCount(); got != 1 {
				t.Errorf("judge ran %d time(s), want exactly 1 — the malformed try has no claim to judge; "+
					"the clean claim must be judged normally", got)
			}

			reqs := worker.snapshot()
			if len(reqs) < 2 {
				t.Fatalf("worker saw %d request(s), want at least 2 (the failed try and the re-prompted one)", len(reqs))
			}
			if strings.Contains(reqs[0], malformedOutputNote) {
				t.Errorf("the FIRST try's request already carries the malformed-output note — the note must " +
					"only reach the try that follows the fault")
			}
			if !strings.Contains(reqs[len(reqs)-1], malformedOutputNote) {
				t.Errorf("the re-prompted try's request does not say the previous try ended on %s", malformedOutputNote)
			}
			rec := waitForGoalState(t, tk.ID, generated.GoalStateMet)
			if rec.Round != 2 {
				t.Errorf("goal tries used = %d, want 2 (the malformed try and the upheld claim)", rec.Round)
			}
		})
	}
}

// BDD: Given a task whose run breaks on a temporary execution error that is NOT
// a malformed-output fault (a provider rate limit),
// When the run handles that error,
// Then the run fails as a whole: one task attempt is used and the task
// restarts in a fresh run, until its attempt limit ends it Failed,
// And no try is ever re-prompted as a malformed-output fault, and nothing is
// ever judged.
//
// This test used a provider auth failure until the founder decision of
// 2026-09-15: an error only an operator can fix now ends the task at once with
// no attempt used (task_run_operator_fix_test.go), so a temporary error is what
// still exercises the restart.
func TestTaskAttempt_BrokenRun_UsesOneAttemptPerRunUntilTheLimit(t *testing.T) {
	worker := &attemptScriptedWorker{
		failForever: true,
		failErr: func() error {
			return &providers.FailoverError{
				Reason: providers.FailoverRateLimit, Provider: "scripted", Model: "test-model", Status: 429,
				Wrapped: errors.New("rate limit exceeded"),
			}
		},
	}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "must never be asked"}
	judgeInst.Provider = judge

	const maxAttempts = 2
	tk := newTurnErrorTask(t, al, "rate limited task", maxAttempts)

	final := waitForTaskStatus(t, al, tk.ID, task.StatusFailed, 60*time.Second)
	if final.AttemptCount != maxAttempts {
		t.Errorf("attempt_count = %d, want %d — every broken run is one failed task attempt", final.AttemptCount, maxAttempts)
	}
	if !strings.Contains(final.Result, "execution error:") {
		t.Errorf("result = %q, want the broken run's execution error as the reason", final.Result)
	}
	if !strings.Contains(final.Result, fmt.Sprintf("(max %d)", maxAttempts)) {
		t.Errorf("result = %q, want it to report the attempt limit the task ran under", final.Result)
	}
	// Give any (wrong) further restart time to reach the worker before
	// asserting that none did.
	time.Sleep(300 * time.Millisecond)
	if n := countCarryingNote(worker.snapshot()); n != 0 {
		t.Errorf("%d request(s) carried the malformed-output note — a rate limit must never be retried as one", n)
	}
	if got, _ := al.taskStore.Get(tk.ID); got.Status != task.StatusFailed || got.AttemptCount != maxAttempts {
		t.Errorf("after the limit the task moved on (status %q, attempt_count %d)", got.Status, got.AttemptCount)
	}
	if got := judge.callCount(); got != 0 {
		t.Errorf("judge ran %d time(s), want 0", got)
	}
}

// BDD: Given a task whose EVERY try ends on malformed tool-call output,
// When each run's tries and then the task's attempts are spent,
// Then the task fails through the attempt-limit path — never an endless loop —
// with exactly max_attempts attempts used.
func TestTaskAttempt_MalformedToolOutputEveryTry_ExhaustsTriesThenAttempts(t *testing.T) {
	const triesPerGoal, maxAttempts = 2, 2
	worker := &attemptScriptedWorker{failForever: true, failErr: func() error { return typedMalformedToolCall(false) }}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = triesPerGoal })
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "must never be asked"}
	judgeInst.Provider = judge

	tk := newTurnErrorTask(t, al, "always malformed", maxAttempts)

	final := t3WaitForTerminal(t, al, tk.ID, triesPerGoal*maxAttempts)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q once the tries and attempts are spent", final.Status, task.StatusFailed)
	}
	if final.AttemptCount != maxAttempts {
		t.Errorf("attempt_count = %d, want %d — each run whose tries are all malformed uses one attempt",
			final.AttemptCount, maxAttempts)
	}
	if want := fmt.Sprintf("did not reach a met verdict within %d tries", triesPerGoal); !strings.Contains(final.Result, want) {
		t.Errorf("result = %q, want it to say the goal %s", final.Result, want)
	}
	time.Sleep(300 * time.Millisecond)
	if got, _ := al.taskStore.Get(tk.ID); got.Status != task.StatusFailed || got.AttemptCount != maxAttempts {
		t.Errorf("after the limit the task moved on (status %q, attempt_count %d) — the loop did not stop",
			got.Status, got.AttemptCount)
	}
	if got := judge.callCount(); got != 0 {
		t.Errorf("judge ran %d time(s), want 0 — no try made a claim", got)
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
