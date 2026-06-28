package commands

import (
	"fmt"
	"strings"
)

// SubCommand defines a single sub-command within a parent command.
type SubCommand struct {
	Name        string
	Description string
	ArgsUsage   string // optional, e.g. "<session-id>"
	Handler     Handler
}

// Definition is the single-source metadata and behavior contract for a slash command.
//
// Design notes:
//   - Every channel reads command shape from this type instead of keeping local copies.
//   - Surface-aware visibility: Surfaces controls which surfaces the command is active on.
//     An empty Surfaces slice means the command is available on all surfaces (back-compat).
//   - Platform menu registration (for example Telegram BotCommand) also derives from this
//     same definition so UI labels and runtime behavior stay aligned.
//   - Hidden commands (aliases/deprecated) are excluded from /help, GET /commands, and
//     channel menus, but still execute when invoked directly (back-compat for one release).
type Definition struct {
	Name        string
	Description string
	Usage       string // for simple commands; ignored when SubCommands is set
	Aliases     []string
	SubCommands []SubCommand // optional; when set, Executor routes to sub-command handlers
	Handler     Handler      // for simple commands without sub-commands

	// Surfaces lists the surfaces on which this command is active.
	// Empty = all surfaces (back-compat default).
	Surfaces []Surface

	// Delivery controls how the web SPA dispatches this command.
	// Only meaningful for web-surfaced commands; defaults to DeliveryAgent.
	Delivery DeliveryMode

	// Hidden marks deprecated commands and back-compat aliases that should not
	// appear in /help, the channel menu, or GET /api/v1/commands, but still
	// execute when invoked directly for one release.
	Hidden bool

	// AvailableWhileStreaming marks commands that can be invoked mid-turn
	// (while the agent is actively streaming a response). Only /cancel sets
	// this to true. Commands that do not set this field default to false.
	AvailableWhileStreaming bool
}

// EffectiveDelivery returns the delivery mode, defaulting to DeliveryAgent
// when none is set. Non-web commands omit Delivery (it is only meaningful
// for web-surfaced commands), so callers must use this method instead of
// reading Delivery directly to avoid emitting an empty string on the wire.
func (d Definition) EffectiveDelivery() DeliveryMode {
	if d.Delivery == "" {
		return DeliveryAgent
	}
	return d.Delivery
}

// EffectiveUsage returns the usage string. When SubCommands are present,
// it is auto-generated from sub-command names so metadata and behavior
// cannot drift.
func (d Definition) EffectiveUsage() string {
	if len(d.SubCommands) == 0 {
		return d.Usage
	}
	names := make([]string, 0, len(d.SubCommands))
	for _, sc := range d.SubCommands {
		name := sc.Name
		if sc.ArgsUsage != "" {
			name += " " + sc.ArgsUsage
		}
		names = append(names, name)
	}
	return fmt.Sprintf("/%s [%s]", d.Name, strings.Join(names, "|"))
}
