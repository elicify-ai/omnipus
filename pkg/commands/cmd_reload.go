package commands

import "context"

// configCommand is the canonical /config command (renamed from /reload).
// Surfaces: CLI, Channel (not web — the Settings screen covers config in the SPA).
func configCommand() Definition {
	return Definition{
		Name:        "config",
		Description: "Reload the configuration file",
		Usage:       "/config",
		Aliases:     []string{"reload"},
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.ReloadConfig == nil {
				return req.Reply(unavailableMsg)
			}
			if err := rt.ReloadConfig(); err != nil {
				return req.Reply("Failed to reload configuration: " + err.Error())
			}
			return req.Reply("Config reload triggered!")
		},
	}
}
