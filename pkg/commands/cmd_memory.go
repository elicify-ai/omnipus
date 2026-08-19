package commands

// rememberCommand, recallCommand, and retrospectiveCommand are the three
// memory slash commands. All three are agent-delivery (Delivery:
// DeliveryAgent — the web SPA inserts "/name " as text and forwards it to the
// agent, same UX as /skill) and deliberately have Handler: nil.
//
// Why nil Handler (constraint, do not "fix" by adding one): a Definition's
// Handler runs synchronously inside Executor.executeDefinition and replies
// immediately (see executor.go), which short-circuits the turn BEFORE the LLM
// ever sees it. These three commands need the model itself to invoke a real
// tool (remember / recall_memory / recall_conversation / run_retrospective,
// all defined in pkg/tools/memory.go) and shape its reply from the tool's
// actual output — a Handler cannot do that. So each Definition here has
// Handler: nil, which makes Executor.executeDefinition return
// OutcomePassthrough (executor.go:69-71) and hands the turn to the agent
// loop. pkg/agent/loop.go's applyMemoryCommandPrompt hook (which calls
// MemoryCommandSteeringPrompt below) then rewrites the turn's user message
// into a steering prompt before the LLM call — the same "rewrite, don't
// reply" pattern applyExplicitSkillCommand uses for one-shot skill
// activation.
//
// The memory tools are registered for EVERY agent now; whether one may use
// them is its tool policy. (They were once withheld from the "main" sentinel
// by a hardcoded identity check — that agent and its gate are both gone.) The
// steering prompt still degrades gracefully for an agent whose policy denies
// them: the model reports it does not have the tool rather than the turn
// erroring.

func rememberCommand() Definition {
	return Definition{
		Name:        "remember",
		Description: "Store something in memory — describe what to keep",
		Usage:       "/remember <what to store>",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     nil,
	}
}

func recallCommand() Definition {
	return Definition{
		Name:        "recall",
		Description: "Recall from memory or this conversation",
		Usage:       "/recall <what to find>",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     nil,
	}
}

func retrospectiveCommand() Definition {
	return Definition{
		Name:        "retrospective",
		Description: "Run a session retrospective — optionally give it focus",
		Usage:       "/retrospective [focus]",
		Surfaces:    []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel},
		Delivery:    DeliveryAgent,
		Handler:     nil,
	}
}

// MemoryCommandSteeringPrompt returns the steering-prompt rewrite text for one
// of the three memory slash commands (remember, recall, retrospective) given
// the command name (as returned by CommandName) and its free-text argument
// (as returned by CommandArgs). ok is false when name is not one of the three,
// in which case prompt is "" and callers must leave the turn untouched.
//
// This is consumed by pkg/agent/loop.go's applyMemoryCommandPrompt hook. It
// lives here (not in pkg/agent) because pkg/commands must not import
// pkg/agent, while pkg/agent already imports pkg/commands — keeping the
// templates here avoids a layering violation and lets them be unit-tested
// alongside the rest of the command definitions.
func MemoryCommandSteeringPrompt(name, args string) (prompt string, ok bool) {
	switch name {
	case "remember":
		return rememberSteeringPrompt(args), true
	case "recall":
		return recallSteeringPrompt(args), true
	case "retrospective":
		return retrospectiveSteeringPrompt(args), true
	default:
		return "", false
	}
}

// rememberSteeringPrompt builds the steering prompt for /remember. With
// arguments, it directs the model to store exactly what the user typed. With
// no arguments, it asks the model to mine the conversation so far for
// anything worth keeping.
func rememberSteeringPrompt(args string) string {
	if args == "" {
		return "Review this conversation and use your 'remember' tool to store anything worth keeping (decisions, references, lessons learned)."
	}
	return "Use your 'remember' tool to store the following, choosing the most fitting category: " + args
}

// recallSteeringPrompt builds the steering prompt for /recall. With
// arguments, it lets the model pick recall_memory (durable MEMORY) vs.
// recall_conversation (this session) based on what the query is actually
// asking for. With no arguments, it steers the model to ask the user what
// they want recalled rather than guessing.
func recallSteeringPrompt(args string) string {
	if args == "" {
		return "Ask the user what they want to recall — something from their stored MEMORY, or something said earlier in THIS conversation — then use 'recall_memory' or 'recall_conversation' accordingly."
	}
	return "The user wants to recall: " + args +
		". If this refers to something from a past conversation or stored knowledge, use 'recall_memory'; " +
		"if it refers to something said earlier in THIS conversation, use 'recall_conversation'. " +
		"Then answer based on what you find."
}

// retrospectiveSteeringPrompt builds the steering prompt for /retrospective.
// The tool call is unconditional; user-supplied args are passed through as
// additional focus for the retrospective rather than replacing the base
// instruction.
func retrospectiveSteeringPrompt(args string) string {
	base := "Run your 'run_retrospective' tool now."
	if args == "" {
		return base
	}
	return base + "\n\nAdditional focus from the user: " + args
}
