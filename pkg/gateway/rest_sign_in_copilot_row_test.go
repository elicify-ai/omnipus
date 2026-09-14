// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestListProviders_CopilotRowReportsLastCheck covers the github-copilot
// provider ROW in GET /providers (ADR-068 FR-009 / FR-034, spec row states
// "signed-in as …" and "session expired").
//
// The row could never say `expired` — or `signed_in` — for Copilot:
// cheapSignInRowStatus only knew codex-cli's login file, so Settings could never
// open the Copilot re-sign-in dialog (ReSignInDialog's "Run `copilot login`
// again, then check"). The row must not find out by running the CLI, because
// that spends a premium request on every list render
// (TestListProviders_NoCopilotVendorFanOut). It now reports what the
// operator's most recent explicit Check sign-in found, while that answer is
// younger than the probe TTL and the CLI is still installed, and nothing
// otherwise.
func TestListProviders_CopilotRowReportsLastCheck(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "github-copilot", Model: "default", AuthMethod: config.AuthMethodSignIn,
	})

	dir := neutralFakeCLIDir(t)
	tally := filepath.Join(dir, "invocations")
	installFakeCopilot(t, dir, "", copilotSessionRejectedStderr, 1, tally)
	t.Setenv("PATH", dir)

	copilotRow := func() gen.Provider {
		t.Helper()
		provs := getProviders(t, api)
		require.Len(t, provs, 1, "got %+v", provs)
		return provs[0]
	}

	assert.Equal(t, gen.ProviderStatusDisconnected, copilotRow().Status,
		"before any Check sign-in the row knows nothing about the login")
	assert.Equal(t, 0, countInvocations(t, tally), "listing must never run the CLI")

	require.Equal(t, gen.SignInStatusStateExpired, copilotStatusState(t, api))
	assert.Equal(t, gen.ProviderStatusExpired, copilotRow().Status,
		"after an expired check the row must say so, so Settings can open the re-sign-in dialog")
	assert.Equal(t, 1, countInvocations(t, tally), "only the explicit check ran the CLI")

	// The operator signs in again and checks.
	installFakeCopilot(t, dir, "ok", "", 0, tally)
	require.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api))
	assert.Equal(t, gen.ProviderStatusSignedIn, copilotRow().Status)
	assert.Equal(t, 1, countInvocations(t, tally), "listing after the check must not run the CLI again")

	// Past the TTL the row stops claiming anything.
	api.copilotProbe.mu.Lock()
	api.copilotProbe.lastAt = time.Now().Add(-copilotProbeCacheTTL - time.Second)
	api.copilotProbe.mu.Unlock()
	assert.Equal(t, gen.ProviderStatusDisconnected, copilotRow().Status,
		"a check older than the TTL must not be shown as the current state")
	assert.Equal(t, 1, countInvocations(t, tally), "an aged-out answer is never refreshed by listing")
}

// TestListProviders_CopilotRowForgetsCheckWhenCLIRemoved covers the cheap
// uninstall case: a recent signed_in check must not be shown once the CLI is
// no longer on this machine (a PATH lookup, no vendor call).
func TestListProviders_CopilotRowForgetsCheckWhenCLIRemoved(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "github-copilot", Model: "default", AuthMethod: config.AuthMethodSignIn,
	})
	putFakeCopilotOnPath(t, "ok", "", 0)
	require.Equal(t, gen.SignInStatusStateSignedIn, copilotStatusState(t, api))

	clearCopilotFromPath(t)

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	assert.Equal(t, gen.ProviderStatusDisconnected, provs[0].Status,
		"with the CLI gone the row must not keep showing the earlier signed_in")
	require.NotNil(t, provs[0].Error)
	assert.Contains(t, *provs[0].Error, "not found on this machine")
}
