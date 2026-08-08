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
	"strings"

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

// ErrRestartNotPermitted is returned by ValidateRestartTransition (and,
// transitively, Store.Update — see the restart-guard block there) when a
// failed->approved RESTART is requested on a plan that is not, right now,
// State==failed with FailedReason==stopped_by_user (ADR-052 §6.7, spec
// FR-016/DS-1/FR-026). It wraps ErrIllegalPlanTransition (and therefore
// ErrValidation), so any existing errors.Is(err, ErrValidation) 400-mapping
// still holds unchanged; callers that need to distinguish "not restartable"
// (spec FR-026: HTTP 409 — wrong state or wrong reason) from a malformed
// request (400) should check errors.Is(err, ErrRestartNotPermitted)
// specifically.
var ErrRestartNotPermitted = fmt.Errorf("%w: restart (failed -> approved) is only permitted from failed(stopped_by_user)", ErrIllegalPlanTransition)

// ErrNotFailed is returned by PlayPlan (engine) when a play operation is
// requested on a plan that is not currently in StateFailed. Play resumes a
// stopped/failed plan; any other state is rejected with this sentinel so the
// REST seam can map it to HTTP 409 via errors.Is without resorting to a
// fragile substring match on the wrapped error message. Modeled after
// ErrRestartNotPermitted above.
var ErrNotFailed = fmt.Errorf("%w: play is only permitted from failed", ErrIllegalPlanTransition)

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

// PermitsMemberDispatch reports whether p may currently have a `next` member
// task dispatched for autonomous execution (S1 UAT fix "PRIYA-GATE-never-
// executed"/"PRIYA-D8-race", plus its PAUSED-state follow-up).
//
// State alone is NOT sufficient: PausedReason is a same-State side-flag —
// pausing a plan does NOT move it out of StateRunning (see PausedReason's own
// doc comment; plan_engine.go's processPlan checks `p.PausedReason != ""`
// completely independently of p.State, per FR-065: "a paused plan neither
// dispatches nor judges"). A predicate keyed on State alone (the shape this
// method replaces — a bare map[State]bool living in pkg/agent) would permit
// dispatch of a paused-but-still-`running` plan, directly contradicting
// FR-065 in the very code meant to enforce the control. Hosting the check
// here, on the entity itself, means every caller sees BOTH fields together
// and this class of bug cannot recur by keying on one of them.
//
// A nil p (unresolved/lookup-failed) permits nothing — callers that cannot
// resolve the plan at all must fail closed exactly as they would for a
// resolved plan sitting in a non-dispatchable state.
//
// Deliberately no `default` case: .golangci.yaml sets
// exhaustive.default-signifies-exhaustive: true, so a default clause would
// silently stop the exhaustive linter from ever catching a future sixth
// State value added to the closed 5-value enum above (see State's own doc).
// Every current value is named explicitly so a new one fails the BUILD, not
// silently falls through here to a possibly-wrong answer.
func (p *Plan) PermitsMemberDispatch() bool {
	if p == nil || p.PausedReason != "" {
		return false
	}
	switch p.State {
	case StateApproved, StateRunning:
		return true
	case StateDraft, StateDone, StateFailed:
		return false
	}
	return false
}

// legalPlanTransitions is the canonical state transition matrix (spec Part A
// "State machine" table). Unlike task.Status's permissive-except-two-
// invariants policy, Plan's matrix is a closed allow-list: any (from, to)
// pair not listed here is illegal.
//
// approved -> failed (ADR-052 spec, Edge Case "Stop wins"): a cap-queued
// `approved` plan is non-terminal and the SPA offers Stop on it exactly like
// a running one — Stop must be able to fail it (stopped_by_user) before the
// engine ever admits/dispatches it. This does NOT touch the frozen-failed
// invariant: failed stays terminal (legalPlanTransitions[StateFailed] is
// still {StateFailed: true} only), and it does not widen approved's OTHER
// edges — approved->running still goes exclusively through the engine's cap
// admission (tryStartApprovedPlan), never through this matrix directly.
var legalPlanTransitions = map[State]map[State]bool{ //nolint:gochecknoglobals
	StateDraft:    {StateDraft: true, StateApproved: true},
	StateApproved: {StateDraft: true, StateApproved: true, StateRunning: true, StateFailed: true},
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

// ValidateRestartTransition reports whether a RESTART transition
// (failed -> approved, ADR-052 §6.7 "Restart = continuation", spec
// FR-016/DS-1) is legal, given the plan's CURRENT state and FailedReason.
//
// This is a narrow, store-level, REASON-AWARE amendment layered ON TOP of
// the canonical legalPlanTransitions matrix above — it does NOT widen that
// matrix (grill M2/R3-4): legalPlanTransitions[StateFailed] still contains
// only {StateFailed: true}, so ValidateStateTransition(failed, approved)
// keeps returning illegal via the matrix, unconditionally, exactly as
// before this function existed. Restart is legal ONLY when
// from == StateFailed AND failedReason == FailedReasonStoppedByUser (a user
// Stop, not a genuine failure). Genuine failures (judge_rounds_exhausted,
// idle_expired) stay frozen — this guard rejects them exactly like the
// matrix does for every other outbound edge off a terminal state.
//
// failed -> running is NEVER legal via this or any other path: a restart
// re-enters the state machine at approved, and the ENGINE promotes
// approved -> running under the global concurrency cap, exactly like a
// first execute — restarting straight to running would skip cap admission
// (the same hole class the PUT-lockdown, ADR-052 G2, closed for the
// original approve path).
//
// The sole in-process caller is Store.Update's State-patch handling
// (store.go); this is exported so the guard is independently
// table-testable per spec DS-1 without going through a full Create+Update
// round-trip for every case.
func ValidateRestartTransition(from State, failedReason FailedReason) error {
	if from != StateFailed {
		return fmt.Errorf("%w: restart is only valid from state %q, got %q", ErrRestartNotPermitted, StateFailed, from)
	}
	if failedReason != FailedReasonStoppedByUser {
		return fmt.Errorf("%w: got failed_reason %q, want %q", ErrRestartNotPermitted, failedReason, FailedReasonStoppedByUser)
	}
	return nil
}

// PlanPhase is the runtime-only sub-state of a State=running plan (R1 of the
// spec's round-3 reconciliation). It is NEVER a PlanState value and never
// appears in the State transition matrix above — Part C badges only the five
// State values and renders PlanPhase (when State==running) as a secondary
// chip.
type PlanPhase string

// The canonical plan phases. The empty string ("") on a fresh/never-run Plan
// is treated as PhaseIdle by EffectivePlanPhase.
//
// PhaseAwaitingSupervision (ADR-053 C1/FR-147; renamed by ADR-055/FR-062 —
// the adjudicator is the plansupervisor System Agent, NOT the plan's owner,
// which the retired name asserted and FR-011 overturned) is the DURABLE plan
// condition entered when an all-terminal DAG is judged UNMET by the plan Judge:
// rather than burning another round re-judging identical evidence (the F2
// round-burn gate, G-9), the plan parks at this phase while the plan-owner
// SESSION sits at the durable lifecycle state `paused` (the 8-state S2 enum is
// NOT inflated — awaiting-correction is a plan condition, not a session
// state). It persists alongside LastUnmetTerminalSignature so the gate
// SURVIVES RESTART (the standalone F2 fix was in-memory only and dropped one
// spurious re-judge per restart). The boot sweep (FR-118/INV-9) EXEMPTS a
// paused plan-owner session whose plan is in this phase from the
// failed(interrupted) sweep.
const (
	PhaseDispatching  PlanPhase = "dispatching"
	PhaseJudging      PlanPhase = "judging"
	PhaseSynthesizing PlanPhase = "synthesizing"
	PhaseIdle         PlanPhase = "idle"
	// PhaseAwaitingSupervision is the parked phase — see this block's header
	// comment. Renamed from the retired owner-correction spelling by
	// ADR-055/FR-062.
	//
	// NO CODE REJECTS THE PRE-RENAME VALUE, and nothing here should be read as
	// promising that it does: Store.load is a ReadFile + Unmarshal with no
	// validation, and normalize() — which is where phase validation lives — is
	// called only from Create and updateLocked. A record carrying the old
	// spelling would therefore LOAD, and surface as an unrecognised phase
	// downstream rather than as a load failure. That is accepted, not
	// overlooked: pkg/plan does not exist on main and neither does
	// contracts/components/schemas/Plan.yaml, so no persisted plan can carry
	// the old value and there is nothing to migrate. If plans ever ship before
	// a phase rename, the migration has to be written — this comment is not it.
	PhaseAwaitingSupervision PlanPhase = "awaiting_supervision"
	// PhaseStalled (swimlane-board UAT fix, round-1 finding #5 "ALSO" half) is
	// set by PlanEngine.surfaceStallIfAny (pkg/agent/plan_engine.go) when a
	// RUNNING, non-all-terminal plan has no member currently dispatchable
	// (`next`) or in flight (`in_progress`) — i.e. the engine's own dispatch
	// loop is guaranteed to be a no-op every tick until something changes (a
	// dependency resolves, or the owner corrects the plan). Reverts to
	// PhaseDispatching once a member becomes dispatchable/in-flight again.
	//
	// PRECEDENCE (never relax without re-checking pkg/agent/plan_engine.go):
	// PhaseAwaitingSupervision is a strictly MORE SPECIFIC condition (a
	// plan-judge dead end on an all-terminal DAG) and must NEVER be masked by
	// PhaseStalled — surfaceStallIfAny explicitly refuses to touch PlanPhase
	// while EffectivePlanPhase() == PhaseAwaitingSupervision. The two are
	// mutually exclusive by construction: entering PhaseAwaitingSupervision
	// requires an all-terminal member DAG (applyJudgeRoundOutcomeLocked is its
	// only writer), while PhaseStalled requires a NON-terminal one (processPlan
	// only ever reaches the stall check after its own allMembersTerminal check
	// has already come back false) — so processPlan can never reach the stall
	// check while genuinely parked at PhaseAwaitingSupervision.
	//
	// Disjointness is NOT the same question as correctability: both phases are
	// members of the supervision-eligible set (IsSupervisionEligiblePhase,
	// ADR-055/FR-029), because a stalled plan needs an adjudicator exactly as a
	// parked one does. Widening a GATE to accept either member of a set does
	// not make the set's members co-occur.
	PhaseStalled PlanPhase = "stalled"
)

// validPlanPhases is the set of allowed non-empty PlanPhase values.
var validPlanPhases = map[PlanPhase]bool{ //nolint:gochecknoglobals
	PhaseDispatching:         true,
	PhaseJudging:             true,
	PhaseSynthesizing:        true,
	PhaseIdle:                true,
	PhaseAwaitingSupervision: true,
	PhaseStalled:             true,
}

// supervisionEligiblePhases is the ADR-055/FR-029 supervision-eligible phase
// set — the phases from which a plan correction may be applied. It has exactly
// two members and is deliberately NOT "any phase": a plan at dispatching or
// judging is still rejected.
var supervisionEligiblePhases = map[PlanPhase]bool{ //nolint:gochecknoglobals
	PhaseAwaitingSupervision: true,
	PhaseStalled:             true,
}

// IsValidPlanPhase reports whether p is a known, explicit plan phase. The
// empty string is NOT itself accepted here — callers wanting the "" -> idle
// default should use EffectivePlanPhase; this validator only accepts an
// explicit, non-empty value.
func IsValidPlanPhase(p PlanPhase) bool { return validPlanPhases[p] }

// IsSupervisionEligiblePhase reports whether p is a member of the
// supervision-eligible phase set (ADR-055/FR-029): PhaseAwaitingSupervision or
// PhaseStalled. It is the single named predicate every supervision requirement
// is stated over — the correction phase gate, the supervision deadline, the
// attempt counter and the attempt ceiling — so those four can never drift apart
// into four separate hand-written phase comparisons.
//
// Callers holding a *Plan should pass EffectivePlanPhase() rather than the raw
// PlanPhase field, so an unset ("") phase resolves to PhaseIdle (not eligible)
// exactly once, at the same place every other phase read resolves it.
//
// The empty string is NOT a member: a plan that has never run is not awaiting
// supervision.
func IsSupervisionEligiblePhase(p PlanPhase) bool { return supervisionEligiblePhases[p] }

// IsAwaitingSupervision reports whether this plan is currently in the
// supervision-eligible phase set, resolving an unset phase to PhaseIdle first.
// Convenience wrapper over IsSupervisionEligiblePhase for the common
// *Plan-in-hand case; it deliberately does NOT consult State, because the
// phase's own writers only ever set it while State == StateRunning.
func (p *Plan) IsAwaitingSupervision() bool {
	return IsSupervisionEligiblePhase(p.EffectivePlanPhase())
}

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

// The canonical failed reasons.
const (
	FailedReasonJudgeRoundsExhausted FailedReason = "judge_rounds_exhausted"
	FailedReasonStoppedByUser        FailedReason = "stopped_by_user"
	FailedReasonIdleExpired          FailedReason = "idle_expired"
	// FailedReasonBudgetExhausted is the ADR-053 D12/R§8.3c/FR-174 graceful
	// wind-down terminal for a plan/task scope that crosses the app-level
	// OVERALL token budget at a dispatch/adjudication boundary (the same brake
	// the goal loop surfaces). The contract enum
	// (contracts/components/schemas/Plan.yaml failed_reason) already lists it.
	FailedReasonBudgetExhausted FailedReason = "budget_exhausted"
	// FailedReasonDoDUnreachable (ADR-055/FR-035) is the honest-exit terminal:
	// the Definition of Done cannot be reached from the plan's current state,
	// either because an applied correction left the plan unable to progress or
	// because the adjudicator issued the `abandon` verb. It is deliberately its
	// OWN value rather than a third meaning of judge_rounds_exhausted, because
	// rounds may still remain when it fires — "we ran out of rounds" and "more
	// rounds would not help" are different facts and the SPA badge, the
	// operator and the test all need to tell them apart.
	FailedReasonDoDUnreachable FailedReason = "dod_unreachable"
	// FailedReasonSupervisionUnavailable (ADR-055/FR-035, FR-022) is the
	// terminal for an exhausted supervision attempt ceiling: the plan parked,
	// was woken, and no valid correction ever arrived. It means the ADJUDICATOR
	// never delivered — not that the work itself failed — which is why it is
	// distinct from every other reason here.
	//
	// A plan with no chat origin must NEVER reach this value: an absent
	// source_channel/source_chat_id is a legitimate state, not a wake failure
	// (FR-012d(4)/(5)).
	FailedReasonSupervisionUnavailable FailedReason = "supervision_unavailable"
)

// validFailedReasons is the set of allowed non-empty FailedReason values.
var validFailedReasons = map[FailedReason]bool{ //nolint:gochecknoglobals
	FailedReasonJudgeRoundsExhausted:   true,
	FailedReasonStoppedByUser:          true,
	FailedReasonIdleExpired:            true,
	FailedReasonBudgetExhausted:        true,
	FailedReasonDoDUnreachable:         true,
	FailedReasonSupervisionUnavailable: true,
}

// IsValidFailedReason reports whether r is a known, explicit failed reason.
func IsValidFailedReason(r FailedReason) bool { return validFailedReasons[r] }

// PausedReasonOwnerDisabled is the one PausedReason value production code
// sets today (PausePlansOwnedBy/ResumePlansOwnedBy, pkg/agent/plan_engine.go):
// a running plan's owner agent was disabled mid-loop.
//
// Fix-wave finding 6(b) (14-reviewer sign-off): Plan.PausedReason is
// deliberately kept typed `string` (NOT a named PausedReason type like
// PlanPhase/FailedReason on the same struct) — several call sites outside
// this package consume it as a bare string via concatenation/strings.TrimSpace
// (pkg/tools/list_jobs_row.go) or assign it directly into a wire `*string`
// field (pkg/gateway/rest_plans.go, gateway.go, websocket.go), all of which a
// named type would break without editing those files. The "closed enum" is
// instead enforced as a runtime-validated set of string constants, mirroring
// FailedReason/PlanPhase's validity CONCEPT without their Go type.
const PausedReasonOwnerDisabled = "owner_disabled"

// validPausedReasons is the closed set IsValidPausedReason checks against.
// The empty string ("not paused") is handled separately by every caller
// (never a member of this set), exactly like PlanPhase/FailedReason's own
// "empty means unset" convention.
var validPausedReasons = map[string]bool{ //nolint:gochecknoglobals
	PausedReasonOwnerDisabled: true,
}

// IsValidPausedReason reports whether r is a known, explicit non-empty
// paused reason (fix-wave finding 6(b)).
func IsValidPausedReason(r string) bool { return validPausedReasons[r] }

// PlanBounds holds per-plan overrides of the global config.PlanningConfig
// bounds (ADR D7/FR-9, spec Part A §A). A nil field inherits that field's
// global default via config.PlanningConfig's Effective* resolvers. NO
// token/money fields (NFR-1).
type PlanBounds struct {
	PlanJudgeMaxRounds *int `json:"plan_judge_max_rounds,omitempty"`
	IdleExpiryDays     *int `json:"idle_expiry_days,omitempty"`
	// SupervisionTurnTimeoutSeconds overrides FR-021's supervision observation
	// deadline for this plan (global default
	// config.DefaultSupervisionTurnTimeoutSeconds = 600 s). Resolve via
	// config.PlanningConfig.EffectiveSupervisionTurnTimeoutSeconds, never by
	// reading the pointer directly.
	SupervisionTurnTimeoutSeconds *int `json:"supervision_turn_timeout_seconds,omitempty"`
	// SupervisionMaxAttempts overrides FR-022's no-correction attempt ceiling
	// for this plan (global default config.DefaultSupervisionMaxAttempts = 3).
	// Resolve via config.PlanningConfig.EffectiveSupervisionMaxAttempts.
	SupervisionMaxAttempts *int `json:"supervision_max_attempts,omitempty"`
}

// Supervision is the durable PlanSupervisor adjudication state for one plan
// (ADR-055/FR-050, contracts/components/schemas/Plan.yaml `supervision`).
// Server-set only; never accepted from a create or update body. It is one
// object rather than five loose Plan fields because the five values are read
// together by every supervision decision, but it is written through FIVE
// DISCRETE plan.Patch pointers rather than one whole-object pointer — see
// Patch's supervision block for why.
//
// LIFETIME IS PER FIELD, NEVER A BLANKET RULE. A plan leaves the
// supervision-eligible phase set on EVERY applied correction (that is the
// contract: an applied correction returns the plan to dispatching), so any
// "reset everything when the phase is left" rule would zero CorrectionRounds
// immediately after each increment and every terminal record would read 0.
// The per-field rule is:
//
//	WakeAt           cleared on leaving the set and on an applied correction
//	WakeError        cleared likewise, and by the next successful wake
//	Attempts         reset to 0 likewise (never touches CorrectionRounds)
//	CorrectionRounds CUMULATIVE FOR THE LIFE OF THE PLAN — never reset
//	SessionID        NEVER cleared; overwritten when the next session is minted
type Supervision struct {
	// WakeAt is the RFC 3339 UTC timestamp of the supervision wake receipt. It
	// does double duty by design: it ARMS the supervision deadline, and it is
	// the once-per-park dedup key that stops every engine tick re-waking the
	// same parked plan. LastUnmetTerminalSignature cannot serve as that key —
	// it is a signature of the MEMBER STATE, which is by definition unchanged
	// while the plan is parked, so a signature-keyed guard fires every tick.
	WakeAt string `json:"wake_at,omitempty"`
	// WakeError is the last supervision wake-publish failure, recorded rather
	// than logged and forgotten so an undelivered wake is observable on the
	// record itself.
	//
	// A plan with NO chat origin is not a wake error — that case is logged at
	// INFO with reason=no_chat_origin and must never land here, or a healthy
	// UI-created plan walks the escalation ladder to
	// FailedReasonSupervisionUnavailable on a false diagnosis.
	WakeError string `json:"wake_error,omitempty"`
	// Attempts counts supervision turns that produced NO valid correction. It
	// is bounded by the supervision_max_attempts ceiling; exhausting it
	// terminates the plan failed(supervision_unavailable). Resetting it never
	// touches CorrectionRounds.
	Attempts int `json:"attempts,omitempty"`
	// CorrectionRounds counts corrections APPLIED over the whole life of the
	// plan. It is an ATTRIBUTION counter, not a budget — nothing gates on it.
	// Its one reader distinguishes the two causes that share the
	// judge_rounds_exhausted value: == 0 means the round ceiling was reached
	// with no correction ever applied; > 0 means corrections consumed the
	// shared round budget.
	//
	// NEVER RESET. See the type comment: a reset-on-leaving-the-phase rule
	// zeroes it immediately after every increment.
	CorrectionRounds int `json:"correction_rounds,omitempty"`
	// SessionID is the real, store-backed session the adjudication turn runs
	// in. It keeps the adjudication transcript out of the plan owner's session
	// (Plan.OwnerSessionID is a DIFFERENT session — the owner's) and is the
	// handle a plan-scoped stop cancels.
	//
	// NEVER CLEARED, only overwritten when the next supervision session is
	// minted: an applied correction returns the plan to dispatching while the
	// adjudication turn may still be running, and blanking the handle in that
	// window would leave a stop unable to name the turn it must cancel.
	// Cancelling an already-finished session is a benign no-op, so retaining
	// the id costs nothing and closes the window.
	SessionID string `json:"session_id,omitempty"`
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
	if b.SupervisionTurnTimeoutSeconds != nil && *b.SupervisionTurnTimeoutSeconds < 1 {
		return verr("bounds.supervision_turn_timeout_seconds must be at least 1")
	}
	if b.SupervisionMaxAttempts != nil && *b.SupervisionMaxAttempts < 1 {
		return verr("bounds.supervision_max_attempts must be at least 1")
	}
	return nil
}

// maxPlanTitleRunes / maxPlanGoalRunes / maxPlanDescriptionRunes bound
// Plan.Title / Plan.Goal / Plan.Description (spec Part A §A).
// maxPlanHandoverRunes bounds Plan.HandoverText (Wave 2-B).
// maxPlanRationaleRunes bounds Plan.Rationale (ADR-053 §Contract Surface,
// mirrors PlanCreateRequest.yaml/Plan.yaml's `rationale: maxLength: 4000`).
const (
	maxPlanTitleRunes       = 200
	maxPlanGoalRunes        = 2000
	maxPlanDescriptionRunes = 2000
	maxPlanHandoverRunes    = 8000
	maxPlanRationaleRunes   = 4000
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
	// LastUnmetTerminalSignature is the durable form of the F2 round-burn gate
	// (ADR-053 C1/FR-147/INV-7). It holds the planTerminalSignature of the
	// all-terminal member state most recently judged UNMET, persisted on the
	// plan record so the "do not re-judge unchanged all-terminal state" gate
	// SURVIVES RESTART (the shipped standalone F2 fix was an in-memory
	// map[string]string on PlanEngine that dropped on every restart, burning
	// one spurious JudgeRound per restart). The engine rehydrates its
	// in-memory gate from this field at boot (bootReconcile). Meaningful only
	// while PlanPhase == PhaseAwaitingSupervision; cleared when the plan
	// (re)enters a fresh dispatch cycle (tryStartApprovedPlan).
	LastUnmetTerminalSignature string `json:"last_unmet_terminal_signature,omitempty"`
	// OwnerSessionID is the named durable linkage to the plan-owner AGENT
	// session (ADR-053 m-3/FR-147) — the reciprocal of
	// session.LifecycleRecord.OwnsPlanID. A top-level plan-owner session's
	// owner_scope is `human` (not `plan_id`), so owner_scope alone cannot
	// identify the plan; this field plus the session-side OwnsPlanID give the
	// boot sweep a named plan<->owner-session linkage to resolve the
	// awaiting-correction exemption (FR-118 exemption b) through. Populated by
	// the Phase-2 owner loop that wraps plan execution; empty until then (the
	// boot sweep resolves the exemption through the session-side OwnsPlanID,
	// which is the reliable direction at boot regardless).
	OwnerSessionID string `json:"owner_session_id,omitempty"`
	// Supervision is the durable PlanSupervisor adjudication state
	// (ADR-055/FR-050). nil until the plan first enters the
	// supervision-eligible phase set. See the Supervision type for its
	// strictly per-field lifetime rules.
	Supervision *Supervision `json:"supervision,omitempty"`
	// SourceChannel/SourceChatID are the plan's CHAT ORIGIN — the channel and
	// chat a plan wake delivers its outcome back to (ADR-055/FR-012d). They
	// mirror task.Task's identically-named pair (pkg/task/task.go), including
	// its optionality, and exist because a plan previously carried no origin at
	// all, so its wakes had to invent a synthetic internal destination that the
	// message path then dropped.
	//
	// BOTH ABSENT IS A LEGITIMATE, EXPECTED STATE, not a degraded one: a plan
	// created over REST from the Plans UI has no chat context to record. Do not
	// mint a synthetic origin, do not fall back to the owner agent's
	// last-active channel, and do not treat absence as an error anywhere — the
	// wake predicate is "both non-empty", and when it is false the owner turn
	// still runs and its synthesis is still persisted; only the outbound
	// message is skipped.
	//
	// Set at creation only and immutable thereafter, like Owner/CreatedBy.
	// Never accepted from a create request or a PATCH body: a client-supplied
	// origin would let a caller redirect another principal's plan outcome into
	// a conversation of its choosing.
	SourceChannel string `json:"source_channel,omitempty"`
	SourceChatID  string `json:"source_chat_id,omitempty"`
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
	// Rationale is the persisted planning rationale — the "why" behind the
	// plan's decomposition (e.g. the write-set/stream split chosen and the
	// join points authored), ADR-053 §Contract Surface. Optional on the wire
	// (soft tier for human/UI-authored plans, mirrors DoD's own tiering);
	// the create_plan AGENT TOOL (pkg/tools/plan.go) requires it
	// unconditionally, since every caller of that tool is an agent — see
	// PlanCreateTool's type doc for why DoD collapses to the strict tier the
	// same way. Set-once-at-create: there is no wire path to change it after
	// creation (PlanUpdateRequest carries no rationale field).
	Rationale string `json:"rationale,omitempty"`

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
	// Trim before validating (S2 UAT finding B sibling: the same
	// untrimmed-`== ""` pattern that let a whitespace-only title through also
	// existed here; mirrors task.Task.normalize's identical fix). A
	// whitespace-only title (" \t ") is not user-facing content — reject it as
	// empty rather than persisting a blank-looking, unfindable plan chip; a
	// legitimate title's incidental leading/trailing whitespace is silently
	// normalized rather than rejected. This runs for every Store.Create
	// caller (REST, plan engine, agent tools), not just the REST handler.
	// task.HasVisibleContent then catches the invisible/zero-width/format
	// case TrimSpace itself misses (UAT round-2 S3 finding — see that
	// function's doc comment): a title made ENTIRELY of zero-width/format
	// codepoints (or the Cf-adjacent BRAILLE PATTERN BLANK) is rejected the
	// same as "".
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || !task.HasVisibleContent(p.Title) {
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
	if len([]rune(p.Rationale)) > maxPlanRationaleRunes {
		return verr("rationale must be %d characters or fewer", maxPlanRationaleRunes)
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
	if p.PausedReason != "" && !IsValidPausedReason(p.PausedReason) {
		return verr("invalid paused_reason %q", p.PausedReason)
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

// --- Corrections (ADR-055 FR-004/FR-046, ADR-053 §3b G-11) ----------------
//
// The correction types live HERE, in pkg/plan, and are re-exported from
// pkg/agent as one-line type aliases — the exact shape IntentEdge already has
// (intent_log.go). The reason is a hard import constraint, not taste:
// pkg/agent imports pkg/tools, so pkg/tools cannot import pkg/agent, and the
// plan_correct tool must name the very same request type the engine consumes.
// Two structurally-identical declarations would compile and would be two
// types; a field added to one and not the other is then a silent divergence no
// compiler reports. pkg/plan is the one package both sides already import.

// Correction payload caps (FR-046 / D-06). Fixed package constants,
// deliberately NOT config fields and NOT PlanBounds-overridable: no operator
// use case was stated for varying a title-byte cap per plan, and each override
// would be a wire addition plus a resolver. They bound the payload processed
// while the engine holds the process-wide planDecisionMu for the whole of
// AppendCorrection — the same mutex the plan tick, StopPlan, the judge round
// and idle expiry take.
//
// Both validators read these: the plan_correct tool boundary (which rejects
// before any mutation) and the engine's own validateCorrection. Promoting one
// to a config field later is a contained change; demoting a shipped wire field
// is not.
//
// Every bound is on BYTES, not runes. MaxTextBytes bounds exactly three
// fields: FalsifiedAssumption, Reason, and each tail member's Description.
// A tail member's Title is bounded by MaxMemberTitleBytes instead.
const (
	// MaxTailMembers caps how many new members one correction may add.
	MaxTailMembers = 20
	// MaxTailEdges caps how many dependency edges one correction may wire.
	MaxTailEdges = 40
	// MaxMemberTitleBytes caps a tail member's title.
	MaxMemberTitleBytes = 512
	// MaxTextBytes caps each free-text correction field.
	MaxTextBytes = 8192
)

// CorrectionCaller is the authenticated principal issuing a correction
// (sec-MAJOR-2). Whoever consumes it decides what authority the identity
// carries — the engine gates on the plan's durable owner linkage
// (OwnerAgentID / OwnerSessionID), while the plan_correct tool gates on the
// PlanSupervisor identity before it ever reaches the engine.
//
// Callers (REST handler, agent tool, or the owner-correction loop) MUST
// resolve the invoking principal's real agent/session identity — there is no
// "trusted internal caller" bypass.
//
// not-wire-format: engine/tool-internal; the REST/tool layer maps its
// authenticated principal to this.
type CorrectionCaller struct {
	AgentID   string
	SessionID string
}

// CorrectionRequest is a validated correction to a plan whose DoD is unmet
// (FR-143/G-11, FR-046). Each verb records a revision entry committed
// transactionally via the intent log (INV-6/N-8).
//
// The shape is structurally incapable of carrying a `dod` or an
// `owner_agent_id` (FR-032): a correction can add work and re-weight evidence,
// never redefine what success means or who is accountable for it.
//
// not-wire-format: engine/tool-internal; the REST/tool layer maps its wire
// type to this.
type CorrectionRequest struct {
	Verb                RevisionVerb `json:"verb"`
	FalsifiedAssumption string       `json:"falsified_assumption,omitempty"`
	TailMembers         []task.Task  `json:"tail_members,omitempty"`
	TailEdges           []IntentEdge `json:"tail_edges,omitempty"`
	SupersededMemberID  string       `json:"superseded_member_id,omitempty"`
	RetriedMemberID     string       `json:"retried_member_id,omitempty"`
	Reason              string       `json:"reason,omitempty"`
}

// CorrectionResult is the outcome of processing a correction. HonestExit
// reports that the correction was applied but the plan still had no way to
// make progress and was failed rather than left to livelock (G-10).
//
// not-wire-format: engine/tool-internal.
type CorrectionResult struct {
	RevisionID    string        `json:"revision_id"`
	Generation    int           `json:"generation"`
	RevisionEntry RevisionEntry `json:"revision_entry"`
	HonestExit    bool          `json:"honest_exit,omitempty"`
}
