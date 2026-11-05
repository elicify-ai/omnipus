// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package taskwire

import (
	"log/slog"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Plan converts an internal plan.Plan to the generated wire type,
// server-computing `progress` read-time via plan.ComputeProgress (R4/C19).
// lister serves that ComputeProgress call; nil leaves progress absent. warn
// receives the non-fatal conversion warnings (a malformed wake_at, a failed
// progress computation); nil logs them with slog.Warn.
func Plan(p plan.Plan, lister plan.TaskLister, warn func(msg string, args ...any)) gen.Plan {
	if warn == nil {
		warn = slog.Warn
	}
	out := gen.Plan{
		Id:           p.ID,
		WorkspaceId:  p.WorkspaceID,
		Title:        p.Title,
		State:        gen.PlanState(p.State),
		OwnerAgentId: p.OwnerAgentID,
		CreatedAt:    ParseTimeOrNow(p.CreatedAt),
		UpdatedAt:    ParseTimeOrNow(p.UpdatedAt),
	}
	if p.Objective != "" {
		out.Objective = ptr(p.Objective)
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
		out.Dod = wirePlanDoD(p.DoD)
	}
	setPlanBounds(&out, p)
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
	setPlanSupervision(&out, p, warn)
	if lister != nil {
		if _, _, progress, err := plan.ComputeProgress(p.ID, lister); err == nil {
			pr := float32(progress)
			out.Progress = &pr
		} else {
			warn("rest: plan progress compute failed", "plan_id", p.ID, "error", err)
		}
	}
	return out
}

// setPlanBounds copies the optional plan bounds onto the wire form.
func setPlanBounds(out *gen.Plan, p plan.Plan) {
	if p.Bounds == nil {
		return
	}
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

// setPlanSupervision copies the durable PlanSupervisor adjudication state
// (ADR-055/FR-050) onto the wire form.
func setPlanSupervision(out *gen.Plan, p plan.Plan, warn func(msg string, args ...any)) {
	if p.Supervision == nil {
		return
	}
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
			warn("rest: plan supervision.wake_at is not RFC3339; omitted from the wire payload",
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

// wirePlanDoD converts internal acceptance criteria to the gen.Plan.Dod
// inline wire shape (read path — GET/PUT/POST/approve/stop responses).
// Mirrors criteria.go's wireCriteria exactly, but oapi-codegen generates
// a per-schema-context anonymous struct type (PlanDodKind etc., distinct from
// TaskCriteriaKind etc. even though both alias string), so the conversion
// cannot be shared — same reason wireCriteria/criteriaFromCreateWire/
// criteriaFromUpdateWire are already three near-duplicate functions there.
func wirePlanDoD(cs []task.AcceptanceCriterion) *[]struct {
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
	ClauseCount *int                   `json:"clause_count,omitempty"`
	Id          *string                `json:"id,omitempty"`
	Judgment    gen.PlanDodJudgment    `json:"judgment"`
	Kind        gen.PlanDodKind        `json:"kind"`
	Provenance  *gen.PlanDodProvenance `json:"provenance,omitempty"`
	Status      gen.PlanDodStatus      `json:"status"`
	Text        string                 `json:"text"`
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
		ClauseCount *int                   `json:"clause_count,omitempty"`
		Id          *string                `json:"id,omitempty"`
		Judgment    gen.PlanDodJudgment    `json:"judgment"`
		Kind        gen.PlanDodKind        `json:"kind"`
		Provenance  *gen.PlanDodProvenance `json:"provenance,omitempty"`
		Status      gen.PlanDodStatus      `json:"status"`
		Text        string                 `json:"text"`
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
			ClauseCount *int                   `json:"clause_count,omitempty"`
			Id          *string                `json:"id,omitempty"`
			Judgment    gen.PlanDodJudgment    `json:"judgment"`
			Kind        gen.PlanDodKind        `json:"kind"`
			Provenance  *gen.PlanDodProvenance `json:"provenance,omitempty"`
			Status      gen.PlanDodStatus      `json:"status"`
			Text        string                 `json:"text"`
		}{
			Kind:     gen.PlanDodKind(c.Kind),
			Judgment: gen.PlanDodJudgment(CriterionJudgment(c)),
			Status:   gen.PlanDodStatus(c.Status),
			Text:     c.Text,
		}
		if c.Provenance != "" {
			p := gen.PlanDodProvenance(c.Provenance)
			item.Provenance = &p
		}
		// C-58/JUDGE-FR-006b: mirrors rest_tasks.go's wireCriteria fix —
		// this field was declared in the anonymous wire shape but never
		// populated. ClauseCount is minimum:1 on the schema, so a zero
		// (never-normalized) value is left absent rather than emitted as an
		// invalid 0.
		if c.ClauseCount > 0 {
			cc := c.ClauseCount
			item.ClauseCount = &cc
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
