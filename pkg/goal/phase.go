// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import generated "github.com/elicify-ai/omnipus/pkg/api/generated"

// Phase is the ADR-086 D2 two-phase model (plus the terminal phase D9
// adds): definition, active, or terminal — a Goal's own coarse-grained
// lifecycle classification, distinct from and narrower than
// GoalStatusFrame's richer display enum (R§8.10), which this package does
// not implement (that frame is the wire/engine lane's concern, not S1's).
//
// "In chat, the two phases collapse: /goal creates the definition and
// activates it in the current session in one step (ADR-081 D1's instant
// activation is unchanged). On a task, they are separated in time: the
// definition is authored up front, dormant, and activates when the task
// starts and mints its session." (ADR-086 D2). This package's job is to
// make that phase observable on the record — status.go's Activate,
// Terminate and Reactivate are what actually move a Goal between phases;
// this file only classifies where a Goal currently sits.
type Phase string

const (
	// PhaseDefining: the goal exists, is readable and editable, and MUST
	// NOT run (ADR-086 D2/D3, GOAL-FR-009).
	PhaseDefining Phase = "defining"
	// PhaseActive: activated and iterating (GOAL-FR-010).
	PhaseActive Phase = "active"
	// PhaseTerminal: one of the four terminal states — met, exhausted,
	// expired, cleared. A task-owned goal's only edge back out of this
	// phase is Reactivate (R-04); a session-owned goal has none.
	PhaseTerminal Phase = "terminal"
)

// Phase classifies g's current State into the three-phase model.
func (g *Goal) Phase() Phase {
	switch {
	case g.State == generated.GoalStateDefining:
		return PhaseDefining
	case g.State == generated.GoalStateActive:
		return PhaseActive
	default:
		return PhaseTerminal
	}
}

// IsDefining reports whether g is in the defining phase (ADR-086 D2/D3,
// GOAL-FR-009: a goal in this phase exists, is readable and editable, and
// MUST NOT run).
func (g *Goal) IsDefining() bool { return g.Phase() == PhaseDefining }

// IsActive reports whether g is in the active phase (activated and
// iterating, GOAL-FR-010).
func (g *Goal) IsActive() bool { return g.Phase() == PhaseActive }

// IsTerminal reports whether g has reached one of the four terminal states.
func (g *Goal) IsTerminal() bool { return IsTerminalState(g.State) }
