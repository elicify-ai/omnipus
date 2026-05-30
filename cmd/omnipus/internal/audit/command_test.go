// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
)

// TestNewAuditCommand_Wired sanity-checks that the command tree is registered
// with the expected verbs. Smoke test for the cobra wiring.
func TestNewAuditCommand_Wired(t *testing.T) {
	cmd := NewAuditCommand()
	require.NotNil(t, cmd)
	require.Equal(t, "audit", cmd.Use)

	// Find the verify subcommand.
	verify := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "verify" {
			verify = true
			require.NotEmpty(t, sub.Short)
			require.NotEmpty(t, sub.Long)
		}
	}
	require.True(t, verify, "verify subcommand must be registered")
}

// TestAuditVerify_DefaultDir confirms the default audit directory is
// $OMNIPUS_HOME/system, matching the gateway boot path.
func TestAuditVerify_DefaultDir(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	expected := internal.GetOmnipusHome() + "/system"
	cmd := newVerifyCommand()
	// We cannot Execute the command without a real credential store, but
	// we can validate the flag default and confirm the help text mentions
	// the default path.
	dirFlag := cmd.Flags().Lookup("dir")
	require.NotNil(t, dirFlag)
	require.Equal(t, "", dirFlag.DefValue, "--dir defaults empty so runVerify uses $OMNIPUS_HOME/system")
	require.Contains(t, cmd.Long, "$OMNIPUS_HOME/system")
	_ = expected
}

// TestAuditVerify_FlagWiring confirms --json and --dir are exposed.
func TestAuditVerify_FlagWiring(t *testing.T) {
	cmd := newVerifyCommand()
	require.NotNil(t, cmd.Flags().Lookup("json"))
	require.NotNil(t, cmd.Flags().Lookup("dir"))
}
