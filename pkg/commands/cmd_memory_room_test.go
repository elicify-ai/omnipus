// License: MIT
// Copyright (c) 2026 Omnipus contributors

package commands

import (
	"strings"
	"testing"
)

// remember's room is a required choice; both /remember prompt forms must name
// it so the model does not learn it from a refused call.
func TestRememberSteeringPrompt_NamesRoomChoice(t *testing.T) {
	for _, args := range []string{"", "always use pnpm"} {
		prompt, ok := MemoryCommandSteeringPrompt("remember", args)
		if !ok {
			t.Fatalf("args=%q: ok=false", args)
		}
		for _, want := range []string{"room", "'shared'", "'private'"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("args=%q: prompt %q must contain %q", args, prompt, want)
			}
		}
	}
}
