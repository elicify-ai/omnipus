// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "context"

// PrivateVariable is one agent-private variable for a child environment.
type PrivateVariable struct {
	Name  string
	Value string
}

// PrivateEnvSource returns the agent-private variables the turn in ctx may
// use (FR-076, FR-241) and a value-free notice naming any variable that could
// not be loaded. The agent loop supplies it; it resolves the owner pair from
// the trusted turn context only.
type PrivateEnvSource func(ctx context.Context) (vars []PrivateVariable, notice string)

// withPrivateVariable appends one private variable AFTER every managed layer
// of a composed child environment (R2-MAJ-007: the document layer strips
// names containing TOKEN/KEY/SECRET, so an earlier injection would be lost).
// The entry is an explicit site value, not inherited parent environment. A
// nil env means "inherit the scrubbed base" on the sandboxed foreground path,
// whose spawner merges caller entries after that base.
func withPrivateVariable(env []string, name, value string) []string {
	return append(env, name+"="+value)
}

// joinNotice adds a notice paragraph to an existing one.
func joinNotice(existing, add string) string {
	switch {
	case add == "":
		return existing
	case existing == "":
		return add
	default:
		return existing + "\n\n" + add
	}
}

// privateEnvFor resolves the turn's private variables through src (nil-safe).
func privateEnvFor(ctx context.Context, src PrivateEnvSource) ([]PrivateVariable, string) {
	if src == nil {
		return nil, ""
	}
	return src(ctx)
}
