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
	return "Create a task on the GTD board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call system.workspace.list first to get the workspace_id.\nParameters: name (required, the task title), description (optional), workspace_id (optional, from system.workspace.list), agent_id (optional, agent to assign), status (optional: inbox=new/unscheduled, next=prioritized for soon, active=in-progress, waiting=blocked/waiting, done=complete — defaults to inbox), start (optional, RFC 3339 start date/time), due (optional, RFC 3339 due date/time), recurrence (optional, RRULE string e.g. 'FREQ=WEEKLY;BYDAY=MO'), blocked_by (optional, array of task IDs this task is blocked by)."
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
			"start":        map[string]any{"type": "string", "description": "RFC 3339 start date/time"},
			"due":          map[string]any{"type": "string", "description": "RFC 3339 due date/time"},
			"recurrence":   map[string]any{"type": "string", "description": "RRULE string (RFC 5545)"},
			"blocked_by":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Task IDs this task is blocked by"},
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
	// Spec-5 fields: start, due, recurrence, blocked_by.
	if v, ok := args["start"].(string); ok && v != "" {
		tk.Start = v
	}
	if v, ok := args["due"].(string); ok && v != "" {
		tk.Due = v
	}
	if v, ok := args["recurrence"].(string); ok && v != "" {
		tk.Recurrence = v
	}
	if rawDeps, ok := args["blocked_by"].([]any); ok && len(rawDeps) > 0 {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		if len(deps) > 0 {
			loader := func(depID string) (boardtask.Task, error) {
				var dep gtdTask
				if err := readEntity(tasksDir(t.deps.Home), depID, &dep); err != nil {
					return boardtask.Task{}, err
				}
				return dep, nil
			}
			if err := boardtask.ValidateBlockedBy(id, deps, loader); err != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT",
					fmt.Sprintf("blocked_by: %v", err), "blocked_by"))
			}
			tk.BlockedBy = deps
		}
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
	return "Update an existing GTD board task. Call this to change status, reassign, rename, or link to a workspace. Use system.task.list first to find the task id. If linking to a workspace by name, call system.workspace.list first to get the workspace_id.\nParameters: id (required, from system.task.list), name, description, workspace_id (from system.workspace.list), agent_id, status (inbox=new/unscheduled, next=prioritized, active=in-progress, waiting=blocked, done=complete), start (RFC 3339 start date/time), due (RFC 3339 due date/time), recurrence (RRULE string), blocked_by (array of task IDs). Only provided fields are updated."
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
			"start":        map[string]any{"type": "string", "description": "RFC 3339 start date/time"},
			"due":          map[string]any{"type": "string", "description": "RFC 3339 due date/time"},
			"recurrence":   map[string]any{"type": "string", "description": "RRULE string (RFC 5545)"},
			"blocked_by":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Task IDs this task is blocked by (replaces existing list)"},
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
	// becameDone records that this update transitions the task to terminal "done";
	// advanceDeps is armed only after a successful write. The deferred closure runs
	// AFTER the per-task lock is released and un-gates any waiting dependents whose
	// blocked_by deps are now all done (FR-6.5). Registered before the unlock defer
	// so it executes last (LIFO).
	becameDone := false
	advanceDeps := false
	defer func() {
		if !advanceDeps {
			return
		}
		if advanced, advErr := boardtask.AdvanceBlockedDependents(tasksDir(t.deps.Home), id); advErr != nil {
			slog.Warn("system.task.update: advance dependents failed", "id", id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("system.task.update: completed task advanced dependents",
				"completed_id", id, "advanced_ids", advanced)
		}
	}()

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
	oldStatus := tk.Status
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
		// FR-6.5: record when a board task newly reaches terminal "done"; the
		// post-unlock dependent advance is armed only after a successful write.
		if tk.Status == boardtask.StatusDone && oldStatus != boardtask.StatusDone {
			becameDone = true
		}
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
	// Spec-5 fields: start, due, recurrence, blocked_by.
	if v, ok := args["start"].(string); ok {
		tk.Start = v
		updated = append(updated, "start")
	}
	if v, ok := args["due"].(string); ok {
		tk.Due = v
		updated = append(updated, "due")
	}
	if v, ok := args["recurrence"].(string); ok {
		tk.Recurrence = v
		updated = append(updated, "recurrence")
	}
	if rawDeps, ok := args["blocked_by"].([]any); ok {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		if len(deps) > 0 {
			loader := func(depID string) (boardtask.Task, error) {
				var dep gtdTask
				if err := readEntity(tasksDir(t.deps.Home), depID, &dep); err != nil {
					return boardtask.Task{}, err
				}
				return dep, nil
			}
			if err := boardtask.ValidateBlockedBy(id, deps, loader); err != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT",
					fmt.Sprintf("blocked_by: %v", err), "blocked_by"))
			}
		}
		if len(deps) == 0 {
			tk.BlockedBy = nil
		} else {
			tk.BlockedBy = deps
		}
		updated = append(updated, "blocked_by")
	}
	tk.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeEntity(tasksDir(t.deps.Home), id, tk); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	// Write succeeded: arm the post-unlock dependent advance if this update
	// completed the task (FR-6.5).
	if becameDone {
		advanceDeps = true
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
	// Spec-5 (FR-8.2): cascade-clean inbound blocked_by edges after delete.
	if unblocked, cascadeErr := boardtask.CascadeDeleteEdges(tasksDir(t.deps.Home), id); cascadeErr != nil {
		// Non-fatal: log and continue — orphaned edges will be cleaned by DropOrphanEdges.
		slog.Warn("sysagent: task delete: cascade edge cleanup failed", "id", id, "error", cascadeErr)
	} else if len(unblocked) > 0 {
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
