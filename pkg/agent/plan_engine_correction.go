// plan_engine_correction.go: Replan a live plan — validate and apply adjudicator corrections (FR-143), superseded-member and generation tracking, boot reconstruction

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- ADR-053 Phase 2: owner loop + correction (§3/§3b/§3c, G-9..G-12) -----

// CorrectionVerb is the owner-correction verb (FR-143/G-11), re-exported as an
// alias of plan.RevisionVerb (FR-004) so the verb the engine switches on and
// the verb the plan_correct tool sends are one type, not two that convert.
type CorrectionVerb = plan.RevisionVerb

// --- Superseded-member tracking (FR-143 SUPERSEDE) -------------------------

// markMemberSuperseded records that memberID's outcome is ignored-by-Judge
// (SUPERSEDE verb). The member's task record stays immutable; only the
// Judge-weighting changes. Same lazy-init + mu pattern as
// recordUnmetTerminalSignature.
func (pe *PlanEngine) markMemberSuperseded(planID, memberID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.supersededMembers == nil {
		pe.supersededMembers = make(map[string]map[string]bool)
	}
	set := pe.supersededMembers[planID]
	if set == nil {
		set = make(map[string]bool)
		pe.supersededMembers[planID] = set
	}
	set[memberID] = true
}

// isMemberSuperseded reports whether memberID's outcome has been marked
// ignored-by-Judge via a SUPERSEDE correction.
//
// This is a single-member ASSERTION ACCESSOR over the same map
// supersededMemberSet returns; production reads go through that one, because
// the judge path needs the whole set at once. Asserting on this alone proves
// only that a map entry was written — which is exactly the gap that let
// SUPERSEDE ship inert. A test that uses it must ALSO assert the observable
// property (the superseded outcome is withheld from the next judge round's
// claim text); see TestSupersede_OutcomeWithheldFromJudgeClaimText.
func (pe *PlanEngine) isMemberSuperseded(planID, memberID string) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.supersededMembers[planID][memberID]
}

// supersededMemberSet returns a copy of the superseded-member set for planID
// (nil if none). Used by evidence-building to exclude superseded done members
// from the Judge's view.
func (pe *PlanEngine) supersededMemberSet(planID string) map[string]bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	src := pe.supersededMembers[planID]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// --- Plan-generation tracking (D13/G-12) -----------------------------------

// planGeneration returns the current generation number for planID (0 on first
// run, incremented by each Play). Same lazy-init pattern.
func (pe *PlanEngine) planGeneration(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.planGenerations == nil {
		pe.planGenerations = make(map[string]int)
	}
	return pe.planGenerations[planID]
}

// incrementPlanGeneration bumps planID's generation and returns the new value.
func (pe *PlanEngine) incrementPlanGeneration(planID string) int {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.planGenerations == nil {
		pe.planGenerations = make(map[string]int)
	}
	pe.planGenerations[planID]++
	return pe.planGenerations[planID]
}

// newRevisionID mints a deterministic-enough revision identifier.
func (pe *PlanEngine) newRevisionID(planID string) string {
	return fmt.Sprintf("rev-%s-%d-%d", planID, pe.clock.Now().UnixNano(), pe.planGeneration(planID))
}

const (
	CorrectionAppend        CorrectionVerb = plan.RevisionAppend
	CorrectionSupersede     CorrectionVerb = plan.RevisionSupersede
	CorrectionTargetedRetry CorrectionVerb = plan.RevisionTargetedRetry
	CorrectionAbandon       CorrectionVerb = plan.RevisionAbandon
)

// CorrectionCaller identifies the principal invoking AppendCorrection
// (sec-MAJOR-2). The engine gates every correction on AgentID alone, and only
// the PlanSupervisor System Agent is admitted (ADR-055 D3/FR-4/FR-6):
//
//   - AgentID must equal planSupervisorAgentID. Correction is the
//     adjudicator's verb; the plan's OWNER has no correction role whatsoever
//     (FR-4) and is denied by the same opaque message as any stranger, so
//     "it is my plan" is not a way in.
//   - SessionID is carried for the audit trail ONLY. It is deliberately NOT
//     gated: the retired session clause compared it against the plan's
//     OwnerSessionID, which locked even the owner out of its own plan from
//     its own chat session (ADR-055 decision 9). OwnerSessionID itself stays
//     on the plan record — ADR-055 D7 explicitly declines to delete it here.
//
// Callers (system tool or an internal supervision loop) MUST resolve the
// invoking principal's real agent identity — there is no "trusted internal
// caller" bypass.
//
// not-wire-format: engine-internal type; the REST/tool layer maps its
// authenticated principal to this.
//
// CorrectionCaller, CorrectionRequest and CorrectionResult are re-exported
// here as aliases of the pkg/plan declarations (FR-004) so callers importing
// pkg/agent still get a single-package API, while pkg/tools — which cannot
// import pkg/agent — names the identical types. IntentEdge below is the
// in-repo precedent this follows.
type CorrectionCaller = plan.CorrectionCaller

// CorrectionRequest is an owner correction to an unmet DoD (FR-143/G-11). The
// owner issues one verb; each records a revision entry committed
// transactionally via the intent-log (INV-6/N-8).
//
// not-wire-format: engine-internal type; the REST/tool layer maps its wire
// type to this.
type CorrectionRequest = plan.CorrectionRequest

// CorrectionResult is the outcome of processing a correction.
//
// not-wire-format: engine-internal type.
type CorrectionResult = plan.CorrectionResult

// --- AppendCorrection (FR-143/G-11, the main correction handler) -----------

// AppendCorrection processes an owner correction to an unmet DoD (FR-143/G-11).
// The plan MUST be in awaiting_supervision. The caller MUST be the plan's
// owner (sec-MAJOR-2): the owner-authority gate runs BEFORE any state
// inspection or mutation, so a non-owner learns nothing about the plan and
// cannot change it. The correction commits transactionally via the intent-log
// (INV-6/N-8): AppendIntent → MarkCommitted → Apply → MarkDone. After the
// commit:
//   - For append/supersede: auto-reset ALL live-round failed members (excludes
//     frozen/done members — G-10).
//   - For targeted_retry: reset ONLY the specified failed member (no full
//     Stop/Play — D4).
//   - Tails depend only on done outcomes; an unreachable DoD takes the
//     honest-exit path (G-10).
//   - The durable unmet signature is cleared (INV-7: correction = new activity).
//   - The DoD stays immutable (G-11).
func (pe *PlanEngine) AppendCorrection(ctx context.Context, planID string, caller CorrectionCaller, req CorrectionRequest) (*CorrectionResult, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: get plan %q: %w", planID, err)
	}
	// Adjudicator-authority gate (ADR-055 D3, sec-MAJOR-2): only the
	// PlanSupervisor may correct a plan — the owner included in "only". Runs
	// before any state/phase inspection so an unauthorised caller cannot probe
	// plan state via error differentiation, and before any mutation.
	if err := pe.requireCorrectionAuthority(caller, p, planID); err != nil {
		return nil, err
	}
	if p.State != plan.StateRunning {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: plan %q is %s, not running", planID, p.State)
	}
	// FR-029: the gate is membership in the SUPERVISION-ELIGIBLE PHASE SET
	// {awaiting_supervision, stalled}, not equality with the parked phase. A
	// stall wake asks the adjudicator for a diagnosis and may well provoke a
	// correction; gating on the parked phase alone rejected 100% of those,
	// leaving a stalled plan with a wake, a rubric and no execution path — its
	// only exits Stop and idle expiry.
	//
	// It MUST NOT become "any phase": a plan at dispatching or judging is
	// still rejected.
	if !plan.IsSupervisionEligiblePhase(p.EffectivePlanPhase()) {
		return nil, fmt.Errorf(
			"plan_engine: AppendCorrection: plan %q is in phase %q, not a supervision-eligible phase (%s or %s)",
			planID, p.EffectivePlanPhase(), plan.PhaseAwaitingSupervision, plan.PhaseStalled)
	}
	if err := pe.validateCorrection(planID, p, req); err != nil {
		return nil, err
	}

	gen := pe.planGeneration(planID)
	revID := pe.newRevisionID(planID)
	tailAddIDs := make([]string, 0, len(req.TailMembers))
	for i := range req.TailMembers {
		tailAddIDs = append(tailAddIDs, req.TailMembers[i].ID)
	}
	now := pe.clock.Now().UTC()
	rev := plan.RevisionEntry{
		RevisionID:          revID,
		PlanID:              planID,
		Generation:          gen,
		Verb:                req.Verb,
		FalsifiedAssumption: req.FalsifiedAssumption,
		TailAdds:            tailAddIDs,
		SupersededMemberID:  req.SupersededMemberID,
		RetriedMemberID:     req.RetriedMemberID,
		Reason:              req.Reason,
		CreatedAt:           now,
	}
	// abandon is the ONE verb that does not return the plan to dispatching: it
	// terminates it. Its record therefore carries no phase patch, and its
	// commit adds no members and wires no edges.
	recPatch := plan.IntentRecordPatch{
		ClearLastUnmetTerminalSignature: true,
		PlanPhase:                       plan.PhaseDispatching,
	}
	if req.Verb == CorrectionAbandon {
		recPatch = plan.IntentRecordPatch{}
	}
	rec := plan.IntentRecord{
		IntentID:  revID,
		PlanID:    planID,
		Members:   req.TailMembers,
		Edges:     req.TailEdges,
		Revision:  rev,
		Patch:     recPatch,
		CreatedAt: now,
	}
	apply := pe.buildCorrectionApplyFunc(planID, req)

	// Transactional commit via the intent-log (INV-6/N-8). When no intent log
	// is wired (tests/degraded), apply directly with no transactional guarantee.
	if pe.intentLog != nil {
		if err := pe.intentLog.CommitCorrection(rec, apply); err != nil {
			return nil, fmt.Errorf("plan_engine: AppendCorrection: commit: %w", err)
		}
	} else if err := apply(rec); err != nil {
		return nil, fmt.Errorf("plan_engine: AppendCorrection: apply (no intent log): %w", err)
	}

	// --- abandon: the adjudicated honest exit (ADR-055/FR-046b) -----------
	//
	// The adjudicator judges the Definition of Done unreachable from the
	// plan's current state and adds no corrective work at all. The revision is
	// now durably committed, so the falsified assumption is on the record;
	// terminate the plan rather than burn the remaining round budget on
	// corrections that cannot succeed.
	//
	// The reason is dod_unreachable, NOT judge_rounds_exhausted: rounds may
	// well remain when this fires, and "we ran out of rounds" and "more rounds
	// would not help" are different facts (see plan.FailedReasonDoDUnreachable).
	if req.Verb == CorrectionAbandon {
		pe.countCorrectionAndClearWake(planID, p, revID)
		pe.clearUnmetTerminalSignature(planID)
		pe.failPlanLocked(planID, plan.FailedReasonDoDUnreachable, buildAbandonHandover(p, req))
		return &CorrectionResult{
			RevisionID: revID, Generation: gen, RevisionEntry: rev,
			HonestExit: true,
		}, nil
	}

	pe.countCorrectionAndClearWake(planID, p, revID)

	// Record supersession in-memory (for Judge evidence building).
	if req.Verb == CorrectionSupersede {
		pe.markMemberSuperseded(planID, req.SupersededMemberID)
	}
	// Clear the in-memory durable unmet signature (correction = new activity,
	// INV-7). The persisted field was cleared by the apply func's plan patch.
	pe.clearUnmetTerminalSignature(planID)
	pe.touchActivity(planID)

	// Auto-reset + honest-exit check (G-10).
	tasks, lerr := pe.taskStore.List(task.Filter{PlanID: planID})
	if lerr != nil {
		// B1 (review BLOCKER): do NOT mask a store-read error as "plan cannot
		// progress" — planCannotProgress(nil) returns true vacuously, which would
		// failPlanLocked a HEALTHY plan with a misleading
		// judge_rounds_exhausted reason. The correction is durably committed
		// above; surface the read error and skip the post-correction honest-exit
		// assessment this cycle (the next processPlan tick re-evaluates against
		// the authoritative store).
		logger.ErrorCF("plan_engine", "AppendCorrection: post-correction task list failed; correction committed, skipping honest-exit assessment",
			map[string]any{"plan_id": planID, "error": lerr.Error()})
		return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
	}
	if req.Verb != CorrectionTargetedRetry {
		// append/supersede: auto-reset ALL live-round failed members
		// (excludes frozen/done members).
		pe.autoResetLiveRoundFailedMembers(planID, tasks)
		tasks, lerr = pe.taskStore.List(task.Filter{PlanID: planID})
		if lerr != nil {
			logger.ErrorCF("plan_engine", "AppendCorrection: post-auto-reset task list failed; correction committed, skipping honest-exit assessment",
				map[string]any{"plan_id": planID, "error": lerr.Error()})
			return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
		}
	}
	// Honest exit: if after the correction + auto-reset the plan still cannot
	// make progress, fail it honestly (no livelock, G-10).
	if planCannotProgress(tasks) {
		handover := buildUnreachableDoDHandover(p, tasks)
		// dod_unreachable, not judge_rounds_exhausted: the correction was
		// applied and rounds may well remain — "more rounds would not help" is
		// a different fact from "we ran out of rounds", and this is the
		// involuntary half of the pair plan.FailedReasonDoDUnreachable
		// documents (the adjudicated `abandon` above is the deliberate half).
		pe.failPlanLocked(planID, plan.FailedReasonDoDUnreachable, handover)
		return &CorrectionResult{
			RevisionID: revID, Generation: gen, RevisionEntry: rev,
			HonestExit: true,
		}, nil
	}
	// Re-dispatch ready members.
	pe.dispatchReadyMembers(ctx, planID, tasks)
	return &CorrectionResult{RevisionID: revID, Generation: gen, RevisionEntry: rev}, nil
}

// countCorrectionAndClearWake performs the post-commit supervision
// bookkeeping shared by every correction verb (FR-050 + FR-029(3)): the plan
// has just left the supervision-eligible phase set, so the wake receipt, wake
// error and attempt counter reset and the correction is counted — in ONE store
// write, so a concurrent REST Store.Update (whose callers do not hold
// planDecisionMu) cannot interleave between them.
//
// correction_rounds is INCREMENTED and never reset: it is an attribution
// counter for the life of the plan, and it is the only thing that tells "the
// round budget ran out with no correction ever applied" apart from
// "corrections consumed the budget".
//
// The stall note goes with it. A correction applied to a STALLED plan must
// clear that note, for the same reason a park does: the plan record is the
// adjudicator's primary input, and a stale stall diagnosis alongside a fresh
// wake is the input most likely to produce the wrong verb next time.
//
// p is the plan as read at the top of AppendCorrection; revID is used for the
// failure log only. Caller must hold planDecisionMu.
func (pe *PlanEngine) countCorrectionAndClearWake(planID string, p *plan.Plan, revID string) {
	rounds := 1
	if p.Supervision != nil {
		rounds = p.Supervision.CorrectionRounds + 1
	}
	postPatch := clearSupervisionWakePatch(plan.Patch{SupervisionCorrectionRounds: &rounds})
	if strings.HasPrefix(p.HandoverText, stallHandoverNotePrefix) {
		cleared := ""
		postPatch.HandoverText = &cleared
	}
	if _, err := pe.planStore.Update(planID, postPatch); err != nil {
		// The correction itself is durably committed by the caller; this write
		// only carries supervision bookkeeping. Surface it — a stuck wake
		// receipt would let the next tick's deadline fire against a plan that
		// has already moved.
		//
		// G3 fix wave, finding 6: that stuck receipt is the LESSER consequence,
		// and naming only it is what made this look benign. The DURABLE one is
		// that supervision.attempts stays at its pre-correction value: this is
		// the only write that resets it, so without it the plan's NEXT park
		// started its escalation ladder part-spent and reached the FR-022
		// ceiling early — a correction that succeeded shortening the budget of
		// a park that had not happened yet. That consequence is now neutralised
		// at the other end: wakeSupervisor takes the park boundary from its
		// caller instead of inferring it from this (possibly unwritten) state,
		// so a new park's first wake is attempt 1 whether or not this write
		// landed. The counter is still worth resetting here — it keeps the
		// persisted record truthful for the UI and for the terminal handover —
		// but nothing's correctness now depends on it.
		//
		// It is deliberately NOT promoted to a returned error: AppendCorrection's
		// error return means "the correction did not happen", and the correction
		// demonstrably DID happen (the caller committed it transactionally above).
		// Returning one here would make pkg/tools report a committed correction
		// as a failure, inviting the adjudicator to issue it a second time.
		logger.ErrorCF("plan_engine", "AppendCorrection: could not reset supervision state after applying the correction",
			map[string]any{"plan_id": planID, "revision_id": revID, "error": err.Error()})
	}
}

// buildAbandonHandover renders the handover for an adjudicated abandon
// (ADR-055/FR-046b). It states the falsified assumption, because that — not
// the member outcomes — is what makes the exit honest rather than a giving-up.
func buildAbandonHandover(p *plan.Plan, req CorrectionRequest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %q was judged unable to reach its Definition of Done and has been "+
		"abandoned by the adjudicator rather than corrected further.\n\n", p.Title)
	fmt.Fprintf(&sb, "Falsified assumption: %s\n", strings.TrimSpace(req.FalsifiedAssumption))
	if reason := strings.TrimSpace(req.Reason); reason != "" {
		fmt.Fprintf(&sb, "Reason: %s\n", reason)
	}
	sb.WriteString("\nNo corrective work was added: the adjudicator judged that no correction " +
		"could reach this Definition of Done from the plan's current state. Review the DoD itself.")
	return sb.String()
}

// validateCorrection is the ENGINE's own, complete precondition check for a
// correction — not a formality delegating to the tool that called it.
//
// It exists as a second, independent line of defence because AppendCorrection
// is an exported engine entrypoint: pkg/tools/plan_correct.go is one caller,
// but nothing structurally prevents another (a REST seam, an internal
// supervision loop, a future tool) from reaching it with a typed request that
// never passed through the tool's raw-argument parser. Every rule the tool
// enforces at its boundary is therefore re-enforced here on the TYPED request.
//
// The checks, in the order a bad request is cheapest to reject:
//
//  1. Payload caps — single-sourced from pkg/plan (MaxTailMembers,
//     MaxTailEdges, MaxMemberTitleBytes, MaxTextBytes), never re-declared
//     locally, so the tool and the engine cannot drift apart.
//  2. The verb/field compatibility matrix. A field that is merely MEANINGLESS
//     for a verb is rejected rather than silently ignored, because the engine
//     creates rec.Members/rec.Edges verb-independently — a targeted_retry
//     carrying 50 tail members would otherwise create all 50.
//  3. Verb-specific member references (exists, right status, belongs to THIS
//     plan).
//  4. FR-030/FR-030b, the supersede pairing rule — see requireSupersedePairing.
//  5. Tail-edge integrity: no dangling endpoints, no self-edges, no edge
//     touching the superseded member, and no cycle in the resulting DAG.
func (pe *PlanEngine) validateCorrection(planID string, p *plan.Plan, req CorrectionRequest) error {
	if err := validateCorrectionPayloadCaps(req); err != nil {
		return err
	}
	if err := validateCorrectionVerbFields(req); err != nil {
		return err
	}

	var supersededMember *task.Task
	switch req.Verb {
	case CorrectionSupersede:
		m, err := pe.validateMemberRef(planID, req.SupersededMemberID, "supersede", task.StatusDone, "done")
		if err != nil {
			return err
		}
		supersededMember = m
	case CorrectionTargetedRetry:
		if _, err := pe.validateMemberRef(planID, req.RetriedMemberID, "targeted_retry", task.StatusFailed, "failed"); err != nil {
			return err
		}
		// targeted_retry adds no work and wires no edges (enforced by the
		// field matrix above), so there is nothing further to validate.
		return nil
	case CorrectionAbandon:
		// The honest exit names no member and adds no work (enforced by the
		// field matrix above). Nothing to resolve against the store.
		return nil
	case CorrectionAppend:
		// Tail members are validated below, shared with supersede.
	default:
		return fmt.Errorf("plan_engine: unknown correction verb %q", req.Verb)
	}

	// FR-030b: replacement work must be held to the SAME standard as the work
	// it replaces. Checked before the edge graph because it is the rule the
	// whole verb's safety rests on.
	//
	// Backfill first (InheritSupersededCriteria), THEN check
	// (RequireCriteriaInheritance) — mirrors the tool's own buildCorrection.
	// This is the second of two call sites (the tool's own pre-flight check is
	// the first, for callers that go through it); a direct engine caller that
	// bypasses the tool entirely still gets the same backfill, so the identity
	// rule is enforceable REGARDLESS of caller. See
	// tools.InheritSupersededCriteria's doc for why the caller-must-reproduce-
	// it-exactly design this still checks was unsatisfiable for the one real
	// caller (PlanSupervisor), whose supervision wake never shows it a
	// member's criteria detail.
	if req.Verb == CorrectionSupersede {
		tools.InheritSupersededCriteria(supersededMember.Criteria, req.TailMembers)
		if err := tools.RequireCriteriaInheritance(supersededMember.Criteria, req.TailMembers); err != nil {
			return fmt.Errorf("plan_engine: %w", err)
		}
	}

	members, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return fmt.Errorf("plan_engine: could not list members of plan %q: %w", planID, err)
	}
	if err := pe.validateCorrectionTailMembers(members, req); err != nil {
		return err
	}
	if err := validateCorrectionTailEdges(members, req); err != nil {
		return err
	}
	// UAT defect A (second half): plan-lint the member set this correction
	// WOULD produce. Until now Lint ran at approve and nowhere else, so every
	// member a supervision correction added — and every edge it wired — was
	// exempt from the write-set-overlap and join-point invariants by
	// construction. Observed live: a supervisor-added member converging four
	// predecessors (two mutually parallel) with is_join=false committed
	// cleanly, which is precisely what approve would have refused.
	//
	// Placed last in validateCorrection, after the cheap structural checks
	// and after RequireAcyclic — the projection is only meaningful once the
	// edges are known well-formed and acyclic, and a caller gets the most
	// specific diagnosis first. Rejecting here leaves the plan exactly as the
	// wake found it: validateCorrection runs BEFORE any intent is appended,
	// so nothing is half-applied and the adjudicator can re-author the
	// correction against the named violation.
	if lerr := plan.LintCorrection(p, members, req); lerr != nil {
		return fmt.Errorf("plan_engine: correction rejected by plan-lint: %w", lerr)
	}
	return nil
}

// validateCorrectionTailMembers checks that every tail member is actually
// CREATABLE. It runs only for append and supersede — the two verbs that accept
// tail members at all (targeted_retry and abandon return before this point,
// having already been rejected for carrying any).
//
// ADR-055 fix wave, finding 4: without this, a non-tool caller (the exact
// caller class validateCorrection's own doc comment says it exists for) could
// pass a tail member with an empty ID and buildCorrectionApplyFunc would
// silently `continue` past it. For SUPERSEDE that is the precise outcome the
// verb is guarded against: the pairing rule (requireSupersedePairing) and the
// criteria-inheritance rule (RequireCriteriaInheritance) both pass, because
// both inspect the REQUEST — the commit then creates nothing, the call reports
// success, and the plan is left with a discounted done outcome and NO
// replacement work. That is the bare discount both rules exist to make
// impossible, reached through the one door neither rule watches. The shipped
// tool mints UUIDs, so this is unreachable THROUGH THE TOOL; it is reachable
// through the seam the engine's second line of defence is for.
//
// The ID rules are checked against real state, not just syntax: an id that
// collides with an EXISTING task is equally fatal, because the apply func
// treats "this task already exists" as a successful idempotent replay and
// skips creation — so a colliding id produces the same silent no-creation as
// an empty one, plus a false claim on somebody else's task.
//
// The title check is not cosmetic either: task.Store.Create rejects an empty
// title, and the apply func runs INSIDE the intent-log commit, so an untitled
// tail member aborts mid-commit with the intent record already committed and
// flagged for boot replay. Rejecting it here turns a half-applied correction
// into a clean refusal that leaves the plan exactly as the wake found it.
//
// The >= 1 acceptance criterion rule is enforced for BOTH verbs, matching the
// plan_correct schema (which marks `criteria` required on every tail member
// and says "REQUIRED, at least one"). Mirroring it here is the whole point of
// this second line of defence: a rule the tool enforces but the engine does
// not is not a rule, it is a bypass with extra steps.
//
// It also closes a gap RequireCriteriaInheritance leaves open. That check
// returns nil early when the SUPERSEDED member itself has no criteria, so
// superseding a criteria-less done member with a criteria-less replacement
// satisfied both integrity rules and produced replacement work the Judge
// cannot adjudicate at all — a bare discount wearing the shape of a paired
// one. Requiring criteria on the replacement makes the pairing meaningful
// whether or not the member being replaced had any.
func (pe *PlanEngine) validateCorrectionTailMembers(members []task.Task, req CorrectionRequest) error {
	if len(req.TailMembers) == 0 {
		return nil
	}
	existing := make(map[string]bool, len(members))
	for i := range members {
		existing[members[i].ID] = true
	}
	seen := make(map[string]bool, len(req.TailMembers))
	for i := range req.TailMembers {
		m := &req.TailMembers[i]
		if m.ID == "" {
			return fmt.Errorf(
				"plan_engine: tail_members[%d] has no id — a tail member with no id is silently skipped at "+
					"commit time, which would apply the correction while creating none of its work", i)
		}
		if seen[m.ID] {
			return fmt.Errorf("plan_engine: tail_members[%d]: id %q appears more than once in this correction", i, m.ID)
		}
		seen[m.ID] = true
		if existing[m.ID] {
			return fmt.Errorf(
				"plan_engine: tail_members[%d]: id %q is already a member of this plan — commit treats an "+
					"existing id as an idempotent replay and would create nothing", i, m.ID)
		}
		// Not just this plan's members: the task store is global, and the
		// apply func's existence check is a bare Get.
		if _, gerr := pe.taskStore.Get(m.ID); gerr == nil {
			return fmt.Errorf(
				"plan_engine: tail_members[%d]: id %q already names an existing task — commit treats an "+
					"existing id as an idempotent replay and would create nothing", i, m.ID)
		} else if !errors.Is(gerr, task.ErrNotFound) {
			return fmt.Errorf("plan_engine: tail_members[%d]: could not check id %q against the task store: %w", i, m.ID, gerr)
		}
		if strings.TrimSpace(m.Title) == "" {
			return fmt.Errorf("plan_engine: tail_members[%d] (%q) has an empty title", i, m.ID)
		}
		if len(m.Criteria) == 0 {
			return fmt.Errorf(
				"plan_engine: tail_members[%d] (%q) carries no acceptance criteria — work the Judge cannot "+
					"adjudicate is not replacement work", i, m.ID)
		}
	}
	return nil
}

// validateCorrectionPayloadCaps bounds every unbounded field on the request.
// The caps live in pkg/plan so this and the plan_correct tool enforce the same
// numbers by construction (FR-004).
func validateCorrectionPayloadCaps(req CorrectionRequest) error {
	if len(req.TailMembers) > plan.MaxTailMembers {
		return fmt.Errorf("plan_engine: tail_members has %d entries; the maximum is %d",
			len(req.TailMembers), plan.MaxTailMembers)
	}
	if len(req.TailEdges) > plan.MaxTailEdges {
		return fmt.Errorf("plan_engine: tail_edges has %d entries; the maximum is %d",
			len(req.TailEdges), plan.MaxTailEdges)
	}
	if err := checkCorrectionTextBytes("falsified_assumption", req.FalsifiedAssumption); err != nil {
		return err
	}
	if err := checkCorrectionTextBytes("reason", req.Reason); err != nil {
		return err
	}
	for i := range req.TailMembers {
		m := &req.TailMembers[i]
		if len(m.Title) > plan.MaxMemberTitleBytes {
			return fmt.Errorf("plan_engine: tail_members[%d].title is %d bytes; the maximum is %d",
				i, len(m.Title), plan.MaxMemberTitleBytes)
		}
		if err := checkCorrectionTextBytes(fmt.Sprintf("tail_members[%d].description", i), m.Description); err != nil {
			return err
		}
	}
	return nil
}

// checkCorrectionTextBytes bounds one free-text correction field on BYTES
// (not runes), matching plan.MaxTextBytes' stated contract.
func checkCorrectionTextBytes(field, value string) error {
	if len(value) > plan.MaxTextBytes {
		return fmt.Errorf("plan_engine: %s is %d bytes; the maximum is %d",
			field, len(value), plan.MaxTextBytes)
	}
	return nil
}

// validateCorrectionVerbFields is the verb/field compatibility matrix: it
// states which fields each verb ACCEPTS, so a field that is meaningless for
// the verb is rejected instead of silently applied.
func validateCorrectionVerbFields(req CorrectionRequest) error {
	reject := func(verb string, offenders map[string]bool) error {
		named := make([]string, 0, len(offenders))
		for _, field := range []string{"superseded_member_id", "retried_member_id", "tail_members", "tail_edges"} {
			if offenders[field] {
				named = append(named, field)
			}
		}
		if len(named) == 0 {
			return nil
		}
		return fmt.Errorf("plan_engine: %s does not accept %s", verb, strings.Join(named, ", "))
	}

	switch req.Verb {
	case CorrectionAppend:
		if err := reject("append", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"retried_member_id":    req.RetriedMemberID != "",
		}); err != nil {
			return err
		}
		if len(req.TailMembers) == 0 {
			return fmt.Errorf("plan_engine: append requires at least one tail member — an append that adds no work is not a correction")
		}
	case CorrectionSupersede:
		if err := reject("supersede", map[string]bool{
			"retried_member_id": req.RetriedMemberID != "",
		}); err != nil {
			return err
		}
		if req.SupersededMemberID == "" {
			return fmt.Errorf("plan_engine: supersede requires superseded_member_id")
		}
		if err := requireSupersedePairing(req); err != nil {
			return err
		}
	case CorrectionTargetedRetry:
		if err := reject("targeted_retry", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"tail_members":         len(req.TailMembers) > 0,
			"tail_edges":           len(req.TailEdges) > 0,
		}); err != nil {
			return err
		}
		if req.RetriedMemberID == "" {
			return fmt.Errorf("plan_engine: targeted_retry requires retried_member_id")
		}
	case CorrectionAbandon:
		if err := reject("abandon", map[string]bool{
			"superseded_member_id": req.SupersededMemberID != "",
			"retried_member_id":    req.RetriedMemberID != "",
			"tail_members":         len(req.TailMembers) > 0,
			"tail_edges":           len(req.TailEdges) > 0,
		}); err != nil {
			return err
		}
		if strings.TrimSpace(req.FalsifiedAssumption) == "" {
			return fmt.Errorf(
				"plan_engine: abandon requires falsified_assumption — terminating a plan as unreachable " +
					"is only honest when the assumption that turned out to be wrong is on the record")
		}
	default:
		return fmt.Errorf("plan_engine: unknown correction verb %q", req.Verb)
	}
	return nil
}

// requireSupersedePairing enforces FR-030: a supersede MUST carry replacement
// work.
//
// supersede marks a done member's outcome ignored by the judge. Unpaired, it
// is a way to satisfy an unmet Definition of Done by DISCOUNTING the evidence
// that failed it instead of fixing the work — which is precisely the move the
// verb must not enable. Pairing composes atomically: the engine creates tail
// members verb-independently, so the discounting and the replacement land in
// the same transactional intent-log commit or neither does.
//
// The complementary half (FR-030b, tools.RequireCriteriaInheritance, backed by
// tools.InheritSupersededCriteria) is what stops the pairing being satisfied
// by one throwaway member: the replacement must carry EVERY acceptance
// criterion of the member it replaces — auto-backfilled onto it now, rather
// than left to the caller to reproduce (see InheritSupersededCriteria's doc).
func requireSupersedePairing(req CorrectionRequest) error {
	if len(req.TailMembers) > 0 {
		return nil
	}
	return fmt.Errorf(
		"plan_engine: supersede requires at least one tail member: discounting a member's outcome is only " +
			"a correction when it is paired with replacement work that addresses the same criteria")
}

// validateCorrectionTailEdges checks the edge list against the plan's real
// member set: both endpoints must resolve to an existing member of THIS plan
// or to a tail member created in this same request, self-edges are rejected,
// no endpoint may name the member being superseded (new work behind a
// discounted member cannot make progress), and the resulting graph must be
// acyclic.
//
// The acyclicity check matters most here: the engine wires edges INSIDE its
// transactional intent-log commit, so a cycle discovered there aborts
// mid-commit, and an unwired cycle is unresolvable by the dispatcher — which,
// combined with a once-per-park supervision wake, strands the plan permanently.
func validateCorrectionTailEdges(members []task.Task, req CorrectionRequest) error {
	if len(req.TailEdges) == 0 {
		return nil
	}
	known := make(map[string]bool, len(members)+len(req.TailMembers))
	for i := range members {
		known[members[i].ID] = true
	}
	for i := range req.TailMembers {
		if id := req.TailMembers[i].ID; id != "" {
			known[id] = true
		}
	}
	for i := range req.TailEdges {
		e := &req.TailEdges[i]
		for _, ep := range []struct{ field, id string }{{"from", e.FromTaskID}, {"to", e.ToTaskID}} {
			if ep.id == "" {
				return fmt.Errorf("plan_engine: tail_edges[%d]: %s is required", i, ep.field)
			}
			if !known[ep.id] {
				return fmt.Errorf(
					"plan_engine: tail_edges[%d]: %s %q is neither an existing member of this plan nor a tail member in this correction",
					i, ep.field, ep.id)
			}
		}
		if e.FromTaskID == e.ToTaskID {
			return fmt.Errorf("plan_engine: tail_edges[%d]: from and to name the same member (self-edge)", i)
		}
		if req.SupersededMemberID != "" &&
			(e.FromTaskID == req.SupersededMemberID || e.ToTaskID == req.SupersededMemberID) {
			return fmt.Errorf(
				"plan_engine: tail_edges[%d]: names member %q, whose outcome this correction is superseding — new work must not depend on it",
				i, req.SupersededMemberID)
		}
	}
	if err := tools.RequireAcyclic(members, req.TailMembers, req.TailEdges); err != nil {
		return fmt.Errorf("plan_engine: %w", err)
	}
	return nil
}

// validateMemberRef is the shared preflight for member-targeted corrections:
// resolve the member, require the expected status (verb-dependent), and
// confirm the task actually belongs to planID. Used by both CorrectionSupersede
// (wantStatus=done, statusMsg="done") and CorrectionTargetedRetry
// (wantStatus=failed, statusMsg="failed") so the only call-site difference is
// the verb label and the status check.
// Returns the resolved member so a caller that needs its body (supersede, for
// the FR-030b criteria-inheritance check) does not re-read it.
func (pe *PlanEngine) validateMemberRef(planID, memberID, verb string, wantStatus task.Status, statusMsg string) (*task.Task, error) {
	t, err := pe.taskStore.Get(memberID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: %s member %q: %w", verb, memberID, err)
	}
	if t.Status != wantStatus {
		return nil, fmt.Errorf("plan_engine: member %q is %s, not %s (only %s members can be %s)",
			memberID, t.Status, statusMsg, statusMsg, verb)
	}
	if t.PlanID != planID {
		return nil, fmt.Errorf("plan_engine: member %q belongs to plan %q, not %q",
			memberID, t.PlanID, planID)
	}
	return t, nil
}

// ErrCorrectionNotAdjudicator is returned by AppendCorrection when the
// invoking principal is not the PlanSupervisor (ADR-055 D3/FR-6, sec-MAJOR-2).
//
// It replaces the retired ErrCorrectionNotOwner, whose rule was the exact
// inverse: the engine used to admit ONLY the plan's OwnerAgentID, while the
// plan_correct tool one layer above admits ONLY `plansupervisor`. Because a
// System Agent can never be a plan's owner (validatePlanOwnerAgentForTool
// rejects any owner that is not a chat target), the two admissible sets were
// disjoint and EVERY correction was denied — ADR-055's headline feature was
// inert. See requireCorrectionAuthority.
var ErrCorrectionNotAdjudicator = errors.New("plan_engine: correction caller is not the plan adjudicator")

// requireCorrectionAuthority is the adjudicator-authority gate for
// AppendCorrection (ADR-055 D3/FR-6, sec-MAJOR-2). It runs before any
// state/phase inspection so an unauthorised caller cannot probe plan state via
// error differentiation, and before any mutation.
//
// THE RULE, in one line: correction is PlanSupervisor's alone — matched on
// exact system-agent identity — and everyone else, the plan's own OWNER
// included, is denied.
//
// Matching on identity rather than on "is a System Agent" is deliberate
// (ADR-055 D3): a future System Agent must not silently inherit correction
// rights by virtue of its type.
//
// The denial stays OPAQUE (sec-MAJOR-2): the error names the plan the caller
// already named and nothing else. It does not echo the plan's OwnerAgentID,
// does not say who IS authorised, and does not vary with plan state — those
// details go to the server-side log only.
//
// Returns ErrCorrectionNotAdjudicator (wrapped) on every denial; callers
// propagate the wrapped sentinel so a calling seam can map it to HTTP 403.
func (pe *PlanEngine) requireCorrectionAuthority(caller CorrectionCaller, p *plan.Plan, planID string) error {
	if caller.AgentID == planSupervisorAgentID {
		return nil
	}
	// An empty AgentID lands here too — a caller with no identity is not the
	// adjudicator, and gets the same message as one with the wrong identity.
	logger.WarnCF("plan_engine", "AppendCorrection denied: caller is not the plan adjudicator",
		map[string]any{
			"plan_id":      planID,
			"caller_agent": caller.AgentID,
			"owner_agent":  p.OwnerAgentID,
			"adjudicator":  planSupervisorAgentID,
		})
	return fmt.Errorf("%w: plan %q", ErrCorrectionNotAdjudicator, planID)
}

// buildCorrectionApplyFunc returns the idempotent ApplyFunc for a correction.
// It performs the per-file writes: create tail-member tasks, wire edges, reset
// the targeted-retry member, apply auto-reset for targeted_retry, and patch
// the plan record (clear unmet signature, set phase to dispatching). Every
// operation is idempotent — boot's replay-forward may call it on a plan whose
// writes partially landed.
func (pe *PlanEngine) buildCorrectionApplyFunc(planID string, req CorrectionRequest) plan.ApplyFunc {
	return func(rec plan.IntentRecord) error {
		// Create tail-member tasks (idempotent: skip if the task already exists).
		for i := range rec.Members {
			m := &rec.Members[i]
			if m.ID == "" {
				continue
			}
			if existing, err := pe.taskStore.Get(m.ID); err == nil && existing != nil {
				continue // already created (idempotent replay)
			}
			if m.PlanID == "" {
				m.PlanID = planID
			}
			if err := pe.taskStore.Create(m); err != nil {
				return fmt.Errorf("create tail member %q: %w", m.ID, err)
			}
		}
		// Wire tail edges (AddDependency is idempotent: returns added=false,
		// nil when the edge already exists — safe for replay).
		for _, e := range rec.Edges {
			if _, _, err := pe.taskStore.AddDependency(e.ToTaskID, e.FromTaskID); err != nil {
				return fmt.Errorf("wire edge %s->%s: %w", e.FromTaskID, e.ToTaskID, err)
			}
		}
		// Targeted-retry: reset the specific failed member (idempotent:
		// RestartReset errors on non-failed, which is the correct no-op).
		if req.Verb == CorrectionTargetedRetry && req.RetriedMemberID != "" {
			if _, err := pe.taskStore.RestartReset(req.RetriedMemberID); err != nil {
				// Already reset (idempotent replay) — not fatal.
				logger.DebugCF("plan_engine", "targeted-retry reset (may be idempotent no-op)",
					map[string]any{"task_id": req.RetriedMemberID, "error": err.Error()})
			}
		}
		// abandon terminates the plan rather than returning it to work, so it
		// must NOT be patched back to dispatching here — AppendCorrection
		// fails it to dod_unreachable immediately after this commit.
		if req.Verb == CorrectionAbandon {
			return nil
		}
		// Patch the plan record (clear unmet signature, set phase).
		dispatching := plan.PhaseDispatching
		clearSig := ""
		if _, err := pe.planStore.Update(planID, plan.Patch{
			PlanPhase:                  &dispatching,
			LastUnmetTerminalSignature: &clearSig,
		}); err != nil {
			return fmt.Errorf("patch plan phase: %w", err)
		}
		return nil
	}
}

// --- Auto-reset + honest exit (G-10) ---------------------------------------

// autoResetLiveRoundFailedMembers resets every failed member back to `next`
// (via RestartReset) EXCEPT done members, which are frozen and preserved. This
// gives the failed members another chance after the adjudicator's correction
// landed. Caller must hold planDecisionMu.
//
// There is deliberately NO superseded-member exclusion here, and this is not
// an omission (ADR-055 fix wave, finding 3): supersede is only legal against a
// member that is `done` (validateMemberRef(..., task.StatusDone, ...)), and a
// superseded member's record is immutable, so a superseded member is never
// `failed` and the loop's status filter already excludes it on every path. The
// exclusion this function used to carry was therefore provably unreachable —
// and, being the SOLE consumer of the superseded set at the time, it was also
// the whole reason supersede looked wired when it was inert. Supersession's
// real effect is on the Judge's evidence (buildPlanClaimText /
// gamingGuardEvidence), not on member re-dispatch.
func (pe *PlanEngine) autoResetLiveRoundFailedMembers(planID string, tasks []task.Task) {
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusFailed {
			continue // only reset failed members; done/next/blocked/in_progress untouched
		}
		if _, err := pe.taskStore.RestartReset(t.ID); err != nil {
			logger.WarnCF("plan_engine", "auto-reset: could not reset failed member",
				map[string]any{"plan_id": planID, "task_id": t.ID, "error": err.Error()})
		}
	}
}

// planCannotProgress reports whether the plan can make NO further progress
// after a correction + auto-reset (the honest-exit condition, G-10). True when:
//   - No member is `next` or `in_progress` (nothing to dispatch/run).
//   - Every remaining non-done member is `failed` (auto-reset already tried
//     and they re-failed, or were not reset) or `blocked` behind a dependency
//     that is itself a dead-end (failed, not done).
//
// This mirrors planStuckAfterMemberCancel's dead-end analysis but is
// correction-scoped: it evaluates the post-correction, post-auto-reset state.
func planCannotProgress(tasks []task.Task) bool {
	hasDispatchable := false
	for i := range tasks {
		s := tasks[i].Status
		if s == task.StatusNext || s == task.StatusInProgress {
			hasDispatchable = true
			break
		}
	}
	if hasDispatchable {
		return false
	}
	// No dispatchable members — check if every non-done member is a dead-end.
	byID := make(map[string]*task.Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	for i := range tasks {
		t := &tasks[i]
		if t.Status == task.StatusDone {
			continue
		}
		if !memberIsDeadEnd(t, byID, make(map[string]bool)) {
			return false // at least one non-done member could still progress
		}
	}
	return true
}

// buildUnreachableDoDHandover renders the honest-exit handover for a plan
// whose DoD is structurally unreachable after correction + auto-reset.
func buildUnreachableDoDHandover(p *plan.Plan, tasks []task.Task) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %q cannot reach its Definition of Done: after the latest correction "+
		"and auto-reset, no member can make further progress (every non-done member is "+
		"failed or blocked behind a failed member).\n\nMember outcomes:\n", p.Title)
	for i := range tasks {
		t := &tasks[i]
		fmt.Fprintf(&sb, "- %s (%s)\n", t.Title, t.Status)
	}
	sb.WriteString("\nThe plan has been marked failed. Review the DoD and member outcomes.")
	return sb.String()
}

// --- Boot reconstruction of corrections ------------------------------------

// reconstructCorrections rebuilds the in-memory superseded-member sets and
// plan-generation counters from the intent log's persisted revision entries.
// Called from bootReconcile after the intent-log replay but before
// processPlan, so the engine's correction state is consistent with the
// durable record. No-ops cleanly when no intent log is wired.
func (pe *PlanEngine) reconstructCorrections() {
	if pe.intentLog == nil {
		return
	}
	entries, err := os.ReadDir(pe.intentLog.Dir())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF("plan_engine", "reconstructCorrections: read intent dir failed",
				map[string]any{"error": err.Error()})
		}
		return
	}
	superCount := 0
	genCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		planID := strings.TrimSuffix(e.Name(), ".jsonl")
		records, err := pe.intentLog.List(planID)
		if err != nil {
			continue
		}
		maxGen := 0
		for _, rec := range records {
			if rec.Revision.Verb == plan.RevisionSupersede && rec.Revision.SupersededMemberID != "" {
				pe.markMemberSuperseded(planID, rec.Revision.SupersededMemberID)
				superCount++
			}
			if rec.Revision.Generation > maxGen {
				maxGen = rec.Revision.Generation
			}
		}
		if maxGen > 0 {
			pe.mu.Lock()
			if pe.planGenerations == nil {
				pe.planGenerations = make(map[string]int)
			}
			pe.planGenerations[planID] = maxGen
			pe.mu.Unlock()
			genCount++
		}
	}
	if superCount > 0 || genCount > 0 {
		logger.InfoCF("plan_engine", "correction state reconstructed",
			map[string]any{"superseded_members": superCount, "plans_with_generations": genCount})
	}
}
