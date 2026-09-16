// config_bridge_test.go covers the SEC-24 SSRF wiring in config_bridge.go:
// the bridge is the only place the gateway's SSRF-protected HTTP client
// (security.SSRFChecker.SafeClient) is attached to outbound ClawHub
// marketplace traffic (see pkg/gateway/gateway.go's registry-manager build and
// gateway_boot.go's REST search path). SSRF protection that is silently not
// attached looks identical to protection that is attached, so these tests
// assert behavior through the bridged client — a loopback dial must be
// refused with the checker's real error, and a policy-permitted address must
// be reachable — not merely that a constructor was called.
//
// The allowlist-the-fixture approach follows the established pattern in
// pkg/security/ssrf_test.go (CheckRedirect/SafeClient coverage): httptest
// servers bind 127.0.0.1, which the default policy blocks, so a test that
// must REACH its fixture allowlists exactly that address — standing in for an
// ordinary external address the operator's policy permits.

package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// bridgeResolve resolves credential refs like the production resolve funcs
// (credentials.Bundle.GetString / os.Getenv): a map lookup, empty when unset.
func bridgeResolve(env map[string]string) func(string) string {
	return func(ref string) string { return env[ref] }
}

// bridgeConfig builds a *config.Config carrying the persisted marketplace
// entries (credential-ref form) exactly as the bridge receives it.
func bridgeConfig(entries ...config.MarketplaceConfig) *config.Config {
	return &config.Config{
		Tools: config.ToolsConfig{
			Skills: config.SkillsToolsConfig{
				Marketplaces: entries,
			},
		},
	}
}

// TestConfigBridge_MarketplacesFromConfig_AttachesSSRFClientToClawHubOnly
// pins the attachment contract: the client handed out for a ClawHub entry IS
// the SSRF-protected client the caller passed (same pointer, not a wrapper or
// a fresh client), GitHub entries get none, and a nil client (the agent-loop
// wiring, where SSRF is handled at the gateway) leaves HTTPClient unset so
// the registry falls back to its own default client.
func TestConfigBridge_MarketplacesFromConfig_AttachesSSRFClientToClawHubOnly(t *testing.T) {
	// Exactly what the gateway builds and passes (gateway.go: ssrfChecker.SafeClient()).
	ssrfClient := security.NewSSRFChecker(nil).SafeClient()

	cfg := bridgeConfig(
		config.MarketplaceConfig{Name: "clawhub", Type: config.MarketplaceTypeClawHub, Enabled: true},
		config.MarketplaceConfig{Name: "github", Type: config.MarketplaceTypeGitHub, Enabled: true},
	)

	out := MarketplacesFromConfig(cfg, bridgeResolve(nil), ssrfClient, "/ws")

	require.Len(t, out, 2)
	assert.Same(t, ssrfClient, out[0].HTTPClient,
		"ClawHub entry must carry the exact SSRF-protected client passed in")
	assert.Nil(t, out[1].HTTPClient,
		"GitHub entries must not receive the SSRF client (they use the shared installer)")

	// nil client (SSRF disabled / agent-loop wiring): no attachment, no panic.
	outNil := MarketplacesFromConfig(cfg, bridgeResolve(nil), nil, "/ws")
	require.Len(t, outNil, 2)
	assert.Nil(t, outNil[0].HTTPClient, "nil ssrfClient must leave ClawHub HTTPClient unset")
	assert.Nil(t, outNil[1].HTTPClient)
}

// TestConfigBridge_MarketplacesFromConfig_FieldMapping pins the non-client
// mapping: credential refs resolved through resolve (empty when unset), plain
// fields copied, GitHub workspace injected only into GitHub entries.
func TestConfigBridge_MarketplacesFromConfig_FieldMapping(t *testing.T) {
	env := map[string]string{
		"CLAWHUB_TOKEN": "claw-secret",
		"GH_TOKEN":      "gh-secret",
	}
	cfg := bridgeConfig(
		config.MarketplaceConfig{
			Name:            "clawhub",
			Type:            config.MarketplaceTypeClawHub,
			Enabled:         true,
			BaseURL:         "https://clawhub.example",
			AuthTokenRef:    "CLAWHUB_TOKEN",
			SearchPath:      "/api/v1/search",
			SkillsPath:      "/api/v1/skills",
			DownloadPath:    "/api/v1/download",
			Timeout:         11,
			MaxZipSize:      22,
			MaxResponseSize: 33,
		},
		config.MarketplaceConfig{
			Name:     "github",
			Type:     config.MarketplaceTypeGitHub,
			Enabled:  true,
			TokenRef: "GH_TOKEN",
			Proxy:    "http://proxy.example:8080",
		},
	)

	out := MarketplacesFromConfig(cfg, bridgeResolve(env), nil, "/injected-ws")

	require.Len(t, out, 2)

	claw := out[0]
	assert.Equal(t, "clawhub", claw.Name)
	assert.Equal(t, "claw-secret", claw.AuthToken, "AuthTokenRef must resolve through resolve")
	assert.Equal(t, "https://clawhub.example", claw.BaseURL)
	assert.Equal(t, "/api/v1/search", claw.SearchPath)
	assert.Equal(t, "/api/v1/skills", claw.SkillsPath)
	assert.Equal(t, "/api/v1/download", claw.DownloadPath)
	assert.Equal(t, 11, claw.Timeout)
	assert.Equal(t, 22, claw.MaxZipSize)
	assert.Equal(t, 33, claw.MaxResponseSize)
	assert.Empty(t, claw.Token, "clawhub entry must not consume the github TokenRef")

	gh := out[1]
	assert.Equal(t, "gh-secret", gh.Token, "TokenRef must resolve through resolve")
	assert.Equal(t, "http://proxy.example:8080", gh.Proxy)
	assert.Equal(t, "/injected-ws", gh.Workspace, "empty github Workspace must be injected")
	assert.Empty(t, gh.AuthToken, "github entry must not consume the clawhub AuthTokenRef")

	// Degenerate configs: nil config yields nil; an empty list yields an empty
	// (non-nil) slice — the gateway fans out over whatever it gets.
	assert.Nil(t, MarketplacesFromConfig(nil, bridgeResolve(nil), nil, "/ws"))
	assert.Empty(t, MarketplacesFromConfig(bridgeConfig(), bridgeResolve(nil), nil, "/ws"))
}

// bridgeSearchServer serves one ClawHub search result and counts requests, so
// tests can prove whether the wire was ever reached.
func bridgeSearchServer(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		slug, name, summary, version := "github", "GitHub Integration", "Interact with repos", "1.0.0"
		_ = json.NewEncoder(w).Encode(clawhubSearchResponse{
			Results: []clawhubSearchResult{
				{Score: 0.95, Slug: &slug, DisplayName: &name, Summary: &summary, Version: &version},
			},
		})
	}))
}

// bridgedClawHubEntry runs the full production path from persisted config to a
// ClawHub registry: bridge (MarketplacesFromConfig) → adaptation
// (clawHubConfigFromMarketplace) → NewClawHubRegistry. This is the chain the
// gateway wires, so the returned registry carries whatever client the bridge
// attached (or the default client when none was).
func bridgedClawHubEntry(baseURL string, ssrfClient *http.Client) *ClawHubRegistry {
	cfg := bridgeConfig(config.MarketplaceConfig{
		Name:    "clawhub",
		Type:    config.MarketplaceTypeClawHub,
		Enabled: true,
		BaseURL: baseURL,
	})
	out := MarketplacesFromConfig(cfg, bridgeResolve(nil), ssrfClient, "/ws")
	return NewClawHubRegistry(clawHubConfigFromMarketplace(out[0]))
}

// TestConfigBridge_BridgedClient_RefusesLoopbackDirect drives a request at a
// loopback address through the bridged client itself and asserts it is
// refused with the checker's real dial-time error — the observable property
// that distinguishes the SSRF-protected client from a plain http.Client
// (which would connect and return 200).
func TestConfigBridge_BridgedClient_RefusesLoopbackDirect(t *testing.T) {
	var hits atomic.Int32
	srv := bridgeSearchServer(&hits)
	defer srv.Close()

	client := security.NewSSRFChecker(nil).SafeClient()
	cfg := bridgeConfig(config.MarketplaceConfig{
		Name: "clawhub", Type: config.MarketplaceTypeClawHub, Enabled: true, BaseURL: srv.URL,
	})
	out := MarketplacesFromConfig(cfg, bridgeResolve(nil), client, "/ws")
	require.Len(t, out, 1)
	require.NotNil(t, out[0].HTTPClient, "bridge must attach the client for a clawhub entry")

	resp, err := out[0].HTTPClient.Get(srv.URL + "/api/v1/search")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "loopback request through the bridged client must be refused")
	assert.Contains(t, err.Error(), "SSRF")
	assert.Contains(t, err.Error(), "blocked private IP range")
	assert.Contains(t, err.Error(), "127.0.0.0/8")
	assert.Equal(t, int32(0), hits.Load(), "the internal address must never be reached")
}

// TestConfigBridge_ClawHubSearch_BlockedAtInternalAddress proves the
// protection survives the whole adaptation chain into the registry that
// actually carries marketplace traffic: a ClawHub entry pointed at an
// internal (loopback) address is refused at dial time, with the SSRF error
// surfacing through Search.
func TestConfigBridge_ClawHubSearch_BlockedAtInternalAddress(t *testing.T) {
	var hits atomic.Int32
	srv := bridgeSearchServer(&hits)
	defer srv.Close()

	reg := bridgedClawHubEntry(srv.URL, security.NewSSRFChecker(nil).SafeClient())

	results, err := reg.Search(context.Background(), "github", 5)
	require.Error(t, err, "search against an internal address must be refused")
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "search request failed")
	assert.Contains(t, err.Error(), "SSRF")
	assert.Contains(t, err.Error(), "blocked private IP range")
	assert.Contains(t, err.Error(), "127.0.0.0/8")
	assert.Equal(t, int32(0), hits.Load(), "no request may reach the internal server")
}

// TestConfigBridge_ClawHubSearch_AllowedWhenPolicyPermits proves the bridged
// client does not over-block: through the same full chain, an address the
// policy permits is reached and the response is parsed. Mirrors the
// allowlist-the-fixture pattern from pkg/security/ssrf_test.go (the fixture's
// own loopback address is allowlisted, standing in for a permitted external
// address — no real outbound network call is made).
func TestConfigBridge_ClawHubSearch_AllowedWhenPolicyPermits(t *testing.T) {
	var hits atomic.Int32
	srv := bridgeSearchServer(&hits)
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	permitted := security.NewSSRFChecker([]string{u.Hostname()}).SafeClient()

	reg := bridgedClawHubEntry(srv.URL, permitted)

	results, err := reg.Search(context.Background(), "github", 5)
	require.NoError(t, err, "a policy-permitted address must not be blocked")
	require.Len(t, results, 1)
	assert.Equal(t, "github", results[0].Slug)
	assert.Equal(t, "clawhub", results[0].RegistryName)
	assert.Equal(t, int32(1), hits.Load(), "the permitted request must have reached the server")
}

// TestConfigBridge_ClawHubMarketplaceFromConfig_Matching pins the lookup the
// REST search endpoint relies on: explicit Type=="clawhub" matches, a
// hand-edited untyped entry named "clawhub" matches defensively (with Type
// normalized), and everything else — including a github-TYPED entry that
// merely happens to be named "clawhub" — does not.
func TestConfigBridge_ClawHubMarketplaceFromConfig_Matching(t *testing.T) {
	client := security.NewSSRFChecker(nil).SafeClient()

	tests := []struct {
		name    string
		entries []config.MarketplaceConfig
		wantOK  bool
	}{
		{
			name:    "explicit clawhub type matches",
			entries: []config.MarketplaceConfig{{Name: "hub", Type: config.MarketplaceTypeClawHub}},
			wantOK:  true,
		},
		{
			name:    "untyped entry named clawhub matches (hand-edited config)",
			entries: []config.MarketplaceConfig{{Name: "clawhub"}},
			wantOK:  true,
		},
		{
			name:    "github-typed entry named clawhub does not match",
			entries: []config.MarketplaceConfig{{Name: "clawhub", Type: config.MarketplaceTypeGitHub}},
			wantOK:  false,
		},
		{
			name:    "untyped entry named otherwise does not match",
			entries: []config.MarketplaceConfig{{Name: "other"}},
			wantOK:  false,
		},
		{
			name:    "github-only config does not match",
			entries: []config.MarketplaceConfig{{Name: "github", Type: config.MarketplaceTypeGitHub}},
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := ClawHubMarketplaceFromConfig(bridgeConfig(tc.entries...), bridgeResolve(nil), client)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Same(t, client, entry.HTTPClient,
					"matched ClawHub entry must carry the exact SSRF-protected client")
				assert.Equal(t, config.MarketplaceTypeClawHub, entry.Type, "Type must be normalized to clawhub")
			} else {
				assert.Zero(t, entry, "no match must return the zero MarketplaceConfig")
			}
		})
	}

	_, ok := ClawHubMarketplaceFromConfig(nil, bridgeResolve(nil), client)
	assert.False(t, ok, "nil config must report no ClawHub entry")
}

// TestConfigBridge_ClawHubMarketplaceFromConfig_Fields pins the matched
// entry's mapping: credential ref resolved, plain fields carried over, and no
// client attached when the caller passes nil (SSRF disabled).
func TestConfigBridge_ClawHubMarketplaceFromConfig_Fields(t *testing.T) {
	cfg := bridgeConfig(config.MarketplaceConfig{
		Name:            "clawhub",
		Type:            config.MarketplaceTypeClawHub,
		Enabled:         true,
		BaseURL:         "https://clawhub.example",
		AuthTokenRef:    "CLAWHUB_TOKEN",
		SearchPath:      "/api/v1/search",
		SkillsPath:      "/api/v1/skills",
		DownloadPath:    "/api/v1/download",
		Timeout:         11,
		MaxZipSize:      22,
		MaxResponseSize: 33,
	})

	entry, ok := ClawHubMarketplaceFromConfig(cfg, bridgeResolve(map[string]string{
		"CLAWHUB_TOKEN": "claw-secret",
	}), nil)
	require.True(t, ok)

	assert.Equal(t, "clawhub", entry.Name)
	assert.True(t, entry.Enabled)
	assert.Equal(t, "https://clawhub.example", entry.BaseURL)
	assert.Equal(t, "claw-secret", entry.AuthToken)
	assert.Equal(t, "/api/v1/search", entry.SearchPath)
	assert.Equal(t, "/api/v1/skills", entry.SkillsPath)
	assert.Equal(t, "/api/v1/download", entry.DownloadPath)
	assert.Equal(t, 11, entry.Timeout)
	assert.Equal(t, 22, entry.MaxZipSize)
	assert.Equal(t, 33, entry.MaxResponseSize)
	assert.Nil(t, entry.HTTPClient, "nil ssrfClient must leave HTTPClient unset")
}

// TestConfigBridge_FirstGitHubMarketplaceCreds pins the installer-seeding
// lookup: the FIRST github entry's resolved token and proxy, empty values
// when no github entry exists. Enabled is not consulted — the first
// github-typed entry wins even when disabled — so the disabled-first case
// below is behavior, not a bug being papered over.
func TestConfigBridge_FirstGitHubMarketplaceCreds(t *testing.T) {
	env := map[string]string{"GH_TOKEN": "gh-secret"}

	token, proxy := FirstGitHubMarketplaceCreds(nil, bridgeResolve(env))
	assert.Equal(t, "", token, "nil config yields no creds")
	assert.Equal(t, "", proxy, "nil config yields no proxy")

	token, proxy = FirstGitHubMarketplaceCreds(bridgeConfig(
		config.MarketplaceConfig{Name: "clawhub", Type: config.MarketplaceTypeClawHub},
	), bridgeResolve(env))
	assert.Equal(t, "", token, "clawhub entries never seed github creds")
	assert.Equal(t, "", proxy)

	token, proxy = FirstGitHubMarketplaceCreds(bridgeConfig(
		config.MarketplaceConfig{Name: "github", Type: config.MarketplaceTypeGitHub, Enabled: false, TokenRef: "GH_TOKEN", Proxy: "http://p1:1"},
		config.MarketplaceConfig{Name: "github2", Type: config.MarketplaceTypeGitHub, Enabled: true, TokenRef: "OTHER", Proxy: "http://p2:2"},
	), bridgeResolve(env))
	assert.Equal(t, "gh-secret", token, "first github entry's TokenRef must resolve")
	assert.Equal(t, "http://p1:1", proxy, "first github entry's proxy must win")
}
