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

	// Explicit retention (ADR-053 "nothing grows unbounded by omission",
	// applied to the S2 lifecycle store — the same posture DoD-10 requires of
	// the message inbox): the store is append-only, one .jsonl per session_id,
	// with no prior prune path at all. Wiring a task-dispatch producer this
	// wave (mintTaskLifecycleRecord, task_executor.go) meaningfully raises the
	// mint rate over delegate-only sessions (a redispatched goal-loop attempt
	// mints a fresh session per attempt, mirroring delegate's own
	// per-call mint), so this prune runs in the SAME boot pass rather than
	// being deferred. Deliberately independent of the sweep's own err/budget
	// above: pruning old TERMINAL records is unrelated to reconciling
	// non-terminal ones, and a prune failure must not be conflated with a
	// sweep failure in the log line above.
	if pruned, perr := ls.PruneTerminal(DefaultLifecycleRetentionDays, time.Now()); perr != nil {
		logger.WarnCF("plan_engine", "boot sweep: lifecycle retention prune failed",
			map[string]any{"error": perr.Error()})
	} else if pruned > 0 {
		logger.InfoCF("plan_engine", "boot sweep: pruned terminal lifecycle records past retention",
			map[string]any{"pruned": pruned, "retention_days": DefaultLifecycleRetentionDays})
	}

	return result
}

// DefaultLifecycleRetentionDays bounds how long a TERMINAL S2 lifecycle
// record (pkg/session/lifecycle.go) is retained on disk before runBootSweep
// prunes it (LifecycleStore.PruneTerminal). Mirrors the 90-day default this
// codebase already uses for day-partitioned session retention (CLAUDE.md
// "Storage" section) — a non-terminal record is NEVER pruned regardless of
// age (see PruneTerminal's own doc comment), so this bound only ever affects
// records the boot sweep (or a normal task/delegate completion) has already
// resolved.
const DefaultLifecycleRetentionDays = 90

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
	pe.reconcileUnifiedMetaStatus(&failed)
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

// reconcileUnifiedMetaStatus reconciles the OTHER session-status store — the
// one GET /api/v1/sessions and the SPA actually read, session.UnifiedMeta.
// Status (daypartition.go: active/archived/interrupted) — with the
// LifecycleRecord this sweep just wrote failed(interrupted). These are two
// deliberately DISTINCT vocabularies for two distinct purposes (see
// lifecycle.go's own package doc: "distinct from SessionStatus ... do not
// conflate"), but as this codebase actually wires them, a lifecycle record
// minted by delegate.go's `run` or by task_executor.go's
// mintTaskLifecycleRecord uses the EXACT SAME session_id that
// UnifiedStore.NewSession minted for that session's chat-transcript meta —
// so leaving UnifiedMeta untouched here would leave the user-visible
// session listing reporting "active" forever after a crash, even though the
// durable S2 record now correctly says failed(interrupted). That divergence
// between the two stores was itself the user-visible half of the FR-118/
// G-13 gap this wave closes: a boot sweep with no user-visible effect is not
// a fix.
//
// Best-effort and expected to legitimately fail for a delegate/subturn
// session: pkg/tools/delegate.go's spawnSubTurn path never calls
// UnifiedStore.NewSession for a child turn at all, so rec.SessionID resolves
// to no UnifiedMeta record for those (SetMeta returns a not-found error) —
// logged at Debug, never escalated, and never blocking the sweep. A nil
// agentLoop (a bare struct-literal test engine) or an unresolvable AgentID
// is handled the same way.
func (pe *PlanEngine) reconcileUnifiedMetaStatus(rec *session.LifecycleRecord) {
	if pe.agentLoop == nil || rec == nil || rec.AgentID == "" {
		return
	}
	sessStore := pe.agentLoop.GetAgentStore(rec.AgentID)
	if sessStore == nil {
		return
	}
	interrupted := session.StatusInterrupted
	if setErr := sessStore.SetMeta(rec.SessionID, session.MetaPatch{Status: &interrupted}); setErr != nil {
		logger.DebugCF("plan_engine",
			"boot sweep: could not reconcile UnifiedMeta status (expected for a delegate/subturn session, "+
				"which has no chat-transcript meta.json at all)",
			map[string]any{"session_id": rec.SessionID, "agent_id": rec.AgentID, "error": setErr.Error()})
	}
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
// idempotently. It is the BOOT half of a correction's apply, and it must reach
// the same durable end state as the LIVE half (PlanEngine.
// buildCorrectionApplyFunc + the supervision bookkeeping AppendCorrection runs
// in the same locked body), because ReplayAtBoot marks the intent DONE the
// moment this returns nil — a record that says "applied" over a plan this
// function only half-corrected is permanent. The four steps, in the live
// path's order:
//
//  1. create each member task (skipping any that already exist — the
//     idempotency guard for a partial re-apply after a crash);
//  2. wire rec.Edges via AddDependency (idempotent: an existing edge returns
//     added=false, nil);
//  3. targeted_retry: reset the named member;
//  4. abandon: terminate the plan (see completeAbandonAtBoot); every other
//     verb: apply the plan-record patch — clear the durable unmet signature so
//     a fresh correction gets a fresh round, set plan_phase, and disarm the
//     supervision wake the plan is leaving behind.
//
// Step 2 exists because the edge list is NOT carried on the member bodies. An
// earlier version of this function skipped it on the claim that "member bodies
// carry their own blocked_by edges, folded in at write time" — pkg/tools/
// plan_correct.go's parseTailMembers builds every tail member with an EMPTY
// BlockedBy and returns the edges separately, so that claim was false and the
// consequence was a replayed correction whose sequenced tail members all came
// back `next`, running concurrently, while the intent log recorded the
// correction as fully applied.
//
// The verb and the retried member id are read off rec.Revision (the intent
// record is self-contained by FR-148 — it carries the whole revision entry),
// which is what makes boot equivalence possible without the live
// CorrectionRequest.
//
// Satisfies the ApplyFunc contract IntentLog.ReplayAtBoot expects (FR-148
// idempotent replay-forward). Boot-only: replayIntentLogs is the sole caller
// and holds no lock, which step 4's abandon branch relies on (it takes
// planDecisionMu itself).
func (pe *PlanEngine) applyIntentRecord(rec plan.IntentRecord) error {
	// (1) Create each member task idempotently. A member that already exists
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
		if clone.PlanID == "" {
			clone.PlanID = rec.PlanID
		}
		if err := pe.taskStore.Create(&clone); err != nil {
			// A collision that isn't "exists" is a real error — surface it so
			// ReplayAtBoot marks the intent not-done and retries next boot.
			return err
		}
	}

	// (2) Wire the tail edges. A failure here is RETURNED, never logged and
	// swallowed: an unwired edge means the tail runs unordered, and the whole
	// point of returning is that ReplayAtBoot then leaves the intent
	// `committed` (not `done`) so the next boot finishes it, instead of
	// stamping "applied" over an unordered plan.
	for _, e := range rec.Edges {
		if e.FromTaskID == "" || e.ToTaskID == "" {
			return fmt.Errorf("intent %q: tail edge with an empty endpoint (%q -> %q)",
				rec.IntentID, e.FromTaskID, e.ToTaskID)
		}
		if _, _, err := pe.taskStore.AddDependency(e.ToTaskID, e.FromTaskID); err != nil {
			return fmt.Errorf("intent %q: wire tail edge %s->%s: %w",
				rec.IntentID, e.FromTaskID, e.ToTaskID, err)
		}
	}

	// (3) targeted_retry resets the specific failed member. Idempotent by way
	// of RestartReset's own contract: it errors on a non-failed task, which on
	// a re-apply is the correct no-op (the member is already `next`), so this
	// is logged rather than returned — mirroring the live apply exactly.
	if rec.Revision.Verb == plan.RevisionTargetedRetry && rec.Revision.RetriedMemberID != "" {
		if _, err := pe.taskStore.RestartReset(rec.Revision.RetriedMemberID); err != nil {
			logger.DebugCF("plan_engine", "intent replay: targeted-retry reset (may be an idempotent no-op)",
				map[string]any{"plan_id": rec.PlanID, "task_id": rec.Revision.RetriedMemberID, "error": err.Error()})
		}
	}

	// (4a) abandon adds no work and does not return the plan to dispatching —
	// it terminates it. Its record carries no phase patch at all, so falling
	// through to the patch below would make the replay a total no-op and leave
	// a plan the durable record says was abandoned parked forever.
	if rec.Revision.Verb == plan.RevisionAbandon {
		return pe.completeAbandonAtBoot(rec)
	}

	// (4b) Apply the plan-record patch, plus the supervision disarm the live
	// path performs immediately after the same commit (countCorrectionAndClear
	// Wake). The plan is leaving the supervision-eligible phase set, so the
	// wake receipt, the wake error and the attempt counter reset (FR-050);
	// without this the replayed plan resumes dispatching with a spent deadline
	// still armed, an attempt ladder already partly burned, and — because
	// ensureSupervisionSessionLocked keys the park boundary off wake_at — a
	// next park that reuses the PREVIOUS park's adjudication session.
	//
	// correction_rounds is deliberately NOT incremented here even though the
	// live path increments it: an increment is not idempotent and this
	// function has no per-revision applied-marker on the plan record to make
	// it so, so a replay that succeeded and then failed to mark the intent
	// done would double-count on the next boot. Nothing gates on the counter
	// (it is attribution only, read to explain a terminal record), so
	// under-counting by the corrections lost in a crash window is strictly
	// safer than fabricating rounds that never happened.
	patch := clearSupervisionWakePatch(plan.Patch{})
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
	return nil
}

// completeAbandonAtBoot finishes a committed-but-not-done `abandon` correction
// (ADR-055/FR-046b): the adjudicator judged the Definition of Done unreachable
// and the revision is durably on the record, so the plan must end
// failed(dod_unreachable) with the falsified assumption in its handover —
// exactly what AppendCorrection does in the live path immediately after the
// same commit. A crash in that window is the only way this record is reached,
// and leaving it a no-op strands the plan parked forever with an abandonment
// on its own record.
//
// Idempotent by state, not by marker: an already-terminal plan is left
// UNTOUCHED. That guard is what stops a re-run from rewriting a plan that
// reached `failed` some other way (a Stop, an idle expiry) into
// dod_unreachable — failed->failed is a legal no-op transition in the plan
// matrix, so the store would happily overwrite the reason and handover.
//
// It takes planDecisionMu because failPlanLocked requires it. That is safe
// only because the sole caller chain (Start -> replayIntentLogs ->
// applyIntentRecord) runs before the tick loop and the event loop are
// launched and holds no plan lock of its own.
func (pe *PlanEngine) completeAbandonAtBoot(rec plan.IntentRecord) error {
	pe.planDecisionMu.Lock()
	defer pe.planDecisionMu.Unlock()

	p, err := pe.planStore.Get(rec.PlanID)
	if err != nil {
		if errors.Is(err, plan.ErrNotFound) {
			// The plan vanished between commit and replay — nothing left to
			// abandon, so the intent is effectively done (same rule the
			// plan-patch path applies).
			return nil
		}
		return fmt.Errorf("intent %q: get plan %q for abandon replay: %w", rec.IntentID, rec.PlanID, err)
	}
	if p.State == plan.StateFailed || p.State == plan.StateDone {
		// Already terminal: the live path completed before the crash (or the
		// plan ended some other way). Never rewrite a terminal record.
		logger.InfoCF("plan_engine", "intent replay: abandon target is already terminal; leaving the record untouched",
			map[string]any{"plan_id": rec.PlanID, "intent_id": rec.IntentID, "state": string(p.State),
				"failed_reason": string(p.FailedReason)})
		return nil
	}

	// The correction is durable, so the F2 round-burn gate must not outlive
	// it. bootReconcile rehydrates the in-memory gate from this same persisted
	// field AFTER replay runs, so clearing the durable field below via
	// failPlanLocked's own write is not enough on its own — clear the
	// in-memory entry too, exactly as the live path does.
	pe.clearUnmetTerminalSignature(rec.PlanID)
	// The handover text comes from buildAbandonHandover — the SAME builder the
	// live path uses — reconstructed from the revision entry rather than
	// re-worded here, so a boot-completed abandon reads identically to a live
	// one. CorrectionRequest is named unqualified deliberately: it is an alias
	// for plan.CorrectionRequest today, and this compiles either way.
	pe.failPlanLocked(rec.PlanID, plan.FailedReasonDoDUnreachable, buildAbandonHandover(p, CorrectionRequest{
		Verb:                rec.Revision.Verb,
		FalsifiedAssumption: rec.Revision.FalsifiedAssumption,
		Reason:              rec.Revision.Reason,
	}))

	// failPlanLocked logs its own store failure and returns nothing, so the
	// transition is VERIFIED rather than assumed: returning nil here is what
	// marks the intent permanently done, and doing that over a plan still
	// sitting at awaiting_supervision is the precise defect this function
	// exists to close.
	after, gerr := pe.planStore.Get(rec.PlanID)
	if gerr != nil {
		return fmt.Errorf("intent %q: verify abandon replay for plan %q: %w", rec.IntentID, rec.PlanID, gerr)
	}
	if after.State != plan.StateFailed {
		return fmt.Errorf("intent %q: abandon replay did not terminate plan %q (state %q, phase %q)",
			rec.IntentID, rec.PlanID, after.State, after.EffectivePlanPhase())
	}
	return nil
}
