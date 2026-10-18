// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "context"

// PrivateEnvSource returns the "NAME=value" entries of the agent-private
// variables the turn in ctx may use (FR-076, FR-241) and a value-free notice
// naming any variable that could not be loaded. The agent loop supplies it;
// it resolves the owner pair from the trusted turn context only.
type PrivateEnvSource func(ctx context.Context) (env []string, notice string)

// appendPrivateEnv appends the private entries AFTER every managed layer of
// a composed child environment (R2-MAJ-007: the document layer strips names
// containing TOKEN/KEY/SECRET, so an earlier injection would be lost). A nil
// env means "inherit the scrubbed base" on the sandboxed foreground path,
// whose spawner merges the caller entries after that base, so the private
// entries alone are passed there.
func appendPrivateEnv(env, private []string) []string {
	if len(private) == 0 {
		return env
	}
	if env == nil {
		return append([]string(nil), private...)
	}
	return append(env, private...)
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

// privateEnvFor resolves the turn's private entries through src (nil-safe).
func privateEnvFor(ctx context.Context, src PrivateEnvSource) ([]string, string) {
	if src == nil {
		return nil, ""
	}
	return src(ctx)
}
