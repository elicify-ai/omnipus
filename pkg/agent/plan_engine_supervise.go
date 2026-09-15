// plan_engine_supervise.go: Supervise — unmet Definition-of-Done handling and the signature gate (judge rounds, stall parking, supervision wakes and deadlines)

package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// nextSupervisionClaimToken returns a fresh, process-unique, non-empty string
// for wakeSupervisor's CAS claim (see supervisionClaimSeq's doc comment for
// why non-empty and unique-per-attempt both matter). Safe for concurrent use.
func (pe *PlanEngine) nextSupervisionClaimToken() string {
	return fmt.Sprintf("wake-%d", atomic.AddUint64(&pe.supervisionClaimSeq, 1))
}

// --- F2 round-burn gate (lastUnmetTerminalSignature) ----------------------

// unmetTerminalSignatureUnchanged reports whether sig — the CURRENT
// all-terminal member-state signature for planID — matches the signature
// most recently recorded as judged UNMET for that plan. A true result means
// nothing has changed since that round: no member was added/removed and no
// member's terminal outcome changed, so processPlan must skip re-invoking
// beginPlanJudgeRound rather than burn another JudgeRound re-judging
// identical evidence. Uses the two-value map read (not a "" sentinel) so a
// plan that legitimately has zero members (signature == "") is still gated
// correctly once recorded.
func (pe *PlanEngine) unmetTerminalSignatureUnchanged(planID, sig string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	last, ok := pe.lastUnmetTerminalSignature[planID]
	return ok && last == sig
}

// unmetTerminalSignatureRecorded reports whether ANY all-terminal signature has
// ever been recorded as judged UNMET for planID — the "was this ever set"
// question, as distinct from unmetTerminalSignatureUnchanged's "is it still
// this value" (G3 fix wave, finding 4).
//
// They are different questions because the empty string is a LEGAL signature:
// planTerminalSignature(nil) == "", so a plan with zero members records "" and
// a bare `sig == ""` test cannot tell that plan apart from one that was never
// judged at all. Every caller that would otherwise branch on emptiness must ask
// this instead.
func (pe *PlanEngine) unmetTerminalSignatureRecorded(planID string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	_, ok := pe.lastUnmetTerminalSignature[planID]
	return ok
}

// recordUnmetTerminalSignature saves sig as the most recently judged-UNMET
// all-terminal signature for planID. Lazily initializes the backing map
// (same pattern as registry() above) so a bare struct-literal test engine,
// which omits NewPlanEngine's construction, never nil-map-panics on write.
func (pe *PlanEngine) recordUnmetTerminalSignature(planID, sig string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.lastUnmetTerminalSignature == nil {
		pe.lastUnmetTerminalSignature = make(map[string]string)
	}
	pe.lastUnmetTerminalSignature[planID] = sig
}

// clearUnmetTerminalSignature drops any recorded signature for planID. Called
// whenever the plan (re)enters running (tryStartApprovedPlan), so a fresh
// dispatch cycle — a brand-new plan's first run, or an owner's restart/Play
// resume of a previously-failed plan — is never blocked by a signature
// recorded during a prior life of the same plan ID, even if the member
// outcomes happen to end up identical again.
func (pe *PlanEngine) clearUnmetTerminalSignature(planID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	delete(pe.lastUnmetTerminalSignature, planID)
	delete(pe.unmetVerdictAt, planID)
}

// recordUnmetVerdictAt stamps WHEN planID's most recent UNMET verdict landed.
// It is written and cleared in lockstep with lastUnmetTerminalSignature above
// (same call sites, same mu) because it answers the companion question: the
// signature says WHICH evidence was judged unmet, this says WHEN — which is
// what makes "this artifact was produced AFTER the plan was told it had
// failed" decidable for the N-2 gaming guard (see gamingGuardEvidence).
//
// In-memory only, deliberately: the durable half of this pair
// (Plan.LastUnmetTerminalSignature) exists because the round-burn GATE must
// survive a restart or a round gets re-burned. Nothing is re-burned by losing
// a post-hoc ADVISORY flag, so this does not justify a new persisted plan
// field. After a restart the guard simply reports no post-unmet artifacts
// until the next unmet verdict — a quieter guard, never a false claim.
func (pe *PlanEngine) recordUnmetVerdictAt(planID string, at time.Time) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.unmetVerdictAt == nil {
		pe.unmetVerdictAt = make(map[string]time.Time)
	}
	pe.unmetVerdictAt[planID] = at
}

// bumpSupervisionSuppressStreak increments and returns the CONSECUTIVE count
// of wakeSupervisor CAS-suppressions for planID (see supervisionSuppressStreak's
// doc comment). Lazily initializes the backing map (same pattern as
// recordUnmetTerminalSignature above) so a bare struct-literal test engine
// never nil-map-panics on write.
func (pe *PlanEngine) bumpSupervisionSuppressStreak(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.supervisionSuppressStreak == nil {
		pe.supervisionSuppressStreak = make(map[string]int)
	}
	pe.supervisionSuppressStreak[planID]++
	return pe.supervisionSuppressStreak[planID]
}

// clearSupervisionSuppressStreak drops planID's suppression streak. Called the
// moment wakeSupervisor's CAS claim actually SUCCEEDS (a real turn is about to
// be dispatched), so the counter always measures the CURRENT unbroken run of
// suppressions since the last time this plan actually got a turn, never a
// stale count left over from an earlier, already-resolved overlap.
func (pe *PlanEngine) clearSupervisionSuppressStreak(planID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	delete(pe.supervisionSuppressStreak, planID)
}

// bumpJudgeUnavailableStreak increments and returns the CONSECUTIVE count of
// judge rounds abandoned as unavailable for planID (see
// judgeUnavailableStreak's doc comment). Lazily initializes the backing map,
// same pattern as bumpSupervisionSuppressStreak above, so a bare
// struct-literal test engine never nil-map-panics on write.
func (pe *PlanEngine) bumpJudgeUnavailableStreak(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.judgeUnavailableStreak == nil {
		pe.judgeUnavailableStreak = make(map[string]int)
	}
	pe.judgeUnavailableStreak[planID]++
	return pe.judgeUnavailableStreak[planID]
}

// judgeUnavailableParked reports whether planID's CURRENT stall was raised by
// the judge-unavailability backstop (its streak has reached the bound) rather
// than by an ordinary blocked/inbox stall. It is what lets processPlan hold
// back a judge round for the former without wedging the latter — see the
// load-bearing note at its call site.
func (pe *PlanEngine) judgeUnavailableParked(planID string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.judgeUnavailableStreak[planID] >= plan.MaxConsecutiveJudgeUnavailable
}

// clearJudgeUnavailableStreak drops planID's unavailability streak. Called
// whenever a judge round produces a REAL verdict (met or unmet) and whenever
// the plan (re)enters running, so the counter only ever measures the CURRENT
// unbroken run of unavailability.
//
// It drops the park record with it: every caller is an event that proves the
// judge is reachable (a real verdict) or that this is a fresh life for the
// plan id (admission, new generation), and in both cases a park decided under
// the previous run — including its unspent retry budget — must not carry
// over.
func (pe *PlanEngine) clearJudgeUnavailableStreak(planID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	delete(pe.judgeUnavailableStreak, planID)
	delete(pe.judgeUnavailableParks, planID)
}

// recordJudgeUnavailablePark remembers (or refreshes) the judge failure reason
// for planID's park without touching its retry budget. Called at the top of
// every park attempt, so the reason a re-attempt renders is always the most
// recent one the judge actually reported.
func (pe *PlanEngine) recordJudgeUnavailablePark(planID, reason string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.judgeUnavailableParks == nil {
		pe.judgeUnavailableParks = make(map[string]*judgeUnavailablePark)
	}
	if existing, ok := pe.judgeUnavailableParks[planID]; ok {
		existing.reason = reason
		return
	}
	pe.judgeUnavailableParks[planID] = &judgeUnavailablePark{reason: reason}
}

// bumpJudgeUnavailableParkAttempt records one more re-park attempt for planID
// and returns the plan's current unavailability streak, the judge reason to
// render, and the new attempt count. Lazily creates the record (an empty
// reason renders as "no reason reported" via judgeUnavailableReasonText), so a
// caller never has to handle a missing one.
func (pe *PlanEngine) bumpJudgeUnavailableParkAttempt(planID string) (streak int, reason string, attempts int) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.judgeUnavailableParks == nil {
		pe.judgeUnavailableParks = make(map[string]*judgeUnavailablePark)
	}
	rec, ok := pe.judgeUnavailableParks[planID]
	if !ok {
		rec = &judgeUnavailablePark{}
		pe.judgeUnavailableParks[planID] = rec
	}
	rec.attempts++
	return pe.judgeUnavailableStreak[planID], rec.reason, rec.attempts
}

// postUnmetMemberIDs returns the set of members whose artifacts were completed
// AFTER planID's most recent UNMET verdict (gamingGuardEvidence's second
// input). Empty when no unmet verdict has been observed by this process for
// this plan — the pre-first-verdict case and the post-restart case both mean
// "nothing is known to be post-hoc", never "nothing is post-hoc".
//
// A member with an unparseable or absent CompletedAt is NOT flagged: the guard
// exists to weight suspicious evidence DOWN, and flagging on missing data
// would weight ordinary evidence down instead.
func (pe *PlanEngine) postUnmetMemberIDs(planID string, tasks []task.Task) map[string]bool {
	pe.mu.Lock()
	at, ok := pe.unmetVerdictAt[planID]
	pe.mu.Unlock()
	if !ok {
		return nil
	}
	var out map[string]bool
	for i := range tasks {
		t := &tasks[i]
		if t.CompletedAt == "" {
			continue
		}
		completed, err := time.Parse(time.RFC3339, t.CompletedAt)
		if err != nil {
			logger.DebugCF("plan_engine", "gaming guard: member completed_at is unparseable; not flagged post-hoc",
				map[string]any{"plan_id": planID, "task_id": t.ID, "completed_at": t.CompletedAt})
			continue
		}
		if completed.After(at) {
			if out == nil {
				out = make(map[string]bool)
			}
			out[t.ID] = true
		}
	}
	return out
}

// planTerminalSignature builds a deterministic signature of tasks' terminal
// state: each member's id + status + cancel reason, sorted by id and joined
// with ASCII field/record separators (never appearing in an id/status/reason
// value) so no delimiter collision can alias two distinct member sets onto
// the same string. Two calls return an identical signature iff the member
// id set and every member's terminal outcome are identical — this is
// intentionally narrower than "the evidence text is unchanged": editing a
// done task's Result/Prompt without changing its id set or status does NOT
// change the signature (per this fix's spec: "member ids + their terminal
// outcomes + DAG generation" — evidence content is not part of the DAG
// shape). An owner who wants a genuine re-judge without adding/removing a
// member resets that member's status (through a non-terminal state and back)
// or restarts/resumes the plan (which clears the gate outright via
// clearUnmetTerminalSignature).
func planTerminalSignature(tasks []task.Task) string {
	type entry struct {
		id     string
		status task.Status
		reason task.CancelReason
	}
	entries := make([]entry, 0, len(tasks))
	for i := range tasks {
		entries = append(entries, entry{tasks[i].ID, tasks[i].Status, tasks[i].CancelReason})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.id)
		b.WriteByte('\x1f') // unit separator
		b.WriteString(string(e.status))
		b.WriteByte('\x1f')
		b.WriteString(string(e.reason))
		b.WriteByte('\x1e') // record separator
	}
	return b.String()
}

// stallHandoverNote builds the persisted stall-note form of reason: the
// prefix above, CLAMPED to exactly what the store will keep.
//
// The clamp is not optional decoration, and it is here — at the one place both
// stall writers build the value — rather than repeated at each of them. Both
// writers DEDUPE against the persisted value (`p.HandoverText == note`) and
// then mirror it into the in-memory plan (`p.HandoverText = note`). pkg/plan's
// Store.write clamps unconditionally on the way to disk, so a writer that
// builds a raw over-bound note compares a raw string against a clamped one,
// never matches, and therefore re-writes the plan AND re-wakes the supervisor
// on EVERY TICK instead of once — and leaves p.HandoverText holding a value
// disk disagrees with. plan.ClampHandoverText is idempotent, so the write
// path clamping again is a no-op.
//
// The stale-note CLEARING path is unaffected: it matches on
// strings.HasPrefix(p.HandoverText, stallHandoverNotePrefix), and the clamp is
// head-preserving, so the prefix survives by construction.
func stallHandoverNote(reason string) string {
	return plan.ClampHandoverText(stallHandoverNotePrefix + reason)
}

// surfaceStallIfAny persists planStallReason's verdict onto p.HandoverText
// AND p.PlanPhase (PhaseStalled), and wakes the owner exactly once per
// distinct stall condition — mirroring this file's other owner-wake decision
// points (plan_judge_unmet/plan_judge_met/plan_<failed_reason>/
// plan_stopped_by_user) with a further one (plan_stalled, FR-059's intent
// extended to this case): a running plan that can make no progress at all is
// exactly the kind of condition that must be surfaced rather than silently
// spun on.
//
// Wire visibility (swimlane-board UAT fix): PlanPhase=stalled is exposed via
// contracts/components/schemas/Plan.yaml's plan_phase enum and read
// generically by pkg/gateway/rest_plans.go's toWirePlan (which already
// forwards EffectivePlanPhase() verbatim — no code change needed there) and
// by the plan_status WS frame (contracts/asyncapi.yaml, so a live push isn't
// dropped by the SPA's Zod validation). The frontend renders it via
// src/lib/planStateColors.ts's planPhaseChip/planPhaseExplanation, mirroring
// the awaiting_supervision pattern exactly. HandoverText itself stays
// server-only (never wire-exposed) — it names internal task IDs meant for
// the owner AGENT's chat turn, not for the chip.
//
// De-duped by comparing against the CURRENTLY persisted HandoverText/
// PlanPhase (as read by the caller at the top of processPlan, under
// planDecisionMu, so this is race-free within one pass) — an unchanged stall
// condition across ticks re-wakes no one; a materially different one (a
// different blocked/inbox member set) does. Clears a stale stall note
// (recognized via stallHandoverNotePrefix, so this never touches text any
// OTHER path wrote) and reverts PlanPhase to PhaseDispatching once the plan
// is no longer stalled.
//
// PRECEDENCE (see plan.PhaseStalled's doc comment for the full rationale):
// awaiting_supervision is a strictly MORE SPECIFIC condition than a
// generic stall and must never be masked by it. This is guaranteed
// structurally (processPlan's own allMembersTerminal check always intercepts
// before this call while genuinely parked at awaiting_supervision) AND
// enforced explicitly below as a belt-and-suspenders guard, so a future
// refactor of that call order cannot silently reintroduce the bug.
//
// Caller must hold planDecisionMu and must only call this once
// allMembersTerminal(tasks) and planStuckAfterMemberCancel(tasks) have both
// already been ruled out (processPlan's own call order guarantees this).
func (pe *PlanEngine) surfaceStallIfAny(p *plan.Plan, tasks []task.Task) {
	if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision {
		// Never mask a judge dead end with a generic stall note — see the
		// PRECEDENCE doc above. Structurally unreachable in production (see
		// plan.PhaseStalled's doc comment) but kept as an explicit guard.
		return
	}

	reason := pe.planStallReason(tasks, pe.clock.Now())
	if reason == "" {
		if strings.HasPrefix(p.HandoverText, stallHandoverNotePrefix) || p.EffectivePlanPhase() == plan.PhaseStalled {
			cleared := ""
			dispatching := plan.PhaseDispatching
			// The plan is LEAVING the supervision-eligible phase set, so the
			// wake receipt, the wake error and the attempt counter reset
			// (FR-050) — a later re-stall must re-wake rather than inherit a
			// spent deadline. correction_rounds and session_id survive.
			patch := clearSupervisionWakePatch(plan.Patch{HandoverText: &cleared, PlanPhase: &dispatching})
			if _, err := pe.planStore.Update(p.ID, patch); err != nil {
				logger.WarnCF("plan_engine", "could not clear stale stall note/phase",
					map[string]any{"plan_id": p.ID, "error": err.Error()})
			}
		}
		return
	}
	note := stallHandoverNote(reason)
	if p.HandoverText == note && p.EffectivePlanPhase() == plan.PhaseStalled {
		// Already surfaced this exact condition — no repeat FIRST wake. The
		// re-wake for a stalled plan whose adjudication turn produced nothing
		// goes through the supervision deadline instead (FR-029): this guard
		// is a first-wake dedup keyed on its own persisted side effect, not a
		// deadline, and routing the re-wake through it would either fight the
		// guard or require mutating HandoverText to defeat it.
		pe.evaluateSupervisionDeadlineLocked(p)
		return
	}
	stalled := plan.PhaseStalled
	// Captured BEFORE the phase write: a plan stalling out of `dispatching`
	// opens a new park; a plan ALREADY stalled whose stall reason merely
	// changed shape (a different blocked/inbox member set) is the same park
	// continuing, and must keep climbing its attempt ladder rather than reset
	// it — otherwise a plan whose stall text churns every tick would re-arm attempt
	// 1 forever and never reach the FR-022 ceiling.
	newPark := !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase())
	if _, err := pe.planStore.Update(p.ID, plan.Patch{HandoverText: &note, PlanPhase: &stalled}); err != nil {
		logger.WarnCF("plan_engine", "could not persist stall note",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	p.HandoverText = note
	p.PlanPhase = stalled
	// FR-012: the stall wake asks PlanSupervisor for a stall DIAGNOSIS, not a
	// DoD verdict, and not the owner — the owner has no correction role.
	pe.wakeSupervisor(p, buildStallWakeText(p, reason, tasks), "plan_stalled", newPark)
}

// surfaceJudgeUnavailableStall is the terminal-ward exit from the judge
// unavailability retry loop (UAT defect B). It is called once
// plan.MaxConsecutiveJudgeUnavailable consecutive rounds have been abandoned
// without ever producing a verdict, INSTEAD of reverting to `dispatching` for
// yet another attempt. Caller must hold planDecisionMu (applyJudgeRoundOutcome
// holds it across this call) and must have bumped the streak already.
//
// It parks the plan at PhaseStalled and wakes PlanSupervisor, rather than
// failing it outright, for three reasons:
//
//  1. It makes the condition VISIBLE. plan_phase=stalled is already on the
//     wire (contracts/components/schemas/Plan.yaml's plan_phase enum) and
//     already renders as its own chip, so the plan stops claiming "Running /
//     Judging" and starts saying it is stuck — with HandoverText naming the
//     real reason. The defect was never only that the plan did not finish; it
//     was that nothing distinguished a wedged plan from a working one.
//
//  2. It makes the condition ACTIONABLE. PhaseStalled is a
//     supervision-eligible phase, so plan_correct is ACCEPTED. In the live
//     incident the oscillation kept flipping the plan into `judging`, and
//     corrections were rejected outright ("plan is in phase \"judging\";
//     corrections are accepted only while a plan is awaiting supervision or
//     stalled") — so the one mechanism that could have rescued the plan was
//     locked out by the bug itself.
//
//  3. It still TERMINATES. Parking is not a third silent state: a parked plan
//     climbs the bounded supervision attempt ladder, and if no valid
//     correction ever arrives that ladder ends the plan at
//     failed(supervision_unavailable) (FR-022). An unbounded loop becomes a
//     bounded one whose every step is visible.
//
// The streak is deliberately NOT reset here. A correction returns the plan to
// `dispatching` and it will be judged again; if the judge is still down, the
// very next abandoned round re-parks immediately rather than handing out a
// fresh budget of silent retries. Only a REAL verdict — proof the judge is
// reachable — clears it.
//
// The note carries stallHandoverNotePrefix so surfaceStallIfAny recognises it
// as our own stall note and clears it once the plan is genuinely unstuck,
// exactly as it does for the blocked/inbox stall it already owns.
func (pe *PlanEngine) surfaceJudgeUnavailableStall(p *plan.Plan, streak int, judgeReason string) {
	// Recorded BEFORE the write, so a park whose write fails is still known to
	// have been DECIDED — that record is what processPlan's phase switch uses
	// to re-attempt it instead of starting another judge round (H1).
	pe.recordJudgeUnavailablePark(p.ID, judgeReason)

	reason := fmt.Sprintf(
		"The plan judge could not be reached on %d consecutive attempts, so this plan's Definition of "+
			"Done cannot be adjudicated right now. Every member has finished, but without a judge "+
			"verdict the plan cannot be declared done or unmet. Last judge failure: %s. No judge round "+
			"was consumed by these attempts. A correction, or Stop, is needed — retrying on its own has "+
			"already been tried %d times.",
		streak, judgeUnavailableReasonText(judgeReason), streak)
	note := stallHandoverNote(reason)

	stalled := plan.PhaseStalled
	// Captured BEFORE the phase write, same rule as surfaceStallIfAny: a plan
	// arriving here from `judging` opens a new park; one already parked keeps
	// climbing its existing attempt ladder rather than re-arming attempt 1.
	newPark := !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase())
	if _, err := pe.planStore.Update(p.ID, plan.Patch{HandoverText: &note, PlanPhase: &stalled}); err != nil {
		// Loud, and NOT swallowed into a silent retry: if the park cannot be
		// persisted the plan stays at `judging` with no goroutine watching
		// it, which is the wedged state this whole function exists to end.
		//
		// H1: it is no longer left there either. The park record written above
		// survives this failure, so processPlan's phase switch intercepts the
		// plan on its next tick and re-attempts THIS park rather than
		// resuming a judge round — bounded by
		// plan.MaxJudgeUnavailableParkAttempts, terminal past it. Returning
		// here (rather than falling through to the wake) stays correct: there
		// is no park to issue a supervision receipt for.
		logger.ErrorCF("plan_engine",
			"could not park plan at stalled after repeated judge unavailability; the park will be re-attempted and the plan failed closed if it will not persist",
			map[string]any{"plan_id": p.ID, "streak": streak, "error": err.Error()})
		return
	}
	p.HandoverText = note
	p.PlanPhase = stalled

	logger.ErrorCF("plan_engine",
		"plan parked at stalled: plan judge unavailable on consecutive rounds (retry bound reached)",
		map[string]any{
			"plan_id": p.ID, "streak": streak,
			"bound": plan.MaxConsecutiveJudgeUnavailable, "reason": judgeReason,
		})

	pe.wakeSupervisor(p, buildJudgeUnavailableWakeText(p, streak, judgeReason), "plan_stalled", newPark)
}

// reparkJudgeUnavailablePlanLocked handles a plan found at PhaseJudging with
// its judge-unavailability streak already at the bound — i.e. a park that was
// decided but did not take effect. Caller must hold planDecisionMu
// (processPlan holds it across this call).
//
// It is the enforcement half of plan.MaxConsecutiveJudgeUnavailable (H1). The
// bound itself is only a number; what makes it binding is that this function,
// not beginPlanJudgeRound, is what runs on such a plan. Whatever happens here,
// no judge round starts and no streak grows.
//
// The ladder, in order:
//
//  1. Up to plan.MaxJudgeUnavailableParkAttempts times, re-attempt the park.
//     A park write can fail transiently (a momentarily full disk, a locked
//     data directory), and a plan whose members all finished successfully must
//     not be destroyed over one such failure.
//  2. Past that, stop trying to park and END the plan at
//     failed(supervision_unavailable). A park that will not persist is not a
//     park: the adjudicator cannot see it, plan_correct cannot act on it, and
//     the bounded FR-021/FR-022 ladder it was supposed to hand the plan to is
//     never armed. supervision_unavailable is the honest name for that — the
//     same terminal that ladder itself reaches when no adjudicator ever
//     answers (FR-022) — and it needs no new wire value.
//
// The terminal write is the ONE thing that keeps being retried if it too
// fails: each later tick lands back at step 2 and tries again. That is
// deliberate. An engine whose plan store accepts no write at all has no better
// move than to keep trying to record the ending, and it costs nothing that
// matters — no judge round, no LLM call, no streak growth, no member dispatch.
func (pe *PlanEngine) reparkJudgeUnavailablePlanLocked(p *plan.Plan) {
	streak, judgeReason, attempts := pe.bumpJudgeUnavailableParkAttempt(p.ID)

	if attempts > plan.MaxJudgeUnavailableParkAttempts {
		logger.ErrorCF("plan_engine",
			"judge-unavailability park would not persist; failing the plan closed instead of re-judging it",
			map[string]any{
				"plan_id": p.ID, "streak": streak, "park_attempts": attempts - 1,
				"bound": plan.MaxJudgeUnavailableParkAttempts, "reason": judgeReason,
			})
		pe.failPlanLocked(p.ID, plan.FailedReasonSupervisionUnavailable,
			buildUnparkableJudgeUnavailableHandover(p, streak, judgeReason))
		return
	}

	logger.WarnCF("plan_engine",
		"plan still at judging after a judge-unavailability park; re-attempting the park instead of starting another judge round",
		map[string]any{
			"plan_id": p.ID, "streak": streak, "park_attempt": attempts,
			"bound": plan.MaxJudgeUnavailableParkAttempts, "reason": judgeReason,
		})
	pe.surfaceJudgeUnavailableStall(p, streak, judgeReason)
}

// buildUnparkableJudgeUnavailableHandover explains the one terminal a user
// should almost never see: the plan judge was unreachable, AND the engine
// could not even record that fact on the plan. It states both facts plainly,
// because the second one means the plan record the reader is looking at may
// not reflect what actually happened, and it names the members' work as
// intact — an adjudication failure is not a work failure.
func buildUnparkableJudgeUnavailableHandover(p *plan.Plan, streak int, judgeReason string) string {
	return fmt.Sprintf(
		"Plan %q ended without a Definition-of-Done verdict. Two things went wrong: the plan judge "+
			"could not be reached on %d consecutive attempts (last failure: %s), and the attempt to "+
			"park this plan for an adjudicator could not be saved %d times in a row, so the plan could "+
			"neither be judged nor handed over. Every member finished — their work is intact and "+
			"unchanged. Check the plan store for write errors (disk space and permissions on the "+
			"plans directory), then start a new plan to re-adjudicate the same Definition of Done.",
		p.Title, streak, judgeUnavailableReasonText(judgeReason), plan.MaxJudgeUnavailableParkAttempts)
}

// judgeUnavailableReasonText renders the judge's own failure reason for a
// user-facing note, substituting a plain description when the judge reported
// none (an empty Reason must not render as an empty sentence).
func judgeUnavailableReasonText(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no reason reported"
	}
	return reason
}

// buildJudgeUnavailableWakeText is the adjudicator-facing wake for the
// judge-unavailability park. It states the fact, the bound that was reached,
// and the two things PlanSupervisor can actually do about it — deliberately
// NOT asking for a DoD verdict, which is precisely what could not be obtained.
func buildJudgeUnavailableWakeText(p *plan.Plan, streak int, judgeReason string) string {
	return fmt.Sprintf(
		"Plan %q (%s) is stalled: its members have all finished, but the plan judge could not be "+
			"reached on %d consecutive rounds (bound: %d), so the Definition of Done could not be "+
			"adjudicated. Last judge failure: %s. No judge round was consumed. This is an adjudication "+
			"failure, not a work failure — the members' results are intact. Diagnose and either issue a "+
			"correction if the work genuinely needs changing, or abandon the plan if its Definition of "+
			"Done cannot be reached.",
		p.Title, p.ID, streak, plan.MaxConsecutiveJudgeUnavailable, judgeUnavailableReasonText(judgeReason))
}

// --- Plan-level judge round (SD-B8) ---------------------------------------

// beginPlanJudgeRound starts (or, per the boot/crash-resume case above,
// restarts) one plan-level judge round for p. Caller must hold
// planDecisionMu. Checks the round ceiling first (fails the plan closed if
// exhausted) — that check always runs, unconditionally, even on an unchanged
// all-terminal state: an already-exhausted plan must still terminate, this
// is not itself a "re-judge" (no judge call happens, no round is debited).
// Only once the ceiling has NOT been reached does the F2 round-burn gate
// (acceptance G-9) apply: if tasks is the plan's current all-terminal member
// snapshot and its signature matches the one already recorded as judged
// UNMET for this plan, the round is skipped entirely — "one round then
// wait; no re-judge of unchanged state." Otherwise claims a judgeSema lane
// (skips this tick — retried next pass — if the lane is full) and launches
// the actual judge call in its own goroutine, decoupled from the tick/event
// cycle (planJudgeRoundTimeout) so a slow-but-alive judge call never blocks
// dispatch of other plans.
//
// tasks is the caller's already-fetched member-task snapshot (processPlan's
// normal path) or the crash-resume snapshot (may be nil on a resume-time
// list failure — allMembersTerminal(nil) is vacuously true with signature
// "", but the in-memory gate map is guaranteed unpopulated for p.ID at that
// point in a freshly-booted process, so this can never spuriously block a
// genuine crash-resume; see this file's package doc on lastUnmetTerminalSignature).
func (pe *PlanEngine) beginPlanJudgeRound(p *plan.Plan, tasks []task.Task) {
	cfg := pe.planningConfig()
	var boundsOverride *int
	if p.Bounds != nil {
		boundsOverride = p.Bounds.PlanJudgeMaxRounds
	}
	maxRounds := cfg.EffectivePlanJudgeMaxRounds(boundsOverride)
	if p.JudgeRounds >= maxRounds {
		pe.failPlanLocked(p.ID, plan.FailedReasonJudgeRoundsExhausted, buildPlanRoundsExhaustedHandover(p, maxRounds))
		return
	}

	if allMembersTerminal(tasks) {
		sig := planTerminalSignature(tasks)
		if pe.unmetTerminalSignatureUnchanged(p.ID, sig) {
			logger.DebugCF("plan_engine",
				"plan judge round skipped: all-terminal state unchanged since last UNMET verdict (awaiting supervision)",
				map[string]any{"plan_id": p.ID})
			// FR-023: the parked plan's per-tick supervision pass. It sits
			// AFTER the unconditional round-ceiling check above by design —
			// an exhausted parked plan must terminate rather than re-wake
			// (the ceiling check is the first thing this function does).
			pe.evaluateSupervisionDeadlineLocked(p)
			return
		}
	}

	acquired, release := pe.judgeSema.TryAcquire()
	if !acquired {
		logger.DebugCF("plan_engine", "plan judge lane full; retrying next tick", map[string]any{"plan_id": p.ID})
		return
	}

	judging := plan.PhaseJudging
	if _, err := pe.planStore.Update(p.ID, plan.Patch{PlanPhase: &judging}); err != nil {
		release()
		logger.WarnCF("plan_engine", "could not set plan_phase=judging",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}

	// Register the round as in-flight BEFORE launching the goroutine —
	// exactly the same synchronous timing the old inFlightJudge[p.ID]=true
	// write had (FR-037 extends this "assign before dispatch" rule to
	// verifiers generally; here it doubles as the plan-round's own
	// crash-resume liveness marker — see verifier_registry.go's package
	// doc). The verifier session id is not known yet at this point (judge.go
	// creates it once the round actually runs JudgeCriteria), so this call
	// registers an empty-session placeholder under the SAME
	// verifierUnitForPlan key runVerifierAdjudication upserts the real id
	// through (F1: both sides must agree on this key) before dispatching the
	// verifier's turn.
	if regErr := pe.registry().Register(verifierUnitForPlan(p.ID), ""); regErr != nil {
		// CAS guard (corr-MAJOR-3, G-1): a LIVE verifier session already holds
		// this plan unit — a judge round is already in flight. Release the
		// round slot and bail rather than launching a duplicate round whose
		// verifier turn would race the in-flight one (exactly-once violation).
		// The phase=judging set above is owned by the in-flight round's own
		// lifecycle from here.
		release()
		logger.WarnCF("plan_engine", "plan judge: round already in-flight; CAS rejected placeholder registration",
			map[string]any{"plan_id": p.ID})
		return
	}

	pe.judgeWG.Add(1)
	go pe.runPlanJudgeRound(p.ID, release)
}

// runPlanJudgeRound performs ONE plan-level judge round. It ALWAYS clears
// its judgeSema slot, verifier-registry entry, and judgeWG count on return
// (defers below), regardless of outcome.
func (pe *PlanEngine) runPlanJudgeRound(planID string, release func()) {
	defer pe.judgeWG.Done()
	defer release()
	defer pe.registry().Unregister(verifierUnitForPlan(planID))
	// Belt-and-braces for the judge-unavailability pause (noteJudgeUnavailable):
	// the round's OWN cleanup clears it on EVERY exit path, including the ones
	// that never reach applyJudgeRoundOutcome at all (the ctx timeout,
	// the reload/list bails above, a Stop landing mid-round so the outcome is
	// dropped). Without this a cancelled round could leave a plan paused
	// forever on a reason that no longer describes anything — a worse failure
	// than the silent stall this whole mechanism exists to replace. Prefix-
	// guarded inside, so it can never clear an owner-disabled pause.
	defer pe.clearJudgeUnavailablePause(planID)

	ctx, cancel := context.WithTimeout(context.Background(), planJudgeRoundTimeout)
	defer cancel()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		logger.WarnCF("plan_engine", "judge round: could not reload plan",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}

	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "judge round: could not list member tasks",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}
	// F2 round-burn gate (acceptance G-9): the signature of the EXACT
	// all-terminal member state this round is about to judge. If the round
	// ends UNMET, applyJudgeRoundOutcome records this so a later
	// unchanged idle tick's processPlan skips re-judging it.
	terminalSig := planTerminalSignature(tasks)

	criteria := p.DoD
	if len(criteria) == 0 {
		if soft := SoftTierCriterion(p.Title, p.Description, p.Goal); soft != nil {
			criteria = []task.AcceptanceCriterion{*soft}
		}
	}
	if len(criteria) == 0 {
		// SD-A7 soft tier: title/description/goal all empty too — nothing to
		// judge at all. Trust completion directly rather than looping
		// forever. Still routed through applyJudgeRoundOutcome's own
		// fresh State==running re-check (FR-014) — a Stop can land during the
		// taskStore.List call above just as easily as during a real judge
		// call, so this short-circuit gets the SAME atomicity guarantee as
		// the real-verdict path below, not a bespoke unlocked shortcut.
		// nothingToJudge always trusts completion (never UNMET), so
		// terminalSig is passed through but never recorded.
		pe.applyJudgeRoundOutcome(planID, JudgeCriteriaResult{}, true, terminalSig)
		return
	}

	// ADR-055 fix wave, finding 3: the correction state THIS round must be
	// judged under. Both are read once, here, and threaded into the two halves
	// of the Judge's input — the outcome list (which withholds a superseded
	// member's claim) and the extra context (which names the withheld members
	// and any post-hoc artifacts explicitly). Read before the judge call, not
	// inside the builders, so a correction landing mid-round cannot make the
	// claim text and the guard block disagree with each other.
	superseded := pe.supersededMemberSet(planID)
	postUnmet := pe.postUnmetMemberIDs(planID, tasks)

	result := pe.judge.JudgeCriteria(ctx, JudgeCriteriaInput{
		Scope:           task.VerdictScopePlan,
		PlanID:          p.ID,
		AssigneeAgentID: p.OwnerAgentID,
		Criteria:        criteria,
		Attempt:         p.JudgeRounds + 1,
		ClaimText:       buildPlanClaimText(tasks, superseded),
		ExtraContext:    buildPlanJudgeExtraContext(p) + gamingGuardEvidence(tasks, superseded, postUnmet),
		// Product-blocker fix (ADR-052 FR-011/012 x ADR-046 P1): the plan's
		// own workspace (plan.go:264) — same rationale as task_executor.go's
		// task-scope call. See JudgeCriteriaInput.WorkspaceID.
		WorkspaceID: p.WorkspaceID,
		// MERGE NOTE 2026-09-15 (release/v0.1.1 + library-improvements):
		// integrate carried a silent-stall fix here — OnUnavailable/OnRecovered
		// hooks on JudgeCriteriaInput that surfaced "waiting on the judge,
		// retrying in …" as Plan.PausedReason. The founder ruled the judge
		// comes from release (#683), whose JudgeCriteriaInput has no such
		// hooks, so the wiring is dropped here. Re-apply as a follow-up on the
		// release judge if the stall notice is still wanted.
	})

	// FR-014 (US-6 acceptance 3, Test 7): JudgeCriteria runs OUTSIDE
	// planDecisionMu by design (this whole goroutine is decoupled from the
	// lock precisely so a slow-but-alive judge call never blocks other
	// plans' dispatch — see beginPlanJudgeRound's doc comment), so a Stop
	// can land on this exact plan at any point up to and including the
	// instant this call returns. Delegate to applyJudgeRoundOutcome,
	// which re-checks State==running and applies the outcome as ONE atomic
	// critical section under planDecisionMu.
	pe.applyJudgeRoundOutcome(planID, result, false, terminalSig)
}

// clearJudgeUnavailablePause retracts a judge-unavailability pause from
// planID, and ONLY that: a PausedReason set by anything else (owner_disabled)
// is left exactly as it is. It is called from three places, all of which must
// be individually sufficient — OnRecovered (the judge came back mid-round),
// applyJudgeRoundOutcome's Unavailable branch (the round gave up and reverted
// to dispatching), and runPlanJudgeRound's unconditional defer (every other
// exit path, including ones that reach neither of the first two).
//
// Deliberately NOT gated on State==running: a round that ends after a Stop
// must still retract its marker from the now-failed plan.
func (pe *PlanEngine) clearJudgeUnavailablePause(planID string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()
	pe.clearJudgeUnavailablePauseLocked(planID)
}

// clearJudgeUnavailablePauseLocked is clearJudgeUnavailablePause's body for
// callers that ALREADY hold planDecisionMu (applyJudgeRoundOutcome).
// planDecisionMu is a plain sync.Mutex — re-entering it would deadlock.
func (pe *PlanEngine) clearJudgeUnavailablePauseLocked(planID string) {
	current, err := pe.planStore.Get(planID)
	if err != nil {
		if !errors.Is(err, plan.ErrNotFound) {
			logger.WarnCF("plan_engine", "judge round: could not reload plan to clear judge-unavailability pause",
				map[string]any{"plan_id": planID, "error": err.Error()})
		}
		return
	}
	if !plan.IsJudgeUnavailablePausedReason(current.PausedReason) {
		return
	}
	cleared := ""
	if _, uerr := pe.planStore.Update(planID, plan.Patch{PausedReason: &cleared}); uerr != nil {
		logger.WarnCF("plan_engine", "judge round: could not clear judge-unavailability pause",
			map[string]any{"plan_id": planID, "error": uerr.Error()})
	}
}

// applyJudgeRoundOutcome applies a just-computed plan-level judge
// round result to planID — but ONLY after acquiring planDecisionMu and
// re-confirming, from a FRESH read, that the plan is still `running`, and it
// keeps holding that lock across every write the outcome requires.
//
// ADR-052 FR-014/§6.4(b) TOCTOU fix (7-reviewer + architect gate,
// "plan-scope variant"): PRE-FIX, the equivalent re-check
// (verdictStillApplicable) ran under its OWN separate, momentary lock
// acquisition and then RELEASED it — the outcome writes that followed
// (PlanPhase revert on Unavailable, JudgeRounds/PlanPhase/HandoverText on an
// unmet verdict, the two synthesizeAndComplete writes on a met verdict, and
// the wakeOwner side effects any of those trigger) were entirely
// UNPROTECTED plain store.Update calls. plan.Store's own transition guard
// rejects a stale State write (failed->done is illegal), but it has no
// opinion on the OTHER fields — so a Stop landing in the gap between that
// released lock and these writes could still see its own HandoverText
// clobbered by the round's steering text, and could still fire a spurious
// plan_judge_unmet/plan_judge_met wakeOwner notification for a plan the
// user had just stopped. Collapsing recheck+apply into ONE lock acquisition
// closes that gap: every OTHER real plan-state mutator
// (StopPlan/StopTask/tryStartApprovedPlan/idleExpireOneLocked) also takes
// planDecisionMu before touching plan state, so nothing can interleave here
// once this function has re-read and confirmed `running`.
//
// nothingToJudge is true for the SD-A7 soft-tier-empty short-circuit
// (completePlan), in which case result is ignored.
//
// terminalSig is the planTerminalSignature (F2 round-burn gate, acceptance
// G-9) of the all-terminal member state runPlanJudgeRound actually judged —
// on an UNMET verdict it is recorded as this plan's "already judged this,
// don't re-judge until it changes" marker so a later unchanged idle tick's
// processPlan skips re-invoking beginPlanJudgeRound.
func (pe *PlanEngine) applyJudgeRoundOutcome(planID string, result JudgeCriteriaResult, nothingToJudge bool, terminalSig string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	current, err := pe.planStore.Get(planID)
	if err != nil || current.State != plan.StateRunning {
		logger.InfoCF("plan_engine",
			"plan judge round outcome dropped: plan left running during adjudication (Stop landed concurrently)",
			map[string]any{"plan_id": planID})
		return
	}

	if nothingToJudge {
		pe.completePlan(current)
		return
	}

	if result.Unavailable {
		// D7: 0 rounds consumed, idle clock NOT bumped (spec R9/m4: judge
		// unavailability pauses do not reset the idle clock). Revert
		// plan_phase to dispatching so the plan is picked up for a fresh
		// round attempt on the next tick/event rather than staying stuck at
		// "judging" with no goroutine watching it (only reached once ctx's
		// OWN timeout fires — JudgeCriteria itself retries forever otherwise,
		// respecting ctx).
		//
		// UAT defect B: that retry is now BOUNDED. Burning no round is still
		// right — a provider timeout is not the user's fault — but "costs
		// nothing" must not mean "forever". Past the bound the plan parks at
		// PhaseStalled instead of spinning; see surfaceJudgeUnavailableStall.
		if streak := pe.bumpJudgeUnavailableStreak(current.ID); streak >= plan.MaxConsecutiveJudgeUnavailable {
			pe.surfaceJudgeUnavailableStall(current, streak, result.Reason)
			return
		}
		dispatching := plan.PhaseDispatching
		if _, uerr := pe.planStore.Update(current.ID, plan.Patch{PlanPhase: &dispatching}); uerr != nil {
			logger.WarnCF("plan_engine", "judge round: could not revert plan_phase after unavailability",
				map[string]any{"plan_id": current.ID, "error": uerr.Error()})
		}
		// The round is over, so its judge-unavailability pause marker no
		// longer describes anything live — retract it in the SAME critical
		// section that reverts the phase, so the plan is never observable as
		// "dispatching AND paused on a judge that is no longer being waited
		// for". Prefix-guarded: an owner-disabled pause is left alone.
		pe.clearJudgeUnavailablePauseLocked(current.ID)
		logger.WarnCF("plan_engine", "plan judge round abandoned (judge unavailable)",
			map[string]any{"plan_id": current.ID, "reason": result.Reason})
		return
	}

	// A real verdict arrived: whatever it says, the judge is reachable, so
	// the unavailability streak is over. Cleared BEFORE the met/unmet split
	// so both outcomes get it — an unmet verdict is a working judge just as
	// much as a met one.
	pe.clearJudgeUnavailableStreak(current.ID)

	verdict := result.Verdict
	// FR-178: JudgeRounds (plan/goal scope) and AttemptCount (per member/task)
	// are TWO DISTINCT brakes, never conflated. This line — the only place
	// JudgeRounds is incremented — is the SOLE writer of the plan's rounds
	// counter; it never touches a member task's AttemptCount, symmetric to
	// TaskExecutor.consumeTaskAttempt being the sole writer of
	// AttemptCount (which never touches JudgeRounds). Whichever trips first
	// stops its OWN scope locally. Pinned by TestAttemptsVsRounds_DistinctBrakes.
	newRounds := current.JudgeRounds + 1
	pe.touchActivity(current.ID)

	// GOAL-FR-036/FR-040: project the verdict's per-criterion outcomes onto
	// current.DoD's Status field — the third of the projection's three
	// write paths (verdict_projection.go), plan.DoD being a single list (no
	// de-union needed, unlike the goal path's criteria/dod split). Runs on
	// BOTH met and unmet outcomes; a soft-tier adjudication (the DoD passed
	// to JudgeCriteria at the dispatch site was the ephemeral synthesized
	// fallback, current.DoD itself empty) naturally resolves as the
	// GOAL-FR-031 logged no-op the projection already implements. Persisted
	// separately, BEFORE the outcome-specific write below, so a persist
	// failure here is independently logged and never blocks the plan's own
	// met/unmet transition (this store write and the transition write are
	// two different fields of the same record; a projection failure must
	// not silently drop or corrupt the round's actual outcome).
	if projectedDoD, pstats := projectVerdictOntoCriteria(current.DoD, verdict); pstats.Applied > 0 {
		if _, perr := pe.planStore.Update(current.ID, plan.Patch{DoD: &projectedDoD}); perr != nil {
			logger.WarnCF("plan_engine",
				"judge round: could not persist the verdict projection onto plan DoD (GOAL-FR-036)",
				map[string]any{"plan_id": current.ID, "error": perr.Error()})
		} else {
			current.DoD = projectedDoD
		}
	}

	if verdict.Met {
		pe.synthesizeAndComplete(current, newRounds)
		return
	}

	steering := buildPlanSteeringText(verdict)
	// ADR-053 C1/FR-147 (INV-2/INV-7): an UNMET verdict on an all-terminal DAG
	// (the only kind of state the plan Judge ever fires on) durably parks the
	// plan at plan_phase=awaiting_supervision — NOT back to dispatching —
	// persisting the unmet terminal signature so the F2 round-burn gate
	// survives restart. The plan-owner session sits at lifecycle `paused`
	// (Phase-2 owner loop's responsibility); the boot sweep exempts that
	// paused session from the failed(interrupted) sweep via OwnsPlanID ->
	// plan.PlanPhase == awaiting_supervision (FR-118 exemption b).
	awaiting := plan.PhaseAwaitingSupervision
	// Captured BEFORE the phase write below: an UNMET verdict on a plan that
	// was not already parked opens a new park, and the wake's attempt counter
	// and adjudication session are scoped to that park (see wakeSupervisor's
	// newPark parameter). Read generically rather than hardcoded true so a
	// future call path that re-judges an already-parked plan cannot silently
	// reset its ladder.
	newPark := !plan.IsSupervisionEligiblePhase(current.EffectivePlanPhase())
	sig := terminalSig
	if _, uerr := pe.planStore.Update(current.ID, plan.Patch{
		JudgeRounds:                &newRounds,
		PlanPhase:                  &awaiting,
		HandoverText:               &steering,
		LastUnmetTerminalSignature: &sig,
	}); uerr != nil {
		logger.ErrorCF("plan_engine", "judge round: could not persist unmet verdict",
			map[string]any{"plan_id": current.ID, "error": uerr.Error()})
		return
	}
	// F2 round-burn gate (acceptance G-9): this exact all-terminal state has
	// now been judged UNMET — record it so processPlan's next idle tick(s)
	// wait instead of re-judging the same evidence again. Recorded only
	// after the store write above actually succeeds, so a persist failure
	// (round effectively didn't happen) never blocks a legitimate retry.
	// Persisted above (LastUnmetTerminalSignature) AND mirrored into the
	// in-memory map here: the in-memory map is the in-process authority (it is
	// cleared on tryStartApprovedPlan), the persisted field is the
	// across-restart authority (rehydrated by bootReconcile) — together they
	// close the standalone-F2 restart gap (C1).
	pe.recordUnmetTerminalSignature(current.ID, terminalSig)
	// Stamp WHEN this verdict landed, in the same breath as WHICH evidence it
	// judged: everything a member completes after this instant is post-hoc
	// evidence for the N-2 gaming guard (see recordUnmetVerdictAt).
	pe.recordUnmetVerdictAt(current.ID, pe.clock.Now().UTC())
	// Keep the in-memory snapshot consistent with what was just persisted —
	// wakeSupervisor below reads the phase through it.
	current.JudgeRounds = newRounds
	current.PlanPhase = awaiting
	current.HandoverText = steering
	current.LastUnmetTerminalSignature = sig
	// FR-012: the UNMET wake goes to the ADJUDICATOR, not the owner. The
	// owner has no correction role, so the message no longer says "awaiting
	// YOUR correction".
	pe.wakeSupervisor(current, pe.buildDoDUnmetWakeText(current, newRounds, steering), "plan_judge_unmet", newPark)
}

// synthesizeAndComplete records the PASS round (or, for completePlan's
// nothing-to-judge case, leaves JudgeRounds unchanged), persists
// plan_phase=synthesizing AND State=done in ONE atomic store write, and only
// THEN wakes the owner to write the closing synthesis — "plan judge PASS" IS
// the definition of Done (plan.go's own StateDone doc comment); the engine
// does not block waiting for the owner's synthesis reply (which is an
// ordinary, possibly-never-answered chat turn) before finalizing the
// mechanical outcome. plan_phase is deliberately left at "synthesizing" after
// Done as a historical marker of how the plan finished. Caller must hold
// planDecisionMu (see applyJudgeRoundOutcome/completePlan).
//
// Fix-wave finding 2 (14-reviewer sign-off): this USED TO persist
// plan_phase=synthesizing, wake the owner (telling them the plan is MET),
// and only THEN persist State=done — with that final write's error only
// logged. A persist failure there left the plan stuck at State=running
// forever (still picked up by the dispatch/idle-expiry loop) even though the
// owner had already been told the outcome was final. Mirrors the sibling
// UNMET path (applyJudgeRoundOutcome): persist the terminal outcome
// FIRST as a single write, and wake only after that write actually
// succeeds — a persist failure here means the round "didn't happen" from the
// owner's perspective, so no wake fires and a later tick can retry.
func (pe *PlanEngine) synthesizeAndComplete(p *plan.Plan, newRounds int) {
	synthesizing := plan.PhaseSynthesizing
	done := plan.StateDone
	if _, err := pe.planStore.Update(p.ID, plan.Patch{
		JudgeRounds: &newRounds,
		PlanPhase:   &synthesizing,
		State:       &done,
	}); err != nil {
		logger.ErrorCF("plan_engine", "could not persist plan judge PASS (phase=synthesizing, state=done)",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		return
	}
	p.PlanPhase = synthesizing
	p.State = done

	// ⚠ REGRESSION ANCHOR (FR-012/FR-012b): this wake target and its ordering
	// are pinned. This is the ONLY wake on the plan's success path — do NOT
	// re-target it to the supervisor, or a plan that SUCCEEDS notifies nobody.
	// The owner is also the right author of a closing synthesis: it is the
	// agent accountable to the requester and the only one holding the
	// requester's conversational context. It fires only once the State=done
	// write above has actually landed (see fix-wave finding 2 above) — never
	// before.
	pe.wakeOwner(p, fmt.Sprintf(
		"Plan %q: the plan judge confirmed the Definition of Done is MET. "+
			"Please write a closing synthesis summarizing the outcome for the requester.",
		p.Title,
	), "plan_judge_met")
}

// completePlan handles the SD-A7 soft-tier-empty case (no DoD, no
// title/description/goal text worth judging at all): nothing to adjudicate,
// so the plan is trusted complete directly. (A task with nothing to judge
// fails its run instead — task_run_loop.go::adjudicateRunClaim.) Caller must hold planDecisionMu (applyJudgeRoundOutcome's
// own re-checked lock, or FR-041/idle-expiry's — every call site already
// holds it before reaching here).
func (pe *PlanEngine) completePlan(p *plan.Plan) {
	pe.touchActivity(p.ID)
	pe.synthesizeAndComplete(p, p.JudgeRounds)
}

// supervisionUnitForPlan is the mutual-exclusion key for the "at most one
// PlanSupervisor turn in flight per plan" invariant wakeSupervisor's CAS gate
// enforces (see that function's doc comment for the full race this closes).
//
// Deliberately a DIFFERENT key namespace from verifierUnitForPlan
// (verifier_registry.go): that key marks the plan-level JUDGE round in
// flight, a different activity that legitimately overlaps in time with a
// PRIOR supervision turn still concluding (the plan transitions through
// PhaseJudging — not itself supervision-eligible — while an earlier
// supervision turn's goroutine is still running). Only two SUPERVISION turns
// for the SAME plan must never overlap; reusing verifierUnitForPlan's own key
// here would conflate the two and either deadlock the judge round against a
// slow supervision turn or vice versa. Both keys share the SAME underlying
// VerifierSessionRegistry (pe.registry()) — a plain unit->string map keyed by
// prefixed strings — so a new prefix is all a new mutual-exclusion domain
// needs; no new primitive is introduced (ADR-053's claim/marker family is
// reused, not reinvented).
func supervisionUnitForPlan(planID string) string { return "supervision:" + planID }

// wakeSupervisor issues a SUPERVISION wake (family A) for a plan sitting in
// the supervision-eligible phase set: it mints (or reuses) the park's
// adjudication session, stamps the durable wake receipt + attempt counter that
// arm FR-021's deadline, and dispatches PlanSupervisor's turn directly.
//
// No outbound message is published on this path at all — not to the plan's
// origin, not anywhere. That is a property of the seam (there is no publish to
// suppress), not a send that happens to fail.
//
// newPark says whether this wake OPENS a new park or re-issues within the park
// already in progress, and the caller computes it as "the plan was not already
// in the supervision-eligible phase set before this transition". It is passed
// explicitly rather than inferred from supervision.wake_at because the wake
// receipt is cleared by a SEPARATE store write from the phase change that ends
// a park (countCorrectionAndClearWake), and that write can fail: inferring the
// park boundary from a stale receipt then charges a brand-new park's FIRST wake
// as attempt N+1 and hands it the previous park's adjudication session,
// shortening the escalation ladder by however many attempts the previous park
// had already spent (G3 fix wave, finding 6 — the durable consequence
// countCorrectionAndClearWake's own comment missed).
//
// ⚠ CONCURRENCY FIX (production race, confirmed live): a plan-level judge
// round completing UNMET transitions the plan THROUGH plan.PhaseJudging —
// which is NOT in the supervision-eligible phase set — on its way to
// awaiting_supervision. That makes applyJudgeRoundOutcome's newPark
// computation (!IsSupervisionEligiblePhase(current-phase-at-reload)) come out
// true even when a PRIOR supervision wake (e.g. a stall diagnosis) is STILL
// being reasoned about by a turn that has not yet completed: the plan's
// PERSISTED phase at the moment of reload is transiently Judging, not Stalled,
// so the DoD-unmet wake reads as "a brand-new park" and would otherwise mint
// and dispatch a SECOND, independent PlanSupervisor session while the first is
// still live. Two adjudicators then race plan_correct against the same plan —
// observed live as multiple supervision sessions minted seconds apart, with
// every attempt after the first failing "the plan is already in a failed
// state". This is a TIMING/mutual-exclusion defect, not a shape defect (both
// wakes named the real plan/member ids correctly).
//
// The fix does NOT try to correct newPark's phase-transition classification
// (that would still leave the underlying "is a turn already running" question
// unasked for every other call path — the retry/rearm wakes below share this
// function too). Instead it gates ALL FOUR callers uniformly on a single,
// explicit invariant: at most one live PlanSupervisor turn per plan, enforced
// by a CAS claim on the SAME VerifierSessionRegistry primitive ADR-053 already
// uses for the judge round's own in-flight marker (verifier_registry.go),
// keyed by supervisionUnitForPlan (a distinct namespace — see its own doc
// comment for why). Register("") claims the plan as a placeholder BEFORE any
// state is touched (mirrors the registry's own "assign before dispatch"
// convention); ErrVerifierSessionHeld means a turn is already in flight for
// this plan, REGARDLESS of which reason (stall vs DoD-unmet vs retry vs
// rearm) triggered either call, and this wake attempt is suppressed
// (back off, per the registry's own documented CAS contract) rather than
// racing a second turn — the plan's existing park record is left completely
// untouched, so whatever condition prompted this call remains recorded
// (HandoverText / LastUnmetTerminalSignature) and is re-evaluated the next
// time this plan is observed as supervision-eligible.
//
// The claim is released when the dispatched turn actually SETTLES — turn
// completion (success, error, or panic-recovered), not merely "dispatch was
// attempted" — via the onTurnSettled callback threaded through
// dispatchPlanTurn, so a suppressed wake is never permanent: once the holder
// finishes (or, on a synchronous dispatch failure, immediately), the claim is
// free again and the NEXT tick's/event's call to this function can dispatch a
// genuinely fresh turn. A holder that crashes mid-turn releases nothing
// explicitly, but the registry is process-local (verifier_registry.go's own
// documented contract) — a restart rebuilds it empty, so a crashed holder
// cannot wedge a plan past that restart, and short of a restart the existing
// FR-021 observation deadline (supervisionTurnTimeout) already bounds the
// turn's own context, so a genuinely wedged turn still exits (context
// deadline) and releases its claim within that same bound.
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) wakeSupervisor(p *plan.Plan, content, sourceKind string, newPark bool) {
	// BOUND THE WAKE PROMPT HERE, and only here.
	//
	// Four builders feed this function (buildStallWakeText,
	// buildJudgeUnavailableWakeText, buildDoDUnmetWakeText,
	// buildSupervisionRetryWakeText) and three of them embed text of arbitrary
	// length that no bound has ever applied to: a provider error body
	// (judgeUnavailableReasonText), the plan judge's own steering, and a
	// per-member target block unbounded in member count. That text never
	// touches the plan store, so pkg/plan's Store.write clamp — the fix for the
	// identical exposure on the persisted handover — does not cover it. This is
	// the same context-budget hazard arriving by a second route.
	//
	// One call at the chokepoint every wake must pass through, rather than one
	// per builder: a clamp a builder can forget is not a bound. A new wake
	// builder added later is covered without being told.
	//
	// SAME BOUND AS THE HANDOVER, deliberately. plan.ClampHandoverText's limit
	// is an AGENT CONTEXT BUDGET (it is not a wire limit — handover_text
	// appears nowhere in contracts/ — and not a store limit), and this prompt
	// is the very context it was chosen to defend: HandoverText's own budget
	// exists because it gets re-embedded verbatim into THIS string. Giving the
	// wake its own, larger number would be a second magic constant with no
	// measurement behind it, and would let a wake blow a budget the note it
	// derives from already respects.
	//
	// ⚠ THE TARGET BLOCK IS EXEMPT, AND THAT EXEMPTION IS LOAD-BEARING. A flat
	// plan.ClampHandoverText(content) here would be a regression, not a fix:
	// ClampHandoverText is HEAD-preserving, every wake builder puts the
	// supervision target block at the TAIL, and that block is where `plan_id:`
	// lives (buildSupervisionTargetsText). PlanSupervisor is seeded exactly one
	// tool — plan_correct — which cannot be called without a plan_id, and it
	// has no other way to resolve one. Clamping the tail off therefore produces
	// a wake asking for a correction the agent is structurally incapable of
	// issuing, while still burning an attempt off the supervision budget: that
	// is ADR-055 fix-wave finding 2, verbatim, and
	// TestSupervisionWakes_CarryEverythingPlanCorrectNeeds exists because it
	// already happened once.
	//
	// The exemption costs nothing, because the tail is the part that was never
	// unbounded: the member list is capped at supervisionTargetsMaxMembers with
	// each title cut to supervisionTargetTitleLimit runes. Everything that is
	// genuinely unbounded — the provider error body, the judge's steering, the
	// stall reason's member enumeration — is in the HEAD, which is exactly what
	// clampWakePrompt bounds.
	content = clampWakePrompt(content)

	// FR-046b-adjacent brake (G3 fix wave, finding 5). See
	// correctionBudgetSpent: without this, the stall -> correct -> run -> stall
	// cycle has NO terminal state at all.
	if maxRounds, spent := pe.correctionBudgetSpent(p); spent {
		logger.ErrorCF("plan_engine", "supervision correction budget spent; terminating the plan instead of waking again",
			map[string]any{
				"plan_id":           p.ID,
				"plan_phase":        string(p.EffectivePlanPhase()),
				"correction_rounds": p.Supervision.CorrectionRounds,
				"max_rounds":        maxRounds,
				"source_kind":       sourceKind,
			})
		pe.failPlanLocked(p.ID, plan.FailedReasonDoDUnreachable,
			buildCorrectionBudgetHandover(p, maxRounds))
		return
	}

	// CAS claim: at most one live PlanSupervisor turn per plan (see the doc
	// comment above). Taken BEFORE anything else — no session is minted, no
	// receipt is stamped, nothing is dispatched — so a suppressed wake has
	// zero side effects on the plan record. The registered value MUST be
	// non-empty and unique to THIS attempt (nextSupervisionClaimToken) — the
	// registry's CAS treats an empty value as an unclaimed placeholder, so
	// two Register(unit, "") calls would never conflict with each other; see
	// supervisionClaimSeq's doc comment for the full reasoning.
	supervisionUnit := supervisionUnitForPlan(p.ID)
	if regErr := pe.registry().Register(supervisionUnit, pe.nextSupervisionClaimToken()); regErr != nil {
		// Code review finding (LOW): the attempt counter is what makes a
		// genuinely wedged claim distinguishable from routine overlap in the
		// logs — a count of 1-2 across a few ticks is an ordinary in-flight
		// turn about to settle; a count climbing into the dozens/hundreds
		// (the same claim held through that many consecutive wake attempts)
		// is the operator-actionable signal that the holder may be wedged
		// (which would also be blocking the plan's own stall-abandon path,
		// since that path funnels through this same claim). See
		// supervisionSuppressStreak's doc comment.
		streak := pe.bumpSupervisionSuppressStreak(p.ID)
		logger.InfoCF("plan_engine",
			"supervision wake suppressed: a supervision turn is already in flight for this plan",
			map[string]any{"plan_id": p.ID, "source_kind": sourceKind, "consecutive_suppressions": streak})
		return
	}
	// The claim succeeded — a turn is actually about to run for this plan, so
	// any consecutive-suppression streak accumulated while a PRIOR turn held
	// the claim is now moot; start the next run of suppressions (if any) from
	// zero rather than compounding across unrelated holders.
	pe.clearSupervisionSuppressStreak(p.ID)
	var released bool
	var releaseMu sync.Mutex
	releaseClaim := func() {
		releaseMu.Lock()
		defer releaseMu.Unlock()
		if released {
			return
		}
		released = true
		pe.registry().Unregister(supervisionUnit)
	}

	sessionID := pe.ensureSupervisionSessionLocked(p, newPark)

	// ⚠ PERSIST THE HANDLE BEFORE DISPATCHING THE TURN. This is the same
	// assign-before-dispatch rule the verifier registry already follows, and
	// for the same reason: a Stop landing in the window between "the turn is
	// running" and "the record names its session" would find nothing to
	// cancel. Ordering the other way round leaves an uncancellable turn for
	// exactly as long as one store write takes.
	attempts := 1
	if !newPark && p.Supervision != nil {
		attempts = p.Supervision.Attempts + 1
	}
	wakeAt := pe.clock.Now().UTC().Format(time.RFC3339)
	noError := ""
	patch := plan.Patch{
		SupervisionWakeAt:    &wakeAt,
		SupervisionAttempts:  &attempts,
		SupervisionWakeError: &noError,
	}
	if sessionID != "" {
		patch.SupervisionSessionID = &sessionID
	}
	updated, err := pe.planStore.Update(p.ID, patch)
	if err != nil {
		logger.ErrorCF("plan_engine", "could not persist supervision wake receipt; wake not dispatched",
			map[string]any{"plan_id": p.ID, "error": err.Error()})
		// The turn was never dispatched — release the claim now, or this plan
		// would be permanently locked out of every future supervision wake by
		// a claim nothing will ever clear.
		releaseClaim()
		return
	}
	// Keep the caller's snapshot current: several call sites read
	// p.Supervision again in the same locked body.
	p.Supervision = updated.Supervision

	// releaseClaim is threaded through as onTurnSettled: dispatchPlanTurn calls
	// it itself, exactly once, either synchronously (a dispatch failure below —
	// no goroutine ever ran) or from the dispatched goroutine's own defer chain
	// once the turn truly settles (success, error, or panic-recovered). Do NOT
	// also call it here on the error branch — dispatchPlanTurn already did.
	if dispatchErr := pe.dispatchPlanTurn(p.ID, planSupervisorAgentID, sessionID, content, sourceKind, pe.supervisionTurnTimeout(p), releaseClaim); dispatchErr != nil {
		// FR-024 / §20: an undelivered supervision wake is RECORDED on the
		// plan, not WARNed away — "parked" and "silently stuck" must be
		// distinguishable from the record alone. Unlike a missing chat origin
		// this IS a real failure, so escalating it to
		// failed(supervision_unavailable) at the ceiling is a TRUE diagnosis.
		logger.ErrorCF("plan_engine", "supervision wake could not dispatch an adjudication turn",
			map[string]any{
				"plan_id":     p.ID,
				"agent_id":    planSupervisorAgentID,
				"source_kind": sourceKind,
				"attempt":     attempts,
				"error":       dispatchErr.Error(),
			})
		wakeErr := dispatchErr.Error()
		if errored, uerr := pe.planStore.Update(p.ID, plan.Patch{SupervisionWakeError: &wakeErr}); uerr != nil {
			logger.ErrorCF("plan_engine", "could not record the supervision wake error",
				map[string]any{"plan_id": p.ID, "error": uerr.Error()})
		} else {
			p.Supervision = errored.Supervision
		}
		return
	}
	logger.InfoCF("plan_engine", "supervision wake dispatched",
		map[string]any{
			"plan_id":     p.ID,
			"session_id":  sessionID,
			"source_kind": sourceKind,
			"attempt":     attempts,
		})
}

// evaluateSupervisionDeadlineLocked is FR-021's observation seam and FR-022's
// post-turn state machine, run once per tick for a plan sitting in the
// supervision-eligible phase set.
//
// Why a deadline and not a callback: the wake is fire-and-forget and the turn
// path reports nothing back into this engine (N8). A callback would create a
// new coupling for a signal the engine can already infer — it owns the plan
// record, and whether the record MOVED is a complete proxy for whether the
// adjudication turn produced anything. It is also the shape every other brake
// here already has (round ceiling, idle expiry).
//
// The predicate, all limbs required:
//
//	phase ∈ {awaiting_supervision, stalled}   (the caller's own gate too)
//	supervision.wake_at is set                (a wake was actually issued)
//	now > wake_at + supervision_turn_timeout  (STRICTLY greater)
//	the unmet-terminal signature is unchanged
//
// The signature limb is VACUOUS on the stall path — no unmet signature is ever
// set there — so for a stalled plan the operative limbs are the phase, the
// wake receipt and the elapsed deadline. Reading "signature unchanged" as
// "a signature exists" would make the predicate unreachable for every stall.
//
// wake_at rehydrates from disk and is never re-armed at boot, so the deadline
// is honoured from its ORIGINAL stamp across a restart and a restart loop
// cannot reset the ceiling.
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) evaluateSupervisionDeadlineLocked(p *plan.Plan) {
	if !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		return
	}
	if p.Supervision == nil || p.Supervision.WakeAt == "" {
		// G3 fix wave, finding 2: this is NOT "nothing to time out yet" — by the
		// time any tick observes a supervision-eligible plan, its park's wake was
		// already issued synchronously inside the same locked section that parked
		// it. A missing receipt therefore means wakeSupervisor's receipt WRITE
		// failed and it returned without dispatching, and every wake entry point
		// is once-per-park (the stall note de-dupes on its own persisted side
		// effect; the DoD-unmet round de-dupes on the terminal signature), so
		// nothing else ever re-arms it. Returning here is what parked such a plan
		// FOREVER: no deadline, no retry, no ceiling, no supervision_unavailable,
		// with only idle expiry (days, and not restartable) to end it. The field
		// that would have recorded the fault, supervision.wake_error, was part of
		// the write that failed.
		//
		// Re-arm instead. newPark=false: the ladder continues from whatever the
		// record says (0 for a park that never got its first receipt written, so
		// this lands as attempt 1 anyway), which means a store that stays broken
		// still climbs to the FR-022 ceiling instead of looping unbounded.
		logger.WarnCF("plan_engine", "supervision-eligible plan has no wake receipt; re-arming the wake",
			map[string]any{
				"plan_id":    p.ID,
				"plan_phase": string(p.EffectivePlanPhase()),
			})
		pe.wakeSupervisor(p, pe.buildSupervisionRetryWakeText(p), "plan_supervision_rearm", false)
		return
	}
	wakeAt, err := time.Parse(time.RFC3339, p.Supervision.WakeAt)
	if err != nil {
		logger.WarnCF("plan_engine", "supervision wake_at is unparseable; deadline not evaluated",
			map[string]any{"plan_id": p.ID, "wake_at": p.Supervision.WakeAt, "error": err.Error()})
		return
	}
	if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision &&
		p.LastUnmetTerminalSignature == "" && !pe.unmetTerminalSignatureRecorded(p.ID) {
		// The signature is written once on the UNMET verdict and cleared only
		// by an applied correction, so "still set" IS "unchanged". Cleared
		// means the record moved — the turn produced something.
		//
		// G3 fix wave, finding 4: "cleared" and "never set" are NOT the same
		// state, and testing emptiness alone conflated them. A plan with ZERO
		// members has planTerminalSignature(nil) == "", so its UNMET park
		// records an EMPTY signature — legitimately, that is its signature —
		// and this predicate then read it as "the record moved" on every tick.
		// Such a plan parked, got exactly one wake, and then fell out of the
		// escalation ladder entirely: no deadline, no retry, no terminal state.
		// The companion "was a signature ever recorded for this plan" question
		// is what actually distinguishes them, and only the recorded-ness map
		// (mirrored durably by the plan field, rehydrated at boot for every
		// parked plan — see bootReconcile) can answer it.
		return
	}
	if !pe.clock.Now().UTC().After(wakeAt.Add(pe.supervisionTurnTimeout(p))) {
		return // strict >: at exactly wake_at+timeout the deadline does NOT fire
	}

	maxAttempts := pe.supervisionMaxAttempts(p)
	if p.Supervision.Attempts < maxAttempts {
		// FR-022(a): re-issue, WITHOUT waking the owner — there is nothing to
		// tell it yet. wakeSupervisor stamps a fresh receipt and increments
		// the attempt count.
		logger.InfoCF("plan_engine", "supervision turn produced nothing before its deadline; re-issuing the wake",
			map[string]any{
				"plan_id":      p.ID,
				"plan_phase":   string(p.EffectivePlanPhase()),
				"attempts":     p.Supervision.Attempts,
				"max_attempts": maxAttempts,
			})
		pe.wakeSupervisor(p, pe.buildSupervisionRetryWakeText(p), "plan_supervision_retry", false)
		return
	}

	// FR-022(b): the ceiling is spent. Terminate with a reason distinct from
	// every other terminal cause, and wake the owner with a handover that says
	// adjudication was unavailable — NOT that the plan's work failed.
	logger.ErrorCF("plan_engine", "supervision attempt ceiling exhausted; terminating the plan",
		map[string]any{
			"plan_id":      p.ID,
			"plan_phase":   string(p.EffectivePlanPhase()),
			"attempts":     p.Supervision.Attempts,
			"max_attempts": maxAttempts,
			"wake_error":   p.Supervision.WakeError,
		})
	pe.failPlanLocked(p.ID, plan.FailedReasonSupervisionUnavailable,
		buildSupervisionUnavailableHandover(p, maxAttempts))
}

// supervisionTurnTimeout resolves FR-021's observation deadline for p:
// a per-plan Bounds override wins, else the global
// planning.supervision_turn_timeout_seconds, else the documented 600 s default.
//
// pkg/config returns SECONDS (it deliberately carries no scheduling
// semantics); the conversion to a Duration happens here, and this accessor is
// the ONLY place it happens.
func (pe *PlanEngine) supervisionTurnTimeout(p *plan.Plan) time.Duration {
	var override *int
	if p != nil && p.Bounds != nil {
		override = p.Bounds.SupervisionTurnTimeoutSeconds
	}
	secs := pe.planningConfig().EffectiveSupervisionTurnTimeoutSeconds(override)
	return time.Duration(secs) * time.Second
}

// correctionBudgetSpent reports whether p has consumed its plan-lifetime
// adjudicator-correction budget, and the ceiling it was measured against
// (G3 fix wave, finding 5).
//
// THE HOLE THIS CLOSES. Every other brake in this engine is defeated on the
// stall -> correct -> stall cycle, individually and by design:
//
//   - JudgeRounds never advances, because a non-terminal DAG never reaches
//     beginPlanJudgeRound — that is the ONLY writer of the rounds counter.
//   - supervision.attempts is reset to 0 by every applied correction
//     (clearSupervisionWakePatch), so the FR-022 ceiling never accumulates.
//   - supervision.correction_rounds DOES advance and is never reset, but until
//     this function nothing read it: it was documented as attribution-only.
//   - idle expiry never fires, because a correction calls touchActivity.
//
// So an adjudicator that keeps appending tail work to a plan that keeps
// re-stalling loops forever, with no terminal state except the adjudicator
// voluntarily choosing `abandon`. correction_rounds is the one counter that
// both survives the cycle and advances with it, which makes it the only honest
// place to put the brake.
//
// The ceiling REUSES plan_judge_max_rounds rather than introducing a knob:
// it is the same question ("how many adjudication cycles may this plan have")
// measured on the other path, and reusing it means the DoD-UNMET path is
// completely unaffected — there, every correction is preceded by a judge round,
// so JudgeRounds >= CorrectionRounds always holds and the judge-rounds ceiling
// (checked first, in beginPlanJudgeRound) always fires first. This brake bites
// only on the path that had none.
//
// The terminal reason is dod_unreachable, not judge_rounds_exhausted: rounds
// may well remain, and this is the involuntary half of the pair
// plan.FailedReasonDoDUnreachable documents — "more corrections would not
// help", established by evidence rather than adjudicated.
func (pe *PlanEngine) correctionBudgetSpent(p *plan.Plan) (maxRounds int, spent bool) {
	if p == nil || p.Supervision == nil {
		return 0, false
	}
	var override *int
	if p.Bounds != nil {
		override = p.Bounds.PlanJudgeMaxRounds
	}
	maxRounds = pe.planningConfig().EffectivePlanJudgeMaxRounds(override)
	if maxRounds <= 0 {
		// A non-positive ceiling is not "no corrections allowed" — it is an
		// unconfigured/degenerate bound, and beginPlanJudgeRound already fails
		// such a plan on its first round. Never terminate a plan on it here.
		return maxRounds, false
	}
	return maxRounds, p.Supervision.CorrectionRounds >= maxRounds
}

// buildCorrectionBudgetHandover renders the terminal handover for a plan that
// spent its correction budget. It says what was tried and how often, because
// "the adjudicator corrected this plan N times and it kept stalling" is the
// operator-actionable fact — not the individual stall reason, which the plan
// record already carries.
func buildCorrectionBudgetHandover(p *plan.Plan, maxRounds int) string {
	rounds := 0
	if p.Supervision != nil {
		rounds = p.Supervision.CorrectionRounds
	}
	return fmt.Sprintf(
		"Plan %q has been stopped: the adjudicator applied %d correction(s) (max %d) and the plan "+
			"still could not reach a terminal outcome — it returned for supervision again.\n\n"+
			"This is not a verdict that the plan's work failed, and it is not an exhausted judge "+
			"round budget: it is evidence that further corrections were not converging. The plan "+
			"was last parked at %q with this diagnosis:\n\n%s\n\n"+
			"Review the Definition of Done itself before re-authoring.",
		p.Title, rounds, maxRounds, p.EffectivePlanPhase(), p.HandoverText,
	)
}

// supervisionMaxAttempts resolves FR-022's no-correction attempt ceiling for
// p: a per-plan Bounds override wins, else the global
// planning.supervision_max_attempts, else the documented default of 3.
func (pe *PlanEngine) supervisionMaxAttempts(p *plan.Plan) int {
	var override *int
	if p != nil && p.Bounds != nil {
		override = p.Bounds.SupervisionMaxAttempts
	}
	return pe.planningConfig().EffectiveSupervisionMaxAttempts(override)
}

// --- Text builders ---------------------------------------------------------

// buildPlanClaimText renders the member-outcome evidence the plan Judge
// adjudicates the Definition of Done against.
//
// ⚠ REGRESSION ANCHOR (ADR-055 fix wave, finding 3) — the `superseded` set is
// NOT optional decoration. This function is the ONLY place a member outcome
// reaches the Judge, so it is the ONLY place SUPERSEDE can mean anything.
//
// Pre-fix, supersede was inert end to end: it recorded a map entry that
// nothing on the judge path ever read. The adjudicator could supersede a done
// member whose outcome was wrong, attach replacement work carrying its
// criteria, pass both integrity rules, get a success result back — and the
// next judge round still received that member's original claim verbatim, with
// no annotation. The PlanSupervisor's own rubric and the plan_correct tool
// description both promise the outcome is "ignored by the Judge"; that promise
// was made good here and nowhere else.
//
// A superseded member's RESULT TEXT is replaced, not merely labelled: leaving
// the wrong claim in the prompt next to a note asking the model to disregard
// it is exactly the shape of instruction models are least reliable at
// following, and the claim is precisely the evidence the correction exists to
// discount. The member's LINE stays (with its id and immutable status) because
// the Judge still needs to see the member exists — the replacement work
// depends on it structurally, and a silently vanishing member reads as a plan
// that lost work.
//
// The member ID is printed on every line, superseded or not: the gaming-guard
// block (gamingGuardEvidence) names members BY ID, and a Judge that cannot map
// those ids onto these outcome lines cannot act on either.
func buildPlanClaimText(tasks []task.Task, superseded map[string]bool) string {
	if len(tasks) == 0 {
		return "(this plan has no member tasks)"
	}
	var sb strings.Builder
	sb.WriteString("Member task outcomes:\n")
	for i := range tasks {
		t := &tasks[i]
		if superseded[t.ID] {
			fmt.Fprintf(&sb, "- %s [%s] (%s): SUPERSEDED by a plan correction — this member's "+
				"outcome is WITHHELD and must not count as evidence for or against any criterion. "+
				"Replacement work carrying its acceptance criteria appears elsewhere in this list; "+
				"judge the criterion on that work.\n", t.Title, t.ID, t.Status)
			continue
		}
		fmt.Fprintf(&sb, "- %s [%s] (%s): %s\n", t.Title, t.ID, t.Status, truncateForClaim(t.Result))
	}
	return sb.String()
}

const planClaimTruncateLimit = 500

func truncateForClaim(s string) string {
	if len(s) <= planClaimTruncateLimit {
		return s
	}
	return s[:planClaimTruncateLimit] + "..."
}

func buildPlanJudgeExtraContext(p *plan.Plan) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Plan: %s\n", p.Title)
	if p.Goal != "" {
		fmt.Fprintf(&sb, "\nGoal: %s\n", p.Goal)
	}
	if p.Description != "" {
		fmt.Fprintf(&sb, "\nDescription: %s\n", p.Description)
	}
	return sb.String()
}

func buildPlanSteeringText(v *task.JudgeVerdict) string {
	var sb strings.Builder
	sb.WriteString("The plan judge reviewed this round and found the Definition of Done UNMET:\n")
	for _, c := range v.PerCriterion {
		if !c.Met {
			fmt.Fprintf(&sb, "- criterion %s: %s\n", c.CriterionID, c.Reason)
		}
	}
	return sb.String()
}

// clampWakePrompt bounds the DIAGNOSIS half of a supervision wake prompt and
// leaves the target block untouched. See wakeSupervisor for why the split
// exists and why a flat head-preserving clamp over the whole prompt would be a
// regression.
//
// The split is taken at the LAST target-block marker, not the first: the
// diagnosis half is provider- or judge-authored text that may legitimately
// contain anything, including a line that looks like a marker. The real block
// is always the last one, because only the engine's own builders append it.
//
// A prompt with no target block at all (no current builder produces one, but
// nothing structurally prevents it) is clamped whole — a bound that applies is
// better than one that is skipped because the shape was unfamiliar.
func clampWakePrompt(s string) string {
	head, tail := splitAtSupervisionTargets(s)
	if tail == "" {
		return plan.ClampHandoverText(s)
	}
	clampedHead := plan.ClampHandoverText(head)
	// The marker ClampHandoverText appends ends with "]" and no newline, while
	// tail begins at a line start by construction. Without this the two are
	// glued into one line reading "...clamped: N of M ...]plan_id: p-42", and
	// every reader that parses the block LINE-WISE — which is how an agent
	// reads it, and how wakePlanID/wakeMemberID model that — stops finding the
	// plan id. Preserving the whole block is pointless if it is unreadable.
	//
	// The guard is conditional so an in-bounds prompt (the overwhelming
	// majority) is returned byte-identical: head already ends with the newline
	// the split cut on, or is empty when the block opens the prompt.
	if clampedHead != "" && !strings.HasSuffix(clampedHead, "\n") {
		clampedHead += "\n"
	}
	return clampedHead + tail
}

// supervisionTargetsMarker opens every supervision target block. It is a
// shared constant rather than a literal in one place and a matcher in another
// because two things depend on it agreeing exactly: buildSupervisionTargetsText
// writes it, and clampWakePrompt finds it to decide where a wake prompt stops
// being clampable diagnosis and starts being the block PlanSupervisor cannot
// act without. Drift between the two would silently re-enable the clamp over
// the target block.
const supervisionTargetsMarker = "plan_id: "

// splitAtSupervisionTargets splits s immediately before the last line that
// opens a supervision target block. Returns (s, "") when there is none.
func splitAtSupervisionTargets(s string) (head, tail string) {
	idx := -1
	if strings.HasPrefix(s, supervisionTargetsMarker) {
		idx = 0
	}
	if i := strings.LastIndex(s, "\n"+supervisionTargetsMarker); i >= 0 {
		idx = i + 1
	}
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx:]
}

// supervisionTargetsMaxMembers caps the target block at a plan size well above
// anything the correction verbs can act on in one wake (plan.MaxTailMembers
// bounds a single correction's ADDITIONS; this bounds what we ENUMERATE). A
// plan larger than this gets a truncation notice rather than an unbounded
// prompt.
const supervisionTargetsMaxMembers = 50

// buildSupervisionTargetsText renders the machine-actionable identity block
// described above. planID is always emitted, even with no members, because
// plan_id is required for EVERY verb — including abandon, which names no
// member at all.
func buildSupervisionTargetsText(planID string, tasks []task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s%s\n", supervisionTargetsMarker, planID)
	if len(tasks) == 0 {
		sb.WriteString("Members: (none)\n")
		return sb.String()
	}
	sb.WriteString("Members (member_id | status | title) — supersede names a `done` member_id, " +
		"targeted_retry names a `failed` member_id:\n")
	shown := tasks
	if len(shown) > supervisionTargetsMaxMembers {
		shown = shown[:supervisionTargetsMaxMembers]
	}
	for i := range shown {
		t := &shown[i]
		fmt.Fprintf(&sb, "- %s | %s | %s\n", t.ID, t.Status, truncateTargetTitle(t.Title))
	}
	if len(tasks) > len(shown) {
		fmt.Fprintf(&sb, "(+%d more members not listed)\n", len(tasks)-len(shown))
	}
	return sb.String()
}

// --- Supervision wake target block (ADR-055 fix wave, finding 2) -----------
//
// ⚠ REGRESSION ANCHOR. EVERY supervision wake MUST carry this block, because
// the wake prompt is the adjudication turn's ONLY input and PlanSupervisor
// cannot look ANY of it up.
//
// Pre-fix, all three wake texts (stall, DoD-unmet, retry) identified the plan
// by TITLE. But plan_correct requires `plan_id` and rejects the call without
// it, and supersede/targeted_retry additionally require a MEMBER id. The
// PlanSupervisor is seeded exactly ONE tool — plan_correct: allow, every other
// static tool deny (pkg/coreagent/core.go) — so it has no way to resolve a
// title to an id; list_jobs would not help either, since it filters to rows
// the principal OWNS and a System Agent can never own a plan. Every wake
// therefore asked for a correction the agent was structurally incapable of
// issuing, while each wake still stamped the attempt counter BEFORE dispatch
// — so the plan burned its whole supervision budget and terminated
// failed(supervision_unavailable).
//
// Compactness is a real constraint (this is a per-wake token cost on every
// attempt), so the block is a header line plus one line per member: id,
// status, truncated title. No descriptions, no results — the member OUTCOMES
// are the Judge's evidence, not the adjudicator's target list; what the
// adjudicator needs from this block is addressability.

// supervisionTargetTitleLimit truncates a member title in the wake target
// block. The title is a human hint for choosing between members; the id is the
// load-bearing part and is never truncated.
const supervisionTargetTitleLimit = 80

// truncateTargetTitle bounds a member title to supervisionTargetTitleLimit
// RUNES (not bytes), so a multi-byte title is never cut mid-character into
// invalid UTF-8 in a prompt. Distinct from this package's truncateRunes, whose
// truncation note ("... (truncated, output continues)") is written for a
// multi-line tool output, not for one field of a single-line table row.
func truncateTargetTitle(s string) string {
	r := []rune(s)
	if len(r) <= supervisionTargetTitleLimit {
		return s
	}
	return string(r[:supervisionTargetTitleLimit]) + "..."
}

// supervisionTargets is the store-reading wrapper for the block above, for the
// wake sites that do not already hold a member snapshot. A list failure
// DEGRADES to the id-only block rather than omitting the block: without the
// member list the adjudicator can still append or abandon, but without the
// plan id it can do nothing at all.
func (pe *PlanEngine) supervisionTargets(planID string) string {
	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "could not list members for the supervision wake target block; waking with plan id only",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return buildSupervisionTargetsText(planID, nil)
	}
	return buildSupervisionTargetsText(planID, tasks)
}

// buildStallWakeText is the text of a FIRST supervision wake on the STALL path
// (FR-012). tasks is the caller's own member snapshot — the very one
// planStallReason diagnosed — not a fresh store read: the wake must describe
// the state it is reporting on.
func buildStallWakeText(p *plan.Plan, reason string, tasks []task.Task) string {
	return fmt.Sprintf(
		"Plan %q is stalled: it is running with real work remaining, but no member is "+
			"dispatchable or in flight.\n\n%s\n\n%s\nDiagnose the stall and, if it is correctable, "+
			"apply a correction. This is a stall diagnosis, not a Definition-of-Done verdict.",
		p.Title, reason, buildSupervisionTargetsText(p.ID, tasks),
	)
}

// buildDoDUnmetWakeText is the text of a FIRST supervision wake on the
// DoD-UNMET path (FR-012). Unlike the stall wake, its caller holds no member
// snapshot (the round's snapshot lives in the judge goroutine that already
// returned), so the target block is read fresh from the store.
func (pe *PlanEngine) buildDoDUnmetWakeText(p *plan.Plan, rounds int, steering string) string {
	return fmt.Sprintf(
		"Plan %q round %d: the plan judge found the Definition of Done UNMET.\n\n%s"+
			"\n%s"+
			"\nThe plan is parked awaiting supervision. Adjudicate it: append tail "+
			"members, SUPERSEDE a done member whose outcome is wrong, TARGETED-RETRY a "+
			"failed member, or ABANDON the plan if its Definition of Done is unreachable.",
		p.Title, rounds, steering, pe.supervisionTargets(p.ID),
	)
}

// buildSupervisionRetryWakeText is the text of a RE-ISSUED supervision wake
// (FR-022(a)). It names the attempt so the adjudicator can tell a retry from a
// first wake, and re-states the plan's own diagnosis (the judge's steering, or
// the stall note) because the previous turn left no artefact behind.
//
// It is a METHOD, not a free function, solely so it can read the plan's live
// member snapshot for the target block — see buildSupervisionTargetsText. A
// retry wake needs that block at least as much as a first wake does: it exists
// precisely because the previous attempt produced no correction.
func (pe *PlanEngine) buildSupervisionRetryWakeText(p *plan.Plan) string {
	attempt := 0
	if p.Supervision != nil {
		attempt = p.Supervision.Attempts
	}
	return fmt.Sprintf(
		"Plan %q is still awaiting supervision after attempt %d produced no correction.\n\n%s\n\n%s\n"+
			"Adjudicate it now: append tail members, SUPERSEDE a done member whose outcome is "+
			"wrong, TARGETED-RETRY a failed member, or ABANDON the plan if its Definition of "+
			"Done is unreachable.",
		p.Title, attempt, p.HandoverText, pe.supervisionTargets(p.ID),
	)
}

// buildSupervisionUnavailableHandover is FR-022(b)'s terminal handover. It is
// deliberately distinct from every other terminal message: the plan's WORK did
// not fail and its round budget was not spent — adjudication never produced a
// usable outcome, which is an operator-facing condition, not a plan-facing one.
func buildSupervisionUnavailableHandover(p *plan.Plan, maxAttempts int) string {
	return fmt.Sprintf(
		"Plan %q could not be reviewed: %d supervision attempt(s) were issued and none produced "+
			"a correction, so the plan has been stopped.\n\n"+
			"This is an ADJUDICATION failure, not a verdict on the plan's work. The plan was "+
			"parked at %q with the following diagnosis:\n\n%s\n\n"+
			"No other agent inherits adjudication for this plan.",
		p.Title, maxAttempts, p.EffectivePlanPhase(), p.HandoverText,
	)
}

func buildPlanRoundsExhaustedHandover(p *plan.Plan, maxRounds int) string {
	return fmt.Sprintf(
		"Plan judge exhausted after %d round(s) (max %d) without a PASS verdict.\n\n"+
			"Last steering:\n%s\n\n"+
			"Review the plan's member tasks and judge history; the plan has been marked "+
			"failed and its owner notified.",
		p.JudgeRounds, maxRounds, p.HandoverText,
	)
}

// ensureSupervisionSessionLocked returns the adjudication session id for the
// CURRENT park, minting one on the park's first wake (FR-016b).
//
// One session per park: re-wakes within the same park share it, so a Stop can
// cancel whichever attempt is in flight; a new park mints a new one.
//
// The park boundary comes from the CALLER (newPark), not from
// supervision.wake_at. The receipt is still required to reuse a session — a
// park with no armed wake has no in-flight attempt to share — but it is no
// longer the sole authority, because the write that clears it at the end of a
// park can fail (see wakeSupervisor's own newPark doc), and a stale receipt
// then makes a NEW park silently inherit the previous park's adjudication
// transcript. session_id itself is deliberately NEVER cleared (an applied
// correction returns the plan to dispatching while the adjudication turn may
// still be running, and blanking the handle in that window would leave a Stop
// unable to name the turn it must cancel), so it cannot serve as the boundary
// either.
//
// Caller must hold planDecisionMu.
func (pe *PlanEngine) ensureSupervisionSessionLocked(p *plan.Plan, newPark bool) string {
	if !newPark && p.Supervision != nil && p.Supervision.WakeAt != "" && p.Supervision.SessionID != "" {
		return p.Supervision.SessionID // same park, same session
	}
	sessionID, err := pe.mintPlanSession(planSupervisorAgentID, "Plan supervision: "+p.Title)
	if err != nil {
		logger.ErrorCF("plan_engine", "could not mint the plan supervision session",
			map[string]any{"plan_id": p.ID, "agent_id": planSupervisorAgentID, "error": err.Error()})
		return ""
	}
	return sessionID
}

// clearSupervisionWakePatch returns the per-field supervision patch applied
// when a plan LEAVES the supervision-eligible phase set (FR-050's lifecycle
// table). Three fields reset, two survive:
//
//   - wake_at    -> cleared. Disarms the deadline; a later re-park re-wakes,
//     and the next park mints its own session.
//   - wake_error -> cleared.
//   - attempts   -> reset to 0.
//   - correction_rounds -> UNTOUCHED. Cumulative for the life of the plan and
//     NEVER reset. A plan leaves this phase set on every applied correction,
//     so a blanket reset zeroes it immediately after every increment and every
//     terminal record reads 0 — which inverts the only thing it is read for
//     (telling "the round budget ran out with no correction ever applied"
//     apart from "corrections consumed the budget").
//   - session_id -> UNTOUCHED, never blanked, only overwritten by the next
//     mint. See ensureSupervisionSessionLocked.
func clearSupervisionWakePatch(patch plan.Patch) plan.Patch {
	empty := ""
	zero := 0
	patch.SupervisionWakeAt = &empty
	patch.SupervisionWakeError = &empty
	patch.SupervisionAttempts = &zero
	return patch
}

// --- Owner-gaming-DoD guards (N-2) -----------------------------------------

// gamingGuardEvidence annotates a member-outcome snapshot for the Judge with
// gaming-guard metadata (N-2). The ladder weights deterministic rungs (check/
// behavior) over prose; artifacts produced AFTER the unmet verdict are flagged
// post-hoc so the Judge can weight them appropriately. This is a pure helper
// the Judge-input builder calls — it does NOT alter the verdict itself.
//
// postUnmetMemberIDs is the set of member IDs whose artifacts were produced
// after the plan entered awaiting_supervision (detected by comparing the
// member's CompletedAt against the plan's last unmet-verdict timestamp — see
// PlanEngine.postUnmetMemberIDs). The guard flags them in the extra-context
// text the Judge receives.
//
// ⚠ This function is CALLED FROM PRODUCTION (runPlanJudgeRound appends it to
// the Judge's ExtraContext). It spent the whole of ADR-055's first pass with a
// test as its only caller, which is the reason SUPERSEDE shipped inert — do
// not let it drift back to test-only.
//
// tasks supplies the id -> title mapping (and the member ORDER), so the guard
// block names members the same way buildPlanClaimText's outcome lines do
// ("title [id]"). A flagged id with no matching member — possible only if the
// two snapshots disagree — is still listed, by id alone, rather than dropped:
// silently omitting a caveat is the one failure mode a guard must not have.
func gamingGuardEvidence(tasks []task.Task, superseded map[string]bool, postUnmetMemberIDs map[string]bool) string {
	if len(postUnmetMemberIDs) == 0 && len(superseded) == 0 {
		return ""
	}
	label := make(map[string]string, len(tasks))
	for i := range tasks {
		label[tasks[i].ID] = fmt.Sprintf("%s [%s]", tasks[i].Title, tasks[i].ID)
	}
	// Member order first (matches the claim text), then any id not present in
	// the snapshot, sorted for determinism.
	render := func(set map[string]bool) string {
		out := make([]string, 0, len(set))
		seen := make(map[string]bool, len(set))
		for i := range tasks {
			if id := tasks[i].ID; set[id] {
				out = append(out, label[id])
				seen[id] = true
			}
		}
		var orphans []string
		for id := range set {
			if !seen[id] {
				orphans = append(orphans, id)
			}
		}
		sort.Strings(orphans)
		return strings.Join(append(out, orphans...), ", ")
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Gaming-guard (N-2)\n")
	sb.WriteString("The evidence ladder weights deterministic rungs (machine checks, behavior ")
	sb.WriteString("scans) over prose self-attestation. The following caveats apply:\n")
	if len(superseded) > 0 {
		sb.WriteString("- Superseded members (outcome ignored-by-Judge, record immutable): ")
		sb.WriteString(render(superseded))
		sb.WriteString("\n  Their outcomes are withheld from the member-outcome list above. Judge the ")
		sb.WriteString("criteria they carried on their replacement work instead.\n")
	}
	if len(postUnmetMemberIDs) > 0 {
		sb.WriteString("- Artifacts produced AFTER the unmet verdict (flagged post-hoc): ")
		sb.WriteString(render(postUnmetMemberIDs))
		sb.WriteString("\n")
	}
	return sb.String()
}
