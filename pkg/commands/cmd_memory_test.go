package commands

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryCommandDefinitions_WellFormed verifies the shape of the three
// memory command Definitions: correct name/surfaces/delivery/usage, and a nil
// Handler (passthrough by design — see cmd_memory.go doc comment).
func TestMemoryCommandDefinitions_WellFormed(t *testing.T) {
	cases := []struct {
		name      string
		def       Definition
		wantUsage string
	}{
		{"remember", rememberCommand(), "/remember <what to store>"},
		{"recall", recallCommand(), "/recall <what to find>"},
		{"retrospective", retrospectiveCommand(), "/retrospective [focus]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.def.Name != tc.name {
				t.Errorf("Name=%q, want %q", tc.def.Name, tc.name)
			}
			if tc.def.Description == "" {
				t.Error("Description must not be empty")
			}
			if tc.def.EffectiveUsage() != tc.wantUsage {
				t.Errorf("Usage=%q, want %q", tc.def.EffectiveUsage(), tc.wantUsage)
			}
			if tc.def.EffectiveDelivery() != DeliveryAgent {
				t.Errorf("Delivery=%q, want %q", tc.def.EffectiveDelivery(), DeliveryAgent)
			}
			if tc.def.Handler != nil {
				t.Error("Handler must be nil — a Handler would reply and short-circuit before the LLM sees the turn")
			}
			if tc.def.Hidden {
				t.Error("must not be Hidden — these are canonical, visible commands")
			}
			wantSurfaces := []Surface{SurfaceWeb, SurfaceCLI, SurfaceChannel}
			if len(tc.def.Surfaces) != len(wantSurfaces) {
				t.Fatalf("Surfaces=%v, want %v", tc.def.Surfaces, wantSurfaces)
			}
			for _, s := range wantSurfaces {
				if !tc.def.AllowsSurface(s) {
					t.Errorf("Surfaces=%v must allow %v", tc.def.Surfaces, s)
				}
			}
		})
	}
}

// TestMemoryCommands_RegisteredInBuiltinDefinitions verifies remember/recall/
// retrospective are present in BuiltinDefinitions() and registered under
// their own names in the Registry.
func TestMemoryCommands_RegisteredInBuiltinDefinitions(t *testing.T) {
	defs := BuiltinDefinitions()
	reg := NewRegistry(defs)

	for _, name := range []string{"remember", "recall", "retrospective"} {
		def, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("Registry.Lookup(%q) not found", name)
			continue
		}
		if def.Name != name {
			t.Errorf("Lookup(%q).Name=%q, want %q", name, def.Name, name)
		}
	}
}

// TestMemoryCommands_ExecutorPassthrough verifies that invoking any of the
// three memory commands through the Executor returns OutcomePassthrough (not
// OutcomeHandled) on every surface — the nil Handler must make the executor
// defer to the agent loop rather than reply inline. This is the contract the
// agent loop's applyMemoryCommandPrompt hook relies on: it only needs to run
// because these commands never produce a handled reply here.
func TestMemoryCommands_ExecutorPassthrough(t *testing.T) {
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	cases := []struct {
		text    string
		channel string
	}{
		{"/remember always use pnpm", "webchat"},
		{"/recall what did we decide", "cli"},
		{"/retrospective", "telegram"},
		{"/remember", "webchat"},
		{"/recall", "cli"},
		{"/retrospective focus on the outage", "telegram"},
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
				t.Errorf("outcome=%v, want %v", res.Outcome, OutcomePassthrough)
			}
			if replied {
				t.Error("Reply must not be called — nil Handler means no inline reply")
			}
		})
	}
}

// TestMemoryCommandSteeringPrompt covers the template dispatch: correct
// template selected per command name, args interpolated verbatim, and
// unknown names report ok=false.
func TestMemoryCommandSteeringPrompt(t *testing.T) {
	t.Run("remember with args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("remember", "always use pnpm")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if !strings.Contains(prompt, "always use pnpm") {
			t.Errorf("prompt=%q must contain the verbatim args", prompt)
		}
		if !strings.Contains(prompt, "'remember' tool") {
			t.Errorf("prompt=%q must mention the remember tool", prompt)
		}
	})

	t.Run("remember without args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("remember", "")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if !strings.Contains(prompt, "'remember' tool") {
			t.Errorf("prompt=%q must mention the remember tool", prompt)
		}
	})

	t.Run("recall with args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("recall", "what did we decide")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if !strings.Contains(prompt, "what did we decide") {
			t.Errorf("prompt=%q must contain the verbatim args", prompt)
		}
		if !strings.Contains(prompt, "recall_memory") || !strings.Contains(prompt, "recall_conversation") {
			t.Errorf("prompt=%q must mention both recall tools", prompt)
		}
	})

	t.Run("recall without args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("recall", "")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if prompt == "" {
			t.Error("prompt must not be empty (ask-back steering line)")
		}
	})

	t.Run("retrospective with args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("retrospective", "focus on the outage")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if !strings.Contains(prompt, "run_retrospective") {
			t.Errorf("prompt=%q must mention run_retrospective", prompt)
		}
		if !strings.Contains(prompt, "focus on the outage") {
			t.Errorf("prompt=%q must contain the verbatim args", prompt)
		}
	})

	t.Run("retrospective without args", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("retrospective", "")
		if !ok {
			t.Fatal("ok=false, want true")
		}
		if !strings.Contains(prompt, "run_retrospective") {
			t.Errorf("prompt=%q must mention run_retrospective", prompt)
		}
	})

	t.Run("unknown name", func(t *testing.T) {
		prompt, ok := MemoryCommandSteeringPrompt("help", "anything")
		if ok {
			t.Error("ok=true for a non-memory command name, want false")
		}
		if prompt != "" {
			t.Errorf("prompt=%q, want empty for unmatched name", prompt)
		}
	})
}

// TestCommandArgs verifies the free-text-remainder extraction helper used by
// the memory command steering-prompt hook.
func TestCommandArgs(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/remember buy milk", "buy milk"},
		{"/remember", ""},
		{"/remember   extra   spaces  here", "extra spaces here"},
		{"", ""},
		{"/recall", ""},
	}
	for _, tc := range cases {
		if got := CommandArgs(tc.input); got != tc.want {
			t.Errorf("CommandArgs(%q)=%q, want %q", tc.input, got, tc.want)
		}
	}
}
