// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestPhaseClassification (ADR-086 D2) walks a goal through defining ->
// active -> terminal and asserts Phase()/IsDefining/IsActive/IsTerminal
// agree at every step.
func TestPhaseClassification(t *testing.T) {
	g := newTestGoal(t, "task", "task-1")
	now := time.Now().UTC()

	if g.Phase() != PhaseDefining || !g.IsDefining() || g.IsActive() || g.IsTerminal() {
		t.Fatalf("fresh goal: Phase()=%q IsDefining=%v IsActive=%v IsTerminal=%v, want defining/true/false/false",
			g.Phase(), g.IsDefining(), g.IsActive(), g.IsTerminal())
	}

	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if g.Phase() != PhaseActive || g.IsDefining() || !g.IsActive() || g.IsTerminal() {
		t.Fatalf("activated goal: Phase()=%q IsDefining=%v IsActive=%v IsTerminal=%v, want active/false/true/false",
			g.Phase(), g.IsDefining(), g.IsActive(), g.IsTerminal())
	}

	if err := g.Terminate(generated.GoalStateMet, "done", now); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if g.Phase() != PhaseTerminal || g.IsDefining() || g.IsActive() || !g.IsTerminal() {
		t.Fatalf("terminated goal: Phase()=%q IsDefining=%v IsActive=%v IsTerminal=%v, want terminal/false/false/true",
			g.Phase(), g.IsDefining(), g.IsActive(), g.IsTerminal())
	}
}

// TestPhaseAllFourTerminalStates proves every one of the four terminal
// states classifies as PhaseTerminal, not just "met".
func TestPhaseAllFourTerminalStates(t *testing.T) {
	for _, s := range []generated.GoalState{
		generated.GoalStateMet, generated.GoalStateExhausted,
		generated.GoalStateExpired, generated.GoalStateCleared,
	} {
		g := newTestGoal(t, "session", "s1")
		g.State = s
		if g.Phase() != PhaseTerminal {
			t.Errorf("state %q: Phase() = %q, want terminal", s, g.Phase())
		}
		if !g.IsTerminal() {
			t.Errorf("state %q: IsTerminal() = false, want true", s)
		}
	}
}
