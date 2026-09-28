// Package gateway — ADR-096 spec test 42 / FR-030 / AC-14.
//
// The migration's winner is computed with APIKey(), which reads the process
// environment. That environment is filled by credentials.InjectFromConfig,
// which bootCredentials runs at step 4. A test that sets the variable itself
// and then calls MigrateWebSearchRoles passes even when boot runs the
// migration first — the defect is WHEN it runs. These fixtures therefore
// put the secret only in the vault and call bootCredentials.
//
// Expected values (spec Migration steps 2–4, install row (c), test 42):
//   - ref present in the vault → default_provider "tavily", fallback "none",
//     roles_migrated_at is RFC 3339
//   - ref absent from the vault → defer: those three keys are not written
package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// g2TavilyRef is not a name this process exports. A developer shell that
// exports TAVILY_API_KEY would hide a migration that ran before injection.
const g2TavilyRef = "ADR096_BOOT_TAVILY_KEY"

func TestBootCredentials_WebSearchMigrationFollowsInject(t *testing.T) {
	t.Run("vault key is recorded as tavily", func(t *testing.T) {
		home, configPath := writeBootWebRolesConfig(t)
		t.Setenv(g2TavilyRef, "")
		require.Empty(t, os.Getenv(g2TavilyRef))

		const vaultValue = "vault-tavily-secret"
		store := credentials.NewStore(filepath.Join(home, "credentials.json"))
		require.NoError(t, credentials.Unlock(store))
		require.NoError(t, store.Set(g2TavilyRef, vaultValue))
		store.Close()

		cfg, _, _, err := bootCredentials(home, configPath)
		require.NoError(t, err)
		require.Equal(t, vaultValue, os.Getenv(g2TavilyRef),
			"InjectFromConfig must publish the vault key; the migration may not run on a pre-seeded env")

		web := readWebSectionMap(t, configPath)
		require.Equal(t, "tavily", web["default_provider"],
			"spec test 42: a Tavily ref that resolves after injection is the default")
		require.Equal(t, "none", web["fallback_provider"], "D11 step 3 writes fallback none")
		marker, _ := web["roles_migrated_at"].(string)
		_, parseErr := time.Parse(time.RFC3339, marker)
		require.NoError(t, parseErr, "D11 step 4: roles_migrated_at must be RFC 3339, got %q", marker)
		require.Equal(t, "tavily", cfg.Tools.Web.DefaultProvider)
		require.Equal(t, "none", cfg.Tools.Web.FallbackProvider)
	})

	t.Run("absent vault ref defers and writes nothing", func(t *testing.T) {
		home, configPath := writeBootWebRolesConfig(t)
		t.Setenv(g2TavilyRef, "")
		require.Empty(t, os.Getenv(g2TavilyRef))

		// Unlock creates the vault but does not store the ref. An enabled
		// keyed provider whose key does not resolve is the defer case.
		store := credentials.NewStore(filepath.Join(home, "credentials.json"))
		require.NoError(t, credentials.Unlock(store))
		store.Close()

		before, err := os.ReadFile(configPath)
		require.NoError(t, err)

		bootCfg, _, _, bootErr := bootCredentials(home, configPath)
		// The missing enabled ref is fatal at ResolveBundle (ADR-004), which
		// runs AFTER the migration. Defer must already have written nothing.
		require.Error(t, bootErr)
		require.Nil(t, bootCfg, "a fatal boot returns no config")

		after, err := os.ReadFile(configPath)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after),
			"spec test 42: a ref absent from the vault defers — no role keys are written")
		assertWebRoleKeysAbsent(t, configPath)
		require.Empty(t, os.Getenv(g2TavilyRef), "a missing vault entry must not be invented into the environment")
	})
}

// writeBootWebRolesConfig writes an upgrade-shaped config: Tavily is enabled
// with a key reference, and the three role keys are absent. That is the file
// MigrateWebSearchRoles decides from — not the defaults overlay, which already
// carries a marker.
func writeBootWebRolesConfig(t *testing.T) (home, configPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	configPath = filepath.Join(home, "config.json")
	writeBootTestFile(t, configPath, `{
		"version": 1,
		"gateway": {"host": "127.0.0.1", "port": 19996},
		"tools": {"web": {"tavily": {"enabled": true, "api_key_ref": "`+g2TavilyRef+`"}}}
	}`)
	return home, configPath
}
