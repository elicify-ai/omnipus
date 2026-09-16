package commands

import (
	"context"
	"testing"
)

// TestStartCommand_ResolveAndExecute covers the hidden /start command kept
// for Telegram bot compatibility: resolution by the typed name (a rename
// would break Telegram's automatic /start on first contact), the Hidden
// flag, and the greeting reply on a channel surface.
func TestStartCommand_ResolveAndExecute(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("start")
	if !ok {
		t.Fatal(`Lookup("start") not found — Telegram sends /start on first contact`)
	}
	if def.Name != "start" {
		t.Fatalf(`Lookup("start").Name=%q, want "start"`, def.Name)
	}
	if !def.Hidden {
		t.Error("/start must stay Hidden — excluded from /help and channel menus by design")
	}

	ex := NewExecutor(reg, nil)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/start",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/start: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Hello! I am Omnipus 🐙" {
		t.Fatalf("/start reply=%q, want greeting", reply)
	}
}
