package commands

import (
	"context"
	"errors"
)

// ClearRefusedError is returned by Runtime.ClearHistory when the session is not
// one /clear may act on (FR-030: only a main or extra chat; every helper,
// worker, delegate and task-child session refuses). Reason is the plain
// explanation shown to the person on every surface; nothing was changed.
type ClearRefusedError struct{ Reason string }

func (e *ClearRefusedError) Error() string { return e.Reason }

// clearCommand is the canonical /clear (FR-030/031, U10b). It moves the current
// conversation's context window forward in the SAME session: the model starts
// from an empty context, the transcript and archive are kept whole, and no
// session is created. /new is retired and has no alias here.
//
// Delivery is DeliveryAgent: the server executes it, so the palette, a typed
// command, a stale-menu direct call and every channel reach the one handler and
// the one eligibility rule.
func clearCommand() Definition {
	return Definition{
		Name:        "clear",
		Description: "Clear this chat's context; the transcript is kept",
		Usage:       "/clear",
		Delivery:    DeliveryAgent,
		Handler:     clearHandler(),
	}
}

func clearHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		if rt == nil || rt.ClearHistory == nil {
			return req.Reply(unavailableMsg)
		}
		if err := rt.ClearHistory(); err != nil {
			var refused *ClearRefusedError
			if errors.As(err, &refused) {
				return req.Reply(refused.Reason)
			}
			return err
		}
		return req.Reply("Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.")
	}
}
