package commands

import (
	"context"
	"fmt"
	"strings"
)

func helpCommand() Definition {
	return Definition{
		Name:        "help",
		Description: "Show available commands",
		Usage:       "/help",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryClient,
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			var defs []Definition
			if rt != nil && rt.ListDefinitions != nil {
				defs = rt.ListDefinitions()
			} else {
				defs = BuiltinDefinitions()
			}
			surface := SurfaceForChannel(req.Channel)
			return req.Reply(formatHelpMessage(defs, surface))
		},
	}
}

// formatHelpMessage renders a help text listing only the canonical (non-hidden)
// commands that are available on the given surface.
func formatHelpMessage(defs []Definition, surface Surface) string {
	visible := make([]Definition, 0, len(defs))
	for _, def := range defs {
		if def.Hidden {
			continue
		}
		if !def.AllowsSurface(surface) {
			continue
		}
		visible = append(visible, def)
	}

	if len(visible) == 0 {
		return "No commands available."
	}

	lines := make([]string, 0, len(visible))
	for _, def := range visible {
		usage := def.EffectiveUsage()
		if usage == "" {
			usage = "/" + def.Name
		}
		desc := def.Description
		if desc == "" {
			desc = "No description"
		}
		lines = append(lines, fmt.Sprintf("%s - %s", usage, desc))
	}
	return strings.Join(lines, "\n")
}
