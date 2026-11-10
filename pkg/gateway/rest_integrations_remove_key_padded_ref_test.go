// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracles: COMBO-C1 in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/combo-code.md,
// and Decision #1104, Shared/custom reference safety, in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md.
// Mailbox consumers trim password_ref; the deletion preflight must protect the
// same effective owner in both saved and live config, before any mutation.
// Handler, config writes, encrypted store and environment remain real. Only
// the existing config-loading reload fixture replaces the full reload pipeline.
// Production GREEN and mutation proof are deferred to independent CHECK.

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func TestRemoveSearchKey_PaddedMailboxReferencesRejectUnchangedWithControls(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		liveOnly bool
	}{
		{"persisted_only_spaces", " TAVILY_API_KEY ", false},
		{"persisted_only_tabs", "\tTAVILY_API_KEY\t", false},
		{"live_only_spaces", " TAVILY_API_KEY ", true},
		{"live_only_tabs", "\tTAVILY_API_KEY\t", true},
		{"persisted_exact_ref_control", "TAVILY_API_KEY", false},
		{"live_exact_ref_control", "TAVILY_API_KEY", true},
		{"unshared_success_control", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, user, cfg := newSearchSettingsAPI(t)
			t.Cleanup(api.credStore.Close)
			setPaddedMailboxRemovalOwner(t, api, cfg, tc.ref, tc.liveOnly)
			web := readRolesWebConfig(t, api)
			require.Equal(t, "tavily", web["default_provider"], "fixture must have decided search roles")
			require.Equal(t, "none", web["fallback_provider"])
			require.Equal(t, rolesMarker, web["roles_migrated_at"])
			requireSavedSearchKey(t, api)
			require.Equal(t, searchSettingsSecret, os.Getenv("TAVILY_API_KEY"), "positive injected-key control")
			row := searchRow(t, getIntegrationList(t, api, user), "tavily")
			require.True(t, row.Configured, "fixture must expose a valid saved key")
			require.NotNil(t, row.Usable)
			require.True(t, *row.Usable, "fixture must start with a usable search service")
			beforeSaved, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			beforeLive, err := json.Marshal(api.agentLoop.GetConfig())
			require.NoError(t, err)
			beforeEncrypted, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)
			beforeEnvironment := os.Environ()
			slices.Sort(beforeEnvironment)

			w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
			t.Logf("COMBO-C1: reference=%q live_only=%t HTTP=%d", tc.ref, tc.liveOnly, w.Code)
			if tc.ref == "" {
				requireSearchKeyRemoval(t, w.Code, w.Body.String())
				assertPaddedMailboxUnsharedRemoval(t, api)
				return
			}

			assert.Equal(t, http.StatusConflict, w.Code, "mailbox ownership must reject before mutation; body=%s", w.Body.String())
			// Exact oracle from Decision #1104 and the existing shared-key tests,
			// deliberately independent of the production removeKeyShared constant.
			assert.Equal(t, "This key is also used elsewhere. Give those connections their own key, then try again.", searchSettingError(t, w.Body.Bytes()))
			afterSaved, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			assert.Equal(t, beforeSaved, afterSaved, "shared refusal must preserve the whole saved config byte-for-byte")
			afterLive, err := json.Marshal(api.agentLoop.GetConfig())
			require.NoError(t, err)
			assert.Equal(t, beforeLive, afterLive, "shared refusal must preserve the whole live config")
			afterEncrypted, err := os.ReadFile(api.credStore.Path())
			require.NoError(t, err)
			assert.Equal(t, beforeEncrypted, afterEncrypted, "shared refusal must preserve the encrypted store byte-for-byte")
			afterEnvironment := os.Environ()
			slices.Sort(afterEnvironment)
			// Compare every entry exactly, but never print inherited secret values.
			if !slices.Equal(beforeEnvironment, afterEnvironment) {
				t.Error("shared refusal must preserve every environment entry (values redacted)")
			}
			assert.NotContains(t, w.Body.String(), searchSettingsSecret)
			requireSavedSearchKey(t, api)
		})
	}
}

func setPaddedMailboxRemovalOwner(t *testing.T, api *restAPI, cfg *config.Config, ref string, liveOnly bool) {
	t.Helper()
	const agentID, workspaceID = "mailbox-owner", "mailbox-workspace"
	require.Empty(t, cfg.Mailboxes, "fixture must start without other mailbox owners")
	if ref != "" {
		owner := config.MailboxesConfig{agentID: {
			workspaceID: {
				Enabled: true, WorkspaceID: workspaceID, PasswordRef: ref,
				Username: "mailbox-owner@example.invalid",
				IMAPHost: "imap.example.invalid", SMTPHost: "smtp.example.invalid",
			},
		}}
		if liveOnly {
			cfg.Mailboxes = owner
		} else {
			data, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(data, &raw))
			raw["mailboxes"] = owner
			data, err = json.Marshal(raw)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(api.configPath(), data, 0o600))
		}
	}
	// Check the real loader, not just the raw fixture map. Padding must survive
	// loading, and the other scan must have no mailbox owner that could mask it.
	saved, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	live := api.agentLoop.GetConfig()
	if ref == "" {
		require.Empty(t, saved.Mailboxes, "unshared control must have no saved mailbox owner")
		require.Empty(t, live.Mailboxes, "unshared control must have no live mailbox owner")
		return
	}
	if liveOnly {
		require.Empty(t, saved.Mailboxes, "live-only case must not be protected by the persisted scan")
		mailbox := live.Mailboxes[agentID][workspaceID]
		require.True(t, mailbox.Enabled, "live owner must be enabled")
		require.Equal(t, ref, mailbox.PasswordRef, "live owner must retain the original reference spelling")
	} else {
		require.Empty(t, live.Mailboxes, "persisted-only case must not be protected by the live scan")
		mailbox := saved.Mailboxes[agentID][workspaceID]
		require.True(t, mailbox.Enabled, "persisted owner must be enabled")
		require.Equal(t, ref, mailbox.PasswordRef, "real config loading must retain the original reference spelling")
	}
}

func assertPaddedMailboxUnsharedRemoval(t *testing.T, api *restAPI) {
	t.Helper()
	var missing *credentials.NotFoundError
	_, err := api.credStore.Get("TAVILY_API_KEY")
	require.ErrorAs(t, err, &missing, "unshared control must delete, not blanket-refuse or save an empty key")
	assert.Equal(t, "TAVILY_API_KEY", missing.Name)
	reopened := credentials.NewStore(api.credStore.Path())
	require.NoError(t, credentials.Unlock(reopened))
	t.Cleanup(reopened.Close)
	_, err = reopened.Get("TAVILY_API_KEY")
	require.ErrorAs(t, err, &missing, "unshared deletion must survive reopening the encrypted store")
	assert.Equal(t, "TAVILY_API_KEY", missing.Name)
	web := readRolesWebConfig(t, api)
	section := roleSection(t, web, "tavily")
	assert.Equal(t, false, section["enabled"])
	require.Contains(t, section, "api_key_ref", "disabled service must retain an explicit empty reference")
	assert.Equal(t, "", section["api_key_ref"])
	assert.Equal(t, "tavily", web["default_provider"])
	assert.Equal(t, "none", web["fallback_provider"])
	assert.Equal(t, rolesMarker, web["roles_migrated_at"])
	_, injected := os.LookupEnv("TAVILY_API_KEY")
	assert.False(t, injected, "unshared removal must unset the injected key")
	live := api.agentLoop.GetConfig()
	assert.False(t, live.Tools.Web.Tavily.Enabled, "unshared removal must disable the live service")
	assert.Equal(t, "", live.Tools.Web.Tavily.APIKeyRef)
	assert.False(t, live.Tools.Web.UsableSearchProvider("tavily"))
}
