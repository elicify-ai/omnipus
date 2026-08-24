// Package gateway — credential redaction regression tests (A1 + A1-regression fixes).
//
// Verifies that SensitiveDataReplacer scrubs resolved credential values even
// though no Config field is of type SecureString any more (all production fields
// are plain-string *Ref fields resolved via credentials.ResolveAll).
//
// Also verifies (TestRefreshConfigAfterSave_PreservesRedaction) that after a
// REST config write via safeUpdateConfigJSON, the replacer is STILL armed with
// the credential — fixing the A1 regression introduced when RefreshConfigFromDisk
// skipped credentials.ResolveBundle and RegisterSensitiveValues.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestSensitiveDataReplacer_ReducesResolvedKey(t *testing.T) {
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("0", 64))
	tmpDir := t.TempDir()
	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))
	require.NoError(t, store.Set("ANTHROPIC_API_KEY", "sk-fake-supersecret"))

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "c",
				APIKeyRef: "ANTHROPIC_API_KEY",
				Model:     "anthropic/claude",
				Provider:  "anthropic",
			},
		},
	}

	plaintexts, errs := credentials.ResolveAll(cfg, store)
	require.Empty(t, errs)

	values := make([]string, 0, len(plaintexts))
	for _, v := range plaintexts {
		values = append(values, v)
	}
	cfg.RegisterSensitiveValues(values)

	got := cfg.SensitiveDataReplacer().Replace("The key is sk-fake-supersecret and should be hidden")
	assert.NotContains(t, got, "sk-fake-supersecret")
	assert.Contains(t, got, "[FILTERED]")
}

// TestRefreshConfigAfterSave_PreservesRedaction is the A1 regression test.
//
// It verifies that after safeUpdateConfigJSON rewires the in-memory config
// (via refreshConfigAndRewireServices), SensitiveDataReplacer still scrubs the
// credential stored in the credential store. The bug class this catches:
// if safeUpdateConfigJSON called RefreshConfigFromDisk (which skipped
// credentials.ResolveBundle and RegisterSensitiveValues), the new in-memory
// config would have an empty registered-sensitive list and the replacer would
// stop scrubbing — meaning newly-added credentials would leak into LLM output
// and audit logs until the next hot-reload cycle.
func TestRefreshConfigAfterSave_PreservesRedaction(t *testing.T) {
	const masterKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	t.Setenv("OMNIPUS_MASTER_KEY", masterKey)
	tmpDir := t.TempDir()

	// Set up a real credential store and populate it with a secret.
	const secretPlaintext = "sk-supersecret-12345"
	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore))
	require.NoError(t, credStore.Set("ANTHROPIC_API_KEY", secretPlaintext))

	// Write a v1 config that references the credential via api_key_ref so that
	// LoadConfigWithStore + ResolveBundle (called by refreshConfigAndRewireServices)
	// resolves the plaintext and arms RegisterSensitiveValues with it.
	diskCfgJSON := `{
  "version": 1,
  "agents": {"defaults": {"model_name": "test-model", "max_tokens": 4096}, "list": []},
  "providers": [{"model_name": "anthropic", "api_key_ref": "ANTHROPIC_API_KEY", "model": "claude-3-5-sonnet-20241022", "provider": "anthropic"}],
  "gateway": {"host": "127.0.0.1", "port": 18080}
}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(diskCfgJSON), 0o600))

	// Build an initial config with the credential registered.
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "anthropic",
				APIKeyRef: "ANTHROPIC_API_KEY",
				Model:     "claude-3-5-sonnet-20241022",
				Provider:  "anthropic",
			},
		},
	}
	// Simulate the boot-time RegisterSensitiveValues call.
	bundle, bundleErrs := credentials.ResolveBundle(cfg, credStore)
	require.Empty(t, bundleErrs, "boot-time ResolveBundle must not error")
	bootValues := make([]string, 0, len(bundle))
	for _, v := range bundle {
		if v != "" {
			bootValues = append(bootValues, v)
		}
	}
	cfg.RegisterSensitiveValues(bootValues)

	// Step 1: Assert initial scrubbing works.
	beforeOutput := cfg.SensitiveDataReplacer().Replace("key=" + secretPlaintext)
	assert.NotContains(t, beforeOutput, secretPlaintext, "replacer must scrub secret before safeUpdateConfigJSON")
	assert.Contains(t, beforeOutput, "[FILTERED]")

	// Step 2: Build a restAPI with the config loaded via agentLoop.
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
		credStore:     credStore,
	}

	// Step 3: Simulate a REST config write (e.g., updating the log level) via
	// safeUpdateConfigJSON — this triggers refreshConfigAndRewireServices, which
	// must re-arm the replacer with credentials from the store.
	err := api.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, _ := m["gateway"].(map[string]any)
		if gw == nil {
			gw = map[string]any{}
		}
		gw["log_level"] = "info"
		m["gateway"] = gw
		return nil
	})
	require.NoError(t, err, "safeUpdateConfigJSON must succeed")

	// Step 4: Assert the replacer on the NEW in-memory config STILL scrubs the
	// credential. This fails against the old RefreshConfigFromDisk path because
	// that path skipped ResolveBundle + RegisterSensitiveValues.
	newCfg := al.GetConfig()
	afterOutput := newCfg.SensitiveDataReplacer().Replace("key=" + secretPlaintext)
	assert.NotContains(t, afterOutput, secretPlaintext,
		"replacer must STILL scrub secret after safeUpdateConfigJSON rewires config (A1 regression)")
	assert.Contains(t, afterOutput, "[FILTERED]",
		"replacer must replace secret with [FILTERED] after config refresh")

	// Step 5: Read the on-disk config to confirm it was actually written.
	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var diskCfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &diskCfg))
	gw, _ := diskCfg["gateway"].(map[string]any)
	assert.Equal(t, "info", gw["log_level"], "log_level must be persisted to disk")
}
