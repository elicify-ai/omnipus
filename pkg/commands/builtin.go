package commands

// BuiltinDefinitions returns all built-in command definitions.
// Each command group is defined in its own cmd_*.go file.
// Definitions are stateless — runtime dependencies are provided
// via the Runtime parameter passed to handlers at execution time.
//
// Canonical (visible) commands (10): clear, help, model, cancel,
// agents, tasks, skills, channels, status, config.
//
// Deprecated/hidden commands (kept for one-release back-compat): start, show,
// list, switch, check. These execute when invoked but are excluded from /help,
// channel menus, and GET /api/v1/commands.
//
// Removed (D1): /skill and /use are hard-removed; they are not hidden aliases.
// Typing /skill or /use now passes through as a normal chat message per D4.
func BuiltinDefinitions() []Definition {
	return []Definition{
		// Canonical commands — visible on their respective surfaces.
		clearCommand(),
		helpCommand(),
		modelCommand(),
		cancelCommand(),
		agentsCommand(),
		tasksCommand(),
		skillsCommand(),
		channelsCommand(),
		statusCommand(),
		configCommand(),

		// Deprecated commands — hidden but still execute (one-release back-compat).
		startCommand(),
		showCommand(),
		listCommand(),
		switchCommand(),
		checkCommand(),
	}
}
