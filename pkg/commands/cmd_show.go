package commands

import (
	"context"
	"fmt"
)

// showCommand is the deprecated /show multiplexer.
// Hidden=true: excluded from /help, channel menus, and GET /commands.
// Kept for one-release back-compat. All sub-handler logic is now reused by the
// canonical noun commands (/model, /agents, /channels, /status).
func showCommand() Definition {
	return Definition{
		Name:        "show",
		Description: "Show current configuration (deprecated — use /status, /model, /agents, or /channels)",
		Hidden:      true,
		SubCommands: []SubCommand{
			{
				Name:        "model",
				Description: "Current model and provider",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.GetModelInfo == nil {
						return req.Reply(unavailableMsg)
					}
					name, provider := rt.GetModelInfo()
					return req.Reply(fmt.Sprintf("Current Model: %s (Provider: %s)", name, provider))
				},
			},
			{
				Name:        "channel",
				Description: "Current channel",
				Handler: func(_ context.Context, req Request, _ *Runtime) error {
					return req.Reply(fmt.Sprintf("Current Channel: %s", req.Channel))
				},
			},
			{
				Name:        "agents",
				Description: "Registered agents",
				Handler:     agentsHandler(),
			},
		},
	}
}
