package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const goalReminderPauseNotice = "No progress after 5 reminders — goal paused. Reply to continue."

var errGoalReminderObsolete = errors.New("goal reminder outcome no longer belongs to the active goal")

// updateGoalReminderStreak counts assessed COMPLETED outcomes, never publication.
// The existing persisted field is a consecutive-outcome streak, not a total quota.
func updateGoalReminderStreak(sessionID, goalID string, progress, completed bool) (*goal.Goal, error) {
	return resolveGoalRecordStore().Update(goalID, func(cur *goal.Goal) error {
		if cur.State != generated.GoalStateActive || cur.ActiveSessionID != sessionID {
			return errGoalReminderObsolete
		}
		if goalReminderBrakePaused(cur) {
			return nil // only a genuine human input resumes the paused brake
		}
		if progress {
			cur.ZeroOutputPushes = 0
		} else if completed {
			cur.ZeroOutputPushes++
		}
		if cur.ZeroOutputPushes >= goalZeroOutputPushMax {
			cur.LatestReason = goalReminderPauseNotice
		}
		return nil
	})
}

func goalReminderBrakePaused(rec *goal.Goal) bool {
	return rec != nil && rec.State == generated.GoalStateActive &&
		(rec.ZeroOutputPushes >= goalZeroOutputPushMax || rec.LatestReason == goalReminderFailedTurnNotice)
}

// pauseGoalReminders uses the existing waiting status and generated replay
// carrier. The goal remains active; no claim, verdict or execution is invented.
// The passive seam stays before the acceptance lock. Human resume uses that
// same lock, so a reply that already cleared the streak publishes nothing.
func (al *AgentLoop) pauseGoalReminders(sessionID string, rec *goal.Goal) error {
	current, err := activeGoalForSession(sessionID)
	if err != nil {
		return err
	}
	if current == nil || rec == nil || current.GoalID != rec.GoalID || !goalReminderBrakePaused(current) {
		return nil
	}
	work := goalReminderWorkFor(current.GoalID)
	work.mu.Lock()
	assessedTurn := work.lastAssessedTurn
	assessedRunID := work.lastAssessedRunID
	noticeID, err := al.goalReminderPauseNoticeID(sessionID, current.GoalID, assessedTurn, assessedRunID, goalReminderPauseContent(current))
	work.mu.Unlock()
	if err != nil {
		return err
	}
	goalReminderPauseCheckpoint(sessionID, current.GoalID)
	work.mu.Lock()
	accepted, err := al.commitGoalReminderPause(sessionID, current.GoalID, assessedTurn, noticeID, work)
	work.mu.Unlock()
	if errors.Is(err, errGoalReminderObsolete) {
		return nil
	}
	if err != nil {
		return err
	}
	return al.finishGoalReminderPauseDelivery(sessionID, accepted)
}

// goalReminderPauseAcceptance is the pause that won the work lock. Delivery
// tails run only after that lock is released.
type goalReminderPauseAcceptance struct {
	goalID   string
	noticeID string
	content  string
	written  bool
}

// goalReminderPauseNoticeID binds the notice to the already assessed producing
// turn before the passive seam. After a process-local reset only, recover the
// existing transcript identity here, never from a later human turn after the seam.
func (al *AgentLoop) goalReminderPauseNoticeID(sessionID, goalID, assessedTurn, assessedRunID, content string) (string, error) {
	entryID := "goal-no-progress-" + goalID
	if content == goalReminderFailedTurnNotice {
		if assessedTurn != "" && assessedRunID == "" {
			return "", errors.New("goal reminder pause: assessed failure execution identity is unavailable")
		}
		return al.goalReminderFailurePauseID(sessionID, entryID, assessedRunID)
	}
	if assessedTurn != "" {
		return entryID + "-" + assessedTurn, nil
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return "", fmt.Errorf("goal reminder pause: transcript store for %q is unavailable", sessionID)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return "", fmt.Errorf("goal reminder pause: read completed outcome identity: %w", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].GoalID == goalID && entries[i].Role == "assistant" && entries[i].TurnID != "" {
			return entryID + "-" + entries[i].TurnID, nil
		}
	}
	return entryID, nil
}

// commitGoalReminderPause and human resume share work.mu through their durable
// update and synchronous status/replay publication. No connector tail runs here.
func (al *AgentLoop) commitGoalReminderPause(sessionID, goalID, assessedTurn, noticeID string, work *goalReminderWork) (*goalReminderPauseAcceptance, error) {
	current, err := activeGoalForSession(sessionID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.GoalID != goalID || current.ActiveSessionID != sessionID || !goalReminderBrakePaused(current) ||
		work.lastAssessedTurn != assessedTurn || work.pending != nil {
		return nil, errGoalReminderObsolete
	}
	goalReminderPauseFreshReadCheckpoint(sessionID, goalID)
	content := goalReminderPauseContent(current)
	al.goalSetWaitingOnUser(goalID, true)
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return nil, fmt.Errorf("goal reminder pause: transcript store for %q is unavailable", sessionID)
	}
	agentID, err := goalWorkingAgentID(sessionID, store)
	if err != nil {
		return nil, fmt.Errorf("goal reminder pause: resolve working agent: %w", err)
	}
	entry := session.TranscriptEntry{
		ID:   noticeID,
		Type: session.EntryTypeSystem, Role: "system", Content: content,
		AgentID: agentID, GoalID: goalID, Timestamp: time.Now().UTC(),
	}
	written, err := store.AppendTranscriptOnce(sessionID, entry)
	if err != nil {
		return nil, fmt.Errorf("goal reminder pause: persist visible system notice: %w", err)
	}
	active, capN := al.activeLoopsSnapshot("goal")
	payload := GoalStatusChangedPayload{
		SessionID: sessionID, GoalID: goalID, Condition: current.Prompt,
		Round: current.Round, MaxRounds: current.MaxRounds, LatestReason: content,
		ActiveLoops: active, Cap: capN, State: goalPillWaitingOnUser,
	}
	if written {
		stamp := entry.Timestamp.Format(time.RFC3339Nano)
		payload.ChatNotice = &generated.ReplayMessageFrame{
			Type: string(generated.WsFrameTypeReplayMessage), SessionId: sessionID,
			Id: &entry.ID, Role: entry.Role, Content: entry.Content,
			AgentId: &entry.AgentID, GoalId: &entry.GoalID, Timestamp: &stamp,
		}
	}
	al.EmitGoalStatusChanged(payload)
	return &goalReminderPauseAcceptance{goalID: goalID, noticeID: entry.ID, content: content, written: written}, nil
}

// finishGoalReminderPauseDelivery sends connector and upward copies after the
// acceptance lock. It does not take that lock itself and does not decide the
// pause again. enqueueGoalReminderPause takes the lock on its own.
func (al *AgentLoop) finishGoalReminderPauseDelivery(sessionID string, accepted *goalReminderPauseAcceptance) error {
	if accepted.written {
		reason := "no progress after 5 reminders"
		if accepted.content == goalReminderFailedTurnNotice {
			reason = "3 reminder turns failed in a row"
		}
		al.notifyGoalPaused(sessionID, accepted.goalID, "reminder_brake", reason)
	}
	if al.audienceFor(context.Background(), steer.BoundaryFinalReply, sessionID) != steer.AudienceUser {
		if accepted.written {
			al.deliverGoalParkUpward(sessionID, false, accepted.content)
		}
		return nil
	}
	route, err := goalTriggers().routeFor(sessionID)
	if err != nil {
		return fmt.Errorf("goal reminder pause: resolve channel: %w", err)
	}
	if route.channel != "" && route.channel != "webchat" && route.chatID != "" {
		return al.enqueueGoalReminderPause(sessionID, accepted.goalID, accepted.noticeID, route.channel, route.chatID, accepted.content)
	}
	return nil
}

// prepareGoalKeeperForTurn reuses the existing real-user entry seam. A task or
// automatic wake cannot clear the five-outcome brake merely by starting a turn.
func (al *AgentLoop) prepareGoalKeeperForTurn(opts processOptions) {
	al.liftGoalKeeperStopPauseIfNewTurn(opts.TranscriptStore, opts.TranscriptSessionID)
	al.resumeGoalRemindersForHuman(opts)
}

func (al *AgentLoop) resumeGoalRemindersForHuman(opts processOptions) {
	if !opts.UserInitiated || opts.SenderID == goalLoopFollowUpSenderID {
		return
	}
	rec, err := activeGoalForSession(opts.TranscriptSessionID)
	if err != nil {
		al.reportGoalReadError(opts.TranscriptSessionID, "resume goal reminders", err)
		return
	}
	if rec == nil {
		return
	}
	work := goalReminderWorkFor(rec.GoalID)
	work.mu.Lock()
	fresh, err := activeGoalForSession(opts.TranscriptSessionID)
	if err != nil {
		work.mu.Unlock()
		al.reportGoalReadError(opts.TranscriptSessionID, "resume goal reminders", err)
		return
	}
	if fresh == nil || fresh.GoalID != rec.GoalID {
		work.mu.Unlock()
		return
	}
	wasPaused := goalReminderBrakePaused(fresh)
	_, err = resolveGoalRecordStore().Update(fresh.GoalID, func(cur *goal.Goal) error {
		if cur.State != generated.GoalStateActive || cur.ActiveSessionID != opts.TranscriptSessionID {
			return errGoalReminderObsolete
		}
		cur.ZeroOutputPushes = 0
		if cur.LatestReason == goalReminderPauseNotice || cur.LatestReason == goalReminderFailedTurnNotice {
			cur.LatestReason = ""
		}
		return nil
	})
	if err != nil {
		work.mu.Unlock()
		if !errors.Is(err, errGoalReminderObsolete) {
			al.reportGoalReadError(opts.TranscriptSessionID, "resume goal reminder streak", err)
		}
		return
	}
	work.pending = nil
	work.failedReminders = 0
	work.lastAssessedRunID = ""
	work.checkIn = nil
	work.checkInSent = false
	work.stallSince = time.Time{}
	consumeGoalReminderProgress(fresh.GoalID, work, time.Now().UTC())
	al.goalMarkIdleSettling(fresh.GoalID, false)
	if wasPaused {
		al.goalSetWaitingOnUser(fresh.GoalID, false)
		al.EmitGoalStatusRehydrate(opts.TranscriptSessionID)
	}
	work.mu.Unlock()
}

// savedGoalWakeHeld keeps a paused automatic input unconsumed, just like an
// existing question hold. Human steering still uses its normal authority path.
func (al *AgentLoop) savedGoalWakeHeld(msg bus.InboundMessage, sessionID, messageID string, saved bool) (bool, error) {
	if held, err := al.savedWakeHeldForQuestion(sessionID, messageID, saved); held || err != nil {
		return held, err
	}
	if msg.Sender.CanonicalID != goalLoopFollowUpSenderID {
		return false, nil
	}
	rec, err := activeGoalForSession(sessionID)
	if err != nil {
		return true, fmt.Errorf("goal reminder pause: read wake goal: %w", err)
	}
	if goalReminderBrakePaused(rec) {
		return true, nil
	}
	if inboundMetadata(msg, "goal_reminder") == "true" {
		if rec == nil || rec.GoalID != inboundMetadata(msg, "goal_id") {
			return true, nil
		}
		return al.primeGoalReminderObservation(sessionID, rec, messageID)
	}
	return false, nil
}
