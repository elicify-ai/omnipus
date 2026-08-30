// Omnipus — run_task Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TaskStartNowFunc launches a standalone task's normal attempt-loop
// immediately, dispatching via the existing task-executor path (ADR-052
// FR-019, spec US-10). It mirrors *pkg/agent.TaskExecutor's StartTaskNow
// method signature exactly (returns the run session id) and is injected via
// SetStartTaskNow rather than called directly, because pkg/tools must not
// import pkg/agent (pkg/agent already imports pkg/tools — a direct import
// the other way would be a cycle). The wiring layer (pkg/agent/loop.go,
// another wave's job) supplies this as taskExecutor.StartTaskNow.
type TaskStartNowFunc func(ctx context.Context, taskID string) (sessionID string, err error)

// TaskRunTool implements the run_task agent tool (ADR-052 FR-019, spec
// US-10, G4/A3). It drives a STANDALONE task's full attempt loop (run ->
// judge -> retry with steering up to the attempt limit) — exactly like a
// plan member's own execution, NOT a single shot; the actual loop mechanics
// live in the injected TaskStartNowFunc (pkg/agent's TaskExecutor), which
// this tool only dispatches into after its own gates pass.
//
// An in-plan task is rejected AT THE TOOL BOUNDARY (G4): a plan's members
// start in DAG dependency order under the plan's own dispatch loop, so a
// manual run_task on one would bypass that ordering. This is enforced here,
// not inside the shared executor (which task_executor.go's ExecuteTask does
// not itself distinguish plan membership for — see that file's doc notes),
// so run_task is the single, tool-level choke point for the standalone-only
// invariant.
type TaskRunTool struct {
	BaseTool
	store *task.Store
	// startTaskNow, when non-nil, dispatches the task via the real executor
	// (see TaskStartNowFunc). FAIL CLOSED when unwired: an unwired dispatcher
	// is a configuration error, never a silent success — mirrors every other
	// unwired-checker discipline in this package.
	startTaskNow TaskStartNowFunc
}

// NewTaskRunTool constructs a TaskRunTool. store may be nil for
// metadata-only construction (GeneralBuiltinMetadata) — never Execute()d in
// that mode.
func NewTaskRunTool(store *task.Store) *TaskRunTool {
	return &TaskRunTool{store: store}
}

// SetStartTaskNow installs the dispatch hook (see TaskStartNowFunc doc).
func (t *TaskRunTool) SetStartTaskNow(fn TaskStartNowFunc) {
	t.startTaskNow = fn
}

func (t *TaskRunTool) Name() string           { return "run_task" }
func (t *TaskRunTool) Scope() ToolScope       { return ScopeCore }
func (t *TaskRunTool) Category() ToolCategory { return CategoryTasks }

func (t *TaskRunTool) Description() string {
	return "Start a STANDALONE task now: marks it in_progress and launches its attempt loop (run, " +
		"judge, retry with steering up to the attempt limit). Returns immediately with the run's " +
		"session_id — it does not wait for the task to finish; poll the task or inspect the session " +
		"for the outcome. The task must already have an assigned agent (assign one first). A done " +
		"task cannot be re-run; a failed one can. A blocked task is rejected — it becomes runnable on " +
		"its own once its blockers complete. Calling this on an already-running task is a safe no-op " +
		"returning the existing session. May fail if the global dispatch cap is exhausted — retry " +
		"shortly. A task belonging to a plan is rejected: call execute_plan on the plan, which drives " +
		"its members in dependency order."
}

func (t *TaskRunTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the standalone (non-plan-member) task to run",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskRunTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("run_task failed: task store is not available")
	}
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return ErrorResult("task_id is required")
	}

	existing, err := t.store.Get(taskID)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return ErrorResult(fmt.Sprintf("task %q not found", taskID))
		}
		return ErrorResult(fmt.Sprintf("run_task failed: could not load task: %v", err))
	}

	// G4/A3: in-plan tasks are rejected AT THE TOOL BOUNDARY — the plan
	// drives its own members' start order; run_task never dispatches one.
	if existing.PlanID != "" {
		return ErrorResult(fmt.Sprintf(
			"task %q is a member of plan %q; in-plan tasks start via the plan (call execute_plan), not run_task",
			taskID, existing.PlanID))
	}

	// `done` is the one genuinely frozen task status; `failed` is
	// deliberately re-runnable (mirrors the board's "Play" affordance on a
	// failed standalone task, and the store's own permissive transition
	// policy — task.Task's `failed` is NOT terminal, unlike a Plan's).
	if existing.Status == task.StatusDone {
		return ErrorResult(fmt.Sprintf("task %q is already done; a completed task cannot be re-run via run_task", taskID))
	}
	// `blocked` is a derived side-state (an unmet blocked_by dependency) —
	// the store itself rejects a direct blocked->in_progress transition
	// (validateTransition, pkg/task/store.go), but reject it here first with
	// a clearer, run_task-specific message.
	if existing.Status == task.StatusBlocked {
		return ErrorResult(fmt.Sprintf(
			"task %q is blocked on an unmet dependency; it becomes runnable automatically once its "+
				"blockers complete", taskID))
	}
	if existing.AgentID == "" {
		return ErrorResult(fmt.Sprintf("task %q has no assigned agent; assign one before running it", taskID))
	}

	if t.startTaskNow == nil {
		slog.Error("run_task: no StartTaskNow dispatcher installed — denying by default", "task_id", taskID)
		return ErrorResult("run_task failed: task executor not wired — denying by default")
	}

	// Already in flight: startTaskNow's own idempotency guard (a live
	// session_id) returns the existing session rather than double-launching,
	// so this is a safe no-op re-dispatch — but skip the status flip below
	// (it is already in_progress) to avoid re-stamping StartedAt.
	if existing.Status == task.StatusInProgress {
		sessionID, startErr := t.startTaskNow(ctx, taskID)
		if startErr != nil {
			return ErrorResult(fmt.Sprintf("run_task failed: could not confirm running task: %v", startErr))
		}
		return NewToolResult(fmt.Sprintf(
			`{"task_id":%q,"status":%q,"session_id":%q,"note":"already running"}`,
			taskID, task.StatusInProgress, sessionID))
	}

	// Mirror the REST PATCH-to-in_progress path (rest_tasks.go): transition
	// to in_progress FIRST (so the board reflects the dispatch immediately),
	// then dispatch; on dispatch failure, revert so the task is never
	// stranded in in_progress with no agent actually running.
	priorStatus := existing.Status
	inProgress := task.StatusInProgress
	if _, uerr := t.store.Update(taskID, task.Patch{Status: &inProgress}); uerr != nil {
		return ErrorResult(fmt.Sprintf("run_task failed: could not mark task in_progress: %v", uerr))
	}

	sessionID, startErr := t.startTaskNow(ctx, taskID)
	if startErr != nil {
		revert := priorStatus
		emptyStarted := ""
		if _, rErr := t.store.Update(taskID, task.Patch{Status: &revert, StartedAt: &emptyStarted}); rErr != nil {
			slog.Error("run_task: could not revert task status after dispatch failure",
				"task_id", taskID, "revert_to", revert, "error", rErr)
		}
		return ErrorResult(fmt.Sprintf("run_task failed: could not dispatch task: %v", startErr))
	}

	return NewToolResult(fmt.Sprintf(`{"task_id":%q,"status":%q,"session_id":%q}`,
		taskID, task.StatusInProgress, sessionID))
}
