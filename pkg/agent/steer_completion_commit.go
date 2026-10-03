// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_completion_commit.go is D2 CRIT-001's one outcome/publication commit
// boundary (ADR-20260928 sub-agent control plane, asset cd20cf8b) and its
// publisher half:
//
//   - commitSteeredCompletion performs the single LifecycleStore.Mutate that
//     checks the producing generation, terminal state and current-generation
//     Stop fence UNDER THE SAME LOCK and, on success, commits the terminal
//     done/failed state AND the protected, unpublished final-delivery outbox
//     tuple (session.FinalDeliveryCommit) together. A current-generation Stop
//     fence that committed first — or a record that has already landed
//     stopped — refuses the final: no outbox entry, no upward inbox
//     message/frame/wake; the record lands stopped with its lasting note.
//   - publishCommittedFinal publishes ONLY a committed outbox entry: it
//     appends the exact committed message to the direct parent's inbox
//     (deterministic replay id), then records the durable delivery facts
//     through LifecycleStore.UpdateFinalDelivery — never a terminal mutation.
//     A publish failure leaves the committed outbox entry pending and
//     retryable (boot/periodic retry consumes
//     LifecycleStore.ListPendingFinalDeliveries), visibly errored, never
//     silently dropped.
package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// steeredCommitKind discriminates what commitSteeredCompletion decided.
type steeredCommitKind uint8

const (
	// steeredCommitRefused — the record vanished, moved generation, or is
	// already terminal. res.terminalNow carries the reloaded record's
	// terminality so the finishing disposal can decide.
	steeredCommitRefused steeredCommitKind = iota
	// steeredCommitTerminal — done/failed AND the protected outbox tuple
	// committed in one mutation; publication is pending.
	steeredCommitTerminal
	// steeredCommitStopped — the stop won: the record landed (or had already
	// landed) LifecycleStopped with its lasting note. No outbox entry, no
	// publication.
	steeredCommitStopped
	// steeredCommitNotice — a non-terminal lifecycle notice (the
	// tool-iteration limit): nothing to commit; the notice is delivered
	// directly, exactly as before this boundary existed.
	steeredCommitNotice
)

// steeredCommitResult carries the commit's decision and, for the terminal
// kind, the exact committed publication payload.
type steeredCommitResult struct {
	kind steeredCommitKind
	// terminalNow is set only for steeredCommitRefused: the record's
	// terminality as reloaded after the refusal.
	terminalNow bool
	// landedStop is true when THIS mutation performed the stopped landing
	// (fresh fence carried out, or a fence-less stop disposition) — as
	// opposed to finding the stop already landed. A fresh landing keeps the
	// legacy interrupted/timeout upward event until the D6 stopped-child
	// notice replaces it; an already-landed stop publishes nothing (the
	// losing completion T11 pins).
	landedStop bool
	commit     *session.FinalDeliveryCommit
	message    generated.SessionMessage
	messageID  string
}

// errCompleteNoPublishableOutcome refuses a terminal commit whose outcome
// has no upward message variant — a visible misuse, never a silent skip.
var errCompleteNoPublishableOutcome = errors.New("steer: complete: terminal commit with no deliverable outcome message")

// commitSteeredCompletion is the linearization point D2 CRIT-001 names. The
// caller MUST NOT hold the per-session lifecycle lock (Mutate takes it).
func (al *AgentLoop) commitSteeredCompletion(
	lifecycle *session.LifecycleStore,
	rec *session.LifecycleRecord,
	nextState session.LifecycleState,
	outcome steer.Outcome,
	answer, failureReason string,
) (steeredCommitResult, error) {
	if completeStateWriteTestHook != nil {
		completeStateWriteTestHook(rec.SessionID)
	}
	res := steeredCommitResult{}

	// The exact upward message, its deterministic replay id and its payload
	// hash are computed BEFORE the commit: they are part of the protected
	// tuple the single mutation stores, so the published bytes can never
	// drift from the committed ones.
	publishable := nextState == session.LifecycleCompleted || nextState == session.LifecycleFailed
	var payload []byte
	var payloadHash string
	var message generated.SessionMessage
	var messageID string
	// CommitID is the producing run's execution identity. Until the D2
	// round-4 R4-MAJ-001 execution-identity seam lands (admission-persisted
	// run_id + boot epoch), each completion flight supplies a correlation id
	// here; it stands in for run_id ONLY — no boot_seq is invented.
	commitID := uuid.NewString()
	if publishable {
		if !isTerminalOutcome(outcome) {
			return res, fmt.Errorf("%w: state %q outcome %q", errCompleteNoPublishableOutcome, nextState, outcome)
		}
		built, err := al.completionMessage(rec, outcome, answer, failureReason)
		if err != nil {
			return res, err
		}
		messageID = fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
		stamped, err := withDeterministicMessageID(built, messageID)
		if err != nil {
			return res, err
		}
		raw, err := stamped.MarshalJSON()
		if err != nil {
			return res, fmt.Errorf("steer: complete: encode committed final %q: %w", messageID, err)
		}
		message = stamped
		payload = raw
		payloadHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	}

	mutateErr := lifecycle.Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			res.kind = steeredCommitRefused
			return fmt.Errorf("steer: complete: record %q vanished during delivery", rec.SessionID)
		}
		if cur.Generation != rec.Generation {
			res.kind = steeredCommitRefused
			return errCompleteStaleGeneration
		}
		if cur.Terminal() {
			res.kind = steeredCommitRefused
			return errCompleteAlreadyTerminal
		}
		// D2 CRIT-001: a current-generation Stop fence that committed before
		// this completion — or a record that has already LANDED stopped —
		// owns this record. The final is refused outright: no outbox entry,
		// no parent inbox message, no frame, no wake.
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			if nextState == session.LifecycleStopped {
				// The fence is the REASON this completion runs: the cancelled
				// turn is carrying the stop out, so this completion lands it
				// (fence spent, lasting note kept) — the refuser here would
				// strand a running+fenced record with no live turn left to
				// land it.
				res.kind = steeredCommitStopped
				res.landedStop = true
				return landSteeredStopLocked(cur, outcome)
			}
			// A terminal/notice disposition racing a fresh fence: refuse. The
			// stop path owns the landing — its never-ran finalizer or the
			// cancelled turn's own completion lands stopped; this write must
			// not consume the stop out from under it (Finding D's rule,
			// pinned by steer_completion_race_test.go).
			res.kind = steeredCommitRefused
			return errCompleteStoppedDuringDelivery
		}
		if cur.State == session.LifecycleStopped {
			// The stop already landed (fence spent, note kept): a terminal
			// disposition arriving afterwards is the losing completion T11
			// pins. Refuse without writing — the stopped record stands.
			res.kind = steeredCommitStopped
			return nil
		}
		if nextState == session.LifecycleStopped {
			// A stop disposition with no fence: a legacy RequestCancel path or
			// a lifetime-budget expiry. Same landing, synthesized note.
			res.kind = steeredCommitStopped
			res.landedStop = true
			return landSteeredStopLocked(cur, outcome)
		}
		if nextState == session.LifecycleRunning {
			// Non-terminal lifecycle notice: nothing to commit, the notice is
			// delivered directly (a fenced record never reaches here — the
			// stop owns it and the notice is refused with it).
			res.kind = steeredCommitNotice
			return nil
		}
		if !publishable {
			res.kind = steeredCommitRefused
			return errCompleteNoPublishableOutcome
		}
		// The winning commit: terminal state AND the protected outbox tuple,
		// one mutation. publishLocked rejects any later same-generation
		// terminal write, so the tuple is immutable once committed.
		cur.State = nextState
		cur.NeedsInput = nil
		if nextState == session.LifecycleFailed {
			cur.FailedReason = failureReason
		}
		cur.FinalDelivery = &session.FinalDeliveryCommit{
			Generation:      cur.Generation,
			CommitID:        commitID,
			MessageID:       messageID,
			Outcome:         string(outcome),
			ParentSessionID: steerParentSessionID(rec),
			PayloadHash:     payloadHash,
			Payload:         payload,
		}
		res.kind = steeredCommitTerminal
		res.commit = cur.FinalDelivery
		res.message = message
		res.messageID = messageID
		return nil
	})
	if mutateErr != nil {
		if res.kind == steeredCommitRefused {
			// The sentinel refusals are legitimate race outcomes, not errors:
			// reload and report the record's terminality so the finishing
			// disposal can decide, exactly as the pre-boundary writer did.
			current, loadErr := lifecycle.Load(rec.SessionID)
			if loadErr != nil {
				return res, fmt.Errorf("steer: complete: reload %q: %w", rec.SessionID, loadErr)
			}
			res.terminalNow = current.Terminal()
			return res, nil
		}
		return res, fmt.Errorf("steer: complete: persist %q: %w", rec.SessionID, mutateErr)
	}
	return res, nil
}

// landSteeredStopLocked lands LifecycleStopped on a non-terminal record the
// stop path owns: fence spent (a current-generation marker is cleared), the
// lasting note kept or synthesized. persistLocked enforces the paired
// invariants (stopped requires a note and forbids a current fence).
func landSteeredStopLocked(cur *session.LifecycleRecord, outcome steer.Outcome) error {
	cur.State = session.LifecycleStopped
	cur.NeedsInput = nil
	if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
		cur.Stop = nil
	}
	if cur.StopNote == nil {
		cause := session.StopCauseStop
		if outcome == steer.OutcomeTimedOut {
			cause = session.StopCauseTimeout
		}
		cur.StopNote = &session.StopNote{
			At: time.Now().UTC(), By: session.StopActorSystem,
			Seq: uint64(cur.Generation), Cause: cause,
		}
	}
	return nil
}

// publishCommittedFinal publishes one committed outbox entry and records
// its durable delivery facts. The returned error is the caller's visible
// signal that the committed final is still pending (the outbox entry stays
// retryable); it is never an acknowledgment.
func (al *AgentLoop) publishCommittedFinal(ctx context.Context, rec *session.LifecycleRecord, res steeredCommitResult) (bool, error) {
	if res.kind != steeredCommitTerminal || res.commit == nil {
		return false, nil
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		return false, errSteerUpwardDelivererNotWired
	}
	event := steer.UpwardEvent{
		ChildSessionID: rec.SessionID,
		Generation:     rec.Generation,
		Outcome:        steer.Outcome(res.commit.Outcome),
		Message:        res.message,
	}
	delivery, err := deliverer.Deliver(ctx, event)
	if err != nil {
		return false, fmt.Errorf("steer: complete: publish committed final %q: %w", res.messageID, err)
	}
	reportUndeliveredWake("steer: complete", event, steerParentSessionID(rec), rec.Generation, delivery)
	woke := deliveryWokeRecipient(delivery.Outcome)

	facts := session.FinalDeliveryProgress{InboxAppended: true, WakeRecorded: woke}
	if !woke {
		// A stored-not-woken delivery may already be genuinely acknowledged
		// (Deliver's dedup short-circuit). Acknowledgement is a durable fact
		// of the inbox; record it as one when the Drain-based check proves it.
		if acked, ackErr := al.finalDeliveryAckObserved(rec, res.messageID); ackErr != nil {
			return woke, errors.Join(fmt.Errorf("steer: complete: inspect committed final acknowledgement %q: %w", res.messageID, ackErr))
		} else if acked {
			facts.AckObserved = true
		}
	}
	if err := al.recordFinalDeliveryProgress(rec, res.commit.CommitID, facts); err != nil {
		return woke, errors.Join(fmt.Errorf("steer: complete: record delivery progress %q: %w", res.messageID, err))
	}
	return woke, nil
}

// finalDeliveryAckObserved reports whether the committed final's inbox id is
// durably acknowledged under the record's steering edge.
func (al *AgentLoop) finalDeliveryAckObserved(rec *session.LifecycleRecord, messageID string) (bool, error) {
	inbox := al.GetMessageInboxStore()
	ownerKey := deliverOwnerKey(rec)
	if inbox == nil || ownerKey == "" {
		return false, nil
	}
	return deliverEntryIsAcked(inbox, ownerKey, rec.SessionID, messageID)
}

// recordFinalDeliveryProgress records durable delivery facts through the one
// legal post-terminal writer (LifecycleStore.UpdateFinalDelivery). Facts are
// monotonic merges, so a concurrent publisher's CAS conflict is retried from
// fresh state without losing progress; a retired final needs nothing.
func (al *AgentLoop) recordFinalDeliveryProgress(rec *session.LifecycleRecord, commitID string, facts session.FinalDeliveryProgress) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return errors.New("steer: complete: lifecycle store is not wired")
	}
	for attempt := 0; attempt < 2; attempt++ {
		state, revision, retired, err := lifecycle.FinalDeliveryState(rec.SessionID, rec.Generation, commitID)
		if err != nil {
			return err
		}
		if retired {
			return nil
		}
		merged := state.Merge(facts)
		if merged.Equal(state) {
			return nil
		}
		err = lifecycle.UpdateFinalDelivery(rec.SessionID, rec.Generation, commitID, revision, session.FinalDeliveryCommand{Advance: &merged})
		if errors.Is(err, session.ErrFinalDeliveryRevisionConflict) {
			continue
		}
		return err
	}
	return fmt.Errorf("steer: complete: delivery progress %q: revision conflict persisted across retries", replayIDFor(rec))
}

// replayIDFor is a diagnostic-only formatter for the retry-exhausted error
// above (the commit's deterministic replay id).
func replayIDFor(rec *session.LifecycleRecord) string {
	return fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
}

// logCommittedFinalPublishFailure is the ONE visible-error surface for a
// publish failure on a caller that cannot propagate (the terminal-report
// path): the committed outbox entry stays pending for boot/periodic retry,
// and the log names the full identity — never a silent drop.
func logCommittedFinalPublishFailure(op string, sessionID string, generation int, res steeredCommitResult, err error) {
	logger.ErrorCF("agent", op+": committed final publication failed — the outbox entry stays pending for retry",
		map[string]any{
			"session_id": sessionID,
			"generation": generation,
			"message_id": res.messageID,
			"commit_id":  res.commit.CommitID,
			"error":      err.Error(),
		})
}
