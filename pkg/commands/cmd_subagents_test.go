package commands

import (
	"context"
	"testing"
)

// TestTasksCommand_ResolveAndExecuteTree covers the canonical /tasks command
// (renamed from /subagents): resolution by the typed name plus the
// string-tree rendering of an active turn.
func TestTasksCommand_ResolveAndExecuteTree(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("tasks")
	if !ok {
		t.Fatal(`Lookup("tasks") not found — the canonical name must resolve`)
	}
	if def.Name != "tasks" {
		t.Fatalf(`Lookup("tasks").Name=%q, want "tasks"`, def.Name)
	}

	rt := &Runtime{
		GetActiveTurn: func() any { return "root → child-1 → child-2" },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli", // /tasks is CLI+Channel surfaced; webchat passes through
		Text:    "/tasks",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/tasks: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	want := "🤖 **Active Subagents Tree**\n```text\nroot → child-1 → child-2\n```"
	if reply != want {
		t.Fatalf("/tasks reply=%q, want %q", reply, want)
	}
}

// TestTasksCommand_AliasExecution runs the deprecated /subagents alias on a
// surface where it is active, so the alias actually reaches the handler
// (existing coverage only ran it from webchat, where it passes through).
func TestTasksCommand_AliasExecution(t *testing.T) {
	rt := &Runtime{
		GetActiveTurn: func() any { return "root → child" },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/subagents",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/subagents: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if res.Command != "tasks" {
		t.Fatalf("/subagents resolved to command=%q, want \"tasks\"", res.Command)
	}
	if !contains(reply, "Active Subagents Tree") || !contains(reply, "root → child") {
		t.Fatalf("/subagents reply=%q, want tree render", reply)
	}
}

// TestTasksCommand_NoActiveTurn covers both no-turn shapes: a nil active
// turn and an empty-string tree both report no running tasks.
func TestTasksCommand_NoActiveTurn(t *testing.T) {
	for _, tc := range []struct {
		name string
		turn any
	}{
		{"nil turn", nil},
		{"empty tree string", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &Runtime{GetActiveTurn: func() any { return tc.turn }}
			ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

			var reply string
			res := ex.Execute(context.Background(), Request{
				Channel: "cli",
				Text:    "/tasks",
				Reply:   func(text string) error { reply = text; return nil },
			})
			if res.Outcome != OutcomeHandled {
				t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
			}
			if reply != "No active tasks running in this session." {
				t.Fatalf("reply=%q, want no-tasks message", reply)
			}
		})
	}
}

// TestTasksCommand_NonStringTurn covers the fallback rendering for an active
// turn value that is not a pre-rendered tree string.
func TestTasksCommand_NonStringTurn(t *testing.T) {
	rt := &Runtime{
		GetActiveTurn: func() any { return []string{"child-1", "child-2"} },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/tasks",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !contains(reply, "Active Subagents List") || !contains(reply, "child-1") {
		t.Fatalf("reply=%q, want list render with child IDs", reply)
	}
}

// TestTasksCommand_MissingDependency covers the unwired-Runtime branch —
// distinct from "no active turn".
func TestTasksCommand_MissingDependency(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/tasks",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Runtime does not support querying active turns." {
		t.Fatalf("reply=%q, want unsupported message", reply)
	}
}
