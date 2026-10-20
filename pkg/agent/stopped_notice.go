// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// stopped_notice.go is the D6 direct-parent stopped-child notice publisher
// (ADR-20260928 sub-agent control plane, frozen asset cd20cf8b).
//
// ONE source, ONE ordering: the control ledger's landed-stop history
// (LifecycleStore.ListStoppedTransitions) is the only thing a notice is ever
// composed from — its id is (parent, child, generation, REAL stop_seq) and
// its content is that transition's cause/actor/original instant. Nothing
// here rewrites or re-reads the record's current stop note, and no notice or
// wake may precede the landed event (D2 CRIT-001): both producers — the
// cancelled turn's completion (steer_completion.go::deliverSteeredCompletion)
// and the never-ran landing (steer_cancel.go::landSteeredStopReport) — call
// this publisher only AFTER the stopped landing and its ledger history are
// durable. A notice append failure leaves the landed history pending and
// retryable (boot_sweep.go::SteerBootRecovery.recoverStoppedChildNotice
// replays it), reported visibly, never gating the landing and never
// considered applied.
//
// W3b boundary: an accepted stop whose landing has not happened has NO
// landed event, so it publishes nothing; finishing such a fence at boot is
// W3b's reconciliation, reported visibly — never minted here, and no
// restart stop is ever fabricated (D8.5).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// stoppedChildNoticeID is D6's dedup key, spelled
// stopped-notice:<parent>:<child>:<generation>:<stop_seq>. stop_seq is the
// landed transition's REAL control-ledger sequence read back from the
// ledger's history — never the generation stand-in a synthesized note
// carries, and never a value invented at send time.
func stoppedChildNoticeID(parentID, childID string, generation int, stopSeq uint64) string {
	return fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, childID, generation, stopSeq)
}

// stoppedChildNoticeText is D6's notice sentence, composed from the LANDED
// transition's own tuple: its cause, actor and original instant — never a
// time.Now reconstruction and never the record's current note.
func stoppedChildNoticeText(tr session.StoppedTransition) string {
	text := fmt.Sprintf(
		"%s cause: %s. actor: %s. at: %s. You can resume it, redirect it, do the work, report it open, or clear that helper's goal.",
		session.LifecycleNoticePrefixStoppedChild, string(tr.Cause), tr.Actor, tr.At.UTC().Format(time.RFC3339Nano),
	)
	if strings.HasPrefix(tr.Actor, "human:") {
		text += " Consider asking the owner first."
	}
	return text
}

// deliverLandedStopNotices delivers one direct-parent notice for EVERY
// landed stop transition in the child's control-ledger history whose
// original parent is a real session. It is the single publisher both stop
// producers call after their landing is durable, and the boot retry replays
// through. Idempotent per transition: stored rows are repaired without a
// second append. PW-D13 keeps intentional Stops quiet on every delivery and
// replay; only restart and historical timeout call WakeSession, whose saved
// input, consumption markers and pending publication coalesce their wakes.
//
// pending reports whether at least one transition had UNFINISHED work — a
// fresh append or a failed one — so the boot caller can tell "nothing
// pending" apart from "handled". The returned error joins every per-
// transition failure; a pending failure is visible (the caller reports it),
// never silent, and never un-lands the stop that already committed.
func (al *AgentLoop) deliverLandedStopNotices(ctx context.Context, rec *session.LifecycleRecord) (pending bool, err error) {
	if al == nil || rec == nil {
		return false, errors.New("steer: stopped notice: loop or record is missing")
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: stopped notice: lifecycle store is not wired")
	}
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return false, errors.New("steer: stopped notice: inbox store is not wired")
	}
	transitions, readErr := lifecycle.ListStoppedTransitions(rec.SessionID)
	if readErr != nil {
		return false, fmt.Errorf("steer: stopped notice: read landed history of %s: %w", rec.SessionID, readErr)
	}
	// Every landed transition — fenced or fence-less — is discovered from
	// the ledger's landed history alone (Correction C3): a fence-less
	// landing ledgered its transition in the same Mutate that wrote its
	// note, so the note-derived fallback is retired. A RESUME clearing the
	// active note can therefore never lose an untaken notice, and two
	// fence-less stops in one generation keep distinct ids.
	var errs []error
	for _, tr := range transitions {
		work, trErr := al.deliverLandedStopNotice(ctx, rec, tr)
		if trErr != nil {
			errs = append(errs, trErr)
			pending = true
			continue
		}
		pending = pending || work
	}
	return pending, errors.Join(errs...)
}

// deliverLandedStopNotice delivers ONE landed transition's notice to its
// ORIGINAL direct parent. A root stop (empty parent in the history) is
// history the publisher keeps, never a parent it invents. work reports
// unfinished or freshly finished work. The row is always repaired before the
// acknowledgement check. An intentional Stop cannot wake or join; a restart
// or timeout uses its own landed-transition id through WakeSession. Failures
// are returned and the landed ledger remains the repair source.
func (al *AgentLoop) deliverLandedStopNotice(ctx context.Context, rec *session.LifecycleRecord, tr session.StoppedTransition) (work bool, err error) {
	parentID := strings.TrimSpace(tr.ParentSessionID)
	if parentID == "" {
		return false, nil
	}
	if rec.SteeredBy == nil {
		// The transition names a parent but the record has no edge to reach
		// one through: a visible failure, retried at boot — never a silent
		// skip of a parent the history names.
		return true, fmt.Errorf("steer: stopped notice: %s has no steering edge for its notice to %s", rec.SessionID, parentID)
	}
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return false, errors.New("steer: stopped notice: inbox store is not wired")
	}
	id := stoppedChildNoticeID(parentID, tr.SessionID, tr.Generation, tr.StopSeq)
	msg, err := stoppedChildNoticeMessage(rec, parentID, id, tr)
	if err != nil {
		return false, err
	}
	res, err := inbox.Append(parentID, msg)
	if err != nil {
		// D6 round-3 MAJ-001: the delivery failure stays PENDING — visibly
		// returned, retried at boot/periodic delivery, never silently
		// considered applied. The landed stop itself is already durable and
		// is not gated by this.
		return true, fmt.Errorf("steer: stopped notice: append %s: %w", id, err)
	}
	// The landed ledger is the lasting repair source. Required rows are
	// repaired even when the inbox notice was acknowledged or compacted.
	helperEvent := stoppedHelperEvent(rec, tr)
	if err := al.saveHelperEvent(parentID, id, helperEvent); err != nil {
		al.publishHelperEventRepairPending(parentID, id, helperEvent)
		return true, errors.Join(err, al.retainPendingHelperEvent(rec.SessionID, parentID, id, helperEvent))
	}
	if tr.Cause == session.StopCauseRestart {
		if err := al.saveHelperEvent(rec.SessionID, id, helperEvent); err != nil {
			al.publishHelperEventRepairPending(rec.SessionID, id, helperEvent)
			return true, errors.Join(err, al.retainPendingHelperEvent(rec.SessionID, rec.SessionID, id, helperEvent))
		}
	}
	if tr.Cause != session.StopCauseRestart && tr.Cause != session.StopCauseTimeout {
		// PW-D13: an intentional Stop never calls the wake entry, including
		// when a live parent could otherwise accept an in-turn join.
		return !res.Deduped, nil
	}
	if res != nil && res.Deduped {
		// A retry may repair rows, but an acknowledged transition starts no
		// turn. An unread restart/timeout still passes through the one shared
		// wake entry; an intentional Stop already returned above.
		taken, ackErr := stopNoticeTaken(inbox, parentID, id)
		if ackErr != nil {
			return false, ackErr
		}
		if taken {
			return false, nil
		}
	}
	return true, al.wakeParentForStoppedNotice(ctx, rec, parentID, id, tr)
}

// stopNoticeTaken reads the parent's durable inbox and reports whether an
// ack entry covers id — the same durable "the parent took the note" fact the
// parent's wake consumer writes (loop_inbound.go's inbox.Ack of the message
// id). Compaction preserves that fact for every retained message entry, so a
// stored note's ack coverage is decidable from the file alone. A read
// failure is returned, never swallowed into a guess about taken-ness.
func stopNoticeTaken(inbox *session.MessageInboxStore, parentID, id string) (bool, error) {
	entries, err := inbox.Entries(parentID)
	if err != nil {
		return false, fmt.Errorf("steer: stopped notice: read ack state of %s for parent %s: %w", id, parentID, err)
	}
	for _, e := range entries {
		if e.Kind != session.InboxEntryAck {
			continue
		}
		for _, acked := range e.AckedIDs {
			if acked == id {
				return true, nil
			}
		}
	}
	return false, nil
}

func stoppedChildNoticeMessage(rec *session.LifecycleRecord, parentID, id string, tr session.StoppedTransition) (generated.SessionMessage, error) {
	generation := tr.Generation
	parent := parentID
	var message generated.SessionMessage
	err := message.FromSessionMessageError(generated.SessionMessageError{
		MessageId:       id,
		SessionId:       tr.SessionID,
		ParentSessionId: &parent,
		CreatedAt:       tr.At.UTC(),
		Depth:           1,
		Direction:       generated.SessionMessageErrorDirectionChildToParent,
		Generation:      &generation,
		Kind:            generated.SessionMessageErrorKindError,
		Fatal:           false,
		Text:            stoppedChildNoticeText(tr),
		SenderIdentity:  noticeSender(rec),
		UntrustedOrigin: false,
	})
	if err != nil {
		return generated.SessionMessage{}, fmt.Errorf("steer: stopped notice: build message: %w", err)
	}
	return message, nil
}

func noticeSender(rec *session.LifecycleRecord) string {
	if rec != nil && strings.TrimSpace(rec.AgentID) != "" {
		return rec.AgentID
	}
	return "runtime"
}

func (al *AgentLoop) wakeParentForStoppedNotice(ctx context.Context, rec *session.LifecycleRecord, parentID, id string, tr session.StoppedTransition) error {
	var reason string
	switch tr.Cause {
	case session.StopCauseRestart:
		reason = "restart"
	case session.StopCauseTimeout:
		reason = "timed_out"
	default:
		return nil
	}
	event := stoppedHelperEvent(rec, tr)
	source := wakeSource{childID: rec.SessionID, event: &event, publishKind: "error"}
	if tr.Cause == session.StopCauseRestart && rec.StopNote != nil && rec.StopNote.Seq == tr.StopSeq {
		source.restartBootSeq = rec.StopNote.BootSeq
	}
	_, err := al.WakeSession(context.WithValue(ctx, wakeSourceKey{}, source), parentID, reason, id, event.Text)
	if err != nil {
		return fmt.Errorf("steer: stopped notice: wake parent %s: %w", parentID, err)
	}
	return nil
}

// recoverStoppedChildNotice replays the direct-parent notices of one steered
// record's LANDED stops at boot. The control ledger's landed history is the
// only source: each pending notice is composed from its own transition, the
// record's current note is never re-read or rewritten, and a stop that has
// not landed publishes nothing (D2 CRIT-001). It returns true only for the
// shapes whose boot handling IS this replay — a stopped record, or an
// accepted-but-unlanded stop fence (W3b's reconciliation, reported visibly,
// never minted here); every other non-terminal state returns false so the
// caller's own boot consequence runs. Correction C5: a replay outcome is NOT
// current-run recovery — a replay failure stays visibly pending and retried
// below, and it never suppresses the caller's current-run stop nor
// authorizes a run. Native restart resume is the separate admission step.
func (r *SteerBootRecovery) recoverStoppedChildNotice(ctx context.Context, rec *session.LifecycleRecord, notice func(string, string)) bool {
	if r == nil || rec == nil || rec.SteeredBy == nil || rec.State == session.LifecycleNeedsInput {
		return false
	}
	// The historical replay is INDEPENDENT of the record's current tail: a
	// landed stop's pending notice is owed for that transition whether the
	// child is now stopped, resumed (the RESUME cleared the active note,
	// never the ledger — founder Q2=A), or already terminal — and the
	// committed final's repair path must never hide it. (needs_input stays
	// excluded above: a parked session's boot handling is its own flow.)
	_, replayErr := r.replayLandedStopNotices(ctx, rec)
	if replayErr != nil {
		r.reportStoppedNotice(rec, notice, replayErr)
	}
	if rec.Terminal() {
		// A terminal record's own committed final takes the completion-repair
		// path below; the historical replay has already run.
		return false
	}
	if rec.State == session.LifecycleStopped {
		return true
	}
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		// An accepted stop that has not landed: no landed event exists, so no
		// notice may precede it, and finishing the fence at boot is W3b's
		// boot reconciliation — reported pending, visibly.
		r.reportPendingFence(rec, notice)
		return true
	}
	// Correction C5: running, queued and every other non-terminal state fall
	// through to the caller's own boot consequence — the interrupted run's
	// stop — whatever the replay above found.
	return false
}

// landedStopNoticeOwnsReplay authenticates a stored notice before excluding
// it from generic boot error replay. The landed ledger event, its original
// parent edge and the existing canonical message builder are the authority;
// a text prefix or caller-supplied id alone never grants notice handling.
// The dedicated landed-history publisher already ran for this boot record.
func (r *SteerBootRecovery) landedStopNoticeOwnsReplay(rec *session.LifecycleRecord, message generated.SessionMessage) (bool, error) {
	stored, err := message.AsSessionMessageError()
	if err != nil {
		return false, fmt.Errorf("steer: stopped notice: decode stored notice: %w", err)
	}
	if !strings.HasPrefix(stored.MessageId, "stopped-notice:") &&
		!strings.HasPrefix(stored.Text, session.LifecycleNoticePrefixStoppedChild) {
		return false, nil
	}
	transitions, err := r.Lifecycle.ListStoppedTransitions(rec.SessionID)
	if err != nil {
		return false, fmt.Errorf("steer: stopped notice: read landed history of %s: %w", rec.SessionID, err)
	}
	for _, tr := range transitions {
		parentID := strings.TrimSpace(tr.ParentSessionID)
		id := stoppedChildNoticeID(parentID, tr.SessionID, tr.Generation, tr.StopSeq)
		if stored.MessageId != id {
			continue
		}
		if parentID == "" || parentID != strings.TrimSpace(rec.SteeringSessionID()) {
			return false, fmt.Errorf("steer: stopped notice: %s does not match the current steering edge", id)
		}
		canonical, buildErr := stoppedChildNoticeMessage(rec, parentID, id, tr)
		if buildErr != nil {
			return false, buildErr
		}
		// Normalize the generated variant's JSON field order before comparing
		// every field, including origin, generation and the original instant.
		storedBytes, marshalErr := json.Marshal(stored)
		if marshalErr != nil {
			return false, fmt.Errorf("steer: stopped notice: encode stored notice %s: %w", id, marshalErr)
		}
		canonicalBytes, marshalErr := canonical.MarshalJSON()
		if marshalErr != nil {
			return false, fmt.Errorf("steer: stopped notice: encode canonical notice %s: %w", id, marshalErr)
		}
		if !bytes.Equal(storedBytes, canonicalBytes) {
			return false, fmt.Errorf("steer: stopped notice: %s does not match its landed transition's canonical message", id)
		}
		return true, nil
	}
	return false, fmt.Errorf("steer: stopped notice: %s has no matching landed transition", stored.MessageId)
}

// replayLandedStopNotices runs the loop's landed-history publisher for one
// boot record.
func (r *SteerBootRecovery) replayLandedStopNotices(ctx context.Context, rec *session.LifecycleRecord) (bool, error) {
	al, err := r.noticeLoop()
	if err != nil {
		return false, err
	}
	return al.deliverLandedStopNotices(ctx, rec)
}

// reportStoppedNotice surfaces a replay failure as the operator-visible
// pending-delivery report, naming the affected child.
func (r *SteerBootRecovery) reportStoppedNotice(rec *session.LifecycleRecord, notice func(string, string), err error) {
	if err == nil || notice == nil || rec == nil {
		return
	}
	notice("stopped-notice:"+rec.SessionID, fmt.Sprintf(
		"session %s stopped-child notice was not delivered: %v", rec.SessionID, err))
}

// reportPendingFence surfaces an accepted-but-unlanded stop fence at boot:
// its notice is pending on the landing, which W3b's boot reconciliation owns.
func (r *SteerBootRecovery) reportPendingFence(rec *session.LifecycleRecord, notice func(string, string)) {
	if notice == nil || rec == nil {
		return
	}
	notice("stopped-notice:"+rec.SessionID, fmt.Sprintf(
		"session %s carries a current stop fence that has not landed; its direct-parent notice stays pending until boot reconciliation finishes the stop", rec.SessionID))
}

func (r *SteerBootRecovery) noticeLoop() (*AgentLoop, error) {
	sud, ok := r.Deliverer.(*SteerUpwardDeliverer)
	if !ok || sud == nil || sud.agentLoop == nil {
		return nil, errors.New("stopped-child notice deliverer is not wired")
	}
	return sud.agentLoop, nil
}
