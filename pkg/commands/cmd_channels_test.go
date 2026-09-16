package commands

import (
	"context"
	"errors"
	"testing"
)

// TestChannelsCommand_ResolveAndList covers the canonical /channels command:
// resolution by the typed name and the bare invocation listing enabled
// channels. /channels had no test at all — its handler was only reachable
// via the deprecated /list channels and /check channel sub-commands.
func TestChannelsCommand_ResolveAndList(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("channels")
	if !ok {
		t.Fatal(`Lookup("channels") not found — the canonical name must resolve`)
	}
	if def.Name != "channels" {
		t.Fatalf(`Lookup("channels").Name=%q, want "channels"`, def.Name)
	}

	rt := &Runtime{
		GetEnabledChannels: func() []string { return []string{"telegram", "slack"} },
	}
	ex := NewExecutor(reg, rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli", // /channels is CLI+Channel surfaced
		Text:    "/channels",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/channels: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Enabled Channels:\n- telegram\n- slack" {
		t.Fatalf("/channels reply=%q, want enabled-channels list", reply)
	}
}

// TestChannelsCommand_EmptyList covers the no-channels reply.
func TestChannelsCommand_EmptyList(t *testing.T) {
	rt := &Runtime{GetEnabledChannels: func() []string { return nil }}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/channels",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "No channels enabled" {
		t.Fatalf("reply=%q, want %q", reply, "No channels enabled")
	}
}

// TestChannelsCommand_CheckByName covers "/channels <name>" — the
// availability-check path with its success and unknown-channel outcomes.
func TestChannelsCommand_CheckByName(t *testing.T) {
	rt := &Runtime{
		SwitchChannel: func(value string) error {
			if value == "nope" {
				return errors.New("channel 'nope' not found")
			}
			return nil
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/channels telegram",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/channels telegram: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "Channel 'telegram' is available and enabled" {
		t.Fatalf("/channels telegram reply=%q, want availability message", reply)
	}

	reply = ""
	res = ex.Execute(context.Background(), Request{
		Channel: "cli",
		Text:    "/channels nope",
		Reply:   func(text string) error { reply = text; return nil },
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/channels nope: outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if reply != "channel 'nope' not found" {
		t.Fatalf("/channels nope reply=%q, want error text", reply)
	}
}

// TestChannelsCommand_MissingDependencies covers both branches against a
// Runtime with no channel dependencies wired, and the webchat surface gate
// on the real definition (the SPA covers channels in its own screen).
func TestChannelsCommand_MissingDependencies(t *testing.T) {
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{})

	for _, text := range []string{"/channels", "/channels telegram"} {
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

	res := ex.Execute(context.Background(), Request{
		Channel: "webchat",
		Text:    "/channels",
		Reply:   func(string) error { return nil },
	})
	if res.Outcome != OutcomePassthrough {
		t.Fatalf("/channels from webchat: outcome=%v, want=%v", res.Outcome, OutcomePassthrough)
	}
}
