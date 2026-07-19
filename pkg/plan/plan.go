// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package plan is the Plan entity (ADR-049 D1/D4, spec Part A §A) — the
// Planning & Goals epic's grouping construct for related tasks under a single
// Definition-of-Done, judged by the Judge System Agent. It mirrors
// pkg/task's store pattern (per-entity JSON files, striped-lock RMW, atomic
// writes) as its own dedicated package: pkg/plan may import pkg/task (for
// the shared AcceptanceCriterion type and its NormalizeCriteria validator),
// but pkg/task must NEVER import pkg/plan — there is no back-reference
// anywhere in this package.
package plan

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// ErrNotFound is returned when a plan ID does not exist on disk.
var ErrNotFound = errors.New("plan not found")

// ErrValidation is the sentinel wrapped by every user-facing field/transition
// validation error the store returns (vs. an internal I/O failure). Mirrors
// task.ErrValidation so the REST seam can map both the same way via
// errors.Is(err, ErrValidation) -> HTTP 400.
var ErrValidation = errors.New("plan validation")

// ErrIllegalPlanTransition is returned when a State patch requests a
// transition the plan lifecycle does not allow (SD-A2). It wraps
// ErrValidation so the REST seam maps it to HTTP 400.
var ErrIllegalPlanTransition = fmt.Errorf("%w: illegal plan state transition", ErrValidation)

// verr wraps a formatted message as a user-facing validation error (ErrValidation).
func verr(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...)
}

// State is the canonical 5-value Plan lifecycle (spec Part A "State
// machine"; R1 of the spec's round-3 reconciliation). Part B's nine runtime
// states re-express onto these five via PlanPhase / FailedReason /
// PausedReason — see those types' docs; PlanState itself stays closed at
// five values.
type State string

// The five canonical plan states. These are the ONLY valid State values.
const (
	StateDraft    State = "draft"    // being authored; not yet runnable
	StateApproved State = "approved" // DoD/owner locked in; ready to run (or cap-waiting)
	StateRunning  State = "running"  // engine dispatching member tasks under plan judge
	StateDone     State = "done"     // terminal success (plan judge PASS)
	StateFailed   State = "failed"   // terminal failure (brake fired / judge rounds exhausted)
)

// validStates is the set of allowed State values.
var validStates = map[State]bool{ //nolint:gochecknoglobals
	StateDraft:    true,
	StateApproved: true,
	StateRunning:  true,
	StateDone:     true,
	StateFailed:   true,
}

// IsValidState reports whether s is one of the five canonical plan states.
func IsValidState(s State) bool { return validStates[s] }

// IsTerminal reports whether s is a terminal (frozen) plan state. Unlike
// task.Status (where only `done` is frozen and `failed` may be retried), a
// Plan freezes BOTH done and failed — a failed plan is never retried (F1 r2
// of the spec reconciliation); the operator authors a fresh plan instead.
func IsTerminal(s State) bool { return s == StateDone || s == StateFailed }

// legalPlanTransitions is the canonical state transition matrix (spec Part A
// "State machine" table). Unlike task.Status's permissive-except-two-
// invariants policy, Plan's matrix is a closed allow-list: any (from, to)
// pair not listed here is illegal.
var legalPlanTransitions = map[State]map[State]bool{ //nolint:gochecknoglobals
	StateDraft:    {StateDraft: true, StateApproved: true},
	StateApproved: {StateDraft: true, StateApproved: true, StateRunning: true},
	StateRunning:  {StateRunning: true, StateDone: true, StateFailed: true},
	StateDone:     {StateDone: true},
	StateFailed:   {StateFailed: true},
}

// ValidateStateTransition reports whether moving from->to is a legal Plan
// state transition per the canonical matrix (SD-A2). `done` and `failed` are
// BOTH terminal/frozen: every outbound edge from either is rejected except
// the no-op (a failed plan is NEVER retried — F1 r2; author a new plan).
// A no-op (from == to) is always legal for every state, including the two
// terminal ones. Returns ErrIllegalPlanTransition (wrapping ErrValidation) on
// rejection.
func ValidateStateTransition(from, to State) error {
	if !IsValidState(from) {
		return verr("invalid state %q", from)
	}
	if !IsValidState(to) {
		return verr("invalid state %q", to)
	}
	if allowed, ok := legalPlanTransitions[from]; ok && allowed[to] {
		return nil
	}
	return fmt.Errorf("%w: %q -> %q is not permitted", ErrIllegalPlanTransition, from, to)
}

// PlanPhase is the runtime-only sub-state of a State=running plan (R1 of the
// spec's round-3 reconciliation). It is NEVER a PlanState value and never
// appears in the State transition matrix above — Part C badges only the five
// State values and renders PlanPhase (when State==running) as a secondary
// chip.
type PlanPhase string

// The four canonical plan phases. The empty string ("") on a fresh/never-run
// Plan is treated as PhaseIdle by EffectivePlanPhase.
const (
	PhaseDispatching  PlanPhase = "dispatching"
	PhaseJudging      PlanPhase = "judging"
	PhaseSynthesizing PlanPhase = "synthesizing"
	PhaseIdle         PlanPhase = "idle"
)

// validPlanPhases is the set of allowed non-empty PlanPhase values.
var validPlanPhases = map[PlanPhase]bool{ //nolint:gochecknoglobals
	PhaseDispatching:  true,
	PhaseJudging:      true,
	PhaseSynthesizing: true,
	PhaseIdle:         true,
}

// IsValidPlanPhase reports whether p is a known, explicit plan phase. The
// empty string is NOT itself accepted here — callers wanting the "" -> idle
// default should use EffectivePlanPhase; this validator only accepts an
// explicit, non-empty value.
func IsValidPlanPhase(p PlanPhase) bool { return validPlanPhases[p] }

// EffectivePlanPhase returns p.PlanPhase, defaulting an unset ("") value to
// PhaseIdle.
func (p *Plan) EffectivePlanPhase() PlanPhase {
	if p.PlanPhase == "" {
		return PhaseIdle
	}
	return p.PlanPhase
}

// FailedReason discriminates why a State=failed plan failed (R1). It is set
// only when State == StateFailed; empty for every other state.
type FailedReason string

// The three canonical failed reasons.
const (
	FailedReasonJudgeRoundsExhausted FailedReason = "judge_rounds_exhausted"
	FailedReasonStoppedByUser        FailedReason = "stopped_by_user"
	FailedReasonIdleExpired          FailedReason = "idle_expired"
)

// validFailedReasons is the set of allowed non-empty FailedReason values.
var validFailedReasons = map[FailedReason]bool{ //nolint:gochecknoglobals
	FailedReasonJudgeRoundsExhausted: true,
	FailedReasonStoppedByUser:        true,
	FailedReasonIdleExpired:          true,
}

// IsValidFailedReason reports whether r is a known, explicit failed reason.
func IsValidFailedReason(r FailedReason) bool { return validFailedReasons[r] }

// PlanBounds holds per-plan overrides of the global config.PlanningConfig
// bounds (ADR D7/FR-9, spec Part A §A). A nil field inherits that field's
// global default via config.PlanningConfig's Effective* resolvers. NO
// token/money fields (NFR-1).
type PlanBounds struct {
	PlanJudgeMaxRounds *int `json:"plan_judge_max_rounds,omitempty"`
	IdleExpiryDays     *int `json:"idle_expiry_days,omitempty"`
}

// validatePlanBounds validates a (possibly nil) PlanBounds. A nil bounds is
// always valid (inherit everything from global config).
func validatePlanBounds(b *PlanBounds) error {
	if b == nil {
		return nil
	}
	if b.PlanJudgeMaxRounds != nil && *b.PlanJudgeMaxRounds < 1 {
		return verr("bounds.plan_judge_max_rounds must be at least 1")
	}
	if b.IdleExpiryDays != nil && *b.IdleExpiryDays < 1 {
		return verr("bounds.idle_expiry_days must be at least 1")
	}
	return nil
}

// maxPlanTitleRunes / maxPlanGoalRunes / maxPlanDescriptionRunes bound
// Plan.Title / Plan.Goal / Plan.Description (spec Part A §A).
// maxPlanHandoverRunes bounds Plan.HandoverText (Wave 2-B).
const (
	maxPlanTitleRunes       = 200
	maxPlanGoalRunes        = 2000
	maxPlanDescriptionRunes = 2000
	maxPlanHandoverRunes    = 8000
)

// Plan is the on-disk Plan entity stored at
// $OMNIPUS_HOME/plans/<id>.json (ADR-049 D1/D4, spec Part A §A).
//
// not-wire-format: internal disk struct; mapped to gen.Plan at the REST layer.
type Plan struct { //nolint:revive // exported name matches package purpose
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	Goal        string `json:"goal,omitempty"`
	Description string `json:"description,omitempty"`
	State       State  `json:"state"`
	// OwnerAgentID is the agent woken at plan decision points (ADR D4);
	// required.
	OwnerAgentID string `json:"owner_agent_id"`
	// DoD is the plan-level acceptance-criteria set the plan judge evaluates.
	// Shape-validated via task.NormalizeCriteria; the tiered "must be
	// non-empty before approve for agent-authored plans" rule (SD-A7) is
	// enforced by the caller (tool/REST layer), not this package — mirrors
	// how task.Task.Criteria's own tiered "at least one" rule is enforced by
	// its caller rather than the task store.
	DoD []task.AcceptanceCriterion `json:"dod,omitempty"`
	// Bounds overrides the global config.PlanningConfig for this plan; nil =
	// inherit every field from the global config.
	Bounds *PlanBounds `json:"bounds,omitempty"`

	// --- persisted counters (ADR D4 MAJ-004: durable, boot-reconciled) ---
	JudgeRounds    int          `json:"judge_rounds,omitempty"`
	ActiveLoop     bool         `json:"active_loop,omitempty"`
	PausedReason   string       `json:"paused_reason,omitempty"`
	LastActivityAt string       `json:"last_activity_at,omitempty"`
	PlanPhase      PlanPhase    `json:"plan_phase,omitempty"`
	FailedReason   FailedReason `json:"failed_reason,omitempty"`
	// Progress is done/total over member tasks (R4/C19/M7). It is
	// server-computed READ-TIME ONLY by scanning the task store for
	// plan_id == this plan's ID (see ComputeProgress). json:"-" (review r1
	// type-design fix, was "progress,omitempty"): Create/Update must NEVER
	// persist this field — the prior omitempty tag let a caller who
	// mistakenly populated it on an in-memory *Plan before calling
	// Create/Update silently write a stale snapshot to disk (nothing
	// rejected it, per this comment's own prior "a value present on disk is
	// only ever a stale snapshot a caller mistakenly wrote" admission).
	// json:"-" makes the "never persisted" guarantee structural — every
	// write path's own json.Marshal now drops it unconditionally — instead
	// of a doc-comment convention callers had to remember. Readers should
	// treat ComputeProgress as authoritative, never this field.
	Progress float64 `json:"-"`
	// HandoverText is the plan engine's (Wave 2-B, pkg/agent's PlanEngine)
	// latest wind-down/steering note: per-criterion unmet reasons after an
	// UNMET plan-judge round (evaluator-optimizer steering, mirrors
	// task.Task's Result-as-steering-carrier convention), or the graceful
	// wind-down summary written on a terminal brake (judge rounds exhausted,
	// idle expiry, stop). Server-set only; not part of plan authoring.
	HandoverText string `json:"handover_text,omitempty"`

	// --- attribution + lifecycle timestamps (RFC 3339 UTC) ---
	Owner       string `json:"owner,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ApprovedAt  string `json:"approved_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// normalize applies field defaults and validates the entity. It does NOT
// touch the filesystem. Returns a user-facing error (wrapping ErrValidation)
// on invalid input.
func (p *Plan) normalize() error {
	if p.Title == "" {
		return verr("title is required")
	}
	if len([]rune(p.Title)) > maxPlanTitleRunes {
		return verr("title must be %d characters or fewer", maxPlanTitleRunes)
	}
	if len([]rune(p.Goal)) > maxPlanGoalRunes {
		return verr("goal must be %d characters or fewer", maxPlanGoalRunes)
	}
	if len([]rune(p.Description)) > maxPlanDescriptionRunes {
		return verr("description must be %d characters or fewer", maxPlanDescriptionRunes)
	}
	if len([]rune(p.HandoverText)) > maxPlanHandoverRunes {
		return verr("handover_text must be %d characters or fewer", maxPlanHandoverRunes)
	}
	if p.WorkspaceID == "" {
		return verr("workspace_id is required")
	}
	if p.OwnerAgentID == "" {
		return verr("owner_agent_id is required")
	}
	if p.State == "" {
		p.State = StateDraft
	}
	if !IsValidState(p.State) {
		return verr("invalid state %q", p.State)
	}
	if p.PlanPhase != "" && !IsValidPlanPhase(p.PlanPhase) {
		return verr("invalid plan_phase %q", p.PlanPhase)
	}
	if p.FailedReason != "" {
		if !IsValidFailedReason(p.FailedReason) {
			return verr("invalid failed_reason %q", p.FailedReason)
		}
		if p.State != StateFailed {
			return verr("failed_reason is only valid when state is failed")
		}
	}
	if p.DoD != nil {
		normalized, err := task.NormalizeCriteria(p.DoD)
		if err != nil {
			// task.NormalizeCriteria wraps task.ErrValidation, not
			// ErrValidation. Wrap ErrValidation too (double %w, Go 1.20+) so
			// callers checking errors.Is(err, plan.ErrValidation) — the
			// REST/tool seam's 400 mapping — see it, while errors.Is(err,
			// task.ErrValidation) still holds transitively for anything
			// checking the more specific criteria-shape sentinel.
			return fmt.Errorf("%w: %w", ErrValidation, err)
		}
		p.DoD = normalized
	}
	if err := validatePlanBounds(p.Bounds); err != nil {
		return err
	}
	return nil
}
