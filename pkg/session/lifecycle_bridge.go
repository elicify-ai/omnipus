// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// lifecycle_bridge.go is the SINGLE MEDIATOR that transitions BOTH session
// stores jointly: the durable LifecycleRecord (the 6-state S2 authority the
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

import (
	"log/slog"
	"time"
)

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

// lifecycleToUnifiedStatus is the CANONICAL mapping from a LifecycleState
// to the UnifiedMeta SessionStatus that mirrors it. It is the single authority
// — no other site in the codebase may hand-roll this mapping. Sub-agent
// control plane ADR D4/MAJ-009: genuine `failed` mirrors to StatusFailed;
// `stopped` (and waiting/working) stay coarse-active, so it joins
// queued/running/needs_input in the no-mirror bucket below — exact helper
// display now lives on Session.lifecycle_state (SessionLifecycleState), not
// on this coarse status. StatusInterrupted is retired from the wire enum and
// is never returned here.
//
//   - LifecycleCompleted → StatusArchived
//   - LifecycleFailed    → StatusFailed
//   - LifecycleStopped/Queued/Running/NeedsInput → (no mirror; chat stays Active)
func lifecycleToUnifiedStatus(to LifecycleState) (SessionStatus, bool) {
	switch to {
	case LifecycleCompleted:
		return StatusArchived, true
	case LifecycleFailed:
		return StatusFailed, true
	default: // LifecycleStopped, LifecycleQueued, LifecycleRunning, LifecycleNeedsInput
		return "", false
	}
}

// LifecycleDisplayState is the domain mirror of the wire
// Session.yaml::lifecycle_state enum (SessionLifecycleState in
// pkg/api/generated) — the "exact helper-state display" referenced by
// lifecycleToUnifiedStatus's own doc comment above. Its five string values
// are chosen to equal the generated wire enum's values byte-for-byte, so a
// plain string cast (gen.SessionLifecycleState(string(d))) at the REST
// boundary is always schema-valid — the same mirroring contract
// TestOwnerScopeKind_MirrorsWireEnum pins for OwnerScopeKind, enforced here
// by TestLifecycleDisplayState_MirrorsWireEnum.
type LifecycleDisplayState string

const (
	LifecycleDisplayWorking          LifecycleDisplayState = "working"
	LifecycleDisplayWaitingForAnswer LifecycleDisplayState = "waiting_for_answer"
	LifecycleDisplayDone             LifecycleDisplayState = "done"
	LifecycleDisplayFailed           LifecycleDisplayState = "failed"
	LifecycleDisplayStopped          LifecycleDisplayState = "stopped"
)

// LifecycleStateToDisplay is the CANONICAL mapping from a LifecycleRecord's
// 6-value LifecycleState to the 5-value Session.yaml::lifecycle_state
// display enum — no other site may hand-roll this collapse (same "single
// authority" rule as lifecycleToUnifiedStatus above). Per Session.yaml's own
// field doc: `queued`/`running` both collapse to `working`, `needs_input`
// maps to `waiting_for_answer`, `completed` maps to `done`, and `failed`/
// `stopped` pass through unchanged.
func LifecycleStateToDisplay(s LifecycleState) LifecycleDisplayState {
	switch s {
	case LifecycleQueued, LifecycleRunning:
		return LifecycleDisplayWorking
	case LifecycleNeedsInput:
		return LifecycleDisplayWaitingForAnswer
	case LifecycleCompleted:
		return LifecycleDisplayDone
	case LifecycleFailed:
		return LifecycleDisplayFailed
	case LifecycleStopped:
		return LifecycleDisplayStopped
	default:
		// Unreached for any record that passed validateLifecycleRecordForPersist
		// (IsValidLifecycleState gates every write), but fall back to the
		// broadest, least-alarming bucket rather than emitting an unknown
		// wire-enum string a Zod-validated SPA response would otherwise reject.
		return LifecycleDisplayWorking
	}
}

// TransitionSession transitions BOTH session stores for sid in one call:
//
//  1. Writes the durable LifecycleRecord to `to` (authoritative), using the
//     store's own atomic Mutate RMW so two concurrent transitions on the same
//     session_id serialize (Correctness-MAJOR-3 / S4 INV-3: cancel-vs-complete
//     race).
//  2. Mirrors completed/failed/stopped onto UnifiedMeta (best-effort), via
//     the canonical lifecycleToUnifiedStatus mapping. Other states skip the
//     mirror (chat stays Active).
//
// Parameters:
//   - ls: the durable lifecycle store (any LifecycleMutator — *LifecycleStore
//     in production, or the delegate's MessageParentLifecycleStore interface).
//     nil is accepted (the lifecycle half is skipped — used by test harnesses
//     and any boot path that has not wired the store yet); the UnifiedMeta
//     mirror still proceeds.
//   - us: the UnifiedStore holding sid's chat-transcript meta. nil means "no
//     chat-transcript meta to mirror" (an unwired caller, or a session that
//     was never minted). Delegate children do have meta — SteerLauncher.Launch
//     mints it — and delegate_run.go::transitionLifecycle passes the tool's
//     UnifiedStore so the mirror runs (issue #947). A nil store skips it.
//   - to: the target LifecycleState.
//   - reason: the FailedReason (set on the record for ANY state, but only
//     REQUIRED — enforced by persistLocked — when to == LifecycleFailed).
//   - note: the StopNote to land when to == LifecycleStopped (D2/D6).
//     Ignored for every other target state. A non-nil note always WINS —
//     pass one whenever this call is itself the stop event (a fresh human
//     Stop, a fresh cascade stamp synthesized by the caller, ...). Pass nil
//     when a prior write (typically steer_cancel.go's stampStop, part of
//     the SAME stop event) already landed the note on this generation —
//     TransitionSession then RETAINS whatever rec.StopNote already holds,
//     matching D2's "landing clears the fence but keeps the note." Seq is
//     always re-stamped from the record's OWN current generation at write
//     time, never taken from note.Seq (see StopNote's own doc comment).
//     persistLocked rejects the write outright if to == LifecycleStopped
//     and neither a passed-in note nor an existing rec.StopNote is present
//     — a loud failure instead of a silently wrong or missing cause.
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
func TransitionSession(ls LifecycleMutator, us *UnifiedStore, sid string, to LifecycleState, reason string, note *StopNote) error {
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
			if to == LifecycleStopped {
				if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
					rec.Stop = nil
				}
				if note != nil {
					stamped := *note
					stamped.Seq = uint64(rec.Generation)
					rec.StopNote = &stamped
				}
				// A restart stop must not move LastActivityAt up to the boot
				// or supersession instant. Other stops end real work.
				if rec.StopNote == nil || rec.StopNote.Cause != StopCauseRestart {
					rec.NoteRealActivity(time.Now().UTC())
				}
				// note == nil: retain whatever rec.StopNote already holds (a
				// prior write in the same stop event already landed it); if
				// nothing ever did, persistLocked's own stopped-requires-note
				// invariant rejects this write rather than stranding the
				// record silently mislabeled.
			}
			return nil
		})
		// lifecycleErr is returned to the caller (who logs it appropriately),
		// but it does NOT gate the UnifiedMeta mirror below — see the doc
		// comment.
	}

	// 2. UnifiedMeta mirror (best-effort, completed/failed/stopped).
	if us == nil {
		return lifecycleErr
	}
	mapped, ok := lifecycleToUnifiedStatus(to)
	if !ok {
		// No mirrored status: chat stays Active — no SetMeta needed.
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
