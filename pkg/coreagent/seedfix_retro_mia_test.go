// Omnipus — seedfix: run_retrospective is default-available to every
// conversational built-in (founder ruling M8), and Mia's seeded delegation
// reaches the General Purpose worker (gap 6).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeedfix_RunRetrospective_NotDeniedForConversationalBuiltins resolves
// run_retrospective through the real compositor for every conversational
// built-in under the fresh-install ceiling.
func TestSeedfix_RunRetrospective_NotDeniedForConversationalBuiltins(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	for _, id := range []coreagent.CoreAgentID{
		coreagent.IDAva, coreagent.IDMia, coreagent.IDJim, coreagent.IDAdmin, coreagent.IDWorker,
	} {
		got := resolveFor(t, cfg, string(id), "run_retrospective", nil)
		// The EXACT value, not "allow or ask" (R1 delta T8): a flip between the
		// two would have gone unnoticed. The shipped ceiling default is allow
		// (pkg/config/defaults.go) and the seeded role policies grant it
		// explicitly (pkg/coreagent/role_policies_adr090.go), so allow is what
		// every conversational built-in must resolve under the fresh-install
		// ceiling.
		assert.Equal(t, "allow", got,
			"run_retrospective must resolve allow for %s (founder ruling M8: default-available to every conversational built-in)", id)
	}
}

// TestSeedfix_MiaSeedDelegatesToWorker pins the Mia -> General Purpose edge.
func TestSeedfix_MiaSeedDelegatesToWorker(t *testing.T) {
	dp := coreagent.SeedDelegationEdges(coreagent.IDMia)
	require.NotNil(t, dp)
	var targets []string
	for _, r := range dp.To {
		targets = append(targets, r.ID)
	}
	assert.Contains(t, targets, string(coreagent.IDWorker), "Mia must be able to delegate to General Purpose")
	assert.Contains(t, targets, string(coreagent.IDMia), "Mia keeps her self-edge candidate")
	assert.NotEmpty(t, dp.Modes, "a cross-agent seed edge needs modes")
}
