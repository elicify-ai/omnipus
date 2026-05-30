package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGatewayCommand(t *testing.T) {
	cmd := NewGatewayCommand()

	require.NotNil(t, cmd)

	assert.Equal(t, "gateway", cmd.Use)
	assert.Equal(t, "Start omnipus gateway", cmd.Short)

	assert.Len(t, cmd.Aliases, 1)
	assert.True(t, cmd.HasAlias("g"))

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
