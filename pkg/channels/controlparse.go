package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
)

type channelSessionStopper interface {
	RequestStopByChannelChat(ctx context.Context, channelName, chatID, userID string) (bool, bool, error)
}

type channelSessionRedirecter interface {
	RequestRedirectByChannelChat(ctx context.Context, channelName, chatID, userID, instruction string) error
}

// DispatchStopIfRecognized intercepts /stop before intake. It returns reply
// transport failures to its caller instead of interpreting them as success.
func DispatchStopIfRecognized(ctx context.Context, msg, channelName, chatID, senderID string, interceptor CancelRequester, sendFn func(context.Context, string, string) error) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(msg), "/stop") {
		return false, nil
	}
	err := fmt.Errorf("session Stop is unavailable; nothing was stopped")
	if stopper, ok := interceptor.(channelSessionStopper); ok {
		fired, armed, stopErr := stopper.RequestStopByChannelChat(ctx, channelName, chatID, senderID)
		switch {
		case stopErr != nil:
			err = stopErr
		case fired:
			err = nil
		case armed:
			err = commands.ErrCancelArmed
		default:
			err = commands.ErrNoActiveTurn
		}
	}
	if sendFn == nil {
		return true, fmt.Errorf("Stop reply transport is unavailable")
	}
	return true, sendFn(ctx, chatID, commands.StopCommandReply(err))
}

// DispatchRedirectIfRecognized is the redirect sibling of the existing cancel
// interception. No instruction reaches the ordinary message/steering intake.
func DispatchRedirectIfRecognized(ctx context.Context, msg, channelName, chatID, senderID string, interceptor CancelRequester, sendFn func(context.Context, string, string) error) (bool, error) {
	name, recognized := commands.CommandName(msg)
	if !recognized || name != "stop-redirect" {
		return false, nil
	}
	instruction := commands.StopRedirectInstruction(msg)
	reply := commands.StopRedirectUsage
	if instruction != "" {
		err := fmt.Errorf("redirect is unavailable; no instruction was applied")
		if redirecter, ok := interceptor.(channelSessionRedirecter); ok {
			err = redirecter.RequestRedirectByChannelChat(ctx, channelName, chatID, senderID, instruction)
		}
		reply = commands.StopRedirectReply(err)
	}
	if sendFn == nil {
		return true, fmt.Errorf("redirect reply transport is unavailable")
	}
	return true, sendFn(ctx, chatID, reply)
}

// interceptSessionControl runs after BaseChannel's sender allow check and before
// any intake side effect. All channel adapters share this authorization boundary.
func (c *BaseChannel) interceptSessionControl(ctx context.Context, content, chatID, senderID string) (bool, error) {
	send := func(ctx context.Context, chatID, text string) error {
		if c.owner == nil {
			return fmt.Errorf("channel reply sender is unavailable")
		}
		return c.owner.Send(ctx, bus.OutboundMessage{ChatID: chatID, Content: text})
	}
	handled, err := DispatchStopIfRecognized(ctx, content, c.Name(), chatID, senderID, c.cancelInterceptor, send)
	if !handled {
		handled, err = DispatchRedirectIfRecognized(ctx, content, c.Name(), chatID, senderID, c.cancelInterceptor, send)
	}
	if handled {
		return true, err
	}
	// The existing cancellation dispatcher reports action failures in its
	// reply. Capture a failed reply too, rather than reducing it to a log.
	var sendErr error
	cancelSend := func(ctx context.Context, chatID, text string) error {
		sendErr = send(ctx, chatID, text)
		return sendErr
	}
	handled = DispatchCancelIfRecognized(ctx, content, c.Name(), chatID, senderID, c.cancelInterceptor, cancelSend)
	return handled, sendErr
}
