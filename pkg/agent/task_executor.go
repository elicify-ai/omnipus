package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	defaultMaxConcurrentTasksPerAgent = 3
	maxTaskDepth                      = 10
)

// ErrDispatchCapReached is returned by StartTaskNow when the global dispatch
// semaphore is exhausted. Callers (e.g. the REST handler) use errors.Is to
// distinguish this retryable condition from hard failures.
var ErrDispatchCapReached = errors.New("task_executor: global dispatch cap reached")

// taskSlot holds the state for one running (or reserved) task goroutine. A
// slot is inserted into te.running atomically under te.mu before the goroutine
// launches; this eliminates the nil-sentinel that previously required every
// reader to remember the nil-check.
//
// States:
//
//	reserved == true, cancel == nil  → slot claimed, goroutine not yet started
//	reserved == false, cancel != nil → goroutine live and cancellable
type taskSlot struct {
	cancel   context.CancelFunc // non-nil once the goroutine starts
	reserved bool               // true while the slot is held but goroutine not yet launched
}

// TaskExecutor runs dispatchable tasks by handing them to agent sessions. It
// operates over the unified pkg/task store and 6-state vocabulary: a
// dispatchable task is `next`, running is `in_progress`, terminal is
// `done`/`failed`.
type TaskExecutor struct {
	agentLoop     *AgentLoop
	store         *task.Store
	mu            sync.Mutex
	running       map[string]*taskSlot
	maxConcurrent int
	// dispatchSema gates the total number of concurrently dispatched tasks
	// across all agents.
	dispatchSema *DispatchSemaphore

	// parentFollowUp is a test seam ONLY — production leaves it nil.
	parentFollowUp func(parentID string)

	// goroutineCtxHook is a test seam ONLY — production leaves it nil.
	// When non-nil, runTaskFromInProgress calls it with the goroutine's context
	// and returns immediately without performing real agent execution. This lets
	// tests observe the context that the goroutine received (e.g. to verify it is
	// not derived from the HTTP request context and survives request cancellation).
	goroutineCtxHook func(ctx context.Context, taskID string)
}

// newTaskExecutor creates a TaskExecutor over the unified task store.
func newTaskExecutor(al *AgentLoop, store *task.Store) *TaskExecutor {
	capacity := defaultMaxConcurrentTasksPerAgent
	if al.cfg != nil {
		if eff := al.cfg.Performance.EffectiveMaxParallelAgents(); eff > 0 {
			capacity = eff
		}
	}
	return &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]*taskSlot),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(capacity),
	}
}

// ExecuteTask starts executing the dispatchable task identified by taskID. It
// atomically claims the task (next→in_progress via ClaimForRun) and dispatches
// it to the agent in a goroutine, gated by per-agent and global concurrency
// controls.
func (te *TaskExecutor) ExecuteTask(ctx context.Context, taskID string) error {
	t, err := te.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_executor: get task %q: %w", taskID, err)
	}
	if t.Status != task.StatusNext {
		return fmt.Errorf("task_executor: task %q is %s, not next", taskID, t.Status)
	}

	// Guard: do not dispatch a task that still has unsatisfied dependencies.
	if len(t.BlockedBy) > 0 {
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				return fmt.Errorf("task_executor: task %q is blocked by %q (not done)", taskID, depID)
			}
		}
	}

	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		return fmt.Errorf(
			"%w (%d/%d in flight), retry later",
			ErrDispatchCapReached,
			te.dispatchSema.InFlight(), te.dispatchSema.Cap(),
		)
	}

	registry := te.agentLoop.GetRegistry()
	if _, ok := registry.GetAgent(t.AgentID); !ok {
		release()
		logger.ErrorCF("task_executor", "Agent not found, failing task",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID})
		te.failTask(taskID, fmt.Sprintf("agent %q not found", t.AgentID))
		return fmt.Errorf("task_executor: agent %q not found", t.AgentID)
	}

	// Per-agent cap: count running (in_progress) tasks for this agent.
	runningTasks, err := te.store.List(task.Filter{Status: task.StatusInProgress, AgentID: t.AgentID})
	if err != nil {
		release()
		return fmt.Errorf("task_executor: list running tasks for agent %q: %w", t.AgentID, err)
	}
	if len(runningTasks) >= te.maxConcurrent {
		release()
		return fmt.Errorf(
			"task_executor: concurrency limit reached for agent %q (%d running)",
			t.AgentID, len(runningTasks),
		)
	}

	// Atomically claim the task (next→in_progress) under the store lock.
	now := time.Now().UTC()
	claimed, err := te.store.ClaimForRun(taskID, now)
	if err != nil {
		release()
		if errors.Is(err, task.ErrAlreadyClaimed) {
			return fmt.Errorf("task_executor: task %q already claimed by concurrent dispatch", taskID)
		}
		return fmt.Errorf("task_executor: claim task %q for run: %w", taskID, err)
	}
	t = claimed

	// M1 (ADR-052 FR-029): create the task session and persist its SessionID
	// SYNCHRONOUSLY, in THIS call, before the task ever leaves ExecuteTask's
	// own goroutine — not async inside the run goroutine as before. This
	// closes the concurrent-dispatch race SC-005 requires: the plan engine's
	// Stop fan-out (PlanEngine.StopPlan/StopTask) runs under planDecisionMu,
	// the SAME lock dispatchReadyMembers dispatches under, so a member whose
	// SessionID was only assigned asynchronously could be dispatched and
	// then immediately escape a concurrently-running Stop fan-out (the fan-
	// out's snapshot, taken microseconds earlier under the same lock, would
	// have seen no SessionID for it yet). Mirrors StartTaskNow's existing
	// synchronous pattern; unlike StartTaskNow (which aborts the whole call
	// on a session-creation failure), this logs-and-continues — dispatch
	// still proceeds session-less exactly as it always has when sessStore is
	// nil (see createTaskSessionSync's own doc comment for why these two
	// callers' error-handling divergence is intentional, not an oversight).
	taskSessionID, sessErr := te.createTaskSessionSync(t)
	if sessErr != nil {
		logger.ErrorCF("task_executor",
			"Could not create task session (dispatch continues without a session)",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID, "error": sessErr.Error()})
	} else if taskSessionID != "" {
		t.SessionID = taskSessionID
	}

	te.emitStatusChanged(t, task.StatusInProgress)

	taskCtx, cancel := context.WithCancel(ctx)
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()

	go te.runTask(taskCtx, t, taskSessionID, cancel, release)
	return nil
}

// createTaskSessionSync creates task t's session (SessionTypeTask), sets its
// meta (Title/TaskID/WorkspaceID), persists SessionID on the task record via
// te.store.Update, and appends the initial prompt transcript entry — all
// synchronously, in the CALLER's own goroutine (M1/FR-029; see ExecuteTask's
// doc comment for why this matters). Shared by ExecuteTask; StartTaskNow
// performs the equivalent block inline (its own error-handling — abort the
// whole call on failure — deliberately differs from ExecuteTask's log-and-
// continue, so it is not routed through this helper; see finishTaskRun's own
// doc comment on that intentional divergence).
//
// Returns ("", nil) when sessStore is nil (no agent store configured for
// t.AgentID) — callers treat that as "no session", not an error, exactly as
// before this refactor moved the block out of runTask's goroutine.
func (te *TaskExecutor) createTaskSessionSync(t *task.Task) (string, error) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		logger.ErrorCF("task_executor", "Agent store not found, task will have no session",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
		return "", nil
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)
	if err != nil {
		return "", fmt.Errorf("task_executor: create task session for %q: %w", t.ID, err)
	}
	taskSessionID := meta.ID
	title := t.Title
	taskID := t.ID
	wsID := t.WorkspaceID
	metaPatch := session.MetaPatch{Title: &title, TaskID: &taskID}
	if wsID != "" {
		metaPatch.WorkspaceID = &wsID
	}
	if setErr := sessStore.SetMeta(meta.ID, metaPatch); setErr != nil {
		logger.ErrorCF("task_executor", "Could not set task session meta",
			map[string]any{"task_id": t.ID, "error": setErr.Error()})
	}
	if _, updateErr := te.store.Update(t.ID, task.Patch{SessionID: &taskSessionID}); updateErr != nil {
		logger.ErrorCF("task_executor", "Could not persist session_id on task",
			map[string]any{"task_id": t.ID, "session_id": taskSessionID, "error": updateErr.Error()})
	}
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        t.ID + "-prompt",
		Role:      "user",
		Content:   te.buildPrompt(t),
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.ErrorCF("task_executor", "Transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
	}
	return taskSessionID, nil
}

// runTask executes the agent prompt and updates the task on completion.
// taskSessionID was already created and persisted SYNCHRONOUSLY by
// ExecuteTask before this goroutine was launched (M1/FR-029 — see
// ExecuteTask's and createTaskSessionSync's doc comments); this goroutine no
// longer creates the session itself.
//
// Goal-loop re-dispatch (SD-B3): when finishTaskRun decides the attempt is
// unmet with attempts remaining, it flips the task back to `next` and
// returns its ID so this goroutine's own trailing cleanup can re-enter
// ExecuteTask — reusing the existing goroutine/dispatch-sema machinery, not
// a new scheduler. The redispatch call is deliberately made from INSIDE the
// single combined deferred closure below, AFTER release()/cancel() have
// already run: calling ExecuteTask while this goroutine still held its own
// dispatch-sema slot would need two slots at once for an instant and could
// spuriously hit the concurrency cap.
func (te *TaskExecutor) runTask(ctx context.Context, t *task.Task, taskSessionID string, cancel context.CancelFunc, release func()) {
	var redispatchTaskID string
	defer func() {
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			if err := te.ExecuteTask(context.Background(), redispatchTaskID); err != nil {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	// Carry the task's delegation generation into the run. processTaskDirect reads
	// it back to seed the root turnState depth (so the per-agent depth gate trips
	// inside the run) and to stamp any nested task_create as generation + 1. This
	// is what bounds an A→B→A task-mode delegation chain — without it every task
	// run starts at depth 0 and the gate never trips (see maxTaskDepth).
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: mark this turn as THIS task's own executor run so
	// TaskUpdateTool can tell an in-run done-claim (staged, adjudicated below
	// by finishTaskRun) from an out-of-band one (rejected — see
	// tools.WithRunningTaskID's doc comment).
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, "")
}

// finishTaskRun handles the shared post-execution logic for both runTask and
// runTaskFromInProgress. It appends the failure/success transcript entry,
// updates the session meta, and — when the agent did not call task_update
// itself — resolves completion from the standardized TASK_STATUS/TASK_SUMMARY
// marker (ADR-043). The marker (or an explicit update_task terminal write) is
// now a CLAIM, never the terminal decision by itself (ADR-049 US-5/US-6):
//
//   - No parseable signal (FR-045): an UNMET claim — the attempt is consumed
//     and the task re-dispatches (or wakes the owner on exhaustion) — NOT an
//     immediate terminal failure.
//   - An explicit FAILURE marker, or an already-terminal `failed` status from
//     a real update_task(status:"failed") call (SD-B1): an accepted give-up —
//     terminal immediately, exactly as before this feature. There is nothing
//     to verify in a worker's own honest failure report.
//   - An explicit update_task(status:"done") call on a task WITH acceptance
//     criteria (hard tier) is now ALSO a CLAIM, not a terminal decision
//     (SD-B2, review r1 blocker C1 fix): pkg/tools/task.go's TaskUpdateTool
//     stages it as Task.PendingJudgeClaim instead of writing a terminal
//     `done` — no DAG-advance, no onComplete, at the tool-call boundary. The
//     block below detects PendingJudgeClaim and routes it through
//     adjudicateClaim exactly like a SUCCESS marker. A criteria-less
//     (soft-tier) explicit done write is UNCHANGED — trusted immediately,
//     current.Status is already terminal `done`, and the IsTerminal check
//     just below handles it exactly as before this feature.
//   - A SUCCESS marker (no explicit terminal tool call, or no
//     PendingJudgeClaim staged): a CLAIM — routed to the evidence-ladder
//     judge (judge.go) before it may become terminal `done` (US-5/US-6, the
//     #1 self-certification failure mode this feature closes).
//
// Scratchpad tasks (FR-048/D5) are exempt from the goal loop entirely: every
// branch below trusts the marker directly, exactly as today, for a
// Scratchpad task.
//
// Returns a non-empty redispatchTaskID when the caller (runTask/
// runTaskFromInProgress) should re-enter ExecuteTask for another attempt
// (SD-B3) — see those callers' own doc comments for why the actual
// ExecuteTask call happens AFTER this function returns, not from inside it.
//
// logSuffix is appended to the "Agent execution failed" log message so
// callers can be identified in structured logs (e.g. " (StartTaskNow path)").
//
// Do NOT merge the pre-execution session-setup blocks of runTask and
// runTaskFromInProgress: runTask logs-and-continues on NewSession failure while
// runTaskFromInProgress aborts; that divergence is intentional and load-bearing.
func (te *TaskExecutor) finishTaskRun(
	ctx context.Context, t *task.Task, taskSessionID, resp string, err error, logSuffix string,
) (redispatchTaskID string) {
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)

	if err != nil {
		logger.ErrorCF("task_executor", "Agent execution failed"+logSuffix,
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "error": err.Error()})
		if taskSessionID != "" && sessStore != nil {
			if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        t.ID + "-error",
				Role:      "assistant",
				Content:   fmt.Sprintf("Task execution failed: %v", err),
				Status:    "error",
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				logger.WarnCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": t.ID, "error": appendErr.Error()})
			}
			status := session.StatusInterrupted
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &status}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		te.failTask(t.ID, fmt.Sprintf("execution error: %v", err))
		failedTask := *t
		failedTask.Status = task.StatusFailed
		failedTask.Result = fmt.Sprintf("execution error: %v", err)
		te.notifySourceChannel(&failedTask)
		return ""
	}

	if taskSessionID != "" && resp != "" && sessStore != nil {
		if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-response",
			Role:      "assistant",
			Content:   resp,
			Timestamp: time.Now().UTC(),
		}); appendErr != nil {
			logger.WarnCF("task_executor", "Transcript write failed",
				map[string]any{"task_id": t.ID, "error": appendErr.Error()})
		}
	}

	// Re-read so we see any explicit update_task write the agent made mid-run.
	current, lerr := te.store.Get(t.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": t.ID, "error": lerr.Error()})
		return ""
	}

	if task.IsTerminal(current.Status) {
		// An explicit update_task(done|failed) call already decided (and, for
		// a criteria-less `done`, already fired DAG-advance at the tool-call
		// boundary) — trust it directly, exactly as today. A hard-tier done
		// claim never reaches this branch: TaskUpdateTool deliberately leaves
		// Status non-terminal for that case (see the PendingJudgeClaim check
		// below).
		if taskSessionID != "" && sessStore != nil {
			statusCompleted := session.StatusArchived
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusCompleted}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		te.notifySourceChannel(current)
		return ""
	}

	if current.PendingJudgeClaim != "" {
		// SD-B2/review r1 C1: an explicit update_task(status:"done") call on a
		// task WITH acceptance criteria was staged here by TaskUpdateTool
		// instead of writing a terminal `done` directly. Clear the staging
		// field up front (adjudication is about to consume it one way or
		// another; leaving it set would leak into the next attempt/read) and
		// route through the SAME evidence-ladder judge path a TASK_STATUS
		// marker uses — this explicit claim takes priority over any marker
		// text that might also be present in resp, so the marker is not
		// parsed at all in this branch.
		claimText := current.PendingJudgeClaim
		cleared := ""
		if _, cerr := te.store.Update(t.ID, task.Patch{PendingJudgeClaim: &cleared}); cerr != nil {
			logger.WarnCF("task_executor", "Could not clear pending judge claim",
				map[string]any{"task_id": t.ID, "error": cerr.Error()})
		}
		return te.adjudicateClaim(ctx, current, taskSessionID, claimText)
	}

	// Agent did not call task_update — no explicit signal, or an explicit
	// non-terminal write. Parse the agent's final output for the standardized
	// TASK_STATUS completion marker instead (ADR-043), uniform across native
	// and external-CLI (subagent_3p) dispatch — see the marker parser's own
	// doc comment for why an external-CLI worker ALWAYS lands here.
	signal := parseTaskCompletionSignal(resp)
	if !signal.Found() {
		logger.WarnCF("task_executor",
			"agent finished with no parseable TASK_STATUS completion signal",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
		rawOutput := resp
		if strings.TrimSpace(rawOutput) == "" {
			rawOutput = "(agent produced no output)"
		} else {
			rawOutput = truncateTaskOutput(rawOutput)
		}
		reason := fmt.Sprintf(
			"agent finished without a completion signal (TASK_STATUS line) — review the run "+
				"transcript and re-run; raw output follows:\n\n%s",
			rawOutput,
		)
		if current.Scratchpad {
			// FR-048: Scratchpad (set_todos) tasks are exempt from the goal
			// loop entirely — today's exact fail-closed-immediately behavior.
			te.completeTaskWithResult(current, taskSessionID, false, reason)
			return ""
		}
		// FR-045: for a real task, no signal is an UNMET claim (attempt
		// consumed) — NOT an immediate terminal failure.
		return te.consumeAttemptOrExhaust(ctx, current, taskSessionID, reason, nil)
	}

	if current.Scratchpad || signal.Status() == task.StatusFailed {
		// FR-048 (Scratchpad: exempt from the goal loop entirely, even for a
		// success marker — trust it directly) OR SD-B1 (an explicit failure
		// marker is an accepted give-up: terminal immediately, no judge).
		te.completeTaskWithResult(current, taskSessionID, signal.Status() == task.StatusDone, signal.Result)
		return ""
	}

	// signal.Status() == task.StatusDone, non-Scratchpad: a success CLAIM —
	// route to the evidence-ladder judge (US-5/US-6).
	return te.adjudicateClaim(ctx, current, taskSessionID, signal.Result)
}

// adjudicateClaim routes a worker's SUCCESS claim through the evidence-ladder
// judge (US-5/US-6, judge.go). Empty Criteria falls back to the ADR-049 D5
// soft tier (SoftTierCriterion: judge against Prompt, else title+description).
// When the soft tier applies AND the Judge System Agent is not registered at
// all (never true post-boot in production, since coreagent.SeedConfig always
// seeds it — only reachable from a raw pkg/agent harness that never ran
// SeedConfig), the claim is trusted directly rather than paused forever: a
// missing Judge in that specific combination is a structural/environment
// gap, not a transient D7 "unavailable" cause.
func (te *TaskExecutor) adjudicateClaim(
	ctx context.Context, t *task.Task, taskSessionID, claimSummary string,
) (redispatchTaskID string) {
	if strings.TrimSpace(claimSummary) == "" {
		// ADR-052 (7-reviewer gate item 3): an empty completion claim has
		// nothing to adjudicate — fail closed BEFORE any verifier dispatch,
		// never a full (potentially D7-backoff-stalled) verifier turn for a
		// claim carrying no content to check evidence against.
		reason := "worker reported a completion signal with an empty claim summary — " +
			"nothing to adjudicate (fail-closed, no verifier dispatched)"
		return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, reason, nil)
	}

	criteria := t.Criteria
	usedSoftTier := false
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(t.Title, t.Description, t.Prompt); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
			usedSoftTier = true
		}
	}

	if len(criteria) == 0 {
		// Structurally empty task (no criteria, no prompt, no title/description
		// text worth judging) — nothing to judge; trust the claim.
		te.completeTaskWithResult(t, taskSessionID, true, claimSummary)
		return ""
	}

	if usedSoftTier {
		if _, ok := te.agentLoop.GetRegistry().GetAgent(string(coreagent.IDJudge)); !ok {
			logger.WarnCF("task_executor",
				"goal-loop: Judge System Agent not configured; trusting the worker's claim "+
					"directly for this criteria-less task",
				map[string]any{"task_id": t.ID})
			te.completeTaskWithResult(t, taskSessionID, true, claimSummary)
			return ""
		}
	}

	result := te.agentLoop.JudgeCriteria(ctx, JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          t.ID,
		AssigneeAgentID: t.AgentID,
		Criteria:        criteria,
		Attempt:         t.AttemptCount + 1,
		ClaimText:       claimSummary,
	})

	if result.Unavailable {
		// D7: the judge itself is unavailable and JudgeCriteria's own
		// internal backoff loop gave up only because ctx was canceled — do
		// NOT consume the attempt or record a verdict; abandon this run.
		logger.WarnCF("task_executor",
			"goal-loop: judge cycle abandoned (context canceled during backoff)",
			map[string]any{"task_id": t.ID, "reason": result.Reason})
		return ""
	}

	// FR-014 (member-path parity with plan_engine.go's verdictStillApplicable
	// — item 4 of the 7-reviewer gate): JudgeCriteria's verifier turn ran
	// OUTSIDE any lock, by design (the SAME reason the plan-round path
	// decouples into its own goroutine) — a concurrent Stop
	// (PlanEngine.StopTask/StopPlan) may have already moved this task out of
	// in_progress while the judge call was in flight. Re-check BEFORE
	// writing the verdict transcript or applying its outcome: a task the
	// Stop fan-out already cancelled/terminated must never have that outcome
	// silently overwritten by a stale verdict, and must never have an
	// attempt "consumed" for a run that was actually cancelled.
	if !te.taskVerdictStillApplicable(t.ID) {
		logger.InfoCF("task_executor",
			"judge verdict dropped: task left in_progress during adjudication (Stop landed concurrently)",
			map[string]any{"task_id": t.ID})
		return ""
	}

	verdict := result.Verdict
	te.writeJudgeVerdictTranscript(t, taskSessionID, verdict)

	if verdict.Met {
		te.completeTaskWithResult(t, taskSessionID, true, claimSummary)
		return ""
	}

	return te.consumeAttemptOrExhaust(ctx, t, taskSessionID, claimSummary, verdict)
}

// taskVerdictStillApplicable re-reads taskID's CURRENT status directly from
// the store (FR-014) and reports whether a judge verdict computed moments
// ago is still safe to apply: the task must still be in_progress. A store
// read failure fails SAFE (returns false, drops the verdict) rather than
// risking a stale-verdict overwrite on an unreadable/uncertain state.
func (te *TaskExecutor) taskVerdictStillApplicable(taskID string) bool {
	current, err := te.store.Get(taskID)
	if err != nil {
		logger.WarnCF("task_executor",
			"adjudicateClaim: could not re-read task before applying verdict (fail-safe: dropping verdict)",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return false
	}
	return current.Status == task.StatusInProgress
}

// consumeAttemptOrExhaust increments+persists Task.AttemptCount (server-set,
// FR-042/R4/C17), and either re-dispatches (attempts remain) with steering
// fed forward into buildPrompt (FR-043), or — once the attempt reaches
// EffectiveTaskMaxAttempts — marks the task terminal `failed`, writes a
// graceful wind-down handover to BOTH the task record and the owning session
// transcript (NFR-3/SD-B9), and wakes the owner via the async-notifier
// (FR-044). verdict is nil for a no-signal unmet outcome (nothing to judge)
// and non-nil for a judge-adjudicated unmet outcome.
//
// FR-047: the hard ceiling (2x the configured attempt bound) is enforced
// independently of the normal maxAttempts gate — belt-and-suspenders so a
// pending re-dispatch can never loop past it "regardless of pending
// re-dispatch or interrupt state", even though under this function's own
// invariants (it is the sole writer of AttemptCount) the two gates always
// coincide.
func (te *TaskExecutor) consumeAttemptOrExhaust(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	claimSummary string,
	verdict *task.JudgeVerdict,
) (redispatchTaskID string) {
	var planningCfg config.PlanningConfig
	if cfg := te.agentLoop.GetConfig(); cfg != nil {
		planningCfg = cfg.Planning
	}
	maxAttempts := planningCfg.EffectiveTaskMaxAttempts(t.MaxAttempts)
	hardCeiling := 2 * maxAttempts

	newAttempt := t.AttemptCount + 1
	nextStatus := task.StatusNext
	updated, uerr := te.store.Update(t.ID, task.Patch{AttemptCount: &newAttempt, Status: &nextStatus})
	if uerr != nil {
		logger.ErrorCF("task_executor",
			"goal-loop: could not persist attempt increment; failing the run closed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
		te.failTask(t.ID, fmt.Sprintf("goal-loop: could not persist attempt increment: %v", uerr))
		return ""
	}

	if newAttempt < maxAttempts && newAttempt <= hardCeiling {
		te.writeSteeringPrompt(updated, taskSessionID, claimSummary, verdict)
		return updated.ID
	}

	handover := buildHandover(updated, claimSummary, verdict, maxAttempts)
	te.completeTaskWithResult(updated, taskSessionID, false, handover)
	te.wakeOwnerAttemptsExhausted(updated, taskSessionID, handover)
	return ""
}

// writeSteeringPrompt persists the judge's (or the no-signal reminder's)
// steering text so the NEXT attempt's buildPrompt call carries it forward
// (FR-043, evaluator-optimizer pattern). t.Result is repurposed as the
// in-flight steering carrier between attempts — it is NOT yet the FINAL
// result while the goal loop is still running; the terminal write
// (completeTaskWithResult) always overwrites it with the real final result.
func (te *TaskExecutor) writeSteeringPrompt(
	t *task.Task, taskSessionID, claimSummary string, verdict *task.JudgeVerdict,
) {
	steering := buildSteeringText(claimSummary, verdict)
	if _, uerr := te.store.Update(t.ID, task.Patch{Result: &steering}); uerr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not persist steering for re-dispatch",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
	}
	if taskSessionID == "" {
		return
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		return
	}
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-steering-%d", t.ID, t.AttemptCount+1),
		Role:      "system",
		Content:   steering,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.WarnCF("task_executor", "goal-loop: steering transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
	}
}

// buildSteeringText renders the feedback fed forward into the next attempt.
func buildSteeringText(claimSummary string, verdict *task.JudgeVerdict) string {
	if verdict == nil {
		// No-signal case: the composed "no completion signal" reason IS the
		// steering — there is no per-criterion breakdown to report.
		return claimSummary
	}
	var sb strings.Builder
	sb.WriteString("The judge reviewed your last attempt and found it UNMET:\n")
	for _, c := range verdict.PerCriterion {
		if !c.Met {
			fmt.Fprintf(&sb, "- criterion %s: %s\n", c.CriterionID, c.Reason)
		}
	}
	return sb.String()
}

// buildHandover renders the graceful wind-down summary written to the task
// Result and the owning session transcript when the goal loop's attempts are
// exhausted (FR-044/NFR-3/SD-B9).
func buildHandover(t *task.Task, claimSummary string, verdict *task.JudgeVerdict, maxAttempts int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb,
		"Goal loop exhausted after %d attempt(s) (max %d) without a judge-confirmed success.\n\n",
		t.AttemptCount, maxAttempts,
	)
	if verdict != nil {
		sb.WriteString("Last judge verdict:\n")
		for _, c := range verdict.PerCriterion {
			status := "met"
			if !c.Met {
				status = "UNMET"
			}
			fmt.Fprintf(&sb, "- criterion %s: %s (%s)\n", c.CriterionID, status, c.Reason)
		}
	} else {
		sb.WriteString("Last attempt outcome:\n")
		sb.WriteString(claimSummary)
		sb.WriteString("\n")
	}
	sb.WriteString(
		"\nProgress/remaining/blockers: review the run transcript for details; the task has " +
			"been marked failed and its owner notified.",
	)
	return sb.String()
}

// wakeOwnerAttemptsExhausted wakes the task's owning agent via the
// async-notifier (FR-044/async_notifier.go) once the goal loop's attempts
// are exhausted. Falls back to a "system"/"task:<id>" destination when the
// task has no SourceChannel/SourceChatID (e.g. a board/REST-created task) —
// AsyncNotifier.Notify rejects an empty destination outright (FR-N7).
func (te *TaskExecutor) wakeOwnerAttemptsExhausted(t *task.Task, taskSessionID, handover string) {
	channel, chatID := t.SourceChannel, t.SourceChatID
	if channel == "" || chatID == "" {
		channel, chatID = "system", "task:"+t.ID
	}
	notifyCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content := fmt.Sprintf(
		"Task %q (%s) exhausted its goal-loop attempts and needs your attention.\n\n%s",
		t.Title, t.ID, handover,
	)
	if notifyErr := te.agentLoop.asyncNotifier.Notify(notifyCtx, AsyncNotifyEvent{
		Channel:             channel,
		ChatID:              chatID,
		AgentID:             t.AgentID,
		TranscriptSessionID: taskSessionID,
		SourceKind:          "task_goal_loop",
		Content:             content,
	}); notifyErr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not wake owner on attempts-exhausted",
			map[string]any{"task_id": t.ID, "error": notifyErr.Error()})
	}
}

// writeJudgeVerdictTranscript writes verdict as a dedicated judge_verdict
// transcript entry (FR-056, EntryTypeJudgeVerdict) alongside the worker's own
// ADR-043 completion marker so the two can never silently disagree (ADR §6).
func (te *TaskExecutor) writeJudgeVerdictTranscript(t *task.Task, taskSessionID string, verdict *task.JudgeVerdict) {
	if taskSessionID == "" || verdict == nil {
		return
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		return
	}
	payload, merr := json.Marshal(verdict)
	if merr != nil {
		logger.WarnCF("task_executor", "goal-loop: could not marshal judge verdict for transcript",
			map[string]any{"task_id": t.ID, "error": merr.Error()})
		return
	}
	if appendErr := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("%s-judge-%d", t.ID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		logger.WarnCF("task_executor", "goal-loop: judge verdict transcript write failed",
			map[string]any{"task_id": t.ID, "error": appendErr.Error()})
	}
}

// completeTaskWithResult marks task t terminal — Done when success is true,
// Failed otherwise — with the given result text, archives its session (if
// any), and runs the shared post-completion hooks (status-changed event,
// parent follow-up, and — for a Done status only, per onTaskComplete's own
// gate — blocked-dependent advance) plus source-channel notification. Shared
// by finishTaskRun's three non-error completion paths (explicit success
// marker, explicit failure marker, fail-closed no-signal). NOT used by the
// hard "agent execution error" branch above, which keeps failTask's existing,
// deliberately narrower shape (no parent follow-up) — that asymmetry predates
// this change and is out of scope here.
//
// The parameter is deliberately a plain bool, not a task.Status (review C1):
// completeTaskWithResult only ever writes one of the two terminal statuses,
// so narrowing the signature to "success or not" makes writing a non-terminal
// status here a compile error instead of a reviewable-but-possible mistake.
func (te *TaskExecutor) completeTaskWithResult(t *task.Task, taskSessionID string, success bool, result string) {
	status := task.StatusDone
	if !success {
		status = task.StatusFailed
	}
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	now := time.Now().UTC().Format(time.RFC3339)
	final, uerr := te.store.Update(t.ID, task.Patch{
		Status:      &status,
		Result:      &result,
		CompletedAt: &now,
	})
	if uerr != nil {
		// Known, accepted limitation (ADR-043 §3): the task is left stuck at
		// whatever non-terminal status it had before this call (typically
		// in_progress) — we do not retry and we do not force a second write
		// with a synthesized failure status. A persistent store failure here
		// (disk full, permissions, corrupt file) would very likely fail a
		// retry identically, and forcing a follow-up write risks compounding
		// a partially-written/corrupted task file rather than recovering it.
		// An operator must notice this ERROR log and manually resolve the
		// stuck task (e.g. via a direct store fix or `omnipus` CLI update).
		logger.ErrorCF("task_executor", "Completion update failed",
			map[string]any{"task_id": t.ID, "status": string(status), "error": uerr.Error()})
		return
	}
	if taskSessionID != "" && sessStore != nil {
		statusArchived := session.StatusArchived
		if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusArchived}); setErr != nil {
			logger.WarnCF("task_executor", "Meta update failed",
				map[string]any{"task_id": t.ID, "error": setErr.Error()})
		}
	}
	te.onTaskComplete(final)
	te.notifySourceChannel(final)
}

// notifySourceChannel sends a compact task result back to the originating
// channel. Only sends for terminal statuses.
func (te *TaskExecutor) notifySourceChannel(t *task.Task) {
	if t.SourceChannel == "" || t.SourceChatID == "" {
		return
	}
	if te.agentLoop.bus == nil {
		logger.WarnCF("task_executor", "Cannot notify source channel — message bus is nil",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel})
		return
	}
	if !task.IsTerminal(t.Status) {
		return
	}

	msg := fmt.Sprintf("**%s** — %s", t.Title, t.Status)
	if t.Result != "" {
		result := t.Result
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		msg += "\n\n" + result
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := te.agentLoop.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
		Channel: t.SourceChannel,
		ChatID:  t.SourceChatID,
		Content: msg,
	}); err != nil {
		logger.WarnCF("task_executor", "Could not notify source channel",
			map[string]any{"task_id": t.ID, "channel": t.SourceChannel, "error": err.Error()})
	}
}

// buildPrompt constructs the prompt sent to the agent for a task.
//
// ADR-043 (task completion contract): every dispatch kind is instructed to
// end its final message with a standardized TASK_STATUS/TASK_SUMMARY marker
// — this is the fail-closed fallback finishTaskRun parses when there is no
// explicit task_update call. Native agents ADDITIONALLY get the task_update
// instruction: for a task WITH acceptance criteria, an explicit call and the
// marker now converge on the SAME evidence-ladder judge path (review r1 C1
// fix, SD-B2) — neither "wins outright" over the other — so this instruction
// deliberately does NOT claim the tool call bypasses adjudication.
//
// Echo-safety (review B1/B2): with the marker grammar now tolerant of
// trailing content (parseTaskCompletionSignal / taskStatusLineRe), a model
// that echoes this instruction block VERBATIM as its own "final message"
// (without ever emitting a real signal) must not resolve to verdictSuccess —
// that would be a false success from the instruction text itself, not from
// anything the agent actually reported. The two TASK_STATUS lines below are
// listed as separate, clean lines with the FAILURE variant deliberately
// LAST: parseTaskCompletionSignal's "last occurrence wins" rule means a raw
// echo of just this block resolves to verdictFailure (the safe direction),
// never verdictSuccess. TestBuildPrompt_InstructionEchoNeverResolvesToSuccess
// (task_completion_contract_test.go) pins this invariant for both dispatch
// kinds — do not reorder the two status lines without re-verifying it holds.
//
// FIX 3 (7-reviewer gate, prompt/capability mismatch, predates ADR-043): the
// task_update instruction stays dispatch-aware. A subagent_3p (external-CLI)
// worker's tool registry is its own CLI's, never Omnipus's — it has no
// task_update tool wired at all (see processTaskDirectExternalCLI's doc
// comment, loop.go) — so telling it to call the tool describes a capability
// it structurally cannot use; it gets the marker instruction only.
func (te *TaskExecutor) buildPrompt(t *task.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Task: %s\n\n", t.Title))
	if t.Prompt != "" {
		sb.WriteString(t.Prompt)
		sb.WriteString("\n\n")
	}
	// FR-043: on a re-dispatch (attempt >= 2), carry the previous attempt's
	// steering forward — the judge's per-criterion unmet reasons, or the
	// no-signal reminder (evaluator-optimizer pattern, ADR D2). t.Result is
	// repurposed as the in-flight steering carrier between attempts (see
	// writeSteeringPrompt) — it is NOT yet the FINAL result while the goal
	// loop is still running; completeTaskWithResult always overwrites it with
	// the real final result at the end of the loop. Attempt 1 (AttemptCount
	// == 0) never has steering, so first-attempt prompts are unaffected.
	if t.AttemptCount > 0 && t.Result != "" {
		sb.WriteString(fmt.Sprintf("## Feedback from attempt %d — address this before re-claiming success:\n", t.AttemptCount))
		sb.WriteString(t.Result)
		sb.WriteString("\n\n")
	}
	sb.WriteString(fmt.Sprintf("Priority: %d (1=highest, 5=lowest)\n", t.EffectivePriority()))
	sb.WriteString(fmt.Sprintf("Task ID: %s\n\n", t.ID))

	sb.WriteString("When you are done, end your final message with ONE of the two status lines " +
		"below (never both), plus an optional one-line summary:\n")
	sb.WriteString("  " + taskStatusLabel + ": success\n")
	sb.WriteString("  " + taskStatusLabel + ": failure\n")
	sb.WriteString("  " + taskSummaryLabel + ": <one-paragraph summary of the outcome>\n")

	if te.dispatchesExternalCLI(t.AgentID) {
		return sb.String()
	}

	// NOTE: the tool's real registered name is "update_task" (pkg/tools/task.go
	// TaskUpdateTool.Name()) — this instruction was previously misnamed
	// "task_update" (a name no registered tool answers to); fixed alongside the
	// B1 rewrite since this block was already being touched.
	sb.WriteString("You may also call `update_task` explicitly when you finish " +
		"(a task with acceptance criteria is adjudicated by the evidence-ladder judge either way — " +
		"calling the tool does not skip that review):\n")
	sb.WriteString(fmt.Sprintf("  task_id: %q\n", t.ID))
	sb.WriteString("  status: \"done\" (or \"failed\" if unsuccessful)\n")
	sb.WriteString("  result: a brief summary of what was accomplished\n")
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

// onTaskComplete handles post-completion logic: parent notification + the
// blocked_by auto-advance (dispatch tasks whose deps are now all done).
func (te *TaskExecutor) onTaskComplete(t *task.Task) {
	te.emitStatusChanged(t, t.Status)

	if t.ParentTaskID != "" {
		te.notifyParentIfAllSiblingsDone(t.ParentTaskID)
	}

	// Only a `done` task unblocks downstream tasks (a `failed` dep does not).
	if t.Status != task.StatusDone {
		return
	}
	// Move dependents blocked→next, then attempt to dispatch the ready ones.
	if _, err := te.store.AdvanceBlockedDependents(t.ID); err != nil {
		logger.WarnCF("task_executor", "Could not advance blocked dependents",
			map[string]any{"completed_task_id": t.ID, "error": err.Error()})
	}
	te.advanceBlockedTasks(context.Background(), t.ID)
}

// notifyParentIfAllSiblingsDone resumes the parent agent once every child task
// of parentID has reached a terminal state. Safe under concurrent sibling
// completions via the atomic FollowedUp claim.
func (te *TaskExecutor) notifyParentIfAllSiblingsDone(parentID string) {
	siblings, err := te.store.List(task.Filter{ParentTaskID: parentID, ParentTaskIDSet: true})
	if err != nil {
		logger.WarnCF("task_executor", "Could not list siblings",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	for _, s := range siblings {
		if !task.IsTerminal(s.Status) {
			return
		}
	}

	parent, err := te.store.Get(parentID)
	if err != nil {
		logger.WarnCF("task_executor", "Could not load parent task",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	if parent.Status != task.StatusInProgress {
		return
	}

	claimed, claimErr := te.store.ClaimParentFollowUp(parent.ID)
	if claimErr != nil {
		logger.WarnCF("task_executor", "Could not claim parent follow-up",
			map[string]any{"parent_id": parent.ID, "error": claimErr.Error()})
		return
	}
	if !claimed {
		return
	}

	if te.parentFollowUp != nil {
		te.parentFollowUp(parent.ID)
		return
	}

	summary := te.buildChildSummary(siblings)
	sessionKey := fmt.Sprintf("agent:%s:task:%s", parent.AgentID, parent.ID)
	followUp := fmt.Sprintf("All child tasks of task %q have completed.\n\n%s", parent.ID, summary)
	parentChatID := "task:" + parent.ID
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("task_executor", "Panic in parent follow-up",
					map[string]any{"parent_id": parent.ID, "panic": r})
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, ferr := te.agentLoop.processTaskDirect(ctx, parent.AgentID, followUp, sessionKey, parentChatID)
		if ferr != nil {
			logger.WarnCF("task_executor", "Parent follow-up failed",
				map[string]any{"parent_id": parent.ID, "error": ferr.Error()})
		}
	}()
}

// readyBlockedCandidates returns the IDs of all `next` tasks that list
// completedTaskID as a blocker AND whose ENTIRE blocked_by set is now `done`.
func (te *TaskExecutor) readyBlockedCandidates(completedTaskID string) []string {
	candidates, err := te.store.List(task.Filter{
		Status:      task.StatusNext,
		BlockedByID: completedTaskID,
	})
	if err != nil {
		logger.WarnCF("task_executor", "Orchestrator: could not scan blocked tasks",
			map[string]any{"completed_task_id": completedTaskID, "error": err.Error()})
		return nil
	}

	var ready []string
	for i := range candidates {
		t := &candidates[i]
		allSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				allSatisfied = false
				break
			}
		}
		if allSatisfied {
			ready = append(ready, t.ID)
		}
	}
	return ready
}

// advanceBlockedTasks dispatches every `next` task whose full dependency set is
// now satisfied by the completion of completedTaskID.
func (te *TaskExecutor) advanceBlockedTasks(ctx context.Context, completedTaskID string) {
	for _, taskID := range te.readyBlockedCandidates(completedTaskID) {
		if err := te.ExecuteTask(ctx, taskID); err != nil {
			logger.WarnCF("task_executor", "Orchestrator: advance dispatch failed",
				map[string]any{"task_id": taskID, "error": err.Error()})
		} else {
			logger.InfoCF("task_executor", "Orchestrator: advanced blocked task",
				map[string]any{"task_id": taskID, "unblocked_by": completedTaskID})
		}
	}
}

// buildChildSummary produces a markdown summary of all child task results.
func (te *TaskExecutor) buildChildSummary(children []task.Task) string {
	var sb strings.Builder
	sb.WriteString("## Child Task Results\n\n")
	for _, c := range children {
		sb.WriteString(fmt.Sprintf("- **%s** (status: %s)", c.Title, c.Status))
		if c.Result != "" {
			sb.WriteString(fmt.Sprintf(": %s", c.Result))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// emitStatusChanged publishes a task_status_changed event onto the agent event
// bus. Best-effort; a nil agentLoop is silently skipped.
func (te *TaskExecutor) emitStatusChanged(t *task.Task, status task.Status) {
	if te.agentLoop == nil {
		return
	}
	sessionID := t.SessionID
	if sessionID == "" {
		sessionID = "task:" + t.ID
	}
	te.agentLoop.EmitTaskStatusChanged(TaskStatusChangedPayload{
		TaskID:    t.ID,
		Status:    string(status),
		SessionID: sessionID,
		AgentID:   t.AgentID,
	})
}

// failTask marks a task as failed with the given reason.
func (te *TaskExecutor) failTask(taskID, reason string) {
	now := time.Now().UTC().Format(time.RFC3339)
	failed := task.StatusFailed
	updated, err := te.store.Update(taskID, task.Patch{
		Status:      &failed,
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		logger.ErrorCF("task_executor", "Could not mark task failed",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return
	}
	te.emitStatusChanged(updated, task.StatusFailed)
}

// StartTaskNow creates the task session, sets session_id on the task, registers
// the cancel in the running map, and launches the agent goroutine — all without
// requiring the task to be in `next` first (the caller has already transitioned
// it to `in_progress` via a PATCH). It is the path taken when the UI hits
// "Run" on a task that already has an assigned agent.
//
// Idempotency: if the task already has a SessionID the call is a no-op and
// returns the existing session ID immediately without launching a second agent.
//
// Returns the session ID that was created (or already existed) on success, or
// an empty string and an error when the task cannot be found, already has no
// agent, or the concurrency cap is exhausted.
func (te *TaskExecutor) StartTaskNow(ctx context.Context, taskID string) (string, error) {
	t, err := te.store.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("task_executor: StartTaskNow get task %q: %w", taskID, err)
	}
	if t.AgentID == "" {
		return "", fmt.Errorf("task_executor: StartTaskNow: task %q has no agent assigned", taskID)
	}

	// Idempotency guard: if a session already exists, don't create another one.
	if t.SessionID != "" {
		return t.SessionID, nil
	}

	// Atomically claim the slot: under a single te.mu critical section, re-check
	// whether a goroutine is already running AND insert a sentinel cancel so
	// competing concurrent callers observe the slot as taken before we unlock.
	// This closes the TOCTOU window where two concurrent StartTaskNow calls could
	// both pass the running-check before either one registered its goroutine.
	//
	// A nil sentinel marks "slot reserved, cancel not yet set". The goroutine
	// replaces it with the real cancel before returning. If setup fails we delete
	// the slot so the task is not permanently locked.
	te.mu.Lock()
	if _, alreadyRunning := te.running[taskID]; alreadyRunning {
		te.mu.Unlock()
		// A goroutine is live (or starting up); the session_id may have been
		// written by now — re-read.
		fresh, rerr := te.store.Get(taskID)
		if rerr == nil && fresh.SessionID != "" {
			return fresh.SessionID, nil
		}
		return "", fmt.Errorf("task_executor: StartTaskNow: task %q goroutine already running", taskID)
	}
	// Reserve the slot explicitly so competing callers bail at the check above.
	// reserved=true, cancel=nil: slot is claimed but the goroutine has not started yet.
	te.running[taskID] = &taskSlot{reserved: true}
	te.mu.Unlock()

	// releaseSlot removes the reservation if setup fails so future callers can
	// retry. A no-op after the goroutine successfully replaces the sentinel.
	slotReleased := false
	releaseSlot := func() {
		if !slotReleased {
			slotReleased = true
			te.mu.Lock()
			delete(te.running, taskID)
			te.mu.Unlock()
		}
	}
	defer releaseSlot()

	// Check that the assigned agent is known.
	registry := te.agentLoop.GetRegistry()
	if registry == nil {
		return "", fmt.Errorf("task_executor: StartTaskNow: agent registry is not available")
	}
	if _, ok := registry.GetAgent(t.AgentID); !ok {
		return "", fmt.Errorf("task_executor: StartTaskNow: agent %q not found for task %q", t.AgentID, taskID)
	}

	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		return "", fmt.Errorf(
			"%w (%d/%d in flight), retry later",
			ErrDispatchCapReached,
			te.dispatchSema.InFlight(), te.dispatchSema.Cap(),
		)
	}

	// Create the session synchronously so we can return the session_id to the
	// caller before the goroutine starts.
	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	var taskSessionID string
	if sessStore != nil {
		meta, sessErr := sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)
		if sessErr != nil {
			release()
			return "", fmt.Errorf("task_executor: StartTaskNow: create session for task %q: %w", taskID, sessErr)
		}
		taskSessionID = meta.ID

		title := t.Title
		tid := t.ID
		wsID := t.WorkspaceID
		metaPatch := session.MetaPatch{Title: &title, TaskID: &tid}
		if wsID != "" {
			metaPatch.WorkspaceID = &wsID
		}
		if setErr := sessStore.SetMeta(meta.ID, metaPatch); setErr != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: could not set task session meta",
				map[string]any{"task_id": taskID, "error": setErr.Error()})
		}
		updated, updateErr := te.store.Update(taskID, task.Patch{SessionID: &taskSessionID})
		if updateErr != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: could not persist session_id on task",
				map[string]any{"task_id": taskID, "session_id": taskSessionID, "error": updateErr.Error()})
		} else {
			t = updated
		}
		if err := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
			ID:        t.ID + "-prompt",
			Role:      "user",
			Content:   te.buildPrompt(t),
			Timestamp: time.Now().UTC(),
		}); err != nil {
			logger.ErrorCF("task_executor", "StartTaskNow: transcript write failed",
				map[string]any{"task_id": taskID, "error": err.Error()})
		}
	} else {
		logger.WarnCF("task_executor", "StartTaskNow: no agent store found, task will have no session",
			map[string]any{"task_id": taskID, "agent_id": t.AgentID})
	}

	te.emitStatusChanged(t, task.StatusInProgress)

	// Detach from the caller's context (typically an HTTP request context that
	// gets canceled as soon as the response is sent). The goroutine must outlive
	// the HTTP request; the explicit cancel stored in te.running[taskID] is the
	// intended cancellation path (a future "cancel task" API).
	//
	// Replace the reserved slot (inserted above, cancel==nil, reserved==true)
	// with a live slot (cancel set, reserved==false) under the same mutex so
	// any concurrent reader always observes a consistent, named state.
	taskCtx, cancel := context.WithCancel(context.Background())
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()
	slotReleased = true // goroutine now owns the slot; don't let releaseSlot clear it

	go te.runTaskFromInProgress(taskCtx, t, taskSessionID, cancel, release)
	return taskSessionID, nil
}

// runTaskFromInProgress is the goroutine body for tasks launched via
// StartTaskNow. The session has already been created and the session_id
// persisted; it skips the session-creation block that runTask performs and
// goes straight to execution, reusing the shared completion logic.
func (te *TaskExecutor) runTaskFromInProgress(
	ctx context.Context,
	t *task.Task,
	taskSessionID string,
	cancel context.CancelFunc,
	release func(),
) {
	var redispatchTaskID string
	defer func() {
		release()
		cancel()
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
		if redispatchTaskID != "" {
			if err := te.ExecuteTask(context.Background(), redispatchTaskID); err != nil {
				logger.WarnCF("task_executor", "goal-loop: re-dispatch failed",
					map[string]any{"task_id": redispatchTaskID, "error": err.Error()})
			}
		}
	}()

	// Test seam: when goroutineCtxHook is set, invoke it and return without
	// performing real agent execution. The hook receives the goroutine's context so
	// tests can assert it is not canceled by the originating request context.
	if te.goroutineCtxHook != nil {
		te.goroutineCtxHook(ctx, t.ID)
		return
	}

	logger.InfoCF("task_executor", "runTaskFromInProgress started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID, "session_id": taskSessionID})

	taskCtx := tools.WithAgentID(ctx, t.AgentID)
	if t.WorkspaceID != "" {
		taskCtx = tools.WithWorkspaceID(taskCtx, t.WorkspaceID)
	}
	taskCtx = tools.WithDelegationDepth(taskCtx, t.DelegationDepth)
	// review r2 Chunk 1: same in-run marker as runTask above — see
	// tools.WithRunningTaskID's doc comment.
	taskCtx = tools.WithRunningTaskID(taskCtx, t.ID)

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	redispatchTaskID = te.finishTaskRun(ctx, t, taskSessionID, resp, err, " (StartTaskNow path)")
}

// SpawnTriggeredRun dispatches a fresh run of a task that a time trigger just
// fired. The task has already been reset to `next` by Store.SpawnReset; this
// claims and dispatches it via the normal ExecuteTask path. ExecuteTask already
// guards status==next and concurrency, so no additional gate is needed here.
func (te *TaskExecutor) SpawnTriggeredRun(ctx context.Context, taskID string) error {
	return te.ExecuteTask(ctx, taskID)
}

// ResizeDispatchSema updates the global dispatch semaphore capacity.
func (te *TaskExecutor) ResizeDispatchSema(newCap int) {
	te.dispatchSema.Resize(newCap)
	logger.InfoCF("task_executor", "Dispatch semaphore resized",
		map[string]any{"new_cap": te.dispatchSema.Cap(), "in_flight": te.dispatchSema.InFlight()})
}

// DispatchSemaCap returns the current dispatch semaphore capacity.
func (te *TaskExecutor) DispatchSemaCap() int {
	return te.dispatchSema.Cap()
}

// TryAcquireDispatchSema attempts to claim one dispatch slot without blocking.
// Returns (true, release) when a slot is available; (false, nil) otherwise.
// Callers MUST call release() when done to avoid permanently exhausting the cap.
// Intended for testing and diagnostic tooling — production dispatch uses the
// internal sema path inside StartTaskNow / ExecuteTask.
func (te *TaskExecutor) TryAcquireDispatchSema() (bool, func()) {
	return te.dispatchSema.TryAcquire()
}

// CheckQueuedTasks picks the highest-priority *dispatchable* `next` task per
// agent and starts it. Called by the heartbeat service. Skips tasks whose
// blocked_by dependencies are not all `done`.
func (te *TaskExecutor) CheckQueuedTasks(ctx context.Context) {
	queued, err := te.store.List(task.Filter{Status: task.StatusNext})
	if err != nil {
		logger.WarnCF("task_executor", "Check queued tasks: list failed",
			map[string]any{"error": err.Error()})
		return
	}
	if len(queued) == 0 {
		return
	}

	type agentState struct{ picked bool }
	agentDone := make(map[string]agentState)

	for i := range queued {
		t := &queued[i]
		if t.AgentID == "" {
			continue // human-only task; not dispatchable by an agent
		}
		if agentDone[t.AgentID].picked {
			continue
		}

		depsSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != task.StatusDone {
				depsSatisfied = false
				break
			}
		}
		if !depsSatisfied {
			logger.WarnCF("task_executor", "Heartbeat: skipping blocked task, trying next",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
			continue
		}

		if err := te.ExecuteTask(ctx, t.ID); err != nil {
			logger.WarnCF("task_executor", "Heartbeat: could not start task",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
		agentDone[t.AgentID] = agentState{picked: true}
	}
}
