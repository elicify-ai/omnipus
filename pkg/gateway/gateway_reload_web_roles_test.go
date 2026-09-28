// Package gateway — ADR-096 D11 / FR-030 reload mirror.
//
// Expected values come from the spec, not from the current reload code:
//
//   - docs/internal/specs/web-search-provider-model-spec.md FR-030: the
//     migration runs after InjectFromConfig inside bootCredentials AND is
//     mirrored in gateway_reload.go::executeReload, before tools are built.
//   - ADR-096 D11: on the reload path the written bytes are registered with
//     configSelfWriteRegistry, or the watcher reloads on our own write.
//   - D11 steps 2–4 and install row (c): a resolving Tavily key becomes
//     default_provider "tavily", fallback_provider "none", and a non-empty
//     RFC 3339 roles_migrated_at. An enabled keyed provider whose key does
//     not resolve defers: those three keys stay absent.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// g1TavilyRef is a vault name this process does not already export. Using the
// real TAVILY_API_KEY would let a developer shell satisfy APIKey() before
// InjectFromConfig, which is the false green FR-030 exists to forbid.
const g1TavilyRef = "ADR096_G1_TAVILY_KEY"

// TestExecuteReload_DeferredWebSearchMigrationDecidesAfterInject is the
// reload half of FR-030. Boot defers because the enabled Tavily ref is not
// in the vault. The key is then stored. executeReload must inject it and
// migrate, and must register the write so the config watcher does not loop.
func TestExecuteReload_DeferredWebSearchMigrationDecidesAfterInject(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	t.Setenv(g1TavilyRef, "")
	require.Empty(t, os.Getenv(g1TavilyRef), "precondition: the ref env is empty before inject")

	writeBootTestFile(t, configPath, `{
		"version": 1,
		"gateway": {"host": "127.0.0.1", "port": 0},
		"agents": {"defaults": {"max_tokens": 4096, "workspace": "`+home+`"}},
		"tools": {"web": {"tavily": {"enabled": true, "api_key_ref": "`+g1TavilyRef+`"}}}
	}`)

	// Boot with the ref absent from the vault. Migration must defer (D11
	// ambiguous-install guard). ResolveBundle then refuses the enabled ref;
	// that fatal is ADR-004, and it must not have written role keys on the way.
	bootCfg, _, _, bootErr := bootCredentials(home, configPath)
	require.Error(t, bootErr, "an enabled Tavily ref missing from the vault is fatal at ResolveBundle")
	require.Nil(t, bootCfg, "a fatal boot returns no config")
	assertWebRoleKeysAbsent(t, configPath)

	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(store))
	const vaultValue = "vault-tavily-secret"
	require.NoError(t, store.Set(g1TavilyRef, vaultValue))
	require.Empty(t, os.Getenv(g1TavilyRef), "storing the key must not publish it; only InjectFromConfig does")

	fresh, err := config.LoadConfigWithStore(configPath, store)
	require.NoError(t, err)
	require.Empty(t, fresh.Tools.Web.Tavily.APIKey(), "precondition: APIKey() is empty until the reload injects")

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, fresh, msgBus, &restMockProvider{})
	provider := providers.LLMProvider(&restMockProvider{})
	channelManager, err := channels.NewManager(fresh, credentials.SecretBundle{}, msgBus, nil)
	require.NoError(t, err)
	reg := &configSelfWriteRegistry{hashes: make(map[[sha256.Size]byte]struct{})}
	rs := &services{
		ChannelManager: channelManager,
		credStore:      store,
		homePath:       home,
		selfWriteReg:   reg,
		restAPIRef: &restAPI{
			agentLoop: al,
			homePath:  home,
			credStore: store,
		},
	}
	assertWebRoleKeysAbsent(t, configPath)

	snapshot, err := config.LoadConfigWithStore(configPath, store)
	require.NoError(t, err)
	rs.reloadInFlight = true
	// The fixture deliberately has no startup model, so executeReload fails
	// later while rebuilding the provider. The migration belongs to its
	// pre-swap phase and must already be complete before that unrelated
	// controlled failure. This exercises the real executeReload path without
	// starting listeners, which the test sandbox forbids.
	require.Error(t, executeReload(context.Background(), al, snapshot, &provider, rs, msgBus, false))

	web := readWebSectionMap(t, configPath)
	require.Equal(t, "tavily", web["default_provider"],
		"FR-030: a deferred install migrates on the reload that first resolves the key")
	require.Equal(t, "none", web["fallback_provider"], "D11 step 3: migration writes fallback none")
	marker, _ := web["roles_migrated_at"].(string)
	_, parseErr := time.Parse(time.RFC3339, marker)
	require.NoError(t, parseErr, "D11 step 4: roles_migrated_at must be RFC 3339, got %q", marker)

	require.Equal(t, vaultValue, os.Getenv(g1TavilyRef),
		"the winner is tavily only because the reload's InjectFromConfig published the vault key")

	written, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.True(t, rs.selfWriteReg.consume(sha256.Sum256(written)),
		"D11: the migration write must be registered or the watcher reloads on it and loops")
}

// assertWebRoleKeysAbsent fails if the migration wrote any role key. Absence
// is the deferred state (D11: write nothing and retry later).
func assertWebRoleKeysAbsent(t *testing.T, configPath string) {
	t.Helper()
	web := readWebSectionMap(t, configPath)
	for _, key := range []string{"default_provider", "fallback_provider", "roles_migrated_at"} {
		_, present := web[key]
		require.False(t, present, "deferred migration must not write %q", key)
	}
}

// readWebSectionMap returns tools.web as a raw map so "key absent" stays
// distinct from "key present".
func readWebSectionMap(t *testing.T, configPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	tools, _ := m["tools"].(map[string]any)
	web, _ := tools["web"].(map[string]any)
	if web == nil {
		t.Fatalf("tools.web missing in %s", configPath)
	}
	return web
}
