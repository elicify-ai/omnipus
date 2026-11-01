// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// task_trigger.go — the `trigger` argument of create_task and update_task
// (tool-parity spec r17 §7.2 TK-30, §7.7, §7.11; scenarios 18.1–18.18,
// 18.23; dataset D-9). The wire shape is REST's TaskTrigger; the conversion
// (task.BuildTrigger, task.LegacyTimingKeysMessage) and the validation
// (task.ValidateTrigger) are the same code REST uses, so the refusal is the
// store's own message. Scheduling states:
//
//   - manual or no trigger: today's behaviour (created `next`).
//   - once/recurring on create: the task lands `inbox`, the scheduler-owned
//     waiting state, never `next`.
//   - attaching a future trigger to a `next` task that is not running moves
//     it to `inbox` in the same write; `blocked`, running and terminal tasks
//     keep their state.
//   - trigger manual removes the schedule and changes no state.
//
// Foreign activation: a non-manual trigger on a create for another workspace,
// or attached to a task in another workspace, is refused with the §7.11
// text — nothing stored, no job armed. Clearing to manual is allowed.

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// foreignTriggerRefusalText is the §7.11 refusal for attaching or changing a
// non-manual trigger on a task in another workspace.
const foreignTriggerRefusalText = "This task runs in another workspace: only its assignee there or a person " +
	"can set its trigger. Nothing was changed."

// foreignScheduledCreateRefusalText is the §7.11 refusal for create_task with
// a non-manual trigger for an assignee in another workspace.
const foreignScheduledCreateRefusalText = "Tasks for agents in another workspace start only when their team " +
	"picks them up there: create it without a trigger, or ask the assignee (or a person) to add the schedule " +
	"after pickup. Nothing was created."

// triggerParameter is the shared `trigger` JSON-schema property of
// create_task and update_task (REST's TaskTrigger shape).
func triggerParameter(purpose string) map[string]any {
	return map[string]any{
		"type": "object",
		"description": purpose + " Same shape as the Calendar: {type, config}. " +
			"type \"manual\" (config {}) means no schedule; \"once\" needs config.at_ms (Unix epoch " +
			"milliseconds); \"recurring\" needs config.rrule (RFC 5545 body without the RRULE: prefix), " +
			"config.dtstart_ms (first occurrence, epoch ms) and config.tz (IANA zone). The rule subset the " +
			"Calendar editor writes: FREQ DAILY/WEEKLY/MONTHLY/YEARLY, INTERVAL, BYDAY, BYMONTHDAY, COUNT, " +
			"UNTIL. A scheduled task waits in inbox until its occurrence; it never runs early.",
		"properties": map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"manual", "once", "recurring"}},
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"at_ms":      map[string]any{"type": "integer"},
					"rrule":      map[string]any{"type": "string", "maxLength": 512},
					"dtstart_ms": map[string]any{"type": "integer"},
					"tz":         map[string]any{"type": "string"},
				},
			},
		},
		"required": []string{"type", "config"},
	}
}

// TriggerFromArg decodes a tool `trigger` argument into an internal trigger
// and validates it exactly as REST does: the store's ValidateTrigger first,
// then the removed every_ms / cron_expr keys. The error text is the refusal.
func TriggerFromArg(raw any) (*task.Trigger, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("trigger must be an object with type and config")
	}
	kind, ok := obj["type"].(string)
	if !ok || strings.TrimSpace(kind) == "" {
		return nil, fmt.Errorf("trigger.type is required")
	}
	cfg := map[string]any{}
	if rawCfg, present := obj["config"]; present && rawCfg != nil {
		cfg, ok = rawCfg.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("trigger.config must be an object")
		}
	}
	atMs, err := optionalInt64(cfg, "at_ms")
	if err != nil {
		return nil, err
	}
	dtstartMs, err := optionalInt64(cfg, "dtstart_ms")
	if err != nil {
		return nil, err
	}
	rrule, err := optionalString(cfg, "rrule")
	if err != nil {
		return nil, err
	}
	tz, err := optionalString(cfg, "tz")
	if err != nil {
		return nil, err
	}
	tr := task.BuildTrigger(kind, atMs, rrule, dtstartMs, tz)
	if vErr := task.ValidateTrigger(tr); vErr != nil {
		return nil, vErr
	}
	if msg := task.LegacyTimingKeysMessage(cfg); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	return tr, nil
}

func optionalInt64(cfg map[string]any, key string) (*int64, error) {
	if _, present := cfg[key]; !present {
		return nil, nil
	}
	v, err := getInt64Arg(cfg, key, 0)
	if err != nil {
		return nil, fmt.Errorf("trigger config.%s: %w", key, err)
	}
	return &v, nil
}

func optionalString(cfg map[string]any, key string) (*string, error) {
	raw, present := cfg[key]
	if !present {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("trigger config.%s must be a string", key)
	}
	return &s, nil
}

// scheduledTrigger reports whether tr arms a schedule (once or recurring).
func scheduledTrigger(tr *task.Trigger) bool {
	return tr != nil && tr.Type != task.TriggerManual
}

// applyCreateTrigger parses create_task's optional trigger. Called after the
// delegation gate, so a target with no edge hears the delegation refusal
// first (18.3). A scheduled trigger for another workspace is refused (18.2).
func (tc *taskCreateToolExecute) applyCreateTrigger() (*ToolResult, bool) {
	raw, present := tc.args["trigger"]
	if !present || raw == nil {
		return nil, false
	}
	tr, err := TriggerFromArg(raw)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_create failed: %v", err)).WithError(err), true
	}
	if scheduledTrigger(tr) && tc.foreign {
		return ErrorResult(foreignScheduledCreateRefusalText), true
	}
	tc.trigger = tr
	return nil, false
}

// applyTriggerField handles update_task's trigger (organisation group:
// creator or assignee, already checked; not frozen while running).
func (tu *taskUpdateToolExecute) applyTriggerField() (*ToolResult, bool) {
	raw, present := tu.args["trigger"]
	if !present || raw == nil {
		return nil, false
	}
	tr, err := TriggerFromArg(raw)
	if err != nil {
		return ErrorResult(fmt.Sprintf("task_update failed: %v", err)).WithError(err), true
	}
	if scheduledTrigger(tr) {
		ownWS := strings.TrimSpace(ToolWorkspaceID(tu.ctx))
		if ownWS != "" && tu.existing.WorkspaceID != "" && tu.existing.WorkspaceID != ownWS {
			return ErrorResult(foreignTriggerRefusalText), true
		}
	}
	tu.patch.Trigger = &tr
	tu.triggerSent = true
	tu.updatedFields = append(tu.updatedFields, "trigger")
	// Scheduling state 3: a future trigger on a dispatchable task moves it to
	// the waiting state in the same write, so the ordinary drain cannot run it
	// early. A caller's own status choice in the same call is left to the
	// store's transition rules.
	if tu.patch.Status == nil && tu.existing.Status == task.StatusNext {
		probe := *tu.existing
		probe.Trigger = tr
		if task.TriggerAwaitingOccurrence(&probe, time.Now()) {
			inbox := task.StatusInbox
			tu.patch.Status = &inbox
			tu.newStatus = inbox
		}
	}
	return nil, false
}

// AuditTriggerChange records the existing FR-022 trigger audit event,
// task.trigger.recurrence_changed, for a save that changed a task's trigger to
// a different recurring rule. It is the one implementation REST and
// update_task share (r17 TK-30); actorAgentID is the agent when a tool made
// the change and empty for a person. The prior and resulting statuses are
// recorded so the state move is reconstructible. Best-effort: a log failure is
// recorded, never surfaced, matching the other task audit events.
func AuditTriggerChange(lg *audit.Logger, actorAgentID, taskID string,
	priorTrigger, newTrigger *task.Trigger, priorStatus, newStatus task.Status,
) {
	if lg == nil || priorTrigger == nil || newTrigger == nil {
		return
	}
	if newTrigger.Type != task.TriggerRecurring || reflect.DeepEqual(*priorTrigger, *newTrigger) {
		return
	}
	if err := lg.Log(&audit.Entry{
		Event:    "task.trigger.recurrence_changed",
		Decision: audit.DecisionAllow,
		AgentID:  actorAgentID,
		Details: map[string]any{
			"task_id":       taskID,
			"prior_trigger": priorTrigger,
			"new_trigger":   newTrigger,
			"prior_status":  priorStatus,
			"new_status":    newStatus,
		},
	}); err != nil {
		slog.Error("task trigger recurrence-change audit log failed", "task_id", taskID, "error", err)
	}
}
