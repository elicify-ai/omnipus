// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestSignInStatus_CopilotCheckNeverRunsInGatewayCwd covers the directory the
// Copilot sign-in check runs in when the gateway has no Omnipus home.
//
// copilotCheckWorkspace's comment always said "the Omnipus home, never the
// gateway's own working directory", but with no home it returned "", so the
// CLI ran wherever the gateway process was started. The check runs the CLI
// with --allow-all-tools, whose tools are rooted in that directory, so the
// comment's intent is the safe one: with no home, the check runs in a fresh
// private directory that is removed afterwards.
func TestSignInStatus_CopilotCheckNeverRunsInGatewayCwd(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	api.homePath = ""

	dir := neutralFakeCLIDir(t)
	record := filepath.Join(dir, "cwd.txt")
	// Built-ins only (pwd, printf, redirection): PATH is narrowed to dir.
	script := "#!/bin/bash\npwd > " + fakeCopilotShellQuote(record) + "\nprintf 'ok\\n'\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"), []byte(script), 0o755))
	t.Setenv("PATH", dir)

	require.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api))

	raw, err := os.ReadFile(record)
	require.NoError(t, err, "the fake must have recorded where it ran")
	ranIn := strings.TrimSpace(string(raw))
	require.NotEmpty(t, ranIn)

	gatewayCwd, err := os.Getwd()
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(ranIn); err == nil {
		cwdResolved, cwdErr := filepath.EvalSymlinks(gatewayCwd)
		require.NoError(t, cwdErr)
		assert.NotEqual(t, cwdResolved, resolved,
			"the check must never run in the gateway's own working directory")
	}
	assert.True(t, strings.HasPrefix(filepath.Base(ranIn), "omnipus-copilot-check-"),
		"with no Omnipus home the check must run in its own private directory, ran in %q", ranIn)
	_, statErr := os.Stat(ranIn)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the private check directory must be removed after the check, %q still exists", ranIn)
}

// TestSignInStatus_CopilotCheckRunsInOmnipusHome pins the ordinary case the
// fix must not change: with a home, the check runs there.
func TestSignInStatus_CopilotCheckRunsInOmnipusHome(t *testing.T) {
	api, home := newAuthMethodOnboardingAPI(t)
	require.Equal(t, home, api.homePath)

	dir := neutralFakeCLIDir(t)
	record := filepath.Join(dir, "cwd.txt")
	script := "#!/bin/bash\npwd > " + fakeCopilotShellQuote(record) + "\nprintf 'ok\\n'\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "copilot"), []byte(script), 0o755))
	t.Setenv("PATH", dir)

	require.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api))

	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	ranIn, err := filepath.EvalSymlinks(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	wantHome, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)
	assert.Equal(t, wantHome, ranIn)
	_, statErr := os.Stat(home)
	assert.NoError(t, statErr, "the Omnipus home must never be removed by the check")
}
