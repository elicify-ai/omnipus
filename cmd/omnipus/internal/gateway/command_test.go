package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGatewayCommand(t *testing.T) {
	cmd := NewGatewayCommand()

	require.NotNil(t, cmd)

	// Renamed `gateway` → `start` (2026-05-30) so the canonical command
	// reflects what a new user wants to do: type `omnipus start`. The legacy
	// names are preserved as aliases so every existing script, CI workflow,
	// doc, and runbook keeps working.
	assert.Equal(t, "start", cmd.Use)
	assert.Contains(t, cmd.Short, "Start Omnipus")

	assert.Len(t, cmd.Aliases, 2)
	assert.True(t, cmd.HasAlias("gateway"), "legacy 'gateway' alias must be accepted")
	assert.True(t, cmd.HasAlias("g"), "legacy 'g' short alias must be accepted")

	assert.Nil(t, cmd.Run)
	assert.NotNil(t, cmd.RunE)

	assert.Nil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	assert.False(t, cmd.HasSubCommands())

	assert.True(t, cmd.HasFlags())
	assert.NotNil(t, cmd.Flags().Lookup("debug"))
	// --allow-empty is preserved as a hidden, deprecated no-op so existing
	// scripts (CI workflows, ops runbooks, eval-runner, docs.troubleshooting)
	// keep working unchanged. The gateway now ALWAYS boots into limited mode
	// when no provider is configured, regardless of the flag value.
	allowEmpty := cmd.Flags().Lookup("allow-empty")
	assert.NotNil(t, allowEmpty)
	assert.True(t, allowEmpty.Hidden, "--allow-empty must be hidden from --help")
}
