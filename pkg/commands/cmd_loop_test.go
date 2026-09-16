package commands

import (
	"context"
	"testing"
)

// TestLoopCommand_RegisteredAsAgentDeliveryPassthrough covers the /loop
// command's dispatch contract, mirroring /goal and the memory trio. /loop is
// agent-delivery with a deliberately nil Handler (cmd_loop.go): the executor
// must defer to the agent loop's applyLoopCommandPrompt hook on every
// surface, never replying inline. Before this test, /loop was never resolved
// or dispatched by any test.
func TestLoopCommand_RegisteredAsAgentDeliveryPassthrough(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("loop")
	if !ok {
		t.Fatal(`Lookup("loop") not found — the canonical name must resolve`)
	}
	if def.Name != "loop" {
		t.Fatalf(`Lookup("loop").Name=%q, want "loop"`, def.Name)
	}
	if def.Handler != nil {
		t.Error("Handler must be nil — a Handler would reply and short-circuit before the loop hook sees the turn")
	}
	if def.Hidden {
		t.Error("/loop must not be Hidden — it is a canonical command")
	}

	ex := NewExecutor(reg, nil)
	cases := []struct {
		text    string
		channel string
	}{
		{"/loop every 30m check the build", "webchat"},
		{"/loop every 30m check the build", "cli"},
		{"/loop", "telegram"},
		{"/loop stop", "telegram"},
	}
	for _, tc := range cases {
		t.Run(tc.text+"@"+tc.channel, func(t *testing.T) {
			replied := false
			res := ex.Execute(context.Background(), Request{
				Channel: tc.channel,
				Text:    tc.text,
				Reply: func(string) error {
					replied = true
					return nil
				},
			})
			if res.Outcome != OutcomePassthrough {
				t.Errorf("outcome=%v, want %v (nil Handler defers to the agent loop)", res.Outcome, OutcomePassthrough)
			}
			if res.Command != "loop" {
				t.Errorf("command=%q, want \"loop\"", res.Command)
			}
			if replied {
				t.Error("Reply must not be called — passthrough means no inline reply")
			}
		})
	}
}
