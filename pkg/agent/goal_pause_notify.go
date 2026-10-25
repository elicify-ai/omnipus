package agent

import "errors"

// GoalPausedEvent is an internal report to the gateway's existing bell producer.
// Owner is read successfully from the actual session, never inferred from a
// missing/unreadable record. Reason is the claim explanation or a fixed brake
// reason, not the final response or a pending question; the gateway bounds it.
// not-wire-format
type GoalPausedEvent struct {
	SessionID string
	GoalID    string
	AgentID   string
	ChatTitle string
	Owner     string
	Reason    string
}

type GoalPausedFunc func(GoalPausedEvent)

// SetGoalPausedHook installs the single receiver. Nil removes it.
func (al *AgentLoop) SetGoalPausedHook(fn GoalPausedFunc) {
	if al == nil {
		return
	}
	if fn == nil {
		al.goalPausedHook.Store(nil)
		return
	}
	al.goalPausedHook.Store(&fn)
}

func (al *AgentLoop) notifyGoalPaused(sessionID, goalID, cause, reason string) {
	if al == nil {
		return
	}
	hook := al.goalPausedHook.Load()
	if hook == nil {
		return
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		al.reportGoalReadError(sessionID, "deliver goal-pause bell warning (notification not sent)", errors.New("chat ownership metadata is unavailable"))
		return
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		// An unreadable owner must never become an all-accounts notification.
		// Use the existing error surface without copying failed metadata or
		// claim/question/output text into the diagnostic.
		al.reportGoalReadError(sessionID, "deliver goal-pause bell warning (notification not sent)", errors.New("chat ownership metadata could not be read"))
		return
	}
	if reason == "" {
		switch cause {
		case "waiting_on_user":
			reason = "waiting for your reply"
		case "blocked":
			reason = "blocked"
		}
	}
	title := meta.Title
	if title == "" {
		title = "this chat"
	}
	(*hook)(GoalPausedEvent{
		SessionID: sessionID, GoalID: goalID, AgentID: meta.AgentID,
		ChatTitle: title, Owner: meta.Owner, Reason: reason,
	})
}
