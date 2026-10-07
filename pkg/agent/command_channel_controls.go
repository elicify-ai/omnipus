package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

var _ channels.CancelInterceptor = (*AgentLoop)(nil)

// commandSessionByChannelChat uses the existing instance-keyed channel index,
// including idle conversations. It never creates a conversation for a control.
func (al *AgentLoop) commandSessionByChannelChat(channelName, chatID string) (string, error) {
	if al == nil || channelName == "" || chatID == "" {
		return "", fmt.Errorf("a channel and chat are required")
	}
	if v, ok := al.channelSessionIdx.Load(channelName + "/" + chatID); ok {
		id, valid := v.(string)
		if !valid || id == "" {
			return "", fmt.Errorf("the channel conversation identity could not be resolved")
		}
		return id, nil
	}
	return al.resolveSessionIDByChannelChat(channelName, chatID), nil
}

func (al *AgentLoop) requestCommandStopByChannelChat(ctx context.Context, channelName, chatID, userID, scope string) (bool, bool, error) {
	sessionID, err := al.commandSessionByChannelChat(channelName, chatID)
	if err != nil {
		return false, false, fmt.Errorf("Stop could not resolve this conversation (%w); nothing was stopped", err)
	}
	if sessionID != "" {
		return al.RequestScopedCancelForSession(ctx, sessionID, userID, channelName, scope)
	}
	if scope == "tree" {
		// No session is indexed for this chat: genuinely nothing to stop.
		return false, false, nil
	}
	// A single-session Stop may arrive before the first turn registers. The
	// existing channel/chat pre-arm latch is its truthful acknowledged outcome.
	if userID == "" {
		return false, false, fmt.Errorf("Stop requires an authenticated sender")
	}
	outcome, err := al.RequestCancel(ctx,
		CancelScope{Channel: channelName, ChatID: chatID, TurnOnly: true},
		CancelCanceller{UserID: userID, Channel: channelName},
		// Q13: a session-only Stop leaves background shells running.
		CancelHooks{})
	return outcome.Fired, outcome.Armed, err
}

// RequestStopByChannelChat is the before-intake, session-only /stop adapter.
func (al *AgentLoop) RequestStopByChannelChat(ctx context.Context, channelName, chatID, userID string) (bool, bool, error) {
	return al.requestCommandStopByChannelChat(ctx, channelName, chatID, userID, "session")
}

// RequestRedirectByChannelChat is /stop-redirect's before-intake sibling.
func (al *AgentLoop) RequestRedirectByChannelChat(ctx context.Context, channelName, chatID, userID, instruction string) error {
	sessionID, err := al.commandSessionByChannelChat(channelName, chatID)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("there is no conversation here to redirect yet; send a message first")
	}
	if _, helperErr := al.helperSessionRecord(sessionID); errors.Is(helperErr, errNotHelperSession) {
		// An ordinary chat continues on this channel and chat.
		return al.redirectOrdinarySession(ctx, sessionID, instruction, userID, channelName, chatID)
	}
	return al.RedirectSessionTurn(ctx, sessionID, instruction, userID, channelName)
}
