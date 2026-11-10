package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// Unreadable *one entry* and unreadable *whole store* are separate classes in
// #1277. The healthy entries are decrypted first as an instrument check:
// the tamper does not simulate a wrong key or a corrupt store document.
func TestIssue1277_UnreadableSingleEntryDegradesOnlyItsConsumer(t *testing.T) {
	for _, consumer := range []string{"channel", "provider"} {
		t.Run(consumer, func(t *testing.T) {
			f := newIssue1277Fixture(t)
			require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-original"))
			badRef := issue1277BrokenRef
			if consumer == "provider" {
				badRef = "QA1277_UNREADABLE_PROVIDER"
				t.Setenv(badRef, "")
				require.NoError(t, f.store.Set(badRef, "qa1277-original-provider"))
				f.cfg.Providers = append(f.cfg.Providers, &config.ModelConfig{
					Provider: "qa1277-broken", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
					Protocol: "openai-compatible", APIBase: f.cfg.Providers[0].APIBase, APIKeyRef: badRef,
				})
			}
			require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
			issue1277TamperSingleEntry(t, f, badRef)
			value, err := f.store.Get(issue1277ProviderRef)
			require.NoError(t, err, "instrument: store key and unrelated provider entry must still decrypt")
			require.Equal(t, issue1277ProviderValue, value)
			value, err = f.store.Get(issue1277HealthyRef)
			require.NoError(t, err)
			require.Equal(t, "qa1277-healthy-secret", value)
			_, err = f.store.Get(badRef)
			var authErr *credentials.EntryAuthError
			require.ErrorAs(t, err, &authErr, "instrument: only the selected entry must be unreadable")
			require.Equal(t, badRef, authErr.Name)

			cfg, bundle, _ := f.boot(t)
			require.NotContains(t, bundle, badRef)
			api, svc, _, _ := f.startRuntime(t, cfg, bundle)
			if consumer == "channel" {
				issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
			} else {
				issue1277RequireProviderError(t, api, "qa1277-broken", badRef)
				require.Empty(t, svc.ChannelManager.FailedChannels(), "a provider error must not disable channels")
			}
			issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
		})
	}
}

func issue1277TamperSingleEntry(t *testing.T, f *issue1277Fixture, ref string) {
	t.Helper()
	path := filepath.Join(f.home, "credentials.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &document))
	var entries map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["credentials"], &entries))
	require.Contains(t, entries, ref)
	var entry map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(entries[ref], &entry))
	var ciphertext string
	require.NoError(t, json.Unmarshal(entry["ciphertext"], &ciphertext))
	sealed, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	require.NotEmpty(t, sealed)
	sealed[len(sealed)-1] ^= 1 // deterministic one-bit authentication-tag fault.
	entry["ciphertext"], err = json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	require.NoError(t, err)
	entries[ref], err = json.Marshal(entry)
	require.NoError(t, err)
	document["credentials"], err = json.Marshal(entries)
	require.NoError(t, err)
	data, err = json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestIssue1277_WrongWholeStoreKeyRemainsFatal(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-under-original-key"))
	// A different valid-length key makes every sealed entry unreadable, not
	// just one integration. #1277's store-level fatal exception must survive.
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("ff", 32))
	cfg, bundle, store, err := bootCredentials(f.home, f.configPath)
	require.ErrorIs(t, err, credentials.ErrWrongKey, "a wrong whole-store key must not be silently degraded")
	require.Contains(t, err.Error(), "master key")
	require.Nil(t, cfg)
	require.Nil(t, bundle)
	require.Nil(t, store)
}

// Provider/MCP cases derive from #1277's parity clause, not current output.
// Missing-provider status/reason is a RED requirement; MCP server-side error
// reporting is an existing-behaviour control. Healthy peers must remain usable.
func TestIssue1277_MissingProviderSecretIsVisibleAndScoped(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	const missingRef = "QA1277_MISSING_PROVIDER"
	t.Setenv(missingRef, "")
	f.cfg.Providers = append(f.cfg.Providers, &config.ModelConfig{
		Provider: "qa1277-broken", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: f.cfg.Providers[0].APIBase, APIKeyRef: missingRef,
	})
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	issue1277RequireProviderError(t, api, "qa1277-broken", missingRef)
	require.Empty(t, svc.ChannelManager.FailedChannels())
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}

func issue1277RequireProviderError(t *testing.T, api *restAPI, providerID, ref string) {
	t.Helper()
	for _, row := range getProviders(t, api) {
		if row.Id != providerID {
			continue
		}
		require.Equal(t, gen.ProviderStatusError, row.Status)
		require.NotNil(t, row.Error)
		require.Contains(t, *row.Error, ref, "user-visible provider error must name the failed ref")
		return
	}
	t.Fatalf("missing provider %q must remain listed in Settings", providerID)
}

func TestIssue1277_MissingMCPSecretIsServerSideScopedError(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	const ref = "QA1277_MISSING_MCP_TOKEN"
	f.cfg.Tools.MCP.Enabled = true
	f.cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"qa1277-mcp": {Type: "http", Enabled: true, URL: strings.TrimSuffix(f.cfg.Providers[0].APIBase, "/v1") + "/mcp",
			EnvRefs: map[string]string{"TOKEN": ref}},
	}
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	cfg, bundle, _ := f.boot(t)
	api, svc, _, _ := f.startRuntime(t, cfg, bundle)
	require.NoError(t, api.agentLoop.ReconcileMCP(context.Background()), "missing connector secret must not abort reconciliation/reload")
	status, count, reason := api.agentLoop.MCPServerStatus("qa1277-mcp")
	require.Equal(t, "error", status)
	require.Zero(t, count, "a broken connector must expose no callable tools")
	require.Contains(t, reason, ref, "server-side connector error must identify the missing secret")
	w := httptest.NewRecorder()
	api.HandleMCPServers(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-servers", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var rows []gen.McpServer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "qa1277-mcp", rows[0].Id)
	require.Equal(t, gen.McpServerStatusError, rows[0].Status)
	// The existing McpServer contract has status but no reason field. The ref-
	// naming assertion above deliberately uses real server state; report this
	// visibility gap to the contract owner rather than inventing a wire type.
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}
