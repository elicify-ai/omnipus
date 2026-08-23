// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
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
			},
		},
	}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	// See newNativeTaskCompletionTestLoop's identical Close() cleanup (same
	// rationale: drain session workers/recaps before t.TempDir() cleanup runs).
	t.Cleanup(func() { al.Close() })
	return al, workspace
}

// TestProcessTaskDirect_ExternalCLIWorker_DispatchesViaExternalCLI proves Fix
// C's dispatch branch directly: a task assigned to a subagent_3p
// (external-CLI) worker routes through runner.ResolveDispatch /
// runExternalCLISubTurn, NOT the native runAgentLoop/runTurn path — the
// native LLM provider is never called, and the returned string is the
// external CLI's own aggregated output (mirrors the agent-to-agent delegation
// dispatch decision in subturn.go's spawnSubTurn).
//
// FIX 2 (7-reviewer gate, persona dropped): a SOUL.md fixture is written into
// the agent's workspace so this test actually exercises composeDelegateInput
// producing a non-trivial composed prompt (an empty soul would make the
// composed and bare-prompt forms indistinguishable) — proving the target
// agent's own persona now travels with a TASK-mode dispatch, not just an
// agent-to-agent delegate call.
func TestProcessTaskDirect_ExternalCLIWorker_DispatchesViaExternalCLI(t *testing.T) {
	provider := &countingProvider{}
	al, workspace := newExternalCLITaskTestLoop(t, provider)

	const soul = "You are External Agent, a specialist worker."
	if err := os.WriteFile(filepath.Join(workspace, "SOUL.md"), []byte(soul), 0o600); err != nil {
		t.Fatalf("write SOUL.md fixture: %v", err)
	}

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind:   runner.EventKindOutput,
			Output: &runner.OutputEvent{Text: "external CLI result"},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel() // closes the event channel so the dispatcher/drain loop ends
	}()

	result, err := al.processTaskDirect(context.Background(), "ext-agent", "do the task", "task-sess-1", "task:1")
	if err != nil {
		t.Fatalf("processTaskDirect: %v", err)
	}
	if result != "external CLI result" {
		t.Fatalf("result = %q, want %q", result, "external CLI result")
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0 — task must not run on the native engine",
			provider.calls)
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	// FIX 2: the driver input is now the COMPOSED (soul, task) form — not the
	// bare prompt — matching composeDelegateInput's "## System\n\n...\n\n##
	// Task\n\n..." shape (subturn.go).
	wantInput := "## System\n\n" + soul + "\n\n## Task\n\ndo the task"
	if opts[0].Input != wantInput {
		t.Errorf("driver input = %q, want %q", opts[0].Input, wantInput)
	}
	// ADR-046 P1 (FR-007/008): WorkDir is always the resolved workspace's
	// work/ dir now, not agent.Home — newExternalCLITaskTestLoop's
	// mustNewAgentLoop call auto-seeds "ext-agent" into the shared
	// test-harness workspace (test_helpers_test.go), so resolve the SAME way
	// runExternalCLISubTurn itself does rather than hardcoding that
	// workspace's id here. SOUL.md above is still read from agent.Home
	// (workspace) — persona resolution is a separate, unaffected mechanism
	// from working-directory resolution.
	wantWorkDir, wsErr := resolveTurnWorkDirOrRefuse(context.Background(), "ext-agent", workspace, "")
	if wsErr != nil {
		t.Fatalf("resolveTurnWorkDirOrRefuse: %v", wsErr)
	}
	if opts[0].WorkDir != wantWorkDir {
		t.Errorf("driver WorkDir = %q, want the resolved workspace's work/ dir %q", opts[0].WorkDir, wantWorkDir)
	}
}

// TestProcessTaskDirect_ExternalCLIWorker_NoSoul_ComposesTaskOnly proves the
// worker case (no SOUL.md, no compiled coreagent prompt): composeDelegateInput
// returns the bare task prompt, unchanged, exactly as before FIX 2 — an empty
// soul must never inject the composed wrapper or a legacy "You are a
// subagent" string.
func TestProcessTaskDirect_ExternalCLIWorker_NoSoul_ComposesTaskOnly(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	_, err := al.processTaskDirect(context.Background(), "ext-agent", "do the task", "task-sess-nosoul", "task:nosoul")
	if err != nil {
		t.Fatalf("processTaskDirect: %v", err)
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Input != "do the task" {
		t.Errorf("driver input = %q, want bare task prompt %q (no soul configured)", opts[0].Input, "do the task")
	}
}

// TestTaskExecutor_ExternalCLIWorker_CompletesViaStatusMarker (renamed from
// ...AutoCompletesTaskViaExternalCLI — review D4: the old name described the
// retired ADR-042 §3 auto-complete-to-Done default; this test now proves the
// ADR-043 marker contract instead) proves the full end-to-end path:
// TaskExecutor.ExecuteTask dispatches a task assigned to a subagent_3p
// worker, which runs via runExternalCLISubTurn (fake driver) instead of the
// native engine, and — since an external-CLI worker's tool registry is its
// own CLI's and has no task_update tool wired to Omnipus at all —
// TaskExecutor.finishTaskRun takes the "agent did not call task_update"
// branch and completes the task from the standardized TASK_STATUS completion
// marker (ADR-043) the fake driver's output carries, using the aggregated CLI
// output (marker included, no TASK_SUMMARY here) as the result.
func TestTaskExecutor_ExternalCLIWorker_CompletesViaStatusMarker(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindOutput,
			// ADR-052 FR-035: the evidence-marker gate (task_executor.go's
			// finishTaskRun, ahead of parseTaskCompletionSignal) requires a
			// "[goal:evidence] ..." line immediately before TASK_STATUS —
			// without it this bare claim would be re-prompted instead of
			// completing the task from the marker.
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
	if !strings.Contains(final.Result, "task finished by external CLI") {
		t.Errorf("task result = %q, want it to contain the external CLI output", final.Result)
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestTaskExecutor_ExternalCLIWorker_FatalError_TaskFails (T1, pr-test-analyzer)
// proves the failure path through the TASK entry point (TaskExecutor.ExecuteTask):
// a fatal EventKindError from the external CLI driver must land the task in
// task.StatusFailed — not merely "not done", and not silently auto-completed
// to Done the way the happy path does — with a Result an operator can actually
// read (the underlying driver error message), mirroring finishTaskRun's error
// branch (te.failTask + Result: "execution error: ...").
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

	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the failing task",
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
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestProcessTaskDirect_ExternalCLIWorker_Timeout_TaskFailsWithoutHanging (T2,
// pr-test-analyzer) proves a driver that never emits an end/error event does
// not hang the task's dispatch forever.
//
// This test surfaced an additional gap beyond the 11 numbered fixes:
// runExternalCLISubTurn never bounded its own ctx with a deadline for the
// TASK path — it derived runCtx via plain context.WithCancel(ctx)
// (external_dispatch.go) and only forwarded rtCfg.defaultTimeout to the
// DRIVER as a RunOptions.TimeoutSeconds hint, which only the REAL drivers
// honor internally (each wraps its own runCtx in context.WithTimeout) — the
// FakeRunner test double does not. spawnSubTurn's native delegation path
// already had its own Go-level safety-net timeout for exactly this reason
// (subturn.go ~458-473); processTaskDirectExternalCLI now has the equivalent
// (loop.go, immediately after `rtCfg := al.getSubTurnConfig()`): a
// `context.WithTimeout(ctx, rtCfg.defaultTimeout)` wrapping the dispatch ctx.
//
// config.SubTurn.DefaultTimeoutMinutes is whole-minute granularity (int), so
// it cannot be lowered below 60s for a fast test — this test instead passes
// its OWN short-deadline ctx into ExecuteTask. Since the new dispatch-ctx
// timeout derives FROM the incoming ctx (context.WithTimeout(ctx, ...), not
// context.Background()), Go's context semantics take the EARLIER of the two
// deadlines, so this test's 2s deadline governs regardless of the
// production 5-minute default — proving the real wrapping code path, not a
// test-only substitute.
func TestProcessTaskDirect_ExternalCLIWorker_Timeout_TaskFailsWithoutHanging(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	_, restore := withFakeDriver(t)
	defer restore()
	// Deliberately inject NOTHING into the fake driver's event channel and
	// never call Cancel() — the only thing that can end this run is the ctx
	// deadline below. A driver that hangs like this is exactly the scenario
	// FR-5.4's turn cap and this timeout exist to bound.

	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the task that never finishes",
		Action:      task.ActionLLM,
		AgentID:     "ext-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := al.taskExecutor.ExecuteTask(shortCtx, tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	// The run should finish around the 2s ctx deadline; waitTaskTerminal's
	// generous ceiling is only the test's patience for the async status write
	// under -p4 CI contention (see asyncTaskPollTimeout), not an SLA on the
	// timeout firing. A dispatch that truly hung (never became terminal) still
	// fails here — the helper fatals when nothing reaches terminal.
	final := waitTaskTerminal(t, al, tk.ID)
	if final.Status != task.StatusFailed {
		t.Fatalf("task status = %q, want %q (result: %s)", final.Status, task.StatusFailed, final.Result)
	}
	// The distinguishing signal for THIS test (vs. the fatal-driver-error test
	// above) is that the failure comes from the ctx deadline racing ahead of a
	// driver that never emits an end/error event: drainExternalRun's ctx.Done()
	// branch sets runErr = ctx.Err() (context.DeadlineExceeded), which
	// propagates unwrapped through runExternalCLISubTurn's result.Err ->
	// processTaskDirectExternalCLI's %w-wrapped error -> finishTaskRun's
	// "execution error: %v" — locking in a stable "deadline exceeded"
	// substring so this test can't silently start passing for the wrong
	// reason (e.g. a different, non-timeout failure).
	if !strings.Contains(final.Result, "deadline exceeded") {
		t.Errorf("task Result = %q, want it to mention the ctx-deadline timeout (%q)",
			final.Result, "deadline exceeded")
	}
	if provider.calls != 0 {
		t.Fatalf("native LLM provider was called %d times, want 0", provider.calls)
	}
}

// TestProcessTaskDirect_ExternalCLIWorker_Cancel_FiresTurnCanceledCallback
// (final-gate audit-trail fix, 2026-07-13) proves the specific regression this
// fix closes: processTaskDirectExternalCLI (pkg/agent/loop.go) registers its
// ephemeral turnState in al.activeTurnStates (making it reachable by
// RequestCancel) but, before this fix, never called ts.Finish on any exit
// path — the ONE place that invokes the onCancelFinish callback RequestCancel
// installs via SetOnCancelFinish (pkg/agent/cancel.go). Without Finish, a
// cancel against an in-flight external-CLI task run claimed cancelFired
// (CancelOutcome{Fired: true}) but produced NO turn_canceled transcript
// entry and NO audit.EventTurnCancelled — a silent audit-trail gap.
//
// This drives the REAL dispatch path end-to-end (TaskExecutor.ExecuteTask ->
// processTaskDirect -> processTaskDirectExternalCLI -> runExternalCLISubTurn),
// not a synthetic turnState (unlike cancel_transcript_test.go's T3, which
// tests the general RequestCancel/Finish machinery in isolation and would
// pass whether or not this dispatch path ever called Finish). The fake
// driver blocks (no InjectEvent yet) so the run is genuinely in-flight when
// RequestCancel fires; only after the cancel is claimed does the test inject
// the End event and unblock the drain loop, letting
// processTaskDirectExternalCLI return and its `defer ts.Finish(false)` run —
// which is exactly what must fire the callback for this test to pass.
func TestProcessTaskDirect_ExternalCLIWorker_Cancel_FiresTurnCanceledCallback(t *testing.T) {
	provider := &countingProvider{}
	al, _ := newExternalCLITaskTestLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the cancellable task",
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

	// Wait until the fake driver's Run has actually been invoked. Inside
	// runTask, the task's SessionID is persisted (te.store.Update) and the
	// ephemeral turnState is registered (al.registerActiveTurn) strictly
	// BEFORE processTaskDirectExternalCLI calls runExternalCLISubTurn ->
	// driver.Run, all on the same goroutine with no intervening hop — so
	// once RecordedRunOpts is non-empty, both are guaranteed to have already
	// happened and RequestCancel below is guaranteed to find the turn.
	runStarted := time.Now().Add(asyncTaskPollTimeout)
	for time.Now().Before(runStarted) && len(fr.RecordedRunOpts()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(fr.RecordedRunOpts()) == 0 {
		t.Fatal("fake driver Run was never invoked — dispatch did not start")
	}

	current, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	taskChatID := current.SessionID
	if taskChatID == "" {
		t.Fatal("task SessionID is empty — cannot resolve the dispatch's transcriptSessionID for RequestCancel")
	}

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: taskChatID},
		CancelCanceller{UserID: "test-user", Channel: "test"},
		CancelHooks{},
	)
	if err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	if !outcome.Fired {
		t.Fatal("RequestCancel: Fired = false, want true — the ephemeral external-CLI turnState " +
			"must be reachable via GetActiveTurnHookForSession while the dispatch is in flight")
	}

	// Unblock the fake driver so the dispatch actually returns and its
	// `defer ts.Finish(false)` runs — the line this test exists to prove.
	fr.InjectEvent(
		runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "finished after cancel"}},
	)
	fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
	fr.Cancel() // closes the event channel so drainExternalRun's loop actually exits

	sessStore := al.GetAgentStore("ext-agent")
	if sessStore == nil {
		t.Fatal("GetAgentStore(\"ext-agent\") returned nil")
	}

	deadline := time.Now().Add(asyncTaskPollTimeout)
	var cancelledEntry *session.TranscriptEntry
	for time.Now().Before(deadline) {
		entries, rerr := sessStore.ReadTranscript(taskChatID)
		if rerr == nil {
			for i := range entries {
				if entries[i].Type == session.EntryTypeTurnCancelled {
					cp := entries[i]
					cancelledEntry = &cp
					break
				}
			}
		}
		if cancelledEntry != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cancelledEntry == nil {
		t.Fatal("no turn_canceled transcript entry appeared — Finish's onCancelFinish callback " +
			"never fired for the external-CLI task dispatch (the regression this test guards against)")
	}
	if cancelledEntry.TurnID != outcome.TurnID {
		t.Errorf("turn_canceled entry TurnID = %q, want RequestCancel's outcome.TurnID %q",
			cancelledEntry.TurnID, outcome.TurnID)
	}
	if cancelledEntry.CancelledByUser != "test-user" {
		t.Errorf("turn_canceled entry CancelledByUser = %q, want %q", cancelledEntry.CancelledByUser, "test-user")
	}
	if cancelledEntry.CancelMethod != "graceful" {
		t.Errorf("turn_canceled entry CancelMethod = %q, want %q (Finish(false) => graceful)",
			cancelledEntry.CancelMethod, "graceful")
	}

	// This test returns as soon as the turn_canceled entry appears (written mid-run
	// in Finish's onCancelFinish callback) — but the runTask goroutine keeps going
	// afterward through finishTaskRun (task-store status + transcript writes). Block
	// until that goroutine has fully finished so t.TempDir()'s deferred cleanup can't
	// race its late writes (the pre-existing "TempDir RemoveAll: directory not empty"
	// flake, which contended package runs occasionally surfaced).
	waitTaskRunGoroutineDone(t, al.taskExecutor, tk.ID)
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
// runs LAST — after finishTaskRun's late task-store/transcript writes — so once
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
