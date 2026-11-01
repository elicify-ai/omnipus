// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// plan_update.go — update_plan (tool-parity spec r17 §7.3 PL-02..PL-08).
// Authority: the plan's owner_agent_id, or the agent recorded as its creator
// (CreatedByKind == agent and CreatedBy == caller). A legacy plan with an
// empty CreatedByKind has no agent creator. An unknown plan and a plan the
// caller may not edit get the same refusal. dod and owner_agent_id are frozen
// once the plan has left draft; title, objective, description and bounds stay
// editable in any state. No state parameter: state changes keep their own
// tools and endpoints (PL-09).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

const (
	planUpdateDeniedText  = "update_plan denied: you are not the owner of a plan with that id"
	planDoDFrozenText     = "dod cannot be changed once the plan has left draft state"
	planOwnerFrozenText   = "owner_agent_id cannot be changed once the plan has left draft state"
	planUpdateNoFieldText = "no updatable fields provided (supply at least one of title, objective, description, owner_agent_id, dod, bounds)"
)

// PlanUpdateTool edits a plan's definition and limits.
type PlanUpdateTool struct {
	BaseTool
	store         *plan.Store
	validateOwner func(ownerAgentID string) error
	planningFn    PlanningConfigFn
}

// NewPlanUpdateTool constructs update_plan. store may be nil for
// metadata-only construction; never executed in that mode.
func NewPlanUpdateTool(store *plan.Store) *PlanUpdateTool {
	return &PlanUpdateTool{store: store}
}

// SetOwnerValidator installs the owner_agent_id check (same validator as
// create_plan; fail closed when unwired).
func (t *PlanUpdateTool) SetOwnerValidator(fn func(ownerAgentID string) error) {
	t.validateOwner = fn
}

// SetPlanningConfigFn installs the live planning-configuration reader used for
// the bounds ceiling. Read at every call, never captured.
func (t *PlanUpdateTool) SetPlanningConfigFn(fn PlanningConfigFn) {
	t.planningFn = fn
}

func (t *PlanUpdateTool) Name() string           { return "update_plan" }
func (t *PlanUpdateTool) Scope() ToolScope       { return ScopeCore }
func (t *PlanUpdateTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanUpdateTool) Description() string {
	return "Change a plan you own or created: title, objective, description, owner_agent_id, dod or bounds.\n" +
		"Only provided fields change. title, objective, description and bounds can change in any state; " +
		"owner_agent_id and dod only while the plan is a draft. bounds merge field by field, and each value " +
		"must be at most its global limit in Settings. State changes use execute_plan and stop_plan."
}

func (t *PlanUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "ID of the plan to change"},
			"title":   map[string]any{"type": "string", "description": "New title (1-200 characters)"},
			"objective": map[string]any{
				"type":        "string",
				"description": "New plain-prose objective (up to 2000 characters)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "New description (up to 2000 characters)",
			},
			"owner_agent_id": map[string]any{
				"type":        "string",
				"description": "New owner; draft plans only. Must be a real, addressable agent (not a System Agent or worker)",
			},
			"dod": map[string]any{
				"type":        "array",
				"items":       planCreateDoDItemsSchema(),
				"description": "Replacement plan-level Definition of Done (at least one criterion); draft plans only",
			},
			"bounds": planBoundsParamSchema(),
		},
		"required": []string{"plan_id"},
	}
}

func (t *PlanUpdateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("update_plan failed: plan store is not available")
	}
	callerID := ToolAgentID(ctx)
	planID, _ := args["plan_id"].(string)
	if callerID == "" || planID == "" || strings.ContainsAny(planID, "/\\\x00") || strings.Contains(planID, "..") {
		return ErrorResult(planUpdateDeniedText)
	}
	existing, err := t.store.Get(planID)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			return ErrorResult(planUpdateDeniedText)
		}
		return ErrorResult(fmt.Sprintf("update_plan failed: could not read the plan: %v", err))
	}
	if !planEditableBy(existing, callerID) {
		return ErrorResult(planUpdateDeniedText)
	}
	patch, fields, refusal := t.buildPatch(existing, args, callerID)
	if refusal != "" {
		return ErrorResult(refusal)
	}
	if len(fields) == 0 {
		return ErrorResult(planUpdateNoFieldText)
	}
	updated, err := t.store.Update(planID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("update_plan failed: %v", err))
	}
	out, err := json.Marshal(map[string]any{"plan_id": updated.ID, "state": updated.State, "updated_fields": fields})
	if err != nil {
		return ErrorResult(fmt.Sprintf("update_plan failed: encode result: %v", err))
	}
	return NewToolResult(string(out))
}

// planEditableBy is the update_plan authority rule.
func planEditableBy(p *plan.Plan, callerID string) bool {
	if p.OwnerAgentID == callerID {
		return true
	}
	return p.CreatedByKind == plan.CreatedByKindAgent && p.CreatedBy == callerID
}

// buildPatch turns the arguments into one store patch; every check runs before
// the write, so a refusal changes nothing.
func (t *PlanUpdateTool) buildPatch(existing *plan.Plan, args map[string]any, callerID string) (plan.Patch, []string, string) {
	var patch plan.Patch
	fields := []string{}
	draft := existing.State == plan.StateDraft
	if raw, ok := args["dod"]; ok && raw != nil {
		if !draft {
			return patch, nil, planDoDFrozenText
		}
		items, isList := raw.([]any)
		if !isList || len(items) == 0 {
			return patch, nil, "dod must list at least one Definition-of-Done criterion"
		}
		dod, dErr := parseCriteriaArgs(items, callerID)
		if dErr != nil {
			return patch, nil, fmt.Sprintf("update_plan failed: dod: %v", dErr)
		}
		patch.DoD = &dod
		fields = append(fields, "dod")
	}
	if owner, ok := args["owner_agent_id"].(string); ok {
		if !draft {
			return patch, nil, planOwnerFrozenText
		}
		if refusal := t.checkOwner(owner, callerID); refusal != "" {
			return patch, nil, refusal
		}
		patch.OwnerAgentID = &owner
		fields = append(fields, "owner_agent_id")
	}
	for _, f := range []struct {
		key  string
		slot **string
	}{{"title", &patch.Title}, {"objective", &patch.Objective}, {"description", &patch.Description}} {
		if v, ok := args[f.key].(string); ok {
			value := v
			*f.slot = &value
			fields = append(fields, f.key)
		}
	}
	if raw, ok := args["bounds"]; ok && raw != nil {
		merged, refusal := mergePlanBounds(existing.Bounds, raw, livePlanning(t.planningFn))
		if refusal != "" {
			return patch, nil, refusal
		}
		patch.Bounds = &merged
		fields = append(fields, "bounds")
	}
	return patch, fields, ""
}

// checkOwner validates a new owner; fail closed when no validator is wired.
func (t *PlanUpdateTool) checkOwner(owner, callerID string) string {
	if owner == "" {
		return "owner_agent_id must not be empty"
	}
	if t.validateOwner == nil {
		slog.Error("update_plan: no owner-agent validator installed — denying by default",
			"caller_id", callerID, "owner_agent_id", owner)
		return "update_plan failed: cannot verify owner_agent_id (owner validator not configured) — denying by default"
	}
	if err := t.validateOwner(owner); err != nil {
		return fmt.Sprintf("update_plan failed: %v", err)
	}
	return ""
}

// planCreateDoDItemsSchema returns create_plan's dod item schema so the two
// tools describe one criterion shape. create_plan always declares it; a nil
// result would only mean its schema lost the dod property.
func planCreateDoDItemsSchema() any {
	props, _ := NewPlanCreateTool(nil).Parameters()["properties"].(map[string]any)
	dod, _ := props["dod"].(map[string]any)
	return dod["items"]
}
