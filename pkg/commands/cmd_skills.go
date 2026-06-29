package commands

import (
	"context"
	"fmt"
	"strings"
)

// skillsCommand is the canonical /skills command.
// Surfaces: Web, CLI, Channel.
// On web: DeliveryClient — the SPA opens the partitioned skill-filter menu
// (showing only the Skills section). The backend Handler is still called for
// CLI/Channel surfaces where it lists skills as a text reply.
func skillsCommand() Definition {
	return Definition{
		Name:        "skills",
		Description: "List installed skills",
		Usage:       "/skills",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryClient,
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
			"Installed Skills:\n- %s\n\nUse /<skillname> <message> to activate a skill for a single request.",
			strings.Join(names, "\n- "),
		))
	}
}
