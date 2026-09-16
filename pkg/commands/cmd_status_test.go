package commands

import (
	"context"
	"testing"
)

// TestStatusCommand_ResolveAndExecute covers the canonical /status command —
// resolution by the typed name and the full summary reply.
func TestStatusCommand_ResolveAndExecute(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("status")
	if !ok {
		t.Fatal(`Lookup("status") not found — the canonical name must resolve`)
	}
	if def.Name != "status" {
		t.Fatalf(`Lookup("status").Name=%q, want "status"`, def.Name)
	}

	rt := &Runtime{
		GetModelInfo: func() (string, string) { return "gpt-4o", "openrouter" },
		ListAgentIDs: func() []string { return []string{"default", "coder"} },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli", // /status is CLI+Channel surfaced
		Text:    "/status",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/status: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	want := "Model: gpt-4o (Provider: openrouter)\nChannel: cli\nAgents: default, coder"
	if reply != want {
		t.Fatalf("/status reply=%q, want %q", reply, want)
	}
}

// TestStatusCommand_NoAgentsRegistered covers the empty-roster line.
func TestStatusCommand_NoAgentsRegistered(t *testing.T) {
	rt := &Runtime{
		GetModelInfo: func() (string, string) { return "m", "p" },
		ListAgentIDs: func() []string { return nil },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/status",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !contains(reply, "Agents: none registered") {
		t.Fatalf("reply=%q, want none-registered agents line", reply)
	}
}

// TestStatusCommand_NoDependencies covers a Runtime with nothing wired: the
// channel line is still reportable, so the command must NOT reply with the
// unavailable message.
func TestStatusCommand_NoDependencies(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/status",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Channel: cli" {
		t.Fatalf("reply=%q, want %q", reply, "Channel: cli")
	}
}
