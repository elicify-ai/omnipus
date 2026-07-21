package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/bus"
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
}

// newTaskExecutor creates a TaskExecutor.
func newTaskExecutor(al *AgentLoop, store *taskstore.TaskStore) *TaskExecutor {
	return &TaskExecutor{
		agentLoop:     al,
		store:         store,
		running:       make(map[string]context.CancelFunc),
		maxConcurrent: defaultMaxConcurrentTasksPerAgent,
	}
}

// ExecuteTask starts executing the task identified by taskID.
// It updates the task's status to "running" and dispatches it to the agent in a goroutine.
func (te *TaskExecutor) ExecuteTask(ctx context.Context, taskID string) error {
	task, err := te.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task_executor: get task %q: %w", taskID, err)
	}
	if task.Status != "queued" {
		return fmt.Errorf("task_executor: task %q is %s, not queued", taskID, task.Status)
	}

	registry := te.agentLoop.GetRegistry()
	if _, ok := registry.GetAgent(task.AgentID); !ok {
		logger.ErrorCF("task_executor", "Agent not found, failing task",
			map[string]any{"task_id": taskID, "agent_id": task.AgentID})
		te.failTask(taskID, fmt.Sprintf("agent %q not found", task.AgentID))
		return fmt.Errorf("task_executor: agent %q not found", task.AgentID)
	}

	// Count running tasks for this specific agent via the store.
	runningTasks, err := te.store.List(taskstore.TaskFilter{Status: "running", AgentID: task.AgentID})
	if err != nil {
		return fmt.Errorf("task_executor: list running tasks for agent %q: %w", task.AgentID, err)
	}
	if len(runningTasks) >= te.maxConcurrent {
		return fmt.Errorf(
			"task_executor: concurrency limit reached for agent %q (%d running)",
			task.AgentID,
			len(runningTasks),
		)
	}

	// Transition to running.
	now := time.Now().UTC()
	updated, err := te.store.Update(taskID, taskstore.TaskPatch{
		Status:    ptrStr("running"),
		StartedAt: &now,
	})
	if err != nil {
		return fmt.Errorf("task_executor: update task %q to running: %w", taskID, err)
	}
	task = updated

	taskCtx, cancel := context.WithCancel(ctx)
	te.mu.Lock()
	te.running[taskID] = cancel
	te.mu.Unlock()

	go te.runTask(taskCtx, task, cancel)
	return nil
}

// runTask executes the agent prompt and updates the task on completion.
func (te *TaskExecutor) runTask(ctx context.Context, task *taskstore.TaskEntity, cancel context.CancelFunc) {
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

// onTaskComplete handles post-completion logic: parent notification.
func (te *TaskExecutor) onTaskComplete(task *taskstore.TaskEntity) {
	if task.ParentTaskID == "" {
		return
	}

	siblings, err := te.store.List(taskstore.TaskFilter{ParentTaskID: task.ParentTaskID})
	if err != nil {
		logger.WarnCF("task_executor", "Could not list siblings",
			map[string]any{"parent_id": task.ParentTaskID, "error": err.Error()})
		return
	}
	for _, s := range siblings {
		if s.Status == "queued" || s.Status == "running" {
			return
		}
	}

	// All siblings done — notify the parent agent.
	parent, err := te.store.Get(task.ParentTaskID)
	if err != nil {
		logger.WarnCF("task_executor", "Could not load parent task",
			map[string]any{"parent_id": task.ParentTaskID, "error": err.Error()})
		return
	}
	if parent.Status != "running" {
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

// failTask marks a task as failed with the given reason.
func (te *TaskExecutor) failTask(taskID, reason string) {
	now := time.Now().UTC()
	if _, err := te.store.Update(taskID, taskstore.TaskPatch{
		Status:      ptrStr("failed"),
		Result:      &reason,
		CompletedAt: &now,
	}); err != nil {
		logger.ErrorCF("task_executor", "Could not mark task failed",
			map[string]any{"task_id": taskID, "error": err.Error()})
	}
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

// CheckQueuedTasks picks up the highest-priority queued task per agent and starts it.
// Called by the heartbeat service.
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

	// Group by agent_id; pick the first (lowest priority number = highest priority) per agent.
	byAgent := make(map[string]*taskstore.TaskEntity)
	for i := range queued {
		t := &queued[i]
		if _, exists := byAgent[t.AgentID]; !exists {
			byAgent[t.AgentID] = t
		}
	}

	for _, t := range byAgent {
		if err := te.ExecuteTask(ctx, t.ID); err != nil {
			logger.WarnCF("task_executor", "Heartbeat: could not start task",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
	}
}

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }
