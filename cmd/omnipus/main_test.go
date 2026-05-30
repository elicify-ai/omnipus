package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

func TestNewOmnipusCommand(t *testing.T) {
	cmd := NewOmnipusCommand()

	require.NotNil(t, cmd)

	short := fmt.Sprintf("%s omnipus - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	assert.Equal(t, "omnipus", cmd.Use)
	assert.Equal(t, short, cmd.Short)

	assert.True(t, cmd.HasSubCommands())
	assert.True(t, cmd.HasAvailableSubCommands())

	assert.False(t, cmd.HasFlags())

	assert.Nil(t, cmd.Run)
	assert.Nil(t, cmd.RunE)

	assert.Nil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	allowedCommands := []string{
		"agent",
		"audit",
		"auth",
		"credentials",
		"cron",
		"doctor",
		// "gateway" was renamed to "start" (2026-05-30). The legacy name
		// remains accepted via Cobra aliases; only the canonical Use is
		// enumerated by cmd.Commands().
		"start",
		"migrate",
		"model",
		"onboard",
		"skills",
		"status",
		"version",
	}

	subcommands := cmd.Commands()
	assert.Len(t, subcommands, len(allowedCommands))

	for _, subcmd := range subcommands {
		found := slices.Contains(allowedCommands, subcmd.Name())
		assert.True(t, found, "unexpected subcommand %q", subcmd.Name())

		assert.False(t, subcmd.Hidden)
	}
}
