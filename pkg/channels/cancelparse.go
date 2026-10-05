package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// CancelRequester is the cancellation dependency used by channel injection.
// Additional controls are separate capabilities and fail visibly if missing.
type CancelRequester interface {
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

// CancelInterceptor is the complete D9 control seam, implemented by AgentLoop.
// The primitive parameters keep channels independent of the agent package.
type CancelInterceptor interface {
	CancelRequester
	RequestStopByChannelChat(ctx context.Context, channelName, chatID, userID string) (fired bool, armed bool, err error)
	RequestRedirectByChannelChat(ctx context.Context, channelName, chatID, userID, instruction string) error
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
// A missing interceptor is reported as unavailable, never as a successful
// single-session fallback. The command is still consumed rather than forwarded
// to the model as ordinary text.
func DispatchCancelIfRecognized(
	ctx context.Context,
	msg, channelName, chatID, senderID string,
	interceptor CancelRequester,
	sendFn func(ctx context.Context, chatID, text string) error,
) bool {
	if !IsCancelCommand(msg) {
		return false
	}

	// The actual tree-stop outcome determines the reply; missing wiring and
	// real failures are visible and cannot be mislabeled as no work.
	var fired, armed bool
	cancelErr := fmt.Errorf("tree-scoped Stop all is unavailable; nothing was stopped")
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
		cancelErr = err
	}

	if sendFn != nil {
		ack := ackTextForCancelOutcome(fired, armed)
		if cancelErr != nil {
			ack = "Cancel request failed: " + cancelErr.Error()
		}
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
