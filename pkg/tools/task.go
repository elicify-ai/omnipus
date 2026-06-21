package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/task"
)

// defaultTaskWorkspaceID is the fallback workspace a tool-created task lands in
// when no active workspace is bound to the turn. Mirrors the pre-seeded
// "My Workspace" default ID created at boot.
const defaultTaskWorkspaceID = "default"

// resolveWorkspaceID returns the active workspace from ctx, falling back to the
// default workspace when none is bound (M4 workspace→turn binding gap).
func resolveWorkspaceID(ctx context.Context) string {
	if ws := ToolWorkspaceID(ctx); ws != "" {
		return ws
	}
	return defaultTaskWorkspaceID
}

// TaskListTool lists tasks for the calling agent.
type TaskListTool struct {
	BaseTool
	store *task.Store
}

func NewTaskListTool(store *task.Store) *TaskListTool {
	return &TaskListTool{store: store}
}

func (t *TaskListTool) Name() string     { return "task_list" }
func (t *TaskListTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskListTool) Description() string {
	return "List tasks. Use role='assignee' for tasks assigned to you, role='delegator' for tasks you created for other agents."
}

func (t *TaskListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role": map[string]any{
				"type":        "string",
				"enum":        []string{"assignee", "delegator"},
				"description": "assignee: tasks assigned to you; delegator: tasks you created for others",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"inbox", "next", "planning", "in_progress", "blocked", "done", "failed"},
				"description": "Filter by status (optional)",
			},
		},
		"required": []string{"role"},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	role, _ := args["role"].(string)
	if role != "assignee" && role != "delegator" {
		return ErrorResult("role must be 'assignee' or 'delegator'")
	}
	status, _ := args["status"].(string)
	agentID := ToolAgentID(ctx)

	filter := task.Filter{Status: task.Status(status)}
	switch role {
	case "assignee":
		filter.AgentID = agentID
	case "delegator":
		filter.CreatedBy = agentID
	}

	tasks, err := t.store.List(filter)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_list failed: %v", err))
	}

	data, err := json.Marshal(tasks)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_list: marshal: %v", err))
	}
	return NewToolResult(string(data))
}

// TaskCreateTool creates a task and delegates it to another agent.
type TaskCreateTool struct {
	BaseTool
	store         *task.Store
	delegateCheck func(targetAgentID string) bool
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2). Returns a non-empty reason to
	// DENY or "" to ALLOW. Takes precedence over delegateCheck.
	delegationDeny func(ctx context.Context, targetAgentID string) string
	// onCreate, when non-nil, is invoked after a task is successfully created so
	// the caller can emit a task_status_changed event.
	onCreate func(*task.Task)
}

func NewTaskCreateTool(store *task.Store) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

// SetDelegateChecker sets the function that checks whether delegation to a target agent is allowed.
func (t *TaskCreateTool) SetDelegateChecker(fn func(targetAgentID string) bool) {
	t.delegateCheck = fn
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2).
func (t *TaskCreateTool) SetDelegationDenyChecker(fn func(ctx context.Context, targetAgentID string) string) {
	t.delegationDeny = fn
}

// SetOnCreate sets the callback invoked after a task is successfully created.
func (t *TaskCreateTool) SetOnCreate(fn func(*task.Task)) {
	t.onCreate = fn
}

func (t *TaskCreateTool) Name() string     { return "task_create" }
func (t *TaskCreateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskCreateTool) Description() string {
	return "Create a task and assign it to an agent for execution. The task lands as a visible card on the workspace board."
}

func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Short title for the task",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Full instructions for the agent",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to assign the task to",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest); default 3",
			},
			"parent_task_id": map[string]any{
				"type":        "string",
				"description": "ID of the parent task (optional) — set when this is a subtask of another task",
			},
		},
		"required": []string{"title", "prompt", "agent_id"},
	}
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	title, _ := args["title"].(string)
	prompt, _ := args["prompt"].(string)
	agentID, _ := args["agent_id"].(string)
	callerID := ToolAgentID(ctx)

	if title == "" {
		return ErrorResult("title is required")
	}
	if prompt == "" {
		return ErrorResult("prompt is required")
	}
	if agentID == "" {
		return ErrorResult("agent_id is required")
	}

	// Delegation policy gate (FR-6.2): trust set + modes ("task") + depth.
	if t.delegationDeny != nil {
		if reason := t.delegationDeny(ctx, agentID); reason != "" {
			return ErrorResult("delegation denied: " + reason)
		}
	} else if t.delegateCheck != nil && !t.delegateCheck(agentID) {
		return ErrorResult(fmt.Sprintf("delegation to %s not allowed", agentID))
	}

	priority := 3
	if p, ok := args["priority"].(float64); ok && p >= 1 && p <= 5 {
		priority = int(p)
	}

	parentTaskID, _ := args["parent_task_id"].(string)

	// A delegated task is ready to be picked up by the executor: it lands in
	// `next` (triaged & dispatchable) rather than `inbox`. Detail #6: it carries
	// a parent link and the originating channel for result delivery.
	entity := &task.Task{
		Title:        title,
		Prompt:       prompt,
		Action:       task.ActionLLM,
		AgentID:      agentID,
		CreatedBy:    callerID,
		Priority:     priority,
		ParentTaskID: parentTaskID,
		WorkspaceID:  resolveWorkspaceID(ctx),
		Status:       task.StatusNext,
	}

	// Propagate the originating channel so completed tasks can route results back.
	if channel := ToolChannel(ctx); channel != "" && channel != "webchat" {
		entity.SourceChannel = channel
		entity.SourceChatID = ToolChatID(ctx)
	}

	if err := t.store.Create(entity); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
		}
		return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
	}

	if t.onCreate != nil {
		t.onCreate(entity)
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, entity.ID, entity.Status))
}

// TaskUpdateTool allows an agent to update status of its own task.
type TaskUpdateTool struct {
	BaseTool
	store      *task.Store
	onComplete func(*task.Task)
}

func NewTaskUpdateTool(store *task.Store) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

// SetOnComplete sets the callback invoked when a task reaches a terminal status.
func (t *TaskUpdateTool) SetOnComplete(fn func(*task.Task)) {
	t.onComplete = fn
}

func (t *TaskUpdateTool) Name() string     { return "task_update" }
func (t *TaskUpdateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskUpdateTool) Description() string {
	return "Update status of a task assigned to you. Mark as in_progress, done, or failed."
}

func (t *TaskUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the task to update",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"in_progress", "done", "failed"},
				"description": "New status for the task",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "Summary of what was accomplished (for done/failed)",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "File paths or URLs produced as artifacts",
			},
		},
		"required": []string{"task_id", "status"},
	}
}

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	taskID, _ := args["task_id"].(string)
	status, _ := args["status"].(string)
	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	if taskID == "" {
		return ErrorResult("task_id is required")
	}
	if status == "" {
		return ErrorResult("status is required")
	}
	st := task.Status(status)
	if !task.IsValidStatus(st) {
		return ErrorResult(fmt.Sprintf("invalid status %q", status))
	}

	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}

	if existing.AgentID != callerID {
		return ErrorResult("you can only update tasks assigned to you")
	}

	patch := task.Patch{Status: &st}
	if result, ok := args["result"].(string); ok && result != "" {
		patch.Result = &result
	}
	if rawArtifacts, ok := args["artifacts"].([]any); ok {
		artifacts := make([]string, 0, len(rawArtifacts))
		for _, a := range rawArtifacts {
			if s, ok := a.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
		patch.Artifacts = &artifacts
	}

	now := time.Now().UTC().Format(time.RFC3339)
	switch st {
	case task.StatusInProgress:
		patch.StartedAt = &now
	case task.StatusDone, task.StatusFailed:
		patch.CompletedAt = &now
	}

	updated, err := t.store.Update(taskID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_update failed: %v", err))
	}

	if task.IsTerminal(st) && t.onComplete != nil {
		t.onComplete(updated)
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, updated.ID, updated.Status))
}

// TaskAddTodoTool appends a checklist item to a task's embedded todos (Tier 1).
type TaskAddTodoTool struct {
	BaseTool
	store *task.Store
}

func NewTaskAddTodoTool(store *task.Store) *TaskAddTodoTool {
	return &TaskAddTodoTool{store: store}
}

func (t *TaskAddTodoTool) Name() string     { return "task_add_todo" }
func (t *TaskAddTodoTool) Scope() ToolScope { return ScopeGeneral }
func (t *TaskAddTodoTool) Description() string {
	return "Add a lightweight checklist item (todo) to a task. A todo is a simple {text, done} line — use a subtask (task_create with parent_task_id) for real decomposition."
}

func (t *TaskAddTodoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "ID of the task to add the todo to"},
			"text":    map[string]any{"type": "string", "description": "The checklist item text (1-500 chars)"},
		},
		"required": []string{"task_id", "text"},
	}
}

func (t *TaskAddTodoTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	taskID, _ := args["task_id"].(string)
	text, _ := args["text"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}
	if text == "" {
		return ErrorResult("text is required")
	}

	mu := t.store.Lock(taskID)
	mu.Lock()
	defer mu.Unlock()

	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	newTodos := append(append([]task.Todo{}, existing.Todos...), task.Todo{Text: text, Done: false})
	if _, err := t.store.Update(taskID, task.Patch{Todos: &newTodos}); err != nil {
		return ErrorResult(fmt.Sprintf("task_add_todo failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"todos":%d}`, taskID, len(newTodos)))
}

// TaskAddDependencyTool adds a blocked_by edge (this task depends on another).
type TaskAddDependencyTool struct {
	BaseTool
	store *task.Store
}

func NewTaskAddDependencyTool(store *task.Store) *TaskAddDependencyTool {
	return &TaskAddDependencyTool{store: store}
}

func (t *TaskAddDependencyTool) Name() string     { return "task_add_dependency" }
func (t *TaskAddDependencyTool) Scope() ToolScope { return ScopeGeneral }
func (t *TaskAddDependencyTool) Description() string {
	return "Add a dependency: mark that a task is blocked by another task (it cannot start until the blocker is done). Rejected if it would create a cycle."
}

func (t *TaskAddDependencyTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id":    map[string]any{"type": "string", "description": "ID of the dependent task"},
			"blocked_by": map[string]any{"type": "string", "description": "ID of the task that must complete first"},
		},
		"required": []string{"task_id", "blocked_by"},
	}
}

func (t *TaskAddDependencyTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	taskID, _ := args["task_id"].(string)
	blocker, _ := args["blocked_by"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}
	if blocker == "" {
		return ErrorResult("blocked_by is required")
	}

	mu := t.store.Lock(taskID)
	mu.Lock()
	defer mu.Unlock()

	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	for _, dep := range existing.BlockedBy {
		if dep == blocker {
			return NewToolResult(fmt.Sprintf(`{"task_id":%q,"blocked_by":%q,"already":true}`, taskID, blocker))
		}
	}
	newDeps := append(append([]string{}, existing.BlockedBy...), blocker)
	if _, err := t.store.Update(taskID, task.Patch{BlockedBy: &newDeps}); err != nil {
		return ErrorResult(fmt.Sprintf("task_add_dependency failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"blocked_by":%q}`, taskID, blocker))
}

// --- TaskDeleteTool ---

type TaskDeleteTool struct {
	BaseTool
	store *task.Store
}

func NewTaskDeleteTool(store *task.Store) *TaskDeleteTool {
	return &TaskDeleteTool{store: store}
}

func (t *TaskDeleteTool) Name() string     { return "task_delete" }
func (t *TaskDeleteTool) Scope() ToolScope { return ScopeGeneral }
func (t *TaskDeleteTool) Description() string {
	return "Delete a task by ID. Only use when explicitly asked to remove a task."
}

func (t *TaskDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "ID of the task to delete"},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskDeleteTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}
	if _, err := t.store.Delete(taskID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not delete task: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"deleted":%q}`, taskID))
}

// --- AgentListTool ---

type AgentListTool struct {
	BaseTool
	listAgents func() []AgentInfo
}

type AgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func NewAgentListTool(lister func() []AgentInfo) *AgentListTool {
	return &AgentListTool{listAgents: lister}
}

func (t *AgentListTool) Name() string     { return "agent_list" }
func (t *AgentListTool) Scope() ToolScope { return ScopeGeneral }
func (t *AgentListTool) Description() string {
	return "List all available agents with their IDs and names. Use this to resolve agent names to IDs before delegating tasks."
}

func (t *AgentListTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *AgentListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	agents := t.listAgents()
	data, err := json.Marshal(agents)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not serialize agent list: %v", err))
	}
	return NewToolResult(string(data))
}
