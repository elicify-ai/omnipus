package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
)

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
	// set + modes ("task") + depth — FR-6.2). Returns a non-nil *DelegationDenial
	// to DENY (carrying the structured reason + policy axis) or nil to ALLOW.
	// Takes precedence over delegateCheck.
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	// onCreate, when non-nil, is invoked after a task is successfully created so
	// the caller can emit a task_status_changed event.
	onCreate func(*task.Task)
	// home is the OMNIPUS_HOME path used to resolve the default workspace ID
	// when no workspace is bound to the current turn context. Set via SetHome.
	home string
	// maxDelegationDepth is the hard ceiling on the task-mode delegation
	// generation counter. A task_create issued from within a task run whose
	// generation already equals (or exceeds) this bound is rejected, so an
	// A→B→A task→task chain cannot recurse unboundedly. Set via
	// SetMaxDelegationDepth; 0 disables the bound (no caller should leave it 0).
	maxDelegationDepth int
}

func NewTaskCreateTool(store *task.Store) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

// SetHome configures the OMNIPUS_HOME path so that task_create can resolve the
// real default workspace ID (via workspace.ResolveDefaultID) when no workspace
// is bound to the turn context. The agent loop calls this after constructing the
// tool (pkg/agent/loop.go ~line 1608).
func (t *TaskCreateTool) SetHome(home string) {
	t.home = home
}

// SetDelegateChecker sets the function that checks whether delegation to a target agent is allowed.
func (t *TaskCreateTool) SetDelegateChecker(fn func(targetAgentID string) bool) {
	t.delegateCheck = fn
}

// SetMaxDelegationDepth installs the hard task-mode recursion bound. A
// task_create issued from within a task run whose stored DelegationDepth is
// already >= the bound is rejected. The agent loop passes agent.maxTaskDepth (10).
func (t *TaskCreateTool) SetMaxDelegationDepth(bound int) {
	t.maxDelegationDepth = bound
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2).
func (t *TaskCreateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
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
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by (optional). Each blocker must exist and be in the same workspace; a cycle is rejected.",
			},
		},
		"required": []string{"title", "prompt", "agent_id"},
	}
}

// resolveBlockedBy parses the optional "blocked_by" array arg into a slice of
// non-empty task IDs. It distinguishes three cases so the caller can tell a
// CLEAR (provided empty) from an unchanged (absent) request:
//   - absent   → deps == nil, provided == false  (leave the field unchanged)
//   - provided → deps == nil/empty, provided == true (CLEAR the list)
//   - provided → deps non-empty, provided == true (REPLACE with deps)
//
// Empty-string and non-string entries are dropped (they are not valid task IDs).
func resolveBlockedBy(args map[string]any) (deps []string, provided bool) {
	rawDeps, ok := args["blocked_by"].([]any)
	if !ok {
		return nil, false
	}
	deps = make([]string, 0, len(rawDeps))
	for _, d := range rawDeps {
		if s, ok := d.(string); ok && s != "" {
			deps = append(deps, s)
		} else {
			slog.Debug("task: dropped invalid blocked_by entry", "entry", d)
		}
	}
	if len(deps) == 0 {
		return nil, true // provided but empty → caller treats as CLEAR
	}
	return deps, true
}

// validateBlockersWorkspace loads each blocker task and verifies it is in the
// same workspace as the dependent. This mirrors TaskAddDependencyTool's
// cross-workspace guard; the store's validateBlockedByLocked handles
// cycle/self-edge/missing/depth, but NOT the same-workspace constraint, so the
// tool layer enforces it.
//
// TOCTOU note: WorkspaceID is immutable (set at create, never in task.Patch), so
// the same-workspace check is race-free — a blocker's workspace cannot change
// between this pre-check and the locked write. The store re-validates the DAG
// invariants (cycle/self-edge/missing/depth) atomically under the per-task lock;
// this tool-layer guard adds the same-workspace rule the store does not enforce.
func validateBlockersWorkspace(store *task.Store, dependentWorkspaceID string, blockers []string) error {
	for _, b := range blockers {
		bt, err := store.Get(b)
		if err != nil {
			if errors.Is(err, task.ErrNotFound) {
				return fmt.Errorf("blocker task %q not found", b)
			}
			return fmt.Errorf("could not load blocker task %q: %w", b, err)
		}
		if bt.WorkspaceID != dependentWorkspaceID {
			return fmt.Errorf("blocker task %q is in a different workspace", b)
		}
	}
	return nil
}

// resolveWorkspaceID returns the workspace ID for the current turn. It first
// checks the turn context (explicitly bound workspace), then falls back to the
// real default workspace on disk (via workspace.ResolveDefaultID). It returns
// an error rather than inventing a literal ID, so chat-delegated tasks never
// land in an invisible workspace.
func (t *TaskCreateTool) resolveWorkspaceID(ctx context.Context) (string, error) {
	if ws := ToolWorkspaceID(ctx); ws != "" {
		// Belt-and-suspenders (M4): a bound id that no longer exists on disk
		// (stale/typo'd) would land the task on an invisible board. Treat a
		// non-existent ctx id as unbound and fall through to the default.
		if t.home == "" || workspace.Exists(t.home, ws) {
			return ws, nil
		}
		slog.Warn("task_create: bound workspace_id does not exist — falling back to default",
			"workspace_id", ws)
	}
	if t.home != "" {
		id, err := workspace.ResolveDefaultID(t.home)
		if err != nil {
			return "", fmt.Errorf("could not resolve default workspace: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("no active workspace bound and no default workspace resolver configured")
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
		if denial := t.delegationDeny(ctx, agentID); denial != nil {
			return DelegationDeniedResult("task_create", denial)
		}
	} else if t.delegateCheck != nil && !t.delegateCheck(agentID) {
		return DelegationDeniedResult("task_create", &DelegationDenial{
			Reason:        fmt.Sprintf("delegation to %s not allowed", agentID),
			Policy:        DenyTrustSet,
			TargetAgentID: agentID,
		})
	}

	// Task-mode recursion bound (SEC): a task_create issued from *within* a task
	// run carries that run's delegation generation on the context. Each task→task
	// hop increments the generation; reject once it would exceed the hard ceiling.
	// This is the runtime bound the per-agent depth gate cannot enforce on its own
	// because every task run starts a FRESH turn at turnState depth 0 — without
	// this counter, an A→B→A task-mode chain would recurse unboundedly.
	parentDepth := ToolDelegationDepth(ctx)
	childDepth := parentDepth + 1
	if t.maxDelegationDepth > 0 && childDepth > t.maxDelegationDepth {
		return DelegationDeniedResult("task_create", &DelegationDenial{
			Reason: fmt.Sprintf(
				"maximum task delegation depth (%d) reached — cannot create a further delegated task",
				t.maxDelegationDepth,
			),
			Policy:        DenyDepth,
			TargetAgentID: agentID,
		})
	}

	priority := 3
	if p, ok := args["priority"].(float64); ok && p >= 1 && p <= 5 {
		priority = int(p)
	}

	parentTaskID, _ := args["parent_task_id"].(string)

	wsID, err := t.resolveWorkspaceID(ctx)
	if err != nil {
		return ErrorResult(fmt.Sprintf("could not resolve workspace: %v", err))
	}

	// A delegated task is ready to be picked up by the executor: it lands in
	// `next` (triaged & dispatchable) rather than `inbox`. Detail #6: it carries
	// a parent link and the originating channel for result delivery.
	entity := &task.Task{
		Title:           title,
		Prompt:          prompt,
		Action:          task.ActionLLM,
		AgentID:         agentID,
		CreatedBy:       callerID,
		Priority:        priority,
		ParentTaskID:    parentTaskID,
		WorkspaceID:     wsID,
		Status:          task.StatusNext,
		DelegationDepth: childDepth,
	}

	// Propagate the originating channel so completed tasks can route results back.
	if channel := ToolChannel(ctx); channel != "" && channel != "webchat" {
		entity.SourceChannel = channel
		entity.SourceChatID = ToolChatID(ctx)
	}

	// Optional blocked_by: mirror admin create. The store's Create validates the
	// blocked_by DAG (cycle/self-edge/missing/depth); the tool layer additionally
	// enforces the same-workspace constraint (the store does not), mirroring
	// TaskAddDependencyTool's cross-workspace guard.
	//
	// For create, provided-empty (CLEAR) and absent are equivalent — a brand-new
	// task starts with no deps either way — so only the populated path sets deps.
	deps, depsProvided := resolveBlockedBy(args)
	if depsProvided && len(deps) > 0 {
		if wErr := validateBlockersWorkspace(t.store, wsID, deps); wErr != nil {
			return ErrorResult(fmt.Sprintf("task_create failed: %v", wErr))
		}
		entity.BlockedBy = deps
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
	store *task.Store
	// delegateCheck, when non-nil, gates reassignment to a different agent
	// (reassignment is re-delegation). Returns false to DENY. Ignored when
	// delegationDeny is non-nil (delegationDeny takes precedence).
	delegateCheck func(targetAgentID string) bool
	// delegationDeny, when non-nil, applies the full delegation policy (trust
	// set + modes ("task") + depth — FR-6.2) to a reassignment. Returns a
	// non-nil *DelegationDenial to DENY or nil to ALLOW. Takes precedence over
	// delegateCheck. Reassignment is re-delegation, so it routes through the
	// SAME gate task_create uses.
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
	onComplete     func(*task.Task)
}

func NewTaskUpdateTool(store *task.Store) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

// SetOnComplete sets the callback invoked when a task reaches a terminal status.
func (t *TaskUpdateTool) SetOnComplete(fn func(*task.Task)) {
	t.onComplete = fn
}

// SetDelegateChecker sets the function that checks whether reassignment to a
// target agent is allowed (reassignment is re-delegation).
func (t *TaskUpdateTool) SetDelegateChecker(fn func(targetAgentID string) bool) {
	t.delegateCheck = fn
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2)
// for reassignment. Reassignment is re-delegation, so it routes through the
// SAME gate task_create uses.
func (t *TaskUpdateTool) SetDelegationDenyChecker(
	fn func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDeny = fn
}

func (t *TaskUpdateTool) Name() string     { return "task_update" }
func (t *TaskUpdateTool) Scope() ToolScope { return ScopeGeneral }

func (t *TaskUpdateTool) Description() string {
	return "Update a task assigned to you. Mark status (in_progress/done/failed) and optionally edit title, priority, due date, agent_id, or blocked_by. Only provided fields are updated."
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
			"title": map[string]any{
				"type":        "string",
				"description": "New title for the task (1-200 chars)",
			},
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest)",
			},
			"due": map[string]any{
				"type":        "string",
				"description": "Due date/time in RFC 3339 format",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the agent to reassign the task to",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of task IDs this task is blocked by. Pass the full list to REPLACE the existing deps; pass [] to CLEAR; omit to leave unchanged. Each blocker must exist + be same-workspace; cycles rejected.",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	taskID, _ := args["task_id"].(string)
	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	if taskID == "" {
		return ErrorResult("task_id is required")
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

	patch := task.Patch{}
	updatedFields := []string{}

	// Status (optional — was the only field historically; still the common path).
	var newStatus task.Status
	statusStr, _ := args["status"].(string)
	if statusStr != "" {
		st := task.Status(statusStr)
		if !task.IsValidStatus(st) {
			return ErrorResult(fmt.Sprintf("invalid status %q", statusStr))
		}
		patch.Status = &st
		newStatus = st
		updatedFields = append(updatedFields, "status")
	}

	// Result / artifacts — accepted with or without a status.
	if result, ok := args["result"].(string); ok && result != "" {
		patch.Result = &result
		updatedFields = append(updatedFields, "result")
	}
	if rawArtifacts, ok := args["artifacts"].([]any); ok {
		artifacts := make([]string, 0, len(rawArtifacts))
		for _, a := range rawArtifacts {
			if s, ok := a.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
		patch.Artifacts = &artifacts
		updatedFields = append(updatedFields, "artifacts")
	}

	// Title.
	if title, ok := args["title"].(string); ok && title != "" {
		patch.Title = &title
		updatedFields = append(updatedFields, "title")
	}

	// Priority (1-5).
	if p, ok := args["priority"].(float64); ok {
		pr := int(p)
		if pr < 1 || pr > 5 {
			return ErrorResult(fmt.Sprintf("priority must be between 1 and 5, got %d", pr))
		}
		patch.Priority = &pr
		updatedFields = append(updatedFields, "priority")
	}

	// Due (RFC 3339 string).
	if due, ok := args["due"].(string); ok && due != "" {
		if _, pErr := time.Parse(time.RFC3339, due); pErr != nil {
			return ErrorResult(fmt.Sprintf("invalid due date %q (must be RFC 3339): %v", due, pErr))
		}
		patch.Due = &due
		updatedFields = append(updatedFields, "due")
	}

	// agent_id (reassign). Reassignment is re-delegation: when the new agent
	// differs from the current assignee, route it through the SAME delegation-
	// policy gate task_create uses (FR-6.2). A no-op reassign (same agent) needs
	// no gate. Mirrors TaskCreateTool.Execute's denial shape.
	if agentID, ok := args["agent_id"].(string); ok && agentID != "" && agentID != existing.AgentID {
		if t.delegationDeny != nil {
			if denial := t.delegationDeny(ctx, agentID); denial != nil {
				return DelegationDeniedResult("task_update", denial)
			}
		} else if t.delegateCheck != nil && !t.delegateCheck(agentID) {
			return DelegationDeniedResult("task_update", &DelegationDenial{
				Reason:        fmt.Sprintf("delegation to %s not allowed", agentID),
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
		patch.AgentID = &agentID
		updatedFields = append(updatedFields, "agent_id")
	}

	// blocked_by (replaces the list). Cross-workspace guard at the tool layer
	// (mirrors TaskAddDependencyTool's cross-workspace guard); the store's
	// validateBlockedByLocked handles cycle/self-edge/missing/depth atomically
	// under the per-task lock.
	//
	// Three-way: provided-empty CLEARs the list, populated REPLACEs it, absent
	// leaves it unchanged.
	deps, depsProvided := resolveBlockedBy(args)
	if depsProvided {
		if len(deps) == 0 {
			// CLEAR — empty list trivially passes the cross-workspace guard.
			patch.BlockedBy = &[]string{}
			updatedFields = append(updatedFields, "blocked_by")
		} else {
			if wErr := validateBlockersWorkspace(t.store, existing.WorkspaceID, deps); wErr != nil {
				return ErrorResult(fmt.Sprintf("task_update failed: %v", wErr))
			}
			patch.BlockedBy = &deps
			updatedFields = append(updatedFields, "blocked_by")
		}
	}

	if len(updatedFields) == 0 {
		return ErrorResult("no updatable fields provided (supply at least one of status, result, artifacts, title, priority, due, agent_id, blocked_by)")
	}

	// Timestamps keyed off status (unchanged behaviour for the status path).
	now := time.Now().UTC().Format(time.RFC3339)
	switch newStatus {
	case task.StatusInProgress:
		patch.StartedAt = &now
	case task.StatusDone, task.StatusFailed:
		patch.CompletedAt = &now
	}

	updated, err := t.store.Update(taskID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_update failed: %v", err))
	}

	// FR-6.5: when the task newly reaches "done", advance dependents (mirror
	// admin: pkg/sysagent/tools/task.go:252-259). The primary update already
	// persisted, so this is best-effort: a storage fault here is surfaced to the
	// caller as an advance_warning rather than turning a successful update into a
	// failure (which would orphan dependents with no signal either way).
	var advanceWarning string
	if newStatus == task.StatusDone {
		advanced, advErr := t.store.AdvanceBlockedDependents(taskID)
		if advErr != nil {
			// Storage fault advancing dependents — the update itself succeeded,
			// so this stays a success, but the warning must reach the LLM/user.
			slog.Error("task_update: advance dependents failed",
				"id", taskID, "error", advErr)
			advanceWarning = advErr.Error()
		} else if len(advanced) > 0 {
			slog.Info("task_update: completed task advanced dependents",
				"completed_id", taskID, "advanced_ids", advanced)
		}
	}

	if task.IsTerminal(newStatus) && t.onComplete != nil {
		t.onComplete(updated)
	}

	// Marshal cannot fail on a []string (updatedFields is always a concrete
	// slice of strings), so the error is impossible in practice — discard it.
	updatedFieldsJSON, _ := json.Marshal(updatedFields)
	result := fmt.Sprintf(`{"task_id":%q,"status":%q,"updated_fields":%s}`,
		updated.ID, updated.Status, string(updatedFieldsJSON))
	if advanceWarning != "" {
		// Append the warning as an escaped string field so the LLM/user can see
		// the dependents were not advanced. Built with json.Marshal so the error
		// message is properly escaped into a JSON string literal.
		warnJSON, _ := json.Marshal(advanceWarning)
		result = fmt.Sprintf(`{"task_id":%q,"status":%q,"updated_fields":%s,"advance_warning":%s}`,
			updated.ID, updated.Status, string(updatedFieldsJSON), string(warnJSON))
	}
	return NewToolResult(result)
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

	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	// Ownership gate: load the task first to verify the caller owns it.
	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	if existing.AgentID != callerID && existing.CreatedBy != callerID {
		return ErrorResult("you can only modify tasks you own or are assigned")
	}

	// AppendTodo takes the per-task lock once internally — do not hold Lock() here.
	updated, err := t.store.AppendTodo(taskID, task.Todo{Text: text, Done: false})
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("task_add_todo failed: %v", err))
	}
	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"todos":%d}`, taskID, len(updated.Todos)))
}

// TaskAddDependencyTool adds a blocked_by edge (this task depends on another).
//
// Deprecated: use task_update{blocked_by} — retires in v0.3 (see
// tasks-system-refactor-2026-06.md §2).
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

	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	// Ownership gate: caller must own or be assigned the dependent task.
	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	if existing.AgentID != callerID && existing.CreatedBy != callerID {
		return ErrorResult("you can only modify tasks you own or are assigned")
	}

	// Cross-workspace dependency guard: load the blocker and verify it is in the
	// same workspace as the dependent task. There is an inherent TOCTOU here; the
	// store's DAG validator still validates the edge itself atomically.
	blockerTask, err := t.store.Get(blocker)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("blocker task %q not found", blocker))
		}
		return ErrorResult(fmt.Sprintf("could not load blocker task: %v", err))
	}
	if blockerTask.WorkspaceID != existing.WorkspaceID {
		return ErrorResult("blocker task is in a different workspace")
	}

	// AddDependency takes the per-task lock once internally — do not hold Lock() here.
	updated, added, err := t.store.AddDependency(taskID, blocker)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_add_dependency failed: %v", err))
	}
	if !added {
		return NewToolResult(fmt.Sprintf(`{"task_id":%q,"blocked_by":%q,"already":true}`, taskID, blocker))
	}
	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"blocked_by":%q,"status":%q}`, taskID, blocker, updated.Status))
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

	callerID := ToolAgentID(ctx)
	if callerID == "" {
		return ErrorResult("agent ID not set in context; cannot verify task ownership")
	}

	// Ownership gate: load the task first to verify the caller owns it.
	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not load task: %v", err))
	}
	if existing.AgentID != callerID && existing.CreatedBy != callerID {
		return ErrorResult("you can only modify/delete tasks you own or are assigned")
	}

	unblocked, err := t.store.Delete(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("could not delete task: %v", err))
	}

	// Advance any dependents that became fully unblocked by this delete.
	// The cascade only rewrote the blocked_by edges; a task that was `blocked`
	// with this task as its only blocker must be moved to `next`. AdvanceUnblocked
	// is a no-op when the dependent is not `blocked` and uses the internal hatch
	// so the transition guard does not reject blocked→next.
	for _, depID := range unblocked {
		if _, uErr := t.store.AdvanceUnblocked(depID); uErr != nil {
			slog.Warn(
				"task_delete: advance unblocked dependent failed",
				"deleted_id",
				taskID,
				"dependent_id",
				depID,
				"error",
				uErr,
			)
			continue
		}
		slog.Info("task_delete: advanced unblocked dependent blocked→next", "deleted_id", taskID, "advanced_id", depID)
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
