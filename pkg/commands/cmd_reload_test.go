package commands

import (
	"context"
	"errors"
	"testing"
)

// TestConfigCommand_ResolveAndExecute covers the canonical /config command
// (renamed from /reload, cmd_reload.go): resolution by the typed name and
// the reload-executed reply.
func TestConfigCommand_ResolveAndExecute(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("config")
	if !ok {
		t.Fatal(`Lookup("config") not found — the canonical name must resolve`)
	}
	if def.Name != "config" {
		t.Fatalf(`Lookup("config").Name=%q, want "config"`, def.Name)
	}

	rt := &Runtime{ReloadConfig: func() error { return nil }}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli", // /config is CLI+Channel surfaced
		Text:    "/config",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/config: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Config reload triggered!" {
		t.Fatalf("/config reply=%q, want %q", reply, "Config reload triggered!")
	}
}

// TestReloadAlias_ExecutesConfigCommand runs the deprecated /reload alias on
// a surface where it is active, so the alias actually reaches the handler.
func TestReloadAlias_ExecutesConfigCommand(t *testing.T) {
	rt := &Runtime{ReloadConfig: func() error { return nil }}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/reload",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/reload: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if res.Command != "config" {
		t.Fatalf("/reload resolved to command=%q, want \"config\"", res.Command)
	}
	if reply != "Config reload triggered!" {
		t.Fatalf("/reload reply=%q, want %q", reply, "Config reload triggered!")
	}
}

// TestConfigCommand_ErrorAndGating covers the reload-failure reply, the
// unwired-Runtime branch, and the webchat surface gate on the REAL
// definition (the executor-level gating test uses a synthetic local def).
func TestConfigCommand_ErrorAndGating(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{
		ReloadConfig: func() error { return errors.New("config.json invalid") },
	})

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/config",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Failed to reload configuration: config.json invalid" {
		t.Fatalf("reply=%q, want failure message", reply)
	}

	exNoDep := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})
	reply = ""
	res = exNoDep.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/config",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("no-dep outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != unavailableMsg {
		t.Fatalf("no-dep reply=%q, want %q", reply, unavailableMsg)
	}

	res = ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "/config",
		Reply:   func(string) error { return nil },
	})
	if res.Outcome != OutcomePassthrough {
		t.Fatalf("/config from webchat: outcome=%v, want=%v", res.Outcome, OutcomePassthrough)
	}
}
