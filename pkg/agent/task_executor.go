package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/taskstore"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

const (
	defaultMaxConcurrentTasksPerAgent = 3
	maxTaskDepth                      = 10
)

// TaskExecutor runs queued tasks by dispatching them to agent sessions.
type TaskExecutor struct {
	agentLoop     *AgentLoop
	store         *taskstore.TaskStore
	mu            sync.Mutex
	running       map[string]context.CancelFunc
	maxConcurrent int
	// dispatchSema gates the total number of concurrently dispatched tasks
	// across all agents. It is driven by MaxParallelAgents from cfg.Performance
	// and is independent from AdmissionController (which only gates inbound
	// user-message session workers).
	dispatchSema *DispatchSemaphore

	// parentFollowUp, when non-nil, is invoked (synchronously) in place of the
	// real "resume the parent agent" goroutine after a follow-up is successfully
	// claimed. It is a test seam ONLY — production leaves it nil and uses
	// processTaskDirect. It lets tests observe exactly how many parent follow-ups
	// fire under concurrent sibling completion without standing up a full AgentLoop.
	parentFollowUp func(parentID string)
}

// newTaskExecutor creates a TaskExecutor.
// The dispatchSema capacity is set to cfg.Performance.EffectiveMaxParallelAgents()
// so the fan-out cap is live from boot.
func newTaskExecutor(al *AgentLoop, store *taskstore.TaskStore) *TaskExecutor {
	capacity := defaultMaxConcurrentTasksPerAgent
	if al.cfg != nil {
		if eff := al.cfg.Performance.EffectiveMaxParallelAgents(); eff > 0 {
			capacity = eff
		}
	}
	return &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
		dispatchSema:  newDispatchSemaphore(capacity),
	}
}

// ExecuteTask starts executing the task identified by taskID.
// It atomically claims the task (queued→running via ClaimForRun) and dispatches
// it to the agent in a goroutine.
//
// The dispatch is gated by two concurrency controls:
//  1. Per-agent cap (maxConcurrent) — prevents a single agent from being flooded.
//  2. Global DispatchSemaphore (dispatchSema) — enforces the MaxParallelAgents
//     fan-out ceiling across ALL agents. TryAcquire is used so the call never
//     blocks; a failed acquire returns an error and the heartbeat will retry.
//
// The queued→running transition is performed via ClaimForRun, which holds the
// store mutex across the read+write, eliminating the TOCTOU race between the
// heartbeat and advanceBlockedTasks. If the task has already been claimed by a
// concurrent caller, ClaimForRun returns ErrAlreadyClaimed and ExecuteTask
// returns without dispatching a duplicate goroutine.
func (te *TaskExecutor) ExecuteTask(ctx context.Context, taskID string) error {
	// Peek at the task before acquiring resources, so we can check deps and the
	// agent cap without holding any lock. The actual status transition is done
	// atomically by ClaimForRun below.
	task, err := te.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_executor: get task %q: %w", taskID, err)
	}
	if task.Status != "queued" {
		return fmt.Errorf("task_executor: task %q is %s, not queued", taskID, task.Status)
	}

	// Guard: do not dispatch a task that still has unsatisfied dependencies.
	if len(task.BlockedBy) > 0 {
		for _, depID := range task.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != "completed" {
				return fmt.Errorf("task_executor: task %q is blocked by %q (not completed)", taskID, depID)
			}
		}
	}

	// Global dispatch semaphore — gate the total fan-out across all agents.
	// Checked early (before registry lookup) so capacity limits are enforced
	// without touching agent state. TryAcquire is non-blocking: the heartbeat
	// retries on the next tick when the cap is reached.
	ok, release := te.dispatchSema.TryAcquire()
	if !ok {
		return fmt.Errorf(
			"task_executor: global dispatch cap reached (%d/%d in flight), retry later",
			te.dispatchSema.InFlight(), te.dispatchSema.Cap(),
		)
	}

	registry := te.agentLoop.GetRegistry()
	if _, ok := registry.GetAgent(task.AgentID); !ok {
		release()
		logger.ErrorCF("task_executor", "Agent not found, failing task",
			map[string]any{"task_id": taskID, "agent_id": task.AgentID})
		te.failTask(taskID, fmt.Sprintf("agent %q not found", task.AgentID))
		return fmt.Errorf("task_executor: agent %q not found", task.AgentID)
	}

	// Count running tasks for this specific agent via the store (per-agent cap).
	runningTasks, err := te.store.List(taskstore.TaskFilter{Status: "running", AgentID: task.AgentID})
	if err != nil {
		// Release the semaphore slot — we won't start.
		release()
		return fmt.Errorf("task_executor: list running tasks for agent %q: %w", task.AgentID, err)
	}
	if len(runningTasks) >= te.maxConcurrent {
		release()
		return fmt.Errorf(
			"task_executor: concurrency limit reached for agent %q (%d running)",
			task.AgentID,
			len(runningTasks),
		)
	}

	// Atomically claim the task (queued→running) under the store mutex.
	// This is the single critical section that eliminates the TOCTOU race:
	// if a concurrent caller (heartbeat or advanceBlockedTasks) already claimed
	// this task, ClaimForRun returns ErrAlreadyClaimed and we abort without
	// spawning a duplicate goroutine.
	now := time.Now().UTC()
	claimed, err := te.store.ClaimForRun(taskID, now)
	if err != nil {
		release()
		if errors.Is(err, taskstore.ErrAlreadyClaimed) {
			// Another goroutine won the race — not an error worth logging at Warn.
			return fmt.Errorf("task_executor: task %q already claimed by concurrent dispatch", taskID)
		}
		return fmt.Errorf("task_executor: claim task %q for run: %w", taskID, err)
	}
	task = claimed

	// Emit a task_status_changed frame for the queued→running transition so
	// the SPA can update its tasks view in real time. ClaimForRun has already
	// persisted the new status on the returned entity.
	te.emitStatusChanged(claimed, "running")

	taskCtx, cancel := context.WithCancel(ctx)
	te.mu.Lock()
	te.running[taskID] = cancel
	te.mu.Unlock()

	go te.runTask(taskCtx, task, cancel, release)
	return nil
}

// runTask executes the agent prompt and updates the task on completion.
// release is the DispatchSemaphore release function — it MUST be called exactly
// once when the task goroutine exits (via the deferred call below).
func (te *TaskExecutor) runTask(
	ctx context.Context,
	task *taskstore.TaskEntity,
	cancel context.CancelFunc,
	release func(),
) {
	defer release()
	defer cancel()
	defer func() {
		te.mu.Lock()
		delete(te.running, task.ID)
		te.mu.Unlock()
	}()

	logger.InfoCF("task_executor", "runTask started",
		map[string]any{"task_id": task.ID, "agent_id": task.AgentID})

	// Resolve the agent's session store once for the entire task execution.
	taskStore := te.agentLoop.GetAgentStore(task.AgentID)
	if taskStore == nil {
		logger.ErrorCF("task_executor", "Agent store not found, task will have no session",
			map[string]any{"task_id": task.ID, "agent_id": task.AgentID})
	}

	// Create a task session in the agent's unified store so the UI can display it.
	var taskSessionID string
	if taskStore != nil {
		if meta, err := taskStore.NewSession(session.SessionTypeTask, "system", task.AgentID); err != nil {
			logger.ErrorCF("task_executor", "Could not create task session",
				map[string]any{"task_id": task.ID, "error": err.Error()})
		} else {
			taskSessionID = meta.ID
			title := task.Title
			taskID := task.ID
			if setErr := taskStore.SetMeta(meta.ID, session.MetaPatch{Title: &title, TaskID: &taskID}); setErr != nil {
				logger.ErrorCF("task_executor", "Could not set task session meta",
					map[string]any{"task_id": task.ID, "error": setErr.Error()})
			}
			// Persist the session ID on the task entity so the UI can find it.
			if _, updateErr := te.store.Update(
				task.ID,
				taskstore.TaskPatch{SessionID: &taskSessionID},
			); updateErr != nil {
				logger.ErrorCF("task_executor", "Could not persist session_id on task",
					map[string]any{"task_id": task.ID, "session_id": taskSessionID, "error": updateErr.Error()})
			}
			// Record the initial prompt as the user turn.
			if err := taskStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        task.ID + "-prompt",
				Role:      "user",
				Content:   te.buildPrompt(task),
				Timestamp: time.Now().UTC(),
			}); err != nil {
				logger.ErrorCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": task.ID, "error": err.Error()})
			}
		}
	}

	// Inject the agent ID into the tool context used during this task session.
	taskCtx := tools.WithAgentID(ctx, task.AgentID)

	sessionKey := fmt.Sprintf("agent:%s:task:%s", task.AgentID, task.ID)
	prompt := te.buildPrompt(task)

	taskChatID := taskSessionID
	if taskChatID == "" {
		taskChatID = "task:" + task.ID
	}
	resp, err := te.agentLoop.processTaskDirect(taskCtx, task.AgentID, prompt, sessionKey, taskChatID)
	if err != nil {
		logger.ErrorCF("task_executor", "Agent execution failed",
			map[string]any{"task_id": task.ID, "agent_id": task.AgentID, "error": err.Error()})
		// Record the failure to the task transcript.
		if taskSessionID != "" && taskStore != nil {
			if appendErr := taskStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
				ID:        task.ID + "-error",
				Role:      "assistant",
				Content:   fmt.Sprintf("Task execution failed: %v", err),
				Status:    "error",
				Timestamp: time.Now().UTC(),
			}); appendErr != nil {
				logger.WarnCF("task_executor", "Transcript write failed",
					map[string]any{"task_id": task.ID, "error": appendErr.Error()})
			}
			status := session.StatusInterrupted
			if setErr := taskStore.SetMeta(taskSessionID, session.MetaPatch{Status: &status}); setErr != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": task.ID, "error": setErr.Error()})
			}
		}
		te.failTask(task.ID, fmt.Sprintf("execution error: %v", err))
		// Notify the originating channel that the task failed.
		failedTask := *task
		failedTask.Status = "failed"
		failedTask.Result = fmt.Sprintf("execution error: %v", err)
		te.notifySourceChannel(&failedTask)
		return
	}

	// Record the final response to the task transcript.
	if taskSessionID != "" && resp != "" && taskStore != nil {
		if err := taskStore.AppendTranscript(taskSessionID, session.TranscriptEntry{
			ID:        task.ID + "-response",
			Role:      "assistant",
			Content:   resp,
			Timestamp: time.Now().UTC(),
		}); err != nil {
			logger.WarnCF("task_executor", "Transcript write failed",
				map[string]any{"task_id": task.ID, "error": err.Error()})
		}
	}

	// Check whether the agent already called task_update (task status is terminal).
	current, lerr := te.store.Get(task.ID)
	if lerr != nil {
		logger.WarnCF("task_executor", "Could not re-read task after execution",
			map[string]any{"task_id": task.ID, "error": lerr.Error()})
		return
	}
	if current.Status == "completed" || current.Status == "failed" {
		// Agent already called task_update which fired onTaskComplete via the tool callback.
		// Mark session completed and do not fire again to avoid duplicate parent notifications.
		if taskSessionID != "" && taskStore != nil {
			statusCompleted := session.StatusArchived
			if err := taskStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusCompleted}); err != nil {
				logger.WarnCF("task_executor", "Meta update failed",
					map[string]any{"task_id": task.ID, "error": err.Error()})
			}
		}
		te.notifySourceChannel(current)
		return
	}

	// Agent did not call task_update — auto-complete.
	now := time.Now().UTC()
	result := resp
	if result == "" {
		result = "Task completed"
	}
	final, uerr := te.store.Update(task.ID, taskstore.TaskPatch{
		Status:      ptrStr("completed"),
		Result:      &result,
		CompletedAt: &now,
	})
	if uerr != nil {
		logger.ErrorCF("task_executor", "Auto-complete task failed",
			map[string]any{"task_id": task.ID, "error": uerr.Error()})
		return
	}
	// Mark the task session as archived on successful auto-completion.
	if taskSessionID != "" && taskStore != nil {
		statusArchived := session.StatusArchived
		if err := taskStore.SetMeta(taskSessionID, session.MetaPatch{Status: &statusArchived}); err != nil {
			logger.WarnCF("task_executor", "Meta update failed",
				map[string]any{"task_id": task.ID, "error": err.Error()})
		}
	}
	te.onTaskComplete(final)
	te.notifySourceChannel(final)
}

// notifySourceChannel sends a compact task result back to the channel that triggered it.
// Only sends for terminal statuses (completed/failed); silently returns otherwise.
func (te *TaskExecutor) notifySourceChannel(task *taskstore.TaskEntity) {
	if task.SourceChannel == "" || task.SourceChatID == "" {
		return
	}
	if te.agentLoop.bus == nil {
		logger.WarnCF("task_executor", "Cannot notify source channel — message bus is nil",
			map[string]any{"task_id": task.ID, "channel": task.SourceChannel})
		return
	}

	status := task.Status
	if status != "completed" && status != "failed" {
		return
	}

	msg := fmt.Sprintf("**%s** — %s", task.Title, status)
	if task.Result != "" {
		result := task.Result
		if len(result) > 500 {
			result = result[:497] + "..."
		}
		msg += "\n\n" + result
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	if err := te.agentLoop.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
		Channel: task.SourceChannel,
		ChatID:  task.SourceChatID,
		Content: msg,
	}); err != nil {
		logger.WarnCF("task_executor", "Could not notify source channel",
			map[string]any{"task_id": task.ID, "channel": task.SourceChannel, "error": err.Error()})
	}
}

// buildPrompt constructs the prompt sent to the agent for a task.
func (te *TaskExecutor) buildPrompt(task *taskstore.TaskEntity) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Task: %s\n\n", task.Title))
	if task.Prompt != "" {
		sb.WriteString(task.Prompt)
		sb.WriteString("\n\n")
	}
	sb.WriteString(fmt.Sprintf("Priority: %d (1=highest, 5=lowest)\n", task.Priority))
	sb.WriteString(fmt.Sprintf("Task ID: %s\n\n", task.ID))
	sb.WriteString("When you have finished this task, call `task_update` with:\n")
	sb.WriteString(fmt.Sprintf("  task_id: %q\n", task.ID))
	sb.WriteString("  status: \"completed\" (or \"failed\" if unsuccessful)\n")
	sb.WriteString("  result: a brief summary of what was accomplished\n")
	return sb.String()
}

// onTaskComplete handles post-completion logic:
//  1. Parent notification (all siblings done → resume parent agent).
//  2. Orchestrator advance: find tasks whose blocked_by list is now fully
//     satisfied (every dep has status "completed") and dispatch them.
//
// This is the sole wiring point for the Orchestrator coordinator — it is
// called by TaskUpdateTool.SetOnComplete (loop.go:1487) and by the
// auto-complete path at the bottom of runTask. The task_status_changed WS
// frame is emitted at every status transition (see emitStatusChanged).
// notifyParentIfAllSiblingsDone resumes the parent agent once every child task
// of parentID has reached a terminal state. It is safe under concurrent sibling
// completions: the parent follow-up is launched at most once, guarded by an
// atomic FollowedUp claim (ClaimParentFollowUp) — without it two siblings could
// each see allDone and each spawn a duplicate resume goroutine.
func (te *TaskExecutor) notifyParentIfAllSiblingsDone(parentID string) {
	siblings, err := te.store.List(taskstore.TaskFilter{ParentTaskID: parentID})
	if err != nil {
		logger.WarnCF("task_executor", "Could not list siblings",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	for _, s := range siblings {
		if s.Status == "queued" || s.Status == "running" {
			return // not all siblings terminal yet
		}
	}

	parent, err := te.store.Get(parentID)
	if err != nil {
		logger.WarnCF("task_executor", "Could not load parent task",
			map[string]any{"parent_id": parentID, "error": err.Error()})
		return
	}
	if parent.Status != "running" {
		return
	}

	// Race guard: concurrent sibling completions can both observe allDone and both
	// reach here. Atomically claim the follow-up so exactly one caller launches the
	// parent resume goroutine; losers (and any later re-entry) skip silently.
	claimed, claimErr := te.store.ClaimParentFollowUp(parent.ID)
	if claimErr != nil {
		logger.WarnCF("task_executor", "Could not claim parent follow-up",
			map[string]any{"parent_id": parent.ID, "error": claimErr.Error()})
		return
	}
	if !claimed {
		return
	}

	// Test seam: when wired, observe the (single) claimed follow-up without the
	// real agent dispatch. Production leaves this nil.
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

func (te *TaskExecutor) onTaskComplete(task *taskstore.TaskEntity) {
	// Emit a task_status_changed frame for the terminal transition. This covers
	// both the auto-complete path (runTask calls onTaskComplete) and the
	// task_update tool path (SetOnComplete → onTaskComplete). The task's status
	// is already terminal on the entity at this point.
	te.emitStatusChanged(task, task.Status)

	// ── 1. Parent notification ──────────────────────────────────────────────
	if task.ParentTaskID != "" {
		te.notifyParentIfAllSiblingsDone(task.ParentTaskID)
	}

	// ── 2. Orchestrator advance — only when the completed task is "completed"
	//       (not "failed": failed deps should not unblock downstream tasks). ──
	if task.Status != "completed" {
		return
	}
	te.advanceBlockedTasks(context.Background(), task.ID)
}

// readyBlockedCandidates returns the IDs of all queued tasks that list
// completedTaskID as a blocker AND whose ENTIRE blocked_by dependency set is now
// in "completed" status. These are the tasks the Orchestrator should dispatch.
//
// This is the pure decision half of advanceBlockedTasks (no dispatch, no agent
// loop) so the gating logic — "only advance when every dep, not just the one that
// just completed, is done; a failed/queued dep does NOT satisfy" — is directly
// unit-testable. Returns a nil slice (not an error) when the scan fails so the
// caller degrades gracefully; the underlying error is logged here.
func (te *TaskExecutor) readyBlockedCandidates(completedTaskID string) []string {
	candidates, err := te.store.List(taskstore.TaskFilter{
		Status:      "queued",
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
		// Verify ALL dependencies of this candidate are now completed (not just
		// the one that triggered this call). A failed or still-queued dep blocks.
		allSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != "completed" {
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

// advanceBlockedTasks finds all queued tasks that list completedTaskID in their
// blocked_by field and attempts to dispatch those whose full dependency set is
// now satisfied. This implements the Orchestrator coordinator seam.
func (te *TaskExecutor) advanceBlockedTasks(ctx context.Context, completedTaskID string) {
	for _, taskID := range te.readyBlockedCandidates(completedTaskID) {
		// All deps satisfied — attempt to dispatch.
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
func (te *TaskExecutor) buildChildSummary(children []taskstore.TaskEntity) string {
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
// bus so the WS forwarder can deliver a task_status_changed frame to connected
// SPA clients. It is a best-effort emit — a nil agentLoop or nil eventBus is
// silently skipped (e.g. in unit tests that construct a TaskExecutor directly).
// sessionID falls back to "task:<id>" when the task has no session yet so the
// contract-required session_id field is always populated.
func (te *TaskExecutor) emitStatusChanged(task *taskstore.TaskEntity, status string) {
	if te.agentLoop == nil {
		return
	}
	sessionID := task.SessionID
	if sessionID == "" {
		sessionID = "task:" + task.ID
	}
	te.agentLoop.EmitTaskStatusChanged(TaskStatusChangedPayload{
		TaskID:    task.ID,
		Status:    status,
		SessionID: sessionID,
		AgentID:   task.AgentID,
	})
}

// failTask marks a task as failed with the given reason.
func (te *TaskExecutor) failTask(taskID, reason string) {
	now := time.Now().UTC()
	updated, err := te.store.Update(taskID, taskstore.TaskPatch{
		Status:      ptrStr("failed"),
		Result:      &reason,
		CompletedAt: &now,
	})
	if err != nil {
		logger.ErrorCF("task_executor", "Could not mark task failed",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return
	}
	// Emit a task_status_changed frame for the →failed transition. failTask is
	// called for executor-internal failures (agent-not-found, execution error)
	// which do NOT flow through onTaskComplete, so emit here directly.
	te.emitStatusChanged(updated, "failed")
}

// ResizeDispatchSema updates the global dispatch semaphore capacity.
// Called by the gateway performance PUT handler after a config update so the
// new value takes effect immediately without a restart.
func (te *TaskExecutor) ResizeDispatchSema(newCap int) {
	te.dispatchSema.Resize(newCap)
	logger.InfoCF("task_executor", "Dispatch semaphore resized",
		map[string]any{"new_cap": te.dispatchSema.Cap(), "in_flight": te.dispatchSema.InFlight()})
}

// DispatchSemaCap returns the current dispatch semaphore capacity.
func (te *TaskExecutor) DispatchSemaCap() int {
	return te.dispatchSema.Cap()
}

// Stop cancels all running task goroutines.
func (te *TaskExecutor) Stop() {
	te.mu.Lock()
	defer te.mu.Unlock()
	for _, cancel := range te.running {
		cancel()
	}
	te.running = make(map[string]context.CancelFunc)
}

// CheckQueuedTasks picks the highest-priority *dispatchable* queued task per
// agent and starts it. Called by the heartbeat service.
//
// "Dispatchable" means all tasks in the task's blocked_by list are in
// "completed" status. Tasks that are still blocked by incomplete dependencies
// are skipped in priority order until a ready task is found, preventing a
// high-priority blocked task from starving lower-priority ready tasks.
func (te *TaskExecutor) CheckQueuedTasks(ctx context.Context) {
	queued, err := te.store.List(taskstore.TaskFilter{Status: "queued"})
	if err != nil {
		logger.WarnCF("task_executor", "Check queued tasks: list failed",
			map[string]any{"error": err.Error()})
		return
	}
	if len(queued) == 0 {
		return
	}

	// List is already sorted by priority ASC then created_at ASC (see store.List).
	// Group by agent_id in that order; for each agent pick the first task whose
	// blocked_by dependencies are all satisfied (skip blocked heads).
	type agentState struct {
		picked bool
	}
	agentDone := make(map[string]agentState)

	for i := range queued {
		t := &queued[i]
		if agentDone[t.AgentID].picked {
			// Already dispatched one task for this agent this tick.
			continue
		}

		// Check whether all deps of this task are completed.
		depsSatisfied := true
		for _, depID := range t.BlockedBy {
			dep, depErr := te.store.Get(depID)
			if depErr != nil || dep.Status != "completed" {
				depsSatisfied = false
				break
			}
		}
		if !depsSatisfied {
			// This task is blocked — skip it and try the next queued task for
			// the same agent (which has lower priority but may be dispatchable).
			logger.WarnCF("task_executor", "Heartbeat: skipping blocked task, trying next",
				map[string]any{"task_id": t.ID, "agent_id": t.AgentID})
			continue
		}

		if err := te.ExecuteTask(ctx, t.ID); err != nil {
			logger.WarnCF("task_executor", "Heartbeat: could not start task",
				map[string]any{"task_id": t.ID, "error": err.Error()})
			// Even if dispatch failed (e.g. cap reached), mark this agent as
			// done for this tick so we don't attempt more tasks for it and
			// amplify load.
		}
		agentDone[t.AgentID] = agentState{picked: true}
	}
}

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }
