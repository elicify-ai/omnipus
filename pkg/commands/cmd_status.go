package commands

import (
	"context"
	"fmt"
	"strings"
)

// statusCommand is the new canonical /status command.
// Reuses the bare /show handler logic (show model + channel + agents summary).
// Surfaces: CLI, Channel (not web — the SPA has dedicated status indicators).
func statusCommand() Definition {
	return Definition{
		Name:        "status",
		Description: "Show current model, channel, and agent summary",
		Usage:       "/status",
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     statusHandler(),
	}
}

// statusHandler returns the /status command handler, which reuses the sub-handler
// logic from the deprecated /show multiplexer's bare invocation path.
func statusHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		parts := make([]string, 0, 3)

		// Model info (mirrors /show model sub-handler)
		if rt != nil && rt.GetModelInfo != nil {
			name, provider := rt.GetModelInfo()
			parts = append(parts, fmt.Sprintf("Model: %s (Provider: %s)", name, provider))
		}

		// Channel info (mirrors /show channel sub-handler)
		parts = append(parts, fmt.Sprintf("Channel: %s", req.Channel))

		// Agents (mirrors /show agents sub-handler / agentsHandler)
		if rt != nil && rt.ListAgentIDs != nil {
			ids := rt.ListAgentIDs()
			if len(ids) > 0 {
				parts = append(parts, fmt.Sprintf("Agents: %s", strings.Join(ids, ", ")))
			} else {
				parts = append(parts, "Agents: none registered")
			}
		}

		if len(parts) == 0 {
			return req.Reply(unavailableMsg)
		}
		return req.Reply(strings.Join(parts, "\n"))
	}
}
