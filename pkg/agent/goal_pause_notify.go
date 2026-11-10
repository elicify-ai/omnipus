package agent

import (
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
)

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
	(*hook)(GoalPausedEvent{
		SessionID: sessionID, GoalID: goalID, AgentID: meta.AgentID,
		ChatTitle: al.goalPausedChatTitle(meta), Owner: meta.Owner, Reason: reason,
	})
}

// untitledGoalPauseChat names a chat that has no title to show. It never
// carries an agent or session ID.
const untitledGoalPauseChat = "an untitled chat"

// goalPausedChatTitle is the chat label the bell names. A main chat is never
// titled (its label is derived on read), so it gets the same
// "<Agent> - Main Session" label the sidebar shows, built from the agent's
// registered display name. A missing or blank name, and any other chat whose
// title is empty or whitespace-only, become "an untitled chat" — never the
// agent ID. A non-blank title is returned exactly as stored.
func (al *AgentLoop) goalPausedChatTitle(meta *session.UnifiedMeta) string {
	if meta.Type == session.SessionTypeMain {
		if name := al.registeredAgentDisplayName(meta.AgentID); name != "" {
			return session.MainSessionTitle(name)
		}
		return untitledGoalPauseChat
	}
	if strings.TrimSpace(meta.Title) == "" {
		return untitledGoalPauseChat
	}
	return meta.Title
}

// registeredAgentDisplayName returns the trimmed display name of a registered
// agent, or "" when the agent is unknown or unnamed. Unlike
// AgentRegistry.GetAgentName it has no agent-ID fallback.
func (al *AgentLoop) registeredAgentDisplayName(agentID string) string {
	registry := al.GetRegistry()
	if registry == nil {
		return ""
	}
	inst, ok := registry.GetAgent(agentID)
	if !ok || inst == nil {
		return ""
	}
	return strings.TrimSpace(inst.Name)
}
