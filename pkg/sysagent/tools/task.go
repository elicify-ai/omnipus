// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// unifiedTask is the canonical on-disk task type used by the sysagent tools.
// Using task.Task ensures field-preserving read-modify-write: all fields survive
// a round-trip through writeEntity.
type unifiedTask = task.Task

func tasksDir(home string) string { return filepath.Join(home, "tasks") }

// taskStoreFor returns a task.Store rooted at the home's tasks directory. It
// shares the process-wide task.TaskFileLock so its DAG validation, auto-advance,
// and cascade operations interleave correctly with the gateway REST handlers.
func taskStoreFor(home string) *task.Store { return task.New(tasksDir(home)) }

// isValidTaskStatus reports whether s is one of the 7 unified statuses.
func isValidTaskStatus(s string) bool { return task.IsValidStatus(task.Status(s)) }

// ---- system.task.create ----

type TaskCreateTool struct{ deps *Deps }

func NewTaskCreateTool(d *Deps) *TaskCreateTool  { return &TaskCreateTool{deps: d} }
func (t *TaskCreateTool) Name() string           { return "system.task.create" }
func (t *TaskCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskCreateTool) Description() string {
	return "Create a task on the workspace board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call system.workspace.list first to get the workspace_id.\nParameters: name (required, the task title), description (optional), prompt (optional, agent instruction), workspace_id (required, from system.workspace.list), agent_id (optional, agent to assign), status (optional: inbox=new/untriaged, next=ready, planning, in_progress, blocked, done, failed — defaults to inbox), due (optional, RFC 3339 due date/time), blocked_by (optional, array of task IDs this task is blocked by)."
}

func (t *TaskCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":         map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"prompt":       map[string]any{"type": "string", "description": "Agent instruction for an llm task"},
			"workspace_id": map[string]any{"type": "string"},
			"agent_id":     map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
			"due":          map[string]any{"type": "string", "description": "RFC 3339 due date/time"},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by",
			},
		},
		"required": []string{"name", "workspace_id"},
	}
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	status := task.StatusInbox
	if v, ok := args["status"].(string); ok && isValidTaskStatus(v) {
		status = task.Status(v)
	}
	id := ulid.Make().String()
	tk := unifiedTask{
		ID:     id,
		Title:  name,
		Action: task.ActionLLM,
		Status: status,
	}
	if v, ok := args["description"].(string); ok {
		tk.Description = v
	}
	if v, ok := args["prompt"].(string); ok {
		tk.Prompt = v
	}
	sessionOwner := tools.ToolSessionOwner(ctx)
	workspaceID, _ := args["workspace_id"].(string)
	if workspaceID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "workspace_id is required", "workspace_id"))
	}
	if err := validateID(workspaceID); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
	}
	ws, wsErr := readWorkspaceFromDisk(t.deps.Home, workspaceID)
	if wsErr != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
	}
	tk.WorkspaceID = workspaceID
	if ws.Owner != "" {
		tk.Owner = ws.Owner
	}
	if tk.Owner == "" && sessionOwner != "" {
		tk.Owner = sessionOwner
	}
	tk.CreatedBy = sessionOwner
	if v, ok := args["agent_id"].(string); ok {
		tk.AgentID = v
	}
	if v, ok := args["due"].(string); ok && v != "" {
		tk.Due = v
	}
	if rawDeps, ok := args["blocked_by"].([]any); ok && len(rawDeps) > 0 {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		tk.BlockedBy = deps
	}

	// Create via the store so DAG validation + atomic write + locking apply.
	store := taskStoreFor(t.deps.Home)
	if err := store.Create(&tk); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": tk.ID, "name": name, "status": string(tk.Status),
		"workspace_id": tk.WorkspaceID, "agent_id": tk.AgentID,
	}))
}

// ---- system.task.update ----

type TaskUpdateTool struct{ deps *Deps }

func NewTaskUpdateTool(d *Deps) *TaskUpdateTool  { return &TaskUpdateTool{deps: d} }
func (t *TaskUpdateTool) Name() string           { return "system.task.update" }
func (t *TaskUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskUpdateTool) Description() string {
	return "Update an existing task. Call this to change status, reassign, rename, or link to a workspace. Use system.task.list first to find the task id.\nParameters: id (required, from system.task.list), name, description, prompt, workspace_id, agent_id, status (inbox/next/planning/in_progress/blocked/done/failed), due (RFC 3339), blocked_by (array of task IDs, replaces existing list). Only provided fields are updated."
}

func (t *TaskUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"name":         map[string]any{"type": "string"},
			"description":  map[string]any{"type": "string"},
			"prompt":       map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string"},
			"agent_id":     map[string]any{"type": "string"},
			"workspace_id": map[string]any{"type": "string"},
			"due":          map[string]any{"type": "string", "description": "RFC 3339 due date/time"},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by (replaces existing list)",
			},
		},
		"required": []string{"id"},
	}
}

func (t *TaskUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}

	store := taskStoreFor(t.deps.Home)
	existing, err := store.Get(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use system.task.list to see available tasks"))
	}

	patch := task.Patch{}
	updated := []string{}
	if v, ok := args["name"].(string); ok && v != "" {
		patch.Title = &v
		updated = append(updated, "name")
	}
	if v, ok := args["description"].(string); ok {
		patch.Description = &v
		updated = append(updated, "description")
	}
	if v, ok := args["prompt"].(string); ok {
		patch.Prompt = &v
		updated = append(updated, "prompt")
	}
	if v, ok := args["status"].(string); ok && isValidTaskStatus(v) {
		st := task.Status(v)
		patch.Status = &st
		updated = append(updated, "status")
	}
	if v, ok := args["agent_id"].(string); ok && v != "" {
		patch.AgentID = &v
		updated = append(updated, "agent_id")
	}
	if v, ok := args["workspace_id"].(string); ok {
		if v != "" {
			if werr := validateID(v); werr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
			}
			if _, wsErr := readWorkspaceFromDisk(t.deps.Home, v); wsErr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id"))
			}
		}
		// workspace_id is not in task.Patch (workspace is required-scoped and not
		// re-pointed via the generic patch); apply it directly under the lock.
		mu := store.Lock(id)
		mu.Lock()
		existing.WorkspaceID = v
		existing.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := writeEntity(tasksDir(t.deps.Home), id, *existing)
		mu.Unlock()
		if writeErr != nil {
			return tools.ErrorResult(errorJSON("SAVE_FAILED", writeErr.Error(), ""))
		}
		updated = append(updated, "workspace_id")
	}
	if v, ok := args["due"].(string); ok {
		patch.Due = &v
		updated = append(updated, "due")
	}
	if rawDeps, ok := args["blocked_by"].([]any); ok {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		patch.BlockedBy = &deps
		updated = append(updated, "blocked_by")
	}

	// Apply the field patch via the store (DAG validation + atomic write).
	result, err := store.Update(id, patch)
	if err != nil {
		if isTaskNotFound(err) {
			return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
				"Use system.task.list to see available tasks"))
		}
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// FR-6.5: when the task newly reaches terminal "done", advance dependents.
	if result.Status == task.StatusDone {
		if advanced, advErr := store.AdvanceBlockedDependents(id); advErr != nil {
			slog.Warn("system.task.update: advance dependents failed", "id", id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("system.task.update: completed task advanced dependents",
				"completed_id", id, "advanced_ids", advanced)
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "updated_fields": updated}))
}

// isTaskNotFound reports whether err wraps task.ErrNotFound.
func isTaskNotFound(err error) bool {
	return err != nil && (err == task.ErrNotFound || fmt.Sprintf("%v", err) == task.ErrNotFound.Error())
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
	store := taskStoreFor(t.deps.Home)
	if _, err := store.Get(id); err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use system.task.list to see available tasks"))
	}
	unblocked, err := store.Delete(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("DELETE_FAILED", err.Error(),
			"Use system.task.list to see available tasks"))
	}
	if len(unblocked) > 0 {
		slog.Info("sysagent: task delete: unblocked dependents", "deleted_id", id, "unblocked", unblocked)
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
	workspaceFilter, _ := args["workspace_id"].(string)
	agentFilter, _ := args["agent_id"].(string)
	statusFilter, _ := args["status"].(string)

	store := taskStoreFor(t.deps.Home)
	filtered, err := store.List(task.Filter{
		WorkspaceID: workspaceFilter,
		AgentID:     agentFilter,
		Status:      task.Status(statusFilter),
	})
	if err != nil {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{"tasks": filtered}))
}
