// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_assignee_readiness.go is the create_task / update_task half of the
// founder decision of 2026-09-15 ("pre-run check"): an agent tool may not
// assign a task to an agent that cannot finish it. The answer itself lives in
// pkg/agent (AgentLoop.TaskAssigneeCannotFinish — pkg/tools cannot import
// pkg/agent) and is installed through SetAssigneeReadinessChecker; this file
// decides only WHEN to ask and how to refuse.
//
// Refused here, warned on REST: ADR-049 D2 rule 5 rejects an unfinishable
// assignment on agent tool paths — an agent cannot change another agent's
// permissions — while the human task form accepts it and shows the same text
// as Task.assignee_warning, because an operator may assign first and fix the
// permission afterwards.
package tools

import (
	"log/slog"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// AssigneeReadinessChecker returns why assigneeAgentID cannot finish a task
// judged against judged (its acceptance criteria and Definition of Done
// together), naming the fix, or "" when nothing knowable stops it.
type AssigneeReadinessChecker func(assigneeAgentID string, judged []task.AcceptanceCriterion) string

// AssigneeCannotFinishField is the task field an assignee-cannot-finish
// refusal is about — the same field REST's Task.assignee_warning names, so a
// form shows it next to the agent picker.
const AssigneeCannotFinishField = "agent_id"

// AssigneeCannotFinishError is the structured error carried on the refusal's
// ToolResult.Err. Reason is exactly the text the model reads in ForLLM.
type AssigneeCannotFinishError struct {
	AgentID string
	Field   string
	Reason  string
}

func (e *AssigneeCannotFinishError) Error() string { return e.Reason }

// assigneeReadinessUnwiredOnce keeps the unwired-checker warning to one line
// per process: every create/update would otherwise repeat it.
var assigneeReadinessUnwiredOnce sync.Once

// SetAssigneeReadinessChecker installs the readiness answer for create_task.
// Production wiring: pkg/agent/loop.go's registerSharedTools.
func (t *TaskCreateTool) SetAssigneeReadinessChecker(fn AssigneeReadinessChecker) {
	t.assigneeCannotFinish = fn
}

// SetAssigneeReadinessChecker installs the readiness answer for update_task.
// Production wiring: pkg/agent/loop.go's registerSharedTools.
func (t *TaskUpdateTool) SetAssigneeReadinessChecker(fn AssigneeReadinessChecker) {
	t.assigneeCannotFinish = fn
}

// AssigneeCannotFinishRefusal asks checker whether agentID can finish a task
// judged against judged and returns the refusal to hand back, or nil when the
// write may go ahead. Exported so the cross-workspace task tools
// (pkg/sysagent/tools) share the one decision and the one error shape.
//
// An unwired checker does not refuse: the task run's own pre-run check
// (pkg/agent task_run_loop.go::executeTaskRun) still ends such a task at once,
// before any model call, so nothing unsafe proceeds — this is the early
// feedback, not the gate. The gap is logged once at Warn so a wiring fault is
// visible rather than silent.
func AssigneeCannotFinishRefusal(
	toolName string, checker AssigneeReadinessChecker, agentID string, judged []task.AcceptanceCriterion,
) *AssigneeCannotFinishError {
	if agentID == "" {
		return nil
	}
	if checker == nil {
		assigneeReadinessUnwiredOnce.Do(func() {
			slog.Warn("task tools: no assignee-readiness checker installed — an assignment to an agent that "+
				"cannot finish the task is not refused at write time; the task run's pre-run check still ends it",
				"tool", toolName)
		})
		return nil
	}
	reason := checker(agentID, judged)
	if reason == "" {
		return nil
	}
	return &AssigneeCannotFinishError{AgentID: agentID, Field: AssigneeCannotFinishField, Reason: reason}
}

// assigneeCannotFinishResult is AssigneeCannotFinishRefusal rendered as a
// create_task / update_task error result.
func assigneeCannotFinishResult(
	toolName string, checker AssigneeReadinessChecker, agentID string, judged []task.AcceptanceCriterion,
) *ToolResult {
	refusal := AssigneeCannotFinishRefusal(toolName, checker, agentID, judged)
	if refusal == nil {
		return nil
	}
	return ErrorResult(refusal.Reason).WithError(refusal)
}

// updateAssigneeCannotFinish is update_task's readiness question. It is asked
// only when the update changes who does the task or what it is judged against
// — an edit to the title of a task whose agent later lost a permission is not
// refused for it; that task's next run is. The effective post-update pair is
// checked: the new agent where one is assigned, the submitted criteria/DoD
// where submitted, the stored ones otherwise. A stored Definition of Done
// that cannot be read is left out with a warning (acting only on what is
// known), mirroring the task run's pre-run check.
func (t *TaskUpdateTool) updateAssigneeCannotFinish(
	existing *task.Task, newAgentID *string,
	newCriteria, newDoD []task.AcceptanceCriterion, criteriaProvided, dodProvided bool,
) *ToolResult {
	agentChanged := newAgentID != nil && *newAgentID != existing.AgentID
	if !agentChanged && !criteriaProvided && !dodProvided {
		return nil
	}
	agentID := existing.AgentID
	if agentChanged {
		agentID = *newAgentID
	}
	if agentID == "" {
		return nil
	}
	criteria := newCriteria
	if !criteriaProvided {
		criteria = existing.Criteria
	}
	judged := append([]task.AcceptanceCriterion{}, criteria...)
	if dodProvided {
		judged = append(judged, newDoD...)
	} else if persisted, err := pairedGoalDoD(t.store, existing.ID); err != nil {
		slog.Warn("update_task: the task's Definition of Done could not be read — checking the assignee "+
			"against its acceptance criteria only", "task_id", existing.ID, "error", err)
	} else {
		judged = append(judged, persisted...)
	}
	return assigneeCannotFinishResult("update_task", t.assigneeCannotFinish, agentID, judged)
}
