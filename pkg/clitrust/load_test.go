// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for T11 slice A, pkg/clitrust (slim spec §3.2, §8 FR-5).
//
// CheckProgramPath and AgentWritableRoots are not in the tree. The route-level
// proof (path-not-allowed, zero spawn) is pkg/gateway.TestCliValidate_SharedCheckBeforeSpawn.
// This package is the direct matrix. It fails with BLOCKED until the symbols
// exist; reconciliation replaces loadT11CLITrust with the two direct calls.
// The assertions are not skipped.
package clitrust_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func loadT11CLITrust(t *testing.T) (
	check func(path string, forbidden []string) (string, error),
	roots func(home string, cfg *config.Config) []string,
) {
	t.Helper()
	t.Fatalf("BLOCKED: pkg/clitrust.CheckProgramPath(path, forbidden) (string, error) and " +
		"AgentWritableRoots(home, cfg) []string not implemented — required by t11 spec §3.2 / FR-5")
	return nil, nil
}
