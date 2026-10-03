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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// through. Idempotent per transition: an already-stored notice id is not
// re-appended and never re-wakes (D8.4's one wake per notice).
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
// unfinished or freshly finished work for this transition: a fresh append
// (followed by its one wake when the parent is working) or a failed append;
// an already-stored notice is finished work — no second wake.
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
	wake, err := al.parentWorkingForStoppedNotice(parentID)
	if err != nil {
		return false, err
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
	if res != nil && res.Deduped {
		return false, nil
	}
	if !wake {
		return true, nil
	}
	return true, al.wakeParentForStoppedNotice(ctx, rec, parentID, id, tr)
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

// parentWorkingForStoppedNotice is D6's routing table. A chat root has no
// lifecycle record and is the working parent. stopped, waiting, done and
// failed retain the notice with no wake.
func (al *AgentLoop) parentWorkingForStoppedNotice(parentID string) (bool, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return false, errors.New("steer: stopped notice: lifecycle store is not wired")
	}
	parent, err := lifecycle.Load(parentID)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("steer: stopped notice: load parent %s: %w", parentID, err)
	}
	switch parent.State {
	case session.LifecycleRunning, session.LifecycleQueued:
		return true, nil
	default:
		return false, nil
	}
}

func (al *AgentLoop) wakeParentForStoppedNotice(ctx context.Context, rec *session.LifecycleRecord, parentID, id string, tr session.StoppedTransition) error {
	if al.asyncNotifier == nil {
		return errors.New("steer: stopped notice: async notifier is not wired")
	}
	target := rec.SteeredBy.ReportingTarget
	if target.Channel == "" || target.ChatID == "" {
		return fmt.Errorf("steer: stopped notice: parent %s has no wake destination", parentID)
	}
	err := al.asyncNotifier.WakeParentAlways(ctx, "error", tools.MessageParentWakeEvent{
		Channel:             target.Channel,
		ChatID:              target.ChatID,
		AgentID:             rec.AgentID,
		TranscriptSessionID: parentID,
		Content:             stoppedChildNoticeText(tr),
		MessageID:           id,
		Generation:          tr.Generation,
	})
	if err != nil {
		return fmt.Errorf("steer: stopped notice: wake parent %s: %w", parentID, err)
	}
	return nil
}

// recoverStoppedChildNotice replays the direct-parent notices of one steered
// record's LANDED stops at boot. The control ledger's landed history is the
// only source: each pending notice is composed from its own transition, the
// record's current note is never re-read or rewritten, and a stop that has
// not landed publishes nothing (D2 CRIT-001). It returns false when a stored
// final should still take the existing completion-repair path, or when there
// is no landed history to replay. Boot never resumes anything and never
// fabricates a restart stop (D8.5); finishing an accepted-but-unlanded fence
// at boot is W3b's reconciliation, reported visibly — never minted here.
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
	pending, replayErr := r.replayLandedStopNotices(ctx, rec)
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
	if rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued {
		return false
	}
	if r.hasUnacknowledgedFinal(rec, notice) {
		return false
	}
	return pending || replayErr != nil
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

func (r *SteerBootRecovery) hasUnacknowledgedFinal(rec *session.LifecycleRecord, notice func(string, string)) bool {
	messages := r.unacknowledged(rec, notice)
	finalID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
	_, ok := findBootMessage(messages, finalID)
	return ok
}

func (r *SteerBootRecovery) noticeLoop() (*AgentLoop, error) {
	sud, ok := r.Deliverer.(*SteerUpwardDeliverer)
	if !ok || sud == nil || sud.agentLoop == nil {
		return nil, errors.New("stopped-child notice deliverer is not wired")
	}
	return sud.agentLoop, nil
}
