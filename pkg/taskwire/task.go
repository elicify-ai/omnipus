// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package taskwire converts task, plan and goal records into their generated
// wire types. It is the one conversion both readers use: the REST surface
// (pkg/gateway, GET /tasks/{id} and GET /plans/{id}) and the agent read tools
// get_task, get_plan and get_goal (pkg/tools, tool-parity spec r17 TK-31,
// PL-12, GL-04, §7.13). Read-time values that need the running system (the
// agent's display name, the assignee warning, live activity, sub-run rollup,
// the effective attempt limit) are resolved by each caller and passed in, so
// this package stays free of the agent loop and the gateway.
package taskwire

import (
	"log/slog"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// RollupItems is the gen.Task.Rollup element list.
// not-wire-format: an alias for the generated inline type, not a parallel one.
type RollupItems = []struct {
	AgentId string               `json:"agent_id"`
	Label   string               `json:"label"`
	Status  gen.TaskRollupStatus `json:"status"`
}

// TaskExtras carries the read-time values a caller resolves from the running
// system. A zero value means "unknown here": the field stays absent on the
// wire (EffectiveMaxAttempts is the exception: the caller always resolves it,
// as the executor does, so it is always emitted).
type TaskExtras struct {
	// AgentName is the assignee's display name, "" when unknown.
	AgentName string
	// AssigneeWarning says why the assignee cannot finish the task, "" for none.
	AssigneeWarning string
	// EffectiveMaxAttempts is the attempt limit the executor enforces.
	EffectiveMaxAttempts int
	// LastActivityAt is the in-progress task's last activity, nil when none.
	LastActivityAt *time.Time
	// Rollup lists the live child runs, nil when there are none.
	Rollup *RollupItems
}

// Task converts t to the generated wire type. g is the task's paired goal
// record (ADR-086 D2/D5) or nil when the task has none; the caller resolves it
// and must not pass nil for a record it failed to read (silent-failure finding
// SF-6: `dod` is omitempty, so a read failure would look like "no DoD").
func Task(t task.Task, g *goal.Goal, x TaskExtras) gen.Task {
	out := gen.Task{
		Id:          t.ID,
		Title:       t.Title,
		Action:      gen.TaskAction(t.Action),
		Status:      gen.TaskStatus(t.Status),
		WorkspaceId: t.WorkspaceID,
		Owner:       t.Owner,
		CreatedBy:   t.CreatedBy,
	}
	if out.Action == "" {
		out.Action = gen.TaskAction(task.ActionLLM)
	}

	prio := t.EffectivePriority()
	out.Priority = &prio
	if t.RunIsolated {
		out.RunIsolated = ptr(true)
	}
	surface := gen.TaskSurface(t.EffectiveSurface())
	out.Surface = &surface

	if t.Description != "" {
		out.Description = ptr(t.Description)
	}
	if t.Prompt != "" {
		out.Prompt = ptr(t.Prompt)
	}
	if t.AgentID != "" {
		out.AgentId = ptr(t.AgentID)
		if x.AgentName != "" {
			out.AgentName = ptr(x.AgentName)
		}
	}
	wireListFields(&out, t)
	if t.ParentTaskID != "" {
		out.ParentTaskId = ptr(t.ParentTaskID)
	}
	if t.PlanID != "" {
		out.PlanId = ptr(t.PlanID)
	}
	if t.Stream != "" {
		out.Stream = ptr(t.Stream)
	}
	if t.IsJoin {
		out.IsJoin = ptr(t.IsJoin)
	}
	setJudgedLists(&out, t, g)
	// Founder decision 2026-09-15: a task whose agent cannot finish it as
	// configured says so, next to the agent picker. Read-time only; it warns
	// and never refuses.
	if x.AssigneeWarning != "" {
		out.AssigneeWarning = &struct {
			Field   gen.TaskAssigneeWarningField `json:"field"`
			Message string                       `json:"message"`
		}{Field: gen.TaskAssigneeWarningFieldAgentId, Message: x.AssigneeWarning}
	}
	// The goal's tries (issue #710): the tries its current (or last) run has
	// used and the try limit that run started with, both read off the goal
	// record — never mixed with the task attempt counter.
	if g != nil {
		if g.Round > 0 {
			out.JudgeRounds = ptr(g.Round)
		}
		if g.MaxRounds >= 1 {
			out.GoalMaxRounds = ptr(g.MaxRounds)
		}
	}
	if t.AttemptCount > 0 {
		out.AttemptCount = ptr(t.AttemptCount)
	}
	if t.MaxAttempts != nil {
		out.MaxAttempts = ptr(*t.MaxAttempts)
	}
	// effective_max_attempts: the task attempt limit (how many fresh runs this
	// task gets), resolved by the caller with the function the executor
	// enforces. It is not the goal try limit (founder decision 2026-09-14).
	out.EffectiveMaxAttempts = ptr(x.EffectiveMaxAttempts)
	setScheduleAndOrigin(&out, t)
	setTimestamps(&out, t)
	if x.LastActivityAt != nil {
		at := x.LastActivityAt.UTC()
		out.LastActivityAt = &at
	}
	out.Rollup = x.Rollup
	return out
}

// setJudgedLists sets criteria and dod. GOAL-FR-003/FR-029/FR-048 (ADR-086
// D5): both judged lists live on the task's paired goal record and the wire
// reads them from there, one source of truth. A task with no goal record (a
// legacy, pre-D-C task) has no dod and falls back to the task record's own
// criteria for continuity.
func setJudgedLists(out *gen.Task, t task.Task, g *goal.Goal) {
	if g != nil && len(g.Criteria) > 0 {
		out.Criteria = wireCriteria(g.Criteria)
	} else if len(t.Criteria) > 0 {
		out.Criteria = wireCriteria(t.Criteria)
	}
	if g != nil && len(g.DoD) > 0 {
		out.Dod = Dod(g.DoD)
	}
}

// setScheduleAndOrigin sets the trigger, due date, chat origin, session,
// result, cancel reason and artifacts, each only when present.
func setScheduleAndOrigin(out *gen.Task, t task.Task) {
	if t.Trigger != nil {
		out.Trigger = wireTrigger(t.Trigger)
	}
	if t.Due != "" {
		if ts, err := time.Parse(time.RFC3339, t.Due); err == nil {
			out.Due = &ts
		}
	}
	if t.SourceChannel != "" {
		out.SourceChannel = ptr(t.SourceChannel)
	}
	if t.SourceChatID != "" {
		out.SourceChatId = ptr(t.SourceChatID)
	}
	if t.SessionID != "" {
		out.SessionId = ptr(t.SessionID)
	}
	if t.Result != "" {
		out.Result = ptr(t.Result)
	}
	if t.CancelReason != "" {
		cr := gen.TaskCancelReason(t.CancelReason)
		out.CancelReason = &cr
	}
	if len(t.Artifacts) > 0 {
		arts := append([]string{}, t.Artifacts...)
		out.Artifacts = &arts
	}
}

// setTimestamps sets created/updated (always) and started/completed (when set).
func setTimestamps(out *gen.Task, t task.Task) {
	out.CreatedAt = ParseTimeOrNow(t.CreatedAt)
	out.UpdatedAt = ParseTimeOrNow(t.UpdatedAt)
	if t.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.StartedAt); err == nil {
			out.StartedAt = &ts
		}
	}
	if t.CompletedAt != "" {
		if ts, err := time.Parse(time.RFC3339, t.CompletedAt); err == nil {
			out.CompletedAt = &ts
		}
	}
}

// Rollup builds the read-time rollup from a parent's child tasks: one row per
// live (non-terminal) child run. It returns nil when no child is live.
func Rollup(children []task.Task) *RollupItems {
	if len(children) == 0 {
		return nil
	}
	items := make(RollupItems, 0, len(children))
	for _, c := range children {
		if task.IsTerminal(c.Status) {
			continue
		}
		items = append(items, struct {
			AgentId string               `json:"agent_id"`
			Label   string               `json:"label"`
			Status  gen.TaskRollupStatus `json:"status"`
		}{AgentId: c.AgentID, Label: c.Title, Status: gen.TaskRollupStatus(c.Status)})
	}
	if len(items) == 0 {
		return nil
	}
	return &items
}

// LastActivityAt resolves Task.last_activity_at for an in-progress task: the
// LATER of the live progress stamp of its running turn (live) and the last
// write to its session transcript (transcript), either zero when unknown.
// ok is false when the task is not in progress or no evidence exists — the
// field is then absent on the wire, never fabricated. A stamp from the future
// (clock jump) still counts, so the reader shows "just now", never a
// negative age.
func LastActivityAt(status task.Status, live, transcript, now time.Time) (time.Time, bool) {
	if status != task.StatusInProgress {
		return time.Time{}, false
	}
	switch {
	case live.After(transcript) && !live.After(now):
		return live, true
	case transcript.After(live) && !transcript.After(now):
		return transcript, true
	case live.After(now) || transcript.After(now):
		if live.After(transcript) {
			return live, true
		}
		return transcript, true
	default:
		return time.Time{}, false
	}
}

// ParseTimeOrNow parses an RFC 3339 timestamp, falling back to now on error.
// It logs a warning when a non-empty string fails to parse (empty is normal
// for an unset optional field).
func ParseTimeOrNow(s string) time.Time {
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts
	}
	if s != "" {
		slog.Warn("rest_tasks: corrupt timestamp, defaulting to now", "value", s)
	}
	return time.Now().UTC()
}

func ptr[T any](v T) *T { return &v }
