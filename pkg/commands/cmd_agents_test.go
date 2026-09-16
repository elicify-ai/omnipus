package commands

import (
	"context"
	"testing"
)

// TestAgentsCommand_ResolveAndExecute covers the canonical /agents noun
// command. Its handler was previously only reachable through the deprecated
// /show agents and /list agents sub-commands — /agents itself had no test.
func TestAgentsCommand_ResolveAndExecute(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("agents")
	if !ok {
		t.Fatal(`Lookup("agents") not found — the canonical name must resolve`)
	}
	if def.Name != "agents" {
		t.Fatalf(`Lookup("agents").Name=%q, want "agents"`, def.Name)
	}

	rt := &Runtime{
		ListAgentIDs: func() []string { return []string{"default", "coder"} },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/agents",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/agents: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Registered agents: default, coder" {
		t.Fatalf("/agents reply=%q, want registered agents", reply)
	}
}

// TestAgentsCommand_EmptyList covers the empty-roster reply.
func TestAgentsCommand_EmptyList(t *testing.T) {
	rt := &Runtime{ListAgentIDs: func() []string { return nil }}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/agents",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "No agents registered" {
		t.Fatalf("reply=%q, want %q", reply, "No agents registered")
	}
}

// TestAgentsCommand_MissingDependency covers the unwired-Runtime branch.
func TestAgentsCommand_MissingDependency(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/agents",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}
