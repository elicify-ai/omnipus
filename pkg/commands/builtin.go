package commands

// BuiltinDefinitions returns all built-in command definitions.
// Each command group is defined in its own cmd_*.go file.
// Definitions are stateless — runtime dependencies are provided
// via the Runtime parameter passed to handlers at execution time.
//
// Canonical commands: help, model, cancel, stop, stop-redirect, tasks,
// skills, channels, status, config, remember, recall, retrospective, goal,
// loop.
//
// The three memory commands (remember, recall, retrospective) are
// agent-delivery and Handler-less by design (see pkg/commands/cmd_memory.go):
// the agent loop rewrites the turn into a steering prompt so the model itself
// invokes the underlying memory tools, rather than a Handler replying inline.
// goal and loop (ADR-049, cmd_goal.go/cmd_loop.go) follow the SAME
// agent-delivery/Handler-nil shape via pkg/agent/loop.go's
// applyGoalCommandPrompt/applyLoopCommandPrompt rewrite hooks.
//
// Removed everywhere, no alias (FR-031; U10a, 2026-10-09): /skill and /use
// (D1); /new — founder ruling 2026-10-09: starting an extra chat is the SPA's
// local "New chat" action, so the server must not expose /new (which cleared
// server history); /clear — absent from the table until U10b ships the real
// main/extra-only safe-point clear (no interim hybrid /clear); /agents and its
// old selector/list action; and the deprecated /start, /show, /list, /switch,
// /check. Typing any of them is ordinary text and passes through to the agent
// (the executor returns Passthrough for an unregistered name).
func BuiltinDefinitions() []Definition {
	return []Definition{
		// Canonical commands — visible on their respective surfaces.
		helpCommand(),
		modelCommand(),
		cancelCommand(),
		stopCommand(),
		stopRedirectCommand(),
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
	}
}
