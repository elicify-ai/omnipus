// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// read_tools_wire.go — wires get_task and get_goal (tool-parity spec r17
// TK-31, GL-04) with the running system's read-time values, so get_task's
// result equals what GET /tasks/{id} renders for the same task (scenario 4.1).
// get_plan registers with the plan tools (wirePlanToolsForAgent).

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/taskwire"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// registerTaskReadTools registers get_task and get_goal on agent.
func (al *AgentLoop) registerTaskReadTools(agent *AgentInstance) {
	taskGet := tools.NewTaskGetTool(al.taskStore)
	taskGet.SetExtras(al.taskReadExtras)
	agent.Tools.RegisterReplacing(taskGet)

	goalGet := tools.NewGoalGetTool(al.taskStore)
	goalGet.SetSessionGoal(activeGoalForSession)
	agent.Tools.RegisterReplacing(goalGet)
}

// taskReadExtras resolves the read-time task values from the same sources the
// REST surface uses: the registry (agent name), the assignee readiness check,
// the live planning config (attempt limit), the executor's live progress and
// the session transcript (last activity), and the store (rollup).
func (al *AgentLoop) taskReadExtras(t task.Task, g *goal.Goal) taskwire.TaskExtras {
	x := taskwire.TaskExtras{
		EffectiveMaxAttempts: tools.EffectiveTaskMaxAttempts(planningConfigOf(al), &t),
	}
	if t.AgentID != "" {
		if reg := al.GetRegistry(); reg != nil {
			if inst, ok := reg.GetAgent(t.AgentID); ok {
				x.AgentName = inst.Name
			}
		}
		if !t.Scratchpad && !task.IsTerminal(t.Status) {
			x.AssigneeWarning = al.TaskAssigneeCannotFinish(t.AgentID, judgedCriteria(t, g))
		}
	}
	if at, ok := al.taskLastActivity(t); ok {
		x.LastActivityAt = &at
	}
	// The rollup is a read-time decoration, as on the REST surface: when the
	// child list cannot be read it is left out and the cause is logged.
	children, err := al.taskStore.List(task.Filter{ParentTaskID: t.ID, ParentTaskIDSet: true})
	if err != nil {
		logger.WarnCF("agent", "get_task: child runs could not be listed; rollup omitted",
			map[string]any{"task_id": t.ID, "error": err.Error()})
	} else {
		x.Rollup = taskwire.Rollup(children)
	}
	return x
}

// judgedCriteria is what the assignee readiness check judges: the goal
// record's criteria (else the task's own) plus its Definition of Done.
func judgedCriteria(t task.Task, g *goal.Goal) []task.AcceptanceCriterion {
	var judged []task.AcceptanceCriterion
	if g != nil && len(g.Criteria) > 0 {
		judged = append(judged, g.Criteria...)
	} else {
		judged = append(judged, t.Criteria...)
	}
	if g != nil {
		judged = append(judged, g.DoD...)
	}
	return judged
}

// taskLastActivity gathers the two activity stamps of an in-progress task
// and lets taskwire choose, as the REST surface does.
func (al *AgentLoop) taskLastActivity(t task.Task) (time.Time, bool) {
	if t.Status != task.StatusInProgress {
		return time.Time{}, false
	}
	var live, transcript time.Time
	if al.taskExecutor != nil {
		if at, ok := al.taskExecutor.LiveTaskLastActivity(t.ID); ok {
			live = at
		}
	}
	if t.SessionID != "" {
		if store := al.ResolveSessionStore(t.SessionID); store != nil {
			if meta, err := store.GetMeta(t.SessionID); err == nil && meta != nil && !meta.UpdatedAt.IsZero() {
				transcript = meta.UpdatedAt
			}
		}
	}
	return taskwire.LastActivityAt(t.Status, live, transcript, time.Now())
}
