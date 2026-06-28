package commands

import (
	"context"
	"fmt"
	"strings"
)

// channelsCommand is the new canonical /channels command.
// - /channels          → lists enabled channels (reuses /list channels logic)
// - /channels <name>   → checks channel availability (reuses /check channel logic)
// Surfaces: CLI, Channel (not web — the Channels screen covers this in the SPA).
func channelsCommand() Definition {
	return Definition{
		Name:        "channels",
		Description: "List enabled channels or check a channel's availability",
		Usage:       "/channels [name]",
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Handler:     channelsHandler(),
	}
}

// channelsHandler returns the /channels command handler, shared between the new
// noun command and the deprecated /show channel, /list channels, /check channel.
func channelsHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		// Token 0 = "/channels", token 1 = optional channel name to check.
		arg := nthToken(req.Text, 1)

		if arg == "" {
			// List enabled channels — reuses /list channels sub-handler logic.
			if rt == nil || rt.GetEnabledChannels == nil {
				return req.Reply(unavailableMsg)
			}
			enabled := rt.GetEnabledChannels()
			if len(enabled) == 0 {
				return req.Reply("No channels enabled")
			}
			return req.Reply(fmt.Sprintf("Enabled Channels:\n- %s", strings.Join(enabled, "\n- ")))
		}

		// Check channel availability — reuses /check channel sub-handler logic.
		if rt == nil || rt.SwitchChannel == nil {
			return req.Reply(unavailableMsg)
		}
		if err := rt.SwitchChannel(arg); err != nil {
			return req.Reply(err.Error())
		}
		return req.Reply(fmt.Sprintf("Channel '%s' is available and enabled", arg))
	}
}
