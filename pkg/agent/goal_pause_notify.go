package agent

import "log/slog"

// GoalPausedEvent is an internal report to the gateway's existing bell producer.
// Owner is read successfully from the actual session, never inferred from a
// missing/unreadable record. Reason contains only the bounded claim explanation
// or a fixed brake reason, not the final response or a pending question.
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
		slog.Error("agent: goal pause session metadata unavailable; bell not delivered", "session_id", sessionID, "goal_id", goalID)
		return
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		// An unreadable owner must never become an all-accounts notification.
		// Do not copy the failed metadata or claim text into diagnostics.
		slog.Error("agent: could not read goal pause session metadata; bell not delivered", "session_id", sessionID, "goal_id", goalID)
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
