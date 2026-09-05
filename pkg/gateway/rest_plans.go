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
	"encoding/json"
	"errors"
	"fmt"
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
// Optional trailing key/value pairs are merged into Details so callers can
// surface restart metadata (new_generation, still_failed_members, …) without
// inventing a parallel audit path.
func (a *restAPI) auditPlan(event, id string, kvs ...any) {
	if a.auditor == nil {
		return
	}
	details := map[string]any{"id": id}
	for i := 0; i+1 < len(kvs); i += 2 {
		k, ok := kvs[i].(string)
		if !ok {
			continue
		}
		details[k] = kvs[i+1]
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: audit.DecisionAllow,
		Details:  details,
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

// taskSnapshotLister implements plan.TaskLister over an already-fetched batch
// of tasks, filtering it in memory via task.Filter.Matches instead of issuing
// a fresh Store.List disk scan. handleWorkspacePlansList builds one of these
// ONCE per request (a single task.Store.List call) and passes it to every
// toWirePlan call in its list loop, so plan.ComputeProgress no longer scans
// the whole task store once PER PLAN — fix-wave finding #1b (mirrors
// rest_tasks.go's rollupIndex fix for the equivalent per-task N+1).
type taskSnapshotLister struct {
	tasks []task.Task
}

// List implements plan.TaskLister by filtering the snapshot in memory. It
// never touches disk and never errors.
func (s taskSnapshotLister) List(filter task.Filter) ([]task.Task, error) {
	out := make([]task.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if filter.Matches(&t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// toWirePlan converts an internal plan.Plan to the generated wire type,
// server-computing `progress` read-time via plan.ComputeProgress (R4/C19,
// mirrors the removed milestone's computeMilestoneCounts pattern —
// rest_milestones.go:153, now deleted). lister is the plan.TaskLister used for
// that ComputeProgress call: pass a.taskStore directly for a single-plan
// response (unchanged behavior, one bounded List call), or a shared
// taskSnapshotLister for a batch/list-loop caller (see its doc comment).
func (a *restAPI) toWirePlan(p plan.Plan, lister plan.TaskLister) gen.Plan {
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
	if p.Rationale != "" {
		out.Rationale = ptr(p.Rationale)
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
			IdleExpiryDays                *int `json:"idle_expiry_days,omitempty"`
			PlanJudgeMaxRounds            *int `json:"plan_judge_max_rounds,omitempty"`
			SupervisionMaxAttempts        *int `json:"supervision_max_attempts,omitempty"`
			SupervisionTurnTimeoutSeconds *int `json:"supervision_turn_timeout_seconds,omitempty"`
		}{}
		if p.Bounds.IdleExpiryDays != nil {
			v := *p.Bounds.IdleExpiryDays
			b.IdleExpiryDays = &v
		}
		if p.Bounds.PlanJudgeMaxRounds != nil {
			v := *p.Bounds.PlanJudgeMaxRounds
			b.PlanJudgeMaxRounds = &v
		}
		if p.Bounds.SupervisionTurnTimeoutSeconds != nil {
			v := *p.Bounds.SupervisionTurnTimeoutSeconds
			b.SupervisionTurnTimeoutSeconds = &v
		}
		if p.Bounds.SupervisionMaxAttempts != nil {
			v := *p.Bounds.SupervisionMaxAttempts
			b.SupervisionMaxAttempts = &v
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
	// ADR-053 C1/INV-7 — the durable F2 round-burn gate. Empty until the plan
	// first parks at awaiting_supervision, and cleared again on a fresh
	// dispatch cycle, so absence is the normal state for most plans.
	if p.LastUnmetTerminalSignature != "" {
		out.LastUnmetTerminalSignature = ptr(p.LastUnmetTerminalSignature)
	}
	// ADR-053 m-3/FR-147 — the named plan<->owner-session linkage. Populated by
	// the owner loop that wraps plan execution; empty before then.
	if p.OwnerSessionID != "" {
		out.OwnerSessionId = ptr(p.OwnerSessionID)
	}
	// ADR-055/FR-012d — the plan's chat origin. BOTH ABSENT IS A LEGITIMATE,
	// EXPECTED STATE (a plan created over REST from the Plans UI has no chat
	// context at all), so each is emitted only when actually set: no synthetic
	// origin is minted here and no default is invented, exactly as the fields'
	// own doc comment on plan.Plan requires. They are server-set at creation
	// and immutable, so this read mapping is their only wire appearance.
	if p.SourceChannel != "" {
		out.SourceChannel = ptr(p.SourceChannel)
	}
	if p.SourceChatID != "" {
		out.SourceChatId = ptr(p.SourceChatID)
	}
	// ADR-055/FR-050 — durable PlanSupervisor adjudication state. Mirrors the
	// `bounds` sub-object mapping above: an intermediate value shaped to
	// oapi-codegen's anonymous field type, populated field by field.
	//
	// nil supervision means the plan has never entered the supervision-eligible
	// phase set, and the whole key is correctly absent. Once the object EXISTS,
	// the two counters are emitted UNCONDITIONALLY — including at zero —
	// because zero is a load-bearing value, not an empty one: FR-035 tells a
	// reader to disambiguate the two `judge_rounds_exhausted` causes by testing
	// `supervision.correction_rounds == 0`, which no client can do if the
	// server omits the field precisely when it is zero. The three string/time
	// members keep normal omitempty semantics — each has a real "not set yet"
	// state (never woken, last wake succeeded, no session minted).
	if p.Supervision != nil {
		s := struct { // not-wire-format: intermediate value shaped to match gen.Plan.Supervision's oapi-codegen anonymous field type, not a parallel wire type
			Attempts         *int       `json:"attempts,omitempty"`
			CorrectionRounds *int       `json:"correction_rounds,omitempty"`
			SessionId        *string    `json:"session_id,omitempty"`
			WakeAt           *time.Time `json:"wake_at,omitempty"`
			WakeError        *string    `json:"wake_error,omitempty"`
		}{
			Attempts:         ptr(p.Supervision.Attempts),
			CorrectionRounds: ptr(p.Supervision.CorrectionRounds),
		}
		if p.Supervision.SessionID != "" {
			s.SessionId = ptr(p.Supervision.SessionID)
		}
		if p.Supervision.WakeAt != "" {
			if ts, err := time.Parse(time.RFC3339, p.Supervision.WakeAt); err == nil {
				s.WakeAt = &ts
			} else {
				slog.Warn("rest: plan supervision.wake_at is not RFC3339; omitted from the wire payload",
					"plan_id", p.ID, "wake_at", p.Supervision.WakeAt, "error", err)
			}
		}
		// FR-024: an undelivered supervisor wake is recorded on the plan
		// precisely so it is OBSERVABLE. Dropping it here is what made a plan
		// burn its attempts and terminate failed(supervision_unavailable) with
		// the recorded cause readable only from ~/.omnipus/plans/<id>.json.
		if p.Supervision.WakeError != "" {
			s.WakeError = ptr(p.Supervision.WakeError)
		}
		out.Supervision = &s
	}
	// Resolve the effective lister explicitly rather than assigning
	// a.taskStore straight into the plan.TaskLister interface unconditionally:
	// a.taskStore is a concrete *task.Store, and a nil *task.Store boxed into
	// an interface value is a NON-nil interface (the classic Go typed-nil
	// trap) — so the nilness check must happen on the concrete pointer before
	// the assignment, not on the interface afterward.
	var effLister plan.TaskLister
	switch {
	case lister != nil:
		effLister = lister
	case a.taskStore != nil:
		effLister = a.taskStore
	}
	if effLister != nil {
		if _, _, progress, err := plan.ComputeProgress(p.ID, effLister); err == nil {
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
	Kind   *gen.PlanCreateRequestDodKind  `json:"kind,omitempty"`
	Status gen.PlanCreateRequestDodStatus `json:"status"`
	Text   string                         `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002): the gateway performs NO kind defaulting —
		// an absent kind passes THROUGH as empty and is inferred downstream by
		// the store's NormalizeCriteria.
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
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
	Kind   *gen.PlanUpdateRequestDodKind  `json:"kind,omitempty"`
	Status gen.PlanUpdateRequestDodStatus `json:"status"`
	Text   string                         `json:"text"`
}) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, 0, len(items))
	for _, it := range items {
		c := task.AcceptanceCriterion{
			Text:   it.Text,
			Status: task.CriterionStatus(it.Status),
			Author: task.CriterionAuthor{Kind: string(it.Author.Kind), ID: it.Author.Id},
		}
		// ADR-074 D2 (spec FR-002): the gateway performs NO kind defaulting —
		// an absent kind passes THROUGH as empty and is inferred downstream by
		// the store's NormalizeCriteria.
		if it.Kind != nil {
			c.Kind = task.CriterionKind(*it.Kind)
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

	// Fix-wave finding #1b: fetch every task in this workspace ONCE and reuse
	// it as a shared plan.TaskLister for every plan below, instead of letting
	// each toWirePlan -> plan.ComputeProgress call trigger its own full
	// task.Store.List scan (previously O(n) List calls for n plans, each an
	// O(m) scan of the whole task store). Scoping the snapshot to
	// workspaceID — rather than every task in the store — is safe: a task can
	// only carry a plan_id belonging to its OWN workspace
	// (validateTaskPlanID/ValidatePlanWorkspace enforce that FK), so no task
	// outside this workspace could ever match one of these plans' IDs anyway.
	var lister plan.TaskLister
	if a.taskStore != nil {
		snapshotTasks, sErr := a.taskStore.List(task.Filter{WorkspaceID: workspaceID})
		if sErr != nil {
			// Non-fatal, mirrors the original per-plan resilience: a task
			// snapshot failure here just means progress falls back to being
			// computed per-plan (toWirePlan(p, nil) below uses a.taskStore
			// directly), each of which logs its own Warn and omits progress
			// rather than failing the whole plan list.
			slog.Warn("rest: plan list: task snapshot fetch failed; progress will be computed per-plan instead",
				"workspace_id", workspaceID, "error", sErr)
		} else {
			lister = taskSnapshotLister{tasks: snapshotTasks}
		}
	}
	wire := make([]gen.Plan, 0, len(plans))
	for _, p := range plans {
		wire = append(wire, a.toWirePlan(p, lister))
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

	// Title whitespace trimming is enforced in plan.Store.Create itself
	// (plan.go's normalize(), S2 UAT finding B) so every caller — REST, the
	// plan engine, agent tools — gets the same behavior; no handler-level
	// trim needed here.
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
	if req.Rationale != nil {
		p.Rationale = *req.Rationale
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
		if req.Bounds.SupervisionTurnTimeoutSeconds != nil {
			v := *req.Bounds.SupervisionTurnTimeoutSeconds
			b.SupervisionTurnTimeoutSeconds = &v
		}
		if req.Bounds.SupervisionMaxAttempts != nil {
			v := *req.Bounds.SupervisionMaxAttempts
			b.SupervisionMaxAttempts = &v
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
	jsonCreated(w, a.toWirePlan(*p, nil))
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
			a.handlePlanRestart(w, r, id)
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
	jsonOK(w, a.toWirePlan(*p, nil))
}

// handlePlanPut handles PUT /api/v1/plans/{id}. Plain field update — NEVER a
// state transition (ADR-052 FR-007/US-5, A1): every plan state change goes
// through a dedicated endpoint (POST /approve, POST /stop, POST /restart, or
// the engine's own cap-gated promotion). Before this guard existed, PUT set
// patch.State directly and skipped BOTH the FR-084 criteria gate (only
// enforced in handlePlanApprove) and the engine's cap admission (Admit lives
// only in tryStartApprovedPlan) — a client could `PUT {"state":"running"}`
// and bypass both safety nets (ADR-052 G2/§3). gen.PlanUpdateRequest still
// carries a State field on the wire (existing generated type, unchanged by
// this feature — Constraint #8 does not require touching it since the fix is
// behavioral, not shape-level). UNLIKE the sandbox_profile/delegation_policy
// precedent (rest.go's PUT /agents/{id}), where the forbidden field is
// entirely ABSENT from the generated struct and a raw-body sniff is the only
// way to detect it, PlanUpdateRequest.State is a `*PlanUpdateRequestState`
// pointer field — its presence is directly observable post-decode via
// `req.State != nil`, with no raw-body sniff needed. (An earlier version of
// this guard raw-body-sniffed for a literal `"state"` byte sequence instead;
// that both over-rejected any field VALUE equal to "state" — e.g.
// {"title":"state"} — and missed a unicode-escaped key, e.g.
// {"\u0073tate":"running"}, which decodes to the same "state" key Go's
// own json.Unmarshal recognizes. The post-decode pointer check below has neither
// failure mode.)
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
	if req.State != nil {
		jsonErr(w, http.StatusBadRequest,
			`state cannot be set via PUT — use POST /plans/{id}/approve, POST /plans/{id}/stop, or `+
				`POST /plans/{id}/restart; every plan state transition goes through a dedicated endpoint`)
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
	//
	// The stored plan is loaded once here and reused by BOTH the freeze check
	// below and the bounds merge further down, so a PUT touching dod/owner and
	// bounds together still costs a single read. Store.Get loads fresh from
	// disk on every call, so nothing below aliases store-owned memory.
	var existing *plan.Plan
	if req.Dod != nil || req.OwnerAgentId != nil || req.Bounds != nil {
		var gerr error
		existing, gerr = a.planStore.Get(id)
		if gerr != nil {
			if errors.Is(gerr, plan.ErrNotFound) {
				jsonErr(w, http.StatusNotFound, "plan not found")
				return
			}
			slog.Error("rest: plan get failed (pre-update state check)", "id", id, "error", gerr)
			jsonErr(w, http.StatusInternalServerError, "could not read plan")
			return
		}
	}
	if req.Dod != nil || req.OwnerAgentId != nil {
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
		// Title whitespace trimming is enforced in plan.Store.Update itself
		// (store.go's updateLocked, S2 UAT finding B) so every caller gets
		// the same behavior; no handler-level trim needed here.
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
	// Bounds MERGE, not replace (data-loss fix). plan.Patch.Bounds is applied
	// wholesale by the store (updateLocked does `p.Bounds = newBounds`), so a
	// handler that built the patch from the request alone silently zeroed every
	// bounds field the request did not mention. That is not a theoretical
	// partial-update purist objection: the shipped SPA plan-edit form
	// (src/components/workspaces/CreatePlanSlideOver.tsx, buildBounds) renders
	// inputs for plan_judge_max_rounds and idle_expiry_days ONLY and sends that
	// object on every save, so editing a plan's TITLE destroyed its
	// supervision_turn_timeout_seconds / supervision_max_attempts overrides.
	// Seeding from the stored bounds and overlaying only the fields actually
	// present in the request fixes it for every client, not just that form.
	//
	// Trade-off accepted (documented on PlanUpdateRequest.bounds): an
	// individual override can no longer be CLEARED AT ALL — not through this
	// handler and not through any other route, because there is no other
	// route. plan.Patch.Bounds has exactly ONE writer in the whole codebase
	// (the `patch.Bounds = &b` below); there is no PATCH endpoint, and no agent
	// tool sets it. So "cannot be cleared through PUT" must not be read as
	// implying some other endpoint can: an override, once set, can only be
	// OVERWRITTEN with a new value >= 1, never removed, by any caller.
	// (Clearing was never reachable from the SPA either — it omits `bounds`
	// entirely once every input is empty, which was and remains a no-op.)
	// Restoring clearability needs a deliberate wire change — e.g. an explicit
	// null-per-field or a `bounds: null` reset sentinel — not a second route.
	//
	// KNOWN, ACCEPTED: this read-modify-write straddles the store lock (the
	// Get above, the Update below), so two concurrent PUTs to the SAME plan
	// can lose one side's bounds field. Closing it properly means merging
	// inside updateLocked, which is pkg/plan's to make. The race needs two
	// simultaneous bounds-writing PUTs to one plan; the replace semantics it
	// replaces lost data on every single SPA save, with no concurrency at all.
	//
	// The `existing != nil` clause is defensive, not decorative: `existing` is
	// loaded ~55 lines above under `req.Dod != nil || req.OwnerAgentId != nil ||
	// req.Bounds != nil`, a strict superset of this condition, so it is
	// guaranteed non-nil here TODAY. Nothing enforces that coupling across
	// those 55 lines, though — dropping the `|| req.Bounds != nil` clause from
	// the load condition during some later refactor would turn every
	// bounds-carrying PUT into a nil-pointer panic on the very next line. The
	// extra check costs one comparison and removes that whole failure mode.
	if req.Bounds != nil && existing != nil {
		b := &plan.PlanBounds{}
		if existing.Bounds != nil {
			if existing.Bounds.IdleExpiryDays != nil {
				v := *existing.Bounds.IdleExpiryDays
				b.IdleExpiryDays = &v
			}
			if existing.Bounds.PlanJudgeMaxRounds != nil {
				v := *existing.Bounds.PlanJudgeMaxRounds
				b.PlanJudgeMaxRounds = &v
			}
			if existing.Bounds.SupervisionTurnTimeoutSeconds != nil {
				v := *existing.Bounds.SupervisionTurnTimeoutSeconds
				b.SupervisionTurnTimeoutSeconds = &v
			}
			if existing.Bounds.SupervisionMaxAttempts != nil {
				v := *existing.Bounds.SupervisionMaxAttempts
				b.SupervisionMaxAttempts = &v
			}
		}
		if req.Bounds.IdleExpiryDays != nil {
			v := *req.Bounds.IdleExpiryDays
			b.IdleExpiryDays = &v
		}
		if req.Bounds.PlanJudgeMaxRounds != nil {
			v := *req.Bounds.PlanJudgeMaxRounds
			b.PlanJudgeMaxRounds = &v
		}
		if req.Bounds.SupervisionTurnTimeoutSeconds != nil {
			v := *req.Bounds.SupervisionTurnTimeoutSeconds
			b.SupervisionTurnTimeoutSeconds = &v
		}
		if req.Bounds.SupervisionMaxAttempts != nil {
			v := *req.Bounds.SupervisionMaxAttempts
			b.SupervisionMaxAttempts = &v
		}
		patch.Bounds = &b
	}
	// req.State is intentionally never read here (FR-007/A1): the guard above
	// already 400s and returns whenever req.State != nil, so it is guaranteed
	// nil at this point — patch.State stays permanently unset on the PUT path.

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
	jsonOK(w, a.toWirePlan(*updated, nil))
}

// handlePlanDelete handles DELETE /api/v1/plans/{id} → 204. A running plan
// cannot be deleted (plan.Store.Delete rejects it, mapped to 409 here); a
// non-running delete clears plan_id on member tasks (best-effort, SD-A5,
// mirrors the removed clearMilestoneOnTasks).
//
// S1 release-blocker fix (UAT round 2): the cascade used to ONLY clear
// plan_id and leave everything else untouched. A member sitting in `next`
// (triaged but never individually approved — its only "approval" was ever
// the PLAN's own approve gate) would come out of the cascade standalone
// (PlanID=="") AND still `next`, which is exactly the state the ~60s
// heartbeat drain (TaskExecutor.CheckQueuedTasks, task.Filter{Status: next})
// auto-dispatches — laundering an unapproved/cancelled plan member into an
// autonomously-executed standalone task and bypassing the Execute approval
// gate entirely. requirePlanExecuting's plan-state gate (pkg/agent/
// task_executor.go) is correctly a no-op for a standalone task (PlanID=="")
// — that half is by design; the bug was handing a plan member that
// dispatchable, ungated resting state in the first place.
//
// Fix: a non-terminal member is detached AND reset to `inbox` (re-triage
// required — it must go through Next/Execute again as a standalone task, on
// its own merits, same as any other freshly-created task) in the SAME
// task.Patch/Update call that clears plan_id, so there is no intermediate
// standalone-and-next state for the drain to observe between two separate
// writes. `blocked` is a store-DERIVED side-state (recomputeBlockedStateLocked
// runs unconditionally at the end of every task.Store.Update) that can only
// be left via the store's own internal recompute hatch, never a public
// Status patch (validateTransition rejects blocked->anything from a non-
// internal caller) — a currently-blocked member's combined patch is rejected
// for that reason alone, not a real failure; detachMemberOnPlanDelete falls
// back to a plan_id-only patch for it and lets the store's own recompute
// resolve `blocked` correctly (to `next` once genuinely unblocked, otherwise
// staying `blocked` — either way never dispatchable, since CheckQueuedTasks
// only ever drains StatusNext). Terminal members (`done`, `failed`) are never
// resurrected or rewritten — `done` is frozen at the store level and a
// combined patch on either is rejected the same way, falling back to the
// plan_id-only clear that preserves their history untouched.
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

	// SD-A5 (+ S1 fix above): clear plan_id on this plan's (former) member
	// tasks, resetting any non-terminal, non-blocked member to `inbox` in the
	// same RMW cycle. Best-effort per task — pkg/plan deliberately holds no
	// task-store handle (see plan.Store.Delete's doc comment), so this is the
	// REST layer's job — but a failure here is never a dispatch-safety hole:
	// a member whose reset failed still carries the now-deleted plan's ID, and
	// every dispatch path (CheckQueuedTasks, requirePlanExecuting/
	// planForGate) fails CLOSED when a task's PlanID cannot be resolved to a
	// live plan (plan.ErrNotFound), so an orphaned reference can never
	// auto-execute — it just needs a manual re-triage/plan_id clear later.
	// 204 is still returned in that case: the plan itself IS gone (the
	// requested operation succeeded), the residual state is safe, and
	// failing the whole delete over a best-effort cleanup step would make the
	// plan appear permanently stuck for what is, at worst, a data-hygiene
	// follow-up — but every such failure is logged at Error (not Warn) since
	// it is exactly the class of leak this fix closes and must not go quiet.
	if a.taskStore != nil {
		members, lerr := a.taskStore.List(task.Filter{PlanID: id})
		if lerr != nil {
			slog.Error("rest: plan delete: list former member tasks failed; any next/in_progress member remains attached to the deleted plan_id (fails closed, cannot auto-dispatch, but needs manual re-triage)",
				"plan_id", id, "error", lerr)
		} else {
			for _, t := range members {
				a.detachMemberOnPlanDelete(id, t.ID)
			}
		}
	}

	a.auditPlan("plan.delete", id)
	w.WriteHeader(http.StatusNoContent)
}

// detachMemberOnPlanDelete clears taskID's plan_id and, unless the task is
// terminal (done/failed) or currently sitting in the derived `blocked`
// side-state, resets it to `inbox` — in ONE task.Patch/Update call, so the
// drain can never observe an intermediate standalone-and-next state (see
// handlePlanDelete's doc comment for the full S1 rationale). The combined
// patch is attempted first so the decision is made against the store's own
// FRESH on-disk status (validateTransition/recomputeBlockedStateLocked),
// never a possibly-stale snapshot from the List call above. If the store
// rejects the combined patch as an illegal transition — the only two ways
// that happens are leaving `done` (frozen) or leaving `blocked` other than
// via the store's internal recompute hatch — this falls back to a plan_id-
// only patch, which always succeeds for those cases and, for a `blocked`
// member, still runs recomputeBlockedStateLocked unconditionally (letting
// the store itself resolve `blocked` correctly rather than this handler
// hand-writing it). Any OTHER error (I/O, not-found — the task was deleted
// concurrently, etc.) is logged at Error: the member remains attached to the
// now-deleted plan_id, which fails closed everywhere dispatch is gated (see
// handlePlanDelete's doc comment) but does need a manual follow-up.
func (a *restAPI) detachMemberOnPlanDelete(planID, taskID string) {
	empty := ""
	inbox := task.StatusInbox
	_, err := a.taskStore.Update(taskID, task.Patch{PlanID: &empty, Status: &inbox})
	if err != nil && errors.Is(err, task.ErrIllegalTransition) {
		_, err = a.taskStore.Update(taskID, task.Patch{PlanID: &empty})
	}
	if err != nil {
		slog.Error("rest: plan delete: detach member task failed; task still references the deleted plan_id (fails closed, cannot auto-dispatch, but needs manual re-triage)",
			"plan_id", planID, "task_id", taskID, "error", err)
	}
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

	// FR-156/FR-159 (G-16, US-11): plan-lint — reject overlapping parallel
	// write_sets and join-less convergence points. Runs after the FR-084
	// criteria gate (same ordering as execute_plan's mirror of this
	// handler, pkg/tools/plan.go) and before the single gated state
	// transition below.
	if lerr := plan.Lint(p, members); lerr != nil {
		writeJSON(w, http.StatusBadRequest, gen.PlanApproveError{Error: ptr(lerr.Error())})
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
	jsonOK(w, a.toWirePlan(*updated, nil))
}

// handlePlanStop handles POST /api/v1/plans/{id}/stop (ADR-052 US-6/FR-009/
// FR-010, spec DS-4; approved-plan Stop per the spec's Edge Case "Stop
// wins"). Delegates the actual fan-out to PlanEngine.StopPlan, which — under
// planDecisionMu, so a concurrently-dispatched member cannot escape (grill
// F3) — issues RequestCancelForSession (the SAME chat cancel every other
// surface uses, A2) over {each in_progress member session} + {each
// registered verifier session, member- and plan-level}, marks every
// in_progress member failed+cancelled, and transitions the plan itself to
// failed(stopped_by_user). For a cap-queued `approved` plan that fan-out is
// naturally a no-op (nothing dispatched yet — Admit hasn't fired), so only
// the state write happens. The state check below is a targeted fail-fast
// error message, not the sole guard: plan.Store.Update re-validates against
// the FRESH on-disk state regardless, and the engine (StopPlan) re-checks
// its own precondition and owns the fan-out + the state write.
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
	if p.State != plan.StateRunning && p.State != plan.StateApproved {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("plan is %q; only a running or approved plan can be stopped", p.State))
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
			// TOCTOU (sign-off P1 finding #3): the precondition check above
			// passed against a.planStore.Get, but StopPlan re-reads the plan
			// under its own planDecisionMu and re-checks the SAME precondition
			// — a concurrent stop/restart/completion between those two reads
			// can flip it. plan_engine.go owns no exported sentinel for this
			// (and none is added here — that package belongs to another
			// agent), so distinguish the race from a genuine internal failure
			// by re-fetching: if the plan is no longer running/approved on
			// disk, this is that same precondition, just lost the race, and
			// gets the engine's own message mapped to 409 rather than a
			// generic 500.
			if reread, rerr := a.planStore.Get(id); rerr == nil &&
				reread.State != plan.StateRunning && reread.State != plan.StateApproved {
				jsonErr(w, http.StatusConflict, serr.Error())
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
	jsonOK(w, a.toWirePlan(*updated, nil))
}

// handlePlanRestart handles POST /api/v1/plans/{id}/restart (ADR-052 §6.7 +
// ADR-053 D13/G-12 ▶ Play). Delegates to PlanEngine.PlayPlan — the single
// chokepoint that mints a new generation, resets non-done members, persists
// ResumeFromCommit, and materializes the isolated resume checkout (#537).
// The plan lands in `approved`; the engine promotes approved→running under
// the global cap on its next tick (same as first execute).
func (a *restAPI) handlePlanRestart(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid plan ID")
		return
	}
	pe := agent.GetPlanEngine(a.agentLoop)
	if a.agentLoop == nil || pe == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan engine is not available")
		return
	}

	// Fail-fast existence + restart-permission check for a specific 404/409
	// before PlayPlan's heavier work (PlayPlan re-validates under its lock).
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
	if rerr := plan.ValidateRestartTransition(p.State, p.FailedReason); rerr != nil {
		jsonErr(w, http.StatusConflict, rerr.Error())
		return
	}

	playRes, perr := pe.PlayPlan(r.Context(), id)
	if perr != nil {
		if errors.Is(perr, plan.ErrRestartNotPermitted) || errors.Is(perr, plan.ErrNotFound) || errors.Is(perr, plan.ErrNotFailed) {
			jsonErr(w, http.StatusConflict, perr.Error())
			return
		}
		slog.Error("rest: plan restart: PlayPlan failed", "id", id, "error", perr)
		jsonErr(w, http.StatusInternalServerError, "could not restart plan")
		return
	}

	// Partial member-reset honesty (ADR-052 §6.4 Item 5 / prior REST contract):
	// PlayPlan logs and continues when RestartReset fails for a member. Surface
	// that as 500 naming the still-failed task IDs rather than an unqualified
	// 200 — the plan may already be approved, but the operator must re-check.
	// Iterate ONLY the failed members PlayPlan reported; the rest of the
	// reset-success members need no chat-side status fan-out (they will
	// re-emit naturally on dispatch).
	if len(playRes.StillFailedMemberIDs) > 0 {
		if a.taskStore != nil {
			for _, memberID := range playRes.StillFailedMemberIDs {
				m, gerr := a.taskStore.Get(memberID)
				if gerr != nil {
					slog.Error("rest: plan restart: get failed member after PlayPlan",
						"id", id, "member_id", memberID, "error", gerr)
					jsonErr(w, http.StatusInternalServerError, "could not verify member reset state after PlayPlan")
					return
				}
				a.emitTaskStatus(m)
				if a.agentLoop != nil {
					a.agentLoop.NotifyTaskUpserted(m)
				}
			}
		}
		a.auditPlan("plan.restart", id,
			"still_failed_members", playRes.StillFailedMemberIDs,
			"new_generation", playRes.NewGeneration)
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("plan restarted, but member reset failed for task(s) %v; re-check their state", playRes.StillFailedMemberIDs))
		return
	}

	a.auditPlan("plan.restart", id, "new_generation", playRes.NewGeneration)
	// Re-read for the response body: PlayPlan mutated the plan (state
	// failed -> approved, new generation, resume baselines). The pre-restart
	// `p` snapshot is stale by now, and returning it would report the OLD
	// state to the caller. Mirrors handlePlanPut/Approve/Stop, which all
	// respond from a post-mutation re-read.
	updated, uerr := a.planStore.Get(id)
	if uerr != nil {
		slog.Error("rest: plan restart: re-read after PlayPlan", "id", id, "error", uerr)
		jsonErr(w, http.StatusInternalServerError, "plan restarted but could not be re-read")
		return
	}
	jsonOK(w, a.toWirePlan(*updated, nil))
}
