// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_sweep.go implements the ADR-053 §5 boot sweep / crash-recovery layer
// (FR-117/FR-118/FR-119/FR-147/FR-193, acceptance G-13, conformance "§5 boot
// sweep", invariants INV-7/INV-9). It runs ONCE at gateway boot, folded into
// the same single pass as the plan engine's bootReconcile (N-15 — one sweep,
// not two).
//
// The sweep reconciles every persisted non-terminal session that has no live
// runtime turn (which, at the moment a fresh process boots, is ALL of them —
// no goroutine from a prior process can still be running a turn) to
// failed(interrupted) within a configurable budget, carrying its last
// checkpoint + undelivered messages and emitting a session.failed hook so
// plan recovery and idle settlement re-arm. Two INV-9 exemptions keep a
// legitimately-idle session from being swept:
//
//  1. a parked needs_input session that is still reconstructable
//     (isNeedsInputReconstructable re-evaluated AT BOOT, R§8.6 — the stored
//     needs_input.reconstructable hint is never the authority); and
//  2. a paused plan-owner session whose plan is durably
//     plan_phase=awaiting_supervision (C1/FR-147), resolved via the named
//     plan<->owner-session linkage (session.LifecycleRecord.OwnsPlanID ->
//     plan.Plan.PlanPhase), NOT via owner_scope (which is `human` for a
//     top-level owner and cannot itself identify the plan).
//
// The durable C1 fix (this wave's other half, in plan_engine.go) persists
// last_unmet_terminal_signature on the plan record; bootReconcile rehydrates
// the in-memory F2 gate from it BEFORE this sweep runs, so an unchanged
// awaiting-correction plan is NOT re-judged at boot (INV-7 preserved across
// INV-9). This file is the session side of that reconciliation.
package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// BootSweepResult records the outcome of one boot sweep pass for observability
// and test assertions.
type BootSweepResult struct {
	// Scanned is the count of non-terminal sessions the sweep considered.
	Scanned int
	// SweptToFailed lists the session IDs marked failed(interrupted).
	SweptToFailed []string
	// PreservedNeedsInput lists reconstructable parked needs_input sessions
	// left alone (exemption a / FR-119).
	PreservedNeedsInput []string
	// PreservedAwaitingCorrection lists paused plan-owner sessions whose plan
	// is durably awaiting_supervision, left alone (exemption b / FR-147).
	PreservedAwaitingCorrection []string
	// RebaselinedGoals lists in-flight goals quiesced/re-baselined for a
	// trigger-semantics change (N-15).
	RebaselinedGoals []string
}

// runBootSweep is the boot-time crash-recovery pass (FR-118/G-13/INV-9). It is
// the sole caller of the sweep logic at boot, invoked from Start right after
// bootReconcile. It no-ops cleanly (returning an empty result) when no
// lifecycle store is wired, so a bare struct-literal test engine or an
// unwired deployment is unaffected. The budget bounds the sweep's wall clock
// (boot_sweep_budget_seconds); the sweep is a bounded local scan so it
// completes well inside the budget in practice.
func (pe *PlanEngine) runBootSweep(ctx context.Context) BootSweepResult {
	pe.mu.Lock()
	ls := pe.lifecycleStore
	budget := pe.bootSweepBudget
	snapMax := pe.snapshotMaxBytes
	pe.mu.Unlock()

	if ls == nil {
		// No durable session store wired — nothing to sweep. The engine still
		// reconciles plans via bootReconcile; only the session side is absent.
		logger.InfoCF("plan_engine", "boot sweep skipped: no lifecycle store wired", nil)
		return BootSweepResult{}
	}
	if budget <= 0 {
		budget = time.Duration(DefaultBootSweepBudgetSeconds) * time.Second
	}
	if snapMax <= 0 {
		snapMax = DefaultSnapshotMaxBytes
	}

	sweepCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	result, err := pe.bootSweep(sweepCtx, ls, snapMax)
	if err != nil {
		// A boot-sweep failure is logged but NEVER aborts startup — the system
		// must still boot and serve. Stranded sessions remain non-terminal on
		// disk and are caught by the next boot sweep or operator intervention
		// (CRIT-1: no wedge from a sweep-time I/O failure).
		logger.ErrorCF("plan_engine", "boot sweep completed with error",
			map[string]any{"error": err.Error(), "scanned": result.Scanned, "swept": len(result.SweptToFailed)})
		return result
	}
	logger.InfoCF("plan_engine", "boot sweep complete",
		map[string]any{"scanned": result.Scanned, "swept": len(result.SweptToFailed),
			"preserved_needs_input": len(result.PreservedNeedsInput),
			"preserved_awaiting":    len(result.PreservedAwaitingCorrection),
			"rebaselined_goals":     len(result.RebaselinedGoals)})
	return result
}

// bootSweep is the testable core of the boot sweep. It is a method on
// PlanEngine (not a free function) because exemption (b) requires resolving a
// paused owner session's plan through the engine's own planStore, and recovery
// (re-judge/re-dispatch of plans whose state changed) is driven through
// processPlan — both already held by the engine. It does not acquire
// planDecisionMu itself (each per-plan recovery touch does, inside
// processPlan); the session Persist calls are serialized by the lifecycle
// store's own per-session striped lock.
func (pe *PlanEngine) bootSweep(ctx context.Context, ls *session.LifecycleStore, snapshotMaxBytes int64) (BootSweepResult, error) {
	result := BootSweepResult{}

	records, err := ls.List(session.LifecycleFilter{NonTerminalOnly: true})
	if err != nil {
		return result, err
	}
	result.Scanned = len(records)

	for i := range records {
		if ctx.Err() != nil {
			// Budget exhausted — stop scanning and return what we have. The
			// remaining non-terminal sessions are caught by the next boot
			// sweep; we never block boot indefinitely (CRIT-1).
			break
		}
		rec := records[i]

		// Exemption (a): a parked needs_input session that is still
		// reconstructable at boot is preserved as resumable (FR-119/R§8.6).
		// The stored needs_input.reconstructable hint is NEVER the authority
		// here — isNeedsInputReconstructable is re-evaluated against the
		// CURRENT state (agents/correlations/checkpoints may have changed
		// since park).
		if rec.State == session.LifecycleNeedsInput {
			if pe.isNeedsInputReconstructable(&rec, snapshotMaxBytes) {
				result.PreservedNeedsInput = append(result.PreservedNeedsInput, rec.SessionID)
				continue
			}
			// Not reconstructable — fall through to the sweep, identical to a
			// stranded running session (R§8.6: "ANY false -> swept
			// identically -> failed(interrupted)").
		}

		// Exemption (b): a paused plan-owner session whose plan is durably
		// plan_phase=awaiting_supervision is legitimately idle awaiting
		// the owner (C1/FR-147/INV-9). Resolved via the NAMED linkage
		// (OwnsPlanID -> plan.PlanPhase), NOT via owner_scope — a top-level
		// owner session's owner_scope is `human`, which cannot identify the
		// plan. OwnsPlanID is the reciprocal of plan.Plan.OwnerSessionID.
		if rec.State == session.LifecyclePaused && rec.OwnsPlanID != "" {
			if pe.planIsAwaitingSupervision(rec.OwnsPlanID) {
				result.PreservedAwaitingCorrection = append(result.PreservedAwaitingCorrection, rec.SessionID)
				continue
			}
		}

		// N-15 live-upgrade re-baseline: an in-flight goal predating a
		// trigger-semantics change is quiesced/re-baselined rather than swept
		// to failed. resolveGoalSemanticsAction returns "rebaseline" when the
		// session's recorded semantics version predates the current build.
		if action := pe.resolveGoalSemanticsAction(&rec); action == goalActionRebaseline {
			result.RebaselinedGoals = append(result.RebaselinedGoals, rec.SessionID)
			continue
		}

		// Sweep: mark failed(interrupted), carrying last checkpoint +
		// undelivered messages (FR-118). The session is non-terminal (the
		// List filter guaranteed it), so this transition is legal on the same
		// generation (the immutable-terminal invariant L-3 only freezes a
		// TERMINAL tail — a non-terminal tail may always transition).
		if err := pe.sweepToFailedInterrupted(ls, &rec); err != nil {
			logger.WarnCF("plan_engine", "boot sweep: could not sweep session to failed(interrupted)",
				map[string]any{"session_id": rec.SessionID, "error": err.Error()})
			continue
		}
		result.SweptToFailed = append(result.SweptToFailed, rec.SessionID)
	}
	// Sweep errors above are deliberately logged + continued, not propagated:
	// one bad record must not abort the entire boot sweep (CRIT-1). The outer
	// err from ls.List is the only error the boot-sweep contract surfaces.
	return result, nil //nolint:nilerr
}

// sweepToFailedInterrupted persists rec's transition to failed(interrupted),
// preserving its checkpoint + undelivered messages and firing the
// session.failed hook so plan recovery / idle settlement re-arm (FR-118
// deliverable 3). It writes on the SAME generation (no follow_up mint): a
// non-terminal session may always transition to a terminal state on its own
// generation, and minting a new generation would orphan the audit trail of the
// interrupted run.
func (pe *PlanEngine) sweepToFailedInterrupted(ls *session.LifecycleStore, rec *session.LifecycleRecord) error {
	failed := *rec // preserve every field (checkpoint, undelivered messages, scope, ...)
	failed.State = session.LifecycleFailed
	failed.FailedReason = failedReasonInterrupted
	// NeedsInput is cleared on any non-needs_input state by Persist's own
	// invariant; explicitly nil it for clarity (the swept session is no longer
	// awaiting input — it was interrupted).
	failed.NeedsInput = nil
	if err := ls.Persist(&failed); err != nil {
		return err
	}
	// Fire the session.failed hook best-effort (FR-118 deliverable 3): a hook
	// panic is recovered and LOGGED (the doc above promises "recovered and
	// logged"), never blocking the sweep. The earlier `recover()` silently
	// swallowed the panic despite that promise — an operator watching logs saw
	// nothing, defeating the contract.
	pe.mu.Lock()
	hook := pe.sessionFailedHook
	pe.mu.Unlock()
	if hook != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("plan_engine", "sessionFailedHook panicked (recovered, never blocking the sweep)",
						map[string]any{"session_id": rec.SessionID, "failed_reason": failedReasonInterrupted, "panic": fmt.Sprint(r)})
				}
			}()
			hook(rec.SessionID, failedReasonInterrupted)
		}()
	}
	return nil
}

// planIsAwaitingSupervision reports whether planID's durable plan record
// is parked at plan_phase=awaiting_supervision (C1/FR-147). This is the
// exemption-(b) resolution: a paused owner session whose plan is in this phase
// is legitimately idle awaiting the owner's correction and MUST NOT be swept
// (INV-7 preserved across INV-9). Best-effort: a missing/corrupt plan record
// returns false (the session is then swept — the safe default, since the named
// linkage cannot be confirmed).
func (pe *PlanEngine) planIsAwaitingSupervision(planID string) bool {
	if planID == "" || pe.planStore == nil {
		return false
	}
	p, err := pe.planStore.Get(planID)
	if err != nil {
		return false
	}
	return p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision
}

// isNeedsInputReconstructable is the R§8.6 warm-resume reconstructability
// predicate — the AUTHORITATIVE determination of whether a parked needs_input
// session may be preserved as resumable at boot (re-evaluated AT BOOT, never
// the stored needs_input.reconstructable hint, which is park-time only/m5).
//
//	AND of four clauses:
//	(1) durable record state=needs_input AND a checkpoint
//	    (last_checkpoint_ref) captured result_so_far/context digest at park;
//	(2) child identity still resolves at boot (agent not deleted) — via the
//	    engine's agentResolver hook, nil = treat as resolving;
//	(3) the open correlation_id + its owner scope still exist
//	    (needs_input.correlation_id non-empty and an owner scope is present);
//	(4) retained snapshot within snapshot_max_bytes.
//
// ALL true -> preserved resumable. ANY false -> swept identically to a
// stranded running session (failed(interrupted)).
//
// Clause (4) is approximated in this wave: the lifecycle record does not yet
// carry a byte-count of the retained snapshot, so the checkpoint's presence
// (clause 1) stands in for "a snapshot was captured within cap at park time".
// The authoritative re-evaluation here focuses on the clauses that CAN change
// between park and boot — identity (2), correlation (3), and checkpoint (1) —
// which are the ones that actually invalidate a warm resume. A future wave
// that persists a snapshot byte-count extends this predicate's clause (4).
func (pe *PlanEngine) isNeedsInputReconstructable(rec *session.LifecycleRecord, snapshotMaxBytes int64) bool {
	if rec == nil || rec.State != session.LifecycleNeedsInput {
		return false
	}
	// (1) checkpoint present at park.
	if rec.LastCheckpointRef == "" {
		return false
	}
	// (2) child identity still resolves at boot.
	pe.mu.Lock()
	resolver := pe.agentResolver
	pe.mu.Unlock()
	if resolver != nil && !resolver(rec.AgentID) {
		return false
	}
	// (3) open correlation_id + owner scope still exist.
	if rec.NeedsInput == nil || rec.NeedsInput.CorrelationID == "" {
		return false
	}
	if rec.OwnerScopeKind == "" {
		return false
	}
	// (4) retained snapshot within snapshot_max_bytes — see the doc comment:
	// approximated by checkpoint presence in this wave (snapshotMaxBytes is
	// accepted as the configured cap but no byte-count is stored yet to test
	// against; clause 1 already guarantees a snapshot was captured).
	_ = snapshotMaxBytes
	return true
}

// --- N-15 live-upgrade re-baseline ---------------------------------------

// goalSemanticsAction classifies what the boot sweep should do with an
// in-flight goal-bearing session whose trigger semantics may predate the
// current build (N-15).
type goalSemanticsAction int

const (
	// goalActionNone means the session is not goal-bearing or its semantics
	// version matches the current build — handled by the normal sweep path.
	goalActionNone goalSemanticsAction = iota
	// goalActionRebaseline means the session predates a trigger-semantics
	// change and must be quiesced/re-baselined (idle timers re-armed, trigger
	// config re-read) so no goal straddles two semantics.
	goalActionRebaseline
)

// currentTriggerSemanticsVersion is the build-level trigger-semantics version.
// Bumped when a release changes goal/loop trigger interpretation; a session
// whose recorded version is strictly less than this is re-baselined at boot
// (N-15). Phase-2-C (the /goal + /loop wiring) records this version onto each
// session at set-time; until then resolveGoalSemanticsAction returns None for
// every session (no version field is populated yet), so the mechanism is in
// place and harmless — ready for Phase-2-C to populate without re-opening this
// file's logic.
const currentTriggerSemanticsVersion = 1

// resolveGoalSemanticsAction decides whether a session is an in-flight goal
// predating a trigger-semantics upgrade (N-15). The session-side semantics
// version is sourced from goalSemanticsVersionFor (a hook the gateway/Phase-2-C
// supplies; 0 means "unversioned" -> no re-baseline yet). The current build's
// version is currentTriggerSemanticsVersion (overridable for tests via
// pe.currentSemanticsVersionOverride).
func (pe *PlanEngine) resolveGoalSemanticsAction(rec *session.LifecycleRecord) goalSemanticsAction {
	if rec == nil || rec.GoalRef == "" {
		return goalActionNone
	}
	recorded := pe.goalSemanticsVersionFor(rec.SessionID)
	if recorded <= 0 {
		// Unversioned (pre-versioning install or Phase-2-C not yet populating
		// it) — do not re-baseline. The mechanism is armed for the first
		// bump that matters.
		return goalActionNone
	}
	current := pe.currentSemanticsVersion()
	if recorded < current {
		return goalActionRebaseline
	}
	return goalActionNone
}

// currentSemanticsVersion returns the build's trigger-semantics version,
// honoring a test override (pe.currentSemanticsVersionOverride) when set.
func (pe *PlanEngine) currentSemanticsVersion() int {
	pe.mu.Lock()
	v := pe.currentSemanticsVersionOverride
	pe.mu.Unlock()
	if v > 0 {
		return v
	}
	return currentTriggerSemanticsVersion
}

// goalSemanticsVersionFor is the per-session trigger-semantics-version
// resolver. It is a nil-safe accessor over the goalSemanticsVersioner field
// (wired by Phase-2-C / the gateway). Returns 0 when no versioner is
// installed, which resolveGoalSemanticsAction treats as "unversioned".
func (pe *PlanEngine) goalSemanticsVersionFor(sessionID string) int {
	pe.mu.Lock()
	v := pe.goalSemanticsVersioner
	pe.mu.Unlock()
	if v == nil {
		return 0
	}
	return v(sessionID)
}

// --- Intent-log boot replay (FR-148/M4/INV-6) ---------------------------

// replayIntentLogs replays the write-ahead intent-log at Start, applying any
// committed-but-not-done tail-append to the plan/task stores before the engine
// reconciles plans. Uncommitted intents are discarded (exact pre-append DAG);
// committed-not-done intents are applied idempotently via applyIntentRecord
// and marked done; done intents need no action. A replay error is logged but
// never aborts startup (CRIT-1: no wedge).
func (pe *PlanEngine) replayIntentLogs() {
	pe.mu.Lock()
	il := pe.intentLog
	pe.mu.Unlock()
	if il == nil {
		return
	}
	results, err := il.ReplayAllPlans(pe.applyIntentRecord)
	replayed, discarded := 0, 0
	for _, r := range results {
		replayed += r.Replayed
		discarded += r.Discarded
	}
	if err != nil {
		logger.ErrorCF("plan_engine", "intent-log boot replay completed with error",
			map[string]any{"error": err.Error(), "replayed": replayed, "discarded": discarded})
		return
	}
	if replayed > 0 || discarded > 0 {
		logger.InfoCF("plan_engine", "intent-log boot replay complete",
			map[string]any{"replayed": replayed, "discarded": discarded})
	}
}

// applyIntentRecord applies ONE self-contained intent record's per-file writes
// idempotently: creates each member task (skipping any that already exist —
// the idempotency guard for a partial re-apply after a crash), and applies the
// plan-record patch (clear the durable unmet signature so a fresh correction
// gets a fresh round, set plan_phase). Member bodies carry their own
// blocked_by edges (the intent's separate edge list is folded into member
// bodies at write time by the Phase-2 owner loop), so task creation is the
// single edge-wiring point. Satisfies the ApplyFunc contract IntentLog.
// ReplayAtBoot expects (FR-148 idempotent replay-forward).
func (pe *PlanEngine) applyIntentRecord(rec plan.IntentRecord) error {
	// Create each member task idempotently. A member that already exists
	// (Create returns its own error path) is treated as already-applied — the
	// replay is a no-op for it, exactly the idempotency INV-6 requires.
	for i := range rec.Members {
		m := rec.Members[i]
		if m.ID == "" {
			continue
		}
		if existing, gerr := pe.taskStore.Get(m.ID); gerr == nil && existing != nil {
			continue // already applied — idempotent no-op.
		}
		clone := m
		if err := pe.taskStore.Create(&clone); err != nil {
			// A collision that isn't "exists" is a real error — surface it so
			// ReplayAtBoot marks the intent not-done and retries next boot.
			return err
		}
	}
	// Apply the plan-record patch.
	if rec.Patch.ClearLastUnmetTerminalSignature || rec.Patch.PlanPhase != "" {
		patch := plan.Patch{}
		if rec.Patch.ClearLastUnmetTerminalSignature {
			empty := ""
			patch.LastUnmetTerminalSignature = &empty
		}
		if rec.Patch.PlanPhase != "" {
			ph := rec.Patch.PlanPhase
			patch.PlanPhase = &ph
		}
		if _, err := pe.planStore.Update(rec.PlanID, patch); err != nil {
			// A missing plan (deleted between commit and replay) is not a
			// replay-blocking error — the correction's target vanished, so the
			// intent is effectively done. Anything else surfaces.
			if !errors.Is(err, plan.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}
