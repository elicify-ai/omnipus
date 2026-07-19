// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
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

// validateBlockersSameWorkspace verifies every blocker task exists and lives in
// dependentWorkspaceID. This mirrors validateBlockersWorkspace in pkg/tools so the
// privileged cross-workspace task tools enforce the SAME same-workspace blocker
// rule the plain create_task / update_task tools enforce. The store validates the
// DAG (cycle/self-edge/missing/depth) under its per-task lock; it does NOT enforce
// the same-workspace constraint, so the tool layer does. WorkspaceID is immutable
// (set at create, never patched), so this pre-check is race-free.
func validateBlockersSameWorkspace(store *task.Store, dependentWorkspaceID string, blockers []string) error {
	for _, b := range blockers {
		bt, err := store.Get(b)
		if err != nil {
			return fmt.Errorf("blocker task %q not found", b)
		}
		if bt.WorkspaceID != dependentWorkspaceID {
			return fmt.Errorf("blocker task %q is in a different workspace", b)
		}
	}
	return nil
}

// parseCriteriaArgsFromWorkspaceTool converts create_task_in_workspace's raw
// "criteria" argument (a []any of map[string]any — the shape LLM tool-call
// arguments always decode into) into []task.AcceptanceCriterion. Mirrors
// pkg/tools/task.go's parseCriteriaArgs exactly (duplicated rather than
// exported+imported: that helper is unexported package-internal to pkg/tools,
// and this package must not reach into pkg/tools' internals). Every criterion
// is server-authored as the CALLING agent — agent-created criteria are, by
// definition, agent-authored (SD-A7); author is never accepted from args.
// Shape/length validation is left to the store's own normalizeCriteria,
// invoked from Store.Create.
func parseCriteriaArgsFromWorkspaceTool(raw []any, authorAgentID string) ([]task.AcceptanceCriterion, error) {
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d]: must be an object", i)
		}
		kind, _ := m["kind"].(string)
		text, _ := m["text"].(string)
		c := task.AcceptanceCriterion{
			Kind:   task.CriterionKind(kind),
			Text:   text,
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorAgentID},
		}
		if chk, ok := m["check"].(map[string]any); ok {
			command, _ := chk["command"].(string)
			var expectedExitCode int
			if v, ok := chk["expected_exit_code"].(float64); ok {
				expectedExitCode = int(v)
			}
			c.Check = &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExitCode}
		}
		out = append(out, c)
	}
	return out, nil
}

// allCheckCriteriaWorkspace reports whether criteria is non-empty and EVERY
// entry is kind=check (ADR-049 D2 rule 5 gate condition). Mirrors
// pkg/tools/task.go's allCheckCriteria.
func allCheckCriteriaWorkspace(criteria []task.AcceptanceCriterion) bool {
	if len(criteria) == 0 {
		return false
	}
	for _, c := range criteria {
		if c.Kind != task.KindCheck {
			return false
		}
	}
	return true
}

// delegationDenied evaluates the FR-6.2 delegation gate for an update/delete
// mutation that targets a task assigned to (or being reassigned to) targetAgentID.
// It returns the structured denial to DENY, or nil to ALLOW. When the hook is
// unwired (Deps.DelegationDeny == nil, i.e. tests/standalone) it ALLOWS — the
// same fail-open-when-unwired behavior the plain tools have when their checker is
// unset; the production gateway always wires it.
func (d *Deps) delegationDenied(ctx context.Context, callerAgentID, targetAgentID string) *tools.DelegationDenial {
	if d == nil || d.DelegationDeny == nil {
		return nil
	}
	return d.DelegationDeny(ctx, callerAgentID, targetAgentID)
}

// ---- create_task_in_workspace ----

type TaskCreateTool struct{ deps *Deps }

func NewTaskCreateTool(d *Deps) *TaskCreateTool  { return &TaskCreateTool{deps: d} }
func (t *TaskCreateTool) Name() string           { return "create_task_in_workspace" }
func (t *TaskCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskCreateTool) Description() string {
	return "Create a task on the workspace board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call list_workspaces first to get the workspace_id.\nParameters: name (required, the task title), description (optional), prompt (optional, agent instruction), workspace_id (required, from list_workspaces), agent_id (optional, agent to assign), status (optional: inbox=new/untriaged, next=ready, planning, in_progress, blocked, done, failed — defaults to inbox), due (optional, RFC 3339 due date/time), blocked_by (optional, array of task IDs this task is blocked by), criteria (REQUIRED when agent_id is set: at least one acceptance criterion / Definition of Done)."
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
			"criteria": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []string{"check", "prose"},
							"description": "check: a shell command verified via the assignee's own bash tool; " +
								"prose: a free-text statement judged by the Judge System Agent",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The criterion statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted when kind is \"prose\"",
						},
					},
					"required": []string{"kind", "text"},
				},
				"description": "Acceptance criteria (Definition of Done). REQUIRED (at least one) when " +
					"agent_id is set — an agent-assigned task with zero criteria is rejected.",
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

	// FR-6.2 delegation gate (parity with the plain create_task tool). The
	// cross-workspace surface is the PRIVILEGED Orchestrator path, so it must
	// enforce the SAME trust-set + mode("task") + depth policy the same-workspace
	// create_task enforces — assigning work to ANOTHER agent is delegation.
	caller := tools.ToolAgentID(ctx)
	if v, ok := args["agent_id"].(string); ok {
		tk.AgentID = v
	}
	// subagent_3p (external-CLI) worker task assignment is no longer guarded
	// here: AgentLoop.processTaskDirect (pkg/agent/loop.go) now branches on
	// runner.ResolveDispatch and routes an external-CLI worker's task run
	// through runExternalCLISubTurn instead of the native engine — see its
	// doc comment for the dispatch design.
	if t.deps.DelegationDeny != nil && tk.AgentID != "" && tk.AgentID != caller {
		if denial := t.deps.DelegationDeny(ctx, caller, tk.AgentID); denial != nil {
			return tools.DelegationDeniedResult(t.Name(), denial)
		}
	}

	// FR-6/D5 strict criteria enforcement (ADR-049, SD-A7, review r1 major
	// M5, parity with the plain create_task tool): an agent-ASSIGNED task
	// requires at least one acceptance criterion — only meaningful once
	// AgentID is set at all (an unassigned, human-tracking-only task never
	// enters the goal loop/judge machinery, so criteria enforcement does not
	// apply to it).
	if tk.AgentID != "" {
		rawCriteria, _ := args["criteria"].([]any)
		if len(rawCriteria) == 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"criteria is required: an agent-assigned task must supply at least one acceptance "+
					"criterion (Definition of Done) — ADR-049 D5/SD-A7", "criteria"))
		}
		criteria, cErr := parseCriteriaArgsFromWorkspaceTool(rawCriteria, caller)
		if cErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", cErr.Error(), "criteria"))
		}
		// D2 rule 5 (FR-017/052): an all-check criteria set can never be
		// adjudicated MET if the assignee's effective bash policy is deny or
		// ask (ask resolves to deny unattended at judge time, D2 rule 2).
		if allCheckCriteriaWorkspace(criteria) {
			if t.deps.ResolveBashPolicy == nil {
				// FAIL CLOSED, not open, when no checker is wired — same
				// rationale as the delegation gate above.
				slog.Error("create_task_in_workspace: no bash-policy resolver installed — denying an "+
					"all-check criteria set by default",
					"caller_id", caller, "target_agent_id", tk.AgentID)
				return tools.ErrorResult(errorJSON("INVALID_INPUT",
					"cannot verify the assignee's bash policy (D2 rule 5 resolver not configured) — "+
						"denying an all-machine-criteria create by default", "criteria"))
			}
			policy, ok := t.deps.ResolveBashPolicy(tk.AgentID)
			if !ok || policy != string(config.ToolPolicyAllow) {
				resolved := "unresolvable"
				if ok {
					resolved = policy
				}
				return tools.ErrorResult(errorJSON("INVALID_INPUT", fmt.Sprintf(
					"all criteria are machine-checkable (kind=check) but agent %q's effective bash "+
						"policy is %q — this criteria set could never be satisfied (structurally "+
						"unsatisfiable, ADR-049 D2 rule 5)",
					tk.AgentID, resolved,
				), "criteria"))
			}
		}
		tk.Criteria = criteria
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

	// Same-workspace blocker guard (parity with validateBlockersWorkspace in the
	// plain tool): every blocked_by edge must point at a task in the SAME target
	// workspace. The store validates the DAG (cycle/self-edge/missing/depth) but
	// NOT this cross-workspace rule, so enforce it at the tool layer.
	if len(tk.BlockedBy) > 0 {
		if wErr := validateBlockersSameWorkspace(store, tk.WorkspaceID, tk.BlockedBy); wErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", wErr.Error(), "blocked_by"))
		}
	}

	if err := store.Create(&tk); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": tk.ID, "name": name, "status": string(tk.Status),
		"workspace_id": tk.WorkspaceID, "agent_id": tk.AgentID,
	}))
}

// ---- update_task_in_workspace ----

type TaskUpdateTool struct{ deps *Deps }

func NewTaskUpdateTool(d *Deps) *TaskUpdateTool  { return &TaskUpdateTool{deps: d} }
func (t *TaskUpdateTool) Name() string           { return "update_task_in_workspace" }
func (t *TaskUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskUpdateTool) Description() string {
	return "Update an existing task. Call this to change status, reassign, rename, or link to a workspace. Use list_tasks_in_workspace first to find the task id.\nParameters: id (required, from list_tasks_in_workspace), name, description, prompt, workspace_id, agent_id, status (inbox/next/planning/in_progress/blocked/done/failed), due (RFC 3339), blocked_by (array of task IDs, replaces existing list). Only provided fields are updated."
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

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}

	store := taskStoreFor(t.deps.Home)
	existing, err := store.Get(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use list_tasks_in_workspace to see available tasks"))
	}

	caller := tools.ToolAgentID(ctx)

	// Ownership gate (parity with the plain update_task tool's "you can only
	// update tasks assigned to you"). The privileged cross-workspace path is
	// permitted to mutate ANOTHER agent's task ONLY when delegation policy allows
	// the caller to delegate to that task's current assignee — otherwise an agent
	// could rewrite work it has no authority over. Tasks the caller owns
	// (assignee or creator) are always mutable by the caller. Unassigned tasks
	// (no AgentID) carry no ownership constraint.
	if existing.AgentID != "" && existing.AgentID != caller && existing.CreatedBy != caller {
		if denied := t.deps.delegationDenied(ctx, caller, existing.AgentID); denied != nil {
			return tools.DelegationDeniedResult(t.Name(), denied)
		}
	}

	// Reassignment is re-delegation: when agent_id changes to a DIFFERENT agent,
	// gate it through the SAME trust-set + mode("task") + depth policy as create.
	if v, ok := args["agent_id"].(string); ok && v != "" && v != existing.AgentID && v != caller {
		if denied := t.deps.delegationDenied(ctx, caller, v); denied != nil {
			return tools.DelegationDeniedResult(t.Name(), denied)
		}
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
		// subagent_3p (external-CLI) worker reassignment is no longer guarded
		// here — same rationale as create_task_in_workspace above.
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
		// Same-workspace blocker guard (parity with validateBlockersWorkspace in
		// the plain tool). Validate against the task's EFFECTIVE workspace —
		// existing.WorkspaceID already reflects any workspace_id change applied
		// above this point. CLEAR ([]) trivially passes.
		if len(deps) > 0 {
			if wErr := validateBlockersSameWorkspace(store, existing.WorkspaceID, deps); wErr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", wErr.Error(), "blocked_by"))
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
				"Use list_tasks_in_workspace to see available tasks"))
		}
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// FR-6.5: when the task newly reaches terminal "done", advance dependents.
	if result.Status == task.StatusDone {
		if advanced, advErr := store.AdvanceBlockedDependents(id); advErr != nil {
			slog.Warn("update_task_in_workspace: advance dependents failed", "id", id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("update_task_in_workspace: completed task advanced dependents",
				"completed_id", id, "advanced_ids", advanced)
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "updated_fields": updated}))
}

// isTaskNotFound reports whether err wraps task.ErrNotFound.
func isTaskNotFound(err error) bool {
	return err != nil && (err == task.ErrNotFound || fmt.Sprintf("%v", err) == task.ErrNotFound.Error())
}

// ---- delete_task_in_workspace ----

type TaskDeleteTool struct{ deps *Deps }

func NewTaskDeleteTool(d *Deps) *TaskDeleteTool  { return &TaskDeleteTool{deps: d} }
func (t *TaskDeleteTool) Name() string           { return "delete_task_in_workspace" }
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

func (t *TaskDeleteTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
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
	existing, err := store.Get(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use list_tasks_in_workspace to see available tasks"))
	}

	// Ownership gate (parity with the plain delete_task tool's "you can only
	// modify/delete tasks you own or are assigned"). The privileged cross-workspace
	// path may delete ANOTHER agent's task ONLY when delegation policy permits the
	// caller to delegate to that task's assignee. Tasks the caller owns (assignee
	// or creator) are always deletable; unassigned tasks carry no ownership gate.
	caller := tools.ToolAgentID(ctx)
	if existing.AgentID != "" && existing.AgentID != caller && existing.CreatedBy != caller {
		if denied := t.deps.delegationDenied(ctx, caller, existing.AgentID); denied != nil {
			return tools.DelegationDeniedResult(t.Name(), denied)
		}
	}

	unblocked, err := store.Delete(id)
	if err != nil {
		if !errors.Is(err, task.ErrCascadeEdgeCleanupFailed) {
			return tools.ErrorResult(errorJSON("DELETE_FAILED", err.Error(),
				"Use list_tasks_in_workspace to see available tasks"))
		}
		// The task itself was deleted; only cleaning up OTHER tasks' dangling
		// blocked_by edges partially failed. Non-fatal — log and continue
		// reporting success for the primary delete, matching how
		// update_task_in_workspace above already treats
		// AdvanceBlockedDependents's write-failure error as a logged,
		// non-fatal side effect.
		slog.Warn("delete_task_in_workspace: cascade edge cleanup partially failed", "deleted_id", id, "error", err)
	}
	if len(unblocked) > 0 {
		slog.Info("sysagent: task delete: unblocked dependents", "deleted_id", id, "unblocked", unblocked)
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "deleted": true}))
}

// ---- list_tasks_in_workspace ----

type TaskListTool struct{ deps *Deps }

func NewTaskListTool(d *Deps) *TaskListTool    { return &TaskListTool{deps: d} }
func (t *TaskListTool) Name() string           { return "list_tasks_in_workspace" }
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
