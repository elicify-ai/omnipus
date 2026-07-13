package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
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
// operates over the unified pkg/task store and 7-state vocabulary: a
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

	te.emitStatusChanged(claimed, task.StatusInProgress)

	taskCtx, cancel := context.WithCancel(ctx)
	te.mu.Lock()
	te.running[taskID] = &taskSlot{cancel: cancel, reserved: false}
	te.mu.Unlock()

	go te.runTask(taskCtx, t, cancel, release)
	return nil
}

// runTask executes the agent prompt and updates the task on completion.
func (te *TaskExecutor) runTask(ctx context.Context, t *task.Task, cancel context.CancelFunc, release func()) {
	defer release()
	defer cancel()
	defer func() {
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": t.ID, "agent_id": t.AgentID})

	sessStore := te.agentLoop.GetAgentStore(t.AgentID)
	if sessStore == nil {
		logger.ErrorCF("task_executor", "Agent store not found, task will have no session",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
	}

	var taskSessionID string
	if sessStore != nil {
		if meta, err := sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID); err != nil {
			logger.ErrorCF("task_executor", "Could not create task session",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		} else {
			taskSessionID = meta.ID
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
			if err := sessStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        t.ID + "-prompt",
				Role:      "user",
				Content:   te.buildPrompt(t),
				Timestamp: time.Now().UTC(),
			}); err != nil {
				logger.ErrorCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": t.ID, "error": err.Error()})
			}
		}
	}

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

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	te.finishTaskRun(t, taskSessionID, resp, err, "")
}

// finishTaskRun handles the shared post-execution logic for both runTask and
// runTaskFromInProgress. It appends the failure/success transcript entry,
// updates the session meta, and auto-completes the task to StatusDone when
// the agent did not call task_update itself.
//
// logSuffix is appended to the "Agent execution failed" log message so
// callers can be identified in structured logs (e.g. " (StartTaskNow path)").
//
// Do NOT merge the pre-execution session-setup blocks of runTask and
// runTaskFromInProgress: runTask logs-and-continues on NewSession failure while
// runTaskFromInProgress aborts; that divergence is intentional and load-bearing.
func (te *TaskExecutor) finishTaskRun(t *task.Task, taskSessionID, resp string, err error, logSuffix string) {
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
		return
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

	// Check whether the agent already called task_update (status terminal).
	current, lerr := te.store.Get(t.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": t.ID, "error": lerr.Error()})
		return
	}
	if task.IsTerminal(current.Status) {
		if taskSessionID != "" && sessStore != nil {
			statusCompleted := session.StatusArchived
			if setErr := sessStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusCompleted}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": t.ID, "error": setErr.Error()})
			}
		}
		te.notifySourceChannel(current)
		return
	}

	// Agent did not call task_update — auto-complete to done.
	//
	// FIX 8 (7-reviewer gate, false-success visibility): an external-CLI
	// (subagent_3p) worker ALWAYS lands here — it has no task_update tool to
	// call at all (buildPrompt's dispatch-aware instruction, FIX 3, doesn't
	// change that) — so a prose-level failure ("I couldn't do it") with a
	// clean process exit auto-completes to Done exactly the same as a real
	// success, and Done unblocks any blocked_by dependent task. WARN-log the
	// external-CLI case specifically (distinguishable in logs from the rare
	// native "agent forgot to call task_update" case, which this same branch
	// also reaches) so an operator scanning logs has a signal to check the
	// result text before trusting the auto-advance. This does NOT change the
	// auto-advance semantics itself — requiring an explicit success signal
	// for a 3p-assigned dependent chain is a deferred product decision (see
	// ADR-042 §3).
	if te.dispatchesExternalCLI(t.AgentID) {
		logger.WarnCF("task_executor",
			"auto-completing task to done for an external-CLI (subagent_3p) worker — "+
				"it has no task_update tool, so this is UNVERIFIED prose-level success, not a "+
				"structured completion signal; review the result before trusting downstream "+
				"blocked_by auto-advance",
			map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result := resp
	if result == "" {
		result = "Task completed"
	}
	doneStatus := task.StatusDone
	final, uerr := te.store.Update(t.ID, task.Patch{
		Status:      &doneStatus,
		Result:      &result,
		CompletedAt: &now,
	})
	if uerr != nil {
		logger.ErrorCF("task_executor", "Auto-complete task failed",
			map[string]any{"task_id": t.ID, "error": uerr.Error()})
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
// FIX 3 (7-reviewer gate, prompt/capability mismatch): the task_update
// instruction is dispatch-aware. A subagent_3p (external-CLI) worker's tool
// registry is its own CLI's, never Omnipus's — it has no task_update tool
// wired at all (see processTaskDirectExternalCLI's doc comment, loop.go) — so
// telling it to "call task_update" describes a capability it structurally
// cannot use. The native prompt (the common case) is byte-identical to
// before this fix; only the external-CLI branch differs.
func (te *TaskExecutor) buildPrompt(t *task.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Task: %s\n\n", t.Title))
	if t.Prompt != "" {
		sb.WriteString(t.Prompt)
		sb.WriteString("\n\n")
	}
	sb.WriteString(fmt.Sprintf("Priority: %d (1=highest, 5=lowest)\n", t.EffectivePriority()))
	sb.WriteString(fmt.Sprintf("Task ID: %s\n\n", t.ID))
	if te.dispatchesExternalCLI(t.AgentID) {
		sb.WriteString(
			"There is no task_update tool available to you for this task. Your final " +
				"response's text becomes the task result automatically once you finish.\n",
		)
		return sb.String()
	}
	sb.WriteString("When you have finished this task, call `task_update` with:\n")
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
	defer release()
	defer cancel()
	defer func() {
		te.mu.Lock()
		delete(te.running, t.ID)
		te.mu.Unlock()
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

	sessionKey := fmt.Sprintf("agent:%s:task:%s", t.AgentID, t.ID)
	prompt := te.buildPrompt(t)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + t.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, t.AgentID, prompt, sessionKey, taskChatID)
	te.finishTaskRun(t, taskSessionID, resp, err, " (StartTaskNow path)")
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
