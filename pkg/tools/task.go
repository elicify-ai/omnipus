package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/taskstore"
)

// TaskListTool lists tasks for the calling agent.
type TaskListTool struct {
	BaseTool
	store *taskstore.TaskStore
}

func NewTaskListTool(store *taskstore.TaskStore) *TaskListTool {
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
				"enum":        []string{"queued", "running", "completed", "failed"},
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

	filter := taskstore.TaskFilter{Status: status}
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
	store         *taskstore.TaskStore
	delegateCheck func(targetAgentID string) bool
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2). It receives the call ctx (for
	// current delegation depth) and the target agent id, and returns a non-empty
	// reason to DENY or "" to ALLOW. Takes precedence over delegateCheck so a
	// rejected task delegation surfaces a clear reason to the LLM.
	delegationDeny func(ctx context.Context, targetAgentID string) string
}

func NewTaskCreateTool(store *taskstore.TaskStore) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

// SetDelegateChecker sets the function that checks whether delegation to a target agent is allowed.
func (t *TaskCreateTool) SetDelegateChecker(fn func(targetAgentID string) bool) {
	t.delegateCheck = fn
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2):
// trust set + modes ("task") + depth. Returns a non-empty reason to DENY or ""
// to ALLOW. When set, it takes precedence over the boolean delegateCheck.
func (t *TaskCreateTool) SetDelegationDenyChecker(fn func(ctx context.Context, targetAgentID string) string) {
	t.delegationDeny = fn
}

func (t *TaskCreateTool) Name() string     { return "task_create" }
func (t *TaskCreateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskCreateTool) Description() string {
	return "Create a task and assign it to an agent for execution."
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
				"description": "ID of the parent task (optional)",
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
	// The full-policy checker takes precedence and surfaces a specific reason.
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

	// Validate parent exists and check depth.
	if parentTaskID != "" {
		if err := t.checkDepth(parentTaskID, 0); err != nil {
			return ErrorResult(err.Error())
		}
	}

	entity := &taskstore.TaskEntity{
		Title:        title,
		Prompt:       prompt,
		AgentID:      agentID,
		CreatedBy:    callerID,
		Priority:     priority,
		ParentTaskID: parentTaskID,
		TriggerType:  "manual",
		Status:       "queued",
	}

	// Propagate the originating channel so completed tasks can route results back.
	if channel := ToolChannel(ctx); channel != "" && channel != "webchat" {
		entity.SourceChannel = channel
		entity.SourceChatID = ToolChatID(ctx)
	}

	if err := t.store.Create(entity); err != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: %v", err))
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":"queued"}`, entity.ID))
}

// checkDepth walks the parent chain and returns an error if depth exceeds 10.
func (t *TaskCreateTool) checkDepth(parentID string, depth int) error {
	if depth >= 10 {
		return fmt.Errorf("maximum dependency depth exceeded (max 10)")
	}
	parent, err := t.store.Get(parentID)
	if err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			return fmt.Errorf("parent task %q not found", parentID)
		}
		return fmt.Errorf("could not load parent task: %v", err)
	}
	if parent.ParentTaskID == "" {
		return nil
	}
	return t.checkDepth(parent.ParentTaskID, depth+1)
}

// TaskUpdateTool allows an agent to update status of its own task.
type TaskUpdateTool struct {
	BaseTool
	store      *taskstore.TaskStore
	onComplete func(*taskstore.TaskEntity)
}

func NewTaskUpdateTool(store *taskstore.TaskStore) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

// SetOnComplete sets the callback invoked when a task reaches a terminal status.
func (t *TaskUpdateTool) SetOnComplete(fn func(*taskstore.TaskEntity)) {
	t.onComplete = fn
}

func (t *TaskUpdateTool) Name() string     { return "task_update" }
func (t *TaskUpdateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskUpdateTool) Description() string {
	return "Update status of a task assigned to you. Mark as running, completed, or failed."
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
				"enum":        []string{"running", "completed", "failed"},
				"description": "New status for the task",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "Summary of what was accomplished (for completed/failed)",
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

	task, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}

	if task.AgentID != callerID {
		return ErrorResult("you can only update tasks assigned to you")
	}

	patch := taskstore.TaskPatch{
		Status: &status,
	}
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

	now := time.Now().UTC()
	switch status {
	case "running":
		patch.StartedAt = &now
	case "completed", "failed":
		patch.CompletedAt = &now
	}

	updated, err := t.store.Update(taskID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_update failed: %v", err))
	}

	if (status == "completed" || status == "failed") && t.onComplete != nil {
		t.onComplete(updated)
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q}`, updated.ID, updated.Status))
}

// --- TaskDeleteTool ---

type TaskDeleteTool struct {
	BaseTool
	store *taskstore.TaskStore
}

func NewTaskDeleteTool(store *taskstore.TaskStore) *TaskDeleteTool {
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
	if err := t.store.Delete(taskID); err != nil {
		if errors.Is(err, taskstore.ErrNotFound) {
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
