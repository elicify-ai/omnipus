package commands

import "context"

// startCommand is the /start greeting command.
// Hidden=true: excluded from /help, channel menus, and GET /commands.
// Kept for Telegram bot compatibility (the platform sends /start on first contact)
// but not shown as a user-facing command.
func startCommand() Definition {
	return Definition{
		Name:        "start",
		Description: "Start the bot",
		Usage:       "/start",
		Hidden:      true,
		Handler: func(_ context.Context, req Request, _ *Runtime) error {
			return req.Reply("Hello! I am Omnipus 🐙")
		},
	}
}
