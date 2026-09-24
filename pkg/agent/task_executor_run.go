// task_executor_run.go: Run one task attempt to completion

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// runTask executes the agent prompt and updates the task on completion.
// taskSessionID was already created and persisted SYNCHRONOUSLY by
// ExecuteTask before this goroutine was launched (M1/FR-029 — see
// ExecuteTask's and createTaskSessionSync's doc comments); this goroutine no
// longer creates the session itself.
//
// Task restart (SD-B3): when a run fails as a whole with task attempts
// remaining, consumeTaskAttempt flips the task back to `next` and the run
// loop returns its ID so this goroutine's own trailing cleanup can re-enter
// ExecuteTask — reusing the existing goroutine/dispatch-sema machinery, not
// a new scheduler. The redispatch call is deliberately made from INSIDE the
// single combined deferred closure below, AFTER release()/cancel() have
// already run: calling ExecuteTask while this goroutine still held its own
// dispatch-sema slot would need two slots at once for an instant and could
// spuriously hit the concurrency cap.
func (te *TaskExecutor) runTask(
	ctx context.Context, t *task.Task, taskSessionID string, cancel context.CancelFunc, release func(),
	occurrenceMs *int64, kind task.RunKind,
) {
	var redispatchTaskID string
	defer func() {
		// Outermost defer within this closure: fires LAST, after the
		// redispatch call below (if any) has already run — see wg's doc
		// comment for why this ordering is what keeps the counter from ever
		// being observably zero mid-chain.
		defer te.wg.Done()
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			// The redispatch reuses the SAME occurrenceMs/kind as this attempt —
			// ADR-050 RD5/RD7: a goal-loop redispatch (steering-and-retry) is
			// another attempt at the SAME execution episode, not a new one, and
			// openRun's (taskID, occurrenceMs) idempotency means the redispatch's
			// own runTask call reopens (not duplicates) the still-open run this
			// attempt leaves behind — only the task's final outcome closes it
			// (completeTaskWithResult); an intermediate restart never does.
			if err := te.ExecuteTask(context.Background(), redispatchTaskID, occurrenceMs); err != nil && !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// run is populated once openRun below succeeds. Declared here (rather than
	// via := at the openRun call site) so the panic-recovery defer immediately
	// below closes over this SAME variable and observes whatever it holds at
	// the moment of a panic — nil (closeRun no-ops) if the panic happened
	// before openRun ran, the real handle otherwise.
	//
	// L5 (operator decision 2026-07-20): the ADR-050 RD10 stuck-run reaper was
	// removed — there is no backstop besides this goroutine's own top-level
	// recover (matching the pattern in session_end.go's runRecap, hooks.go's
	// runObserver, this file's own deliverTaskCompletionUpward, and — pre-
	// ADR-091 — the deleted spawnSubTurn, subturn.go). A panic here that left an open TaskRun
	// un-closed would strand it in_progress forever. Logs and returns rather
	// than re-panicking — this goroutine has no caller to propagate to
	// (launched via `go te.runTask(...)`).
	var run *activeRun
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("task_executor",
				"Panic in runTask — closing its TaskRun as failed (no reaper backstop exists)",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "panic": r})
			te.closeRun(t.ID, run, task.StatusFailed, fmt.Sprintf("panic during task execution: %v", r))
		}
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})
	// FR-118/G-13: the goroutine is now genuinely executing this attempt —
	// mirrors pkg/tools/delegate.go's transitionLifecycle(..., LifecycleRunning,
	// "") at the start of its own dispatch path.
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleRunning, "")

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution — mirrors runTaskFromInProgress's
	// identical seam below (see goroutineCtxHook's doc comment). Added so a
	// test can intercept THIS (ExecuteTask) dispatch path too: this is where
	// the now-removed per-agent concurrency cap used to live (the
	// `defaultMaxConcurrentTasksPerAgent = 3` gate deleted from executeTask
	// above), and the regression test for its removal needs to hold a real
	// runTask goroutine in flight to measure peak concurrent dispatch for one
	// agent — see task_executor_no_per_agent_cap_test.go.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	// ADR-050 RD5 run-open (task-run-history-spec.md §3.2): now that
	// taskSessionID is settled (created successfully, or left empty on a
	// session-store failure), open this execution's TaskRun record using the
	// session the dispatch actually minted — see openRun's own doc comment
	// for why run-open cannot happen any earlier than this point.
	run = te.openRun(t.ID, occurrenceMs, kind, taskSessionID)

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	// D13/G-12 (E.5): root a Play-resumed member's turn at its restored tree.
	// No-op (ctx unchanged) for an ordinary attempt.
	taskCtx = WithResumeWorkDirOverride(taskCtx, te.resumeWorkDirFor(t))
	// Carry the task's delegation generation into the run. processTaskDirect reads
	// it back to seed the root turnState depth (so the per-agent depth gate trips
	// inside the run) and to stamp any nested task_create as generation + 1. This
	// is what bounds an A→B→A task-mode delegation chain — without it every task
	// run starts at depth 0 and the gate never trips (see taskCreate's
	// SetMaxDelegationDepth bound, resolved from performance.max_delegation_depth).
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: mark this turn as THIS task's own executor run so
	// TaskUpdateTool refuses any status write on it and goal_claim accepts
	// this turn's claim at any delegation depth — completion is claimed with
	// goal_claim and judged by the run loop (founder decision 2026-09-14; see
	// tools.WithRunningTaskID's doc comment).
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := taskTurnSessionKey(t.AgentID, t.ID)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	turn := func(prompt string) (string, error) {
		return te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	}
	redispatchTaskID = te.executeTaskRun(ctx, t, taskSessionID, "", run, turn)
}

// taskTurnSessionKey is the al.activeTurnStates key a task run's turns are
// registered under. It is the ONE definition of that format — both dispatch
// entry points (runTask above and runTaskFromInProgress below) call it, and
// nothing reconstructs the string by hand.
//
// It is deliberately NOT the child's own session.LifecycleRecord.SessionID.
// A task run is keyed by (agent, task) because the run owns the agent's task
// conversation across every turn of that run; the durable session id names
// only the transcript. That distinction is the reason
// steer_completion.go::hasRunningOrQueuedDescendant cannot ask
// getActiveTurnState(child.SessionID) whether a task-origin child is alive —
// the answer is unconditionally nil for the whole run. It must not call this
// function to "fix" that either: a task RUN spans MANY turns
// (task_run_loop.go::executeTaskRun loops until the claim is adjudicated),
// and between two of those turns no turnState is registered under any key at
// all, so a turn-registry lookup would report a genuinely-working task as
// idle. Run liveness is a different question with a different answer —
// taskRunInFlight below.
func taskTurnSessionKey(agentID, taskID string) string {
	return fmt.Sprintf("agent:%s:task:%s", agentID, taskID)
}

// taskRunInFlight reports whether rec names a TASK-ORIGIN steered child whose
// task run the executor is currently holding — the origin-correct liveness
// signal steer_completion.go::hasRunningOrQueuedDescendant needs and could
// not get from the turn registry.
//
// [ADR-091 fix lane RX-OUTCOME] Background: a task-origin child used to block
// its parent's completion UNCONDITIONALLY while `running`, because the turn
// registry is keyed by taskTurnSessionKey rather than the child's SessionID
// (see that function). That was safe but too coarse: a task-origin child left
// `running` by its own tool-iteration lifecycle notice — its turn long
// finished, its record deliberately kept resumable — blocked its parent for
// ever. The parent WAS woken (message_inbox.go::classifyEnvelope's fix is
// origin-agnostic); it simply could never complete past that child.
//
// The dispatch slot is the right authority, and the only one that is right:
//
//   - It is taken BEFORE the `running` lifecycle write, not after
//     (ExecuteTask/StartTaskNow/dispatchLaunchedTask all insert into
//     te.running under te.mu before launching the goroutine, and the
//     goroutine writes LifecycleRunning only once it is genuinely executing),
//     so there is no window in which the record says `running` and this says
//     "idle" at the start of a run. A turn-registry lookup HAS such a window,
//     and a wide one — hooks and MCP initialisation run between the two.
//   - It is released only in the run goroutine's outermost defer, after the
//     terminal lifecycle write, so it also covers the gaps BETWEEN a run's
//     turns, which a turn-registry lookup cannot.
//   - It is keyed by rec.Origin.TaskID, which is the same id
//     task_executor.go::dispatchLaunchedTask loads the task by and which
//     pkg/session/lifecycle.go validates as non-empty for this origin kind.
//     Nothing is reconstructed and nothing can drift.
//
// Known narrow window, accepted deliberately: a goal-loop restart
// (runTask's trailing `redispatchTaskID` re-entry) deletes the slot and then
// calls ExecuteTask, which re-reserves it — a few in-process instructions
// during which this returns false. It is a real but microscopic exposure,
// and the alternative is the permanent hang this replaces.
//
// Fails CLOSED — "still working", the pre-fix behaviour — for a record whose
// task id is missing or whose executor is not wired, rather than letting a
// parent complete on an unanswerable question.
func (al *AgentLoop) taskRunInFlight(rec *session.LifecycleRecord) bool {
	if rec == nil || rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask {
		return false
	}
	if al == nil || al.taskExecutor == nil || rec.Origin.TaskID == "" {
		return true
	}
	return taskExecutorHoldsDispatchSlot(al.taskExecutor, rec.Origin.TaskID)
}

// resumeWorkDirFor returns the materialized Play-from-commit resume tree for t,
// or "" when this run is an ordinary (non-resumed) attempt (D13/G-12, E.5).
//
// PlanEngine.Play persists t.ResumeFromCommit and materializes the checkout at
// the deterministic workspaces/<ws>/resume/<taskID> path; this reads that same
// path back. The directory is re-derived rather than threaded through a new
// task field precisely BECAUSE it is deterministic — Play and this call site
// cannot disagree about where the tree is.
//
// Returns "" for every degrade (no resume baseline, no workspace, unsafe id, or
// a tree that is not actually on disk) so the turn falls through to the
// workspace's shared work/ dir exactly as it did before Play-from-commit
// existed.
func (te *TaskExecutor) resumeWorkDirFor(t *task.Task) string {
	if t == nil || t.ResumeFromCommit == "" || t.WorkspaceID == "" {
		return ""
	}
	dir, err := memberResumeDir(omnipusHome(), t.WorkspaceID, t.ID)
	if err != nil {
		logger.WarnCF("task_executor", "resume tree: unsafe path — running in the shared work dir",
			map[string]any{"task_id": t.ID, "error": err.Error()})
		return ""
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		// Play recorded a baseline but the tree is gone (manual cleanup, or a
		// materialization that failed and degraded). Fall back rather than
		// refusing the run.
		logger.WarnCF("task_executor", "resume tree missing — running in the shared work dir",
			map[string]any{"task_id": t.ID, "dir": dir, "commit": t.ResumeFromCommit})
		return ""
	}
	logger.InfoCF("task_executor", "resume tree: turn rooted at the restored checkout",
		map[string]any{"task_id": t.ID, "dir": dir, "commit": t.ResumeFromCommit})
	return dir
}

// buildPrompt constructs the FIRST prompt of a task run (later turns in the
// run are the steering prompts task_run_loop.go feeds the same session).
//
// Founder decision 2026-09-14 — ONE claim mechanism: a native worker reports
// completion ONLY by calling the goal_claim tool. A judge checks the claim;
// the task is done only when the judge upholds it, and the worker is never
// told it can mark its own task done (update_task refuses a status on the
// task its own run is executing). The prose completion markers (TASK_STATUS/
// TASK_SUMMARY + [goal:evidence], ADR-043/ADR-052) are taught ONLY to
// subagent_3p (external-CLI) workers — their CLI cannot call Omnipus tools,
// so the marker is their only channel, and the marker claim feeds the SAME
// adjudication path the tool claim uses (task_run_loop.go::resolveRunClaim).
func (te *TaskExecutor) buildPrompt(t *task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Task: %s\n\n", t.Title)
	if t.Prompt != "" {
		sb.WriteString(t.Prompt)
		sb.WriteString("\n\n")
	}
	// On an outer restart (attempt >= 2), t.Result carries why the previous
	// run failed (consumeTaskAttempt). The fresh run's first prompt carries
	// it forward; completeTaskWithResult overwrites Result with the real
	// final result when the task ends.
	if t.AttemptCount > 0 && t.Result != "" {
		fmt.Fprintf(&sb, "## Feedback from attempt %d — address this before claiming success:\n", t.AttemptCount)
		sb.WriteString(t.Result)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "Priority: %d (1=highest, 5=lowest)\n", t.EffectivePriority())
	fmt.Fprintf(&sb, "Task ID: %s\n\n", t.ID)

	if te.dispatchesExternalCLI(t.AgentID) {
		// External-CLI worker: the marker family is its only completion
		// channel (ADR-043, narrowed to subagent_3p by the founder decision).
		// The [goal:evidence] line must immediately precede TASK_STATUS
		// (checkEvidenceMarkerGate); the two status lines stay separate with
		// the FAILURE variant last so a verbatim echo of this block resolves
		// to failure, never success (ADR-043 §2.4 echo-safety).
		sb.WriteString("When you are done, verify your work, then end your final message with the " +
			"evidence line immediately followed by ONE of the two status lines below (never both), " +
			"plus an optional one-line summary:\n")
		sb.WriteString("  " + goalEvidenceLabel + " <one line stating what you verified>\n")
		sb.WriteString("  " + taskStatusLabel + ": success\n")
		sb.WriteString("  " + taskStatusLabel + ": failure\n")
		sb.WriteString("  " + taskSummaryLabel + ": <one-paragraph summary of the outcome>\n")
		return sb.String()
	}

	// Native worker: teach the ONE claim path. status "met" requires a
	// one-line evidence statement of what the worker verified; "blocked" is
	// an honest cannot-proceed; neither ends the turn in a terminal task
	// status the worker could write itself.
	sb.WriteString("How to report completion — the ONLY way this task can finish:\n")
	sb.WriteString("  When the work is done and you have verified it, call the goal_claim tool with " +
		"status \"met\" and evidence: one line stating what you verified. A judge then checks the " +
		"claim against the task's acceptance criteria — the task is only done when the judge upholds it, " +
		"which may take a moment and may disagree with you.\n")
	sb.WriteString("  If you cannot proceed and it is not something the operator can answer directly, " +
		"call goal_claim with status \"blocked\" and the reason as evidence.\n")
	sb.WriteString("  Do not try to mark this task done any other way while it runs — a status you write " +
		"yourself is refused. Keep working, and claim when the work is genuinely verified.\n")
	return sb.String()
}

// dispatchesExternalCLI reports whether agentID's configured executor
// resolves to external-CLI dispatch (subagent_3p) rather than the native
// Omnipus engine — the same runner.ResolveDispatch gate processTaskDirect
// (pkg/agent/loop.go) uses to decide HOW a task actually runs. Fails closed
// to "native" (false) on any resolution failure (unknown agent, nil
// registry, unresolvable executor kind) so a config problem never silently
// strips the task_update instruction from a run that will actually need it.
func (te *TaskExecutor) dispatchesExternalCLI(agentID string) bool {
	registry := te.agentLoop.GetRegistry()
	if registry == nil {
		logger.DebugCF("task_executor", "dispatchesExternalCLI: nil agent registry — failing closed to native dispatch",
			map[string]any{"agent_id": agentID})
		return false
	}
	ag, ok := registry.GetAgent(agentID)
	if !ok || ag == nil {
		logger.DebugCF("task_executor", "dispatchesExternalCLI: agent not found — failing closed to native dispatch",
			map[string]any{"agent_id": agentID})
		return false
	}
	kind, err := runner.ResolveDispatch(executorConfigOf(ag))
	if err != nil {
		logger.DebugCF(
			"task_executor",
			"dispatchesExternalCLI: ResolveDispatch failed — failing closed to native dispatch",
			map[string]any{"agent_id": agentID, "error": err.Error()},
		)
		return false
	}
	return kind == runner.DispatchKindExternalCLI
}

// openRun best-effort opens (or, per Store.OpenRun's own idempotency,
// transparently reuses) the TaskRun record for (taskID, occurrenceMs) —
// ADR-050 RD5/RD7. Called from runTask AFTER the task's session has been
// created so the run's session_id is the session the dispatch actually
// minted (task-run-history-spec.md §3.2: "the run's session_id is the one
// the dispatch already mints"). Run-open cannot happen any earlier than that
// point without either duplicating session creation or leaving the run's
// session_id permanently empty: Store.OpenRun's signature requires a
// session_id at open time (a close record cannot amend it later — see
// TaskRun's own doc comment on why folding is last-record-wins with no
// field-level merge), and pkg/session.UnifiedStore.NewSession always
// self-mints its own ID — there is no way to pre-select one from outside
// pkg/session.
//
// A run-store failure here is logged and degrades to nil (no run tracked
// for this execution) rather than failing the task dispatch — TaskRun is a
// purely additive record layer (RD2); a run-history I/O problem must never
// prevent or fail a real agent execution.
func (te *TaskExecutor) openRun(taskID string, occurrenceMs *int64, kind task.RunKind, sessionID string) *activeRun {
	run, _, err := te.store.OpenRun(taskID, occurrenceMs, kind, sessionID)
	if err != nil {
		// M3-log: escalated from Warn to Error — a failed open means no run
		// will ever be tracked for this execution, and there is no reaper to
		// notice or retry it later.
		logger.ErrorCF("task_executor", "Could not open task run record",
			map[string]any{"task_id": taskID, "kind": string(kind), "error": err.Error()})
		return nil
	}
	te.emitRunStatus(taskID, run.RunID, run.OccurrenceMs, task.StatusInProgress)
	return &activeRun{runID: run.RunID, occurrenceMs: run.OccurrenceMs}
}

// closeRun best-effort closes run's TaskRun record with the given terminal
// status/result (ADR-050 RD5) — a no-op when run is nil (openRun degraded,
// or this execution never opened one). Called from completeTaskWithResult
// always AFTER the existing Task.status/result mirror write that function
// already performs, so run-history strictly observes
// the SAME completion signal, never a second source of truth for it.
//
// closeRun can legitimately be invoked TWICE for the same run (delta-review
// Fix 2, 2026-07-20): runTask's own top-level panic-recovery defer
// (~line 218) closes over the SAME *activeRun completeTaskWithResult already
// closed, and re-invokes closeRun if a panic occurs in POST-completion
// housekeeping (onTaskComplete / deliverTaskCompletionUpward) that runs
// AFTER completeTaskWithResult's own successful closeRun call. That second
// call hits task.ErrRunAlreadyClosed — the record is correctly terminal, not
// stranded — so it is logged at Info, not Error: an ERROR log here reading
// "permanently strands" would fire a false on-call alert for a run that
// closed successfully the first time.
func (te *TaskExecutor) closeRun(taskID string, run *activeRun, status task.Status, result string) {
	if run == nil || run.runID == "" {
		return
	}
	if err := te.store.CloseRun(taskID, run.runID, status, result); err != nil {
		if errors.Is(err, task.ErrRunAlreadyClosed) {
			logger.InfoCF(
				"task_executor",
				"Task run already closed by an earlier step; ignoring duplicate close from panic-recovery/housekeeping",
				map[string]any{"task_id": taskID, "run_id": run.runID, "attempted_status": string(status)},
			)
			return
		}
		// M3-log: escalated from Warn to Error — a failed close permanently
		// strands this run in_progress; there is no reaper to close it later.
		logger.ErrorCF("task_executor", "Could not close task run record",
			map[string]any{"task_id": taskID, "run_id": run.runID, "error": err.Error()})
		return
	}
	te.emitRunStatus(taskID, run.runID, run.occurrenceMs, status)
}

// emitRunStatus publishes a TaskRun open/close transition (ADR-050
// Consequences "Realtime", task-run-history-spec.md §3.8) onto
// the agent event bus via AgentLoop.EmitTaskRunStatus — the same
// emitEvent-based mechanism emitStatusChanged above uses for Task.status
// transitions. Best-effort; a nil agentLoop (test seams that construct a
// bare TaskExecutor) is silently skipped.
func (te *TaskExecutor) emitRunStatus(taskID, runID string, occurrenceMs *int64, status task.Status) {
	if te.agentLoop == nil {
		return
	}
	te.agentLoop.EmitTaskRunStatus(TaskRunStatusPayload{
		TaskID:       taskID,
		RunID:        runID,
		OccurrenceMs: occurrenceMs,
		Status:       string(status),
	})
}

// runTaskFromInProgress is the goroutine body for tasks launched via
// StartTaskNow. The session has already been created and the session_id
// persisted; it skips the session-creation block that runTask performs and
// goes straight to execution, reusing the shared completion logic.
//
// BLK-3 (operator decision 2026-07-20): it DOES open an ADR-050 RD5/RD7
// TaskRun record (task-run-history-spec.md §3.2), threading the resulting
// *activeRun into the run loop so completion closes it. This was previously
// out of scope — StartTaskNow's raw-PATCH entry point is distinct from the
// ClaimForRun/SpawnReset-guarded paths §3.2 originally scoped run-open to —
// but the gateway's "Start Task" and "Create & Run now" UI actions BOTH
// PATCH→in_progress→StartTaskNow→runTaskFromInProgress, making this the most
// common launch path in practice; leaving it unrecorded meant the majority
// of real runs recorded no history at all. kind is always RunKindManual
// (every launch through here is user-initiated) with occurrenceMs always nil
// (no recurring-fire context reaches this entry point). StartOccurrenceRun
// below is the OTHER run-aware manual entry point — the calendar's
// per-occurrence Run-now.
func (te *TaskExecutor) runTaskFromInProgress(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	cancel context.CancelFunc,
	release func(),
) {
	var redispatchTaskID string
	defer func() {
		// Outermost defer within this closure: fires LAST, after the
		// redispatch call below (if any) has already run — see
		// TaskExecutor.wg's doc comment for why this ordering is what keeps
		// the counter from ever being observably zero mid-chain.
		defer te.wg.Done()
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			// occurrenceMs is always nil here — see this function's own doc
			// comment (no recurring-fire context reaches StartTaskNow).
			if err := te.ExecuteTask(context.Background(), redispatchTaskID, nil); err != nil && !isRoutineAutoDispatchRefusal(err) {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// run is populated once openRun below succeeds. Declared here (rather than
	// via := at the openRun call site) so the panic-recovery defer immediately
	// below closes over this SAME variable — see runTask's identical
	// declaration for the full rationale (no reaper backstop).
	var run *activeRun
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("task_executor",
				"Panic in runTaskFromInProgress — closing its TaskRun as failed (no reaper backstop exists)",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "panic": r})
			te.closeRun(t.ID, run, task.StatusFailed, fmt.Sprintf("panic during task execution: %v", r))
		}
	}()

	// FR-118/G-13: the goroutine is now genuinely executing this attempt —
	// mirrors runTask's identical call (see its doc comment) and, deliberately,
	// sits BEFORE the goroutineCtxHook test seam below: a test using the hook
	// to intercept before real execution is still simulating a goroutine that
	// truly started, so the durable record should show running, not queued,
	// at the moment of interception.
	te.transitionTaskLifecycle(taskSessionID, session.LifecycleRunning, "")

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution. The hook receives the goroutine's context so
	// tests can assert it is not canceled by the originating request context.
	// Deliberately BEFORE the run-open below: this seam never reaches
	// the run loop, so opening a run here would create one that this
	// (never-executing) test double can never close.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	logger.InfoCF("task_executor", "runTaskFromInProgress started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

	// ADR-050 RD5/RD7 run-open (task-run-history-spec.md §3.2): taskSessionID
	// was already created and persisted synchronously by StartTaskNow before
	// this goroutine was launched (unlike runTask, which must wait for its
	// own session-creation block to settle), so it is available immediately.
	run = te.openRun(t.ID, nil, task.RunKindManual, taskSessionID)

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	// D13/G-12 (E.5): root a Play-resumed member's turn at its restored tree.
	// No-op (ctx unchanged) for an ordinary attempt. Both dispatch entry points
	// set this — runTaskFromInProgress is the one Play itself re-enters through.
	taskCtx = WithResumeWorkDirOverride(taskCtx, te.resumeWorkDirFor(t))
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: same in-run marker as runTask above — see
	// tools.WithRunningTaskID's doc comment.
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := taskTurnSessionKey(t.AgentID, t.ID)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	turn := func(prompt string) (string, error) {
		return te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	}
	redispatchTaskID = te.executeTaskRun(ctx, t, taskSessionID, " (StartTaskNow path)", run, turn)
}

// processTaskDirectExternalCLI runs a task assigned to a subagent_3p
// (external-CLI) worker through runExternalCLISubTurn — the same dispatch
// machinery task_executor_run.go's dispatchesExternalCLI check and the
// delegate tool's own dispatch path share for agent-to-agent delegation
// today (pre-ADR-091, the deleted spawnSubTurn, subturn.go). A task run has
// no parent turnState to derive a child from (unlike a delegated
// sub-turn), so this builds a minimal turnState directly for the target agent
// via newTurnState, wiring the agent snapshot, the task's transcript session
// (so the run is replayable on reload, same as the native task path), and the
// task's WorkspaceID (so workspace.FindForAgentPreferring can route the run
// into the workspace's shared work/ directory when the agent is a workspace
// CoreTeam member — mirrors the native runTurn resolution). Channel/ChatID/
// SenderID/UserMessage are ALSO set on opts — not inert: since FIX 5 (below)
// registers this turnState in al.activeTurnStates, turnState.snapshot() (via
// GetActiveTurn/GetActiveTurnBySession) now surfaces them for the duration of
// the run, exactly like a native turn's.
//
// An external-CLI worker's tool registry is its OWN CLI's, never Omnipus's —
// it has no task_update tool wired at all — so buildPrompt's ADR-043
// TASK_STATUS/TASK_SUMMARY marker instruction (task_executor.go) is this
// dispatch kind's ONLY possible completion signal. The task run loop
// (task_run_loop.go::resolveRunClaim) reads the aggregated CLI output
// (ForUser, falling back to ForLLM) for that marker and feeds it into the same
// claim path goal_claim feeds: success with an evidence line is a met claim the
// Judge checks, failure is a blocked claim that ends the task Failed, and no
// marker at all spends one goal try — it is NEVER auto-completed to Done on
// unverified prose alone. This replaced the former "auto-complete to Done,
// WARN-only" default (ADR-042 §3's finding); see ADR-043 §8 for the current
// contract.
//
// Delegation-depth bounding: the dispatched CLI child runs as a separate OS
// process with its own tool registry — it has no delegate/create_task tools
// wired to Omnipus at all — so it structurally cannot recurse into another
// Omnipus delegation or task chain regardless of depth. ts.depth is still
// seeded from the caller's delegationDepth for observability/symmetry with
// the native branch's opts.InitialDelegationDepth.
//
// FIX 5 (7-reviewer gate, visibility): this turnState IS now registered in
// al.activeTurnStates for the run's duration (register/defer-clear below,
// mirroring native runTurn's registerActiveTurn/clearActiveTurn pair and
// the pre-ADR-091 subturn.go's own childTS registration (since deleted) —
// ts.depth is read
// by cancel.go's activeTurnStates.Range-based readers now that the turn is
// reachable there (it previously was not: an unregistered turnState made
// ts.depth dead for every purpose except this function's own local seeding).
// Registering also means:
//   - writeTurnCancelledRestartForActiveTurns' FR-048 graceful-shutdown scan
//     now covers an in-flight external-CLI task run (previously it silently
//     vanished from the transcript on a mid-run restart).
//   - GetActiveTurn/GetActiveAgentIDs now report this run like any other.
//   - A RequestCancel against this session (transcriptSessionID == taskChatID)
//     can reach and ClaimCancel this turnState. STALE-COMMENT CORRECTION
//     (doc-only, cancel-propagation FIX 1): this used to say ts.cancelFunc/
//     ts.providerCancel stay nil for this dispatch path — that is no longer
//     true. runExternalCLISubTurn (external_dispatch.go) now calls
//     childTS.setTurnCancel(cancel) / childTS.setProviderCancel(cancel) on
//     THIS SAME ts (it is passed in as runExternalCLISubTurn's childTS
//     argument below), wiring both fields to the context.CancelFunc that
//     actually tears down the dispatched external-CLI subprocess (every
//     driver binds the OS child via exec.CommandContext(runCtx, ...), so
//     canceling that func kills the subprocess outright — see FIX 1's own
//     doc comment at that call site for the full rationale). So a
//     RequestCancel reaching this turnState now does more than update
//     transcript/audit bookkeeping: it ALSO cancels the real external-CLI
//     process, the same as the native delegation path. The remaining true
//     part of the original claim: this dispatch still has no
//     delegate/create_task tools that could populate ts.childTurnIDs, so the
//     hard-abort child-cascade branch is still unreachable here — that part
//     of the "no-panic" reasoning is unaffected.
func (al *AgentLoop) processTaskDirectExternalCLI(
	ctx context.Context,
	liveAgent *AgentInstance,
	prompt, sessionKey, taskChatID string,
	delegationDepth int,
) (string, error) {
	// FIX 1 (7-reviewer gate, data race): liveAgent is the LIVE registry
	// *AgentInstance (registry.GetAgent, in processTaskDirect above) —
	// SwitchModel/ApplyAgentModel may concurrently rewrite its
	// Model/Provider/Candidates/ThinkingLevel tuple (+ providerPool) while
	// this run is in flight (AgentInstance.mu's doc, instance.go:28-30), and
	// runExternalCLISubTurn reads agent.Model unlocked (transcript
	// attribution + RunOptions.Model) — a read/write race with SwitchModel.
	// snapshotForExternalDispatch takes a single RLock and copies the whole
	// mutex-protected quad together into a private AgentInstance value
	// nothing else can mutate, mirroring the same execSource-snapshot
	// pattern the pre-ADR-091 native delegation path (subturn.go, since
	// deleted) relied on for the identical reason. Every field below (opts,
	// newTurnState, composeDelegateInput) reads from this snapshot, never
	// liveAgent directly.
	agent := liveAgent.snapshotForExternalDispatch()

	opts := processOptions{
		SessionKey:          sessionKey,
		Channel:             "webchat",
		ChatID:              taskChatID,
		SenderID:            "task-executor",
		UserMessage:         prompt,
		TranscriptSessionID: taskChatID,
		TranscriptStore:     al.taskSessionStore(taskChatID, agent.ID),
		// WorkspaceID is already on ctx via tools.WithWorkspaceID (set by the
		// task executor before calling processTaskDirect); thread it through
		// processOptions explicitly too so runExternalCLISubTurn's
		// workspace.FindForAgentPreferring(..., childTS.opts.WorkspaceID) call
		// sees it — that field reads ts.opts, not the context.
		WorkspaceID: tools.ToolWorkspaceID(ctx),
	}
	ts := newTurnState(agent, opts, al.newTurnEventScope(agent.ID, sessionKey))
	ts.depth = delegationDepth
	ts.al = al // FIX 5: back-ref for hard-abort cascade (mirrors the pre-ADR-091 subturn.go, since deleted)

	// FIX 5: register for the run's duration — see this function's doc
	// comment for the full reachability analysis.
	al.registerActiveTurn(ts)
	// FINAL-GATE FIX (2026-07-13, cancel audit-trail gap): FIX 5 registered
	// this turnState so a RequestCancel could reach and ClaimCancel it, but
	// nothing on this path ever called ts.Finish — the ONE place that fires
	// the onCancelFinish callback RequestCancel installs via
	// SetOnCancelFinish (pkg/agent/cancel.go). Without it, a cancel here
	// claimed cancelFired (CancelOutcome{Fired: true}) but produced NO
	// turn_canceled transcript entry, NO MarkLastEntryTruncated, and NO
	// audit.EventTurnCancelled — silently contradicting this function's own
	// doc comment above, which already (incorrectly, until this fix)
	// described the callback as firing.
	//
	// Calling Finish here is safe to add: at THIS point (construction, right
	// before registerActiveTurn ran above) ts.cancelFunc/ts.providerCancel
	// are still nil — cancel-propagation FIX 1 (external_dispatch.go's
	// runExternalCLISubTurn) only wires them once dispatch actually starts,
	// below. By the time this function returns and the deferred Finish call
	// below actually RUNS, those fields are typically non-nil (set to the
	// dispatch's own context.CancelFunc) — see this function's top doc
	// comment's STALE-COMMENT CORRECTION note for the full explanation. That
	// does not change this safety argument: Finish's cancelFunc branch
	// (`if ts.cancelFunc != nil { ts.cancelFunc() }`) simply invokes it,
	// which is exactly what dispatchCancel's own `defer dispatchCancel()`
	// below already guarantees happens — canceling an already-canceled
	// context is a no-op, so calling it twice (once via that defer, once via
	// Finish) is harmless. Below (FIX 2), this call passes
	// ts.hardAbortRequested() rather than a hardcoded false, so the
	// child-cascade branch CAN run here when a hard abort was requested — but
	// that is also safe: Finish's closeOnce.Do + the
	// cancelFired-swap-then-nil-check around onCancelFinish make ANY repeated
	// Finish call (e.g. a concurrent InterruptSessionHard elsewhere calling
	// requestHardAbort on this same ts via steering.go, whether or not this
	// site's own call also cascades) idempotent — the identical safety
	// runTurn's own deferred Finish call already relies on for the
	// hard-abort-then-deferred-Finish sequence (loop.go, "closeOnce.Do
	// inside Finish makes repeated Finish calls safe" comment).
	//
	// Ordering matches runTurn's LIFO defer pattern (loop.go, "Execution
	// order (LIFO defer...)" comment): clearActiveTurn must run BEFORE
	// Finish, so a cancel racing the tail end of this dispatch cannot find a
	// since-finished turnState still reachable via
	// GetActiveTurnHookForSession and register a callback that can now never
	// fire — the same class of race that ordering guards against in
	// runTurn. Defers execute LIFO, so writing Finish's defer first and
	// clearActiveTurn's defer second makes clearActiveTurn run FIRST and
	// Finish run LAST, exactly like runTurn's own Finish/clearActiveTurn pair.
	//
	// FIX 2: call Finish with ts.hardAbortRequested(), not a hardcoded false.
	// InterruptSessionHard (the session-wide web-cancel escalation path;
	// steering.go) hard-aborts a turn by calling ts.requestHardAbort() alone —
	// it never calls ts.Finish(true) itself (only the legacy single-session
	// HardAbort()/InterruptHard do that). For a turn hard-aborted that way,
	// THIS deferred call is the only Finish call that will ever happen, so a
	// hardcoded false silently mislabeled a genuine hard abort as a graceful
	// finish — wrong for the cancelFired-gated onCancelFinish callback
	// (cancel.go's RequestCancel), which threads its "graceful"/"hard"
	// cancelMethod straight into the persisted turn_canceled transcript entry
	// that pkg/gateway/replay.go renders back to the user as
	// "Turn canceled (%s)". Must be wrapped in a closure: a bare
	// `defer ts.Finish(ts.hardAbortRequested())` would evaluate
	// hardAbortRequested() immediately at THIS defer statement (Go evaluates
	// deferred arguments at registration time, not at call time) — i.e.
	// always false, reproducing the exact bug this fixes.
	defer func() { ts.Finish(ts.hardAbortRequested()) }()
	defer al.clearActiveTurn(ts)

	delegationTimeout := al.effectiveDelegationTimeout()

	// ADDITIONAL FINDING (surfaced while writing pr-test-analyzer's T2, not
	// one of the 11 numbered fixes): runExternalCLISubTurn never wraps its
	// own ctx with a deadline — it derives runCtx via plain
	// context.WithCancel(ctx) (external_dispatch.go) and only forwards
	// rtCfg.defaultTimeout to the DRIVER as RunOptions.TimeoutSeconds, a hint
	// each real driver applies itself (driver_claude.go/driver_codex.go/
	// driver_opencode.go all do `context.WithTimeout(runCtx,
	// TimeoutSeconds*time.Second)` internally). The pre-ADR-091 native
	// delegation path (subturn.go, since deleted) already had its OWN
	// Go-level safety-net timeout (`context.WithTimeout(context.Background(),
	// timeout)`) precisely so a driver that never honors/emits an end event
	// cannot hang the dispatch forever; this task-mode dispatch had no
	// equivalent — a stuck external CLI would tie up a dispatch-semaphore
	// slot indefinitely with nothing to notice. Unlike that path's
	// Background()-rooted child (deliberately independent so a Critical
	// sub-turn survives its parent's graceful finish), this derives the
	// deadline FROM the incoming ctx — consistent with the native task path,
	// where runTurn's own turnTimeout is likewise derived from the given ctx
	// (loop.go) — so a TaskExecutor-level cancel (te.running[taskID].cancel(),
	// ExecuteTask/StartTaskNow) still takes effect immediately in addition to
	// this deadline.
	dispatchCtx, dispatchCancel := context.WithTimeout(ctx, delegationTimeout)
	defer dispatchCancel()

	// FIX 2 (7-reviewer gate, persona dropped): compose the same (soul, task)
	// pair the native delegation path uses ahead of its own
	// runExternalCLISubTurn call (subturn_identity.go's composeDelegateInput,
	// also called just below in this file, ahead of THIS file's own
	// runExternalCLISubTurn call) so the target's own soul/persona travels with a TASK-mode dispatch too,
	// not just an agent-to-agent delegate call. An empty soul (a soul-less
	// custom agent — a seeded worker's compiled prompt is non-empty as of
	// the RC-6 fix, coreagent's "worker" prompts-map entry) yields
	// task-only input, identical to the pre-fix behavior.
	externalInput := composeDelegateInput(al, prompt, "", agent.ID)

	result, err := runExternalCLISubTurn(dispatchCtx, al, ts, externalInput, delegationTimeout)
	if err != nil {
		return "", fmt.Errorf("processTaskDirect: external-cli dispatch: %w", err)
	}
	// FIX 6 (7-reviewer gate, dead defensive branch): runExternalCLISubTurn's
	// only two return statements are `return nil, fmt.Errorf(...)` (already
	// handled by the err != nil check above — a non-nil err is ALWAYS paired
	// with a nil result on that path) and the terminal `return result,
	// result.Err` (drainExternalRun always returns a non-nil *tools.ToolResult,
	// so result.Err is nil exactly when err here is nil). So once err == nil,
	// result == nil and result.Err != nil are BOTH unreachable. The old `if
	// result == nil { return "", nil }` silently reported task SUCCESS for a
	// broken invariant instead of surfacing it — fail loudly so a future
	// change that breaks the invariant is caught immediately, not masked as
	// an empty-but-successful task result.
	if result == nil {
		return "", fmt.Errorf("processTaskDirect: external-cli dispatch: nil result with no error")
	}
	if result.ForUser != "" {
		return result.ForUser, nil
	}
	return result.ForLLM, nil
}
