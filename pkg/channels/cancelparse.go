package channels

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
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

	// RequestRedirectByChannelChat is the redirect sibling of
	// RequestCancelByChannelChat (ADR-20260928 D9 row 2 via the architect's
	// corrected transport ruling §3.1): it runs the D2 redirect for the
	// session identified by (channelName, chatID) with instruction as the
	// newest instruction. All parameters are primitives to avoid importing
	// pkg/agent from pkg/channels (circular dependency).
	//
	// Guidance outcomes return as the commands package's named sentinels —
	// commands.ErrNotHelperSession (root-style refusal),
	// commands.ErrNothingToRedirect (already finished → "use RESUME") — so
	// DispatchRedirectIfRecognized can reply truthfully instead of one
	// opaque error.
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

// stopRedirectCommandToken is the /stop-redirect command word (D9). The
// matcher below requires it as the message's own first token so a sentence
// that merely contains "/stop-redirect" never triggers interception.
const stopRedirectCommandToken = "/stop-redirect"

// IsStopRedirectCommand reports whether msg is the /stop-redirect command
// (ADR-20260928 D9) — case-insensitive, whitespace-trimmed, first-token
// equality only, mirroring IsCancelCommand's whole-message rule (FR-2) for
// the one D9 command that carries an argument. The instruction is the
// Unicode-whitespace-trimmed remainder ("" for the bare form).
//
//	"/stop-redirect export the CSV" → (true, "export the CSV")
//	"/stop-redirect"                → (true, "")
//	"/stop-redirect\u00a0"             → (true, "")   (NBSP is Unicode whitespace)
//	"please /stop-redirect now"     → (false, "")  (not the first token)
func IsStopRedirectCommand(msg string) (matched bool, instruction string) {
	trimmed := strings.TrimSpace(msg)
	lower := strings.ToLower(trimmed)
	if lower == stopRedirectCommandToken {
		return true, ""
	}
	if !strings.HasPrefix(lower, stopRedirectCommandToken) {
		return false, ""
	}
	rest := trimmed[len(stopRedirectCommandToken):]
	if rest != "" {
		// The token must end at a whitespace boundary (or end-of-message),
		// never mid-word: "/stop-redirectx" and "/stop-redirect明确" are not
		// the command. The boundary rune is decoded as UTF-8 — not judged
		// from the raw first byte — so multi-byte non-ASCII words are
		// classified correctly.
		r, _ := utf8.DecodeRuneInString(rest)
		if !unicode.IsSpace(r) {
			return false, ""
		}
	}
	return true, strings.TrimFunc(rest, unicode.IsSpace)
}

// DispatchRedirectIfRecognized is DispatchCancelIfRecognized's D9 sibling:
// checks whether msg is a /stop-redirect command. If so it runs the redirect
// for the (channelName, chatID) session BEFORE the message can reach the
// agent loop's intake (mid-stream text would only enter the steering queue —
// the corrected transport ruling §3.1), sends the truthful outcome via
// sendFn, and returns true — the caller must NOT pass the message to the
// agent loop.
//
// Returns false when msg is not a redirect command; the caller should
// dispatch normally.
//
// A bare /stop-redirect (no instruction) is consumed with the usage reply
// and changes nothing (D9). A nil interceptor is accepted: the command is
// still consumed (it must never reach intake) and the reply says so
// honestly. A nil sendFn is accepted (the ack is skipped).
func DispatchRedirectIfRecognized(
	ctx context.Context,
	msg, channelName, chatID, senderID string,
	interceptor CancelInterceptor,
	sendFn func(ctx context.Context, chatID, text string) error,
) bool {
	matched, instruction := IsStopRedirectCommand(msg)
	if !matched {
		return false
	}

	ack := redirectUsageReplyText
	switch {
	case instruction == "":
		// Bare form — usage only, change nothing (D9).
	case interceptor == nil:
		ack = "Redirect is not available: no handler is wired for this channel."
	default:
		err := interceptor.RequestRedirectByChannelChat(ctx, channelName, chatID, senderID, instruction)
		ack = ackTextForRedirectOutcome(err)
	}

	if sendFn != nil {
		if err := sendFn(ctx, chatID, ack); err != nil {
			logger.WarnCF("channels", "redirect ack send failed", map[string]any{
				"channel": channelName,
				"chat_id": chatID,
				"error":   err.Error(),
			})
		}
	}
	return true
}

// redirectUsageReplyText mirrors the Tier A usage reply (cmd_stop.go) for
// the bare /stop-redirect form. Kept as its own constant (not an import of
// the unexported command-layer one) so the two surfaces stay free to word
// their replies for their audiences while pinning the same contract: name
// the command, change nothing.
const redirectUsageReplyText = "/stop-redirect <instruction> — stop this helper's current turn, then continue it with <instruction> as the newest instruction (its helpers keep working)."

// ackTextForRedirectOutcome chooses the user-facing reply for a Tier B
// redirect from the primitive's outcome, mirroring Tier A's /stop-redirect
// handler (cmd_stop.go): guidance sentinels reply as guidance, everything
// else surfaces the failure.
func ackTextForRedirectOutcome(err error) string {
	switch {
	case err == nil:
		return "⏪ Redirecting — stopping this helper's current turn, then continuing with your instruction (its helpers keep working)."
	case errors.Is(err, commands.ErrNothingToRedirect):
		// done/failed target (D2 stop table) — guidance, not a failure.
		return "already finished — use RESUME"
	case errors.Is(err, commands.ErrNotHelperSession):
		return "/stop-redirect works only in a helper session's chat — this conversation is not a steered helper session."
	default:
		return "Redirect failed: " + err.Error()
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
