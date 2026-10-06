package commands

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

// StopRedirectUsage explains the command's required instruction.
const StopRedirectUsage = "Usage: `/stop-redirect <instruction>` — stops this chat's current turn and continues this chat with your new instruction. The instruction text is required; whitespace alone is not an instruction."

// StopCommandReply keeps fired, armed, no-op and failure outcomes distinct.
func StopCommandReply(err error) string {
	switch {
	case err == nil:
		return "Stop requested."
	case errors.Is(err, ErrCancelArmed):
		return "Stop acknowledged — nothing is running yet, but it will stop the instant it starts."
	case errors.Is(err, ErrNoActiveTurn):
		return "Nothing to stop"
	default:
		return "Stop request failed: " + err.Error()
	}
}

// StopRedirectReply maps the redirect primitive's guidance and failure outcomes.
func StopRedirectReply(err error) string {
	switch {
	case err == nil:
		return "Redirect accepted: this chat's current turn is being stopped and the chat continues with your new instruction."
	case errors.Is(err, ErrNothingToRedirect):
		return "This chat has already finished — use RESUME to start its next round."
	default:
		return "Redirect request failed: " + err.Error()
	}
}

// StopRedirectInstruction trims only the command token and surrounding space;
// newlines and spacing inside the instruction remain the user's own text.
func StopRedirectInstruction(text string) string {
	text = strings.TrimSpace(text)
	end := strings.IndexFunc(text, unicode.IsSpace)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(text[end:])
}

func stopCommand() Definition {
	return Definition{
		Name: "stop", Description: "Stop only this conversation's current turn",
		Usage: "/stop", Surfaces: []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery: DeliveryClient, AvailableWhileStreaming: true,
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil {
				return req.Reply(unavailableMsg)
			}
			var sessionID string
			if rt.SessionID != nil {
				sessionID = rt.SessionID()
			}
			err := rt.StopSessionTurn(ctx, sessionID, Canceller{UserID: req.SenderID, Channel: req.Channel})
			return req.Reply(StopCommandReply(err))
		},
	}
}

func stopRedirectCommand() Definition {
	return Definition{
		Name: "stop-redirect", Description: "Stop this chat's current turn and continue it with a new instruction",
		Usage: "/stop-redirect <instruction>", ArgumentHint: "<instruction>", Surfaces: []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery: DeliveryClient, AvailableWhileStreaming: true,
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			instruction := StopRedirectInstruction(req.Text)
			if instruction == "" {
				return req.Reply(StopRedirectUsage)
			}
			if rt == nil {
				return req.Reply(unavailableMsg)
			}
			var sessionID string
			if rt.SessionID != nil {
				sessionID = rt.SessionID()
			}
			err := rt.RedirectSessionTurn(ctx, sessionID, instruction, Canceller{UserID: req.SenderID, Channel: req.Channel})
			return req.Reply(StopRedirectReply(err))
		},
	}
}
