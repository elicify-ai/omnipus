// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// boot_final_delivery.go is ADR-20260928 (sub-agent control plane, frozen
// asset cd20cf8b) D8.1's boot pass two: the DELIVERY-ONLY retry of every
// pending committed final, across ALL generations, even when the session's
// current tail is a newer working G+1 (D2 "Discovery after a
// newer-generation RESUME", D8.5/D8.9, T11's W3a slice).
//
// The pass publishes ONLY committed outbox tuples (the protected
// FinalDeliveryCommit one outcome/publication commit wrote — see
// steer_completion_commit.go::commitSteeredCompletion). It never mints a
// final, never promotes a lifecycle record from an inbox final, never
// writes a LifecycleRecord behind a newer generation, and never touches a
// record's state, generation or execution identity: the only writer it
// uses is the delivery-only LifecycleStore.UpdateFinalDelivery, and the
// only discovery read is LifecycleStore.ListPendingFinalDeliveries. The
// automatic boot stop of an uncommitted current run (D8.1 pass one /
// D8.3 stopped(restart)) is a separate pass — W3b's — that must stay
// independent of this one so QA can snapshot G+1 across ONLY delivery.
package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// maxFinalDeliveryCASAttempts bounds the UpdateFinalDelivery CAS retry: a
// conflicting concurrent publisher is retried from fresh joined state
// (progress merges are monotonic, so nothing is lost), twice, then the
// failure is surfaced — never an abandoned silent write.
const maxFinalDeliveryCASAttempts = 2

// runFinalDeliveryPass retries publication of every pending committed final
// (D8.1 pass two). Run calls it after its per-record reconciliation, in D8
// order; it is also the production-used seam a delivery-only before/after
// snapshot calls directly, because it is the whole delivery half of boot —
// nothing here stops a session, starts a turn, or writes an identity.
//
// A scan-level consistency error (an orphan or corrupt delivery envelope
// fails ListPendingFinalDeliveries for the whole store, D2) is returned to
// the caller; per-item failures are surfaced through the operator notice
// and leave the item pending and retryable — never silently dropped, never
// acknowledged.
func (r *SteerBootRecovery) runFinalDeliveryPass(ctx context.Context) error {
	if r == nil || r.Lifecycle == nil {
		return errors.New("agent: steer boot final delivery: lifecycle store must be configured")
	}
	noticed := make(map[string]bool)
	notice := func(key, message string) {
		if noticed[key] {
			return
		}
		noticed[key] = true
		if r.OperatorNotice != nil {
			r.OperatorNotice(message)
		}
	}
	pending, err := r.Lifecycle.ListPendingFinalDeliveries()
	if err != nil {
		return fmt.Errorf("agent: steer boot final delivery: scan pending committed finals: %w", err)
	}
	for _, item := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !item.Pending() {
			// Retired, or its durable facts already show it delivered — the
			// replay identity is the duplicate guard; nothing to retry.
			continue
		}
		r.deliverPendingFinal(ctx, item, notice)
	}
	return nil
}

// deliverPendingFinal delivers ONE committed final to its committed direct
// parent and records the durable delivery facts. Every refusal is visible
// and leaves the item pending/retryable; nothing is ever acknowledged or
// marked persisted on an attempt that did not durably happen (D2).
func (r *SteerBootRecovery) deliverPendingFinal(ctx context.Context, item session.PendingFinalDelivery, notice func(key, message string)) {
	commit := item.Commit
	replayID := commit.ReplayID(item.SessionID)
	if commit.MessageID != replayID {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s generation %d commit %q carries message_id %q, want the deterministic replay id %q — visible inconsistency, publication refused (D2)",
			item.SessionID, item.Generation, commit.CommitID, commit.MessageID, replayID))
		return
	}
	if commit.ParentSessionID == "" {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s generation %d commit %q carries no parent_session_id — visible inconsistency, publication refused (D2)",
			item.SessionID, item.Generation, commit.CommitID))
		return
	}
	if len(commit.Payload) == 0 {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s generation %d commit %q is pending but carries no payload bytes — visible inconsistency, publication refused (D2)",
			item.SessionID, item.Generation, commit.CommitID))
		return
	}
	// Payload integrity BEFORE anything else touches the bytes (D2:
	// payload_hash identifies the exact stored upward envelope). A commit
	// whose stored payload no longer hashes to its protected payload_hash is
	// a visible consistency error that refuses publication before any
	// deliverer call, inbox lookup or append — the commit stays pending, and
	// no payload is ever rewritten or hash back-filled to force a match.
	if commit.PayloadHash == "" {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s (generation %d commit %q) is pending but carries no protected payload_hash — visible inconsistency, publication refused (D2)",
			item.SessionID, replayID, item.Generation, commit.CommitID))
		return
	}
	if computed := fmt.Sprintf("%x", sha256.Sum256(commit.Payload)); computed != commit.PayloadHash {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s (generation %d commit %q) payload hash %s does not match the protected payload_hash %s — visible consistency error, publication refused before any deliverer call or inbox append; the commit stays pending (D2)",
			item.SessionID, replayID, item.Generation, commit.CommitID, computed, commit.PayloadHash))
		return
	}
	var message generated.SessionMessage
	if err := message.UnmarshalJSON(commit.Payload); err != nil {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s generation %d commit %q payload does not decode: %v — publication refused, the commit stays pending (D2)",
			item.SessionID, item.Generation, commit.CommitID, err))
		return
	}

	sighting, err := r.inboxFinalSighting(commit.ParentSessionID, replayID, commit.Payload)
	if err != nil {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s could not be reconciled against parent %s inbox: %v — retry next boot (D2)",
			item.SessionID, replayID, commit.ParentSessionID, err))
		return
	}
	// A same-id inbox entry with different bytes is a visible consistency
	// error: never an acknowledgment, never an append, never a suppression (D2).
	if sighting.appended && sighting.bytesDiverge {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s collides with a parent %s inbox entry of the same id and different bytes — visible consistency error, publication refused (D2)",
			item.SessionID, replayID, commit.ParentSessionID))
		return
	}
	if sighting.acked {
		if !sighting.appended {
			// A STRAY acknowledgement: an ack entry consumed an id the
			// parent's inbox never held (MessageInboxStore.AckDetailed
			// durably records unknown-id acks and reports them Unknown).
			// D2: a genuinely acknowledged matching id consumes the commit
			// only when the id was really delivered — an ack with no
			// matching appended message is a visible inconsistency that
			// consumes nothing: no ack_observed fact is minted, nothing is
			// published or suppressed silently, and the outbox entry stays
			// pending/retryable behind this refusal.
			notice("final-delivery:"+item.SessionID, fmt.Sprintf(
				"session %s committed final %s carries a stray acknowledgement in parent %s inbox with no matching appended message — visible inconsistency, the commit stays pending/retryable and is never consumed by it (D2)",
				item.SessionID, replayID, commit.ParentSessionID))
			return
		}
		// A genuinely acknowledged matching id means this committed result
		// was already consumed: record the receipt, never send a second
		// final, never re-wake (D2).
		r.recordFinalDeliveryFacts(item, session.FinalDeliveryProgress{AckObserved: true}, notice)
		return
	}
	if !sighting.appended && r.compactedAway(item, sighting.ackedMessages) {
		// POSITIVE evidence this final was consumed and then compacted out of
		// the parent's inbox (#1027, ADR-091 "each upward event starts at most
		// one parent turn"): the durable facts say it was appended AND the
		// parent was woken, and the inbox file still shows compaction's
		// footprint — as many acked messages as compaction always leaves. A
		// missing/lost file, a never-woken append, a torn line or a thinned
		// inbox show none of that and fall through to the normal redelivery
		// (the replay id dedups it). Say so out loud, never silently.
		//
		// RESIDUAL (accepted by the founder, no tombstone): an inbox restored
		// from a backup, or one with a torn line, that happens to keep a full
		// retention cap of acked messages while the final's entry is gone,
		// reads as "compacted away" and the final is not redelivered. The
		// operator notice below is the safety net — it names the session, the
		// parent inbox and the counts so an operator can spot and re-send it.
		notice("final-delivery-consumed:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s is no longer in parent %s inbox (%d entries, %d acked messages retained, retention cap %d) but its durable facts show it appended and the parent woken — treated as consumed and compacted away, not redelivered (#1027)",
			item.SessionID, replayID, commit.ParentSessionID, sighting.entries, sighting.ackedMessages, r.Inbox.AckedRetention()))
		r.recordFinalDeliveryFacts(item, session.FinalDeliveryProgress{AckObserved: true}, notice)
		return
	}
	if !sighting.appended {
		event := steer.UpwardEvent{
			ChildSessionID: item.SessionID,
			Generation:     item.Generation,
			Outcome:        steer.Outcome(commit.Outcome),
			Message:        message,
		}
		delivery, deliverErr := r.deliverCommittedFinal(ctx, event, item, notice)
		if deliverErr != nil {
			// The append/wake did not durably happen: record NOTHING — the
			// commit stays pending and retryable, visibly errored (D2).
			notice("final-delivery:"+replayID, fmt.Sprintf(
				"session %s committed final %s not delivered to parent %s: %v — the outbox entry stays pending for retry (D2)",
				item.SessionID, replayID, commit.ParentSessionID, deliverErr))
			return
		}
		r.reportAndRecordDelivery(ctx, event, item, delivery, notice)
		return
	}
	// Already appended (same id, same bytes) but unacknowledged: retry the
	// downstream effects — the wake — without a second append. Deliver's own
	// append dedups the id (message_id is unique per owner key).
	event := steer.UpwardEvent{
		ChildSessionID: item.SessionID,
		Generation:     item.Generation,
		Outcome:        steer.Outcome(commit.Outcome),
		Message:        message,
	}
	delivery, deliverErr := r.deliverCommittedFinal(ctx, event, item, notice)
	if deliverErr != nil {
		notice("final-delivery:"+replayID, fmt.Sprintf(
			"session %s committed final %s wake retry to parent %s failed: %v — facts unchanged, the outbox entry stays pending (D2)",
			item.SessionID, replayID, commit.ParentSessionID, deliverErr))
		return
	}
	r.reportAndRecordDelivery(ctx, event, item, delivery, notice)
}

// deliverCommittedFinal delivers ONE committed final through the deliverer,
// guarding the unwired deliverer visibly (a boot without a deliverer can
// still record nothing — it must not claim delivery). When the deliverer
// provides the RESTRICTED committed-outbox path (the production
// SteerUpwardDeliverer does), the publish is authorized by the store-verified
// protected commit — the exact payload hash, message id, parent and outcome
// must match — so a historical generation publishes through the real
// deliverer even when an explicit RESUME has moved the tail to G+1. The
// public Deliver call stays for a deliverer without the restricted path
// (the boot suite's recording seam) and keeps its generation guard for every
// ordinary uncommitted event; a guessed Generation or arbitrary inbox id can
// never bypass either way.
func (r *SteerBootRecovery) deliverCommittedFinal(ctx context.Context, event steer.UpwardEvent, item session.PendingFinalDelivery, notice func(key, message string)) (steer.Delivery, error) {
	if r.Deliverer == nil {
		notice("final-delivery:"+item.SessionID, fmt.Sprintf(
			"session %s committed final %s not delivered: upward deliverer is not configured — the outbox entry stays pending (D2)",
			item.SessionID, item.Commit.MessageID))
		return steer.Delivery{}, errors.New("agent: steer boot final delivery: upward deliverer is not configured")
	}
	if publisher, ok := r.Deliverer.(committedOutboxPublisher); ok {
		return publisher.deliverCommittedOutboxFinal(ctx, steerCommittedFinalRef{
			SessionID:       item.SessionID,
			Generation:      item.Generation,
			CommitID:        item.Commit.CommitID,
			MessageID:       item.Commit.MessageID,
			ParentSessionID: item.Commit.ParentSessionID,
			Outcome:         item.Commit.Outcome,
			PayloadHash:     item.Commit.PayloadHash,
		})
	}
	return r.Deliverer.Deliver(ctx, event)
}

// reportAndRecordDelivery reports a stored-not-woken delivery with the same
// last-resort posture every other Deliver call site uses (boot IS the
// last-resort re-nudge), records the ack receipt when the dedup short-circuit
// already consumed the id, and records the durable delivery facts.
func (r *SteerBootRecovery) reportAndRecordDelivery(ctx context.Context, event steer.UpwardEvent, item session.PendingFinalDelivery, delivery steer.Delivery, notice func(key, message string)) {
	reportUndeliveredWake("steer: boot final delivery", event, item.Commit.ParentSessionID, item.Generation, delivery)
	woke := deliveryWokeRecipient(delivery.Outcome)
	facts := session.FinalDeliveryProgress{InboxAppended: true, WakeRecorded: woke}
	if !woke {
		sighting, err := r.inboxFinalSighting(item.Commit.ParentSessionID, item.Commit.MessageID, item.Commit.Payload)
		if err != nil {
			notice("final-delivery:"+item.SessionID, fmt.Sprintf(
				"session %s committed final %s acknowledgement could not be inspected: %v — recorded facts stay at the observed wake only (D2)",
				item.SessionID, item.Commit.MessageID, err))
		} else if sighting.acked {
			facts.AckObserved = true
		}
	}
	r.recordFinalDeliveryFacts(item, facts, notice)
}

// compactedAway reports whether item's absence from the parent's inbox is
// explained by compaction: appended and woken per the durable facts, and the
// inbox still holds at least the retention cap of acked messages (compaction
// purges only acked messages and always keeps the newest retention-cap many).
func (r *SteerBootRecovery) compactedAway(item session.PendingFinalDelivery, ackedMessages int) bool {
	retention := r.Inbox.AckedRetention()
	return item.Progress.InboxAppended && item.Progress.WakeRecorded && retention > 0 && ackedMessages >= retention
}

// inboxFinalSighting reports how the committed final's replay id exists in
// the direct parent's durable inbox: appended (a message entry with that
// id), bytesDiverge (the stored entry's bytes differ from the committed
// payload), and acked (an ack entry consumed the id). Built on
// MessageInboxStore.Entries' documented whole-file read — no new pkg/session
// surface — so an acknowledged entry (invisible to Drain) is still seen.
func (r *SteerBootRecovery) inboxFinalSighting(ownerKey, messageID string, payload []byte) (sighting struct {
	appended     bool
	bytesDiverge bool
	acked        bool
	// entries and ackedMessages describe the whole inbox file: its entry
	// count and how many message entries in it are acked — the compaction
	// footprint compactedAway reads.
	entries       int
	ackedMessages int
}, err error) {
	if ownerKey == "" {
		return sighting, errors.New("agent: steer boot final delivery: empty parent inbox owner key")
	}
	entries, err := r.Inbox.Entries(ownerKey)
	if err != nil {
		return sighting, fmt.Errorf("agent: steer boot final delivery: read parent %s inbox: %w", ownerKey, err)
	}
	sighting.entries = len(entries)
	messageIDs := map[string]bool{}
	ackedIDs := map[string]bool{}
	for _, entry := range entries {
		switch entry.Kind {
		case session.InboxEntryMessage:
			if entry.Message == nil {
				continue
			}
			envelope, envErr := decodeBootMessage(*entry.Message)
			if envErr == nil {
				messageIDs[envelope.MessageID] = true
			}
			if envErr != nil || envelope.MessageID != messageID {
				continue
			}
			sighting.appended = true
			raw, rawErr := entry.Message.MarshalJSON()
			if rawErr != nil || string(raw) != string(payload) {
				sighting.bytesDiverge = true
			}
		case session.InboxEntryAck:
			for _, id := range entry.AckedIDs {
				ackedIDs[id] = true
			}
			if slices.Contains(entry.AckedIDs, messageID) {
				sighting.acked = true
			}
		}
	}
	for id := range messageIDs {
		if ackedIDs[id] {
			sighting.ackedMessages++
		}
	}
	return sighting, nil
}

// recordFinalDeliveryFacts records durable delivery facts through the one
// legal post-terminal writer (LifecycleStore.UpdateFinalDelivery — delivery
// envelopes only, never a LifecycleRecord behind a newer generation). The
// revision CAS conflict is retried from fresh joined state; every failure is
// surfaced through the operator notice and leaves the item retryable.
func (r *SteerBootRecovery) recordFinalDeliveryFacts(item session.PendingFinalDelivery, facts session.FinalDeliveryProgress, notice func(key, message string)) {
	for attempt := 0; attempt < maxFinalDeliveryCASAttempts; attempt++ {
		state, revision, retired, err := r.Lifecycle.FinalDeliveryState(item.SessionID, item.Generation, item.Commit.CommitID)
		if err != nil {
			notice("final-delivery:"+item.SessionID, fmt.Sprintf(
				"session %s committed final %s delivery facts could not be read: %v — retry next boot (D2)",
				item.SessionID, item.Commit.MessageID, err))
			return
		}
		if retired {
			// Retirement completes the summary; nothing to record.
			return
		}
		merged := state.Merge(facts)
		if merged.Equal(state) {
			return
		}
		err = r.Lifecycle.UpdateFinalDelivery(item.SessionID, item.Generation, item.Commit.CommitID, revision, session.FinalDeliveryCommand{Advance: &merged})
		if errors.Is(err, session.ErrFinalDeliveryRevisionConflict) {
			continue
		}
		if err != nil {
			notice("final-delivery:"+item.SessionID, fmt.Sprintf(
				"session %s committed final %s delivery facts could not be recorded: %v — the facts stay unrecorded and retryable, never assumed (D2)",
				item.SessionID, item.Commit.MessageID, err))
			return
		}
		return
	}
	notice("final-delivery:"+item.SessionID, fmt.Sprintf(
		"session %s committed final %s delivery facts hit a persistent revision conflict — retry next boot (D2)",
		item.SessionID, item.Commit.MessageID))
}
