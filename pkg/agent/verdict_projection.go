// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verdict_projection.go is the SINGLE verdict -> criterion-status writer
// (ADR-086 D8, GOAL-FR-036/FR-038/FR-039/FR-040/FR-041; ADR-084 revision 9
// JUDGE-FR-076 step 2). Per the joint delivery plan's C-03 resolution, this
// is the ONE file: pkg/agent/verdict_status_projection.go MUST NEVER exist
// (the merge guard scripts/check-single-verdict-projection.sh enforces
// this). It is called from all three places criteria live (FR-040):
//
//   - the goal record, from goal_triggers.go::runGoalAdjudication
//   - the task record, from task_executor.go::adjudicateClaim
//   - a plan member's definition of done, from
//     plan_engine.go::applyJudgeRoundOutcome
//
// Every function here is a PURE function over its arguments: no store
// access, no persistence, no side effects beyond structured logging of the
// no-op rules below. Each caller persists the returned slice through its
// OWN store's write path — this file has no opinion on session meta,
// pkg/goal.Store, pkg/task.Store or pkg/plan.Store, and never itself decides
// whether a criterion is eligible to be judged (D-B/GOAL-FR-038/FR-039: the
// Judge's Met bool, wherever it came from, is trusted as-is — this file
// never re-derives or second-guesses it from EvidenceQuote/EvidenceSource/
// Provenance/Evidence; those remain reporting-only).
package agent

import (
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// ProjectionStats counts what one projectVerdictOntoCriteria (or
// projectGoalVerdict) call actually did, so "the projection ran and found
// nothing to do" is always distinguishable from "the projection was never
// called" (C-52) — every no-op rule below increments a counter a caller or
// test can assert on.
type ProjectionStats struct {
	// Applied is the number of AcceptanceCriterion entries whose Status was
	// actually overwritten from the verdict (GOAL-FR-036).
	Applied int
	// EphemeralNoOps counts verdict entries whose CriterionID names a
	// synthetic, never-persisted criterion — the soft-tier fallback
	// (judge.go's softTierCriterionID) or the pre-Phase-2 back-compat
	// single-condition criterion (goal.ReservedCriterionIDGoalCondition).
	// GOAL-FR-031: there is no persisted record to write back to, so this is
	// an explicit, counted, warn-logged no-op — never a silent skip and
	// never a store write.
	EphemeralNoOps int
	// UnresolvedNoOps counts verdict entries whose CriterionID matches
	// nothing in the input list at all (C-52 rule 2) — warn-logged and
	// counted, never a silent drop.
	UnresolvedNoOps int
	// Unchanged counts input criteria the verdict said nothing about — their
	// existing Status is kept exactly as it was, never reset to pending and
	// never defaulted to met (NFR-2, C-52 rule 3).
	Unchanged int
	// DuplicateIDs counts an id collision: either two verdict entries for
	// the same CriterionID (the last one by index wins, C-52 rule 4) or two
	// input criteria sharing an id (OQ-15 — detected and counted; no
	// rejection policy is implemented, so every matching input entry is
	// projected identically from the same resolved outcome).
	DuplicateIDs int
}

// add folds b into a and returns the sum — used by projectGoalVerdict to
// merge its two de-unioned calls' stats into one report.
func (a ProjectionStats) add(b ProjectionStats) ProjectionStats {
	return ProjectionStats{
		Applied:         a.Applied + b.Applied,
		EphemeralNoOps:  a.EphemeralNoOps + b.EphemeralNoOps,
		UnresolvedNoOps: a.UnresolvedNoOps + b.UnresolvedNoOps,
		Unchanged:       a.Unchanged + b.Unchanged,
		DuplicateIDs:    a.DuplicateIDs + b.DuplicateIDs,
	}
}

// isEphemeralCriterionID reports whether id names a criterion that is
// synthesized for judging but never persisted (GOAL-FR-031): the soft-tier
// fallback, or the pre-Phase-2 back-compat single-condition criterion
// goal_compile.go::compiledGoalCriteriaFor synthesizes when no compiled
// ladder exists yet. The goal-dod-floor-* prefix is DELIBERATELY excluded
// from this check: unlike the other two reserved ids, the floor DoD IS
// persisted (pkg/goal/criteria.go's validateCriteriaList permits it in the
// dod list) — a floor DoD item's verdict projects exactly like any other DoD
// item, never as a no-op.
func isEphemeralCriterionID(id string) bool {
	return id == softTierCriterionID || id == goal.ReservedCriterionIDGoalCondition
}

// projectVerdictOntoCriteria is the base projection (JUDGE-FR-076 step 2,
// GOAL-FR-036). It returns a NEW slice — criteria is never mutated in place
// — with each entry's Status overwritten from verdict.PerCriterion where the
// ids match (met -> task.CritMet, unmet -> task.CritUnmet), and left exactly
// as it was where they don't (rule 3). A nil verdict, or one with no
// PerCriterion entries, returns an unchanged copy of criteria with zero
// stats: absence of a verdict is never synthesized into a status change
// (NFR-2, mirrors task.JudgeVerdict's own doc comment).
func projectVerdictOntoCriteria(criteria []task.AcceptanceCriterion, verdict *task.JudgeVerdict) ([]task.AcceptanceCriterion, ProjectionStats) {
	var stats ProjectionStats
	out := make([]task.AcceptanceCriterion, len(criteria))
	copy(out, criteria)
	if verdict == nil || len(verdict.PerCriterion) == 0 {
		return out, stats
	}

	// Rule 4 (verdict side): last entry wins per id. Fold outcomes into a
	// map keyed by id, counting an in-verdict duplicate id as it folds.
	outcome := make(map[string]bool, len(verdict.PerCriterion))
	seenInVerdict := make(map[string]bool, len(verdict.PerCriterion))
	for _, cv := range verdict.PerCriterion {
		if seenInVerdict[cv.CriterionID] {
			stats.DuplicateIDs++
			logger.WarnCF("agent",
				"verdict projection: duplicate criterion id within one verdict's per_criterion list — the last entry wins (OQ-15, no rejection policy)",
				map[string]any{"criterion_id": cv.CriterionID})
		}
		seenInVerdict[cv.CriterionID] = true
		outcome[cv.CriterionID] = cv.Met
	}

	// Rule 4 (input side): a duplicate id in the INPUT list is detected too
	// — every entry sharing that id is projected identically from the same
	// resolved outcome value, and the collision is still counted so it
	// stays discoverable (OQ-15's "detect, log and count" assumption).
	seenInInput := make(map[string]bool, len(out))
	for _, c := range out {
		if c.ID == "" {
			continue
		}
		if seenInInput[c.ID] {
			stats.DuplicateIDs++
			logger.WarnCF("agent",
				"verdict projection: duplicate criterion id in the input list — every matching entry is projected identically (OQ-15, no rejection policy)",
				map[string]any{"criterion_id": c.ID})
		}
		seenInInput[c.ID] = true
	}

	resolved := make(map[string]bool, len(out))
	for id, met := range outcome {
		if isEphemeralCriterionID(id) {
			stats.EphemeralNoOps++
			logger.WarnCF("agent",
				"verdict projection: verdict entry targets an ephemeral criterion id — no persisted record exists to write back to (GOAL-FR-031)",
				map[string]any{"criterion_id": id})
			continue
		}
		matched := false
		for i := range out {
			if out[i].ID != id {
				continue
			}
			if met {
				out[i].Status = task.CritMet
			} else {
				out[i].Status = task.CritUnmet
			}
			resolved[id] = true
			matched = true
			stats.Applied++
		}
		if !matched {
			stats.UnresolvedNoOps++
			logger.WarnCF("agent",
				"verdict projection: verdict entry matches no criterion in the input list — counted, never a silent skip (C-52)",
				map[string]any{"criterion_id": id})
		}
	}

	for _, c := range out {
		if c.ID != "" && !resolved[c.ID] {
			stats.Unchanged++
		}
	}

	return out, stats
}

// projectGoalVerdict is GOAL-FR-041's de-union: goal_compile.go's
// compiledGoalCriteriaFor hands the Judge criteria ∪ dod, so
// verdict.PerCriterion legitimately carries ids from BOTH lists. This splits
// the verdict by id membership BEFORE calling projectVerdictOntoCriteria
// separately on each list, so neither call reports the other list's ids as
// "matches nothing" (C-52 rule 2 would otherwise fire on every DoD id when
// checked against criteria alone, and vice versa).
//
// An id that resolves to neither list — a genuinely stray verdict entry, or
// one of the two ephemeral ids (isEphemeralCriterionID) — is routed to the
// criteria-side call, so it is still warn-logged and counted exactly once by
// projectVerdictOntoCriteria's own rules, rather than dropped by the split
// itself.
func projectGoalVerdict(criteria, dod []task.AcceptanceCriterion, verdict *task.JudgeVerdict) (updatedCriteria, updatedDoD []task.AcceptanceCriterion, stats ProjectionStats) {
	if verdict == nil || len(verdict.PerCriterion) == 0 {
		uc, cs := projectVerdictOntoCriteria(criteria, nil)
		ud, ds := projectVerdictOntoCriteria(dod, nil)
		return uc, ud, cs.add(ds)
	}

	inDoD := make(map[string]bool, len(dod))
	inCriteria := make(map[string]bool, len(criteria))
	for _, c := range criteria {
		inCriteria[c.ID] = true
	}
	for _, c := range dod {
		inDoD[c.ID] = true
	}

	criteriaVerdict := *verdict
	dodVerdict := *verdict
	criteriaVerdict.PerCriterion = nil
	dodVerdict.PerCriterion = nil

	for _, cv := range verdict.PerCriterion {
		if inDoD[cv.CriterionID] && !inCriteria[cv.CriterionID] {
			dodVerdict.PerCriterion = append(dodVerdict.PerCriterion, cv)
			continue
		}
		// The criteria list, both ephemeral ids, and any genuinely stray id
		// all land here — see doc comment above.
		criteriaVerdict.PerCriterion = append(criteriaVerdict.PerCriterion, cv)
	}

	updatedCriteria, cStats := projectVerdictOntoCriteria(criteria, &criteriaVerdict)
	updatedDoD, dStats := projectVerdictOntoCriteria(dod, &dodVerdict)
	return updatedCriteria, updatedDoD, cStats.add(dStats)
}
