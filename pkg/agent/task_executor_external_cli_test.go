// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newExternalCLITaskTestLoop builds a real AgentLoop (registry + task store)
// with a single subagent_3p (external-CLI) worker agent registered, so a task
// assigned to it exercises the real registry.GetAgent lookup + executorConfigOf
// / runner.ResolveDispatch path processTaskDirect uses in production.
func newExternalCLITaskTestLoop(t *testing.T, provider providers.LLMProvider) (al *AgentLoop, workspace string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace = t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{
					ID:   "ext-agent",
					Name: "External Agent",
					Type: config.AgentTypeWorker,
					Home: workspace,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
				// GOAL-FR-022/R-27: an external-CLI worker's TASK_STATUS
				// success marker is adjudicated exactly like a native one —
				// see judgeAgentConfigForTaskTests (task_completion_contract_
				// test.go) for why a registered Judge is now mandatory for any
				// harness whose task can reach a completion claim.
				judgeAgentConfigForTaskTests(t),
			},
		},
	}
	// Production seeds goal_claim "allow" for every agent (pkg/config/defaults.go);
	// a task worker can only finish by calling it (founder decision 2026-09-14).
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	bindMetSoftTierJudge(t, al)
	// See newNativeTaskCompletionTestLoop's identical Close() cleanup (same
	// rationale: drain session workers/recaps before t.TempDir() cleanup runs).
	t.Cleanup(func() { al.Close() })
	return al, workspace
}

// TestTaskExecutor_ExternalCLIWorker_CompletesViaStatusMarker proves the full
// end-to-end path for a subagent_3p worker: TaskExecutor.ExecuteTask dispatches
// the task, it runs via runExternalCLISubTurn (fake driver) instead of the
// native engine, and — because an external CLI cannot call Omnipus tools — its
// evidence line plus TASK_STATUS marker (ADR-043) is read as its claim and fed
// into the SAME claim path goal_claim feeds (founder decision 2026-09-14): the
// Judge checks it, and the upheld claim completes the task with the worker's
// own evidence line as its result, exactly as for a native worker.
func TestTaskExecutor_ExternalCLIWorker_CompletesViaStatusMarker(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindOutput,
			// ADR-052 FR-035: an external CLI's success marker is a claim only
			// with a "[goal:evidence] ..." line immediately before it — without
			// it the bare marker spends a goal try and the worker is re-prompted.
			Output: &runner.OutputEvent{
				Text: "task finished by external CLI\n[goal:evidence] confirmed the external run completed\nTASK_STATUS: success",
			},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the external task",
		Action:      task.ActionLLM,
		AgentID:     "ext-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitTaskTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("task status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if final.Result != "confirmed the external run completed" {
		t.Errorf("task result = %q, want the upheld claim's evidence line", final.Result)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestTaskExecutor_ExternalCLIWorker_FatalError_TaskFails (T1, pr-test-analyzer)
// proves the failure path through the TASK entry point (TaskExecutor.ExecuteTask):
// a fatal EventKindError from the external CLI driver breaks the run, which
// fails the run as a whole (founder decision 2026-09-14: a broken run is an
// OUTER attempt failure). With a task attempt limit of 1 there is no restart:
// the task lands in task.StatusFailed — not merely "not done", and not
// auto-completed — with a Result an operator can actually read.
func TestTaskExecutor_ExternalCLIWorker_FatalError_TaskFails(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err:  &runner.ErrorEvent{Message: "external CLI crashed", Fatal: true},
		})
		fr.Cancel() // closes the event channel so the dispatcher/drain loop ends
	}()

	one := 1
	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the failing task",
		Action:      task.ActionLLM,
		AgentID:     "ext-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		MaxAttempts: &one,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitTaskTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("task status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	if strings.TrimSpace(final.Result) == "" {
		t.Error("task Result is empty — a failed task must carry a usable error message")
	}
	// ADR-051 §RD5: the raw driver stderr ("external CLI crashed") is now
	// sanitized through TranslateLLMError before reaching the task result.
	// The result MUST carry the generic failure copy, NOT the raw text.
	if strings.Contains(final.Result, "external CLI crashed") {
		t.Errorf("task Result = %q must NOT contain the raw driver stderr (ADR-051 sanitization)",
			final.Result)
	}
	if !strings.Contains(final.Result, "external-cli run failed") {
		t.Errorf("task Result = %q, want it to mention the generic failure wrapper",
			final.Result)
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 — a broken run is one failed task attempt", final.AttemptCount)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// asyncTaskPollTimeout bounds how long these tests wait for an ASYNCHRONOUS
// runTask goroutine to drive its side effects (terminal task status, transcript
// entries, run-lock release) to completion. It is deliberately generous: the
// assertions verify that the effect DOES happen, not that it happens within any
// particular wall-clock budget. The ci go-test gate runs packages in parallel
// (-p 4) on 2–4 shared cores, so a fixed few-second deadline starves under
// cross-package CPU contention and flakes (the work completes, just later — the
// root cause of the CompletesViaStatusMarker flake seen 2026-07-17). The package
// -timeout is the real hang backstop; a genuinely stuck task still fails here,
// only later and with a clear message rather than an intermittent red.
const asyncTaskPollTimeout = 30 * time.Second

// waitTaskTerminal polls the task store until taskID reaches a terminal status,
// returning the terminal task. It fatals if the task never becomes terminal
// within asyncTaskPollTimeout (a genuine hang, not contention — see the const's
// doc). Callers assert on the returned task's Status/Result.
func waitTaskTerminal(t *testing.T, al *AgentLoop, taskID string) *task.Task {
	t.Helper()
	deadline := time.Now().Add(asyncTaskPollTimeout)
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.IsTerminal(got.Status) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal status within %s", taskID, asyncTaskPollTimeout)
	return nil
}

// waitTaskRunGoroutineDone blocks until the runTask goroutine for taskID has
// fully finished. runTask defers delete(te.running, id) (task_executor.go), which
// runs LAST — after the run loop's late task-store/transcript writes — so once
// te.running no longer holds the id, no further writes from that goroutine can
// race a test's t.TempDir() cleanup.
func waitTaskRunGoroutineDone(t *testing.T, te *TaskExecutor, taskID string) {
	t.Helper()
	deadline := time.Now().Add(asyncTaskPollTimeout)
	for time.Now().Before(deadline) {
		te.mu.Lock()
		_, running := te.running[taskID]
		te.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runTask goroutine did not finish within %s", asyncTaskPollTimeout)
}
