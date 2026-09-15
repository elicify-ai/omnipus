// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop.go implements `/goal <condition>` (ADR-049 D6/D7, spec Part B
// US-8): a proof-driven session loop where a round = one worker turn plus
// its judge evaluation (D7/MIN-001). Command parsing/persistence lives in
// applyGoalCommandPrompt (the AgentLoop.handleCommand rewrite-hook
// precedent, mirroring applyMemoryCommandPrompt); the judge-gated round
// advance itself lives in checkGoalLoopAfterTurn, called once from
// runAgentLoop right after every natural turn stop (fast no-op unless the
// turn's session carries an active goal in its UnifiedMeta).
//
// Origin gating (Gap #8/r2, R6): applyGoalCommandPrompt requires
// opts.UserInitiated — a cron/async/task/sub-turn turn's "/goal ..." text is
// NOT matched at all and passes through as ordinary chat content.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// cancelOrphanedClarifyCard cancels any AskUserQuestion card parked on this
// session WITHOUT dispatching a resume turn (ADR-088 D9/FR-028 —
// reg.CancelByUser is the WRONG primitive here: it injects a resume turn the
// caller has no context to act on). Re-homed by ADR-088: its OLD call sites
// (inside the now-deleted emitGoalClarificationCard, cleaning up a
// just-created card's own persist failure) are gone; its NEW call sites are
// a fresh goal activation superseding a stale card from an earlier attempt
// (E1/S-32) and clearGoal (a `/goal clear` must not leave an orphaned card
// behind, E7/S-41). A no-op when no registry is wired or nothing is parked
// for this session.
func (al *AgentLoop) cancelOrphanedClarifyCard(reg tools.AskUserQuestionRegistry, sessionID string) {
	if reg == nil || sessionID == "" {
		return
	}
	if reg.CancelOnSessionStop(sessionID) {
		logger.InfoCF("agent", "goal: cancelled a parked clarify card without dispatching a resume",
			map[string]any{"session_id": sessionID})
	}
}

// goalClearNoteMet / goalClearNoteUser are the two `clearGoal` note literals
// clearGoal itself pattern-matches to pick a pill state (below) — named so
// every call site and the match live in exactly one place instead of two
// independently-typed string literals drifting apart.
//
// goalIdleExpiredNotePrefix is the third recognized literal shape (ADR-086
// GOAL-FR-028, this wave): goalIdleExpirySweep's note is always
// fmt.Sprintf("idle-expired after %d day(s)", maxDays) — matched by prefix
// (not equality, since the day count varies) so idle expiry gets its own
// terminal pill/state distinct from a genuine round- or budget-exhaustion
// brake. Every OTHER note (round-bound-reached, budget-exhausted, and their
// variants across every call site — including handleBareGoalClaim's, wave
// E12, which this function's signature intentionally stays stable for) falls
// through to the exhausted/failed default, unchanged.
const (
	goalClearNoteMet          = "condition met"
	goalClearNoteUser         = "cleared by user"
	goalIdleExpiredNotePrefix = "idle-expired after "
)

// clearGoal ends the session's active goal (FR-070's shared body for
// `/goal clear` + aliases, the round/budget/idle-expiry brakes, and the
// task/plan card Clear button's future REST equivalent). Returns the
// user-facing reply. Despite its name and its ADR-088-era session-meta
// zeroing below, this is now (ADR-086 GOAL-FR-027/FR-028, this wave) the
// SHARED terminal-transition body for every ending kind, not merely the
// explicit user clear — its signature stays exactly as it was so every
// existing call site (including handleBareGoalClaim's and
// checkGoalLoopAfterTurn's, both outside this wave's write-set) keeps
// compiling and behaving correctly through this function's new internals
// alone.
//
// ADR-086 (S6): there is exactly ONE thing that ends here now — the goal's
// own persisted pkg/goal.Store record (D1: "a goal MUST be a stored entity
// in its own store"). The parallel session-meta bookkeeping this function
// used to zero first (GoalCondition/GoalRoundsUsed/… on
// session.UnifiedMeta) does not exist any more; wave S6 deleted those
// fields. terminateGoalRecordByID performs a STATUS TRANSITION on the
// retained record via Goal.Terminate, which is exactly what FR-027 means by
// "the record MUST survive with its criteria, their final statuses, the
// verdict, the reason and any handover" — never an erasure. A fresh `/goal
// <intent>` on the same session is still correctly recognized as a NEW goal
// rather than a restate, because the just-ended record is no longer ACTIVE
// and activeGoalForSession therefore stops returning it.
//
// Terminal pill/state (ADR-053 R§8.10 pill enum, extended by the UAT S3 fix
// with a 9th "cleared" value and by ADR-086 GOAL-FR-028 with a 10th
// "expired" value — the goal record's own four-way terminal split,
// generated.GoalState's met/exhausted/expired/cleared, each with its own
// distinguishable pill):
//   - note == goalClearNoteMet ("condition met") → pill "done", record state met.
//   - note == goalClearNoteUser ("cleared by user", the ONLY user-initiated
//     path — applyGoalCommandPrompt's `/goal clear|stop|off|reset|cancel|none`)
//     → pill "cleared", record state cleared: a deliberate, successful user
//     action is NOT a failure and must not paint the pill as one (UAT S3 —
//     the prior "collapse into failed" behavior flipped a red X badge for an
//     intentional stop; see contracts/components/schemas/GoalStatusFrame.yaml
//     for the enum extension this required per Constraint #8).
//   - note has the goalIdleExpiredNotePrefix prefix → pill "expired", record
//     state expired: the 7-day idle-expiry calendar brake (D-A), distinct
//     from a genuine round exhaustion.
//   - anything else (round bound reached) → pill "failed",
//     record state exhausted: a genuine terminal failure, not a user choice.
//
// clearGoal returns the reply ONLY. Callers that must know whether the
// durable transition actually landed — every caller that writes a terminal
// handover into the user's own transcript, or that paints a terminal pill —
// call clearGoalStatus instead (silent-SF-7/SF-8): a discarded result is how
// a user came to read "Goal X idle-expired" in their transcript while the
// record was still ACTIVE, and how a met goal's card stuck on `judging`
// forever.
func (al *AgentLoop) clearGoal(sessionID string, store *session.UnifiedStore, note string) string {
	reply, _ := al.clearGoalStatus(sessionID, store, note)
	return reply
}

// clearGoalStatus is clearGoal's full result: the user-facing reply AND
// whether the goal actually ended. ok is false ONLY when there was an active
// goal and its durable terminal transition was refused by the store — the
// deferral case clearGoal's own Fix-B.7 branch describes, where every
// terminal side effect is deliberately skipped and the next turn re-drives
// the clear. It is true when the transition landed AND when there was no
// active goal to clear in the first place (nothing is pending in that case,
// so a caller has nothing to defer).
//
// The split exists because clearGoal's signature is deliberately frozen (see
// its doc comment) while three call sites genuinely need the outcome. Adding
// a sibling keeps both properties.
func (al *AgentLoop) clearGoalStatus(sessionID string, store *session.UnifiedStore, note string) (string, bool) {
	reply, _, ok := al.endActiveGoal(sessionID, store, note)
	return reply, ok
}

// endedGoal is what endActiveGoal reports about a goal it actually ended: the
// record as it was just before the terminal transition, and that transition's
// own timestamp (the value persisted as the record's LastActivityAt).
type endedGoal struct {
	rec     *goal.Goal
	endedAt time.Time
}

// endActiveGoal is clearGoalStatus's body. It additionally returns the goal it
// ended — nil when there was no active goal or the transition was refused — so
// clearGoalWithOutcome (goal_outcome.go) can record the ending's outcome line
// only once the ending itself is saved.
func (al *AgentLoop) endActiveGoal(sessionID string, store *session.UnifiedStore, note string) (string, *endedGoal, bool) {
	// FR-114 (N-12): /goal clear cancels the in-flight verifier. ADR-088 D9
	// retires the pending-amendment/pending-compile states this check used to
	// also cover (GoalPendingJSON/GoalClarificationJSON no longer exist) —
	// hadGoal is now simply "is there an active goal record to clear".
	rec := activeGoalForSession(sessionID)
	hadGoal := rec != nil

	// ADR-088 D9/FR-028 (E7/S-41): a `/goal clear` must not leave an
	// AskUserQuestion card parked on this session — cancel it WITHOUT
	// dispatching a resume turn (the re-homed cancelOrphanedClarifyCard).
	// Runs whether or not a goal was actually active, exactly as before.
	al.cancelOrphanedClarifyCard(al.getAskUserRegistry(), sessionID)

	if !hadGoal {
		return "No active goal to clear.", nil, true
	}

	// Capture goal-id + condition + rounds BEFORE the terminal transition so
	// the terminal pill frame still carries the id/text the user was
	// watching (UAT S3: the frame that announces a goal's end must identify
	// WHICH goal ended).
	goalID := rec.GoalID
	condition := rec.Prompt
	rounds, maxRounds := rec.Round, rec.MaxRounds
	pillState := goalPillFailed
	goalState := generated.GoalStateExhausted
	switch {
	case note == goalClearNoteMet:
		pillState = goalPillDone
		goalState = generated.GoalStateMet
	case note == goalClearNoteUser:
		pillState = goalPillCleared
		goalState = generated.GoalStateCleared
	case strings.HasPrefix(note, goalIdleExpiredNotePrefix):
		pillState = goalPillExpired
		goalState = generated.GoalStateExpired
	case strings.HasPrefix(note, goalAgentDeletedNotePrefix):
		// UAT E-3 (goal_owner_deleted.go): the operator deleted the agent
		// working this goal. That is an explicit operator action ending the
		// goal — `cleared` — not a met, an exhaustion or an idle expiry; the
		// note (stored as the terminal reason) names the deleted agent.
		pillState = goalPillCleared
		goalState = generated.GoalStateCleared
	}

	// ADR-086 GOAL-FR-027/FR-028: the goal ends by a STATUS TRANSITION on
	// its own retained record — never by field-zeroing erasure. The
	// session-meta zeroing this function used to perform first is gone with
	// the fields themselves (S6); this transition IS the clear now, so its
	// failure is what the Fix B.7 deferral below keys on.
	endedAt := time.Now().UTC()
	if terr := terminateGoalRecordAt(goalID, goalState, note, endedAt); terr != nil {
		// The durable record is still active. Do NOT emit the terminal pill
		// (the user would see a cleared status while the durable state still
		// has the live goal), and do NOT release the verifier / clear the
		// trigger state (that would diverge the in-memory surface from the
		// still-active on-disk record). Let the next turn re-drive the clear.
		// (Fix B.7 — silent-failure hunter #11: prior code logged the warning
		// but proceeded as if the clear succeeded.)
		logger.WarnCF("agent", "goal: failed to clear goal state — skipping terminal side effects; next turn will re-drive clear",
			map[string]any{"session_id": sessionID, "goal_id": goalID, "error": terr.Error()})
		return "Goal clear deferred (the goal record could not be transitioned — will retry on next turn).", nil, false
	}
	if pe := GetPlanEngine(al); pe != nil {
		// R5 admission accounting for "goal" moves off the old Admit/Release
		// pair (ADR-086 GOAL-FR-049, this wave, paired with wave E11's
		// RegisterActiveCounter("goal", …) closure landing in the SAME
		// round): the cap is now recomputed live from pkg/goal's own active
		// records rather than an Admit-time reservation this function used
		// to release. Release was already an advisory no-op before this
		// change (plan_engine.go's own doc comment); deleting the call here
		// removes dead code, not a behavior. cancelGoalVerifierIfAny is
		// unrelated to admission accounting and is kept unchanged.
		al.cancelGoalVerifierIfAny(pe, sessionID)
	}
	// ADR-053 Phase-2 §1 (FR-114/N-12): reset the in-memory trigger surface so
	// a later stray GOAL_STATUS: met is inert (no active goal to adjudicate
	// against), the waiting_on_user pause clears, and the bounce streak / idle
	// re-arm marker / routing entry all drop.
	al.clearGoalTriggerState(sessionID, goalID)
	al.emitGoalStatusFrame(sessionID, goalID, condition, rounds, maxRounds, note, pillState)
	return "Goal cleared (" + note + ").", &endedGoal{rec: rec, endedAt: endedAt}, true
}

// terminateGoalRecordByID performs ADR-086 GOAL-FR-027/FR-028's status
// transition on the goal's OWN persisted pkg/goal.Store record: a STATUS
// TRANSITION on a retained record (Goal.Terminate, pkg/goal/status.go),
// never field-zeroing erasure. The record survives with its criteria, their
// statuses so far, its verdict and its terminal reason all intact — exactly
// what the verdict→criterion-status projection (GOAL-FR-036) needs to still
// be there to write into.
//
// It is keyed by goal id, which serves BOTH owner kinds identically
// (GOAL-FR-013): its two callers — clearGoal and goalIdleExpirySweep — each
// already hold the record they are ending, so no owner-keyed lookup is
// needed and none is done. (The owner-keyed twin this function used to have,
// terminateGoalRecordForOwner, was session-owner-only and therefore a silent
// no-op for a task-owned goal; it is deleted rather than kept as a second
// way to do the same thing.)
//
// The error is RETURNED, not merely logged: clearGoal's Fix-B.7 deferral
// depends on knowing whether the durable transition actually landed, since
// the record is now the only durable copy of the goal's state. A Warn is
// logged here too so the storage fault is visible even to a caller that
// only cares about its own reply.
func terminateGoalRecordByID(goalID string, state generated.GoalState, reason string) error {
	return terminateGoalRecordAt(goalID, state, reason, time.Now().UTC())
}

// terminateGoalRecordAt is terminateGoalRecordByID with the transition's
// timestamp supplied by the caller, so endActiveGoal can stamp the goal
// outcome line with exactly the time the record was ended at (Terminate
// persists it as LastActivityAt).
func terminateGoalRecordAt(goalID string, state generated.GoalState, reason string, now time.Time) error {
	if goalID == "" {
		return fmt.Errorf("goal: terminal transition requires a goal id")
	}
	store := resolveGoalRecordStore()
	if _, err := store.Update(goalID, func(cur *goal.Goal) error {
		return cur.Terminate(state, reason, now)
	}); err != nil {
		logger.WarnCF("agent", "goal: could not persist terminal transition on goal record",
			map[string]any{"goal_id": goalID, "state": string(state), "error": err.Error()})
		return fmt.Errorf("goal: terminal transition on %q: %w", goalID, err)
	}
	return nil
}

// cancelGoalVerifierIfAny implements ADR-052 FR-037's `/goal clear` cancel
// half (7-reviewer gate item 2): looks up the goal unit's registered
// verifier session (verifierUnitForGoal(sessionID)) — set BEFORE dispatch by
// the SAME runVerifierAdjudication (verifier_adjudication.go) plan-Stop's
// fan-out reads — and, if adjudication is currently in flight for this
// session, cancels it via the SAME RequestCancelForSession chat-cancel
// primitive every other Stop surface uses (A2, precedent: plan_engine.go's
// StopPlan/StopTask) — no new cancel machinery — then unregisters the entry.
// A no-op when no verifier is currently registered for this goal (the common
// case: most /goal clears land between rounds, with nothing in flight).
func (al *AgentLoop) cancelGoalVerifierIfAny(pe *PlanEngine, sessionID string) {
	unit := verifierUnitForGoal(sessionID)
	verifierSessionID, ok := pe.VerifierRegistry().Lookup(unit)
	if !ok || verifierSessionID == "" {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, armed, err := al.RequestCancelForSession(cancelCtx, verifierSessionID, "", "")
	switch {
	case err != nil:
		logger.WarnCF("agent", "goal: could not cancel in-flight goal verifier session",
			map[string]any{"session_id": sessionID, "verifier_session_id": verifierSessionID, "error": err.Error()})
	case armed:
		// No turn was registered yet for the verifier session at the moment
		// `/goal clear` ran — a pre-registration cancel latch
		// (cancel_prearm.go) now stands in for this cancel and will fire the
		// instant that turn registers (within cancelPreArmTTL). Not a
		// failure, just deferred; Debug (not Warn) because
		// RequestCancelForSession's own OnLatchExpired hook (cancel.go)
		// already gives an operator-visible Warn if the latch itself later
		// expires unconsumed.
		logger.DebugCF("agent", "goal: clear armed a pre-registration cancel latch for the in-flight goal verifier session",
			map[string]any{"session_id": sessionID, "verifier_session_id": verifierSessionID})
	}
	pe.VerifierRegistry().Unregister(unit)
}

// activeLoopsSnapshot reads R5's global active-loop count/cap via the
// installed PlanEngine's side-effect-free Admit (its doc comment: the count
// is computed fresh from persisted state on every call, never mutated) —
// safe to call purely for its (active, cap) return with no admission
// side-effect. Returns (0, DefaultGlobalActiveLoopCap) when no PlanEngine is
// installed (tests, or before boot wiring completes).
func (al *AgentLoop) activeLoopsSnapshot(kind string) (active, capN int) {
	pe := GetPlanEngine(al)
	if pe == nil {
		return 0, config.DefaultGlobalActiveLoopCap
	}
	_, active, capN = pe.Admit(kind)
	return active, capN
}

// emitGoalStatusFrame publishes a goal_status WS frame (FR-069/US-8) via
// EmitGoalStatusChanged. goalID is the stable per-generation identifier
// (UAT S3 fix, ADR-053 R§8.11) — empty for a legacy pre-upgrade goal that
// never had one minted, in which case the wire frame simply omits goal_id
// (it is OPTIONAL per GoalStatusFrame.yaml).
func (al *AgentLoop) emitGoalStatusFrame(sessionID, goalID, condition string, round, maxRounds int, reason, state string) {
	al.emitGoalStatusFrameWithCriteria(sessionID, goalID, condition, round, maxRounds, reason, state, nil)
}

// emitGoalStatusFrameWithCriteria is emitGoalStatusFrame plus the compiled
// criteria breakdown (ADR-074 D5.2 / FR-011): every current call site passes
// nil (ADR-088 D9 retired the `queued` pending-confirm emission that used to
// be the one caller passing a populated slice) — wave 2 (ADR-088 FR-019)
// wires a `set_goal`-registered/updated record's criteria/DoD through this
// path on the `active` emission.
func (al *AgentLoop) emitGoalStatusFrameWithCriteria(sessionID, goalID, condition string, round, maxRounds int, reason, state string, criteria []task.AcceptanceCriterion) {
	al.emitGoalStatusFrameWithCriteriaAndDoD(sessionID, goalID, condition, round, maxRounds, reason, state, "", criteria, nil)
}

// emitGoalStatusFrameWithCriteriaAndDoD is emitGoalStatusFrameWithCriteria
// plus ADR-080's `definition` (D-STATEMENT) and `dod` (D-DOD) breakdown.
// Every current call site passes "" / nil (see emitGoalStatusFrameWithCriteria's
// doc comment) so the confirm card renders the same statement + criteria +
// DoD the channel echo (formatGoalEcho) does once wave 2 wires a populated
// emission through here.
func (al *AgentLoop) emitGoalStatusFrameWithCriteriaAndDoD(
	sessionID, goalID, condition string, round, maxRounds int, reason, state, definition string,
	criteria, dod []task.AcceptanceCriterion,
) {
	active, capN := al.activeLoopsSnapshot("goal")
	al.EmitGoalStatusChanged(GoalStatusChangedPayload{
		SessionID:    sessionID,
		GoalID:       goalID,
		Condition:    condition,
		Round:        round,
		MaxRounds:    maxRounds,
		LatestReason: reason,
		ActiveLoops:  active,
		Cap:          capN,
		State:        state,
		Definition:   definition,
		Criteria:     criteria,
		DoD:          dod,
	})
}

// goalJudgeRoundTimeout bounds one /goal round's judge call (review r1 major
// M2). Mirrors plan_engine.go's planJudgeRoundTimeout exactly (same 10-minute
// bound) but for a DIFFERENT reason: the plan judge decouples into its own
// goroutine so a slow call never blocks the tick cycle, whereas a goal round
// runs SYNCHRONOUSLY inside the interactive turn (its outcome — met / unmet+
// round-advance / rounds-exhausted — must be known before the turn can decide
// whether to re-inject a follow-up round, checkGoalLoopAfterTurn's own doc
// comment below). Without an upper bound of its own, JudgeCriteria's D7
// contract of retrying FOREVER on judge-unavailability (respecting only ctx
// cancellation, judge.go's doc comment) would hang the user's live chat turn
// indefinitely whenever the judge is throttled/erroring and the turn's own
// ctx carries no deadline. A var (not const) so tests can substitute a short
// bound without a real multi-minute wait.
var goalJudgeRoundTimeout = planJudgeRoundTimeout

//nolint:gochecknoglobals

// checkGoalLoopAfterTurn is US-8's judge-gated round-advance hook (ADR-049
// D6/D7, FR-067..070; rewired by ADR-084 revision 9 D13/JUDGE-FR-092/095/
// 098/101, this wave, E13). Called once, synchronously, from runAgentLoop
// right after every natural (non-aborted) turn stop — a fast no-op unless
// the turn's session carries an active goal.
//
// D13 (JUDGE-FR-098): this hook stays cheap and synchronous for everything
// that always was — the origin gate, the waiting_on_user/blocked park, the
// activity bump, the ADR-088 D3 post-turn
// correction — but it no longer calls runGoalAdjudication itself. On a
// resolved `met` claim it records a DEFERRED dispatch on result (the
// goalDeferredAdjudication field, turn.go) instead: runAgentLoop performs
// that dispatch, in a goroutine, AFTER the operator's answer has already
// been published — see runAgentLoop's own doc comment at the call site.
//
// JUDGE-FR-092: before classifying the turn, this hook resolves tool-vs-
// marker precedence — a successful goal_claim tool call (resolveToolClaim,
// below) is authoritative over the prose GOAL_STATUS marker whenever both
// are present in the same turn.
//
// On an unmet verdict with rounds remaining, the (now-deferred) adjudication
// delivers its steer via the async-notifier (idleSteerDeliverer,
// goal_triggers.go — JUDGE-FR-099), NOT via result.followUps: by the time a
// deferred adjudication resolves, this turn's own result.followUps has
// already been published and read by nobody again.
// goalLoopFollowUpSenderID is the sentinel Sender.CanonicalID stamped on the
// goal loop's own re-injected follow-up turn (below). checkGoalLoopAfterTurn
// reads it back via opts.SenderID (processMessage threads msg.Sender.CanonicalID
// onto SenderID for every turn, including a republished follow-up) to
// recognize its own continuation without relying on UserInitiated, which is
// correctly false for a system-originated follow-up.
const goalLoopFollowUpSenderID = "system:goal_loop"

// goalDeferredAdjudicationWork is JUDGE-FR-098's deferred-dispatch payload
// (D13, this wave): checkGoalLoopAfterTurn's claim-resolution step records
// this on result.goalDeferredAdjudication (turnResult, turn.go) instead of
// calling runGoalAdjudication synchronously. runAgentLoop (loop.go)
// dispatches it, in a goroutine, strictly AFTER bus.PublishOutbound of this
// turn's own finalContent — the reordering that is FR-098's whole mechanism.
//
// agentInst/workspaceID/sessionID/claimText are captured at claim-resolution
// time (the turn that produced the claim); meta itself is deliberately NOT
// captured here — dispatchDeferredGoalAdjudication re-reads it fresh from
// the store at dispatch time, since an arbitrary (bounded by the ctx
// timeout) amount of wall-clock time may pass between recording this work
// and actually running it, and FR-100 requires a new operator message in
// that window to proceed normally rather than be blocked by stale state.
type goalDeferredAdjudicationWork struct {
	agentInst   *AgentInstance
	workspaceID string
	sessionID   string
	claimText   string
}

// goalDeferredAdjudicationDoneFn is a TEST SEAM ONLY — production leaves it
// nil and behaviour is identical either way (same precedent as
// task_executor.go's goroutineCtxHook and verifier_adjudication.go's
// judgeSleepFn). dispatchDeferredGoalAdjudication calls it on its OWN
// goroutine, on every exit path, once that adjudication's writes have
// landed.
//
// It exists because FR-098's dispatch is deliberately fire-and-forget: the
// turn that produced the claim has already returned to its caller, so a test
// driving runAgentLoop has nothing to join and its t.TempDir cleanup races
// the adjudication's still-running writes (observed: the goal entity file
// under entities/goals/ being written while RemoveAll walked the tree →
// "directory not empty"). Joining the goroutine through this seam is the
// honest fix — waiting for a proxy signal (the judge LLM call returning)
// only narrows the window, because every write AFTER the verdict is still
// outstanding at that point.
var goalDeferredAdjudicationDoneFn func(sessionID string)

// dispatchDeferredGoalAdjudication performs JUDGE-FR-098's deferred
// dispatch: re-reads the session's current goal state fresh (never the
// turn-time snapshot — see goalDeferredAdjudicationWork's own doc comment),
// then runs the SAME shared adjudication body the idle path used to drive,
// with two D13-mandated differences from that retired call shape (FR-098):
// the context is derived from context.Background() (the turn ctx that
// produced the claim is long finished by the time this goroutine runs, per
// C23), and the steer is delivered via the async-notifier
// (idleSteerDeliverer, JUDGE-FR-099) rather than result.followUps, which no
// longer exists by the time this runs.
//
// Called from runAgentLoop in its own goroutine, strictly after
// PublishOutbound — the turn that produced the claim has already returned
// to its caller by the time this executes.
func (al *AgentLoop) dispatchDeferredGoalAdjudication(work *goalDeferredAdjudicationWork) {
	if work == nil || work.sessionID == "" {
		return
	}
	if fn := goalDeferredAdjudicationDoneFn; fn != nil {
		defer fn(work.sessionID)
	}
	store := al.GetSessionStore()
	if store == nil {
		logger.WarnCF("agent", "goal: deferred adjudication dispatch failed — no session store",
			map[string]any{"session_id": work.sessionID})
		return
	}
	rec := activeGoalForSession(work.sessionID)
	if rec == nil {
		// The goal cleared (or the session vanished) in the window between
		// the claim and this dispatch — nothing left to adjudicate against.
		logger.InfoCF("agent", "goal: deferred adjudication skipped — no active goal on this session any more",
			map[string]any{"session_id": work.sessionID})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), goalJudgeRoundTimeout)
	defer cancel()
	al.runGoalAdjudication(
		ctx, work.agentInst, work.workspaceID, work.sessionID, store, rec, work.claimText,
		al.idleSteerDeliverer(work.sessionID, rec.GoalID, goalClaimDeferredSourceKind),
	)
}

// goalClaimUnparseableResults counts successful goal_claim tool calls whose
// recorded result resolveToolClaim could not read as the expected JSON object
// (silent-SF-4). It follows this package's established counter precedent —
// task_executor.go's taskGoalTranscriptWriteFailures, turn.go's
// transcriptWriteFailures — a package-local counter scoped to this unit's own
// call sites.
//
// Why a counter and not just the WARN beside it: the shape is produced in
// pkg/tools/goal_claim.go and persisted by loop.go's tcRecord construction,
// two independent places it can drift. If it ever does, EVERY claim stops
// resolving at once and the goal loop looks merely quiet rather than broken —
// the failure mode is total and silent, and a nonzero counter is the one
// signal an operator can see without reading logs line by line.
var goalClaimUnparseableResults atomic.Uint64

// GoalClaimUnparseableResults returns the current value of the unparseable
// goal_claim-result counter (silent-SF-4). Used by tests and operator tooling.
func GoalClaimUnparseableResults() uint64 {
	return goalClaimUnparseableResults.Load()
}

// resolveToolClaim implements JUDGE-FR-092's tool-vs-marker precedence: it
// scans sessionID's OWN transcript (never descendants — goal_claim itself
// refuses on a delegated sub-turn, JUDGE-FR-090, so only the owner/root
// turn can ever produce a successful call) for goal_claim tool-call entries
// timestamped strictly after since, and returns the LAST successful one
// (E-27's last-occurrence-wins multiplicity rule, mirroring
// parseGoalStatusMarker's own rule). A refused (FR-088/FR-090) or
// policy-denied (E-29) call has Status != "success" and is skipped — it is
// not a successful call and must not suppress marker parsing (FR-092's own
// text).
//
// The tool's own Execute (pkg/tools/goal_claim.go) encodes its result as a
// JSON object ({"status":..., "evidence":..., "goal_id":...}) via
// NewToolResult — persisted verbatim as this ToolCall entry's
// Result["text"] by loop.go's tcRecord construction (the same "text" key
// every non-media, non-delegate successful tool result lands under).
//
// silent-SF-3 (review): the final return value is a READ ERROR, distinct from
// "scanned successfully and found no claim". Both used to collapse into
// found=false, and the caller advanced its scan watermark on both — so a
// single transient transcript read error skipped a REAL claim permanently
// (the watermark moved past it and no later pass would ever look there
// again). The caller now advances the watermark only on a clean scan.
func (al *AgentLoop) resolveToolClaim(store *session.UnifiedStore, sessionID string, since time.Time) (found bool, status, evidence, goalID string, readErr error) {
	if store == nil || sessionID == "" {
		return false, "", "", "", nil
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		logger.WarnCF("agent", "goal claim: could not read transcript to resolve tool-vs-marker precedence",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return false, "", "", "", fmt.Errorf("goal claim: read transcript %q: %w", sessionID, err)
	}
	for _, e := range entries {
		if e.Type != session.EntryTypeToolCall || !e.Timestamp.After(since) {
			continue
		}
		for _, tc := range e.ToolCalls {
			if tc.Tool != tools.GoalClaimToolName || tc.Status != "success" {
				continue
			}
			text, _ := tc.Result["text"].(string)
			var payload struct {
				Status   string `json:"status"`
				Evidence string `json:"evidence"`
				GoalID   string `json:"goal_id"`
			}
			// silent-SF-4 (review): a SUCCESSFUL goal_claim call whose
			// recorded result is not the expected JSON object used to be
			// skipped with zero logging. The shape is produced by
			// pkg/tools/goal_claim.go and persisted by loop.go's tcRecord
			// construction — two places it can drift — and if it ever does,
			// EVERY claim silently stops resolving and the goal loop looks
			// merely quiet rather than broken. Each rejection reason is now
			// named at WARN so the drift is visible in one log line.
			switch {
			case text == "":
				goalClaimUnparseableResults.Add(1)
				logger.WarnCF("agent", "goal claim: successful goal_claim tool call carries no result text — claim not resolvable (JUDGE-FR-092)",
					map[string]any{"session_id": sessionID, "entry_id": e.ID, "result_keys": len(tc.Result)})
				continue
			case json.Unmarshal([]byte(text), &payload) != nil:
				goalClaimUnparseableResults.Add(1)
				logger.WarnCF("agent", "goal claim: successful goal_claim tool call result is not the expected JSON object — claim not resolvable (JUDGE-FR-092)",
					map[string]any{"session_id": sessionID, "entry_id": e.ID, "result_text_len": len(text)})
				continue
			case payload.Status == "":
				goalClaimUnparseableResults.Add(1)
				logger.WarnCF("agent", "goal claim: successful goal_claim tool call result carries an empty status — claim not resolvable (JUDGE-FR-092)",
					map[string]any{"session_id": sessionID, "entry_id": e.ID})
				continue
			}
			// Last-occurrence wins (E-27): keep overwriting as the scan
			// proceeds forward through the (chronologically ordered)
			// transcript.
			found = true
			status = payload.Status
			evidence = payload.Evidence
			goalID = payload.GoalID
		}
	}
	return found, status, evidence, goalID, nil
}

// agentLoopCheckGoalLoopAfterTurn carries the shared state of checkGoalLoopAfterTurn across its stages.
type agentLoopCheckGoalLoopAfterTurn struct {
	al           *AgentLoop
	ctx          context.Context
	agentInst    *AgentInstance
	opts         processOptions
	result       *turnResult
	store        *session.UnifiedStore
	sessionID    string
	rec          *goal.Goal
	toolFound    bool
	toolEvidence string
	marker       goalStatusMarker
}

func (al *AgentLoop) checkGoalLoopAfterTurn(
	ctx context.Context,
	agentInst *AgentInstance,
	opts processOptions,
	result *turnResult,
) {
	gl := &agentLoopCheckGoalLoopAfterTurn{al: al, ctx: ctx, agentInst: agentInst, opts: opts, result: result}

	if gl.checkEligibility() {
		return
	}

	gl.resolveClaim()

	gl.handleOutcome()
}

// checkEligibility rejects turns that cannot advance a chat-owned goal loop and loads the active chat goal.
func (gl *agentLoopCheckGoalLoopAfterTurn) checkEligibility() bool {
	// Founder decision 2026-09-14 (one Judge pipeline): a TASK run's goal is
	// owned end-to-end by the task executor (pkg/agent/task_run_loop.go) —
	// its turns' claims, the parks, the Judge dispatches and the try budget
	// are all resolved there, in the run's own session. This hook must NOT
	// resolve a task run's claim or schedule a second adjudication for it
	// (the B-5 duplicate-pipeline defect), so a task-run turn never proceeds
	// past this gate. Chat goals keep this hook as their only consumer.
	if gl.opts.IsTaskRun {
		return true
	}
	if gl.result == nil || gl.opts.TranscriptStore == nil || gl.opts.TranscriptSessionID == "" {
		return true
	}
	// askuserquestion-tool-spec §0.7 (M-R2-5): TurnEndStatusParked is NOT a
	// natural turn stop — a parked clarification/compile turn (AskUserQuestion,
	// or any future ParksTurn tool) never advances the goal round, invokes the
	// Judge, or re-dispatches. The gate lives here, at the function's entry.
	if gl.result.status == TurnEndStatusParked {
		return true
	}
	// review r2 RV3: origin-gate the hook itself, not just IsTaskRun. /goal and
	// /loop can coexist on one session; a /loop/cron/heartbeat/async turn
	// (ProcessScheduled, loop.go, IsTaskRun=false) has UserInitiated=false and
	// SenderID="" (ProcessScheduled builds its processOptions literal directly,
	// never through the msg-based path that sets these — see UserInitiated's
	// own doc comment) and must NOT advance or touch the goal. A genuine user
	// turn (UserInitiated), the goal loop's own re-injected follow-up
	// (SenderID == goalLoopFollowUpSenderID, stamped below), OR a task run's
	// own dispatched turn (opts.IsTaskRun, loop.go's processTaskDirect —
	// GOAL-FR-015: a task-owned goal's own turns are the ONLY activity that
	// should ever advance IT, and IsTaskRun is the one signal that
	// unambiguously identifies them) may proceed — mirrors
	// applyGoalCommandPrompt's own origin gate for the non-task cases.
	if !gl.opts.UserInitiated && gl.opts.SenderID != goalLoopFollowUpSenderID && !gl.opts.IsTaskRun {
		return true
	}
	if gl.agentInst == nil {
		return true
	}
	gl.store = gl.opts.TranscriptStore
	gl.sessionID = gl.opts.TranscriptSessionID

	// ADR-086: the entry condition is the existence of an ACTIVE goal record
	// bound to this session — the direct replacement for the retired
	// `rec.Prompt != ""` check, and the one lookup that serves a
	// task-owned goal as well as a chat-owned one (GOAL-FR-013).
	gl.rec = activeGoalForSession(gl.sessionID)
	if gl.rec == nil {
		return true // no active goal — fast path
	}
	// A task-owned goal is consumed ONLY by the task executor
	// (task_run_loop.go), even when a turn in its session arrives through a
	// non-task path: resolving its claim here would start a second Judge
	// pipeline on the same goal.
	if gl.rec.OwnerKind == generated.GoalOwnerKindTask {
		return true
	}
	return false
}

// resolveClaim lifts a prior Stop pause, resolves claims, and clears user-resumable pause states.
func (gl *agentLoopCheckGoalLoopAfterTurn) resolveClaim() {
	// Founder decision 2026-09-14 (UAT B-1 run 4): lift the Stop-pause once a
	// genuine new turn was asked for on this session — a user message saved
	// after the Stop (liftGoalKeeperStopPauseIfNewTurn). The turn that was
	// itself Stopped can finish here as a normal completion (a graceful Stop's
	// last tool-less round); its user message predates the Stop, so it does
	// not lift. The keeper's own follow-ups (SenderID ==
	// goalLoopFollowUpSenderID) are never eligible. A task run saves no user
	// message of its own (processTaskDirect), so on a task session the pause
	// lifts only once the user messages that thread.
	if (gl.opts.UserInitiated || gl.opts.IsTaskRun) && gl.al.liftGoalKeeperStopPauseIfNewTurn(gl.store, gl.sessionID) {
		logger.InfoCF("agent", "goal: a user message arrived after the Stop — the Stop-pause on the goal keeper is lifted",
			map[string]any{"component": "goal", "session_id": gl.sessionID, "goal_id": gl.rec.GoalID})
	}

	// ADR-053 Phase-2 §1 (FR-101, superseded in shape but not in spirit by
	// D13/FR-095): the Judge fires ONLY on an explicit completion claim —
	// never on a timer, never after every worker turn. This turn-stop hook
	// implements the CLAIM path (both channels — JUDGE-FR-092) and the
	// waiting_on_user/blocked parks; the retired claimless idle-adjudication
	// path is gone (FR-095) — the idle tick (goalQuietWindowSettle, driven
	// via goalIdleExpirySweep) now only ever RE-POSTS (FR-097), never
	// judges.
	//
	// JUDGE-FR-092: resolve tool-vs-marker precedence BEFORE classifying
	// this turn. scanSince is a per-goal-id watermark (independent of
	// GoalLastActivityAt, which the waiting_on_user/blocked parks below do
	// not bump) so a claim already resolved by an earlier pass through this
	// function is never re-discovered by a later one just because activity
	// went un-bumped in between.
	scanSince, _ := gl.al.goalClaimScanWatermark(gl.rec.GoalID)
	var toolStatus string
	var claimScanErr error
	gl.toolFound, toolStatus, gl.toolEvidence, _, claimScanErr = gl.al.resolveToolClaim(gl.store, gl.sessionID, scanSince)
	// silent-SF-3 (review): advance the watermark ONLY on a clean scan. It
	// used to advance unconditionally, including when resolveToolClaim
	// returned the zero tuple because the transcript READ FAILED — which
	// moved the scan window past a real, unread claim and guaranteed no later
	// pass would ever find it. Leaving the watermark put costs at worst a
	// re-scan of the same window on the next turn; advancing it past an
	// unread window loses the claim permanently.
	if claimScanErr == nil {
		gl.al.setGoalClaimScanWatermark(gl.rec.GoalID, time.Now())
	}

	if gl.toolFound {
		// FR-092: the tool call is authoritative — result.finalContent is
		// NOT parsed for markers at all this turn. HasEvidence is always
		// true for a tool-recorded `met` claim (the tool itself refuses a
		// met call with empty evidence at the call boundary, FR-088), which
		// is exactly what makes the bare-claim bounce (FR-091) unreachable
		// for the tool path, by construction — not a branch this file has
		// to special-case.
		gl.marker = goalStatusMarker{Present: true, Status: toolStatus, HasEvidence: toolStatus == tools.GoalClaimStatusMet}
		if prose := parseGoalStatusMarker(gl.result.finalContent); prose.Present {
			logger.InfoCF("agent", "goal claim: both channels fired in one turn — the tool call is authoritative (JUDGE-FR-092)",
				map[string]any{"session_id": gl.sessionID, "goal_id": gl.rec.GoalID, "tool_status": toolStatus, "marker_status": prose.Status})
		}
	} else {
		// FR-091: the prose marker parser is UNCHANGED and remains the
		// fallback — parsed here only when no successful tool claim exists
		// for this turn.
		gl.marker = parseGoalStatusMarker(gl.result.finalContent)
	}

	// G-5 resume (US-2 AS-3): a genuine user reply (UserInitiated) to a
	// waiting_on_user goal clears the pause and re-arms the idle timer. The
	// user's turn itself is the new activity; it is NOT itself a claim, so
	// fall through to the classification below rather than judging here. The
	// goal loop's own re-injected follow-up (SenderID == goalLoopFollowUpSenderID)
	// does NOT clear the pause — only a real user message resumes.
	if gl.opts.UserInitiated && gl.al.goalIsWaitingOnUser(gl.rec.GoalID) {
		gl.al.goalSetWaitingOnUser(gl.rec.GoalID, false)
		gl.al.bumpGoalActivityOnTurn(gl.rec.GoalID)
	}
	// JUDGE-FR-093/R-24: the SAME resume applies to a blocked park — a
	// genuine operator message is the only thing that clears it, mirroring
	// waiting_on_user exactly.
	if gl.opts.UserInitiated && gl.al.goalIsBlocked(gl.rec.GoalID) {
		gl.al.goalSetBlocked(gl.rec.GoalID, false)
		gl.al.bumpGoalActivityOnTurn(gl.rec.GoalID)
	}
}

// handleOutcome dispatches the resolved claim or records ordinary goal activity.
func (gl *agentLoopCheckGoalLoopAfterTurn) handleOutcome() {
	switch {
	case gl.marker.Present && gl.marker.Status == goalStatusWaitingOnUser:
		// FR-104 / G-5: typed pause. NO verdict, NO round consumed; idle
		// settlement SUPPRESSED while the pause holds (goalIsWaitingOnUser,
		// checked in maybeSettleGoalIdle). Explicit Non-Behavior: only this
		// typed marker counts — never a prose classifier.
		gl.al.goalSetWaitingOnUser(gl.rec.GoalID, true)
		// The claim — its status and the worker's one-line question, when it
		// gave one — is recorded on the goal record through the one claim
		// writer a task run uses too (GOAL-FR-013/FR-014).
		if cerr := recordGoalClaim(gl.rec.GoalID, generated.GoalLatestClaimStatusWaitingOnUser, gl.toolEvidence); cerr != nil {
			logger.WarnCF("agent", "goal: could not persist the waiting_on_user claim onto the goal record",
				map[string]any{"session_id": gl.sessionID, "goal_id": gl.rec.GoalID, "error": cerr.Error()})
		}
		gl.al.emitGoalStatusFrame(gl.sessionID, gl.rec.GoalID, gl.rec.Prompt, gl.rec.Round,
			gl.rec.MaxRounds, "waiting on user", goalPillWaitingOnUser)
		return

	case gl.marker.Present && gl.marker.Status == tools.GoalClaimStatusBlocked:
		// JUDGE-FR-093: `blocked` parks the goal without an adjudication and
		// without consuming a round — reachable ONLY through the goal_claim
		// tool (FR-091 forbids extending the prose marker parser to
		// recognize it, so this arm can only ever be reached via the
		// toolFound branch above). Distinct from waiting_on_user in what it
		// asks of the operator: "I cannot proceed and it is not something
		// you can answer directly" rather than "I need an answer".
		gl.al.goalSetBlocked(gl.rec.GoalID, true)
		// The one-line reason the worker gave (goal_claim carries it as
		// evidence for blocked) is kept so the operator can see why — through
		// the one claim writer a task run uses too (GOAL-FR-013).
		if cerr := recordGoalClaim(gl.rec.GoalID, generated.GoalLatestClaimStatusBlocked, gl.toolEvidence); cerr != nil {
			logger.WarnCF("agent", "goal: could not persist the blocked claim onto the goal record",
				map[string]any{"session_id": gl.sessionID, "goal_id": gl.rec.GoalID, "error": cerr.Error()})
		}
		gl.al.emitGoalStatusFrame(gl.sessionID, gl.rec.GoalID, gl.rec.Prompt, gl.rec.Round,
			gl.rec.MaxRounds, "blocked", goalPillBlocked)
		return

	case gl.marker.Present && gl.marker.Status == goalStatusMet && gl.marker.HasEvidence:
		// G-1 / JUDGE-FR-098: an explicit completion claim WITH evidence —
		// tool or marker. Under D13 the Judge is dispatched AFTER this
		// turn's own answer has been delivered, off the critical path
		// (runAgentLoop, loop.go) — this function no longer invokes the
		// Judge itself; it records the DEFERRED work below and returns.
		//
		// JUDGE-FR-101 (open item 3): a second claim arriving while an
		// adjudication is already in flight for this goal MUST be refused,
		// not superseded — reusing the SAME two shipped layers unchanged
		// (goalAdjudicationInFlight here; the verifier registry's CAS is the
		// second layer, catching whatever races past this check-then-act
		// guard). What changed under D12 is the REPORTING: the model is
		// told, via a stated steer rather than a silent log-only drop —
		// goal_claim's own doc comment names this "pkg/agent's job, reading
		// this tool's result off the transcript" (this function IS that
		// job); the tool call itself already completed and returned its
		// normal recorded-status result by the time this post-turn hook
		// runs, so the refusal reaches the model on its NEXT turn via the
		// same re-inject seam a bounced bare claim already uses, rather
		// than by mutating a tool result that already left the building.
		// This refused claim does NOT clear the bare-claim streak and does
		// NOT count as a claim for any purpose (FR-101's own text) — it
		// simply returns without recording deferred work.
		if gl.al.goalAdjudicationInFlight(gl.sessionID) {
			logger.InfoCF("agent", "goal claim: adjudication already in-flight; refusing the second claim (JUDGE-FR-101)",
				map[string]any{"session_id": gl.sessionID, "goal_id": gl.rec.GoalID})
			gl.result.followUps = append(gl.result.followUps, bus.InboundMessage{
				Channel: gl.opts.Channel, ChatID: gl.opts.ChatID,
				Sender: bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID},
				Content: "An adjudication for this goal is already running in the background. " +
					"Its verdict will arrive on its own — there is no need to claim again right now.",
				SessionID: gl.sessionID, SessionKey: gl.opts.SessionKey,
			})
			return
		}
		// Clear any bounce streak (the worker satisfied the evidence gate).
		gl.al.clearGoalBareClaimStreak(gl.rec.GoalID)
		// JUDGE-FR-094: the tool path's evidence argument becomes ClaimText,
		// occupying the SAME position the marker path's whole
		// result.finalContent occupies — a narrower, less injectable input.
		claimText := gl.result.finalContent
		evidence := gl.marker.EvidenceText
		if gl.toolFound {
			claimText = gl.toolEvidence
			evidence = gl.toolEvidence
		}
		// GOAL-FR-013/FR-014: the met claim is recorded on the goal record
		// before the Judge is dispatched, through the one claim writer a task
		// run uses (task_run_loop.go::adjudicateRunClaim) — so a chat goal's
		// record carries its latest claim whether or not the Judge then runs.
		if cerr := recordGoalClaim(gl.rec.GoalID, generated.GoalLatestClaimStatusMet, evidence); cerr != nil {
			logger.WarnCF("agent", "goal: could not persist the met claim onto the goal record",
				map[string]any{"session_id": gl.sessionID, "goal_id": gl.rec.GoalID, "error": cerr.Error()})
		}
		gl.result.goalDeferredAdjudication = &goalDeferredAdjudicationWork{
			agentInst: gl.agentInst, workspaceID: gl.opts.WorkspaceID,
			sessionID: gl.sessionID, claimText: claimText,
		}
		return

	case gl.marker.Present && gl.marker.Status == goalStatusMet:
		// G-4: bare claim (GOAL_STATUS: met with NO [goal:evidence]). Bounce
		// economics — 1st free (teaching steer), 2nd costs a round. NEVER
		// invokes the Judge (nothing to judge). Claiming stays cheaper than
		// idling (D8/N-13). JUDGE-FR-091: unreachable for a tool-path claim
		// by construction (the tool itself refuses an empty-evidence `met`
		// call at the call boundary, so a tool-recorded `met` always carries
		// HasEvidence=true and lands in the arm above instead).
		gl.al.handleBareGoalClaim(gl.ctx, gl.agentInst, gl.opts, gl.store, gl.sessionID, gl.rec, gl.result)
		return

	default:
		// No claim marker and not waiting/blocked: an ordinary worker turn.
		// Bump the activity clock (re-arm the idle quiet window, FR-102) and
		// clear any already-settled marker (G-2 re-arm). Do NOT judge — a
		// `met` claim is the sole adjudication trigger now (FR-095); the
		// idle path only ever re-posts (FR-097). An UNRECOGNIZED marker
		// value also lands here: the deterministic not-a-claim-not-a-pause
		// fallback (FR-104 AS-2 — no marker means not-waiting, and by
		// symmetry not a claim either).
		gl.al.bumpGoalActivityOnTurn(gl.rec.GoalID)
		gl.al.emitGoalStatusFrame(gl.sessionID, gl.rec.GoalID, gl.rec.Prompt, gl.rec.Round,
			gl.rec.MaxRounds, gl.rec.LatestReason, goalPillActive)
		// ADR-088 D3 AMENDMENT item 3 (2026-09-07): the immediate post-turn
		// correction that replaces provider tool-choice forcing as the
		// enforcement point. An ordinary (non-parked, non-waiting, non-claim)
		// goal turn just completed with the compiled record STILL empty —
		// the working agent skipped its first-move door entirely. Nudge it
		// right now rather than waiting for the idle keeper's quiet window
		// (D6c) to notice.
		gl.al.maybeNudgeUnregisteredGoal(gl.store, gl.sessionID, gl.rec, gl.agentInst, gl.opts)
	}
}

// maybeNudgeUnregisteredGoal is ADR-088 D3 amendment item 3's immediate
// post-turn correction: called only from checkGoalLoopAfterTurn's ordinary-
// turn branch (default case — a claim, a waiting_on_user pause, or a bare
// claim all take their own dedicated action and never reach here). When the
// goal is still recordless after an ordinary turn, this dispatches the SAME
// registration nudge D6c's idle ladder would eventually dispatch — reusing
// settleRecordlessGoal (goal_triggers.go) rather than a second dispatch
// path, so there is exactly one persisted counter (GoalZeroOutputPushes)
// and exactly one nudge-or-fallback decision function.
//
// Two guards keep this from misfiring:
//   - a parked AskUserQuestion card (goalHasParkedCard) means the agent
//     DID take a first-move door — it is waiting on the operator's answer,
//     not stalled — so nudging now would talk over that pending question.
//   - a turn whose SenderID is goalLoopFollowUpSenderID is itself a
//     goal-loop-dispatched follow-up (a nudge, a continue-push, or an idle
//     steer) that STILL didn't register. Re-dispatching immediately from
//     here would tight-loop with zero delay between attempts. Instead this
//     case is deliberately left to the existing idle ladder
//     (maybeSettleGoalIdle -> settleRecordlessGoal, goal_triggers.go),
//     which advances the SAME GoalZeroOutputPushes counter after the
//     normal quiet window — still bounded by goalZeroOutputPushMax before
//     the D7 engine fallback takes over, so the guarantee holds either way;
//     only the FIRST registration attempt is sped up by this function, not
//     every subsequent retry.
func (al *AgentLoop) maybeNudgeUnregisteredGoal(
	store *session.UnifiedStore, sessionID string, rec *goal.Goal,
	agentInst *AgentInstance, opts processOptions,
) {
	// ADR-086: "still recordless" is the goal record's own criteria list
	// being empty (GOAL-FR-003's typed lists), not the retired session-meta
	// GoalCriteriaJSON string. This is also what keeps the nudge ladder
	// unreachable for a task-owned goal (GOAL-FR-020) now that the
	// "task_explicit" session-meta sentinel is gone: a task goal's criteria
	// are fixed on its record at creation (D-C), so its list is never empty.
	if len(rec.Criteria) > 0 {
		return // registered — nothing to correct
	}
	if al.goalHasParkedCard(sessionID) {
		return // waiting on the operator's answer, not stalled
	}
	if opts.SenderID == goalLoopFollowUpSenderID {
		return // a goal-loop follow-up that itself failed — let the idle ladder pick it up
	}
	meta, merr := store.GetMeta(sessionID)
	if merr != nil || meta == nil {
		logger.WarnCF("agent", "goal: could not read session meta for the immediate registration nudge; leaving it to the idle ladder",
			map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID, "error": errString(merr)})
		return
	}
	logger.InfoCF("agent", "goal: record not registered on the turn; nudging immediately",
		map[string]any{"component": "goal", "session_id": sessionID, "goal_id": rec.GoalID})
	al.settleRecordlessGoal(store, meta, rec, agentInst)
}

// goalVerdictReasonText builds a human-readable summary of the judge's unmet
// per-criterion reasons, fed forward as steering (FR-043 pattern, applied to
// /goal rounds).
//
// Identical reasons are collapsed to ONE occurrence, and empty reasons are
// skipped entirely. The judged set is criteria UNION definition-of-done
// (ADR-080 D-DOD), so a single blocking fact — "the suite still fails" —
// routinely comes back as the reason on several of those items at once. The
// prior verbatim concatenation therefore wrote that one sentence two or three
// times into the goal record's LatestReason, and from there into the steering
// prompt the worker reads next round and the "Latest judge feedback:"
// handover the USER reads when a goal exhausts. Repetition carries no extra
// information, costs tokens on every subsequent round, and reads as a
// malfunction. Deduplication is order-preserving: the first occurrence of each
// distinct reason stays where the Judge put it, so genuinely different
// per-criterion reasons are all still reported, in order, exactly as before.
//
// UAT (worker polled a verification task 174 times): a per-criterion reason
// from a NON-judgment path — "could not verify" (unable_to_verify, the check
// mechanism itself could not run) or criterion_unjudgeable (the Judge ran but
// formed no judgment) — is NOT work to redo. Feeding it to the worker as an
// unmet reason made the worker re-verify and poll the thing the JUDGE failed
// to run. Those reasons are therefore partitioned out of the unmet list and
// reported as one separate clause (goalJudgeCouldNotVerifyMarker), which
// goalSteeringPrompt turns into an explicit do-not-re-verify instruction.
// The markers are the engine's own stable reason strings
// (judgeCouldNotVerifyReason), never a sniff of arbitrary model prose.
func goalVerdictReasonText(v *task.JudgeVerdict) string {
	if v == nil || len(v.PerCriterion) == 0 {
		return "(no reason recorded)"
	}
	var sb strings.Builder
	seen := make(map[string]struct{}, len(v.PerCriterion))
	couldNotVerify := 0
	for _, cv := range v.PerCriterion {
		if cv.Met || cv.Reason == "" {
			continue
		}
		if judgeCouldNotVerifyReason(cv.Reason) {
			couldNotVerify++
			continue
		}
		if _, dup := seen[cv.Reason]; dup {
			continue
		}
		seen[cv.Reason] = struct{}{}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(cv.Reason)
	}
	if couldNotVerify > 0 {
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "(the Judge could not verify %d criterion/criteria — its own check could not run; this is not work to redo)", couldNotVerify)
	}
	if sb.Len() == 0 {
		return "(no reason recorded)"
	}
	return sb.String()
}

// goalJudgeCouldNotVerifyMarker is the clause goalVerdictReasonText appends
// when unmet criteria came back via a non-judgment path. goalSteeringPrompt
// keys its do-not-re-verify instruction on it.
const goalJudgeCouldNotVerifyMarker = "the Judge could not verify"

// judgeCouldNotVerifyReason reports whether an unmet criterion's Reason was
// produced by a NON-judgment path rather than the Judge looking and saying no:
// a verification mechanism that could not run (unable_to_verify — the
// engine-stable marker appears as a prefix on prose fail-closed reasons and as
// a parenthesised suffix on deterministic-rung reasons, so this matches it
// anywhere), or a criterion the verifier ran on but formed no judgment for
// (criterion_unjudgeable, always a prefix). These are the two shapes
// summarizeVerdict partitions as "could not verify"
// (pkg/agent/judge.go, JUDGE-FR-022); this is the per-criterion Reason
// counterpart of that engine-tracked split, matching on the engine's own
// reason strings — never on model prose.
func judgeCouldNotVerifyReason(reason string) bool {
	r := strings.TrimSpace(reason)
	return strings.Contains(r, "unable_to_verify") || strings.HasPrefix(r, "criterion_unjudgeable")
}

// goalSteeringPrompt builds the next round's user-turn content. When the
// reason carries the could-not-verify clause, the steer explicitly tells the
// worker NOT to re-verify or poll — the Judge's own check failed to run, which
// is not something the worker can fix by redoing or watching it (UAT: a
// worker polled a verification task 174 times). Normal unmet reasons keep the
// exact original wording.
func goalSteeringPrompt(condition, reason string) string {
	steer := fmt.Sprintf(
		"Continue working toward the goal: %s\n\n"+
			"The judge reviewed your last attempt and found it UNMET:\n%s\n\nKeep going.",
		condition, reason,
	)
	if strings.Contains(reason, goalJudgeCouldNotVerifyMarker) {
		steer += "\n\nThe Judge itself could not run its check for some criteria — that is not feedback about your work. " +
			"Do not re-run, re-verify or poll any verification. Continue the goal's actual work, or state what is blocking you."
	}
	return steer
}

// writeGoalVerdictTranscript writes verdict as a dedicated judge_verdict
// transcript entry (FR-056) so it can never silently disagree with the
// worker's own claim (ADR §6) — mirrors TaskExecutor.writeJudgeVerdictTranscript
// for the goal (session) scope.
func (al *AgentLoop) writeGoalVerdictTranscript(store *session.UnifiedStore, sessionID string, verdict *task.JudgeVerdict) {
	if verdict == nil {
		return
	}
	payload, merr := json.Marshal(verdict)
	if merr != nil {
		logger.WarnCF("agent", "goal loop: could not marshal judge verdict for transcript",
			map[string]any{"session_id": sessionID, "error": merr.Error()})
		return
	}
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-judge-%d", sessionID, verdict.Round),
		Type:      session.EntryTypeJudgeVerdict,
		Role:      "system",
		Content:   string(payload),
		AgentID:   verdict.JudgeAgentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal loop: judge verdict transcript write failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return
	}
	// Live push, ONLY once the entry above is durably saved (mirrors
	// recordGoalOutcome's ordering, goal_outcome.go): the WS forwarder
	// (websocket.go's EventKindJudgeVerdict case) turns this into a live
	// generated.JudgeVerdictFrame carrying sessionID as session_id, so the
	// SPA can anchor the card in this goal's own chat session thread — not
	// just the GLOBAL ActivityPanel.
	al.emitEvent(EventKindJudgeVerdict, EventMeta{Source: "goal_loop"},
		JudgeVerdictPayload{SessionID: sessionID, Verdict: *verdict})
}

// --- Idle-expiry sweep (FR-064/D7, review r1) -----------------------------

// goalIdleExpirySweep expires any session with an active `/goal` loop idle
// for longer than its effective IdleExpiryDays bound — the `/goal`
// counterpart to plan_engine.go's PlanEngine.idleExpirySweep, driven from the
// SAME periodic tick (PlanEngine.goalAndLoopIdleExpirySweep) rather than a
// second ticker. "idle" mirrors the plan engine's own definition: no genuine
// round activity — GoalLastActivityAt is bumped on goal-set and on every
// judge round that actually ran (checkGoalLoopAfterTurn), but deliberately
// NOT on a judge-unavailability pause (R9/m4), so a permanently-unavailable
// judge still ends the loop via this calendar brake rather than looping
// forever. now is caller-supplied (PlanEngine's own injectable clock) so
// tests can pin exact idle-boundary math without a real sleep.
// goalIdleExpirySweep's selector (R-06, ADR-086 GOAL-FR-028/FR-049): this
// wave re-points the sweep off session.ListSessions()+GoalCondition (a
// session-owned-only, session-meta-only selector) onto pkg/goal.Store's own
// ListActive() — S1's exported "an active goal record exists for this
// owner" predicate — so idle expiry uses the goal RECORD's own
// LastActivityAt "for both owner kinds" (R-06's own words), not just chat
// sessions.
//
// ADR-086 (S6): this used to run TWO passes — a legacy
// session.ListSessions()+GoalCondition selector alongside the record-driven
// one — because during the delivery's transitional window a chat goal
// existed only in session meta and was invisible to pkg/goal.ListActive().
// That window is closed: every activation path now creates a real record
// (applyGoalCommandPrompt/activateInstantGoal for chat, activateTaskGoal for
// a task) and wave S6 deleted the session-meta fields the legacy pass
// selected on, so that pass could no longer select anything at all. It is
// deleted rather than left as an unreachable branch. Per D-F there is no
// migration path to preserve for a pre-existing session-meta-only goal.
func (al *AgentLoop) goalIdleExpirySweep(cfg config.PlanningConfig, now time.Time) {
	store := al.GetSessionStore()
	if store == nil {
		return
	}
	maxDays := cfg.EffectiveIdleExpiryDays(nil)

	// pkg/goal.Store's own predicate, both owner kinds (R-06).
	if gstore := resolveGoalRecordStore(); gstore != nil {
		active, gerr := gstore.ListActive()
		if gerr != nil {
			logger.WarnCF("agent", "goal idle sweep: list active goal records failed", map[string]any{"error": gerr.Error()})
		}
		for i := range active {
			g := &active[i]
			last := effectiveGoalActivity(g)
			if last.IsZero() {
				continue // nothing to compare against; skip rather than guess
			}
			if now.Sub(last) < time.Duration(maxDays)*24*time.Hour {
				continue
			}
			sessionID := g.ActiveSessionID
			if sessionID == "" {
				logger.WarnCF("agent", "goal idle sweep: active goal record has no active_session_id — cannot expire",
					map[string]any{"goal_id": g.GoalID, "owner_kind": string(g.OwnerKind), "owner_id": g.OwnerID})
				continue
			}
			reason := fmt.Sprintf("idle-expired after %d day(s)", maxDays)
			// ADR-086 parity, same cause as goalQuietWindowSettle's resolution:
			// a TASK goal's session is minted per-agent by
			// task_executor.go::createTaskSessionSync, not in the shared store.
			// The TERMINATION below is safe either way because clearGoalStatus
			// transitions by GOAL id — but agentID resolution and the handover
			// write both go through this store, so on the shared store a task
			// goal expired SILENTLY: the transcript note never landed and
			// writeGoalSystemTranscript only bumped a failure counter. Under
			// operator decision D-A this sweep is the SOLE terminator of a quiet
			// goal, so that note is the only thing that tells anyone it ended.
			recStore := al.ResolveSessionStore(sessionID)
			if recStore == nil {
				recStore = store
			}
			agentID := ""
			if meta, merr := recStore.GetMeta(sessionID); merr == nil && meta != nil {
				agentID = meta.ActiveAgentID
			}
			// clearGoalStatus performs the terminal transition itself, by
			// goal id, which is correct for BOTH owner kinds — the separate
			// terminateGoalRecordByID call this loop used to make first
			// existed only because clearGoal's old transition was
			// session-owner-keyed and therefore a no-op for a task-owned
			// record. Calling it twice would now make the second call log a
			// spurious "not active" warning.
			//
			// silent-SF-7 (review): the handover used to be written to the
			// user's OWN transcript BEFORE this call, and the call's result
			// was discarded. On a store failure the user read "Goal X
			// idle-expired" in their transcript while the record was still
			// ACTIVE and the keeper went on pushing it — a lie the user has
			// no way to detect. Transition FIRST, honour the result, and
			// write the handover only once the goal has actually ended.
			//
			// The handover is the outcome entry's own content, written into
			// recStore (not store) because that is the thread the operator
			// actually reads.
			handover := fmt.Sprintf(
				"Goal %q idle-expired after %d day(s) with no activity (last activity: %s).",
				g.Prompt, maxDays, last.Format(time.RFC3339),
			)
			if _, ok := al.clearGoalWithOutcome(sessionID, recStore, reason, goalOutcomeInput{
				ending:      generated.GoalOutcomeEndingOther,
				roundsUsed:  g.Round,
				maxRounds:   g.MaxRounds,
				judgeReason: g.LatestReason,
				agentID:     agentID,
				content:     handover,
			}); !ok {
				logger.WarnCF("agent", "goal idle sweep: expiry transition failed; no handover written (the goal is still active and will be re-swept)",
					map[string]any{"session_id": sessionID, "goal_id": g.GoalID})
				continue
			}
		}
	}

	// ADR-053 Phase-2 §1 (FR-102/G-2/G-3): the ~60 s quiet-window idle
	// settlement — distinct from the multi-DAY calendar brake above — fires
	// ONE claimless adjudication per goal-id whose quiet window elapsed.
	// Same tick driver (DoD-11: one periodic driver for all goal sweeps).
	al.goalQuietWindowSettle(now)
}

// effectiveGoalActivity returns the best available "last real activity"
// timestamp for a goal: the record's own LastActivityAt when set, falling
// back to StartedAt (mirrors plan_engine.go's effectiveLastActivity).
//
// ADR-086 (GOAL-FR-004): both clocks are now typed time.Time fields on the
// goal record instead of RFC3339 strings on session.UnifiedMeta, so there is
// no parse step and no unparseable-string case left to fall through.
func effectiveGoalActivity(g *goal.Goal) time.Time {
	if g == nil {
		return time.Time{}
	}
	if !g.LastActivityAt.IsZero() {
		return g.LastActivityAt
	}
	if g.StartedAt != nil {
		return *g.StartedAt
	}
	return time.Time{}
}

// writeGoalSystemTranscript writes a plain system-entry note (used for the
// round-bound-reached handover, SD-B9) to the session transcript.
func (al *AgentLoop) writeGoalSystemTranscript(store *session.UnifiedStore, sessionID, agentID, content string) {
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("goal-%s-handover-%d", sessionID, time.Now().UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		Content:   content,
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		taskGoalTranscriptWriteFailures.Add(1)
		logger.WarnCF("agent", "goal loop: handover transcript write failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}
