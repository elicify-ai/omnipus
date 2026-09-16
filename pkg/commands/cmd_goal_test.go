package commands

import (
	"context"
	"slices"
	"testing"
)

// TestGoalCommand_RegisteredAsAgentDeliveryPassthrough covers the /goal
// command's dispatch contract, mirroring the memory-trio coverage in
// cmd_memory_test.go. /goal is agent-delivery with a deliberately nil Handler
// (cmd_goal.go): the executor must defer to the agent loop's
// applyGoalCommandPrompt hook on every surface, never replying inline.
// Before this test, /goal was never resolved or dispatched by any test — a
// rename would have gone unnoticed.
func TestGoalCommand_RegisteredAsAgentDeliveryPassthrough(t *testing.T) {
	reg := NewRegistry(BuiltinDefinitions())

	def, ok := reg.Lookup("goal")
	if !ok {
		t.Fatal(`Lookup("goal") not found — the canonical name must resolve`)
	}
	if def.Name != "goal" {
		t.Fatalf(`Lookup("goal").Name=%q, want "goal"`, def.Name)
	}
	if def.Handler != nil {
		t.Error("Handler must be nil — a Handler would reply and short-circuit before the goal loop sees the turn")
	}
	if def.Hidden {
		t.Error("/goal must not be Hidden — it is a canonical command")
	}

	ex := NewExecutor(reg, nil)
	cases := []struct {
		text    string
		channel string
	}{
		{"/goal ship the release with zero regressions", "webchat"},
		{"/goal ship the release with zero regressions", "cli"},
		{"/goal", "telegram"},
		{"/goal clear", "telegram"},
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
			if res.Command != "goal" {
				t.Errorf("command=%q, want \"goal\"", res.Command)
			}
			if replied {
				t.Error("Reply must not be called — passthrough means no inline reply")
			}
		})
	}
}

// TestGoalClearAliases_ListsDocumentedVerbs covers the exported alias list
// consumed by pkg/agent/goal_loop.go's applyGoalCommandPrompt to recognize
// every documented clear verb (FR-070), not just the literal word "clear".
func TestGoalClearAliases_ListsDocumentedVerbs(t *testing.T) {
	got := GoalClearAliases()
	if len(got) != 6 {
		t.Fatalf("GoalClearAliases len=%d, want 6 (clear/stop/off/reset/cancel/none): %v", len(got), got)
	}
	for _, verb := range []string{"clear", "stop", "off", "reset", "cancel", "none"} {
		if !slices.Contains(got, verb) {
			t.Errorf("GoalClearAliases missing %q: %v", verb, got)
		}
	}
}
