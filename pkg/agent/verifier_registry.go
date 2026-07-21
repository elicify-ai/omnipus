// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_registry.go implements the verifier-session registry (ADR-052
// §6.10 "Judge / Verifier architecture", FR-037/R3-7). It replaces
// plan_engine.go's old `inFlightJudge map[string]bool` (a plan-ID-only,
// session-less liveness flag) with a richer mapping from an adjudication
// UNIT — a plan ID, a task ID, or a chat `/goal` session ID — to the
// verifier's own session ID, so the Stop fan-out (ADR §6.4/§6.9, spec US-6/
// US-7) always has a session handle to cancel, and `/goal clear` can cancel
// an in-flight goal verifier the same way (FR-037).
//
// Ownership / wiring: PlanEngine constructs and owns the single process-wide
// instance (NewPlanEngine), and points the package-wide publisher seam
// (verifier_adjudication.go's SetVerifierSessionRegistry) at that SAME
// instance at construction time — NOT via a direct
// GetPlanEngine(al).VerifierRegistry() call from the registration side.
// runVerifierAdjudication (verifier_adjudication.go, FR-011) reaches it
// through currentVerifierSessionRegistry() (the package seam) and registers
// the real verifier session id it creates BEFORE dispatching that session's
// first turn (the same synchronous-assignment rule M1 applies to a member
// task's own SessionID), unregistering once the verdict resolves or the
// adjudication is abandoned. The Stop fan-out (plan_engine.go's
// StopPlan/StopTask) reaches the SAME instance via PlanEngine.VerifierRegistry
// (the pe.registry() accessor). Chat `/goal`'s verifier (goal_loop.go)
// follows the same contract, keyed by its own goal-session id.
//
// Key scheme (ADR-052 FR-037, F1 fix): both the registration side and the
// Stop-fan-out lookup side MUST derive their keys from the SAME helpers —
// verifierUnitForPlan/verifierUnitForTask/verifierUnitForGoal below (which
// verifierUnitID, verifier_adjudication.go, and plan_engine.go's
// StopPlan/StopTask/beginPlanJudgeRound/processPlan all call) — never a raw
// id or an ad hoc prefix construction. A previous version of this wave had
// runVerifierAdjudication registering under "plan:"+id/"task:"+id/"goal:"+id
// while the Stop fan-out enumerated the RAW id, so Stop never found a live
// verifier session at all; these helpers are the single source of truth that
// closes that gap.
//
// PlanEngine's own plan-level judge round (beginPlanJudgeRound /
// runPlanJudgeRound in plan_engine.go) additionally uses Register/Unregister
// on the SAME registry, keyed by planID, as its round-in-flight liveness
// marker — registered with an empty verifierSessionID synchronously BEFORE
// the round's goroutine is launched (mirroring the exact timing the old
// inFlightJudge[p.ID]=true write had), then upserted with the REAL verifier
// session id once judge.go creates it. SessionsFor deliberately omits
// empty-session entries: an adjudication that has been claimed but has not
// yet created its verifier session has nothing live to cancel yet, so a
// Stop landing in that split-second window correctly finds nothing to kill
// for that particular unit (there is no LLM/tool/shell activity in flight to
// interrupt) while still being unable to "miss" a real, already-dispatched
// verifier session, which is the actual guarantee FR-029/FR-037 require.
package agent

import "sync"

// verifierUnitForPlan, verifierUnitForTask, and verifierUnitForGoal are the
// single source of truth for the verifier-session registry's key scheme
// (ADR-052 FR-037). Every caller that registers OR looks up an adjudication
// unit's entry — verifierUnitID (verifier_adjudication.go), and
// plan_engine.go's StopPlan/StopTask/beginPlanJudgeRound/runPlanJudgeRound/
// processPlan — MUST derive its key through these, never by concatenating
// the prefix ad hoc. See this file's package doc for the F1 regression this
// closes.
func verifierUnitForPlan(planID string) string        { return "plan:" + planID }
func verifierUnitForTask(taskID string) string        { return "task:" + taskID }
func verifierUnitForGoal(goalSessionID string) string { return "goal:" + goalSessionID }

// VerifierSessionRegistry maps an adjudication unit (plan ID, task ID, or
// goal-session ID) to the verifier's own session ID for the lifetime of one
// adjudication. It is the exported, minimal dependency surface a verifier
// dispatcher (judge.go's runVerifierAdjudication, goal_loop.go's `/goal`
// verifier path) and the Stop fan-out (PlanEngine.StopPlan/StopTask) both
// depend on — neither needs to know about the other's implementation.
//
// Implementations must be safe for concurrent use.
type VerifierSessionRegistry interface {
	// Register records that verifierSessionID is (or is about to become) the
	// live verifier session adjudicating unit. Callers that have not yet
	// created the verifier session MAY register an empty verifierSessionID
	// as a "claimed" placeholder and Register AGAIN with the real ID once
	// the session exists — Register is an idempotent upsert, not
	// create-once. Whichever call is dispatch-side MUST run BEFORE the
	// verifier's own turn is dispatched (FR-029/FR-037's synchronous-
	// assignment rule) so a concurrent Stop can never race past it.
	Register(unit, verifierSessionID string)

	// Unregister removes unit's entry, if any. Idempotent — unregistering an
	// absent (or already-removed) unit is a no-op.
	Unregister(unit string)

	// Lookup returns the verifier session ID currently registered for unit
	// and whether ANY entry (even an empty-session placeholder) exists.
	Lookup(unit string) (verifierSessionID string, ok bool)

	// SessionsFor returns the distinct, non-empty verifier session IDs
	// currently registered across all of units, in no particular order.
	// Units with no entry, or only an empty-session placeholder entry,
	// contribute nothing. This is the primitive the Stop fan-out uses to
	// gather every relevant verifier session (a plan's own unit plus every
	// one of its member tasks' units) in one call.
	SessionsFor(units ...string) []string
}

// verifierSessionRegistry is the concrete, in-memory VerifierSessionRegistry.
// It is deliberately process-local (not persisted): a verifier session's
// liveness is meaningful only within the process that dispatched it — a
// restart leaves no in-flight verifier turns to resume (mirrors
// inFlightJudge's own prior in-memory-only contract, which the crash-resume
// handling in plan_engine.go's processPlan already relies on).
type verifierSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]string // unit -> verifier session id ("" = claimed, pending)
}

// NewVerifierSessionRegistry constructs an empty VerifierSessionRegistry.
func NewVerifierSessionRegistry() VerifierSessionRegistry {
	return &verifierSessionRegistry{sessions: make(map[string]string)}
}

func (r *verifierSessionRegistry) Register(unit, verifierSessionID string) {
	if unit == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[unit] = verifierSessionID
}

func (r *verifierSessionRegistry) Unregister(unit string) {
	if unit == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, unit)
}

func (r *verifierSessionRegistry) Lookup(unit string) (verifierSessionID string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	verifierSessionID, ok = r.sessions[unit]
	return verifierSessionID, ok
}

func (r *verifierSessionRegistry) SessionsFor(units ...string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(units))
	var out []string
	for _, unit := range units {
		id, ok := r.sessions[unit]
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
