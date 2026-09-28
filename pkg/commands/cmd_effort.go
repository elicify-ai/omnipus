package commands

import (
	"context"
	"fmt"
	"strings"
)

// effortCommand registers /effort — the conversation-scoped reasoning-effort
// command (thinking-reasoning-spec.md §9.5, D27/D30).
//
//   - /effort         → report the conversation's stored effort (or "Default")
//   - /effort <level> → store the level on THIS conversation's session meta
//   - /effort default → clear the stored effort (provider default applies)
//
// On CLI and messenger channels the handler reads/patches
// SessionMeta.ReasoningEffort for commands.Runtime.SessionID through the
// conversation-effort callbacks on Runtime. On web the value is per-message
// picker state, so the server-side handler here never touches session state —
// the web slash palette handles the command client-side (a later frontend
// dispatch wires the palette entry).
//
// /effort MUST NOT call Runtime.SwitchModel, ApplyAgentModel, an agent PUT,
// or any agent-config writer — those are today's agent-wide /model path and
// are explicitly the non-path (§2.2). A conversation model change clears the
// stored effort; see pkg/agent/loop_slash.go::buildCommandsRuntime.
func effortCommand() Definition {
	return Definition{
		Name:        "effort",
		Description: "Show or set the conversation's reasoning effort (default | minimal | low | medium | high)",
		Usage:       "/effort [level|default]",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryClient,
		Handler:     effortHandler(),
	}
}

// effortHandler returns the /effort command handler. Mirrors modelHandler's
// shape: token 1 carries the optional argument; an unwired runtime gets the
// package's standard unavailable reply.
func effortHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		// D23/D30: web effort is per-message picker state — the server-side
		// handler must never write session state for a webchat request. The
		// web slash palette implements the command client-side.
		if SurfaceForChannel(req.Channel) == SurfaceWeb {
			return req.Reply("Reasoning effort on web is set per message from the model picker; nothing is stored on the session.")
		}

		arg := nthToken(req.Text, 1)

		if rt == nil || rt.GetConversationEffort == nil || rt.SetConversationEffort == nil {
			return req.Reply(unavailableMsg)
		}

		switch {
		case arg == "":
			// /effort — report the stored value (or "Default").
			value, err := rt.GetConversationEffort()
			if err != nil {
				return req.Reply(fmt.Sprintf("Failed to read reasoning effort: %v", err))
			}
			if value == "" {
				return req.Reply("Reasoning effort: Default")
			}
			return req.Reply(fmt.Sprintf("Reasoning effort: %s", value))

		case strings.EqualFold(arg, "default"):
			// /effort default — clear (empty string through the callback).
			if err := rt.SetConversationEffort(""); err != nil {
				return req.Reply(fmt.Sprintf("Failed to clear reasoning effort: %v", err))
			}
			return req.Reply("Reasoning effort set to default (provider default applies)")

		default:
			if err := rt.SetConversationEffort(arg); err != nil {
				return req.Reply(fmt.Sprintf("Failed to set reasoning effort: %v", err))
			}
			return req.Reply(fmt.Sprintf("Reasoning effort set to %s", arg))
		}
	}
}
