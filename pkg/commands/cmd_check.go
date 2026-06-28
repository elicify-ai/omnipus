package commands

import (
	"context"
	"fmt"
)

// checkCommand is the deprecated /check multiplexer.
// Hidden=true: excluded from /help, channel menus, and GET /commands.
// Kept for one-release back-compat. The /check channel logic is now reused by /channels.
func checkCommand() Definition {
	return Definition{
		Name:        "check",
		Description: "Check channel availability (deprecated — use /channels <name>)",
		Hidden:      true,
		SubCommands: []SubCommand{
			{
				Name:        "channel",
				Description: "Check if a channel is available",
				ArgsUsage:   "<name>",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.SwitchChannel == nil {
						return req.Reply(unavailableMsg)
					}
					value := nthToken(req.Text, 2)
					if value == "" {
						return req.Reply("Usage: /check channel <name>")
					}
					if err := rt.SwitchChannel(value); err != nil {
						return req.Reply(err.Error())
					}
					return req.Reply(fmt.Sprintf("Channel '%s' is available and enabled", value))
				},
			},
		},
	}
}
