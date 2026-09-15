// task_executor_test.go: tests for board and task dispatch

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestProcessTaskDirect_MediaToolDelivery_StampsWorkspaceID is the
// processTaskDirect (native) regression: WorkspaceID is read back from
// tools.ToolWorkspaceID(ctx) — the same context value processTaskDirectExternalCLI
// already reads a few lines below in production, seeded by the task executor
// before calling processTaskDirect. Channel is hardcoded to "webchat" so the
// fake channel is registered under that name.
func TestProcessTaskDirect_MediaToolDelivery_StampsWorkspaceID(t *testing.T) {
	al, defaultAgent, webchatChannel, _ := newMediaWorkspaceIDTestLoop(t, "webchat")

	ctx := tools.WithWorkspaceID(context.Background(), "sales")
	result, err := al.processTaskDirect(
		ctx, defaultAgent.ID, "take a screenshot of the screen and send it to me",
		"task-sess-1", "task:1",
	)
	require.NoError(t, err)
	assert.Equal(t, "Here is the screenshot.", result)

	require.Len(t, webchatChannel.sentMedia, 1)
	assert.Equal(t, "sales", webchatChannel.sentMedia[0].WorkspaceID)
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

	// A run that times out is broken — one failed task attempt; a limit of 1
	// ends the task on it instead of restarting (founder decision 2026-09-14).
	oneAttempt := 1
	tk := &task.Task{
		Title:       "assigned to external worker",
		Prompt:      "do the task that never finishes",
		Action:      task.ActionLLM,
		AgentID:     "ext-agent",
		Priority:    3,
		WorkspaceID: "default",
		Status:      task.StatusNext,
		MaxAttempts: &oneAttempt,
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
	// processTaskDirectExternalCLI's %w-wrapped error -> the task run loop's
	// "execution error: <plain message>". The task result states a turn error
	// in the contract's plain words for its typed code, never in the error's
	// own text (task_run_error_leak_test.go), so this locks in the timed-out
	// message: only a deadline classifies as CodeTurnTimedOut, so this test
	// can't silently start passing for the wrong reason (a cancel reads
	// turn_canceled's message, any other failure its own).
	if want := UserMessageForCode(CodeTurnTimedOut); !strings.Contains(final.Result, want) {
		t.Errorf("task Result = %q, want it to carry the timed-out message %q", final.Result, want)
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

	// A stopped run ENDS the task. Documented intent: both turn-error
	// classifiers inherit TranslateTurnError's precedence so "a Stop ... still
	// ends the way a Stop does" (operator_only_turn_error.go,
	// task_attempt_turn_error.go), and task_run_loop.go restarts a task only for
	// a run that broke or ended not met — a stop is neither, so it uses no task
	// attempt and never starts a fresh run. Before this was pinned, the cancel
	// read as a broken run and the task restarted; that restarted run was still
	// calling the fake driver factory after this test returned and restored it
	// (the -race failure on CI, 2026-09-15). waitTaskTerminal comes first on
	// purpose: a restart moves the task back to `next`, never to a terminal
	// status, so these assertions cannot pass in the window before a restart
	// begins.
	final := waitTaskTerminal(t, al, tk.ID)

	// The runTask goroutine keeps going after the task's terminal write
	// (run-history close, transcript archive). Block until it has fully finished
	// so t.TempDir()'s deferred cleanup can't race its late writes (the
	// pre-existing "TempDir RemoveAll: directory not empty" flake, which
	// contended package runs occasionally surfaced).
	waitTaskRunGoroutineDone(t, al.taskExecutor, tk.ID)

	if final.Status != task.StatusFailed {
		t.Errorf("task Status = %q, want %q — a stopped task ends", final.Status, task.StatusFailed)
	}
	if final.AttemptCount != 0 {
		t.Errorf("task AttemptCount = %d, want 0 — a stop is not a failed attempt", final.AttemptCount)
	}
	if want := UserMessageForCode(CodeTurnCanceled); !strings.Contains(final.Result, want) {
		t.Errorf("task Result = %q, want it to carry the stopped message %q", final.Result, want)
	}
	if runs := len(fr.RecordedRunOpts()); runs != 1 {
		t.Errorf("external driver Run was invoked %d times, want 1 — a stopped task must not start a fresh run", runs)
	}
}
