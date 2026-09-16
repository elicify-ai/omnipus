package commands

import (
	"context"
	"testing"
)

// TestSkillsCommand_ResolveAndExecute covers the canonical /skills command.
// Its handler was previously only reachable through the deprecated /list
// skills sub-command — /skills itself had no test.
func TestSkillsCommand_ResolveAndExecute(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("skills")
	if !ok {
		t.Fatal(`Lookup("skills") not found — the canonical name must resolve`)
	}
	if def.Name != "skills" {
		t.Fatalf(`Lookup("skills").Name=%q, want "skills"`, def.Name)
	}

	rt := &Runtime{
		ListSkillNames: func() []string { return []string{"shell", "git"} },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "/skills",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/skills: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{"Installed Skills:", "shell", "git", "/<skillname> <message>"} {
		if !contains(reply, want) {
			t.Fatalf("/skills reply=%q, want it to contain %q", reply, want)
		}
	}
}

// TestSkillsCommand_EmptyList covers the no-skills reply.
func TestSkillsCommand_EmptyList(t *testing.T) {
	rt := &Runtime{ListSkillNames: func() []string { return nil }}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/skills",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "No installed skills" {
		t.Fatalf("reply=%q, want %q", reply, "No installed skills")
	}
}

// TestSkillsCommand_MissingDependency covers the unwired-Runtime branch.
func TestSkillsCommand_MissingDependency(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/skills",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}
