// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T11 slice A, pkg/clitrust (slim spec §3.2, §8 FR-5).
//
// The route-level proof (path-not-allowed, zero spawn) is
// pkg/gateway.TestCliValidate_SharedCheckBeforeSpawn. This package is the
// direct matrix, calling CheckProgramPath and AgentWritableRoots.
package clitrust_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/clitrust"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func loadT11CLITrust(t *testing.T) (
	check func(path string, forbidden []string) (string, error),
	roots func(home string, cfg *config.Config) []string,
) {
	t.Helper()
	return clitrust.CheckProgramPath, clitrust.AgentWritableRoots
}
