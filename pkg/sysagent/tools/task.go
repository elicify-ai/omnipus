// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/dapicom-ai/omnipus/pkg/boardtask"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// gtdTask is the canonical on-disk GTD task type used by the sysagent tools.
// Using boardtask.Task ensures field-preserving read-modify-write: all fields
// survive a round-trip through readEntity/writeEntity, including prompt,
// priority, milestone_id, session_id, result, and owner.
type gtdTask = boardtask.Task

func tasksDir(home string) string { return filepath.Join(home, "tasks") }

// gtdStatusSet is the set of valid GTD task statuses.
// Used to exclude workflow/taskstore tasks from agent-visible task lists.
var gtdStatusSet = boardtask.GTDStatuses

// ---- system.task.create ----

type TaskCreateTool struct{ deps *Deps }

func NewTaskCreateTool(d *Deps) *TaskCreateTool  { return &TaskCreateTool{deps: d} }
func (t *TaskCreateTool) Name() string           { return "system.task.create" }
func (t *TaskCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskCreateTool) Description() string {
	return "Create a task on the GTD board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call system.workspace.list first to get the workspace_id.\nParameters: name (required, the task title), description (optional), workspace_id (optional, from system.workspace.list), agent_id (optional, agent to assign), status (optional: inbox=new/unscheduled, next=prioritized for soon, active=in-progress, waiting=blocked/waiting, done=complete — defaults to inbox)."
}

func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":         map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"workspace_id": map[string]any{"type": "string"},
			"agent_id":     map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
		},
		"required": []string{"name"},
	}
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	status := boardtask.StatusInbox
	if v, ok := args["status"].(string); ok && gtdStatusSet[v] {
		status = boardtask.Status(v)
	}
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)
	tk := gtdTask{
		ID:        id,
		Name:      name,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if v, ok := args["description"].(string); ok {
		tk.Description = v
	}
	// Owner is stamped from the session context (attribution only — not an access gate).
	sessionOwner := tools.ToolSessionOwner(ctx)
	if v, ok := args["workspace_id"].(string); ok && v != "" {
		if err := validateID(v); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
		}
		ws, wsErr := readWorkspaceFromDisk(t.deps.Home, v)
		if wsErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
		}
		tk.WorkspaceID = v

		// Inherit the workspace's owner (attribution stamping).
		if ws.Owner != "" {
			tk.Owner = ws.Owner
		}
	}
	// Fall back to session owner if workspace didn't set one.
	if tk.Owner == "" && sessionOwner != "" {
		tk.Owner = sessionOwner
	}
	if v, ok := args["agent_id"].(string); ok {
		tk.AgentID = v
	}
	if err := writeEntity(tasksDir(t.deps.Home), id, tk); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": id, "name": name, "status": string(status),
		"workspace_id": tk.WorkspaceID, "agent_id": tk.AgentID,
	}))
}

// ---- system.task.update ----

type TaskUpdateTool struct{ deps *Deps }

func NewTaskUpdateTool(d *Deps) *TaskUpdateTool  { return &TaskUpdateTool{deps: d} }
func (t *TaskUpdateTool) Name() string           { return "system.task.update" }
func (t *TaskUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskUpdateTool) Description() string {
	return "Update an existing GTD board task. Call this to change status, reassign, rename, or link to a workspace. Use system.task.list first to find the task id. If linking to a workspace by name, call system.workspace.list first to get the workspace_id.\nParameters: id (required, from system.task.list), name, description, workspace_id (from system.workspace.list), agent_id, status (inbox=new/unscheduled, next=prioritized, active=in-progress, waiting=blocked, done=complete). Only provided fields are updated."
}

func (t *TaskUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"name":         map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
			"agent_id":     map[string]any{"type": "string"},
			"workspace_id": map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}

func (t *TaskUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}

	// Acquire the process-wide per-task striped lock before the read→mutate→write
	// so that a concurrent gateway PUT on the same task cannot interleave and lose
	// updates (#407). The gateway's handleBoardTaskPut and handleBoardTaskStart use
	// the same boardtask.TaskFileLock singleton keyed by task ID.
	mu := boardtask.TaskFileLock.Get(id)
	mu.Lock()
	defer mu.Unlock()

	// Field-preserving read: load the FULL on-disk struct so no fields are lost.
	var tk gtdTask
	if err := readEntity(tasksDir(t.deps.Home), id, &tk); err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use system.task.list to see available tasks"))
	}
	if !gtdStatusSet[string(tk.Status)] {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No GTD task %q", id),
			"Use system.task.list to see available tasks"))
	}
	updated := []string{}
	if v, ok := args["name"].(string); ok && v != "" {
		tk.Name = v
		updated = append(updated, "name")
	}
	if v, ok := args["description"].(string); ok {
		tk.Description = v
		updated = append(updated, "description")
	}
	if v, ok := args["status"].(string); ok && gtdStatusSet[v] {
		// Only /start may reach "active"; the tool path cannot bypass this.
		if v == string(boardtask.StatusActive) {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"status 'active' can only be set via /start, not by the task.update tool", "status"))
		}
		tk.Status = boardtask.Status(v)
		updated = append(updated, "status")
	}
	// Only overwrite agent_id when the caller explicitly provides a non-empty value.
	if v, ok := args["agent_id"].(string); ok && v != "" {
		tk.AgentID = v
		updated = append(updated, "agent_id")
	}
	if v, ok := args["workspace_id"].(string); ok {
		if v != "" {
			if err := validateID(v); err != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
			}
			if _, wsErr := readWorkspaceFromDisk(t.deps.Home, v); wsErr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
			}
		}
		tk.WorkspaceID = v
		updated = append(updated, "workspace_id")
	}
	tk.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeEntity(tasksDir(t.deps.Home), id, tk); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "updated_fields": updated}))
}

// ---- system.task.delete ----

type TaskDeleteTool struct{ deps *Deps }

func NewTaskDeleteTool(d *Deps) *TaskDeleteTool  { return &TaskDeleteTool{deps: d} }
func (t *TaskDeleteTool) Name() string           { return "system.task.delete" }
func (t *TaskDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskDeleteTool) Description() string {
	return "Delete a task. Parameters: id (required), confirm (bool, must be true)."
}

func (t *TaskDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"id", "confirm"},
	}
}

func (t *TaskDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	confirm, _ := args["confirm"].(bool)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete a task", ""))
	}
	var tk gtdTask
	if err := readEntity(tasksDir(t.deps.Home), id, &tk); err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use system.task.list to see available tasks"))
	}
	if !gtdStatusSet[string(tk.Status)] {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No GTD task %q", id),
			"Use system.task.list to see available tasks"))
	}
	if err := deleteEntity(tasksDir(t.deps.Home), id); err != nil {
		return tools.ErrorResult(errorJSON("DELETE_FAILED", err.Error(),
			"Use system.task.list to see available tasks"))
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "deleted": true}))
}

// ---- system.task.list ----

type TaskListTool struct{ deps *Deps }

func NewTaskListTool(d *Deps) *TaskListTool    { return &TaskListTool{deps: d} }
func (t *TaskListTool) Name() string           { return "system.task.list" }
func (t *TaskListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskListTool) Description() string {
	return "List tasks with optional filters.\nParameters: workspace_id, agent_id, status (all optional)."
}

func (t *TaskListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workspace_id": map[string]any{"type": "string"},
			"agent_id":     map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
		},
	}
}

func (t *TaskListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	all, err := listEntities[gtdTask](tasksDir(t.deps.Home))
	if err != nil {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}
	workspaceFilter, _ := args["workspace_id"].(string)
	agentFilter, _ := args["agent_id"].(string)
	statusFilter, _ := args["status"].(string)

	var filtered []gtdTask
	for _, tk := range all {
		if !gtdStatusSet[string(tk.Status)] {
			continue
		}
		if workspaceFilter != "" && tk.WorkspaceID != workspaceFilter {
			continue
		}
		if agentFilter != "" && tk.AgentID != agentFilter {
			continue
		}
		if statusFilter != "" && string(tk.Status) != statusFilter {
			continue
		}
		filtered = append(filtered, tk)
	}
	return tools.NewToolResult(successJSON(map[string]any{"tasks": filtered}))
}
