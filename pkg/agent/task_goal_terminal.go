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
// This is NOT a reintroduction of the deleted terminateGoalRecordForOwner.
// That function was deleted because it was session-owner-only and therefore a
// silent no-op for exactly the records this file exists to end. This one is
// task-owner-only BY DESIGN and performs no transition of its own — it resolves
// the owner and delegates to the single terminateGoalRecordByID, so there is
// still exactly one implementation of the transition itself.
package agent

import (
	"errors"
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// taskGoalDoD returns the Definition of Done recorded on the goal record
// paired with taskID, or nil when the task has none to judge.
//
// nil is returned — never an error — for every "there is nothing to union"
// shape, because each is a legitimate state rather than a fault:
//
//   - no paired goal record at all (goal.ErrOwnerNotFound): a pre-D-C legacy
//     task, or a Scratchpad task that never had one (GOAL-FR-023). Debug only;
//     activateTaskGoal already logs this same condition once at INFO per run,
//     and repeating it per adjudication would add noise, not information.
//   - a record whose DoD list is empty: cannot happen for a record created
//     through the product (goal.Goal.Validate requires a non-empty DoD) but is
//     cheap to tolerate.
//
// A genuine STORE FAULT is different and is logged at Warn: the DoD exists and
// could not be read, so this adjudication is about to judge a narrower set than
// the operator authored. It still returns nil rather than failing the claim —
// D-B's precedent (a reporting obligation must never become a gate) applied to
// the mechanism itself: a transient goal-store read error must not convert a
// worker's legitimate claim into a failed attempt.
func taskGoalDoD(taskID string) []task.AcceptanceCriterion {
	if taskID == "" {
		return nil
	}
	g, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			logger.DebugCF("task_executor",
				"goal: task has no paired goal record — judging acceptance criteria only, with no Definition of Done (GOAL-FR-023)",
				map[string]any{"task_id": taskID})
			return nil
		}
		logger.WarnCF("task_executor",
			"goal: could not read the paired goal record's Definition of Done — this claim is being judged "+
				"against the acceptance criteria ALONE (GOAL-FR-047/FR-048 DoD not enforced for this round)",
			map[string]any{"task_id": taskID, "error": err.Error()})
		return nil
	}
	if len(g.DoD) == 0 {
		return nil
	}
	return g.DoD
}

// persistTaskGoalDoDProjection writes the DoD half of a verdict projection
// back onto the goal record paired with taskID (GOAL-FR-036/FR-040/FR-041).
//
// The task half of the same projection stays where it already was, on
// t.Criteria — the two lists are written to two DIFFERENT stores because they
// LIVE in two different stores, and each side's ids came from the list it is
// written back to, so neither projection can mismatch.
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
		return cur.RecordVerdict(verdict, reason, time.Now().UTC())
	}); uerr != nil {
		logger.WarnCF("task_executor",
			"goal: could not record this round's verdict on the paired goal record",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "error": uerr.Error()})
	}
}

// goalStateForTerminalTask maps a task's terminal disposition onto the goal
// state its paired record must end in, using the SAME vocabulary
// goal_loop.go::clearGoalStatus already uses for a chat goal — so a task goal
// and a chat goal that ended the same way read the same way:
//
//	task done                       -> met        (clearGoalStatus's goalClearNoteMet)
//	task failed, stopped by a user  -> cleared    (clearGoalStatus's goalClearNoteUser)
//	task failed, any other reason   -> exhausted  (clearGoalStatus's default)
//
// `expired` is deliberately NOT produced here: it belongs to the idle-expiry
// calendar sweep (goalIdleExpirySweep), which is about a goal nobody touched
// for days, not about a task that ran and finished.
//
// ok is false for a non-terminal status, so a caller that reaches this with a
// mid-flight task transitions nothing.
func goalStateForTerminalTask(status task.Status, cancelReason task.CancelReason) (generated.GoalState, bool) {
	switch status {
	case task.StatusDone:
		return generated.GoalStateMet, true
	case task.StatusFailed:
		if cancelReason == task.CancelReasonStoppedByUser {
			return generated.GoalStateCleared, true
		}
		return generated.GoalStateExhausted, true
	default:
		return "", false
	}
}

// terminateTaskGoalRecord ends the goal record paired with a task that has
// just reached a terminal status (GOAL-FR-015/FR-027/FR-028).
//
// Call it from EVERY terminal disposition of a task. There are three, and they
// do not share a chokepoint: TaskExecutor.completeTaskWithResult (judged
// done/failed and the attempts-exhausted wind-down), TaskExecutor.failTask
// (an infrastructure failure) and PlanEngine.cancelMemberLocked (a user Stop).
// cancelMemberLocked's own comment already names the other two as its peers for
// exactly this reason — the evidence-gate streak clear has the same shape.
//
// Every outcome is a no-op rather than an error except a genuine store fault:
//
//   - no paired goal record (GOAL-FR-023 legacy/Scratchpad task): nothing to end.
//   - the record is still `defining`: the task terminated without ever starting
//     (Stop on a queued task, or a create-then-cancel). `defining` is the
//     legitimate resting state for a record whose task has not run —
//     goal.Goal.Terminate refuses it outright, and forcing a transition would
//     invent an adjudication that never happened.
//   - the record is already terminal: the chat path (or a previous call) ended
//     it. Idempotent by construction.
//
// Best-effort and non-fatal: the task has ALREADY been written terminal by the
// caller, so a broken goal store must never retroactively change the task's
// own outcome — the same contract TaskExecutor.recordEvidenceBoundary states
// for the evidence repo. A fault is logged at Warn so "the goal record was not
// closed" is never silent, which is precisely how this defect survived to UAT.
func terminateTaskGoalRecord(taskID string, status task.Status, cancelReason task.CancelReason, reason string) {
	if taskID == "" {
		return
	}
	state, ok := goalStateForTerminalTask(status, cancelReason)
	if !ok {
		logger.WarnCF("task_executor",
			"goal: refusing to terminate a task's goal record for a non-terminal task status",
			map[string]any{"task_id": taskID, "status": string(status)})
		return
	}

	g, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		if errors.Is(err, goal.ErrOwnerNotFound) {
			logger.DebugCF("task_executor",
				"goal: terminated task has no paired goal record to end (GOAL-FR-023)",
				map[string]any{"task_id": taskID})
			return
		}
		logger.WarnCF("task_executor",
			"goal: could not look up the paired goal record of a terminated task — the record may be left ACTIVE (GOAL-FR-015)",
			map[string]any{"task_id": taskID, "status": string(status), "error": err.Error()})
		return
	}
	if !g.IsActive() {
		logger.DebugCF("task_executor",
			"goal: paired goal record is not active — nothing to terminate",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "goal_state": string(g.State)})
		return
	}

	if terr := terminateGoalRecordByID(g.GoalID, state, terminalReasonForTask(status, reason)); terr != nil {
		// terminateGoalRecordByID already logged the storage fault itself;
		// this adds the task-side context that call has no way to know.
		logger.WarnCF("task_executor",
			"goal: the paired goal record of a terminated task could not be transitioned — it is still ACTIVE (GOAL-FR-015)",
			map[string]any{"task_id": taskID, "goal_id": g.GoalID, "goal_state": string(state), "error": terr.Error()})
		return
	}
	logger.InfoCF("task_executor", "goal: paired goal record terminated with its task",
		map[string]any{"task_id": taskID, "goal_id": g.GoalID, "goal_state": string(state)})
}

// terminalReasonForTask builds the Goal.TerminalReason text retained on the
// record. It names the task outcome that ended the goal and carries the task's
// own result/reason text when there is one, so a terminal goal record explains
// itself without a reader having to go and find the task.
func terminalReasonForTask(status task.Status, reason string) string {
	if reason == "" {
		return fmt.Sprintf("owning task reached %s", status)
	}
	return fmt.Sprintf("owning task reached %s: %s", status, truncateTaskOutput(reason))
}
