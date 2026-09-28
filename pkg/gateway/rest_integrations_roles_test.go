// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-096 gateway lane — the Settings → Integrations REST half of the
// web-search provider model: the resolved roles on the wire (GET) and the
// live, role-aware save (PUT).
//
// Oracle: docs/internal/specs/web-search-provider-model-spec.md — sections
// "Settings screen", "Contract shape", "Resolution" (R1–R9, save-time rules),
// and FR-028 / FR-031 / FR-033; BDD scenarios "Save rejects a fallback that
// is the same provider", "An absent fallback uses DuckDuckGo when two
// providers are usable", "A file that names the same provider twice calls it
// once"; TDD tests 20, 21, 22, 23, 26, 40, 44, 52. Field semantics come from
// contracts/components/schemas/IntegrationProvider.yaml and the
// IntegrationProvidersResponse schema in contracts/openapi.yaml — the
// descriptions there are normative.
//
// THE RELOAD IN THESE TESTS: the unit harness's AgentLoop has no production
// reload pipeline, so each PUT test wires a stand-in via SetReloadFunc that
// (a) loads config.json fresh the way executeReload does and (b) refreshes
// the shared config struct in place — the loop has no exported config
// setter, so same-pointer content refresh is the honest equivalent of the
// production config swap. The reload hook is also where a test stands in for
// the reload's InjectFromConfig (which is what makes a stored key visible to
// APIKey()): an inject func that sets the env var INSIDE the reload proves
// the handler judges usability from post-reload state (FR-033) — a handler
// that judged before the reload would see an empty key and fail these.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- harness ---------------------------------------------------------------

const (
	rolesTestUsername = "roles-admin"
	rolesTestPassword = "correcthorse"
)

// newRolesTestAPI builds the same restAPI the re-auth tests use, plus the
// loop's config pointer (so a test can seed Tools.Web directly and the fake
// reload can refresh it in place) and an unlocked credential store, so PUTs
// can store keys.
func newRolesTestAPI(t *testing.T) (*restAPI, *config.UserConfig, *config.Config) {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("0", 64))
	tmpDir := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte(rolesTestPassword), bcrypt.DefaultCost)
	require.NoError(t, err)
	createTestConfigWithUser(t, tmpDir, rolesTestUsername, string(hash))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	store := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))
	api := &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     store,
	}
	user := &config.UserConfig{Username: rolesTestUsername, PasswordHash: string(hash)}
	return api, user, cfg
}

// reauthFor mints a consent token for the sensitive PUT.
func reauthFor(t *testing.T, api *restAPI, user *config.UserConfig) string {
	t.Helper()
	rw := doReAuth(api, user, rolesTestPassword)
	require.Equal(t, http.StatusOK, rw.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
	token, ok := resp["token"].(string)
	require.True(t, ok, "reauth token must be a string")
	return token
}

// putRoles issues PUT /integrations/providers/{id} with a fresh re-auth token.
func putRoles(t *testing.T, api *restAPI, user *config.UserConfig, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return putIntegration(api, user, id, body, reauthFor(t, api, user))
}

// getIntegrationList runs the GET handler and decodes the generated response
// type — the only legal wire type (Hard Constraint #8).
func getIntegrationList(t *testing.T, api *restAPI, user *config.UserConfig) gen.IntegrationProvidersResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleIntegrationProviders(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp gen.IntegrationProvidersResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// searchRow returns the search row for id.
func searchRow(t *testing.T, resp gen.IntegrationProvidersResponse, id string) gen.IntegrationProvider {
	t.Helper()
	for _, p := range resp.Search {
		if string(p.Id) == id {
			return p
		}
	}
	t.Fatalf("search row %q not in response", id)
	return gen.IntegrationProvider{}
}

// writeRolesWebConfig rewrites config.json's tools.web section in place,
// preserving everything else (users, version).
func writeRolesWebConfig(t *testing.T, api *restAPI, web map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	tools, _ := m["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
		m["tools"] = tools
	}
	tools["web"] = web
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

// readRolesWebConfig reads config.json's tools.web section back from disk —
// the persisted truth a reload would load.
func readRolesWebConfig(t *testing.T, api *restAPI) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	tools, _ := m["tools"].(map[string]any)
	web, _ := tools["web"].(map[string]any)
	if web == nil {
		web = map[string]any{}
	}
	return web
}

// roleSection asserts the provider section exists in tools.web and returns
// it — the checked form of web[id].(map[string]any), which the linter
// requires (forcetypeassert).
func roleSection(t *testing.T, web map[string]any, id string) map[string]any {
	t.Helper()
	sec, ok := web[id].(map[string]any)
	require.True(t, ok, "provider section %q missing from tools.web", id)
	return sec
}

// wireRolesReload replaces the harness's absent reload with one that stands
// in for the production executeReload: it loads config.json fresh and
// refreshes the shared config's content in place (the loop keeps the pointer
// and has no exported setter, so a field copy is the honest equivalent of the
// production swap — a whole-struct assign would copy Config's sync.RWMutex,
// which vet forbids). inject, when non-nil, runs INSIDE the reload — the
// stand-in for the reload's InjectFromConfig making stored keys visible.
func wireRolesReload(t *testing.T, cfg *config.Config, api *restAPI, inject func(), failWith error) {
	t.Helper()
	api.agentLoop.SetReloadFunc(func() error {
		if failWith != nil {
			return failWith
		}
		fresh, err := config.LoadConfig(api.configPath())
		if err != nil {
			return err
		}
		if inject != nil {
			inject()
		}
		cfg.Tools = fresh.Tools
		cfg.Providers = fresh.Providers
		cfg.Agents = fresh.Agents
		return nil
	})
}

// rolesMarker is a fixed migration marker for fixtures.
const rolesMarker = "2026-09-27T00:00:00Z"

// rolesFixtureWeb is a tools.web section with decided roles: Tavily is the
// default with its ref stored, DuckDuckGo is on, an explicit none fallback.
func rolesFixtureWeb() map[string]any {
	return map[string]any{
		"default_provider":  "tavily",
		"fallback_provider": "none",
		"roles_migrated_at": rolesMarker,
		"tavily":            map[string]any{"enabled": true, "api_key_ref": "TAVILY_API_KEY"},
		"duckduckgo":        map[string]any{"enabled": true},
	}
}

// seedTavilyKey makes Tavily usable the way the reload's InjectFromConfig
// would: the ref resolves to a process-env value.
func seedTavilyKey(t *testing.T) {
	t.Helper()
	t.Setenv("TAVILY_API_KEY", "k-tavily")
}

// storeWebSearchKeys pre-stores enabled providers' keys in the credential
// store — the mechanism gateway-saved keys actually live in (ADR-004 boot
// contract: the config refresh resolves enabled refs FROM THE STORE and
// rejects a refresh whose enabled ref the store cannot resolve; env-only
// keys are the InjectFromConfig lane, not the store lane). Fixtures that
// mark a keyed provider enabled+ref'd must pre-store its key or the PUT's
// internal refresh 500s before the handler's own logic runs.
func storeWebSearchKeys(t *testing.T, api *restAPI, refs ...string) {
	t.Helper()
	require.NotNil(t, api.credStore, "roles tests need an unlocked credential store")
	for _, ref := range refs {
		require.NoError(t, api.credStore.Set(ref, "stored-key-for-"+strings.ToLower(ref)))
	}
}

// --- GET: the resolved roles on the wire -----------------------------------

// TestIntegrationRolesGET_DecidedRoles pins the read path for a migrated
// install: default_search / fallback_search present, active_search mirroring
// the default, per-row usable/fallback/active from the resolved roles
// (spec "Settings screen" + "Contract shape").
func TestIntegrationRolesGET_DecidedRoles(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb())
	cfg.Tools.Web.DefaultProvider = "tavily"
	cfg.Tools.Web.FallbackProvider = "none"
	cfg.Tools.Web.RolesMigratedAt = rolesMarker
	cfg.Tools.Web.Tavily.Enabled = true
	cfg.Tools.Web.Tavily.APIKeyRef = "TAVILY_API_KEY"
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	resp := getIntegrationList(t, api, user)

	require.NotNil(t, resp.DefaultSearch, "default_search must be present when roles are decided")
	assert.Equal(t, "tavily", *resp.DefaultSearch)
	assert.Nil(t, resp.FallbackSearch, "fallback_provider=none means NO resolved fallback")
	require.NotNil(t, resp.ActiveSearch, "active_search mirrors default_search for old clients")
	assert.Equal(t, "tavily", *resp.ActiveSearch)
	assert.Nil(t, resp.FallbackIgnoredReason, "no ignored reason when the stored fallback is none")
	require.NotNil(t, resp.NativeSearchInEffect, "the native flag is always reported")
	assert.False(t, *resp.NativeSearchInEffect, "prefer_native off → not in effect")

	tavily := searchRow(t, resp, "tavily")
	require.NotNil(t, tavily.Usable)
	assert.True(t, *tavily.Usable, "tavily: enabled with a resolving key")
	require.NotNil(t, tavily.Active)
	assert.True(t, *tavily.Active, "the default row is the active one")
	require.NotNil(t, tavily.Fallback)
	assert.False(t, *tavily.Fallback)
	require.NotNil(t, tavily.FallbackAutomatic)
	assert.False(t, *tavily.FallbackAutomatic)

	ddg := searchRow(t, resp, "duckduckgo")
	require.NotNil(t, ddg.Usable)
	assert.True(t, *ddg.Usable, "duckduckgo: switched on")
	require.NotNil(t, ddg.Active)
	assert.False(t, *ddg.Active)
	require.NotNil(t, ddg.Fallback)
	assert.False(t, *ddg.Fallback, "stored fallback is none — no row is the fallback")

	brave := searchRow(t, resp, "brave")
	require.NotNil(t, brave.Usable)
	assert.False(t, *brave.Usable, "brave: enabled but no key")

	// Voice rows keep today's shape: no search-role fields, active only.
	for _, v := range resp.Voice {
		assert.Nil(t, v.Usable, "voice rows must not carry usable")
		assert.Nil(t, v.Fallback, "voice rows must not carry fallback")
		assert.Nil(t, v.FallbackAutomatic, "voice rows must not carry fallback_automatic")
	}
}

// TestIntegrationRolesGET_Undecided_NoRolesOnWire pins the undecided state:
// the migration has not run (no marker in the file), so default_search and
// fallback_search are ABSENT and no row presents itself as the default or the
// fallback (contract: "Absent when the roles are not yet decided").
func TestIntegrationRolesGET_Undecided_NoRolesOnWire(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	// No tools.web in the file at all — the migration never ran on it.
	cfg.Tools.Web.DefaultProvider = ""
	cfg.Tools.Web.FallbackProvider = ""
	cfg.Tools.Web.RolesMigratedAt = ""

	resp := getIntegrationList(t, api, user)

	assert.Nil(t, resp.DefaultSearch, "undecided: default_search must be absent")
	assert.Nil(t, resp.FallbackSearch, "undecided: fallback_search must be absent")
	assert.Nil(t, resp.ActiveSearch, "undecided: active_search (the mirror) must be absent")
	assert.Nil(t, resp.FallbackIgnoredReason)

	for _, p := range resp.Search {
		require.NotNil(t, p.Active)
		assert.False(t, *p.Active, "undecided: no row is the default (id=%s)", p.Id)
		require.NotNil(t, p.Fallback)
		assert.False(t, *p.Fallback, "undecided: no row is the fallback (id=%s)", p.Id)
		assert.False(t, *p.FallbackAutomatic)
	}
}

// TestIntegrationRolesGET_ExplicitNone_NoFallback pins R2 on the read path:
// fallback_provider "none" resolves to NO fallback (spec Resolution R2).
func TestIntegrationRolesGET_ExplicitNone_NoFallback(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb())
	cfg.Tools.Web.DefaultProvider = "tavily"
	cfg.Tools.Web.FallbackProvider = "none"
	cfg.Tools.Web.RolesMigratedAt = rolesMarker
	cfg.Tools.Web.Tavily.Enabled = true
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	resp := getIntegrationList(t, api, user)
	ddg := searchRow(t, resp, "duckduckgo")
	require.NotNil(t, ddg.Fallback)
	assert.False(t, *ddg.Fallback,
		"R2: an explicit none is not a resolved fallback, even with DuckDuckGo usable")
}

// TestIntegrationRolesGET_SameAsDefault_HealedNotWritten pins R5's wire
// shape: stored fallback == default resolves to no fallback, and the response
// carries fallback_ignored_reason "same_as_default" — the file is healed in
// interpretation, not rewritten (scenario "A file that names the same
// provider twice calls it once").
func TestIntegrationRolesGET_SameAsDefault_HealedNotWritten(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	web := rolesFixtureWeb()
	web["fallback_provider"] = "tavily" // hand-edited same-as-default file
	writeRolesWebConfig(t, api, web)
	cfg.Tools.Web.DefaultProvider = "tavily"
	cfg.Tools.Web.FallbackProvider = "tavily"
	cfg.Tools.Web.RolesMigratedAt = rolesMarker
	cfg.Tools.Web.Tavily.Enabled = true
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	resp := getIntegrationList(t, api, user)
	require.NotNil(t, resp.FallbackIgnoredReason, "R5 must surface the ignored reason")
	assert.Equal(t, "same_as_default", *resp.FallbackIgnoredReason)
	assert.Nil(t, resp.FallbackSearch, "R5: the healed fallback is no fallback")

	tavily := searchRow(t, resp, "tavily")
	require.NotNil(t, tavily.Fallback)
	assert.False(t, *tavily.Fallback, "a provider cannot fall back to itself")

	// The file must NOT have been rewritten by a GET.
	assert.Equal(t, "tavily", readRolesWebConfig(t, api)["fallback_provider"],
		"R5 heals the interpretation without a write")
}

// TestIntegrationRolesGET_AbsentFallback_AutomaticDuckDuckGo pins R3 on the
// read path: an absent stored fallback resolves to DuckDuckGo when DuckDuckGo
// is usable and not the default, and the payload marks it AUTOMATIC
// (scenario "An absent fallback uses DuckDuckGo when two providers are
// usable").
func TestIntegrationRolesGET_AbsentFallback_AutomaticDuckDuckGo(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	web := rolesFixtureWeb()
	delete(web, "fallback_provider") // the absent state
	writeRolesWebConfig(t, api, web)
	cfg.Tools.Web.DefaultProvider = "tavily"
	cfg.Tools.Web.FallbackProvider = "" // absent
	cfg.Tools.Web.RolesMigratedAt = rolesMarker
	cfg.Tools.Web.Tavily.Enabled = true
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	resp := getIntegrationList(t, api, user)
	require.NotNil(t, resp.FallbackSearch)
	assert.Equal(t, "duckduckgo", *resp.FallbackSearch, "R3: absent fallback resolves to DuckDuckGo")
	assert.Nil(t, resp.FallbackIgnoredReason)

	ddg := searchRow(t, resp, "duckduckgo")
	require.NotNil(t, ddg.Fallback)
	assert.True(t, *ddg.Fallback)
	require.NotNil(t, ddg.FallbackAutomatic)
	assert.True(t, *ddg.FallbackAutomatic,
		"the Settings payload says the fallback is automatic (not operator-picked)")

	tavily := searchRow(t, resp, "tavily")
	require.NotNil(t, tavily.FallbackAutomatic)
	assert.False(t, *tavily.FallbackAutomatic)
}

// TestIntegrationRolesGET_UnusableDefaultNotActive pins FR-028 (tests 26/40):
// a stored key name whose resolved value is empty is NOT usable and its row
// is NOT presented as the provider that runs — the badge uses the env-key
// test, not the ref string.
func TestIntegrationRolesGET_UnusableDefaultNotActive(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	// NOTE: TAVILY_API_KEY is pinned EMPTY — this dev machine exports a real
	// one, and "no key in env" is the state under test (APIKey() resolves the
	// ref via os.Getenv at call time).
	t.Setenv("TAVILY_API_KEY", "")
	web := rolesFixtureWeb()
	delete(web, "fallback_provider") // absent → R3
	writeRolesWebConfig(t, api, web)
	cfg.Tools.Web.DefaultProvider = "tavily"
	cfg.Tools.Web.FallbackProvider = ""
	cfg.Tools.Web.RolesMigratedAt = rolesMarker
	cfg.Tools.Web.Tavily.Enabled = true
	cfg.Tools.Web.Tavily.APIKeyRef = "TAVILY_API_KEY"
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	resp := getIntegrationList(t, api, user)
	tavily := searchRow(t, resp, "tavily")
	require.NotNil(t, tavily.Usable)
	assert.False(t, *tavily.Usable,
		"FR-028: a key name with an empty resolved value is not usable")
	require.NotNil(t, tavily.Active)
	assert.False(t, *tavily.Active,
		"FR-028/test 26: active is not that row — it does not run")

	// With the default unusable and the fallback ABSENT, no fallback
	// resolves: R3's automatic DuckDuckGo requires a usable default (its
	// third conjunct), so this is R7 — nobody is called, and DuckDuckGo is
	// not presented as the fallback (K6, gate round 1).
	ddg := searchRow(t, resp, "duckduckgo")
	assert.False(t, ddg.FallbackAutomatic != nil && *ddg.FallbackAutomatic,
		"R3 needs a usable default: DuckDuckGo is not the automatic fallback")
	assert.False(t, ddg.Fallback != nil && *ddg.Fallback,
		"R7: no provider holds the fallback role")
	assert.Nil(t, resp.FallbackSearch, "decided with no fallback: fallback_search is null")
}

// TestIntegrationRolesGET_NativeSearchInEffect pins FR-031 (test 52's payload
// half): when prefer_native is set and the active model's provider reports
// native search, the payload says so. The negative cases: prefer_native off,
// and a provider host without native search.
func TestIntegrationRolesGET_NativeSearchInEffect(t *testing.T) {
	newAPI := func(t *testing.T, apiBase string, preferNative bool) (*restAPI, *config.UserConfig) {
		api, user, cfg := newRolesTestAPI(t)
		cfg.Tools.Web.PreferNative = preferNative
		cfg.Providers = []*config.ModelConfig{{
			Name:     "gpt-test-native",
			Provider: "test-openai-host",
			Protocol: "openai-compatible",
			APIBase:  apiBase,
			Model:    "gpt-test-native",
		}}
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
			Provider: "test-openai-host",
			Model:    "gpt-test-native",
		}
		return api, user
	}

	t.Run("in effect on an openai host", func(t *testing.T) {
		api, user := newAPI(t, "https://api.openai.com/v1", true)
		resp := getIntegrationList(t, api, user)
		require.NotNil(t, resp.NativeSearchInEffect)
		assert.True(t, *resp.NativeSearchInEffect,
			"FR-031: prefer_native on an OpenAI-host model is native search in effect")
	})

	t.Run("not in effect when prefer_native is off", func(t *testing.T) {
		api, user := newAPI(t, "https://api.openai.com/v1", false)
		resp := getIntegrationList(t, api, user)
		require.NotNil(t, resp.NativeSearchInEffect)
		assert.False(t, *resp.NativeSearchInEffect)
	})

	t.Run("not in effect on a non-native host", func(t *testing.T) {
		api, user := newAPI(t, "https://search.example.com/v1", true)
		resp := getIntegrationList(t, api, user)
		require.NotNil(t, resp.NativeSearchInEffect)
		assert.False(t, *resp.NativeSearchInEffect)
	})
}

// --- PUT: the live, role-aware save (FR-033) --------------------------------

// TestIntegrationPut_SetDefaultWithKey_IsLiveAndReady is the journey FR-033
// exists to make possible (test 44 / SC-004 / H7): one PUT stores the key and
// sets the default; the save is made live by the reload; usability is judged
// AFTER the reload; the response is built from post-reload state; and no
// other provider's api_key_ref is deleted (FR-005).
func TestIntegrationPut_SetDefaultWithKey_IsLiveAndReady(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)

	// Pre-seed Brave with a stored, usable key: the save must leave it alone.
	seedBraveRef := map[string]any{"enabled": true, "api_key_ref": "BRAVE_API_KEY"}
	writeRolesWebConfig(t, api, map[string]any{
		"brave": seedBraveRef,
	})
	// The refresh resolves enabled refs from the STORE — store Brave's key
	// the way production does (env alone is the InjectFromConfig lane).
	storeWebSearchKeys(t, api, "BRAVE_API_KEY")
	t.Setenv("BRAVE_API_KEY", "k-brave")

	// The reload stands in for InjectFromConfig: the Tavily key becomes
	// visible ONLY inside the reload. A handler that judged usability before
	// the reload would see an empty key and answer 400. (TAVILY_API_KEY is
	// pinned empty first — this dev machine exports a real one.)
	t.Setenv("TAVILY_API_KEY", "")
	wireRolesReload(t, cfg, api, func() {
		if os.Getenv("TAVILY_API_KEY") != "" {
			t.Error("reload must run before the handler judges usability")
		}
		os.Setenv("TAVILY_API_KEY", "k-tavily") // cleanup below
	}, nil)
	t.Cleanup(func() { os.Unsetenv("TAVILY_API_KEY") })

	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"k-tavily-raw","active":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	resp := gen.IntegrationProvidersResponse{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Post-reload state: tavily is the default and reads ready.
	require.NotNil(t, resp.DefaultSearch)
	assert.Equal(t, "tavily", *resp.DefaultSearch)
	tavily := searchRow(t, resp, "tavily")
	require.NotNil(t, tavily.Usable)
	assert.True(t, *tavily.Usable,
		"FR-028/FR-033: a key stored in the same request reads ready (post-reload state)")

	// Persisted truth: default set, provider switched on, ref written, and —
	// because roles are now decided — the migration marker stamped so the
	// roles migration can never overwrite the operator's choice.
	web := readRolesWebConfig(t, api)
	assert.Equal(t, "tavily", web["default_provider"])
	assert.Equal(t, "BRAVE_API_KEY", roleSection(t, web, "brave")["api_key_ref"],
		"SC-004: the save must leave another provider's api_key_ref in place")
	tavilySec, _ := web["tavily"].(map[string]any)
	require.NotNil(t, tavilySec)
	assert.Equal(t, true, tavilySec["enabled"], "FR-005: setting a default switches the provider on")
	assert.Equal(t, "TAVILY_API_KEY", tavilySec["api_key_ref"])
	assert.NotEmpty(t, web["roles_migrated_at"],
		"a role written through Settings decides the roles — stamp the marker")
}

// TestIntegrationPut_SetFallback_RoleOnWire pins the fallback radio: PUT
// fallback:true makes that provider the stored fallback, live in the same
// request.
func TestIntegrationPut_SetFallback_RoleOnWire(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb())
	storeWebSearchKeys(t, api, "TAVILY_API_KEY")
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "duckduckgo", `{"kind":"search","fallback":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "duckduckgo", web["fallback_provider"])

	resp := gen.IntegrationProvidersResponse{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.FallbackSearch)
	assert.Equal(t, "duckduckgo", *resp.FallbackSearch)
	ddg := searchRow(t, resp, "duckduckgo")
	require.NotNil(t, ddg.Fallback)
	assert.True(t, *ddg.Fallback)
	require.NotNil(t, ddg.FallbackAutomatic)
	assert.False(t, *ddg.FallbackAutomatic,
		"the operator picked the radio — not automatic")
}

// TestIntegrationPut_NoFallback_WritesNone pins "No fallback": fallback:false
// writes the literal none, regardless of which provider row it came from, and
// an absent fallback is NOT materialized to DuckDuckGo when the operator
// explicitly picked No fallback (save-rule table).
func TestIntegrationPut_NoFallback_WritesNone(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	web := rolesFixtureWeb()
	delete(web, "fallback_provider") // absent → R3 would apply
	writeRolesWebConfig(t, api, web)
	storeWebSearchKeys(t, api, "TAVILY_API_KEY")
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "tavily", `{"kind":"search","fallback":false}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	web = readRolesWebConfig(t, api)
	assert.Equal(t, "none", web["fallback_provider"],
		"an explicit none is a choice and is never flipped")

	resp := gen.IntegrationProvidersResponse{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.FallbackSearch, "none → no resolved fallback")
}

// TestIntegrationPut_SameProviderBothRoles_Rejected400 pins the save
// rejection (scenario "Save rejects a fallback that is the same provider",
// test 20): marking one provider default and fallback in one request is 400
// and writes nothing.
func TestIntegrationPut_SameProviderBothRoles_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	writeRolesWebConfig(t, api, map[string]any{}) // no roles stored
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"k-tavily-raw","active":true,"fallback":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	web := readRolesWebConfig(t, api)
	assert.NotContains(t, web, "default_provider", "the rejected save must not write roles")
	assert.NotContains(t, web, "fallback_provider")
}

// TestIntegrationPut_FallbackEqualsStoredDefault_Rejected400 pins the other
// half of "a provider cannot fall back to itself": the fallback radio on the
// row that is ALREADY the stored default is rejected 400 (contract,
// IntegrationProviderUpdateRequest.fallback).
func TestIntegrationPut_FallbackEqualsStoredDefault_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb())
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "tavily", `{"kind":"search","fallback":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "none", web["fallback_provider"],
		"the rejected save must not change the stored fallback")
}

// TestIntegrationPut_ActiveFalse_Rejected400 pins the contract rule: an
// explicit active:false is rejected 400 — roles are moved by setting another
// provider active, they are not unset (a silently accepted no-op is how a UI
// comes to lie).
func TestIntegrationPut_ActiveFalse_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "brave", `{"kind":"search","active":false}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestIntegrationPut_VoiceRoleRejections_Rejected400 pins the voice rules:
// voice rows keep today's behavior — no fallback field at all, and an
// explicit active:false is rejected for the same reason as on search rows.
func TestIntegrationPut_VoiceRoleRejections_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "elevenlabs", `{"kind":"voice","fallback":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "fallback is not valid on a voice row")

	w = putRoles(t, api, user, "elevenlabs", `{"kind":"voice","active":false}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "active:false is rejected on voice rows too")
}

// TestIntegrationPut_KeyOnlySave_NoRoleChange pins D18 (test 44's second
// half): storing a key alone — with no role fields in the request — stores
// the key and changes no role and does not touch the migration marker.
func TestIntegrationPut_KeyOnlySave_NoRoleChange(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb())
	storeWebSearchKeys(t, api, "TAVILY_API_KEY")
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "brave", `{"kind":"search","api_key":"k-brave-raw"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "BRAVE_API_KEY", roleSection(t, web, "brave")["api_key_ref"],
		"the key is stored")
	assert.Equal(t, "tavily", web["default_provider"], "D18: no role changed")
	assert.Equal(t, "none", web["fallback_provider"], "D18: no role changed")
	assert.Equal(t, rolesMarker, web["roles_migrated_at"],
		"D18: a key-only save must not re-stamp the migration marker")
}

// TestIntegrationPut_MaterializesAutomaticFallback pins the save rule "Save
// while the fallback key is absent and R3 would apply, and the operator does
// not pick 'No fallback' → saves the resolved id (duckduckgo)" (test 23).
func TestIntegrationPut_MaterializesAutomaticFallback(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	web := rolesFixtureWeb()
	web["brave"] = map[string]any{"enabled": true, "api_key_ref": "BRAVE_API_KEY"}
	delete(web, "fallback_provider") // absent → R3 would apply
	writeRolesWebConfig(t, api, web)
	// Store lane (refresh resolves enabled refs from the store) AND env lane
	// (the post-reload usability judgment resolves the ref via os.Getenv —
	// this test's fake reload performs no injection).
	storeWebSearchKeys(t, api, "TAVILY_API_KEY", "BRAVE_API_KEY")
	t.Setenv("TAVILY_API_KEY", "k-tavily")
	t.Setenv("BRAVE_API_KEY", "k-brave")
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "tavily", `{"kind":"search","active":true,"api_key":"k-tavily-raw"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	web = readRolesWebConfig(t, api)
	assert.Equal(t, "duckduckgo", web["fallback_provider"],
		"the save materializes the resolved id so the file is no longer absent")
	assert.Equal(t, "BRAVE_API_KEY", roleSection(t, web, "brave")["api_key_ref"],
		"the materialization must not resurrect the ref deletion (FR-005)")
}

// TestIntegrationPut_FallbackTargetNotUsable_Rejected400 pins the save rule
// "Fallback points at a provider that is not enabled → Rejected".
func TestIntegrationPut_FallbackTargetNotUsable_Rejected400(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	seedTavilyKey(t)
	writeRolesWebConfig(t, api, rolesFixtureWeb()) // glm not enabled
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "glm", `{"kind":"search","fallback":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "none", web["fallback_provider"], "the rejected save must not write")
}

// TestIntegrationPut_ReloadFailure_KeepsWritesReports500 pins FR-033's last
// sentence: a reload failure does NOT undo the persisted writes and is
// reported separately.
func TestIntegrationPut_ReloadFailure_KeepsWritesReports500(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	wireRolesReload(t, cfg, api, nil, errors.New("simulated reload failure"))

	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"k","active":true}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"the write is saved but the reload failed — report it, do not 200")
	assert.Contains(t, w.Body.String(), "reload")

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "tavily", web["default_provider"],
		"FR-033: the reload failure must not undo the persisted write")
	tavilySec, _ := web["tavily"].(map[string]any)
	require.NotNil(t, tavilySec)
	assert.Equal(t, true, tavilySec["enabled"])
}

// TestIntegrationPut_DefaultKeyUnresolvedAfterReload_400KeepsWrites pins the
// round-2 save rule: a default whose key still does not resolve AFTER the
// reload is rejected 400 ("needs an API key") — and the persisted write
// stays, naming the step that failed.
func TestIntegrationPut_DefaultKeyUnresolvedAfterReload_400KeepsWrites(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	// The env lane is pinned empty: only the stored body key exists, and the
	// post-reload judgment must reject on it. (This dev machine exports a
	// real TAVILY_API_KEY, which would otherwise satisfy the judgment.)
	t.Setenv("TAVILY_API_KEY", "")
	// Reload runs (config swap) but the key never resolves — no injection.
	wireRolesReload(t, cfg, api, nil, nil)

	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"k","active":true}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "API key")

	web := readRolesWebConfig(t, api)
	assert.Equal(t, "tavily", web["default_provider"],
		"the rejection happens after the write; the write stays")
}

// rawIntegrationBody decodes a response body into a raw JSON map so a test
// can distinguish a JSON null from an absent key — a typed decode into
// gen.IntegrationProvidersResponse cannot (both become a nil *string),
// which is exactly the gap the first implementation fell through.
func rawIntegrationBody(t *testing.T, w *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// rawIntegrationGet runs the GET handler without decoding, returning the raw
// recorder so null-vs-absent assertions can read the body directly.
func rawIntegrationGet(t *testing.T, api *restAPI, user *config.UserConfig) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/providers", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey{}, user))
	w := httptest.NewRecorder()
	api.HandleIntegrationProviders(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	return w
}

// TestIntegrationRolesGET_FallbackSearchNullContract pins the null-vs-absent
// wire rule on the RAW response body (contract, IntegrationProvidersResponse
// .fallback_search): "id of the resolved fallback, or null when there is
// none … Absent when the roles are not yet decided." A typed decode cannot
// see the difference; this test exists so it never regresses silently again.
func TestIntegrationRolesGET_FallbackSearchNullContract(t *testing.T) {
	t.Run("decided + explicit none -> present, null", func(t *testing.T) {
		api, user, cfg := newRolesTestAPI(t)
		writeRolesWebConfig(t, api, rolesFixtureWeb()) // fallback_provider: "none"
		_ = cfg

		m := rawIntegrationBody(t, rawIntegrationGet(t, api, user))
		raw, present := m["fallback_search"]
		require.True(t, present, "decided: the key must be present (body=%s)", m)
		assert.Equal(t, "null", string(raw), "explicit none is the \"No fallback\" state")
	})

	t.Run("decided + stored absent + no automatic fallback -> present, null", func(t *testing.T) {
		api, user, _ := newRolesTestAPI(t)
		fixture := rolesFixtureWeb()
		delete(fixture, "fallback_provider")       // stored value absent
		fixture["default_provider"] = "duckduckgo" // R3 auto-DDG needs default != ddg
		writeRolesWebConfig(t, api, fixture)

		m := rawIntegrationBody(t, rawIntegrationGet(t, api, user))
		raw, present := m["fallback_search"]
		require.True(t, present, "decided: the key must be present (body=%s)", m)
		assert.Equal(t, "null", string(raw), "stored absent + no auto fallback is still the \"No fallback\" state")
	})

	t.Run("decided + provider fallback -> present, string id", func(t *testing.T) {
		api, user, _ := newRolesTestAPI(t)
		fixture := rolesFixtureWeb()
		fixture["fallback_provider"] = "duckduckgo" // a real stored fallback
		writeRolesWebConfig(t, api, fixture)

		m := rawIntegrationBody(t, rawIntegrationGet(t, api, user))
		raw, present := m["fallback_search"]
		require.True(t, present, "decided: the key must be present (body=%s)", m)
		assert.Equal(t, `"duckduckgo"`, string(raw), "the resolved fallback id")
	})

	t.Run("undecided -> key absent", func(t *testing.T) {
		api, user, _ := newRolesTestAPI(t) // no marker: undecided
		m := rawIntegrationBody(t, rawIntegrationGet(t, api, user))
		_, present := m["fallback_search"]
		assert.False(t, present, "undecided: the key must be absent (body=%s)", m)
	})
}

// TestIntegrationPut_FallbackSearchNullContract applies the same raw-JSON
// rule to the PUT response, which returns the same shape.
func TestIntegrationPut_FallbackSearchNullContract(t *testing.T) {
	t.Run("saved none -> present, null", func(t *testing.T) {
		api, user, _ := newRolesTestAPI(t)
		writeRolesWebConfig(t, api, rolesFixtureWeb())
		storeWebSearchKeys(t, api, "TAVILY_API_KEY")

		w := putRoles(t, api, user, "tavily", `{"kind":"search","fallback":false}`)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		m := rawIntegrationBody(t, w)
		raw, present := m["fallback_search"]
		require.True(t, present, "decided: the key must be present (body=%s)", m)
		assert.Equal(t, "null", string(raw), "the saved none must read back as the No-fallback state")
	})

	t.Run("saved fallback -> present, string id", func(t *testing.T) {
		api, user, _ := newRolesTestAPI(t)
		fixture := rolesFixtureWeb()
		fixture["default_provider"] = "brave"
		fixture["brave"] = map[string]any{"enabled": true, "api_key_ref": "BRAVE_API_KEY"}
		writeRolesWebConfig(t, api, fixture)
		storeWebSearchKeys(t, api, "TAVILY_API_KEY", "BRAVE_API_KEY")
		// The post-reload usability check (G4) reads the env lane, and this
		// test's reload stand-in performs no injection — pin both keys so the
		// test does not depend on the machine's environment (CI has none).
		t.Setenv("TAVILY_API_KEY", "k-tavily")
		t.Setenv("BRAVE_API_KEY", "k-brave")

		w := putRoles(t, api, user, "tavily", `{"kind":"search","fallback":true}`)
		require.Equal(t, 200, w.Code, "body=%s", w.Body.String())

		m := rawIntegrationBody(t, w)
		raw, present := m["fallback_search"]
		require.True(t, present, "decided: the key must be present (body=%s)", m)
		assert.Equal(t, `"tavily"`, string(raw), "the saved fallback id")
	})
}

// TestIntegrationPut_SavedKeyLiveThroughRealInject closes the last instrument
// gap the dispatcher named: no earlier test proved a key saved via PUT is
// readable through the tool's APIKey() WITHOUT the test setting the env var
// itself. Here the fake reload stands in only for executeReload's
// scaffolding (tool-policy coverage validation, service restarts); the
// inject step is the REAL credentials.InjectFromConfig — the exact call
// executeReload makes on the production reload path (gateway_reload.go
// "Re-inject provider credentials", symmetric with boot) — and the test
// pins the env lane empty first, so anything APIKey() returns afterwards
// came through the store→env bridge and nothing else.
func TestIntegrationPut_SavedKeyLiveThroughRealInject(t *testing.T) {
	api, user, cfg := newRolesTestAPI(t)
	t.Setenv("TAVILY_API_KEY", "")

	api.agentLoop.SetReloadFunc(func() error {
		fresh, err := config.LoadConfig(api.configPath())
		if err != nil {
			return err
		}
		// The REAL production inject — os.Setenv from the encrypted store.
		if errs := credentials.InjectFromConfig(fresh, api.credStore); len(errs) > 0 {
			return errors.Join(errs...)
		}
		cfg.Tools = fresh.Tools
		cfg.Providers = fresh.Providers
		cfg.Agents = fresh.Agents
		return nil
	})

	w := putRoles(t, api, user, "tavily", `{"kind":"search","api_key":"k-live","active":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// The saved key is live: readable through the tool's APIKey() on the
	// swapped-in config, without this test ever setting TAVILY_API_KEY.
	got := api.agentLoop.GetConfig().Tools.Web.Tavily.APIKey()
	require.Equal(t, "k-live", got,
		"a key saved via PUT must be readable through APIKey() after the reload's real inject")
}
