package commands

// skillCommand is the canonical /skill command (renamed from /use).
// Surfaces: all. Delivery: agent (SPA inserts text and forwards to the agent).
func skillCommand() Definition {
	return Definition{
		Name:        "skill",
		Description: "Force a specific installed skill for one request",
		Usage:       "/skill <name> [message]",
		Aliases:     []string{"use"},
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		// No backend handler: the agent loop resolves skill by name from the
		// injected text (existing behavior, unchanged). The SPA forwards /skill
		// as a message frame; CLI/channel pipelines pass through to the agent.
	}
}
