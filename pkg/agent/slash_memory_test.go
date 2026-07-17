package agent

// TestApplyMemoryCommandPrompt covers the steering-prompt rewrite hook for
// the three memory slash commands (/remember, /recall, /retrospective). The
// hook is applyMemoryCommandPrompt (pkg/agent/loop.go), which rewrites
// opts.UserMessage using commands.MemoryCommandSteeringPrompt
// (pkg/commands/cmd_memory.go) and lets the turn continue to the LLM
// (matched=true, handled=false) — mirroring applyExplicitSkillCommand's
// contract for one-shot skill activation (see slash_skill_test.go).

import (
	"strings"
	"testing"
)

// TestApplyMemoryCommandPrompt_Matrix verifies the rewrite for all three
// commands, with and without trailing args.
func TestApplyMemoryCommandPrompt_Matrix(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	type row struct {
		id          string
		input       string
		wantMatched bool
		wantHandled bool
		wantContain []string // substrings opts.UserMessage must contain after rewrite
	}

	rows := []row{
		{
			id: "remember-with-args", input: "/remember always use pnpm",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"always use pnpm", "'remember' tool"},
		},
		{
			id: "remember-bare", input: "/remember",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"'remember' tool"},
		},
		{
			id: "recall-with-args", input: "/recall what did we decide",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"what did we decide", "recall_memory", "recall_conversation"},
		},
		{
			id: "recall-bare", input: "/recall",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"recall_memory", "recall_conversation"},
		},
		{
			id: "retrospective-bare", input: "/retrospective",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"run_retrospective"},
		},
		{
			id: "retrospective-with-args", input: "/retrospective focus on the outage",
			wantMatched: true, wantHandled: false,
			wantContain: []string{"run_retrospective", "focus on the outage"},
		},
		// Non-memory builtin: must not match this hook at all.
		{id: "help-unaffected", input: "/help", wantMatched: false},
		{id: "clear-unaffected", input: "/clear", wantMatched: false},
		// Unknown slash text: must not match.
		{id: "unknown-unaffected", input: "/nonesuch hello", wantMatched: false},
		// Normal text (no slash): must not match.
		{id: "plain-text-unaffected", input: "hello there", wantMatched: false},
	}

	for _, r := range rows {
		t.Run(r.id, func(t *testing.T) {
			opts := &processOptions{
				SessionKey:  "test-session",
				UserMessage: r.input,
			}
			matched, handled, reply := al.applyMemoryCommandPrompt(r.input, opts)

			if matched != r.wantMatched {
				t.Fatalf("matched=%v, want %v (reply=%q)", matched, r.wantMatched, reply)
			}
			if !matched {
				// Unmatched rows must leave opts.UserMessage untouched.
				if opts.UserMessage != r.input {
					t.Errorf("UserMessage=%q, want unchanged %q", opts.UserMessage, r.input)
				}
				return
			}
			if handled != r.wantHandled {
				t.Fatalf("handled=%v, want %v", handled, r.wantHandled)
			}
			if reply != "" {
				t.Fatalf("reply=%q, want empty (turn continues to the LLM)", reply)
			}
			for _, sub := range r.wantContain {
				if !strings.Contains(opts.UserMessage, sub) {
					t.Errorf("UserMessage=%q, want to contain %q", opts.UserMessage, sub)
				}
			}
			// The rewritten message must differ from the raw slash command —
			// the model must never see the literal "/remember ..." token.
			if opts.UserMessage == r.input {
				t.Errorf("UserMessage unchanged (%q) — expected a steering-prompt rewrite", opts.UserMessage)
			}
		})
	}
}

// TestApplyMemoryCommandPrompt_Precedence pins the precedence between the
// skill hook (applyExplicitSkillCommand) and the memory hook
// (applyMemoryCommandPrompt), the same seam handleCommand wires them into:
// the skill hook runs first and, because remember/recall/retrospective are
// registered builtins (pkg/commands/cmd_memory.go), its own builtin-wins
// check (D3) makes it return matched=false for all three names — so the
// memory hook is what actually performs the rewrite. A skill sharing one of
// these names must not be able to shadow the builtin either.
func TestApplyMemoryCommandPrompt_Precedence(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	// Install a skill whose slug collides with a memory command name.
	writeSkillFile(t, cfg.Agents.Defaults.Workspace, "remember")

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	input := "/remember buy milk"
	opts := &processOptions{SessionKey: "test-session", UserMessage: input}

	// Step 1: the skill hook must defer (builtin wins, D3) — matched=false.
	skillMatched, _, _ := al.applyExplicitSkillCommand(input, agent, opts)
	if skillMatched {
		t.Fatal("applyExplicitSkillCommand must not match 'remember' — it is a registered builtin (D3 builtin-wins)")
	}
	if len(opts.ForcedSkills) > 0 {
		t.Fatalf("ForcedSkills must be empty when the builtin wins, got %v", opts.ForcedSkills)
	}
	if opts.UserMessage != input {
		t.Fatalf("UserMessage must be untouched by the skill hook, got %q", opts.UserMessage)
	}

	// Step 2: the memory hook runs next (same order as handleCommand) and
	// performs the actual rewrite.
	memMatched, memHandled, _ := al.applyMemoryCommandPrompt(input, opts)
	if !memMatched {
		t.Fatal("applyMemoryCommandPrompt must match '/remember'")
	}
	if memHandled {
		t.Fatal("applyMemoryCommandPrompt must not short-circuit (handled=false) — the turn must reach the LLM")
	}
	if !strings.Contains(opts.UserMessage, "buy milk") {
		t.Fatalf("UserMessage=%q must contain the verbatim args after rewrite", opts.UserMessage)
	}
}

// TestApplyMemoryCommandPrompt_MainAgentDegradesGracefully documents the
// known nuance that agentID "main" never receives the remember /
// recall_memory / run_retrospective tools (pkg/agent/instance.go registers
// them only for agentID != "main"). The rewrite hook itself is agent-agnostic
// — it operates purely on command text — so it still fires and produces a
// steering prompt even when called in a context that would resolve to the
// main agent; the model is left to report the missing capability rather than
// the turn erroring.
func TestApplyMemoryCommandPrompt_MainAgentDegradesGracefully(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	opts := &processOptions{SessionKey: "test-session", UserMessage: "/retrospective"}
	matched, handled, _ := al.applyMemoryCommandPrompt("/retrospective", opts)
	if !matched || handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if !strings.Contains(opts.UserMessage, "run_retrospective") {
		t.Fatalf(
			"UserMessage=%q must still contain the steering prompt regardless of which agent handles the turn",
			opts.UserMessage,
		)
	}
}
