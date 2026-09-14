// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_terminal.go closes the two task-side halves of the goal record's
// life that ADR-086 left wired on the chat side only. Both were found by UAT
// against a live gateway, not by a test.
//
// Half 1 — taskGoalDoD (the READ half, ADR-080 D-DOD / GOAL-FR-047/FR-048).
// A task's Definition of Done lives EXCLUSIVELY on its paired goal record
// (task.Task has no Dod field at all — see Task.Criteria's own doc comment).
// Task creation REFUSES with 400 unless a DoD distinct from the acceptance
// criteria is supplied, and then nothing judged it: TaskExecutor.adjudicateClaim
// fed the Judge `t.Criteria` alone and never opened the goal record, so every
// terminated task's goal record read `dod: [pending]` — including tasks that
// reached `done` with `criteria: [met]`. The floor-DoD items
// (goal-dod-floor-no-secrets, goal-dod-floor-grounded-claims) are exactly the
// protections a never-adjudicated DoD silently removes.
//
// The chat path never had this hole: goal_compile.go::compiledGoalCriteriaFor
// unions Criteria ∪ DoD for goal_triggers.go::runGoalAdjudication, and its own
// doc comment states the rule this file now applies to the task path verbatim —
// "Without this union the DoD would be defined, confirmed, and never scored
// (ADR-080 R-C1)". GOAL-FR-013's "one code path for both owner kinds" is what
// makes the divergence a defect rather than a design.
//
// Half 2 — terminateTaskGoalRecord (the WRITE half, GOAL-FR-015/FR-027/FR-028).
// goal_loop.go::terminateGoalRecordByID is owner-kind agnostic and always was
// (its doc comment: "keyed by goal id, which serves BOTH owner kinds
// identically"), but its only two callers are chat surfaces — clearGoal
// (`/goal clear`) and goalIdleExpirySweep. Nothing on the TASK terminal path
// ever called it, so a task reaching done/failed/stopped left its goal record
// `state: active` forever: three terminated UAT tasks left three active,
// round-0 records behind, contradicting the very tasks they belonged to.
//
// The first fix for Half 2 shipped an UNEXPORTED helper here with three call
// sites and a doc comment asserting those three were the terminal writers.
// Review finding C1: there are at least seven, and three of them live in
// pkg/gateway, pkg/tools and pkg/sysagent/tools where an unexported pkg/agent
// symbol is not merely unused but unreachable. The transition therefore moved
// to pkg/tools.TerminateTaskGoalRecord — one implementation, shared by all
// four writer packages — and what remains in this file is the pkg/agent-side
// wrapper plus this narrative. Read that function's section header for why the
// obvious chokepoint (task.Store's own status write) is impossible.
package agent

import (
	"errors"
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// taskGoalDoD returns the Definition of Done recorded on the goal record
// paired with taskID.
//
// (nil, nil) means "this task genuinely has no Definition of Done to union in",
// and is returned for the two shapes that are legitimate states rather than
// faults:
//
//   - no paired goal record at all (goal.ErrOwnerNotFound): a pre-D-C legacy
//     task, or a Scratchpad task that never had one (GOAL-FR-023). Debug only;
//     activateTaskGoal already logs this same condition once at INFO per run,
//     and repeating it per adjudication would add noise, not information.
//   - a record whose DoD list is empty: cannot happen for a record created
//     through the product (goal.Goal.Validate requires a non-empty DoD) but is
//     cheap to tolerate.
//
// A STORE FAULT returns an ERROR, and the caller must not let the claim be
// judged at all (review finding C2). This function used to return a bare nil
// with one WARN for every fault, which made the mandatory Definition of Done
// FAIL OPEN: nil is indistinguishable from "no DoD" at the call site, so the
// judged set silently narrowed to the acceptance criteria alone, a Met verdict
// followed, and completeTaskWithResult wrote StatusDone. One unreadable file
// and the whole gate — including the floor-DoD items goal-dod-floor-no-secrets
// and goal-dod-floor-grounded-claims — evaporated, while the task read Done.
//
// THE D-B QUESTION, ANSWERED. The old behaviour cited operator decision D-B
// ("evidence tiers and provenance are REPORTING obligations, never gates: no
// tier value, no provenance value and no quote check may flip a criterion's
// outcome, withhold a verdict or downgrade a met"). D-B governs what may be
// done to a VERDICT the Judge has already returned — it forbids the grounding
// machinery from overruling the Judge's own judgement. It says nothing about
// whether a criterion the operator authored reaches the Judge in the first
// place. Refusing to adjudicate an input set we know to be incomplete flips no
// criterion, downgrades no met and withholds no verdict; it declines to
// manufacture one. The precedent that DOES apply is two lines of REST away:
// rest_tasks.go's toWireTask returns 500 on this identical unreadable record,
// with the reasoning "a reader seeing an empty DoD would conclude the task has
// none and act on it" (SF-6). It would be incoherent for the DISPLAY of a
// task's DoD to fail closed while the GATE it represents failed open.
//
// The unreadable-record case needs its own detection because goal.Store hides
// it: GetByOwner delegates to List and DISCARDS List's `skipped` return, so a
// goal record file that exists on disk but cannot be unmarshalled resolves to
// ErrOwnerNotFound — byte-identical to a task that never had a record. That is
// the exact shape the C2 scenario produces (a partially-written record under
// disk pressure, or a second process on Windows where fileutil.WithFlock is a
// documented no-op, ADR-054 §5). So ErrOwnerNotFound is re-checked against
// List's own `skipped`: any unreadable record on disk means this task's DoD
// cannot be proven absent, and "cannot be proven absent" is not "absent".
// A skipped record alongside a record we DID resolve for this task is
// irrelevant and deliberately does not fault — that one belongs to some other
// owner, and blocking every task's adjudication on it would be a denial of
// service, not a safeguard.
func taskGoalDoD(taskID string) ([]task.AcceptanceCriterion, error) {
	if taskID == "" {
		return nil, nil
	}
	gs := resolveGoalRecordStore()
	g, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
	switch {
	case err == nil:
		if len(g.DoD) == 0 {
			return nil, nil
		}
		return g.DoD, nil

	case errors.Is(err, goal.ErrOwnerNotFound):
		_, skipped, lErr := gs.List()
		if lErr != nil {
			return nil, fmt.Errorf(
				"the goal store could not be listed, so this task's Definition of Done cannot be "+
					"proven absent: %w", lErr)
		}
		if len(skipped) > 0 {
			return nil, fmt.Errorf(
				"%d goal record(s) on disk are unreadable (%v), so this task's Definition of Done "+
					"cannot be proven absent", len(skipped), skipped)
		}
		logger.DebugCF("task_executor",
			"goal: task has no paired goal record — judging acceptance criteria only, with no Definition of Done (GOAL-FR-023)",
			map[string]any{"task_id": taskID})
		return nil, nil

	default:
		return nil, fmt.Errorf("read the paired goal record's Definition of Done: %w", err)
	}
}

// persistTaskGoalDoDProjection writes the DoD half of a verdict projection
// back onto the goal record paired with taskID (GOAL-FR-036/FR-040/FR-041).
//
// The task half of the same projection is written onto t.Criteria by the
// caller. The goal record's OWN criteria[] mirror — which this function never
// wrote — is projected by recordTaskGoalVerdict, in the same store write that
// records the verdict (UAT defect 3). The DoD statuses that function writes are
// the ones written here, so this write is now redundant but harmless; removing
// it needs a call-site change in task_executor.go::adjudicateClaim.
//
// Called on BOTH met and unmet outcomes (FR-040 is not conditioned on the
// overall verdict) and only when the projection actually changed something, so
// a no-op verdict never touches the goal store.
func persistTaskGoalDoDProjection(taskID string, projectedDoD []task.AcceptanceCriterion) {
	gstore := resolveGoalRecordStore()
	g, err := gstore.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		// ErrOwnerNotFound is unreachable here (taskGoalDoD returned a
		// non-empty DoD moments ago, so a record existed), but a record
		// deleted concurrently must not crash an adjudication — report it the
		// same way as any other read fault.
		logger.WarnCF("task_executor",
			"goal: could not re-read the paired goal record to persist the Definition-of-Done verdict projection (GOAL-FR-036)",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return
	}
	if _, uerr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		cur.DoD = projectedDoD
		return nil
	}); uerr != nil {
		logger.WarnCF("task_executor",
			"goal: could not persist the Definition-of-Done verdict projection onto the goal record (GOAL-FR-036)",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "error": uerr.Error()})
	}
}

// recordTaskGoalVerdict stamps the adjudicated verdict onto the paired goal
// record (LatestVerdict/LatestReason/Round) so the record this task's goal
// terminates as is a real adjudication record rather than an empty shell.
//
// This mirrors goal_triggers.go::runGoalAdjudication's own RecordVerdict call
// and the review finding that put it there: a met goal that terminated having
// persisted NOTHING left LatestVerdict nil and Round at its pre-adjudication
// value, "directly contradicting Goal.Terminate's own contract". For a
// task-owned goal the consequence is worse, because Goal.Reactivate builds the
// next run's TerminalHistory entry out of exactly these fields — a successful
// run would otherwise archive {Verdict: nil, Round: 0}, i.e. no history at all
// for the only outcome anyone wants a history of.
//
// It also projects the verdict onto the record's OWN Criteria and DoD lists
// (ADR-086 D8, GOAL-FR-036/FR-040/FR-041), inside the same store write that
// records the verdict — UAT defect 3. Nothing on the task path ever wrote the
// goal record's criteria[] mirror: adjudicateClaim projected the criteria half
// onto the task record and persistTaskGoalDoDProjection wrote the DoD half
// alone, so record eed50f19 read `criteria: [pending]` beside a latest_verdict
// that judged that very criterion met. Projecting here, against the record's
// current lists, keeps the verdict and the ticks in one atomic write — they can
// never disagree — and uses the same de-union (projectGoalVerdict) the chat
// path's runGoalAdjudication applies.
//
// Best-effort by design: the task's own verdict has already been applied by the
// caller, so a goal-store fault must not retroactively change the task outcome.
func recordTaskGoalVerdict(taskID string, verdict *task.JudgeVerdict, reason string) {
	if verdict == nil {
		return
	}
	gstore := resolveGoalRecordStore()
	g, err := gstore.GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if !errors.Is(err, goal.ErrOwnerNotFound) {
			logger.WarnCF("task_executor",
				"goal: could not read the paired goal record to record this round's verdict",
				map[string]any{"task_id": taskID, "error": err.Error()})
		}
		return
	}
	if !g.IsActive() {
		// A record still `defining` (the task never activated it) or already
		// terminal has no round to advance. Not a fault.
		return
	}
	if _, uerr := gstore.Update(g.GoalID, func(cur *goal.Goal) error {
		if uc, ud, stats := projectGoalVerdict(cur.Criteria, cur.DoD, verdict); stats.Applied > 0 {
			cur.Criteria = uc
			cur.DoD = ud
		}
		return cur.RecordVerdict(verdict, reason, time.Now().UTC())
	}); uerr != nil {
		logger.WarnCF("task_executor",
			"goal: could not record this round's verdict on the paired goal record",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "error": uerr.Error()})
	}
}

// terminateTaskGoalRecord is this package's entry point to the ONE shared
// task-terminal -> goal-terminal transition, tools.TerminateTaskGoalRecord.
//
// The transition used to be implemented HERE, unexported, and that is review
// finding C1: three of at least seven real terminal writers live in
// pkg/gateway, pkg/tools and pkg/sysagent/tools, none of which can reach an
// unexported pkg/agent symbol, so more than half the writers could not have
// called this hook even if their authors had wanted to. The implementation
// moved to pkg/tools — the only package all four writer packages already
// import that can also see pkg/goal — and this wrapper exists purely so
// pkg/agent's three call sites (completeTaskWithResult, failTask,
// PlanEngine.cancelMemberLocked) keep their short, store-free signature.
// Read tools.TerminateTaskGoalRecord's section header for the full rationale,
// including why a real store-level chokepoint is impossible here.
//
// On the deliberate near-duplication with goal_loop.go's
// terminateGoalRecordByID: that function remains the CHAT/session-owner
// transition and is untouched. The task-owner transition is now a single
// implementation shared by every task writer, which is the property that was
// actually missing — one unexported copy reachable by one package out of four
// is not "exactly one implementation", it is an unreachable one.
func terminateTaskGoalRecord(taskID string, status task.Status, cancelReason task.CancelReason, reason string) {
	tools.TerminateTaskGoalRecord(resolveGoalRecordStore(), taskID, status, cancelReason, reason)
}
