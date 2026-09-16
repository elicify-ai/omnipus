package commands

import (
	"context"
	"errors"
	"testing"
)

// TestNewCommand_ResolveAndExecuteByName covers the canonical /new command
// the way a user types it. /new was renamed from /clear (cmd_clear.go); a
// rename with no test behind it is how a command silently stops resolving.
func TestNewCommand_ResolveAndExecuteByName(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("new")
	if !ok {
		t.Fatal(`Lookup("new") not found — the canonical name must resolve`)
	}
	if def.Name != "new" {
		t.Fatalf(`Lookup("new").Name=%q, want "new"`, def.Name)
	}

	rt := &Runtime{ClearHistory: func() error { return nil }}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/new",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/new: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Chat history cleared!" {
		t.Fatalf("/new reply=%q, want %q", reply, "Chat history cleared!")
	}
}

// TestClearAlias_ExecutesNewCommand verifies the hidden /clear alias still
// resolves to the same definition and executes its handler (CLI/channel
// muscle memory). Dropping the Aliases entry in cmd_clear.go breaks this.
func TestClearAlias_ExecutesNewCommand(t *testing.T) {
	rt := &Runtime{ClearHistory: func() error { return nil }}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/clear",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/clear: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if res.Command != "new" {
		t.Fatalf("/clear resolved to command=%q, want \"new\"", res.Command)
	}
	if reply != "Chat history cleared!" {
		t.Fatalf("/clear reply=%q, want %q", reply, "Chat history cleared!")
	}
}

// TestNewCommand_ErrorAndUnavailablePaths covers the failure paths the
// handler itself implements: ClearHistory returning an error, and a Runtime
// without the ClearHistory dependency.
func TestNewCommand_ErrorAndUnavailablePaths(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{
		ClearHistory: func() error { return errors.New("disk full") },
	})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/new",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Failed to clear chat history: disk full" {
		t.Fatalf("reply=%q, want failure message", reply)
	}

	exNoDep := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})
	reply = ""
	res = exNoDep.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/new",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("no-dep outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("no-dep reply=%q, want %q", reply, unavailableMsg)
	}
}
