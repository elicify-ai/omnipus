package channels

import (
	"context"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// CancelInterceptor is the subset of the agent loop that Tier B channels need
// to fire a cancel. Defined here (pkg/channels) to avoid an import cycle with
// pkg/agent. The agent loop's *AgentLoop implements this interface.
type CancelInterceptor interface {
	// RequestCancelByChannelChat runs the full cancel state machine for the
	// turn identified by (channelName, chatID). All parameters are primitives to
	// avoid importing pkg/agent from pkg/channels (circular dependency).
	//
	// Returns (fired, armed, err): fired is true when an active turn was
	// claimed; armed is true when no turn registered yet but a pre-registration
	// latch was recorded (the cancel WILL fire the instant a turn registers);
	// err is non-nil only for parameter validation failures. The (fired, armed)
	// pair lets DispatchCancelIfRecognized choose honest ack text — Defect #29:
	// the prior bare-error return made every cancel look like "Canceling..."
	// regardless of outcome.
	RequestCancelByChannelChat(ctx context.Context, channelName, chatID, userID string) (fired bool, armed bool, err error)
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

	// Defect #29: the cancel outcome determines the ack text. A nil
	// interceptor yields (fired=false, armed=false) → "Nothing to cancel",
	// matching the honest no-op the nil case actually is.
	var fired, armed bool
	if interceptor != nil {
		// RequestCancelByChannelChat runs the full cancel state machine: audit,
		// transcript marking, abuse detection, and the 2-stage graceful→hard timer.
		f, a, err := interceptor.RequestCancelByChannelChat(ctx, channelName, chatID, senderID)
		if err != nil {
			logger.WarnCF("channels", "cancel intercept error", map[string]any{
				"channel": channelName,
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
		fired = f
		armed = a
	}

	if sendFn != nil {
		ack := ackTextForCancelOutcome(fired, armed)
		if err := sendFn(ctx, chatID, ack); err != nil {
			logger.WarnCF("channels", "cancel ack send failed", map[string]any{
				"channel": channelName,
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
	}

	return true
}

// ackTextForCancelOutcome chooses the user-facing ack text for a Tier B cancel
// based on the (fired, armed) outcome — mirroring Tier A's /cancel wording
// (cmd_cancel.go) so a Tier B user sees the same honest feedback:
//
//   - fired  → "⏸ Canceling..." (the cascade is running)
//   - armed  → "⏸ Cancel acknowledged — nothing is running yet, but it will
//     stop the instant it starts." (a latch stands in; the next turn to
//     register will be canceled)
//   - neither → "Nothing to cancel" (genuine no-op)
//
// armed is NEVER true when fired is true (see CancelOutcome.Armed's contract),
// so the case order is safe.
func ackTextForCancelOutcome(fired, armed bool) string {
	switch {
	case fired:
		return "⏸ Canceling..."
	case armed:
		return "⏸ Cancel acknowledged — nothing is running yet, but it will stop the instant it starts."
	default:
		return "Nothing to cancel"
	}
}

// CancelSendFn builds a sendFn closure from a Channel's Send method.
// This is a convenience constructor so each Tier B channel does not repeat
// the closure boilerplate.
func CancelSendFn(ch Channel) func(ctx context.Context, chatID, text string) error {
	return func(ctx context.Context, chatID, text string) error {
		return ch.Send(ctx, bus.OutboundMessage{ChatID: chatID, Content: text})
	}
}
