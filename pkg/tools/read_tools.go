// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// read_tools.go — get_task, get_plan and get_goal (tool-parity spec r17
// TK-31, PL-12, GL-04, §7.8 read visibility, §7.9, §7.13 field selection).
// Each converts the stored record with the shared pkg/taskwire conversion the
// REST surface uses, then returns the identity plus the requested fields.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/taskwire"
)

// TaskFieldSpec is get_task's selectable object (§7.13 get_task row).
var TaskFieldSpec = FieldSpec{Object: "task", Identity: []string{"id"}, Valid: []string{
	"title", "description", "prompt", "action", "status", "agent_id",
	"cancel_reason", "agent_name", "run_isolated", "priority", "blocked_by",
	"todos", "parent_task_id", "workspace_id", "tags", "plan_id", "write_set",
	"stream", "is_join", "judge_rounds", "goal_max_rounds", "criteria", "dod",
	"attempt_count", "max_attempts", "effective_max_attempts", "trigger",
	"due", "surface", "source_channel", "source_chat_id", "session_id",
	"result", "artifacts", "owner", "created_by", "created_at", "updated_at",
	"started_at", "completed_at", "last_activity_at", "assignee_warning",
	"rollup",
}}

// PlanFieldSpec is get_plan's selectable object (§7.13 get_plan row).
var PlanFieldSpec = FieldSpec{Object: "plan", Identity: []string{"id"}, Valid: []string{
	"workspace_id", "title", "objective", "description", "state",
	"plan_phase", "last_unmet_terminal_signature", "owner_session_id",
	"failed_reason", "supervision", "source_channel", "source_chat_id",
	"owner_agent_id", "dod", "rationale", "bounds", "judge_rounds",
	"active_loop", "paused_reason", "last_activity_at", "progress", "owner",
	"created_by", "created_at", "updated_at", "approved_at", "started_at",
	"completed_at",
}}

// GoalFieldSpec is get_goal's selectable object (§7.13 get_goal row).
var GoalFieldSpec = FieldSpec{Object: "goal", Identity: []string{"goal_id"}, Valid: []string{
	"owner_kind", "owner_id", "source", "prompt", "definition", "criteria",
	"dod", "max_rounds", "round", "attempts_used", "state", "created_at",
	"started_at", "last_activity_at", "active_session_id", "latest_reason",
	"terminal_reason", "latest_claim", "latest_verdict",
	"superseded_criteria", "terminal_history", "zero_output_pushes",
	"question_rounds_used", "route_channel", "route_chat_id",
}}

// noSessionGoalMessage is get_goal()'s plain no-goal result text (§7.8).
const noSessionGoalMessage = "No goal is set in this chat."

// TaskExtrasFn resolves a task's read-time wire values (agent name, assignee
// warning, live activity, rollup, effective attempt limit) from the running
// system. Nil leaves them at their zero value.
type TaskExtrasFn func(t task.Task, g *goal.Goal) taskwire.TaskExtras

// SessionGoalFn returns the active goal bound to a session, (nil, nil) when
// there is none.
type SessionGoalFn func(sessionID string) (*goal.Goal, error)

// callerScope reads the calling agent and the turn's workspace. An empty
// agent is refused (fail closed, as list_tasks refuses it).
func callerScope(ctx context.Context, toolName string) (agentID, workspaceID string, refusal *ToolResult) {
	agentID = strings.TrimSpace(ToolAgentID(ctx))
	if agentID == "" {
		return "", "", ErrorResult(toolName + ": cannot resolve the calling agent; refusing to read")
	}
	return agentID, strings.TrimSpace(ToolWorkspaceID(ctx)), nil
}

// taskReadable is the get_task / get_goal(task_id) visibility rule (§7.8):
// the caller is the assignee or the agent creator, and the task is in the
// turn's workspace (any workspace when the turn has none).
func taskReadable(t *task.Task, agentID, workspaceID string) bool {
	if t.AgentID != agentID && !t.CreatedByAgent(agentID) {
		return false
	}
	return workspaceID == "" || t.WorkspaceID == workspaceID
}

// readableTask loads a task the caller may read. A missing task and a hidden
// one give the same text; a storage failure is reported as such.
func readableTask(store *task.Store, toolName, taskID, agentID, workspaceID string) (*task.Task, *ToolResult) {
	notFound := ErrorResult(fmt.Sprintf("task %q not found", taskID))
	t, err := store.Get(taskID)
	if errors.Is(err, task.ErrNotFound) {
		return nil, notFound
	}
	if err != nil {
		return nil, ErrorResult(fmt.Sprintf("%s failed: could not read task %q: %v", toolName, taskID, err))
	}
	if !taskReadable(t, agentID, workspaceID) {
		return nil, notFound
	}
	return t, nil
}

// pairedGoal returns the task's goal record, nil when it has none. A read
// failure is an error, never "no goal" (SF-6).
func pairedGoal(store *task.Store, taskID string) (*goal.Goal, error) {
	g, err := GoalStoreForTasks(store).GetByOwner(gen.GoalOwnerKindTask, taskID)
	if errors.Is(err, goal.ErrOwnerNotFound) {
		return nil, nil //nolint:nilnil // A task without a goal record has no goal, not a read failure.
	}
	return g, err
}

// projectedResult projects full and renders it as the JSON tool result.
func projectedResult(full any, spec FieldSpec, args map[string]any) *ToolResult {
	selected, refusal := ValidateFieldsArg(args, spec)
	if refusal != nil {
		return refusal
	}
	out, refusal := Project(full, spec, selected)
	if refusal != nil {
		return refusal
	}
	return jsonToolResult(out, spec.Object)
}

func jsonToolResult(v any, object string) *ToolResult {
	raw, err := json.Marshal(v)
	if err != nil {
		return ErrorResult(fmt.Sprintf("read %s failed: could not encode the result: %v", object, err))
	}
	return NewToolResult(string(raw))
}

// --- get_task ----------------------------------------------------------------

// TaskGetTool reads one task the caller is assigned or created.
type TaskGetTool struct {
	BaseTool
	store  *task.Store
	extras TaskExtrasFn
}

// NewTaskGetTool returns get_task over store.
func NewTaskGetTool(store *task.Store) *TaskGetTool { return &TaskGetTool{store: store} }

// SetExtras injects the read-time wire value resolver.
func (t *TaskGetTool) SetExtras(fn TaskExtrasFn) { t.extras = fn }

func (t *TaskGetTool) Name() string           { return "get_task" }
func (t *TaskGetTool) Scope() ToolScope       { return ScopeGeneral }
func (t *TaskGetTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskGetTool) Description() string {
	return "Read the full details of a task you are assigned or created: its prompt, status, " +
		"criteria, Definition of Done, schedule (trigger), result and more. Pass `fields` with the " +
		"properties you need, or [\"all\"]. Use list_tasks to find the task id."
}

func (t *TaskGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "The task to read."},
			"fields":  FieldsParameter(TaskFieldSpec.Valid),
		},
		"required": []string{"task_id", "fields"},
	}
}

// FieldSpecFor implements FieldSelecting.
func (t *TaskGetTool) FieldSpecFor(map[string]any) FieldSpec { return TaskFieldSpec }

func (t *TaskGetTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("get_task failed: task store is not available")
	}
	agentID, workspaceID, refusal := callerScope(ctx, "get_task")
	if refusal != nil {
		return refusal
	}
	taskID, _ := args["task_id"].(string)
	tk, refusal := readableTask(t.store, "get_task", strings.TrimSpace(taskID), agentID, workspaceID)
	if refusal != nil {
		return refusal
	}
	g, err := pairedGoal(t.store, tk.ID)
	if err != nil {
		return ErrorResult(fmt.Sprintf(
			"task %q could not be read completely: its Definition of Done is unavailable (%v)", tk.ID, err))
	}
	var x taskwire.TaskExtras
	if t.extras != nil {
		x = t.extras(*tk, g)
	}
	return projectedResult(taskwire.Task(*tk, g, x), TaskFieldSpec, args)
}

// --- get_plan ----------------------------------------------------------------

// PlanGetTool reads one plan the caller owns, created, or has a task in.
type PlanGetTool struct {
	BaseTool
	plans *plan.Store
	tasks *task.Store
}

// NewPlanGetTool returns get_plan over the plan store; tasks serves the
// member-assignee visibility rule and the plan's progress.
func NewPlanGetTool(plans *plan.Store, tasks *task.Store) *PlanGetTool {
	return &PlanGetTool{plans: plans, tasks: tasks}
}

func (t *PlanGetTool) Name() string           { return "get_plan" }
func (t *PlanGetTool) Scope() ToolScope       { return ScopeGeneral }
func (t *PlanGetTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanGetTool) Description() string {
	return "Read the details of a plan you own, created, or have a task in: its objective, state, " +
		"Definition of Done, bounds, progress and more. Pass `fields` with the properties you need, " +
		"or [\"all\"]."
}

func (t *PlanGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "The plan to read."},
			"fields":  FieldsParameter(PlanFieldSpec.Valid),
		},
		"required": []string{"plan_id", "fields"},
	}
}

// FieldSpecFor implements FieldSelecting.
func (t *PlanGetTool) FieldSpecFor(map[string]any) FieldSpec { return PlanFieldSpec }

func (t *PlanGetTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.plans == nil || t.tasks == nil {
		return ErrorResult("get_plan failed: plan or task store is not available")
	}
	agentID, workspaceID, refusal := callerScope(ctx, "get_plan")
	if refusal != nil {
		return refusal
	}
	planID, _ := args["plan_id"].(string)
	planID = strings.TrimSpace(planID)
	notFound := ErrorResult(fmt.Sprintf("plan %q not found", planID))
	p, err := t.plans.Get(planID)
	if errors.Is(err, plan.ErrNotFound) {
		return notFound
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("get_plan failed: could not read plan %q: %v", planID, err))
	}
	if workspaceID != "" && p.WorkspaceID != workspaceID {
		return notFound
	}
	if !planEditableBy(p, agentID) {
		members, err := t.tasks.List(task.Filter{PlanID: p.ID, AgentID: agentID})
		if err != nil {
			return ErrorResult(fmt.Sprintf("get_plan failed: could not check plan %q membership: %v", planID, err))
		}
		if len(members) == 0 {
			return notFound
		}
	}
	return projectedResult(taskwire.Plan(*p, t.tasks, nil), PlanFieldSpec, args)
}

// --- get_goal ----------------------------------------------------------------

// GoalGetTool reads a task's goal, or the calling session's own goal.
type GoalGetTool struct {
	BaseTool
	tasks       *task.Store
	sessionGoal SessionGoalFn
}

// NewGoalGetTool returns get_goal over the task store (whose goal store holds
// the task goals).
func NewGoalGetTool(tasks *task.Store) *GoalGetTool { return &GoalGetTool{tasks: tasks} }

// SetSessionGoal injects the session-bound goal lookup used by get_goal().
func (t *GoalGetTool) SetSessionGoal(fn SessionGoalFn) { t.sessionGoal = fn }

func (t *GoalGetTool) Name() string           { return "get_goal" }
func (t *GoalGetTool) Scope() ToolScope       { return ScopeGeneral }
func (t *GoalGetTool) Category() ToolCategory { return CategoryTasks }

func (t *GoalGetTool) Description() string {
	return "Read a goal: with task_id, the goal of a task you are assigned or created; without it, " +
		"this chat's own goal. Returns its definition, criteria, rounds, state and more. Pass " +
		"`fields` with the properties you need, or [\"all\"]."
}

func (t *GoalGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "Optional: the task whose goal to read. Omit for this chat's goal."},
			"fields":  FieldsParameter(GoalFieldSpec.Valid),
		},
		"required": []string{"fields"},
	}
}

// FieldSpecFor implements FieldSelecting.
func (t *GoalGetTool) FieldSpecFor(map[string]any) FieldSpec { return GoalFieldSpec }

func (t *GoalGetTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.tasks == nil {
		return ErrorResult("get_goal failed: task store is not available")
	}
	agentID, workspaceID, refusal := callerScope(ctx, "get_goal")
	if refusal != nil {
		return refusal
	}
	if _, refusal = ValidateFieldsArg(args, GoalFieldSpec); refusal != nil {
		return refusal
	}
	taskID, _ := args["task_id"].(string)
	if taskID = strings.TrimSpace(taskID); taskID == "" {
		return t.sessionOwnGoal(ctx, args)
	}
	tk, refusal := readableTask(t.tasks, "get_goal", taskID, agentID, workspaceID)
	if refusal != nil {
		return refusal
	}
	g, err := pairedGoal(t.tasks, tk.ID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("get_goal failed: could not read the goal of task %q: %v", tk.ID, err))
	}
	if g == nil {
		return jsonToolResult(map[string]any{"goal": nil, "message": fmt.Sprintf("Task %q has no goal.", tk.ID)}, "goal")
	}
	return t.goalResult(g, args)
}

// sessionOwnGoal answers get_goal() with the calling session's goal, or the
// plain no-goal result (not an error).
func (t *GoalGetTool) sessionOwnGoal(ctx context.Context, args map[string]any) *ToolResult {
	if t.sessionGoal == nil {
		return ErrorResult("get_goal failed: the session goal lookup is not available")
	}
	g, err := t.sessionGoal(ToolTranscriptSessionID(ctx))
	if err != nil {
		return ErrorResult(fmt.Sprintf("get_goal failed: could not read this chat's goal: %v", err))
	}
	if g == nil {
		return jsonToolResult(map[string]any{"goal": nil, "message": noSessionGoalMessage}, "goal")
	}
	return t.goalResult(g, args)
}

func (t *GoalGetTool) goalResult(g *goal.Goal, args map[string]any) *ToolResult {
	wire, err := taskwire.Goal(g)
	if err != nil {
		return ErrorResult(fmt.Sprintf("get_goal failed: %v", err))
	}
	return projectedResult(wire, GoalFieldSpec, args)
}
