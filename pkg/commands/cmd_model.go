package commands

import (
	"context"
	"fmt"
)

// modelCommand is the new canonical /model command.
// - /model        → shows current model (reuses /show model logic)
// - /model <name> → switches model (reuses /switch model logic)
// Surfaces: all. Delivery: client (web sets the chat model selector locally).
func modelCommand() Definition {
	return Definition{
		Name:        "model",
		Description: "Show or switch the active model",
		Usage:       "/model [name]",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryClient,
		Handler:     modelHandler(),
	}
}

// modelHandler returns the /model command handler, shared between the new noun
// command and the deprecated /show model + /switch model sub-commands.
func modelHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		// Token 0 = "/model", token 1 = optional model name.
		arg := nthToken(req.Text, 1)

		if arg == "" {
			// Show current model — reuses /show model sub-handler logic.
			if rt == nil || rt.GetModelInfo == nil {
				return req.Reply(unavailableMsg)
			}
			name, provider := rt.GetModelInfo()
			return req.Reply(fmt.Sprintf("Current Model: %s (Provider: %s)", name, provider))
		}

		// Switch model — reuses /switch model sub-handler logic.
		if rt == nil || rt.SwitchModel == nil {
			return req.Reply(unavailableMsg)
		}
		oldModel, err := rt.SwitchModel(arg)
		if err != nil {
			return req.Reply(err.Error())
		}
		return req.Reply(fmt.Sprintf("Switched model from %s to %s", oldModel, arg))
	}
}
