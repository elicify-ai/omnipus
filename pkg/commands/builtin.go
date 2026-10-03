package commands

// BuiltinDefinitions returns all built-in command definitions.
// Each command group is defined in its own cmd_*.go file.
// Definitions are stateless — runtime dependencies are provided
// via the Runtime parameter passed to handlers at execution time.
//
// Canonical (visible) commands (17): clear, help, model, cancel, stop,
// stop-redirect, agents, tasks, skills, channels, status, config, remember,
// recall, retrospective, goal, loop.
//
// stop and stop-redirect (ADR-20260928 D9) are their OWN commands — not
// aliases of /cancel, which keeps FR-5's no-aliases rule; no /steer alias
// exists (founder O4).
//
// The three memory commands (remember, recall, retrospective) are
// agent-delivery and Handler-less by design (see pkg/commands/cmd_memory.go):
// the agent loop rewrites the turn into a steering prompt so the model itself
// invokes the underlying memory tools, rather than a Handler replying inline.
// goal and loop (ADR-049, cmd_goal.go/cmd_loop.go) follow the SAME
// agent-delivery/Handler-nil shape via pkg/agent/loop.go's
// applyGoalCommandPrompt/applyLoopCommandPrompt rewrite hooks.
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
		stopCommand(),
		stopRedirectCommand(),
		agentsCommand(),
		tasksCommand(),
		skillsCommand(),
		channelsCommand(),
		statusCommand(),
		configCommand(),
		rememberCommand(),
		recallCommand(),
		retrospectiveCommand(),
		goalCommand(),
		loopCommand(),

		// Deprecated commands — hidden but still execute (one-release back-compat).
		startCommand(),
		showCommand(),
		listCommand(),
		switchCommand(),
		checkCommand(),
	}
}
