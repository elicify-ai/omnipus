package commands

// goalCommand is the `/goal <condition>` slash command (ADR-049 D6, spec Part
// B US-8). Like the three memory commands (cmd_memory.go), it is
// agent-delivery and deliberately Handler: nil — see this file's doc comment
// there for why a Handler cannot do this work.
//
// Why nil Handler here specifically: setting a goal must run a REAL worker
// turn (round 1 IS the first judged turn), which only the agent loop's LLM
// call can do; a synchronous Handler replies immediately and never reaches
// the LLM (executor.go's OutcomePassthrough contract). pkg/agent/goal_loop.go's
// applyGoalCommandPrompt hook (mirroring applyMemoryCommandPrompt/
// applyExplicitSkillCommand's "rewrite opts.UserMessage, matched=true" shape)
// runs BEFORE the registered-command dispatch path and handles all four
// verbs itself:
//   - bare `/goal` (status) and `/goal clear|stop|cancel|off|reset|none`
//     (clear) are answered SYNCHRONOUSLY (matched=true, handled=true) — no
//     LLM call, deterministic reply text so `/goal status`'s BDD assertions
//     hold exactly.
//   - a MARKER-ONLY `/goal <condition>` persists the condition + round state
//     on the session's UnifiedMeta and rewrites opts.UserMessage to the
//     condition itself (matched=true, handled=false) so the turn continues to
//     the LLM as the goal's first round. A PROSE/mixed condition (ADR-074
//     D4a) instead compiles to a PENDING goal answered with an itemized echo
//     (matched=true, handled=true); `/goal confirm` (or a bare confirm reply)
//     activates it and runs round 1.
//   - a non-user-initiated turn (Gap #8/R6) is NOT matched at all — the text
//     passes through untouched as ordinary chat content.
func goalCommand() Definition {
	return Definition{
		Name:        "goal",
		Description: "Iterate until a condition is judged met — describe what \"done\" looks like",
		Usage:       "/goal <condition> | /goal | /goal confirm | /goal clear",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     nil,
	}
}

// GoalClearAliases lists the verbs that clear an active goal (FR-070). Used
// by pkg/agent/goal_loop.go's applyGoalCommandPrompt to recognize any of them
// as "clear", not just the literal word "clear".
func GoalClearAliases() []string {
	return []string{"clear", "stop", "off", "reset", "cancel", "none"}
}
