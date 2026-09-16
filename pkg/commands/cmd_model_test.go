package commands

import (
	"context"
	"errors"
	"testing"
)

// TestModelCommand_ResolveAndShowCurrent covers the canonical /model noun
// command: resolution by the typed name and the bare invocation showing the
// current model. The handler is shared with the deprecated /show model and
// /switch model sub-commands, but /model itself had no coverage.
func TestModelCommand_ResolveAndShowCurrent(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("model")
	if !ok {
		t.Fatal(`Lookup("model") not found — the canonical name must resolve`)
	}
	if def.Name != "model" {
		t.Fatalf(`Lookup("model").Name=%q, want "model"`, def.Name)
	}

	rt := &Runtime{
		GetModelInfo: func() (string, string) { return "gpt-4o", "openrouter" },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/model",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/model: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Current Model: gpt-4o (Provider: openrouter)" {
		t.Fatalf("/model reply=%q, want current-model message", reply)
	}
}

// TestModelCommand_SwitchModel covers "/model <name>" — the switch path with
// its success and error outcomes.
func TestModelCommand_SwitchModel(t *testing.T) {
	rt := &Runtime{
		SwitchModel: func(value string) (string, error) {
			if value == "bad-model" {
				return "", errors.New("model not found")
			}
			return "old-model", nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/model llama",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/model llama: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Switched model from old-model to llama" {
		t.Fatalf("/model llama reply=%q, want switch message", reply)
	}

	reply = ""
	res = ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/model bad-model",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/model bad-model: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "model not found" {
		t.Fatalf("/model bad-model reply=%q, want error text", reply)
	}
}

// TestModelCommand_MissingDependencies covers both branches against a Runtime
// with no model dependencies wired.
func TestModelCommand_MissingDependencies(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	for _, text := range []string{"/model", "/model llama"} {
		var reply string
		res := ex.Execute(context.Background(), Request{
			Channel: "cli",
			Text:    text,
			Reply:   func(text string) error { reply = text; return nil },
		})
		if res.Outcome != OutcomeHandled {
			t.Fatalf("%s: outcome=%v, want=%v", text, res.Outcome, OutcomeHandled)
		}
		if reply != unavailableMsg {
			t.Fatalf("%s reply=%q, want %q", text, reply, unavailableMsg)
		}
	}
}
