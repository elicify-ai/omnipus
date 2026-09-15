// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// End-to-end coverage for ADR-043's task completion contract
// (docs/internal/architecture/ADR-043-task-completion-contract.md): a task
// completes only through a claim the Judge upholds — goal_claim for a native
// worker, the evidence line and TASK_STATUS marker for an external CLI worker
// (ADR-043 §8) — and never defaults to success on no signal. Covers both dispatch
// kinds (native via processTaskDirect/a scripted LLM provider, and external
// via the fake driver task_executor_external_cli_test.go already exercises)
// so the contract is proven uniform across both, per the feature spec.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// judgeAgentConfigForTaskTests is the Judge System Agent entry every task
// dispatch harness in this package must register.
//
// GOAL-FR-022/FR-023 (plan row R-27) deleted the last trust-the-claim branch
// from the task claim adjudication (now task_run_loop.go::adjudicateRunClaim):
// when the SOFT tier applied (a task with no explicit Criteria, judged against
// judge.go::SoftTierCriterion) and the Judge agent was absent from the
// registry, the task used to be completed on the worker's own say-so. It now
// ends Failed "The Judge could not run: …", with no attempt and no round used
// (founder decision 2026-09-14).
//
// Every harness in this file creates criteria-less tasks, so every one of
// them took that soft tier. They were green ONLY because of the deleted
// branch: the claim was never judged at all. Registering a real Judge is what
// makes them prove what they say they prove — a task completes BECAUSE a
// verdict came back met.
func judgeAgentConfigForTaskTests(t *testing.T) config.AgentConfig {
	t.Helper()
	return config.AgentConfig{
		ID:   string(coreagent.IDJudge),
		Name: "Judge",
		Type: config.AgentTypeSystem,
		Home: t.TempDir(),
	}
}

// bindMetSoftTierJudge binds a canned MET-verdict provider to al's Judge
// agent and returns it (so a caller can assert it was really dispatched).
//
// Registering the Judge agent alone is NECESSARY BUT NOT SUFFICIENT: an
// AgentInstance built from config inherits the loop's shared worker provider,
// which in these harnesses is a scriptedProvider replaying the WORKER's task
// text. Fed to the verifier that parses as no judgment at all, every
// criterion comes back unjudgeable/unmet, and the claim spends a goal try
// instead of reaching `done` — the same red, for a different
// reason. The Judge needs its own provider that actually answers a verdict.
//
// The canned verdict answers for every criterion id the verifier is asked
// about — the soft-tier criterion ("soft-tier-implicit", judge.go) and the
// floor Definition of Done a legacy task's minted goal record carries — because
// a verdict that omitted one would be classified criterion_unjudgeable
// (JUDGE-FR-138) and resolve unmet.
func bindMetSoftTierJudge(t *testing.T, al *AgentLoop) *b6ScriptedJudge {
	t.Helper()
	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok {
		t.Fatalf("the Judge System Agent (%s) is not registered — a task claim could never be judged",
			coreagent.IDJudge)
	}
	// Answers met for every criterion the verifier is actually asked about —
	// the soft-tier criterion AND the floor Definition of Done a legacy task's
	// minted goal record carries.
	fake := &b6ScriptedJudge{metFromCall: 1, reason: "the claim's evidence satisfies the criterion"}
	judgeInst.Provider = fake
	return fake
}

// newNativeTaskCompletionTestLoop builds a real AgentLoop with a single
// native (non-external-CLI) worker agent registered, backed by provider, so a
// task assigned to it exercises the real processTaskDirect -> runAgentLoop ->
// provider.Chat dispatch path and lands in the task run loop (task_run_loop.go)
// exactly as production does.
func newNativeTaskCompletionTestLoop(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{
					ID:   "native-agent",
					Name: "Native Agent",
					Type: config.AgentTypeWorker,
					Home: workspace,
				},
				// GOAL-FR-022/R-27: a success claim from this worker is a
				// REQUEST to be judged, never a completion — without a
				// registered Judge the claim can no longer complete anything.
				// See judgeAgentConfigForTaskTests' doc comment.
				judgeAgentConfigForTaskTests(t),
			},
		},
	}
	// Production seeds goal_claim "allow" for every agent (pkg/config/defaults.go);
	// a task worker can only finish by calling it (founder decision 2026-09-14).
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	bindMetSoftTierJudge(t, al)
	// al.Close() drains session workers/recaps before t.TempDir()'s own
	// cleanup runs, so a real tool-call test (e.g. one that exercises
	// update_task's session-worker writes) can't race an async write against
	// TempDir's RemoveAll — see Close()'s own doc comment (loop.go) for why
	// this ordering matters.
	t.Cleanup(func() { al.Close() })
	return al
}

// newCompletionContractTask creates and stores a dispatchable `next` task for
// agentID via al's own task store, mirroring what the REST/board layer does
// before calling TaskExecutor.ExecuteTask.
// The task pins max_attempts=3 explicitly. ADR-086 GOAL-FR-024/FR-026 (plan
// row R-03, operator decision D10) raised config.DefaultTaskMaxAttempts from
// 3 to 20 so one budget number governs both owner kinds. That is a deliberate
// PRODUCT change, but it silently rewrote the runtime of every fail-closed
// test built on this fixture: an unmet/no-signal outcome re-dispatches once
// per attempt, so exhaustion went from ~3 dispatches (~2s) to ~20 (~12s) and
// blew the 5s polling deadline in waitForCompletionContractTerminal. Pinning
// the old number keeps these tests exercising the exhaustion path they are
// about — the ceiling's VALUE is pkg/config's own concern (its
// planning_test.go covers it), not this contract's. Same technique, same
// reason as TestTaskCompletionContract_External_NoMarker_FailsClosed_
// NotAutoDone's own explicit max_attempts pin, which still overrides this.
func newCompletionContractTask(t *testing.T, al *AgentLoop, agentID, title string) *task.Task {
	t.Helper()
	maxAttempts := 3
	tk := &task.Task{
		Title:       title,
		Prompt:      "do the task",
		Action:      task.ActionLLM,
		AgentID:     agentID,
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		MaxAttempts: &maxAttempts,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk
}

// waitForCompletionContractTerminal polls al's task store until taskID
// reaches a terminal status (done/failed) or the deadline elapses.
//
// It ALSO waits for the task's own session to be archived (when one exists)
// before returning — not just terminal task status. A task can go terminal
// mid-goroutine (e.g. a Stop lands during iteration 1 of a multi-iteration
// run) well BEFORE the run's own goroutine
// actually returns; runTask/runTaskFromInProgress only archive the session
// when the run loop ends the task, once processTaskDirect itself returns (i.e. after
// EVERY iteration, not just the one that flipped the status). Returning as
// soon as status alone goes terminal would let the test (and its t.Cleanup,
// including al.Close()'s own graceful-shutdown transcript write) race the
// still-in-flight goroutine's own trailing writes to the same session files —
// exactly the class of race Close()'s doc comment warns about for TempDir
// cleanup. Waiting for archival too closes that window because it is the
// LAST write completeTaskWithResult performs before returning.
func waitForCompletionContractTerminal(t *testing.T, al *AgentLoop, taskID string) *task.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.IsTerminal(got.Status) {
			if got.SessionID == "" {
				return got
			}
			sessStore := al.GetAgentStore(got.AgentID)
			if sessStore == nil {
				return got
			}
			if meta, merr := sessStore.GetMeta(got.SessionID); merr == nil && meta.Status == session.StatusArchived {
				return got
			}
			// Terminal, but the session archive write hasn't landed yet —
			// keep polling rather than returning early.
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not reach a terminal status within the deadline")
	return nil
}

// TestTaskCompletionContract_Native_ClaimMet_DoneWithEvidence: a native worker
// completes a task ONLY by calling goal_claim (founder decision 2026-09-14). The
// Judge checks the claim, and a met verdict completes the task with the
// worker's own evidence line as its result.
func TestTaskCompletionContract_Native_ClaimMet_DoneWithEvidence(t *testing.T) {
	const evidence = "ran the test suite, all green"
	al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnClaimMet(evidence)))
	judge := bindMetSoftTierJudge(t, al)
	tk := newCompletionContractTask(t, al, "native-agent", "native claim met")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if judge.callCount() != 1 {
		t.Errorf("Judge calls = %d, want 1 — the task must reach done through a judged claim", judge.callCount())
	}
	if final.Result != evidence {
		t.Errorf("result = %q, want the claim's evidence %q", final.Result, evidence)
	}
}

// TestTaskCompletionContract_Native_Blocked_FailedWithReason: a worker that
// honestly cannot proceed calls goal_claim(status:"blocked"). The task ends
// Failed with "Blocked: <reason>" — no attempt used, no Judge run, no restart.
func TestTaskCompletionContract_Native_Blocked_FailedWithReason(t *testing.T) {
	const reason = "the upstream API refuses every connection"
	worker := newClaimingWorker(turnClaimBlocked(reason))
	al := newNativeTaskCompletionTestLoop(t, worker)
	judge := bindMetSoftTierJudge(t, al)
	tk := newCompletionContractTask(t, al, "native-agent", "native blocked claim")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	if want := "Blocked: " + reason; final.Result != want {
		t.Errorf("result = %q, want %q", final.Result, want)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — blocked is not a failed run", final.AttemptCount)
	}
	if judge.callCount() != 0 {
		t.Errorf("Judge calls = %d, want 0 — a blocked claim is never judged", judge.callCount())
	}
	if n := worker.turnsStarted(); n != 1 {
		t.Errorf("worker turns = %d, want 1 — a blocked task is never restarted", n)
	}
}

// TestTaskCompletionContract_Native_NoClaim_FailsClosed_NotAutoDone: a worker
// that never claims can never complete its task. Each claimless turn spends a
// goal try; with the tries and the attempts spent, the task ends Failed.
func TestTaskCompletionContract_Native_NoClaim_FailsClosed_NotAutoDone(t *testing.T) {
	al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnNoClaim("I made some progress but did not finish.")))
	if err := al.MutateConfig(func(cfg *config.Config) error { cfg.Planning.GoalMaxRounds = 1; return nil }); err != nil {
		t.Fatalf("set the goal try limit: %v", err)
	}
	tk := newCompletionContractTask(t, al, "native-agent", "native no claim")
	one := 1
	onePtr := &one
	if _, err := al.taskStore.Update(tk.ID, task.Patch{MaxAttempts: &onePtr}); err != nil {
		t.Fatalf("pin max_attempts=1: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q — a worker that never claims must never complete (result: %s)",
			final.Status, task.StatusFailed, final.Result)
	}
	if strings.Contains(final.Result, "Task completed") {
		t.Errorf("result = %q, the retired 'Task completed' default must be gone", final.Result)
	}
	if !strings.Contains(final.Result, "did not reach a met verdict") {
		t.Errorf("result = %q, want it to say the goal did not reach a met verdict", final.Result)
	}
}

// TestTaskCompletionContract_External_FailureMarker_FailedWithAgentWords is
// the external-CLI (subagent_3p) counterpart of the native failure-marker
// test: the fake driver's aggregated output carries "TASK_STATUS: failure" +
// a TASK_SUMMARY, and the task fails with the agent's own words as Result.
func TestTaskCompletionContract_External_FailureMarker_FailedWithAgentWords(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindOutput,
			Output: &runner.OutputEvent{
				Text: "Ran into a permissions error partway through.\n" +
					// ADR-052 FR-035: evidence-marker gate applies to failure
					// markers too — see the native-dispatch counterpart above.
					"[goal:evidence] attempted the write, got a permissions error\n" +
					"TASK_STATUS: failure\n" +
					"TASK_SUMMARY: Blocked by missing write access to the target repo.",
			},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	tk := newCompletionContractTask(t, al, "ext-agent", "external failure marker")
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	// An external CLI's failure marker is its blocked claim (the same claim
	// path goal_claim feeds): Failed with the worker's own words, no attempt.
	want := "Blocked: Blocked by missing write access to the target repo."
	if final.Result != want {
		t.Errorf("result = %q, want %q", final.Result, want)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a blocked claim is not a failed run", final.AttemptCount)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestTaskCompletionContract_External_NoMarker_FailsClosed_NotAutoDone is the
// external-CLI counterpart of the native no-marker test: a clean external-CLI
// exit with prose output but no TASK_STATUS marker must fail closed — this is
// precisely the false-success cascade ADR-042 §3 flagged and ADR-043 closes.
// The fake driver's goroutine below injects its single scripted event
// sequence exactly ONCE (it calls fr.Cancel() right after), so this test
// pins max_attempts=1 on the task — under the ADR-049 goal loop, a no-signal
// outcome now consumes an attempt and re-dispatches (FR-045) rather than
// failing on the spot; with the ceiling at 1 it still exhausts (and thus
// fails closed) after exactly the one dispatch this fake driver can serve.
func TestTaskCompletionContract_External_NoMarker_FailsClosed_NotAutoDone(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind:   runner.EventKindOutput,
			Output: &runner.OutputEvent{Text: "I did some work but never signaled completion."},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	if err := al.MutateConfig(func(cfg *config.Config) error { cfg.Planning.GoalMaxRounds = 1; return nil }); err != nil {
		t.Fatalf("set the goal try limit: %v", err)
	}
	tk := newCompletionContractTask(t, al, "ext-agent", "external no marker")
	one := 1
	onePtr := &one
	if _, err := al.taskStore.Update(tk.ID, task.Patch{MaxAttempts: &onePtr}); err != nil {
		t.Fatalf("pin max_attempts=1: %v", err)
	}
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q — an external-CLI run with no TASK_STATUS marker must fail "+
			"closed, never auto-complete to done (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	if strings.Contains(final.Result, "Task completed") {
		t.Error("result must not contain the retired 'Task completed' auto-complete default")
	}
	if !strings.Contains(final.Result, "did not reach a met verdict") {
		t.Errorf("result = %q, want it to say the goal did not reach a met verdict", final.Result)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestTaskCompletionContract_BlockedDependent_StaysBlockedOnFailedBlocker is
// review E1 — the headline regression ADR-043 exists to close: task B is
// blocked on task A; A's run ends with NO TASK_STATUS marker (fails closed to
// StatusFailed, not Done). onTaskComplete only auto-advances a blocked
// dependent when the just-completed blocker reached StatusDone (task_executor.go
// onTaskComplete's own gate) — so B must remain exactly as created
// (StatusBlocked), never advanced to next/dispatched, proving a false "Done"
// can no longer silently cascade into a dependent that assumed real work was
// done.
func TestTaskCompletionContract_BlockedDependent_StaysBlockedOnFailedBlocker(t *testing.T) {
	al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnClaimBlocked("the service is down")))

	blocker := newCompletionContractTask(t, al, "native-agent", "blocker task (blocked)")

	dependent := &task.Task{
		Title:       "dependent task",
		Prompt:      "do the dependent work",
		Action:      task.ActionLLM,
		AgentID:     "native-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusBlocked,
		BlockedBy:   []string{blocker.ID},
	}
	if err := al.taskStore.Create(dependent); err != nil {
		t.Fatalf("create dependent task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), blocker.ID, nil); err != nil {
		t.Fatalf("ExecuteTask(blocker): %v", err)
	}

	finalBlocker := waitForCompletionContractTerminal(t, al, blocker.ID)
	if finalBlocker.Status != task.StatusFailed {
		t.Fatalf("blocker status = %q, want %q — a blocked claim ends the task Failed",
			finalBlocker.Status, task.StatusFailed)
	}

	finalDependent, err := al.taskStore.Get(dependent.ID)
	if err != nil {
		t.Fatalf("get dependent task: %v", err)
	}
	if finalDependent.Status != task.StatusBlocked {
		t.Errorf("dependent status = %q, want %q — a failed (not done) blocker must never advance "+
			"a dependent task (this is the cascade ADR-043 exists to prevent)",
			finalDependent.Status, task.StatusBlocked)
	}
}

// TestTaskCompletionContract_UpdateTaskOnOwnRun_RefusedThenClaimJudged: a
// worker that tries to mark its own running task done with update_task is
// refused (founder decision 2026-09-14); the task completes only when its next
// turn claims with goal_claim and the Judge upholds the claim.
func TestTaskCompletionContract_UpdateTaskOnOwnRun_RefusedThenClaimJudged(t *testing.T) {
	provider := newScriptedProvider() // responses patched in once tk.ID is known
	al := newNativeTaskCompletionTestLoop(t, provider)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found in registry")
	}
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"update_task": "allow", "goal_claim": "allow"},
	})
	judge := bindMetSoftTierJudge(t, al)
	tk := newCompletionContractTask(t, al, "native-agent", "update_task refused in run")

	const evidence = "re-ran the export and compared the output by hand"
	provider.responses = []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{
			ID: "call-update-task", Type: "function", Name: "update_task",
			Arguments: map[string]any{"task_id": tk.ID, "status": "done", "result": "Done via update_task."},
		}}},
		{Content: "Marked it done."},
		{ToolCalls: []providers.ToolCall{{
			ID: "call-goal-claim", Type: "function", Name: tools.GoalClaimToolName,
			Arguments: map[string]any{"status": "met", "evidence": evidence},
		}}},
		{Content: "Claimed."},
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if final.Result != evidence {
		t.Errorf("result = %q, want the judged claim's evidence %q — the refused update_task must not have written it",
			final.Result, evidence)
	}
	if judge.callCount() != 1 {
		t.Errorf("Judge calls = %d, want 1", judge.callCount())
	}
}

// TestTaskCompletionContract_SessionArchivedOnCompletion is review E3: after
// completeTaskWithResult runs (any terminal outcome), the task's own session
// must be archived (session.StatusArchived) — not left active/interrupted.
func TestTaskCompletionContract_SessionArchivedOnCompletion(t *testing.T) {
	al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnClaimMet("ran the test suite, all green")))
	tk := newCompletionContractTask(t, al, "native-agent", "session archival check")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.SessionID == "" {
		t.Fatal("final task has no SessionID — cannot verify session archival")
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("GetAgentStore(\"native-agent\") returned nil")
	}
	meta, err := sessStore.GetMeta(final.SessionID)
	if err != nil {
		t.Fatalf("GetMeta(%q): %v", final.SessionID, err)
	}
	if meta.Status != session.StatusArchived {
		t.Errorf("session Status = %q, want %q — completeTaskWithResult must archive the task's session",
			meta.Status, session.StatusArchived)
	}
}
