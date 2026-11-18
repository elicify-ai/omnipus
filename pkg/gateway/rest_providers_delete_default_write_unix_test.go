//go:build linux || darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestDeleteProvider_ReplacementWriteFailurePreservesAllState is #1620 T1c,
// the chief's 2026-11-05 addendum. Fail the REAL atomic config write at step 0,
// not testHookProviderDeleteConfigWrite (which fires at step 2, too late).
// Read-only home permissions leave config readable and the agent subdirectory
// writable, but prevent creating the config writer's temporary file.
func TestDeleteProvider_ReplacementWriteFailurePreservesAllState(t *testing.T) {
	pair := config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"}
	cfg := providerDeleteBaseConfig(pair,
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
		config.AgentConfig{ID: "scout", Name: "Scout",
			Model: &config.AgentModelConfig{Provider: "openrouter", Primary: "z-ai/glm-5"}},
	)
	api, home, auditDir := newProviderDeleteAPI(t, cfg)
	before := providerRemovalState(t, api)
	require.Contains(t, before, "entities/agents/scout.json")
	info, err := os.Stat(home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chmod(home, info.Mode().Perm())) })
	require.NoError(t, os.Chmod(home, 0o500))

	// Test the failure instrument: privileged environments can ignore chmod.
	// They must report BLOCKED, never silently pass this as an atomicity proof.
	probe, probeErr := os.CreateTemp(home, ".replacement-write-probe-*")
	if probeErr == nil {
		require.NoError(t, probe.Close())
		require.NoError(t, os.Remove(probe.Name()))
		t.Fatal("BLOCKED: read-only directory does not prevent writes in this environment; replacement-write failure was not injected")
	}
	require.ErrorIs(t, probeErr, fs.ErrPermission, "the injected failure must be a real filesystem permission denial")
	t.Log("instrument: configuration directory rejects temporary-file creation with permission denied")

	w := doProviderDelete(t, api, "openrouter",
		`{"new_default":{"provider":"anthropic","model":"claude-sonnet-4.6"}}`, nil, true)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "a valid replacement whose save fails must fail the entire removal")
	var result gen.ProviderDeleteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.False(t, result.Deleted, "the provider must not be removed when its replacement could not be saved")
	assert.False(t, result.DefaultChanged, "a failed replacement save must not claim a new default")
	assert.Nil(t, result.NewDefault, "no replacement was applied")
	assert.Equal(t, []string{"openrouter", "anthropic"}, diskProviderIDs(t, home))
	assert.Equal(t, pair, api.agentLoop.GetConfig().Agents.Defaults.DefaultModel)
	assert.Equal(t, before, providerRemovalState(t, api), "failed step 0 must leave config and every agent record byte-identical")
	assert.Equal(t, 0, countAuditEvents(t, auditDir, "provider.deleted"))
}
