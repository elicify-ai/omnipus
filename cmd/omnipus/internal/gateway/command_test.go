package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/clitoken"
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

// TestNewCliTokenFlag verifies FR-018/US-9:
//   - the --new-cli-token flag is registered on the start command
//   - calling ResetCLIToken via the flag path rotates the token
//     (old token no longer matches new hash)
//
// This test does NOT boot a real gateway — it only tests the flag wiring
// and the clitoken helper round-trip.
func TestNewCliTokenFlag(t *testing.T) {
	cmd := NewGatewayCommand()
	require.NotNil(t, cmd)

	// The flag must exist on the command.
	f := cmd.Flags().Lookup("new-cli-token")
	require.NotNil(t, f, "--new-cli-token flag must be registered on the start command")
	assert.Equal(t, "false", f.DefValue, "--new-cli-token must default to false")

	// Verify the flag is a bool flag.
	assert.Equal(t, "bool", f.Value.Type())

	// Exercise the token rotation helper that --new-cli-token calls.
	// Seed a minimal config so clitoken operations have a valid file.
	home := t.TempDir()
	cfg := map[string]any{
		"gateway": map[string]any{
			"port":  5000,
			"users": []any{},
		},
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))

	// Mint initial token.
	created, err := clitoken.EnsureCLIToken(home)
	require.NoError(t, err)
	assert.True(t, created, "first EnsureCLIToken should create the token")

	oldToken, err := clitoken.LoadCLIToken(home)
	require.NoError(t, err)
	require.NotEmpty(t, oldToken)

	// Rotate — this is what --new-cli-token triggers in RunE.
	require.NoError(t, clitoken.ResetCLIToken(home))

	newToken, err := clitoken.LoadCLIToken(home)
	require.NoError(t, err)
	require.NotEmpty(t, newToken)

	assert.NotEqual(t, oldToken, newToken, "ResetCLIToken must produce a different token")

	// Verify the new token file has mode 0600.
	info, err := os.Stat(clitoken.CLITokenPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
