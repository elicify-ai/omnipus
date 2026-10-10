package commands

import (
	"context"
	"strings"
	"testing"
)

func findDefinitionByName(t *testing.T, defs []Definition, name string) Definition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("missing /%s definition", name)
	return Definition{}
}

// TestBuiltinHelpHandler_ReturnsFormattedMessage verifies /help lists the
// canonical noun commands (not the hidden/removed ones) for the caller's surface.
// Updated for D1 (/skill and /use hard-removed), for U10a (2026-10-09), which
// removed /new, /agents, /start, /show, /list, /switch and /check from the
// table entirely (FR-031), and for U10b (2026-10-10), which restored /clear as
// a canonical command. The canonical CLI set is now 12 for the
// asserted names (help/model/clear/cancel/tasks/skills/channels/status/config/
// remember/recall/retrospective; stop, stop-redirect, goal and loop are also
// canonical and all-surface).
func TestBuiltinHelpHandler_ReturnsFormattedMessage(t *testing.T) {
	defs := BuiltinDefinitions()
	helpDef := findDefinitionByName(t, defs, "help")
	if helpDef.Handler == nil {
		t.Fatalf("/help handler should not be nil")
	}

	var reply string
	err := helpDef.Handler(context.Background(), Request{
		Channel: "cli",
		Text:    "/help",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("/help handler error: %v", err)
	}

	// Canonical commands must appear. /clear is canonical again since U10b
	// shipped the real /clear (FR-030/031, founder ruling 2026-10-09): it moves
	// the context window in the same session and preserves the transcript.
	for _, name := range []string{"help", "model", "clear", "cancel", "tasks", "skills", "channels", "status", "config", "remember", "recall", "retrospective"} {
		if !strings.Contains(reply, "/"+name) {
			t.Errorf("/help cli: missing /%s in output:\n%s", name, reply)
		}
	}

	// Removed, hidden/deprecated and alias names must NOT appear as their own
	// command entry. /clear is NOT in this list: U10b restored it as a real
	// canonical command (FR-030).
	for _, name := range []string{"show", "list", "switch", "check", "start", "use", "skill", "subagents", "reload", "new", "agents", "channel", "resume"} {
		// Check that the name doesn't appear as a /name entry (it could appear in descriptions)
		// We check for the usage-format "/<name> -" or a leading "/<name>".
		if strings.Contains(reply, "/"+name+" -") || strings.HasPrefix(reply, "/"+name) {
			t.Errorf("/help cli: /%s must not appear as a command in output:\n%s", name, reply)
		}
	}
}

// TestBuiltinNoRemovedCommandTableEntries verifies that the command-table cut of
// U10a is complete: none of the retired names resolves as a built-in command
// name or alias (FR-031: deleted everywhere, no alias). This replaces the
// back-compat tests that asserted the old hidden /show, /list, /switch, /check
// and /start commands still executed — that behaviour is removed by the same
// unit that removes the commands.
func TestBuiltinNoRemovedCommandTableEntries(t *testing.T) {
	// "clear" is deliberately absent: U10b (FR-030/031) restored /clear as a
	// canonical command, so it is no longer a retired table entry.
	removed := []string{"new", "agents", "start", "show", "list", "switch", "check", "channel", "resume"}
	reg := NewRegistry(BuiltinDefinitions())
	for _, name := range removed {
		if def, found := reg.Lookup(name); found {
			t.Errorf("retired name %q still resolves to /%s (FR-031: deleted everywhere, no alias)", name, def.Name)
		}
	}
}

// TestBuiltinNoSkillOrUseCommand verifies that /skill and /use are NOT registered
// in the built-in command set (D1 hard removal). The executor must return Passthrough
// (no-op) for both — they are treated as unknown text by the executor, which means
// they flow to the agent loop as normal messages (D4).
func TestBuiltinNoSkillOrUseCommand(t *testing.T) {
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	for _, text := range []string{"/skill shell run ls", "/use shell run ls"} {
		res := ex.Execute(context.Background(), Request{
			Channel: "telegram",
			Text:    text,
		})
		if res.Outcome != OutcomePassthrough {
			t.Errorf("%q: executor outcome=%v, want Passthrough (not a registered command, D1)", text, res.Outcome)
		}
		// Command field should be empty or the name parsed from the text; either
		// way, neither "skill" nor "use" should be registered.
		reg := NewRegistry(defs)
		if _, found := reg.Lookup("skill"); found {
			t.Error("Registry must not contain 'skill' command (D1 hard removal)")
		}
		if _, found := reg.Lookup("use"); found {
			t.Error("Registry must not contain 'use' command (D1 hard removal)")
		}
	}
}
