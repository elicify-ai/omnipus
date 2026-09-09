// Regression tests for BUG 4 (architect finding, post-review fix wave):
// buildEnabledRefMap enumerated channels/voice/web-search/marketplaces/
// mailboxes for the sensitive-value scrubber (SensitiveDataReplacer via
// cfg.RegisterSensitiveValues) but had ZERO MCP references, so a resolved
// MCP server env secret was never registered and so never scrubbed from LLM
// output, audit logs, or task evidence. mcpEnabledEnvSensitiveValues (added
// alongside buildEnabledRefMap in gateway.go) closes that gap; these tests
// exercise it directly against a real (in-memory-keyed) credential store.

package gateway

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newUnlockedTestCredStore returns a credential store unlocked with a
// deterministic random key — hermetic and fast, no passphrase/Argon2id
// overhead (mirrors newTestRestAPIWithHomeAndCredStore).
func newUnlockedTestCredStore(t *testing.T) *credentials.Store {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	require.NoError(t, store.UnlockWithKey(key))
	return store
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
