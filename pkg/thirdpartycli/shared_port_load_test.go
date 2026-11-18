// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T11 combined: assertion-preserving slice-A port (slim spec §3.2, §8 FR-5).
//
// The route-level proof (path-not-allowed, zero spawn) is
// pkg/gateway.TestCliValidate_SharedCheckBeforeSpawn. This package is the
// direct matrix, calling the shared public API through the adapter below.
package thirdpartycli_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/stretchr/testify/require"
)

func loadT11CLITrust(t *testing.T) (
	check func(path string, forbidden []string) (string, error),
	roots func(home string, cfg *config.Config) ([]string, error),
) {
	t.Helper()
	return func(path string, forbidden []string) (string, error) {
		return thirdpartycli.ValidateProgramPath("claude-code", path, forbidden...)
	}, thirdpartycli.AgentWritableRoots
}

// t11RequireCheck strengthens the old free-text needle to the exact shared check id.
func t11RequireCheck(t *testing.T, err error, want string) {
	t.Helper()
	var ce *thirdpartycli.CheckError
	require.ErrorAs(t, err, &ce, "spec §3.2: refusal carries the structured failed check")
	require.Equal(t, want, ce.Check, "spec §3.2: exact failed check, not any error")
}
