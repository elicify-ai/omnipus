// gateway_boot_credentials_test.go: tests for unlock the credential store and resolve secret bundles at boot, including the blocked-provider fallback when the default model's credential never resolves

package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from gateway_boot.go tests 2026-09-15 ---

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
	// BUG 4 (architect finding): MCP server env-var refs must be gated at
	// BOTH the per-server level (srv.Enabled) and the global kill-switch
	// level (cfg.Tools.MCP.Enabled) — a server marked Enabled under a
	// globally-disabled tools.mcp.enabled never actually connects, so its
	// ref must not read as "in use" any more than a disabled channel's does.
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled":  {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_ENABLED_REF"}},
		"mcp-disabled": {Enabled: false, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_DISABLED_REF"}},
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

	assert.True(t, m["MCP_ENABLED_REF"], "an enabled MCP server's env ref must be in the map")
	assert.False(t, m["MCP_DISABLED_REF"], "a disabled MCP server's env ref must NOT be in the map")
}

// TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers proves the second
// half of the MCP gate: even a per-server Enabled=true entry must NOT read as
// "in use" when the global tools.mcp.enabled kill-switch is off, mirroring
// ReconcileMCP's own desired-set gating (pkg/agent/loop_mcp.go: mcpCfg.Enabled
// gates the whole loop before any per-server Enabled check).
func TestBuildEnabledRefMap_MCPGlobalKillSwitchGatesAllServers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = false
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"mcp-enabled": {Enabled: true, Type: "stdio", Command: "npx", EnvRefs: map[string]string{"TOKEN": "MCP_REF"}},
	}

	m := buildEnabledRefMap(cfg)

	assert.False(t, m["MCP_REF"],
		"an MCP server's ref must not be 'in use' when the global tools.mcp.enabled kill-switch is off")
}

// TestDefaultModelCredentialBlocked_ByPair: the limited-mode check matches the
// default model's backing rows by the exact (provider, model) pair — a row
// serving the same model under a DIFFERENT provider is not a candidate.
func TestDefaultModelCredentialBlocked_ByPair(t *testing.T) {
	t.Run("blocked when every row backing the pair has an unresolved ref", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openai", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		reason, blocked := defaultModelCredentialBlocked(cfg)
		require.True(t, blocked)
		assert.Contains(t, reason, "T068_07_UNSET_REF")
		assert.Contains(t, reason, "openai/gpt-4o")
	})
	t.Run("not blocked when the only unresolved row is under another provider", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Provider: "openai", Model: "gpt-4o"},
			}},
			Providers: []*config.ModelConfig{
				{Provider: "openrouter", Model: "gpt-4o", APIKeyRef: "T068_07_UNSET_REF"},
			},
		}
		_, blocked := defaultModelCredentialBlocked(cfg)
		assert.False(t, blocked, "a row under a different provider never backs the pair; that is CreateProvider's not-found error to report")
	})
	t.Run("zero pair is never blocked", func(t *testing.T) {
		_, blocked := defaultModelCredentialBlocked(&config.Config{})
		assert.False(t, blocked)
	})
}

// TestMcpEnabledEnvSensitiveValues_ResolvesOnlyEnabledServers proves the core
// BUG 4 fix: an enabled MCP server's env secret is resolved and returned for
// registration, while a disabled server's secret (or one gated off by the
// global tools.mcp.enabled kill-switch) is not.
func TestMcpEnabledEnvSensitiveValues_ResolvesOnlyEnabledServers(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	require.NoError(t, store.Set("mcp_enabled-srv_TOKEN", "enabled-secret-value"))
	require.NoError(t, store.Set("mcp_disabled-srv_TOKEN", "disabled-secret-value"))

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"enabled-srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_enabled-srv_TOKEN"},
		},
		"disabled-srv": {
			Enabled: false, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_disabled-srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)

	assert.Contains(t, values, "enabled-secret-value",
		"an enabled MCP server's env secret must be resolved for sensitive-value registration")
	assert.NotContains(t, values, "disabled-secret-value",
		"a disabled MCP server's env secret must NOT be resolved/registered")
}

// TestMcpEnabledEnvSensitiveValues_GlobalKillSwitchOffReturnsNothing proves
// the global-gate half: even an Enabled=true server contributes nothing when
// tools.mcp.enabled is off, matching ReconcileMCP's own gating.
func TestMcpEnabledEnvSensitiveValues_GlobalKillSwitchOffReturnsNothing(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	require.NoError(t, store.Set("mcp_srv_TOKEN", "some-secret-value"))

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = false
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)
	assert.Empty(t, values, "global tools.mcp.enabled=false must suppress every MCP server's contribution")
}

// TestMcpEnabledEnvSensitiveValues_DanglingRefIsSwallowed proves a dangling
// (unresolvable) ref does not panic or error out the whole call — it simply
// contributes nothing, consistent with reconcileLocked's own WARN+skip
// handling of the same condition at connect time.
func TestMcpEnabledEnvSensitiveValues_DanglingRefIsSwallowed(t *testing.T) {
	store := newUnlockedTestCredStore(t)
	// Deliberately do NOT store anything under "mcp_srv_TOKEN".

	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {
			Enabled: true, Type: "stdio", Command: "npx",
			EnvRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"},
		},
	}

	values := mcpEnabledEnvSensitiveValues(cfg, store)
	assert.Empty(t, values, "a dangling ref must be swallowed, not panic or surface as a value")
}

// TestMcpEnabledEnvSensitiveValues_NilStoreOrConfig proves the nil-safety
// guards: a nil store or nil config must return nil rather than panicking —
// bootCredentials/executeReload call this unconditionally alongside the
// bundle-derived values.
func TestMcpEnabledEnvSensitiveValues_NilStoreOrConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	assert.Nil(t, mcpEnabledEnvSensitiveValues(cfg, nil))
	assert.Nil(t, mcpEnabledEnvSensitiveValues(nil, newUnlockedTestCredStore(t)))
}

// TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken is the gateway half of
// ADR-068 FR-046's agent-path gap. providers hands the registrar a freshly
// minted token; what has to happen next is that the LIVE config's scrubber
// starts filtering it — and, because RegisterSensitiveValues replaces rather
// than appends, that no OTHER already-protected secret is evicted in the
// process. A registrar that registered only the new value would look correct
// and would silently unprotect everything else.
func TestOAuthSensitiveValueRegistrar_ScrubsRefreshedToken(t *testing.T) {
	store := newRegistrarTestStore(t)
	storeOAuthEntry(t, store, "openai", "stored-access-token", "stored-refresh-token")
	// A SECOND signed-in vendor, and a provider API key reached through a
	// config ref. Both are part of the canonical "complete current set" that
	// boot registers, so both must survive a refresh-triggered
	// re-registration — RegisterSensitiveValues replaces rather than appends,
	// so a registrar that passed only the new token would silently unprotect
	// every one of them.
	storeOAuthEntry(t, store, "xai", "other-vendor-access-token", "other-vendor-refresh-token")
	if err := store.Set("OPENAI_API_KEY", "provider-api-key-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cfg := &config.Config{
		Providers: []*config.ModelConfig{{Provider: "openai", APIKeyRef: "OPENAI_API_KEY"}},
	}

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return cfg }, store)
	registrar("brand-new-access-token", "brand-new-refresh-token")

	replacer := cfg.SensitiveDataReplacer()
	if replacer == nil {
		t.Fatal("SensitiveDataReplacer returned nil after registration")
	}

	for _, secret := range []string{
		"brand-new-access-token",     // handed to the registrar directly
		"brand-new-refresh-token",    // ditto
		"stored-access-token",        // recomputed from the store
		"stored-refresh-token",       // ditto
		"other-vendor-access-token",  // a different vendor's stored tokens
		"other-vendor-refresh-token", // ditto
		"provider-api-key-secret",    // the config-ref-driven bundle
	} {
		out := replacer.Replace("prefix " + secret + " suffix")
		if strings.Contains(out, secret) {
			t.Errorf("secret %q survives the scrubber: %q", secret, out)
		}
	}
}

// TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig: a config reload swaps
// the *config.Config, so the registrar must read it through the getter on
// every call. Capturing the boot-time instance would leave every refresh after
// the first reload registering onto an object nothing consults.
func TestOAuthSensitiveValueRegistrar_ReadsTheLiveConfig(t *testing.T) {
	store := newRegistrarTestStore(t)

	bootCfg := &config.Config{}
	reloadedCfg := &config.Config{}
	live := bootCfg

	registrar := oauthSensitiveValueRegistrar(func() *config.Config { return live }, store)

	live = reloadedCfg
	registrar("post-reload-token")

	if out := reloadedCfg.SensitiveDataReplacer().Replace("post-reload-token"); strings.Contains(out, "post-reload-token") {
		t.Error("the post-reload config's scrubber does not filter the token — the registrar is not reading the live config")
	}
	if out := bootCfg.SensitiveDataReplacer().Replace("post-reload-token"); !strings.Contains(out, "post-reload-token") {
		t.Error("the token was registered onto the stale boot config")
	}
}

// TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies: this runs on
// the refresh path inside a live turn. A nil config (pre-boot, or a test that
// never built one) must be a no-op, never a panic that takes the turn down.
func TestOAuthSensitiveValueRegistrar_SurvivesMissingDependencies(t *testing.T) {
	store := newRegistrarTestStore(t)

	oauthSensitiveValueRegistrar(func() *config.Config { return nil }, store)("tok")
	oauthSensitiveValueRegistrar(nil, store)("tok")
	oauthSensitiveValueRegistrar(func() *config.Config { return &config.Config{} }, nil)("tok")
}

// TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential pins the
// second honesty surface. Once boot survives, the factory would happily build
// an HTTP provider with an EMPTY api key (api_base alone satisfies it), and the
// operator's first message would come back as a bare upstream 401 naming
// neither the provider nor the credential. Instead every turn must answer with
// the real cause.
func TestCreateStartupProvider_BlockedDefaultModelNamesTheCredential(t *testing.T) {
	const ref = "DEGRADED_TEST_BLOCKED_DEFAULT_KEY"
	t.Setenv(ref, "") // ref configured, credential never resolved

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider must not fail when the default model's credential is missing: %v", err)
	}
	if _, ok := p.(*startupBlockedProvider); !ok {
		t.Fatalf(
			"expected a startupBlockedProvider for a default model with an unresolvable credential, got %T "+
				"— an HTTP provider with an empty key would 401 with no mention of the real cause",
			p,
		)
	}
	_, chatErr := p.Chat(context.Background(), nil, nil, "", nil)
	if chatErr == nil {
		t.Fatal("a blocked provider must fail every chat turn")
	}
	// The message names the default PAIR (provider/model), never a row alias.
	if !strings.Contains(chatErr.Error(), ref) || !strings.Contains(chatErr.Error(), "openrouter/openrouter/z-ai/glm-5-turbo") {
		t.Errorf(
			"the chat error must name the model and the missing credential so the operator can act; got: %q",
			chatErr.Error(),
		)
	}
}

// TestCreateStartupProvider_ResolvedCredentialIsNotBlocked is the control: the
// same config with the credential present must build the real provider. Without
// it, the test above would still pass if createStartupProvider blocked
// unconditionally.
func TestCreateStartupProvider_ResolvedCredentialIsNotBlocked(t *testing.T) {
	const ref = "DEGRADED_TEST_RESOLVED_DEFAULT_KEY"
	t.Setenv(ref, "sk-resolved")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a provider whose credential resolves must NOT be blocked")
	}
}

// TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable guards the
// multi-entry case: several providers[] entries may share one model_name for
// load balancing (config.GetModelConfig round-robins over them). One broken
// sibling must not disable a model that still has a working entry.
func TestCreateStartupProvider_LoadBalancedSiblingKeepsModelUsable(t *testing.T) {
	const goodRef = "DEGRADED_TEST_LB_GOOD_KEY"
	const badRef = "DEGRADED_TEST_LB_BAD_KEY"
	t.Setenv(goodRef, "sk-good")
	t.Setenv(badRef, "")

	entry := func(ref string) *config.ModelConfig {
		return &config.ModelConfig{
			Name:      "openrouter-auto",
			Model:     "openrouter/z-ai/glm-5-turbo",
			Provider:  "openrouter",
			APIBase:   "https://openrouter.ai/api/v1",
			APIKeyRef: ref,
		}
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"}},
		},
		Providers: []*config.ModelConfig{entry(badRef), entry(goodRef)},
	}

	p, _, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider: %v", err)
	}
	if _, blocked := p.(*startupBlockedProvider); blocked {
		t.Fatal("a model with at least one usable load-balanced entry must not be blocked")
	}
}

// ---------------------------------------------------------------------------
// M1 — the startup orphan sweep could never sweep openai_OAUTH
// ---------------------------------------------------------------------------

// TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow is the
// M1 regression test. sweepOrphanedProviderCredentials built configuredVendors
// from EVERY cfg.Providers row without applying isSeedTemplateRow, and
// pkg/config/defaults.go seeds `{Provider: "openai"}` as a permanent keyless
// template row — so configuredVendors["openai"] was populated on every install
// and `openai_OAUTH`, the only OAuth grant the product currently issues, was
// structurally unsweepable. If the process died between the config write and
// the credential delete during provider removal, the live access AND refresh
// token survived with nothing in the UI referencing them.
func TestSweepOrphanedProviderCredentials_SweepsOAuthBehindASeedTemplateRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"orphan"}`))
	require.NoError(t, store.Set("openai_API_KEY", "sk-orphan"))

	// Exactly the shipped seed: a keyless template row with a provider
	// identity and nothing else. No operator ever created it.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openai", Model: "gpt-5", APIBase: ""},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: the fixture row must be the seeded template shape")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.Error(t, err,
		"a seeded template row must not protect %s from the orphan sweep", oauthName)
	_, err = store.Get("openai_API_KEY")
	assert.Error(t, err,
		"a seeded template row must not protect openai_API_KEY from the orphan sweep")
}

// TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant
// is the guard on the M1 fix itself, for a mistake the fix made on its first
// attempt and the existing suite caught: filtering the vendor keep-set on
// isSeedTemplateRow ALONE deletes live OAuth grants.
//
// A sign_in row legitimately carries no api_key_ref, no api_base and no
// models — it authenticates with a vendor session, not a key — so it can be
// seed-SHAPED while being a real, operator-configured row whose grant is
// live. Sweeping that is unrecoverable, and strictly worse than the orphan
// M1 set out to reclaim. The row's id mapping to a DIFFERENT vendor is what
// distinguishes it from the shipped api-key seed.
func TestSweepOrphanedProviderCredentials_SeedShapedSignInRowStillProtectsItsGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live","refresh_token":"live-refresh"}`))

	// Deliberately the MINIMAL sign-in row: no auth_method, no api_key_ref,
	// no api_base, no models. isSeedTemplateRow says "template"; it is not.
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Name: "openai-chatgpt", Provider: "openai-chatgpt", Model: "gpt-5.2"},
	}}
	require.True(t, isSeedTemplateRow(cfg.Providers[0]),
		"precondition: this real sign-in row is seed-SHAPED — that is the whole trap")

	sweepOrphanedProviderCredentials(cfg, store, nil)

	_, err := store.Get(oauthName)
	assert.NoError(t, err,
		"a configured sign-in row must protect its vendor's live OAuth grant even when seed-shaped")
}

// TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced pins the
// two keep-sets the M1 filter must NOT weaken. Wrongly deleting a live secret
// is unrecoverable; failing to sweep is merely untidy.
func TestSweepOrphanedProviderCredentials_KeepsConfiguredAndReferenced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))

	oauthName := credentials.OAuthEntryName("openai")
	require.NoError(t, store.Set(oauthName, `{"access_token":"live"}`))
	require.NoError(t, store.Set("anthropic_API_KEY", "sk-live"))
	require.NoError(t, store.Set("weird_API_KEY", "sk-hand-named"))

	cfg := &config.Config{Providers: []*config.ModelConfig{
		// A real, operator-configured openai-chatgpt row: its vendor entry is
		// openai_OAUTH and must survive.
		{Provider: "openai-chatgpt", Model: "gpt-5", AuthMethod: config.AuthMethodSignIn},
		// A real anthropic row.
		{Provider: "anthropic", Model: "claude", APIKeyRef: "anthropic_API_KEY"},
		// A row whose ref was renamed by hand: the belt-and-braces keep-set.
		{Provider: "custom-thing", Model: "m", APIKeyRef: "weird_API_KEY"},
	}}

	sweepOrphanedProviderCredentials(cfg, store, nil)

	for _, name := range []string{oauthName, "anthropic_API_KEY", "weird_API_KEY"} {
		_, err := store.Get(name)
		assert.NoError(t, err, "%s is live and must never be swept", name)
	}
}
