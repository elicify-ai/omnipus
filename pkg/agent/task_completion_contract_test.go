// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// End-to-end coverage for ADR-043's task completion contract
// (docs/internal/architecture/ADR-043-task-completion-contract.md): when the
// agent does not call task_update explicitly, TaskExecutor.finishTaskRun must
// resolve completion from the TASK_STATUS/TASK_SUMMARY marker in the agent's
// final output — never default to success on no signal. Covers both dispatch
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
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newNativeTaskCompletionTestLoop builds a real AgentLoop with a single
// native (non-external-CLI) worker agent registered, backed by provider, so a
// task assigned to it exercises the real processTaskDirect -> runAgentLoop ->
// provider.Chat dispatch path and lands in TaskExecutor.finishTaskRun exactly
// as production does.
func newNativeTaskCompletionTestLoop(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: t.TempDir(), ModelName: "test-model"},
			List: []config.AgentConfig{
				{
					ID:        "native-agent",
					Name:      "Native Agent",
					Type:      config.AgentTypeWorker,
					Workspace: workspace,
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
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
func newCompletionContractTask(t *testing.T, al *AgentLoop, agentID, title string) *task.Task {
	t.Helper()
	tk := &task.Task{
		Title:       title,
		Prompt:      "do the task",
		Action:      task.ActionLLM,
		AgentID:     agentID,
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
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
// mid-goroutine (e.g. an explicit task_update tool call sets it Done inside
// iteration 1 of a multi-iteration run) well BEFORE the run's own goroutine
// actually returns; runTask/runTaskFromInProgress only archive the session in
// finishTaskRun, which runs once processTaskDirect itself returns (i.e. after
// EVERY iteration, not just the one that flipped the status). Returning as
// soon as status alone goes terminal would let the test (and its t.Cleanup,
// including al.Close()'s own graceful-shutdown transcript write) race the
// still-in-flight goroutine's own trailing writes to the same session files —
// exactly the class of race Close()'s doc comment warns about for TempDir
// cleanup. Waiting for archival too closes that window because it is the
// LAST write finishTaskRun performs before returning.
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
			// Terminal, but the session archive write (finishTaskRun's last
			// step) hasn't landed yet — keep polling rather than returning
			// early.
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not reach a terminal status within the deadline")
	return nil
}

// TestTaskCompletionContract_Native_SuccessMarker_DoneWithSummary proves the
// native-dispatch happy path: a scripted LLM response ending with
// "TASK_STATUS: success" + "TASK_SUMMARY: ..." (no task_update tool call)
// completes the task to Done with the TASK_SUMMARY text as Result.
func TestTaskCompletionContract_Native_SuccessMarker_DoneWithSummary(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Implemented the feature and ran the tests.\n" +
			"TASK_STATUS: success\n" +
			"TASK_SUMMARY: Added the new export endpoint and its tests.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "native success marker")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	want := "Added the new export endpoint and its tests."
	if final.Result != want {
		t.Errorf("result = %q, want the TASK_SUMMARY text %q", final.Result, want)
	}
}

// TestTaskCompletionContract_Native_FailureMarker_FailedWithAgentWords proves
// that "TASK_STATUS: failure" fails the task with the AGENT's own reported
// words (the TASK_SUMMARY text) as Result — not the "no signal" framing,
// since the agent did report an outcome.
func TestTaskCompletionContract_Native_FailureMarker_FailedWithAgentWords(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Tried to reach the upstream service repeatedly.\n" +
			"TASK_STATUS: failure\n" +
			"TASK_SUMMARY: Could not reach the upstream API (connection timeout).",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "native failure marker")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	want := "Could not reach the upstream API (connection timeout)."
	if final.Result != want {
		t.Errorf("result = %q, want the agent's own TASK_SUMMARY text %q", final.Result, want)
	}
	if strings.Contains(final.Result, "no completion signal") {
		t.Error("an explicit failure marker must not be framed as a missing-signal fail-closed result")
	}
}

// TestTaskCompletionContract_Native_NoMarker_FailsClosed_NotAutoDone proves
// the central regression this feature exists to close: a plain response with
// NO TASK_STATUS marker (and no task_update call) must fail the task closed,
// never silently default to Done — the retired behavior.
func TestTaskCompletionContract_Native_NoMarker_FailsClosed_NotAutoDone(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "I looked into this and made some progress, but didn't finish.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "native no marker")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q — no TASK_STATUS marker must fail closed, never auto-complete "+
			"to done (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	if final.Result == "Task completed" || strings.Contains(final.Result, "Task completed") {
		t.Errorf("result = %q, the retired 'Task completed' auto-complete default must be gone", final.Result)
	}
	if !strings.Contains(final.Result, "completion signal") {
		t.Errorf("result = %q, want it to explain the missing completion signal", final.Result)
	}
	if !strings.Contains(final.Result, "I looked into this and made some progress") {
		t.Errorf("result = %q, want it to include the agent's raw output for operator review", final.Result)
	}
}

// TestTaskCompletionContract_FinishTaskRun_EmptyOutput_FailsClosed proves the
// empty-output edge case (item (c) of the feature spec) directly against
// TaskExecutor.finishTaskRun: a genuinely empty resp must fail the task
// closed, and the retired "Task completed" default must never appear.
//
// This calls finishTaskRun directly rather than through a live dispatch
// because BOTH real dispatch paths substitute a non-empty sentinel before an
// empty LLM/CLI response ever reaches finishTaskRun (runAgentLoop's
// package-level defaultResponse, loop.go; drainExternalRun's "completed with
// no textual output" fallback, external_dispatch.go) — so this is the only
// way to exercise the executor's own fail-closed handling of a truly empty
// string, which the parser and finishTaskRun both explicitly guard for.
func TestTaskCompletionContract_FinishTaskRun_EmptyOutput_FailsClosed(t *testing.T) {
	al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
	tk := newCompletionContractTask(t, al, "native-agent", "empty output task")

	al.taskExecutor.finishTaskRun(tk, "", "", nil, "")

	final, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q — empty output must fail closed, not auto-complete to done "+
			"(result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	if final.Result == "Task completed" || strings.Contains(final.Result, "Task completed") {
		t.Errorf("result = %q, the retired 'Task completed' auto-complete default must be gone", final.Result)
	}
	if !strings.Contains(final.Result, "completion signal") {
		t.Errorf("result = %q, want it to explain the missing completion signal", final.Result)
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
					"TASK_STATUS: failure\n" +
					"TASK_SUMMARY: Blocked by missing write access to the target repo.",
			},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	tk := newCompletionContractTask(t, al, "ext-agent", "external failure marker")
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	want := "Blocked by missing write access to the target repo."
	if final.Result != want {
		t.Errorf("result = %q, want the agent's own TASK_SUMMARY text %q", final.Result, want)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestTaskCompletionContract_External_NoMarker_FailsClosed_NotAutoDone is the
// external-CLI counterpart of the native no-marker test: a clean external-CLI
// exit with prose output but no TASK_STATUS marker must fail closed — this is
// precisely the false-success cascade ADR-042 §3 flagged and ADR-043 closes.
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

	tk := newCompletionContractTask(t, al, "ext-agent", "external no marker")
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
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
	if !strings.Contains(final.Result, "completion signal") {
		t.Errorf("result = %q, want it to explain the missing completion signal", final.Result)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestBuildPrompt_InstructionEchoNeverResolvesToSuccess is the review B2
// regression-proof lock on buildPrompt's echo-safety property (ADR-043
// §2.4 / review B1): if a model paraphrases or echoes buildPrompt's own
// instruction text verbatim as its "final message" — without ever emitting a
// genuine completion signal of its own — parsing that echoed text must NEVER
// resolve to verdictSuccess. At worst it fails closed (verdictNotFound) or
// resolves to verdictFailure (the deliberately "safe direction" per B1's
// ordering of the two TASK_STATUS lines), but never verdictSuccess. Proven
// for both dispatch kinds since buildPrompt's dispatch branch means native
// (marker + task_update instruction) and external-CLI (marker only) produce
// different final strings.
func TestBuildPrompt_InstructionEchoNeverResolvesToSuccess(t *testing.T) {
	t.Run("native", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
		tk := newCompletionContractTask(t, al, "native-agent", "echo-safety native")
		prompt := al.taskExecutor.buildPrompt(tk)
		sig := parseTaskCompletionSignal(prompt)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing buildPrompt's own raw instruction text resolved to verdictSuccess "+
				"(echo-unsafe) — prompt:\n%s", prompt)
		}
	})
	t.Run("external_cli", func(t *testing.T) {
		al, _ := newExternalCLITaskTestLoop(t, &countingProvider{})
		tk := newCompletionContractTask(t, al, "ext-agent", "echo-safety external")
		prompt := al.taskExecutor.buildPrompt(tk)
		sig := parseTaskCompletionSignal(prompt)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing buildPrompt's own raw instruction text resolved to verdictSuccess "+
				"(echo-unsafe) — prompt:\n%s", prompt)
		}
	})
	// bulleted_paraphrase is hardening review finding H2's echo-safety
	// extension: a model doesn't always echo buildPrompt's instruction lines
	// verbatim — a real, observed reformatting is to restate them as a
	// bulleted list (e.g. summarizing its plan as "* TASK_STATUS: success -
	// if all tests pass" / "* TASK_STATUS: failure - otherwise"). Before the
	// bullet-exclusion fix, the SUCCESS bullet's leading "* " was just more
	// whitespace to the wrapper regex, so this paraphrase could resolve to
	// verdictSuccess purely from quoted instruction text. It must now fail
	// closed (verdictNotFound) or resolve to verdictFailure (the parser's
	// existing safe-direction ordering), but never verdictSuccess.
	t.Run("bulleted_paraphrase", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, &mockProvider{})
		tk := newCompletionContractTask(t, al, "native-agent", "echo-safety bulleted paraphrase")
		prompt := al.taskExecutor.buildPrompt(tk)
		bulleted := strings.ReplaceAll(prompt,
			"TASK_STATUS: success", "* `TASK_STATUS: success` - if all tests pass")
		bulleted = strings.ReplaceAll(bulleted,
			"TASK_STATUS: failure", "* `TASK_STATUS: failure` - otherwise")
		sig := parseTaskCompletionSignal(bulleted)
		if sig.Verdict == verdictSuccess {
			t.Fatalf("parsing a bulleted paraphrase of buildPrompt's instruction text resolved to "+
				"verdictSuccess (echo-unsafe) — prompt:\n%s", bulleted)
		}
	})
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
	provider := &scriptedProvider{
		responseBody: "I looked into this and made some progress, but didn't finish.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)

	blocker := newCompletionContractTask(t, al, "native-agent", "blocker task (no marker)")

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

	if err := al.taskExecutor.ExecuteTask(context.Background(), blocker.ID); err != nil {
		t.Fatalf("ExecuteTask(blocker): %v", err)
	}

	finalBlocker := waitForCompletionContractTerminal(t, al, blocker.ID)
	if finalBlocker.Status != task.StatusFailed {
		t.Fatalf("blocker status = %q, want %q — no TASK_STATUS marker must fail closed",
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

// TestTaskCompletionContract_TaskUpdatePrecedence_WinsOverMarkerlessResponse
// is review E2: the agent calls the REAL update_task tool (pkg/tools/task.go
// TaskUpdateTool.Name() == "update_task") to set a terminal status mid-run,
// then returns a marker-less final response with no TASK_STATUS line at all.
// finishTaskRun's own precedence check (re-read task, task.IsTerminal) must
// find the task ALREADY terminal from the tool call and return before ever
// calling parseTaskCompletionSignal — the explicit status/result set by the
// tool call must be preserved verbatim, not overwritten or reinterpreted by
// the (absent) marker branch.
func TestTaskCompletionContract_TaskUpdatePrecedence_WinsOverMarkerlessResponse(t *testing.T) {
	// tk.ID must be known before scripting the tool call's arguments, so the
	// task is created first via a throwaway loop construction, then the real
	// scripted provider (which needs tk.ID baked into its first response) is
	// used to build the real loop. Simpler: create the loop once, create the
	// task, THEN build the scripted provider referencing tk.ID, and register
	// it — newNativeTaskCompletionTestLoop takes the provider at construction
	// time, so we build the task ID deterministically ourselves instead of
	// relying on Store.Create's UUID generation.
	provider := newScriptedProvider() // empty; real responses patched in below
	al := newNativeTaskCompletionTestLoop(t, provider)

	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found in registry")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): update_task needs
	// an explicit agent-level grant or it fails closed to "deny" before the
	// tool call under test ever executes.
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"update_task": "allow"},
	})

	tk := newCompletionContractTask(t, al, "native-agent", "task_update precedence")

	provider.responses = []*providers.LLMResponse{
		{
			ToolCalls: []providers.ToolCall{{
				ID:   "call-update-task",
				Type: "function",
				Name: "update_task",
				Arguments: map[string]any{
					"task_id": tk.ID,
					"status":  "done",
					"result":  "Done via explicit update_task call.",
				},
			}},
		},
		{
			// Marker-less final response — no TASK_STATUS line anywhere. If
			// the marker branch engaged at all, this response has no signal
			// and would fail the task closed; the explicit tool call above
			// must win outright before that branch is ever reached.
			Content: "Wrapping up now, nothing further to report.",
		},
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want %q — the explicit update_task call must win (result: %s)",
			final.Status, task.StatusDone, final.Result)
	}
	want := "Done via explicit update_task call."
	if final.Result != want {
		t.Errorf("result = %q, want the tool call's own result %q — the marker-less final response "+
			"must never overwrite it", final.Result, want)
	}
}

// TestTaskCompletionContract_SessionArchivedOnCompletion is review E3: after
// completeTaskWithResult runs (any terminal outcome), the task's own session
// must be archived (session.StatusArchived) — not left active/interrupted.
func TestTaskCompletionContract_SessionArchivedOnCompletion(t *testing.T) {
	provider := &scriptedProvider{
		responseBody: "Implemented the feature and ran the tests.\n" +
			"TASK_STATUS: success\n" +
			"TASK_SUMMARY: Added the new export endpoint and its tests.",
	}
	al := newNativeTaskCompletionTestLoop(t, provider)
	tk := newCompletionContractTask(t, al, "native-agent", "session archival check")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID); err != nil {
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
