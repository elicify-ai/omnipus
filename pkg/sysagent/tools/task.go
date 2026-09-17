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

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// unifiedTask is the canonical on-disk task type used by the sysagent tools.
// Using task.Task ensures field-preserving read-modify-write: all fields survive
// a round-trip through writeEntity.
type unifiedTask = task.Task

func tasksDir(home string) string { return filepath.Join(home, "tasks") }

// This file has NO goal-store wrapper and NO task-delete goal cleanup of its
// own. The goal store is constructed inline as goal.NewStore(t.deps.Home) —
// the retired goalStoreForWorkspace added nothing to that one call — and the
// delete cleanup is tools.RemoveTaskGoalRecords (pkg/tools/task.go), the single
// implementation shared with the REST surface and the plain delete_task tool.
// The private copies that used to live here (goalTerminalReasonOwnerDeleted,
// terminateGoalForOwnerDeletion, removeTaskGoalRecords) were byte-identical
// mirrors and are deleted; mirroring is the shape that let the sibling
// task-terminal hook reach only three of its seven writers.

// syncWorkspaceTaskGoalRecord mirrors pkg/tools/task.go's syncTaskGoalRecord
// exactly (ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029) — see its doc
// comment for the full contract. Duplicated per this file's own established
// convention (parseCriteriaArgsFromWorkspaceTool, allCheckCriteriaWorkspace,
// deferWorkspaceDoneClaimToJudge all mirror a pkg/tools/task.go twin rather
// than import it).
//
// THE CRITERIA COME FROM t, NEVER FROM THE CALLER. This signature used to take
// a `criteria []task.AcceptanceCriterion` alongside criteriaProvided, and every
// one of its call sites handed it the PRE-normalisation slice it had just given
// to the task store — the one whose criteria still carry empty ids. The store
// mints ids into its own deep copy (task.normalizeCriteria), and goal.New /
// Goal.SetCriteria then mint a SECOND, different set for the same text. The
// criterion id is the join key the verdict projection de-unions the Judge's
// result on (GOAL-FR-007/FR-041), so the same criterion ended up reading `met`
// on the task and `pending` on its goal record. Taking the list off t makes
// that divergence unrepresentable rather than merely fixed at three call sites.
func syncWorkspaceTaskGoalRecord(
	home string,
	t *task.Task,
	criteriaProvided bool,
	dod []task.AcceptanceCriterion, dodProvided bool,
	goalMaxRoundsFn func() int,
) error {
	if !criteriaProvided && !dodProvided {
		return nil
	}
	criteria := t.Criteria
	gs := goal.NewStore(home)
	now := time.Now().UTC()
	existing, err := gs.GetByOwner(generated.GoalOwnerKindTask, t.ID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			return fmt.Errorf("load paired goal record: %w", err)
		}
		if !criteriaProvided || !dodProvided {
			return fmt.Errorf(
				"this task has no existing Definition of Done — saving criteria or dod for the " +
					"first time requires supplying BOTH together (GOAL-FR-048)")
		}
		maxRounds := config.DefaultGoalMaxRounds
		if goalMaxRoundsFn != nil {
			maxRounds = goalMaxRoundsFn()
		}
		// goal.New requires a non-empty Prompt; fall back to Title (always
		// present) when the task carries no Prompt.
		goalPrompt := t.Prompt
		if goalPrompt == "" {
			goalPrompt = t.Title
		}
		g, nErr := goal.New(
			generated.GoalOwnerKindTask, t.ID, generated.GoalSourceTaskExplicit,
			goalPrompt, "", criteria, dod, maxRounds, now,
		)
		if nErr != nil {
			return fmt.Errorf("build goal record: %w", nErr)
		}
		if cErr := gs.Create(g); cErr != nil {
			return fmt.Errorf("create task goal record: %w", cErr)
		}
		return nil
	}
	_, err = gs.Update(existing.GoalID, func(g *goal.Goal) error {
		if criteriaProvided {
			if sErr := g.SetCriteria(criteria, now); sErr != nil {
				return fmt.Errorf("Goal.SetCriteria: %w", sErr)
			}
		}
		if dodProvided {
			if sErr := g.SetDoD(dod, now); sErr != nil {
				return fmt.Errorf("Goal.SetDoD: %w", sErr)
			}
		}
		return nil
	})
	return fmt.Errorf("syncWorkspaceTaskGoalRecord: %w", err)
}

// pairedWorkspaceGoalDoD returns the Definition of Done currently persisted on
// the goal record paired with taskID, or nil when the task has no paired record
// at all (a legacy pre-D-C task, GOAL-FR-023/FR-048). Mirrors pkg/tools's
// pairedGoalDoD.
//
// It exists for the one edit shape that cannot evaluate the distinctness rule
// (GOAL-FR-021/FR-047/FR-048) from the call arguments alone: an update that
// replaces `criteria` and leaves `dod` untouched has to compare the NEW criteria
// against the DoD already on file, and the task record has no DoD field to read
// it from (ADR-086 D5). A read fault is returned, never swallowed.
func pairedWorkspaceGoalDoD(home, taskID string) ([]task.AcceptanceCriterion, error) {
	g, err := goal.NewStore(home).GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("load paired goal record: %w", err)
	}
	return g.DoD, nil
}

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
//
// Behavior payloads (ADR-052 FR-034 / ADR-074 D3a) decode via the shared
// task.DecodeBehaviorPayload, which honors the pointer semantics
// pkg/task/criterion.go documents (absent min_count/max_count stay nil; an
// explicit 0 decodes to a pointer at 0).
func parseCriteriaArgsFromWorkspaceTool(raw []any, authorAgentID string) ([]task.AcceptanceCriterion, error) {
	out := make([]task.AcceptanceCriterion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("criteria[%d]: must be an object", i)
		}
		kind, _ := m["kind"].(string)
		judgment, _ := m["judgment"].(string)
		text, _ := m["text"].(string)
		c := task.AcceptanceCriterion{
			Kind:     task.CriterionKind(kind),
			Judgment: task.JudgmentKind(judgment),
			Text:     text,
			Author:   task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: authorAgentID},
		}
		if chk, ok := m["check"].(map[string]any); ok {
			command, _ := chk["command"].(string)
			var expectedExitCode int
			if v, ok := chk["expected_exit_code"].(float64); ok {
				expectedExitCode = int(v)
			}
			c.Check = &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExitCode}
		}
		if beh, ok := m["behavior"].(map[string]any); ok {
			c.Behavior = task.DecodeBehaviorPayload(beh)
		}
		// ADR-074 D2: kind is optional at authoring time — resolve it from the
		// payload shape HERE, before the caller's ADR-049 D2-rule-5 all-check
		// bash-policy gate runs, so the gate fires on inferred kinds too. An
		// explicit kind passes through unchanged; kind-less with BOTH payloads
		// is rejected as ambiguous.
		k, kErr := task.InferCriterionKind(&c)
		if kErr != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, kErr)
		}
		c.Kind = k
		// ADR-080 D-TYPES: judgment is likewise optional at authoring time —
		// resolve it from the now-resolved kind HERE, mirroring
		// pkg/tools/task.go's twin exactly.
		j, jErr := task.InferJudgment(&c)
		if jErr != nil {
			return nil, fmt.Errorf("criteria[%d]: %w", i, jErr)
		}
		c.Judgment = j
		out = append(out, c)
	}
	return out, nil
}

// workspaceUpdateAssigneeCannotFinish is update_task_in_workspace's readiness
// question (founder decision 2026-09-15) — the same rule as the plain
// update_task tool: asked only when the update changes who does the task or
// what it is judged against, over the effective post-update agent, criteria
// and Definition of Done. A stored Definition of Done that cannot be read is
// left out with a warning, acting only on what is known.
func workspaceUpdateAssigneeCannotFinish(
	checker tools.AssigneeReadinessChecker, home string, existing *task.Task, newAgentID *string,
	newCriteria, newDoD []task.AcceptanceCriterion, criteriaProvided, dodProvided bool,
) *tools.AssigneeCannotFinishError {
	agentChanged := newAgentID != nil && *newAgentID != existing.AgentID
	if !agentChanged && !criteriaProvided && !dodProvided {
		return nil
	}
	agentID := existing.AgentID
	if agentChanged {
		agentID = *newAgentID
	}
	criteria := newCriteria
	if !criteriaProvided {
		criteria = existing.Criteria
	}
	judged := append([]task.AcceptanceCriterion{}, criteria...)
	if dodProvided {
		judged = append(judged, newDoD...)
	} else if persisted, err := pairedWorkspaceGoalDoD(home, existing.ID); err != nil {
		slog.Warn("update_task_in_workspace: the task's Definition of Done could not be read — checking the "+
			"assignee against its acceptance criteria only", "task_id", existing.ID, "error", err)
	} else {
		judged = append(judged, persisted...)
	}
	return tools.AssigneeCannotFinishRefusal("update_task_in_workspace", checker, agentID, judged)
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

// This package has NO plan-linkage validator of its own — create_task_in_
// workspace calls tools.ValidateTaskPlanMembership (pkg/tools/plan.go)
// directly, the same choke point the plain create_task tool and the REST
// surface use. The retired local copy here (validateTaskPlanLinkageWorkspace)
// was a byte-for-byte duplicate kept only because the original was
// unexported; exporting it removed the reason for the copy. Do not re-add
// one — a second copy is how the rule drifts.

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
	return "Create a task on the workspace board. Call this when the user wants to create, add, or track a task or action item. If the user mentioned a workspace name, call list_workspaces first to get the workspace_id.\nParameters: name (required, the task title), description (optional), prompt (optional, agent instruction), workspace_id (required, from list_workspaces), agent_id (optional, agent to assign), status (optional: inbox=new/untriaged, next=ready, blocked, done, failed — defaults to inbox; in_progress is rejected — it is only ever reached through real dispatch via run_task, never persisted directly), due (optional, RFC 3339 due date/time), priority (optional, 1 highest to 5 lowest, default 3), plan_id (optional, ID of the Plan this task is a member of — must exist in the same workspace and must still be a draft plan; a plan's membership is frozen at approval), write_set (optional, array of concrete paths this plan member creates/edits; meaningful only alongside plan_id), stream (optional, the parallel-group id this plan member belongs to), is_join (optional, true marks this plan member as an authored join/assemble member), blocked_by (optional, array of task IDs this task is blocked by), criteria (REQUIRED when agent_id is set: at least one acceptance criterion), dod (REQUIRED when agent_id is set: at least one definition-of-done item, distinct from criteria — GOAL-FR-021/D-C). Before authoring acceptance criteria or dod, load the define-goal skill (via the Skill tool) and follow its quality bar. Assigning agent_id to an agent other than yourself is delegation and requires delegation trust to that agent within the workspace, or the call is refused. If every acceptance criterion is kind=check, the assignee must have bash policy allow — otherwise the criteria set could never be satisfied and the create is rejected. An unknown status value is rejected, not defaulted."
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
				"description": "ID of the Plan this task is a member of (optional). Must exist in the same workspace and must still be a DRAFT plan — a plan's membership is frozen at approval, so an approved, running, done or failed plan rejects a new member.",
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
							"enum": []string{"check", "prose", "behavior"},
							"description": "check: a shell command verified via the assignee's own bash tool; " +
								"prose: a free-text statement judged by the Judge System Agent; " +
								"behavior: a deterministic count of successful calls of a named tool in the " +
								"session's tool-call log. Optional (ADR-074 D2) — when omitted, inferred " +
								"from the payload: check payload => check, behavior payload => behavior, " +
								"no payload => prose. An explicit kind mismatching its payload is rejected.",
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
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Acceptance criteria — the outcome-specific checks. REQUIRED (at least " +
					"one) when agent_id is set — an agent-assigned task with zero criteria is rejected.",
			},
			"dod": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type":        "string",
							"enum":        []string{"check", "prose", "behavior"},
							"description": "See criteria.kind — same inference rules.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "The definition-of-done statement (1-1000 characters)",
						},
						"check": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command":            map[string]any{"type": "string", "description": "Shell command to run"},
								"expected_exit_code": map[string]any{"type": "integer", "minimum": 0, "maximum": 255},
							},
							"description": "Required when kind is \"check\"; must be omitted for other kinds",
						},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Definition of Done (GOAL-FR-003/FR-021/FR-048) — generic standing " +
					"quality gates, DISTINCT from criteria and never mixed into it, judged identically. " +
					"REQUIRED (at least one) when agent_id is set.",
			},
		},
		"required": []string{"name", "workspace_id"},
	}
}

// taskCreateToolExecute carries the shared state of Execute across its stages.
type taskCreateToolExecute struct {
	t       *TaskCreateTool
	ctx     context.Context
	args    map[string]any
	name    string
	status  task.Status
	tk      unifiedTask
	caller  string
	goalDoD []task.AcceptanceCriterion
	store   *task.Store
}

func (t *TaskCreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tc := &taskCreateToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := tc.validateArgs(); stop {
		return r0
	}
	tc.initTask()
	if r0, stop := tc.resolveWorkspace(); stop {
		return r0
	}

	if r0, stop := tc.enforceDelegation(); stop {
		return r0
	}

	if r0, stop := tc.enforceCriteriaContract(); stop {
		return r0
	}

	if r0, stop := tc.applySimpleFields(); stop {
		return r0
	}

	if r0, stop := tc.validateBlockers(); stop {
		return r0
	}

	if r0, stop := tc.persistTask(); stop {
		return r0
	}

	if r0, stop := tc.syncGoalRecord(); stop {
		return r0
	}

	return tc.respond()
}

// validateArgs requires a name and parses status, rejecting unknown values and in_progress.
func (tc *taskCreateToolExecute) validateArgs() (*tools.ToolResult, bool) {
	tc.name, _ = tc.args["name"].(string)
	if tc.name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "")), true
	}
	tc.status = task.StatusInbox
	if v, ok := tc.args["status"].(string); ok && v != "" {
		// An unknown status is rejected outright, not silently defaulted to
		// inbox — same reasoning as list_tasks_in_workspace's own status
		// filter: defaulting a typo would teach the caller its requested
		// status was applied when it never was.
		if !isValidTaskStatus(v) {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				fmt.Sprintf("unknown status %q: expected one of inbox, next, blocked, done, failed", v), "status")), true
		}
		// Issue #593 (Option A): in_progress is a DISPATCH state, never a
		// caller-settable value at create time either — mirrors the guard
		// update_task_in_workspace already enforces on the same transition.
		// Without this, a caller could persist a "running" task with no
		// session and no executor by simply creating it that way, bypassing
		// the update-path guard entirely.
		if task.Status(v) == task.StatusInProgress {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"in_progress cannot be set directly — it is only ever reached through real dispatch; "+
					"create the task as next and call run_task to start it", "status")), true
		}
		tc.status = task.Status(v)
	}
	return nil, false
}

// initTask mints the task id and builds the task shell with title, status, description and prompt.
func (tc *taskCreateToolExecute) initTask() {
	id := ulid.Make().String()
	tc.tk = unifiedTask{
		ID:     id,
		Title:  tc.name,
		Action: task.ActionLLM,
		Status: tc.status,
	}
	if v, ok := tc.args["description"].(string); ok {
		tc.tk.Description = v
	}
	if v, ok := tc.args["prompt"].(string); ok {
		tc.tk.Prompt = v
	}
}

// resolveWorkspace requires and validates workspace_id, then resolves the task's owner and creator.
func (tc *taskCreateToolExecute) resolveWorkspace() (*tools.ToolResult, bool) {
	sessionOwner := tools.ToolSessionOwner(tc.ctx)
	workspaceID, _ := tc.args["workspace_id"].(string)
	if workspaceID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "workspace_id is required", "workspace_id")), true
	}
	if err := validateID(workspaceID); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id")), true
	}
	ws, wsErr := readWorkspaceFromDisk(tc.t.deps.Home, workspaceID)
	if wsErr != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id")), true
	}
	tc.tk.WorkspaceID = workspaceID
	if ws.Owner != "" {
		tc.tk.Owner = ws.Owner
	}
	if tc.tk.Owner == "" && sessionOwner != "" {
		tc.tk.Owner = sessionOwner
	}
	tc.tk.CreatedBy = sessionOwner
	return nil, false
}

// enforceDelegation gates assigning the task to another agent through the delegation policy.
func (tc *taskCreateToolExecute) enforceDelegation() (*tools.ToolResult, bool) {
	// FR-6.2 delegation gate (parity with the plain create_task tool). The
	// cross-workspace surface is the PRIVILEGED Orchestrator path, so it must
	// enforce the SAME trust-set + mode("task") + depth policy the same-workspace
	// create_task enforces — assigning work to ANOTHER agent is delegation.
	tc.caller = tools.ToolAgentID(tc.ctx)
	if v, ok := tc.args["agent_id"].(string); ok {
		tc.tk.AgentID = v
	}
	// subagent_3p (external-CLI) worker task assignment is no longer guarded
	// here: AgentLoop.processTaskDirect (pkg/agent/loop.go) now branches on
	// runner.ResolveDispatch and routes an external-CLI worker's task run
	// through runExternalCLISubTurn instead of the native engine — see its
	// doc comment for the dispatch design.
	if tc.t.deps.DelegationDeny != nil && tc.tk.AgentID != "" && tc.tk.AgentID != tc.caller {
		if denial := tc.t.deps.DelegationDeny(tc.ctx, tc.caller, tc.tk.AgentID); denial != nil {
			return tools.DelegationDeniedResult(tc.t.Name(), denial), true
		}
	}
	return nil, false
}

// enforceCriteriaContract enforces the criteria/dod contract, distinctness, bash-policy satisfiability and assignee readiness for an assigned task.
func (tc *taskCreateToolExecute) enforceCriteriaContract() (*tools.ToolResult, bool) {
	// FR-6/D5 strict criteria enforcement (ADR-049, SD-A7, review r1 major
	// M5, parity with the plain create_task tool): an agent-ASSIGNED task
	// requires at least one acceptance criterion — only meaningful once
	// AgentID is set at all (an unassigned, human-tracking-only task never
	// enters the goal loop/judge machinery, so criteria enforcement does not
	// apply to it).
	// Only the DoD needs carrying past this block: the criteria reach the goal
	// record off tk.Criteria, which store.Create normalises in place (see
	// syncWorkspaceTaskGoalRecord's doc comment on why the criteria may not be
	// passed separately).

	if tc.tk.AgentID != "" {
		rawCriteria, _ := tc.args["criteria"].([]any)
		if len(rawCriteria) == 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"Add at least one acceptance criterion: say what must be true for this task to be done.",
				"criteria")), true
		}
		criteria, cErr := parseCriteriaArgsFromWorkspaceTool(rawCriteria, tc.caller)
		if cErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", cErr.Error(), "criteria")), true
		}
		// GOAL-FR-021/D-C: dod is mandatory alongside criteria, uniformly,
		// on every task-creation surface (R-25) — this cross-workspace twin
		// included. Same conditionality as criteria above: only meaningful
		// once AgentID is set (an unassigned, human-tracking-only task never
		// enters the goal loop/judge machinery).
		rawDoD, _ := tc.args["dod"].([]any)
		if len(rawDoD) == 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"Add at least one Definition of Done item, distinct from the acceptance criteria.", "dod")), true
		}
		dod, dErr := parseCriteriaArgsFromWorkspaceTool(rawDoD, tc.caller)
		if dErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", dErr.Error(), "dod")), true
		}
		// The DISTINCTNESS half of the same rule the refusal above advertises.
		// It was advertised on every surface and checked on none: a task whose
		// DoD restates its acceptance criteria spends a second judged slot on a
		// sentence already being scored (the judge unions Criteria and DoD).
		// See task.ValidateDoDDistinct for the rule and for why it stops at
		// whitespace and case. Checked before any store write.
		if vErr := task.ValidateDoDDistinct(criteria, dod); vErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", vErr.Error(), "dod")).WithError(vErr), true
		}
		tc.goalDoD = dod
		// D2 rule 5 (FR-017/052): an all-check criteria set can never be
		// adjudicated MET if the assignee's effective bash policy is deny or
		// ask (ask resolves to deny unattended at judge time, D2 rule 2).
		if allCheckCriteriaWorkspace(criteria) {
			if tc.t.deps.ResolveBashPolicy == nil {
				// FAIL CLOSED, not open, when no checker is wired — same
				// rationale as the delegation gate above.
				slog.Error("create_task_in_workspace: no bash-policy resolver installed — denying an "+
					"all-check criteria set by default",
					"caller_id", tc.caller, "target_agent_id", tc.tk.AgentID)
				return tools.ErrorResult(errorJSON("INVALID_INPUT",
					"cannot verify the assignee's bash policy (D2 rule 5 resolver not configured) — "+
						"denying an all-machine-criteria create by default", "criteria")), true
			}
			policy, ok := tc.t.deps.ResolveBashPolicy(tc.tk.AgentID)
			if !ok || policy != string(config.ToolPolicyAllow) {
				resolved := "unresolvable"
				if ok {
					resolved = policy
				}
				return tools.ErrorResult(errorJSON("INVALID_INPUT", fmt.Sprintf(
					"all criteria are machine-checkable (kind=check) but agent %q's effective bash "+
						"policy is %q — this criteria set could never be satisfied (structurally "+
						"unsatisfiable, ADR-049 D2 rule 5)",
					tc.tk.AgentID, resolved,
				), "criteria")), true
			}
		}
		// Founder decision 2026-09-15 (parity with the plain create_task tool):
		// refuse an assignee that cannot finish this task — denied goal_claim,
		// or a check its bash policy cannot run — before anything is written.
		if refusal := tools.AssigneeCannotFinishRefusal("create_task_in_workspace", tc.t.deps.AssigneeCannotFinish,
			tc.tk.AgentID, append(append([]task.AcceptanceCriterion{}, criteria...), dod...)); refusal != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", refusal.Reason, refusal.Field)).WithError(refusal), true
		}
		tc.tk.Criteria = criteria
	}
	return nil, false
}

// applySimpleFields applies due, priority, plan_id, write_set, stream, is_join and blocked_by from the args.
func (tc *taskCreateToolExecute) applySimpleFields() (*tools.ToolResult, bool) {
	if v, ok := tc.args["due"].(string); ok && v != "" {
		tc.tk.Due = v
	}

	// Optional priority: priority is a general task attribute, not gated
	// behind plan_id, so its absence here (while the plain create_task tool
	// accepts it) was a genuine schema-consistency gap rather than a
	// deliberate restriction. Unset (0) is treated as 3 on read
	// (task.Task.EffectivePriority), matching create_task's own default.
	if v, ok := tc.args["priority"].(float64); ok {
		pr := int(v)
		// args["priority"] IS present (ok==true), so this is an EXPLICIT value —
		// including an explicit 0, which task.ValidatePriority rejects with no
		// exception (unlike Task.Priority's own "0 = unset" struct-field
		// contract). Shared with the plain create_task tool, update_task_in_
		// workspace's store-layer check, and REST's create handler so "priority
		// must be between 1 and 5" can never drift across entry points again
		// (M2(b)).
		if err := task.ValidatePriority(pr); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "priority")), true
		}
		tc.tk.Priority = pr
	}

	// Optional plan_id (ADR-052 FR-002): same-workspace FK + draft-only
	// membership (tools.ValidateTaskPlanMembership — see its doc for why an
	// approved/running plan refuses a new member). A call with no plan_id is
	// entirely unaffected — the check is a no-op for planID == "".
	if v, ok := tc.args["plan_id"].(string); ok && v != "" {
		if pErr := tools.ValidateTaskPlanMembership(tc.t.deps.PlanStore, v, tc.tk.WorkspaceID); pErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", pErr.Error(), "plan_id")), true
		}
		tc.tk.PlanID = v
	}

	// Optional write_set/stream/is_join (ADR-053 §Contract Surface, US-11
	// G-16) — parity with the plain create_task tool.
	if rawWriteSet, ok := tc.args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		tc.tk.WriteSet = writeSet
	}
	if v, ok := tc.args["stream"].(string); ok {
		tc.tk.Stream = v
	}
	if v, ok := tc.args["is_join"].(bool); ok {
		tc.tk.IsJoin = v
	}

	if rawDeps, ok := tc.args["blocked_by"].([]any); ok && len(rawDeps) > 0 {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		tc.tk.BlockedBy = deps
	}
	return nil, false
}

// validateBlockers checks every blocked_by edge points at a task in the same target workspace.
func (tc *taskCreateToolExecute) validateBlockers() (*tools.ToolResult, bool) {
	// Create via the store so DAG validation + atomic write + locking apply.
	tc.store = taskStoreFor(tc.t.deps.Home)

	// Same-workspace blocker guard (parity with validateBlockersWorkspace in the
	// plain tool): every blocked_by edge must point at a task in the SAME target
	// workspace. The store validates the DAG (cycle/self-edge/missing/depth) but
	// NOT this cross-workspace rule, so enforce it at the tool layer.
	if len(tc.tk.BlockedBy) > 0 {
		if wErr := validateBlockersSameWorkspace(tc.store, tc.tk.WorkspaceID, tc.tk.BlockedBy); wErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", wErr.Error(), "blocked_by")), true
		}
	}
	return nil, false
}

// persistTask creates the task via the store, stamping agent provenance when a caller exists.
func (tc *taskCreateToolExecute) persistTask() (*tools.ToolResult, bool) {
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
	if strings.TrimSpace(tc.caller) != "" {
		createErr = tc.store.CreateByAgent(&tc.tk, tc.caller)
	} else {
		slog.Debug("create_task_in_workspace: no calling agent on context — "+
			"persisting the task without FR-037 agent provenance",
			"workspace_id", tc.tk.WorkspaceID, "assignee_agent_id", tc.tk.AgentID)
		createErr = tc.store.Create(&tc.tk)
	}
	if createErr != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", createErr.Error(), "")), true
	}
	return nil, false
}

// syncGoalRecord authors the task's Definition of Done onto its paired goal record for an assigned task.
func (tc *taskCreateToolExecute) syncGoalRecord() (*tools.ToolResult, bool) {
	// ADR-086 D2/D5, GOAL-FR-003/FR-012/FR-021/FR-029: mirrors the plain
	// create_task tool — a task's Definition of Done is authored ahead of
	// time onto its own paired goal record (stays "defining" until the task
	// starts, GOAL-FR-012). Only meaningful once AgentID is set, matching
	// the criteria/dod requirement's own conditionality above.
	if tc.tk.AgentID != "" {
		var goalMaxRoundsFn func() int
		if tc.t.deps.GetCfg != nil {
			goalMaxRoundsFn = func() int {
				cfg := tc.t.deps.GetCfg()
				if cfg == nil {
					return config.DefaultGoalMaxRounds
				}
				return cfg.Planning.EffectiveGoalMaxRounds()
			}
		}
		if gErr := syncWorkspaceTaskGoalRecord(tc.t.deps.Home, &tc.tk, true, tc.goalDoD, true, goalMaxRoundsFn); gErr != nil {
			slog.Error("create_task_in_workspace: failed to create paired goal record",
				"task_id", tc.tk.ID, "error", gErr)
			return tools.ErrorResult(errorJSON("SAVE_FAILED",
				fmt.Sprintf("task %q was created but its Definition of Done could not be persisted: %v",
					tc.tk.ID, gErr), "")), true
		}
	}
	return nil, false
}

// respond returns the created task's id, name, status, workspace and assignee.
func (tc *taskCreateToolExecute) respond() *tools.ToolResult {
	return tools.NewToolResult(successJSON(map[string]any{
		"id": tc.tk.ID, "name": tc.name, "status": string(tc.tk.Status),
		"workspace_id": tc.tk.WorkspaceID, "agent_id": tc.tk.AgentID,
	}))
}

// ---- update_task_in_workspace ----

type TaskUpdateTool struct{ deps *Deps }

func NewTaskUpdateTool(d *Deps) *TaskUpdateTool  { return &TaskUpdateTool{deps: d} }
func (t *TaskUpdateTool) Name() string           { return "update_task_in_workspace" }
func (t *TaskUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *TaskUpdateTool) Description() string {
	return "Update an existing task. Call this to change status, reassign, rename, or link to a workspace. Use list_tasks_in_workspace first to find the task id.\nParameters: id (required, from list_tasks_in_workspace), name, description, prompt, workspace_id, agent_id, status (inbox/next/blocked/done/failed — in_progress is reached only through real dispatch via run_task, never written directly here), due (RFC 3339), priority (1 highest to 5 lowest), blocked_by (array of task IDs, replaces existing list), write_set (array of concrete paths this plan member creates/edits, replaces existing list; pass [] to clear), stream (new parallel-group id for this plan member; pass \"\" to clear), is_join (set/clear whether this plan member is an authored join/assemble member), result (completion summary; used as the judge's claim text — required in practice for a done claim on a task with acceptance criteria, since only the criteria's own assignee running that task can force one through). Only provided fields are updated. A status:\"done\" call on a task with acceptance criteria is NOT applied immediately — it is adjudicated by the judge during that task's own run. You may only update a task you own (assignee or creator); updating another agent's task requires delegation trust to that agent within the workspace, or the call is refused. Refuses with PRINCIPAL_REQUIRED if the calling agent cannot be resolved. A call that fails validation changes nothing — the whole update, including a workspace_id move, is all-or-nothing."
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
			"criteria": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":     map[string]any{"type": "string", "enum": []string{"check", "prose", "behavior"}},
						"text":     map[string]any{"type": "string"},
						"check":    map[string]any{"type": "object"},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Replacement acceptance-criteria set (replaces the current set atomically). " +
					"Pass at least one item — GOAL-FR-021/D-C binds at edit too: an update supplying an " +
					"empty criteria array is rejected. Omit to leave criteria unchanged.",
			},
			"dod": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind":     map[string]any{"type": "string", "enum": []string{"check", "prose", "behavior"}},
						"text":     map[string]any{"type": "string"},
						"check":    map[string]any{"type": "object"},
						"behavior": task.BehaviorCriterionParamSchema(),
					},
					"required": []string{"text"},
				},
				"description": "Replacement Definition-of-Done set (replaces the current set atomically). " +
					"Pass at least one item — GOAL-FR-021/D-C binds at edit too: an update supplying an " +
					"empty dod array is rejected. Omit to leave dod unchanged.",
			},
		},
		"required": []string{"id"},
	}
}

// taskUpdateToolExecute carries the shared state of Execute across its stages.
type taskUpdateToolExecute struct {
	t                    *TaskUpdateTool
	ctx                  context.Context
	args                 map[string]any
	id                   string
	caller               string
	store                *task.Store
	existing             *task.Task
	err                  error
	patch                task.Patch
	updated              []string
	pendingWorkspaceID   string
	workspaceIDPending   bool
	effectiveWorkspaceID string
	goalCriteria         []task.AcceptanceCriterion
	goalDoD              []task.AcceptanceCriterion
	criteriaProvided     bool
	dodProvided          bool
	result               *task.Task
	goalSyncWarning      string
}

func (t *TaskUpdateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	tu := &taskUpdateToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := tu.validatePrincipal(); stop {
		return r0
	}

	if r0, stop := tu.loadAndAuthorize(); stop {
		return r0
	}

	if r0, stop := tu.buildPatch(); stop {
		return r0
	}
	if r0, stop := tu.resolveWorkspace(); stop {
		return r0
	}
	if r0, stop := tu.collectSimpleFields(); stop {
		return r0
	}

	if r0, stop := tu.parseCriteria(); stop {
		return r0
	}

	if r0, stop := tu.checkDistinct(); stop {
		return r0
	}

	if r0, stop := tu.applyPatch(); stop {
		return r0
	}

	tu.runPostUpdateHooks()

	return tu.respond()
}

// validatePrincipal checks id and resolves the calling agent, refusing an update with no principal.
func (tu *taskUpdateToolExecute) validatePrincipal() (*tools.ToolResult, bool) {
	tu.id, _ = tu.args["id"].(string)
	if tu.id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", "")), true
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
	tu.caller = strings.TrimSpace(tools.ToolAgentID(tu.ctx))
	if tu.caller == "" {
		return tools.ErrorResult(errorJSON("PRINCIPAL_REQUIRED",
			"cannot resolve the calling agent; refusing to update a task", "")), true
	}
	return nil, false
}

// loadAndAuthorize loads the task and runs the ownership and reassignment delegation gates.
func (tu *taskUpdateToolExecute) loadAndAuthorize() (*tools.ToolResult, bool) {
	tu.store = taskStoreFor(tu.t.deps.Home)
	tu.existing, tu.err = tu.store.Get(tu.id)
	if tu.err != nil {
		return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", tu.id),
			"Use list_tasks_in_workspace to see available tasks")), true
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
	if tu.existing.AgentID != "" && tu.existing.AgentID != tu.caller && !tu.existing.CreatedByAgent(tu.caller) {
		if denied := tu.t.deps.delegationDenied(tu.ctx, tu.caller, tu.existing.AgentID); denied != nil {
			return tools.DelegationDeniedResult(tu.t.Name(), denied), true
		}
	}

	// Reassignment is re-delegation: when agent_id changes to a DIFFERENT agent,
	// gate it through the SAME trust-set + mode("task") + depth policy as create.
	if v, ok := tu.args["agent_id"].(string); ok && v != "" && v != tu.existing.AgentID && v != tu.caller {
		if denied := tu.t.deps.delegationDenied(tu.ctx, tu.caller, v); denied != nil {
			return tools.DelegationDeniedResult(tu.t.Name(), denied), true
		}
	}
	return nil, false
}

// buildPatch translates name, description, prompt, status and agent_id args into a task.Patch, enforcing the status guards.
func (tu *taskUpdateToolExecute) buildPatch() (*tools.ToolResult, bool) {
	tu.patch = task.Patch{}
	tu.updated = []string{}
	if v, ok := tu.args["name"].(string); ok && v != "" {
		tu.patch.Title = &v
		tu.updated = append(tu.updated, "name")
	}
	if v, ok := tu.args["description"].(string); ok {
		tu.patch.Description = &v
		tu.updated = append(tu.updated, "description")
	}
	if v, ok := tu.args["prompt"].(string); ok {
		tu.patch.Prompt = &v
		tu.updated = append(tu.updated, "prompt")
	}
	if v, ok := tu.args["status"].(string); ok {
		if !isValidTaskStatus(v) {
			// UAT batch3 S58 (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
			// finding #2): an unrecognized status used to fall through this
			// gate silently — no error, no patch, status left unchanged, and
			// the caller had no way to tell their request was ignored. Mirror
			// TaskCreateTool.Execute's own rejection (same error shape, same
			// enumerated status list) rather than let a typo look like a
			// successful, silent no-op.
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				fmt.Sprintf("unknown status %q: expected one of inbox, next, blocked, done, failed", v), "status")), true
		}
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
		if st == task.StatusInProgress && tu.existing.Status != task.StatusInProgress {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"in_progress cannot be set directly — it is only ever reached through real dispatch; "+
					"call run_task to actually start this task", "status")), true
		}
		// Founder decision 2026-09-14 (one claim mechanism): while THIS
		// task's own executor run is in flight, no terminal status may be
		// written through this privileged tool either — completion is claimed
		// with goal_claim and decided by the judge. Out-of-band done on a
		// criteria task stays refused for the same reason it always was:
		// nothing would ever adjudicate it.
		if tools.ToolRunningTaskID(tu.ctx) == tu.id {
			return tools.ErrorResult(errorJSON("JUDGE_REQUIRED",
				"you cannot set this task's status while it is running — its completion is decided "+
					"by the judge. The worker claims with goal_claim (status \"met\" with evidence, "+
					"or \"blocked\" when it cannot proceed)", "status")), true
		}
		if st == task.StatusDone && !tu.existing.Scratchpad && len(tu.existing.Criteria) > 0 {
			return tools.ErrorResult(errorJSON("JUDGE_REQUIRED",
				"this task has acceptance criteria — completion is adjudicated by the judge "+
					"during a task run; it cannot be force-completed here", "status")), true
		}
		tu.patch.Status = &st
		tu.updated = append(tu.updated, "status")
	}
	if v, ok := tu.args["agent_id"].(string); ok && v != "" {
		// subagent_3p (external-CLI) worker reassignment is no longer guarded
		// here — same rationale as create_task_in_workspace above.
		tu.patch.AgentID = &v
		tu.updated = append(tu.updated, "agent_id")
	}
	return nil, false
}

// resolveWorkspace validates a requested workspace move and defers its write until the patch lands.
func (tu *taskUpdateToolExecute) resolveWorkspace() (*tools.ToolResult, bool) {
	// workspace_id is not in task.Patch (workspace is required-scoped and not
	// re-pointed via the generic patch), so it is applied via a separate,
	// direct read-modify-write rather than through store.Update. Validate the
	// target up front — as before — but do NOT write it yet: the actual write
	// is deferred until AFTER store.Update (below) succeeds, so a subsequently
	// rejected patch (an out-of-range priority, a DAG cycle, a depth
	// violation) can no longer leave the task moved to a different workspace
	// and persisted while the caller is told INVALID_INPUT and reasonably
	// assumes nothing changed. The whole call is now all-or-nothing.

	// effectiveWorkspaceID is what blocked_by's same-workspace check (below)
	// validates against: the workspace this task WILL be in once this call
	// completes, even though the move itself has not been written yet.
	tu.effectiveWorkspaceID = tu.existing.WorkspaceID
	if v, ok := tu.args["workspace_id"].(string); ok {
		if v != "" {
			if werr := validateID(v); werr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id")), true
			}
			if _, wsErr := readWorkspaceFromDisk(tu.t.deps.Home, v); wsErr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "invalid workspace_id: not found", "workspace_id")), true
			}
		}
		// UAT batch3 S58 (finding #2, second half): workspace_id used to be
		// unconditionally appended to `updated` (and unconditionally
		// re-written to disk) whenever the key was merely PRESENT in the
		// caller's args, regardless of whether the value actually differed
		// from the task's current workspace — including a resupplied,
		// unchanged value on every call. That made `updated_fields` an
		// unreliable diagnostic (it could list a field that never changed)
		// and did a pointless write-lock+persist for a no-op move. Only
		// treat this as a real change — and only then defer the actual
		// write and report it in `updated` — when v differs from the task's
		// current workspace.
		if v != tu.existing.WorkspaceID {
			tu.pendingWorkspaceID = v
			tu.workspaceIDPending = true
			tu.effectiveWorkspaceID = v
			tu.updated = append(tu.updated, "workspace_id")
		}
	}
	return nil, false
}

// collectSimpleFields collects due, priority, blocked_by, write_set, stream and is_join into the patch.
func (tu *taskUpdateToolExecute) collectSimpleFields() (*tools.ToolResult, bool) {
	if v, ok := tu.args["due"].(string); ok {
		tu.patch.Due = &v
		tu.updated = append(tu.updated, "due")
	}
	// Optional priority — parity with the plain update_task tool. Range
	// validation (1-5) is enforced by store.Update (pkg/task/store.go),
	// whose error surfaces via the generic INVALID_INPUT mapping below.
	if v, ok := tu.args["priority"].(float64); ok {
		pr := int(v)
		tu.patch.Priority = &pr
		tu.updated = append(tu.updated, "priority")
	}
	if rawDeps, ok := tu.args["blocked_by"].([]any); ok {
		deps := make([]string, 0, len(rawDeps))
		for _, d := range rawDeps {
			if s, ok := d.(string); ok && s != "" {
				deps = append(deps, s)
			}
		}
		// Same-workspace blocker guard (parity with validateBlockersWorkspace in
		// the plain tool). Validate against the task's EFFECTIVE workspace —
		// effectiveWorkspaceID reflects any requested-but-not-yet-written
		// workspace_id change from above, since the move itself is deferred
		// until after store.Update succeeds (see the workspace_id block
		// above). CLEAR ([]) trivially passes.
		if len(deps) > 0 {
			if wErr := validateBlockersSameWorkspace(tu.store, tu.effectiveWorkspaceID, deps); wErr != nil {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", wErr.Error(), "blocked_by")), true
			}
		}
		tu.patch.BlockedBy = &deps
		tu.updated = append(tu.updated, "blocked_by")
	}

	// write_set/stream/is_join (ADR-053 §Contract Surface, US-11 G-16) —
	// parity with the plain update_task tool.
	if rawWriteSet, ok := tu.args["write_set"].([]any); ok {
		writeSet := make([]string, 0, len(rawWriteSet))
		for _, p := range rawWriteSet {
			if s, ok := p.(string); ok && s != "" {
				writeSet = append(writeSet, s)
			}
		}
		tu.patch.WriteSet = &writeSet
		tu.updated = append(tu.updated, "write_set")
	}
	if v, ok := tu.args["stream"].(string); ok {
		tu.patch.Stream = &v
		tu.updated = append(tu.updated, "stream")
	}
	if v, ok := tu.args["is_join"].(bool); ok {
		tu.patch.IsJoin = &v
		tu.updated = append(tu.updated, "is_join")
	}
	return nil, false
}

// parseCriteria parses submitted criteria and dod, refusing an edit that empties either list.
func (tu *taskUpdateToolExecute) parseCriteria() (*tools.ToolResult, bool) {
	// criteria / dod (GOAL-FR-021/FR-029/FR-030/D-C): mirrors the plain
	// update_task tool — the mandatory-count gate binds at edit too,
	// uniformly with create. Persisted to the paired goal record after
	// store.Update succeeds (syncWorkspaceTaskGoalRecord, below).

	tu.criteriaProvided, tu.dodProvided = false, false
	if rawCriteria, ok := tu.args["criteria"].([]any); ok {
		tu.criteriaProvided = true
		if len(rawCriteria) == 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"An update that changes the acceptance criteria must leave at least one.", "criteria")), true
		}
		parsed, cErr := parseCriteriaArgsFromWorkspaceTool(rawCriteria, tu.caller)
		if cErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", cErr.Error(), "criteria")), true
		}
		tu.goalCriteria = parsed
		tu.patch.Criteria = &parsed
		tu.updated = append(tu.updated, "criteria")
	}
	if rawDoD, ok := tu.args["dod"].([]any); ok {
		tu.dodProvided = true
		if len(rawDoD) == 0 {
			return tools.ErrorResult(errorJSON("INVALID_INPUT",
				"An update that changes the Definition of Done must leave at least one item.", "dod")), true
		}
		parsed, dErr := parseCriteriaArgsFromWorkspaceTool(rawDoD, tu.caller)
		if dErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", dErr.Error(), "dod")), true
		}
		tu.goalDoD = parsed
		tu.updated = append(tu.updated, "dod")
	}
	return nil, false
}

// checkDistinct keeps criteria and dod distinct post-edit and refuses an assignee that cannot finish.
func (tu *taskUpdateToolExecute) checkDistinct() (*tools.ToolResult, bool) {
	// GOAL-FR-048 binds the distinctness rule at SAVE, not only at create —
	// otherwise the rule is a door you walk around: create with a distinct DoD,
	// then edit it into a duplicate.
	//
	// An edit may touch one list and not the other, so the comparison is
	// against the EFFECTIVE post-edit pair: the submitted list where one was
	// submitted, the persisted list otherwise. The persisted criteria come off
	// the task record; the persisted DoD can only come off the paired goal
	// record, which is the only place a task's DoD exists (ADR-086 D5).
	// Checked before store.Update, so a refusal writes nothing at all.
	if tu.criteriaProvided || tu.dodProvided {
		effectiveCriteria := tu.goalCriteria
		if !tu.criteriaProvided {
			effectiveCriteria = tu.existing.Criteria
		}
		effectiveDoD := tu.goalDoD
		if !tu.dodProvided {
			persistedDoD, dErr := pairedWorkspaceGoalDoD(tu.t.deps.Home, tu.id)
			if dErr != nil {
				return tools.ErrorResult(errorJSON("SAVE_FAILED",
					"could not read the task's Definition of Done to check it stays distinct from "+
						"the criteria: "+dErr.Error(), "criteria")), true
			}
			effectiveDoD = persistedDoD
		}
		if vErr := task.ValidateDoDDistinct(effectiveCriteria, effectiveDoD); vErr != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", vErr.Error(), "dod")).WithError(vErr), true
		}
	}

	// Founder decision 2026-09-15 (parity with the plain update_task tool): a
	// reassignment, or a change to what the task is judged against, may not
	// leave it with an agent that cannot finish it. Checked before
	// store.Update, so a refusal writes nothing.
	if refusal := workspaceUpdateAssigneeCannotFinish(tu.t.deps.AssigneeCannotFinish, tu.t.deps.Home, tu.existing,
		tu.patch.AgentID, tu.goalCriteria, tu.goalDoD, tu.criteriaProvided, tu.dodProvided); refusal != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", refusal.Reason, refusal.Field)).WithError(refusal), true
	}
	return nil, false
}

// applyPatch persists the patch via the store and then writes the deferred workspace move.
func (tu *taskUpdateToolExecute) applyPatch() (*tools.ToolResult, bool) {
	// Apply the field patch via the store (DAG validation + atomic write).
	tu.result, tu.err = tu.store.Update(tu.id, tu.patch)
	if tu.err != nil {
		if isTaskNotFound(tu.err) {
			return tools.ErrorResult(errorJSON("TASK_NOT_FOUND", fmt.Sprintf("No task %q", tu.id),
				"Use list_tasks_in_workspace to see available tasks")), true
		}
		return tools.ErrorResult(errorJSON("INVALID_INPUT", tu.err.Error(), "")), true
	}

	// Only now — AFTER store.Update has validated and persisted the rest of
	// the patch — write the workspace_id move validated earlier. This keeps
	// the call all-or-nothing: if store.Update had failed above, we already
	// returned without touching the task at all.
	if tu.workspaceIDPending {
		mu := tu.store.Lock(tu.id)
		mu.Lock()
		tu.result.WorkspaceID = tu.pendingWorkspaceID
		tu.result.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		writeErr := writeEntity(tasksDir(tu.t.deps.Home), tu.id, *tu.result)
		mu.Unlock()
		if writeErr != nil {
			return tools.ErrorResult(errorJSON("SAVE_FAILED", writeErr.Error(), "")), true
		}
	}
	return nil, false
}

// runPostUpdateHooks terminates the goal record on a terminal write, advances dependents and syncs criteria/dod to the goal record.
func (tu *taskUpdateToolExecute) runPostUpdateHooks() {
	// GOAL-FR-015/FR-027/FR-028 (review finding C1): this privileged tool is
	// one of the seven terminal task writers and had no goal hook at all. The
	// judge deferral above only intercepts `done` — status:"failed" was
	// written straight through, leaving the paired goal record ACTIVE forever
	// and silently killing Goal.Reactivate on every re-run of that task.
	//
	// Keyed on result.Status (what landed on disk) rather than the requested
	// status, so a deferred done-claim — which deliberately leaves patch.Status
	// unset — terminates nothing. The prior-status guard keeps a no-op resend
	// on an already-terminal task out of the hook; the hook is idempotent
	// anyway, this just keeps the logs honest.
	if task.IsTerminal(tu.result.Status) && !task.IsTerminal(tu.existing.Status) {
		tools.TerminateTaskGoalRecord(
			goal.NewStore(tu.t.deps.Home), tu.id, tu.result.Status, tu.result.CancelReason, tu.result.Result)
	}

	// FR-6.5: when the task newly reaches terminal "done", advance dependents.
	if tu.result.Status == task.StatusDone {
		if advanced, advErr := tu.store.AdvanceBlockedDependents(tu.id); advErr != nil {
			slog.Warn("update_task_in_workspace: advance dependents failed", "id", tu.id, "error", advErr)
		} else if len(advanced) > 0 {
			slog.Info("update_task_in_workspace: completed task advanced dependents",
				"completed_id", tu.id, "advanced_ids", advanced)
		}
	}

	// GOAL-FR-029/FR-030: the task record itself already carries the new
	// criteria (dual-write, via patch.Criteria above); this is what actually
	// persists the change onto the task's paired goal record.

	if tu.criteriaProvided || tu.dodProvided {
		var goalMaxRoundsFn func() int
		if tu.t.deps.GetCfg != nil {
			goalMaxRoundsFn = func() int {
				cfg := tu.t.deps.GetCfg()
				if cfg == nil {
					return config.DefaultGoalMaxRounds
				}
				return cfg.Planning.EffectiveGoalMaxRounds()
			}
		}
		if gErr := syncWorkspaceTaskGoalRecord(tu.t.deps.Home, tu.result, tu.criteriaProvided, tu.goalDoD, tu.dodProvided, goalMaxRoundsFn); gErr != nil {
			slog.Error("update_task_in_workspace: failed to sync paired goal record",
				"task_id", tu.id, "error", gErr)
			tu.goalSyncWarning = gErr.Error()
		}
	}
}

// respond builds the success payload with updated_fields and any goal sync warning.
func (tu *taskUpdateToolExecute) respond() *tools.ToolResult {
	respFields := map[string]any{"id": tu.id, "updated_fields": tu.updated}
	if tu.goalSyncWarning != "" {
		respFields["goal_sync_warning"] = "criteria/dod saved on the task, but the paired goal " +
			"record could not be updated: " + tu.goalSyncWarning
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
		"the task id.\nParameters: id (required), confirm (bool, must be true). Deleting a task also removes " +
		"it from every other task's blocked_by list, which can unblock dependents and make them runnable — " +
		"the response lists any tasks unblocked this way in unblocked_tasks. If those edges cannot be fully " +
		"cleaned up the deletion still stands and the response carries a cascade_warning."
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

	// GOAL-FR-044/EC-4: a goal MUST NOT outlive its owner as an unreferenced
	// record. Runs BEFORE the task file is removed and a failure refuses the
	// whole delete — see tools.RemoveTaskGoalRecords' doc for why this ordering
	// replaced the best-effort-afterwards shape all three delete surfaces used
	// to share. Nothing has been deleted yet here, so refusing leaves the task
	// and its goal exactly as they were and a retry is safe.
	if gErr := tools.RemoveTaskGoalRecords(goal.NewStore(t.deps.Home), id); gErr != nil {
		slog.Error("delete_task_in_workspace: refusing to delete a task whose paired goal record "+
			"could not be removed (GOAL-FR-044) — nothing was deleted", "task_id", id, "error", gErr)
		return tools.ErrorResult(errorJSON("GOAL_CLEANUP_FAILED",
			"the task was NOT deleted: its paired goal record could not be removed, and deleting "+
				"the task anyway would leave that record behind as an unreferenced orphan: "+gErr.Error(),
			"Retry the delete once the goal store is writable again"))
	}

	unblocked, err := store.Delete(id)
	cascadeFailed := false
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
		// non-fatal side effect. But the caller must be able to SEE the
		// partial failure, not just have it buried in a server log —
		// surfaced below via cascade_warning (mirrors the publish_warning
		// pattern used by the three agent tools for the same shape).
		cascadeFailed = true
		slog.Warn("delete_task_in_workspace: cascade edge cleanup partially failed", "deleted_id", id, "error", err)
	}
	if len(unblocked) > 0 {
		slog.Info("sysagent: task delete: unblocked dependents", "deleted_id", id, "unblocked", unblocked)
	}
	result := map[string]any{"id": id, "deleted": true}
	if len(unblocked) > 0 {
		result["unblocked_tasks"] = unblocked
	}
	if cascadeFailed {
		result["cascade_warning"] = "the task was deleted but some other tasks' blocked_by edges could not " +
			"be cleaned up and now reference a deleted task"
	}
	return tools.NewToolResult(successJSON(result))
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
// private todo card), DelegationDepth — and a whole-struct
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
