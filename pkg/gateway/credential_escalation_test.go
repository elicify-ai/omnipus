package gateway

// Regression tests for the credential-resolution escalation coverage gaps
// found by the 14-reviewer sign-off pass on the combined diff that added
// enabledRefFromBundleError / buildEnabledRefMap escalation to
// bootCredentials and executeReload (gateway.go).
//
// Task 1: refreshConfigAndRewireServices (rest.go) — the REST config-write
// path (safeUpdateConfigJSON -> updateConfigJSONLocked ->
// refreshConfigAndRewireServices), reached by nearly every settings-mutating
// REST handler including configureChannel, was the third production call
// site of credentials.ResolveBundle and had been left un-escalated: it
// Warn-only logged ANY bundle error, including a NotFoundError or a
// corrupted-store error on an enabled channel's credential.
//
// Task 2: buildEnabledRefMap (gateway.go) only covered channel refs; a
// corrupted voice / web-search / skill-marketplace credential ref degraded
// to a silent Warn on boot and reload despite credentials.ResolveBundle
// (== ResolveAll, pkg/credentials/inject.go) already producing a bundle
// error for it.
//
// Task 3: HandleProviders' GET list handler (rest.go) did not use
// describeCredentialResolutionError, so a locked/undecryptable vault and "no
// key configured at all" both reported as plain status=disconnected.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled pins Task 2: the
// non-channel categories credentials.ResolveAll can produce a bundle error
// for (voice, web-search tools, skill marketplaces — see
// pkg/credentials/inject.go's nonChannelRefs) must be included in
// buildEnabledRefMap's "in use" set when the owning feature is enabled, and
// excluded when it is disabled — mirroring the channel Enabled gate that
// already existed.
func TestBuildEnabledRefMap_IncludesNonChannelRefsWhenEnabled(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {Enabled: true, TokenRef: "TELEGRAM_REF"},
			"discord":  {Enabled: false, TokenRef: "DISCORD_REF"},
		},
	}
	cfg.Voice.ElevenLabsAPIKeyRef = "ELEVENLABS_REF"
	cfg.Voice.GroqAPIKeyRef = "GROQ_VOICE_REF"
	cfg.Tools.Web.Brave = config.BraveConfig{Enabled: true, APIKeyRef: "BRAVE_REF"}
	cfg.Tools.Web.Tavily = config.TavilyConfig{Enabled: false, APIKeyRef: "TAVILY_REF"}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: true, AuthTokenRef: "CLAWHUB_REF"},
		{Name: "github", Type: "github", Enabled: false, TokenRef: "GITHUB_REF"},
	}

	m := buildEnabledRefMap(cfg)

	assert.True(t, m["TELEGRAM_REF"], "enabled channel ref must be in the map")
	assert.False(t, m["DISCORD_REF"], "disabled channel ref must NOT be in the map")

	assert.True(t, m["ELEVENLABS_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")
	assert.True(t, m["GROQ_VOICE_REF"], "voice refs have no separate enabled toggle — ref presence IS in-use")

	assert.True(t, m["BRAVE_REF"], "enabled web-search tool ref must be in the map")
	assert.False(t, m["TAVILY_REF"], "disabled web-search tool ref must NOT be in the map")

	assert.True(t, m["CLAWHUB_REF"], "enabled marketplace ref must be in the map")
	assert.False(t, m["GITHUB_REF"], "disabled marketplace ref must NOT be in the map")
}

// TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledChannelCredential
// pins Task 1: refreshConfigAndRewireServices must reject the in-memory
// refresh (return an error, do NOT SwapConfig) when an ENABLED channel's
// credential ref is present in the store but fails to decrypt — a corrupted
// store entry / wrong-master-key scenario, distinct from (and worse than) a
// simple missing ref. This is the REST-write-path counterpart to
// TestExecuteReload_RejectsOnCorruptedEnabledChannelCredential
// (reload_rollback_test.go) and
// TestGatewayBoot_CorruptedCredentialForEnabledChannelFailsFast
// (boot_order_test.go).
func TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "TELEGRAM_TOKEN")

	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19989},
		"providers": []any{},
		"channels": map[string]any{
			"telegram": map[string]any{"type": "telegram", "enabled": true, "token_ref": "TELEGRAM_TOKEN"},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19989},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected refreshConfigAndRewireServices to reject a corrupted enabled-channel credential")
	assert.Contains(t, err.Error(), "TELEGRAM_TOKEN")
	assert.Contains(t, err.Error(), "failed to resolve")

	// The broken config must NOT have been swapped into the live in-memory
	// pointer — the gateway keeps serving the previous good config even
	// though config.json on disk was already durably written before this
	// method ran.
	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestRefreshConfigAndRewireServices_RejectsOnMissingEnabledChannelCredential
// pins the other half of Task 1: a genuinely missing (NotFoundError)
// credential ref on an ENABLED channel must also be rejected, not merely the
// "worse than missing" corrupted case above — this path previously
// Warn-logged even a flatly missing ref on an enabled channel.
func TestRefreshConfigAndRewireServices_RejectsOnMissingEnabledChannelCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore))
	// Deliberately do NOT store "TELEGRAM_TOKEN" — the ref is genuinely absent.

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19990},
		"providers": []any{},
		"channels": map[string]any{
			"telegram": map[string]any{"type": "telegram", "enabled": true, "token_ref": "TELEGRAM_TOKEN"},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19990},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected rejection when an enabled channel's credential ref is genuinely missing, not just Warn-logged")
	assert.Contains(t, err.Error(), "TELEGRAM_TOKEN")
	assert.Contains(t, err.Error(), "not found in store")

	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledMailboxCredential
// pins the mailbox gap in buildEnabledRefMap: cfg.Mailboxes[agentID]
// [workspaceID].PasswordRef is resolved by credentials.ResolveAll via its
// own dedicated per-(agent,workspace) loop (pkg/credentials/inject.go) — NOT
// part of the nonChannelRefs slice — so buildEnabledRefMap omitted mailbox
// refs entirely despite its doc comment's (factually wrong) claim to mirror
// everything ResolveAll's nonChannelRefs can error on.
//
// bootCredentials and executeReload happened to be masked from this gap by
// their own unconditional InjectFromConfig call, which independently walks
// cfg.Mailboxes and treats ANY mailbox resolution failure as fatal
// regardless of Enabled — see InjectFromConfig, pkg/credentials/inject.go.
// refreshConfigAndRewireServices has no InjectFromConfig call, only the
// buildEnabledRefMap-gated ResolveBundle check below, so this is the call
// site the gap was actually unmasked at: a corrupted enabled-mailbox
// credential ref fell through to a Warn-only log instead of being escalated
// — the opposite of what this whole credential-escalation cluster
// guarantees for every other in-use credential category. This is reached in
// production by setAgentMailbox (rest_mailbox.go) itself, via
// safeUpdateConfigJSON -> updateConfigJSONLocked ->
// refreshConfigAndRewireServices.
func TestRefreshConfigAndRewireServices_RejectsOnCorruptedEnabledMailboxCredential(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "MAILBOX_PW_REF")

	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": tmpDir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 19991},
		"providers": []any{},
		"mailboxes": map[string]any{
			"mia": map[string]any{
				"default": map[string]any{
					"enabled":      true,
					"workspace_id": "default",
					"password_ref": "MAILBOX_PW_REF",
				},
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0o600))

	baseCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 19991},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, baseCfg, msgBus, &restMockProvider{})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	err = api.refreshConfigAndRewireServices(configPath)
	require.Error(t, err,
		"expected refreshConfigAndRewireServices to reject a corrupted enabled-mailbox credential")
	assert.Contains(t, err.Error(), "MAILBOX_PW_REF")
	assert.Contains(t, err.Error(), "failed to resolve")

	// The broken config must NOT have been swapped into the live in-memory
	// pointer, same invariant as the enabled-channel counterpart above.
	assert.Same(t, baseCfg, al.GetConfig(), "in-memory config must not be swapped on rejection")
}

// TestHandleProviders_CorruptedCredential_IsErrorStatus pins Task 3: when a
// provider's api_key_ref is present in config but the credential store
// cannot decrypt it (corrupted entry / wrong master key — worse than "not
// configured"), GET /api/v1/providers must report status=error with a
// remediation message distinguishing it from a provider that was simply
// never configured (which remains status=disconnected — see
// TestHandleProviders_CredStoreRef_EmptyRef_IsDisconnected in
// s1_befixes_test.go).
func TestHandleProviders_CorruptedCredential_IsErrorStatus(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	// Ensure the env-var resolution path (ModelConfig.APIKey) is empty so the
	// only resolution path exercised is the credential store.
	t.Setenv("CORRUPT_PROVIDER_KEY", "")

	tmpDir := t.TempDir()
	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "CORRUPT_PROVIDER_KEY")
	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "claude-sonnet-4-6"},
			"list":     []any{},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "claude-sonnet-4-6",
				"provider":    "anthropic",
				"model":       "claude-sonnet-4-6",
				"api_key_ref": "CORRUPT_PROVIDER_KEY",
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4-6"}, MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "claude-sonnet-4-6",
				Provider:  "anthropic",
				Model:     "claude-sonnet-4-6",
				APIKeyRef: "CORRUPT_PROVIDER_KEY",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, isolateRateLimit(t, r))

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "anthropic" {
			found = true
			assert.Equal(t, "error", p["status"],
				"a locked/undecryptable vault must report status=error, distinguishable from never-configured (Task 3)")
			errMsg, _ := p["error"].(string)
			assert.Contains(t, errMsg, "vault could not be read")
			break
		}
	}
	assert.True(t, found, "anthropic provider must appear in the list even when its credential is corrupted")
}
