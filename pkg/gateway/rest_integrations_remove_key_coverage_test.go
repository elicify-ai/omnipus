// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1104 (ownership, persisted disabling, partial changes and audit),
// and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-integrations/receipts/a-review-tests.md,
// C1 and I1. The coverage brief additionally requires response key_removed:false.
// These tests keep the handler, encrypted store, config writes and audit real;
// only the existing config-loading reload fixture replaces the absent pipeline.
// RED and mutation proof are deferred to an independent CHECK, not claimed here.

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

func TestRemoveSearchKey_SharedOwnershipCasesRejectUnchangedWithUnsharedControl(t *testing.T) {
	cases := []struct {
		name     string
		addOwner func(*testing.T, map[string]any, *config.Config)
	}{
		{
			name: "live_only_configuration_reference",
			addOwner: func(t *testing.T, raw map[string]any, cfg *config.Config) {
				// The saved owner has been removed, but the live owner still uses
				// the key. A saved-file-only scan would wrongly allow deletion.
				raw["providers"] = []any{}
				cfg.Providers = append(cfg.Providers, &config.ModelConfig{
					Provider: "shared-key-owner", Model: "coverage-model", APIKeyRef: "TAVILY_API_KEY",
				})
			},
		},
		{
			name: "array_nested_reference",
			addOwner: func(t *testing.T, raw map[string]any, cfg *config.Config) {
				// Keep this owner saved-only, so the live scan cannot mask a
				// regression in traversal of the persisted providers array.
				raw["providers"] = []any{map[string]any{
					"provider": "shared-key-owner", "model": "coverage-model", "api_key_ref": "TAVILY_API_KEY",
				}}
			},
		},
		{
			name: "mcp_env_refs_owner",
			addOwner: func(t *testing.T, raw map[string]any, cfg *config.Config) {
				tools, ok := raw["tools"].(map[string]any)
				require.True(t, ok, "fixture must retain the real tools section")
				tools["mcp"] = map[string]any{"servers": map[string]any{
					"shared-key-owner": map[string]any{
						"enabled": true, "command": "coverage-fixture",
						// This env name deliberately does not end in _ref: only
						// ownership of the env_refs value can protect the key.
						"env_refs": map[string]any{"UPSTREAM_TOKEN": "TAVILY_API_KEY"},
					},
				}}
			},
		},
		{name: "unshared_success_control"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, user, cfg := newSearchSettingsAPI(t)
			t.Cleanup(api.credStore.Close)
			data, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(data, &raw))
			if tc.addOwner != nil {
				tc.addOwner(t, raw, cfg)
				data, err = json.Marshal(raw)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(api.configPath(), data, 0o600))
			}
			beforeSaved, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			beforeLive, err := json.Marshal(api.agentLoop.GetConfig())
			require.NoError(t, err)
			beforeEncrypted, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)
			beforeEnvironment := os.Environ()
			slices.Sort(beforeEnvironment)
			requireSavedSearchKey(t, api)
			require.Equal(t, searchSettingsSecret, os.Getenv("TAVILY_API_KEY"), "positive injected-key control")

			w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
			if tc.addOwner == nil {
				requireSearchKeyRemoval(t, w.Code, w.Body.String())
				var missing *credentials.NotFoundError
				_, err = api.credStore.Get("TAVILY_API_KEY")
				require.ErrorAs(t, err, &missing, "unshared control must really delete, not blanket-refuse or save an empty secret")
				assert.Equal(t, "TAVILY_API_KEY", missing.Name)
				reopened := credentials.NewStore(api.credStore.Path())
				require.NoError(t, credentials.Unlock(reopened))
				t.Cleanup(reopened.Close)
				_, err = reopened.Get("TAVILY_API_KEY")
				require.ErrorAs(t, err, &missing, "unshared removal must survive reopening the encrypted store")
				web := readRolesWebConfig(t, api)
				section := roleSection(t, web, "tavily")
				assert.Equal(t, false, section["enabled"])
				require.Contains(t, section, "api_key_ref", "the empty reference must be explicit")
				assert.Equal(t, "", section["api_key_ref"])
				assert.Equal(t, "tavily", web["default_provider"])
				assert.Equal(t, "none", web["fallback_provider"])
				assert.Equal(t, rolesMarker, web["roles_migrated_at"])
				_, injected := os.LookupEnv("TAVILY_API_KEY")
				assert.False(t, injected, "unshared removal must unset the owned injection")
				assert.False(t, api.agentLoop.GetConfig().Tools.Web.UsableSearchProvider("tavily"))
				var response gen.IntegrationProvidersResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				row := searchRow(t, response, "tavily")
				assert.False(t, row.Configured)
				require.NotNil(t, row.Usable)
				assert.False(t, *row.Usable)
				return
			}

			assert.Equal(t, http.StatusConflict, w.Code, "shared ownership must reject before mutation; body=%s", w.Body.String())
			// Exact text is fixed by Decision #1104 and the existing shared-reference oracle.
			assert.Equal(t, "This key is also used elsewhere. Give those connections their own key, then try again.", searchSettingError(t, w.Body.Bytes()))
			afterSaved, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			assert.Equal(t, beforeSaved, afterSaved, "rejection must preserve the whole saved config byte-for-byte")
			afterLive, err := json.Marshal(api.agentLoop.GetConfig())
			require.NoError(t, err)
			assert.Equal(t, beforeLive, afterLive, "rejection must preserve the whole live config")
			afterEncrypted, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)
			assert.Equal(t, beforeEncrypted, afterEncrypted, "rejection must preserve the real encrypted store byte-for-byte")
			afterEnvironment := os.Environ()
			slices.Sort(afterEnvironment)
			assert.Equal(t, beforeEnvironment, afterEnvironment, "rejection must preserve every environment entry, including the injected key")
			requireSavedSearchKey(t, api)
			assert.NotContains(t, w.Body.String(), searchSettingsSecret)
		})
	}
}

// blockSearchRemovalCredentialWrites faults only the filesystem write boundary.
// A directory at the sidecar lock path makes Unix O_RDWR|O_CREATE fail regardless
// of uid, without touching credentials.json. Windows WithFlock never opens its
// sidecar; there an os.Open read handle permits reads but denies replacement
// (Go's syscall.Open shares READ|WRITE, not DELETE). No permissions, sleeps,
// deletion mocks, store hooks or production test branches are involved.
func blockSearchRemovalCredentialWrites(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		file, err := os.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, file.Close()) })
		return
	}
	lockPath := fileutil.SidecarLockPath(path)
	require.NoError(t, os.Remove(lockPath), "the real fixture write must have created its sidecar")
	require.NoError(t, os.Mkdir(lockPath, 0o700))
	t.Cleanup(func() { require.NoError(t, os.Remove(lockPath)) })
}

func TestRemoveSearchKey_DeletionWriteFailureRetainsKeyAndReloadsDisabledState(t *testing.T) {
	api, user, cfg := newSearchSettingsAPI(t)
	t.Cleanup(api.credStore.Close)
	beforeEncrypted, err := os.ReadFile(api.credStore.Path())
	require.NoError(t, err)
	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	expectedWeb := readRolesWebConfig(t, api)
	roleSection(t, expectedWeb, "tavily")["enabled"] = false
	roleSection(t, expectedWeb, "tavily")["api_key_ref"] = ""

	blockSearchRemovalCredentialWrites(t, api.credStore.Path())
	requireSavedSearchKey(t, api)
	def, ok := config.SearchProviderDefByID("tavily")
	require.True(t, ok)
	require.NoError(t, api.preflightSearchKeyRemoval(raw, cfg, def), "the complete real preflight, including decryption, must succeed with the write-only fault active")
	t.Log("real preflight succeeded with the credential-write fault active")
	var reloadAttempts atomic.Int32
	wireRolesReload(t, cfg, api, func() { reloadAttempts.Add(1) }, nil)

	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "deletion write failure must never return a successful response; body=%s", w.Body.String())
	requireSavedSearchKey(t, api)
	afterEncrypted, err := os.ReadFile(api.credStore.Path())
	require.NoError(t, err)
	assert.Equal(t, beforeEncrypted, afterEncrypted, "failed deletion must retain the encrypted data unchanged and readable")
	reopened := credentials.NewStore(api.credStore.Path())
	require.NoError(t, credentials.Unlock(reopened))
	t.Cleanup(reopened.Close)
	retained, err := reopened.Get("TAVILY_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, searchSettingsSecret, retained, "credential retention must survive reopening, not just an in-memory cache")
	assert.Equal(t, expectedWeb, readRolesWebConfig(t, api), "disabled/explicit-empty-reference state must persist while raw roles stay unchanged")
	assert.Equal(t, int32(1), reloadAttempts.Load(), "deletion failure must still attempt the config-loading reload exactly once")
	live := api.agentLoop.GetConfig()
	assert.False(t, live.Tools.Web.Tavily.Enabled, "confirmed reload must switch the service off despite retaining its credential")
	assert.Equal(t, "", live.Tools.Web.Tavily.APIKeyRef)
	assert.False(t, live.Tools.Web.UsableSearchProvider("tavily"))

	values := removalAuditValues(t, api)
	assert.Equal(t, map[string]any{
		"provider": "tavily", "kind": "search", "action": "remove_key",
		"key_removed": false, "roles_changed": false, "enabled": false,
		"outcome": "partial_failure", "failed_stage": "credential_delete",
	}, values, "audit must distinguish a deletion-write failure from failed preflight or a successful removal")
	var response gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	// The design fixes the meaning/copy, not the punctuation used to report
	// stages: check every promised clause without inventing a full-message oracle.
	assert.Contains(t, response.Error, "Could not remove the saved key. The service has been switched off.")
	assert.Contains(t, response.Error, "credential_delete", "the response must name the failed stage")
	assert.True(t, strings.HasSuffix(response.Error, "Try again."), "the error must give the specified retry action")
	assert.NotContains(t, response.Error, "not confirmed", "reload was confirmed; copy must not claim otherwise")
	assert.NotContains(t, response.Error, "The saved key was removed.")
	assert.NotContains(t, w.Body.String(), searchSettingsSecret)
	t.Logf("HTTP %d; reload attempts=%d; audit outcome=%v, failed_stage=%v, key_removed=%v; error=%q", w.Code, reloadAttempts.Load(), values["outcome"], values["failed_stage"], values["key_removed"], response.Error)

	// I1 requires an explicit response key_removed:false, not an absent value
	// or merely the audit field. Inspect generic JSON without a hand-written
	// wire struct; no location was specified, so accept top-level or the
	// generated standard error envelope's structured Details.
	var fields map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &fields))
	keyRemoved, present := fields["key_removed"]
	if !present && response.Details != nil {
		keyRemoved, present = (*response.Details)["key_removed"]
	}
	if !present {
		t.Fatal("BLOCKED: partial-failure response key_removed is not implemented — required by I1 coverage brief (key_removed:false); audit is not response evidence")
	}
	assert.Equal(t, false, keyRemoved, "the response must explicitly report that the retained key was not removed")
}
