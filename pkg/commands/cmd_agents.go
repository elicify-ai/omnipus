package commands

// agentsCommand is the canonical /agents command.
// Surfaces: Web, CLI, Channel.
// On web: DeliveryClient — the SPA opens the in-header agent selector
// (mirroring how /model opens the model selector). The backend Handler is
// still called for CLI/Channel surfaces where it lists agents as a text reply.
func agentsCommand() Definition {
	return Definition{
		Name:        "agents",
		Description: "Switch the active agent",
		Usage:       "/agents",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryClient,
		Handler:     agentsHandler(),
	}
}
