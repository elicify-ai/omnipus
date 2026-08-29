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
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
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

// isValidTaskStatus reports whether s is one of the 6 unified statuses.
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

// validateTaskPlanLinkageWorkspace enforces the same-workspace FK a
// Task.plan_id reference must satisfy AND rejects linking to a terminal
// (done/failed) plan (ADR-052 spec "Edge Cases": "create_task(plan_id)
// referencing a plan already done -> reject"). Mirrors
// pkg/tools/plan.go's validateTaskPlanLinkage exactly (duplicated rather
// than exported+imported: that helper is unexported package-internal to
// pkg/tools, and this package must not reach into pkg/tools' internals —
// same rationale as parseCriteriaArgsFromWorkspaceTool above). planID == ""
// (no linkage requested) is always valid (nil). A nil planStore fails
// CLOSED — an unwired store is a configuration error, never a permission
// grant.
func validateTaskPlanLinkageWorkspace(planStore *plan.Store, planID, workspaceID string) error {
	if planID == "" {
		return nil
	}
	if planStore == nil {
		return fmt.Errorf("cannot verify plan_id %q: plan store is not configured", planID)
	}
	if err := planStore.ValidatePlanWorkspace(planID, workspaceID); err != nil {
		return err
	}
	p, err := planStore.Get(planID)
	if err != nil {
		return fmt.Errorf("could not load plan %q: %w", planID, err)
	}
	if plan.IsTerminal(p.State) {
		return fmt.Errorf("plan %q is %q (terminal) and cannot accept new member tasks", planID, p.State)
	}
	return nil
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
	return "Create a task on the workspace board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call list_workspaces first to get the workspace_id.\nParameters: name (required, the task title), description (optional), prompt (optional, agent instruction), workspace_id (required, from list_workspaces), agent_id (optional, agent to assign), status (optional: inbox=new/untriaged, next=ready, in_progress, blocked, done, failed — defaults to inbox), due (optional, RFC 3339 due date/time), priority (optional, 1 highest to 5 lowest, default 3), plan_id (optional, ID of the Plan this task is a member of — must exist in the same workspace and must not be a terminal plan), blocked_by (optional, array of task IDs this task is blocked by), criteria (REQUIRED when agent_id is set: at least one acceptance criterion / Definition of Done)."
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
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest); default 3 (unset)",
			},
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the Plan this task is a member of (optional). Must exist in the same workspace and must not be a terminal (done/failed) plan.",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Concrete paths this plan member creates/edits (optional). Meaningful only alongside plan_id.",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "The parallel-group id this plan member belongs to (optional).",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "True marks this plan member as an authored join/assemble member. Defaults to false.",
			},
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

	// Optional priority: priority is a general task attribute, not gated
	// behind plan_id, so its absence here (while the plain create_task tool
	// accepts it) was a genuine schema-consistency gap rather than a
	// deliberate restriction. Unset (0) is treated as 3 on read
	// (task.Task.EffectivePriority), matching create_task's own default.
	if v, ok := args["priority"].(float64); ok {
		pr := int(v)
		// args["priority"] IS present (ok==true), so this is an EXPLICIT value —
		// including an explicit 0, which task.ValidatePriority rejects with no
		// exception (unlike Task.Priority's own "0 = unset" struct-field
		// contract). Shared with the plain create_task tool, update_task_in_
		// workspace's store-layer check, and REST's create handler so "priority
		// must be between 1 and 5" can never drift across entry points again
		// (M2(b)).
		if err := task.ValidatePriority(pr); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "priority"))
		}
		tk.Priority = pr
	}

	// Optional plan_id (ADR-052 FR-002): same-workspace FK + not-terminal
	// (validateTaskPlanLinkageWorkspace above). A call with no plan_id is
	// entirely unaffected — the check is a no-op for planID == "".
	if v, ok := args["plan_id"].(string); ok && v != "" {
		if pErr := validateTaskPlanLinkageWorkspace(t.deps.PlanStore, v, tk.WorkspaceID); pErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", pErr.Error(), "plan_id"))
		}
		tk.PlanID = v
	}

	// Optional write_set/stream/is_join (ADR-053 §Contract Surface, US-11
	// G-16) — parity with the plain create_task tool.
	if rawWriteSet, ok := args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		tk.WriteSet = writeSet
	}
	if v, ok := args["stream"].(string); ok {
		tk.Stream = v
	}
	if v, ok := args["is_join"].(bool); ok {
		tk.IsJoin = v
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

	// FR-037 provenance. CreateByAgent stamps Task.CreatedByAgentID (agent-id
	// namespace) so the created task is findable by its author — list_jobs'
	// dispatched half and list_tasks role="delegator" both filter on it, and
	// neither can use tk.CreatedBy above, which holds the SESSION OWNER
	// (a username) on this tool.
	//
	// The fallback to plain Create is deliberate and is NOT the pkg/tools
	// create_task posture. This tool is also driven with no agent principal at
	// all — the System Agent surface is reachable from a plain human session
	// (see pkg/gateway/tenancy_regression_test.go, which drives it with a bare
	// context carrying only a session owner) — and that is a legitimate,
	// human-authored task, not a misconfiguration. An unattributed task is
	// exactly what the store's plain Create path exists to write, and
	// Task.CreatedByAgent fails closed on an empty stamp, so such a task is
	// never disclosed to any agent's roster. Fabricating a caller id here, or
	// refusing the create outright, would both be worse than recording the
	// truth: nobody-in-the-agent-namespace created this.
	var createErr error
	if strings.TrimSpace(caller) != "" {
		createErr = store.CreateByAgent(&tk, caller)
	} else {
		slog.Debug("create_task_in_workspace: no calling agent on context — "+
			"persisting the task without FR-037 agent provenance",
			"workspace_id", tk.WorkspaceID, "assignee_agent_id", tk.AgentID)
		createErr = store.Create(&tk)
	}
	if createErr != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", createErr.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": tk.ID, "name": name, "status": string(tk.Status),
		"workspace_id": tk.WorkspaceID, "agent_id": tk.AgentID,
	}))
}

// deferWorkspaceDoneClaimToJudge mirrors pkg/tools/task.go's
// deferDoneClaimToJudge exactly (duplicated rather than exported/shared —
// same "criteria/timestamp-helper dedup" scope call already accepted
// elsewhere in this feature's review): a `status:"done"` write on a task WITH
// explicit acceptance criteria (hard tier, non-Scratchpad) must be
// adjudicated by the judge, never trusted as an immediate terminal write.
func deferWorkspaceDoneClaimToJudge(t *task.Task, newStatus task.Status) bool {
	return newStatus == task.StatusDone && !t.Scratchpad && len(t.Criteria) > 0
}

// ---- update_task_in_workspace ----

type TaskUpdateTool struct{ deps *Deps }

func NewTaskUpdateTool(d *Deps) *TaskUpdateTool  { return &TaskUpdateTool{deps: d} }
func (t *TaskUpdateTool) Name() string           { return "update_task_in_workspace" }
func (t *TaskUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskUpdateTool) Description() string {
	return "Update an existing task. Call this to change status, reassign, rename, or link to a workspace. Use list_tasks_in_workspace first to find the task id.\nParameters: id (required, from list_tasks_in_workspace), name, description, prompt, workspace_id, agent_id, status (inbox/next/blocked/done/failed — in_progress is reached only through real dispatch via run_task, never written directly here), due (RFC 3339), priority (1 highest to 5 lowest), blocked_by (array of task IDs, replaces existing list), result (completion summary; used as the judge's claim text — required in practice for a done claim on a task with acceptance criteria, since only the criteria's own assignee running that task can force one through). Only provided fields are updated. A status:\"done\" call on a task with acceptance criteria is NOT applied immediately — it is adjudicated by the judge during that task's own run."
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
			"priority": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     5,
				"description": "Priority 1 (highest) to 5 (lowest)",
			},
			"result": map[string]any{
				"type": "string",
				"description": "Summary of what was accomplished (used as the judge's claim text when " +
					"status:\"done\" is set on a task with acceptance criteria)",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Task IDs this task is blocked by (replaces existing list)",
			},
			"write_set": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Replacement set of concrete paths this plan member creates/edits (replaces existing list; pass [] to clear)",
			},
			"stream": map[string]any{
				"type":        "string",
				"description": "New parallel-group id this plan member belongs to (pass \"\" to clear)",
			},
			"is_join": map[string]any{
				"type":        "boolean",
				"description": "Set/clear whether this plan member is an authored join/assemble member",
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

	// FAIL CLOSED on an unresolvable caller, and do it BEFORE the store read so
	// an unprincipled call cannot even probe which task ids exist.
	//
	// This is not defence in depth, it is the gate itself. `caller` is the sole
	// input to the ownership condition below, and BOTH of that condition's
	// owner comparisons are satisfiable by an empty value on both sides: an
	// unattributed task (Task.CreatedByAgent documents "" as the normal state
	// for every REST/human-created task) plus an empty caller made the final
	// clause `"" != ""` false, which made the whole condition false, which
	// meant delegationDenied — the ONLY authorization on this path — was never
	// called at all.
	//
	// Production always injects a principal (pkg/agent/loop.go seeds every turn
	// ctx via tools.WithAgentID), so an empty one here is a wiring fault rather
	// than a legitimate human-driven call. The create path's deliberate
	// no-principal allowance (see TaskCreateTool.Execute) is NOT precedent for
	// relaxing this: recording "nobody in the agent namespace authored this new
	// task" is an honest attribution of a task that did not exist before;
	// mutating an EXISTING task that may belong to someone else is an
	// authorization decision, and there is no principal to authorize.
	caller := strings.TrimSpace(tools.ToolAgentID(ctx))
	if caller == "" {
		return tools.ErrorResult(errorJSON("PRINCIPAL_REQUIRED",
			"cannot resolve the calling agent; refusing to update a task", ""))
	}

	store := taskStoreFor(t.deps.Home)
	existing, err := store.Get(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", id),
			"Use list_tasks_in_workspace to see available tasks"))
	}

	// Ownership gate (parity with the plain update_task tool's "you can only
	// update tasks assigned to you"). The privileged cross-workspace path is
	// permitted to mutate ANOTHER agent's task ONLY when delegation policy allows
	// the caller to delegate to that task's current assignee — otherwise an agent
	// could rewrite work it has no authority over. Tasks the caller owns
	// (assignee or creator) are always mutable by the caller. Unassigned tasks
	// (no AgentID) carry no ownership constraint.
	//
	// CreatedByAgent, never CreatedBy. Task.CreatedBy is MIXED-NAMESPACE and its
	// own doc comment states the rule this predicate used to break: "both are
	// MIXED-NAMESPACE and MUST NEVER be used as an ownership or authorization
	// predicate". On THIS tool the mismatch is not even theoretical — the create
	// path above writes `tk.CreatedBy = sessionOwner`, a human USERNAME, while
	// `caller` is an agent id, so a human who registers the username `jim` hands
	// agent `jim` a pass on every task they create, skipping the delegation
	// check entirely. CreatedByAgent reads the agent-id-namespaced field and
	// fails closed on BOTH sides by construction, so "" is never a wildcard.
	if existing.AgentID != "" && existing.AgentID != caller && !existing.CreatedByAgent(caller) {
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
	pendingJudgeNote := ""
	if v, ok := args["status"].(string); ok && isValidTaskStatus(v) {
		st := task.Status(v)
		// Issue #593 (Option A): in_progress is a DISPATCH state, not a
		// caller-settable status the way done/failed are — this privileged,
		// cross-workspace twin of update_task is MORE permissive than the
		// plain tool (it can mutate another agent's task once the delegation
		// gate above clears) and was flagged as the SAME hole. The only
		// legitimate writers are the executor's ClaimForRun, REST's
		// handleTaskPatch, run_task, and set_todos-via-Create — none of which
		// call this tool. Mirror the plain update_task tool's guard (same
		// rejection shape, same run_task pointer): reject a transition INTO
		// in_progress from any other status outright, rather than persist a
		// "running" task with no session and no executor. A resend on a task
		// that is ALREADY in_progress is a harmless no-op and is let through
		// unchanged.
		if st == task.StatusInProgress && existing.Status != task.StatusInProgress {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"in_progress cannot be set directly — it is only ever reached through real dispatch; "+
					"call run_task to actually start this task", "status"))
		}
		if deferWorkspaceDoneClaimToJudge(existing, st) {
			// review r2 Chunk 1: close the privileged-tool judge-bypass — this
			// tool (update_task_in_workspace) previously wrote status:"done"
			// straight to disk and advanced dependents directly, with no judge
			// routing at all, for a task WITH acceptance criteria. Mirror the
			// plain update_task gate: only stage a claim when this call is
			// genuinely part of THAT task's own executor run (so
			// finishTaskRun, the sole reader of PendingJudgeClaim, will
			// adjudicate it) — otherwise reject outright rather than stage a
			// claim nothing will ever adjudicate.
			if tools.ToolRunningTaskID(ctx) != id {
				return tools.ErrorResult(errorJSON("JUDGE_REQUIRED",
					"this task has acceptance criteria — completion is adjudicated by the judge "+
						"during a task run; it cannot be force-completed here", "status"))
			}
			claimText, _ := args["result"].(string)
			patch.PendingJudgeClaim = &claimText
			updated = append(updated, "pending_judge_claim")
			pendingJudgeNote = "completion claim recorded — this task has acceptance criteria, so it is " +
				"NOT yet done; the evidence-ladder judge will adjudicate your claim against the criteria " +
				"before the task can reach a terminal status"
			// Status is deliberately left unpatched — the judge decides.
		} else {
			patch.Status = &st
			updated = append(updated, "status")
		}
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
	// Optional priority — parity with the plain update_task tool. Range
	// validation (1-5) is enforced by store.Update (pkg/task/store.go),
	// whose error surfaces via the generic INVALID_INPUT mapping below.
	if v, ok := args["priority"].(float64); ok {
		pr := int(v)
		patch.Priority = &pr
		updated = append(updated, "priority")
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

	// write_set/stream/is_join (ADR-053 §Contract Surface, US-11 G-16) —
	// parity with the plain update_task tool.
	if rawWriteSet, ok := args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		patch.WriteSet = &writeSet
		updated = append(updated, "write_set")
	}
	if v, ok := args["stream"].(string); ok {
		patch.Stream = &v
		updated = append(updated, "stream")
	}
	if v, ok := args["is_join"].(bool); ok {
		patch.IsJoin = &v
		updated = append(updated, "is_join")
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
	respFields := map[string]any{"id": id, "updated_fields": updated}
	if pendingJudgeNote != "" {
		respFields["pending_judge_note"] = pendingJudgeNote
	}
	return tools.NewToolResult(successJSON(respFields))
}

// isTaskNotFound reports whether err wraps task.ErrNotFound.
func isTaskNotFound(err error) bool {
	return errors.Is(err, task.ErrNotFound)
}

// ---- delete_task_in_workspace ----

type TaskDeleteTool struct{ deps *Deps }

func NewTaskDeleteTool(d *Deps) *TaskDeleteTool  { return &TaskDeleteTool{deps: d} }
func (t *TaskDeleteTool) Name() string           { return "delete_task_in_workspace" }
func (t *TaskDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskDeleteTool) Description() string {
	return "Permanently delete a to-do/task item from a workspace by id. Irreversible. You may only delete " +
		"a task you own (creator or assignee) — deleting another agent's task requires delegation trust to " +
		"that agent within the workspace, or the call is refused. Use list_tasks_in_workspace first to find " +
		"the task id.\nParameters: id (required), confirm (bool, must be true)."
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
	// FAIL CLOSED on an unresolvable caller, before the store read — same
	// reasoning as update_task_in_workspace above, which documents in full why
	// an empty principal previously slipped past this gate entirely (empty
	// caller + unattributed task made every clause of the condition false, so
	// delegationDenied was never reached) and why the create path's deliberate
	// no-principal allowance is not precedent for a destructive operation.
	caller := strings.TrimSpace(tools.ToolAgentID(ctx))
	if caller == "" {
		return tools.ErrorResult(errorJSON("PRINCIPAL_REQUIRED",
			"cannot resolve the calling agent; refusing to delete a task", ""))
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
	//
	// CreatedByAgent, never CreatedBy — see update_task_in_workspace's gate for
	// the full namespace rationale (Task.CreatedBy holds a human username on
	// this surface, so comparing it against an agent id is a namespace
	// collision, not an ownership test).
	if existing.AgentID != "" && existing.AgentID != caller && !existing.CreatedByAgent(caller) {
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

// maxWorkspaceTaskRows bounds one list_tasks_in_workspace response.
//
// The task store grows monotonically and nothing sweeps it, so a long-lived
// install exceeding this bound is the steady state rather than the exception —
// which is why crossing it is REPORTED (truncated + matched) rather than
// silently absorbed. A short list that looks complete is the worst possible
// output for a tool whose entire job is "find the id of the task I need to act
// on". Sized to match list_jobs' own effective row ceiling (95).
const maxWorkspaceTaskRows = 100

// workspaceTaskRow is the ALLOWLIST projection list_tasks_in_workspace returns
// in place of the on-disk task.Task struct.
//
// Allowlist, never denylist, and task.Task is never marshalled whole. That is
// the load-bearing property. task.Task carries fields whose own doc comments
// declare them DISK-ONLY and forbid them from crossing any boundary —
// CreatedByAgentID ("the REST mapper does NOT copy it to the wire type, and it
// MUST NOT be added to any schema in contracts/"), Scratchpad (every agent's
// private todo card), PendingJudgeClaim, DelegationDepth — and a whole-struct
// marshal shipped every one of them, plus Prompt and Result, straight into an
// LLM's context. With an allowlist a field added to task.Task tomorrow is NOT
// disclosed by default; with a denylist the next field to land would re-open
// exactly this hole.
type workspaceTaskRow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Relation says WHY this row is the caller's, mirroring list_jobs:
	// "runs" = assigned to the caller, "dispatched" = created by the caller.
	Relation    string `json:"relation"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	// Priority, WriteSet, and Stream (M7 fix): create_task_in_workspace/
	// update_task_in_workspace accept all three, but before this fix nothing
	// on the tool read surface ever showed them back to the calling agent —
	// which is exactly what let the M2 priority-validation gap go unnoticed
	// (a caller had no way to verify what was actually persisted). Priority
	// uses EffectivePriority() (never 0) so a task created without an explicit
	// priority still reads back a meaningful, real value (3), matching what
	// the REST read surface (toWireTask) already shows.
	Priority  int      `json:"priority,omitempty"`
	Due       string   `json:"due,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	WriteSet  []string `json:"write_set,omitempty"`
	Stream    string   `json:"stream,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// workspaceTaskListResponse is the list_tasks_in_workspace envelope. `matched`
// and `truncated` exist so a bounded list is never mistaken for a complete one.
type workspaceTaskListResponse struct {
	Tasks     []workspaceTaskRow `json:"tasks"`
	Matched   int                `json:"matched"`
	Returned  int                `json:"returned"`
	Truncated bool               `json:"truncated,omitempty"`
	Note      string             `json:"note,omitempty"`
}

type TaskListTool struct {
	deps *Deps
	// auditLogger records one entry per call. Propagated by the tool registry
	// via the auditLoggerAware contract (pkg/tools/registry.go); nil is a
	// best-effort no-op, never an error.
	auditLogger *audit.Logger
}

func NewTaskListTool(d *Deps) *TaskListTool    { return &TaskListTool{deps: d} }
func (t *TaskListTool) Name() string           { return "list_tasks_in_workspace" }
func (t *TaskListTool) Scope() tools.ToolScope { return tools.ScopeCore }

// SetAuditLogger satisfies the auditLoggerAware contract so the registry
// propagates the audit logger after construction.
func (t *TaskListTool) SetAuditLogger(logger *audit.Logger) { t.auditLogger = logger }

func (t *TaskListTool) Description() string {
	return "List YOUR OWN tasks across workspaces: tasks assigned to you, plus tasks you created " +
		"for other agents. It never returns another agent's or a human's tasks, so an empty result " +
		"means you have no matching work — not that the workspace is empty. Bounded to the 100 " +
		"most recently updated matches; `truncated` and `matched` say when there were more.\n" +
		"Parameters: workspace_id, agent_id, status (all optional, all narrow the result further)."
}

func (t *TaskListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workspace_id": map[string]any{
				"type":        "string",
				"description": "Narrow to one workspace (from list_workspaces). Optional.",
			},
			"agent_id": map[string]any{
				"type": "string",
				"description": "Narrow to tasks assigned to this agent. Only ever narrows within " +
					"your own tasks — naming another agent shows the work you dispatched to them, " +
					"never their own.",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "Narrow to one status: inbox, next, in_progress, blocked, done, failed.",
			},
		},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// FAIL CLOSED on an unresolvable caller, exactly as the plain list_tasks
	// and list_jobs do.
	//
	// Every argument this tool accepts is optional, and task.Filter treats each
	// empty field as "filter OFF" (Filter.matches, pkg/task/store.go). So
	// without this guard — and without the caller scope applied below —
	// `list_tasks_in_workspace {}` asked the store for EVERY task file in
	// $OMNIPUS_HOME/tasks: every workspace, every agent, every human. This tool
	// resolves `allow` from the global seed (pkg/config/defaults.go), so that
	// was reachable from any agent's turn, not just the Orchestrator's.
	//
	// Never relax this into "return an empty list": a silent empty success is
	// indistinguishable from genuinely having no tasks and hides the
	// misconfiguration that produced it.
	caller := strings.TrimSpace(tools.ToolAgentID(ctx))
	if caller == "" {
		t.writeAudit(ctx, audit.DecisionError, "", nil, 0, 0, false)
		return tools.ErrorResult(errorJSON("PRINCIPAL_REQUIRED",
			"cannot resolve the calling agent; refusing to list tasks", ""))
	}

	workspaceFilter := strings.TrimSpace(argString(args, "workspace_id"))
	agentFilter := strings.TrimSpace(argString(args, "agent_id"))
	statusFilter := strings.TrimSpace(argString(args, "status"))
	if statusFilter != "" && !isValidTaskStatus(statusFilter) {
		// Rejected rather than passed through: an unknown status matches
		// nothing, and returning an empty list for a typo would teach the
		// caller its work had vanished.
		t.writeAudit(ctx, audit.DecisionError, caller, map[string]any{
			"workspace_id": workspaceFilter, "agent_id": agentFilter, "status": statusFilter,
		}, 0, 0, false)
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("unknown status %q: expected one of inbox, next, in_progress, blocked, done, failed",
				statusFilter), "status"))
	}

	auditDetails := map[string]any{
		"workspace_id": workspaceFilter, "agent_id": agentFilter, "status": statusFilter,
	}

	store := taskStoreFor(t.deps.Home)
	// The caller's own arguments only ever NARROW. The authorization scope is
	// applied below, over the returned records, and cannot be widened by any
	// argument.
	records, err := store.List(task.Filter{
		WorkspaceID: workspaceFilter,
		AgentID:     agentFilter,
		Status:      task.Status(statusFilter),
	})
	if err != nil {
		t.writeAudit(ctx, audit.DecisionError, caller, auditDetails, 0, 0, false)
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}

	// Caller scope: the UNION of the two agent-id-namespaced readings of
	// ownership, identical to list_jobs' task collector.
	//
	//	AgentID == caller      -> "runs"       (work assigned to me)
	//	CreatedByAgent(caller) -> "dispatched" (work I created)
	//
	// Never Task.CreatedBy or Task.Owner: both are mixed-namespace (this file's
	// own create path writes a human username into CreatedBy) and using either
	// would disclose a human's task titles to a same-named agent — the exact
	// collision Task.CreatedBy's doc comment forbids.
	//
	// Cross-WORKSPACE reach is preserved deliberately: that is this tool's
	// entire reason to exist, and it is what the Orchestrator uses it for. What
	// is removed is cross-PRINCIPAL reach, which was never stated as intended
	// anywhere in this file — the "privileged" framing in the comments above
	// attaches only to the mutation gates, each of which still consults
	// delegation policy.
	scoped := make([]*task.Task, 0, len(records))
	for i := range records {
		tk := &records[i]
		assigned := tk.AgentID != "" && tk.AgentID == caller
		if !assigned && !tk.CreatedByAgent(caller) {
			continue
		}
		scoped = append(scoped, tk)
	}

	// Deterministic order BEFORE the bound, so which rows survive truncation is
	// a stated rule (most recently updated first) rather than directory order.
	sort.SliceStable(scoped, func(i, j int) bool {
		if scoped[i].UpdatedAt != scoped[j].UpdatedAt {
			return scoped[i].UpdatedAt > scoped[j].UpdatedAt
		}
		return scoped[i].ID > scoped[j].ID
	})

	matched := len(scoped)
	truncated := matched > maxWorkspaceTaskRows
	if truncated {
		scoped = scoped[:maxWorkspaceTaskRows]
	}

	rows := make([]workspaceTaskRow, 0, len(scoped))
	for _, tk := range scoped {
		relation := "dispatched"
		if tk.AgentID != "" && tk.AgentID == caller {
			relation = "runs"
		}
		rows = append(rows, workspaceTaskRow{
			ID:          tk.ID,
			Title:       tk.Title,
			Status:      string(tk.Status),
			Relation:    relation,
			WorkspaceID: tk.WorkspaceID,
			AgentID:     tk.AgentID,
			PlanID:      tk.PlanID,
			Priority:    tk.EffectivePriority(),
			Due:         tk.Due,
			BlockedBy:   tk.BlockedBy,
			WriteSet:    tk.WriteSet,
			Stream:      tk.Stream,
			CreatedAt:   tk.CreatedAt,
			UpdatedAt:   tk.UpdatedAt,
		})
	}

	resp := workspaceTaskListResponse{
		Tasks:    rows,
		Matched:  matched,
		Returned: len(rows),
	}
	if truncated {
		resp.Truncated = true
		resp.Note = fmt.Sprintf(
			"showing the %d most recently updated of %d matching tasks — narrow with workspace_id, agent_id or status",
			len(rows), matched)
	}
	t.writeAudit(ctx, audit.DecisionAllow, caller, auditDetails, matched, len(rows), truncated)
	return tools.NewToolResult(successJSON(resp))
}

// writeAudit records exactly one entry per call, including a rejected one.
//
// This is a security control, not a debugging aid: the tool enumerates task
// handles and would enumerate another principal's if the scope above were ever
// wrong, so a scoping regression must leave a forensic trail. Task TITLES are
// deliberately never recorded — they are precisely what the scope protects.
// A nil logger is a no-op; an audit write failure never fails the call.
func (t *TaskListTool) writeAudit(
	ctx context.Context,
	decision, caller string,
	filters map[string]any,
	matched, returned int,
	truncated bool,
) {
	if t.auditLogger == nil {
		return
	}
	details := map[string]any{
		"matched":   matched,
		"returned":  returned,
		"truncated": truncated,
	}
	for k, v := range filters {
		details["filter_"+k] = v
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  decision,
		AgentID:   caller,
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Tool:      "list_tasks_in_workspace",
		Details:   details,
	}
	if err := t.auditLogger.Log(entry); err != nil {
		slog.Warn("list_tasks_in_workspace: audit log failed", "agent_id", caller, "error", err)
	}
}

// argString reads a string tool argument, tolerating an absent or non-string
// value as "unset" — the same lenient decode the rest of this file uses.
func argString(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}
