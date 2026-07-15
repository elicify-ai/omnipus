package commands

import "context"

func clearCommand() Definition {
	return Definition{
		Name:        "new",
		Description: "Start a new chat (web: local session; CLI/channel: clear server history)",
		Usage:       "/new",
		// "clear" survives as a hidden alias — CLI/channel muscle memory keeps
		// working, but /help and the web palette advertise only /new.
		Aliases:  []string{"clear"},
		Surfaces: []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery: DeliveryClient,
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.ClearHistory == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.ClearHistory(); err != nil {
				return req.Reply("Failed to clear chat history: " + err.Error())
			}
			return req.Reply("Chat history cleared!")
		},
	}
}
