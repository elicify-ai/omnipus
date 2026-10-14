package gateway

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

const (
	issue1277SoleRef     = "QA1277_SOLE_REFERENCED_SECRET"
	issue1277WitnessRef  = "QA1277_UNREFERENCED_READABLE_SECRET"
	issue1277WitnessVal  = "qa1277-readable-unreferenced-value"
	issue1277SoleGoodVal = "qa1277-sole-reference-value"
)

// Accepted CHECK F1: the store's readability cannot be inferred from whether
// any CONFIGURED reference resolves. Exactly two entries, only one referenced.
// Providers are keyless; the healthy witness is deliberately not in config.
func newIssue1277SoleRefFixture(t *testing.T) *issue1277Fixture {
	t.Helper()
	f := newIssue1277Fixture(t)
	names, err := f.store.List()
	require.NoError(t, err)
	for _, name := range names {
		require.NoError(t, f.store.Delete(name))
	}
	require.NoError(t, f.store.Set(issue1277SoleRef, issue1277SoleGoodVal))
	require.NoError(t, f.store.Set(issue1277WitnessRef, issue1277WitnessVal))
	f.cfg.Providers[0].APIKeyRef = ""
	f.cfg.Voice = config.VoiceConfig{}
	f.cfg.Tools.Web = config.WebToolsConfig{}
	f.cfg.Tools.Skills.Marketplaces = nil
	f.cfg.Tools.MCP.Enabled = false
	f.cfg.Tools.MCP.Servers = nil
	f.cfg.Mailboxes = nil
	// WeCom has one required secret, unlike LINE's two-reference bundle.
	// Healthy-start controls dial loopback only, never the vendor endpoint.
	f.cfg.Channels = map[string]config.ChannelInstanceConfig{
		"wecom": {Type: "wecom", Enabled: true, BotID: "qa1277-bot", SecretRef: issue1277SoleRef,
			WebSocketURL: "ws://127.0.0.1:1/qa1277"},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	return f
}

func issue1277RequireSoleRefBoundary(t *testing.T, f *issue1277Fixture, cfg *config.Config) {
	t.Helper()
	names, err := f.store.List()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{issue1277SoleRef, issue1277WitnessRef}, names)
	value, err := f.store.Get(issue1277WitnessRef)
	require.NoError(t, err, "instrument: the same key must decrypt the unreferenced store entry")
	require.Equal(t, issue1277WitnessVal, value)
	for _, row := range cfg.Providers {
		require.Empty(t, row.APIKeyRef, "a healthy configured provider ref would hide CHECK F1")
	}
	require.Equal(t, map[string]bool{issue1277SoleRef: true}, buildEnabledRefMap(cfg))
	bundle, errs := credentials.ResolveBundle(cfg, f.store)
	require.Empty(t, bundle, "instrument: NO configured reference may supply a successful witness")
	require.Len(t, errs, 1)
	var auth *credentials.EntryAuthError
	require.ErrorAs(t, errs[0], &auth)
	require.Equal(t, issue1277SoleRef, auth.Name)
}

func issue1277RequireSoleRefDegraded(t *testing.T, api *restAPI) {
	t.Helper()
	mgr := api.agentLoop.GetChannelManager()
	failed := mgr.FailedChannels()
	require.Len(t, failed, 1)
	require.Equal(t, "wecom", failed[0].Name)
	require.Contains(t, failed[0].Err.Error(), issue1277SoleRef)
	if ch, exists := mgr.GetChannel("wecom"); exists {
		require.False(t, ch.IsRunning(), "unreadable sole-ref channel must not serve")
	}
	row := issue1277ChannelRows(t, api)["wecom"]
	require.True(t, row.Enabled, "persisted enabled intent must not be hidden")
	require.NotNil(t, row.Degraded)
	require.True(t, *row.Degraded)
	require.NotNil(t, row.DegradedReason)
	require.Contains(t, *row.DegradedReason, issue1277SoleRef)
}

// #1277 founder 2026-10-12; accepted CHECK F1: one tampered entry, readable store.
func TestIssue1277_UnreferencedHealthyEntryWitnessAllowsBoot(t *testing.T) {
	f := newIssue1277SoleRefFixture(t)
	issue1277TamperSingleEntry(t, f, issue1277SoleRef)
	issue1277RequireSoleRefBoundary(t, f, f.cfg)
	cfg, bundle, _, err := bootCredentials(f.home, f.configPath)
	require.NoError(t, err, "CHECK F1: a readable unreferenced store entry proves this is not a whole-store key failure")
	require.Empty(t, bundle)
	api := f.startAPI(t, cfg, bundle)
	issue1277RequireSoleRefDegraded(t, api)
	p, model, err := providers.CreateProviderFromConfig(cfg.Providers[0])
	require.NoError(t, err, "the unrelated keyless provider must remain constructible")
	require.Equal(t, "health-probe", model)
	require.NotNil(t, p)
}

// #1277 founder 2026-10-12; accepted CHECK F1 through the actual reload pipeline.
func TestIssue1277_UnreferencedHealthyEntryWitnessAllowsReload(t *testing.T) {
	f := newIssue1277SoleRefFixture(t)
	cfg, bundle, _ := f.boot(t)
	api, svc, msgBus, provider := f.startRuntime(t, cfg, bundle)
	issue1277TamperSingleEntry(t, f, issue1277SoleRef)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err)
	issue1277RequireSoleRefBoundary(t, f, candidate)
	require.NoError(t, executeReload(context.Background(), api.agentLoop, candidate, &provider, svc, msgBus, false),
		"CHECK F1: an empty configured bundle must not override actual store readability")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	degraded, reason := svc.reloadDegradedStatus()
	require.False(t, degraded)
	require.Empty(t, reason)
	require.Empty(t, svc.bundle)
	issue1277RequireSoleRefDegraded(t, api)
}

// #1277 founder 2026-10-12; accepted CHECK F1 at the distinct REST refresh seam.
func TestIssue1277_UnreferencedHealthyEntryWitnessAllowsRefresh(t *testing.T) {
	f := newIssue1277SoleRefFixture(t)
	cfg, bundle, _ := f.boot(t)
	api := f.startAPI(t, cfg, bundle)
	issue1277TamperSingleEntry(t, f, issue1277SoleRef)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	issue1277RequireSoleRefBoundary(t, f, f.cfg)
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath),
		"CHECK F1: readable store plus one bad configured secret must still apply Settings refresh")
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireSoleRefBoundary(t, f, api.agentLoop.GetConfig())
}

func TestIssue1277_SoleHealthyRefWithUnreferencedWitnessWorks(t *testing.T) {
	f := newIssue1277SoleRefFixture(t)
	cfg, bundle, _ := f.boot(t)
	require.Equal(t, credentials.SecretBundle{issue1277SoleRef: issue1277SoleGoodVal}, bundle)
	require.NotContains(t, bundle, issue1277WitnessRef, "unreferenced plaintext must not be injected into the config bundle")
	api, svc, msgBus, provider := f.startRuntime(t, cfg, bundle)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath))
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err)
	require.NoError(t, executeReload(context.Background(), api.agentLoop, candidate, &provider, svc, msgBus, false))
	require.Empty(t, svc.ChannelManager.FailedChannels())
	require.Equal(t, credentials.SecretBundle{issue1277SoleRef: issue1277SoleGoodVal}, svc.bundle)
}

func newIssue1277ProviderOnlyRefresh(t *testing.T) (*issue1277Fixture, *restAPI) {
	t.Helper()
	f := newIssue1277Fixture(t)
	f.cfg.Channels = nil
	f.cfg.Mailboxes = nil
	f.cfg.Voice = config.VoiceConfig{}
	f.cfg.Tools.Web = config.WebToolsConfig{}
	f.cfg.Tools.Skills.Marketplaces = nil
	f.cfg.Tools.MCP.Enabled = false
	f.cfg.Tools.MCP.Servers = nil
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	require.Empty(t, buildEnabledRefMap(cfg), "instrument: provider-only config must not accidentally reach an enabled channel/ref guard")
	api := f.startAPI(t, cfg, bundle)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	return f, api
}

func issue1277RequireVisibleRefreshRefusal(t *testing.T, api *restAPI) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/config", strings.NewReader(`{"gateway":{"log_level":"debug"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleConfig(w, withReAuthAdmin(t, api, r))
	require.Equal(t, http.StatusInternalServerError, w.Code, "store-level refresh rejection must be visible, not HTTP 200")
	var response gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotEmpty(t, response.Error, "the user must receive an error, not only a server-log warning")
}

// Store-level fatal ruling includes refresh, explicitly confirmed for CHECK F2.
func TestIssue1277_RefreshLockedStoreRejectsAndKeepsOldConfig(t *testing.T) {
	f, api := newIssue1277ProviderOnlyRefresh(t)
	before := api.agentLoop.GetConfig()
	api.credStore = credentials.NewStore(filepath.Join(f.home, "credentials.json"))
	require.True(t, api.credStore.IsLocked())
	err := api.refreshConfigAndRewireServices(f.configPath)
	require.ErrorIs(t, err, credentials.ErrStoreLocked, "CHECK F2: a locked store must reject REST refresh")
	require.Same(t, before, api.agentLoop.GetConfig())
	require.Equal(t, "info", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireVisibleRefreshRefusal(t, api)
	require.Same(t, before, api.agentLoop.GetConfig())
}

// Store-level whole-file corruption is not a scoped provider error (CHECK F2).
func TestIssue1277_RefreshWholeFileCorruptProviderStoreRejectsAndKeepsOldConfig(t *testing.T) {
	f, api := newIssue1277ProviderOnlyRefresh(t)
	before := api.agentLoop.GetConfig()
	require.NoError(t, os.WriteFile(filepath.Join(f.home, "credentials.json"), []byte(`{not-json`), 0o600))
	err := api.refreshConfigAndRewireServices(f.configPath)
	var syntax *json.SyntaxError
	require.ErrorAs(t, err, &syntax, "CHECK F2: provider-only whole-file corruption must reject REST refresh")
	require.Contains(t, err.Error(), "corrupted")
	require.Same(t, before, api.agentLoop.GetConfig())
	require.Equal(t, "info", api.agentLoop.GetConfig().Gateway.LogLevel)
	issue1277RequireVisibleRefreshRefusal(t, api)
	require.Same(t, before, api.agentLoop.GetConfig())
}

func TestIssue1277_RefreshHealthyProviderOnlyStoreApplies(t *testing.T) {
	f, api := newIssue1277ProviderOnlyRefresh(t)
	before := api.agentLoop.GetConfig()
	require.NoError(t, api.refreshConfigAndRewireServices(f.configPath))
	require.NotSame(t, before, api.agentLoop.GetConfig())
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	bundle, errs := credentials.ResolveBundle(api.agentLoop.GetConfig(), f.store)
	require.Empty(t, errs)
	require.Equal(t, credentials.SecretBundle{issue1277ProviderRef: issue1277ProviderValue}, bundle)
	value, err := f.store.Get(issue1277ProviderRef)
	require.NoError(t, err)
	require.Equal(t, issue1277ProviderValue, value)
}

// Keep a typed readability check distinct from a caller's bundle-population check.
func TestIssue1277_UnreferencedWitnessActuallyDistinguishesWrongKey(t *testing.T) {
	f := newIssue1277SoleRefFixture(t)
	issue1277TamperSingleEntry(t, f, issue1277SoleRef)
	_, badErr := f.store.Get(issue1277SoleRef)
	require.ErrorIs(t, badErr, credentials.ErrWrongKey)
	good, err := f.store.Get(issue1277WitnessRef)
	require.NoError(t, err)
	require.Equal(t, issue1277WitnessVal, good)
	wrong := credentials.NewStore(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, wrong.UnlockWithKey([]byte(strings.Repeat("x", 32))))
	_, wrongErr := wrong.Get(issue1277WitnessRef)
	require.True(t, errors.Is(wrongErr, credentials.ErrWrongKey), "control: a genuine different key cannot read the same witness")
}
