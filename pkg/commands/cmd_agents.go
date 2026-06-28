package commands

// agentsCommand is the new canonical /agents command.
// Reuses the shared agentsHandler() (same as /show agents + /list agents).
// Surfaces: CLI, Channel (not web — the Agents screen covers this in the SPA).
func agentsCommand() Definition {
	return Definition{
		Name:        "agents",
		Description: "List registered agents",
		Usage:       "/agents",
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     agentsHandler(),
	}
}
