// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Claim is a snapshot of the most recent goal_claim tool call against a
// goal (JUDGE §D12's reliable claim channel, GOAL-FR-006). It does not
// itself carry a verdict — Goal.LatestVerdict is set only once the claim's
// adjudication completes. Mirrors contracts/components/schemas/Goal.yaml's
// latest_claim.
type Claim struct {
	// Status is the goal_claim.status enum — machine-verifiable constraint,
	// exactly these three values (generated.GoalLatestClaimStatus.Valid()).
	Status generated.GoalLatestClaimStatus `json:"status"`
	// Evidence is the claim's own evidence text — a snapshot of what was
	// submitted, not a grounding excerpt (distinct from
	// task.CriterionVerdict.EvidenceQuote). Required and non-empty
	// (whitespace-trimmed) when Status == met; ignored otherwise.
	Evidence string `json:"evidence,omitempty"`
	// ClaimedAt is when the claim was made.
	ClaimedAt time.Time `json:"claimed_at"`
}

// SupersededCriteriaEntry is one prior criteria/dod snapshot a goal carried
// before a set_goal(mode: update) steering revision replaced it (ADR-081).
// Distinct from TerminalHistoryEntry, which records prior COMPLETED RUNS of
// a re-run task-owned goal, not prior criteria revisions of the current
// run.
type SupersededCriteriaEntry struct {
	SupersededAt time.Time                  `json:"superseded_at"`
	Criteria     []task.AcceptanceCriterion `json:"criteria"`
	DoD          []task.AcceptanceCriterion `json:"dod"`
}

// TerminalHistoryEntry is one prior run's terminal outcome, retained when a
// task-owned goal re-enters active on task re-run (R-04, GOAL-FR-028) — see
// status.go's Reactivate. A session-owned goal never re-enters active from
// terminal, so this stays empty/unused for it.
type TerminalHistoryEntry struct {
	State          generated.GoalState `json:"state"`
	TerminalReason string              `json:"terminal_reason,omitempty"`
	Verdict        *task.JudgeVerdict  `json:"verdict,omitempty"`
	EndedAt        time.Time           `json:"ended_at"`
	AttemptsUsed   int                 `json:"attempts_used"`
	Round          int                 `json:"round"`
}

// Goal is the persisted goal record (ADR-086 D1, GOAL-FR-001/FR-002/FR-006).
// Field names and json tags mirror contracts/components/schemas/Goal.yaml
// (OQ-1's assumption, carried through into F1's shipped schema) so a future
// wire projection of this record needs no field renaming.
//
// This type is this package's own domain struct, not the deeply-nested
// generated.Goal wire type: generated.Goal denormalises every nested object
// (criteria items, the verdict, the claim) into anonymous structs suited to
// JSON-schema codegen, not to being mutated by engine code. Goal.yaml is
// TYPE-ONLY on the wire (D-E, OQ-2 ANSWERED — no GET/PATCH /api/v1/goals/{id}
// exists or is planned), so generated.Goal has no REST/WS consumer this
// package would need to match structurally; where a real wire consumer DOES
// exist for one of Goal's sub-shapes (GoalOwnerKind, GoalSource, GoalState,
// GoalLatestClaimStatus), this struct uses that exact generated type instead
// of a hand-rolled duplicate (R-14's "give the generated GoalState type a
// real consumer").
type Goal struct {
	// GoalID is the unique goal identifier. Left empty by a caller
	// constructing a fresh Goal — Store.Create mints a UUID via
	// pkg/entity's Accessors.SetID, mirroring every other entity store in
	// this codebase.
	GoalID string `json:"goal_id"`

	// OwnerKind and OwnerID together are the goal's single owner reference
	// (GOAL-FR-002) — never inferred, always part of the persisted record.
	OwnerKind generated.GoalOwnerKind `json:"owner_kind"`
	OwnerID   string                  `json:"owner_id"`

	// Source records how this goal's criteria were authored.
	Source generated.GoalSource `json:"source"`

	// Prompt is the raw user intent this goal was set/compiled from.
	Prompt string `json:"prompt"`
	// Definition is the compiled SMART restatement of Prompt (US-3
	// echo-confirm), absent for task_explicit sources.
	Definition string `json:"definition,omitempty"`

	// Criteria and DoD are REAL TYPED LISTS (GOAL-FR-003) — never a
	// serialised string. Reused unchanged from ADR-080's shared type.
	Criteria []task.AcceptanceCriterion `json:"criteria"`
	DoD      []task.AcceptanceCriterion `json:"dod"`

	// MaxRounds is the single budget ceiling (GOAL-FR-024, D-D/D-E) —
	// always stamped from the one global Settings -> Performance value by
	// the caller that creates this record. There is no per-goal override
	// (see doc.go).
	MaxRounds int `json:"max_rounds"`
	// Round is adjudications consumed so far (one round = one
	// adjudication, claim-triggered or idle-settled).
	Round int `json:"round"`
	// AttemptsUsed is attempts consumed so far against this goal's owner's
	// attempt ceiling — a DISTINCT counter from Round (R-03): the two
	// counters are never conflated, even though nothing requires their
	// numbers to differ.
	AttemptsUsed int `json:"attempts_used"`

	// State is the goal's persisted status (GOAL-FR-006/FR-027/FR-028,
	// R-14). See status.go for the phase/transition rules.
	State generated.GoalState `json:"state"`

	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	LastActivityAt time.Time  `json:"last_activity_at"`

	// ActiveSessionID is the session this goal is/was active in, once
	// State != defining. See status.go's Activate/Terminate for the
	// "frozen at its last value on termination, never re-pointed" rule
	// (Goal.yaml's active_session_id description, EC-10).
	ActiveSessionID string `json:"active_session_id,omitempty"`

	// LatestReason is the most recent judge reason, fed forward as
	// steering (evaluator-optimizer pattern) — mirrors
	// GoalStatusFrame.latest_reason.
	LatestReason string `json:"latest_reason,omitempty"`
	// TerminalReason is why the goal reached its current terminal state —
	// distinct from LatestReason, which tracks the latest ROUND's judge
	// reason. Set in the same transition as State (GOAL-FR-028).
	TerminalReason string `json:"terminal_reason,omitempty"`

	LatestClaim   *Claim             `json:"latest_claim,omitempty"`
	LatestVerdict *task.JudgeVerdict `json:"latest_verdict,omitempty"`

	SupersededCriteria []SupersededCriteriaEntry `json:"superseded_criteria,omitempty"`
	TerminalHistory    []TerminalHistoryEntry    `json:"terminal_history,omitempty"`

	// ZeroOutputPushes and QuestionRoundsUsed are the keeper's own durable
	// counters (GOAL-FR-004), relocated onto the goal record so they apply
	// identically to a task-owned and a session-owned goal.
	ZeroOutputPushes   int `json:"zero_output_pushes"`
	QuestionRoundsUsed int `json:"question_rounds_used"`

	// RouteChannel/RouteChatID are the two surviving routing fields (D6) —
	// a keeper follow-up's delivery address on a non-webchat channel.
	RouteChannel string `json:"route_channel,omitempty"`
	RouteChatID  string `json:"route_chat_id,omitempty"`
}

// New constructs a fresh Goal in the defining phase (D2): it exists, is
// readable, and MUST NOT run yet (ADR-086 D2/D3, GOAL-FR-009) — the caller
// activates it separately via (*Goal).Activate once it is bound to a
// session (a chat /goal activates in the same turn per ADR-081 D1; a task's
// goal stays in defining until the task starts and mints its session,
// GOAL-FR-012).
//
// criteria/dod are normalised and validated via task.NormalizeCriteria (ID
// minting, status defaulting, shape validation) before being checked for
// reserved-id collisions (FR-007). dod MUST contain at least one item after
// normalisation (D11/D15, the schema's minItems: 1) — New does not silently
// backfill a floor DoD; that compiler-side floor-layer responsibility
// belongs to whichever engine wave authors the goal (ADR-086 D11's own
// text: "the compiler's built-in floor layer guarantees at least one item
// on every newly-compiled goal").
func New(
	ownerKind generated.GoalOwnerKind,
	ownerID string,
	source generated.GoalSource,
	prompt, definition string,
	criteria, dod []task.AcceptanceCriterion,
	maxRounds int,
	now time.Time,
) (*Goal, error) {
	normCriteria, err := task.NormalizeCriteria(criteria)
	if err != nil {
		return nil, fmt.Errorf("goal: new: criteria: %w", err)
	}
	normDoD, err := task.NormalizeCriteria(dod)
	if err != nil {
		return nil, fmt.Errorf("goal: new: dod: %w", err)
	}

	g := &Goal{
		OwnerKind:          ownerKind,
		OwnerID:            ownerID,
		Source:             source,
		Prompt:             prompt,
		Definition:         definition,
		Criteria:           normCriteria,
		DoD:                normDoD,
		MaxRounds:          maxRounds,
		Round:              0,
		AttemptsUsed:       0,
		State:              generated.GoalStateDefining,
		CreatedAt:          now,
		LastActivityAt:     now,
		ZeroOutputPushes:   0,
		QuestionRoundsUsed: 0,
	}
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("goal: new: %w", err)
	}
	return g, nil
}

// Validate checks Goal's content invariants. It deliberately does NOT
// require GoalID to be set — Store.Create backfills it (via pkg/entity's
// Accessors.SetID) for a freshly constructed record that has none yet, so a
// pre-Create Validate call on a fresh Goal must not reject that normal
// state.
func (g *Goal) Validate() error {
	if g == nil {
		return fmt.Errorf("goal: validate: nil goal")
	}
	if !g.OwnerKind.Valid() {
		return fmt.Errorf("goal: invalid owner_kind %q (GOAL-FR-002)", g.OwnerKind)
	}
	if g.OwnerID == "" {
		return fmt.Errorf("goal: owner_id is required (GOAL-FR-002)")
	}
	if !g.Source.Valid() {
		return fmt.Errorf("goal: invalid source %q", g.Source)
	}
	if g.Prompt == "" {
		return fmt.Errorf("goal: prompt is required")
	}
	if len(g.DoD) == 0 {
		return fmt.Errorf("goal: dod must contain at least one item (schema minItems: 1, D11)")
	}
	if g.MaxRounds < 1 {
		return fmt.Errorf("goal: max_rounds must be >= 1")
	}
	if g.Round < 0 {
		return fmt.Errorf("goal: round must not be negative")
	}
	if g.AttemptsUsed < 0 {
		return fmt.Errorf("goal: attempts_used must not be negative")
	}
	if !g.State.Valid() {
		return fmt.Errorf("goal: invalid state %q", g.State)
	}
	if err := validateCriteriaList(g.Criteria, "criteria"); err != nil {
		return err
	}
	if err := validateCriteriaList(g.DoD, "dod"); err != nil {
		return err
	}
	if g.LatestClaim != nil {
		if err := validateClaim(g.LatestClaim); err != nil {
			return err
		}
	}
	return nil
}
