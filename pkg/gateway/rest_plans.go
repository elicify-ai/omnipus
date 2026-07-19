//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_plans.go — the Plans REST surface (ADR-049 D1/D4, spec Part A/B, Wave
// 2-C1 contracts row R4/C19). Deferred paths from Wave 0/1's contract-surface
// table:
//
//	GET/POST /workspaces/{id}/plans   list/create (workspace-nested, mirrors
//	                                   the removed Milestone shape)
//	GET/PUT/DELETE /plans/{id}        get/update/delete
//	POST /plans/{id}/approve          tiered-DoD + unconditional member-
//	                                  criteria gated draft->approved
//	POST /plans/{id}/stop             running->failed(stopped_by_user)
//
// The GET/POST /workspaces/{id}/plans paths are dispatched from
// HandleWorkspaces (rest_workspaces.go) via a "/plans" suffix branch, exactly
// mirroring the existing "/delegation" and "/instructions" branches. All
// mutating writes go through a.planStore (pkg/plan.Store), whose OnChange
// hook (wired at boot in gateway.go) emits the plan_status WS frame for every
// successful Create/Update — this file never emits frames directly.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// isPlanValidationErr reports whether err is a user-facing validation error
// (400) rather than an internal failure (500). All plan.Store validation
// errors wrap plan.ErrValidation (ErrIllegalPlanTransition included, since it
// itself wraps ErrValidation). ErrNotFound is handled separately as 404 by
// every caller — it must NOT match here.
func isPlanValidationErr(err error) bool {
	return errors.Is(err, plan.ErrValidation)
}

// auditPlan writes an audit entry for a plan mutation (best-effort, mirrors auditTask).
func (a *restAPI) auditPlan(event, id string) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: audit.DecisionAllow,
		Details:  map[string]any{"id": id},
	}); err != nil {
		slog.Error("rest: plan audit log failed", "event", event, "error", err)
	}
}

// isAgentID reports whether id resolves to a known agent in the registry
// (SD-A7's tiered-DoD authorship-kind heuristic: a Plan's CreatedBy is either
// a human username or an agent ID depending on who created it — there is no
// separate discriminator field, mirroring how CriterionAuthor.Kind is
// recorded explicitly at criterion-authorship time but Plan.CreatedBy is not).
// Every plan created via THIS wave's only creation path (handleWorkspacePlanCreate,
// human/UI via REST) sets CreatedBy to the caller's username, which never
// resolves to an agent ID — so this heuristic naturally routes every
// REST-created plan through the soft tier today, while staying
// forward-compatible with a future agent-side create_plan tool (out of this
// wave's scope) that would set CreatedBy to an agent ID and thereby trigger
// the strict tier automatically, with no change needed here.
func (a *restAPI) isAgentID(id string) bool {
	if id == "" || a.agentLoop == nil {
		return false
	}
	reg := a.agentLoop.GetRegistry()
	if reg == nil {
		return false
	}
	_, ok := reg.GetAgent(id)
	return ok
}

// --- wire mapping ------------------------------------------------------------

// toWirePlan converts an internal plan.Plan to the generated wire type,
// server-computing `progress` read-time via plan.ComputeProgress (R4/C19,
// mirrors the removed milestone's computeMilestoneCounts pattern —
// rest_milestones.go:153, now deleted).
func (a *restAPI) toWirePlan(p plan.Plan) gen.Plan {
	out := gen.Plan{
		Id:           p.ID,
		WorkspaceId:  p.WorkspaceID,
		Title:        p.Title,
		State:        gen.PlanState(p.State),
		OwnerAgentId: p.OwnerAgentID,
		CreatedAt:    parseTimeOrNow(p.CreatedAt),
		UpdatedAt:    parseTimeOrNow(p.UpdatedAt),
	}
	if p.Goal != "" {
		out.Goal = ptr(p.Goal)
	}
	if p.Description != "" {
		out.Description = ptr(p.Description)
	}
	phase := gen.PlanPlanPhase(p.EffectivePlanPhase())
	out.PlanPhase = &phase
	if p.FailedReason != "" {
		fr := gen.PlanFailedReason(p.FailedReason)
		out.FailedReason = &fr
	}
	if len(p.DoD) > 0 {
		out.Dod = toWirePlanDoD(p.DoD)
	}
	if p.Bounds != nil {
		b := struct { // not-wire-format: intermediate value shaped to match gen.Plan.Bounds' oapi-codegen anonymous field type, not a parallel wire type
			IdleExpiryDays     *int `json:"idle_expiry_days,omitempty"`
			PlanJudgeMaxRounds *int `json:"plan_judge_max_rounds,omitempty"`
		}{}
		if p.Bounds.IdleExpiryDays != nil {
			v := *p.Bounds.IdleExpiryDays
			b.IdleExpiryDays = &v
		}
		if p.Bounds.PlanJudgeMaxRounds != nil {
			v := *p.Bounds.PlanJudgeMaxRounds
			b.PlanJudgeMaxRounds = &v
		}
		out.Bounds = &b
	}
	if p.JudgeRounds > 0 {
		out.JudgeRounds = ptr(p.JudgeRounds)
	}
	out.ActiveLoop = ptr(p.ActiveLoop)
	if p.PausedReason != "" {
		out.PausedReason = ptr(p.PausedReason)
	}
	if p.LastActivityAt != "" {
		if ts, err := time.Parse(time.RFC3339, p.LastActivityAt); err == nil {
			out.LastActivityAt = &ts
		}
	}
	if p.Owner != "" {
		out.Owner = ptr(p.Owner)
	}
	if p.CreatedBy != "" {
		out.CreatedBy = ptr(p.CreatedBy)
	}
	if p.ApprovedAt != "" {
		if ts, err := time.Parse(time.RFC3339, p.ApprovedAt); err == nil {
			out.ApprovedAt = &ts
		}
	}
	if p.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339, p.StartedAt); err == nil {
			out.StartedAt = &ts
		}
	}
	if p.CompletedAt != "" {
		if ts, err := time.Parse(time.RFC3339, p.CompletedAt); err == nil {
			out.CompletedAt = &ts
		}
	}
	if a.taskStore != nil {
		if _, _, progress, err := plan.ComputeProgress(p.ID, a.taskStore); err == nil {
			pr := float32(progress)
			out.Progress = &pr
		} else {
			slog.Warn("rest: plan progress compute failed", "plan_id", p.ID, "error", err)
		}
	}
	return out
}

// toWirePlanDoD converts internal acceptance criteria to the gen.Plan.Dod
// inline wire shape (read path — GET/PUT/POST/approve/stop responses).
// Mirrors rest_tasks.go's toWireCriteria exactly, but oapi-codegen generates
// a per-schema-context anonymous struct type (PlanDodKind etc., distinct from
// TaskCriteriaKind etc. even though both alias string), so the conversion
// cannot be shared — same reason toWireCriteria/criteriaFromCreateWire/
// criteriaFromUpdateWire are already three near-duplicate functions there.
func toWirePlanDoD(cs []task.AcceptanceCriterion) *[]struct {
	Author struct {
		Id   string                `json:"id"`
		Kind gen.PlanDodAuthorKind `json:"kind"`
	} `json:"author"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string           `json:"id,omitempty"`
	Kind   gen.PlanDodKind   `json:"kind"`
	Status gen.PlanDodStatus `json:"status"`
	Text   string            `json:"text"`
} {
	out := make([]struct {
		Author struct {
			Id   string                `json:"id"`
			Kind gen.PlanDodAuthorKind `json:"kind"`
		} `json:"author"`
		Check *struct {
			Command          string `json:"command"`
			ExpectedExitCode int    `json:"expected_exit_code"`
		} `json:"check,omitempty"`
		Id     *string           `json:"id,omitempty"`
		Kind   gen.PlanDodKind   `json:"kind"`
		Status gen.PlanDodStatus `json:"status"`
		Text   string            `json:"text"`
	}, 0, len(cs))
	for _, c := range cs {
		item := struct { // not-wire-format: intermediate value built to match gen.Plan.Dod's oapi-codegen anonymous element type, not a parallel wire type
			Author struct {
				Id   string                `json:"id"`
				Kind gen.PlanDodAuthorKind `json:"kind"`
			} `json:"author"`
			Check *struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			} `json:"check,omitempty"`
			Id     *string           `json:"id,omitempty"`
			Kind   gen.PlanDodKind   `json:"kind"`
			Status gen.PlanDodStatus `json:"status"`
			Text   string            `json:"text"`
		}{
			Kind:   gen.PlanDodKind(c.Kind),
			Status: gen.PlanDodStatus(c.Status),
			Text:   c.Text,
		}
		item.Author.Id = c.Author.ID
		item.Author.Kind = gen.PlanDodAuthorKind(c.Author.Kind)
		if c.ID != "" {
			item.Id = ptr(c.ID)
		}
		if c.Check != nil {
			item.Check = &struct {
				Command          string `json:"command"`
				ExpectedExitCode int    `json:"expected_exit_code"`
			}{Command: c.Check.Command, ExpectedExitCode: c.Check.ExpectedExitCode}
		}
		out = append(out, item)
	}
	return &out
}

// planDoDFromCreateWire converts the gen.PlanCreateRequest.Dod inline wire
// shape to internal acceptance criteria (create path).
func planDoDFromCreateWire(items []struct {
	Author struct {
		Id   string                             `json:"id"`
		Kind gen.PlanCreateRequestDodAuthorKind `json:"kind"`
	} `json:"author"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string                        `json:"id,omitempty"`
	Kind   gen.PlanCreateRequestDodKind   `json:"kind"`
	Status gen.PlanCreateRequestDodStatus `json:"status"`
	Text   string                         `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Kind:   task.CriterionKind(it.Kind),
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		out = append(out, c)
	}
	return out
}

// planDoDFromUpdateWire converts the gen.PlanUpdateRequest.Dod inline wire
// shape to internal acceptance criteria (PUT path).
func planDoDFromUpdateWire(items []struct {
	Author struct {
		Id   string                             `json:"id"`
		Kind gen.PlanUpdateRequestDodAuthorKind `json:"kind"`
	} `json:"author"`
	Check *struct {
		Command          string `json:"command"`
		ExpectedExitCode int    `json:"expected_exit_code"`
	} `json:"check,omitempty"`
	Id     *string                        `json:"id,omitempty"`
	Kind   gen.PlanUpdateRequestDodKind   `json:"kind"`
	Status gen.PlanUpdateRequestDodStatus `json:"status"`
	Text   string                         `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Kind:   task.CriterionKind(it.Kind),
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		if it.Id != nil {
			c.ID = *it.Id
		}
		if it.Check != nil {
			c.Check = &task.CriterionCheck{Command: it.Check.Command, ExpectedExitCode: it.Check.ExpectedExitCode}
		}
		out = append(out, c)
	}
	return out
}

// planListResponse builds gen.PlanListResponse from internal plans via a JSON
// round-trip through gen.Plan. oapi-codegen generates a SEPARATE anonymous
// element type for PlanListResponse.Plans (e.g. PlanListResponsePlansState,
// distinct from PlanState even though both alias string) even though the
// schema is the identical Plan.yaml $ref — hand-duplicating toWirePlan's full
// field-by-field mapping a second time for that element type would be a
// ~150-line near-exact copy with no behavioral difference. Both types are
// generated from the exact same OpenAPI schema, so they are JSON-shape
// identical by construction; round-tripping through encoding/json is the
// correct, minimal way to reconcile two nominally-distinct-but-structurally-
// identical generated types without a parallel hand-written struct
// (Constraint #8 governs cross-boundary WIRE types — this conversion touches
// only generated types on both ends).
func planListResponse(wire []gen.Plan) (gen.PlanListResponse, error) {
	data, err := json.Marshal(struct {
		Plans []gen.Plan `json:"plans"`
		Total int        `json:"total"`
	}{Plans: wire, Total: len(wire)})
	if err != nil {
		return gen.PlanListResponse{}, fmt.Errorf("plan list response: marshal: %w", err)
	}
	var resp gen.PlanListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return gen.PlanListResponse{}, fmt.Errorf("plan list response: unmarshal: %w", err)
	}
	return resp, nil
}

// --- handlers: GET/POST /workspaces/{id}/plans (dispatched from HandleWorkspaces) ---

// handleWorkspacePlansList handles GET /api/v1/workspaces/{id}/plans.
func (a *restAPI) handleWorkspacePlansList(w http.ResponseWriter, workspaceID string) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if a.planStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan store is not available")
		return
	}
	if _, wsErr := readWorkspaceFile(a.homePath, workspaceID); wsErr != nil {
		if errors.Is(wsErr, errWorkspaceNotFound) {
			jsonErr(w, http.StatusNotFound, "workspace not found")
			return
		}
		jsonErr(w, http.StatusInternalServerError, "failed to validate workspace")
		return
	}
	plans, err := a.planStore.List(plan.Filter{WorkspaceID: workspaceID})
	if err != nil {
		slog.Error("rest: plan list failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list plans")
		return
	}
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].CreatedAt > plans[j].CreatedAt })
	wire := make([]gen.Plan, 0, len(plans))
	for _, p := range plans {
		wire = append(wire, a.toWirePlan(p))
	}
	resp, err := planListResponse(wire)
	if err != nil {
		slog.Error("rest: plan list response build failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not build plan list response")
		return
	}
	jsonOK(w, resp)
}

// handleWorkspacePlanCreate handles POST /api/v1/workspaces/{id}/plans → 201 Created.
func (a *restAPI) handleWorkspacePlanCreate(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if a.planStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan store is not available")
		return
	}
	if _, wsErr := readWorkspaceFile(a.homePath, workspaceID); wsErr != nil {
		if errors.Is(wsErr, errWorkspaceNotFound) {
			jsonErr(w, http.StatusNotFound, "workspace not found")
			return
		}
		jsonErr(w, http.StatusInternalServerError, "failed to validate workspace")
		return
	}

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.PlanCreateRequest
	if !decodeAndValidate(w, r, "PlanCreateRequest", &req, validateEnabled) {
		return
	}
	if req.WorkspaceId != "" && req.WorkspaceId != workspaceID {
		jsonErr(w, http.StatusBadRequest, "workspace_id in body must match the workspace in the URL path")
		return
	}
	if err := validateEntityID(req.OwnerAgentId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid owner_agent_id")
		return
	}

	c := a.callerIdentity(r)
	p := &plan.Plan{
		WorkspaceID:  workspaceID,
		Title:        req.Title,
		OwnerAgentID: req.OwnerAgentId,
		Owner:        c.Username,
		CreatedBy:    c.Username,
	}
	if req.Goal != nil {
		p.Goal = *req.Goal
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if req.Dod != nil {
		p.DoD = planDoDFromCreateWire(*req.Dod)
	}
	if req.Bounds != nil {
		b := &plan.PlanBounds{}
		if req.Bounds.IdleExpiryDays != nil {
			v := *req.Bounds.IdleExpiryDays
			b.IdleExpiryDays = &v
		}
		if req.Bounds.PlanJudgeMaxRounds != nil {
			v := *req.Bounds.PlanJudgeMaxRounds
			b.PlanJudgeMaxRounds = &v
		}
		p.Bounds = b
	}

	if err := a.planStore.Create(p); err != nil {
		if isPlanValidationErr(err) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: plan create failed", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not create plan")
		return
	}
	a.auditPlan("plan.create", p.ID)
	jsonCreated(w, a.toWirePlan(*p))
}

// --- handlers: /api/v1/plans/{id}[/approve|/stop] ---------------------------

// HandlePlans dispatches GET/PUT/DELETE /api/v1/plans/{id} and
// POST /api/v1/plans/{id}/approve|stop.
func (a *restAPI) HandlePlans(w http.ResponseWriter, r *http.Request) {
	if a.planStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan store is not available")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/plans")
	rest = strings.TrimPrefix(rest, "/")

	if rest == "" {
		// Bare /plans has no GET/POST — creation and workspace-scoped listing
		// live at /workspaces/{id}/plans (dispatched via HandleWorkspaces).
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		id := rest[:idx]
		sub := rest[idx+1:]
		switch sub {
		case "approve":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handlePlanApprove(w, id)
		case "stop":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handlePlanStop(w, id)
		default:
			http.NotFound(w, r)
		}
		return
	}

	id := rest
	switch r.Method {
	case http.MethodGet:
		a.handlePlanGet(w, id)
	case http.MethodPut:
		a.handlePlanPut(w, r, id)
	case http.MethodDelete:
		a.handlePlanDelete(w, id)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handlePlanGet handles GET /api/v1/plans/{id}.
func (a *restAPI) handlePlanGet(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	p, err := a.planStore.Get(id)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "plan not found")
			return
		}
		slog.Error("rest: plan get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read plan")
		return
	}
	jsonOK(w, a.toWirePlan(*p))
}

// handlePlanPut handles PUT /api/v1/plans/{id}. Plain field/state-transition
// update — no DoD/criteria gating (use POST /plans/{id}/approve for the
// gated draft->approved transition).
func (a *restAPI) handlePlanPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.PlanUpdateRequest
	if !decodeAndValidate(w, r, "PlanUpdateRequest", &req, validateEnabled) {
		return
	}

	patch := plan.Patch{}
	if req.Title != nil {
		patch.Title = req.Title
	}
	if req.Goal != nil {
		patch.Goal = req.Goal
	}
	if req.Description != nil {
		patch.Description = req.Description
	}
	if req.OwnerAgentId != nil {
		if err := validateEntityID(*req.OwnerAgentId); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid owner_agent_id")
			return
		}
		patch.OwnerAgentID = req.OwnerAgentId
	}
	if req.Dod != nil {
		dod := planDoDFromUpdateWire(*req.Dod)
		patch.DoD = &dod
	}
	if req.Bounds != nil {
		b := &plan.PlanBounds{}
		if req.Bounds.IdleExpiryDays != nil {
			v := *req.Bounds.IdleExpiryDays
			b.IdleExpiryDays = &v
		}
		if req.Bounds.PlanJudgeMaxRounds != nil {
			v := *req.Bounds.PlanJudgeMaxRounds
			b.PlanJudgeMaxRounds = &v
		}
		patch.Bounds = &b
	}
	if req.State != nil {
		st := plan.State(*req.State)
		patch.State = &st
	}

	updated, err := a.planStore.Update(id, patch)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "plan not found")
			return
		}
		if isPlanValidationErr(err) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: plan update failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not update plan")
		return
	}
	a.auditPlan("plan.update", id)
	jsonOK(w, a.toWirePlan(*updated))
}

// handlePlanDelete handles DELETE /api/v1/plans/{id} → 204. A running plan
// cannot be deleted (plan.Store.Delete rejects it, mapped to 409 here); a
// non-running delete clears plan_id on member tasks (best-effort, SD-A5,
// mirrors the removed clearMilestoneOnTasks).
func (a *restAPI) handlePlanDelete(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	if err := a.planStore.Delete(id); err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "plan not found")
			return
		}
		if isPlanValidationErr(err) {
			// plan.Store.Delete's only validation error is "cannot delete a
			// running plan" — 409 Conflict per the contract (mirrors the
			// task-delete cascade's error mapping conventions).
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		slog.Error("rest: plan delete failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not delete plan")
		return
	}

	// SD-A5: clear plan_id on this plan's (former) member tasks. Best-effort
	// striped-lock RMW per task, mirroring clearMilestoneOnTasks
	// (rest_milestones.go, removed) — pkg/plan deliberately holds no task-store
	// handle (see plan.Store.Delete's doc comment), so this is the REST layer's
	// job.
	if a.taskStore != nil {
		members, lerr := a.taskStore.List(task.Filter{PlanID: id})
		if lerr != nil {
			slog.Warn("rest: plan delete: list former member tasks failed", "id", id, "error", lerr)
		} else {
			empty := ""
			for _, t := range members {
				if _, uerr := a.taskStore.Update(t.ID, task.Patch{PlanID: &empty}); uerr != nil {
					slog.Warn("rest: plan delete: clear plan_id on member task failed",
						"plan_id", id, "task_id", t.ID, "error", uerr)
				}
			}
		}
	}

	a.auditPlan("plan.delete", id)
	w.WriteHeader(http.StatusNoContent)
}

// handlePlanApprove handles POST /api/v1/plans/{id}/approve. Runs the tiered
// DoD gate (SD-A7: strict for agent-authored plans, soft for human/UI-
// authored) plus the unconditional member-task-criteria gate (FR-084), then
// transitions draft->approved. The single plan-engine instance auto-advances
// approved->running on its next tick.
func (a *restAPI) handlePlanApprove(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	p, err := a.planStore.Get(id)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "plan not found")
			return
		}
		slog.Error("rest: plan approve: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read plan")
		return
	}
	if p.State != plan.StateDraft {
		writeJSON(w, http.StatusBadRequest, gen.PlanApproveError{
			Error: ptr(fmt.Sprintf("plan is %q; only a draft plan can be approved", p.State)),
		})
		return
	}

	// SD-A7 tiered DoD gate.
	if a.isAgentID(p.CreatedBy) && len(p.DoD) == 0 {
		writeJSON(w, http.StatusBadRequest, gen.PlanApproveError{
			Error: ptr("plan requires a Definition of Done before approval (agent-authored plan)"),
		})
		return
	}

	// FR-084 (unconditional, all tiers): every member task must carry >=1 criterion.
	if a.taskStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "task store is not available")
		return
	}
	members, lerr := a.taskStore.List(task.Filter{PlanID: id})
	if lerr != nil {
		slog.Error("rest: plan approve: list member tasks failed", "id", id, "error", lerr)
		jsonErr(w, http.StatusInternalServerError, "could not list member tasks")
		return
	}
	var offenders []struct {
		Reason string `json:"reason"`
		TaskId string `json:"task_id"`
		Title  string `json:"title"`
	}
	for _, t := range members {
		if len(t.Criteria) == 0 {
			offenders = append(offenders, struct {
				Reason string `json:"reason"`
				TaskId string `json:"task_id"`
				Title  string `json:"title"`
			}{Reason: "task has no acceptance criteria", TaskId: t.ID, Title: t.Title})
		}
	}
	if len(offenders) > 0 {
		writeJSON(w, http.StatusBadRequest, gen.PlanApproveError{TaskErrors: &offenders})
		return
	}

	approved := plan.StateApproved
	updated, uerr := a.planStore.Update(id, plan.Patch{State: &approved})
	if uerr != nil {
		if isPlanValidationErr(uerr) {
			writeJSON(w, http.StatusBadRequest, gen.PlanApproveError{Error: ptr(uerr.Error())})
			return
		}
		slog.Error("rest: plan approve: update failed", "id", id, "error", uerr)
		jsonErr(w, http.StatusInternalServerError, "could not approve plan")
		return
	}
	a.auditPlan("plan.approve", id)
	jsonOK(w, a.toWirePlan(*updated))
}

// handlePlanStop handles POST /api/v1/plans/{id}/stop. Transitions a running
// plan to failed(stopped_by_user); rejected 400 when not currently running.
func (a *restAPI) handlePlanStop(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	p, err := a.planStore.Get(id)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "plan not found")
			return
		}
		slog.Error("rest: plan stop: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read plan")
		return
	}
	if p.State != plan.StateRunning {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("plan is %q; only a running plan can be stopped", p.State))
		return
	}

	failed := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	updated, uerr := a.planStore.Update(id, plan.Patch{State: &failed, FailedReason: &reason})
	if uerr != nil {
		if isPlanValidationErr(uerr) {
			jsonErr(w, http.StatusBadRequest, uerr.Error())
			return
		}
		slog.Error("rest: plan stop: update failed", "id", id, "error", uerr)
		jsonErr(w, http.StatusInternalServerError, "could not stop plan")
		return
	}
	a.auditPlan("plan.stop", id)
	jsonOK(w, a.toWirePlan(*updated))
}
