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

// IsTerminalState reports whether s is one of the four terminal
// generated.GoalState values (GOAL-FR-027/FR-028): met, exhausted, expired,
// cleared. defining and active are not terminal.
func IsTerminalState(s generated.GoalState) bool {
	switch s {
	case generated.GoalStateMet, generated.GoalStateExhausted,
		generated.GoalStateExpired, generated.GoalStateCleared:
		return true
	default:
		return false
	}
}

// Activate transitions a goal from the defining phase into the active
// phase (ADR-086 D2, GOAL-FR-010): binds it to exactly one session and
// stamps StartedAt. It is a no-op-refusing state check, not a full
// activation implementation — the loop-starting side of activation belongs
// to the engine wave that calls this (E4/E8/E12), not to this package.
func (g *Goal) Activate(sessionID string, now time.Time) error {
	if g.State != generated.GoalStateDefining {
		return fmt.Errorf("goal: activate %q: not in defining phase (state=%q)", g.GoalID, g.State)
	}
	if sessionID == "" {
		return fmt.Errorf("goal: activate %q: session id is required", g.GoalID)
	}
	startedAt := now
	g.State = generated.GoalStateActive
	g.ActiveSessionID = sessionID
	g.StartedAt = &startedAt
	g.LastActivityAt = now
	return nil
}

// Terminate transitions an active goal to one of the four terminal states
// (GOAL-FR-027/FR-028, D9): a STATUS TRANSITION on a retained record, never
// field-zeroing erasure. The record survives with its criteria, their final
// statuses, the verdict, the reason and ActiveSessionID all intact.
//
// ActiveSessionID is deliberately left unchanged here (Goal.yaml's own
// description: "cleared [i.e. settled/frozen] — never re-pointed — once the
// goal reaches a terminal state ... the terminal record still names the
// session that carried it via the LAST value this field held before the
// transition"). Terminate freezes it at whatever it already holds; it never
// blanks it to empty and never re-points it to a different session — only
// Reactivate (task-owned re-entry, R-04) ever changes it again.
func (g *Goal) Terminate(state generated.GoalState, reason string, now time.Time) error {
	if !IsTerminalState(state) {
		return fmt.Errorf("goal: terminate %q: %q is not a terminal state", g.GoalID, state)
	}
	if g.State != generated.GoalStateActive {
		return fmt.Errorf("goal: terminate %q: not active (state=%q)", g.GoalID, g.State)
	}
	g.State = state
	g.TerminalReason = reason
	g.LastActivityAt = now
	return nil
}

// Reactivate re-enters a TERMINAL task-owned goal into the active phase
// (R-04, the answer to OQ-10): "a terminal task-owned goal re-enters active
// when its task is re-run, resetting attempts_used and rounds_used to 0,
// clearing latest_reason and the current verdict, and appending the prior
// verdict to a retained history array rather than overwriting it."
//
// A session-owned goal has no such re-entry edge (a terminal chat goal
// stays terminal, GOAL-FR-002's OwnerKind description) — Reactivate refuses
// any goal whose OwnerKind is not task.
//
// The prior run's outcome (state, terminal reason, verdict, attempts and
// rounds consumed) is appended to TerminalHistory BEFORE those counters are
// reset, so a task's full adjudication history across every re-run survives
// (D9's "records are never erased" extended to the re-run case).
//
// EVERY per-run counter resets, not only the two R-04 names (review finding
// 11). AttemptsUsed and Round were reset from the first revision; the
// keeper's OWN two durable counters — ZeroOutputPushes (the bounded
// continue-push ladder, GOAL-FR-017) and QuestionRoundsUsed (the forced
// question budget) — were not, and a task whose FIRST run spent them came
// back with both bounds already tripped: the keeper ladder was dead for
// every later run of that task, silently, because nothing in the re-run path
// ever looked at a counter it did not reset. They are per-RUN budgets on a
// record that outlives the run, so re-entry is exactly where they zero.
//
// The per-criterion projected statuses (GOAL-FR-036's verdict→criterion
// projection, task.CriterionStatus) reset to CritPending on both lists for
// the same reason: they are the PRIOR run's judgement, and a fresh run that
// starts with criteria already painted met/unmet shows the user a verdict
// that was never rendered against this run's work. The prior run's statuses
// are not lost — the TerminalHistory entry appended above retains its
// verdict, which is where a past run's per-criterion outcome belongs.
// The criteria TEXT, ids, authorship and clause counts are untouched: a
// re-run judges the SAME ladder, only with a clean slate of outcomes.
func (g *Goal) Reactivate(sessionID string, now time.Time) error {
	if g.OwnerKind != generated.GoalOwnerKindTask {
		return fmt.Errorf("goal: reactivate %q: only a task-owned goal may re-enter active from terminal (owner_kind=%q, R-04)", g.GoalID, g.OwnerKind)
	}
	if !IsTerminalState(g.State) {
		return fmt.Errorf("goal: reactivate %q: not terminal (state=%q)", g.GoalID, g.State)
	}
	if sessionID == "" {
		return fmt.Errorf("goal: reactivate %q: session id is required", g.GoalID)
	}

	g.TerminalHistory = append(g.TerminalHistory, TerminalHistoryEntry{
		State:          g.State,
		TerminalReason: g.TerminalReason,
		Verdict:        g.LatestVerdict,
		EndedAt:        g.LastActivityAt,
		AttemptsUsed:   g.AttemptsUsed,
		Round:          g.Round,
	})

	startedAt := now
	g.State = generated.GoalStateActive
	g.ActiveSessionID = sessionID
	g.AttemptsUsed = 0
	g.Round = 0
	g.LatestReason = ""
	g.TerminalReason = ""
	g.LatestVerdict = nil
	g.ZeroOutputPushes = 0
	g.QuestionRoundsUsed = 0
	resetCriterionStatuses(g.Criteria)
	resetCriterionStatuses(g.DoD)
	g.StartedAt = &startedAt
	g.LastActivityAt = now
	return nil
}

// resetCriterionStatuses returns every criterion in list to the unjudged
// CritPending status, in place. Used by Reactivate to clear the PRIOR run's
// projected per-criterion outcomes without disturbing anything else on the
// criterion (see Reactivate's doc comment).
func resetCriterionStatuses(list []task.AcceptanceCriterion) {
	for i := range list {
		list[i].Status = task.CritPending
	}
}
