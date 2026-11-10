package commands

import (
	"context"
	"errors"
)

func cancelCommand() Definition {
	return Definition{
		Name:                    "cancel",
		Description:             "Stop this session and every helper under it",
		Usage:                   "/cancel",
		Surfaces:                []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:                DeliveryClient,
		AvailableWhileStreaming: true,
		// No aliases. D9 adds /stop and /stop-redirect as separate commands;
		// /cancel remains the explicit tree-scoped Stop-all confirmation.
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil {
				return req.Reply(unavailableMsg)
			}

			var sessionID string
			if rt.SessionID != nil {
				sessionID = rt.SessionID()
			}

			canceller := Canceller{
				UserID:  req.SenderID,
				Channel: req.Channel,
			}

			err := rt.CancelActiveTurn(ctx, sessionID, canceller)
			switch {
			case err == nil:
				// Interrupt successfully fired.
				return req.Reply("⏸ Canceling...")
			case errors.Is(err, ErrCancelArmed):
				// Nothing was running YET, but the cancel was acknowledged and
				// will fire the instant a turn registers for this session —
				// honest middle ground between "canceling now" and "nothing
				// to cancel" (neither of which is true here).
				return req.Reply("⏸ Cancel acknowledged — nothing is running yet, but it will stop the instant it starts.")
			case errors.Is(err, ErrNoActiveTurn):
				// Informational — nothing was running; not a failure.
				return req.Reply("Nothing to stop.")
			default:
				// Real failure (e.g., fsync error, lock contention).
				return req.Reply("Cancel request failed: " + err.Error())
			}
		},
	}
}
