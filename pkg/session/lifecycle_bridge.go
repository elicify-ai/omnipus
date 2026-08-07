// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// lifecycle_bridge.go is the SINGLE MEDIATOR that transitions BOTH session
// stores jointly: the durable LifecycleRecord (the 8-state S2 authority the
// boot sweep reconciles) first, then the UnifiedMeta (the 3-status
// chat-transcript metadata GET /api/v1/sessions and the SPA actually read) as
// a best-effort mirror.
//
// WHY THIS EXISTS (Defect #28 / issue #28 — dual-store divergence). Before this
// mediator, the two independently-persisted stores were paired BY HAND at
// scattered call sites (task_executor.go, delegate.go, cancel.go), and the
// cancel path wrote ONLY UnifiedMeta — orphaning the parent session's
// LifecycleRecord (it stayed running/queued on disk until a future boot sweep
// caught it). That is the same defect as a kill -9 crash, produced by NORMAL
// cancel operation, not just crashes. This mediator is the one place that owns
// the lifecycle-state → unified-status mapping table, so a future writer can
// never pair them incorrectly again: every transition funnels through here.
package session

import "log/slog"

// LifecycleMutator is the minimal lifecycle-store surface TransitionSession
// needs: just the atomic Mutate RMW. *LifecycleStore satisfies it directly,
// and so does the delegate tool's MessageParentLifecycleStore interface
// (pkg/tools/message_parent.go) — which is how the delegate path (whose
// lifecycle field is that interface, not the concrete pointer) reaches the
// mediator without a fragile type assertion. Any test double implementing the
// same Mutate contract also satisfies it.
type LifecycleMutator interface {
	Mutate(sessionID string, fn func(*LifecycleRecord) error) error
}

// lifecycleMutatorIsNil reports whether ls is a nil interface OR a nil concrete
// *LifecycleStore wrapped in the interface (Go's interface-nil pitfall: a nil
// pointer stored in an interface value is NOT == nil). *LifecycleStore is the
// only production type that implements LifecycleMutator, so a type assertion to
// it covers every real case without resorting to reflect.
func lifecycleMutatorIsNil(ls LifecycleMutator) bool {
	if ls == nil {
		return true
	}
	if ptr, ok := ls.(*LifecycleStore); ok && ptr == nil {
		return true
	}
	return false
}

// lifecycleToUnifiedStatus is the CANONICAL mapping from a terminal
// LifecycleState to the UnifiedMeta SessionStatus that mirrors it. It is the
// single authority — no other site in the codebase may hand-roll this mapping.
// Returns (zero, false) for non-terminal lifecycle states: a non-terminal
// transition must NOT touch UnifiedMeta (the chat session stays Active).
//
//   - LifecycleCompleted  → StatusArchived
//   - LifecycleFailed     → StatusInterrupted
//   - LifecycleCancelled  → StatusInterrupted
//   - LifecycleTimedOut   → StatusInterrupted
//   - Queued/Running/NeedsInput/Paused → (no mirror; chat stays Active)
func lifecycleToUnifiedStatus(to LifecycleState) (SessionStatus, bool) {
	switch to {
	case LifecycleCompleted:
		return StatusArchived, true
	case LifecycleFailed, LifecycleCancelled, LifecycleTimedOut:
		return StatusInterrupted, true
	default: // LifecycleQueued, LifecycleRunning, LifecycleNeedsInput, LifecyclePaused
		return "", false
	}
}

// TransitionSession transitions BOTH session stores for sid in one call:
//
//  1. Writes the durable LifecycleRecord to `to` (authoritative), using the
//     store's own atomic Mutate RMW so two concurrent transitions on the same
//     session_id serialize (Correctness-MAJOR-3 / S4 INV-3: cancel-vs-complete
//     race).
//  2. Mirrors the transition onto UnifiedMeta (best-effort) for terminal
//     states, via the canonical lifecycleToUnifiedStatus mapping. Non-terminal
//     states skip the mirror (chat stays Active).
//
// Parameters:
//   - ls: the durable lifecycle store (any LifecycleMutator — *LifecycleStore
//     in production, or the delegate's MessageParentLifecycleStore interface).
//     nil is accepted (the lifecycle half is skipped — used by test harnesses
//     and any boot path that has not wired the store yet); the UnifiedMeta
//     mirror still proceeds.
//   - us: the UnifiedStore holding sid's chat-transcript meta. nil means "no
//     chat-transcript meta" (the delegate/subturn case — those sessions are
//     never created via UnifiedStore.NewSession, so there is no meta.json to
//     mirror onto). The delegate call sites pass nil.
//   - to: the target LifecycleState.
//   - reason: the FailedReason (set on the record for ANY state, but only
//     REQUIRED — enforced by persistLocked — when to == LifecycleFailed).
//
// Return value: the error from the LifecycleRecord Mutate, if any (including
// ErrLifecycleNotFound when no record exists for sid, and
// ErrLifecycleTerminalImmutable when the tail is already terminal on the same
// generation). The UnifiedMeta mirror is ALWAYS attempted regardless of the
// lifecycle outcome — a missing/terminal lifecycle record must not prevent the
// user-visible store from following — and its failure is logged at Warn here
// (never rolled back; the durable record is the authority and the boot sweep
// reconciles). Callers that treat ErrLifecycleNotFound as expected (e.g. a
// chat session with no lifecycle record) should silence it with errors.Is.
func TransitionSession(ls LifecycleMutator, us *UnifiedStore, sid string, to LifecycleState, reason string) error {
	// 1. LifecycleRecord (authoritative).
	//
	// lifecycleMutatorIsNil guards against BOTH a nil interface and a nil
	// concrete *LifecycleStore wrapped in the interface (Go's interface-nil
	// pitfall: a nil pointer stored in an interface value is NOT == nil).
	// Callers like AgentLoop.GetSessionLifecycleStore return *LifecycleStore;
	// when nothing is wired that is a nil concrete pointer, and passing it
	// straight through would panic inside Mutate's s.lock.Get(...).
	var lifecycleErr error
	if !lifecycleMutatorIsNil(ls) && sid != "" {
		lifecycleErr = ls.Mutate(sid, func(rec *LifecycleRecord) error {
			if rec == nil {
				return ErrLifecycleNotFound
			}
			rec.State = to
			rec.FailedReason = reason
			if to != LifecycleNeedsInput {
				rec.NeedsInput = nil
			}
			return nil
		})
		// lifecycleErr is returned to the caller (who logs it appropriately),
		// but it does NOT gate the UnifiedMeta mirror below — see the doc
		// comment.
	}

	// 2. UnifiedMeta mirror (best-effort, terminal states only).
	if us == nil {
		return lifecycleErr
	}
	mapped, ok := lifecycleToUnifiedStatus(to)
	if !ok {
		// Non-terminal state: chat stays Active — no SetMeta needed.
		return lifecycleErr
	}
	if err := us.SetMeta(sid, MetaPatch{Status: &mapped}); err != nil {
		// Log at Warn; do NOT roll back the lifecycle write — the durable
		// record is the authority and the boot sweep reconciles.
		slog.Warn("session: TransitionSession: could not mirror status onto UnifiedMeta",
			"session_id", sid,
			"lifecycle_state", string(to),
			"target_status", string(mapped),
			"error", err)
	}
	return lifecycleErr
}
