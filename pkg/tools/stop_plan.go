// Omnipus — stop_plan Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — stop_plan (ADR-055, plan-supervisor-spec FR-042/FR-043,
// US-8). The containment control: an agent that can start autonomous work
// must be able to stop it.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// stopPlanDeniedMessage is the SINGLE opaque denial (FR-043, mirroring
// FR-010). It does not name the plan id, does not name the owner, and does
// not distinguish "this plan is not yours" from "there is no such plan" —
// otherwise the tool is a plan-existence oracle for any agent holding it.
const stopPlanDeniedMessage = "stop_plan denied: you are not the owner of a stoppable plan with that id"

// stopPlanToolChannel is the channel attributed to a tool-originated stop when
// the calling turn has no channel bound. StopPlan's channel argument flows into
// the cancel attribution (RequestCancelForSession) and into the handover text,
// so it must never be the empty string (FR-042).
const stopPlanToolChannel = "tool"

// StopPlanFunc stops a plan and cascades the cancellation to everything
// working on it. It mirrors *pkg/agent.PlanEngine.StopPlan, injected as a func
// value rather than called directly because pkg/tools must not import
// pkg/agent (pkg/agent already imports pkg/tools — a cycle). The wiring layer
// supplies it.
//
// userID is the actor attributed with the stop; channel is the surface it came
// from. StopPlan is already actor-parameterised on both.
type StopPlanFunc func(ctx context.Context, planID, userID, channel string) (*plan.Plan, error)

// PlanStopTool implements the stop_plan agent tool (FR-042/FR-043, US-8).
//
// Authority is the plan's OWNER — `caller.AgentID == p.OwnerAgentID` —
// deliberately the inverse of plan_correct's PlanSupervisor-only authority.
// The adjudicator corrects; the owner contains. PlanSupervisor does not hold
// this tool.
//
// Stopping cascades: every in-flight member session, every verifier session,
// the owner session and the supervision session are cancelled, then the plan
// is failed stopped_by_user. That cascade is the engine's, reached through the
// same StopPlan the SPA's Stop button calls — this tool adds the agent-facing
// surface and the owner-authority gate, not a second stop path.
type PlanStopTool struct {
	BaseTool
	planStore *plan.Store
	// stopPlan is the engine hook. FAIL CLOSED when unwired: an unwired hook
	// is a configuration error, never a silent success. Reporting a stop that
	// did not happen is the worst possible failure mode for a kill switch —
	// the operator stops watching.
	stopPlan StopPlanFunc
}

// NewPlanStopTool constructs a PlanStopTool. planStore may be nil for
// metadata-only construction (GeneralBuiltinMetadata) — never Execute()d in
// that mode.
func NewPlanStopTool(planStore *plan.Store) *PlanStopTool {
	return &PlanStopTool{planStore: planStore}
}

// SetStopPlan installs the engine hook (see the field doc).
func (t *PlanStopTool) SetStopPlan(fn StopPlanFunc) {
	t.stopPlan = fn
}

func (t *PlanStopTool) Name() string           { return "stop_plan" }
func (t *PlanStopTool) Scope() ToolScope       { return ScopeCore }
func (t *PlanStopTool) Category() ToolCategory { return CategoryTasks }

func (t *PlanStopTool) Description() string {
	return "Stop a plan you own and cancel everything working on it: every in-flight member task, " +
		"every verifier run, and the supervisor's adjudication. The plan ends failed (stopped by user) " +
		"and cannot be resumed — this is containment, not a pause. Only the plan's owner agent can " +
		"stop it. IMPORTANT: a plan shown as blocked or awaiting supervision is NOT stuck — an " +
		"adjudicator may be actively working out how to correct it, and stopping it aborts that " +
		"in-flight adjudication and discards the correction it was about to make. Use this for work " +
		"that must not continue, not for work that is merely slow. Only a running or approved plan " +
		"can be stopped; a draft has nothing in flight and a finished or failed plan is terminal. " +
		"Denials are deliberately identical whether the plan is not yours or does not exist."
}

func (t *PlanStopTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan_id": map[string]any{
				"type":        "string",
				"description": "ID of the plan to stop. You must be the plan's owner agent.",
			},
		},
		"required": []string{"plan_id"},
	}
}

func (t *PlanStopTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.planStore == nil {
		return ErrorResult("stop_plan failed: plan store is not available")
	}
	// FAIL CLOSED when unwired (FR-042). This is checked before any plan is
	// read, and it leaks nothing about any particular plan — it is a property
	// of the process, not of the id in the request.
	if t.stopPlan == nil {
		slog.Error("stop_plan: no stop hook installed — denying by default")
		return ErrorResult(
			"stop_plan failed: the plan engine is not wired (configuration error) — denying by default")
	}

	planID, _ := args["plan_id"].(string)
	if planID == "" {
		return ErrorResult("plan_id is required")
	}
	callerID := ToolAgentID(ctx)

	// --- owner-authority gate (FR-043) -----------------------------------
	// Every denial below returns the SAME message: a caller with no identity,
	// a caller that is not the owner, and a plan that does not exist are
	// indistinguishable. The plan id and the real reason go to the
	// server-side log only.
	if callerID == "" {
		slog.Warn("stop_plan: denied — caller has no agent identity", "plan_id", planID)
		return ErrorResult(stopPlanDeniedMessage)
	}
	p, err := t.planStore.Get(planID)
	if err != nil {
		slog.Warn("stop_plan: denied — plan could not be read",
			"caller_id", callerID, "plan_id", planID, "error", err)
		return ErrorResult(stopPlanDeniedMessage)
	}
	if p.OwnerAgentID != callerID {
		slog.Warn("stop_plan: denied — caller is not the plan's owner agent",
			"caller_id", callerID, "plan_id", planID, "owner_agent_id", p.OwnerAgentID)
		return ErrorResult(stopPlanDeniedMessage)
	}

	// The caller IS the owner, so real state errors are returned from here on.
	// StopPlan itself accepts only running or approved plans; a terminal plan
	// is rejected with no cascade and no second terminal write (E32).
	if p.State != plan.StateRunning && p.State != plan.StateApproved {
		return ErrorResult(fmt.Sprintf(
			"plan %q is %q; only a running or approved plan can be stopped", planID, p.State))
	}

	channel := ToolChannel(ctx)
	if channel == "" {
		channel = stopPlanToolChannel
	}

	stopped, sErr := t.stopPlan(ctx, planID, callerID, channel)
	if sErr != nil {
		return ErrorResult(fmt.Sprintf("stop_plan failed: %v", sErr))
	}

	payload := map[string]any{
		"plan_id": planID,
		"state":   string(plan.StateFailed),
		"note": "plan stopped: every in-flight member task, verifier run and supervision turn on this " +
			"plan has been cancelled. A stopped plan is terminal and cannot be resumed.",
	}
	if stopped != nil {
		payload["state"] = string(stopped.State)
		if stopped.FailedReason != "" {
			payload["failed_reason"] = string(stopped.FailedReason)
		}
	}
	encoded, mErr := json.Marshal(payload)
	if mErr != nil {
		// The plan is already stopped — report the outcome rather than an
		// error the caller would (wrongly) read as "the stop did not happen".
		slog.Error("stop_plan: could not encode result payload", "plan_id", planID, "error", mErr)
		return NewToolResult(fmt.Sprintf("plan %s stopped", planID))
	}
	return NewToolResult(string(encoded))
}
