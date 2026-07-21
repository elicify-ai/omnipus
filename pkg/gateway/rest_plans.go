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
//	GET/PUT/DELETE /plans/{id}        get/update/delete (PUT never sets state,
//	                                  ADR-052 FR-007/A1 — every plan state
//	                                  transition goes through a dedicated
//	                                  endpoint below)
//	POST /plans/{id}/approve          tiered-DoD + unconditional member-
//	                                  criteria gated draft->approved
//	POST /plans/{id}/stop             running->failed(stopped_by_user);
//	                                  delegates to PlanEngine.StopPlan
//	                                  (ADR-052 FR-009/010)
//	POST /plans/{id}/restart          failed(stopped_by_user)->approved,
//	                                  resets non-done members (ADR-052
//	                                  FR-016/017/026)
//
// The GET/POST /workspaces/{id}/plans paths are dispatched from
// HandleWorkspaces (rest_workspaces.go) via a "/plans" suffix branch, exactly
// mirroring the existing "/delegation" and "/instructions" branches. All
// mutating writes go through a.planStore (pkg/plan.Store), whose OnChange
// hook (wired at boot in gateway.go) emits the plan_status WS frame for every
// successful Create/Update — this file never emits frames directly.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// validatePlanOwnerAgent rejects a plan owner_agent_id that is not a
// registered agent, or that IS registered but is a System Agent or a worker
// (review r1 m1: the prior format-only validateEntityID check let a
// typo'd/nonexistent id, or a non-chat-target agent that can never actually
// be woken at a plan decision point, through unrejected). OwnerAgentID's own
// doc comment on plan.Plan ("the agent woken at plan decision points, ADR
// D4") requires a real, addressable agent — mirrors
// AgentConfig.IsChatTarget's exact "not worker, not system" rule (the same
// rule routing/default-agent resolution already uses to exclude
// non-addressable agent kinds).
func validatePlanOwnerAgent(cfg *config.Config, ownerAgentID string) error {
	if cfg == nil {
		return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != ownerAgentID {
			continue
		}
		if !cfg.Agents.List[i].IsChatTarget() {
			return fmt.Errorf(
				"owner_agent_id %q is a System Agent or worker and cannot own a plan", ownerAgentID)
		}
		return nil
	}
	return fmt.Errorf("owner_agent_id %q is not a registered agent", ownerAgentID)
}

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
	Behavior *struct {
		MaxCount *int                      `json:"max_count,omitempty"`
		MinCount *int                      `json:"min_count,omitempty"`
		Scope    *gen.PlanDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                    `json:"tool"`
	} `json:"behavior,omitempty"`
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
		Behavior *struct {
			MaxCount *int                      `json:"max_count,omitempty"`
			MinCount *int                      `json:"min_count,omitempty"`
			Scope    *gen.PlanDodBehaviorScope `json:"scope,omitempty"`
			Tool     string                    `json:"tool"`
		} `json:"behavior,omitempty"`
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
			Behavior *struct {
				MaxCount *int                      `json:"max_count,omitempty"`
				MinCount *int                      `json:"min_count,omitempty"`
				Scope    *gen.PlanDodBehaviorScope `json:"scope,omitempty"`
				Tool     string                    `json:"tool"`
			} `json:"behavior,omitempty"`
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
		if c.Behavior != nil {
			beh := &struct { // not-wire-format: intermediate value built to match gen.Plan.Dod's oapi-codegen anonymous element type, not a parallel wire type
				MaxCount *int                      `json:"max_count,omitempty"`
				MinCount *int                      `json:"min_count,omitempty"`
				Scope    *gen.PlanDodBehaviorScope `json:"scope,omitempty"`
				Tool     string                    `json:"tool"`
			}{
				// MinCount/MaxCount are passed straight through — both are
				// already *int on task.CriterionBehavior (fix-wave finding
				// #5), so no ptr()-wrap is needed (or type-correct: wrapping
				// an already-*int value would produce **int). This also
				// preserves the nil/0 distinction on read: an
				// explicitly-zero MinCount round-trips as 0, not defaulted —
				// the wire's own `default: 1` (schema) is authoritative only
				// for an ABSENT create/update request field, never for what
				// GET echoes back (fix-wave finding #6).
				Tool:     c.Behavior.Tool,
				MinCount: c.Behavior.MinCount,
				MaxCount: c.Behavior.MaxCount,
			}
			if c.Behavior.Scope != "" {
				s := gen.PlanDodBehaviorScope(c.Behavior.Scope)
				beh.Scope = &s
			}
			item.Behavior = beh
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
	Behavior *struct {
		MaxCount *int                                   `json:"max_count,omitempty"`
		MinCount *int                                   `json:"min_count,omitempty"`
		Scope    *gen.PlanCreateRequestDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                                 `json:"tool"`
	} `json:"behavior,omitempty"`
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
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
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
	Behavior *struct {
		MaxCount *int                                   `json:"max_count,omitempty"`
		MinCount *int                                   `json:"min_count,omitempty"`
		Scope    *gen.PlanUpdateRequestDodBehaviorScope `json:"scope,omitempty"`
		Tool     string                                 `json:"tool"`
	} `json:"behavior,omitempty"`
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
		if it.Behavior != nil {
			c.Behavior = behaviorFromWire(it.Behavior.Tool, it.Behavior.MinCount, it.Behavior.MaxCount, it.Behavior.Scope)
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
	if err := validatePlanOwnerAgent(a.agentLoop.GetConfig(), req.OwnerAgentId); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
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
			a.handlePlanStop(w, r, id)
		case "restart":
			if r.Method != http.MethodPost {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.handlePlanRestart(w, id)
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

// handlePlanPut handles PUT /api/v1/plans/{id}. Plain field update — NEVER a
// state transition (ADR-052 FR-007/US-5, A1): every plan state change goes
// through a dedicated endpoint (POST /approve, POST /stop, POST /restart, or
// the engine's own cap-gated promotion). `[FACT]` before this guard, PUT set
// patch.State directly and skipped BOTH the FR-084 criteria gate (only
// enforced in handlePlanApprove) and the engine's cap admission (Admit lives
// only in tryStartApprovedPlan) — a client could `PUT {"state":"running"}`
// and bypass both safety nets (ADR-052 G2/§3). gen.PlanUpdateRequest still
// carries a State field on the wire (existing generated type, unchanged by
// this feature — Constraint #8 does not require touching it since the fix is
// behavioral, not shape-level), so the guard cannot rely on Go's decode
// silently dropping an unrecognized field the way the sandbox_profile/
// delegation_policy precedent (rest.go's PUT /agents/{id}) does; it must
// reject the presence of ANY "state" key at the raw-JSON level instead,
// mirroring that same raw-body-sniff pattern (ADR-035 precedent) one level
// up: a present-but-absent-effect field is still a request that named a
// forbidden transition and must 400, not silently no-op.
func (a *restAPI) handlePlanPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}

	rawBody, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if readErr != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	if bytes.Contains(rawBody, []byte(`"state"`)) {
		jsonErr(w, http.StatusBadRequest,
			`state cannot be set via PUT — use POST /plans/{id}/approve, POST /plans/{id}/stop, or `+
				`POST /plans/{id}/restart; every plan state transition goes through a dedicated endpoint`)
		return
	}

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.PlanUpdateRequest
	if !decodeAndValidate(w, r, "PlanUpdateRequest", &req, validateEnabled) {
		return
	}

	// review r1 m6: DoD and owner_agent_id are frozen once the plan has left
	// draft. DoD is the contract the plan judge adjudicates against — letting
	// it change mid-flight (approved/running/done/failed) could invalidate
	// in-progress or already-recorded judge rounds retroactively. Owner is
	// who gets woken at plan decision points (Plan.OwnerAgentID's own doc
	// comment) — reassigning it mid-flight would silently redirect wake
	// notifications away from whoever is actually watching this plan run.
	// Bounds is deliberately NOT frozen here: an operator may legitimately
	// want to extend a running plan's idle-expiry/judge-round budget.
	if req.Dod != nil || req.OwnerAgentId != nil {
		existing, gerr := a.planStore.Get(id)
		if gerr != nil {
			if errors.Is(gerr, plan.ErrNotFound) {
				jsonErr(w, http.StatusNotFound, "plan not found")
				return
			}
			slog.Error("rest: plan get failed (pre-update state check)", "id", id, "error", gerr)
			jsonErr(w, http.StatusInternalServerError, "could not read plan")
			return
		}
		if existing.State != plan.StateDraft {
			if req.Dod != nil {
				jsonErr(w, http.StatusConflict, "dod cannot be changed once the plan has left draft state")
				return
			}
			jsonErr(w, http.StatusConflict, "owner_agent_id cannot be changed once the plan has left draft state")
			return
		}
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
		if err := validatePlanOwnerAgent(a.agentLoop.GetConfig(), *req.OwnerAgentId); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
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
	// req.State is intentionally never read here (FR-007/A1): the raw-body
	// sniff above already 400s any request whose body contains a "state" key,
	// so this decoded field is unreachable non-nil at this point — patch.State
	// stays permanently unset on the PUT path.

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

// handlePlanStop handles POST /api/v1/plans/{id}/stop (ADR-052 US-6/FR-009/
// FR-010, spec DS-4). Delegates the actual fan-out to PlanEngine.StopPlan,
// which — under planDecisionMu, so a concurrently-dispatched member cannot
// escape (grill F3) — issues RequestCancelForSession (the SAME chat cancel
// every other surface uses, A2) over {each in_progress member session} +
// {each registered verifier session, member- and plan-level}, marks every
// in_progress member failed+cancelled, and transitions the plan itself to
// failed(stopped_by_user). This handler no longer touches a.planStore
// directly — the precondition check (only a running plan can be stopped) and
// the state write both live in the engine now.
func (a *restAPI) handlePlanStop(w http.ResponseWriter, r *http.Request, id string) {
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

	pe := agent.GetPlanEngine(a.agentLoop)
	if pe == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan engine is not available")
		return
	}
	c := a.callerIdentity(r)
	updated, serr := pe.StopPlan(r.Context(), id, c.Username, "system")
	if serr != nil {
		if updated == nil {
			if errors.Is(serr, plan.ErrNotFound) {
				jsonErr(w, http.StatusNotFound, "plan not found")
				return
			}
			slog.Error("rest: plan stop: engine stop failed", "id", id, "error", serr)
			jsonErr(w, http.StatusInternalServerError, "could not stop plan")
			return
		}
		// Partial fan-out failure (ADR-052 §6.4 Item 5 / aggregateMemberCancelErrors):
		// the plan itself DID transition to failed(stopped_by_user) — updated
		// reflects that, and the audit entry below records it happened — but
		// >=1 member task's own cancel-write failed and may remain
		// in_progress. Map this honestly as a server error rather than an
		// unqualified 200: the caller must know to re-check member state
		// instead of assuming a fully clean stop.
		slog.Error("rest: plan stop: partial fan-out failure", "id", id, "error", serr)
		a.auditPlan("plan.stop", id)
		jsonErr(w, http.StatusInternalServerError, serr.Error())
		return
	}
	a.auditPlan("plan.stop", id)
	jsonOK(w, a.toWirePlan(*updated))
}

// handlePlanRestart handles POST /api/v1/plans/{id}/restart (ADR-052 §6.7,
// FR-016/017/026, US-9 — the ▶ Play route). Resets every non-done member
// task via task.Store.RestartReset — any-reason failed->next un-freeze
// (FR-016/DS-5: a genuinely-failed member inside an otherwise user-cancelled
// plan is reset too, "re-run all its non-done members") — then transitions
// the plan itself failed[stopped_by_user] -> approved via the store-level
// reason-aware guard (plan.ValidateRestartTransition / plan.Store.Update's
// restarting branch), NEVER straight to running: the engine promotes
// approved->running under the global cap on its next tick, exactly like a
// first execute (restarting straight to running would skip cap admission,
// the same hole class the PUT-lockdown, FR-007, closed for approve).
// Store.Update's restart branch also clears FailedReason and resets
// JudgeRounds to 0 (A4) — this handler does not set either directly.
//
// Member resets happen BEFORE the plan's own state write: while the plan is
// still `failed`, the engine's tick loop and event loop both ignore it
// (they only act on `approved`/`running`), so there is no lock-free race
// with dispatch here the way Stop has (grill F3 does not apply — restart's
// own state write is the single point where the engine can react, and it
// runs last).
func (a *restAPI) handlePlanRestart(w http.ResponseWriter, id string) {
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
		slog.Error("rest: plan restart: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read plan")
		return
	}
	// Fail fast with a specific 409 before touching any member task.
	// plan.Store.Update re-validates against the FRESH on-disk state at write
	// time regardless (fix-wave finding #4's onDiskFailedReason capture), so
	// this early check is a targeted error message, not the sole guard.
	if rerr := plan.ValidateRestartTransition(p.State, p.FailedReason); rerr != nil {
		jsonErr(w, http.StatusConflict, rerr.Error())
		return
	}

	if a.taskStore == nil {
		jsonErr(w, http.StatusServiceUnavailable, "task store is not available")
		return
	}
	members, lerr := a.taskStore.List(task.Filter{PlanID: id})
	if lerr != nil {
		slog.Error("rest: plan restart: list member tasks failed", "id", id, "error", lerr)
		jsonErr(w, http.StatusInternalServerError, "could not list member tasks")
		return
	}
	var resetFailures []string
	for i := range members {
		m := &members[i]
		if m.Status != task.StatusFailed {
			// done members are preserved untouched (FR-017); next/blocked
			// members never ran and are already sitting in a fresh
			// pre-run state — RestartReset only ever applies to `failed`
			// (ErrNotRestartable otherwise), so only failed members need it.
			continue
		}
		reset, rrerr := a.taskStore.RestartReset(m.ID)
		if rrerr != nil {
			slog.Error("rest: plan restart: member reset failed", "plan_id", id, "task_id", m.ID, "error", rrerr)
			resetFailures = append(resetFailures, m.ID)
			continue
		}
		a.emitTaskStatus(reset)
		if a.agentLoop != nil {
			a.agentLoop.NotifyTaskUpserted(reset)
		}
	}
	if len(resetFailures) > 0 {
		slog.Error("rest: plan restart: some member resets failed; proceeding with plan transition anyway",
			"plan_id", id, "task_ids", resetFailures)
	}

	approved := plan.StateApproved
	updated, uerr := a.planStore.Update(id, plan.Patch{State: &approved})
	if uerr != nil {
		if errors.Is(uerr, plan.ErrRestartNotPermitted) {
			jsonErr(w, http.StatusConflict, uerr.Error())
			return
		}
		if isPlanValidationErr(uerr) {
			jsonErr(w, http.StatusBadRequest, uerr.Error())
			return
		}
		slog.Error("rest: plan restart: update failed", "id", id, "error", uerr)
		jsonErr(w, http.StatusInternalServerError, "could not restart plan")
		return
	}
	a.auditPlan("plan.restart", id)
	jsonOK(w, a.toWirePlan(*updated))
}
