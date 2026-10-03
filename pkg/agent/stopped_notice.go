package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// errStopNoteUnchanged tells Mutate to leave the record alone. The current
// generation already has a persisted stop_note; rewriting it would change
// the notice id's stop_seq or the recorded cause.
var errStopNoteUnchanged = errors.New("steer: stop note already current")

// stoppedChildNoticeID is D6's dedup key, spelled
// stopped-notice:<parent>:<child>:<generation>:<stop_seq>. stop_seq is the
// persisted note's Seq, not a value invented at send time. Today that Seq
// is still the generation stand-in stamped with the note; it is not a
// certification that the per-session control ledger exists.
func stoppedChildNoticeID(parentID, childID string, generation int, stopSeq uint64) string {
	return fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, childID, generation, stopSeq)
}

func stoppedChildNoticeText(note *session.StopNote) string {
	cause := strings.ReplaceAll(string(note.Cause), "_", " ")
	text := fmt.Sprintf(
		"%s cause: %s. actor: %s. at: %s. You can resume it, redirect it, do the work, report it open, or clear that helper's goal.",
		session.LifecycleNoticePrefixStoppedChild, cause, note.By, note.At.UTC().Format(time.RFC3339Nano),
	)
	if strings.HasPrefix(note.By, "human:") {
		text += " Consider asking the owner first."
	}
	return text
}

// ensureCurrentStopNote makes sure this generation has a persisted stop_note
// before a notice id is composed. A stamp that already wrote one is kept.
// Timeout and an unstamped stop synthesize the note here; they have no
// cascade stamp. The record stays non-stopped until the notice append
// succeeds and the caller lands it.
func (al *AgentLoop) ensureCurrentStopNote(snapshot *session.LifecycleRecord, outcome steer.Outcome) (*session.LifecycleRecord, error) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil || snapshot == nil {
		return nil, errors.New("steer: stopped notice: lifecycle store is not wired")
	}
	err := lifecycle.Mutate(snapshot.SessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return fmt.Errorf("steer: stopped notice: record %q vanished", snapshot.SessionID)
		}
		if cur.Generation != snapshot.Generation {
			return errCompleteStaleGeneration
		}
		if cur.StopNote != nil && cur.StopNote.Seq == uint64(cur.Generation) {
			return errStopNoteUnchanged
		}
		cause := session.StopCauseStop
		if outcome == steer.OutcomeTimedOut {
			cause = session.StopCauseTimeout
		}
		cur.StopNote = &session.StopNote{
			At:    time.Now().UTC(),
			By:    session.StopActorSystem,
			Seq:   uint64(cur.Generation),
			Cause: cause,
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopNoteUnchanged) {
		return nil, err
	}
	return lifecycle.Load(snapshot.SessionID)
}

// deliverStoppedChildNotice appends one direct-parent notice derived from
// the persisted stop_note. A duplicate id does not wake again.
// wakeIfAlreadyStored is for a retry of a notice that was stored before the
// stop landed (the wake itself failed, or boot is finishing the landing).
// An already-landed replay passes false so a consumed id is not woken twice.
func (al *AgentLoop) deliverStoppedChildNotice(ctx context.Context, rec *session.LifecycleRecord, wakeIfAlreadyStored bool) error {
	if al == nil || rec == nil || rec.SteeredBy == nil {
		return errors.New("steer: stopped notice: child has no direct parent")
	}
	if rec.StopNote == nil || !session.IsValidStopCause(rec.StopNote.Cause) {
		return fmt.Errorf("steer: stopped notice: %s has no persisted stop_note", rec.SessionID)
	}
	parentID := strings.TrimSpace(rec.SteeringSessionID())
	if parentID == "" {
		return fmt.Errorf("steer: stopped notice: %s has an empty direct parent", rec.SessionID)
	}
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return errors.New("steer: stopped notice: inbox store is not wired")
	}
	wake, err := al.parentWorkingForStoppedNotice(parentID)
	if err != nil {
		return err
	}
	id := stoppedChildNoticeID(parentID, rec.SessionID, rec.Generation, rec.StopNote.Seq)
	msg, err := stoppedChildNoticeMessage(rec, parentID, id)
	if err != nil {
		return err
	}
	res, err := inbox.Append(parentID, msg)
	if err != nil {
		return fmt.Errorf("steer: stopped notice: append %s: %w", id, err)
	}
	if res != nil && res.Deduped && !wakeIfAlreadyStored {
		return nil
	}
	if !wake {
		return nil
	}
	return al.wakeParentForStoppedNotice(ctx, rec, parentID, id)
}

func stoppedChildNoticeMessage(rec *session.LifecycleRecord, parentID, id string) (generated.SessionMessage, error) {
	generation := rec.Generation
	parent := parentID
	var message generated.SessionMessage
	err := message.FromSessionMessageError(generated.SessionMessageError{
		MessageId:       id,
		SessionId:       rec.SessionID,
		ParentSessionId: &parent,
		CreatedAt:       time.Now().UTC(),
		Depth:           1,
		Direction:       generated.SessionMessageErrorDirectionChildToParent,
		Generation:      &generation,
		Kind:            generated.SessionMessageErrorKindError,
		Fatal:           false,
		Text:            stoppedChildNoticeText(rec.StopNote),
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

func (al *AgentLoop) wakeParentForStoppedNotice(ctx context.Context, rec *session.LifecycleRecord, parentID, id string) error {
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
		Content:             stoppedChildNoticeText(rec.StopNote),
		MessageID:           id,
		Generation:          rec.Generation,
	})
	if err != nil {
		return fmt.Errorf("steer: stopped notice: wake parent %s: %w", parentID, err)
	}
	return nil
}

// recoverStoppedChildNotice retries or creates the direct-parent notice for
// one steered record at boot. It returns false when a stored final should
// still take the existing completion-repair path. It does not allocate a
// boot epoch and it cannot retry a notice whose stop_note a later resume
// has cleared — that history is W2's control ledger.
func (r *SteerBootRecovery) recoverStoppedChildNotice(ctx context.Context, rec *session.LifecycleRecord, notice func(string, string)) bool {
	if r == nil || rec == nil || rec.SteeredBy == nil || rec.Terminal() || rec.State == session.LifecycleNeedsInput {
		return false
	}
	if rec.State == session.LifecycleStopped {
		r.reportStoppedNotice(rec, notice, r.retryLandedStoppedNotice(ctx, rec))
		return true
	}
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		r.reportStoppedNotice(rec, notice, r.finishFencedStopNotice(ctx, rec))
		return true
	}
	if rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued {
		return false
	}
	if r.hasUnacknowledgedFinal(rec, notice) {
		return false
	}
	r.reportStoppedNotice(rec, notice, r.restartStopAndNotice(ctx, rec))
	return true
}

func (r *SteerBootRecovery) reportStoppedNotice(rec *session.LifecycleRecord, notice func(string, string), err error) {
	if err == nil || notice == nil || rec == nil {
		return
	}
	notice("stopped-notice:"+rec.SessionID, fmt.Sprintf(
		"session %s stopped-child notice was not delivered: %v", rec.SessionID, err))
}

func (r *SteerBootRecovery) retryLandedStoppedNotice(ctx context.Context, rec *session.LifecycleRecord) error {
	al, err := r.noticeLoop()
	if err != nil {
		return err
	}
	return al.deliverStoppedChildNotice(ctx, rec, false)
}

func (r *SteerBootRecovery) finishFencedStopNotice(ctx context.Context, rec *session.LifecycleRecord) error {
	if rec.StopNote == nil || rec.StopNote.Seq != uint64(rec.Generation) {
		return fmt.Errorf("session %s has a current stop fence but no current stop_note, so the notice id is not derivable", rec.SessionID)
	}
	al, err := r.noticeLoop()
	if err != nil {
		return err
	}
	// The stop has not landed yet. A notice stored by an earlier attempt
	// still needs its one wake, then the fence can clear.
	if err := al.deliverStoppedChildNotice(ctx, rec, true); err != nil {
		return err
	}
	return r.landStopped(rec.SessionID)
}

func (r *SteerBootRecovery) restartStopAndNotice(ctx context.Context, rec *session.LifecycleRecord) error {
	noted, err := r.persistRestartStopNote(rec)
	if err != nil {
		return err
	}
	al, err := r.noticeLoop()
	if err != nil {
		return err
	}
	if err := al.deliverStoppedChildNotice(ctx, noted, noted.State != session.LifecycleStopped); err != nil {
		return err
	}
	return r.landStopped(noted.SessionID)
}

// persistRestartStopNote writes a restart note only when this generation
// does not already have one. A timeout note that landed before a failed
// notice append must not be relabelled as a restart.
func (r *SteerBootRecovery) persistRestartStopNote(rec *session.LifecycleRecord) (*session.LifecycleRecord, error) {
	if r == nil || r.Lifecycle == nil || rec == nil {
		return nil, errors.New("steer: stopped notice: boot lifecycle store is not wired")
	}
	err := r.Lifecycle.Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return fmt.Errorf("steer: stopped notice: record %q vanished", rec.SessionID)
		}
		if cur.Terminal() || cur.State == session.LifecycleStopped || cur.State == session.LifecycleNeedsInput {
			return errStopNoteUnchanged
		}
		if cur.StopNote != nil && cur.StopNote.Seq == uint64(cur.Generation) {
			return errStopNoteUnchanged
		}
		cur.StopNote = &session.StopNote{
			At:    time.Now().UTC(),
			By:    session.StopActorRestart,
			Seq:   uint64(cur.Generation),
			Cause: session.StopCauseRestart,
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopNoteUnchanged) {
		return nil, err
	}
	return r.Lifecycle.Load(rec.SessionID)
}

func (r *SteerBootRecovery) landStopped(sessionID string) error {
	if r == nil || r.Lifecycle == nil {
		return errors.New("steer: stopped notice: boot lifecycle store is not wired")
	}
	return r.Lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return fmt.Errorf("steer: stopped notice: record %q vanished", sessionID)
		}
		if cur.Terminal() || cur.State == session.LifecycleStopped {
			return nil
		}
		if cur.StopNote == nil {
			return fmt.Errorf("steer: stopped notice: %s cannot land stopped without a stop_note", sessionID)
		}
		cur.State = session.LifecycleStopped
		cur.NeedsInput = nil
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			cur.Stop = nil
		}
		return nil
	})
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
