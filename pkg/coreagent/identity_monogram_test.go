package coreagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// AC-5: a stored figure "Monogram" survives a restart with no identity write on
// the second boot; a stored invalid figure still migrates to Omnipus.
//
// Oracle: migrateUnenforcedAgentIdentity, which SeedConfig folds into its boot
// bool. A valid enum member is left alone; a non-member becomes the default.
func TestMigrateUnenforcedAgentIdentity_MonogramSurvivesAndInvalidMigrates(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{
			ID:     "custom-monogram",
			Name:   "Monogram agent",
			Type:   config.AgentTypeCustom,
			Figure: "Monogram",
			Role:   "general",
			Color:  "#3B82F6",
		},
		{
			ID:     "custom-bogus",
			Name:   "Bogus figure",
			Type:   config.AgentTypeCustom,
			Figure: "octopus",
			Role:   "general",
			Color:  "#3B82F6",
		},
	}}}

	require.True(t, migrateUnenforcedAgentIdentity(cfg), "the invalid figure migrates on the first pass")

	monogram := agentByID(cfg, "custom-monogram")
	require.NotNil(t, monogram)
	assert.Equal(t, "Monogram", monogram.Figure, "a stored Monogram is a valid enum member and is left alone")

	bogus := agentByID(cfg, "custom-bogus")
	require.NotNil(t, bogus)
	assert.Equal(t, "Omnipus", bogus.Figure, "an invalid figure still migrates to the default")

	require.False(t, migrateUnenforcedAgentIdentity(cfg), "the second boot performs no identity write")
	assert.Equal(t, "Monogram", agentByID(cfg, "custom-monogram").Figure, "the second boot leaves the Monogram in place")
}
