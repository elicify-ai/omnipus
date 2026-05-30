package channels

import (
	"context"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// CancelInterceptor is the subset of the agent loop that Tier B channels need
// to fire a cancel. Defined here (pkg/channels) to avoid an import cycle with
// pkg/agent. The agent loop's *AgentLoop implements this interface.
type CancelInterceptor interface {
	// InterruptByChannelChat gracefully cancels all active turns whose channel
	// and chatID match. Returns nil when no active turn exists (no-op).
	// Deprecated: prefer RequestCancelByChannelChat which runs the full cancel
	// state machine (audit, transcript, abuse-detection, 2-stage timer).
	InterruptByChannelChat(channel, chatID, hint string) error
	// RequestCancelByChannelChat runs the full cancel state machine for the
	// turn identified by (channelName, chatID). All parameters are primitives to
	// avoid importing pkg/agent from pkg/channels (circular dependency).
	// Returns nil when no active turn exists (no-op).
	RequestCancelByChannelChat(ctx context.Context, channelName, chatID, userID string) error
}

// IsCancelCommand reports whether msg is exactly the /cancel command per FR-2:
// case-insensitive, whitespace-trimmed, whole-message equality only.
// It NEVER triggers on substrings or sentences that contain /cancel as a word.
func IsCancelCommand(msg string) bool {
	return strings.ToLower(strings.TrimSpace(msg)) == "/cancel"
}

// DispatchCancelIfRecognized checks whether msg is a /cancel command. If so it
// fires the graceful interrupt for the (channelName, chatID) pair, sends a
// confirmation via sendFn, and returns true — the caller must NOT pass the
// message to the agent loop.
//
// Returns false when msg is not a cancel command; the caller should dispatch
// normally.
//
// sendFn signature: func(ctx context.Context, chatID, text string) error.
// A nil sendFn is accepted (ack will be silently skipped).
//
// A nil interceptor is accepted (the cancel is a no-op but the function still
// returns true so the message is consumed, preventing it from reaching the
// agent loop with text "/cancel").
func DispatchCancelIfRecognized(
	ctx context.Context,
	msg, channelName, chatID, senderID string,
	interceptor CancelInterceptor,
	sendFn func(ctx context.Context, chatID, text string) error,
) bool {
	if !IsCancelCommand(msg) {
		return false
	}

	if interceptor != nil {
		// RequestCancelByChannelChat runs the full cancel state machine: audit,
		// transcript marking, abuse detection, and the 2-stage graceful→hard timer.
		// This supersedes the old InterruptByChannelChat path which skipped all of that.
		if err := interceptor.RequestCancelByChannelChat(ctx, channelName, chatID, senderID); err != nil {
			logger.WarnCF("channels", "cancel intercept error", map[string]any{
				"channel": channelName,
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
	}

	if sendFn != nil {
		if err := sendFn(ctx, chatID, "⏸ Canceling..."); err != nil {
			logger.WarnCF("channels", "cancel ack send failed", map[string]any{
				"channel": channelName,
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
	}

	return true
}

// CancelSendFn builds a sendFn closure from a Channel's Send method.
// This is a convenience constructor so each Tier B channel does not repeat
// the closure boilerplate.
func CancelSendFn(ch Channel) func(ctx context.Context, chatID, text string) error {
	return func(ctx context.Context, chatID, text string) error {
		return ch.Send(ctx, bus.OutboundMessage{ChatID: chatID, Content: text})
	}
}
