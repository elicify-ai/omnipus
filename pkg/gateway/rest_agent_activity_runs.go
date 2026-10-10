// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_agent_activity_runs.go: GET /api/v1/agents/{id}/activity-runs, the
// server source for the Activity panel's task and scheduler run rows
// (session-core FR-033). It lists the OPEN TaskRun records where the agent is
// the task's assignee or one of the run's captured recipients (FR-019), each
// with the run's own session for the row's Open control. It reads only what U6
// already stores — the TaskRun record and the run session's lifecycle record —
// and carries no provider-token figure (descoped: unknown is never zero).

package gateway

import (
	"log/slog"
	"net/http"
	"sort"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// listAgentActivityRuns handles GET /api/v1/agents/{id}/activity-runs.
func (a *restAPI) listAgentActivityRuns(w http.ResponseWriter, r *http.Request, agentID string) {
	if a.taskStore == nil {
		jsonOK(w, []gen.AgentActivityRun{})
		return
	}
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID != "" {
		if err := validateEntityID(workspaceID); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
	}
	tasks, err := a.taskStore.List(task.Filter{Status: task.StatusInProgress, WorkspaceID: workspaceID})
	if err != nil {
		slog.Error("rest: activity runs: list tasks failed", "agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list tasks")
		return
	}
	lifecycle := a.agentLoop.GetSessionLifecycleStore()
	rows := make([]gen.AgentActivityRun, 0)
	for i := range tasks {
		t := &tasks[i]
		runs, runsErr := a.taskStore.ListRuns(t.ID)
		if runsErr != nil {
			slog.Warn("rest: activity runs: could not read a task's runs; its rows are omitted",
				"task_id", t.ID, "error", runsErr)
			continue
		}
		for _, run := range runs {
			if !run.IsOpen() || run.Status != task.StatusInProgress {
				continue
			}
			role, visible := activityRunRole(agentID, t, run)
			if !visible {
				continue
			}
			rows = append(rows, toActivityRun(t, run, role, lifecycle))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartedAt.After(rows[j].StartedAt) })
	jsonOK(w, rows)
}

// activityRunRole decides whether agentID sees run and why: as the task's
// assignee, or because one of the run's captured recipients is that agent's main.
func activityRunRole(agentID string, t *task.Task, run task.TaskRun) (gen.AgentActivityRunRole, bool) {
	if t.AgentID == agentID {
		return gen.AgentActivityRunRoleAssignee, true
	}
	for _, recipient := range run.RecipientSessionIDs {
		if _, recipientAgent, ok := session.SplitMainSessionID(recipient); ok && recipientAgent == agentID {
			return gen.AgentActivityRunRoleRecipient, true
		}
	}
	return "", false
}

// toActivityRun projects one open run onto the wire row. The run's mode is MAIN
// when its session is a child steered under a main (FR-017); its state follows
// the session's lifecycle record and defaults to running when there is none.
func toActivityRun(t *task.Task, run task.TaskRun, role gen.AgentActivityRunRole, lifecycle *session.LifecycleStore) gen.AgentActivityRun {
	row := gen.AgentActivityRun{
		RunId:     run.RunID,
		TaskId:    t.ID,
		TaskTitle: t.Title,
		Kind:      gen.AgentActivityRunKindTask,
		Mode:      gen.AgentActivityRunModeIsolated,
		State:     gen.AgentActivityRunStateRunning,
		Role:      role,
		AgentId:   t.AgentID,
		StartedAt: parseTimeOrNow(run.StartedAt),
	}
	if run.Kind == task.RunKindScheduled {
		row.Kind = gen.AgentActivityRunKindScheduled
	}
	if run.SessionID == "" {
		return row
	}
	sid := run.SessionID
	row.SessionId = &sid
	if lifecycle == nil {
		return row
	}
	rec, err := lifecycle.Load(run.SessionID)
	if err != nil || rec == nil {
		return row
	}
	switch rec.State {
	case session.LifecycleQueued:
		row.State = gen.AgentActivityRunStateQueued
	case session.LifecycleNeedsInput:
		row.State = gen.AgentActivityRunStateWaiting
	}
	if rec.SteeredBy != nil {
		if _, _, isMain := session.SplitMainSessionID(rec.SteeredBy.SteeringSessionID); isMain {
			row.Mode = gen.AgentActivityRunModeMain
		}
	}
	return row
}
