package commands

import (
	"context"
	"testing"
)

// The model-info sub-commands of the deprecated /show and /list
// multiplexers were the only sub-handlers never executed by any test
// (/show channel, /show agents, /list channels|agents|skills were covered;
// the "/show model" appearances in executor_test.go run synthetic LOCAL
// definitions, not these handlers). The canonical /model noun command now
// reuses this logic, so a break here surfaces in both surfaces.

// TestShowModelSubCommand_CurrentModel executes the real /show model
// definition and asserts the current-model reply.
func TestShowModelSubCommand_CurrentModel(t *testing.T) {
	rt := &Runtime{
		GetModelInfo: func() (string, string) { return "gpt-4o", "openrouter" },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/show model",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/show model: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Current Model: gpt-4o (Provider: openrouter)" {
		t.Fatalf("/show model reply=%q, want current-model message", reply)
	}
}

// TestShowBareAndUnknownSubcommand covers the multiplexer bad-input paths on
// the REAL definition: bare /show replies the generated usage, and an
// unknown sub-command is named in the error.
func TestShowBareAndUnknownSubcommand(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/show",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/show: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Usage: /show [model|channel|agents]" {
		t.Fatalf("/show reply=%q, want usage message", reply)
	}

	reply = ""
	res = ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/show bogus",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/show bogus: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	want := "Unknown option: bogus. Usage: /show [model|channel|agents]"
	if reply != want {
		t.Fatalf("/show bogus reply=%q, want %q", reply, want)
	}
}

// TestListModelsSubCommand_ConfiguredModel executes the real /list models
// definition: the configured-model reply, the provider fallback when the
// provider is empty, and the unwired-Runtime branch.
func TestListModelsSubCommand_ConfiguredModel(t *testing.T) {
	rt := &Runtime{
		GetModelInfo: func() (string, string) { return "gpt-4o", "openrouter" },
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/list models",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/list models: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	want := "Configured Model: gpt-4o\nProvider: openrouter\n\nTo change models, update config.json"
	if reply != want {
		t.Fatalf("/list models reply=%q, want %q", reply, want)
	}

	rtDefault := &Runtime{
		GetModelInfo: func() (string, string) { return "gpt-4o", "" },
	}
	exDefault := NewExecutor(NewRegistry(BuiltinDefinitions()), rtDefault)
	reply = ""
	res = exDefault.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/list models",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("default-provider: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !contains(reply, "Provider: configured default") {
		t.Fatalf("default-provider reply=%q, want configured-default provider line", reply)
	}

	exNoDep := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})
	reply = ""
	res = exNoDep.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/list models",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("no-dep: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("no-dep reply=%q, want %q", reply, unavailableMsg)
	}
}
