package commands

// BuiltinDefinitions returns all built-in command definitions.
// Each command group is defined in its own cmd_*.go file.
// Definitions are stateless — runtime dependencies are provided
// via the Runtime parameter passed to handlers at execution time.
//
// Canonical (visible) commands (11): clear, help, model, skill, cancel,
// agents, tasks, skills, channels, status, config.
//
// Deprecated/hidden commands (kept for one-release back-compat): start, show,
// list, switch, check. These execute when invoked but are excluded from /help,
// channel menus, and GET /api/v1/commands.
func BuiltinDefinitions() []Definition {
	return []Definition{
		// Canonical commands — visible on their respective surfaces.
		clearCommand(),
		helpCommand(),
		modelCommand(),
		skillCommand(),
		cancelCommand(),
		agentsCommand(),
		tasksCommand(),
		skillsCommand(),
		channelsCommand(),
		statusCommand(),
		configCommand(),

		// Deprecated commands — hidden but still execute (one-release back-compat).
		// TODO(v0.2): remove these deprecated multiplexers + aliases after one release of back-compat (tracking issue to be filed).
		startCommand(),
		showCommand(),
		listCommand(),
		switchCommand(),
		checkCommand(),
	}
}
