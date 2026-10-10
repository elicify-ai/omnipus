package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// clearMarkerText is the chat-view marker /clear appends to the archive.
const clearMarkerText = "Conversation context cleared"

// clearConversation is the server side of /clear (FR-030/031, U10b). It runs
// from the command dispatcher at the start of the session's own turn, which is
// the safe point: turns of one session are serialized, so a /clear typed while a
// step is in flight is dispatched only after that step ends, and input sent
// after it is delivered under the same session afterwards.
//
// Only a main or extra chat (an ordinary root of type main, chat or channel)
// may be cleared. Every helper, worker, delegate, external-CLI and task-child
// session refuses with an explanation and nothing is changed — no window move,
// no native reset, no marker. The window move keeps every archive byte
// (clearSessionWindow); the marker is appended to the chat view only.
//
// sessionKey addresses the live window store; transcriptSessionID is the
// unified session record /clear classifies. An empty transcriptSessionID (a CLI
// turn with no saved session) has no helper concept, so it is cleared as a root.
func (al *AgentLoop) clearConversation(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey, transcriptSessionID string,
) error {
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("sessions not initialized for agent")
	}
	var store *session.UnifiedStore
	if transcriptSessionID != "" {
		store = al.ResolveSessionStore(transcriptSessionID)
		if store == nil {
			return fmt.Errorf("clear: session %q has no store; nothing was cleared", transcriptSessionID)
		}
		if reason, err := al.clearRefusal(ctx, store, transcriptSessionID); err != nil {
			return err
		} else if reason != "" {
			return &commands.ClearRefusedError{Reason: reason}
		}
	}

	if err := clearSessionWindow(agent.Sessions, sessionKey); err != nil {
		return fmt.Errorf("clear: move the context window: %w", err)
	}
	if store == nil {
		return nil
	}
	marker := session.TranscriptEntry{
		ID:             fmt.Sprintf("clear-%d", time.Now().UnixNano()),
		Type:           session.EntryTypeSystem,
		Role:           "system",
		Content:        clearMarkerText,
		AgentID:        agent.ID,
		Timestamp:      time.Now().UTC(),
		ViewMembership: session.ViewMembershipChat,
	}
	if err := store.AppendTranscriptStrict(transcriptSessionID, marker); err != nil {
		return fmt.Errorf("clear: the context was cleared but the marker could not be recorded: %w", err)
	}
	return nil
}

// clearRefusal returns the plain refusal reason when sessionID is not a main or
// extra chat, "" when /clear may act on it. A classification or metadata read
// failure is returned as an error, never treated as eligible.
func (al *AgentLoop) clearRefusal(ctx context.Context, store *session.UnifiedStore, sessionID string) (string, error) {
	const helperReason = "/clear works only in a main or extra chat. This is a helper or task session, " +
		"so nothing was cleared."
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		return "", fmt.Errorf("clear: read session %q: %w; nothing was cleared", sessionID, err)
	}
	class, err := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), store).Classify(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("clear: classify session %q: %w; nothing was cleared", sessionID, err)
	}
	if class != steer.ClassOrdinaryRoot {
		return helperReason, nil
	}
	if strings.TrimSpace(meta.ParentSessionID) != "" {
		return helperReason, nil
	}
	switch meta.Type {
	case session.SessionTypeMain, session.SessionTypeChat, session.SessionTypeChannel:
		return "", nil
	default:
		return helperReason, nil
	}
}
