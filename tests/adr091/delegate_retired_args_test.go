// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix lane RX-TESTS. `async` is the argument this ADR nailed shut:
// wait-inline delegation (`async:false`) is the mechanism it deleted, and
// pkg/tools/delegate_run.go::validateRequest is the door.
//
// Every pre-existing test of that door sent `async: true` — pkg/tools's
// TestDelegateTool_ExecuteRejectsInvalidRunInput ("retired async"),
// TestDelegate_RejectsRemovedArgs, and the metadata test that only checks the
// published parameter list. A reviewer changed the rejection to accept
// `async:false` while still refusing `async:true` and ALL THREE stayed green.
// That is exactly the regression shape that matters: `async` comes back as a
// recognised argument DEFAULTING to true, and a caller sending the retired
// `async:false` walks straight through the door the ADR closed.
//
// This lives in tests/adr091 rather than pkg/tools because this lane owns
// tests/adr091 and pkg/agent/testutil only; the tool's constructor and
// Execute are exported, so the door can be exercised from outside the
// package exactly as a real caller reaches it.
package adr091_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestDelegateRun_RejectsRetiredArgumentsAtEveryValue drives the retired
// arguments through the tool's real Execute entry point at EVERY value a
// caller could send, not just the one value the old tests happened to pick.
func TestDelegateRun_RejectsRetiredArgumentsAtEveryValue(t *testing.T) {
	for _, retired := range []string{"async", "allow_blocking_question"} {
		for _, value := range []struct {
			name  string
			value any
		}{
			// The value the pre-existing tests all sent.
			{name: "true", value: true},
			// The value NOTHING tested: `async:false` IS wait-inline, the
			// single mechanism ADR-091 exists to remove. A rejection that
			// only refuses `true` refuses the harmless half and admits the
			// harmful one.
			{name: "false", value: false},
			{name: "string false", value: "false"},
			{name: "zero", value: 0},
			{name: "null", value: nil},
		} {
			t.Run(retired+"/"+value.name, func(t *testing.T) {
				tool := tools.NewDelegateTool("test-model", 0, 0)
				result := tool.Execute(context.Background(), map[string]any{
					"action": "run",
					"task":   "inspect checkout",
					retired:  value.value,
				})
				if result == nil {
					t.Fatalf("Execute returned nil for %s=%v", retired, value.value)
				}
				want := "invalid_argument: " + retired
				if !result.IsError || !strings.Contains(result.ForLLM, want) {
					t.Fatalf("delegate(action=run, %s=%#v) = %+v; want an error containing %q — a "+
						"retired argument must be refused by NAME whatever value it carries, or it is "+
						"back as a recognised argument and wait-inline delegation returns with it",
						retired, value.value, result, want)
				}
			})
		}
	}
}

// TestDelegateRun_AcceptsTheRequestWithoutTheRetiredArguments is the
// positive control: the identical call, minus the retired key, must NOT be
// refused with invalid_argument. Without it, a validateRequest that rejected
// every request would pass the table above and prove nothing.
func TestDelegateRun_AcceptsTheRequestWithoutTheRetiredArguments(t *testing.T) {
	tool := tools.NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{
		"action": "run",
		"task":   "inspect checkout",
	})
	if result == nil {
		t.Fatal("Execute returned nil for a well-formed run request")
	}
	if strings.Contains(result.ForLLM, "invalid_argument") {
		t.Fatalf("a run request carrying NO retired argument was refused as invalid: %+v — the "+
			"rejection table above would pass even if every request were refused", result)
	}
}
