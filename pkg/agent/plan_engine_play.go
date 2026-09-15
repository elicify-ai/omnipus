// plan_engine_play.go: Play a plan — admit it, pick the next runnable member and dispatch it (dispatch pass, promotions, stall diagnosis, Play/resume)

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- approved -> running auto-tick (R1/O2) --------------------------------

// tryStartApprovedPlan admits planID (via Admit, R5) and, if the global cap
// has room, transitions it approved→running and dispatches its initial
// ready wave. If the cap is full the plan is left `approved` (a legitimate
// cap-waiting state, O2 r3) and retried on the next tick.
func (pe *PlanEngine) tryStartApprovedPlan(ctx context.Context, planID string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		if !errors.Is(err, plan.ErrNotFound) {
			logger.WarnCF("plan_engine", "tryStartApprovedPlan: get plan failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
		}
		return
	}
	if p.State != plan.StateApproved {
		return // vanished or already moved on by another path
	}

	ok, active, loopCap := pe.Admit("plan")
	if !ok {
		logger.InfoCF("plan_engine", "approved plan is cap-waiting",
			map[string]any{"plan_id": planID, "active": active, "cap": loopCap})
		return
	}

	running := plan.StateRunning
	updated, err := pe.planStore.Update(planID, plan.Patch{State: &running})
	if err != nil {
		logger.WarnCF("plan_engine", "approved->running transition failed",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}
	// F2 round-burn gate (acceptance G-9): a fresh admission, and equally an
	// owner's restart/Play resume of a previously-failed plan (ADR-052 §6.7,
	// same planID), must always get a genuinely fresh judge round rather than
	// being silently gated by a signature recorded during a prior life of
	// this plan ID — clear it here, the sole approved->running transition
	// point. Clears BOTH the in-memory map and the persisted
	// LastUnmetTerminalSignature field (C1 — the durable gate must not
	// outlive the dispatch cycle it was meant to gate), so a restart/Play
	// resume after a C1 park gets a genuinely fresh round even if the member
	// outcomes end up identical again.
	pe.clearUnmetTerminalSignature(planID)
	// Same reasoning for the judge-unavailability streak (UAT defect B): a
	// fresh admission or a restart/Play resume must not inherit a streak
	// accumulated during a prior life of this plan ID, or it would park at
	// stalled on its very first abandoned round.
	pe.clearJudgeUnavailableStreak(planID)
	clearSig := ""
	if _, cerr := pe.planStore.Update(planID, plan.Patch{LastUnmetTerminalSignature: &clearSig}); cerr != nil {
		logger.WarnCF("plan_engine", "could not clear durable unmet signature on start",
			map[string]any{"plan_id": planID, "error": cerr.Error()})
	}
	logger.InfoCF("plan_engine", "plan admitted: approved->running", map[string]any{"plan_id": planID})

	tasks, lerr := pe.taskStore.List(task.Filter{PlanID: updated.ID})
	if lerr != nil {
		logger.WarnCF("plan_engine", "could not list member tasks after plan start",
			map[string]any{"plan_id": planID, "error": lerr.Error()})
		return
	}
	// Round-1 UAT finding #5 fix: a member attached to the plan before/at
	// Execute lands in `inbox` (task.normalize()'s default) — promote it to
	// `next` immediately on admission so the very first dispatch wave below
	// actually reaches it, rather than waiting for the next tick's
	// processPlan pass to notice it (see promoteInboxMembers' doc comment).
	if pe.promoteInboxMembers(updated, tasks) {
		tasks, lerr = pe.taskStore.List(task.Filter{PlanID: updated.ID})
		if lerr != nil {
			logger.WarnCF("plan_engine", "could not re-list member tasks after inbox promotion on start",
				map[string]any{"plan_id": planID, "error": lerr.Error()})
			return
		}
	}
	pe.dispatchReadyMembers(ctx, updated.ID, tasks)
	pe.touchActivity(updated.ID)
}

// --- Per-plan dispatch + judge-trigger pass -------------------------------

// processPlan is the core per-plan evaluation+action pass for one RUNNING
// plan, used by both Tick (for every running plan, each pass) and the
// reactive task_status_changed handler (for the specific plan a changed
// task belongs to). It is idempotent and safe to call redundantly.
func (pe *PlanEngine) processPlan(ctx context.Context, planID string) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		if !errors.Is(err, plan.ErrNotFound) {
			logger.WarnCF("plan_engine", "processPlan: get plan failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
		}
		return
	}
	if p.State != plan.StateRunning {
		return
	}
	if p.PausedReason != "" {
		return // FR-065: a paused plan neither dispatches nor judges
	}

	switch p.EffectivePlanPhase() {
	case plan.PhaseJudging:
		_, inFlight := pe.registry().Lookup(verifierUnitForPlan(p.ID))
		if inFlight {
			return // a goroutine is already adjudicating this round
		}
		// UAT defect B, H1: a plan whose unavailability streak is AT the bound
		// must never start another judge round — and finding one here, at
		// `judging` with no goroutine watching it, means the park that was
		// supposed to stop exactly that did not take effect (its store write
		// failed, or the phase it wrote was reverted). The resume below would
		// start round N+1, the round would be abandoned too, the streak would
		// climb past a bound that can no longer bite, and the original
		// unbounded oscillation would resume verbatim — which is precisely
		// what the first version of this fix left in place, because its
		// hold-back gate also required the phase the failed write never set.
		//
		// Re-attempt the PARK instead of the ROUND, bounded and terminal-ward:
		// see reparkJudgeUnavailablePlanLocked.
		if pe.judgeUnavailableParked(p.ID) {
			pe.reparkJudgeUnavailablePlanLocked(p)
			return
		}
		// plan_phase=judging with no in-flight goroutine in THIS process can
		// only mean a prior process died mid-round (FR-062 boot case) — no
		// round was actually consumed (JudgeCriteria's own "0 rounds on
		// Unavailable/crash" contract), so resuming from scratch is safe.
		logger.InfoCF("plan_engine", "resuming plan judge round interrupted by a prior crash/restart",
			map[string]any{"plan_id": p.ID})
		resumeTasks, terr := pe.taskStore.List(task.Filter{PlanID: p.ID})
		if terr != nil {
			logger.WarnCF("plan_engine", "processPlan: list member tasks for judge-round resume failed",
				map[string]any{"plan_id": p.ID, "error": terr.Error()})
			resumeTasks = nil
		}
		pe.beginPlanJudgeRound(p, resumeTasks)
		return
	case plan.PhaseSynthesizing:
		return // terminal hand-off already in progress
	}

	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		logger.WarnCF("plan_engine", "processPlan: list member tasks failed",
			map[string]any{"plan_id": planID, "error": err.Error()})
		return
	}

	// Round-1 UAT finding #5 fix: promote every inbox member of THIS
	// dispatchable plan to `next` before the ready/blocked-cascade pass and
	// dispatch below run — see promoteInboxMembers' own doc comment. This is
	// the per-tick sweep (not a one-shot at Execute/approval): a member
	// attached to an already-running plan gets promoted here on the very
	// next pass, exactly like one attached before Execute.
	if pe.promoteInboxMembers(p, tasks) {
		tasks, err = pe.taskStore.List(task.Filter{PlanID: planID})
		if err != nil {
			logger.WarnCF("plan_engine", "processPlan: re-list member tasks after inbox promotion failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
			return
		}
	}

	if pe.promoteReadyMembers(tasks) {
		tasks, err = pe.taskStore.List(task.Filter{PlanID: planID})
		if err != nil {
			logger.WarnCF("plan_engine", "processPlan: re-list member tasks after promotion failed",
				map[string]any{"plan_id": planID, "error": err.Error()})
			return
		}
	}

	dispatched := pe.dispatchReadyMembers(ctx, planID, tasks)
	if dispatched {
		pe.touchActivity(planID)
	}

	// FR-041/R2-04 (US-7 acceptance 3, DS-4, "member-cancel plan outcome"):
	// checked BEFORE allMembersTerminal, and takes priority over it. A
	// member left `blocked` behind a cancelled member is never terminal
	// (AdvanceBlockedDependents only ever promotes a blocked dependent on a
	// DONE dependency — blocked_by.go — never on a cancelled/failed one), so
	// allMembersTerminal would silently stay false forever and the plan
	// would otherwise rot until FR-064's (days-later, UNrestartable)
	// idle-expiry brake — exactly the outcome this rule exists to prevent.
	if planStuckAfterMemberCancel(tasks) {
		pe.failPlanLocked(p.ID, plan.FailedReasonStoppedByUser, buildMemberCancelHandover(p, tasks))
		return
	}

	if allMembersTerminal(tasks) {
		// UAT defect B: a plan parked by the judge-unavailability backstop is
		// waiting for an ADJUDICATOR, not for another judge round. Starting
		// one here is the busy-loop the park exists to end —
		// surfaceJudgeUnavailableStall parks precisely because repeated rounds
		// produced no verdict, and falling through would immediately start
		// round N+1 and undo the park on the very next tick.
		//
		// ⚠ THE STREAK CONDITION IS LOAD-BEARING — do not simplify this to
		// `phase == PhaseStalled` alone. PhaseStalled is ALSO set by
		// surfaceStallIfAny for an ordinary blocked/inbox stall, and although
		// THAT stall can only be RAISED while the DAG is non-terminal, the
		// phase persists after the condition clears: surfaceStallIfAny is the
		// only code that clears it and it is only reached on the
		// non-all-terminal path. So a plan that stalled on a blocked member
		// and then had that member resolve arrives here all-terminal and
		// still phased `stalled`, and it legitimately needs its judge round.
		// Gating on the phase alone wedges exactly that plan forever — caught
		// by TestSupervisionWake_NewConditionWakesAgainAfterFirstTurnCompletes,
		// which is a regression anchor for this line.
		//
		// ⚠ THE PHASE CONDITION IS LOAD-BEARING TOO — do not simplify this to
		// `judgeUnavailableParked(planID)` alone either (the H1 review
		// suggested it; this is why it was not taken). The streak is
		// deliberately NOT reset by a correction — only a real verdict clears
		// it — so a parked plan that the adjudicator corrects arrives back
		// here all-terminal, at `dispatching`, still parked. Dropping the
		// phase clause would hold its judge round back forever and make the
		// correction path incapable of ever rescuing the plan, which is the
		// one recovery route the park exists to open. The genuinely
		// unenforceable case the H1 finding names — a park whose write failed,
		// leaving the phase at `judging` — is intercepted upstream in this
		// same function's phase switch, before the plan can reach here.
		//
		// The unmet-verdict park (awaiting_supervision) is deliberately not
		// covered here either: it has its own, already-working brake in
		// beginPlanJudgeRound's F2 round-burn signature gate.
		//
		// Routed into the supervision deadline ladder rather than simply
		// returning: that ladder is bounded and terminal (FR-021/FR-022 —
		// re-wake, then failed(supervision_unavailable) once the attempt
		// ceiling is spent), which is what turns an unbounded silent phase
		// into a bounded, visible one.
		if p.EffectivePlanPhase() == plan.PhaseStalled && pe.judgeUnavailableParked(planID) {
			pe.evaluateSupervisionDeadlineLocked(p)
			return
		}
		pe.beginPlanJudgeRound(p, tasks)
		return
	}

	// Round-1 UAT finding #5, "ALSO" half: the DAG is not all-terminal (real
	// work remains) yet nothing was dispatched this pass — if that is because
	// nothing is dispatchable or in-flight at all (not a transient dispatch
	// error, which would still have found a `next` member to retry), the plan
	// cannot make progress and must say so rather than spin on "Running 0/N"
	// forever. See surfaceStallIfAny's doc comment.
	pe.surfaceStallIfAny(p, tasks)
}

// promoteReadyMembers re-runs AdvanceBlockedDependents (pkg/task/blocked_by.go)
// for every DONE member task's ID. task_executor.go's onTaskComplete already
// triggers this reactively on every individual task completion (it is not
// plan-scoped — it fires for any task, member or not); this is a defensive
// re-scan (FR-062's boot-reconciliation requirement: "blocked tasks whose
// deps are all done are advanced") catching any edge the reactive path might
// have missed, e.g. a process crash between a dependency completing and its
// dependent's promotion. AdvanceBlockedDependents is idempotent: a dependent
// already promoted, or not yet fully satisfied, is a no-op.
func (pe *PlanEngine) promoteReadyMembers(tasks []task.Task) (advancedAny bool) {
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusDone {
			continue
		}
		advanced, err := pe.taskStore.AdvanceBlockedDependents(t.ID)
		if err != nil {
			logger.WarnCF("plan_engine", "advance blocked dependents failed",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
		if len(advanced) > 0 {
			advancedAny = true
		}
	}
	return advancedAny
}

// promoteInboxMembers promotes every member task of a DISPATCHABLE plan
// (p.PermitsMemberDispatch()) that is still sitting in the derived-nothing
// `inbox` status to `next` (round-1 UAT finding #5: "an executed plan
// silently stalls forever when its members are in inbox"). A task attached
// to a plan — whether at Execute time (attached before/at approval) or
// later, to an ALREADY-running plan (attached via the task detail panel) —
// lands in `inbox` by default (task.normalize()); NEITHER dispatchReadyMembers
// (only ever looks at `next`) NOR promoteReadyMembers above (only ever
// cascades a DONE member's blocked dependents) ever look at `inbox`, so
// without this an inbox member is invisible to the plan engine forever — the
// plan sits at "Running 0/N" with no self-heal and no signal.
//
// Being a member of a plan the engine considers dispatchable IS the
// commitment `next` represents for a standalone task (there is no separate
// per-member approval step in this product) — so promoting a dispatchable
// plan's inbox member to `next` is completing the state transition Execute
// already authorized, not a policy relaxation.
//
// Gated STRICTLY by p.PermitsMemberDispatch() — the plan-dispatch gate stays
// the sole authority on whether a plan may promote/dispatch anything at all;
// a draft, cap-waiting-approved, done, or failed plan, or a paused running
// plan, promotes NOTHING. Every caller of this function has already re-read
// the plan's live State/PausedReason under planDecisionMu in the same
// critical section (mirrors dispatchReadyMembers' own doc comment on why
// that re-check is sufficient) — this is the same "single source of truth,
// checked once more defensively at the point of use" pattern used throughout
// this file.
//
// Delegates the actual write to the ordinary task.Store.Update path — NOT a
// bespoke direct write — so every existing invariant keeps applying
// unconditionally: inbox -> next is already a legal lifecycle transition
// (validateTransition, store.go), and recomputeBlockedStateLocked (store.go)
// runs as the UNCONDITIONAL terminal step of every Update, so a member whose
// blocked_by set has an unmet dependency is immediately and correctly
// re-derived to `blocked` by this SAME write — this function never inspects
// BlockedBy itself, and therefore can never promote a member whose
// dependencies are unmet. That member then promotes later via the EXISTING
// cascade (AdvanceBlockedDependents / promoteReadyMembers above) exactly like
// any other blocked member, once its dependency completes — no new
// promotion path is introduced for that case.
//
// Runs on EVERY processPlan pass (the per-tick sweep AND the reactive
// task_status_changed handler both call processPlan) — not a one-shot at
// Execute/approval — so a member attached to an ALREADY-running plan gets
// the identical self-heal on the very next pass, not just a plan's first
// dispatch wave. tryStartApprovedPlan additionally calls this once,
// immediately on approved->running admission, so a plan's initial member
// wave does not have to wait for the next tick to be promoted and dispatched.
func (pe *PlanEngine) promoteInboxMembers(p *plan.Plan, tasks []task.Task) (promotedAny bool) {
	if !p.PermitsMemberDispatch() {
		return false
	}
	next := task.StatusNext
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusInbox {
			continue
		}
		if _, err := pe.taskStore.Update(t.ID, task.Patch{Status: &next}); err != nil {
			logger.WarnCF("plan_engine", "promote inbox member to next failed",
				map[string]any{"plan_id": p.ID, "task_id": t.ID, "error": err.Error()})
			continue
		}
		promotedAny = true
	}
	return promotedAny
}

// promoteReadyStandaloneTasks re-derives the `blocked`/`next` state of every
// STANDALONE (PlanID == "") task currently sitting in the derived `blocked`
// side-state, once per engine Tick — regardless of whether any plan exists
// or is running. Plan MEMBER tasks already get an equivalent defensive
// re-check via promoteReadyMembers (called per RUNNING plan from processPlan,
// scoped to that plan's own DONE members via a PlanID-filtered task.Filter);
// a standalone task, by definition, falls outside that scope entirely, so it
// needs its own plan-independent sweep. Tasks with a non-empty PlanID are
// skipped here unconditionally so this never double-promotes a plan member
// or fights promoteReadyMembers' own pass over the same task.
//
// This closes the regression the blocked-derivation persistence fix (S2 UAT
// finding A) introduced: `blocked` became a durable, EVENT-triggered latch —
// recomputeBlockedStateLocked only ever runs inside Create/updateLocked/
// RestartReset/AddDependency/SpawnReset (see that function's own doc
// comment), so a standalone dependent whose blocker completes via a path
// that does not also re-trigger AdvanceBlockedDependents (a crash between
// the blocker's `done` write and the cascade, or a direct-write path like
// DropOrphanEdges) is stuck in `blocked` forever with no self-heal and no
// user-facing escape — the store rejects a client PATCH back to `next`
// (ErrBlockedNotSettable). Before the blocked-derivation fix, a dependent
// simply stayed `next` and CheckQueuedTasks' own per-tick dependency check
// re-verified it every heartbeat; `blocked` tasks are invisible to that
// dispatch-side filter (task.Filter{Status: task.StatusNext}), so this sweep
// is the replacement self-heal for the STANDALONE half of that lost
// self-healing behavior.
//
// Reuses AdvanceUnblocked (pkg/task/store.go) rather than a third status
// derivation, per the review's explicit ask: AdvanceUnblocked forces the
// task to `next` under the internal allowBlockedSet hatch and then
// unconditionally re-runs recomputeBlockedStateLocked as the terminal step
// of updateLocked — which snaps the task straight back to `blocked` if a
// dependency is genuinely still unmet. Calling it on every blocked
// standalone task, every tick, is therefore a safe, idempotent no-op
// whenever nothing has actually changed (the exact "re-checked every tick"
// self-healing property the blocked-derivation fix took away), and a
// correct promotion the moment a dependency really has completed.
func (pe *PlanEngine) promoteReadyStandaloneTasks() {
	tasks, err := pe.taskStore.List(task.Filter{Status: task.StatusBlocked})
	if err != nil {
		logger.WarnCF("plan_engine", "promoteReadyStandaloneTasks: list blocked tasks failed",
			map[string]any{"error": err.Error()})
		return
	}
	for i := range tasks {
		t := &tasks[i]
		if t.PlanID != "" {
			// Plan members are promoteReadyMembers' concern (per running
			// plan); skip to avoid double-promoting or racing it.
			continue
		}
		if _, err := pe.taskStore.AdvanceUnblocked(t.ID); err != nil {
			logger.WarnCF("plan_engine", "promoteReadyStandaloneTasks: advance failed",
				map[string]any{"task_id": t.ID, "error": err.Error()})
		}
	}
}

// dispatchReadyMembers dispatches every member task currently `next`
// (FR-058). A task in `next` should always have every blocked_by dependency
// satisfied already (the store's own recompute keeps that invariant), but
// ExecuteTask re-verifies it independently regardless (task_executor.go) —
// dispatchReadyMembers does not duplicate that check. ExecuteTask is itself
// bounded by TaskExecutor's own global dispatch semaphore; a transient
// ErrDispatchCapReached (or any other dispatch error) is logged and simply
// retried on the next tick/event, never treated as fatal to the plan.
//
// Calls pe.dispatcher.executeTaskPlanVerified — NOT ExecuteTask — the ONE
// documented bypass of TaskExecutor's plan-state gate (requirePlanExecuting).
// Every caller of THIS function (tryStartApprovedPlan, processPlan,
// AppendCorrection) has already re-read planID's live State (and
// PausedReason) under pe.planDecisionMu, in the SAME critical section that
// then calls this, immediately before doing so — re-verifying it a second
// time inside ExecuteTask would be redundant, and would also make this
// dispatch depend on TaskExecutor's OWN, independently-wired plan.Store
// agreeing (see executeTaskPlanVerified's doc for why that's a real boot-
// ordering risk, not just an optimization concern).
func (pe *PlanEngine) dispatchReadyMembers(ctx context.Context, planID string, tasks []task.Task) (dispatchedAny bool) {
	// NewPlanEngine leaves this a TRUE nil interface when constructed with no
	// TaskExecutor (see its "typed nil inside an interface" note). Every other
	// pe.dispatcher call site guards; this one did not, and reached the nil
	// only once corrections became applicable at all — an engine with no
	// executor crashed the whole turn instead of reporting it.
	//
	// Loud, not silent: with no dispatcher NO member will ever run, so the
	// plan sits at dispatching forever. That is a wiring defect, not a
	// condition to swallow.
	if pe.dispatcher == nil {
		logger.ErrorCF("plan_engine", "no task dispatcher wired; plan members cannot be dispatched",
			map[string]any{"plan_id": planID, "ready_members": len(tasks)})
		return false
	}
	// ⚠ REGRESSION ANCHOR (ADR-055 fix wave, finding 1) — DO NOT pass `ctx`
	// straight through to the dispatcher.
	//
	// A dispatched member runs on its OWN goroutine that must outlive this
	// call. TaskExecutor.ExecuteTask derives the member's execution context
	// with a plain context.WithCancel(ctx) and does NOT detach (unlike its
	// sibling StartTaskNow, which detaches from context.Background() and says
	// why: "The goroutine must outlive the request"). So whatever cancellation
	// authority the CALLER's context carries silently becomes the member turn's
	// kill switch.
	//
	// That was harmless only for as long as every caller happened to pass
	// context.Background() (Tick, runEventLoop). AppendCorrection broke the
	// coincidence: it is reached from the PlanSupervisor's own turn, so its ctx
	// is a DESCENDANT of that turn's context — which dispatchPlanTurn cancels
	// unconditionally at turn end (`defer cancel()`). A correction's tail
	// member was therefore dispatched, then killed with context.Canceled
	// seconds later when the supervisor emitted its closing message, failing as
	// "execution error: context canceled". The plan went straight back to
	// all-terminal → judge → UNMET → park → wake → correct, until the round
	// budget ran out. No correction's work could ever complete.
	//
	// The guarantee belongs HERE, at the single chokepoint every plan-member
	// dispatch funnels through, rather than at the one call site that happened
	// to expose it: any FUTURE caller reached from a request/turn context has
	// the same hazard and gets the same protection by construction.
	//
	// WithoutCancel (not Background) keeps the caller's context VALUES —
	// request/trace identity used for logging and attribution downstream —
	// while severing cancellation and deadline. The member's real cancellation
	// authority is unchanged: the explicit cancel func TaskExecutor stores in
	// te.running[taskID], which the plan-scoped Stop fan-out drives.
	dispatchCtx := context.WithoutCancel(ctx)
	for i := range tasks {
		t := &tasks[i]
		if t.Status != task.StatusNext {
			continue
		}
		if err := pe.dispatcher.executeTaskPlanVerified(dispatchCtx, t.ID); err != nil {
			logger.WarnCF("plan_engine", "member task dispatch failed (will retry)",
				map[string]any{"plan_id": planID, "task_id": t.ID, "error": err.Error()})
			continue
		}
		dispatchedAny = true
	}
	return dispatchedAny
}

func allMembersTerminal(tasks []task.Task) bool {
	for i := range tasks {
		if !task.IsTerminal(tasks[i].Status) {
			return false
		}
	}
	return true
}

// planStallReason inspects a RUNNING, dispatchable plan's freshest member
// snapshot (taken by the caller AFTER this pass's own inbox-promotion and
// blocked-cascade attempts, immediately before dispatch) and reports a
// plain-language reason the plan is stuck, or "" when it is not. "Stuck"
// here means: the DAG is NOT all-terminal (the caller checks
// allMembersTerminal first — a genuinely finished DAG goes to the plan
// judge, not here) AND no member is currently dispatchable (`next`) or
// GENUINELY in flight (`in_progress` AND something is executing it — see the
// third stall shape below) — i.e. this pass's dispatchReadyMembers call is
// guaranteed to have been a complete no-op.
//
// This is the "ALSO: THE SILENT PART IS ITS OWN BUG" half of round-1 UAT
// finding #5: even with inbox members now self-promoting
// (promoteInboxMembers), a member can still be legitimately `blocked` on a
// dependency this plan's own dispatch loop will never itself resolve (e.g. a
// blocker outside this plan's member set that nobody is running), or an
// inbox member whose promotion attempt itself failed (already logged at the
// point of failure). Either is a genuine "no progress possible without help"
// condition, and must not render as an indefinitely-spinning "Running" chip
// with nothing to explain why.
//
// THE THIRD STALL SHAPE (UAT wedge fix): a member that is `in_progress` on
// disk while NOTHING IS EXECUTING IT. Until this, `in_progress` was read as
// "in flight" unconditionally, so the most eternal stall the system can
// produce was the one shape this function was guaranteed to miss.
//
// It is a real, reachable state, not a hypothetical. The task run loop
// (task_run_loop.go) has paths that end a member's run WITHOUT a terminal
// write and WITHOUT a restart — adjudicateRunClaim's DoD-unreadable branch and
// finishRunTurn's claim-read-fault branch (both leave the task in_progress
// with the reason written on it), and consumeTaskAttempt's CAS-conflict
// branch — for which boot reconciliation is the accepted backstop if the
// retry never comes; there is no dedicated in-process reaper. For a PLAN member that is
// not a backstop at all: the plan goes on rendering "Running 5/6" for as long
// as the process lives, its own LastActivityAt frozen at the last dispatch,
// and its only terminator is the multi-day idle-expiry calendar brake.
//
// The test is the TaskExecutor's own dispatch-slot map, not a timer: a member
// holds its slot for the entire run — it is deleted in runTask's OUTERMOST
// defer, after adjudication and after any redispatch — so a legitimately slow
// member (the UAT's 40-minute one) is never once observed stranded, however
// long it takes. See strandedSince for the dwell, which exists purely to
// outlast the narrow claim-before-slot and cold-boot windows.
//
// A stranded member does not make a plan stalled on its own: if any OTHER
// member is dispatchable or genuinely in flight, the plan can still make
// progress and is reported as running, exactly as before. What changes is that
// a stranded member no longer COUNTS as in-flight, so the "Running 5/6" case —
// where the stranded member is the only non-terminal one left — is now
// diagnosed instead of spun on.
func (pe *PlanEngine) planStallReason(tasks []task.Task, now time.Time) string {
	stranded := pe.observeStrandedMembers(tasks, now)

	var blockedIDs, inboxIDs, strandedIDs []string
	for i := range tasks {
		switch tasks[i].Status {
		case task.StatusNext:
			return "" // something is dispatchable - not stalled
		case task.StatusInProgress:
			if !stranded[tasks[i].ID] {
				return "" // genuinely in flight - not stalled
			}
			strandedIDs = append(strandedIDs, tasks[i].ID)
		case task.StatusBlocked:
			blockedIDs = append(blockedIDs, tasks[i].ID)
		case task.StatusInbox:
			inboxIDs = append(inboxIDs, tasks[i].ID)
		}
	}
	if len(blockedIDs) == 0 && len(inboxIDs) == 0 && len(strandedIDs) == 0 {
		return "" // no non-terminal, non-dispatchable member found
	}
	var sb strings.Builder
	sb.WriteString("This plan has no dispatchable or in-flight members, so it cannot make progress right now.")
	if len(strandedIDs) > 0 {
		fmt.Fprintf(&sb, " %d member(s) are recorded as in_progress but no run is executing them — "+
			"their run ended without writing an outcome, so nothing will move them again: %s.",
			len(strandedIDs), strings.Join(strandedIDs, ", "))
	}
	if len(blockedIDs) > 0 {
		fmt.Fprintf(&sb, " %d member(s) are blocked on an unmet dependency this plan cannot itself resolve: %s.",
			len(blockedIDs), strings.Join(blockedIDs, ", "))
	}
	if len(inboxIDs) > 0 {
		fmt.Fprintf(&sb, " %d member(s) could not be promoted from inbox: %s.",
			len(inboxIDs), strings.Join(inboxIDs, ", "))
	}
	sb.WriteString(" A correction (adjust dependencies, or Stop and re-author) is needed to unstick it.")
	return sb.String()
}

// planMemberStrandedGrace is how long a member must be CONTINUOUSLY observed
// in_progress-with-no-dispatch-slot before planStallReason counts it as
// stranded. It is a race guard, not a patience setting — see strandedSince for
// the three windows it exists to outlast, and for why it cannot false-positive
// on a slow member no matter how slow that member is.
//
// Sized at four production ticks (defaultPlanEngineTickInterval = 30 s), which
// is orders of magnitude longer than the widest of those windows (one
// fsync-bound session mint) while still turning an eternal stall into a
// two-minute one.
const planMemberStrandedGrace = 2 * time.Minute

// strandedMemberEvictAfter bounds strandedSince: an entry nothing has observed
// for this long belongs to a member whose plan is gone, terminal, or no longer
// swept, and is dropped. Generous relative to the grace so a genuinely
// stranded member that IS still being observed every tick is never evicted out
// from under its own diagnosis.
const strandedMemberEvictAfter = time.Hour

// observeStrandedMembers advances the stranded-observation clock for every
// in_progress member in tasks and returns the set that has been stranded for
// longer than planMemberStrandedGrace.
//
// Returns an empty set when memberExecuting is unwired (a struct-literal test
// engine, or a boot with no task executor): "cannot tell" must never read as
// "stranded", so the whole term disappears and stall diagnosis behaves exactly
// as it did before it existed.
//
// Caller holds planDecisionMu; this takes pe.mu underneath it, the same
// ordering every other in-memory map on this engine uses. The memberExecuting
// reader is called BEFORE pe.mu is taken — it locks the TaskExecutor's own
// mutex, and no lock ordering between the two is established anywhere else.
func (pe *PlanEngine) observeStrandedMembers(tasks []task.Task, now time.Time) map[string]bool {
	stranded := map[string]bool{}
	pe.mu.Lock()
	reader := pe.memberExecuting
	pe.mu.Unlock()
	if reader == nil {
		return stranded
	}

	type probe struct {
		id        string
		executing bool
	}
	probes := make([]probe, 0, len(tasks))
	for i := range tasks {
		if tasks[i].Status != task.StatusInProgress || tasks[i].ID == "" {
			continue
		}
		probes = append(probes, probe{id: tasks[i].ID, executing: reader(tasks[i].ID)})
	}

	pe.mu.Lock()
	defer pe.mu.Unlock()
	if pe.strandedSince == nil {
		pe.strandedSince = make(map[string]strandedMemberObservation)
	}
	for _, p := range probes {
		if p.executing {
			// A slot exists: the run is alive (or about to be). Any prior
			// stranded run is over and must not be resumed later from its old
			// start time.
			delete(pe.strandedSince, p.id)
			continue
		}
		obs, ok := pe.strandedSince[p.id]
		if !ok || now.Before(obs.first) {
			pe.strandedSince[p.id] = strandedMemberObservation{first: now, lastSeen: now}
			continue
		}
		obs.lastSeen = now
		pe.strandedSince[p.id] = obs
		if now.Sub(obs.first) >= planMemberStrandedGrace {
			stranded[p.id] = true
		}
	}
	for id, obs := range pe.strandedSince {
		if now.Sub(obs.lastSeen) >= strandedMemberEvictAfter {
			delete(pe.strandedSince, id)
		}
	}
	return stranded
}

// taskExecutorHoldsDispatchSlot reports whether te currently holds a dispatch
// slot for taskID — a reserved slot (claimed, goroutine not yet launched) or a
// live one (goroutine running). It is the raw read behind
// PlanEngine.memberExecuting.
//
// It lives here rather than on TaskExecutor because it is this file's
// question, and te.running's critical sections are all leaf sections (a map
// read or write and nothing else, never a call out), so taking te.mu from
// under planDecisionMu introduces no lock-ordering hazard.
func taskExecutorHoldsDispatchSlot(te *TaskExecutor, taskID string) bool {
	if te == nil || taskID == "" {
		return false
	}
	te.mu.Lock()
	defer te.mu.Unlock()
	_, ok := te.running[taskID]
	return ok
}

// --- Play = resumed_from generation (D13/G-12) -----------------------------

// PlayResult is the outcome of a Play (resume) operation.
//
// StillFailedMemberIDs lists the task IDs whose RestartReset failed (the
// per-member reset is logged-and-continued — see PlayPlan — so a partial
// failure is not fatal but the REST handler must surface it to the operator
// rather than returning an unqualified 200).
//
// not-wire-format: engine-internal type.
type PlayResult struct {
	NewGeneration        int      `json:"new_generation"`
	ResumedFrom          string   `json:"resumed_from,omitempty"`
	PlanID               string   `json:"plan_id"`
	StillFailedMemberIDs []string `json:"still_failed_member_ids,omitempty"`
}

// PlayPlan resumes a stopped/failed plan as a new owner-session generation
// (FR-144/D13/G-12). Transitions the plan cancelled/failed → approved (the
// restart transition, which zeroes JudgeRounds), preserves done members,
// resets failed/cancelled members to `next` (resumed from the last git commit
// if a commitResolver is wired; fresh attempt otherwise), clears the durable
// unmet signature, and increments the generation. The plan then re-enters
// the normal approved→running admission path on the next tick.
func (pe *PlanEngine) PlayPlan(ctx context.Context, planID string) (*PlayResult, error) {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(planID)
	if err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: get plan %q: %w", planID, err)
	}
	if p.State != plan.StateFailed {
		return nil, fmt.Errorf("%w: plan %q is %s", plan.ErrNotFailed, planID, p.State)
	}
	// The restart transition validates failed_reason == stopped_by_user
	// (plan.ValidateRestartTransition). This is the same gate the REST
	// /restart endpoint uses.
	approved := plan.StateApproved
	if _, updateErr := pe.planStore.Update(planID, plan.Patch{State: &approved}); updateErr != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: restart transition: %w", updateErr)
	}

	// Reset failed/cancelled members to `next`; preserve done members.
	// Resume from last git commit if available (D13); fresh attempt otherwise.
	// Track still-failed members so the REST handler can surface partial
	// resets (RestartReset is logged-and-continued, never fatal).
	tasks, err := pe.taskStore.List(task.Filter{PlanID: planID})
	if err != nil {
		return nil, fmt.Errorf("plan_engine: PlayPlan: list member tasks: %w", err)
	}
	var stillFailed []string
	for i := range tasks {
		t := &tasks[i]
		if task.IsTerminal(t.Status) && t.Status != task.StatusDone {
			// Failed/cancelled member — reset for re-dispatch.
			if _, rerr := pe.taskStore.RestartReset(t.ID); rerr != nil {
				logger.WarnCF("plan_engine", "PlayPlan: could not reset member",
					map[string]any{"plan_id": planID, "task_id": t.ID, "error": rerr.Error()})
				stillFailed = append(stillFailed, t.ID)
				continue
			}
			pe.recordMemberResumePoint(planID, t.ID)
		}
	}

	// Clear the durable unmet signature (fresh round on the new generation).
	pe.clearUnmetTerminalSignature(planID)
	// A new generation is a genuinely fresh attempt at adjudication too, so
	// it must not inherit a prior generation's judge-unavailability streak
	// (UAT defect B).
	pe.clearJudgeUnavailableStreak(planID)
	clearSig := ""
	if _, err := pe.planStore.Update(planID, plan.Patch{LastUnmetTerminalSignature: &clearSig}); err != nil {
		logger.WarnCF("plan_engine", "PlayPlan: could not clear unmet signature",
			map[string]any{"plan_id": planID, "error": err.Error()})
	}

	// Mint a new generation (D13/G-12).
	prevGen := pe.planGeneration(planID)
	newGen := pe.incrementPlanGeneration(planID)

	logger.InfoCF("plan_engine", "plan played: new generation",
		map[string]any{"plan_id": planID, "generation": newGen, "resumed_from": prevGen})

	return &PlayResult{
		NewGeneration:        newGen,
		ResumedFrom:          fmt.Sprintf("gen-%d", prevGen),
		PlanID:               planID,
		StillFailedMemberIDs: stillFailed,
	}, nil
}

// recordMemberResumePoint resolves the gitevidence checkpoint for a member
// being resumed via Play (D13: "resume from last git commit"), PERSISTS it
// on the task record as ResumeFromCommit — the resume baseline the worker turn
// and plan Judge consume (the next attempt's diff is measured from this hash)
// — and materializes the member's isolated resume working tree at that commit
// via the resolver's ResetMemberCheckout (#537: the D10 isolation ladder).
//
// If no commitResolver is wired, or the resolver returns no commit (unborn
// repo / nested-repo degrade / member never committed), ResumeFromCommit is
// cleared to "" — the FR-155 fresh-attempt fallback, signalled in the log. The
// task was already RestartReset to `next` by the caller, which cleared any
// stale ResumeFromCommit; this call sets the value for the new generation.
func (pe *PlanEngine) recordMemberResumePoint(planID, taskID string) {
	pe.mu.Lock()
	cr := pe.commitResolver
	pe.mu.Unlock()

	var hash string
	switch cr {
	case nil:
		logger.InfoCF("plan_engine", "member resume: fresh attempt (no commit resolver)",
			map[string]any{"plan_id": planID, "task_id": taskID})
	default:
		h, err := cr.LastMemberCommit(planID, taskID)
		if err != nil || h == "" {
			logger.InfoCF("plan_engine", "member resume: fresh attempt (no boundary commit)",
				map[string]any{"plan_id": planID, "task_id": taskID, "error": fmt.Sprintf("%v", err)})
		} else {
			hash = h
			logger.InfoCF("plan_engine", "member resume: from commit",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash})
		}
	}

	// D13/#537: materialize the member's resume working tree at the resolved
	// commit via the isolation ladder (or clear any stale tree on the
	// fresh-attempt path). A materialization failure degrades the member to
	// the shared-tree resume — the baseline hash below is still persisted and
	// the committed work remains in the shared tree — so it is logged, never
	// fatal to Play.
	if cr != nil {
		dir, cerr := cr.ResetMemberCheckout(planID, taskID, hash)
		if cerr != nil {
			logger.WarnCF("plan_engine", "member resume: could not materialize resume checkout — shared-tree resume",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "error": cerr.Error()})
		} else if dir != "" {
			logger.InfoCF("plan_engine", "member resume: working tree restored at commit",
				map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "dir": dir})
		}
	}

	// Persist the resolved baseline (hash, or "" for fresh attempt) on the
	// task so the worker turn / Judge start from it. Best-effort: a store
	// failure is logged, not fatal — the resume still proceeds (as a fresh
	// attempt if the hash couldn't be recorded).
	if _, err := pe.taskStore.Update(taskID, task.Patch{ResumeFromCommit: &hash}); err != nil {
		logger.WarnCF("plan_engine", "member resume: could not persist resume_from_commit",
			map[string]any{"plan_id": planID, "task_id": taskID, "commit": hash, "error": err.Error()})
	}
}
