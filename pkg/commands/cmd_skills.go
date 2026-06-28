package commands

import (
	"context"
	"fmt"
	"strings"
)

// skillsCommand is the new canonical /skills command.
// Reuses the /list skills sub-handler logic.
// Surfaces: CLI, Channel (not web — the Skills screen covers this in the SPA).
func skillsCommand() Definition {
	return Definition{
		Name:        "skills",
		Description: "List installed skills",
		Usage:       "/skills",
		Surfaces:    []Surface{SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     listSkillsHandler(),
	}
}

// listSkillsHandler returns the handler shared between /skills and the deprecated
// /list skills sub-command.
func listSkillsHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		if rt == nil || rt.ListSkillNames == nil {
			return req.Reply(unavailableMsg)
		}
		names := rt.ListSkillNames()
		if len(names) == 0 {
			return req.Reply("No installed skills")
		}
		return req.Reply(fmt.Sprintf(
			"Installed Skills:\n- %s\n\nUse /skill <skill> <message> to force one for a single request, or /skill <skill> to apply it to your next message.",
			strings.Join(names, "\n- "),
		))
	}
}
