package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1277 oracles: the founder's 2026-10-12 gateway-immunity decision and
// ADR-20261011 OSA-7.9. Only the external LINE/model HTTP edge is simulated;
// boot, the encrypted store, channel constructors, manager, REST and reload
// remain real. All values below are synthetic test data, not usable secrets.
const (
	issue1277BrokenRef       = "QA1277_LINE_SECRET"
	issue1277HealthyRef      = "QA1277_HEALTHY_LINE_SECRET"
	issue1277ProviderRef     = "QA1277_PROVIDER_KEY"
	issue1277BrokenAccessRef = "QA1277_LINE_ACCESS"
	issue1277HealthyAccess   = "qa1277-healthy-access"
	issue1277ProviderValue   = "qa1277-provider-value"
	// issue1277UnauthorizedAccess is a credential the fixture recognises and
	// cleanly rejects with 401 — no fixture error, no t.Error: the
	// controlled rejection the connect journey's saved-but-not-working
	// oracle needs (the platform refused the credential, the fixture did
	// not stumble).
	issue1277UnauthorizedAccess = "qa1277-unauthorized-access"
	// Distinct from the pre-existing healthy channel's bearer, so its boot
	// probe cannot satisfy a new connect journey's platform-call assertion.
	issue1277ConnectAccess = "qa1277-connect-access"
)

type issue1277Fixture struct {
	home, configPath string
	cfg              *config.Config
	store            *credentials.Store
	providerCalls    atomic.Int64
	healthyPushes    atomic.Int64
	// authorizedProbes counts bot-info probes that presented the accepted
	// healthy credential — the counted credential-bearing platform request
	// the successful connect journey must show. unauthorizedProbes counts
	// the clean-401 rejections.
	authorizedProbes   atomic.Int64
	unauthorizedProbes atomic.Int64
	connectProbes      atomic.Int64
}

// issue1277Transport redirects LINE's fixed API URL to a loopback protocol
// fixture and refuses every non-fixture host. No real integration is contacted.
type issue1277Transport struct {
	base     *http.Transport
	endpoint *url.URL
}

func (tr *issue1277Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "api.line.me" {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = tr.endpoint.Scheme, tr.endpoint.Host
		r.Host = tr.endpoint.Host
	}
	if r.URL.Host != tr.endpoint.Host {
		return nil, fmt.Errorf("#1277 test refuses non-fixture HTTP host %q", r.URL.Host)
	}
	return tr.base.RoundTrip(r)
}

func newIssue1277Fixture(t *testing.T) *issue1277Fixture {
	t.Helper()
	f := &issue1277Fixture{home: t.TempDir()}
	f.configPath = filepath.Join(f.home, "config.json")
	t.Setenv("HOME", f.home)
	t.Setenv("OMNIPUS_HOME", f.home)
	t.Setenv("OMNIPUS_CONFIG", f.configPath)
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	t.Setenv("OMNIPUS_KEY_FILE", "")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv(issue1277ProviderRef, "")
	previousStore := providers.DefaultCredentialStore
	t.Cleanup(func() { providers.SetDefaultCredentialStore(previousStore) })
	previousLevel := logger.GetLevel()
	t.Cleanup(func() { logger.SetLevel(previousLevel) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert (not require): this callback runs outside the test goroutine.
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/bot/info":
			switch r.Header.Get("Authorization") {
			case "Bearer " + issue1277HealthyAccess, "Bearer qa1277-broken-access":
				f.authorizedProbes.Add(1)
			case "Bearer " + issue1277ConnectAccess:
				f.connectProbes.Add(1)
			case "Bearer " + issue1277UnauthorizedAccess:
				// The controlled rejection: a known test credential the
				// platform refuses, cleanly, with no fixture error.
				f.unauthorizedProbes.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				_, writeErr := io.WriteString(w, `{"message":"invalid bearer"}`)
				assert.NoError(t, writeErr)
				return
			default:
				t.Error("LINE bot-info probe did not use the resolved fixture credential")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, err := io.WriteString(w, `{"userId":"qa1277-bot","displayName":"QA1277"}`)
			assert.NoError(t, err)
		case "/v2/bot/message/push":
			var body map[string]json.RawMessage
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				return
			}
			assert.JSONEq(t, `"qa1277-user"`, string(body["to"]))
			assert.JSONEq(t, `[{"type":"text","text":"healthy channel still works"}]`, string(body["messages"]))
			assert.Equal(t, "Bearer "+issue1277HealthyAccess, r.Header.Get("Authorization"))
			f.healthyPushes.Add(1)
			_, err := io.WriteString(w, `{}`)
			assert.NoError(t, err)
		case "/v1/models":
			// Local custom providers are listed live by Settings. This model
			// catalogue is deliberately public; it cannot supply a missing key.
			_, err := io.WriteString(w, `{"data":[{"id":"health-probe","object":"model"}]}`)
			assert.NoError(t, err)
		case "/v1/chat/completions":
			assert.Equal(t, "Bearer "+issue1277ProviderValue, r.Header.Get("Authorization"))
			var body map[string]json.RawMessage
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
				return
			}
			assert.JSONEq(t, `"health-probe"`, string(body["model"]))
			f.providerCalls.Add(1)
			_, err := io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"healthy provider still works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			assert.NoError(t, err)
		default:
			t.Errorf("unexpected fixture HTTP path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	base := &http.Transport{}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = &issue1277Transport{base: base, endpoint: endpoint}
	t.Cleanup(func() { http.DefaultTransport = previousTransport; base.CloseIdleConnections() })

	f.store = credentials.NewStore(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, credentials.Unlock(f.store))
	for ref, value := range map[string]string{
		issue1277HealthyRef: "qa1277-healthy-secret", issue1277ProviderRef: issue1277ProviderValue,
		issue1277BrokenAccessRef: "qa1277-broken-access", "QA1277_HEALTHY_LINE_ACCESS": issue1277HealthyAccess,
	} {
		require.NoError(t, f.store.Set(ref, value))
	}
	f.cfg = config.DefaultConfig()
	f.cfg.Gateway.Host, f.cfg.Gateway.Port, f.cfg.Gateway.LogLevel = "127.0.0.1", 0, "info"
	f.cfg.Agents.Defaults.Home = filepath.Join(f.home, "agents")
	f.cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "qa1277", Model: "health-probe"}
	f.cfg.Tools.Browser.Enabled = false
	// The fixture's model must have a context length: the qa1277 row is a
	// loopback custom endpoint, and ADR-066 D3's pre-turn gate
	// (pkg/agent/loop_run_turn.go) refuses ANY turn against a local endpoint
	// with no reported window and no override (context_window_unknown) — a
	// turn-running test would die before reaching its tool call. 128000 is
	// the repo's documented window size (resolve_window.go cloudWindowFloor,
	// SC-009); any positive value admits the turn, the scripted provider is
	// what actually answers it.
	f.cfg.Context.ModelOverrides = append(f.cfg.Context.ModelOverrides,
		config.ContextModelOverride{Provider: "qa1277", Model: "health-probe", ContextWindow: 128000})
	f.cfg.Providers = []*config.ModelConfig{{
		Provider: "qa1277", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: server.URL + "/v1", APIKeyRef: issue1277ProviderRef,
	}}
	f.cfg.Channels = map[string]config.ChannelInstanceConfig{
		"line": {Type: "line", Enabled: true, ChannelSecretRef: issue1277BrokenRef,
			ChannelAccessTokenRef: issue1277BrokenAccessRef, WebhookPath: "/webhook/qa1277-line"},
		"line.healthy": {Type: "line", Enabled: true, ChannelSecretRef: issue1277HealthyRef,
			ChannelAccessTokenRef: "QA1277_HEALTHY_LINE_ACCESS", WebhookPath: "/webhook/qa1277-healthy"},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	return f
}

func (f *issue1277Fixture) boot(t *testing.T) (*config.Config, credentials.SecretBundle, *credentials.Store) {
	t.Helper()
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	require.NoError(t, err, "#1277: a single integration's credential failure must not abort gateway boot")
	return cfg, bundle, store
}

// issue1277RuntimeState is test setup state, never a wire-format payload.
type issue1277RuntimeState struct {
	api      *restAPI
	services *services
	bus      *bus.MessageBus
	provider providers.LLMProvider
}

func (f *issue1277Fixture) startRuntime(t *testing.T, cfg *config.Config, bundle credentials.SecretBundle) (*restAPI, *services, *bus.MessageBus, providers.LLMProvider) {
	t.Helper()
	runtime := f.startRuntimeState(t, cfg, bundle)
	return runtime.api, runtime.services, runtime.bus, runtime.provider
}

// startAPI keeps API-only tests from discarding three unrelated return values.
// All constructors, assertions, cleanup and startup still run in the same helper.
func (f *issue1277Fixture) startAPI(t *testing.T, cfg *config.Config, bundle credentials.SecretBundle) *restAPI {
	t.Helper()
	return f.startRuntimeState(t, cfg, bundle).api
}

func (f *issue1277Fixture) startRuntimeState(t *testing.T, cfg *config.Config, bundle credentials.SecretBundle) issue1277RuntimeState {
	t.Helper()
	p, _, err := createStartupProvider(cfg, false)
	require.NoError(t, err)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, p)
	al.SetCredentialResolver(f.store.Get)
	mgr, err := channels.NewManager(cfg, bundle, msgBus, nil)
	require.NoError(t, err, "#1277: credential-degraded channels must not abort manager construction")
	hs := health.NewServer("127.0.0.1", 0)
	mgr.SetupHTTPServer("127.0.0.1:0", hs)
	al.SetChannelManager(mgr)
	api := &restAPI{agentLoop: al, homePath: f.home, credStore: f.store}
	svc := &services{
		homePath: f.home, ChannelManager: mgr, HealthServer: hs, bundle: bundle, credStore: f.store,
		restAPIRef: api, reloadOutcome: &reloadOutcomeTracker{}, manualReloadChan: make(chan struct{}, 1),
	}
	api.reloadOutcome = svc.reloadOutcome
	hs.SetDegradedFunc(svc.reloadDegradedStatus)
	svc.loadConfigForSwap = func() (*config.Config, error) { return config.LoadConfigWithStore(f.configPath, f.store) }
	t.Cleanup(func() { stopAndCleanupServices(svc, 5*time.Second, false) })
	require.NoError(t, mgr.StartAll(context.Background()), "#1277: the shared HTTP listener and healthy channels must start")
	return issue1277RuntimeState{api: api, services: svc, bus: msgBus, provider: p}
}

func issue1277ChannelRows(t *testing.T, api *restAPI) map[string]gen.ChannelEntry {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleChannels(w, httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var rows []gen.ChannelEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	byID := make(map[string]gen.ChannelEntry, len(rows))
	for _, row := range rows {
		require.NotContains(t, byID, row.Id, "a connection must have exactly one Settings row")
		byID[row.Id] = row
	}
	return byID
}

func issue1277RequireHealthyProvider(t *testing.T, al *agent.AgentLoop) {
	t.Helper()
	var row *config.ModelConfig
	for _, candidate := range al.GetConfig().Providers {
		if candidate.Provider == "qa1277" {
			row = candidate
			break
		}
	}
	require.NotNil(t, row, "unrelated provider must remain configured")
	require.Equal(t, issue1277ProviderValue, row.APIKey(), "unrelated provider credential must remain usable")
	p, model, err := providers.CreateProviderFromConfig(row)
	require.NoError(t, err)
	response, err := p.Chat(context.Background(), []providers.Message{{Role: "user", Content: "health probe"}}, nil, model, nil)
	require.NoError(t, err, "unrelated provider must still execute a real HTTP-backed chat call")
	require.NotNil(t, response, "the healthy provider must return its actual response")
	require.Equal(t, "healthy provider still works", response.Content)
}
