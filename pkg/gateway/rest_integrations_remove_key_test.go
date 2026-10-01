// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1104 and QA acceptance list. Expected roles, copy and state below
// were derived from that design, not from the handler's current output.
// The new request field is deliberately sent as HTTP JSON until it exists in
// the generated contract. Existing response types are generated, never copied.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const removeSearchKeyJSON = `{"kind":"search","clear_api_key":true}`
const searchSettingsSecret = "saved-key-1104-1105-do-not-expose"

// The real encrypted store, real disk config and real boot-wired audit logger
// stay inside the boundary. The existing reload fixture ONLY loads config; it
// must not delete a credential, clear an environment value or disable a service
// on the handler's behalf. That would manufacture the promised removal.
func newSearchSettingsAPI(t *testing.T) (*restAPI, *config.UserConfig, *config.Config) {
	t.Helper()
	api, user, cfg := newRolesTestAPI(t)
	cfg.Agents.Defaults.Home = filepath.Join(api.homePath, "agents")
	cfg.Sandbox.AuditLog = true
	cfg.Gateway.ValidateInbound = true
	api.agentLoop = mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	require.NotNil(t, api.agentLoop.AuditLogger(), "fixture must boot the real audit sink")
	t.Setenv("TAVILY_API_KEY", searchSettingsSecret)
	require.NoError(t, api.credStore.Set("TAVILY_API_KEY", searchSettingsSecret))
	web := rolesFixtureWeb()
	web["tavily"] = map[string]any{
		"enabled": true, "api_key_ref": "TAVILY_API_KEY", "search_depth": "basic",
	}
	writeRolesWebConfig(t, api, web)
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	cfg.Tools = fresh.Tools
	wireRolesReload(t, cfg, api, nil, nil)
	return api, user, cfg
}

func requireSearchKeyRemoval(t *testing.T, code int, body string) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("BLOCKED: clear_api_key removal not implemented — required by design Decision #1104; want HTTP 200, got %d: %s", code, body)
	}
}

func searchSettingError(t *testing.T, data []byte) string {
	t.Helper()
	var body gen.ErrorResponse
	require.NoError(t, json.Unmarshal(data, &body))
	return body.Error
}

func requireSavedSearchKey(t *testing.T, api *restAPI) {
	t.Helper()
	key, err := api.credStore.Get("TAVILY_API_KEY")
	require.NoError(t, err)
	require.Equal(t, searchSettingsSecret, key, "rejection must keep the original secret")
}

func removalAuditValues(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	entries := readAuditEntries(t, filepath.Join(api.homePath, "system"), audit.EventSecuritySettingChange)
	var matching []map[string]any
	for _, entry := range entries {
		if entry["resource"] == "integrations.provider" {
			matching = append(matching, entry)
		}
	}
	require.Len(t, matching, 1, "the removal itself must emit exactly one real audit record")
	require.Equal(t, rolesTestUsername, matching[0]["actor"], "audit must identify the consenting actor")
	values, ok := matching[0]["new_value"].(map[string]any)
	require.True(t, ok, "removal audit must contain structured new_value")
	encoded, err := json.Marshal(matching[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), searchSettingsSecret, "audit must never contain the saved secret")
	return values
}

func TestRemoveSearchKey_DeletesEncryptedEntryAndDisablesLiveService(t *testing.T) {
	api, user, cfg := newSearchSettingsAPI(t)
	before := getIntegrationList(t, api, user)
	require.Equal(t, true, searchRow(t, before, "tavily").Configured, "positive fixture control")
	require.Equal(t, true, *searchRow(t, before, "tavily").Usable, "positive fixture control")
	requireSavedSearchKey(t, api)

	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	requireSearchKeyRemoval(t, w.Code, w.Body.String())
	_, err := api.credStore.Get("TAVILY_API_KEY")
	var missing *credentials.NotFoundError
	require.ErrorAs(t, err, &missing, "Delete must remove the entry, not store an empty secret")
	assert.Equal(t, "TAVILY_API_KEY", missing.Name)
	names, err := api.credStore.List()
	require.NoError(t, err)
	assert.NotContains(t, names, "TAVILY_API_KEY")
	// Reopening the encrypted file proves absence survived a store restart.
	reopened := credentials.NewStore(api.credStore.Path())
	require.NoError(t, credentials.Unlock(reopened))
	t.Cleanup(reopened.Close)
	_, err = reopened.Get("TAVILY_API_KEY")
	require.ErrorAs(t, err, &missing, "the persisted encrypted entry must really be absent")

	section := roleSection(t, readRolesWebConfig(t, api), "tavily")
	assert.Equal(t, false, section["enabled"], "persist the disabled state")
	ref, present := section["api_key_ref"]
	assert.True(t, present, "an omitted ref would be restored by defaults on restart")
	assert.Equal(t, "", ref)
	assert.Empty(t, os.Getenv("TAVILY_API_KEY"), "the handler must clear its owned injected value before success")
	assert.False(t, cfg.Tools.Web.UsableSearchProvider("tavily"), "confirmed reload must disable fresh use")
	fresh, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	assert.False(t, fresh.Tools.Web.Tavily.Enabled, "a subsequent load must not restore the service")
	assert.Equal(t, "", fresh.Tools.Web.Tavily.APIKeyRef)

	var resp gen.IntegrationProvidersResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	row := searchRow(t, resp, "tavily")
	assert.False(t, row.Configured)
	require.NotNil(t, row.Usable)
	assert.False(t, *row.Usable)
	assert.NotContains(t, w.Body.String(), searchSettingsSecret)
	// A fresh service-targeted search must refuse before any network access;
	// the diagnostic must not be implemented by leaving the ordinary tool live.
	tool, err := tools.NewWebSearchTool(tools.WebSearchToolOptions{Roles: func() *config.WebToolsConfig { return &cfg.Tools.Web }})
	require.NoError(t, err)
	result := tool.Execute(t.Context(), map[string]any{"query": "Omnipus", "provider": "tavily"})
	require.True(t, result.IsError, "fresh calls cannot use a removed service")
}

func TestRemoveSearchKey_PreservesRawRolesAndMigrationMarker(t *testing.T) {
	for _, tc := range []struct{ name, defaultID, fallbackID string }{
		{"default_with_no_fallback", "tavily", "none"},
		{"default_with_usable_fallback", "tavily", "duckduckgo"},
		{"assigned_fallback", "duckduckgo", "tavily"},
		{"unassigned", "duckduckgo", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, user, cfg := newSearchSettingsAPI(t)
			web := readRolesWebConfig(t, api)
			web["default_provider"], web["fallback_provider"] = tc.defaultID, tc.fallbackID
			writeRolesWebConfig(t, api, web)
			wireRolesReload(t, cfg, api, nil, nil)
			w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
			requireSearchKeyRemoval(t, w.Code, w.Body.String())
			got := readRolesWebConfig(t, api)
			assert.Equal(t, tc.defaultID, got["default_provider"], "never choose a replacement default")
			assert.Equal(t, tc.fallbackID, got["fallback_provider"], "preserve even an explicit none")
			assert.Equal(t, rolesMarker, got["roles_migrated_at"], "removal is not a role mutation")
		})
	}
}

func TestRemoveSearchKey_RequiresSingleUsePasswordConsent(t *testing.T) {
	for _, consent := range []string{"missing", "expired", "replayed"} {
		t.Run(consent, func(t *testing.T) {
			api, user, _ := newSearchSettingsAPI(t)
			before, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			token := ""
			if consent != "missing" {
				token = reauthFor(t, api, user)
				store := api.reauthStoreOrInit()
				if consent == "expired" {
					store.mu.Lock()
					store.tokens[token] = reauthEntry{username: user.Username, expiresAt: time.Now().Add(-time.Second)}
					store.mu.Unlock()
				} else {
					require.True(t, store.consume(token, user.Username), "fixture consumed the token once")
				}
			}
			w := putIntegration(api, user, "tavily", removeSearchKeyJSON, token)
			assert.Equal(t, http.StatusForbidden, w.Code, "Decision #1104 requires the same password consent as saving; body=%s", w.Body.String())
			assert.Equal(t, "this change requires re-typing your password — call POST /api/v1/auth/reauth first", searchSettingError(t, w.Body.Bytes()))
			requireSavedSearchKey(t, api)
			after, err := os.ReadFile(api.configPath())
			require.NoError(t, err)
			assert.Equal(t, before, after, "consent rejection must not write config")
		})
	}
}

func TestRemoveSearchKey_ConsumedTokenCannotRemoveAgain(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	token := reauthFor(t, api, user)
	w := putIntegration(api, user, "tavily", removeSearchKeyJSON, token)
	requireSearchKeyRemoval(t, w.Code, w.Body.String())
	second := putIntegration(api, user, "tavily", removeSearchKeyJSON, token)
	assert.Equal(t, http.StatusForbidden, second.Code, "even an idempotent repeat needs fresh consent")
	assert.Equal(t, "this change requires re-typing your password — call POST /api/v1/auth/reauth first", searchSettingError(t, second.Body.Bytes()))
}

func TestRemoveSearchKey_FalseIsCompatibleButNeverDeletes(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	w := putRoles(t, api, user, "tavily", `{"kind":"search","clear_api_key":false,"api_key":""}`)
	require.Equal(t, http.StatusOK, w.Code, "false keeps existing empty-key no-op semantics; body=%s", w.Body.String())
	requireSavedSearchKey(t, api)
	assert.Equal(t, true, roleSection(t, readRolesWebConfig(t, api), "tavily")["enabled"])
}

func TestRemoveSearchKey_OmittedAndEmptySaveNeverDelete_Control(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":""}`)
	require.Equal(t, http.StatusOK, w.Code, "compatibility control; body=%s", w.Body.String())
	requireSavedSearchKey(t, api)
	assert.Equal(t, true, roleSection(t, readRolesWebConfig(t, api), "tavily")["enabled"])
}

func TestRemoveSearchKey_RejectsMixedFieldsBeforeAnyWrite(t *testing.T) {
	for _, extra := range []string{`"api_key":""`, `"api_key":"new-secret"`, `"active":false`, `"active":true`, `"fallback":false`, `"fallback":true`} {
		t.Run(extra, func(t *testing.T) {
			api, user, _ := newSearchSettingsAPI(t)
			before := readRolesWebConfig(t, api)
			w := putRoles(t, api, user, "tavily", `{"kind":"search","clear_api_key":true,`+extra+`}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "presence, not truthiness, conflicts with removal; body=%s", w.Body.String())
			requireSavedSearchKey(t, api)
			assert.Equal(t, before, readRolesWebConfig(t, api))
		})
	}
}

func TestRemoveSearchKey_RejectsInvalidContractAndNonKeyedServices(t *testing.T) {
	for _, tc := range []struct{ name, id, body string }{
		{"voice", "elevenlabs", `{"kind":"voice","clear_api_key":true}`},
		{"keyless", "duckduckgo", removeSearchKeyJSON},
		{"unknown", "not-a-provider", removeSearchKeyJSON},
		{"wrong_kind", "tavily", `{"kind":"voice","clear_api_key":true}`},
		{"extra_field", "tavily", `{"kind":"search","clear_api_key":true,"query":"private"}`},
		{"wrong_type", "tavily", `{"kind":"search","clear_api_key":"true"}`},
		{"null", "tavily", `{"kind":"search","clear_api_key":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, user, _ := newSearchSettingsAPI(t)
			before := readRolesWebConfig(t, api)
			w := putRoles(t, api, user, tc.id, tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "closed request and keyed-search eligibility; body=%s", w.Body.String())
			requireSavedSearchKey(t, api)
			assert.Equal(t, before, readRolesWebConfig(t, api))
		})
	}
}

func TestRemoveSearchKey_UndecidedRolesRejectUnchanged(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	web := readRolesWebConfig(t, api)
	delete(web, "roles_migrated_at")
	delete(web, "default_provider")
	delete(web, "fallback_provider")
	writeRolesWebConfig(t, api, web)
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	assert.Equal(t, http.StatusConflict, w.Code, "preflight must not let reload choose roles; body=%s", w.Body.String())
	assert.Equal(t, "Choose your default and fallback search services before removing this key.", searchSettingError(t, w.Body.Bytes()))
	requireSavedSearchKey(t, api)
	assert.Equal(t, web, readRolesWebConfig(t, api))
}

func TestRemoveSearchKey_SharedReferenceRejectsUnchanged(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	web := readRolesWebConfig(t, api)
	web["exa"] = map[string]any{"enabled": true, "api_key_ref": "TAVILY_API_KEY"}
	writeRolesWebConfig(t, api, web)
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	assert.Equal(t, http.StatusConflict, w.Code, "another search connection owns the same secret; body=%s", w.Body.String())
	assert.Equal(t, "This key is also used elsewhere. Give those connections their own key, then try again.", searchSettingError(t, w.Body.Bytes()))
	requireSavedSearchKey(t, api)
	assert.Equal(t, web, readRolesWebConfig(t, api))
}

func TestRemoveSearchKey_CustomReferenceRejectsWithoutDeletingCanonicalKey(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	require.NoError(t, api.credStore.Set("CUSTOM_SEARCH_KEY", "custom-secret"))
	t.Setenv("CUSTOM_SEARCH_KEY", "custom-secret")
	web := readRolesWebConfig(t, api)
	roleSection(t, web, "tavily")["api_key_ref"] = "CUSTOM_SEARCH_KEY"
	writeRolesWebConfig(t, api, web)
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	assert.Equal(t, http.StatusConflict, w.Code, "must not delete an unrelated canonical entry; body=%s", w.Body.String())
	assert.Equal(t, "This service uses a key set outside Settings. Remove it from the configuration first.", searchSettingError(t, w.Body.Bytes()))
	requireSavedSearchKey(t, api)
	custom, err := api.credStore.Get("CUSTOM_SEARCH_KEY")
	require.NoError(t, err)
	assert.Equal(t, "custom-secret", custom)
	assert.Equal(t, web, readRolesWebConfig(t, api))
}

func TestRemoveSearchKey_AlreadyMissingEntryIsIdempotent(t *testing.T) {
	api, user, cfg := newSearchSettingsAPI(t)
	require.NoError(t, api.credStore.Delete("TAVILY_API_KEY"))
	web := readRolesWebConfig(t, api)
	roleSection(t, web, "tavily")["enabled"] = false
	roleSection(t, web, "tavily")["api_key_ref"] = ""
	writeRolesWebConfig(t, api, web)
	wireRolesReload(t, cfg, api, nil, nil)
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	requireSearchKeyRemoval(t, w.Code, w.Body.String())
	assert.Empty(t, os.Getenv("TAVILY_API_KEY"), "idempotent removal must also clean a stale owned injection")
	assert.Equal(t, "none", readRolesWebConfig(t, api)["fallback_provider"])
}

func TestRemoveSearchKey_AuditRecordsRemovedOutcomeAndNoSecret(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	requireSearchKeyRemoval(t, w.Code, w.Body.String())
	values := removalAuditValues(t, api)
	assert.Equal(t, "tavily", values["provider"])
	assert.Equal(t, "search", values["kind"])
	assert.Equal(t, "remove_key", values["action"])
	assert.Equal(t, true, values["key_removed"])
	assert.Equal(t, false, values["roles_changed"])
	assert.Equal(t, false, values["enabled"])
	assert.Equal(t, "removed", values["outcome"])
	assert.NotContains(t, values, "failed_stage")
}

func TestRemoveSearchKey_LockedCorruptOrUnwritableStoreIsNotMissing(t *testing.T) {
	for _, failure := range []string{"locked", "corrupt", "unwritable"} {
		t.Run(failure, func(t *testing.T) {
			api, user, _ := newSearchSettingsAPI(t)
			switch failure {
			case "locked":
				api.credStore.Close()
			case "corrupt":
				require.NoError(t, os.WriteFile(api.credStore.Path(), []byte("not-json"), 0o600))
			case "unwritable":
				// A directory at the data-file path fails regardless of uid; no
				// permission assumptions and no production write-hook mutation.
				require.NoError(t, os.Rename(api.credStore.Path(), api.credStore.Path()+".backup"))
				require.NoError(t, os.Mkdir(api.credStore.Path(), 0o700))
			default:
				t.Fatalf("unsupported store failure fixture %q", failure)
			}
			w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
			assert.GreaterOrEqual(t, w.Code, 400, "store failure cannot produce removal success")
			assert.Less(t, w.Code, 600)
			assert.NotContains(t, w.Body.String(), searchSettingsSecret)
			assert.Regexp(t, `(?i)(saved key|credential)`, searchSettingError(t, w.Body.Bytes()), "error must name the failed store stage; an unrelated schema error mentioning clear_api_key is not a valid store-failure result")
		})
	}
}

func TestRemoveSearchKey_ReloadFailureReportsPartialStateAndAuditsIt(t *testing.T) {
	api, user, cfg := newSearchSettingsAPI(t)
	wireRolesReload(t, cfg, api, nil, errors.New("forced-reload-failure"))
	w := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "reload failure cannot claim removal success; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(searchSettingError(t, w.Body.Bytes())), "reload")
	assert.Contains(t, strings.ToLower(searchSettingError(t, w.Body.Bytes())), "not confirmed", "copy must not claim live switching-off without confirmed reload")
	web := readRolesWebConfig(t, api)
	assert.Equal(t, false, roleSection(t, web, "tavily")["enabled"], "partial persisted disabling must remain")
	assert.Equal(t, "", roleSection(t, web, "tavily")["api_key_ref"])
	assert.Equal(t, "tavily", web["default_provider"])
	assert.Equal(t, "none", web["fallback_provider"])
	values := removalAuditValues(t, api)
	assert.Equal(t, "partial_failure", values["outcome"])
	assert.Equal(t, "reload", values["failed_stage"])
	assert.Equal(t, false, values["roles_changed"])
}

func TestRemoveSearchKey_AuditFailureIsVisibleAfterDeletion(t *testing.T) {
	api, user, _ := newSearchSettingsAPI(t)
	token := reauthFor(t, api, user)
	require.NoError(t, api.agentLoop.AuditLogger().Close(), "a closed real sink forces an audit write failure")
	w := putIntegration(api, user, "tavily", removeSearchKeyJSON, token)
	assert.Equal(t, http.StatusInternalServerError, w.Code, "audit failure must never be swallowed into HTTP 200; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(searchSettingError(t, w.Body.Bytes())), "audit")
	_, err := api.credStore.Get("TAVILY_API_KEY")
	var missing *credentials.NotFoundError
	assert.ErrorAs(t, err, &missing, "an audit failure cannot undo the already-deleted key")
	assert.Equal(t, false, roleSection(t, readRolesWebConfig(t, api), "tavily")["enabled"])
}

func TestRemoveSearchKey_AddKeyRestoresUsabilityWithoutReassigningRoles(t *testing.T) {
	api, user, cfg := newSearchSettingsAPI(t)
	removed := putRoles(t, api, user, "tavily", removeSearchKeyJSON)
	requireSearchKeyRemoval(t, removed.Code, removed.Body.String())
	// For save only, the existing reload seam models credential injection.
	wireRolesReload(t, cfg, api, func() {
		key, err := api.credStore.Get("TAVILY_API_KEY")
		require.NoError(t, err)
		require.NoError(t, os.Setenv("TAVILY_API_KEY", key))
	}, nil)
	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"replacement-key"}`)
	require.Equal(t, http.StatusOK, w.Code, "Add uses existing save-enables semantics; body=%s", w.Body.String())
	assert.True(t, cfg.Tools.Web.UsableSearchProvider("tavily"))
	web := readRolesWebConfig(t, api)
	assert.Equal(t, "tavily", web["default_provider"])
	assert.Equal(t, "none", web["fallback_provider"])
	assert.Equal(t, rolesMarker, web["roles_migrated_at"])
}
