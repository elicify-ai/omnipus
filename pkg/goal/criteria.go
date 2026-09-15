// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// The reserved, non-UUID criterion ids (GOAL-FR-007). A persisted goal's
// Criteria/DoD list MUST NOT mint any of these — they are reserved for
// ephemeral or fixed-identity criteria minted elsewhere in the codebase:
//
//   - ReservedCriterionIDSoftTierImplicit is pkg/agent/judge.go's
//     softTierCriterionID — the ephemeral soft-tier fallback criterion,
//     never itself persisted onto a goal record.
//   - ReservedCriterionIDGoalCondition is pkg/agent/goal_compile.go's
//     compiled /goal condition criterion id.
//   - ReservedCriterionIDDoDFloorPrefix is the prefix
//     pkg/agent/goal_compile.go's built-in floor DoD items use
//     ("goal-dod-floor-no-secrets", "goal-dod-floor-grounded-claims", …).
//
// task.NormalizeCriteria alone does not close this gap: it mints a UUID
// only when a criterion's id is ABSENT — an explicit, caller-supplied id
// that happens to collide with one of these reserved values passes through
// NormalizeCriteria unchanged. validateCriteriaList (below) is what
// actually enforces the namespace for everything this package persists.
const (
	ReservedCriterionIDSoftTierImplicit = "soft-tier-implicit"
	ReservedCriterionIDGoalCondition    = "goal-condition"
	ReservedCriterionIDDoDFloorPrefix   = "goal-dod-floor-"
)

// IsReservedCriterionID reports whether id is one of the three reserved,
// non-UUID criterion id forms (GOAL-FR-007) that must never be minted for a
// persisted criterion.
func IsReservedCriterionID(id string) bool {
	if id == ReservedCriterionIDSoftTierImplicit || id == ReservedCriterionIDGoalCondition {
		return true
	}
	return strings.HasPrefix(id, ReservedCriterionIDDoDFloorPrefix)
}

// validateCriteriaList validates a criteria/dod list's shape (delegating to
// task.NormalizeCriteria, which is authoritative for AcceptanceCriterion
// shape validation and does not mutate the caller's slice) and additionally
// rejects any criterion carrying a reserved id (FR-007 — the check
// NormalizeCriteria itself does not perform).
func validateCriteriaList(list []task.AcceptanceCriterion, listName string) error {
	if _, err := task.NormalizeCriteria(list); err != nil {
		return fmt.Errorf("goal: %s: %w", listName, err)
	}
	for i, c := range list {
		if c.ID == "" || !IsReservedCriterionID(c.ID) {
			continue
		}
		// GOAL-FR-007 reserves three id forms, but they are reserved for two
		// DIFFERENT reasons and only one of them is "never persisted".
		//
		//   - soft-tier-implicit and goal-condition are EPHEMERAL: minted at
		//     judge/compile time, never written to a goal record. Rejected in
		//     both lists.
		//   - the goal-dod-floor-* prefix is FIXED-IDENTITY, not ephemeral. The
		//     floor DoD is deliberately persisted with stable sentinel ids so it
		//     is byte-stable across reloads and so the two constructors that
		//     build it — pkg/agent/goal_compile.go::newFloorDoD and
		//     pkg/tools/set_goal.go::setGoalFloorDoD — produce records that are
		//     "indistinguishable on disk" (both constructors say so in terms).
		//     Banning it outright made every set_goal call that omits an
		//     explicit dod fail, because that is the path that backfills it.
		//
		// So the prefix is legal in the DoD list, where it belongs and where its
		// own name points, and illegal in the criteria list, where it would be a
		// namespace collision. Found by wave E4 when the re-pointed WriteRecord
		// first drove a real set_goal call through this validator.
		if listName == "dod" && strings.HasPrefix(c.ID, ReservedCriterionIDDoDFloorPrefix) {
			continue
		}
		return fmt.Errorf("goal: %s[%d]: id %q is reserved and MUST NOT be minted for a persisted criterion (GOAL-FR-007)", listName, i, c.ID)
	}
	return nil
}

// SetCriteria replaces g.Criteria with a normalised, validated copy of
// criteria. It does not touch DoD and does not snapshot the prior list —
// use SupersedeCriteria when the caller needs the superseded-criteria
// history (ADR-081 set_goal(mode: update)) preserved.
func (g *Goal) SetCriteria(criteria []task.AcceptanceCriterion, now time.Time) error {
	normalized, err := task.NormalizeCriteria(criteria)
	if err != nil {
		return fmt.Errorf("goal: set criteria: %w", err)
	}
	if err := validateCriteriaList(normalized, "criteria"); err != nil {
		return err
	}
	g.Criteria = normalized
	g.LastActivityAt = now
	return nil
}

// SetDoD replaces g.DoD with a normalised, validated copy of dod. dod MUST
// contain at least one item after normalisation (D11/D15, the schema's
// minItems: 1) — SetDoD refuses to persist an empty definition of done.
func (g *Goal) SetDoD(dod []task.AcceptanceCriterion, now time.Time) error {
	normalized, err := task.NormalizeCriteria(dod)
	if err != nil {
		return fmt.Errorf("goal: set dod: %w", err)
	}
	if len(normalized) == 0 {
		return fmt.Errorf("goal: set dod: dod must contain at least one item (schema minItems: 1, D11)")
	}
	if err := validateCriteriaList(normalized, "dod"); err != nil {
		return err
	}
	g.DoD = normalized
	g.LastActivityAt = now
	return nil
}

// SupersedeCriteria snapshots g's CURRENT Criteria and DoD into
// SupersededCriteria (ADR-081's set_goal(mode: update) steering revision),
// then replaces both lists with the normalised, validated newCriteria and
// newDoD. newDoD MUST contain at least one item after normalisation, same
// as SetDoD — a steering revision may not leave the goal with no definition
// of done.
func (g *Goal) SupersedeCriteria(newCriteria, newDoD []task.AcceptanceCriterion, now time.Time) error {
	normCriteria, err := task.NormalizeCriteria(newCriteria)
	if err != nil {
		return fmt.Errorf("goal: supersede criteria: criteria: %w", err)
	}
	if vErr := validateCriteriaList(normCriteria, "criteria"); vErr != nil {
		return vErr
	}
	normDoD, err := task.NormalizeCriteria(newDoD)
	if err != nil {
		return fmt.Errorf("goal: supersede criteria: dod: %w", err)
	}
	if len(normDoD) == 0 {
		return fmt.Errorf("goal: supersede criteria: dod must contain at least one item (schema minItems: 1, D11)")
	}
	if err := validateCriteriaList(normDoD, "dod"); err != nil {
		return err
	}

	g.SupersededCriteria = append(g.SupersededCriteria, SupersededCriteriaEntry{
		SupersededAt: now,
		Criteria:     g.Criteria,
		DoD:          g.DoD,
	})
	g.Criteria = normCriteria
	g.DoD = normDoD
	g.LastActivityAt = now
	return nil
}

// Restate replaces an ACTIVE goal's prompt with a new operator intent — a
// prose `/goal <intent>` issued while the goal is still active (ADR-081
// D1/US-5: "the working prompt is replaced by the restated intent"). The
// GoalID and every budget counter are deliberately untouched (a restate is
// the SAME goal generation, work-first-goal-flow-spec round-2 M-7), but the
// compiled definition is NOT carried across: Definition, Criteria and DoD were
// authored for the PREVIOUS prompt, so keeping them would leave one record
// describing two different pieces of work — the new prompt on top, the old
// acceptance ladder underneath — and the Judge would then adjudicate a claim
// about the new work against the old criteria (UAT E-1).
//
// So when the prompt actually changes, the current Criteria/DoD are moved
// into SupersededCriteria (history, never erased — D9's spirit), Criteria is
// emptied, DoD is reset to floorDoD, and the per-definition outcome fields
// (LatestClaim, LatestVerdict, LatestReason) are cleared because they judged
// the superseded definition. That is ADR-081 D1's legal transient state
// ("active, no record registered yet"): the working agent registers a record
// for the new intent via set_goal, the engine's post-turn registration nudge
// fires if it does not, and until one exists the Judge is given the new
// prompt itself as the criterion (compiledGoalCriteriaFor's condition
// fallback) — never the stale ladder.
//
// A restate to the identical prompt (whitespace-trimmed) changes nothing and
// reports changed=false. floorDoD must be non-empty (D11, the schema's
// minItems: 1). Only an active goal may be restated; a terminal goal is never
// re-opened this way (a fresh `/goal` mints a new record instead).
func (g *Goal) Restate(prompt string, floorDoD []task.AcceptanceCriterion, now time.Time) (changed bool, err error) {
	if g.State != generated.GoalStateActive {
		return false, fmt.Errorf("goal: restate %q: not active (state=%q)", g.GoalID, g.State)
	}
	newPrompt := strings.TrimSpace(prompt)
	if newPrompt == "" {
		return false, fmt.Errorf("goal: restate %q: prompt is required", g.GoalID)
	}
	if newPrompt == strings.TrimSpace(g.Prompt) {
		return false, nil
	}
	normFloor, err := task.NormalizeCriteria(floorDoD)
	if err != nil {
		return false, fmt.Errorf("goal: restate %q: floor dod: %w", g.GoalID, err)
	}
	if len(normFloor) == 0 {
		return false, fmt.Errorf("goal: restate %q: floor dod must contain at least one item (schema minItems: 1, D11)", g.GoalID)
	}
	if err := validateCriteriaList(normFloor, "dod"); err != nil {
		return false, err
	}

	if len(g.Criteria) > 0 {
		g.SupersededCriteria = append(g.SupersededCriteria, SupersededCriteriaEntry{
			SupersededAt: now,
			Criteria:     g.Criteria,
			DoD:          g.DoD,
		})
	}
	g.Prompt = newPrompt
	g.Definition = ""
	g.Criteria = nil
	g.DoD = normFloor
	g.LatestClaim = nil
	g.LatestVerdict = nil
	g.LatestReason = ""
	g.LastActivityAt = now
	return true, nil
}
